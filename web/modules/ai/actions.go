package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"nas-panel/common"
	"nas-panel/modules/users"
)

// pendingActionTTL 是待确认操作卡的存活时长：超过这个时长 Take 视为不存在
// （惰性过期——不开后台 goroutine 清理，命中时才判断，零足迹模块的一部分）。
const pendingActionTTL = 5 * time.Minute

// pendingActionConfirmWord 是确认门的 type-to-confirm 确认词（服务端下发时
// 存的值，对齐前端 i18n ai.confirm_word 的 zh 值——服务端/前端不走各自
// 独立的字面量，前端展示的是服务端下发的 ConfirmWord 字段本身，这里只是
// "服务端生成时填什么"的唯一来源）。
const pendingActionConfirmWord = "执行"

// PendingAction 是写工具被模型请求、但尚未执行的待确认操作卡。
// 参数不经客户端回传（防篡改）——服务端只把 ID 交给前端，前端确认时
// 带 ID + ConfirmWord 回来，参数全程留在服务端内存里。
type PendingAction struct {
	ID          string    `json:"id"`
	ToolName    string    `json:"tool_name"`
	ArgsJSON    string    `json:"args_json"`
	Summary     string    `json:"summary"`
	ConfirmWord string    `json:"confirm_word"`
	CreatedAt   time.Time `json:"created_at"`
}

// PendingStore 是待确认操作卡的服务端存储接口。
type PendingStore interface {
	// Put 存入一个待确认操作卡（覆盖同 ID 的旧值）。
	Put(action PendingAction) error
	// Take 取出并删除（一次性）：命中且未过期返回 (action, true)；
	// 不存在或已过期返回 (zero值, false)。
	Take(id string) (PendingAction, bool)
}

// memoryPendingStore 是 PendingStore 的内存实现：sync.Map + 惰性 TTL 清理。
// 面板单管理员、单进程，内存存储足够；进程重启丢失待确认卡是可接受的
// （前端重新发起写请求即可再生成一张）。
type memoryPendingStore struct {
	m sync.Map // id -> PendingAction
}

// newMemoryPendingStore 构造一个空的内存 PendingStore。
func newMemoryPendingStore() *memoryPendingStore {
	return &memoryPendingStore{}
}

func (s *memoryPendingStore) Put(action PendingAction) error {
	s.m.Store(action.ID, action)
	return nil
}

func (s *memoryPendingStore) Take(id string) (PendingAction, bool) {
	v, ok := s.m.LoadAndDelete(id)
	if !ok {
		return PendingAction{}, false
	}
	action := v.(PendingAction)
	if time.Since(action.CreatedAt) > pendingActionTTL {
		return PendingAction{}, false
	}
	return action, true
}

// defaultPendingStore 是进程级单例，chat.go 的 handleChat 用它承接 agent loop
// 产出的 PendingAction；确认/取消端点同样消费这个单例。
var defaultPendingStore PendingStore = newMemoryPendingStore()

// confirmActionRequest 是 POST /api/ai/action/confirm 的请求体。
// client_payload 只承载 create_user 的一次性生成密码——pending action 的
// 其余参数全部按 action_id 从服务端 PendingStore 取出，不经客户端回传
// （硬约束：防篡改）。
type confirmActionRequest struct {
	ActionID      string                `json:"action_id"`
	ConfirmWord   string                `json:"confirm_word"`
	ClientPayload *confirmClientPayload `json:"client_payload,omitempty"`
}

// confirmClientPayload 目前只有 password 一个字段（create_user 专用）。
type confirmClientPayload struct {
	Password string `json:"password,omitempty"`
}

// cancelActionRequest 是 POST /api/ai/action/cancel 的请求体。
type cancelActionRequest struct {
	ActionID string `json:"action_id"`
}

