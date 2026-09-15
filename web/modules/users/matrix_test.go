package users

import "testing"

// 开放共享（无 valid users）回显：Samba 语义任何可认证用户可连
func TestGetUserFolderPermissionOpenShare(t *testing.T) {
	cases := []struct {
		name    string
		conf    string
		want    string
	}{
		{
			name: "开放共享默认读写",
			conf: "[public]\n   path = /data/nas1/public\n   writable = yes\n",
			want: "readwrite",
		},
		{
			name: "开放共享 read only 全员只读",
			conf: "[public]\n   path = /data/nas1/public\n   read only = yes\n",
			want: "readonly",
		},
		{
			name: "开放共享 read only + write list 覆盖",
			conf: "[public]\n   path = /data/nas1/public\n   read only = yes\n   write list = alice\n",
			want: "readwrite",
		},
		{
			name: "封闭共享不在 valid users 里禁止",
			conf: "[fm]\n   path = /data/nas1/fm\n   valid users = fm\n   writable = yes\n",
			want: "noaccess",
		},
		{
			name: "封闭共享在 valid users 里读写",
			conf: "[fm]\n   path = /data/nas1/fm\n   valid users = fm\n   writable = yes\n",
			want: "readwrite",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user := "alice"
			share := "public"
			if tc.name == "封闭共享不在 valid users 里禁止" || tc.name == "封闭共享在 valid users 里读写" {
				share = "fm"
				if tc.name == "封闭共享在 valid users 里读写" {
					user = "fm"
				}
			}
			got := getUserFolderPermission(tc.conf, user, share)
			if got != tc.want {
				t.Errorf("getUserFolderPermission(%q, %q) = %q, want %q", share, user, got, tc.want)
			}
		})
	}
}

// 开放共享物化：空 valid_users 首次按用户编辑展开为全部用户
func TestApplyPermissionChangeMaterializesOpenShare(t *testing.T) {
	// 模拟 setSharePermission 的物化前置
	validUsers := ""
	allUsers := []string{"fm", "alice", "bob"}
	if trim(validUsers) {
		t.Fatal("precondition")
	}
	validUsers = joinUsers(allUsers)
	v, w, p := applyPermissionChange(validUsers, "", "readwrite", "alice", "readonly")
	if !containsUser(v, "fm") || !containsUser(v, "bob") {
		t.Errorf("物化后其他用户被锁出: valid_users=%q", v)
	}
	if containsUser(w, "alice") {
		t.Errorf("readonly 用户不应在 write_users: %q", w)
	}
	if p != "readwrite" {
		t.Errorf("permission = %q, want readwrite (仍有写者)", p)
	}
}

func trim(s string) bool { return s != "" }
func joinUsers(us []string) string {
	out := ""
	for i, u := range us {
		if i > 0 {
			out += ","
		}
		out += u
	}
	return out
}
func containsUser(list, u string) bool { return listHas(list, u) }
