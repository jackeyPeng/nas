package ai

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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

// handleChat：POST /api/ai/chat。本 Task（T6）只实现非流式分支——返回
// 一次性 JSON {conversation_id, reply, tool_trace, pending_action}。
// SSE 流式分支（Accept: text/event-stream）在 Task 7 接入，按 Accept 头分流。
func handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// 进 handler 即审计：GET/POST 都要显式记（SSE 流式分支同样靠这行覆盖，
	// 不依赖全局中间件——那个只审计非 GET，SSE 本身就是 GET 触发的变体场景）。
	var req chatRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.LogAuditRequest(r, "AI", "AI对话", "请求体非法", "failed")
		http.Error(w, `{"error":"请求体非法"}`, http.StatusBadRequest)
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		common.LogAuditRequest(r, "AI", "AI对话", "message 为空", "failed")
		http.Error(w, `{"error":"message 不能为空"}`, http.StatusBadRequest)
		return
	}

	convID := req.ConversationID
	if convID == "" {
		convID = newConversationID()
	}
	common.LogAuditRequest(r, "AI", "AI对话", "conv="+convID, "pending")

	if err := EnsureConversation(convID); err != nil {
		http.Error(w, `{"error":"会话初始化失败"}`, http.StatusInternalServerError)
		return
	}

	cfg := LoadConfig()
	stored, err := LoadConversation(convID, historyWindow)
	if err != nil {
		http.Error(w, `{"error":"加载会话历史失败"}`, http.StatusInternalServerError)
		return
	}
	history := historyToChatMessages(stored)

	username := common.RequestUsername(r)

	result, err := RunAgent(r.Context(), cfg, toolRegistry, history, req.Message)
	if err != nil {
		common.LogAuditRequest(r, "AI", "AI对话", "conv="+convID+" error="+err.Error(), "failed")
		http.Error(w, `{"error":"模型调用失败: `+jsonEscape(err.Error())+`"}`, http.StatusBadGateway)
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
		http.Error(w, `{"error":"保存消息失败"}`, http.StatusInternalServerError)
		return
	}
	if err := EnsureTitle(convID, req.Message); err != nil {
		http.Error(w, `{"error":"保存会话标题失败"}`, http.StatusInternalServerError)
		return
	}
	if err := SaveMessage(convID, "assistant", result.Reply, ""); err != nil {
		http.Error(w, `{"error":"保存回复失败"}`, http.StatusInternalServerError)
		return
	}

	if result.PendingAction != nil {
		result.PendingAction.ID = newPendingActionID()
		result.PendingAction.Summary = pendingActionSummary(result.PendingAction.ToolName, result.PendingAction.ArgsJSON)
		result.PendingAction.ConfirmWord = "执行"
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

// handleListConversations：GET /api/ai/conversations，返回最近 20 个会话。
func handleListConversations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	common.LogAuditRequest(r, "AI", "查看AI会话列表", "", "success")
	list, err := ListConversations(historyWindow)
	if err != nil {
		http.Error(w, `{"error":"读取会话列表失败"}`, http.StatusInternalServerError)
		return
	}
	common.JSONResponse(w, map[string]interface{}{"conversations": list})
}

// handleGetConversation：GET /api/ai/conversations/{id}，返回该会话最近
// 50 条消息（与计划定义的 LoadConversation 默认窗口一致，独立于送进 agent
// loop 的 20 条历史窗口——这个端点是给前端回显整段对话用的）。
func handleGetConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	convID := strings.TrimPrefix(r.URL.Path, "/api/ai/conversations/")
	convID = strings.TrimSuffix(convID, "/")
	if convID == "" {
		http.Error(w, `{"error":"缺少会话 ID"}`, http.StatusBadRequest)
		return
	}
	common.LogAuditRequest(r, "AI", "查看AI会话", "conv="+convID, "success")
	stored, err := LoadConversation(convID, 50)
	if err != nil {
		http.Error(w, `{"error":"读取会话失败"}`, http.StatusInternalServerError)
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

// pendingActionSummary 生成待确认操作卡的人类可读摘要。本 Task 没有写
// 工具实现（Task 10 才有 nasWriteTools），这里给出通用兜底：工具名 +
// 参数摘要；Task 10 接入具体写工具时可在这里按 ToolName 扩展专用文案
// （计划里 ai.action_summary_* 的 i18n 键对应关系）。
func pendingActionSummary(toolName, argsJSON string) string {
	return "即将执行写操作「" + toolName + "」，参数：" + digest(argsJSON)
}
