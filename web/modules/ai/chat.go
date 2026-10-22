package ai

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"nas-panel/common"
)

// nowFunc 可在测试里替换，验证 PendingAction.CreatedAt/TTL 相关行为时
// 不依赖真实时钟（当前测试未用到这个能力，但对齐其余模块"时间可注入"
// 的一贯测试模式，预留）。
var nowFunc = time.Now

// historyWindow 是载入 agent loop 的会话历史消息条数上限（最近 N 条）。
const historyWindow = 20

// chatRequestBody 是 POST /api/ai/chat 的请求体：conversation_id 为空时
// 新建会话，否则沿用已有会话继续对话。
type chatRequestBody struct {
	ConversationID string `json:"conversation_id"`
	Message        string `json:"message"`
}

// chatResponseBody 是非流式分支的响应体（SSE 分支见 Task 7）。
type chatResponseBody struct {
	ConversationID string           `json:"conversation_id"`
	Reply          string           `json:"reply"`
	ToolTrace      []ToolTraceEntry `json:"tool_trace"`
	PendingAction  *PendingAction   `json:"pending_action,omitempty"`
}

// newConversationID 生成一个随机会话 ID（16 字节十六进制，128 bit 熵，
// 不依赖时间戳防冲突——与 common 包其余 ID 生成风格一致地够用且简单）。
func newConversationID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 失败概率极低；兜底仍返回一个可用但较弱的占位值，
		// 不让整个请求因为 ID 生成失败而 500。
		return "conv-fallback"
	}
	return "conv-" + hex.EncodeToString(buf)
}

// newPendingActionID 生成一个随机的待确认操作卡 ID。
func newPendingActionID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "action-fallback"
	}
	return "action-" + hex.EncodeToString(buf)
}

// historyToChatMessages 把落库的 StoredMessage 还原成 ChatMessage，供拼进
// 发给 LLM 的请求历史。role=assistant 且带 tool_calls 的消息要把 ToolCalls
// 字段还原，否则下一轮模型看不到自己此前请求过哪些工具。
func historyToChatMessages(stored []StoredMessage) []ChatMessage {
	out := make([]ChatMessage, 0, len(stored))
	for _, m := range stored {
		cm := ChatMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		if m.ToolCalls != "" {
			cm.ToolCalls = toolCallsFromJSON(m.ToolCalls)
		}
		out = append(out, cm)
	}
	return out
}

// handleChat：POST /api/ai/chat。按 Accept: text/event-stream 分两支：
// 非流式（T6）返回一次性 JSON；SSE 流式（T7）逐事件推送，见 handleChatSSE。
func handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req chatRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.LogAuditRequest(r, "AI", "AI对话", "请求体非法", "failed")
		jsonError(w, "请求体非法", http.StatusBadRequest)
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		common.LogAuditRequest(r, "AI", "AI对话", "message 为空", "failed")
		jsonError(w, "message 不能为空", http.StatusBadRequest)
		return
	}

	convID := req.ConversationID
	if convID == "" {
		convID = newConversationID()
	}
	// 进 handler 即审计：GET/POST 都要显式记（SSE 本身是 POST 触发，但同样
	// 走这里——不依赖全局中间件，因为中间件只审非 GET，而且这里需要富 detail）。
	common.LogAuditRequest(r, "AI", "AI对话", "conv="+convID, "pending")

	if err := EnsureConversation(convID); err != nil {
		jsonError(w, "会话初始化失败", http.StatusInternalServerError)
		return
	}

	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		handleChatSSE(w, r, convID, req.Message)
		return
	}

	cfg := LoadConfig()
	stored, err := LoadConversation(convID, historyWindow)
	if err != nil {
		jsonError(w, "加载会话历史失败", http.StatusInternalServerError)
		return
	}
	history := historyToChatMessages(stored)

	username := common.RequestUsername(r)

	result, err := RunAgent(r.Context(), cfg, toolRegistry, history, req.Message)
	if err != nil {
		common.LogAuditRequest(r, "AI", "AI对话", "conv="+convID+" error="+err.Error(), "failed")
		jsonError(w, "模型调用失败: "+err.Error(), http.StatusBadGateway)
		return
	}

	ip := common.RequestIP(r)
	for _, trace := range result.ToolTrace {
		common.LogAudit(username, "AI_TOOL", "AI", r.URL.Path, sanitizeArgsDigest(trace.Name, trace.ArgsJSON), "success", ip)
	}

	// 落库：用户消息 + 本轮助手产出的消息。工具调用轨迹已经在 RunAgent 内部
	// 经由 ChatOnce 的多轮请求/响应完成，这里只持久化这一轮对话对用户可见的
	// 首尾两条消息（user 请求 + assistant 最终回复），足够下一轮历史回放。
	if err := SaveMessage(convID, "user", req.Message, ""); err != nil {
		jsonError(w, "保存消息失败", http.StatusInternalServerError)
		return
	}
	if err := EnsureTitle(convID, req.Message); err != nil {
		jsonError(w, "保存会话标题失败", http.StatusInternalServerError)
		return
	}
	if err := SaveMessage(convID, "assistant", result.Reply, ""); err != nil {
		jsonError(w, "保存回复失败", http.StatusInternalServerError)
		return
	}

	if result.PendingAction != nil {
		result.PendingAction.ID = newPendingActionID()
		result.PendingAction.Summary = pendingActionSummary(result.PendingAction.ToolName, result.PendingAction.ArgsJSON)
		result.PendingAction.ConfirmWord = pendingActionConfirmWord
		result.PendingAction.CreatedAt = nowFunc()
		defaultPendingStore.Put(*result.PendingAction)
	}

	common.LogAuditRequest(r, "AI", "AI对话", "conv="+convID, "success")
	common.JSONResponse(w, chatResponseBody{
		ConversationID: convID,
		Reply:          result.Reply,
		ToolTrace:      result.ToolTrace,
		PendingAction:  result.PendingAction,
	})
}

