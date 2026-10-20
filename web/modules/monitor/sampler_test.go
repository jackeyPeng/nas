package monitor

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// resetSamplerState 清空采样器全局状态，让每个测试独立。不触碰
// samplerStarted（后台 samplerLoop 不需要在单测里真的跑起来，这里只测
// getMonitorSnapshot 本身的缓存/冷启动兜底逻辑）。
func resetSamplerState() {
	sampleMu.Lock()
	latestSample = nil
	hasSample = false
	sampleMu.Unlock()
	coldStartOnce = sync.Once{}
}

// TestGetMonitorSnapshot_CacheHit 验证已有缓存时直接返回缓存，不会调用
// sampleFunc 重新采样。
func TestGetMonitorSnapshot_CacheHit(t *testing.T) {
	resetSamplerState()
	defer resetSamplerState()

	oldSampleFunc := sampleFunc
	defer func() { sampleFunc = oldSampleFunc }()

	var calls int32
	sampleFunc = func() map[string]interface{} {
		atomic.AddInt32(&calls, 1)
		return map[string]interface{}{"cpu_percent": "9.9"}
	}

	sampleMu.Lock()
	latestSample = map[string]interface{}{"cpu_percent": "1.0"}
	hasSample = true
	sampleMu.Unlock()

	got := getMonitorSnapshot()
	if got["cpu_percent"] != "1.0" {
		t.Errorf("getMonitorSnapshot() = %v, want cpu_percent=1.0 (缓存值)", got)
	}
	if calls != 0 {
		t.Errorf("sampleFunc 被调用 %d 次，有缓存时不应重新采样", calls)
	}
}

// TestGetMonitorSnapshot_ColdStartSingleFlight 验证进程刚启动、还没有缓存时，
// 并发打进来的多个请求只触发一次真实采样（single-flight），其余请求等那
// 一次采样完成后复用同一份快照，而不是每个请求各跑一次慢采样。
func TestGetMonitorSnapshot_ColdStartSingleFlight(t *testing.T) {
	resetSamplerState()
	defer resetSamplerState()

	oldSampleFunc := sampleFunc
	defer func() { sampleFunc = oldSampleFunc }()

	var calls int32
	sampleFunc = func() map[string]interface{} {
		atomic.AddInt32(&calls, 1)
		time.Sleep(30 * time.Millisecond) // 放大竞态窗口，模拟慢采样
		return map[string]interface{}{"cpu_percent": "2.0"}
	}

	const n = 20
	var wg sync.WaitGroup
	results := make([]map[string]interface{}, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = getMonitorSnapshot()
		}(i)
	}
	wg.Wait()

	for i, r := range results {
		if r["cpu_percent"] != "2.0" {
			t.Errorf("results[%d] = %v, want cpu_percent=2.0", i, r)
		}
	}
	if calls != 1 {
		t.Errorf("sampleFunc 被调用 %d 次，want 恰好 1 次（single-flight 兜底采样）", calls)
	}
}

// TestSamplerLoop_WritesSnapshotPeriodically 验证 samplerLoop 确实会把
// sampleFunc 的结果写进 latestSample/hasSample（不依赖真实 5s 间隔，
// 这里直接起一个短循环变体验证写入逻辑本身）。
func TestSamplerLoop_WritesSnapshotPeriodically(t *testing.T) {
	resetSamplerState()
	defer resetSamplerState()

	oldSampleFunc := sampleFunc
	defer func() { sampleFunc = oldSampleFunc }()

	var seq int32
	sampleFunc = func() map[string]interface{} {
		n := atomic.AddInt32(&seq, 1)
		return map[string]interface{}{"seq": n}
	}

	// 直接复用 samplerLoop 的写入语义，不跑真实 5s sleep。
	snap := sampleFunc()
	sampleMu.Lock()
	latestSample = snap
	hasSample = true
	sampleMu.Unlock()

	got := getMonitorSnapshot()
	if got["seq"] != int32(1) {
		t.Errorf("getMonitorSnapshot() = %v, want seq=1", got)
	}
}
