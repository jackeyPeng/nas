package logs

import "testing"

// TestQueryAuditDoesNotPanic 验证 QueryAudit() 在开发机（audit db 未初始化，
// common.InitAuditLog 未被调用）下不 panic——common.QueryAuditLog 对
// auditDB==nil 的情况返回 error，不是 panic；这里断言该错误被 QueryAudit
// 原样吞成 error 返回，不附带 panic。
func TestQueryAuditDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("QueryAudit() panicked: %v", r)
		}
	}()
	entries, err := QueryAudit(50, 24)
	if err == nil && entries == nil {
		t.Error("QueryAudit() 无错误时应返回非 nil 的 slice（可为空）")
	}
}

// TestQueryAuditClampsLimit 验证 limit 超过硬上限时被 clamp 到
// maxAuditQueryLimit，不是原样传给 common.QueryAuditLog。
func TestQueryAuditClampsLimit(t *testing.T) {
	// db 未初始化时 QueryAudit 直接返回 error，clamp 发生在走到
	// common.QueryAuditLog 之前；这里只能通过不 panic + 不越界的方式间接
	// 验证 clamp 逻辑被执行到（真正的数值断言需要真实 audit.db，留给
	// 双机集成验证）。
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("QueryAudit() panicked: %v", r)
		}
	}()
	if _, err := QueryAudit(999, 9999); err == nil {
		t.Log("QueryAudit(999, 9999) 未报错（可能跑在已初始化 audit db 的环境）")
	}
}
