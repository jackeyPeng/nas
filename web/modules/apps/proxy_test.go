package apps

// 反代与 SSO 单测（APP-SPEC §4-5）：覆盖会话门禁、路径前缀原样透传、
// X-NAS-* 头注入与伪造剥离、HMAC 往返、停用/未注册错误码。
// 全部验证在包内完成（httptest 假上游 + 注册表插桩 + httptest 客户端），
// 不触达仓库外任何资源。

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"nas-panel/common"
)

// setupProxyTest 打开一个全新 apps.db，把包级单例 reg 指向它，返回挂了
// RegisterProxyRoutes 的 mux（对齐 setupLifecycleTest 的隔离模式，每个
// 测试独立临时目录与 db，互不污染）。
func setupProxyTest(t *testing.T) *http.ServeMux {
	t.Helper()
	db, err := openAppsDB(t.TempDir())
	if err != nil {
		t.Fatalf("openAppsDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	reg = &registry{db: db, dataDir: t.TempDir(), executor: newFakeExecutor()}

	mux := http.NewServeMux()
	RegisterProxyRoutes(mux)
	return mux
}

// loginToken 签发一个供测试用的面板会话 JWT（复用 common.CreateToken，
// 与反代校验路径使用的 common.ValidTokenFromRequest/RequestUsername 一致，
// 不另造鉴权机制）。
func loginToken(t *testing.T, user string) string {
	t.Helper()
	tok, err := common.CreateToken(user)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	return tok
}

// fakeUpstream 是模拟托管应用的假上游：记录收到的请求（路径、全部头）与
// 请求计数，供断言"未认证时上游零请求"等场景。监听地址固定落在
// [minPort, maxPort] 段（反代出站强制 127.0.0.1 且只信任注册表端口，
// §5.1/防 SSRF 校验要求目标端口落在该段——httptest.NewServer 的随机
// 临时端口会落在校验段外，必须自己在该段内找一个空闲端口手动监听）。
type fakeUpstream struct {
	srv     *httptest.Server
	ln      net.Listener
	count   atomic.Int64
	lastReq *http.Request
	lastHdr http.Header
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	fu := &fakeUpstream{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fu.count.Add(1)
		fu.lastReq = r
		fu.lastHdr = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "path=%s", r.URL.Path)
	})

	var ln net.Listener
	var err error
	for port := minPort; port <= maxPort; port++ {
		ln, err = net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err == nil {
			break
		}
	}
	if ln == nil {
		t.Fatalf("在 %d-%d 段内找不到可用端口起假上游: %v", minPort, maxPort, err)
	}
	fu.ln = ln
	fu.srv = &httptest.Server{Listener: ln, Config: &http.Server{Handler: handler}}
	fu.srv.Start()
	t.Cleanup(fu.srv.Close)
	return fu
}

// port 返回假上游监听端口（整数），供插入注册表记录。
func (fu *fakeUpstream) port(t *testing.T) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(fu.ln.Addr().String())
	if err != nil {
		t.Fatalf("解析假上游端口失败: %v", err)
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("假上游端口非数字: %v", err)
	}
	return p
}

