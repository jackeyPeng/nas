package system

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

// TestSyncRegistryMetaPrunesRetired 锁定升级场景：目录删项后（如 FileBrowser
// ID 6/21/24/29/33 随 beta.24 移除），旧 registry.db 里的残留行必须被
// syncRegistryMeta 剪除，否则 GetRegistry 永远返回幽灵项（46 而非 41）。
func TestSyncRegistryMetaPrunesRetired(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE registry (
		id INTEGER PRIMARY KEY,
		category TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		detail TEXT NOT NULL DEFAULT '',
		description TEXT NOT NULL DEFAULT '',
		checked_at TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	// 模拟 beta.23 时代旧库：FileBrowser 幽灵行 + 旧文案的合法行（带旧状态）
	db.Exec(`INSERT INTO registry (id, category, name, status, detail, description) VALUES
		(6, '服务', 'filebrowser — 网页文件管理', 'pass', '未安装(面板文件管理已取代)', '端口 8081')`)
	db.Exec(`INSERT INTO registry (id, category, name, status, detail) VALUES
		(1, '服务', 'smbd — 旧文案', 'pass', 'active + enabled')`)

	syncRegistryMeta(db)

	// FileBrowser 幽灵行必须被剪除
	var ghost int
	db.QueryRow(`SELECT COUNT(*) FROM registry WHERE id=6`).Scan(&ghost)
	if ghost != 0 {
		t.Errorf("retired item id=6 not pruned (rows=%d)", ghost)
	}
	// 总行数 == 当前目录数（41）
	var total int
	db.QueryRow(`SELECT COUNT(*) FROM registry`).Scan(&total)
	if want := len(allRegistryItems()); total != want {
		t.Errorf("registry rows = %d, want %d (catalog size)", total, want)
	}
	// 合法行：名称同步为 canonical，旧状态保留（剪枝不清状态）
	var name, status string
	db.QueryRow(`SELECT name, status FROM registry WHERE id=1`).Scan(&name, &status)
	canonical := ""
	for _, it := range allRegistryItems() {
		if it.ID == 1 {
			canonical = it.Name
		}
	}
	if name != canonical {
		t.Errorf("id=1 name = %q, want canonical %q", name, canonical)
	}
	if status != "pass" {
		t.Errorf("id=1 status = %q, want preserved pass", status)
	}
}

// TestAllRegistryItemsNoFileBrowser 锁定 FileBrowser 彻底移除：注册表应为
// 41 项（46 - 5 个 FileBrowser 专属项），且任何 item 的 Name/Description
// 都不应再提及 filebrowser/FileBrowser（新装机不再安装，不应再出现相关检查项）。
func TestAllRegistryItemsNoFileBrowser(t *testing.T) {
	items := allRegistryItems()
	if len(items) != 41 {
		t.Fatalf("allRegistryItems() 长度 = %d, want 41", len(items))
	}
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.Name), "filebrowser") {
			t.Errorf("item %d Name %q 不应包含 filebrowser", item.ID, item.Name)
		}
		if strings.Contains(strings.ToLower(item.Description), "filebrowser") {
			t.Errorf("item %d Description %q 不应包含 filebrowser", item.ID, item.Description)
		}
	}
}

// TestDetectPoolMode covers mount-source device → pool mode classification.
func TestDetectPoolMode(t *testing.T) {
	cases := []struct {
		name   string
		device string
		want   string
	}{
		{"raid md0", "/dev/md0", "raid"},
		{"raid md slash 0", "/dev/md/0", "raid"},
		{"lvm mapper", "/dev/mapper/vg_nas-data", "lvm"},
		{"lvm dm", "/dev/dm-0", "lvm"},
		{"none empty", "", "none"},
		{"other direct disk", "/dev/sdb1", "other"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := detectPoolMode(c.device)
			if got != c.want {
				t.Errorf("detectPoolMode(%q) = %q, want %q", c.device, got, c.want)
			}
		})
	}
}

const mdstatHealthyFixture = `Personalities : [raid1] 
md0 : active raid1 sdc[1] sdb[0]
      52395008 blocks super 1.2 [2/2] [UU]
      [======>..............]  resync = 30.1% (15789696/52395008) finish=2.9min speed=205938K/sec
      
unused devices: <none>
`

