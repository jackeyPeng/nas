package apps

// 应用会话 Cookie 单测：票据兑换种 Cookie、凭 Cookie 免票据放行、应用间
// 隔离、篡改/过期拒绝、Bearer 优先不种 Cookie、三者皆无仍 401（现状回归）。
// 风格与 proxy_test.go / ticket_test.go 对齐，复用其 setupProxyTest /
// newFakeUpstream / installTestApp / loginToken 等 helper。

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"nas-panel/common"
)

// initTestAppSessSecret 给当前测试初始化一把应用会话密钥，t.Cleanup 时
// 还原（包级变量单测间共享，必须显式复位，对齐 initTestTicketSecret 的
// 写法）。
func initTestAppSessSecret(t *testing.T, secret string) {
	t.Helper()
	old := appSessSecret
	appSessSecret = DeriveAppSessSecret(secret)
	t.Cleanup(func() { appSessSecret = old })
}

// extractCookie 从响应头的 Set-Cookie 里找到指定名字的那一枚，没有则
// 返回 nil（http.Response.Cookies() 需要完整响应对象，这里直接用
// httptest.ResponseRecorder 的 Result() 更贴近真实往返）。
func extractCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────
// 票据首次兑换 → Set-Cookie，属性符合约定。
// ─────────────────────────────────────────────────────────────

func TestProxy_TicketExchange_SetsAppSessCookie(t *testing.T) {
	common.InitAuth("proxy-test-secret")
	initTestTicketSecret(t, "ticket-test-secret")
	initTestAppSessSecret(t, "appsess-test-secret")
	mux := setupProxyTest(t)
	fu := newFakeUpstream(t)
	installTestApp(t, "photos", "app-secret-123", statusInstalled, fu.port(t))

	token, err := IssueOpenTicket("fm")
	if err != nil {
		t.Fatalf("IssueOpenTicket: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/apps/photos/?nas_ticket="+token, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("票据兑换应放行, got %d body=%s", rec.Code, rec.Body.String())
	}

	c := extractCookie(rec, appSessCookieName)
	if c == nil {
		t.Fatal("票据兑换响应应包含 Set-Cookie nas_app_sess")
	}
	if c.Path != "/apps/photos/" {
		t.Fatalf("Cookie Path 应为 /apps/photos/, got %q", c.Path)
	}
	if !c.HttpOnly {
		t.Fatal("Cookie 应为 HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("Cookie SameSite 应为 Lax, got %v", c.SameSite)
	}
	if c.Secure {
		t.Fatal("Cookie 不应设 Secure（面板常在 http 内网跑）")
	}
	if c.Value == "" {
		t.Fatal("Cookie 值不应为空")
	}
}

// ─────────────────────────────────────────────────────────────
// 凭 Cookie 免票据放行：第二次请求只带 Cookie（无 Bearer 无票据）应 200，
// 且转发到上游的 X-NAS-User 与票据绑定用户一致；转发请求的 Cookie 头
// 不应再含 nas_app_sess 段。
// ─────────────────────────────────────────────────────────────

func TestProxy_AppSessCookie_AllowsSubsequentRequest_NoTicketNoBearer(t *testing.T) {
	common.InitAuth("proxy-test-secret")
	initTestTicketSecret(t, "ticket-test-secret")
	initTestAppSessSecret(t, "appsess-test-secret")
	mux := setupProxyTest(t)
	fu := newFakeUpstream(t)
	installTestApp(t, "photos", "app-secret-123", statusInstalled, fu.port(t))

	token, err := IssueOpenTicket("fm")
	if err != nil {
		t.Fatalf("IssueOpenTicket: %v", err)
	}

	req1 := httptest.NewRequest(http.MethodGet, "/apps/photos/?nas_ticket="+token, nil)
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("首次票据兑换应放行, got %d", rec1.Code)
	}
	cookie := extractCookie(rec1, appSessCookieName)
	if cookie == nil {
		t.Fatal("首次兑换应种下 Cookie")
	}

	// 第二次请求：应用页自身发出的同源请求，模拟 fetch('./api/photos')，
	// 既无 Authorization 也无 nas_ticket，只带上一次种下的 Cookie。
	req2 := httptest.NewRequest(http.MethodGet, "/apps/photos/api/photos", nil)
	req2.AddCookie(&http.Cookie{Name: appSessCookieName, Value: cookie.Value})
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("凭应用会话 Cookie 应放行, got %d body=%s", rec2.Code, rec2.Body.String())
	}
	if got := fu.lastHdr.Get("X-NAS-User"); got != "fm" {
		t.Fatalf("X-NAS-User 应为票据绑定用户 fm, got %q", got)
	}
	// 转发给应用进程的请求，Cookie 头不应再带 nas_app_sess 段
	// （面板内部机制，应用进程不该看到）。
	fwdCookie := fu.lastHdr.Get("Cookie")
	if strings.Contains(fwdCookie, appSessCookieName+"=") {
		t.Fatalf("转发给应用的 Cookie 头不应包含 nas_app_sess 段, got %q", fwdCookie)
	}
}

