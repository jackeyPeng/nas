package diskmgmt

import "testing"

// 场景 A：alice 读写、bob 只读、charlie 禁止（write_users 非空 → 按用户粒度）
func TestSMBShareParams_PerUser(t *testing.T) {
	m := FolderMeta{
		Name:       "shared",
		Permission: "readwrite",
		ValidUsers: "alice,bob",
		WriteUsers: "alice",
	}
	mode, list := smbShareParams(m, "nas")
	if mode != "read only = yes" {
		t.Errorf("writeMode = %q, want %q", mode, "read only = yes")
	}
	if list == "" || !containsStr(list, "write list = alice") {
		t.Errorf("writeList = %q, want to contain %q", list, "write list = alice")
	}
	if containsStr(list, "bob") {
		t.Errorf("writeList 不应包含 bob: %q", list)
	}
}

// 场景 B：旧数据全员只读（write_users 空 + permission=readonly）
func TestSMBShareParams_LegacyReadonly(t *testing.T) {
	m := FolderMeta{
		Name:       "photos",
		Permission: "readonly",
		ValidUsers: "alice,bob",
		WriteUsers: "",
	}
	mode, list := smbShareParams(m, "nas")
	if mode != "read only = yes" {
		t.Errorf("writeMode = %q, want read only = yes", mode)
	}
	if list != "" {
		t.Errorf("writeList 应为空，got %q", list)
	}
}

// 场景 C：旧数据全员读写（write_users 空 + permission=readwrite）
func TestSMBShareParams_LegacyReadwrite(t *testing.T) {
	m := FolderMeta{
		Name:       "backup",
		Permission: "readwrite",
		ValidUsers: "alice,bob",
		WriteUsers: "",
	}
	mode, list := smbShareParams(m, "nas")
	if mode != "writable = yes" {
		t.Errorf("writeMode = %q, want writable = yes", mode)
	}
	if list != "" {
		t.Errorf("writeList 应为空，got %q", list)
	}
}

// 场景 D：write_users 只含一个用户时，read only 仍应为 yes
func TestSMBShareParams_WriteListSingle(t *testing.T) {
	m := FolderMeta{
		Name:       "docs",
		Permission: "readonly", // 即便旧 permission 是 readonly，write_users 优先
		ValidUsers: "alice",
		WriteUsers: "alice",
	}
	mode, _ := smbShareParams(m, "nas")
	if mode != "read only = yes" {
		t.Errorf("writeMode = %q, want read only = yes（write list 覆盖）", mode)
	}
}

func containsStr(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
