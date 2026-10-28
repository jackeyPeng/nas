package diskmgmt

import (
	"reflect"
	"testing"
)

// Fix 2：选盘参数（disks）与空闲盘集合求交集。交集为空时调用方必须报错中止，
// 不能静默回退到全部空闲盘（那会把操作范围从"用户选中的盘"扩大成"全部空闲盘"）。
func TestFilterSelectedDisks(t *testing.T) {
	unused := []string{"/dev/sdb", "/dev/sdc", "/dev/sdd"}

	t.Run("正常交集", func(t *testing.T) {
		filtered, hasSel := filterSelectedDisks(unused, "/dev/sdb,/dev/sdc")
		if !hasSel {
			t.Fatalf("hasSel = false, want true")
		}
		want := []string{"/dev/sdb", "/dev/sdc"}
		if !reflect.DeepEqual(filtered, want) {
			t.Errorf("filtered = %v, want %v", filtered, want)
		}
	})

	t.Run("交集为空_选盘已不在空闲盘里", func(t *testing.T) {
		filtered, hasSel := filterSelectedDisks(unused, "/dev/sdz")
		if !hasSel {
			t.Fatalf("hasSel = false, want true")
		}
		if len(filtered) != 0 {
			t.Errorf("filtered = %v, want empty", filtered)
		}
	})

	t.Run("sel为空串_未传参数_行为不变", func(t *testing.T) {
		filtered, hasSel := filterSelectedDisks(unused, "")
		if hasSel {
			t.Fatalf("hasSel = true, want false")
		}
		if filtered != nil {
			t.Errorf("filtered = %v, want nil", filtered)
		}
	})

	t.Run("sel含空白项", func(t *testing.T) {
		filtered, hasSel := filterSelectedDisks(unused, " /dev/sdb , , /dev/sdd ")
		if !hasSel {
			t.Fatalf("hasSel = false, want true")
		}
		want := []string{"/dev/sdb", "/dev/sdd"}
		if !reflect.DeepEqual(filtered, want) {
			t.Errorf("filtered = %v, want %v", filtered, want)
		}
	})

	t.Run("sel全是空白_视为未传", func(t *testing.T) {
		filtered, hasSel := filterSelectedDisks(unused, "   ")
		if hasSel {
			t.Fatalf("hasSel = true, want false")
		}
		if filtered != nil {
			t.Errorf("filtered = %v, want nil", filtered)
		}
	})
}

// TestValidateModeRejectsSeparate 验证 B2：separate 多池创建模式已下线后，
// validateMode 对 mode="separate" 应落到 default 分支，视为未知模式拒绝，
// 而不是继续沿用旧的"至少需要 2 块磁盘"校验规则。
func TestValidateModeRejectsSeparate(t *testing.T) {
	got := validateMode("separate", 4)
	if got == "" {
		t.Fatal("validateMode(separate, 4) = \"\" (通过)，want 非空错误（separate 已下线）")
	}
	if got != "未知模式: separate" {
		t.Errorf("validateMode(separate, 4) = %q, want %q", got, "未知模式: separate")
	}
}

// TestGetModeStepCountRejectsSeparate 验证 B2：getModeStepCount 对
// mode="separate" 应返回 0（未知模式），不再按 5*diskCount 计算步数。
func TestGetModeStepCountRejectsSeparate(t *testing.T) {
	if got := getModeStepCount("separate", 3); got != 0 {
		t.Errorf("getModeStepCount(separate, 3) = %d, want 0", got)
	}
}

// TestValidateModeStillAcceptsKnownModes 确认下线 separate 没有误伤其他模式的校验。
func TestValidateModeStillAcceptsKnownModes(t *testing.T) {
	cases := []struct {
		mode      string
		diskCount int
	}{
		{"single", 1},
		{"merge", 2},
		{"raid0", 2},
		{"raid1", 2},
		{"raid5", 3},
		{"raid6", 4},
	}
	for _, c := range cases {
		if got := validateMode(c.mode, c.diskCount); got != "" {
			t.Errorf("validateMode(%q, %d) = %q, want \"\" (应通过)", c.mode, c.diskCount, got)
		}
	}
}
