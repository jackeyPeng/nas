package common

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEventBusEmitAndQuery(t *testing.T) {
	dir := t.TempDir()
	InitEventBusForTest(dir)
	defer CloseEventBusForTest()

	EmitEvent("health", EventWarn, "health.disk_hot", map[string]interface{}{"disk": "sda"}, "温度 55C")
	EmitEvent("diskmgmt", EventError, "disk.offline", nil, "sdb 掉盘")
	EmitEvent("update", EventSuccess, "update.applied", map[string]interface{}{"version": "v1.4.0-beta.9"}, "")
	DrainEventsForTest()

	evs, total, err := QueryEvents("", "", 0, 50, 0)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if total != 3 || len(evs) != 3 {
		t.Fatalf("want 3 events, got total=%d len=%d", total, len(evs))
	}
	// 最新在前
	if evs[0].Source != "update" {
		t.Errorf("want newest first (update), got %s", evs[0].Source)
	}
	if evs[0].Params == "" || evs[0].Params == "{}" {
		t.Errorf("update event params lost: %q", evs[0].Params)
	}

	// source 过滤
	evs, total, err = QueryEvents("health", "", 0, 50, 0)
	if err != nil || total != 1 || evs[0].Code != "health.disk_hot" {
		t.Fatalf("source filter: %v total=%d", err, total)
	}
	// type 过滤
	_, total, err = QueryEvents("", EventError, 0, 50, 0)
	if err != nil || total != 1 {
		t.Fatalf("type filter: %v total=%d", err, total)
	}
	// 分页
	evs, total, err = QueryEvents("", "", 0, 1, 1)
	if err != nil || total != 3 || len(evs) != 1 {
		t.Fatalf("paging: %v total=%d len=%d", err, total, len(evs))
	}
}

func TestEventBusBadTypeNormalized(t *testing.T) {
	dir := t.TempDir()
	InitEventBusForTest(dir)
	defer CloseEventBusForTest()

	EmitEvent("test", "banana", "x.y", nil, "")
	DrainEventsForTest()
	evs, _, err := QueryEvents("", "", 0, 10, 0)
	if err != nil || len(evs) != 1 || evs[0].Type != EventInfo {
		t.Fatalf("bad type not normalized to info: %v %+v", err, evs)
	}
}

func TestEventBusStats(t *testing.T) {
	dir := t.TempDir()
	InitEventBusForTest(dir)
	defer CloseEventBusForTest()

	EmitEvent("a", EventInfo, "a.b", nil, "")
	EmitEvent("a", EventWarn, "a.c", nil, "")
	EmitEvent("a", EventWarn, "a.d", nil, "")
	EmitEvent("a", EventError, "a.e", nil, "")
	DrainEventsForTest()

	st, err := QueryEventStats(7)
	if err != nil {
		t.Fatalf("QueryEventStats: %v", err)
	}
	if st.Info != 1 || st.Warn != 2 || st.Error != 1 || st.Total != 4 {
		t.Fatalf("stats wrong: %+v", st)
	}
}

func TestEventBusSubscribe(t *testing.T) {
	dir := t.TempDir()
	InitEventBusForTest(dir)
	defer CloseEventBusForTest()

	ch := Subscribe()
	defer Unsubscribe(ch)

	// flaky 根治：等 goroutine 的 Emit+Drain 完整结束再返回，
	// 否则 Drain 的库写入会与 t.TempDir 清理竞态（directory not empty）
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(10 * time.Millisecond)
		EmitEvent("sub", EventInfo, "sub.test", nil, "")
		DrainEventsForTest()
	}()
	select {
	case ev := <-ch:
		if ev.Code != "sub.test" {
			t.Fatalf("got wrong event: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber did not receive event")
	}
	<-done
}

func TestEventBusEmitBeforeInitIsNoop(t *testing.T) {
	// 未 Init 时 Emit 不应 panic
	CloseEventBusForTest()
	EmitEvent("x", EventInfo, "x.y", nil, "")
}

