package diskmgmt

import "testing"

// 真机 fixture（10.216.10.57，md0 raid1 2×50G，Debian 13，2026-10-28 实采）。
// 四态：干净/降级/重建/恢复完成——逐字段按任务书给出的真机采样构造。

// cleanRAID1SysfsVals 干净态。
func cleanRAID1SysfsVals() map[string]string {
	return map[string]string{
		"level":            "raid1",
		"array_state":      "clean",
		"degraded":         "0",
		"raid_disks":       "2",
		"sync_action":      "idle",
		"sync_completed":   "none",
		"sync_speed":       "none",
		"reshape_position": "none",
		"last_sync_action": "idle",
	}
}

func cleanRAID1SysfsMembers() map[string]string {
	return map[string]string{"rd0": "in_sync", "rd1": "in_sync"}
}

// degradedRAID1SysfsVals 降级态：array_state 仍是 clean；仅 rd0 存在
// （failed 成员 rd* 目录消失）。
func degradedRAID1SysfsVals() map[string]string {
	return map[string]string{
		"level":            "raid1",
		"array_state":      "clean",
		"degraded":         "1",
		"raid_disks":       "2",
		"sync_action":      "idle",
		"sync_completed":   "none",
		"sync_speed":       "none",
		"reshape_position": "none",
		"last_sync_action": "recover",
	}
}

func degradedRAID1SysfsMembers() map[string]string {
	return map[string]string{"rd0": "in_sync"}
}

// recoveringRAID1SysfsVals 重建态：sync_action=recover（不是 mdstat 文本的
// "recovery"），sync_completed 带单位 KB 的 done/total。
func recoveringRAID1SysfsVals() map[string]string {
	return map[string]string{
		"level":            "raid1",
		"array_state":      "clean",
		"degraded":         "1",
		"raid_disks":       "2",
		"sync_action":      "recover",
		"sync_completed":   "0 / 104790016",
		"sync_speed":       "200213",
		"reshape_position": "none",
	}
}

func recoveringRAID1SysfsMembers() map[string]string {
	return map[string]string{"rd0": "in_sync", "rd1": "spare"}
}

// recoveredRAID1SysfsVals 恢复完成：degraded=0, sync_action=idle, array_state=clean。
func recoveredRAID1SysfsVals() map[string]string {
	return map[string]string{
		"level":            "raid1",
		"array_state":      "clean",
		"degraded":         "0",
		"raid_disks":       "2",
		"sync_action":      "idle",
		"sync_completed":   "none",
		"sync_speed":       "none",
		"reshape_position": "none",
	}
}

func recoveredRAID1SysfsMembers() map[string]string {
	return map[string]string{"rd0": "in_sync", "rd1": "in_sync"}
}

