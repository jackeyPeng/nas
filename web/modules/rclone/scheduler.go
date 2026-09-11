package rclone

import (
	"strconv"
	"strings"
	"sync"
	"time"
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

// fieldRanges：各字段取值范围，顺序 分 时 日 月 周（0=周日）。
var fieldRanges = [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}

// cronMatch 判断时间是否命中 5 段 cron 表达式。
// 支持 *、逗号列表、范围(a-b)、步进(*/n 或 a-b/n)。
func cronMatch(expr string, t time.Time) bool {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return false
	}
	vals := []int{t.Minute(), t.Hour(), t.Day(), int(t.Month()), int(t.Weekday())}
	for i := 0; i < 5; i++ {
		if !cronFieldMatch(fields[i], vals[i], fieldRanges[i][0], fieldRanges[i][1]) {
			return false
		}
	}
	return true
}

func cronFieldMatch(field string, val, loDefault, hiDefault int) bool {
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		base, step := part, 1
		if i := strings.Index(part, "/"); i >= 0 {
			base = part[:i]
			if s, err := strconv.Atoi(part[i+1:]); err == nil && s > 0 {
				step = s
			}
		}
		lo, hi := loDefault, hiDefault
		if base != "*" {
			if i := strings.Index(base, "-"); i >= 0 {
				a, e1 := strconv.Atoi(base[:i])
				b, e2 := strconv.Atoi(base[i+1:])
				if e1 != nil || e2 != nil {
					continue
				}
				lo, hi = a, b
			} else if n, err := strconv.Atoi(base); err == nil {
				lo, hi = n, n
			} else {
				continue
			}
		}
		if val >= lo && val <= hi && (val-lo)%step == 0 {
			return true
		}
	}
	return false
}
