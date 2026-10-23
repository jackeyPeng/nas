package triggers

import (
	"encoding/json"
	"testing"
	"time"

	"nas-panel/common"
	"nas-panel/modules/diskmgmt"
)

// ── 1. 规则单测（注入假事件到内部 handler，不起真循环） ──

func TestHandleTriggerEventStorageErrorCreatesSuggestion(t *testing.T) {
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)

	ev := common.Event{Source: "health", Type: common.EventError, Code: "health.storage",
		Params: `{"check":"storage","status":"error"}`}
	handleTriggerEvent(ev)

	all := ListSuggestions(true)
	if len(all) != 1 {
		t.Fatalf("want 1 suggestion, got %d: %+v", len(all), all)
	}
	sg := all[0]
	if sg.TriggerKey != "health_storage_error" {
		t.Fatalf("want trigger_key=health_storage_error, got %s", sg.TriggerKey)
	}
	if sg.Title != "trig.storage_error_title" || sg.Body != "trig.storage_error_body" {
		t.Fatalf("键名不符: title=%s body=%s", sg.Title, sg.Body)
	}
	var actions []map[string]interface{}
	if err := json.Unmarshal([]byte(sg.ActionsJSON), &actions); err != nil {
		t.Fatalf("actions_json 非法: %v", err)
	}
	if len(actions) != 1 || actions[0]["type"] != "navigate" || actions[0]["page"] != "health" {
		t.Fatalf("actions 不符: %+v", actions)
	}
}

func TestHandleTriggerEventStorageWarnDoesNotCreateSuggestion(t *testing.T) {
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)

	// health.storage 的 warn 迁移 = "尚未创建存储池"，不是硬盘故障，
	// 盘点修正后不建议（见 rules.go 文件头注释）。
	ev := common.Event{Source: "health", Type: common.EventWarn, Code: "health.storage",
		Params: `{"check":"storage","status":"warn"}`}
	handleTriggerEvent(ev)

	if len(ListSuggestions(true)) != 0 {
		t.Fatalf("health.storage warn 不应产生建议")
	}
}

func TestHandleTriggerEventBackupFailedCreatesSuggestion(t *testing.T) {
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)

	ev := common.Event{Source: "backup", Type: common.EventError, Code: "backup.data_failed",
		Params: `{"target":"/mnt/x"}`}
	handleTriggerEvent(ev)

	all := ListSuggestions(true)
	if len(all) != 1 || all[0].TriggerKey != "backup_failed" {
		t.Fatalf("want 1 suggestion trigger_key=backup_failed, got %+v", all)
	}
}

func TestHandleTriggerEventSyncFailedCreatesSuggestion(t *testing.T) {
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)

	ev := common.Event{Source: "rclone", Type: common.EventError, Code: "rclone.sync_failed",
		Params: `{}`}
	handleTriggerEvent(ev)

	all := ListSuggestions(true)
	if len(all) != 1 || all[0].TriggerKey != "sync_failed" {
		t.Fatalf("want 1 suggestion trigger_key=sync_failed, got %+v", all)
	}
}

func TestHandleTriggerEventIgnoresUnknownCode(t *testing.T) {
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)

	handleTriggerEvent(common.Event{Source: "tasks", Type: common.EventSuccess, Code: "task.done", Params: "{}"})

	if len(ListSuggestions(true)) != 0 {
		t.Fatalf("task.done 不在 F2 规则表内，不应产生建议（F4 留钩）")
	}
}

// 同 key 连发两次，第二次被冷却拒（trigger_key 24h 冷却内核已在 Task 2
// 测过，这里断言规则层调用 CreateSuggestion 时会受同一冷却约束）。
func TestHandleTriggerEventCooldownAppliesAcrossRepeatedEvents(t *testing.T) {
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)

	ev := common.Event{Source: "backup", Type: common.EventError, Code: "backup.data_failed", Params: "{}"}
	handleTriggerEvent(ev)
	handleTriggerEvent(ev)

	all := ListSuggestions(true)
	if len(all) != 1 {
		t.Fatalf("24h 冷却内同 trigger_key 连发两次应只产生 1 条建议, got %d", len(all))
	}
}

// ── 2. 水位：假 PoolUsage（注入函数变量）84%→不建议、85%→建议、池错误→不建议不 panic ──

func withFakePoolUsage(t *testing.T, fn func() (float64, float64, int, error)) {
	t.Helper()
	old := poolUsageFunc
	poolUsageFunc = fn
	t.Cleanup(func() { poolUsageFunc = old })
}

func TestCheckWatermarkBelowThresholdNoSuggestion(t *testing.T) {
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)
	withFakePoolUsage(t, func() (float64, float64, int, error) { return 100, 84, 84, nil })

	checkWatermark()

	if len(ListSuggestions(true)) != 0 {
		t.Fatalf("84%% 不应触发水位建议")
	}
}