const mdstatDegradedFixture = `Personalities : [raid1] 
md0 : active raid1 sdc[1]
      52395008 blocks super 1.2 [2/1] [U_]
      
unused devices: <none>
`

const mdstatEmptyFixture = `Personalities : 
unused devices: <none>
`

const mdstatMultiFixture = `Personalities : [raid1] 
md0 : active raid1 sdc[1] sdb[0]
      52395008 blocks super 1.2 [2/2] [UU]
      
md1 : active raid1 sdd[1] sde[0]
      104790016 blocks super 1.2 [2/2] [UU]
      
unused devices: <none>
`

// mdstatRebuildingFixture 建池后短时间窗口内的典型现场：3 盘 RAID5 刚建好，
// 第 4 个槽位仍在 recovery（初始同步），不是真降级。
const mdstatRebuildingFixture = `Personalities : [raid5] 
md0 : active raid5 sdd[3] sdc[2] sdb[1] sda[0]
      157161472 blocks super 1.2 level 5, 512k chunk, algorithm 2 [4/3] [UUU_]
      [=====>...............]  recovery = 50.3% (26383616/52387328) finish=2.0min speed=218938K/sec
      
unused devices: <none>
`

// mdstatTrulyDegradedFixture 缺盘且没有任何 resync/recovery 进度行
// （重建已结束但仍缺盘，或从未开始重建）——这才是真正的「阵列降级」。
const mdstatTrulyDegradedFixture = `Personalities : [raid1] 
md0 : active raid1 sdc[1]
      52395008 blocks super 1.2 [2/1] [U_]
      
unused devices: <none>
`

// mdstatHealthyNoResyncFixture 全部槽位在位、没有重建行——稳定态 pass。
const mdstatHealthyNoResyncFixture = `Personalities : [raid1] 
md0 : active raid1 sdc[1] sdb[0]
      52395008 blocks super 1.2 [2/2] [UU]
      
unused devices: <none>
`

