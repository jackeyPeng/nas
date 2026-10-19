package users

import "testing"

// TestIsShareIdentity 锁定共享影子身份判定：nologin 类 shell + 同名主组。
// 回归背景：建共享文件夹时 pending_ops 用 useradd -M -s nologin -g 同名 建占位
// 用户，此前泄漏到用户页/用户组页显示为幻影用户（如 rbtest）。
func TestIsShareIdentity(t *testing.T) {
	cases := []struct {
		name         string
		user         string
		shell        string
		primaryGroup string
		want         bool
	}{
		{"共享影子用户", "rbtest", "/usr/sbin/nologin", "rbtest", true},
		{"nologin+bin/false 变体", "media", "/bin/false", "media", true},
		{"usr/bin/nologin 变体", "docs", "/usr/bin/nologin", "docs", true},
		{"真实面板用户(bash)", "alice", "/bin/bash", "alice", false},
		{"管理员(bash+同名组)", "fm", "/bin/bash", "fm", false},
		{"nologin 但主组不同名", "svcacct", "/usr/sbin/nologin", "nasusers", false},
		{"主组为空", "ghost", "/usr/sbin/nologin", "", false},
		{"空 shell", "weird", "", "weird", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isShareIdentity(c.user, c.shell, c.primaryGroup); got != c.want {
				t.Errorf("isShareIdentity(%q,%q,%q) = %v, want %v", c.user, c.shell, c.primaryGroup, got, c.want)
			}
		})
	}
}
