package common

// CronMatch 判断时间是否命中 5 段 cron 表达式（分 时 日 月 周）。
// 支持 *、逗号列表、范围(a-b)、步进(*/n 或 a-b/n)。
//
// 从 modules/rclone/scheduler.go 机械迁移（F2 Task 2）：语义逐字不变，
// 供 rclone 同步调度与 triggers 水位/周报 cron 共享。
import (
	"strconv"
	"strings"
	"time"
)

// cronFieldRanges：各字段取值范围，顺序 分 时 日 月 周（0=周日）。
var cronFieldRanges = [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}

// CronMatch 判断时间是否命中 5 段 cron 表达式。
func CronMatch(expr string, t time.Time) bool {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return false
	}
	vals := []int{t.Minute(), t.Hour(), t.Day(), int(t.Month()), int(t.Weekday())}
	for i := 0; i < 5; i++ {
		if !cronFieldMatch(fields[i], vals[i], cronFieldRanges[i][0], cronFieldRanges[i][1]) {
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
