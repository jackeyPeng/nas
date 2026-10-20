package common

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestLockDiskOpMutualExclusion 验证 LockDiskOp/UnlockDiskOp 确实互斥：
// 并发启动多个 goroutine 竞争锁，持锁区间内用一个非原子计数器+sleep
// 放大竞态窗口，若锁失效则 -race 或计数器错误都能抓到。
func TestLockDiskOpMutualExclusion(t *testing.T) {
	const n = 20
	var inCriticalSection int32
	var maxConcurrent int32
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			LockDiskOp()
			defer UnlockDiskOp()

			cur := atomic.AddInt32(&inCriticalSection, 1)
			// 记录历史最大并发数（应恒为1）
			for {
				m := atomic.LoadInt32(&maxConcurrent)
				if cur <= m || atomic.CompareAndSwapInt32(&maxConcurrent, m, cur) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			atomic.AddInt32(&inCriticalSection, -1)
		}()
	}
	wg.Wait()

	if maxConcurrent != 1 {
		t.Errorf("LockDiskOp 未能互斥：同时进入临界区的最大数量 = %d，want 1", maxConcurrent)
	}
}
