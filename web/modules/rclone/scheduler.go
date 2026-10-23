package rclone

import (
	"strings"
	"sync"
	"time"

	"nas-panel/common"
)

// StartScheduler 启动定时同步调度器（main.go 启动时调用一次）。
// 每 30 秒扫描一次，执行「已启用 + 设了 cron 计划 + 当前命中 + 未在运行」的同步任务。
func StartScheduler() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			runDueTasks()
		}
	}()
}

// lastScheduledRun 记录任务最近一次触发的分钟桶（"2006-01-02 15:04"），
// 防止 30s 的 tick 在同一分钟内把任务触发两次。
var lastScheduledRun sync.Map // taskID -> string(分钟桶)

func runDueTasks() {
	now := time.Now()
	minuteKey := now.Format("2006-01-02 15:04")
	tasks := loadTasks()
	for _, t := range tasks {
		if !t.Enabled || strings.TrimSpace(t.Schedule) == "" {
			continue
		}
		if !cronMatch(t.Schedule, now) {
			continue
		}
		if v, ok := lastScheduledRun.Load(t.ID); ok && v == minuteKey {
			continue
		}
		if _, running := runningTasks.LoadOrStore(t.ID, true); running {
			continue
		}
		lastScheduledRun.Store(t.ID, minuteKey)
		go executeTask(t)
	}
}

// cronMatch 判断时间是否命中 5 段 cron 表达式（分 时 日 月 周）。
// F2 Task 2 机械迁移：真实实现已抽到 common.CronMatch 供 triggers 共享，
// 这里保留同名薄包装，行为逐字不变（既有调用点与 scheduler_test.go 不改）。
func cronMatch(expr string, t time.Time) bool {
	return common.CronMatch(expr, t)
}