// ─────────────────────────────────────────────────────────────
// 应用间隔离：A 应用签发的 Cookie 不能用于访问 B 应用。
// ─────────────────────────────────────────────────────────────

func TestProxy_AppSessCookie_CrossAppRejected(t *testing.T) {
	common.InitAuth("proxy-test-secret")
	initTestTicketSecret(t, "ticket-test-secret")
	initTestAppSessSecret(t, "appsess-test-secret")
	mux := setupProxyTest(t)
	fuA := newFakeUpstream(t)
	fuB := newFakeUpstream(t)
	installTestApp(t, "photos", "secret-a", statusInstalled, fuA.port(t))
	installTestApp(t, "notes", "secret-b", statusInstalled, fuB.port(t))

	token, err := IssueOpenTicket("fm")
	if err != nil {
		t.Fatalf("IssueOpenTicket: %v", err)
	}
	req1 := httptest.NewRequest(http.MethodGet, "/apps/photos/?nas_ticket="+token, nil)
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("photos 应用票据兑换应放行, got %d", rec1.Code)
	}
	cookie := extractCookie(rec1, appSessCookieName)
	if cookie == nil {
		t.Fatal("应种下 Cookie")
	}
	if cookie.Path != "/apps/photos/" {
		t.Fatalf("Cookie Path 应限定在 photos 应用, got %q", cookie.Path)
	}

	// 用 photos 应用的 Cookie 去访问 notes 应用（浏览器 Path 隔离本来
	// 不会发生这种情况，这里直接构造请求绕过浏览器，验证反代侧也校验
	// appID，不单纯依赖浏览器的 Path 隔离这一道防线）。
	req2 := httptest.NewRequest(http.MethodGet, "/apps/notes/", nil)
	req2.AddCookie(&http.Cookie{Name: appSessCookieName, Value: cookie.Value})
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("跨应用使用 Cookie 应 401, got %d body=%s", rec2.Code, rec2.Body.String())
	}
	if fuB.count.Load() != 0 {
		t.Fatalf("跨应用请求不应到达 notes 上游, got %d 次调用", fuB.count.Load())
	}
}

// ─────────────────────────────────────────────────────────────
// 篡改 Cookie 值 → 401。
// ─────────────────────────────────────────────────────────────

