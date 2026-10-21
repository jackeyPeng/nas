package diagnostics

import "testing"

// TestStatusSnapshotDoesNotPanic 验证 StatusSnapshot() 不 panic。diagnostics
// 包的 RegisterRoutes 会 initDB()，但 export_test 走的是包级 TestMain 之外
// 的默认单测生命周期——db 可能为 nil（未调用 RegisterRoutes 的单测场景）。
// buildStatusSnapshot 用 db.QueryRow 读状态，db=nil 时会 panic，所以这里
// 先用 initDB() 到 t.TempDir() 确保 db 可用，再验证薄导出层调用安全。
func TestStatusSnapshotDoesNotPanic(t *testing.T) {
	if db == nil {
		initDB()
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("StatusSnapshot() panicked: %v", r)
		}
	}()
	got := StatusSnapshot()
	if got == nil {
		t.Fatal("StatusSnapshot() 返回 nil，期望至少含 items 键")
	}
	if _, ok := got["items"]; !ok {
		t.Error("StatusSnapshot() 缺少 items 字段")
	}
}
