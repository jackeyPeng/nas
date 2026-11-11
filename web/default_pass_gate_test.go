package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"nas-panel/common"
)

// ============================================================
// default_pass_gate_test.go — 默认密码强制改密门禁单测
// ============================================================

func TestDefaultPassGateAllowed(t *testing.T) {
	nasUser := "fm"
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"login_allowed", "/api/login", true},
		{"version_allowed", "/api/version", true},
		{"health_allowed", "/api/health", true},
		{"own_password_change_allowed", "/api/users/fm/password", true},
		{"other_user_password_blocked", "/api/users/bob/password", false},
		{"static_root_allowed", "/", true},
		{"static_asset_allowed", "/app.js", true},
		{"other_api_blocked", "/api/users", false},
		{"backup_blocked", "/api/backup/restore", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := defaultPassGateAllowed(tt.path, nasUser); got != tt.want {
				t.Errorf("defaultPassGateAllowed(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// TestDefaultPassGateMiddleware 验证中间件层：默认密码未改时非白名单 /api/* 请求
// 返回 403 must_change_password；改密后（UpdateNasPass）立即放行。
func TestDefaultPassGateMiddleware(t *testing.T) {
	common.InitAuth("gate-test-secret")
	common.SetNasPass(common.DefaultPass)
	t.Cleanup(func() { common.SetNasPass("") })

	handler := loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 默认密码仍生效：非白名单 API 路径应被拦截
	req := httptest.NewRequest("GET", "/api/users", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("默认密码未改时 /api/users 应返回 403，got %d", rec.Code)
	}

	// 白名单路径（登录）仍可达
	reqLogin := httptest.NewRequest("GET", "/api/login", nil)
	recLogin := httptest.NewRecorder()
	handler.ServeHTTP(recLogin, reqLogin)
	if recLogin.Code != http.StatusOK {
		t.Fatalf("默认密码未改时 /api/login 应可达，got %d", recLogin.Code)
	}

	// 改密后（模拟 UpdateNasPass）门禁应立即解除
	common.UpdateNasPass("a-real-new-password")
	req2 := httptest.NewRequest("GET", "/api/users", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("改密后 /api/users 应放行，got %d", rec2.Code)
	}
}
