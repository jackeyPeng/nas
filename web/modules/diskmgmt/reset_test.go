package diskmgmt

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// errPvsUnavailableForTest 模拟 pvs 采集失败（设备忙/权限不足等）。fail-closed
// stub 根本不会调用 pvs，这个错误值只用于构造"即使采集会失败，stub 也走同一条
// 禁用路径"的 fixture，不代表 handler 实际读取了它。两个入口（非流式/流式）共用
// 同一个值，便于断言措辞保持一致。
var errPvsUnavailableForTest = errors.New("pvs: device busy")

// ═══════════════════════════════════════════════════
// Release-blocking fix: handleWizardReset is now a fail-closed stub.
// managedTargets (collectManagedWipeTargets) is still derived purely from a
// live pvs/mdstat scan — it is not proof that the scanned devices belong
// exclusively to this panel's managed pool. Until that proof exists, the
// reset endpoint must refuse the request before touching anything, every
// time, regardless of confirm/confirm_token or how clean-looking the
// fixture is. These tests replace the old suite that asserted the
// destructive-but-"hardened" behavior, which no longer exists.
// ═══════════════════════════════════════════════════

// resetForm 构造 handleWizardReset 的确认表单（哪怕带着合法的 DELETE
// 确认，fail-closed stub 也必须拒绝——这正是本轮要验证的行为）。
func resetForm() url.Values {
	v := url.Values{}
	v.Set("confirm", "yes")
	v.Set("confirm_token", "DELETE")
	return v
}

// destructiveResetCommands 列出 reset 流程曾经会调用的全部破坏性命令前缀。
// fail-closed stub 生效后，这些命令必须一次都不会被执行。
var destructiveResetCommands = []string{
	"umount",
	"/usr/sbin/lvremove",
	"/usr/sbin/vgremove",
	"/usr/sbin/pvremove",
	"/usr/sbin/mdadm",
	"/usr/sbin/wipefs",
	"mkdir",
	"chown",
}

// TestHandleWizardReset_AlwaysReturnsDisabledEvenWithValidConfirm 验证即使
// 带着合法的 confirm=yes&confirm_token=DELETE，reset 端点也必须立即返回
// "重置功能暂时禁用"错误，不执行任何拆除/擦盘操作。
func TestHandleWizardReset_AlwaysReturnsDisabledEvenWithValidConfirm(t *testing.T) {
	stub := withStubExec(t, map[string]cmdResponse{
		"df":            {Out: "Filesystem                Size  Used Avail Use% Mounted on\n/dev/mapper/vg_nas-data   100G   10G   90G   10% /data/nas1\n"},
		"/usr/sbin/pvs": {Out: "  /dev/sda1   vg_nas\n  /dev/sdb1   vg_nas\n"},
		"cat": {Fn: func(args []string) (string, error) {
			if len(args) > 0 && args[0] == "/proc/mdstat" {
				return "md0 : active raid1 sdd[1] sdc[0]\n      1953511424 blocks super 1.2 [2/2] [UU]\n", nil
			}
			return "", nil
		}},
	})

	r := httptest.NewRequest(http.MethodPost, "/api/disk/wizard/reset", strings.NewReader(resetForm().Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handleWizardReset(w, r)

	if w.Code == http.StatusOK {
		t.Fatalf("status = %d, want non-200（重置功能禁用，不应返回成功）", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, resetDisabledMessage) {
		t.Errorf("body = %q, want 包含禁用说明 %q", body, resetDisabledMessage)
	}
	if len(stub.calls) != 0 {
		t.Errorf("calls = %v, want 零调用（fail-closed 必须在任何命令之前拒绝）", stub.calls)
	}
}

// TestHandleWizardReset_NeverCallsAnyDestructiveCommand 显式逐一断言每个
// 曾经的破坏性命令都没有被调用——不只是"calls 为空"的整体断言，防止将来
// 有人往 handler 里加了新代码却漏看整体断言。
func TestHandleWizardReset_NeverCallsAnyDestructiveCommand(t *testing.T) {
	stub := withStubExec(t, map[string]cmdResponse{
		"df":            {Out: "Filesystem                Size  Used Avail Use% Mounted on\n/dev/mapper/vg_nas-data   100G   10G   90G   10% /data/nas1\n"},
		"/usr/sbin/pvs": {Out: "  /dev/sda1   vg_nas\n  /dev/sdb1   vg_nas\n"},
		"cat": {Fn: func(args []string) (string, error) {
			if len(args) > 0 && args[0] == "/proc/mdstat" {
				return "md0 : active raid1 sdd[1] sdc[0]\n      1953511424 blocks super 1.2 [2/2] [UU]\n", nil
			}
			return "", nil
		}},
	})

	r := httptest.NewRequest(http.MethodPost, "/api/disk/wizard/reset", strings.NewReader(resetForm().Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handleWizardReset(w, r)

	for _, cmd := range destructiveResetCommands {
		if stub.wasCalledWithPrefix(cmd) {
			t.Errorf("calls = %v, 重置功能已禁用，不应调用 %s", stub.calls, cmd)
		}
	}
}

// TestHandleWizardReset_RejectsWithoutConfirmToken 验证没有 DELETE 确认时
// 同样直接拒绝、零调用（fail-closed 对所有输入组合一致生效，不依赖确认
// 校验本身区分"安全"与"不安全"的请求）。
func TestHandleWizardReset_RejectsWithoutConfirmToken(t *testing.T) {
	stub := withStubExec(t, map[string]cmdResponse{})

	v := url.Values{}
	v.Set("confirm", "yes")
	r := httptest.NewRequest(http.MethodPost, "/api/disk/wizard/reset", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handleWizardReset(w, r)

	if w.Code == http.StatusOK {
		t.Fatalf("status = %d, want non-200", w.Code)
	}
	if len(stub.calls) != 0 {
		t.Errorf("calls = %v, want 空", stub.calls)
	}
}

// TestHandleWizardReset_RejectsOnPvsEnumerationFailure 验证即使 pvs/mdstat
// 采集本身失败（旧版本唯一会 fail-closed 的场景），reset 也是走同一条
// "功能已禁用"路径返回，而不是先尝试采集再失败——因为新实现根本不会调用
// pvs/mdstat。
func TestHandleWizardReset_RejectsOnPvsEnumerationFailure(t *testing.T) {
	stub := withStubExec(t, map[string]cmdResponse{
		"/usr/sbin/pvs": {Out: "", Err: errPvsUnavailableForTest},
		"df":            {Out: ""},
	})

	r := httptest.NewRequest(http.MethodPost, "/api/disk/wizard/reset", strings.NewReader(resetForm().Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handleWizardReset(w, r)

	if !stub.wasCalledWithPrefix("") && len(stub.calls) != 0 {
		t.Errorf("calls = %v, want 空（fail-closed stub 根本不应采集 pvs/mdstat）", stub.calls)
	}
	body := w.Body.String()
	if !strings.Contains(body, resetDisabledMessage) {
		t.Errorf("body = %q, want 包含禁用说明", body)
	}
}

// TestHandleWizardReset_RejectsGetMethod 验证非 POST 方法仍被拒绝（防
// 回归：fail-closed 改动不应放宽方法校验）。
func TestHandleWizardReset_RejectsGetMethod(t *testing.T) {
	stub := withStubExec(t, map[string]cmdResponse{})

	r := httptest.NewRequest(http.MethodGet, "/api/disk/wizard/reset", nil)
	w := httptest.NewRecorder()

	handleWizardReset(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
	if len(stub.calls) != 0 {
		t.Errorf("calls = %v, want 空", stub.calls)
	}
}
