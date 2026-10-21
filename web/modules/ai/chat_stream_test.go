package ai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sseScriptedLLM 起一个假 OpenAI-compatible 服务器，流式返回固定的
// SSE 帧序列（每次请求都走同一组 frames——chat_stream_test.go 的场景
// 只需要验证单轮请求内的事件顺序，不需要跨轮脚本化）。
func sseScriptedLLM(t *testing.T, frames []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
}

// parsedSSEEvent 是从捕获的 SSE 全文里解析出的单行事件，供断言用。
type parsedSSEEvent struct {
	Type           string `json:"type"`
	Text           string `json:"text,omitempty"`
	Name           string `json:"name,omitempty"`
	ArgsDigest     string `json:"args_digest,omitempty"`
	Digest         string `json:"digest,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	Message        string `json:"message,omitempty"`
	ID             string `json:"id,omitempty"`
	ToolName       string `json:"tool_name,omitempty"`
}

// parseSSEBody 把 httptest.NewRecorder 捕获的 SSE 响应全文拆成逐行事件，
// 同时断言每个 data 行都是合法 JSON（计划里"每个 data 行 json.Valid"的
// 硬要求）。
func parseSSEBody(t *testing.T, body []byte) []parsedSSEEvent {
	t.Helper()
	var events []parsedSSEEvent
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if !json.Valid([]byte(payload)) {
			t.Fatalf("SSE data 行不是合法 JSON: %s", payload)
		}
		var ev parsedSSEEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			t.Fatalf("SSE data 行解析失败: %v, 原文: %s", err, payload)
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("扫描 SSE body 失败: %v", err)
	}
	return events
}

func sseChatRequest(t *testing.T, convID, message string) *http.Request {
	t.Helper()
	body := chatRequestJSON(t, convID, message)
	req := authedRequest(t, http.MethodPost, "/api/ai/chat", body)
	req.Header.Set("Accept", "text/event-stream")
	return req
}

// TestHandleChatSSEEventOrderDeltaToolCallToolResultDeltaDone 是计划
// chat_stream_test.go 指定的核心场景：流式返回 delta×2 + 跨 chunk 拆分的
// tool_call，断言事件顺序 delta→tool_call→tool_result→delta→done，且
// 每个 data 行 json.Valid（parseSSEBody 已经断言过这一点）。
func TestHandleChatSSEEventOrderDeltaToolCallToolResultDeltaDone(t *testing.T) {
	// 第一轮：2 个 content delta + 跨 chunk 拆分的 tool_call（id/name 在首块，
	// arguments 分两块到达），对齐 llm_test.go 的拆分模式。
	// 第二轮：纯文本回复，触发 done。
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		var frames []string
		if callCount == 1 {
			frames = []string{
				`{"choices":[{"delta":{"content":"正在"}}]}`,
				`{"choices":[{"delta":{"content":"查询"}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"get_monitor_snapshot","arguments":""}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]}}]}`,
			}
		} else {
			frames = []string{
				`{"choices":[{"delta":{"content":"CPU 正常。"}}]}`,
			}
		}
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer srv.Close()

	mux := setupChatTest(t, srv.URL)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, sseChatRequest(t, "", "CPU怎么样"))

	if rec.Code != http.StatusOK {
		t.Fatalf("SSE 响应应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	events := parseSSEBody(t, rec.Body.Bytes())

	var types []string
	for _, ev := range events {
		types = append(types, ev.Type)
	}

	// 断言顺序：delta, delta, tool_call, tool_result, delta, done
	// （第一轮 2 个 delta → 聚合出 tool_call → Exec 后 tool_result →
	// 第二轮 1 个 delta → 循环以纯文本终止 → done）。
	wantOrder := []string{"delta", "delta", "tool_call", "tool_result", "delta", "done"}
	if len(types) != len(wantOrder) {
		t.Fatalf("事件序列 = %v, want %v", types, wantOrder)
	}
	for i, want := range wantOrder {
		if types[i] != want {
			t.Errorf("事件[%d] = %q, want %q（完整序列: %v）", i, types[i], want, types)
		}
	}

	// delta 文本内容：前两个 delta 拼起来应是 "正在查询"，最后一个是 "CPU 正常。"
	if events[0].Text != "正在" || events[1].Text != "查询" {
		t.Errorf("前两个 delta 文本 = %q, %q", events[0].Text, events[1].Text)
	}
	if events[4].Text != "CPU 正常。" {
		t.Errorf("第二轮 delta 文本 = %q", events[4].Text)
	}

	// tool_call 事件字段
	if events[2].Name != "get_monitor_snapshot" {
		t.Errorf("tool_call.name = %q", events[2].Name)
	}
	if events[2].ArgsDigest != "{}" {
		t.Errorf("tool_call.args_digest = %q, want {}（跨 chunk 拆分的 arguments 聚合后）", events[2].ArgsDigest)
	}

	// tool_result 事件字段
	if events[3].Name != "get_monitor_snapshot" {
		t.Errorf("tool_result.name = %q", events[3].Name)
	}
	if events[3].Digest == "" {
		t.Errorf("tool_result.digest 不应为空")
	}

	// done 事件带 conversation_id
	if events[5].ConversationID == "" {
		t.Errorf("done 事件应带 conversation_id")
	}
}

// TestHandleChatSSEPendingActionEvent 验证写工具触发时 SSE 会发出
// pending_action 事件，字段与 PendingAction 结构体对齐（全字段摊平到顶层）。
func TestHandleChatSSEPendingActionEvent(t *testing.T) {
	srv := sseScriptedLLM(t, []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"restart_service","arguments":"{\"service\":\"smbd\"}"}}]}}]}`,
	})
	defer srv.Close()

	var writeExecCalled bool
	origRegistry := toolRegistry
	toolRegistry = testRegistryWithWriteTool(t, &writeExecCalled)
	defer func() { toolRegistry = origRegistry }()

	mux := setupChatTest(t, srv.URL)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, sseChatRequest(t, "", "重启samba"))

	events := parseSSEBody(t, rec.Body.Bytes())
	var pendingEv *parsedSSEEvent
	var doneEv *parsedSSEEvent
	for i := range events {
		switch events[i].Type {
		case "pending_action":
			pendingEv = &events[i]
		case "done":
			doneEv = &events[i]
		}
	}
	if pendingEv == nil {
		t.Fatal("应收到 pending_action 事件")
	}
	if pendingEv.ID == "" {
		t.Error("pending_action 事件应带非空 id")
	}
	if pendingEv.ToolName != "restart_service" {
		t.Errorf("pending_action.tool_name = %q", pendingEv.ToolName)
	}
	if doneEv == nil {
		t.Fatal("即便产出 pending_action，流程仍应正常走到 done")
	}
	if writeExecCalled {
		t.Fatal("写工具不应被执行")
	}
}

// TestHandleChatSSEErrorEventOnLLMFailure 验证模型调用失败时发 error 事件
// 而不是直接 500/挂起连接（SSE 一旦建立连接，错误也要走事件协议通知前端）。
func TestHandleChatSSEErrorEventOnLLMFailure(t *testing.T) {
	// 起一个立刻关闭的 server，制造"无法连接"错误。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	mux := setupChatTest(t, url)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, sseChatRequest(t, "", "你好"))

	events := parseSSEBody(t, rec.Body.Bytes())
	if len(events) != 1 || events[0].Type != "error" {
		t.Fatalf("连接失败应只发一个 error 事件, got %v", events)
	}
	if events[0].Message == "" {
		t.Error("error 事件应带 message")
	}
}