func TestParseMdstat(t *testing.T) {
	t.Run("healthy with resync", func(t *testing.T) {
		got := parseMdstat(mdstatHealthyFixture)
		want := []MdArray{
			{Name: "md0", Level: "raid1", Slots: "[UU]", Degraded: false, Resync: "30.1%", ResyncETA: "2.9min"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("parseMdstat() = %+v, want %+v", got, want)
		}
	})

	t.Run("degraded", func(t *testing.T) {
		got := parseMdstat(mdstatDegradedFixture)
		if len(got) != 1 {
			t.Fatalf("expected 1 array, got %d: %+v", len(got), got)
		}
		if !got[0].Degraded {
			t.Errorf("expected Degraded=true for slots %q", got[0].Slots)
		}
		if got[0].Slots != "[U_]" {
			t.Errorf("Slots = %q, want [U_]", got[0].Slots)
		}
	})

	t.Run("empty / unused-only", func(t *testing.T) {
		got := parseMdstat(mdstatEmptyFixture)
		if len(got) != 0 {
			t.Errorf("expected empty slice, got %+v", got)
		}
	})

	t.Run("multiple arrays", func(t *testing.T) {
		got := parseMdstat(mdstatMultiFixture)
		if len(got) != 2 {
			t.Fatalf("expected 2 arrays, got %d: %+v", len(got), got)
		}
		if got[0].Name != "md0" || got[1].Name != "md1" {
			t.Errorf("unexpected names: %+v", got)
		}
	})
}

// TestClassifyMdstat 锁定 48 机验收发现的修复：[UUU_] + recovery 进度行
// 在重建中应为 warn（含进度与 ETA），而非 fail「阵列降级」；重建结束仍缺盘
// 才是真降级 fail；[UU] 无缺位为 pass。
func TestClassifyMdstat(t *testing.T) {
	t.Run("rebuilding in progress -> warn with eta", func(t *testing.T) {
		status, detail := classifyMdstat(mdstatRebuildingFixture)
		if status != "warn" {
			t.Errorf("status = %q, want warn (detail=%q)", status, detail)
		}
		if !strings.Contains(detail, "50.3%") {
			t.Errorf("detail %q should mention progress 50.3%%", detail)
		}
		if !strings.Contains(detail, "2.0min") {
			t.Errorf("detail %q should mention ETA 2.0min", detail)
		}
	})

	t.Run("truly degraded no resync -> fail", func(t *testing.T) {
		status, detail := classifyMdstat(mdstatTrulyDegradedFixture)
		if status != "fail" {
			t.Errorf("status = %q, want fail (detail=%q)", status, detail)
		}
		if !strings.Contains(detail, "降级") {
			t.Errorf("detail %q should mention 降级", detail)
		}
	})

	t.Run("all slots present -> pass", func(t *testing.T) {
		status, _ := classifyMdstat(mdstatHealthyNoResyncFixture)
		if status != "pass" {
			t.Errorf("status = %q, want pass", status)
		}
	})

	t.Run("unparseable block -> fail", func(t *testing.T) {
		status, _ := classifyMdstat("garbage, no md header here\n")
		if status != "fail" {
			t.Errorf("status = %q, want fail for unparseable block", status)
		}
	})
}

// TestStorageItemStates drives the pool-mode-aware judgement table for
// registry items 34-39 across lvm/raid/none/other modes.
func TestStorageItemStates(t *testing.T) {
	healthyArrays := []MdArray{{Name: "md0", Level: "raid1", Slots: "[UU]", Degraded: false, Resync: ""}}
	resyncArrays := []MdArray{{Name: "md0", Level: "raid1", Slots: "[UU]", Degraded: false, Resync: "30.1%"}}
	degradedArrays := []MdArray{{Name: "md0", Level: "raid1", Slots: "[U_]", Degraded: true, Resync: ""}}

	type want struct {
		id     int
		status string
	}

	cases := []struct {
		name       string
		mode       string
		hasVG      bool
		hasLV      bool
		hasPV      bool
		mdCount    int
		arrays     []MdArray
		hasSig     bool
		mountCount int
		expect     []want
	}{
		{
			name: "lvm healthy", mode: "lvm", hasVG: true, hasLV: true, hasPV: true,
			mdCount: 0, arrays: nil, hasSig: true, mountCount: 1,
			expect: []want{{34, "pass"}, {35, "pass"}, {36, "pass"}, {37, "pass"}, {38, "pass"}, {39, "pass"}},
		},
		{
			name: "lvm missing VG matrix contradiction", mode: "lvm", hasVG: false, hasLV: false, hasPV: false,
			mdCount: 0, arrays: nil, hasSig: false, mountCount: 1,
			expect: []want{{34, "fail"}, {35, "fail"}, {36, "fail"}, {37, "pass"}, {38, "warn"}, {39, "pass"}},
		},
		{
			name: "lvm with stray md residue", mode: "lvm", hasVG: true, hasLV: true, hasPV: true,
			mdCount: 1, arrays: healthyArrays, hasSig: true, mountCount: 1,
			expect: []want{{37, "fail"}},
		},
		{
			name: "raid healthy", mode: "raid", hasVG: false, hasLV: false, hasPV: false,
			mdCount: 1, arrays: healthyArrays, hasSig: true, mountCount: 1,
			expect: []want{{34, "pass"}, {35, "pass"}, {36, "pass"}, {37, "pass"}, {38, "pass"}, {39, "pass"}},
		},
		{
			name: "raid degraded", mode: "raid", hasVG: false, hasLV: false, hasPV: false,
			mdCount: 1, arrays: degradedArrays, hasSig: true, mountCount: 1,
			expect: []want{{37, "fail"}},
		},
		{
			name: "raid resync in progress", mode: "raid", hasVG: false, hasLV: false, hasPV: false,
			mdCount: 1, arrays: resyncArrays, hasSig: true, mountCount: 1,
			expect: []want{{37, "pass"}},
		},
		{
			name: "raid degraded but actively rebuilding -> warn not fail", mode: "raid",
			hasVG: false, hasLV: false, hasPV: false,
			mdCount: 1, arrays: parseMdstat(mdstatRebuildingFixture), hasSig: true, mountCount: 1,
			expect: []want{{37, "warn"}},
		},
		{
			name: "raid mount but no array (contradiction)", mode: "raid", hasVG: false, hasLV: false, hasPV: false,
			mdCount: 0, arrays: nil, hasSig: true, mountCount: 1,
			expect: []want{{37, "fail"}},
		},
		{
			name: "raid no signature", mode: "raid", hasVG: false, hasLV: false, hasPV: false,
			mdCount: 1, arrays: healthyArrays, hasSig: false, mountCount: 1,
			expect: []want{{38, "warn"}},
		},
		{
			name: "none unbuilt", mode: "none", hasVG: false, hasLV: false, hasPV: false,
			mdCount: 0, arrays: nil, hasSig: false, mountCount: 0,
			expect: []want{{34, "warn"}, {35, "warn"}, {36, "warn"}, {37, "pass"}, {38, "warn"}, {39, "warn"}},
		},
		{
			name: "none with md residue fail", mode: "none", hasVG: false, hasLV: false, hasPV: false,
			mdCount: 2, arrays: nil, hasSig: false, mountCount: 0,
			expect: []want{{37, "fail"}},
		},
		{
			name: "other direct mount", mode: "other", hasVG: false, hasLV: false, hasPV: false,
			mdCount: 0, arrays: nil, hasSig: true, mountCount: 1,
			expect: []want{{34, "warn"}, {35, "warn"}, {36, "warn"}, {37, "pass"}, {38, "pass"}, {39, "pass"}},
		},
		{
			name: "other with unrelated md devices warn", mode: "other", hasVG: false, hasLV: false, hasPV: false,
			mdCount: 1, arrays: healthyArrays, hasSig: false, mountCount: 1,
			expect: []want{{37, "warn"}, {38, "warn"}},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			states := storageItemStates(c.mode, c.hasVG, c.hasLV, c.hasPV, c.mdCount, c.arrays, c.hasSig, c.mountCount)
			for _, w := range c.expect {
				s, ok := states[w.id]
				if !ok {
					t.Fatalf("item %d missing from states: %+v", w.id, states)
				}
				if s.status != w.status {
					t.Errorf("item %d status = %q, want %q (detail=%q)", w.id, s.status, w.status, s.detail)
				}
			}
		})
	}
}

