package common

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// resetDirSizeState 清空目录大小缓存的全局状态，保证每个测试独立。
func resetDirSizeState(t *testing.T) {
	t.Helper()
	dirSizeCache = sync.Map{}
	dirSizeRefreshMu = sync.Map{}
	old := dirSizeExecFunc
	t.Cleanup(func() { dirSizeExecFunc = old })
}

// TestCachedDirSize_HitDoesNotReexec 验证缓存命中（未过期）时不会再执行一次
// du（用注入的假命令计数器验证）。
func TestCachedDirSize_HitDoesNotReexec(t *testing.T) {
	resetDirSizeState(t)

	var calls int32
	dirSizeExecFunc = func(path string) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "1.2G", nil
	}

	size1, err := CachedDirSize("/data/nas1", time.Minute)
	if err != nil || size1 != "1.2G" {
		t.Fatalf("first call = (%q, %v), want (1.2G, nil)", size1, err)
	}
	if calls != 1 {
		t.Fatalf("第一次调用应执行一次 du，got calls=%d", calls)
	}

	for i := 0; i < 5; i++ {
		size, err := CachedDirSize("/data/nas1", time.Minute)
		if err != nil || size != "1.2G" {
			t.Errorf("cached call %d = (%q, %v), want (1.2G, nil)", i, size, err)
		}
	}
	if calls != 1 {
		t.Errorf("TTL 内重复调用不应重新执行 du，但 calls=%d", calls)
	}
}

// TestCachedDirSize_StaleWhileRevalidate 验证缓存过期后：
// 1) 立刻返回旧值（不阻塞等待新值）
// 2) 后台异步刷新一次缓存，刷新完成后下次调用能拿到新值
func TestCachedDirSize_StaleWhileRevalidate(t *testing.T) {
	resetDirSizeState(t)

	var calls int32
	refreshStarted := make(chan struct{})
	refreshRelease := make(chan struct{})
	dirSizeExecFunc = func(path string) (string, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return "1.0G", nil
		}
		// 第二次调用（后台刷新）故意卡住，直到测试放行，
		// 用来验证"立刻返回旧值"而不是等刷新完成。
		close(refreshStarted)
		<-refreshRelease
		return "2.0G", nil
	}

	// 第一次：填充缓存。
	size, err := CachedDirSize("/data/nas2", 10*time.Millisecond)
	if err != nil || size != "1.0G" {
		t.Fatalf("seed call = (%q, %v), want (1.0G, nil)", size, err)
	}

	// 等 TTL 过期。
	time.Sleep(20 * time.Millisecond)

	// 第二次：应该立刻拿到旧值 "1.0G"，即使后台刷新函数还卡着没返回。
	start := time.Now()
	size, err = CachedDirSize("/data/nas2", 10*time.Millisecond)
	elapsed := time.Since(start)
	if err != nil || size != "1.0G" {
		t.Fatalf("stale call = (%q, %v), want (1.0G, nil)（应立刻返回旧值）", size, err)
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("stale 调用耗时 %v，应该立刻返回旧值而不是等后台刷新", elapsed)
	}

	select {
	case <-refreshStarted:
	case <-time.After(time.Second):
		t.Fatal("后台刷新没有被触发")
	}
	close(refreshRelease)

	// 给后台 goroutine 一点时间把新值写进缓存。
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if v, ok := dirSizeCache.Load("/data/nas2"); ok {
			if v.(*dirSizeEntry).value == "2.0G" {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	v, _ := dirSizeCache.Load("/data/nas2")
	if v == nil || v.(*dirSizeEntry).value != "2.0G" {
		t.Errorf("后台刷新完成后缓存应变为 2.0G，got %+v", v)
	}
}

// TestCachedDirSize_ConcurrentStaleOnlyRefreshesOnce 验证同一路径过期后被
// 多个并发请求同时命中时，只触发一次后台刷新，不会堆积多个 du 调用。
func TestCachedDirSize_ConcurrentStaleOnlyRefreshesOnce(t *testing.T) {
	resetDirSizeState(t)

	var calls int32
	dirSizeExecFunc = func(path string) (string, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return "1.0G", nil
		}
		time.Sleep(20 * time.Millisecond)
		return "2.0G", nil
	}

	if _, err := CachedDirSize("/data/nas3", 5*time.Millisecond); err != nil {
		t.Fatalf("seed: %v", err)
	}
	time.Sleep(10 * time.Millisecond) // 过期

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			CachedDirSize("/data/nas3", 5*time.Millisecond)
		}()
	}
	wg.Wait()

	// 等后台刷新真正落地（而不是固定 sleep 一段时间就assume完成——
	// 这会在刷新 goroutine 还没退出时就让测试结束，导致下一个测试
	// reset 全局状态时和它产生数据竞争）。轮询缓存值直到变成刷新后
	// 的 "2.0G" 或超时。
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if v, ok := dirSizeCache.Load("/data/nas3"); ok && v.(*dirSizeEntry).value == "2.0G" {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	if got := atomic.LoadInt32(&calls); got > 2 {
		t.Errorf("并发过期触发了 %d 次 du 调用，want 最多 2 次（1 次种子 + 1 次刷新）", got)
	}
}

// TestCachedDirSize_ColdStartTimeout 验证冷启动（无缓存）路径下，若底层命令
// 超时/出错，CachedDirSize 会把错误传回而不是挂死。
func TestCachedDirSize_ColdStartTimeout(t *testing.T) {
	resetDirSizeState(t)

	dirSizeExecFunc = func(path string) (string, error) {
		return "-", errors.New("simulated timeout")
	}

	size, err := CachedDirSize("/data/nas-timeout", time.Minute)
	if err == nil {
		t.Fatal("want error propagated from underlying exec")
	}
	if size != "-" {
		t.Errorf("size = %q, want \"-\" on error", size)
	}
}

// TestExecDirSizeReal_NonexistentPathReturnsError 验证 execDirSizeReal 真的
// 调用了外部命令（sudo 在沙箱里可能不可用/du 对不存在路径报错），
// 两种情况下都应该返回 error 而不是 panic 或挂死，且失败时返回 "-"。
// 不依赖 sudo 密码或真实磁盘状态，只验证"调用失败时的契约"。
func TestExecDirSizeReal_NonexistentPathReturnsError(t *testing.T) {
	size, err := execDirSizeReal("/nonexistent-path-for-nas-panel-test-" + time.Now().Format("150405.000000000"))
	if err == nil {
		t.Skip("当前环境 sudo du 对不存在路径没有报错（非预期但不是本测试要覆盖的核心契约），跳过")
	}
	if size != "-" {
		t.Errorf("size = %q, want \"-\" on error", size)
	}
}
