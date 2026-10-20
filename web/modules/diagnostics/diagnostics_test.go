package diagnostics

import "testing"

func TestParseHHMMToMinutes(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"02:30", 150},
		{"00:00", 0},
		{"23:59", 1439},
		{"06:30", 390},
		{"", -1},
		{"2:30:00", -1},
		{"ab:cd", -1},
		{"24:00", -1},
		{"12:60", -1},
	}
	for _, c := range cases {
		if got := parseHHMMToMinutes(c.in); got != c.want {
			t.Errorf("parseHHMMToMinutes(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestInTimeWindowAt(t *testing.T) {
	// 窗口 02:30-06:30（非跨午夜），用分钟数覆盖窗口内/外/边界
	cases := []struct {
		name       string
		start, end string
		nowMinutes int
		want       bool
	}{
		{"窗口内-中段", "02:30", "06:30", 4*60 + 0, true},        // 04:00
		{"起点边界-闭区间", "02:30", "06:30", 2*60 + 30, true},     // 02:30 本身算在窗口内
		{"终点边界-开区间", "02:30", "06:30", 6*60 + 30, false},    // 06:30 本身不算在窗口内（< end）
		{"终点前1分钟-窗口内", "02:30", "06:30", 6*60 + 29, true},   // 06:29
		{"起点前1分钟-窗口外", "02:30", "06:30", 2*60 + 29, false},  // 02:29
		{"窗口外-更早", "02:30", "06:30", 1*60 + 0, false},       // 01:00
		{"窗口外-更晚", "02:30", "06:30", 20*60 + 0, false},      // 20:00
		{"跨午夜窗口-夜间", "22:00", "06:00", 23*60 + 0, true},     // 23:00 在 22:00-06:00 内
		{"跨午夜窗口-凌晨", "22:00", "06:00", 1*60 + 0, true},      // 01:00 在 22:00-06:00 内
		{"跨午夜窗口-白天不在窗口内", "22:00", "06:00", 12 * 60, false}, // 12:00 不在窗口内
		{"跨午夜窗口-起点边界", "22:00", "06:00", 22 * 60, true},     // 22:00 本身算在窗口内
		{"跨午夜窗口-终点边界", "22:00", "06:00", 6 * 60, false},     // 06:00 本身不算在窗口内
		{"解析失败-保守返回false", "bad", "06:30", 4 * 60, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := inTimeWindowAt(c.start, c.end, c.nowMinutes); got != c.want {
				t.Errorf("inTimeWindowAt(%q, %q, %d) = %v, want %v", c.start, c.end, c.nowMinutes, got, c.want)
			}
		})
	}
}

func TestSmartShortProgress(t *testing.T) {
	cases := []struct {
		name           string
		disksDone      int
		diskIndexPolls int
		totalDisks     int
		pollsPerDisk   int
		want           int
	}{
		{"单盘-开始", 0, 1, 1, 30, 3},
		{"单盘-完成", 0, 30, 1, 30, 100},
		{"双盘-第一块盘中途", 0, 15, 2, 30, 25},
		{"双盘-第一块盘完成等于第二块盘刚开始前", 0, 30, 2, 30, 50},
		{"双盘-第二块盘开始", 1, 1, 2, 30, 51},
		{"双盘-全部完成", 1, 30, 2, 30, 100},
		{"非法参数-总盘数0", 0, 1, 0, 30, 0},
		{"非法参数-轮询数0", 0, 1, 1, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := smartShortProgress(c.disksDone, c.diskIndexPolls, c.totalDisks, c.pollsPerDisk)
			if got != c.want {
				t.Errorf("smartShortProgress(%d,%d,%d,%d) = %d, want %d",
					c.disksDone, c.diskIndexPolls, c.totalDisks, c.pollsPerDisk, got, c.want)
			}
			// 进度不应因为多盘切换而回退到更低值（本轮修的根本问题）
			if got < 0 || got > 100 {
				t.Errorf("progress %d out of range [0,100]", got)
			}
		})
	}
}

// 多盘场景下进度应单调不减：模拟 2 块盘、每盘 5 次轮询，确认进度序列单调递增。
func TestSmartShortProgressMonotonic(t *testing.T) {
	totalDisks := 2
	pollsPerDisk := 5
	prev := -1
	for diskIdx := 0; diskIdx < totalDisks; diskIdx++ {
		for poll := 1; poll <= pollsPerDisk; poll++ {
			got := smartShortProgress(diskIdx, poll, totalDisks, pollsPerDisk)
			if got < prev {
				t.Errorf("progress regressed: disk=%d poll=%d got=%d prev=%d", diskIdx, poll, got, prev)
			}
			prev = got
		}
	}
	if prev != 100 {
		t.Errorf("final progress = %d, want 100", prev)
	}
}
