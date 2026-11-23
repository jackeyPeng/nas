package apps

// 一次性打开票据单测（M1-7）：签发/验签往返、过期拒绝、重放拒绝、
// 未认证且无票据时反代入口仍 401。风格与 proxy_test.go 对齐，复用其
// setupProxyTest/loginToken/newFakeUpstream/installTestApp 等 helper。

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"nas-panel/common"
)

// initTestTicketSecret 给当前测试初始化一把票据密钥，t.Cleanup 时还原为
// nil（避免某个测试留下的密钥状态影响后面没调用本函数的测试——包级变量
// 单测间共享，必须显式复位）。
func initTestTicketSecret(t *testing.T, secret string) {
	t.Helper()
	old := ticketSecret
	ticketSecret = DeriveTicketSecret(secret)
	t.Cleanup(func() { ticketSecret = old })
}

// ─────────────────────────────────────────────────────────────
// 签发 / 验签往返。
// ─────────────────────────────────────────────────────────────

func TestTicket_IssueVerify_RoundTrip(t *testing.T) {
	initTestTicketSecret(t, "ticket-test-secret")

	token, err := IssueOpenTicket("fm")
	if err != nil {
		t.Fatalf("IssueOpenTicket: %v", err)
	}
	if token == "" {
		t.Fatal("票据不应为空字符串")
	}

	user, ok := verifyOpenTicket(token, time.Now())
	if !ok {
		t.Fatal("刚签发的票据应验签通过")
	}
	if user != "fm" {
		t.Fatalf("票据绑定用户应为 fm, got %q", user)
	}
}

func TestTicket_Verify_RejectsTamperedOrUnknownSecret(t *testing.T) {
	initTestTicketSecret(t, "ticket-test-secret")

	token, err := IssueOpenTicket("fm")
	if err != nil {
		t.Fatalf("IssueOpenTicket: %v", err)
	}

	// 篡改 token 字符串 → 验签失败。
	if _, ok := verifyOpenTicket(token+"x", time.Now()); ok {
		t.Fatal("篡改后的票据不应验签通过")
	}

	// 换一把密钥（模拟密钥不一致/未初始化）→ 验签失败。
	ticketSecret = DeriveTicketSecret("a-different-secret")
	if _, ok := verifyOpenTicket(token, time.Now()); ok {
		t.Fatal("换密钥后旧票据不应验签通过")
	}
}

func TestTicket_Verify_EmptyOrUninitializedSecret(t *testing.T) {
	ticketSecret = nil
	if _, err := IssueOpenTicket("fm"); err == nil {
		t.Fatal("票据密钥未初始化时签发应返回错误")
	}
	if _, ok := verifyOpenTicket("anything", time.Now()); ok {
		t.Fatal("票据密钥未初始化时验签应恒为 false")
	}
}

// ─────────────────────────────────────────────────────────────
// 过期拒绝（任务要求「30 秒有效」）。
// ─────────────────────────────────────────────────────────────

func TestTicket_Verify_RejectsExpired(t *testing.T) {
	initTestTicketSecret(t, "ticket-test-secret")

	token, err := IssueOpenTicket("fm")
	if err != nil {
		t.Fatalf("IssueOpenTicket: %v", err)
	}

	// 签发瞬间校验通过。
	if _, ok := verifyOpenTicket(token, time.Now()); !ok {
		t.Fatal("签发瞬间验签应通过")
	}
	// TTL 之后（30s 有效期 + 余量）校验应失败。
	future := time.Now().Add(ticketTTL + 5*time.Second)
	if _, ok := verifyOpenTicket(token, future); ok {
		t.Fatal("超过 30s 有效期的票据不应验签通过")
	}
}

// ─────────────────────────────────────────────────────────────
// 重放拒绝：同一票据只能被消费一次。
// ─────────────────────────────────────────────────────────────

func TestTicket_ConsumeOnce_RejectsReplay(t *testing.T) {
	initTestTicketSecret(t, "ticket-test-secret")

	token, err := IssueOpenTicket("fm")
	if err != nil {
		t.Fatalf("IssueOpenTicket: %v", err)
	}
	expiry := ticketExpiryFromToken(token)

	if !consumeTicketOnce(token, expiry) {
		t.Fatal("第一次消费票据应成功")
	}
	if consumeTicketOnce(token, expiry) {
		t.Fatal("第二次消费同一票据应被拒绝（防重放）")
	}
}

