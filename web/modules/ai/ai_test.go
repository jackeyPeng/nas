package ai

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	// 控制面豁免（R4 联调裁定）：关闭态必须能读配置，否则无法完成首次开启
	if rec.Code != http.StatusOK {
		t.Fatalf("未启用时 GET /api/ai/config 应 200（控制面豁免）, got %d body=%s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("api_key_sk_live")) {
		t.Fatalf("config 响应泄漏了完整 key: %s", rec.Body.String())
	}
}

func TestChatStillGuardedWhenDisabled(t *testing.T) {
	mux := newTestMux(t)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(t, http.MethodPost, "/api/ai/chat", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("未启用时 POST /api/ai/chat 应 403, got %d body=%s", rec.Code, rec.Body.String())
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

// TestConfigSaveEmptyAPIKeyPreservesRealKey 覆盖用户实报 bug 的根因 1：
// 前端保存成功后清空表单 api_key，testAIConnection 的"先保存再测"流程会
// 带空 api_key 再 POST 一次——这不该把已存的真 key 覆盖成空。
func TestConfigSaveEmptyAPIKeyPreservesRealKey(t *testing.T) {
	mux := newTestMux(t)
	if err := SaveConfig(Config{
		BaseURL:      "https://api.deepseek.com/v1",
		APIKey:       "sk-abcdef1234567890",
		Model:        "deepseek-chat",
		MaxToolCalls: 12,
		Verified:     true,
	}); err != nil {
		t.Fatalf("SaveConfig 失败: %v", err)
	}

	body, _ := json.Marshal(map[string]interface{}{
		"preset":         "deepseek",
		"base_url":       "https://api.deepseek.com/v1",
		"api_key":        "",
		"model":          "deepseek-chat",
		"max_tool_calls": 12,
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(t, http.MethodPost, "/api/ai/config", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("空 api_key 保存应 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	cfg := LoadConfig()
	if cfg.APIKey != "sk-abcdef1234567890" {
		t.Fatalf("空 api_key 回传应保留原 key, got %q", cfg.APIKey)
	}
	// base_url/model 均未变，Verified 不应被清。
	if !cfg.Verified {
		t.Fatalf("base_url/model/key 均未变时 Verified 不应被清")
	}
}

// TestConfigSaveMaskedAPIKeyPreservesRealKey 覆盖根因 1 的第二个触发路径：
// 前端 GET 回显掩码值后不改动直接保存，掩码串不应被当成新 key 存进去。
func TestConfigSaveMaskedAPIKeyPreservesRealKey(t *testing.T) {
	mux := newTestMux(t)
	realKey := "sk-abcdef1234567890"
	if err := SaveConfig(Config{
		BaseURL:      "https://api.deepseek.com/v1",
		APIKey:       realKey,
		Model:        "deepseek-chat",
		MaxToolCalls: 12,
		Verified:     true,
	}); err != nil {
		t.Fatalf("SaveConfig 失败: %v", err)
	}

	masked := MaskAPIKey(realKey)
	body, _ := json.Marshal(map[string]interface{}{
		"preset":         "deepseek",
		"base_url":       "https://api.deepseek.com/v1",
		"api_key":        masked,
		"model":          "deepseek-chat",
		"max_tool_calls": 12,
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(t, http.MethodPost, "/api/ai/config", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("掩码 api_key 保存应 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	cfg := LoadConfig()
	if cfg.APIKey != realKey {
		t.Fatalf("掩码回显值回传应保留原 key, got %q", cfg.APIKey)
	}
	if !cfg.Verified {
		t.Fatalf("base_url/model/key 均未变时 Verified 不应被清")
	}
}

// TestConfigSaveNewRealKeyUpdatesAndResetsVerified 覆盖"真的改了 key"的
// 正常路径：非空且不等于当前掩码的值必须被当成新 key 存入，且清 Verified
// （开关要求重新测试通过才能开启）。
func TestConfigSaveNewRealKeyUpdatesAndResetsVerified(t *testing.T) {
	mux := newTestMux(t)
	if err := SaveConfig(Config{
		BaseURL:      "https://api.deepseek.com/v1",
		APIKey:       "sk-oldkey1234567890",
		Model:        "deepseek-chat",
		MaxToolCalls: 12,
		Verified:     true,
	}); err != nil {
		t.Fatalf("SaveConfig 失败: %v", err)
	}

	newKey := "sk-brandnewkey9999999"
	body, _ := json.Marshal(map[string]interface{}{
		"preset":         "deepseek",
		"base_url":       "https://api.deepseek.com/v1",
		"api_key":        newKey,
		"model":          "deepseek-chat",
		"max_tool_calls": 12,
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(t, http.MethodPost, "/api/ai/config", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("新 api_key 保存应 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	cfg := LoadConfig()
	if cfg.APIKey != newKey {
		t.Fatalf("新 api_key 应被存入, got %q, want %q", cfg.APIKey, newKey)
	}
	if cfg.Verified {
		t.Fatalf("key 变更后 Verified 应被清为 false")
	}
}

// TestJSONErrorSetsJSONContentType 覆盖 jsonError helper 本身：
// Content-Type 必须是 application/json，响应体必须是合法 JSON 且含 error 字段
// （修复前 http.Error 固定写 text/plain，前端 api() 的 JSON 自动 toast 守卫
// 据此判断，text/plain 会落到字符串兜底分支——测试连通性失败静默的根因 2）。
func TestJSONErrorSetsJSONContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	jsonError(rec, "示例错误：包含\"引号\"与换行\n与反斜杠\\", http.StatusBadGateway)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("状态码 = %d, want %d", rec.Code, http.StatusBadGateway)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json 前缀", ct)
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("jsonError 响应体非合法 JSON: %v, body=%s", err, rec.Body.String())
	}
	if resp["error"] == "" {
		t.Fatalf("响应体缺 error 字段: %s", rec.Body.String())
	}
}

// TestTestConnectionFailureUsesJSONContentType 是端到端验证：
// handleTestConnection 失败路径（502）的真实 HTTP 响应也必须是 JSON
// content-type，不是 jsonError 单测层面的间接验证。
func TestTestConnectionFailureUsesJSONContentType(t *testing.T) {
	mux := newTestMux(t)
	if err := SaveConfig(Config{
		BaseURL:      "http://127.0.0.1:1", // 拒绝连接，必然失败
		Model:        "test-model",
		MaxToolCalls: 12,
	}); err != nil {
		t.Fatalf("SaveConfig 失败: %v", err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(t, http.MethodPost, "/api/ai/test-connection", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("连接失败应 502, got %d body=%s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json 前缀", ct)
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("test-connection 失败响应体非合法 JSON: %v, body=%s", err, rec.Body.String())
	}
	if resp["error"] == "" {
		t.Fatalf("响应体缺 error 字段: %s", rec.Body.String())
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
