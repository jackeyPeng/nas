package diskmgmt

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// ═══════════════════════════════════════════════════
// Item 1/4 回归测试：handlePoolCreate 此前把 mkdir -p mountPoint、mount 的
// 返回错误直接丢弃，失败后仍继续往下走 fstab/chown；chown 阶段对整个
// mountPoint（默认即 /data 本身）做递归 chown -R，波及范围不可控。
// 现在 mkdir/mount 失败必须中止，chown 阶段改用 EnsurePoolStructure 只对
// 自己创建的 public/<nasUser>/ 子目录做精确范围处理。
//
// withFakeValidateBlockDeviceList 绕开 common.ValidateBlockDeviceList 的真实
// os.Stat 块设备存在性检查（单测环境没有、也不应该创建真实 /dev/sdX 节点），
// 只用于驱动 handlePoolCreate 走到 pvcreate 之后的步骤；设备白名单校验本身
// 由 common/validate_test.go 独立覆盖，不是这里的测试目标。
// ═══════════════════════════════════════════════════

// withFakeValidateBlockDeviceList 把 validateBlockDeviceListFn 替换为跳过
// os.Stat 存在性检查的假实现（仍保留"非空设备列表"这一最基本校验），
// t.Cleanup 还原为真实实现。
func withFakeValidateBlockDeviceList(t *testing.T) {
	t.Helper()
	orig := validateBlockDeviceListFn
	validateBlockDeviceListFn = func(devicesStr string) ([]string, error) {
		var out []string
		for _, d := range strings.Split(devicesStr, ",") {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			out = append(out, d)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("设备列表为空")
		}
		return out, nil
	}
	t.Cleanup(func() { validateBlockDeviceListFn = orig })
}

// createPoolForm 构造 handlePoolCreate 所需的最小合法表单。
func createPoolForm(devices, mountPoint, fstype string) url.Values {
	v := url.Values{
		"devices": {devices},
		"confirm": {"yes"},
	}
	if mountPoint != "" {
		v.Set("mountpoint", mountPoint)
	}
	if fstype != "" {
		v.Set("fstype", fstype)
	}
	return v
}

func postPoolCreate(form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/disk/pool/create", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handlePoolCreate(w, r)
	return w
}

// TestHandlePoolCreateMkdirFailureAbortsBeforeMount 验证自定义挂载点下
// mkdir -p 失败必须中止，且 mount 从未被调用——此前失败被丢弃，流程继续
// 往下 mount 到一个状态不确定的目录上。
func TestHandlePoolCreateMkdirFailureAbortsBeforeMount(t *testing.T) {
	withFakeValidateBlockDeviceList(t)
	stub := withStubExec(t, map[string]cmdResponse{
		"/usr/sbin/pvcreate": {Out: "", Err: nil},
		"/usr/sbin/vgcreate":  {Out: "", Err: nil},
		"/usr/sbin/lvcreate":  {Out: "", Err: nil},
		"mkfs.ext4":           {Out: "", Err: nil},
		"mkdir":               {Out: "", Err: fmt.Errorf("permission denied")},
	})

	form := createPoolForm("/dev/sdb", "/data/nas1", "")
	w := postPoolCreate(form)

	if w.Code == http.StatusOK {
		t.Fatalf("status = 200, want non-200（mkdir 失败不应报告成功）; body=%s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "创建挂载点目录失败") {
		t.Errorf("body = %q, want 包含 mkdir 失败说明", w.Body.String())
	}
	if stub.countCalls("mount") != 0 {
		t.Errorf("mount 被调用了 %d 次, want 0（mkdir 失败后不应继续执行 mount）", stub.countCalls("mount"))
	}
}

// TestHandlePoolCreateMountFailureAbortsBeforeChown 验证 mount 失败必须
// 中止，且后续 fstab 写入/EnsurePoolStructure（chown 阶段）从未被触发——
// 此前失败被丢弃，chown 会落在一个空目录（而非真正挂载的 LV）上。
func TestHandlePoolCreateMountFailureAbortsBeforeChown(t *testing.T) {
	withFakeValidateBlockDeviceList(t)
	stub := withStubExec(t, map[string]cmdResponse{
		"/usr/sbin/pvcreate": {Out: "", Err: nil},
		"/usr/sbin/vgcreate":  {Out: "", Err: nil},
		"/usr/sbin/lvcreate":  {Out: "", Err: nil},
		"mkfs.ext4":           {Out: "", Err: nil},
		"mkdir":               {Out: "", Err: nil},
		"mount":               {Out: "", Err: fmt.Errorf("wrong fs type")},
	})

	form := createPoolForm("/dev/sdb", "/data/nas1", "")
	w := postPoolCreate(form)

	if w.Code == http.StatusOK {
		t.Fatalf("status = 200, want non-200（mount 失败不应报告成功）; body=%s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "挂载失败") {
		t.Errorf("body = %q, want 包含 mount 失败说明", w.Body.String())
	}
	if stub.countCalls("blkid") != 0 {
		t.Errorf("blkid 被调用了 %d 次, want 0（mount 失败后不应继续写 fstab）", stub.countCalls("blkid"))
	}
}

// TestHandlePoolCreatePvcreateFailureAbortsBeforeVgcreate 验证 pvcreate
// 失败（最早的破坏性步骤）必须立即中止，vgcreate/lvcreate/mkfs 均不应
// 被调用（正向确认"失败不继续"覐盖了整条链路的最前端，不只是 mkdir/mount）。
func TestHandlePoolCreatePvcreateFailureAbortsBeforeVgcreate(t *testing.T) {
	withFakeValidateBlockDeviceList(t)
	stub := withStubExec(t, map[string]cmdResponse{
		"/usr/sbin/pvcreate": {Out: "", Err: fmt.Errorf("device busy")},
	})

	form := createPoolForm("/dev/sdb", "", "")
	w := postPoolCreate(form)

	if w.Code == http.StatusOK {
		t.Fatalf("status = 200, want non-200（pvcreate 失败不应报告成功）; body=%s", w.Body.String())
	}
	if stub.countCalls("/usr/sbin/vgcreate") != 0 {
		t.Errorf("vgcreate 被调用了 %d 次, want 0", stub.countCalls("/usr/sbin/vgcreate"))
	}
	if stub.countCalls("/usr/sbin/lvcreate") != 0 {
		t.Errorf("lvcreate 被调用了 %d 次, want 0", stub.countCalls("/usr/sbin/lvcreate"))
	}
}

// TestHandlePoolCreateRejectsEmptyDevices 回归：设备列表为空时应在任何
// sudoExecFn 调用之前以 400 拒绝（表单层校验先于破坏性命令）。
func TestHandlePoolCreateRejectsEmptyDevices(t *testing.T) {
	stub := withStubExec(t, map[string]cmdResponse{})

	form := createPoolForm("", "", "")
	w := postPoolCreate(form)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if len(stub.calledNames()) != 0 {
		t.Errorf("calls = %v, want 0（空设备列表不应触发任何命令）", stub.calledNames())
	}
}
