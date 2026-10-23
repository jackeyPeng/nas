// rules.go 实现事件源接线（F2 Task 3）：eventbus 订阅规则表 + 水位 cron
// 检查。事件→建议的映射表以 grep -rn 'EmitEvent(' modules/ 的实际盘点为准，
// 与计划草案规则表的出入在 OPENCODE-REPORT-F2-R2.md 记录。
//
// 盘点结论（关键偏离，详见报告）：
//   - health 模块实际 checkID 注册表只有 6 个固定值（system/storage/
//     network/backup/security/cloud_sync），没有计划设想的 raid/disk_smart
//     专用 checkID；RAID 降级与 LVM 健康检测都折进 health.storage 这一个
//     checkID 里（见 health/health.go checkStorage），SMART 只存在于
//     diskmgmt 模块（未接入 health 事件）。计划里 health.raid/
//     health.disk_smart 两行规则因此合并为一条真实规则：订阅 health.storage
//     的 error 迁移（= 磁盘/RAID 真的出问题，对应 checkStorage 的
//     degraded 分支）。warn 迁移（= 尚未创建存储池，非故障，不是"硬盘
//     出问题"）不生成建议卡——用户在仪表盘/存储页已能看到，不需要管家
//     用紧急语气打扰。
//   - backup 模块原先只有成功事件 backup.data_done，失败路径缺事件
//     （本轮已在 backup.go 补 backup.data_failed 生产点，见该文件改动）。
package triggers

import (
	"fmt"
	"time"

	"nas-panel/common"
	"nas-panel/modules/diskmgmt"
)

// suggestionTTL 是事件/水位驱动建议卡的存活期：比 24h 冷却窗口长，
// 保证用户有时间看到并处理，过期后惰性清理不再占上限名额。
const suggestionTTL = 7 * 24 * time.Hour

// watermarkCronExpr 是水位检查的 cron 表达式（计划 Task 3 逐字：每 6 小时）。
const watermarkCronExpr = "0 */6 * * *"

// watermarkThresholdPercent 是触发"存储空间快满了"建议的用量百分比下限。
const watermarkThresholdPercent = 85

// watermarkLastRun 记录水位检查上次命中 cron 窗口的时间（内存态，6h 窗口
// 去重：同一个 */6 窗口内 ticker 多次 tick 不重复跑检查）。
var watermarkLastRun time.Time

// poolUsageFunc 是 diskmgmt.PoolUsage 的可替换引用（测试注入假值）。
var poolUsageFunc = diskmgmt.PoolUsage

// handleTriggerEvent 是事件订阅规则表的核心分发：根据事件 code/type 决定
// 是否创建建议卡。调用方（eventLoop）已经查过 guard，这里不重复查。
func handleTriggerEvent(ev common.Event) {
	switch {
	case ev.Code == "health.storage" && ev.Type == common.EventError:
		// 盘点修正：计划的 health.raid/health.disk_smart 合并成这一条
		// 真实规则（见文件头注释）。
		createSuggestionSilently("health_storage_error",
			"trig.storage_error_title", "trig.storage_error_body",
			`[{"type":"navigate","page":"health"}]`)

	case ev.Code == "backup.data_failed":
		createSuggestionSilently("backup_failed",
			"trig.backup_failed_title", "trig.backup_failed_body",
			`[{"type":"navigate","page":"backup"}]`)

	case ev.Code == "rclone.sync_failed":
		createSuggestionSilently("sync_failed",
			"trig.sync_failed_title", "trig.sync_failed_body",
			`[{"type":"navigate","page":"backup"}]`)

		// 锚点（F4 预留，spec §五③）：task.done 目前不在 triggers 建建议
		// ——F2 没有第二种 kind 可荐，F4 扩展任务引擎 kind 后在此加分支。
		// notifications 模块已经直接订阅 task.done 发收件箱通知，这里
		// 不需要重复处理。
	}
}

// createSuggestionSilently 包装 CreateSuggestion：事件驱动的建议卡创建
// 不对外暴露 error（冷却/上限/guard 拒绝都是正常静默路径，不是故障），
// 只在走到数据库真实错误时打印日志方便排查。无插值参数的规则用这个。
func createSuggestionSilently(triggerKey, titleKey, bodyKey, actionsJSON string) {
	if _, _, err := CreateSuggestion(triggerKey, titleKey, bodyKey, actionsJSON, suggestionTTL); err != nil {
		fmt.Printf("triggers: CreateSuggestion(%s) 失败: %v\n", triggerKey, err)
	}
}

// eventLoop 是事件订阅循环（StartLoops 挂的第二个 goroutine）：收到事件
// 即交给 handleTriggerEvent 做规则匹配。guard=false 时事件被丢弃（零足迹）。
func eventLoop(ch chan common.Event, stop chan struct{}) {
	defer common.Unsubscribe(ch)
	for {
		select {
		case <-stop:
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if !guardAllows() {
				continue
			}
			handleTriggerEvent(ev)
		}
	}
}

// checkWatermark 是水位检查：池不存在/未建（diskmgmt.ErrPoolNotExist 或
// 其它查询错误）→ 静默跳过（不 panic、不产生建议）；use_percent ≥
// watermarkThresholdPercent → 建 photo_dedupe 建议卡（冷却/上限由
// CreateSuggestion 内核守），body 插值参数 {pct}/{free} 存进
// Suggestion.ParamsJSON。
func checkWatermark() {
	totalGB, usedGB, usePercent, err := poolUsageFunc()
	if err != nil {
		// 池不存在/查询失败：静默跳过，不是故障。
		return
	}
	if usePercent < watermarkThresholdPercent {
		return
	}
	freeGB := totalGB - usedGB
	if freeGB < 0 {
		freeGB = 0
	}
	paramsJSON := fmt.Sprintf(`{"pct":%d,"free":%q}`, usePercent, formatFreeSpace(freeGB))
	if _, _, err := CreateSuggestionWithParams("watermark_high",
		"trig.watermark_title", "trig.watermark_body",
		`[{"type":"task_plan","kind":"photo_dedupe","scope":""}]`,
		paramsJSON, suggestionTTL); err != nil {
		fmt.Printf("triggers: CreateSuggestionWithParams(watermark_high) 失败: %v\n", err)
	}
}

// formatFreeSpace 把剩余容量（GB）格式化为人话文案参数（如 "15GB"）。
func formatFreeSpace(freeGB float64) string {
	return fmt.Sprintf("%.0fGB", freeGB)
}

// runWatermarkCronIfDue 在每次 ticker tick 时检查是否命中水位 cron 窗口，
// 6h 窗口去重（同窗口内第二次 tick 不重复跑）。
func runWatermarkCronIfDue(now time.Time) {
	if !common.CronMatch(watermarkCronExpr, now) {
		return
	}
	// 6h 窗口去重：lastRun 落在同一个 6h 窗口（同一天且 hour/6 相同）则跳过。
	if !watermarkLastRun.IsZero() && sameWatermarkWindow(watermarkLastRun, now) {
		return
	}
	watermarkLastRun = now
	checkWatermark()
}

// sameWatermarkWindow 判断两个时间是否落在同一个"0 */6"窗口（同一天且
// hour/6 相同）。
func sameWatermarkWindow(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay() && a.Hour()/6 == b.Hour()/6
}
