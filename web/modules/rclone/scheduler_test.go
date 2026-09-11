package rclone

import (
	"testing"
	"time"
)

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
		// 非法表达式
		{"", time.Date(2026, 9, 11, 2, 0, 0, 0, time.Local), false},
		{"not a cron", time.Date(2026, 9, 11, 2, 0, 0, 0, time.Local), false},
		{"0 2 * *", time.Date(2026, 9, 11, 2, 0, 0, 0, time.Local), false},
	}
	for _, c := range cases {
		got := cronMatch(c.expr, c.tm)
		if got != c.want {
			t.Errorf("cronMatch(%q, %s) = %v, want %v",
				c.expr, c.tm.Format("2006-01-02 15:04:05"), got, c.want)
		}
	}
}
