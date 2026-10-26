package diskmgmt

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// handleUnmount：target 此前零校验直通 umount（加固轮1 #1 漏补的一处，
// handleMount 有 ValidateMountPoint/ValidateBlockDevice 校验链，handleUnmount
// 没有）——未经校验的 target 能卸载任意已挂载路径（如 /、/boot），属于可用
// 面板 API 发起的系统级 DoS。覆盖两类非法形状：系统目录挂载点、非块设备路径。
func TestHandleUnmount_RejectsInvalidTarget(t *testing.T) {
	cases := []struct {
		name   string
		target string
	}{
		{"根目录", "/"},
		{"系统目录", "/etc"},
		{"非法设备路径", "/etc/passwd"},
		{"相对路径", "data/nas1"},
	}
	for _, c := range cases {
		form := url.Values{"target": {c.target}}
		r := httptest.NewRequest(http.MethodPost, "/api/disks/unmount", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()

		handleUnmount(w, r)

		if w.Code != http.StatusBadRequest {
			t.Errorf("%s (target=%q): got %d, want 400: %s", c.name, c.target, w.Code, w.Body.String())
		}
	}
}

// 空 target 必须被显式拒绝（既有行为，确保本次改动未破坏）。
func TestHandleUnmount_RejectsEmptyTarget(t *testing.T) {
	form := url.Values{"target": {""}}
	r := httptest.NewRequest(http.MethodPost, "/api/disks/unmount", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handleUnmount(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("空 target: got %d, want 400", w.Code)
	}
}
