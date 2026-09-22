package common

import (
	"context"
	"net/http"
	"strings"
)

// ─────────────────────────────────────────────────────────────
// 请求级审计上下文
//
// 全局 loggingMiddleware（main.go）对所有写请求自动写审计日志。
// handler 内如需记录更丰富的 detail，调用 LogAuditRequest：
// 它会带上真实 username/IP，并通过 ctx 标记该请求"已由 handler 审计"，
// 中间件随后跳过，避免同一操作双写两条记录。
// ─────────────────────────────────────────────────────────────

type auditFlagKeyType struct{}

var auditFlagKey = auditFlagKeyType{}

// WithAuditFlag 在请求 ctx 中注入审计标记位（中间件调用）
func WithAuditFlag(r *http.Request) (*http.Request, *bool) {
	flag := new(bool)
	return r.WithContext(context.WithValue(r.Context(), auditFlagKey, flag)), flag
}

// markRequestAudited 标记当前请求已由 handler 自行审计
func markRequestAudited(r *http.Request) {
	if flag, ok := r.Context().Value(auditFlagKey).(*bool); ok && flag != nil {
		*flag = true
	}
}

// RequestUsername 从 Authorization Bearer token 解出用户名
func RequestUsername(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		if user, err := VerifyToken(auth[7:]); err == nil {
			return user
		}
	}
	return ""
}

// RequestIP 提取客户端 IP（优先 X-Forwarded-For）
func RequestIP(r *http.Request) string {
	ip := r.RemoteAddr
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		ip = strings.Split(fwd, ",")[0]
	}
	// 去掉端口（兼容 IPv6 裸地址）
	if i := strings.LastIndex(ip, ":"); i > 0 && !strings.Contains(ip[i:], "]") {
		ip = ip[:i]
	}
	return strings.TrimSpace(ip)
}

// LogAuditRequest 在 handler 内记录审计日志：自动带真实 username/IP，
// 并标记该请求已审计（全局中间件不再重复记录）。
// category 为日志分类（如 STORAGE/SYSTEM/VAULT），action 为中文操作名。
func LogAuditRequest(r *http.Request, category, action, detail, result string) {
	username := RequestUsername(r)
	if username == "" {
		username = "system"
	}
	markRequestAudited(r)
	LogAudit(username, action, category, r.URL.Path, detail, result, RequestIP(r))
}
