package diskmgmt

import (
	"sync"
	"testing"
	"time"
)

// 缺陷 1（P0）：新建共享文件夹 SMB 不可访问 —— 根因是 GenerateSambaConfig 对非 public
// 文件夹生成 force user=<文件夹名>，但文件夹创建从不创建同名系统用户/组。
// 修复前置条件：文件夹名必须是合法系统用户名（groupadd/useradd 可用）。
func TestIsValidFolderName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"team", true},
		{"te", true},     // 2 位下限
		{"a1", true},     // 字母+数字
		{"team_1", true}, // 下划线
		{"team-1", true}, // 短横线
		{"", false},
		{"t", false},        // 1 位过短
		{"Team", false},     // 大写开头不可作为系统用户
		{"team doc", false}, // 空格非法
		{"team.1", false},   // 点非法
		{"团队", false},       // 非 ASCII 非法
		{"team!", false},    // 特殊字符
	}
	for _, c := range cases {
		if got := isValidFolderName(c.name); got != c.want {
			t.Errorf("isValidFolderName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
	// 超长（>32）非法
	long := ""
	for i := 0; i < 33; i++ {
		long += "a"
	}
	if isValidFolderName(long) {
		t.Errorf("isValidFolderName(33 个 a) 应为 false")
	}
	if !isValidFolderName(long[:32]) {
		t.Errorf("isValidFolderName(32 个 a) 应为 true")
	}
}

// 缺陷 2：SyncAllConfigs 每次都对 rclone-s3/webdav 做 systemctl restart，
// 短时间多次操作触发 systemd start-limit（StartLimitBurst=5/10s），服务 failed。
// 修复：restart 去抖（trailing debounce），窗口内多次请求合并为一次，最终仍会执行。
func TestRestartRcloneDebounced(t *testing.T) {
	// 保存并替换 restart 命令与 delay，让测试不依赖真实 systemctl、不等待 2s
	origCmd := rcloneRestartCmd
	origDelay := rcloneReloadDelay
	origTm := rcloneReloadTm
	defer func() {
		rcloneRestartCmd = origCmd
		rcloneReloadDelay = origDelay
		// 停止遗留 timer 并恢复
		rcloneReloadMu.Lock()
		for _, tm := range rcloneReloadTm {
			tm.Stop()
		}
		rcloneReloadTm = origTm
		rcloneReloadMu.Unlock()
	}()

	rcloneReloadDelay = 30 * time.Millisecond
	rcloneReloadMu.Lock()
	rcloneReloadTm = map[string]*time.Timer{}
	rcloneReloadMu.Unlock()

	var mu sync.Mutex
	calls := 0
	rcloneRestartCmd = func(_ string) {
		mu.Lock()
		calls++
		mu.Unlock()
	}

	// 窗口内连续触发 5 次（模拟建池+建文件夹+建多个用户）
	for i := 0; i < 5; i++ {
		restartRcloneDebounced("rclone-s3")
		time.Sleep(5 * time.Millisecond)
	}

	// 等待合并后的单次执行
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("rclone restart 应合并为 1 次，实际 %d 次", calls)
	}
}
