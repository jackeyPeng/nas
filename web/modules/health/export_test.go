package health

import "testing"

// TestSummaryDoesNotPanic 验证 Summary() 在开发机（无 vgs/ufw/rclone 等
// 真实系统命令）下不 panic，且 pass+warn+fail 恰好等于检查项总数（6 项：
// system/storage/network/backup/security/cloud_sync）。
func TestSummaryDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Summary() panicked: %v", r)
		}
	}()
	pass, warn, fail := Summary()
	total := pass + warn + fail
	if total != 6 {
		t.Fatalf("pass+warn+fail = %d, want 6（检查项总数）", total)
	}
}
