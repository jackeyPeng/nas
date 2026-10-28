package diskmgmt

import (
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
