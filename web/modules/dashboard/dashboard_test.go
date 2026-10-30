package dashboard

import (
	"reflect"
	"testing"
)

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

// TestServicesSummary 锁定仪表盘服务计数口径（核心服务 + 已启用的可选服务）
// 的三态：可选服务 disabled 不进分母，可选服务 enabled 但未 active 算异常，
// 核心服务始终计入分母且不活跃即异常。
func TestServicesSummary(t *testing.T) {
	cases := []struct {
		name      string
		list      []ServiceState
		wantTotal int
		wantBad   int
	}{
		{
			name: "全新安装-四协议disabled不计分母不算异常",
			list: []ServiceState{
				{Optional: false, Enabled: true, Active: true},  // smbd
				{Optional: false, Enabled: true, Active: true},  // nmbd
				{Optional: false, Enabled: true, Active: true},  // fail2ban
				{Optional: false, Enabled: true, Active: true},  // nas-panel
				{Optional: true, Enabled: false, Active: false}, // nfs disabled
				{Optional: true, Enabled: false, Active: false}, // vsftpd disabled
				{Optional: true, Enabled: false, Active: false}, // webdav disabled
				{Optional: true, Enabled: false, Active: false}, // s3 disabled
			},
			wantTotal: 4,
			wantBad:   0,
		},
		{
			name: "可选服务enabled但stopped算异常",
			list: []ServiceState{
				{Optional: false, Enabled: true, Active: true},
				{Optional: true, Enabled: true, Active: false}, // 用户开了 FTP 但挂了
			},
			wantTotal: 2,
			wantBad:   1,
		},
		{
			name: "核心服务不活跃始终算异常",
			list: []ServiceState{
				{Optional: false, Enabled: true, Active: false}, // smbd 挂了
			},
			wantTotal: 1,
			wantBad:   1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotTotal, gotBad := servicesSummary(c.list)
			if gotTotal != c.wantTotal || gotBad != c.wantBad {
				t.Errorf("servicesSummary(%+v) = (%d, %d), want (%d, %d)", c.list, gotTotal, gotBad, c.wantTotal, c.wantBad)
			}
		})
	}
}

// TestIsOptionalService 锁定 optional 门禁查询：四协议为 true，核心服务为 false，
// 未知服务名默认 false（防止拼写错误绕过门禁）。
func TestIsOptionalService(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"nfs-kernel-server", true},
		{"vsftpd", true},
		{"rclone-webdav", true},
		{"rclone-s3", true},
		{"smbd", false},
		{"nmbd", false},
		{"fail2ban", false},
		{"nas-panel", false},
		{"unknown-service", false},
	}
	for _, c := range cases {
		if got := IsOptionalService(c.name); got != c.want {
			t.Errorf("IsOptionalService(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestNasServicesOptionalFlags 锁定 NasServices 表里哪些条目标了 Optional，
// 防止后续增删服务时漏打标签导致门禁失效或误伤核心服务。
func TestNasServicesOptionalFlags(t *testing.T) {
	want := map[string]bool{
		"smbd":              false,
		"nmbd":              false,
		"nfs-kernel-server": true,
		"vsftpd":            true,
		"rclone-webdav":     true,
		"rclone-s3":         true,
		"fail2ban":          false,
	}
	got := map[string]bool{}
	for _, svc := range NasServices {
		got[svc.Name] = svc.Optional
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NasServices optional flags = %+v, want %+v", got, want)
	}
}
