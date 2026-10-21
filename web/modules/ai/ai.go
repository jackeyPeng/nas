package ai

import (
	"encoding/json"
	"net/http"

	"nas-panel/common"
)

// buildRegistry 组装 agent loop 使用的工具注册表。本期（M1/T6）只有 nas
// 域的只读工具；写工具（nasWriteTools，M2/T10）接入时在这里追加一个
// Domain，不改调用方——RunAgent/handleChat 只认 *Registry。
func buildRegistry() *Registry {
	return NewRegistry(Domain{
		Name:  "nas",
		Tools: nasReadTools(),
	})
}

// toolRegistry 是进程级单例：工具清单编译期固定，不需要每次请求重新构建。
var toolRegistry = buildRegistry()

// RegisterRoutes 注册 ai 模块的全部 HTTP 路由。
// AI 助手是默认关闭的功能模块：requireEnabled 包裹除 GET /api/ai/status
// 外的全部端点；toggle 本身例外（必须在关闭态也能调用才能打开开关）。
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/ai/status", common.AuthMiddleware(handleStatus))
	mux.HandleFunc("/api/ai/config", common.AuthMiddleware(requireEnabled(handleConfig)))
	mux.HandleFunc("/api/ai/toggle", common.AuthMiddleware(handleToggle))
	// test-connection 同 toggle 例外：配置阶段（功能尚未开启）就需要能测连通性。
	mux.HandleFunc("/api/ai/test-connection", common.AuthMiddleware(handleTestConnection))

	mux.HandleFunc("/api/ai/chat", common.AuthMiddleware(requireEnabled(handleChat)))
	mux.HandleFunc("/api/ai/conversations", common.AuthMiddleware(requireEnabled(handleListConversations)))
	mux.HandleFunc("/api/ai/conversations/", common.AuthMiddleware(requireEnabled(handleGetConversation)))

	// 确认执行端点是 Task 10（M2）的事：本 Task 只占位返回 501，
	// 避免前端/调用方提前对接时拿到 404 误判为路由缺失。
	mux.HandleFunc("/api/ai/action/confirm", common.AuthMiddleware(requireEnabled(handleActionConfirmPlaceholder)))
	mux.HandleFunc("/api/ai/action/cancel", common.AuthMiddleware(requireEnabled(handleActionCancelPlaceholder)))
}

// handleActionConfirmPlaceholder 占位：M2（Task 10）落地确认门全链前，
// 先保证路由存在、行为明确（501 而非 404），前端可以据此判断"功能未实现"
// 而不是"接口不存在"。
func handleActionConfirmPlaceholder(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	json.NewEncoder(w).Encode(map[string]string{"error": "确认执行端点将在后续版本提供"})
}

func handleActionCancelPlaceholder(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	json.NewEncoder(w).Encode(map[string]string{"error": "取消操作端点将在后续版本提供"})
}

// aiEnabled 读内存缓存的配置，避免每请求读盘；SaveConfig 刷新缓存后即时生效。
func aiEnabled() bool {
	return LoadConfig().Enabled
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
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
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
		http.Error(w, `{"error":"请求体非法"}`, http.StatusBadRequest)
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
		http.Error(w, `{"error":"保存配置失败"}`, http.StatusInternalServerError)
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
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req toggleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"请求体非法"}`, http.StatusBadRequest)
		return
	}

	cfg := LoadConfig()
	if req.Enabled {
		configured := cfg.BaseURL != "" && cfg.Model != ""
		if !configured || !cfg.Verified {
			http.Error(w, `{"error":"请先配置模型端点并通过连通性测试"}`, http.StatusBadRequest)
			return
		}
	}
	cfg.Enabled = req.Enabled
	if err := SaveConfig(cfg); err != nil {
		http.Error(w, `{"error":"保存配置失败"}`, http.StatusInternalServerError)
		return
	}
	action := "关闭AI助手"
	if req.Enabled {
		action = "开启AI助手"
	}
	common.LogAuditRequest(r, "AI", action, "", "success")
	common.JSONResponse(w, map[string]interface{}{"enabled": cfg.Enabled})
}
