package diskmgmt

import (
	"encoding/json"
	"strings"
	"testing"
)

// ═══════════════════════════════════════════════════
// Release-blocking fix（SSE 变体）：handleWizardResetStream 是前端真实调用的
// 流式重置入口（见 app.js /api/disk/wizard/reset-stream），现在与
// handleWizardReset（reset_test.go）同样是 fail-closed stub —— managedTargets
// 仍只是对 pvs/mdstat 的一次实时全量扫描结果，不是"这些设备只属于本面板纳管
// 池"的证明。在能够证明设备归属之前，流式入口也必须在任何拆除/擦盘命令之前
// 就通过单条 SSE error 事件拒绝请求，不管 confirm/confirm_token 或 fixture
// 看起来多"干净"。这些测试替换了旧版断言"收敛到 managedTargets 后执行"的
// 整套用例——那套行为已不存在。
// ═══════════════════════════════════════════════════

// destructiveResetStreamCommands 列出流式重置流程曾经会调用的全部破坏性命令
// 前缀。fail-closed stub 生效后，这些命令必须一次都不会被执行。复用
// reset_test.go 中 destructiveResetCommands 的同一张清单会更简洁，但这里
// 保留独立命名以便未来两个 stub 的清单各自演化时不互相牵连。
var destructiveResetStreamCommands = destructiveResetCommands

func resetStreamQuery() map[string]string {
	return map[string]string{"confirm": "yes", "confirm_token": "DELETE"}
}

// decodeSSEProgress 把 parseSSE 拆出的每条 "data: {...}" JSON 反序列化为
// ProgressEvent，供用例按 Detail/Status 断言。
func decodeSSEProgress(t *testing.T, raw []string) []ProgressEvent {
	t.Helper()
	events := make([]ProgressEvent, 0, len(raw))
	for _, s := range raw {
		var ev ProgressEvent
		if err := json.Unmarshal([]byte(s), &ev); err != nil {
			t.Fatalf("解析 SSE 事件失败: %v, raw=%q", err, s)
		}
		events = append(events, ev)
	}
	return events
}

// TestHandleWizardResetStream_AlwaysReturnsDisabledEvenWithValidConfirm 验证
// 即使带着合法的 confirm=yes&confirm_token=DELETE，流式重置端点也必须立即
// 发出"重置功能暂时禁用"的单条 SSE error 事件，不执行任何拆除/擦盘操作。
func TestHandleWizardResetStream_AlwaysReturnsDisabledEvenWithValidConfirm(t *testing.T) {
	stub := withStubExec(t, map[string]cmdResponse{
		"df":            {Out: "Filesystem                Size  Used Avail Use% Mounted on\n/dev/mapper/vg_nas-data   100G   10G   90G   10% /data/nas1\n"},
		"/usr/sbin/pvs": {Out: "  /dev/sda1   vg_nas\n  /dev/sdb1   vg_nas\n"},
		"cat": {Fn: func(args []string) (string, error) {
			if len(args) > 0 && args[0] == "/proc/mdstat" {
				return "md0 : active raid1 sdd[1] sdc[0]\n      1953511424 blocks super 1.2 [2/2] [UU]\n", nil
			}
			return "", nil
		}},
	})

	r, w := withSSE("GET", "/api/disk/wizard/reset-stream", resetStreamQuery())

	handleWizardResetStream(w, r)

	body := w.Body.String()
	if !strings.Contains(body, resetDisabledMessage) {
		t.Errorf("body = %q, want 包含禁用说明 %q", body, resetDisabledMessage)
	}
	if !strings.Contains(body, `"status":"error"`) {
		t.Errorf("body = %q, want 以 error 状态上报", body)
	}
	events := decodeSSEProgress(t, parseSSE(body))
	if len(events) != 1 {
		t.Errorf("events = %+v, want 恰好一条事件（fail-closed 不应发任何中间进度事件）", events)
	}
	if len(stub.calls) != 0 {
		t.Errorf("calls = %v, want 零调用（fail-closed 必须在任何命令之前拒绝）", stub.calls)
	}
}

