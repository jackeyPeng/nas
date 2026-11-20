package apps

// 统一入口反代（APP-SPEC §4-5）：/apps/<id>/* → 127.0.0.1:<port>/*，路径前缀
// 原样透传（应用自己处理 /apps/<id>/ 前缀），转发前复用面板现有会话鉴权，
// 并注入 X-NAS-User/X-NAS-Roles/X-NAS-Auth/X-NAS-Time 供应用侧 SSO。

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"time"

	"nas-panel/common"
)

// nasAuthSkew 是 X-NAS-Auth 签名的时间容差（§5.2/§5.3「容差 ±60s」）。
const nasAuthSkew = 60 * time.Second

// RegisterProxyRoutes 挂 /apps/<id>/... 统一入口路由。main.go 与
// RegisterRoutes 同一接线模式单独调用，便于单测只挂反代而不带 AdminMiddleware
// （反代自身的会话校验不是 AdminMiddleware——SSO 面向全部已登录用户，不要求
// 管理员身份；AdminMiddleware 语义是"管理员专属"，与此处"任意已登录会话"不同）。
func RegisterProxyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/apps/", handleAppProxy)
}

// handleAppProxy 是 /apps/<id>/... 的统一入口：会话校验 → 查注册表 → 剥离
// 伪造头 → 注入 SSO 头 → 反代到 127.0.0.1:<port>（路径原样透传）。
func handleAppProxy(w http.ResponseWriter, r *http.Request) {
	// 会话校验（§5.2「核心在转发前校验面板会话；无效会话 → 401」）：复用
	// 面板现有 JWT Bearer 校验（common.ValidTokenFromRequest/RequestUsername），
	// 不新造第二套会话机制。401 响应带与 common.AuthMiddleware 一致的 JSON
	// body（前端 401 拦截逻辑按现有口径识别）。
	if !common.ValidTokenFromRequest(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	user := common.RequestUsername(r)
	if user == "" {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// 解析 /apps/<id>/... 的 id：剩余路径（含前导 /）原样转发，不剥前缀
	// （§5.2「路径前缀原样透传，应用自己处理前缀」）。
	rest := strings.TrimPrefix(r.URL.Path, "/apps/")
	slash := strings.IndexByte(rest, '/')
	var id string
	if slash < 0 {
		id = rest
	} else {
		id = rest[:slash]
	}
	if id == "" {
		httpError(w, http.StatusNotFound, "缺少应用 id")
		return
	}

	app, err := getInstalledApp(reg.db, id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询注册表失败: "+err.Error())
		return
	}
	if app == nil || app.Status == statusPending {
		httpError(w, http.StatusNotFound, "应用未安装: "+id)
		return
	}
	// 注册表里的端口在分配时已校验落在 18000-18999 段（registry.go
	// allocatePort），这里不重复校验——id 只从注册表解析，反代目标恒为
	// 127.0.0.1:<port>，不接受任何外部输入改写目标主机（防 SSRF）。
	if app.Port < minPort || app.Port > maxPort {
		httpError(w, http.StatusInternalServerError, "注册表端口异常")
		return
	}
	if app.Status == statusStopped {
		httpError(w, http.StatusServiceUnavailable, "应用已停止: "+id)
		return
	}

	// 角色：当前面板是单用户架构，没有角色概念（见 common.AdminMiddleware
	// 注释）；取不到角色时留空字符串（任务要求）。
	roles := ""

	now := time.Now().Unix()
	sig := SignNASAuth(app.Secret, user, now)

	target := "127.0.0.1:" + strconv.Itoa(app.Port)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// 只改 scheme/host，Path 原样透传（§5.2）。
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = target
			pr.Out.Host = target

			// 防伪造：转发前先剥离入站请求里的同名头，再注入面板计算出的
			// 真实值（任务硬红线：伪造头剥离缺失=返工）。
			pr.Out.Header.Del("X-Nas-User")
			pr.Out.Header.Del("X-Nas-Roles")
			pr.Out.Header.Del("X-Nas-Auth")
			pr.Out.Header.Del("X-Nas-Time")
			pr.Out.Header.Set("X-NAS-User", user)
			pr.Out.Header.Set("X-NAS-Roles", roles)
			pr.Out.Header.Set("X-NAS-Auth", sig)
			pr.Out.Header.Set("X-NAS-Time", strconv.FormatInt(now, 10))

			// ProxyRequest.Rewrite 默认已把本机地址追加进 X-Forwarded-For
			// （httputil 行为），此处无需手动处理。
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// 转发错误（连接拒绝/超时等）：503，不向客户端泄内部错误细节
			// （任务要求）。
			httpError(w, http.StatusServiceUnavailable, "应用暂不可用: "+id)
		},
	}
	proxy.ServeHTTP(w, r)
}

// SignNASAuth 计算 §5.3 的 X-NAS-Auth 签名：
// hex(hmac-sha256(secret, user + "\n" + unix秒))。
func SignNASAuth(secret, user string, unixSeconds int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(user + "\n" + strconv.FormatInt(unixSeconds, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyNASAuth 校验 §5.3 的 X-NAS-Auth 签名：sig 必须是
// hex(hmac-sha256(secret, user + "\n" + ts)) 且 ts 与 now 的差值在
// ±60s 容差内（§5.3）。导出供托管应用（hello-app M1-6）与 SDK 参考实现。
func VerifyNASAuth(secret, user, ts, sig string, now int64) bool {
	tsNum, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	delta := now - tsNum
	if delta < 0 {
		delta = -delta
	}
	if time.Duration(delta)*time.Second > nasAuthSkew {
		return false
	}
	want := SignNASAuth(secret, user, tsNum)
	return subtle.ConstantTimeCompare([]byte(want), []byte(sig)) == 1
}