// TestStorageItemStatesNoneWithLVMPresent 锁定审查 Important#1 回归：池未挂载
// 但 LVM 存在（mount 失败/待重启）时，34-36 应 pass 并保留诊断信号，39 warn。
func TestStorageItemStatesNoneWithLVMPresent(t *testing.T) {
	states := storageItemStates("none", true, true, true, 0, nil, true, 0)
	for _, id := range []int{34, 35, 36} {
		if states[id].status != "pass" {
			t.Errorf("item %d status = %q, want pass (detail=%q)", id, states[id].status, states[id].detail)
		}
	}
	if got := states[34].detail; got != "存在 VG（但池未挂载）" {
		t.Errorf("34 detail = %q", got)
	}
	if states[39].status != "warn" {
		t.Errorf("39 status = %q, want warn", states[39].status)
	}
	if states[38].status != "pass" {
		t.Errorf("38 status = %q, want pass", states[38].status)
	}
}

// TestStorageItemStatesDetailMultiArrayAndCount 锁定审查 Minor#4/#5：多阵列
// 全部列出（不只报第一个），39 恢复挂载计数。
func TestStorageItemStatesDetailMultiArrayAndCount(t *testing.T) {
	arrays := []MdArray{
		{Name: "md0", Level: "raid1", Slots: "[UU]", Resync: ""},
		{Name: "md1", Level: "raid0", Slots: "[UU]", Resync: "12.5%"},
	}
	states := storageItemStates("raid", false, false, false, 2, arrays, true, 2)
	d := states[37].detail
	for _, want := range []string{"md0 raid1 [UU]", "md1 raid0 [UU](resync 12.5%)"} {
		if !strings.Contains(d, want) {
			t.Errorf("37 detail %q should contain %q", d, want)
		}
	}
	if got := states[39].detail; got != "存储池已挂载 (2 个)" {
		t.Errorf("39 detail = %q", got)
	}
}
