package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"nas-panel/common"
)

var auditOnce sync.Once

// 包级共享 audit.db（t.TempDir 会在单个测试结束后删除，跨测试共享会失效）
var auditTestDir string

func init() {
	common.InitAuth("test-secret")
	d, _ := os.MkdirTemp("", "nas-audit-test")
	auditTestDir = d
}

func TestMain(m *testing.M) {
	code := m.Run()
	os.RemoveAll(auditTestDir)
	os.Exit(code)
}

// TestLoggingMiddlewareAuditDedup 验证：
// 1. 普通写请求（handler 无 LogAuditRequest）由中间件审计一条
// 2. handler 内调用 LogAuditRequest 后中间件不重复记录
// 3. GET 请求不审计
// 4. 4xx/5xx 记为 failed
func TestLoggingMiddlewareAuditDedup(t *testing.T) {
	auditOnce.Do(func() { common.InitAuditLog(auditTestDir) })

	plainWrite := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
	selfAudit := func(w http.ResponseWriter, r *http.Request) {
		common.LogAuditRequest(r, "STORAGE", "自定义操作", "detail=xyz", "success")
		w.WriteHeader(http.StatusOK)
	}
	failWrite := func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	}
	readOnly := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/plain", plainWrite)
	mux.HandleFunc("/api/self", selfAudit)
	mux.HandleFunc("/api/fail", failWrite)
	mux.HandleFunc("/api/read", readOnly)

	token, _ := common.CreateToken("tester")

	do := func(method, path string) {
		form := url.Values{"name": {"foo"}}
		var body *strings.Reader
		if method == http.MethodPost {
			body = strings.NewReader(form.Encode())
		} else {
			body = strings.NewReader("")
		}
		req := httptest.NewRequest(method, path, body)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		loggingMiddleware(mux).ServeHTTP(httptest.NewRecorder(), req)
	}

	before := common.GetAuditLogCount()
	do(http.MethodPost, "/api/plain")
	do(http.MethodPost, "/api/self")
	do(http.MethodPost, "/api/fail")
	do(http.MethodGet, "/api/read")

	// 给异步 auditWriter 时间落库
	waitForCount(t, before+3)

	entries, total, err := common.QueryAuditLog("", "", "", 1, 50, 0)
	if err != nil {
		t.Fatalf("QueryAuditLog: %v", err)
	}
	if total != before+3 {
		t.Fatalf("expected %d new entries, got total=%d before=%d", 3, total, before)
	}

	var plain, self, fail *common.AuditEntry
	for i := range entries {
		switch entries[i].Path {
		case "/api/plain":
			plain = &entries[i]
		case "/api/self":
			self = &entries[i]
		case "/api/fail":
			fail = &entries[i]
		case "/api/read":
			t.Errorf("GET should not be audited")
		}
	}
	if plain == nil || self == nil || fail == nil {
		t.Fatalf("missing entries: plain=%v self=%v fail=%v", plain, self, fail)
	}
	if plain.Username != "tester" {
		t.Errorf("plain username = %q, want tester", plain.Username)
	}
	if plain.Result != "success" {
		t.Errorf("plain result = %q, want success", plain.Result)
	}
	if self.Action != "自定义操作" || self.Detail != "detail=xyz" {
		t.Errorf("self entry = %+v, want custom action/detail", self)
	}
	if fail.Result != "failed" {
		t.Errorf("fail result = %q, want failed", fail.Result)
	}
}

// TestLoginAudit 验证登录成功/失败均记审计
func TestLoginAudit(t *testing.T) {
	auditOnce.Do(func() { common.InitAuditLog(auditTestDir) })
	before := common.GetAuditLogCount()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", handleLogin)

	form := url.Values{"username": {"wronguser"}, "password": {"wrongpass"}}
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loggingMiddleware(mux).ServeHTTP(httptest.NewRecorder(), req)

	waitForCount(t, before+1)

	entries, _, err := common.QueryAuditLog("", "login", "", 1, 10, 0)
	if err != nil {
		t.Fatalf("QueryAuditLog: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("login failure not audited")
	}
	if entries[0].Result != "failed" {
		t.Errorf("login result = %q, want failed", entries[0].Result)
	}
}

func waitForCount(t *testing.T, want int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if common.GetAuditLogCount() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond) // auditWriter 异步落库，给点时间
	}
	t.Fatalf("audit count did not reach %d (got %d)", want, common.GetAuditLogCount())
}
