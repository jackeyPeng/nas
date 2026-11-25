package apps

// 应用反代会话 Cookie（修复浏览器应用页后续请求 401）：票据
// （ticket.go）解决的是「打开」按钮首次导航一次性放行，但票据消费后，
// 应用页内自身发起的同源请求（fetch('./api/xxx')、<img src="./thumb/x">
// 等）既没有 Authorization 头也没有票据 → 反代 401（真机 Chrome 实测
// nas-photos 现象）。方案：票据兑换放行那一刻，反代顺手 Set-Cookie 一枚
// 签名会话 Cookie，Path 限定在 /apps/<id>/ 下，此后浏览器对该应用路径下
// 的任何同源请求都会自动带上这枚 Cookie，反代验签通过即视同会话有效。
//
// 与票据同风格：HMAC-SHA256，密钥从面板 JWT secret 派生（独立标签，不
// 与票据密钥、JWT 签名密钥混用）。Cookie 只在面板与浏览器之间流转——
// 反代转发给应用进程时必须剥离（见 proxy.go delAppSessCookie），应用
// 后端永远不该看到这枚 Cookie。

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// appSessCookieName 是票据兑换后种下的应用会话 Cookie 名。Path 作用域
// /apps/<id>/，多应用各持一枚（同名不同 Path，浏览器按 Path 精确匹配
// 只会把对应应用的那一枚带上，不会串到其他应用）。
const appSessCookieName = "nas_app_sess"

// appSessTTL 是应用会话 Cookie 的有效期。比票据的 30 秒长得多——票据只
// 负责"首次导航那一下"，Cookie 负责的是用户在应用页面停留期间持续发出
// 的同源请求，8 小时覆盖一个典型使用会话，过期后重新打开应用会再走一次
// 票据兑换重新种 Cookie。
const appSessTTL = 8 * time.Hour

// appSessSecret 是应用会话 Cookie 的签名密钥，InitAppSessSecret 在
// main.go 里与 InitTicketSecret 同一接线时机设置；留 nil 表示未初始化，
// 签发/验签都恒为失败，对应"该功能不可用"而非 panic。
var appSessSecret []byte

// InitAppSessSecret 设置应用会话 Cookie 的签名密钥，main.go 紧邻
// InitTicketSecret 调用点追加调用一次（同一个 jwt secret 来源派生）。
func InitAppSessSecret(secret []byte) {
	appSessSecret = secret
}

// DeriveAppSessSecret 从面板 JWT secret 派生一把独立的应用会话 Cookie
// 签名密钥：hmac-sha256(jwtSecret, "nas-apps-session-cookie")。与
// DeriveTicketSecret 平行，但标签不同——票据密钥与会话 Cookie 密钥是
// 两个不同用途（一次性打开 vs. 持续会话），即便都从同一个 jwt secret
// 派生，也不应共享同一把派生密钥，避免其中一个用途的密钥材料意外泄露
// 时直接波及另一个。
func DeriveAppSessSecret(jwtSecret string) []byte {
	mac := hmac.New(sha256.New, []byte(jwtSecret))
	mac.Write([]byte("nas-apps-session-cookie"))
	return mac.Sum(nil)
}

// appSessPayload 是 Cookie 值编码前的明文结构：<user>.<appID>.<expiry>，
// 签名覆盖这三段。signingString 用 "\n" 连接而不是 Cookie 明文段的
// "."——防止 user 或 appID 本身含 "." 时，不同取值拼接后可能产生同一个
// 签名串（比如 user="a.b", appID="c" 与 user="a", appID="b.c" 用 "."
// 连接会得到同一个待签名串），用 "\n" 连接可以避免这种边界歧义（user/
// appID 当前实际取值都不含换行符，仍选用更安全的分隔符）。
type appSessPayload struct {
	user   string
	appID  string
	expiry int64 // unix 秒，Cookie 失效时间
}

func (p appSessPayload) signingString() string {
	return p.user + "\n" + p.appID + "\n" + strconv.FormatInt(p.expiry, 10)
}

