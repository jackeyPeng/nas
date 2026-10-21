package monitor

import "testing"

// TestSnapshotDoesNotPanic 验证 Snapshot() 薄导出层不 panic、返回非 nil 的
// map。预置一份假缓存快照，避免在开发机（无系统命令环境）触发真实的
// getMonitorStatus 冷启动采样——那条路径已有 sampler_test.go 覆盖，这里只
// 验证 Snapshot() = getMonitorSnapshot() 的导出语义本身。
func TestSnapshotDoesNotPanic(t *testing.T) {
	resetSamplerState()
	defer resetSamplerState()

	sampleMu.Lock()
	latestSample = map[string]interface{}{"cpu_percent": "0.0"}
	hasSample = true
	sampleMu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Snapshot() panicked: %v", r)
		}
	}()
	got := Snapshot()
	if got == nil {
		t.Fatal("Snapshot() 返回 nil，期望至少是空 map")
	}
	if got["cpu_percent"] != "0.0" {
		t.Errorf("Snapshot() = %v, want 透传 getMonitorSnapshot 的缓存值", got)
	}
}
