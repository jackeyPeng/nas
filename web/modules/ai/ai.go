package ai

import (
	"encoding/json"
	"net/http"

	"nas-panel/common"
)

// buildRegistry 组装 agent loop 使用的工具注册表：nas 域的只读工具
// （M1/T5）+ 写工具（M2/T10）+ 任务工具（F1 T5，plan/start/cancel/pause/
// resume/list/detail）。RunAgent/handleChat 只认 *Registry，写工具的
// Write=true 标记让 agent loop 自动转 PendingAction，不需要调用方额外
// 区分。
func buildRegistry() *Registry {
	tools := append(nasReadTools(), nasWriteTools()...)
	tools = append(tools, nasTasksTools()...)
	return NewRegistry(Domain{
		Name:  "nas",
		Tools: tools,
	})
}

// toolRegistry 是进程级单例：工具清单编译期固定，不需要每次请求重新构建。
var toolRegistry = buildRegistry()

// jsonError 统一错误响应体的 Content-Type：原先 modules/ai 下大量 handler
// 直调 http.Error 手拼 `{"error":"..."}` 字符串，但 http.Error 固定写
// text/plain，导致前端 api() 的 JSON 自动 toast 守卫（按 Content-Type 判断）
// 失效——错误体虽然长得像 JSON，实际走的是字符串兜底分支（见
// app.js testAIConnection 的 pitfall 2c）。这里统一设
// application/json 并用 json.Marshal 生成体，msg 含用户可控/动态内容
// （比如上游 LLM 错误原文）时也能被正确转义，不再需要调用方自己处理 %q。
func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	body, err := json.Marshal(map[string]string{"error": msg})
	if err != nil {
		// json.Marshal 对 map[string]string 实际不会失败；兜底仍给出合法 JSON。
		body = []byte(`{"error":"内部错误"}`)
	}
	w.Write(body)
}

// RegisterRoutes 注册 ai 模块的全部 HTTP 路由。
// AI 助手是默认关闭的功能模块：requireEnabled 只包裹**功能面**端点
// （chat/conversations/action）；**控制面**端点（status/config/toggle/
// test-connection）豁免——关闭态必须能读写配置与测连通性，否则永远无法
// 完成首次开启（R4 前端联调发现的设计缺口，parent 裁定：控制面不跑 AI
// 功能不调 LLM 会话，豁免不破坏"关闭零足迹"语义）。
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/ai/status", common.AuthMiddleware(handleStatus))
	mux.HandleFunc("/api/ai/config", common.AuthMiddleware(handleConfig))
	mux.HandleFunc("/api/ai/toggle", common.AuthMiddleware(handleToggle))
	// test-connection 同 toggle 例外：配置阶段（功能尚未开启）就需要能测连通性。
	mux.HandleFunc("/api/ai/test-connection", common.AuthMiddleware(handleTestConnection))

	mux.HandleFunc("/api/ai/chat", common.AuthMiddleware(requireEnabled(handleChat)))
	mux.HandleFunc("/api/ai/conversations", common.AuthMiddleware(requireEnabled(handleListConversations)))
	mux.HandleFunc("/api/ai/conversations/", common.AuthMiddleware(requireEnabled(handleGetConversation)))

	// 确认执行门（M2/T10）：action_id 按服务端 PendingStore 取参数执行，
	// 参数不经客户端回传；create_user 的密码走 client_payload 专用通道。
	mux.HandleFunc("/api/ai/action/confirm", common.AuthMiddleware(requireEnabled(handleActionConfirm)))
	mux.HandleFunc("/api/ai/action/cancel", common.AuthMiddleware(requireEnabled(handleActionCancel)))
}

// aiEnabled 读内存缓存的配置，避免每请求读盘；SaveConfig 刷新缓存后即时生效。
func aiEnabled() bool {
	return LoadConfig().Enabled
}

// IsEnabled 是 aiEnabled 的包级薄导出（不改行为），供其它模块接入同一个
// enabled 守卫——tasks 模块的 worker/REST 关断守卫即走这里（main.go:
// tasks.RegisterRoutes(mux, ai.IsEnabled)），保持"AI 关=任务引擎也零足迹"
// 的语义单一来源，不在 tasks 里重复判断配置。
func IsEnabled() bool {
	return aiEnabled()
}

// requireEnabled 包裹需要功能模块已开启才能访问的 handler；
// 未启用时统一返回 403，不执行任何后续逻辑（零足迹语义的一部分）。
func requireEnabled(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !aiEnabled() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "AI 功能未启用"})
			return
		}
		next(w, r)
	}
}

