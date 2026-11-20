package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestHandleHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	handleHealthz(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("healthz 状态码 = %d，期望 200", w.Code)
	}
}

func TestHandleWhoamiValidSignature(t *testing.T) {
	secret := "test-secret"
	user := "alice"
	now := time.Now().Unix()
	sig := signFor(secret, user, now)

	req := httptest.NewRequest(http.MethodGet, "/api/whoami", nil)
	req.Header.Set("X-NAS-User", user)
	req.Header.Set("X-NAS-Auth", sig)
	req.Header.Set("X-NAS-Time", strconv.FormatInt(now, 10))

	w := httptest.NewRecorder()
	handleWhoami(w, req, secretCtx{secret: secret, dataDir: ""})

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", w.Code)
	}
	var resp whoamiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if !resp.Valid {
		t.Error("合法签名应验签通过（valid=true）")
	}
	if resp.User != user {
		t.Errorf("user = %q，期望 %q", resp.User, user)
	}
}

func TestHandleWhoamiInvalidSignature(t *testing.T) {
	secret := "test-secret"
	user := "alice"
	now := time.Now().Unix()

	req := httptest.NewRequest(http.MethodGet, "/api/whoami", nil)
	req.Header.Set("X-NAS-User", user)
	req.Header.Set("X-NAS-Auth", "0000000000000000000000000000000000000000000000000000000000000000")
	req.Header.Set("X-NAS-Time", strconv.FormatInt(now, 10))

	w := httptest.NewRecorder()
	handleWhoami(w, req, secretCtx{secret: secret, dataDir: ""})

	// 任务要求：验签失败仍 200，只是 valid:false（演示报告性质，不做二次拒绝）。
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", w.Code)
	}
	var resp whoamiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Valid {
		t.Error("伪造签名不应验签通过（valid 应为 false）")
	}
}

func TestHandleWhoamiExpiredTimestamp(t *testing.T) {
	secret := "test-secret"
	user := "alice"
	staleTime := time.Now().Add(-5 * time.Minute).Unix() // 超出 ±60s 容差
	sig := signFor(secret, user, staleTime)

	req := httptest.NewRequest(http.MethodGet, "/api/whoami", nil)
	req.Header.Set("X-NAS-User", user)
	req.Header.Set("X-NAS-Auth", sig)
	req.Header.Set("X-NAS-Time", strconv.FormatInt(staleTime, 10))

	w := httptest.NewRecorder()
	handleWhoami(w, req, secretCtx{secret: secret, dataDir: ""})

	var resp whoamiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Valid {
		t.Error("超出 ±60s 容差的时间戳不应验签通过")
	}
}

func TestHandleWhoamiMissingSecret(t *testing.T) {
	// NAS_SECRET 未注入（裸跑调试场景）：不 panic，直接 valid=false。
	req := httptest.NewRequest(http.MethodGet, "/api/whoami", nil)
	req.Header.Set("X-NAS-User", "alice")

	w := httptest.NewRecorder()
	handleWhoami(w, req, secretCtx{secret: "", dataDir: ""})

	var resp whoamiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Valid {
		t.Error("缺少 NAS_SECRET 时不应判定为验签通过")
	}
}

func TestCountDataDirFiles(t *testing.T) {
	if got := countDataDirFiles(""); got != 0 {
		t.Errorf("空 DATA_DIR 应返回 0，实际 %d", got)
	}
	if got := countDataDirFiles("/path/does/not/exist"); got != 0 {
		t.Errorf("不存在的目录应返回 0，实际 %d", got)
	}
	tmp := t.TempDir()
	countingFiles := 3
	for i := 0; i < countingFiles; i++ {
		f, err := os.CreateTemp(tmp, "f")
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	if got := countDataDirFiles(tmp); got != countingFiles {
		t.Errorf("DataDirFiles = %d，期望 %d", got, countingFiles)
	}
}

// signFor 是测试用的签名构造函数，与 verifyNASAuth 期望的算法一致：
// hex(hmac-sha256(secret, user + "\n" + unix_time))。
func signFor(secret, user string, unixSeconds int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(user + "\n" + strconv.FormatInt(unixSeconds, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}
