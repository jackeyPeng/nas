package diskmgmt

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func resetSmartctlCacheState(t *testing.T) {
	t.Helper()
	smartctlCache = sync.Map{}
	old := smartctlExecFunc
	t.Cleanup(func() { smartctlExecFunc = old })
}

// TestCachedSmartctl_HitDoesNotReexec 验证缓存命中（未过期）不会重新执行
// smartctl（用注入的假命令计数器验证）。
func TestCachedSmartctl_HitDoesNotReexec(t *testing.T) {
	resetSmartctlCacheState(t)

	var calls int32
	smartctlExecFunc = func(args []string) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "PASSED", nil
	}

	for i := 0; i < 5; i++ {
		out, err := cachedSmartctl(time.Minute, "-H", "/dev/sda")
		if err != nil || out != "PASSED" {
			t.Fatalf("call %d = (%q, %v), want (PASSED, nil)", i, out, err)
		}
	}
	if calls != 1 {
		t.Errorf("TTL 内重复调用不应重新执行 smartctl，但 calls=%d", calls)
	}
}

// TestCachedSmartctl_DifferentArgsAreDifferentKeys 验证不同参数（不同设备/
// 不同 flag）各自独立缓存，不会互相串用。
func TestCachedSmartctl_DifferentArgsAreDifferentKeys(t *testing.T) {
	resetSmartctlCacheState(t)

	smartctlExecFunc = func(args []string) (string, error) {
		// 用参数拼接出的内容反映在返回值里，方便断言键区分正确。
		joined := ""
		for _, a := range args {
			joined += a
		}
		return joined, nil
	}

	outA, _ := cachedSmartctl(time.Minute, "-H", "/dev/sda")
	outB, _ := cachedSmartctl(time.Minute, "-H", "/dev/sdb")
	outC, _ := cachedSmartctl(time.Minute, "-A", "/dev/sda")

	if outA == outB || outA == outC || outB == outC {
		t.Errorf("不同参数应命中不同缓存键: outA=%q outB=%q outC=%q", outA, outB, outC)
	}
}

// TestCachedSmartctl_ExpiresAfterTTL 验证超过 TTL 后会重新执行。
func TestCachedSmartctl_ExpiresAfterTTL(t *testing.T) {
	resetSmartctlCacheState(t)

	var calls int32
	smartctlExecFunc = func(args []string) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "PASSED", nil
	}

	cachedSmartctl(5*time.Millisecond, "-H", "/dev/sda")
	time.Sleep(15 * time.Millisecond)
	cachedSmartctl(5*time.Millisecond, "-H", "/dev/sda")

	if calls != 2 {
		t.Errorf("TTL 过期后应重新执行一次，calls=%d, want 2", calls)
	}
}

// TestCachedSmartctl_PropagatesError 验证底层命令出错时错误被正常传回
// （而不是被缓存吞掉或 panic）。
func TestCachedSmartctl_PropagatesError(t *testing.T) {
	resetSmartctlCacheState(t)

	wantErr := errors.New("smartctl exit 1")
	smartctlExecFunc = func(args []string) (string, error) {
		return "", wantErr
	}

	_, err := cachedSmartctl(time.Minute, "-H", "/dev/sdz")
	if err == nil {
		t.Fatal("want error propagated")
	}
}
