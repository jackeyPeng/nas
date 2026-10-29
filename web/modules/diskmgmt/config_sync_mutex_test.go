package diskmgmt

import (
	"sync"
	"testing"
	"time"
)

// TestSyncAllMu_ConcurrentSmoke 验证 syncAllMu 本身在并发 Lock/Unlock 下不死锁、
// 不 panic（§八 #7 互斥修复的冒烟测试）。不直接调用 SyncAllConfigs()：
// 它内部会 shell 出 sudo/systemctl 命令，在测试/CI 环境里不可控（任务书禁止
// 真实执行 sudo/systemctl），这里只验证锁本身的并发语义。
func TestSyncAllMu_ConcurrentSmoke(t *testing.T) {
	const n = 20
	var wg sync.WaitGroup
	var counter int
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			syncAllMu.Lock()
			defer syncAllMu.Unlock()
			counter++
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		if counter != n {
			t.Errorf("counter = %d, want %d（临界区未被正确串行化）", counter, n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("并发 Lock/Unlock 超时，疑似死锁")
	}
}
