package diskmgmt

import (
	"sync"
	"testing"
)

// TestMatchVGForMapper 验证 A4：/dev/mapper/<name> 反推 VG 名时，优先做精确前缀
// 匹配（避免短名 VG 如 "vg" 在列表顺序靠前时，对 "vg_nas-data" 这类长名 mapper
// 产生误命中的 Contains 兜底匹配）。
func TestMatchVGForMapper(t *testing.T) {
	cases := []struct {
		name       string
		mapperName string
		vgList     []string
		want       string
	}{
		{
			name:       "普通mapper名直接前缀命中",
			mapperName: "vg_nas-data",
			vgList:     []string{"vg_nas"},
			want:       "vg_nas",
		},
		{
			name:       "VG名含连字符需转义为--再匹配",
			mapperName: "vg--nas-data",
			vgList:     []string{"vg-nas"},
			want:       "vg-nas",
		},
		{
			name:       "短名vg排在长名vg_nas前_长名仍应精确前缀命中不被短名Contains误命中",
			mapperName: "vg_nas-data",
			vgList:     []string{"vg", "vg_nas"},
			want:       "vg_nas",
		},
		{
			name:       "长名vg_nas排在短名vg前_同样应该是长名精确命中",
			mapperName: "vg_nas-data",
			vgList:     []string{"vg_nas", "vg"},
			want:       "vg_nas",
		},
		{
			name:       "无任何VG匹配返回空串",
			mapperName: "vg_other-data",
			vgList:     []string{"vg_nas", "vg_backup"},
			want:       "",
		},
		{
			name:       "空VG列表返回空串",
			mapperName: "vg_nas-data",
			vgList:     nil,
			want:       "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := matchVGForMapper(c.mapperName, c.vgList)
			if got != c.want {
				t.Errorf("matchVGForMapper(%q, %v) = %q, want %q", c.mapperName, c.vgList, got, c.want)
			}
		})
	}
}

// TestExtractVGName 验证 consensus R2 Item B：extractVGName 支持 VG 路径、
// LV 直路径、dm-mapper 三种设备形态，mapper 形态需要正确处理 "--" 转义还原。
func TestExtractVGName(t *testing.T) {
	cases := []struct {
		name   string
		device string
		want   string
	}{
		{"VG路径本身_base现状保持", "/dev/vg_nas", "vg_nas"},
		{"LV直路径取父目录名", "/dev/vg_nas/data", "vg_nas"},
		{"mapper命名_第一个落单连字符分隔", "/dev/mapper/vg_nas-data", "vg_nas"},
		{"mapper命名_转义还原", "/dev/mapper/my--vg-data", "my-vg"},
		{"mapper_control设备特判空串", "/dev/mapper/control", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractVGName(c.device)
			if got != c.want {
				t.Errorf("extractVGName(%q) = %q, want %q", c.device, got, c.want)
			}
		})
	}
}

// TestUnescapeMapperVGName 单独覆盖 mapper 名转义还原算法的边界情况。
func TestUnescapeMapperVGName(t *testing.T) {
	cases := []struct {
		name       string
		mapperName string
		want       string
	}{
		{"简单VG无连字符", "vg_nas-data", "vg_nas"},
		{"VG名含一个转义连字符", "my--vg-data", "my-vg"},
		{"VG名含多个转义连字符", "a--b--c-lv", "a-b-c"},
		{"整串都是转义对无分隔符_还原整串", "a--b--c", "a-b-c"},
		{"空字符串", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := unescapeMapperVGName(c.mapperName)
			if got != c.want {
				t.Errorf("unescapeMapperVGName(%q) = %q, want %q", c.mapperName, got, c.want)
			}
		})
	}
}

// TestOverviewCachesConcurrentAccess 验证 consensus P1-B（C1）的修法：
// cachedPVMappings/cachedRAIDMembers 两个包级变量在并发场景下读写不再 data race。
// 多 goroutine 并发调用 getCachedPVMappings/getCachedRAIDMappings 以及
// resetOverviewCaches（模拟 buildStorageOverview 的并发构建），go test -race
// 必须干净。断言上只验证不 panic/不 race（getter 返回值在并发 reset 下允许
// 看到任意一次填充结果，这是预期的"构建被串行化"行为，不是本测试要验证的点）。
func TestOverviewCachesConcurrentAccess(t *testing.T) {
	var wg sync.WaitGroup
	const n = 50
	wg.Add(n * 3)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = getCachedPVMappings()
		}()
		go func() {
			defer wg.Done()
			_ = getCachedRAIDMembers()
		}()
		go func() {
			defer wg.Done()
			resetOverviewCaches()
		}()
	}
	wg.Wait()
}

// TestBuildStorageOverviewConcurrentNoRace 并发调用 buildStorageOverview 本身
// （真实场景：多个请求同时打存储总览页），验证不 data race。测试环境没有真实
// pvs/mdadm，返回内容为空集是预期的，这里只关心并发安全性。
func TestBuildStorageOverviewConcurrentNoRace(t *testing.T) {
	var wg sync.WaitGroup
	const n = 20
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = buildStorageOverview()
		}()
	}
	wg.Wait()
}