// TestHandleWizardResetStream_NeverCallsAnyDestructiveCommand 显式逐一断言
// 每个曾经的破坏性命令都没有被调用——不只是"calls 为空"的整体断言，防止
// 将来有人往 handler 里加了新代码却漏看整体断言。
func TestHandleWizardResetStream_NeverCallsAnyDestructiveCommand(t *testing.T) {
	stub := withStubExec(t, map[string]cmdResponse{
		"df":            {Out: "Filesystem                Size  Used Avail Use% Mounted on\n/dev/mapper/vg_nas-data   100G   10G   90G   10% /data/nas1\n"},
		"/usr/sbin/pvs": {Out: "  /dev/sda1   vg_nas\n  /dev/sdb1   vg_nas\n"},
		"cat": {Fn: func(args []string) (string, error) {
			if len(args) > 0 && args[0] == "/proc/mdstat" {
				return "md0 : active raid1 sdd[1] sdc[0]\n      1953511424 blocks super 1.2 [2/2] [UU]\n", nil
			}
			return "", nil
		}},
	})

	r, w := withSSE("GET", "/api/disk/wizard/reset-stream", resetStreamQuery())

	handleWizardResetStream(w, r)

	for _, cmd := range destructiveResetStreamCommands {
		if stub.wasCalledWithPrefix(cmd) {
			t.Errorf("calls = %v, 重置功能已禁用，不应调用 %s", stub.calls, cmd)
		}
	}
}

// TestHandleWizardResetStream_RejectsWithoutConfirmToken 验证没有 DELETE
// 确认时同样直接拒绝、零调用（fail-closed 对所有输入组合一致生效，不依赖
// 确认校验本身区分"安全"与"不安全"的请求）。
func TestHandleWizardResetStream_RejectsWithoutConfirmToken(t *testing.T) {
	stub := withStubExec(t, map[string]cmdResponse{})

	r, w := withSSE("GET", "/api/disk/wizard/reset-stream", map[string]string{
		"confirm": "yes",
	})

	handleWizardResetStream(w, r)

	if len(stub.calls) != 0 {
		t.Errorf("calls = %v, want 空（确认校验失败不应执行任何命令）", stub.calls)
	}
	events := decodeSSEProgress(t, parseSSE(w.Body.String()))
	if len(events) != 1 || events[0].Status != "error" {
		t.Errorf("events = %+v, want 单条 error 事件", events)
	}
	if !strings.Contains(events[0].Detail, resetDisabledMessage) {
		t.Errorf("events[0].Detail = %q, want 包含禁用说明", events[0].Detail)
	}
}

// TestHandleWizardResetStream_RejectsOnPvsEnumerationFailure 验证即使
// pvs/mdstat 采集本身失败（旧版本唯一会 fail-closed 的场景），流式重置也是
// 走同一条"功能已禁用"路径返回单条 SSE error 事件，而不是先尝试采集再
// 失败——因为新实现根本不会调用 pvs/mdstat。
func TestHandleWizardResetStream_RejectsOnPvsEnumerationFailure(t *testing.T) {
	stub := withStubExec(t, map[string]cmdResponse{
		"/usr/sbin/pvs": {Out: "", Err: errPvsUnavailableForTest},
		"df":            {Out: ""},
	})

	r, w := withSSE("GET", "/api/disk/wizard/reset-stream", resetStreamQuery())

	handleWizardResetStream(w, r)

	if len(stub.calls) != 0 {
		t.Errorf("calls = %v, want 空（fail-closed stub 根本不应采集 pvs/mdstat）", stub.calls)
	}
	body := w.Body.String()
	if !strings.Contains(body, resetDisabledMessage) {
		t.Errorf("body = %q, want 包含禁用说明", body)
	}
}

// TestHandleWizardResetStream_SetsSSEHeaders 验证 fail-closed stub 仍然像
// 其它流式端点一样设置 SSE 相关响应头（前端 EventSource 依赖这些头部才能
// 正确解析，不应因为功能禁用就跳过）。
func TestHandleWizardResetStream_SetsSSEHeaders(t *testing.T) {
	withStubExec(t, map[string]cmdResponse{})

	r, w := withSSE("GET", "/api/disk/wizard/reset-stream", resetStreamQuery())

	handleWizardResetStream(w, r)

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}
