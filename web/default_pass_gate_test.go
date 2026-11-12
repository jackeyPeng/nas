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

// TestDefaultPassGateMiddleware 验证中间件层：默认密码未改时，已认证会话访问
// 非白名单 /api/* 请求返回 403 must_change_password；改密后（UpdateNasPass）立即放行。
func TestDefaultPassGateMiddleware(t *testing.T) {
	common.InitAuth("gate-test-secret")
	common.SetNasPass(common.DefaultPass)
	t.Cleanup(func() { common.SetNasPass("") })

	handler := loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	token, err := common.CreateToken(common.GetNASUser())
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// 默认密码仍生效 + 已认证：非白名单 API 路径应被拦截
	req := httptest.NewRequest("GET", "/api/users", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("已认证且默认密码未改时 /api/users 应返回 403，got %d", rec.Code)
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
	req2.Header.Set("Authorization", "Bearer "+token)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("改密后 /api/users 应放行，got %d", rec2.Code)
	}
}

// TestDefaultPassGateMiddlewareUnauthenticated 验证门禁不对未认证请求生效：
// 未登录访客（无 token / 无效 token）访问非白名单路径应直达 next handler，
// 而不是被门禁拦下回 403 must_change_password——否则登录页上无守卫的 x-init
// 请求会被误判成「需要强制改密」。真正的鉴权交给各路由自身的 AuthMiddleware。
func TestDefaultPassGateMiddlewareUnauthenticated(t *testing.T) {
	common.InitAuth("gate-test-secret")
	common.SetNasPass(common.DefaultPass)
	t.Cleanup(func() { common.SetNasPass("") })

	handler := loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 无 Authorization 头：门禁不拦截，请求到达 next handler
	reqNoAuth := httptest.NewRequest("GET", "/api/users", nil)
	recNoAuth := httptest.NewRecorder()
	handler.ServeHTTP(recNoAuth, reqNoAuth)
	if recNoAuth.Code != http.StatusOK {
		t.Fatalf("无 token 访问 /api/users 应放行给 next handler，got %d", recNoAuth.Code)
	}

	// 无效 token：门禁同样不拦截
	reqBadToken := httptest.NewRequest("GET", "/api/users", nil)
	reqBadToken.Header.Set("Authorization", "Bearer garbage")
	recBadToken := httptest.NewRecorder()
	handler.ServeHTTP(recBadToken, reqBadToken)
	if recBadToken.Code != http.StatusOK {
		t.Fatalf("无效 token 访问 /api/users 应放行给 next handler，got %d", recBadToken.Code)
	}

	// 有效 token：门禁仍应拦截，返回 403 must_change_password
	token, err := common.CreateToken(common.GetNASUser())
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}
	reqValid := httptest.NewRequest("GET", "/api/users", nil)
	reqValid.Header.Set("Authorization", "Bearer "+token)
	recValid := httptest.NewRecorder()
	handler.ServeHTTP(recValid, reqValid)
	if recValid.Code != http.StatusForbidden {
		t.Fatalf("有效 token 且默认密码未改时 /api/users 应返回 403，got %d", recValid.Code)
	}
}
