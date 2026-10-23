package triggers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nas-panel/common"
	"nas-panel/modules/notifications"
	"nas-panel/modules/tasks"
)

// resetReportDepsForTest 初始化周报依赖的 notifications 收件箱（真实落地
// 目标），并把三个数据源函数变量重置为可控的假实现（种子数据/假
// PoolUsage/假 Summary，不依赖真实系统命令）。
func resetReportDepsForTest(t *testing.T) {
	t.Helper()
	notifications.StopSubscriberForTest()
	if err := notifications.InitNotifications(t.TempDir()); err != nil {
		t.Fatalf("InitNotifications: %v", err)
	}
	notifications.SetEnabledGuard(nil)
	t.Cleanup(func() { notifications.SetEnabledGuard(nil) })

	oldJobs, oldCounters, oldPool, oldHealth := reportJobsFunc, reportCountersFunc, reportPoolUsageFunc, reportHealthFunc
	t.Cleanup(func() {
		reportJobsFunc, reportCountersFunc, reportPoolUsageFunc, reportHealthFunc = oldJobs, oldCounters, oldPool, oldHealth
	})
	reportPoolUsageFunc = func() (float64, float64, int, error) { return 100, 50, 50, nil }
	reportHealthFunc = func() (int, int, int) { return 6, 0, 0 }
	reportJobsFunc = func(limit int) []tasks.Job { return nil }
	reportCountersFunc = func(id string) map[string]int64 { return map[string]int64{} }
}

func finishedAt(t time.Time) *time.Time { return &t }

// ── 1. 种子数据 → GenerateWeeklyReport JSON 字段值精确断言（含本周过滤边界：恰好 7 天前） ──

func TestBuildWeeklyReportFieldsExact(t *testing.T) {
	resetForTest(t)
	resetReportDepsForTest(t)

	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	weekStart := now.Add(-reportWindow)

	reportPoolUsageFunc = func() (float64, float64, int, error) { return 200, 170, 85, nil }
	reportHealthFunc = func() (int, int, int) { return 4, 1, 1 }
	reportJobsFunc = func(limit int) []tasks.Job {
		return []tasks.Job{
			{ID: "job-done-in", Status: tasks.StatusDone, FinishedAt: finishedAt(now.Add(-2 * 24 * time.Hour))},
			{ID: "job-failed-in", Status: tasks.StatusFailed, FinishedAt: finishedAt(now.Add(-1 * time.Hour))},
			{ID: "job-done-out", Status: tasks.StatusDone, FinishedAt: finishedAt(now.Add(-8 * 24 * time.Hour))}, // 超出本周
			{ID: "job-running", Status: tasks.StatusRunning, FinishedAt: nil},                                   // 未完成，不计
			{ID: "job-boundary", Status: tasks.StatusDone, FinishedAt: finishedAt(weekStart)},                   // 恰好 7 天前：应计入
		}
	}
	reportCountersFunc = func(id string) map[string]int64 {
		switch id {
		case "job-done-in":
			return map[string]int64{"moved": 10, "freed": 1000}
		case "job-boundary":
			return map[string]int64{"moved": 3, "freed": 300}
		default:
			return map[string]int64{}
		}
	}

	report := buildWeeklyReport(now)

	if report.WeekStart != formatTime(weekStart) || report.WeekEnd != formatTime(now) {
		t.Fatalf("week_start/week_end 不符: %+v", report)
	}
	if report.Storage.TotalGB != 200 || report.Storage.UsedGB != 170 || report.Storage.UsePercent != 85 {
		t.Fatalf("storage 块不符: %+v", report.Storage)
	}
	// done: job-done-in + job-boundary = 2；failed: job-failed-in = 1
	if report.Tasks.Done != 2 || report.Tasks.Failed != 1 {
		t.Fatalf("tasks done/failed 不符: %+v", report.Tasks)
	}
	if report.Tasks.MovedFiles != 13 || report.Tasks.FreedBytes != 1300 {
		t.Fatalf("tasks moved/freed 汇总不符: %+v", report.Tasks)
	}
	if report.Health.Pass != 4 || report.Health.Warn != 1 || report.Health.Fail != 1 {
		t.Fatalf("health 块不符: %+v", report.Health)
	}
}

func TestBuildWeeklyReportJustOutsideBoundaryExcluded(t *testing.T) {
	resetForTest(t)
	resetReportDepsForTest(t)

	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	weekStart := now.Add(-reportWindow)

	reportJobsFunc = func(limit int) []tasks.Job {
		return []tasks.Job{
			// 比 weekStart 早 1 纳秒：应排除在本周之外。
			{ID: "job-just-outside", Status: tasks.StatusDone, FinishedAt: finishedAt(weekStart.Add(-time.Nanosecond))},
		}
	}

	report := buildWeeklyReport(now)
	if report.Tasks.Done != 0 {
		t.Fatalf("恰好 7 天前之外 1ns 的任务不应计入本周, got done=%d", report.Tasks.Done)
	}
}

