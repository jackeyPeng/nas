package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// setupChatTest 准备一个已启用、已配置、指向假 LLM 的 AI 模块实例，并返回
// mux。同时切换到临时 ai.db，互不干扰其余测试。
func setupChatTest(t *testing.T, llmURL string) *http.ServeMux {
	t.Helper()
	setFakeConfigPath(t)
	setFakeDBPath(t)
	if err := SaveConfig(Config{
		Enabled:      true,
		BaseURL:      llmURL,
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

func chatRequestJSON(t *testing.T, convID, message string) []byte {
	t.Helper()
	body, err := json.Marshal(chatRequestBody{ConversationID: convID, Message: message})
	if err != nil {
		t.Fatalf("序列化请求体失败: %v", err)
	}
	return body
}

func TestHandleChatNonStreamingPlainReply(t *testing.T) {
	srv, _ := scriptedLLM(t, []string{
		`{"choices":[{"message":{"role":"assistant","content":"你好！"}}]}`,
	})
	defer srv.Close()

	mux := setupChatTest(t, srv.URL)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(t, http.MethodPost, "/api/ai/chat", chatRequestJSON(t, "", "你好")))

	if rec.Code != http.StatusOK {
		t.Fatalf("应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp chatResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非法 JSON: %v", err)
	}
	if resp.ConversationID == "" {
		t.Fatal("应自动生成 conversation_id")
	}
	if resp.Reply != "你好！" {
		t.Errorf("Reply = %q", resp.Reply)
	}
	if resp.PendingAction != nil {
		t.Errorf("纯文本回复不应有 PendingAction")
	}
}

func TestHandleChatContinuesExistingConversation(t *testing.T) {
	srv, calls := scriptedLLM(t, []string{
		`{"choices":[{"message":{"role":"assistant","content":"第一条回复"}}]}`,
		`{"choices":[{"message":{"role":"assistant","content":"第二条回复"}}]}`,
	})
	defer srv.Close()

	mux := setupChatTest(t, srv.URL)

	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, authedRequest(t, http.MethodPost, "/api/ai/chat", chatRequestJSON(t, "", "第一句")))
	var resp1 chatResponseBody
	json.Unmarshal(rec1.Body.Bytes(), &resp1)

	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, authedRequest(t, http.MethodPost, "/api/ai/chat", chatRequestJSON(t, resp1.ConversationID, "第二句")))
	var resp2 chatResponseBody
	json.Unmarshal(rec2.Body.Bytes(), &resp2)

	if resp2.ConversationID != resp1.ConversationID {
		t.Fatalf("沿用已有会话 ID 应不变: %q vs %q", resp1.ConversationID, resp2.ConversationID)
	}
	if resp2.Reply != "第二条回复" {
		t.Errorf("Reply = %q", resp2.Reply)
	}
	if *calls != 2 {
		t.Fatalf("应调用模型 2 次, got %d", *calls)
	}

	// 会话历史应包含两轮问答，GET /api/ai/conversations/{id} 可读回。
	rec3 := httptest.NewRecorder()
	mux.ServeHTTP(rec3, authedRequest(t, http.MethodGet, "/api/ai/conversations/"+resp1.ConversationID, nil))
	if rec3.Code != http.StatusOK {
		t.Fatalf("GET 会话应 200, got %d body=%s", rec3.Code, rec3.Body.String())
	}
	var convResp struct {
		Messages []StoredMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec3.Body.Bytes(), &convResp); err != nil {
		t.Fatalf("会话响应非法 JSON: %v", err)
	}
	if len(convResp.Messages) != 4 {
		t.Fatalf("消息数 = %d, want 4（两轮 user+assistant）", len(convResp.Messages))
	}
}

func TestHandleChatWriteToolReturnsPendingActionNotExecuted(t *testing.T) {
	srv, _ := scriptedLLM(t, []string{
		`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"restart_service","arguments":"{\"service\":\"smbd\"}"}}]}}]}`,
	})
	defer srv.Close()

	var writeExecCalled bool
	origRegistry := toolRegistry
	toolRegistry = testRegistryWithWriteTool(t, &writeExecCalled)
	defer func() { toolRegistry = origRegistry }()

	mux := setupChatTest(t, srv.URL)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(t, http.MethodPost, "/api/ai/chat", chatRequestJSON(t, "", "重启samba")))

	if rec.Code != http.StatusOK {
		t.Fatalf("应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp chatResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非法 JSON: %v", err)
	}
	if resp.PendingAction == nil {
		t.Fatal("写工具请求应产出 pending_action")
	}
	if resp.PendingAction.ID == "" {
		t.Fatal("pending_action 应带非空 ID")
	}
	if resp.PendingAction.ConfirmWord != "执行" {
		t.Errorf("ConfirmWord = %q, want 执行", resp.PendingAction.ConfirmWord)
	}
	if writeExecCalled {
		t.Fatal("写工具不应被执行")
	}

	// PendingAction 应已存入 defaultPendingStore，可被 Take 一次。
	action, ok := defaultPendingStore.Take(resp.PendingAction.ID)
	if !ok {
		t.Fatal("PendingAction 应可从 defaultPendingStore 取出")
	}
	if action.ToolName != "restart_service" {
		t.Errorf("存入的 PendingAction.ToolName = %q", action.ToolName)
	}
	if _, ok := defaultPendingStore.Take(resp.PendingAction.ID); ok {
		t.Fatal("同一 ID 第二次 Take 应失败（一次性消费）")
	}
}

func TestHandleChatEmptyMessageRejected(t *testing.T) {
	srv, _ := scriptedLLM(t, []string{`{"choices":[{"message":{"role":"assistant","content":"x"}}]}`})
	defer srv.Close()
	mux := setupChatTest(t, srv.URL)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(t, http.MethodPost, "/api/ai/chat", chatRequestJSON(t, "", "   ")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("空消息应 400, got %d", rec.Code)
	}
}

func TestHandleChatRequiresEnabled(t *testing.T) {
	setFakeConfigPath(t)
	setFakeDBPath(t)
	mux := http.NewServeMux()
	RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(t, http.MethodPost, "/api/ai/chat", chatRequestJSON(t, "", "hi")))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("未启用时应 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleActionConfirmPlaceholderReturns501(t *testing.T) {
	srv, _ := scriptedLLM(t, []string{`{"choices":[{"message":{"role":"assistant","content":"x"}}]}`})
	defer srv.Close()
	mux := setupChatTest(t, srv.URL)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(t, http.MethodPost, "/api/ai/action/confirm", []byte(`{}`)))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("占位端点应 501, got %d body=%s", rec.Code, rec.Body.String())
	}
}
