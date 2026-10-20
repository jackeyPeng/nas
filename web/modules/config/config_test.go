package config

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestValidateSambaShareFields_Valid(t *testing.T) {
	if err := validateSambaShareFields("/data/nas1/public", "公开共享", "alice,bob"); err != nil {
		t.Errorf("合法字段不应报错: %v", err)
	}
}

func TestValidateSambaShareFields_NewlineInjection(t *testing.T) {
	cases := []struct {
		name                       string
		path, comment, validUsers string
	}{
		{"path 换行注入", "/data/nas1/x\n   root preexec = /bin/sh\n[evil]", "", ""},
		{"comment 换行注入", "/data/nas1/x", "ok\n   root preexec = /bin/sh", ""},
		{"validUsers 换行注入", "/data/nas1/x", "", "alice\n   root preexec = /bin/sh"},
		{"回车注入", "/data/nas1/x\rroot preexec = /bin/sh", "", ""},
		{"控制字符", "/data/nas1/x\x00", "", ""},
	}
	for _, c := range cases {
		if err := validateSambaShareFields(c.path, c.comment, c.validUsers); err == nil {
			t.Errorf("%s: 应被拒绝", c.name)
		}
	}
}

func TestValidateSambaShareFields_PathChecks(t *testing.T) {
	if err := validateSambaShareFields("relative/path", "", ""); err == nil {
		t.Error("非绝对路径应被拒绝")
	}
	if err := validateSambaShareFields("/data/nas1/../etc", "", ""); err == nil {
		t.Error("未归一化路径 (..) 应被拒绝")
	}
	if err := validateSambaShareFields("/data//nas1", "", ""); err == nil {
		t.Error("未归一化路径 (双斜杠) 应被拒绝")
	}
}

func TestValidateSambaShareFields_CommentTooLong(t *testing.T) {
	long := make([]byte, 256)
	for i := range long {
		long[i] = 'a'
	}
	if err := validateSambaShareFields("/data/nas1/x", string(long), ""); err == nil {
		t.Error("comment 超过 255 字符应被拒绝")
	}
}

func TestHandleServiceToggle_RejectsUnknownService(t *testing.T) {
	form := url.Values{"service": {"docker"}, "action": {"enable"}}
	r := httptest.NewRequest(http.MethodPost, "/api/config/service-toggle", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handleServiceToggle(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("不在白名单的服务应返回 400，got %d", w.Code)
	}
}

func TestHandleServiceToggle_RejectsInvalidAction(t *testing.T) {
	form := url.Values{"service": {"smbd"}, "action": {"restart"}}
	r := httptest.NewRequest(http.MethodPost, "/api/config/service-toggle", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handleServiceToggle(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("action 必须是 enable/disable，got %d", w.Code)
	}
}