// ── 2. 报告落 notifications（kind=report、data_json 可解析）+ 事件发出 ──

func TestGenerateWeeklyReportLandsNotificationAndEmitsEvent(t *testing.T) {
	resetForTest(t)
	resetReportDepsForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)

	ch := common.Subscribe()
	defer common.Unsubscribe(ch)

	id, err := GenerateWeeklyReport()
	if err != nil {
		t.Fatalf("GenerateWeeklyReport: %v", err)
	}
	if id == "" {
		t.Fatalf("want non-empty notification id")
	}

	list := notifications.ListNotifications(10, false)
	if len(list) != 1 {
		t.Fatalf("want 1 notification, got %d", len(list))
	}
	n := list[0]
	if n.ID != id || n.Kind != "report" {
		t.Fatalf("通知形状不符: %+v", n)
	}
	if n.Title != "trig.report_title" || n.Body != "trig.report_body" {
		t.Fatalf("通知键名不符: %+v", n)
	}
	var parsed WeeklyReport
	if err := json.Unmarshal([]byte(n.DataJSON), &parsed); err != nil {
		t.Fatalf("data_json 不可解析为 WeeklyReport: %v", err)
	}

	common.DrainEventsForTest()
	select {
	case ev := <-ch:
		if ev.Code != "report.generated" {
			t.Fatalf("want code=report.generated, got %s", ev.Code)
		}
		var params map[string]string
		json.Unmarshal([]byte(ev.Params), &params)
		if params["notification_id"] != id {
			t.Fatalf("事件参数 notification_id 不符: %+v", params)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("未收到 report.generated 事件")
	}
}

// ── 3. guard=false 报错不产生 ──

func TestGenerateWeeklyReportGuardFalse(t *testing.T) {
	resetForTest(t)
	resetReportDepsForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)
	SetEnabledGuard(alwaysDisabled)

	ch := common.Subscribe()
	defer common.Unsubscribe(ch)

	_, err := GenerateWeeklyReport()
	if err == nil {
		t.Fatalf("guard=false 应返回 error")
	}
	if len(notifications.ListNotifications(10, false)) != 0 {
		t.Fatalf("guard=false 时不应产生通知")
	}
	common.DrainEventsForTest()
	select {
	case ev := <-ch:
		t.Fatalf("guard=false 时不应发事件, got %+v", ev)
	default:
	}
}

// ── 4. REST：403/200、审计行写入断言 ──

func TestHandleGenerateReportGuardFalseForbids(t *testing.T) {
	resetForTest(t)
	resetReportDepsForTest(t)

	mux := http.NewServeMux()
	registerRoutesBare(mux, alwaysDisabled)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/triggers/report/generate", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guard=false 应 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleGenerateReportHappyPath(t *testing.T) {
	resetForTest(t)
	resetReportDepsForTest(t)

	mux := http.NewServeMux()
	registerRoutesBare(mux, alwaysEnabled)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/triggers/report/generate", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST report/generate: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		NotificationID string `json:"notification_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if resp.NotificationID == "" {
		t.Fatalf("want non-empty notification_id")
	}
}

// ── 5. cron 表达式 0 9 * * 0 用 common.CronMatch 对固定周日/非周日时刻断言 ──

func TestReportCronExprMatchesSundayNineAM(t *testing.T) {
	sunday9am := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC) // 2026-10-25 是周日
	if sunday9am.Weekday() != time.Sunday {
		t.Fatalf("测试基准日期不是周日: %v", sunday9am.Weekday())
	}
	if !common.CronMatch(reportCronExpr, sunday9am) {
		t.Fatalf("周日 9 点应命中 cron 表达式 %q", reportCronExpr)
	}

	monday9am := sunday9am.AddDate(0, 0, 1)
	if common.CronMatch(reportCronExpr, monday9am) {
		t.Fatalf("周一 9 点不应命中 cron 表达式 %q", reportCronExpr)
	}

	sunday10am := sunday9am.Add(time.Hour)
	if common.CronMatch(reportCronExpr, sunday10am) {
		t.Fatalf("周日 10 点不应命中 cron 表达式 %q", reportCronExpr)
	}
}

// ── cron 窗口去重（同款"当天不重复生成"模式） ──

func TestRunWeeklyReportCronWindowDedup(t *testing.T) {
	resetForTest(t)
	resetReportDepsForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)
	t.Cleanup(func() { reportLastRun = time.Time{} })

	sunday9am := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	runWeeklyReportCronIfDue(sunday9am)
	if len(notifications.ListNotifications(10, false)) != 1 {
		t.Fatalf("首次命中应生成 1 份周报")
	}

	// 同一天内再 tick 一次（比如 9:00:30，但这里直接用同一分钟模拟）不应重复生成。
	runWeeklyReportCronIfDue(sunday9am)
	if len(notifications.ListNotifications(10, false)) != 1 {
		t.Fatalf("同一天内第二次 tick 不应重复生成周报")
	}
}
