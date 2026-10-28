package diskmgmt

import (
	"net/http/httptest"
	"testing"
)

// TestParseMdDetailLevelAndDevices 验证 A2 用到的 mdadm --detail 解析：从输出中
// 提取 Raid Level 和 Raid Devices 两个字段。
func TestParseMdDetailLevelAndDevices(t *testing.T) {
	detail := `/dev/md0:
           Version : 1.2
     Creation Time : Mon Jan  1 00:00:00 2026
        Raid Level : raid0
        Array Size : 1046528 (1022.00 MiB 1071.64 MB)
      Raid Devices : 2
     Total Devices : 2
`
	level, devs := parseMdDetailLevelAndDevices(detail)
	if level != "raid0" {
		t.Errorf("level = %q, want raid0", level)
	}
	if devs != 2 {
		t.Errorf("devices = %d, want 2", devs)
	}

	t.Run("空输入", func(t *testing.T) {
		level, devs := parseMdDetailLevelAndDevices("")
		if level != "" || devs != 0 {
			t.Errorf("got (%q, %d), want (\"\", 0)", level, devs)
		}
	})

	t.Run("raid1", func(t *testing.T) {
		d := "        Raid Level : raid1\n      Raid Devices : 2\n"
		level, devs := parseMdDetailLevelAndDevices(d)
		if level != "raid1" || devs != 2 {
			t.Errorf("got (%q, %d), want (raid1, 2)", level, devs)
		}
	})
}

// TestWaitForResyncReturnsNilWhenNoResyncLine 验证 A1：当 /proc/mdstat 里找不到
// 该 md 设备的 resync/recovery 行（本沙箱环境没有真实 RAID 设备）时，waitForResync
// 最终应返回 nil，不触发 10 分钟轮询等待——这里主要验证函数签名变更后（void→error）
// 行为仍然正确、调用方能拿到可判断的返回值。
//
// Finding 3（首轮竞态）修复后，waitForResync 不再是"读到一次干净就算完成"：
// 循环前先 sleep 2s，之后要求连续 3 轮（cleanStreak）都查不到 resync/recovery
// 行才返回 nil。本测试场景从头到尾都没有匹配设备，相当于一直是"干净"读数，
// 会按新逻辑跑满 2s（首次等待）+ 3*2s（streak 轮询）≈ 8s 才返回，而不再是
// 旧版本的"立即返回"——这里用真实 time.Sleep（无可注入的时钟/sleep 依赖），
// 运行时间变长但在可接受范围（单测总耗时从毫秒级变为个位数秒级）。
// waitForResync 没有暴露 mdstat 路径为参数，无法在不改生产签名的前提下 mock
// /proc/mdstat 走到"观察到 resync 行后 streak 清零重新累计"的分支，这部分
// 行为（cleanStreak 归零重计）在报告里以代码审查方式说明验证，不在本测试
// 覆盖范围内。
func TestWaitForResyncReturnsNilWhenNoResyncLine(t *testing.T) {
	w := httptest.NewRecorder()
	err := waitForResync(w, "/dev/md_test_nonexistent", 0, 5)
	if err != nil {
		t.Fatalf("waitForResync() = %v, want nil (无匹配设备应视为同步完成)", err)
	}
}
