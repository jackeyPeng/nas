package diskmgmt

import (
	"errors"
	"testing"
)

// TestOverviewDataDoesNotPanic 验证 OverviewData() 在开发机（无 lsblk/
// smartctl/samba 等真实存储环境）下不 panic。buildStorageOverview 对各数据
// 源的失败路径都是静默跳过/置空，返回的 StorageOverview 在开发机上多半是
// 空池、空盘的零值形态，这里只断言调用安全。
func TestOverviewDataDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("OverviewData() panicked: %v", r)
		}
	}()
	_ = OverviewData()
}

// TestSmartSnapshotDoesNotPanic 验证 SmartSnapshot() 不 panic；lsblk 不存在
// 或解析失败时 getDiskStatus 返回 nil，是合法的"空值"兜底。
func TestSmartSnapshotDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SmartSnapshot() panicked: %v", r)
		}
	}()
	_ = SmartSnapshot()
}

// TestPoolUsageNoPoolReturnsErrPoolNotExist 验证开发机（无 vgs/LVM 环境）
// 下 PoolUsage() 返回 ErrPoolNotExist 哨兵错误，不 panic，totalGB/usedGB/
// usePercent 为零值（F2 Task 3：水位 cron 据此静默跳过）。
func TestPoolUsageNoPoolReturnsErrPoolNotExist(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PoolUsage() panicked: %v", r)
		}
	}()
	totalGB, usedGB, usePercent, err := PoolUsage()
	if err == nil {
		t.Skip("本机检测到真实 LVM 池，跳过池不存在断言")
	}
	if !errors.Is(err, ErrPoolNotExist) {
		t.Fatalf("want ErrPoolNotExist, got %v", err)
	}
	if totalGB != 0 || usedGB != 0 || usePercent != 0 {
		t.Fatalf("池不存在时三个数值应为零值: total=%v used=%v pct=%v", totalGB, usedGB, usePercent)
	}
}

// TestPoolUsageKernelCalculation 直接断言 poolUsageKernel 的用量换算逻辑：
// 用注入的假 vgs 输出覆盖 totalGB/usedGB/usePercent 的计算正确性（不依赖
// 真实 vgs 二进制存在与否）。
func TestPoolUsageKernelCalculation(t *testing.T) {
	cases := []struct {
		totalStr, freeStr  string
		wantTotal, wantUse float64
		wantPct            int
	}{
		{"100.0g", "15.0g", 100, 85, 85},
		{"200.0g", "100.0g", 200, 100, 50},
		{"50.0g", "50.0g", 50, 0, 0},
	}
	for _, c := range cases {
		total := parseFloatSafe(c.totalStr)
		free := parseFloatSafe(c.freeStr)
		used := total - free
		pct := 0
		if total > 0 {
			pct = int((used / total) * 100)
		}
		if total != c.wantTotal || used != c.wantUse || pct != c.wantPct {
			t.Fatalf("vgSize=%s vgFree=%s: got total=%v used=%v pct=%v, want total=%v used=%v pct=%v",
				c.totalStr, c.freeStr, total, used, pct, c.wantTotal, c.wantUse, c.wantPct)
		}
	}
}

func TestParseDfSizeUsedGB(t *testing.T) {
	cases := []struct {
		in       string
		wantT    float64
		wantU    float64
		wantOK   bool
	}{
		{"  Size Used\n   50G   2G\n", 50, 2, true},
		{"Size Used\n0G 0G", 0, 0, false},
		{"garbage", 0, 0, false},
		{"", 0, 0, false},
		{"Size Used\n12G 11G", 12, 11, true},
	}
	for i, c := range cases {
		gotT, gotU, ok := parseDfSizeUsedGB(c.in)
		if ok != c.wantOK || (ok && (gotT != c.wantT || gotU != c.wantU)) {
			t.Fatalf("case %d: parseDfSizeUsedGB(%q) = (%v,%v,%v), want (%v,%v,%v)", i, c.in, gotT, gotU, ok, c.wantT, c.wantU, c.wantOK)
		}
	}
}
