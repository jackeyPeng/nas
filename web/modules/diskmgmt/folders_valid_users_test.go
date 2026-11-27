package diskmgmt

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// item E 修复：valid_users 表单字段最终被 smbManagedLines（config_sync.go）
// 原样拼进 smb.conf 的 "valid users =" 行。handleCreateFolder/
// handleFolderPermission 此前零校验直通 PendingOp.ValidUsers，含换行/等号/
// 方括号等字符的值可逃出该行注入任意 smb.conf 指令或伪造新 share 段
// （与 users/matrix.go #41 同类问题，这里是 create_folder/folder_permission
// 表单路径的同一个洞）。

func TestIsValidSambaUsernameEntry(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"合法用户名", "alice", true},
		{"合法带下划线短横线", "alice_bob-2", true},
		{"数字开头被拒", "2alice", false},
		{"太短被拒", "a", false},
		{"换行被拒", "alice\nvalid users = everyone", false},
		{"等号被拒", "alice=admin", false},
		{"空格被拒", "ali ce", false},
		{"分号被拒", "alice;rm", false},
		{"方括号被拒", "alice]\n[evil", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isValidSambaUsernameEntry(tc.in)
			if got != tc.want {
				t.Errorf("isValidSambaUsernameEntry(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestValidateValidUsersField(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"空字符串合法(开放共享)", "", false},
		{"单个合法用户", "alice", false},
		{"逗号分隔多个合法用户", "alice, bob , carol", false},
		{"换行注入被拒", "alice\nvalid users = everyone", true},
		{"单条非法不影响其它合法条目判定前即整体拒绝", "alice,bob;rm", true},
		{"等号注入被拒", "alice=admin", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateValidUsersField(tc.in)
			if tc.wantErr && err == nil {
				t.Errorf("validateValidUsersField(%q) = nil, want error", tc.in)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateValidUsersField(%q) = %v, want nil", tc.in, err)
			}
		})
	}
}

// TestHandleCreateFolder_RejectsInvalidValidUsers 确认 handler 在校验失败时
// 于触达 applyCreateFolder（数据库/磁盘操作）之前短路返回 400。
func TestHandleCreateFolder_RejectsInvalidValidUsers(t *testing.T) {
	form := url.Values{}
	form.Set("pool", "/data/pool1")
	form.Set("name", "testshare")
	form.Set("permission", "readwrite")
	form.Set("valid_users", "alice\nvalid users = everyone")
	req := httptest.NewRequest(http.MethodPost, "/api/folders", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handleCreateFolder(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("预期 jsonError(400)，got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "valid_users") {
		t.Errorf("响应体应提示 valid_users 非法，got %s", rec.Body.String())
	}
}

// TestHandleFolderPermission_RejectsInvalidValidUsers 同上，覆盖权限更新路径。
func TestHandleFolderPermission_RejectsInvalidValidUsers(t *testing.T) {
	form := url.Values{}
	form.Set("path", "/data/pool1/testshare")
	form.Set("permission", "readwrite")
	form.Set("valid_users", "alice;rm -rf /")
	req := httptest.NewRequest(http.MethodPost, "/api/folders/permission", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handleFolderPermission(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("预期 jsonError(400)，got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "valid_users") {
		t.Errorf("响应体应提示 valid_users 非法，got %s", rec.Body.String())
	}
}
