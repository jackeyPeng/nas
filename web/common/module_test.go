package common

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// ============================================================
// module_test.go — Module 接口和中间件测试
//
// 执行方式：
//   cd web && go test ./common/ -v -run TestModule
// ============================================================

// mockModule 实现 Module 接口，用于测试
type mockModule struct {
	routesRegistered bool
}

func (m *mockModule) RegisterRoutes(mux *http.ServeMux) {
	m.routesRegistered = true
	mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// TestModuleInterface 验证 Module 接口使用正确
func TestModuleInterface(t *testing.T) {
	m := &mockModule{}
	mux := http.NewServeMux()

	// 调用接口方法
	m.RegisterRoutes(mux)

	if !m.routesRegistered {
		t.Error("RegisterRoutes() did not set routesRegistered flag")
	}

	// 验证路由确实注册了
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("registered handler status = %d, want 200", rec.Code)
	}

	// 编译时检查：mockModule 必须实现 Module 接口
	var _ Module = (*mockModule)(nil)
}

// TestLoggingMiddleware 验证中间件不阻塞请求
func TestLoggingMiddleware(t *testing.T) {
	handler := LoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("LoggingMiddleware status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("LoggingMiddleware body = %q, want %q", rec.Body.String(), "ok")
	}
}