// TestBuildRAIDStatusFromSysfs_FourStates 表驱动覆盖真机四态 fixture，
// 验证 Health/Activity 分离语义，尤其降级态 array_state 仍是 clean 但
// Health 必须判 degraded。
func TestBuildRAIDStatusFromSysfs_FourStates(t *testing.T) {
	cases := []struct {
		name    string
		vals    map[string]string
		members map[string]string
		want    RAIDStatus
	}{
		{
			name:    "干净态",
			vals:    cleanRAID1SysfsVals(),
			members: cleanRAID1SysfsMembers(),
			want: RAIDStatus{
				Device: "/dev/md0", Level: "1", State: "clean",
				Disks: 2, ActiveDisks: 2, Health: "normal", Activity: "idle",
			},
		},
		{
			name:    "降级态_array_state仍是clean",
			vals:    degradedRAID1SysfsVals(),
			members: degradedRAID1SysfsMembers(),
			want: RAIDStatus{
				Device: "/dev/md0", Level: "1", State: "degraded",
				Disks: 1, ActiveDisks: 1, FailedDisks: 1,
				Health: "degraded", Activity: "idle",
			},
		},
		{
			name:    "重建态_sync_action是recover不是recovery",
			vals:    recoveringRAID1SysfsVals(),
			members: recoveringRAID1SysfsMembers(),
			want: RAIDStatus{
				Device: "/dev/md0", Level: "1", State: "degraded",
				Disks: 2, ActiveDisks: 1, SpareDisks: 1,
				Health: "degraded", Activity: "recover", SyncPercent: "0.0%",
			},
		},
		{
			name:    "恢复完成",
			vals:    recoveredRAID1SysfsVals(),
			members: recoveredRAID1SysfsMembers(),
			want: RAIDStatus{
				Device: "/dev/md0", Level: "1", State: "clean",
				Disks: 2, ActiveDisks: 2, Health: "normal", Activity: "idle",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildRAIDStatusFromSysfs("md0", c.vals, c.members)
			if got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestParseSyncCompleted 覆盖 "none"/正常值/畸形输入三类。
func TestParseSyncCompleted(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantDone  int64
		wantTotal int64
		wantOK    bool
	}{
		{"none值", "none", 0, 0, false},
		{"空字符串", "", 0, 0, false},
		{"正常值", "0 / 104790016", 0, 104790016, true},
		{"正常值_done非零", "52395008 / 104790016", 52395008, 104790016, true},
		{"畸形_无斜杠", "12345", 0, 0, false},
		{"畸形_非数字", "abc / def", 0, 0, false},
		{"畸形_total为0", "0 / 0", 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			done, total, ok := parseSyncCompleted(c.raw)
			if ok != c.wantOK || done != c.wantDone || total != c.wantTotal {
				t.Errorf("parseSyncCompleted(%q) = (%d, %d, %v), want (%d, %d, %v)",
					c.raw, done, total, ok, c.wantDone, c.wantTotal, c.wantOK)
			}
		})
	}
}

// TestSyncCompletedPercent 验证百分比格式化为 "N.N%" 形态。
func TestSyncCompletedPercent(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"none", ""},
		{"0 / 104790016", "0.0%"},
		{"52395008 / 104790016", "50.0%"},
		{"", ""},
	}
	for _, c := range cases {
		if got := syncCompletedPercent(c.raw); got != c.want {
			t.Errorf("syncCompletedPercent(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

// TestRaidHealthFromCounts 覆盖 normal/degraded/failed 三分支及语义陷阱：
// 降级时 array_state 仍是 clean 也必须判 degraded，不能信 array_state。
func TestRaidHealthFromCounts(t *testing.T) {
	cases := []struct {
		name                        string
		arrayState                  string
		degraded, raidDisks, failed int
		want                        string
	}{
		{"正常", "clean", 0, 2, 0, "normal"},
		{"降级但array_state仍是clean", "clean", 1, 2, 0, "degraded"},
		{"有FailedDisks但degraded计数为0", "clean", 0, 2, 1, "degraded"},
		{"array_state显式inactive", "inactive", 0, 2, 0, "failed"},
		{"全部成员失效degraded等于raid_disks", "clean", 2, 2, 2, "failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := raidHealthFromCounts(c.arrayState, c.degraded, c.raidDisks, c.failed)
			if got != c.want {
				t.Errorf("raidHealthFromCounts(%q,%d,%d,%d) = %q, want %q",
					c.arrayState, c.degraded, c.raidDisks, c.failed, got, c.want)
			}
		})
	}
}

// TestEnumerateSysfsMDNames_NoSysMdInTestEnv 测试沙箱环境没有真实
// /sys/block/md*，glob 应返回空切片，不 panic。
func TestEnumerateSysfsMDNames_NoSysMdInTestEnv(t *testing.T) {
	got := enumerateSysfsMDNames()
	if len(got) != 0 {
		t.Logf("沙箱环境意外发现 md 设备: %v（如果是真机运行此测试这是正常的）", got)
	}
}

// TestReadRAIDSysfsValues_DirMissing 验证目录不存在时 ok=false，调用方应
// 回退 mdstat 路径。
func TestReadRAIDSysfsValues_DirMissing(t *testing.T) {
	_, _, ok := readRAIDSysfsValues("md_test_nonexistent_9999")
	if ok {
		t.Error("不存在的 md 设备应返回 ok=false")
	}
}

// TestMdstatProgressKindToActivity 验证 recovery→recover 映射，其余 kind 原样透传。
func TestMdstatProgressKindToActivity(t *testing.T) {
	cases := []struct{ kind, want string }{
		{"recovery", "recover"},
		{"resync", "resync"},
		{"reshape", "reshape"},
		{"check", "check"},
		{"repair", "repair"},
	}
	for _, c := range cases {
		if got := mdstatProgressKindToActivity(c.kind); got != c.want {
			t.Errorf("mdstatProgressKindToActivity(%q) = %q, want %q", c.kind, got, c.want)
		}
	}
}

// TestSysfsCleanStreakResult 验证 waitForResync 轮询完成判定的纯函数核心：
// 连续 N 轮 idle 才算完成，中途观察到非 idle 则 streak 清零重新累计。
func TestSysfsCleanStreakResult(t *testing.T) {
	cases := []struct {
		name           string
		actions        []string
		requiredStreak int
		wantDone       bool
		wantAtIndex    int
	}{
		{"全部idle_第3轮达成", []string{"idle", "idle", "idle"}, 3, true, 2},
		{"全部idle但轮数不足", []string{"idle", "idle"}, 3, false, -1},
		{"中途出现recover_streak清零重新累计", []string{"idle", "idle", "recover", "idle", "idle", "idle"}, 3, true, 5},
		{"全程recover_从不完成", []string{"recover", "recover", "recover"}, 3, false, -1},
		{"空序列", []string{}, 3, false, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			done, idx := sysfsCleanStreakResult(c.actions, c.requiredStreak)
			if done != c.wantDone || idx != c.wantAtIndex {
				t.Errorf("sysfsCleanStreakResult(%v, %d) = (%v, %d), want (%v, %d)",
					c.actions, c.requiredStreak, done, idx, c.wantDone, c.wantAtIndex)
			}
		})
	}
}

// TestReshapeETAMinutes 覆盖正常估算与 speed=0/total<=done 的置空分支。
func TestReshapeETAMinutes(t *testing.T) {
	cases := []struct {
		name        string
		done, total int64
		speed       float64
		wantETA     string
		wantOK      bool
	}{
		{"正常估算", 0, 104790016, 200213, "8.7", true},
		{"speed为0", 0, 104790016, 0, "", false},
		{"speed为负", 0, 104790016, -1, "", false},
		{"total等于done_已完成", 104790016, 104790016, 200213, "", false},
		{"total小于done_异常数据", 100, 50, 200213, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eta, ok := reshapeETAMinutes(c.done, c.total, c.speed)
			if ok != c.wantOK || eta != c.wantETA {
				t.Errorf("reshapeETAMinutes(%d,%d,%v) = (%q,%v), want (%q,%v)",
					c.done, c.total, c.speed, eta, ok, c.wantETA, c.wantOK)
			}
		})
	}
}

// TestParseSyncSpeed 覆盖正常/none/畸形。
func TestParseSyncSpeed(t *testing.T) {
	cases := []struct {
		raw    string
		want   float64
		wantOK bool
	}{
		{"200213", 200213, true},
		{"none", 0, false},
		{"", 0, false},
		{"abc", 0, false},
	}
	for _, c := range cases {
		got, ok := parseSyncSpeed(c.raw)
		if ok != c.wantOK || got != c.want {
			t.Errorf("parseSyncSpeed(%q) = (%v,%v), want (%v,%v)", c.raw, got, ok, c.want, c.wantOK)
		}
	}
}
