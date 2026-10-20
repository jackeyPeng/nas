package monitor

import (
	"sync"
	"time"
)

// ═══ 后台采样器 ═══
//
// getMonitorStatus() 里网络采样 sleep 1s + CPU 采样 sleep 500ms，
// 每次 /api/monitor 请求都硬阻塞 ~1.5s。改成后台 goroutine 定期
// （5s 一次）跑同一套采样逻辑，结果存进一份带锁的快照；handler 只读
// 快照，不再自己跑 sleep。
//
// 字段语义和响应 JSON 形状完全不变（仍是原始数值，前端 formatNetRate
// 等 formatter 继续做二次换算）——这里只是把"谁来采样、多久采一次"
// 从"每个请求各采一次"改成"后台独立节奏采样，请求只读最近一次结果"。

var (
	sampleMu       sync.Mutex
	latestSample   map[string]interface{}
	hasSample      bool
	samplerStarted sync.Once

	// coldStartOnce 保证进程启动后第一批并发请求只触发一次同步兜底采样
	// （而不是每个请求各跑一次 1.5s 的 getMonitorStatus）。后续请求一旦
	// hasSample 变 true 就直接走缓存分支，不再用到这个 Once。
	coldStartOnce sync.Once

	// sampleFunc 是实际执行一次采样的函数，默认指向 getMonitorStatus。
	// 定义成变量是为了单测能替换成快速假实现，不必真的睡 1.5s。
	sampleFunc = getMonitorStatus
)

// startSampler 启动后台采样 goroutine（5s 间隔）。幂等，可多次调用。
func startSampler() {
	samplerStarted.Do(func() {
		go samplerLoop()
	})
}

func samplerLoop() {
	for {
		snap := sampleFunc()
		sampleMu.Lock()
		latestSample = snap
		hasSample = true
		sampleMu.Unlock()
		time.Sleep(5 * time.Second)
	}
}

// getMonitorSnapshot 返回最近一次后台采样结果（<5ms，纯读内存）。
// 进程刚启动、后台采样器还没跑出第一份快照时，同步采一次兜底，
// 保证第一批请求也能拿到有效数值（不是空白/零值占位）。
//
// 冷启动窗口内可能有多个请求同时发现 hasSample==false；用 coldStartOnce
// 保证这些并发请求只有一个真正执行 getMonitorStatus()（耗时 ~1.5s），
// 其余的等它写完缓存后直接读缓存，而不是每个请求各跑一次同步采样。
//
// 注意：不在这里调用 startSampler() —— 采样器由 RegisterRoutes 在
// 进程启动时启动一次，保持"谁启动谁负责"单一职责，也避免这个函数
// 在单测里被直接调用时意外拉起一个跟 sampleFunc 替身测试竞争的真实
// 后台 goroutine。
func getMonitorSnapshot() map[string]interface{} {
	sampleMu.Lock()
	ready := hasSample
	sampleMu.Unlock()

	if !ready {
		coldStartOnce.Do(func() {
			snap := sampleFunc()
			sampleMu.Lock()
			if !hasSample {
				latestSample = snap
				hasSample = true
			}
			sampleMu.Unlock()
		})
	}

	sampleMu.Lock()
	defer sampleMu.Unlock()
	return latestSample
}
