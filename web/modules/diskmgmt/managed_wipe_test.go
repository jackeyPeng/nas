package diskmgmt

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
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

// TestMdstatMemberDisks_InactiveArray 是 consensus R2 Item C 的核心用例：
// inactive 阵列（成员盘仍带 RAID 超级块，常见于掉盘/未完全组装）此前因只认
// " : active" 行被完全漏掉，重置后残留签名可能被开机自动重组成幽灵阵列。
func TestMdstatMemberDisks_InactiveArray(t *testing.T) {
	content := `md0 : inactive sdb[1](S) sda[0](S)
      104792064 blocks super 1.2

unused devices: <none>
`
	got := sortedCopy(mdstatMemberDisks(content))
	want := []string{"/dev/sda", "/dev/sdb"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v（inactive 阵列成员必须纳入擦盘集合）", got, want)
	}
}

// TestMdstatMemberDisks_ActiveAndInactiveMixed 验证 active/inactive 两种阵列
// 并存时，两边成员都要进纳管集合，互不遗漏。
func TestMdstatMemberDisks_ActiveAndInactiveMixed(t *testing.T) {
	content := `md1 : active raid1 sdd[1] sdc[0]
      976630464 blocks super 1.2 [2/2] [UU]

md0 : inactive sdb[1](S) sda[0](S)
      104792064 blocks super 1.2
`
	got := sortedCopy(mdstatMemberDisks(content))
	want := []string{"/dev/sda", "/dev/sdb", "/dev/sdc", "/dev/sdd"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestMdstatMemberDisks_FailedAndSpareFlags 验证成员带 (F)/(S) 标志不影响
// 归集——重置要擦的就是所有成员盘，不区分其当前角色。
func TestMdstatMemberDisks_FailedAndSpareFlags(t *testing.T) {
	content := `md0 : active raid1 sdc[2](S) sdb[1](F) sda[0]
      1953511424 blocks super 1.2 [2/1] [U_]
`
	got := sortedCopy(mdstatMemberDisks(content))
	want := []string{"/dev/sda", "/dev/sdb", "/dev/sdc"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v（(F)/(S) 标志不应影响成员归集）", got, want)
	}
}

// TestPvsListAll 验证 PV 设备列表解析——单列输入（兜底，非实际调用形态）。
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

// TestPvsListAll_TwoColumnRealistic 回归测试（issue #1）：所有实际调用方
// （reset.go、stream.go）传入的都是 `pvs --noheadings -o pv_name,vg_name`
// 两列输出。修复前此函数把整行当成一个设备字符串，会产出
// "/dev/sda1   vg_nas" 这种非法条目，同时真正的 PV 名称不会被正确识别为
// "已纳管"——污染 collectManagedWipeTargets 的判定依据。
func TestPvsListAll_TwoColumnRealistic(t *testing.T) {
	out := "  /dev/sda1   vg_nas\n  /dev/sdb1   vg_nas\n  /dev/sdc1   vg_other\n"
	got := sortedCopy(pvsListAll(out))
	want := []string{"/dev/sda1", "/dev/sdb1", "/dev/sdc1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v（两列 pvs 输出必须只取 PV 列，不能把整行当设备名）", got, want)
	}
	for _, dev := range got {
		if strings.Contains(dev, " ") {
			t.Errorf("设备名 %q 包含空格，说明把 VG 名也拼进了设备字符串", dev)
		}
	}
}

// TestPvsListAll_MixedVG 验证混合多个 VG、含不规则空白（tab/多空格）的真实
// pvs 输出仍能正确解析出全部 PV，且不会产生跨 VG 串污。
func TestPvsListAll_MixedVG(t *testing.T) {
	out := "  /dev/sda1\tvg_nas\n  /dev/nvme0n1p1   vg_fast\n  /dev/sdd1  vg_backup\n\n"
	got := sortedCopy(pvsListAll(out))
	want := []string{"/dev/nvme0n1p1", "/dev/sda1", "/dev/sdd1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
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
	// issue #1 回归：pvsOut 是真实的两列 pvs 输出，修复前 pvsListAll 会把
	// "/dev/sda1   vg_nas" 整行当成一个设备名塞进集合，产出带空格的非法条目。
	for _, d := range got {
		if strings.Contains(d, " ") {
			t.Errorf("纳管集合包含非法设备名 %q（疑似把 pvs 整行当成了设备名）", d)
		}
	}
	if gotSet["/dev/sda1   vg_nas"] || gotSet["/dev/sdb1   vg_nas"] {
		t.Errorf("纳管集合混入了未切分的 pvs 整行，got %v", got)
	}
}

// TestCollectManagedWipeTargets_MixedVGAndPartitions 覆盖 issue #1 要求的
// 混合场景：多个 VG（各自的 PV 来自不同分区/整盘）同时存在，且其中一个 VG
// 通过 /data/nas* 挂载点引用、另一个 VG 只出现在 pvs 输出里（未挂载也必须
// 纳管，因为它是面板可见的 PV），验证两者都被正确识别为独立设备、不互相
// 污染，且不产生跨行拼接的垃圾条目。
func TestCollectManagedWipeTargets_MixedVGAndPartitions(t *testing.T) {
	pvsOut := strings.Join([]string{
		"  /dev/sda1   vg_nas",
		"  /dev/sdb1   vg_nas",
		"  /dev/nvme0n1p1   vg_fast",
		"  /dev/sdd1   vg_backup",
	}, "\n") + "\n"
	mdstat := ""
	dataMounts := []map[string]string{
		{"device": "/dev/mapper/vg_nas-data", "mount": "/data/nas1"},
	}
	got := collectManagedWipeTargets(pvsOut, mdstat, dataMounts)
	gotSet := map[string]bool{}
	for _, d := range got {
		gotSet[d] = true
	}
	want := []string{"/dev/sda1", "/dev/sdb1", "/dev/nvme0n1p1", "/dev/sdd1"}
	for _, w := range want {
		if !gotSet[w] {
			t.Errorf("设备 %s 应在纳管集合内（来自 pvs 列出的 PV），got %v", w, got)
		}
	}
	for _, d := range got {
		if strings.Contains(d, " ") {
			t.Errorf("纳管集合包含非法设备名 %q", d)
		}
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

// TestCollectManagedWipeTargets_InactiveArrayMembersIncluded 是 consensus R2
// Item C 的端到端用例：inactive 阵列的成员盘即便该阵列未挂载/未被任何
// /data 挂载点引用，也必须整体进入纳管集合（通过 path (b) 全部活跃+inactive
// MD 阵列成员覆盖），防止重置后残留 RAID 超级块被开机自动重组成幽灵阵列。
func TestCollectManagedWipeTargets_InactiveArrayMembersIncluded(t *testing.T) {
	pvsOut := ""
	mdstat := "md0 : inactive sdb[1](S) sda[0](S)\n      104792064 blocks super 1.2\n"
	dataMounts := []map[string]string{} // 未挂载（inactive 阵列常见场景）
	got := collectManagedWipeTargets(pvsOut, mdstat, dataMounts)
	gotSet := map[string]bool{}
	for _, d := range got {
		gotSet[d] = true
	}
	for _, want := range []string{"/dev/sda", "/dev/sdb"} {
		if !gotSet[want] {
			t.Errorf("inactive 阵列成员盘 %s 应在纳管集合内，got %v", want, got)
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

// TestIntersectDevices_OnlyRealPVsKept 是本轮独立复核修复的核心用例：
// managedTargets 混有真实 PV 与纯 RAID 成员盘（纯 RAID 场景下 RAID 成员盘
// 根本不是 PV），intersectDevices 与 pvsListAll 的结果取交集后，必须只保留
// 真正的 PV，RAID 成员盘一个都不应出现——否则 reset.go/stream.go 会对
// RAID 成员盘误调用 pvremove。
func TestIntersectDevices_OnlyRealPVsKept(t *testing.T) {
	// 纯 RAID、未叠 LVM：managedTargets 里全是 RAID 成员盘，pvsOut 为空
	// （主机上没有任何 PV）。
	managedTargets := []string{"/dev/sda", "/dev/sdb"}
	realPVs := pvsListAll("") // 空 pvs 输出 → 没有真实 PV
	got := intersectDevices(managedTargets, realPVs)
	if len(got) != 0 {
		t.Errorf("纯 RAID 场景下交集应为空（没有真实 PV），got %v", got)
	}
}

// TestIntersectDevices_MixedPVAndRAIDMembers 验证混合场景：managedTargets
// 同时包含 LVM 的真实 PV（/dev/sdc1、/dev/sdd1）与另一套纯 RAID 的成员盘
// （/dev/sda、/dev/sdb，不叠 LVM）——交集必须只保留 PV，RAID 成员盘必须
// 被排除在 pvremove 目标之外。
func TestIntersectDevices_MixedPVAndRAIDMembers(t *testing.T) {
	managedTargets := []string{"/dev/sda", "/dev/sdb", "/dev/sdc1", "/dev/sdd1"}
	pvsOut := "  /dev/sdc1   vg_nas\n  /dev/sdd1   vg_nas\n"
	got := sortedCopy(intersectDevices(managedTargets, pvsListAll(pvsOut)))
	want := []string{"/dev/sdc1", "/dev/sdd1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v（RAID 成员盘 /dev/sda、/dev/sdb 不是 PV，不应进入 pvremove 目标）", got, want)
	}
}

func TestIntersectDevices_EmptyTargets(t *testing.T) {
	if got := intersectDevices(nil, []string{"/dev/sda1"}); len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestIntersectDevices_PreservesTargetsOrder(t *testing.T) {
	managedTargets := []string{"/dev/sdd1", "/dev/sdc1", "/dev/sda1"}
	allowed := []string{"/dev/sda1", "/dev/sdc1", "/dev/sdd1"}
	got := intersectDevices(managedTargets, allowed)
	want := []string{"/dev/sdd1", "/dev/sdc1", "/dev/sda1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v（应保持 targets 原始顺序，不是 allowed 的顺序）", got, want)
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

// 问题 1 补充回归：此前 mdadm --zero-superblock / wipefs -a 的错误被
// common.SudoExec 直接丢弃，不管命令实际成功与否，step 文案永远是
// "清除磁盘签名 <dev>"——用户完全看不出某块盘的签名其实没擦掉。现在任一
// 命令失败都必须让该盘对应的 step 文案带上 "✗" 和真实错误，不能假装成功。
func TestWipeManagedTargets_WipefsFailure_ReportsFailureNotFakeSuccess(t *testing.T) {
	orig := sudoExecFn
	sudoExecFn = func(name string, args ...string) (string, error) {
		if name == "/usr/sbin/wipefs" {
			return "", fmt.Errorf("wipefs: 设备忙")
		}
		return "", nil
	}
	t.Cleanup(func() { sudoExecFn = orig })

	steps := wipeManagedTargets([]string{"/dev/zz_not_exist"})
	if len(steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(steps))
	}
	if !strings.Contains(steps[0], "✗") || !strings.Contains(steps[0], "设备忙") {
		t.Errorf("wipefs 失败应在 step 中如实体现，got: %q", steps[0])
	}
}

// mdadm --zero-superblock 失败同理，且与 wipefs 失败互不掩盖——两个都失败时
// 两条失败原因都应出现在同一条 step 文案里。
func TestWipeManagedTargets_MdadmAndWipefsBothFail_ReportsBothReasons(t *testing.T) {
	orig := sudoExecFn
	sudoExecFn = func(name string, args ...string) (string, error) {
		switch name {
		case "/usr/sbin/mdadm":
			return "", fmt.Errorf("mdadm: 找不到超级块")
		case "/usr/sbin/wipefs":
			return "", fmt.Errorf("wipefs: 设备忙")
		}
		return "", nil
	}
	t.Cleanup(func() { sudoExecFn = orig })

	steps := wipeManagedTargets([]string{"/dev/zz_not_exist"})
	if len(steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(steps))
	}
	if !strings.Contains(steps[0], "找不到超级块") || !strings.Contains(steps[0], "设备忙") {
		t.Errorf("两个命令的失败原因都应出现在 step 中，got: %q", steps[0])
	}
}

// 全部成功时 step 文案应保持原有的简洁正常路径格式，不受本次修复影响。
func TestWipeManagedTargets_Success_NormalStepFormat(t *testing.T) {
	orig := sudoExecFn
	sudoExecFn = func(name string, args ...string) (string, error) { return "", nil }
	t.Cleanup(func() { sudoExecFn = orig })

	steps := wipeManagedTargets([]string{"/dev/zz_not_exist"})
	if len(steps) != 1 || steps[0] != "清除磁盘签名 /dev/zz_not_exist" {
		t.Errorf("成功路径 step 文案不应变化，got: %v", steps)
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

// TestManagedVGNames_OnlyManagedVGIncluded 是 Item 1 的核心用例：宿主机上
// 既有面板纳管的 vg_nas（成员盘在 managedTargets 内），又有一个完全不相关的
// vg_other（比如同机另一套未被面板接管的 LVM）——managedVGNames 必须只返回
// vg_nas，vg_other 绝不能出现（否则会被外层 vgremove 无差别删除）。
func TestManagedVGNames_OnlyManagedVGIncluded(t *testing.T) {
	pvsOut := "  /dev/sda1   vg_nas\n  /dev/sdb1   vg_nas\n  /dev/sdc1   vg_other\n"
	managedTargets := []string{"/dev/sda1", "/dev/sdb1"}
	got := managedVGNames(pvsOut, managedTargets)
	want := []string{"vg_nas"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v（vg_other 未纳管，不应出现）", got, want)
	}
}

func TestManagedVGNames_NoManagedTargets(t *testing.T) {
	pvsOut := "  /dev/sda1   vg_nas\n"
	got := managedVGNames(pvsOut, nil)
	if len(got) != 0 {
		t.Errorf("got %v, want empty（managedTargets 为空时不应返回任何 VG）", got)
	}
}

func TestManagedVGNames_Dedup(t *testing.T) {
	pvsOut := "  /dev/sda1   vg_nas\n  /dev/sdb1   vg_nas\n"
	managedTargets := []string{"/dev/sda1", "/dev/sdb1"}
	got := managedVGNames(pvsOut, managedTargets)
	want := []string{"vg_nas"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v（同一 VG 两个 PV 都纳管时只应出现一次）", got, want)
	}
}

func TestManagedVGNames_MalformedLinesIgnored(t *testing.T) {
	pvsOut := "garbage line\n  /dev/sda1   vg_nas\n\n"
	managedTargets := []string{"/dev/sda1"}
	got := managedVGNames(pvsOut, managedTargets)
	want := []string{"vg_nas"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestManagedMDDevices_OnlyManagedArrayIncluded 验证宿主机上既有纳管阵列
// （成员盘在 managedTargets 内）又有一个完全不相关的外来 md 阵列时，只返回
// 纳管的那个——外来阵列绝不能被 stopRAIDArrays 一起停掉。
func TestManagedMDDevices_OnlyManagedArrayIncluded(t *testing.T) {
	mdstat := `md0 : active raid1 sdb[1] sda[0]
      1953511424 blocks super 1.2 [2/2] [UU]

md1 : active raid1 sdd[1] sdc[0]
      976630464 blocks super 1.2 [2/2] [UU]
`
	managedTargets := []string{"/dev/sda", "/dev/sdb"}
	got := managedMDDevices(mdstat, managedTargets)
	want := []string{"/dev/md0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v（md1 未纳管，不应出现）", got, want)
	}
}

func TestManagedMDDevices_NoManagedTargets(t *testing.T) {
	mdstat := "md0 : active raid1 sdb[1] sda[0]\n      1953511424 blocks super 1.2 [2/2] [UU]\n"
	got := managedMDDevices(mdstat, nil)
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestManagedMDDevices_InactiveArrayStillMatched(t *testing.T) {
	mdstat := "md0 : inactive sdb[1](S) sda[0](S)\n      104792064 blocks super 1.2\n"
	managedTargets := []string{"/dev/sda"}
	got := managedMDDevices(mdstat, managedTargets)
	want := []string{"/dev/md0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v（inactive 阵列只要有一个成员纳管也要命中）", got, want)
	}
}

func TestManagedMDDevices_EmptyMdstat(t *testing.T) {
	got := managedMDDevices("", []string{"/dev/sda"})
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

// TestMdstatMembersByDevice_InactiveArray 验证 mdstatMembersByDevice（path (c)
// 用于 /data/nas* 挂载点解析）同样覆盖 inactive 阵列——与 mdstatMemberDisks
// 同源同逻辑，consensus R2 Item C 范围扩展（报告已申报）。
func TestMdstatMembersByDevice_InactiveArray(t *testing.T) {
	content := `md0 : inactive sdb[1](S) sda[0](S)
      104792064 blocks super 1.2
`
	got := mdstatMembersByDevice(content)
	if got["/dev/md0"] == nil {
		t.Fatalf("got %v, want md0 present", got)
	}
	if !reflect.DeepEqual(sortedCopy(got["/dev/md0"]), []string{"/dev/sda", "/dev/sdb"}) {
		t.Errorf("md0 members = %v, want [/dev/sda /dev/sdb]", got["/dev/md0"])
	}
}
