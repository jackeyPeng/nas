package diskmgmt

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Fix 5b：删 LVM/RAID/single 池时，fstab 清理只删属于被删池的行，separate 模式
// 多池机器删一个池不应抹掉其他保留池的开机挂载。
// A3：新增判据 (c) —— 池未挂载时删除（findmnt 查不到挂载点），只有 UUID 行能
// 命中残留的 fstab 条目，见相关子用例。
func TestFilterFstabForDeletedPool(t *testing.T) {
	t.Run("多nas挂载点只删目标", func(t *testing.T) {
		fstab := "UUID=aaa /data/nas1 xfs defaults,prjquota 0 0\n" +
			"UUID=bbb /data/nas2 xfs defaults,prjquota 0 0\n"
		got := filterFstabForDeletedPool(fstab, []string{"/data/nas1"}, nil, "")
		want := "UUID=bbb /data/nas2 xfs defaults,prjquota 0 0\n"
		if got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("LVM_mapper行按vgName删除", func(t *testing.T) {
		fstab := "/dev/mapper/vg_nas-data /data/nas1 xfs defaults,prjquota 0 0\n" +
			"/dev/mapper/vg_other-data /data/nas2 xfs defaults,prjquota 0 0\n"
		got := filterFstabForDeletedPool(fstab, []string{"/data/nas1"}, nil, "vg_nas")
		want := "/dev/mapper/vg_other-data /data/nas2 xfs defaults,prjquota 0 0\n"
		if got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("LVM_dev_vgname路径形式按前缀删除", func(t *testing.T) {
		fstab := "/dev/vg_nas/data /data/nas1 xfs defaults,prjquota 0 0\n" +
			"/dev/vg_other/data /data/nas2 xfs defaults,prjquota 0 0\n"
		got := filterFstabForDeletedPool(fstab, nil, nil, "vg_nas")
		want := "/dev/vg_other/data /data/nas2 xfs defaults,prjquota 0 0\n"
		if got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("其他池行保留_RAID分支只用挂载点判据", func(t *testing.T) {
		// RAID/single 分支 lvmVgName 传空串，即便设备路径碰巧含被删 VG 的名字
		// 也不应被判据(b)误删——这里用非 LVM 设备路径验证只判挂载点。
		fstab := "/dev/md0 /data/nas1 xfs defaults,prjquota 0 0\n" +
			"/dev/sdc1 /data/nas2 xfs defaults,prjquota 0 0\n"
		got := filterFstabForDeletedPool(fstab, []string{"/data/nas1"}, nil, "")
		want := "/dev/sdc1 /data/nas2 xfs defaults,prjquota 0 0\n"
		if got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("注释和空行原样保留", func(t *testing.T) {
		fstab := "# fstab comment\n\nUUID=aaa /data/nas1 xfs defaults,prjquota 0 0\n" +
			"UUID=bbb /data/nas2 xfs defaults,prjquota 0 0\n"
		got := filterFstabForDeletedPool(fstab, []string{"/data/nas1"}, nil, "")
		want := "# fstab comment\n\nUUID=bbb /data/nas2 xfs defaults,prjquota 0 0\n"
		if got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("无匹配时输出与输入一致", func(t *testing.T) {
		fstab := "UUID=bbb /data/nas2 xfs defaults,prjquota 0 0"
		got := filterFstabForDeletedPool(fstab, []string{"/data/nas1"}, nil, "")
		if got != fstab {
			t.Errorf("got:\n%q\nwant unchanged:\n%q", got, fstab)
		}
	})

	t.Run("A3_未挂载池只剩UUID行也被删", func(t *testing.T) {
		// 池未挂载时 findmnt 查不到挂载点，deletedMounts 为空；只有判据(c)
		// 的 UUID 匹配能命中这行，否则残留会在下次开机导致 emergency mode。
		fstab := "UUID=aaa /data/nas1 xfs defaults,prjquota 0 2\n" +
			"UUID=bbb /data/nas2 xfs defaults,prjquota 0 2\n"
		got := filterFstabForDeletedPool(fstab, nil, []string{"aaa"}, "")
		want := "UUID=bbb /data/nas2 xfs defaults,prjquota 0 2\n"
		if got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("A3_其他池UUID行保留", func(t *testing.T) {
		fstab := "UUID=aaa /data/nas1 xfs defaults,prjquota 0 2\n" +
			"UUID=bbb /data/nas2 xfs defaults,prjquota 0 2\n"
		got := filterFstabForDeletedPool(fstab, nil, []string{"ccc"}, "")
		if got != fstab {
			t.Errorf("got:\n%q\nwant unchanged:\n%q", got, fstab)
		}
	})

	t.Run("A3_UUID为空值被跳过不panic", func(t *testing.T) {
		fstab := "UUID=aaa /data/nas1 xfs defaults,prjquota 0 2\n"
		got := filterFstabForDeletedPool(fstab, nil, []string{"", "  "}, "")
		if got != fstab {
			t.Errorf("got:\n%q\nwant unchanged:\n%q", got, fstab)
		}
	})

	t.Run("A3_挂载点与UUID判据同时命中去重自然成立", func(t *testing.T) {
		// 挂载态删除时 deletedMounts 和 deletedUUIDs 都非空，两个判据都指向
		// 同一行，结果应与单判据一致（不会重复删或漏删）。
		fstab := "UUID=aaa /data/nas1 xfs defaults,prjquota 0 2\n" +
			"UUID=bbb /data/nas2 xfs defaults,prjquota 0 2\n"
		got := filterFstabForDeletedPool(fstab, []string{"/data/nas1"}, []string{"aaa"}, "")
		want := "UUID=bbb /data/nas2 xfs defaults,prjquota 0 2\n"
		if got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})
}

// TestFirstLine 验证 firstLine 只取多行输出首行（Minor finding：findmnt 对多
// 挂载点 LV 可能输出多行，脏值会破坏挂载点判据）。
func TestFirstLine(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/data/nas1\n", "/data/nas1"},
		{"/data/nas1\n/data/nas1-bind\n", "/data/nas1"},
		{"", ""},
		{"   /data/nas1   \n", "/data/nas1"},
	}
	for _, c := range cases {
		if got := firstLine(c.in); got != c.want {
			t.Errorf("firstLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFindLivePoolByTypeDevice 验证 consensus P0-A1 的服务端重解析匹配逻辑：
// 必须 (Type, Device) 同时精确匹配实时 overview 中的某个池，单独匹配 Type 或
// Device 都不算命中——防止客户端提交过期/篡改的 pool_device 冒充其他池。
func TestFindLivePoolByTypeDevice(t *testing.T) {
	pools := []PoolSummary{
		{Type: "lvm", Device: "/dev/vg_nas", Name: "vg_nas"},
		{Type: "raid1", Device: "/dev/md0", Name: "md0"},
	}

	t.Run("精确匹配命中", func(t *testing.T) {
		got, ok := findLivePoolByTypeDevice(pools, "lvm", "/dev/vg_nas")
		if !ok || got.Device != "/dev/vg_nas" {
			t.Errorf("got %+v, ok=%v, want vg_nas pool matched", got, ok)
		}
	})

	t.Run("设备存在但type不符_不命中", func(t *testing.T) {
		_, ok := findLivePoolByTypeDevice(pools, "raid5", "/dev/vg_nas")
		if ok {
			t.Error("type 不符不应命中")
		}
	})

	t.Run("type存在但device不符_不命中", func(t *testing.T) {
		_, ok := findLivePoolByTypeDevice(pools, "lvm", "/dev/vg_other")
		if ok {
			t.Error("device 不符不应命中（池已变化/被篡改）")
		}
	})

	t.Run("池列表为空_不命中", func(t *testing.T) {
		_, ok := findLivePoolByTypeDevice(nil, "lvm", "/dev/vg_nas")
		if ok {
			t.Error("空池列表不应命中任何请求")
		}
	})

	t.Run("设备已不存在_池已被删除", func(t *testing.T) {
		_, ok := findLivePoolByTypeDevice(pools, "raid1", "/dev/md1")
		if ok {
			t.Error("已删除/不存在的池不应命中")
		}
	})
}

// TestExpandPartitions 验证 single 分支系统盘闸门展开整盘+分区设备名的覆盖面：
// 标准命名（sda→sda1..9）与 NVMe 命名（nvme0n1→nvme0n1p1..9）都要能枚举到。
func TestExpandPartitions(t *testing.T) {
	got := expandPartitions("/dev/sda")
	wantContains := []string{"/dev/sda", "/dev/sda1", "/dev/sda9"}
	for _, w := range wantContains {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expandPartitions(/dev/sda) missing %q, got %v", w, got)
		}
	}

	got2 := expandPartitions("/dev/nvme0n1")
	wantContains2 := []string{"/dev/nvme0n1", "/dev/nvme0n1p1", "/dev/nvme0n1p9"}
	for _, w := range wantContains2 {
		found := false
		for _, g := range got2 {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expandPartitions(/dev/nvme0n1) missing %q, got %v", w, got2)
		}
	}
}

// TestAnySystemDisk 验证组合器正确短路并返回命中的设备名，供 handlePoolDelete
// 的闸门分支单测复用（注入假的 isSystemDisk，不触碰真实系统命令）。
func TestAnySystemDisk(t *testing.T) {
	isSys := func(d string) bool { return d == "/dev/sda" || d == "/dev/sda1" }

	t.Run("命中整盘", func(t *testing.T) {
		dev, hit := anySystemDisk([]string{"/dev/sdb", "/dev/sda"}, isSys)
		if !hit || dev != "/dev/sda" {
			t.Errorf("got dev=%q hit=%v, want /dev/sda,true", dev, hit)
		}
	})

	t.Run("命中分区", func(t *testing.T) {
		dev, hit := anySystemDisk([]string{"/dev/sda1"}, isSys)
		if !hit || dev != "/dev/sda1" {
			t.Errorf("got dev=%q hit=%v, want /dev/sda1,true", dev, hit)
		}
	})

	t.Run("无命中", func(t *testing.T) {
		_, hit := anySystemDisk([]string{"/dev/sdb", "/dev/sdc"}, isSys)
		if hit {
			t.Error("不应命中任何系统盘")
		}
	})

	t.Run("空列表", func(t *testing.T) {
		_, hit := anySystemDisk(nil, isSys)
		if hit {
			t.Error("空列表不应命中")
		}
	})
}

// TestVgsOutputContainsVGName 验证 consensus R2 Item B 的 vgs 存在性闸门：
// 精确行匹配，不被前缀相似的其他 VG 误命中。
func TestVgsOutputContainsVGName(t *testing.T) {
	vgsOut := "  vg_nas\n  vg_nas_backup\n  vg_other\n"
	cases := []struct {
		name   string
		vgName string
		want   bool
	}{
		{"精确命中", "vg_nas", true},
		{"精确命中_另一个", "vg_other", true},
		{"前缀相似但不应误命中", "vg_na", false},
		{"不存在的VG", "vg_ghost", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := vgsOutputContainsVGName(vgsOut, c.vgName)
			if got != c.want {
				t.Errorf("vgsOutputContainsVGName(vgsOut, %q) = %v, want %v", c.vgName, got, c.want)
			}
		})
	}
	t.Run("空输出", func(t *testing.T) {
		if got := vgsOutputContainsVGName("", "vg_nas"); got {
			t.Error("空输出不应命中任何 VG 名")
		}
	})
	t.Run("空VG名不应命中输出中的空行", func(t *testing.T) {
		if got := vgsOutputContainsVGName("  vg_nas\n\n  \n", ""); got {
			t.Error("空字符串 VG 名不应命中输出里 trim 后的空行")
		}
	})
}

// TestParseMdMemberDisks 覆盖正常/空/畸形 mdadm --detail 输出（consensus R1
// Fix 1：handlePoolDelete 的 raid 分支在 parseMdMemberDisks 结果为空时必须安全
// 拒绝删除，这里先确认解析函数本身在空输出/畸形输出下确实返回空切片，
// 不会意外解析出假成员掩盖该安全拒绝路径）。
func TestParseMdMemberDisks(t *testing.T) {
	t.Run("正常输出", func(t *testing.T) {
		output := "    Number   Major   Minor   RaidDevice State\n" +
			"       0       8       16        0      active sync   /dev/sdb\n" +
			"       1       8       32        1      active sync   /dev/sdc\n"
		got := parseMdMemberDisks(output)
		want := []string{"/dev/sdb", "/dev/sdc"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("got[%d]=%q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("空输出", func(t *testing.T) {
		if got := parseMdMemberDisks(""); len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})

	t.Run("畸形输出_无active或spare关键词", func(t *testing.T) {
		// 真实 mdadm --detail 失败/异常时常见："mdadm: cannot open /dev/mdX: No such file or directory"
		output := "mdadm: cannot open /dev/md0: No such file or directory\n"
		if got := parseMdMemberDisks(output); len(got) != 0 {
			t.Errorf("got %v, want empty（无 active/spare 关键词不应解析出成员）", got)
		}
	})

	t.Run("畸形输出_无dev路径", func(t *testing.T) {
		output := "       0       8       16        0      active sync   (no device)\n"
		if got := parseMdMemberDisks(output); len(got) != 0 {
			t.Errorf("got %v, want empty（没有 /dev/ 路径不应解析出成员）", got)
		}
	})
}

// TestHandlePoolDeleteRejectsUnmatchedLivePool 验证 consensus P0-A1 的服务端
// 重解析闸门：测试环境没有真实 mdadm/lvm，buildStorageOverview() 的实时池列表
// 必然为空，任何表单提交的 (pool_type, pool_device) 都无法匹配到实时池，
// handlePoolDelete 必须在任何破坏性命令之前以 409 拒绝，不能走到 switch 分支。
func TestHandlePoolDeleteRejectsUnmatchedLivePool(t *testing.T) {
	form := url.Values{
		"pool_type":    {"lvm"},
		"pool_device":  {"/dev/vg_nas"},
		"pool_name":    {"vg_nas"},
		"confirm_name": {"vg_nas"},
		"confirm":      {"yes"},
	}
	r := httptest.NewRequest(http.MethodPost, "/api/disk/pool/delete", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handlePoolDelete(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d (409)", w.Code, http.StatusConflict)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", ct)
	}
	if !strings.Contains(w.Body.String(), "存储池不存在或状态已变化") {
		t.Errorf("body = %q, want to contain 池不存在/状态已变化 文案", w.Body.String())
	}
}

// TestHandlePoolDeleteRejectsConfirmNameMismatch 回归验证：确认名检查仍先于
// 重解析闸门执行（表单层校验失败不应触发任何 overview 构建开销）。
func TestHandlePoolDeleteRejectsConfirmNameMismatch(t *testing.T) {
	form := url.Values{
		"pool_type":    {"lvm"},
		"pool_device":  {"/dev/vg_nas"},
		"pool_name":    {"vg_nas"},
		"confirm_name": {"wrong"},
		"confirm":      {"yes"},
	}
	r := httptest.NewRequest(http.MethodPost, "/api/disk/pool/delete", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handlePoolDelete(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (400)", w.Code, http.StatusBadRequest)
	}
}

// ═══════════════════════════════════════════════════
// Bug 2 回归测试：handlePoolDelete 此前忽略 umount/lvremove/vgremove/
// pvremove/wipefs/mdadm --stop 等破坏性命令的返回错误，任一失败都会被当
// 成功继续执行后续步骤，最终仍统一报告"存储池已删除"。现在每条破坏性命令
// 失败都必须立即中止，不得继续执行、不得报告虚假成功。
//
// 用 withStubExec 注入命令替身 + withFakeLivePool 注入 findLivePoolFn，
// 绕开真实 overview 探测链，直接驱动 switch 分支内部逻辑。
// ═══════════════════════════════════════════════════

// withFakeLivePool 把 findLivePoolFn 替换为固定返回 (pool, true)，绕开
// buildStorageOverview() 的真实 df/lsblk/mdadm 探测链，让 handlePoolDelete
// 的服务端重解析闸门（consensus P0-A1）直接放行到 switch 分支。t.Cleanup
// 还原原始值（默认实现，行为与生产一致）。
func withFakeLivePool(t *testing.T, pool PoolSummary) {
	t.Helper()
	orig := findLivePoolFn
	findLivePoolFn = func(poolType, poolDevice string) (PoolSummary, bool) {
		if poolType == pool.Type && poolDevice == pool.Device {
			return pool, true
		}
		return PoolSummary{}, false
	}
	t.Cleanup(func() { findLivePoolFn = orig })
}

// deletePoolForm 构造 handlePoolDelete 所需的最小合法表单（通过确认名校验 +
// 重解析闸门后，实际路由到 switch 分支）。
func deletePoolForm(poolType, poolDevice, poolName string) url.Values {
	return url.Values{
		"pool_type":    {poolType},
		"pool_device":  {poolDevice},
		"pool_name":    {poolName},
		"confirm_name": {poolName},
		"confirm":      {"yes"},
	}
}

// TestHandlePoolDeleteSingleSuccessReportsTrueSuccess 验证 single 分支全程
// 成功（无挂载点，跳过 folders.db/SyncAllConfigs 阶段）时如实报告成功，且
// wipefs 确实被调用一次。
func TestHandlePoolDeleteSingleSuccessReportsTrueSuccess(t *testing.T) {
	withFakeLivePool(t, PoolSummary{Type: "single", Device: "/dev/sdz"})
	stub := withStubExec(t, map[string]cmdResponse{
		// findmnt 对 /dev/sdz 本身与分区查询都返回空（无挂载），wipefs 成功。
		"findmnt":          {Out: "", Err: nil},
		"blkid":            {Out: "", Err: nil},
		"cat":              {Out: "", Err: nil},
		"/usr/sbin/wipefs": {Out: "", Err: nil},
	})

	form := deletePoolForm("single", "/dev/sdz", "disk-z")
	r := httptest.NewRequest(http.MethodPost, "/api/disk/pool/delete", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handlePoolDelete(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "存储池已删除") {
		t.Errorf("body = %q, want 成功文案", w.Body.String())
	}
	if !stub.wasCalledWith("/usr/sbin/wipefs", "-a", "/dev/sdz") {
		t.Errorf("calls = %v, want wipefs -a /dev/sdz 被调用", stub.calledNames())
	}
}

// TestHandlePoolDeleteSingleWipefsFailureAbortsAndReportsError 核心回归：
// wipefs 失败必须立即中止并返回明确错误（非 200），不能把失败吞掉继续走到
// "存储池已删除"的成功响应。
func TestHandlePoolDeleteSingleWipefsFailureAbortsAndReportsError(t *testing.T) {
	withFakeLivePool(t, PoolSummary{Type: "single", Device: "/dev/sdz"})
	stub := withStubExec(t, map[string]cmdResponse{
		"findmnt":          {Out: "", Err: nil},
		"blkid":            {Out: "", Err: nil},
		"/usr/sbin/wipefs": {Out: "", Err: fmt.Errorf("device or resource busy")},
	})

	form := deletePoolForm("single", "/dev/sdz", "disk-z")
	r := httptest.NewRequest(http.MethodPost, "/api/disk/pool/delete", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handlePoolDelete(w, r)

	if w.Code == http.StatusOK {
		t.Fatalf("status = 200, want non-200（wipefs 失败不应报告成功）; body=%s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "存储池已删除") {
		t.Errorf("body = %q, 不应包含成功文案", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "busy") {
		t.Errorf("body = %q, want 包含底层错误信息 busy", w.Body.String())
	}
	// cat /etc/fstab、SyncAllConfigs 等收尾清理步骤不应被执行——wipefs 失败后
	// 必须直接 return，不能继续往下走。
	for _, name := range stub.calledNames() {
		if name == "cat" {
			t.Errorf("calls = %v, wipefs 失败后不应继续执行 cat /etc/fstab", stub.calledNames())
		}
	}
}

// TestHandlePoolDeleteSingleUmountFailureAbortsBeforeWipefs 验证 umount 失败
// 同样立即中止，且后续的 wipefs（真正抹除签名的一步）从未被调用——失败后
// 绝不能继续执行更具破坏性的下一步。
func TestHandlePoolDeleteSingleUmountFailureAbortsBeforeWipefs(t *testing.T) {
	withFakeLivePool(t, PoolSummary{Type: "single", Device: "/dev/sdz"})
	stub := withStubExec(t, map[string]cmdResponse{
		"findmnt": {Out: "/data/nas1", Err: nil},
		"umount":  {Out: "", Err: fmt.Errorf("target is busy")},
	})

	form := deletePoolForm("single", "/dev/sdz", "disk-z")
	r := httptest.NewRequest(http.MethodPost, "/api/disk/pool/delete", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handlePoolDelete(w, r)

	if w.Code == http.StatusOK {
		t.Fatalf("status = 200, want non-200（umount 失败不应报告成功）; body=%s", w.Body.String())
	}
	if stub.countCalls("/usr/sbin/wipefs") != 0 {
		t.Errorf("wipefs 被调用了 %d 次, want 0（umount 失败后不应继续执行 wipefs）", stub.countCalls("/usr/sbin/wipefs"))
	}
}

// TestHandlePoolDeleteSingleConfigCleanupFailureReportsDegraded 验证"磁盘已
// 成功拆除，但收尾配置清理失败"场景：wipefs 成功后，读取 /etc/fstab 失败
// （cat 返回 error）。此前这种失败只是追加一行文案进 steps，最终响应仍统一
// 报告"存储池已删除"成功——用户据此以为一切正常，实际上 fstab 清理没做。
// 现在必须用非 200（207）状态码 + 响应体明确标记问题，不能让前端把这种
// 情况当完全成功处理。
func TestHandlePoolDeleteSingleConfigCleanupFailureReportsDegraded(t *testing.T) {
	withFakeLivePool(t, PoolSummary{Type: "single", Device: "/dev/sdz"})
	withStubExec(t, map[string]cmdResponse{
		"findmnt":          {Out: "", Err: nil},
		"blkid":            {Out: "", Err: nil},
		"/usr/sbin/wipefs": {Out: "", Err: nil},
		"cat":              {Out: "", Err: fmt.Errorf("permission denied")},
	})

	form := deletePoolForm("single", "/dev/sdz", "disk-z")
	r := httptest.NewRequest(http.MethodPost, "/api/disk/pool/delete", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handlePoolDelete(w, r)

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want %d (207，部分成功)；body=%s", w.Code, http.StatusMultiStatus, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"message":"存储池已删除，释放`) {
		t.Errorf("body = %q, 磁盘拆除成功但配置清理失败时不应报告完全成功文案", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "permission denied") {
		t.Errorf("body = %q, want 包含底层错误信息 permission denied", w.Body.String())
	}
}

// TestHandlePoolDeleteLVMLvremoveFailureAbortsBeforeVgremove 验证 lvm 分支：
// lvremove 失败必须中止，不能继续执行 vgremove/pvremove/wipefs（否则会在
// LV 还没删掉的情况下把 VG/PV 签名抹了，留下无法恢复的中间状态）。
func TestHandlePoolDeleteLVMLvremoveFailureAbortsBeforeVgremove(t *testing.T) {
	withFakeLivePool(t, PoolSummary{Type: "lvm", Device: "/dev/vg_nas"})
	stub := withStubExec(t, map[string]cmdResponse{
		"/usr/sbin/vgs": {Out: "  vg_nas\n", Err: nil},
		"/usr/sbin/pvs": {Out: "/dev/sdb /dev/sdc", Err: nil},
		"/usr/sbin/lvs": {Out: "data /dev/vg_nas/data\n", Err: nil},
		"findmnt":       {Out: "", Err: nil},
		"/usr/sbin/lvremove": {Out: "", Err: fmt.Errorf("Logical volume vg_nas/data is used by another device")},
	})

	form := deletePoolForm("lvm", "/dev/vg_nas", "vg_nas")
	r := httptest.NewRequest(http.MethodPost, "/api/disk/pool/delete", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handlePoolDelete(w, r)

	if w.Code == http.StatusOK {
		t.Fatalf("status = 200, want non-200（lvremove 失败不应报告成功）; body=%s", w.Body.String())
	}
	if stub.countCalls("/usr/sbin/vgremove") != 0 {
		t.Errorf("vgremove 被调用了 %d 次, want 0（lvremove 失败后不应继续执行 vgremove）", stub.countCalls("/usr/sbin/vgremove"))
	}
	if stub.countCalls("/usr/sbin/pvremove") != 0 {
		t.Errorf("pvremove 被调用了 %d 次, want 0", stub.countCalls("/usr/sbin/pvremove"))
	}
	if stub.countCalls("/usr/sbin/wipefs") != 0 {
		t.Errorf("wipefs 被调用了 %d 次, want 0", stub.countCalls("/usr/sbin/wipefs"))
	}
}

// TestHandlePoolDeleteLVMFullSuccessCallsAllDestructiveSteps 验证 lvm 分支
// 全程成功时，确实依次调用了 lvremove/vgremove/pvremove/wipefs，且如实报告
// 成功（正向用例，防止 Bug 2 修复过度，错误地在成功路径也中止）。
func TestHandlePoolDeleteLVMFullSuccessCallsAllDestructiveSteps(t *testing.T) {
	withFakeLivePool(t, PoolSummary{Type: "lvm", Device: "/dev/vg_nas"})
	stub := withStubExec(t, map[string]cmdResponse{
		"/usr/sbin/vgs":      {Out: "  vg_nas\n", Err: nil},
		"/usr/sbin/pvs":      {Out: "/dev/sdb /dev/sdc", Err: nil},
		"/usr/sbin/lvs":      {Out: "data /dev/vg_nas/data\n", Err: nil},
		"findmnt":            {Out: "", Err: nil},
		"/usr/sbin/lvremove": {Out: "", Err: nil},
		"/usr/sbin/vgremove": {Out: "", Err: nil},
		"/usr/sbin/pvremove": {Out: "", Err: nil},
		"/usr/sbin/wipefs":   {Out: "", Err: nil},
		"cat":                {Out: "", Err: nil},
	})

	form := deletePoolForm("lvm", "/dev/vg_nas", "vg_nas")
	r := httptest.NewRequest(http.MethodPost, "/api/disk/pool/delete", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handlePoolDelete(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200；body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "存储池已删除") {
		t.Errorf("body = %q, want 成功文案", w.Body.String())
	}
	wantCalled := []string{"/usr/sbin/lvremove", "/usr/sbin/vgremove", "/usr/sbin/pvremove", "/usr/sbin/wipefs"}
	for _, name := range wantCalled {
		if stub.countCalls(name) == 0 {
			t.Errorf("命令 %s 未被调用，calls=%v", name, stub.calledNames())
		}
	}
}
