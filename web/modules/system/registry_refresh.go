package system

import (
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// registryRefreshDelay 是 ScheduleRegistryRefresh 的去抖窗口，默认 3 秒。
// 测试用例会把它改成更短的值，避免真睡 3 秒。
var registryRefreshDelay = 3 * time.Second

// registryRefreshFn 是实际执行重算的函数，默认指向 RefreshRegistry。
// 测试用 fake 替换它以避免真跑 41 项检查（systemctl 子进程）。
var registryRefreshFn func() RegistryReport = RefreshRegistry

var (
	// registryRefreshMu 保证同一时刻只有一次 refreshNow() 在跑，防止并发重算。
	registryRefreshMu sync.Mutex
	// registryRefreshScheduled 标记当前是否已有一次刷新排队等待触发。
	// 已排程时再次调用 ScheduleRegistryRefresh 直接返回——本次排程的定时器
	// 触发时会看到最新状态，合并即正确。
	registryRefreshScheduled atomic.Bool
)

// ScheduleRegistryRefresh 安排一次延迟注册表重算（尾随去抖）。
// 磁盘/共享配置变更完成后调用；3 秒窗口内的多次调用合并为一次重算，
// 避免「建池 + 建共享夹 + 建用户」连发触发多轮 41 项检查。
// 重算在后台 goroutine 执行，绝不阻塞请求路径。
func ScheduleRegistryRefresh() {
	if !registryRefreshScheduled.CompareAndSwap(false, true) {
		return
	}
	time.AfterFunc(registryRefreshDelay, func() {
		registryRefreshScheduled.Store(false)
		registryRefreshMu.Lock()
		defer registryRefreshMu.Unlock()
		refreshNow()
	})
}

// refreshNow 实际执行一次重算，仅记录日志，绝不 panic、不向调用方返回错误。
func refreshNow() {
	start := time.Now()
	report := registryRefreshFn()
	log.Printf("[REGISTRY] 后台刷新完成，耗时 %v，%s", time.Since(start), report.Summary)
}
