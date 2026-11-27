package rclone

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// item E 修复：rc_* 字段的 key/value 最终以 "key=value" 文本行原样写入
// rclone.conf（INI 文件，见 rcloneCmdConf → `rclone config create/update`）。
// handleCreateRemote/handleUpdateRemote 此前零校验直通，value 含换行可在该
// 字段之后注入任意新行/新 section（例如伪造一个指向任意路径的新 remote），
// key 含等号/方括号同理。必须在拼 args 之前拒绝。

func TestContainsControlChar(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"普通字符串", "hello-world_123", false},
		{"换行", "evil\nvalue", true},
		{"回车", "evil\rvalue", true},
		{"tab", "evil\tvalue", true},
		{"NUL字节", "evil\x00value", true},
		{"DEL字节", "evil\x7fvalue", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := containsControlChar(tc.in); got != tc.want {
				t.Errorf("containsControlChar(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestValidateRcloneConfigField(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		value   string
		wantErr bool
	}{
		{"合法字段", "endpoint", "https://s3.example.com", false},
		{"value含换行注入新section被拒", "remote", "x\n[admin_backdoor]\ntype=local", true},
		{"value含回车被拒", "secret_access_key", "abc\rdef", true},
		{"key含等号被拒", "evil=injected", "v", true},
		{"key含方括号被拒", "evil]", "v", true},
		{"value含NUL被拒", "endpoint", "abc\x00def", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRcloneConfigField(tc.key, tc.value)
			if tc.wantErr && err == nil {
				t.Errorf("validateRcloneConfigField(%q, %q) = nil, want error", tc.key, tc.value)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateRcloneConfigField(%q, %q) = %v, want nil", tc.key, tc.value, err)
			}
		})
	}
}

// TestHandleCreateRemote_RejectsControlCharInConfigValue 确认 handler 在校验
// 失败时于触达 rcloneCmdConf（真实系统调用）之前短路返回 400。
func TestHandleCreateRemote_RejectsControlCharInConfigValue(t *testing.T) {
	form := url.Values{}
	form.Set("name", "testremote")
	form.Set("type", "s3")
	form.Set("rc_endpoint", "x\n[admin_backdoor]\ntype=local")
	req := httptest.NewRequest(http.MethodPost, "/api/rclone/remotes", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handleCreateRemote(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("预期 400，got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleUpdateRemote_RejectsControlCharInConfigValue 同上，覆盖更新路径。
func TestHandleUpdateRemote_RejectsControlCharInConfigValue(t *testing.T) {
	form := url.Values{}
	form.Set("rc_endpoint", "evil\rvalue")
	req := httptest.NewRequest(http.MethodPut, "/api/rclone/remotes/testremote", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("name", "testremote")
	rec := httptest.NewRecorder()

	handleUpdateRemote(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("预期 400，got %d: %s", rec.Code, rec.Body.String())
	}
}