// TestEventBusConcurrentStress 并发压测：多 goroutine 同时 EmitEvent、
// QueryEvents、Subscribe/Unsubscribe，验证 eventDB/eventChan 包级全局
// 在 -race 下无数据竞争（M2 二阶段事件桥验收暴露的预存在竞争）。
// 用 InitEventBus（真正起 eventWriter/eventCleanupLoop 后台 goroutine），
// 而非 InitEventBusForTest，因为这里要的是异步写路径下的真实并发场景。
func TestEventBusConcurrentStress(t *testing.T) {
	dir := t.TempDir()
	InitEventBus(dir)
	defer CloseEventBusForTest()

	const (
		emitters   = 8
		emitsEach  = 100
		queriers   = 4
		queryLoops = 50
		subFlips   = 20
	)

	var wg sync.WaitGroup

	// 并发 EmitEvent。
	for i := 0; i < emitters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < emitsEach; j++ {
				EmitEvent("stress", EventInfo, "stress.emit",
					map[string]interface{}{"worker": id, "n": j}, "")
			}
		}(i)
	}

	// 并发 QueryEvents / QueryEventStats（只读路径）。
	for i := 0; i < queriers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < queryLoops; j++ {
				if _, _, err := QueryEvents("", "", 0, 20, 0); err != nil {
					t.Errorf("QueryEvents concurrent: %v", err)
					return
				}
				if _, err := QueryEventStats(1); err != nil {
					t.Errorf("QueryEventStats concurrent: %v", err)
					return
				}
			}
		}()
	}

	// 并发 Subscribe/Unsubscribe（反复开关订阅者，和 eventWriter 的广播竞争）。
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < subFlips; j++ {
				ch := Subscribe()
				// 消费几条，避免订阅者 channel 满后一直 select-default 跳过。
				select {
				case <-ch:
				case <-time.After(time.Millisecond):
				}
				Unsubscribe(ch)
			}
		}()
	}

	wg.Wait()

	// writer 是异步的，给它一点时间把 channel 里剩余事件写完再查总数。
	// EmitEvent 的非阻塞语义允许 channel 满时丢事件（见 EmitEvent 文档），
	// 本测试的目的是验证并发访问无 -race 竞争，不是验证零丢弃；
	// 因此只断言总数落在 (0, 上限] 区间、且后续查询不再变化（writer 已清空 channel）。
	const want = emitters * emitsEach
	deadline := time.Now().Add(2 * time.Second)
	var total, prev int
	stable := 0
	for time.Now().Before(deadline) {
		_, total, _ = QueryEvents("stress", "", 0, 1, 0)
		if total == want {
			break
		}
		if total == prev {
			stable++
			if stable >= 5 {
				break // 连续多次读数不再增长，说明 channel 已排空
			}
		} else {
			stable = 0
		}
		prev = total
		time.Sleep(10 * time.Millisecond)
	}
	if total <= 0 || total > want {
		t.Fatalf("persisted events out of range: got %d, want (0, %d]", total, want)
	}
	if total < want {
		t.Logf("persisted %d/%d events, %d dropped by backpressure (expected under 256-buffer burst load)",
			total, want, want-total)
	}
}

// BenchmarkEmitEvent 量化加锁对 EmitEvent 热路径的开销：RWMutex.RLock/RUnlock
// 各一次 + channel 非阻塞发送，预期在无竞争时近似原子操作量级（ns 级）。
func BenchmarkEmitEvent(b *testing.B) {
	dir := b.TempDir()
	InitEventBusForTest(dir)
	defer CloseEventBusForTest()
	params := map[string]interface{}{"disk": "sda"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		EmitEvent("bench", EventInfo, "bench.emit", params, "")
	}
}

// BenchmarkEmitEventParallel 并发场景下的 EmitEvent 开销（多核竞争 RLock）。
func BenchmarkEmitEventParallel(b *testing.B) {
	dir := b.TempDir()
	InitEventBusForTest(dir)
	defer CloseEventBusForTest()
	params := map[string]interface{}{"disk": "sda"}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			EmitEvent("bench", EventInfo, "bench.emit", params, "")
			DrainEventsForTest() // 防止 256 buffer 很快打满导致后续全是 default 分支
		}
	})
}

func TestEventBusQueryParamsJSON(t *testing.T) {
	dir := t.TempDir()
	InitEventBusForTest(dir)
	defer CloseEventBusForTest()

	EmitEvent("rclone", EventError, "rclone.sync_failed", map[string]interface{}{
		"task": "backup-daily", "err": "connection refused", "n": 42,
	}, "")
	DrainEventsForTest()
	evs, _, err := QueryEvents("rclone", "", 0, 10, 0)
	if err != nil || len(evs) != 1 {
		t.Fatalf("query: %v", err)
	}
	for _, want := range []string{`"task":"backup-daily"`, `"err":"connection refused"`, `"n":42`} {
		if !strings.Contains(evs[0].Params, want) {
			t.Errorf("params missing %s: %s", want, evs[0].Params)
		}
	}
}
