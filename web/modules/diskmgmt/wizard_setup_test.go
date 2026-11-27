package diskmgmt

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// ═══════════════════════════════════════════════════
// Item 2 回归测试：旧的非流式建池入口 handleWizardSetup 已下线。
// 该接口此前绕开了 handleWizardSetupStream 后补的多项前置检查（已存在存储池
// 检测、错误传播），且前端无真实调用方——入口必须直接拒绝并且不执行任何
// 磁盘/LVM/RAID 命令，不能假装成功或部分执行后才失败。
// ═══════════════════════════════════════════════════

func TestHandleWizardSetup_Disabled(t *testing.T) {
	stub := withStubExec(t, map[string]cmdResponse{})

	for _, mode := range []string{"single", "merge", "raid0", "raid1", "raid5", "raid6", "separate", ""} {
		v := url.Values{}
		v.Set("mode", mode)
		v.Set("confirm", "yes")
		r := httptest.NewRequest(http.MethodPost, "/api/disk/wizard/setup", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()

		handleWizardSetup(w, r)

		if w.Code != http.StatusGone {
			t.Errorf("mode=%q: status = %d, want %d（已下线接口必须明确拒绝）", mode, w.Code, http.StatusGone)
		}
		if !strings.Contains(w.Body.String(), "已下线") {
			t.Errorf("mode=%q: body = %q, want 包含“已下线”提示，不能让调用方误以为是普通校验失败", mode, w.Body.String())
		}
	}

	if len(stub.calls) != 0 {
		t.Errorf("calls = %v, want 空（已下线接口不应执行任何磁盘/LVM/RAID 命令）", stub.calls)
	}
}