// sseEvent 是每一行 `data: <json>\n\n` 的通用外壳；Type 决定其余字段的
// 含义，按计划定义的事件协议逐字段实现：
//   - delta：{"type":"delta","text":...}
//   - tool_call：{"type":"tool_call","name":...,"args_digest":...}
//   - tool_result：{"type":"tool_result","name":...,"digest":...}
//   - pending_action：{"type":"pending_action", ...PendingAction 全字段}
//   - done：{"type":"done","conversation_id":...}
//   - error：{"type":"error","message":...}
type sseEvent struct {
	Type           string `json:"type"`
	Text           string `json:"text,omitempty"`
	Name           string `json:"name,omitempty"`
	ArgsDigest     string `json:"args_digest,omitempty"`
	Digest         string `json:"digest,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	Message        string `json:"message,omitempty"`

	// pending_action 事件内联 PendingAction 的全部字段（计划："...PendingAction
	// 全字段"）；嵌入指针字段让 json.Marshal 把这些字段摊平到顶层，与
	// delta/tool_call 等事件共享同一个 sseEvent 类型、同一次 json.Marshal
	// 调用，避免手写两套序列化路径。为 nil 时（绝大多数事件类型）这些字段
	// 不会出现在输出里。
	*PendingAction `json:",omitempty"`
}

// sendSSEEvent 按 "data: <json>\n\n" 格式写一行 SSE 事件并立即 Flush，
// 对齐 diskmgmt/stream.go 的 sendProgress 模式。
func sendSSEEvent(w http.ResponseWriter, ev sseEvent) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// handleChatSSE 是 POST /api/ai/chat 的流式分支：RunAgentStream 替代
// RunAgent，工具执行间隙即时发事件。convID/message 已在 handleChat 校验过
// （非空、会话已 Ensure），这里只管编排与推流。
func handleChatSSE(w http.ResponseWriter, r *http.Request, convID, message string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	cfg := LoadConfig()
	stored, err := LoadConversation(convID, historyWindow)
	if err != nil {
		sendSSEEvent(w, sseEvent{Type: "error", Message: "加载会话历史失败"})
		return
	}
	history := historyToChatMessages(stored)

	username := common.RequestUsername(r)
	ip := common.RequestIP(r)

	hooks := AgentStreamHooks{
		OnDelta: func(text string) {
			sendSSEEvent(w, sseEvent{Type: "delta", Text: text})
		},
		OnToolCall: func(name, argsJSON string) {
			sendSSEEvent(w, sseEvent{Type: "tool_call", Name: name, ArgsDigest: digest(argsJSON)})
		},
		OnToolResult: func(name, result string) {
			sendSSEEvent(w, sseEvent{Type: "tool_result", Name: name, Digest: digest(result)})
		},
	}

	result, err := RunAgentStream(r.Context(), cfg, toolRegistry, history, message, hooks)
	if err != nil {
		common.LogAuditRequest(r, "AI", "AI对话", "conv="+convID+" error="+err.Error(), "failed")
		sendSSEEvent(w, sseEvent{Type: "error", Message: "模型调用失败: " + err.Error()})
		return
	}

	// 工具调用审计：逐条记录（detail=工具名+脱敏后的参数摘要），数据来源是
	// RunAgentStream 返回的 ToolTrace（与 SSE tool_call/tool_result 事件
	// 同一份工具调用记录，不是另外重新收集）。
	for _, trace := range result.ToolTrace {
		common.LogAudit(username, "AI_TOOL", "AI", r.URL.Path, sanitizeArgsDigest(trace.Name, trace.ArgsJSON), "success", ip)
	}

	if err := SaveMessage(convID, "user", message, ""); err != nil {
		sendSSEEvent(w, sseEvent{Type: "error", Message: "保存消息失败"})
		return
	}
	if err := EnsureTitle(convID, message); err != nil {
		sendSSEEvent(w, sseEvent{Type: "error", Message: "保存会话标题失败"})
		return
	}
	if err := SaveMessage(convID, "assistant", result.Reply, ""); err != nil {
		sendSSEEvent(w, sseEvent{Type: "error", Message: "保存回复失败"})
		return
	}

	if result.PendingAction != nil {
		result.PendingAction.ID = newPendingActionID()
		result.PendingAction.Summary = pendingActionSummary(result.PendingAction.ToolName, result.PendingAction.ArgsJSON)
		result.PendingAction.ConfirmWord = pendingActionConfirmWord
		result.PendingAction.CreatedAt = nowFunc()
		defaultPendingStore.Put(*result.PendingAction)
		sendSSEEvent(w, sseEvent{Type: "pending_action", PendingAction: result.PendingAction})
	}

	common.LogAuditRequest(r, "AI", "AI对话", "conv="+convID, "success")
	sendSSEEvent(w, sseEvent{Type: "done", ConversationID: convID})
}

// handleListConversations：GET /api/ai/conversations，返回最近 20 个会话。
func handleListConversations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	common.LogAuditRequest(r, "AI", "查看AI会话列表", "", "success")
	list, err := ListConversations(historyWindow)
	if err != nil {
		jsonError(w, "读取会话列表失败", http.StatusInternalServerError)
		return
	}
	common.JSONResponse(w, map[string]interface{}{"conversations": list})
}

// handleGetConversation：GET /api/ai/conversations/{id}，返回该会话最近
// 50 条消息（与计划定义的 LoadConversation 默认窗口一致，独立于送进 agent
// loop 的 20 条历史窗口——这个端点是给前端回显整段对话用的）。
func handleGetConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	convID := strings.TrimPrefix(r.URL.Path, "/api/ai/conversations/")
	convID = strings.TrimSuffix(convID, "/")
	if convID == "" {
		jsonError(w, "缺少会话 ID", http.StatusBadRequest)
		return
	}
	common.LogAuditRequest(r, "AI", "查看AI会话", "conv="+convID, "success")
	stored, err := LoadConversation(convID, 50)
	if err != nil {
		jsonError(w, "读取会话失败", http.StatusInternalServerError)
		return
	}
	common.JSONResponse(w, map[string]interface{}{"conversation_id": convID, "messages": stored})
}

// jsonEscape 把一个 Go 字符串变成可以安全嵌进手写 JSON 字面量里的片段
// （去掉双引号/反斜杠/换行——这里只用在极少数手写 JSON 错误响应里，正常
// 响应全部走 common.JSONResponse/json.Marshal，不手搓 JSON 字符串）。
func jsonEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// pendingActionSummary 生成待确认操作卡的人类可读摘要：按 ToolName 给出
// 专用文案（对应前端 i18n ai.action_summary_{tool} 键描述的内容，摘要本身
// 由服务端生成并随 PendingAction.Summary 下发，前端直接展示，不需要
// 自己拼参数）。解析失败（argsJSON 非法，理论上不会发生——到这里之前
// 已经过 ValidateArgs）时退化为通用兜底文案。
func pendingActionSummary(toolName, argsJSON string) string {
	var args map[string]interface{}
	_ = json.Unmarshal([]byte(argsJSON), &args)
	str := func(k string) string {
		v, _ := args[k].(string)
		return v
	}
	num := func(k string) string {
		v, ok := args[k].(float64)
		if !ok {
			return "0"
		}
		return fmt.Sprintf("%d", int(v))
	}
	svcOn := func(k string) string {
		if b, ok := args[k].(bool); ok && b {
			return "开"
		}
		return "关"
	}

	switch toolName {
	case "set_user_quota":
		return fmt.Sprintf("设置用户「%s」的私有目录配额为 %sGB", str("username"), num("quota_gb"))
	case "create_user":
		return fmt.Sprintf("创建用户「%s」（Samba:%s FTP:%s WebDAV:%s），密码将由你在确认时生成", str("username"), svcOn("svc_samba"), svcOn("svc_ftp"), svcOn("svc_webdav"))
	case "add_firewall_rule":
		return fmt.Sprintf("添加防火墙规则：端口 %s/%s，来源 %s", str("port"), orDefault(str("proto"), "tcp"), orDefault(str("from"), "任意"))
	case "restart_service":
		return fmt.Sprintf("重启服务「%s」", str("service"))
	case "run_diagnostics":
		return fmt.Sprintf("运行诊断项「%s」", str("item"))
	case "create_folder":
		return fmt.Sprintf("在存储池「%s」下创建文件夹「%s」（权限：%s）", str("pool"), str("name"), orDefault(str("permission"), "readwrite"))
	default:
		return "即将执行写操作「" + toolName + "」，参数：" + digest(argsJSON)
	}
}

// orDefault 在 s 为空时返回 def，否则返回 s 本身。
func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
