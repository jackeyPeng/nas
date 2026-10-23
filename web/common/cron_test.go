package common

import (
	"testing"
	"time"
)

// TestCronMatch 迁移等价测试（F2 Task 2）：覆盖 rclone 既有
// scheduler_test.go 的全部用例 + 计划要求的 */15、1-5 两档，确保
// modules/rclone/scheduler.go 改调 common.CronMatch 后行为逐字不变。
func TestCronMatch(t *testing.T) {
	cases := []struct {
		expr string
		tm   time.Time
		want bool
	}{
		// 每天 02:00
		{"0 2 * * *", time.Date(2026, 9, 11, 2, 0, 0, 0, time.Local), true},
		{"0 2 * * *", time.Date(2026, 9, 11, 2, 30, 0, 0, time.Local), false},
		{"0 2 * * *", time.Date(2026, 9, 11, 3, 0, 0, 0, time.Local), false},
		// 每 5 分钟
		{"*/5 * * * *", time.Date(2026, 9, 11, 13, 5, 0, 0, time.Local), true},
		{"*/5 * * * *", time.Date(2026, 9, 11, 13, 7, 0, 0, time.Local), false},
		// 每 15 分钟（计划要求的 */15 用例）
		{"*/15 * * * *", time.Date(2026, 9, 11, 13, 30, 0, 0, time.Local), true},
		{"*/15 * * * *", time.Date(2026, 9, 11, 13, 20, 0, 0, time.Local), false},
		// 每周日 00:00（2026-09-06 是周日，09-07 是周一）
		{"0 0 * * 0", time.Date(2026, 9, 6, 0, 0, 0, 0, time.Local), true},
		{"0 0 * * 0", time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local), false},
		// 每天 01:00-03:00 的第 30 分
		{"30 1-3 * * *", time.Date(2026, 9, 11, 2, 30, 0, 0, time.Local), true},
		{"30 1-3 * * *", time.Date(2026, 9, 11, 4, 30, 0, 0, time.Local), false},
		// 每月 15 日 02:00
		{"0 2 15 * *", time.Date(2026, 9, 15, 2, 0, 0, 0, time.Local), true},
		{"0 2 15 * *", time.Date(2026, 9, 16, 2, 0, 0, 0, time.Local), false},
		// 9 月每天 02:00
		{"0 2 * 9 *", time.Date(2026, 9, 11, 2, 0, 0, 0, time.Local), true},
		{"0 2 * 10 *", time.Date(2026, 9, 11, 2, 0, 0, 0, time.Local), false},
		// 逗号列表：每周一、三、五 08:30
		{"30 8 * * 1,3,5", time.Date(2026, 9, 9, 8, 30, 0, 0, time.Local), true},  // 周三
		{"30 8 * * 1,3,5", time.Date(2026, 9, 8, 8, 30, 0, 0, time.Local), false}, // 周二
		// 逗号列表（计划要求的 1,5 用例）：分钟字段 1,5
		{"1,5 * * * *", time.Date(2026, 9, 11, 2, 1, 0, 0, time.Local), true},
		{"1,5 * * * *", time.Date(2026, 9, 11, 2, 5, 0, 0, time.Local), true},
		{"1,5 * * * *", time.Date(2026, 9, 11, 2, 3, 0, 0, time.Local), false},
		// 范围（计划要求的 1-5 用例）：分钟字段 1-5
		{"1-5 * * * *", time.Date(2026, 9, 11, 2, 3, 0, 0, time.Local), true},
		{"1-5 * * * *", time.Date(2026, 9, 11, 2, 6, 0, 0, time.Local), false},
		// 水位 cron：0 */6 * * *（Task 3 要用）
		{"0 */6 * * *", time.Date(2026, 9, 11, 6, 0, 0, 0, time.Local), true},
		{"0 */6 * * *", time.Date(2026, 9, 11, 7, 0, 0, 0, time.Local), false},
		// 周报 cron：0 9 * * 0（周日 09:00）
		{"0 9 * * 0", time.Date(2026, 9, 6, 9, 0, 0, 0, time.Local), true},
		{"0 9 * * 0", time.Date(2026, 9, 7, 9, 0, 0, 0, time.Local), false},
		// 非法表达式
		{"", time.Date(2026, 9, 11, 2, 0, 0, 0, time.Local), false},
		{"not a cron", time.Date(2026, 9, 11, 2, 0, 0, 0, time.Local), false},
		{"0 2 * *", time.Date(2026, 9, 11, 2, 0, 0, 0, time.Local), false},
	}
	for _, c := range cases {
		got := CronMatch(c.expr, c.tm)
		if got != c.want {
			t.Errorf("CronMatch(%q, %s) = %v, want %v",
				c.expr, c.tm.Format("2006-01-02 15:04:05"), got, c.want)
		}
	}
}
