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
//
// 会话校验失败时还有第二条放行路径（M1-7「打开」按钮票据引导）：query 带
// nas_ticket 且验签+一次性消费通过，等价于该票据绑定用户的一次会话校验
// 通过，换算成同一套 X-NAS-* 注入放行——window.open 新开的页面天然不带
// Authorization 头，没有这条路径会直接 401（真机实测现象）。
func handleAppProxy(w http.ResponseWriter, r *http.Request) {
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

	// GET /apps/<id>/open 是票据签发入口，不是反代目标，单独分发
	// （与反代共用 /apps/ 前缀路由，这里按剩余路径精确匹配 "<id>/open"）。
	if slash >= 0 && rest[slash+1:] == "open" {
		handleAppOpen(w, r, id)
		return
	}

	// 会话校验（§5.2「核心在转发前校验面板会话；无效会话 → 401」）：复用
	// 面板现有 JWT Bearer 校验（common.ValidTokenFromRequest/RequestUsername），
	// 不新造第二套会话机制。401 响应带与 common.AuthMiddleware 一致的 JSON
	// body（前端 401 拦截逻辑按现有口径识别）。
	user := common.RequestUsername(r)
	validSession := common.ValidTokenFromRequest(r) && user != ""
	if !validSession {
		// 回退路径：query 带票据且验签+一次性消费通过 → 换算成票据绑定的
		// 用户身份放行；票据剥离在下面 Rewrite 里统一做，这里只确定身份。
		ticketUser, ticketOK := ticketFallbackUser(r)
		if !ticketOK {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		user = ticketUser
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

			// 票据剥离（任务硬红线：票据放 URL query 时必须剥离后再转发给
			// 应用，应用不该见到票据）：无论本次请求是走票据回退放行的，
			// 还是真实会话请求里碰巧带了这个参数，一律从转发 query 中去掉。
			if q := pr.Out.URL.Query(); q.Has(ticketQueryParam) {
				q.Del(ticketQueryParam)
				pr.Out.URL.RawQuery = q.Encode()
			}

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

// ticketFallbackUser 校验请求 query 里的 nas_ticket：验签通过（未过期、
// HMAC 匹配）且尚未被消费过，返回票据绑定的用户名。失败（缺票据/签名不对/
// 过期/重放）统一返回 ok=false，调用方按 401 处理，不区分失败原因（避免
// 给攻击者可用于调整重试策略的信息）。
func ticketFallbackUser(r *http.Request) (user string, ok bool) {
	token := r.URL.Query().Get(ticketQueryParam)
	if token == "" {
		return "", false
	}
	user, valid := verifyOpenTicket(token, time.Now())
	if !valid {
		return "", false
	}
	if !consumeTicketOnce(token, ticketExpiryFromToken(token)) {
		return "", false // 重放：同一票据第二次出现
	}
	return user, true
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
