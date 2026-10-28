package diskmgmt

import (
	"reflect"
	"sort"
	"testing"
)

func sortedCopy(s []string) []string {
	c := append([]string{}, s...)
	sort.Strings(c)
	return c
}

// TestMdstatMemberDisks 验证从 /proc/mdstat 文本解析活跃阵列成员盘集合，
// 去重且补全 /dev/ 前缀。
func TestMdstatMemberDisks(t *testing.T) {
	content := `Personalities : [raid1]
md0 : active raid1 sdb[1] sda[0]
      1953511424 blocks super 1.2 [2/2] [UU]

unused devices: <none>
`
	got := sortedCopy(mdstatMemberDisks(content))
	want := []string{"/dev/sda", "/dev/sdb"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMdstatMemberDisks_NoArrays(t *testing.T) {
	content := "Personalities : \nunused devices: <none>\n"
	got := mdstatMemberDisks(content)
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestMdstatMemberDisks_MultiArray(t *testing.T) {
	content := `md1 : active raid1 sdd[1] sdc[0]
      976630464 blocks super 1.2 [2/2] [UU]

md0 : active raid5 sdb[2] sda[0] sde[1]
      3906764800 blocks super 1.2 level 5, 512k chunk, algorithm 2 [3/3] [UUU]
`
	got := sortedCopy(mdstatMemberDisks(content))
	want := []string{"/dev/sda", "/dev/sdb", "/dev/sdc", "/dev/sdd", "/dev/sde"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestPvsListAll 验证 PV 设备列表解析。
func TestPvsListAll(t *testing.T) {
	out := "  /dev/sda1\n  /dev/sdb1\n"
	got := sortedCopy(pvsListAll(out))
	want := []string{"/dev/sda1", "/dev/sdb1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestPvsListAll_Empty(t *testing.T) {
	if got := pvsListAll(""); len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

// TestResolveMountSourceToPhysicalDevices 覆盖 LV/MD/整盘三种挂载源解析路径。
func TestResolveMountSourceToPhysicalDevices(t *testing.T) {
	pvMappings := map[string]string{
		"/dev/sda1": "vg_nas",
		"/dev/sdb1": "vg_nas",
		"/dev/sdc1": "vg_other",
	}
	mdMembers := map[string][]string{
		"/dev/md0": {"/dev/sdd", "/dev/sde"},
	}

	t.Run("mapper形式LV解析为VG全部PV", func(t *testing.T) {
		got := sortedCopy(resolveMountSourceToPhysicalDevices("/dev/mapper/vg_nas-data", pvMappings, mdMembers))
		want := []string{"/dev/sda1", "/dev/sdb1"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("路径形式LV解析为VG全部PV", func(t *testing.T) {
		got := sortedCopy(resolveMountSourceToPhysicalDevices("/dev/vg_nas/data", pvMappings, mdMembers))
		want := []string{"/dev/sda1", "/dev/sdb1"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("MD阵列解析为成员盘", func(t *testing.T) {
		got := sortedCopy(resolveMountSourceToPhysicalDevices("/dev/md0", pvMappings, mdMembers))
		want := []string{"/dev/sdd", "/dev/sde"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("未知MD阵列无成员信息时退化为自身", func(t *testing.T) {
		got := resolveMountSourceToPhysicalDevices("/dev/md1", pvMappings, mdMembers)
		want := []string{"/dev/md1"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("整盘分区直接挂载返回自身", func(t *testing.T) {
		got := resolveMountSourceToPhysicalDevices("/dev/sdf1", pvMappings, mdMembers)
		want := []string{"/dev/sdf1"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("空字符串返回nil", func(t *testing.T) {
		got := resolveMountSourceToPhysicalDevices("", pvMappings, mdMembers)
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

// TestCollectManagedWipeTargets_ForeignUSBDiskNeverManaged 是 consensus P0-A2
// 修法要求的核心用例：外来 USB 备份盘带文件系统/数据签名，但从未成为 PV/RAID
// 成员，也不在 /data/nas* 挂载点下 —— 必须完全不在纳管目标集合内。
func TestCollectManagedWipeTargets_ForeignUSBDiskNeverManaged(t *testing.T) {
	// 面板纳管：/dev/sda1 + /dev/sdb1 组成 vg_nas，挂载在 /data/nas1。
	pvsOut := "  /dev/sda1   vg_nas\n  /dev/sdb1   vg_nas\n"
	mdstat := "Personalities : \nunused devices: <none>\n"
	dataMounts := []map[string]string{
		{"device": "/dev/mapper/vg_nas-data", "mount": "/data/nas1"},
	}

	got := collectManagedWipeTargets(pvsOut, mdstat, dataMounts)
	gotSet := map[string]bool{}
	for _, d := range got {
		gotSet[d] = true
	}

	// 纳管设备必须在集合内
	for _, want := range []string{"/dev/sda1", "/dev/sdb1"} {
		if !gotSet[want] {
			t.Errorf("纳管设备 %s 应在集合内，got %v", want, got)
		}
	}
	// 外来 USB 盘 /dev/sdc（从未纳管，即便带文件系统签名）绝不能出现在集合里——
	// 这个用例本身只验证集合的"正向"内容不含 sdc；真正的"不触碰"保障在
	// isSystemDisk 之外是靠 wipeManagedTargets 只遍历这个集合来实现的。
	if gotSet["/dev/sdc"] {
		t.Error("外来 USB 盘 /dev/sdc 从未纳管，不应出现在擦盘目标集合中")
	}
}

// TestCollectManagedWipeTargets_UnionDedup 验证三路来源的并集去重：同一设备
// 既是 PV 又是 /data/nas* 挂载源解析结果时，只出现一次。
func TestCollectManagedWipeTargets_UnionDedup(t *testing.T) {
	pvsOut := "  /dev/sda1   vg_nas\n"
	mdstat := ""
	dataMounts := []map[string]string{
		{"device": "/dev/vg_nas/data", "mount": "/data/nas1"},
	}
	got := collectManagedWipeTargets(pvsOut, mdstat, dataMounts)
	count := 0
	for _, d := range got {
		if d == "/dev/sda1" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("/dev/sda1 应只出现一次，got %d 次，完整结果 %v", count, got)
	}
}

// TestCollectManagedWipeTargets_RAIDMembersIncluded 验证活跃 RAID 阵列的成员盘
// 即便 /data 挂载点解析走的是别的分支，也必须整体进入纳管集合（独立于挂载点来源）。
func TestCollectManagedWipeTargets_RAIDMembersIncluded(t *testing.T) {
	pvsOut := ""
	mdstat := "md0 : active raid1 sdb[1] sda[0]\n      1953511424 blocks super 1.2 [2/2] [UU]\n"
	dataMounts := []map[string]string{
		{"device": "/dev/md0", "mount": "/data/nas1"},
	}
	got := collectManagedWipeTargets(pvsOut, mdstat, dataMounts)
	gotSet := map[string]bool{}
	for _, d := range got {
		gotSet[d] = true
	}
	for _, want := range []string{"/dev/sda", "/dev/sdb"} {
		if !gotSet[want] {
			t.Errorf("RAID 成员盘 %s 应在集合内，got %v", want, got)
		}
	}
}

// TestCollectManagedWipeTargets_IgnoresNonDataMounts 验证非 /data/nas* 前缀的
// 挂载点（如 /mnt/usb-backup）不会被当作纳管挂载源解析进集合。
func TestCollectManagedWipeTargets_IgnoresNonDataMounts(t *testing.T) {
	pvsOut := ""
	mdstat := ""
	dataMounts := []map[string]string{
		{"device": "/dev/sdz1", "mount": "/mnt/usb-backup"},
	}
	got := collectManagedWipeTargets(pvsOut, mdstat, dataMounts)
	for _, d := range got {
		if d == "/dev/sdz1" {
			t.Errorf("非 /data/nas* 挂载点的设备不应进入纳管集合，got %v", got)
		}
	}
}

// TestSkipUnmanagedDisks 验证 getDataDisks() 中不在纳管集合内的盘被正确识别。
func TestSkipUnmanagedDisks(t *testing.T) {
	dataDisks := []string{"/dev/sda", "/dev/sdb", "/dev/sdc"}
	managed := []string{"/dev/sda", "/dev/sdb"}
	got := skipUnmanagedDisks(dataDisks, managed)
	want := []string{"/dev/sdc"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSkipUnmanagedDisks_AllManaged(t *testing.T) {
	got := skipUnmanagedDisks([]string{"/dev/sda"}, []string{"/dev/sda"})
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

// TestWipeManagedTargets_SkipsSystemDisk 验证系统盘命中时跳过该盘记日志，
// 但不中止——其余目标仍继续处理（"尽力拆除"语义，与 handlePoolDelete 的
// "拒绝整个操作"不同）。因为 isSystemDisk 内部调用真实 findmnt，这里只验证
// 返回的 steps 文案结构（findmnt 在测试环境查不到挂载点，不会误判为系统盘，
// 真正的系统盘分支覆盖已由 storage_test.go 的 isSystemDisk 单测覆盖）。
func TestWipeManagedTargets_StepsNonEmpty(t *testing.T) {
	steps := wipeManagedTargets([]string{"/dev/zz_not_exist"})
	if len(steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(steps))
	}
}

func TestWipeManagedTargets_EmptyInput(t *testing.T) {
	steps := wipeManagedTargets(nil)
	if len(steps) != 0 {
		t.Errorf("got %v, want empty", steps)
	}
}

// TestPvsPVToVGMap 验证 PV→VG 映射解析。
func TestPvsPVToVGMap(t *testing.T) {
	out := "  /dev/sda1   vg_nas\n  /dev/sdb1   vg_nas\n  /dev/sdc1   vg_other\n"
	got := pvsPVToVGMap(out)
	want := map[string]string{
		"/dev/sda1": "vg_nas",
		"/dev/sdb1": "vg_nas",
		"/dev/sdc1": "vg_other",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestMdstatMembersByDevice 验证 md设备→成员盘 映射解析。
func TestMdstatMembersByDevice(t *testing.T) {
	content := `md1 : active raid1 sdd[1] sdc[0]
      976630464 blocks super 1.2 [2/2] [UU]

md0 : active raid5 sdb[2] sda[0] sde[1]
      3906764800 blocks super 1.2 level 5, 512k chunk, algorithm 2 [3/3] [UUU]
`
	got := mdstatMembersByDevice(content)
	if got["/dev/md1"] == nil || got["/dev/md0"] == nil {
		t.Fatalf("got %v, want both md0 and md1 present", got)
	}
	if !reflect.DeepEqual(sortedCopy(got["/dev/md1"]), []string{"/dev/sdc", "/dev/sdd"}) {
		t.Errorf("md1 members = %v", got["/dev/md1"])
	}
	if !reflect.DeepEqual(sortedCopy(got["/dev/md0"]), []string{"/dev/sda", "/dev/sdb", "/dev/sde"}) {
		t.Errorf("md0 members = %v", got["/dev/md0"])
	}
}
