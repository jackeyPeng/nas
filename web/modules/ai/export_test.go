package ai

import (
	"testing"
	"time"

	"nas-panel/modules/tasks"
)

// ── CreatePendingAction：复用既有确认门/TTL/确认词的共享 helper ──────

func TestCreatePendingActionRegisteredWriteToolSucceeds(t *testing.T) {
	id, confirmWord, err := CreatePendingAction("start_task", `{"job_id":"job-1"}`, "启动任务：清理重复照片（0 组文件，释放 0B）")
	if err != nil {
		t.Fatalf("CreatePendingAction: %v", err)
	}
	if id == "" {
		t.Fatal("id 不应为空")
	}
	if confirmWord != pendingActionConfirmWord {
		t.Fatalf("confirmWord = %q, want %q（必须等于既有确认门下发值）", confirmWord, pendingActionConfirmWord)
	}

	action, ok := defaultPendingStore.Take(id)
	if !ok {
		t.Fatal("CreatePendingAction 应把 PendingAction 存进既有 defaultPendingStore（与 chat.go 共用单例）")
	}
	if action.ToolName != "start_task" {
		t.Fatalf("ToolName = %q", action.ToolName)
	}
	if action.ArgsJSON != `{"job_id":"job-1"}` {
		t.Fatalf("ArgsJSON = %q", action.ArgsJSON)
	}
	if action.Summary != "启动任务：清理重复照片（0 组文件，释放 0B）" {
		t.Fatalf("Summary 应原样保留调用方传入值, got %q", action.Summary)
	}
	if action.ConfirmWord != pendingActionConfirmWord {
		t.Fatalf("ConfirmWord = %q", action.ConfirmWord)
	}
}

func TestCreatePendingActionUnknownToolRejected(t *testing.T) {
	_, _, err := CreatePendingAction("does_not_exist", `{}`, "摘要")
	if err == nil {
		t.Fatal("未注册的工具名必须被拒绝")
	}
}

func TestCreatePendingActionReadOnlyToolRejected(t *testing.T) {
	// plan_task 是只读工具（Write=false）：CreatePendingAction 只接受
	// 已注册的写工具，读工具必须拒绝（硬约束：不许绕过确认门语义的
	// 工具面扩大——读工具本来就不需要走 pending action）。
	_, _, err := CreatePendingAction("plan_task", `{"kind":"photo_dedupe"}`, "摘要")
	if err == nil {
		t.Fatal("只读工具（Write=false）必须被 CreatePendingAction 拒绝")
	}
}

func TestCreatePendingActionTTLSameAsChatPath(t *testing.T) {
	id, _, err := CreatePendingAction("start_task", `{"job_id":"job-1"}`, "摘要")
	if err != nil {
		t.Fatalf("CreatePendingAction: %v", err)
	}
	action, ok := defaultPendingStore.Take(id)
	if !ok {
		t.Fatal("应能取出")
	}
	// TTL 复用 memoryPendingStore.Take 的惰性过期逻辑：构造一个已过期的
	// CreatedAt 存回去，验证 Take 按同一套 pendingActionTTL 判断。
	action.CreatedAt = time.Now().Add(-pendingActionTTL - time.Second)
	defaultPendingStore.Put(action)
	if _, ok := defaultPendingStore.Take(action.ID); ok {
		t.Fatal("超过 pendingActionTTL 的 action 应 Take 失败——TTL 常量必须与 chat.go 共用同一个")
	}
}

// ── StartTaskSummary：与 chat.go pendingActionSummary 的 start_task 分支同源 ──

func TestStartTaskSummaryMatchesChatPathFormat(t *testing.T) {
	job := tasks.Job{
		Kind:   "photo_dedupe",
		Impact: tasks.Impact{Groups: 3, Files: 7, Bytes: 1024 * 1024},
	}
	got := StartTaskSummary(job)
	want := startTaskSummary(job) // chat.go 内部函数，同一份实现的直接调用
	if got != want {
		t.Fatalf("StartTaskSummary 必须与 chat.go 内部 startTaskSummary 产出一致, got %q want %q", got, want)
	}
}
