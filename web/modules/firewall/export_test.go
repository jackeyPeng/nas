package firewall

import "testing"

// TestStatusDoesNotPanic 验证 Status() 在开发机（无 ufw 命令）下不 panic。
// getStatus 在 "which ufw" 失败时直接返回 Installed=false 的零值状态，
// 不会走到后续 sudo ufw status 调用。
func TestStatusDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Status() panicked: %v", r)
		}
	}()
	got := Status()
	if got.Rules == nil {
		t.Error("Status().Rules 应至少是空 slice（getStatus 初始化为 []FirewallRule{}）")
	}
}