func TestProxy_AppSessCookie_TamperedRejected(t *testing.T) {
	common.InitAuth("proxy-test-secret")
	initTestTicketSecret(t, "ticket-test-secret")
	initTestAppSessSecret(t, "appsess-test-secret")
	mux := setupProxyTest(t)
	fu := newFakeUpstream(t)
	installTestApp(t, "photos", "app-secret-123", statusInstalled, fu.port(t))

	token, err := IssueOpenTicket("fm")
	if err != nil {
		t.Fatalf("IssueOpenTicket: %v", err)
	}
	req1 := httptest.NewRequest(http.MethodGet, "/apps/photos/?nas_ticket="+token, nil)
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, req1)
	cookie := extractCookie(rec1, appSessCookieName)
	if cookie == nil {
		t.Fatal("应种下 Cookie")
	}

	cases := []struct {
		name  string
		value string
	}{
		{"末尾追加字符破坏签名", cookie.Value + "x"},
		{"整体替换为不相关字符串", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		{"空字符串", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/apps/photos/", nil)
			if tc.value != "" {
				req.AddCookie(&http.Cookie{Name: appSessCookieName, Value: tc.value})
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("篡改/非法 Cookie 应 401, got %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────
// 过期 Cookie → 401（直接用 verifyAppSessCookie 构造一个过期签发值，
// 不依赖真实等待）。
// ─────────────────────────────────────────────────────────────

func TestAppSessCookie_Verify_RejectsExpired(t *testing.T) {
	initTestAppSessSecret(t, "appsess-test-secret")

	// 直接构造一个 expiry 在过去的 payload，走签发逻辑里一致的签名方式
	// （不调 IssueAppSessCookie，因为它总是用当前时间 + TTL 签发；这里要
	// 的是"签发时写入的 expiry 已经过去"这一具体场景）。
	p := appSessPayload{user: "fm", appID: "photos", expiry: time.Now().Add(-1 * time.Hour).Unix()}
	sig := hmacSign(appSessSecret, p.signingString())
	raw := p.user + "." + p.appID + "." + strconv.FormatInt(p.expiry, 10) + "." + sig
	value := base64.RawURLEncoding.EncodeToString([]byte(raw))

	if _, ok := verifyAppSessCookie(value, "photos", time.Now()); ok {
		t.Fatal("过期 Cookie 不应验签通过")
	}
}

func TestProxy_AppSessCookie_ExpiredRejectedOnProxy(t *testing.T) {
	common.InitAuth("proxy-test-secret")
	initTestAppSessSecret(t, "appsess-test-secret")
	mux := setupProxyTest(t)
	fu := newFakeUpstream(t)
	installTestApp(t, "photos", "app-secret-123", statusInstalled, fu.port(t))

	p := appSessPayload{user: "fm", appID: "photos", expiry: time.Now().Add(-1 * time.Hour).Unix()}
	sig := hmacSign(appSessSecret, p.signingString())
	raw := p.user + "." + p.appID + "." + strconv.FormatInt(p.expiry, 10) + "." + sig
	value := base64.RawURLEncoding.EncodeToString([]byte(raw))

	req := httptest.NewRequest(http.MethodGet, "/apps/photos/", nil)
	req.AddCookie(&http.Cookie{Name: appSessCookieName, Value: value})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("过期应用会话 Cookie 应 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	if fu.count.Load() != 0 {
		t.Fatalf("过期 Cookie 请求不应到达上游, got %d 次调用", fu.count.Load())
	}
}

// ─────────────────────────────────────────────────────────────
// Bearer 有效时行为不变：不种 Cookie 也 200。
// ─────────────────────────────────────────────────────────────

func TestProxy_BearerValid_NoCookieSet_StillOK(t *testing.T) {
	common.InitAuth("proxy-test-secret")
	initTestAppSessSecret(t, "appsess-test-secret")
	mux := setupProxyTest(t)
	fu := newFakeUpstream(t)
	installTestApp(t, "photos", "app-secret-123", statusInstalled, fu.port(t))

	token := loginToken(t, "fm")
	req := httptest.NewRequest(http.MethodGet, "/apps/photos/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Bearer 有效应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if c := extractCookie(rec, appSessCookieName); c != nil {
		t.Fatalf("Bearer 有效路径不应种应用会话 Cookie, got Set-Cookie=%v", c)
	}
}

// ─────────────────────────────────────────────────────────────
// 三者皆无（无 Bearer 无 Cookie 无票据）→ 401，现状回归。
// ─────────────────────────────────────────────────────────────

func TestProxy_NoBearerNoCookieNoTicket_401(t *testing.T) {
	common.InitAuth("proxy-test-secret")
	initTestTicketSecret(t, "ticket-test-secret")
	initTestAppSessSecret(t, "appsess-test-secret")
	mux := setupProxyTest(t)
	fu := newFakeUpstream(t)
	installTestApp(t, "photos", "app-secret-123", statusInstalled, fu.port(t))

	req := httptest.NewRequest(http.MethodGet, "/apps/photos/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("三者皆无应 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	if fu.count.Load() != 0 {
		t.Fatalf("未认证请求不应到达上游, got %d 次调用", fu.count.Load())
	}
}
