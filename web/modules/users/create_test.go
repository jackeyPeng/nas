package users

import "testing"

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
