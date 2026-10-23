// report.go 实现家庭周报（F2 Task 4）：周日 9 点 cron + 手动生成端点，
// 汇总存储/本周任务/健康三块数据，模板化落收件箱（kind=report，零 LLM
// 依赖，纯数字汇总）。
package triggers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"nas-panel/common"
	"nas-panel/modules/diskmgmt"
	"nas-panel/modules/health"
	"nas-panel/modules/notifications"
	"nas-panel/modules/tasks"
)

// reportCronExpr 是周报生成的 cron 表达式（计划 Task 4 逐字：周日 9 点）。
const reportCronExpr = "0 9 * * 0"

// reportWindow 是周报统计的时间跨度：[now-7d, now]（含边界，"恰好 7 天前"
// 算在本周内）。
const reportWindow = 7 * 24 * time.Hour

// reportLastRun 记录周报 cron 上次命中窗口的时间（内存态，同款 6h 水位
// lastRun 去重模式，这里窗口是"当天"：同一天内第二次 tick 不重复生成）。
var reportLastRun time.Time

// reportJobsFunc / reportCountersFunc / reportPoolUsageFunc / reportHealthFunc
// 是周报数据源的可替换引用（测试注入假值，不依赖真实系统命令/磁盘）。
var (
	reportJobsFunc      = tasks.ListJobs
	reportCountersFunc  = tasks.LoadCounters
	reportPoolUsageFunc = diskmgmt.PoolUsage
	reportHealthFunc    = health.Summary
)

// WeeklyReport 是周报的三块模板化数据（计划 Task 4 schema 逐字）。
type WeeklyReport struct {
	WeekStart string             `json:"week_start"`
	WeekEnd   string             `json:"week_end"`
	Storage   WeeklyReportStore  `json:"storage"`
	Tasks     WeeklyReportTasks  `json:"tasks"`
	Health    WeeklyReportHealth `json:"health"`
}

// WeeklyReportStore 是周报的存储块（diskmgmt.PoolUsage 来源）。
type WeeklyReportStore struct {
	TotalGB    float64 `json:"total_gb"`
	UsedGB     float64 `json:"used_gb"`
	UsePercent int     `json:"use_percent"`
}

// WeeklyReportTasks 是周报的本周任务块（tasks.ListJobs + LoadCounters 来源）。
type WeeklyReportTasks struct {
	Done       int   `json:"done"`
	Failed     int   `json:"failed"`
	MovedFiles int64 `json:"moved_files"`
	FreedBytes int64 `json:"freed_bytes"`
}

// WeeklyReportHealth 是周报的健康块（health.Summary 来源）。
type WeeklyReportHealth struct {
	Pass int `json:"pass"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
}

// buildWeeklyReport 收集三块数据，返回 WeeklyReport 结构体（GenerateWeeklyReport
// 的内核，拆出来方便单测直接断言字段值不用过 JSON 序列化）。now 是基准
// 时间（REST/cron 调用传 time.Now()，测试传固定值方便断言边界）。
func buildWeeklyReport(now time.Time) WeeklyReport {
	weekStart := now.Add(-reportWindow)

	report := WeeklyReport{
		WeekStart: formatTime(weekStart),
		WeekEnd:   formatTime(now),
	}

	// ① 存储：diskmgmt.PoolUsage。池不存在/查询失败时三个数值留零值
	// （不是失败语义，报告仍要生成——没有存储池也是一种真实状态）。
	if totalGB, usedGB, usePercent, err := reportPoolUsageFunc(); err == nil {
		report.Storage = WeeklyReportStore{TotalGB: totalGB, UsedGB: usedGB, UsePercent: usePercent}
	}

	// ② 本周任务：tasks.ListJobs(50) 按 finished_at ∈ [weekStart, now] 过滤，
	// done/failed 计数 + Σ counters moved/freed。
	for _, job := range reportJobsFunc(50) {
		if job.FinishedAt == nil {
			continue
		}
		finishedAt := *job.FinishedAt
		if finishedAt.Before(weekStart) || finishedAt.After(now) {
			continue
		}
		switch job.Status {
		case tasks.StatusDone:
			report.Tasks.Done++
		case tasks.StatusFailed:
			report.Tasks.Failed++
		default:
			continue
		}
		counters := reportCountersFunc(job.ID)
		report.Tasks.MovedFiles += counters["moved"]
		report.Tasks.FreedBytes += counters["freed"]
	}

	// ③ 健康：health.Summary（只读，不触发事件）。
	pass, warn, fail := reportHealthFunc()
	report.Health = WeeklyReportHealth{Pass: pass, Warn: warn, Fail: fail}

	return report
}

// GenerateWeeklyReport 生成本周家庭报告并落收件箱：
//   - guard=false → 不生成、不落地、不发事件（零足迹语义，对齐
//     CreateSuggestion 的拒绝路径）
//   - 落地：notifications.AddNotification("report", "trig.report_title",
//     "trig.report_body", 报告JSON) + EmitEvent("triggers", EventInfo,
//     "report.generated", {notification_id}, "")
//   - 返回落地的 notification id
func GenerateWeeklyReport() (string, error) {
	if !guardAllows() {
		return "", fmt.Errorf("triggers: 功能未启用")
	}

	report := buildWeeklyReport(time.Now().UTC())
	reportJSON, err := json.Marshal(report)
	if err != nil {
		return "", fmt.Errorf("周报序列化失败: %w", err)
	}

	id, err := notifications.AddNotification("report", "trig.report_title", "trig.report_body", string(reportJSON))
	if err != nil {
		return "", fmt.Errorf("周报落收件箱失败: %w", err)
	}

	common.EmitEvent("triggers", common.EventInfo, "report.generated",
		map[string]interface{}{"notification_id": id}, "")

	return id, nil
}

// runWeeklyReportCronIfDue 在每次 ticker tick 时检查是否命中周报 cron 窗口
// （周日 9 点），同款"同窗口不重复跑"去重：以"当天"为窗口粒度（一天只
// 生成一次，不像水位 6h 窗口那么细）。
func runWeeklyReportCronIfDue(now time.Time) {
	if !common.CronMatch(reportCronExpr, now) {
		return
	}
	if !reportLastRun.IsZero() && sameCalendarDay(reportLastRun, now) {
		return
	}
	reportLastRun = now
	if _, err := GenerateWeeklyReport(); err != nil {
		fmt.Printf("triggers: GenerateWeeklyReport 失败: %v\n", err)
	}
}

// sameCalendarDay 判断两个时间是否落在同一天（UTC 口径，与 formatTime/
// CronMatch 使用的时间基准一致）。
func sameCalendarDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

// handleGenerateReport: POST /api/triggers/report/generate → 200
// {"notification_id":...}（手动生成，E2E 与"重新生成"按钮用）。
func handleGenerateReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, err := GenerateWeeklyReport()
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	common.LogAuditRequest(r, "TRIGGERS", "生成家庭周报", "notification_id="+id, "success")
	common.JSONResponse(w, map[string]interface{}{"notification_id": id})
}