// handleActionConfirm：POST /api/ai/action/confirm。Take(action_id) 校验
// TTL（一次性——Take 本身就是取出即删，二次确认同一 id 必然落到"不存在"分支）；
// confirm_word 必须等于服务端下发时存的值；create_user 走专用分支注入
// client_payload.password；其余工具直调 tool.Exec（args 来自服务端存储，
// 不接受客户端覆盖）。执行成功/失败都走 LogAuditRequest 富 detail。
func handleActionConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req confirmActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"请求体非法"}`, http.StatusBadRequest)
		return
	}
	if req.ActionID == "" {
		http.Error(w, `{"error":"action_id 必填"}`, http.StatusBadRequest)
		return
	}

	action, ok := defaultPendingStore.Take(req.ActionID)
	if !ok {
		common.LogAuditRequest(r, "AI", "确认AI写操作", "action_id="+req.ActionID+" 不存在或已过期", "failed")
		http.Error(w, `{"error":"操作已过期或不存在，请重新发起请求"}`, http.StatusGone)
		return
	}

	if req.ConfirmWord != action.ConfirmWord {
		common.LogAuditRequest(r, "AI", "确认AI写操作", sanitizeArgsDigest(action.ToolName, action.ArgsJSON)+" 确认词错误", "failed")
		http.Error(w, `{"error":"确认词不正确"}`, http.StatusBadRequest)
		return
	}

	result, err := executeConfirmedAction(r.Context(), action, req.ClientPayload)

	// 审计 detail：工具名+参数摘要（sanitizeArgsDigest 已做敏感字段脱敏，
	// create_user 的密码永不进这里——它根本不在 action.ArgsJSON 里，
	// ArgsJSON 是 ValidateArgs 过的 schema 参数，schema 里压根没有 password）。
	detail := sanitizeArgsDigest(action.ToolName, action.ArgsJSON)
	if err != nil {
		common.LogAuditRequest(r, "AI", "确认AI写操作", detail+" error="+jsonEscape(err.Error()), "failed")
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
		return
	}
	common.LogAuditRequest(r, "AI", "确认AI写操作", detail, "success")
	common.JSONResponse(w, map[string]interface{}{
		"tool_name": action.ToolName,
		"result":    result,
	})
}

// handleActionCancel：POST /api/ai/action/cancel。Take 后丢弃，不执行、
// 不留痕迹（审计仍记一条"取消"，用于追溯模型曾经请求过什么写操作）。
func handleActionCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req cancelActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"请求体非法"}`, http.StatusBadRequest)
		return
	}
	if req.ActionID == "" {
		http.Error(w, `{"error":"action_id 必填"}`, http.StatusBadRequest)
		return
	}
	action, ok := defaultPendingStore.Take(req.ActionID)
	if !ok {
		// 已过期/不存在也视为取消成功——用户意图（不执行）已经达成。
		common.JSONResponse(w, map[string]interface{}{"cancelled": true})
		return
	}
	common.LogAuditRequest(r, "AI", "取消AI写操作", sanitizeArgsDigest(action.ToolName, action.ArgsJSON), "success")
	common.JSONResponse(w, map[string]interface{}{"cancelled": true})
}

// executeConfirmedAction 是确认门之后真正执行写工具的唯一入口。
// create_user 是特例：args 里没有 password（schema 使然），密码从
// clientPayload 取出、经 common.ValidSecret 校验后直接传给
// users.CreateUserWithPassword，全程不经过 ValidateArgs/args map，
// 也不会被塞进任何会落库/审计/日志的结构里。
func executeConfirmedAction(ctx context.Context, action PendingAction, clientPayload *confirmClientPayload) (string, error) {
	tool, ok := toolRegistry.Find(action.ToolName)
	if !ok {
		return "", fmt.Errorf("未知工具 %s", action.ToolName)
	}

	args, err := ValidateArgs(tool, action.ArgsJSON)
	if err != nil {
		return "", fmt.Errorf("参数校验失败: %w", err)
	}

	if action.ToolName == "create_user" {
		return executeCreateUserConfirmed(args, clientPayload)
	}

	return tool.Exec(ctx, args)
}

// executeCreateUserConfirmed 是 create_user 的专用执行路径：password 只能
// 来自 clientPayload（前端确认时生成），不接受任何其他来源；
// common.ValidSecret 拒绝换行/控制字符（防 chpasswd stdin 协议注入），
// 长度下限对齐 handleCreateUser 现有策略（>=12 位）。
func executeCreateUserConfirmed(args map[string]interface{}, clientPayload *confirmClientPayload) (string, error) {
	if clientPayload == nil || clientPayload.Password == "" {
		return "", fmt.Errorf("create_user 必须提供 client_payload.password")
	}
	password := clientPayload.Password
	if len(password) < 12 {
		return "", fmt.Errorf("密码至少12位")
	}
	if err := common.ValidSecret(password); err != nil {
		return "", err
	}

	username, _ := args["username"].(string)
	svcSamba := argBool(args, "svc_samba")
	svcFtp := argBool(args, "svc_ftp")
	svcWebdav := argBool(args, "svc_webdav")

	steps, err := users.CreateUserWithPassword(username, password, svcSamba, svcFtp, svcWebdav)
	if err != nil {
		return "", err
	}
	out, merr := json.Marshal(map[string]interface{}{
		"message":  fmt.Sprintf("用户 %s 创建成功", username),
		"username": username,
		"steps":    steps,
	})
	if merr != nil {
		return `{"message":"用户创建成功"}`, nil
	}
	return string(out), nil
}
