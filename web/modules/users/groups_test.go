package users

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// validateMemberNames：逗号分隔成员名逐个过 isValidUsername，任一非法即整体拒绝。
func TestValidateMemberNames(t *testing.T) {
	cases := []struct {
		members string
		wantErr bool
	}{
		{"", false},
		{"alice", false},
		{"alice,bob", false},
		{" alice , bob ", false}, // 允许空格，TrimSpace 后再校验
		{"alice,,bob", false},    // 空字段忽略
		{"alice,root;whoami", true},
		{"../etc/passwd", true},
		{"alice,-x", true},
	}
	for _, c := range cases {
		err := validateMemberNames(c.members)
		if (err != nil) != c.wantErr {
			t.Errorf("validateMemberNames(%q) err=%v, wantErr=%v", c.members, err, c.wantErr)
		}
	}
}

// isManagedGroup：GID 范围过滤是组操作端点的核心安全边界（见 handleGroupAction
// 注释）。系统组（root，GID 0）与不存在的组都必须返回 false，否则
// PUT /api/user-groups/root/members 或 /sudo/members 会被当作"可管理"放行。
func TestIsManagedGroup(t *testing.T) {
	if isManagedGroup("root") {
		t.Error("root 组（GID 0，系统组）不应被判定为可管理")
	}
	if isManagedGroup("this-group-should-not-exist-12345") {
		t.Error("不存在的组不应被判定为可管理（防 groupadd 时序竞争放行）")
	}
}

// handleGroupAction：组名未过 isValidUsername/isManagedGroup 时，无论
// members/删除请求，都必须在触碰 gpasswd/groupdel 之前短路为 400。
// 这是本轮修复的核心回归测试：此前 groupName 直接来自 URL 路径段，
// PUT /api/user-groups/sudo/members members=fm 能把 fm 加入 sudo 组提权。
func TestHandleGroupAction_RejectsUnmanagedGroup(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		method string
		form   url.Values
	}{
		{"系统组 root 的 members 修改应拒绝", "/api/user-groups/root/members", http.MethodPut, url.Values{"members": {"fm"}}},
		{"非法组名的 members 修改应拒绝", "/api/user-groups/../etc/members", http.MethodPut, url.Values{"members": {"fm"}}},
		{"不存在的组 delete 应拒绝", "/api/user-groups/this-group-should-not-exist-12345", http.MethodDelete, nil},
	}
	for _, c := range cases {
		var r *http.Request
		if c.form != nil {
			r = httptest.NewRequest(c.method, c.path, strings.NewReader(c.form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			r = httptest.NewRequest(c.method, c.path, nil)
		}
		w := httptest.NewRecorder()
		handleGroupAction(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400: %s", c.name, w.Code, w.Body.String())
		}
	}
}
