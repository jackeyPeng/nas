package diskmgmt

import "testing"

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
