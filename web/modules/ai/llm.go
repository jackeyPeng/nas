package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"nas-panel/common"
)

// ── OpenAI-compatible 协议类型 ─────────────────────────────

// ChatMessage 对齐 OpenAI /chat/completions 的消息体，同时覆盖 tool 角色
// 回填（ToolCallID/Name）与 assistant 发起的工具调用（ToolCalls）。
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall 是 assistant 消息里携带的一次工具调用请求。
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ChatRequest 是发给 /chat/completions 的请求体（Tools 为 Registry.SchemaForLLM() 输出）。
type ChatRequest struct {
	Model    string                   `json:"model"`
	Messages []ChatMessage            `json:"messages"`
	Tools    []map[string]interface{} `json:"tools,omitempty"`
	Stream   bool                     `json:"stream,omitempty"`
}

// chatCompletionResponse 是非流式响应体的最小解析形态（只取第一个 choice）。
type chatCompletionResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// streamChunk 是 SSE data: 行的最小解析形态。
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
}

const (
	// maxResponseBodyBytes 限制非流式响应体读取上限，防止恶意/异常端点把内存打爆。
	maxResponseBodyBytes = 8 * 1024 * 1024
	// maxSSELineBytes 是 bufio.Scanner 逐行解析 SSE 的缓冲上限。
	maxSSELineBytes = 1024 * 1024
	// testConnectionTimeout 是连通性测试的硬超时。
	testConnectionTimeout = 5 * time.Second
)

// chatCompletionsURL 拼接 baseURL + /chat/completions，容忍 baseURL 带尾斜杠。
func chatCompletionsURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/chat/completions"
}

func newChatHTTPRequest(ctx context.Context, cfg Config, req ChatRequest) (*http.Request, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, chatCompletionsURL(cfg.BaseURL), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	return httpReq, nil
}

// ChatOnce 发一次非流式 /chat/completions 请求，返回第一个 choice 的 message。
func ChatOnce(ctx context.Context, cfg Config, req ChatRequest) (*ChatMessage, error) {
	req.Stream = false
	httpReq, err := newChatHTTPRequest(ctx, cfg, req)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, classifyNetworkError(err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return nil, err
	}

	var parsed chatCompletionResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("响应解析失败（非 JSON 或格式不符）: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apiErrorFromStatus(resp.StatusCode, &parsed)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("响应不含任何 choice")
	}
	msg := parsed.Choices[0].Message
	return &msg, nil
}

// ChatStream 发一次流式 /chat/completions 请求，逐行解析 SSE data: 帧。
// onDelta 收到每个 content 增量；onToolCallDelta 收到工具调用的聚合原料
// （index 定位到第几个 tool_call，id/name/arguments 均为该帧携带的增量，
// 调用方负责按 index 累加聚合，本函数不做聚合）。
func ChatStream(ctx context.Context, cfg Config, req ChatRequest, onDelta func(text string), onToolCallDelta func(index int, idDelta, nameDelta, argsDelta string)) error {
	req.Stream = true
	httpReq, err := newChatHTTPRequest(ctx, cfg, req)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return classifyNetworkError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
		var parsed chatCompletionResponse
		json.Unmarshal(data, &parsed)
		return apiErrorFromStatus(resp.StatusCode, &parsed)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSELineBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			// 单帧解析失败不中断整个流（容忍协议噪音/心跳），继续下一行。
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != "" && onDelta != nil {
			onDelta(delta.Content)
		}
		if onToolCallDelta != nil {
			for _, tc := range delta.ToolCalls {
				onToolCallDelta(tc.Index, tc.ID, tc.Function.Name, tc.Function.Arguments)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取流式响应失败: %w", err)
	}
	return nil
}

// TestConnection 发一条最小 ping 消息验证端点可用，5s 超时；返回带语义的中文错误。
func TestConnection(ctx context.Context, cfg Config) error {
	ctx, cancel := context.WithTimeout(ctx, testConnectionTimeout)
	defer cancel()
	_, err := ChatOnce(ctx, cfg, ChatRequest{
		Model:    cfg.Model,
		Messages: []ChatMessage{{Role: "user", Content: "ping"}},
	})
	return err
}

// classifyNetworkError 把 http.Client 的传输层错误（连接拒绝/超时/DNS 等）
// 归一成带中文语义的错误，供 TestConnection 的错误文案契约使用。
func classifyNetworkError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "Client.Timeout") {
		return fmt.Errorf("连接超时: %w", err)
	}
	return fmt.Errorf("无法连接到模型端点: %w", err)
}

// apiErrorFromStatus 把非 2xx 状态码归一成带中文语义的错误。
func apiErrorFromStatus(status int, parsed *chatCompletionResponse) error {
	detail := ""
	if parsed != nil && parsed.Error != nil && parsed.Error.Message != "" {
		detail = "：" + parsed.Error.Message
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("鉴权失败（API key 无效或权限不足）%s", detail)
	case http.StatusNotFound:
		return fmt.Errorf("模型不存在或端点路径错误%s", detail)
	default:
		return fmt.Errorf("模型端点返回错误状态 %d%s", status, detail)
	}
}

// handleTestConnection 调 TestConnection 验证当前保存的端点配置；成功则置位
// Config.Verified 并落盘（开关 handleToggle 依赖这个标记才允许开启），
// 失败回 502 + 中文原因（不挂 requireEnabled：配置阶段功能本身还没开启）。
func handleTestConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := LoadConfig()
	if cfg.BaseURL == "" || cfg.Model == "" {
		jsonError(w, "请先填写模型端点和模型名称", http.StatusBadRequest)
		return
	}
	if err := TestConnection(r.Context(), cfg); err != nil {
		common.LogAuditRequest(r, "AI", "连通性测试", "model="+cfg.Model, "failed")
		jsonError(w, err.Error(), http.StatusBadGateway)
		return
	}
	cfg.Verified = true
	if err := SaveConfig(cfg); err != nil {
		jsonError(w, "保存配置失败", http.StatusInternalServerError)
		return
	}
	common.LogAuditRequest(r, "AI", "连通性测试", "model="+cfg.Model, "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}
