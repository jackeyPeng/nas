package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testCfg(baseURL string) Config {
	return Config{
		BaseURL:      baseURL,
		APIKey:       "sk-test-key",
		Model:        "test-model",
		MaxToolCalls: 12,
	}
}

// ── ChatOnce：非流式，断言解析出的 tool_calls ───────────────────────

func TestChatOnceParsesToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test-key" {
			t.Errorf("Authorization header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "",
					"tool_calls": [{
						"id": "call_1",
						"type": "function",
						"function": {"name": "get_monitor_snapshot", "arguments": "{\"x\":1}"}
					}]
				}
			}]
		}`)
	}))
	defer srv.Close()

	msg, err := ChatOnce(context.Background(), testCfg(srv.URL), ChatRequest{
		Model:    "test-model",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatOnce 失败: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("ToolCalls 数量 = %d, want 1", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "call_1" {
		t.Errorf("ID = %q, want call_1", tc.ID)
	}
	if tc.Function.Name != "get_monitor_snapshot" {
		t.Errorf("Function.Name = %q", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"x":1}` {
		t.Errorf("Function.Arguments = %q", tc.Function.Arguments)
	}
}

// ── ChatOnce：baseURL 尾斜杠容忍 ───────────────────────

func TestChatOnceTrailingSlashBaseURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()

	cfg := testCfg(srv.URL + "/")
	_, err := ChatOnce(context.Background(), cfg, ChatRequest{Model: "test-model", Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("ChatOnce 失败: %v", err)
	}
	if gotPath != "/chat/completions" {
		t.Fatalf("请求路径 = %q, want /chat/completions（尾斜杠应被容忍去重）", gotPath)
	}
}

// ── ChatStream：3 个 content delta + 跨 chunk 拆分的 tool_call ───────────────────────

func TestChatStreamDeltasAndToolCallAggregation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		lines := []string{
			`{"choices":[{"delta":{"content":"你"}}]}`,
			`{"choices":[{"delta":{"content":"好"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"get_monitor_snapshot","arguments":""}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\":"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}`,
			`{"choices":[{"delta":{"content":"！"}}]}`,
		}
		for _, l := range lines {
			fmt.Fprintf(w, "data: %s\n\n", l)
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

	var deltas []string
	type toolDelta struct {
		index          int
		idD, nameD, argD string
	}
	var toolDeltas []toolDelta

	err := ChatStream(context.Background(), testCfg(srv.URL), ChatRequest{
		Model:    "test-model",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Stream:   true,
	}, func(text string) {
		deltas = append(deltas, text)
	}, func(index int, idDelta, nameDelta, argsDelta string) {
		toolDeltas = append(toolDeltas, toolDelta{index, idDelta, nameDelta, argsDelta})
	})
	if err != nil {
		t.Fatalf("ChatStream 失败: %v", err)
	}

	wantDeltas := []string{"你", "好", "！"}
	if len(deltas) != len(wantDeltas) {
		t.Fatalf("delta 序列 = %v, want %v", deltas, wantDeltas)
	}
	for i, w := range wantDeltas {
		if deltas[i] != w {
			t.Errorf("deltas[%d] = %q, want %q", i, deltas[i], w)
		}
	}

	if len(toolDeltas) != 3 {
		t.Fatalf("tool_call delta 次数 = %d, want 3", len(toolDeltas))
	}
	// 聚合原料：id 在第一块、name 在第一块、arguments 分两块
	if toolDeltas[0].idD != "call_abc" || toolDeltas[0].nameD != "get_monitor_snapshot" {
		t.Errorf("首块聚合原料不对: %+v", toolDeltas[0])
	}
	aggregatedArgs := toolDeltas[0].argD + toolDeltas[1].argD + toolDeltas[2].argD
	if aggregatedArgs != `{"a":1}` {
		t.Errorf("聚合后的 arguments = %q, want {\"a\":1}", aggregatedArgs)
	}
}

// ── TestConnection：401 鉴权失败 / 连接拒绝 ───────────────────────

func TestTestConnectionAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid api key"}}`)
	}))
	defer srv.Close()

	err := TestConnection(context.Background(), testCfg(srv.URL))
	if err == nil {
		t.Fatal("401 应返回错误")
	}
	if !strings.Contains(err.Error(), "鉴权") {
		t.Fatalf("错误信息应含“鉴权”, got %q", err.Error())
	}
}

