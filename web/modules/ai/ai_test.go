package ai

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nas-panel/common"
)

func newTestMux(t *testing.T) *http.ServeMux {
	t.Helper()
	setFakeConfigPath(t)
	mux := http.NewServeMux()
	RegisterRoutes(mux)
	return mux
}

func authedRequest(t *testing.T, method, path string, body []byte) *http.Request {
	t.Helper()
	token, err := common.CreateToken("fm")
	if err != nil {
		t.Fatalf("CreateToken 失败: %v", err)
	}
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, path, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestStatusAlwaysAccessibleAndNeverLeaksKey(t *testing.T) {
	mux := newTestMux(t)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(t, http.MethodGet, "/api/ai/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/ai/status 未启用时应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("api_key")) {
		t.Fatalf("status 响应不应包含 api_key 字段: %s", rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("status 响应非合法 JSON: %v", err)
	}
	for _, key := range []string{"enabled", "configured", "model", "preset"} {
		if _, ok := resp[key]; !ok {
			t.Fatalf("status 响应缺字段 %q: %v", key, resp)
		}
	}
}

func TestConfigEndpointRequiresEnabled(t *testing.T) {
	mux := newTestMux(t)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(t, http.MethodGet, "/api/ai/config", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("未启用时 GET /api/ai/config 应 403, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("403 响应非合法 JSON: %v", err)
	}
	if resp["error"] != "AI 功能未启用" {
		t.Fatalf("403 错误信息 = %q, want AI 功能未启用", resp["error"])
	}
}

func TestToggleOnWithoutVerifiedConfigRejected(t *testing.T) {
	mux := newTestMux(t)
	rec := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]interface{}{"enabled": true})
	mux.ServeHTTP(rec, authedRequest(t, http.MethodPost, "/api/ai/toggle", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未配置/未验证时开启应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("400 响应非合法 JSON: %v", err)
	}
	if resp["error"] != "请先配置模型端点并通过连通性测试" {
		t.Fatalf("400 错误信息 = %q", resp["error"])
	}
}

func TestToggleOnSucceedsWhenVerified(t *testing.T) {
	mux := newTestMux(t)
	if err := SaveConfig(Config{
		Enabled:      false,
		BaseURL:      "http://localhost:11434/v1",
		Model:        "qwen3:32b",
		MaxToolCalls: 12,
		Verified:     true,
	}); err != nil {
		t.Fatalf("SaveConfig 失败: %v", err)
	}

	rec := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]interface{}{"enabled": true})
	mux.ServeHTTP(rec, authedRequest(t, http.MethodPost, "/api/ai/toggle", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("已验证配置开启应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !aiEnabled() {
		t.Fatalf("开启后 aiEnabled() 应为 true")
	}

	// 关闭态下 enabled 守卫立刻解除
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, authedRequest(t, http.MethodGet, "/api/ai/config", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("开启后 GET /api/ai/config 应 200, got %d body=%s", rec2.Code, rec2.Body.String())
	}
}

func TestToggleOffAlwaysAllowed(t *testing.T) {
	mux := newTestMux(t)
	if err := SaveConfig(Config{
		Enabled:      true,
		BaseURL:      "http://localhost:11434/v1",
		Model:        "qwen3:32b",
		MaxToolCalls: 12,
		Verified:     true,
	}); err != nil {
		t.Fatalf("SaveConfig 失败: %v", err)
	}

	rec := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]interface{}{"enabled": false})
	mux.ServeHTTP(rec, authedRequest(t, http.MethodPost, "/api/ai/toggle", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("关闭开关应始终 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if aiEnabled() {
		t.Fatalf("关闭后 aiEnabled() 应为 false")
	}

	// 已保存的 endpoint/key 配置应保留（关闭不清空配置，只清 enabled）
	cfg := LoadConfig()
	if cfg.BaseURL == "" || cfg.Model == "" {
		t.Fatalf("关闭开关不应清空已保存的端点配置: %+v", cfg)
	}
}

func TestConfigGetMasksAPIKey(t *testing.T) {
	mux := newTestMux(t)
	if err := SaveConfig(Config{
		Enabled:  true,
		BaseURL:  "https://api.deepseek.com/v1",
		APIKey:   "sk-abcdef1234567890",
		Model:    "deepseek-chat",
		Verified: true,
	}); err != nil {
		t.Fatalf("SaveConfig 失败: %v", err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(t, http.MethodGet, "/api/ai/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/ai/config 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("1234567890")) {
		t.Fatalf("GET /api/ai/config 响应不应包含 key 明文: %s", rec.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["api_key"] != "****7890" {
		t.Fatalf("api_key 掩码 = %v, want ****7890", resp["api_key"])
	}
}
