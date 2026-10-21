package ai

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ── memoryPendingStore：Put/Take/TTL/一次性 ─────────────────────

func TestMemoryPendingStorePutTakeRoundTrip(t *testing.T) {
	s := newMemoryPendingStore()
	action := PendingAction{ID: "a1", ToolName: "restart_service", ArgsJSON: `{"service":"smbd"}`, CreatedAt: time.Now()}
	if err := s.Put(action); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	got, ok := s.Take("a1")
	if !ok {
		t.Fatal("Take 应命中刚存入的 action")
	}
	if got.ToolName != "restart_service" {
		t.Fatalf("Take 返回的 ToolName = %q", got.ToolName)
	}
}

func TestMemoryPendingStoreTakeIsOneTime(t *testing.T) {
	s := newMemoryPendingStore()
	s.Put(PendingAction{ID: "a1", ToolName: "x", CreatedAt: time.Now()})
	if _, ok := s.Take("a1"); !ok {
		t.Fatal("第一次 Take 应命中")
	}
	if _, ok := s.Take("a1"); ok {
		t.Fatal("二次 Take 同一 id 必须失败——一次性语义")
	}
}

func TestMemoryPendingStoreTakeMissingID(t *testing.T) {
	s := newMemoryPendingStore()
	if _, ok := s.Take("does-not-exist"); ok {
		t.Fatal("不存在的 id 应 Take 失败")
	}
}

func TestMemoryPendingStoreTakeExpiredTTL(t *testing.T) {
	s := newMemoryPendingStore()
	s.Put(PendingAction{ID: "a1", ToolName: "x", CreatedAt: time.Now().Add(-pendingActionTTL - time.Second)})
	if _, ok := s.Take("a1"); ok {
		t.Fatal("超过 TTL 的 action 应 Take 失败（视为不存在）")
	}
}

// ── 确认/取消端点 httptest ─────────────────────────────────────

// setupActionTest 准备一个已启用的 AI 模块 mux（不需要真实 LLM——确认端点
// 不调用模型），并返回它。
func setupActionTest(t *testing.T) *http.ServeMux {
	t.Helper()
	setFakeConfigPath(t)
	setFakeDBPath(t)
	if err := SaveConfig(Config{
		Enabled:      true,
		BaseURL:      "http://127.0.0.1:1",
		Model:        "test-model",
		MaxToolCalls: 12,
		Verified:     true,
	}); err != nil {
		t.Fatalf("SaveConfig 失败: %v", err)
	}
	mux := http.NewServeMux()
	RegisterRoutes(mux)
	return mux
}

func postJSON(t *testing.T, mux *http.ServeMux, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("序列化请求体失败: %v", err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(t, http.MethodPost, path, data))
	return rec
}

