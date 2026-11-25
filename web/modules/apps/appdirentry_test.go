package apps

import "testing"

// TestAppDirEntry 升级路径权限修复的边界判定：/var/lib/nas/apps/ 直接子项
// 路由 sudo mvapp/rmapp，其余（更深路径/别的目录）走普通 os 调用。
func TestAppDirEntry(t *testing.T) {
	cases := []struct {
		path string
		name string
		ok   bool
	}{
		{"/var/lib/nas/apps/nas-photos", "nas-photos", true},
		{"/var/lib/nas/apps/nas-photos.bak-1732-ab12", "nas-photos.bak-1732-ab12", true},
		{"/var/lib/nas/apps", "", false},                      // 根本身
		{"/var/lib/nas/apps/", "", false},                     // 空名
		{"/var/lib/nas/apps/nas-photos/app", "", false},       // 更深路径不匹配
		{"/var/lib/nas/apps-data/nas-photos", "", false},      // 相邻目录不算
		{"/etc/systemd/system/nas-app-x.service", "", false},  // 无关路径
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := appDirEntry(c.path)
		if ok != c.ok || got != c.name {
			t.Errorf("appDirEntry(%q) = (%q,%v), want (%q,%v)", c.path, got, ok, c.name, c.ok)
		}
	}
}