func TestTestConnectionConnRefused(t *testing.T) {
	// 起一个 server 再立刻关闭，拿到一个保证连不上的 URL。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	err := TestConnection(context.Background(), testCfg(url))
	if err == nil {
		t.Fatal("连接拒绝应返回错误")
	}
	if !strings.Contains(err.Error(), "无法连接") {
		t.Fatalf("错误信息应含“无法连接”, got %q", err.Error())
	}
}

func TestTestConnectionNotFoundModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":{"message":"model not found"}}`)
	}))
	defer srv.Close()

	err := TestConnection(context.Background(), testCfg(srv.URL))
	if err == nil {
		t.Fatal("404 应返回错误")
	}
	if !strings.Contains(err.Error(), "模型") {
		t.Fatalf("错误信息应含“模型”, got %q", err.Error())
	}
}

func TestTestConnectionNonJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "<html>not json</html>")
	}))
	defer srv.Close()

	err := TestConnection(context.Background(), testCfg(srv.URL))
	if err == nil {
		t.Fatal("非 JSON 响应应返回错误")
	}
}

// ── POST /api/ai/test-connection：成功置 Verified、失败 502 ───────────────────────

func TestHandleTestConnectionSuccessSetsVerified(t *testing.T) {
	mux := newTestMux(t)
	fakeLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"pong"}}]}`)
	}))
	defer fakeLLM.Close()

	if err := SaveConfig(Config{BaseURL: fakeLLM.URL, Model: "test-model", MaxToolCalls: 12}); err != nil {
		t.Fatalf("SaveConfig 失败: %v", err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(t, http.MethodPost, "/api/ai/test-connection", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("test-connection 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !LoadConfig().Verified {
		t.Fatalf("成功后 Config.Verified 应为 true")
	}
}

func TestHandleTestConnectionFailureReturns502(t *testing.T) {
	mux := newTestMux(t)
	fakeLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"bad key"}}`)
	}))
	defer fakeLLM.Close()

	if err := SaveConfig(Config{BaseURL: fakeLLM.URL, Model: "test-model", MaxToolCalls: 12}); err != nil {
		t.Fatalf("SaveConfig 失败: %v", err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, authedRequest(t, http.MethodPost, "/api/ai/test-connection", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("test-connection 失败应 502, got %d body=%s", rec.Code, rec.Body.String())
	}
	if LoadConfig().Verified {
		t.Fatalf("失败后 Config.Verified 应保持 false")
	}
}

func TestTestConnectionSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"pong"}}]}`)
	}))
	defer srv.Close()

	if err := TestConnection(context.Background(), testCfg(srv.URL)); err != nil {
		t.Fatalf("应成功, got %v", err)
	}
}

// ── TestConnection 超时：5s 上限（用极慢 server 验证 ctx 超时触发，不等真 5s，用短 ctx 注入） ───

func TestTestConnectionRespectsContextTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := TestConnection(ctx, testCfg(srv.URL))
	if err == nil {
		t.Fatal("超时应返回错误")
	}
}

// ── bufio.Scanner 1MB 缓冲上限的回归保障：超长单行 SSE 不 panic ───────────────────────

func TestChatStreamHandlesLongLineWithinBuffer(t *testing.T) {
	// 构造一个接近但不超过 1MB 的 content，验证不会因为默认 64KB scanner 缓冲而出错。
	bigContent := strings.Repeat("a", 500*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		payload, _ := json.Marshal(map[string]interface{}{
			"choices": []map[string]interface{}{{"delta": map[string]interface{}{"content": bigContent}}},
		})
		fmt.Fprintf(w, "data: %s\n\n", payload)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	var got string
	err := ChatStream(context.Background(), testCfg(srv.URL), ChatRequest{Model: "test-model", Messages: []ChatMessage{{Role: "user", Content: "hi"}}, Stream: true},
		func(text string) { got += text },
		func(index int, idDelta, nameDelta, argsDelta string) {})
	if err != nil {
		t.Fatalf("ChatStream 失败: %v", err)
	}
	if got != bigContent {
		t.Fatalf("长内容未完整传递, got len=%d want len=%d", len(got), len(bigContent))
	}
}
