package diagnostics

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteSysfs 验证 writeSysfs 的写入语义：等价于原来的
// `echo value > path 2>/dev/null`——成功时内容被整体覆盖写入，
// 失败（如路径不存在）时不 panic、不返回错误给调用方（容错语义）。
func TestWriteSysfs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sync_speed_max")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatalf("setup WriteFile failed: %v", err)
	}

	writeSysfs(path, "200000")

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(got) != "200000" {
		t.Errorf("writeSysfs 内容 = %q, want %q", string(got), "200000")
	}
}

// TestWriteSysfs_MissingPathDoesNotPanic 验证写入不存在的路径时
// 保留原 2>/dev/null 的静默容错语义：不 panic，不阻断调用方。
func TestWriteSysfs_MissingPathDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("writeSysfs panicked on missing path: %v", r)
		}
	}()
	writeSysfs("/nonexistent/dir/does/not/exist/sync_action", "check")
}