// installTestApp 往台账插入一条已安装应用记录，端口指向 fu，供反代命中。
func installTestApp(t *testing.T, id, secret, status string, port int) {
	t.Helper()
	app := installedApp{
		ID: id, Name: id, Version: "1.0.0", Spec: "1.0",
		Port: port, Unit: appUnitName(id), Secret: secret, Status: status,
	}
	if err := insertInstalledApp(reg.db, app); err != nil {
		t.Fatalf("insertInstalledApp: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// 认证请求全链路：会话有效 → 上游收到正确 User/Auth/Roles + 路径前缀
// 原样透传。
// ─────────────────────────────────────────────────────────────

func TestProxy_AuthenticatedRequest_FullChain(t *testing.T) {
	common.InitAuth("proxy-test-secret")
	mux := setupProxyTest(t)
	fu := newFakeUpstream(t)
	installTestApp(t, "photos", "app-secret-123", statusInstalled, fu.port(t))

	token := loginToken(t, "fm")
	req := httptest.NewRequest(http.MethodGet, "/apps/photos/gallery/1.jpg", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	before := time.Now().Unix()
	mux.ServeHTTP(rec, req)
	after := time.Now().Unix()

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if fu.count.Load() != 1 {
		t.Fatalf("上游应收到 1 次请求, got %d", fu.count.Load())
	}

	// 路径前缀原样透传（§5.2「应用自己处理前缀」）：上游收到的是含
	// /apps/photos/ 前缀的完整路径，不是剥过前缀的 /gallery/1.jpg。
	wantPath := "/apps/photos/gallery/1.jpg"
	if fu.lastReq.URL.Path != wantPath {
		t.Fatalf("路径前缀应原样透传, want %q got %q", wantPath, fu.lastReq.URL.Path)
	}

	gotUser := fu.lastHdr.Get("X-NAS-User")
	if gotUser != "fm" {
		t.Fatalf("X-NAS-User 应为会话用户 fm, got %q", gotUser)
	}
	if gotRoles := fu.lastHdr.Get("X-NAS-Roles"); gotRoles != "" {
		t.Fatalf("单用户架构下 X-NAS-Roles 应为空字符串, got %q", gotRoles)
	}

	gotTs := fu.lastHdr.Get("X-NAS-Time")
	ts, err := strconv.ParseInt(gotTs, 10, 64)
	if err != nil {
		t.Fatalf("X-NAS-Time 应为十进制 unix 秒, got %q err=%v", gotTs, err)
	}
	if ts < before || ts > after {
		t.Fatalf("X-NAS-Time 应落在请求处理窗口内: got %d, want [%d,%d]", ts, before, after)
	}

	gotSig := fu.lastHdr.Get("X-NAS-Auth")
	if gotSig == "" {
		t.Fatal("X-NAS-Auth 不应为空")
	}
	// HMAC 往返：上游侧用 VerifyNASAuth 验真（§5.3）。
	if !VerifyNASAuth("app-secret-123", gotUser, gotTs, gotSig, time.Now().Unix()) {
		t.Fatal("VerifyNASAuth 应验证面板注入的签名通过")
	}
	// 错误密钥必须验证失败，确认签名确实绑定了该应用的专属 secret。
	if VerifyNASAuth("wrong-secret", gotUser, gotTs, gotSig, time.Now().Unix()) {
		t.Fatal("错误 secret 不应验证通过")
	}
}

// ─────────────────────────────────────────────────────────────
// 未认证 → 401，且上游零请求（请求不得到达上游）。
// ─────────────────────────────────────────────────────────────

func TestProxy_Unauthenticated_401_UpstreamNeverCalled(t *testing.T) {
	common.InitAuth("proxy-test-secret")
	mux := setupProxyTest(t)
	fu := newFakeUpstream(t)
	installTestApp(t, "photos", "app-secret-123", statusInstalled, fu.port(t))

	cases := []struct {
		name   string
		mutate func(r *http.Request)
	}{
		{"无 Authorization 头", func(r *http.Request) {}},
		{"非 Bearer scheme", func(r *http.Request) { r.Header.Set("Authorization", "Basic xxx") }},
		{"伪造/篡改 token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer not-a-real-jwt") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/apps/photos/", nil)
			tc.mutate(req)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("want 401, got %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
	if fu.count.Load() != 0 {
		t.Fatalf("未认证请求不应到达上游, got %d 次调用", fu.count.Load())
	}
}

// ─────────────────────────────────────────────────────────────
// 客户端伪造 X-NAS-* 头 → 上游收到的必须是会话身份计算出的真实值，
// 不是客户端注入的值；大小写变体伪造同样必须被剥离
// （net/http.Header 内部以 CanonicalHeaderKey 存储，Set/Get/Del 对任意
// 大小写输入都会先归一化到同一个 canonical key，所以 Del("X-Nas-User")
// 这个具体大小写写法本身就对 "x-nas-user"/"X-NAS-USER" 等变体生效——
// 用真实请求断言这一行为，不单独起进程验证）。
// ─────────────────────────────────────────────────────────────

func TestProxy_ForgedNASHeaders_AreStrippedAndOverwritten(t *testing.T) {
	common.InitAuth("proxy-test-secret")
	mux := setupProxyTest(t)
	fu := newFakeUpstream(t)
	installTestApp(t, "photos", "app-secret-123", statusInstalled, fu.port(t))

	token := loginToken(t, "fm")

	headerCasingVariants := [][4]string{
		{"X-NAS-User", "X-NAS-Roles", "X-NAS-Auth", "X-NAS-Time"},
		{"x-nas-user", "x-nas-roles", "x-nas-auth", "x-nas-time"},
		{"X-Nas-User", "X-Nas-Roles", "X-Nas-Auth", "X-Nas-Time"},
		{"X-NAS-USER", "X-NAS-ROLES", "X-NAS-AUTH", "X-NAS-TIME"},
	}

	for _, v := range headerCasingVariants {
		t.Run(v[0], func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/apps/photos/", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set(v[0], "root")                 // 伪造身份
			req.Header.Set(v[1], "admin,superuser")      // 伪造角色
			req.Header.Set(v[2], "deadbeefdeadbeefdead") // 伪造签名
			req.Header.Set(v[3], "1000000000")           // 伪造时间（过期值）
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
			}
			if got := fu.lastHdr.Get("X-NAS-User"); got != "fm" {
				t.Fatalf("伪造头 %s 未被剥离: 上游收到 X-NAS-User=%q, want fm (伪造值 root 不应存活)", v[0], got)
			}
			if got := fu.lastHdr.Get("X-NAS-Roles"); got != "" {
				t.Fatalf("伪造头 %s 未被剥离: 上游收到 X-NAS-Roles=%q, want 空字符串", v[1], got)
			}
			gotTs := fu.lastHdr.Get("X-NAS-Time")
			if gotTs == "1000000000" {
				t.Fatalf("伪造的 X-NAS-Time 未被剥离: 上游收到了客户端注入的过期时间戳")
			}
			gotSig := fu.lastHdr.Get("X-NAS-Auth")
			if gotSig == "deadbeefdeadbeefdead" {
				t.Fatalf("伪造的 X-NAS-Auth 未被剥离: 上游收到了客户端注入的签名")
			}
			if !VerifyNASAuth("app-secret-123", "fm", gotTs, gotSig, time.Now().Unix()) {
				t.Fatal("剥离伪造头后应仍能用真实签名验证通过")
			}

			// 确认上游请求里真的只有一个该头的值（没有客户端伪造值与面板
			// 真实值并存——Set 覆盖而非 Add 的语义完整性）。
			if n := len(fu.lastHdr.Values("X-NAS-User")); n != 1 {
				t.Fatalf("X-NAS-User 应恰好出现一次, got %d 次", n)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────
// 时间容差 ±60s 单元测试（§5.3）。
// ─────────────────────────────────────────────────────────────

func TestVerifyNASAuth_TimeSkewTolerance(t *testing.T) {
	const secret = "skew-test-secret"
	const user = "fm"
	now := time.Now().Unix()

	cases := []struct {
		name  string
		ts    int64
		valid bool
	}{
		{"同一秒", now, true},
		{"+59s 在容差内", now + 59, true},
		{"-59s 在容差内", now - 59, true},
		{"恰好 +60s 边界内", now + 60, true},
		{"恰好 -60s 边界内", now - 60, true},
		{"+61s 超出容差", now + 61, false},
		{"-61s 超出容差", now - 61, false},
		{"+3600s 远超容差", now + 3600, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sig := SignNASAuth(secret, user, tc.ts)
			got := VerifyNASAuth(secret, user, strconv.FormatInt(tc.ts, 10), sig, now)
			if got != tc.valid {
				t.Fatalf("ts=%d now=%d: want valid=%v, got %v", tc.ts, now, tc.valid, got)
			}
		})
	}
}

func TestVerifyNASAuth_RejectsTamperedSigOrWrongUser(t *testing.T) {
	const secret = "tamper-test-secret"
	now := time.Now().Unix()
	sig := SignNASAuth(secret, "fm", now)
	tsStr := strconv.FormatInt(now, 10)

	if !VerifyNASAuth(secret, "fm", tsStr, sig, now) {
		t.Fatal("合法签名应验证通过")
	}
	if VerifyNASAuth(secret, "attacker", tsStr, sig, now) {
		t.Fatal("签名是对 fm 签的，attacker 不应验证通过")
	}
	if VerifyNASAuth(secret, "fm", tsStr, sig+"ff", now) {
		t.Fatal("篡改后的签名不应验证通过")
	}
	if VerifyNASAuth(secret, "fm", "not-a-number", sig, now) {
		t.Fatal("非法时间戳格式应验证失败，不应 panic")
	}
}

// ─────────────────────────────────────────────────────────────
// 停用应用 503 / 未注册 404。
// ─────────────────────────────────────────────────────────────

func TestProxy_StoppedApp_503(t *testing.T) {
	common.InitAuth("proxy-test-secret")
	mux := setupProxyTest(t)
	fu := newFakeUpstream(t)
	installTestApp(t, "photos", "app-secret-123", statusStopped, fu.port(t))

	req := httptest.NewRequest(http.MethodGet, "/apps/photos/", nil)
	req.Header.Set("Authorization", "Bearer "+loginToken(t, "fm"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("已停止应用应返回 503, got %d body=%s", rec.Code, rec.Body.String())
	}
	if fu.count.Load() != 0 {
		t.Fatalf("停用应用不应转发到上游, got %d 次调用", fu.count.Load())
	}
}

func TestProxy_UnregisteredApp_404(t *testing.T) {
	common.InitAuth("proxy-test-secret")
	mux := setupProxyTest(t)

	req := httptest.NewRequest(http.MethodGet, "/apps/does-not-exist/", nil)
	req.Header.Set("Authorization", "Bearer "+loginToken(t, "fm"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("未注册应用应返回 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestProxy_PendingApp_404(t *testing.T) {
	// pending 是 install 验签解包后、confirm 前的台账状态（未完成安装），
	// 与 handleAppProxy 当前判定一致：pending 等同"应用未安装"，404。
	common.InitAuth("proxy-test-secret")
	mux := setupProxyTest(t)
	installTestApp(t, "photos", "app-secret-123", statusPending, 18010)

	req := httptest.NewRequest(http.MethodGet, "/apps/photos/", nil)
	req.Header.Set("Authorization", "Bearer "+loginToken(t, "fm"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("pending 状态应用应返回 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ─────────────────────────────────────────────────────────────
// 转发错误不泄内部细节：上游不可达（已注册但没有进程在监听分配端口）
// 时返回 503，响应体不含连接错误的底层细节（地址/系统调用名等）。
// ─────────────────────────────────────────────────────────────

func TestProxy_UpstreamUnreachable_503_NoInternalLeak(t *testing.T) {
	common.InitAuth("proxy-test-secret")
	mux := setupProxyTest(t)
	// 挑一个大概率没有进程监听的端口（落在合法范围内，但未起任何假上游）。
	installTestApp(t, "ghost", "app-secret-123", statusInstalled, 18777)

	req := httptest.NewRequest(http.MethodGet, "/apps/ghost/", nil)
	req.Header.Set("Authorization", "Bearer "+loginToken(t, "fm"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("上游不可达应返回 503, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, leak := range []string{"connection refused", "dial tcp", "127.0.0.1:18777"} {
		if containsFold(body, leak) {
			t.Fatalf("响应体不应泄露内部连接细节 %q, body=%s", leak, body)
		}
	}
}

// containsFold 是大小写不敏感的 strings.Contains，避免额外引入依赖。
func containsFold(s, substr string) bool {
	return len(s) >= len(substr) && indexFold(s, substr) >= 0
}

func indexFold(s, substr string) int {
	ls, lsub := len(s), len(substr)
	if lsub == 0 {
		return 0
	}
	for i := 0; i+lsub <= ls; i++ {
		if equalFold(s[i:i+lsub], substr) {
			return i
		}
	}
	return -1
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
