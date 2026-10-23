package triggers

import (
	"sync"
	"time"

	"nas-panel/common"
)

// loopsStop 用于单测停止后台循环（StartLoops 的测试专用镜像，对齐
// tasks.StopWorkerForTest / notifications.StopSubscriberForTest 模式）。
var loopsStop chan struct{}

// loopsWG 配合 loopsStop：StopLoopsForTest 等两个后台 goroutine 真正退出
// 后才返回，避免单测里"关一个循环、起下一个测试"时跟尚未退出的旧
// goroutine 产生数据竞争（如 guardAllows 读跟下个用例的 SetEnabledGuard
// 写撞上）。
var loopsWG sync.WaitGroup

// loopTickInterval 是水位/周报 cron 的扫描间隔（照 rclone scheduler 的
// 30s ticker 模式）。
const loopTickInterval = 30 * time.Second

// StartLoops 启动 triggers 后台常驻循环：eventbus 订阅 + cron ticker。
// Task 2 先建空壳（订阅/扫描动作本身无副作用），Task 3 在 rules.go 挂真实
// 规则，Task 4 挂周报 cron。goroutine 常驻但每次动作前查 guard（关断时
// 不产生任何建议/通知/周报，零足迹语义的一部分）。幂等：重复调用不会
// 启动第二个循环。
func StartLoops() {
	if loopsStop != nil {
		return
	}
	stop := make(chan struct{})
	loopsStop = stop
	loopsWG.Add(2)
	go func() { defer loopsWG.Done(); cronLoop(stop) }()
	ch := common.Subscribe()
	go func() { defer loopsWG.Done(); eventLoop(ch, stop) }()
}

// StopLoopsForTest 停止后台循环、等待两个 goroutine 真正退出后再清空
// 单例状态，仅供单测调用。生产路径（main.go）从不调用它。
func StopLoopsForTest() {
	if loopsStop == nil {
		return
	}
	close(loopsStop)
	loopsWG.Wait()
	loopsStop = nil
}

func cronLoop(stop chan struct{}) {
	ticker := time.NewTicker(loopTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			runCronChecks(time.Now())
		}
	}
}

// runCronChecks 在每次 tick 时执行 cron 驱动的规则检查。Task 2 阶段无
// 规则注册，是空操作；Task 3（水位）/Task 4（周报）往这里挂钩子。
func runCronChecks(now time.Time) {
	if !guardAllows() {
		return
	}
	runWatermarkCronIfDue(now)
	// 锚点：Task 4 在此处挂周报生成（report.generated，cron "0 9 * * 0"）。
}