// IssueAppSessCookie 签发一枚应用会话 Cookie 的值（未做 base64 外层
// http.Cookie 本身的转义处理，值本身已经是 base64url，Cookie 语法安全）。
// 调用方（handleAppProxy 票据兑换放行分支）负责包成 http.Cookie 并
// Set-Cookie，Path 限定到 /apps/<appID>/。
func IssueAppSessCookie(user, appID string) (string, error) {
	if len(appSessSecret) == 0 {
		return "", errAppSessSecretUninitialized
	}
	p := appSessPayload{
		user:   user,
		appID:  appID,
		expiry: time.Now().Add(appSessTTL).Unix(),
	}
	sig := hmacSign(appSessSecret, p.signingString())
	raw := p.user + "." + p.appID + "." + strconv.FormatInt(p.expiry, 10) + "." + sig
	return base64.RawURLEncoding.EncodeToString([]byte(raw)), nil
}

// errAppSessSecretUninitialized 对应密钥未初始化时的签发失败原因
// （与 ticket.go 的 fmt.Errorf 写法不同，这里用预定义错误值——两种风格
// repo 内都有先例，此处用 var 错误值是因为该函数只有这一种失败原因，
// 不需要 fmt.Errorf 的格式化能力）。
var errAppSessSecretUninitialized = &appSessError{"应用会话密钥未初始化"}

type appSessError struct{ msg string }

func (e *appSessError) Error() string { return e.msg }

// verifyAppSessCookie 校验 Cookie 值：base64 解码、按 "." 拆 4 段、appID
// 必须与当前路由的 app id 完全相等（防 A 应用的 Cookie 被拿去访问 B
// 应用——Cookie 的 Path 作用域理论上已经让浏览器不会跨应用携带，但反代
// 侧仍要显式校验，不完全依赖浏览器 Path 隔离这一道防线）、expiry 未过、
// HMAC 匹配。任何一步失败都返回 ok=false，调用方按 401 处理，不区分
// 具体原因（与票据校验一致的设计：不向客户端泄露失败细节）。
func verifyAppSessCookie(value, appID string, now time.Time) (user string, ok bool) {
	if len(appSessSecret) == 0 || value == "" {
		return "", false
	}
	rawBytes, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", false
	}
	parts := strings.SplitN(string(rawBytes), ".", 4)
	if len(parts) != 4 {
		return "", false
	}
	cookieUser, cookieAppID, expiryStr, sig := parts[0], parts[1], parts[2], parts[3]
	if cookieAppID != appID {
		return "", false
	}
	expiry, err := strconv.ParseInt(expiryStr, 10, 64)
	if err != nil {
		return "", false
	}
	p := appSessPayload{user: cookieUser, appID: cookieAppID, expiry: expiry}
	want := hmacSign(appSessSecret, p.signingString())
	if subtle.ConstantTimeCompare([]byte(want), []byte(sig)) != 1 {
		return "", false
	}
	if now.Unix() > expiry {
		return "", false
	}
	if cookieUser == "" {
		return "", false
	}
	return cookieUser, true
}

// delAppSessCookie 从转发给应用进程的出站请求 Cookie 头里精确剔除
// nas_app_sess=... 这一段，其余段（如果将来应用自身也用到 Cookie）保留
// 原样，用 "; " 重新拼接。
//
// 为什么不是整段删除 Cookie 头：httputil.ReverseProxy 默认会原样转发
// 入站请求的全部头（包括 Cookie），这枚会话 Cookie 是面板与浏览器之间
// 的内部机制，应用进程不该看到；但 Cookie 头里理论上可能还携带其他与
// 应用自身语义相关的段（当前 APP-SPEC 没有定义应用自有 Cookie 的来源，
// 但 Del 整个 Cookie 头属于比需求更激进的改动，一旦未来出现应用自有
// Cookie 需求，整段删除会让其无端失效——精确剔除是最小惊讶的做法）。
func delAppSessCookie(h http.Header) {
	raw := h.Get("Cookie")
	if raw == "" {
		return
	}
	segs := strings.Split(raw, "; ")
	kept := make([]string, 0, len(segs))
	prefix := appSessCookieName + "="
	for _, seg := range segs {
		if strings.HasPrefix(seg, prefix) {
			continue
		}
		kept = append(kept, seg)
	}
	if len(kept) == 0 {
		h.Del("Cookie")
		return
	}
	h.Set("Cookie", strings.Join(kept, "; "))
}
