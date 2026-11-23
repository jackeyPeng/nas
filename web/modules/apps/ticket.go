package apps

// 应用「打开」按钮一次性票据（M1-7）：window.open('/apps/<id>/') 直开不带
// Authorization 头，反代会话校验 401（真机实测现象）。方案：面板先发一张
// 30 秒有效、一次性消费的短期票据，前端用 302 跳转（浏览器原生导航自动带
// query 参数，不需要 JS 设头）落到真实应用页面；反代校验会话失败时，若
// query 带票据且验签通过，换算成与 SSO 相同的身份注入放行。
//
// HMAC 复用 common 现有签名原语（proxy.go 的 SignNASAuth/VerifyNASAuth 走
// 应用专属 secret；票据走面板自身的 JWT secret 等价强度，这里用
// crypto/hmac 独立实现，不混用应用 secret——票据证明的是"这是哪个登录
// 用户"，与具体应用无关，不应绑定某个应用的 __NAS_SECRET__）。

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"nas-panel/common"
)

// ticketTTL 是票据有效期（任务要求「30 秒有效」）。
const ticketTTL = 30 * time.Second

// ticketQueryParam 是票据在 URL query 里的参数名；反代转发前必须从转发
// query 中剥掉（任务硬红线：应用不该见到票据）。
const ticketQueryParam = "nas_ticket"

// ticketSecret 是票据签发/验签密钥，InitApps 时与 common.InitAuth 用同一
// 面板级密钥材料生成（避免再引入一套独立密钥管理）；留 nil 表示未初始化，
// 签发/验签都会失败，对应"票据功能不可用"而非 panic。
var ticketSecret []byte

// InitTicketSecret 设置票据签名密钥，main.go 在 common.InitAuth 之后调用
// 一次（与 InitApps 同一接线时机）。
func InitTicketSecret(secret []byte) {
	ticketSecret = secret
}

// DeriveTicketSecret 从面板 JWT secret 派生一把独立的票据签名密钥：
// hmac-sha256(jwtSecret, "nas-apps-open-ticket")。不直接复用 JWT secret
// 本身签票据——两者用途不同（会话 token vs. 一次性打开票据），加标签派生
// 避免一把密钥挪作多用途、也避免其中一个用途的密钥材料泄露直接波及另一个。
func DeriveTicketSecret(jwtSecret string) []byte {
	mac := hmac.New(sha256.New, []byte(jwtSecret))
	mac.Write([]byte("nas-apps-open-ticket"))
	return mac.Sum(nil)
}

// ─────────────────────────────────────────────────────────────
// 票据签发 / 验签
// ─────────────────────────────────────────────────────────────

// ticketPayload 是票据编码进 query 参数前的明文结构：<user>.<expiry>.<nonce>，
// 签名覆盖这三段拼接串。nonce 保证同一用户同一秒内连续点「打开」也能拿到
// 不同票据（否则一次性缓存会把它们视为同一张重放）。
type ticketPayload struct {
	user   string
	expiry int64 // unix 秒，票据失效时间
	nonce  string
}

func (p ticketPayload) signingString() string {
	return p.user + "\n" + strconv.FormatInt(p.expiry, 10) + "\n" + p.nonce
}

// IssueOpenTicket 签发一张一次性票据，供 GET /apps/<id>/open 302 跳转时
// 附加到目标 URL（query 参数 nas_ticket）。
func IssueOpenTicket(user string) (string, error) {
	if len(ticketSecret) == 0 {
		return "", fmt.Errorf("票据密钥未初始化")
	}
	nonce, err := randomNonce()
	if err != nil {
		return "", err
	}
	p := ticketPayload{
		user:   user,
		expiry: time.Now().Add(ticketTTL).Unix(),
		nonce:  nonce,
	}
	sig := hmacSign(ticketSecret, p.signingString())
	raw := p.user + "." + strconv.FormatInt(p.expiry, 10) + "." + p.nonce + "." + sig
	return base64.RawURLEncoding.EncodeToString([]byte(raw)), nil
}

