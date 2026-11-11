package common

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// ============================================================
// admin_test.go — AdminMiddleware / IsAdmin 单元测试
//
// GetNASUser() 在测试环境没有 NAS_USER env 时默认返回 "fm"，
// 所以下面用 "fm" 作为管理员用户名，"bob" 作为非管理员用户名。
// ============================================================

func TestAdminMiddleware(t *testing.T) {
	InitAuth("admin-test-secret")
	os.Unsetenv("NAS_USER") // 确保走默认 "fm"

	adminToken, err := CreateToken("fm")
	if err != nil {
		t.Fatalf("CreateToken(fm) failed: %v", err)
	}
	nonAdminToken, err := CreateToken("bob")
	if err != nil {
		t.Fatalf("CreateToken(bob) failed: %v", err)
	}

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{"valid_admin_token", "Bearer " + adminToken, http.StatusOK},
		{"valid_non_admin_token", "Bearer " + nonAdminToken, http.StatusForbidden},
		{"no_token", "", http.StatusUnauthorized},
		{"malformed_header", "Bearer", http.StatusUnauthorized},
		{"invalid_token", "Bearer invalid.token.here", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := AdminMiddleware(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			req := httptest.NewRequest("GET", "/api/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()

			handler(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("AdminMiddleware() status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestIsAdmin(t *testing.T) {
	InitAuth("admin-test-secret-2")
	os.Unsetenv("NAS_USER")

	adminToken, _ := CreateToken("fm")
	nonAdminToken, _ := CreateToken("bob")

	tests := []struct {
		name       string
		authHeader string
		want       bool
	}{
		{"admin_token", "Bearer " + adminToken, true},
		{"non_admin_token", "Bearer " + nonAdminToken, false},
		{"no_token", "", false},
		{"invalid_token", "Bearer garbage", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			if got := IsAdmin(req); got != tt.want {
				t.Errorf("IsAdmin() = %v, want %v", got, tt.want)
			}
		})
	}
}