func TestTicketFallbackUser_RejectsReplayedTicketOnProxy(t *testing.T) {
	common.InitAuth("proxy-test-secret")
	initTestTicketSecret(t, "ticket-test-secret")
	mux := setupProxyTest(t)
	fu := newFakeUpstream(t)
	installTestApp(t, "photos", "app-secret-123", statusInstalled, fu.port(t))

	token, err := IssueOpenTicket("fm")
	if err != nil {
		t.Fatalf("IssueOpenTicket: %v", err)
	}

	// 第一次带票据访问（无 Authorization 头，模拟 window.open 新页面）→ 放行。
	req1 := httptest.NewRequest(http.MethodGet, "/apps/photos/?nas_ticket="+token, nil)
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("首次使用票据应放行, got %d body=%s", rec1.Code, rec1.Body.String())
	}
	if got := fu.lastHdr.Get("X-NAS-User"); got != "fm" {
		t.Fatalf("票据放行后 X-NAS-User 应为 fm, got %q", got)
	}
	// 票据必须在转发前从 query 中剥离，应用不该见到它（任务硬红线）。
	if fu.lastReq.URL.Query().Has("nas_ticket") {
		t.Fatal("转发给应用的请求不应再带 nas_ticket 参数")
	}

	// 第二次用同一票据再访问 → 必须 401（重放）。
	req2 := httptest.NewRequest(http.MethodGet, "/apps/photos/?nas_ticket="+token, nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("重放同一票据应返回 401, got %d body=%s", rec2.Code, rec2.Body.String())
	}
	if fu.count.Load() != 1 {
		t.Fatalf("重放请求不应再次到达上游, got %d 次调用", fu.count.Load())
	}
}

// ─────────────────────────────────────────────────────────────
// 未认证且无票据 → 401（确保票据回退路径不会放宽原有鉴权要求）。
// ─────────────────────────────────────────────────────────────

func TestProxy_Unauthenticated_NoTicket_401(t *testing.T) {
	common.InitAuth("proxy-test-secret")
	initTestTicketSecret(t, "ticket-test-secret")
	mux := setupProxyTest(t)
	fu := newFakeUpstream(t)
	installTestApp(t, "photos", "app-secret-123", statusInstalled, fu.port(t))

	req := httptest.NewRequest(http.MethodGet, "/apps/photos/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无会话无票据应返回 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	if fu.count.Load() != 0 {
		t.Fatalf("未认证请求不应到达上游, got %d 次调用", fu.count.Load())
	}
}

// ─────────────────────────────────────────────────────────────
// GET /apps/<id>/open：会话有效才能拿到票据，302 落到 entry 路径且带票据。
// ─────────────────────────────────────────────────────────────

func TestHandleAppOpen_ValidSession_RedirectsWithTicket(t *testing.T) {
	common.InitAuth("proxy-test-secret")
	initTestTicketSecret(t, "ticket-test-secret")
	mux := setupProxyTest(t)
	fu := newFakeUpstream(t)
	installTestApp(t, "photos", "app-secret-123", statusInstalled, fu.port(t))

	token := loginToken(t, "fm")
	req := httptest.NewRequest(http.MethodGet, "/apps/photos/open?entry=/apps/photos/gallery", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("有效会话应 302, got %d body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if loc == "" {
		t.Fatal("302 响应应带 Location 头")
	}
	parsed, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("Location 应是合法 URL: %v", err)
	}
	if got := parsed.Path; got != "/apps/photos/gallery" {
		t.Fatalf("应落到 entry 指定路径, want /apps/photos/gallery got %q", got)
	}
	if !parsed.Query().Has("nas_ticket") {
		t.Fatal("Location 应带 nas_ticket 票据参数")
	}
}

func TestHandleAppOpen_Unauthenticated_401_NoTicketIssued(t *testing.T) {
	common.InitAuth("proxy-test-secret")
	initTestTicketSecret(t, "ticket-test-secret")
	mux := setupProxyTest(t)
	fu := newFakeUpstream(t)
	installTestApp(t, "photos", "app-secret-123", statusInstalled, fu.port(t))

	req := httptest.NewRequest(http.MethodGet, "/apps/photos/open", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未登录访问 open 入口应 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleAppOpen_RejectsEntryOutsideAppPrefix(t *testing.T) {
	common.InitAuth("proxy-test-secret")
	initTestTicketSecret(t, "ticket-test-secret")
	mux := setupProxyTest(t)
	fu := newFakeUpstream(t)
	installTestApp(t, "photos", "app-secret-123", statusInstalled, fu.port(t))

	token := loginToken(t, "fm")
	req := httptest.NewRequest(http.MethodGet, "/apps/photos/open?entry=/apps/other-app/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("entry 跳出本应用前缀应被拒绝 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}
