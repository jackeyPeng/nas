package diskmgmt

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// TestDebugDiskGateBlocksByDefault 验证 B1：format/mount/unmount/mkdir 四个
// 低级别磁盘接口默认（NAS_DISK_DEBUG 未设置或非 "1"）一律 403，不执行被包裹的
// handler。
func TestDebugDiskGateBlocksByDefault(t *testing.T) {
	os.Unsetenv("NAS_DISK_DEBUG")
	called := false
	gated := debugDiskGate(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	r := httptest.NewRequest(http.MethodPost, "/api/disk/format", nil)
	w := httptest.NewRecorder()
	gated(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", w.Code)
	}
	if called {
		t.Error("被包裹的 handler 不应被调用")
	}
}

// TestDebugDiskGateAllowsWhenEnvSet 验证显式开启 NAS_DISK_DEBUG=1 后放行。
func TestDebugDiskGateAllowsWhenEnvSet(t *testing.T) {
	os.Setenv("NAS_DISK_DEBUG", "1")
	defer os.Unsetenv("NAS_DISK_DEBUG")
	called := false
	gated := debugDiskGate(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	r := httptest.NewRequest(http.MethodPost, "/api/disk/format", nil)
	w := httptest.NewRecorder()
	gated(w, r)

	if !called {
		t.Error("NAS_DISK_DEBUG=1 时应放行并调用被包裹的 handler")
	}
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
}

// TestDebugDiskGateRejectsNonOneValue 确认只有精确值 "1" 才放行，其他真值
// 字符串（如 "true"）不应被当作开启。
func TestDebugDiskGateRejectsNonOneValue(t *testing.T) {
	os.Setenv("NAS_DISK_DEBUG", "true")
	defer os.Unsetenv("NAS_DISK_DEBUG")
	gated := debugDiskGate(func(w http.ResponseWriter, r *http.Request) {
		t.Error("NAS_DISK_DEBUG=true 不应放行")
	})
	r := httptest.NewRequest(http.MethodPost, "/api/disk/format", nil)
	w := httptest.NewRecorder()
	gated(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", w.Code)
	}
}
