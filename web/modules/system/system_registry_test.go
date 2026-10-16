package system

import (
	"reflect"
	"strings"
	"testing"
)

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

func TestParseMdstat(t *testing.T) {
	t.Run("healthy with resync", func(t *testing.T) {
		got := parseMdstat(mdstatHealthyFixture)
		want := []MdArray{
			{Name: "md0", Level: "raid1", Slots: "[UU]", Degraded: false, Resync: "30.1%"},
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