// handleStatus 是唯一免 enabled 守卫的端点：告知前端是否已开启/已配置，
// 永不回显 key（哪怕掩码也不给，入口页只需要布尔态）。
func handleStatus(w http.ResponseWriter, r *http.Request) {
	cfg := LoadConfig()
	configured := cfg.BaseURL != "" && cfg.Model != ""
	common.JSONResponse(w, map[string]interface{}{
		"enabled":    cfg.Enabled,
		"configured": configured,
		"model":      cfg.Model,
		"preset":     cfg.Preset,
	})
}

// handleConfig：GET 掩码回显当前配置；POST 保存新配置（本 Task 只做保存+
// 掩码回显，连通性预检在 Task 2 接入）。
func handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := LoadConfig()
		common.JSONResponse(w, map[string]interface{}{
			"preset":         cfg.Preset,
			"base_url":       cfg.BaseURL,
			"api_key":        MaskAPIKey(cfg.APIKey),
			"model":          cfg.Model,
			"max_tool_calls": cfg.MaxToolCalls,
			"verified":       cfg.Verified,
		})
	case http.MethodPost:
		handleConfigSave(w, r)
	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type configSaveRequest struct {
	Preset       string `json:"preset"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key"`
	Model        string `json:"model"`
	MaxToolCalls int    `json:"max_tool_calls"`
}

func handleConfigSave(w http.ResponseWriter, r *http.Request) {
	var req configSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求体非法", http.StatusBadRequest)
		return
	}
	prev := LoadConfig()
	next := Config{
		Enabled:      prev.Enabled,
		Preset:       req.Preset,
		BaseURL:      req.BaseURL,
		APIKey:       req.APIKey,
		Model:        req.Model,
		MaxToolCalls: req.MaxToolCalls,
	}
	// 空值/掩码回显一律视为"未修改"：前端保存成功后会清空表单里的
	// api_key（避免真 key 常驻 DOM/内存），GET /api/ai/config 回显的也是
	// MaskAPIKey 掩码串而非原文——这两种值重新 POST 回来都不代表用户真的
	// 改了 key，必须保留 prev.APIKey，否则会把真 key 覆盖成空值或掩码串
	// 字面量（用户实报的 bug：保存后测连通性必 401）。
	if next.APIKey == "" || next.APIKey == MaskAPIKey(prev.APIKey) {
		next.APIKey = prev.APIKey
	}
	if next.MaxToolCalls <= 0 {
		next.MaxToolCalls = 12
	}
	// 端点/key/model 任一变更即清除 Verified——开启条件要求重新测试通过。
	if next.BaseURL == prev.BaseURL && next.APIKey == prev.APIKey && next.Model == prev.Model {
		next.Verified = prev.Verified
	} else {
		next.Verified = false
	}
	if err := SaveConfig(next); err != nil {
		jsonError(w, "保存配置失败", http.StatusInternalServerError)
		return
	}
	common.LogAuditRequest(r, "AI", "保存AI配置", "preset="+next.Preset+" model="+next.Model, "success")
	common.JSONResponse(w, map[string]interface{}{
		"preset":         next.Preset,
		"base_url":       next.BaseURL,
		"api_key":        MaskAPIKey(next.APIKey),
		"model":          next.Model,
		"max_tool_calls": next.MaxToolCalls,
		"verified":       next.Verified,
	})
}

type toggleRequest struct {
	Enabled bool `json:"enabled"`
}

// handleToggle 不挂 requireEnabled——开关必须在关闭态也能调用才能打开。
// 开启要求：配置已填（BaseURL+Model 非空）且 Verified=true（已通过连通性测试）。
// 关闭：始终允许，且不清空已保存的 endpoint/key 配置（重新打开即恢复）。
func handleToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req toggleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "请求体非法", http.StatusBadRequest)
		return
	}

	cfg := LoadConfig()
	if req.Enabled {
		configured := cfg.BaseURL != "" && cfg.Model != ""
		if !configured || !cfg.Verified {
			jsonError(w, "请先配置模型端点并通过连通性测试", http.StatusBadRequest)
			return
		}
	}
	cfg.Enabled = req.Enabled
	if err := SaveConfig(cfg); err != nil {
		jsonError(w, "保存配置失败", http.StatusInternalServerError)
		return
	}
	action := "关闭AI助手"
	if req.Enabled {
		action = "开启AI助手"
	}
	common.LogAuditRequest(r, "AI", action, "", "success")
	common.JSONResponse(w, map[string]interface{}{"enabled": cfg.Enabled})
}
