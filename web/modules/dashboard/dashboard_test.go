package dashboard

import "testing"

// TestClassifyServiceActive 锁定 systemctl is-active 退出码→面板四态映射。
// 回归背景：旧实现把 exit 3（inactive，含「已停止」）一律映射 failed，
// 面板停止的服务显示「失败」并触发健康检查假告警。
func TestClassifyServiceActive(t *testing.T) {
	cases := []struct {
		name   string
		stdout string
		err    string
		want   string
	}{
		{"active", "active\n", "", "active"},
		{"stopped-exit3", "inactive\n", "exit status 3", "inactive"},
		{"failed-exit3", "failed\n", "exit status 3", "failed"},
		{"not-installed-exit4", "inactive\n", "exit status 4", "not-installed"},
		{"not-installed-unknown-state", "unknown\n", "exit status 3", "not-installed"},
		{"not-installed-empty", "", "exit status 4", "not-installed"},
		{"activating-passthrough", "activating\n", "exit status 3", "activating"},
		{"no-err-empty-stdout", "", "", "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyServiceActive(c.stdout, c.err); got != c.want {
				t.Errorf("classifyServiceActive(%q, %q) = %q, want %q", c.stdout, c.err, got, c.want)
			}
		})
	}
}