// TestBuildRAIDStatusFromMdstat 验证 getRAIDStatus 枚举源从 `ls /dev/md*`
// 改为直接遍历 parseMdstatBlocks 的块之后，字段填充语义逐字节不变（真机实锤：
// ls 走 exec.Command 直调无 shell，glob 字面量恒报错，此前 getRAIDStatus 恒
// 返回空切片，导致下游 raidMap 恒空、RAID 池 healthy 恒 true）。
func TestBuildRAIDStatusFromMdstat(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []RAIDStatus
	}{
		{
			name: "干净raid1_无进度行",
			content: `Personalities : [raid1]
md0 : active raid1 sdb[1] sda[0]
      1953511424 blocks super 1.2 [2/2] [UU]

unused devices: <none>
`,
			want: []RAIDStatus{
				{Device: "/dev/md0", Level: "1", State: "active", Disks: 2, ActiveDisks: 2, Health: "normal", Activity: "idle"},
			},
		},
		{
			name: "降级阵列_含F失败成员",
			content: `md0 : active raid1 sdb[1](F) sda[0]
      1953511424 blocks super 1.2 [2/1] [U_]

unused devices: <none>
`,
			want: []RAIDStatus{
				{Device: "/dev/md0", Level: "1", State: "degraded", Disks: 2, ActiveDisks: 1, FailedDisks: 1, Health: "degraded", Activity: "idle"},
			},
		},
		{
			name: "含S真spare成员",
			content: `md0 : active raid1 sdc[2](S) sdb[1] sda[0]
      1953511424 blocks super 1.2 [2/2] [UU]

unused devices: <none>
`,
			want: []RAIDStatus{
				{Device: "/dev/md0", Level: "1", State: "active", Disks: 3, ActiveDisks: 2, SpareDisks: 1, Health: "normal", Activity: "idle"},
			},
		},
		{
			name: "resync中_进度行在blocks行之后",
			content: `md0 : active raid1 sdb[1] sda[0]
      1953511424 blocks super 1.2 [2/2] [UU]
      [=>...................]  resync = 15.2% (298560640/1953382464) finish=17.0min speed=162144K/sec

unused devices: <none>
`,
			want: []RAIDStatus{
				{Device: "/dev/md0", Level: "1", State: "resyncing", Disks: 2, ActiveDisks: 2, SyncPercent: "15.2%", Health: "normal", Activity: "resync"},
			},
		},
		{
			name: "多数组并存_各自独立互不污染",
			content: `md1 : active raid1 sdd[1] sdc[0]
      976630464 blocks super 1.2 [2/2] [UU]
      [=>...................]  resync = 42.0% (410000000/976630464) finish=5.0min speed=90000K/sec

md0 : active raid1 sdb[1] sda[0]
      1953511424 blocks super 1.2 [2/2] [UU]

unused devices: <none>
`,
			want: []RAIDStatus{
				{Device: "/dev/md1", Level: "1", State: "resyncing", Disks: 2, ActiveDisks: 2, SyncPercent: "42.0%", Health: "normal", Activity: "resync"},
				{Device: "/dev/md0", Level: "1", State: "active", Disks: 2, ActiveDisks: 2, Health: "normal", Activity: "idle"},
			},
		},
		{
			name:    "空输入返回空切片不panic",
			content: "",
			want:    nil,
		},
		{
			name:    "只有Personalities和unused devices尾行_返回空切片不panic",
			content: "Personalities : [raid1]\nunused devices: <none>\n",
			want:    nil,
		},
		{
			name: "md126多位数命名_正常识别",
			content: `md126 : active raid1 sdb[1] sda[0]
      1953511424 blocks super 1.2 [2/2] [UU]

unused devices: <none>
`,
			want: []RAIDStatus{
				{Device: "/dev/md126", Level: "1", State: "active", Disks: 2, ActiveDisks: 2, Health: "normal", Activity: "idle"},
			},
		},
		// consensus R2 回炉 Fix1：失败成员已被 mdadm --remove 摘除——头行
		// 只剩存活盘 token（无 (F) 标记），降级信息只在 StatusLine 下划线
		// 里。FailedDisks/Disks 均无法反映降级，必须靠 StatusLine 兜底。
		{
			name: "降级阵列_F成员已被remove摘除_头行只剩存活token",
			content: `md0 : active raid1 sda[1]
      1953511424 blocks super 1.2 [2/1] [U_]

unused devices: <none>
`,
			want: []RAIDStatus{
				{Device: "/dev/md0", Level: "1", State: "degraded", Disks: 1, ActiveDisks: 1, Health: "degraded", Activity: "idle"},
			},
		},
		// 干净阵列 [2/2] [UU]，StatusLine 无下划线，Health 仍应判 normal，
		// 确认兜底逻辑不会对健康阵列误报降级。
		{
			name: "干净阵列_StatusLine无下划线_确认不误报降级",
			content: `md0 : active raid1 sdb[1] sda[0]
      1953511424 blocks super 1.2 [2/2] [UU]

unused devices: <none>
`,
			want: []RAIDStatus{
				{Device: "/dev/md0", Level: "1", State: "active", Disks: 2, ActiveDisks: 2, Health: "normal", Activity: "idle"},
			},
		},
		// 含 (F) token 且 StatusLine 同时反映降级（[U_]）——两来源一致，
		// 取 max 不应重复计数（FailedDisks=1、statusDegraded=1，max=1）。
		{
			name: "含F token且StatusLine同步反映降级_两来源取max不重复计数",
			content: `md0 : active raid1 sdb[1](F) sda[0]
      1953511424 blocks super 1.2 [2/1] [U_]

unused devices: <none>
`,
			want: []RAIDStatus{
				{Device: "/dev/md0", Level: "1", State: "degraded", Disks: 2, ActiveDisks: 1, FailedDisks: 1, Health: "degraded", Activity: "idle"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildRAIDStatusFromMdstat(c.content)
			if len(got) != len(c.want) {
				t.Fatalf("got %d entries, want %d: %+v", len(got), len(c.want), got)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("entry %d = %+v, want %+v", i, got[i], c.want[i])
				}
			}
		})
	}
}
