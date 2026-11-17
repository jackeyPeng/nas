package system

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// withTestRefreshDelay 把去抖延迟换成测试用的短窗口，并在用例结束后还原。
func withTestRefreshDelay(t *testing.T, d time.Duration) {
	oldDelay := registryRefreshDelay
	registryRefreshDelay = d
	t.Cleanup(func() { registryRefreshDelay = oldDelay })
}

// withFakeRefreshFn 把 registryRefreshFn 换成 fake，并在用例结束后还原。
func withFakeRefreshFn(t *testing.T, fn func() RegistryReport) {
	old := registryRefreshFn
	registryRefreshFn = fn
	t.Cleanup(func() { registryRefreshFn = old })
}

// TestScheduleRegistryRefreshCoalesces 验证 3s 去抖窗口内连续多次调用合并为一次重算。
func TestScheduleRegistryRefreshCoalesces(t *testing.T) {
	withTestRefreshDelay(t, 50*time.Millisecond)

	var calls atomic.Int32
	done := make(chan struct{})
	withFakeRefreshFn(t, func() RegistryReport {
		n := calls.Add(1)
		if n == 1 {
			// 给后续的排程调用留出时间，证明它们被合并而不是各自触发一次。
		}
		return RegistryReport{Summary: "fake"}
	})

	for i := 0; i < 10; i++ {
		ScheduleRegistryRefresh()
	}

	go func() {
		time.Sleep(200 * time.Millisecond)
		close(done)
	}()
	<-done

	if got := calls.Load(); got != 1 {
		t.Errorf("registryRefreshFn called %d times, want 1 (coalesced)", got)
	}
}

// TestScheduleRegistryRefreshSerializes 验证重叠窗口下，第二次排程的重算会等
// 第一次完成后才跑，不会并发执行。
func TestScheduleRegistryRefreshSerializes(t *testing.T) {
	withTestRefreshDelay(t, 20*time.Millisecond)

	var mu sync.Mutex
	var order []string
	var running atomic.Bool
	var overlapped atomic.Bool

	withFakeRefreshFn(t, func() RegistryReport {
		if !running.CompareAndSwap(false, true) {
			overlapped.Store(true)
		}
		mu.Lock()
		order = append(order, "start")
		mu.Unlock()

		time.Sleep(80 * time.Millisecond)

		mu.Lock()
		order = append(order, "end")
		mu.Unlock()
		running.Store(false)
		return RegistryReport{Summary: "fake"}
	})

	// 第一次排程
	ScheduleRegistryRefresh()
	// 等第一次的定时器触发、refreshNow 开始执行后，再排第二次，
	// 制造与第一次重算重叠的窗口。
	time.Sleep(40 * time.Millisecond)
	ScheduleRegistryRefresh()

	// 等两轮都跑完：第一轮在 t=20ms 触发、跑到 t=100ms；
	// 第二轮在 t=40+20=60ms 触发排程回调，但 Lock() 会阻塞到第一轮 Unlock（~100ms），
	// 然后跑到 ~180ms。留足余量再断言。
	time.Sleep(300 * time.Millisecond)

	if overlapped.Load() {
		t.Error("refreshNow executed concurrently, want serialized")
	}
	mu.Lock()
	got := append([]string{}, order...)
	mu.Unlock()
	want := []string{"start", "end", "start", "end"}
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// TestScheduleRegistryRefreshDoesNotBlockCaller 验证 ScheduleRegistryRefresh
// 本身立即返回，不等待重算执行（哪怕重算本身很慢）。
func TestScheduleRegistryRefreshDoesNotBlockCaller(t *testing.T) {
	withTestRefreshDelay(t, 3*time.Second) // 远大于本测试的超时阈值，确保回调不会在断言前触发

	withFakeRefreshFn(t, func() RegistryReport {
		time.Sleep(200 * time.Millisecond)
		return RegistryReport{Summary: "fake"}
	})

	start := time.Now()
	ScheduleRegistryRefresh()
	elapsed := time.Since(start)

	if elapsed >= 50*time.Millisecond {
		t.Errorf("ScheduleRegistryRefresh took %v, want <50ms (must not block caller)", elapsed)
	}

	// 清理：避免遗留的 AfterFunc 在后续用例运行期间触发并污染全局状态。
	registryRefreshScheduled.Store(false)
}

// TestRefreshRegistrySmoke 冒烟：单测环境调用默认 RefreshRegistry 不 panic。
//
// 本沙箱环境下 /opt/nas/data 由 root 所有，当前用户无写权限，
// common.OpenSQLite 会打开失败，GetRegistry/RefreshRegistry 走
// "注册表数据库不可用" 分支返回空 Report（Total=0）——这是预期的降级路径，
// 不是 panic。既有 system_registry_test.go 并未实际调用 RefreshRegistry/
// GetRegistry（只测 syncRegistryMeta 对内存库的行为），brief 中「该文件已证明
// 此环境可跑」的说法与实际不符，此处按真实环境行为断言「不 panic」，
// 不对 Total 非空做强断言（已在报告中申报偏离）。
func TestRefreshRegistrySmoke(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RefreshRegistry() panicked: %v", r)
		}
	}()
	report := RefreshRegistry()
	if report.Summary == "" {
		t.Errorf("RefreshRegistry() returned report with empty Summary: %+v", report)
	}
}
