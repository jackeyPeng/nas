package users

import (
	"testing"

	"nas-panel/modules/diskmgmt"
)

// 从 folders.db 元数据回显单用户权限（替代旧的 smb.conf 解析路径）。
// 场景与旧 TestGetUserFolderPermissionOpenShare 等价，外加按用户粒度用例。
func TestFolderMetaUserPermission(t *testing.T) {
	smb := func(perm, valid, write string) diskmgmt.FolderMeta {
		return diskmgmt.FolderMeta{Name: "share", Path: "/data/nas1/share", SambaShare: true,
			Permission: perm, ValidUsers: valid, WriteUsers: write}
	}
	cases := []struct {
		name string
		meta diskmgmt.FolderMeta
		user string
		want string
	}{
		{"开放共享默认读写", smb("readwrite", "", ""), "alice", "readwrite"},
		{"开放共享 readonly 全员只读", smb("readonly", "", ""), "alice", "readonly"},
		{"noaccess 全员禁止", smb("noaccess", "", ""), "alice", "noaccess"},
		{"非 samba 共享禁止", diskmgmt.FolderMeta{Name: "x", SambaShare: false, Permission: "readwrite"}, "alice", "noaccess"},
		{"封闭共享不在 valid 禁止", smb("readwrite", "fm", ""), "alice", "noaccess"},
		{"封闭共享 valid 命中且 write 空（旧数据）读写", smb("readwrite", "fm", ""), "fm", "readwrite"},
		{"封闭共享 valid 命中 readonly", smb("readwrite", "fm,alice", "fm"), "alice", "readonly"},
		{"write 命中 readwrite（覆盖 read only）", smb("readonly", "fm,alice", "fm,alice"), "alice", "readwrite"},
		{"write 命中 readwrite（文件夹级 readonly）", smb("readonly", "fm,alice", "alice"), "alice", "readwrite"},
		{"public 被按用户编辑后走按用户粒度", smb("readwrite", "fm,bob", "fm"), "alice", "noaccess"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FolderMetaUserPermission(tc.meta, tc.user)
			if got != tc.want {
				t.Errorf("FolderMetaUserPermission(%+v, %q) = %q, want %q", tc.meta, tc.user, got, tc.want)
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