// verifyOpenTicket 验签并返回票据绑定的用户名；任何一步失败都返回空字符串
// + 具体原因（供 handler 决定响应码，不对外暴露原因细节）。
// 不在这里做一次性消费判定——消费检查是有状态操作，交给调用方搭配
// consumeTicketOnce 一起用，职责分离方便单测分别覆盖"签名/过期"与"重放"。
func verifyOpenTicket(token string, now time.Time) (user string, ok bool) {
	if len(ticketSecret) == 0 || token == "" {
		return "", false
	}
	rawBytes, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", false
	}
	parts := strings.SplitN(string(rawBytes), ".", 4)
	if len(parts) != 4 {
		return "", false
	}
	user, expiryStr, nonce, sig := parts[0], parts[1], parts[2], parts[3]
	expiry, err := strconv.ParseInt(expiryStr, 10, 64)
	if err != nil {
		return "", false
	}
	p := ticketPayload{user: user, expiry: expiry, nonce: nonce}
	want := hmacSign(ticketSecret, p.signingString())
	if subtle.ConstantTimeCompare([]byte(want), []byte(sig)) != 1 {
		return "", false
	}
	if now.Unix() > expiry {
		return "", false
	}
	if user == "" {
		return "", false
	}
	return user, true
}

func hmacSign(secret []byte, msg string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

func randomNonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// ─────────────────────────────────────────────────────────────
// 一次性防重放缓存：内存 map + 互斥锁。
//
// 单实例面板场景够用（没有多副本部署需求，APP-SPEC v0.1 未定义面板本身
// 的水平扩展）；票据 TTL 仅 30 秒，缓存按 janitor 扫描过期项，长期运行
// 不会无限增长。
// ─────────────────────────────────────────────────────────────

var (
	usedTicketsMu sync.Mutex
	usedTickets   = map[string]time.Time{} // token -> 过期时间，过期后可被 janitor 清理
)

// consumeTicketOnce 原子地"查是否已用过 + 标记为已用"；第二次对同一 token
// 调用返回 false（防重放）。调用前必须已经过 verifyOpenTicket 签名校验，
// 这里只负责一次性语义，不重复验签。
func consumeTicketOnce(token string, expiry time.Time) bool {
	usedTicketsMu.Lock()
	defer usedTicketsMu.Unlock()
	if _, exists := usedTickets[token]; exists {
		return false
	}
	usedTickets[token] = expiry
	// 顺手清理过期项，避免无限增长（票据 TTL 很短，map 不会积累太久的历史）。
	now := time.Now()
	for t, exp := range usedTickets {
		if now.After(exp) {
			delete(usedTickets, t)
		}
	}
	return true
}

// ticketExpiryFromToken 从已验签的 token 中取出过期时间，供
// consumeTicketOnce 记录清理用（避免再传一遍 ticketPayload）。
func ticketExpiryFromToken(token string) time.Time {
	rawBytes, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return time.Now()
	}
	parts := strings.SplitN(string(rawBytes), ".", 4)
	if len(parts) != 4 {
		return time.Now()
	}
	expiry, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return time.Now()
	}
	return time.Unix(expiry, 0)
}

// ─────────────────────────────────────────────────────────────
// GET /apps/<id>/open：校验会话 → 签发票据 → 302 到真实应用页面
// ─────────────────────────────────────────────────────────────

// handleAppOpen 实现「打开」按钮的票据引导：会话有效才能拿到票据
// （未登录 window.open 直接 401，不签票据），302 Location 带
// nas_ticket query 参数，落回同一个 /apps/<id>/entry 路径（entry 拼接
// 由调用方——即前端传入的完整相对路径——决定，这里只管加票据）。
func handleAppOpen(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !common.ValidTokenFromRequest(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	user := common.RequestUsername(r)
	if user == "" {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
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

	ticket, err := IssueOpenTicket(user)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "签发票据失败: "+err.Error())
		return
	}

	// entry 相对路径由前端经 query 参数 entry 传入（§3.3 entry 字段拼接保持，
	// apps.js openApp 原逻辑负责拼 app.entry，这里只负责附加票据）；缺省时
	// 落到应用根 /apps/<id>/。
	entry := r.URL.Query().Get("entry")
	if entry == "" {
		entry = "/apps/" + id + "/"
	}
	target, err := url.Parse(entry)
	if err != nil || !strings.HasPrefix(target.Path, "/apps/"+id) {
		// entry 必须落在本应用自己的路径段下，防止票据被拼到任意路径
		// （open target 来自前端参数，不信任其跳出应用前缀）。
		httpError(w, http.StatusBadRequest, "非法的 entry 参数")
		return
	}
	q := target.Query()
	q.Set(ticketQueryParam, ticket)
	target.RawQuery = q.Encode()

	http.Redirect(w, r, target.String(), http.StatusFound)
}
