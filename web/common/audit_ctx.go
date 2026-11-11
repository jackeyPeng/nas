package common

import (
	"context"
	"net"
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

// trustedProxyCIDRs 列出反代/本机可信网段：只有连接本身来自这些网段时，
// 才采信该连接声明的 X-Forwarded-For（否则任意客户端可伪造 XFF 头绕过
// 审计溯源或共享限流桶）。
var trustedProxyCIDRs = mustParseCIDRs(
	"127.0.0.0/8",
	"::1/128",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
)

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		nets = append(nets, n)
	}
	return nets
}

// hostFromRemoteAddr 从 http.Request.RemoteAddr（"host:port" 或裸
// IPv6 地址如 "[::1]:1234"）中取出主机部分，解析失败时原样返回。
func hostFromRemoteAddr(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return strings.TrimSpace(remoteAddr)
	}
	return host
}

// IsTrustedProxyHost 判断给定主机地址是否属于可信代理/本机网段
// （127.0.0.0/8、::1、10.0.0.0/8、172.16.0.0/12、192.168.0.0/16）。
// 只有这些来源的连接才有资格声明 X-Forwarded-For。
func IsTrustedProxyHost(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range trustedProxyCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// requestIPFrom 是 RequestIP 的纯函数版本，便于单测：只有 remoteAddr
// 的主机部分命中可信代理网段时，才采信 forwarded（X-Forwarded-For）
// 首段；否则回退到 remoteAddr 本身的主机部分。
func requestIPFrom(remoteAddr, forwarded string) string {
	host := hostFromRemoteAddr(remoteAddr)
	if forwarded != "" && IsTrustedProxyHost(host) {
		first := strings.TrimSpace(strings.Split(forwarded, ",")[0])
		if first != "" {
			return first
		}
	}
	return host
}

// RequestIP 提取客户端 IP：仅当直连方（RemoteAddr）是可信代理/本机网段时，
// 才采信其声明的 X-Forwarded-For 首段，否则直接使用 RemoteAddr 的主机部分。
// 防止公网客户端直连时伪造 XFF 头污染审计日志来源 IP。
func RequestIP(r *http.Request) string {
	return requestIPFrom(r.RemoteAddr, r.Header.Get("X-Forwarded-For"))
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
