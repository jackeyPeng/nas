package users

import (
	"strings"
	"testing"
)

func TestListAdd(t *testing.T) {
	cases := []struct {
		list, user, want string
	}{
		{"", "alice", "alice"},
		{"alice", "bob", "alice,bob"},
		{"alice,bob", "alice", "alice,bob"}, // 去重
		{"", "", ""},                        // 空用户名
		{"alice,", "bob", "alice,bob"},      // 去掉尾部逗号
	}
	for _, c := range cases {
		if got := listAdd(c.list, c.user); got != c.want {
			t.Errorf("listAdd(%q, %q) = %q, want %q", c.list, c.user, got, c.want)
		}
	}
}

func TestListRemove(t *testing.T) {
	cases := []struct {
		list, user, want string
	}{
		{"alice,bob", "alice", "bob"},
		{"alice,bob", "charlie", "alice,bob"}, // 不存在则不变
		{"alice", "alice", ""},
		{" alice , bob ", "alice", "bob"}, // 带空格
	}
	for _, c := range cases {
		if got := listRemove(c.list, c.user); got != c.want {
			t.Errorf("listRemove(%q, %q) = %q, want %q", c.list, c.user, got, c.want)
		}
	}
}

func TestListHas(t *testing.T) {
	if !listHas("alice,bob", "bob") {
		t.Error("应包含 bob")
	}
	if listHas("alice,bob", "charlie") {
		t.Error("不应包含 charlie")
	}
	if listHas("", "alice") {
		t.Error("空列表不应包含任何用户")
	}
}

// 端到端发现的 bug：纯文件夹级数据（write_users 空）上「降为只读」失效。
// 场景：folder 创建时 valid_users=alice,bob,charlie、permission=readwrite、write_users 空。
// 依次 bob→readonly、charlie→noaccess，最终应：alice 读写、bob 只读、charlie 禁止。
func TestApplyPermissionChange_MaterializeFromFolderLevel(t *testing.T) {
	valid, write, perm := "alice,bob,charlie", "", "readwrite"

	// bob → readonly（必须先物化，否则 write_users 空时无法移除 bob）
	valid, write, perm = applyPermissionChange(valid, write, perm, "bob", "readonly")
	if perm != "readwrite" {
		t.Errorf("bob→readonly 后 permission = %q, want readwrite（alice/charlie 仍是写者）", perm)
	}
	if !listHas(write, "alice") || !listHas(write, "charlie") {
		t.Errorf("bob→readonly 后 write_users = %q，应仍含 alice,charlie", write)
	}
	if listHas(write, "bob") {
		t.Errorf("bob→readonly 后 write_users 不应含 bob: %q", write)
	}

	// charlie → noaccess
	valid, write, perm = applyPermissionChange(valid, write, perm, "charlie", "noaccess")
	if listHas(valid, "charlie") {
		t.Errorf("charlie→noaccess 后 valid_users 不应含 charlie: %q", valid)
	}
	if listHas(write, "charlie") {
		t.Errorf("charlie→noaccess 后 write_users 不应含 charlie: %q", write)
	}
	if !listHas(valid, "alice") || !listHas(valid, "bob") {
		t.Errorf("valid_users = %q，应含 alice,bob", valid)
	}
	if !listHas(write, "alice") {
		t.Errorf("write_users = %q，应含 alice", write)
	}
	if perm != "readwrite" {
		t.Errorf("最终 permission = %q, want readwrite", perm)
	}
}

// 全员降为只读 → write_users 空 → permission 应回落为 readonly
func TestApplyPermissionChange_AllReadonly(t *testing.T) {
	valid, write, perm := "alice,bob", "alice,bob", "readwrite"
	valid, write, perm = applyPermissionChange(valid, write, perm, "alice", "readonly")
	valid, write, perm = applyPermissionChange(valid, write, perm, "bob", "readonly")
	if strings.TrimSpace(write) != "" {
		t.Errorf("全员只读后 write_users 应为空, got %q", write)
	}
	if perm != "readonly" {
		t.Errorf("全员只读后 permission = %q, want readonly", perm)
	}
}

// 全员禁止 → valid_users 空 → permission 回落 noaccess
func TestApplyPermissionChange_AllDenied(t *testing.T) {
	valid, write, perm := "alice,bob", "alice,bob", "readwrite"
	valid, write, perm = applyPermissionChange(valid, write, perm, "alice", "noaccess")
	valid, write, perm = applyPermissionChange(valid, write, perm, "bob", "noaccess")
	if strings.TrimSpace(valid) != "" {
		t.Errorf("全员禁止后 valid_users 应为空, got %q", valid)
	}
	if perm != "noaccess" {
		t.Errorf("全员禁止后 permission = %q, want noaccess", perm)
	}
}