func TestConfirmActionWrongConfirmWordRejected(t *testing.T) {
	mux := setupActionTest(t)
	defaultPendingStore.Put(PendingAction{
		ID: "pa1", ToolName: "restart_service", ArgsJSON: `{"service":"smbd"}`,
		ConfirmWord: pendingActionConfirmWord, CreatedAt: time.Now(),
	})

	rec := postJSON(t, mux, "/api/ai/action/confirm", map[string]interface{}{
		"action_id": "pa1", "confirm_word": "不对",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("确认词错误应 400, got %d body=%s", rec.Code, rec.Body.String())
	}

	// 确认词错误不消耗 PendingAction？——Take 在校验确认词之前已经取出且删除
	// （一次性协议：无论确认词对不对，这个 id 都已经从存储移除）。
	// 这里断言的是"错误响应码"，不额外断言二次请求行为（协议已在上面测试覆盖）。
}

func TestConfirmActionExpiredReturns410(t *testing.T) {
	mux := setupActionTest(t)
	defaultPendingStore.Put(PendingAction{
		ID: "pa-expired", ToolName: "restart_service", ArgsJSON: `{"service":"smbd"}`,
		ConfirmWord: pendingActionConfirmWord, CreatedAt: time.Now().Add(-pendingActionTTL - time.Second),
	})

	rec := postJSON(t, mux, "/api/ai/action/confirm", map[string]interface{}{
		"action_id": "pa-expired", "confirm_word": pendingActionConfirmWord,
	})
	if rec.Code != http.StatusGone {
		t.Fatalf("过期 action 确认应 410, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestConfirmActionUnknownIDReturns410(t *testing.T) {
	mux := setupActionTest(t)
	rec := postJSON(t, mux, "/api/ai/action/confirm", map[string]interface{}{
		"action_id": "does-not-exist", "confirm_word": pendingActionConfirmWord,
	})
	if rec.Code != http.StatusGone {
		t.Fatalf("不存在的 action_id 应 410, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestConfirmActionSecondTakeFails(t *testing.T) {
	mux := setupActionTest(t)
	defaultPendingStore.Put(PendingAction{
		ID: "pa-once", ToolName: "restart_service", ArgsJSON: `{"service":"smbd"}`,
		ConfirmWord: pendingActionConfirmWord, CreatedAt: time.Now(),
	})

	// 第一次确认：restart_service 的 Exec 会真的调 systemctl（开发机大概率
	// 失败，但这不是本测试关注点——只关心 HTTP 层的一次性协议）。
	rec1 := postJSON(t, mux, "/api/ai/action/confirm", map[string]interface{}{
		"action_id": "pa-once", "confirm_word": pendingActionConfirmWord,
	})
	if rec1.Code == http.StatusGone {
		t.Fatalf("第一次确认不应命中 410（action 应仍在存储里）: %s", rec1.Body.String())
	}

	// 第二次确认同一 id：无论第一次执行成功与否，Take 都已经取走并删除，
	// 二次确认必须失败（410）。
	rec2 := postJSON(t, mux, "/api/ai/action/confirm", map[string]interface{}{
		"action_id": "pa-once", "confirm_word": pendingActionConfirmWord,
	})
	if rec2.Code != http.StatusGone {
		t.Fatalf("二次确认同一 action_id 必须失败, got %d body=%s", rec2.Code, rec2.Body.String())
	}
}

func TestConfirmActionMissingActionID(t *testing.T) {
	mux := setupActionTest(t)
	rec := postJSON(t, mux, "/api/ai/action/confirm", map[string]interface{}{
		"confirm_word": pendingActionConfirmWord,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺 action_id 应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestConfirmCreateUserMissingClientPayloadPassword(t *testing.T) {
	mux := setupActionTest(t)
	defaultPendingStore.Put(PendingAction{
		ID: "pa-cu", ToolName: "create_user", ArgsJSON: `{"username":"alice"}`,
		ConfirmWord: pendingActionConfirmWord, CreatedAt: time.Now(),
	})

	rec := postJSON(t, mux, "/api/ai/action/confirm", map[string]interface{}{
		"action_id": "pa-cu", "confirm_word": pendingActionConfirmWord,
	})
	// executeCreateUserConfirmed 缺密码返回 error，确认端点把执行错误映射成 502。
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("create_user 缺 client_payload.password 应执行失败(502), got %d body=%s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("password")) == false {
		t.Fatalf("错误信息应提示缺少 password: %s", rec.Body.String())
	}
}

func TestConfirmCreateUserControlCharPasswordRejected(t *testing.T) {
	mux := setupActionTest(t)
	defaultPendingStore.Put(PendingAction{
		ID: "pa-cu2", ToolName: "create_user", ArgsJSON: `{"username":"alice"}`,
		ConfirmWord: pendingActionConfirmWord, CreatedAt: time.Now(),
	})

	rec := postJSON(t, mux, "/api/ai/action/confirm", map[string]interface{}{
		"action_id": "pa-cu2", "confirm_word": pendingActionConfirmWord,
		"client_payload": map[string]interface{}{"password": "abc123456789\n rm -rf"},
	})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("含控制字符的密码应被 common.ValidSecret 拒绝(502), got %d body=%s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("abc123456789")) {
		t.Fatalf("错误响应不应回显密码原文: %s", rec.Body.String())
	}
}

func TestConfirmCreateUserShortPasswordRejected(t *testing.T) {
	mux := setupActionTest(t)
	defaultPendingStore.Put(PendingAction{
		ID: "pa-cu3", ToolName: "create_user", ArgsJSON: `{"username":"alice"}`,
		ConfirmWord: pendingActionConfirmWord, CreatedAt: time.Now(),
	})

	rec := postJSON(t, mux, "/api/ai/action/confirm", map[string]interface{}{
		"action_id": "pa-cu3", "confirm_word": pendingActionConfirmWord,
		"client_payload": map[string]interface{}{"password": "short"},
	})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("短密码应被拒绝(502), got %d body=%s", rec.Code, rec.Body.String())
	}
}

// 说明：executeConfirmedAction 成功路径真的调用 Exec 这一点，由
// TestConfirmActionSecondTakeFails（走真实 restart_service Exec，断言 HTTP
// 层一次性协议）间接覆盖；写工具 schema 形态与 agent loop 不直接执行写工具
// 的断言见 tools_nas_write_test.go / agent_test.go。这里不重复造一个无法
// 匹配 Tool.Exec 真实签名（context.Context）的假工具。