func TestCheckWatermarkAtThresholdCreatesSuggestion(t *testing.T) {
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)
	withFakePoolUsage(t, func() (float64, float64, int, error) { return 100, 85, 85, nil })

	checkWatermark()

	all := ListSuggestions(true)
	if len(all) != 1 {
		t.Fatalf("85%% 应触发水位建议, got %d", len(all))
	}
	sg := all[0]
	if sg.TriggerKey != "watermark_high" {
		t.Fatalf("want trigger_key=watermark_high, got %s", sg.TriggerKey)
	}
	if sg.Title != "trig.watermark_title" || sg.Body != "trig.watermark_body" {
		t.Fatalf("键名不符: title=%s body=%s", sg.Title, sg.Body)
	}
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(sg.ParamsJSON), &params); err != nil {
		t.Fatalf("params_json 非法: %v", err)
	}
	if int(params["pct"].(float64)) != 85 {
		t.Fatalf("params.pct 不符: %+v", params)
	}
	if params["free"] != "15GB" {
		t.Fatalf("params.free 不符: %+v", params)
	}
	var actions []map[string]interface{}
	json.Unmarshal([]byte(sg.ActionsJSON), &actions)
	if len(actions) != 1 || actions[0]["type"] != "task_plan" || actions[0]["kind"] != "photo_dedupe" {
		t.Fatalf("actions 不符: %+v", actions)
	}
}

func TestCheckWatermarkPoolErrorSkipsSilently(t *testing.T) {
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)
	withFakePoolUsage(t, func() (float64, float64, int, error) { return 0, 0, 0, diskmgmt.ErrPoolNotExist })

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("池不存在时 checkWatermark 不应 panic: %v", r)
		}
	}()
	checkWatermark()

	if len(ListSuggestions(true)) != 0 {
		t.Fatalf("池不存在时不应产生建议")
	}
}

// ── 3. cron 窗口去重：同窗口第二次 tick 不重复检查（lastRun 断言） ──

func TestRunWatermarkCronWindowDedup(t *testing.T) {
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)
	t.Cleanup(func() { watermarkLastRun = time.Time{} })

	callCount := 0
	withFakePoolUsage(t, func() (float64, float64, int, error) {
		callCount++
		return 100, 90, 90, nil
	})

	// 命中窗口的时刻：0 */6 * * * → 整点、hour%6==0 的分钟 0。
	hit := time.Date(2026, 10, 25, 6, 0, 0, 0, time.UTC)
	runWatermarkCronIfDue(hit)
	if callCount != 1 {
		t.Fatalf("首次命中窗口应检查一次, got callCount=%d", callCount)
	}

	// 同一个 6h 窗口内的第二次 tick（如 6:00:30 再 tick 一次，非 cron 分钟
	// 但为了直接测 sameWatermarkWindow 去重逻辑，这里直接用同样命中 cron
	// 表达式的下一分钟 06:00 不会复现——改用窗口内另一刻意调用一次
	// checkWindowDedup 辅助断言）。
	hitAgain := time.Date(2026, 10, 25, 6, 0, 0, 0, time.UTC)
	runWatermarkCronIfDue(hitAgain)
	if callCount != 1 {
		t.Fatalf("同窗口第二次 tick 不应重复检查, got callCount=%d", callCount)
	}

	// 下一个 6h 窗口（12:00）应再检查一次。
	nextWindow := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	runWatermarkCronIfDue(nextWindow)
	if callCount != 2 {
		t.Fatalf("下一个 6h 窗口应再检查一次, got callCount=%d", callCount)
	}
}

func TestRunWatermarkCronSkipsNonMatchingMinute(t *testing.T) {
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)
	t.Cleanup(func() { watermarkLastRun = time.Time{} })

	callCount := 0
	withFakePoolUsage(t, func() (float64, float64, int, error) {
		callCount++
		return 100, 90, 90, nil
	})

	// 06:05 不命中 "0 */6 * * *"（分钟字段必须是 0）。
	miss := time.Date(2026, 10, 25, 6, 5, 0, 0, time.UTC)
	runWatermarkCronIfDue(miss)
	if callCount != 0 {
		t.Fatalf("非 cron 命中分钟不应检查, got callCount=%d", callCount)
	}
}

// ── 4. guard=false：事件到达不产生建议 ──

func TestEventLoopGuardFalseDropsEvent(t *testing.T) {
	resetForTest(t)
	common.InitEventBus(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)
	SetEnabledGuard(alwaysDisabled)

	StartLoops()
	t.Cleanup(StopLoopsForTest)

	common.EmitEvent("backup", common.EventError, "backup.data_failed", nil, "")

	// 轮询确认收件箱始终为空（guard=false 时事件在 eventLoop 里被丢弃）。
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(ListSuggestions(true)) != 0 {
			t.Fatalf("guard=false 时不应产生建议")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ── 真实 StartLoops 端到端：事件经真实订阅循环产生建议 ──

func TestStartLoopsEndToEndEventProducesSuggestion(t *testing.T) {
	resetForTest(t)
	common.InitEventBus(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)

	StartLoops()
	t.Cleanup(StopLoopsForTest)

	common.EmitEvent("rclone", common.EventError, "rclone.sync_failed", nil, "")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(ListSuggestions(true)) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	all := ListSuggestions(true)
	if len(all) != 1 || all[0].TriggerKey != "sync_failed" {
		t.Fatalf("want 1 suggestion trigger_key=sync_failed within 2s, got %+v", all)
	}
}
