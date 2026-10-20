package system

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"nas-panel/common"
)

func TestCheckAccessAllowed_Loopback(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/system/check", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	if !checkAccessAllowed(r) {
		t.Error("本机回环地址应放行")
	}
}

func TestCheckAccessAllowed_LoopbackIPv6(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/system/check", nil)
	r.RemoteAddr = "[::1]:54321"
	if !checkAccessAllowed(r) {
		t.Error("本机 IPv6 回环地址应放行")
	}
}

func TestCheckAccessAllowed_SpoofedXFFRejected(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/system/check", nil)
	r.RemoteAddr = "203.0.113.5:12345"
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	if checkAccessAllowed(r) {
		t.Error("伪造的 X-Forwarded-For 不应被用于放行判断")
	}
}

func TestCheckAccessAllowed_RemoteNoTokenRejected(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/system/check", nil)
	r.RemoteAddr = "203.0.113.5:12345"
	if checkAccessAllowed(r) {
		t.Error("远程无 token 请求应被拒绝")
	}
}

func TestCheckAccessAllowed_RemoteValidTokenAllowed(t *testing.T) {
	common.InitAuth("test-secret-for-system-check")
	token, err := common.CreateToken("admin")
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/system/check", nil)
	r.RemoteAddr = "203.0.113.5:12345"
	r.Header.Set("Authorization", "Bearer "+token)
	if !checkAccessAllowed(r) {
		t.Error("远程携带有效 token 应放行")
	}
}

func TestCheckAccessAllowed_RemoteInvalidTokenRejected(t *testing.T) {
	common.InitAuth("test-secret-for-system-check")
	r := httptest.NewRequest(http.MethodGet, "/api/system/check", nil)
	r.RemoteAddr = "203.0.113.5:12345"
	r.Header.Set("Authorization", "Bearer not-a-real-token")
	if checkAccessAllowed(r) {
		t.Error("远程无效 token 应被拒绝")
	}
}

func TestHandleSystemCheck_RemoteNoAuthReturns401(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/system/check", nil)
	r.RemoteAddr = "203.0.113.5:12345"
	w := httptest.NewRecorder()
	handleSystemCheck(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("远程无 Authorization 的请求应返回 401，实际 %d", w.Code)
	}
}
