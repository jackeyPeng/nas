package apps

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"nas-panel/common"
)

// setupExternalTest 复用 setupLifecycleTest 的 db+fakeExecutor+裸路由装配，
// 另外重置外部应用的健康轮询单例，避免跨测试串状态（与 setupHealthTest
// 重置 poller 同理）。
func setupExternalTest(t *testing.T) (*http.ServeMux, *fakeExecutor) {
	t.Helper()
	mux, fe := setupLifecycleTest(t)
	externalPoller = &externalPollerState{states: map[string]externalHealthState{}}
	common.InitEventBusForTest(t.TempDir())
	// 事件总线是包级单例，不会在测试间自动重置；兜底清空本测试产生、
	// 但未被消费的事件，避免遗留在 eventChan 里被下一个测试的订阅者
	// 误收（同 setupHealthTest 的 cleanup 套路）。
	t.Cleanup(common.DrainEventsForTest)
	return mux, fe
}

// jsonBody 把 map/struct 编成 *bytes.Buffer，供 doRequest 发 JSON 请求体。
func jsonBody(t *testing.T, v interface{}) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json body: %v", err)
	}
	return bytes.NewBuffer(b)
}

// fakeHealthzResponse 构造 externalHealthHTTPGet 的假响应（仅需要
// StatusCode + 可关闭的 Body，不碰真实网络）。
func fakeHealthzResponse(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

// stubExternalHealthHTTP 把包级 externalHealthHTTPGet 替换为按 url 查表的
// 假实现（与 market_test.go 的 stubMarketHTTP 同一模式），返回的 restore
// 必须 defer 调用还原——这是包级变量，被全部测试共享。
func stubExternalHealthHTTP(t *testing.T, healthy map[string]bool) func() {
	t.Helper()
	prev := externalHealthHTTPGet
	externalHealthHTTPGet = func(url string) (*http.Response, error) {
		if ok, found := healthy[url]; found {
			if ok {
				return fakeHealthzResponse(http.StatusOK), nil
			}
			return fakeHealthzResponse(http.StatusServiceUnavailable), nil
		}
		return fakeHealthzResponse(http.StatusOK), nil
	}
	return func() { externalHealthHTTPGet = prev }
}

// ─────────────────────────────────────────────────────────────
// 注册
// ─────────────────────────────────────────────────────────────

func TestExternalRegisterSuccess(t *testing.T) {
	mux, _ := setupExternalTest(t)

	req := externalRegisterRequest{
		ID:   "ankang",
		Name: "安康",
		URL:  "http://192.168.1.50:8080",
	}
	rec := doRequest(mux, http.MethodPost, "/api/apps/external", jsonBody(t, req), "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("register: %d body=%s", rec.Code, rec.Body.String())
	}
	var sum externalSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &sum); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sum.Kind != "external" || sum.ID != "ankang" || sum.Status != externalStatusUnknown {
		t.Fatalf("unexpected summary: %+v", sum)
	}

	a, err := getExternalApp(reg.db, "ankang")
	if err != nil || a == nil {
		t.Fatalf("getExternalApp: %v a=%v", err, a)
	}
	if a.RegisteredAt == "" || a.UpdatedAt == "" {
		t.Fatalf("registered_at/updated_at 应被写入: %+v", a)
	}
}

func TestExternalRegisterIDCollisionWithManaged(t *testing.T) {
	mux, _ := setupExternalTest(t)

	if err := insertInstalledApp(reg.db, installedApp{
		ID: "photos", Name: "照片", Version: "1.0.0", Spec: "{}",
		Port: 18100, Unit: appUnitName("photos"), Status: statusInstalled,
	}); err != nil {
		t.Fatalf("insertInstalledApp: %v", err)
	}

	req := externalRegisterRequest{ID: "photos", Name: "冲突", URL: "http://127.0.0.1:9999"}
	rec := doRequest(mux, http.MethodPost, "/api/apps/external", jsonBody(t, req), "application/json")
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestExternalRegisterDuplicate(t *testing.T) {
	mux, _ := setupExternalTest(t)

	req := externalRegisterRequest{ID: "ankang", Name: "安康", URL: "http://127.0.0.1:8080"}
	rec := doRequest(mux, http.MethodPost, "/api/apps/external", jsonBody(t, req), "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("first register: %d body=%s", rec.Code, rec.Body.String())
	}

	rec = doRequest(mux, http.MethodPost, "/api/apps/external", jsonBody(t, req), "application/json")
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409 on duplicate, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestExternalRegisterFieldValidation(t *testing.T) {
	mux, _ := setupExternalTest(t)

	cases := []struct {
		name string
		req  externalRegisterRequest
	}{
		{"id 非法-大写", externalRegisterRequest{ID: "Ankang", Name: "安康", URL: "http://127.0.0.1:8080"}},
		{"id 非法-以横线开头", externalRegisterRequest{ID: "-ankang", Name: "安康", URL: "http://127.0.0.1:8080"}},
		{"id 非法-过长", externalRegisterRequest{ID: strings.Repeat("a", 33), Name: "安康", URL: "http://127.0.0.1:8080"}},
		{"name 为空", externalRegisterRequest{ID: "ankang", Name: "  ", URL: "http://127.0.0.1:8080"}},
		{"url 非法-无 scheme", externalRegisterRequest{ID: "ankang", Name: "安康", URL: "192.168.1.50:8080"}},
		{"url 非法-ftp scheme", externalRegisterRequest{ID: "ankang", Name: "安康", URL: "ftp://127.0.0.1:8080"}},
		{"unit 非法-缺 .service 后缀", externalRegisterRequest{ID: "ankang", Name: "安康", URL: "http://127.0.0.1:8080", Unit: "ankang"}},
		{"healthzUrl 非法 scheme", externalRegisterRequest{ID: "ankang", Name: "安康", URL: "http://127.0.0.1:8080", HealthzURL: "javascript:alert(1)"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := doRequest(mux, http.MethodPost, "/api/apps/external", jsonBody(t, c.req), "application/json")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────
// 启停（无 unit → 409；有 unit → 走 Executor）
// ─────────────────────────────────────────────────────────────

func TestExternalActionNoUnitReturns409(t *testing.T) {
	mux, _ := setupExternalTest(t)

	req := externalRegisterRequest{ID: "ankang", Name: "安康", URL: "http://127.0.0.1:8080"}
	if rec := doRequest(mux, http.MethodPost, "/api/apps/external", jsonBody(t, req), "application/json"); rec.Code != http.StatusOK {
		t.Fatalf("register: %d body=%s", rec.Code, rec.Body.String())
	}

	for _, action := range []string{"start", "stop", "restart"} {
		rec := doRequest(mux, http.MethodPost, "/api/apps/external/ankang/"+action, nil, "")
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s: want 409, got %d body=%s", action, rec.Code, rec.Body.String())
		}
	}
}

func TestExternalActionWithUnitCallsExecutor(t *testing.T) {
	mux, fe := setupExternalTest(t)

	req := externalRegisterRequest{ID: "ankang", Name: "安康", URL: "http://127.0.0.1:8080", Unit: "ankang.service"}
	if rec := doRequest(mux, http.MethodPost, "/api/apps/external", jsonBody(t, req), "application/json"); rec.Code != http.StatusOK {
		t.Fatalf("register: %d body=%s", rec.Code, rec.Body.String())
	}

	cases := []struct {
		action string
		want   string
	}{
		{"start", "systemctl start ankang.service"},
		{"stop", "systemctl stop ankang.service"},
		{"restart", "systemctl restart ankang.service"},
	}
	for _, c := range cases {
		fe.calls = nil
		rec := doRequest(mux, http.MethodPost, "/api/apps/external/ankang/"+c.action, nil, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: want 200, got %d body=%s", c.action, rec.Code, rec.Body.String())
		}
		found := false
		for _, call := range fe.calls {
			if call == c.want {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: 未记录到期望调用 %q, got: %v", c.action, c.want, fe.calls)
		}
	}
}

func TestExternalActionUnknownAppReturns404(t *testing.T) {
	mux, _ := setupExternalTest(t)
	rec := doRequest(mux, http.MethodPost, "/api/apps/external/nope/start", nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ─────────────────────────────────────────────────────────────
// 注销（需 confirm:true）
// ─────────────────────────────────────────────────────────────

func TestExternalDeleteRequiresConfirm(t *testing.T) {
	mux, _ := setupExternalTest(t)

	req := externalRegisterRequest{ID: "ankang", Name: "安康", URL: "http://127.0.0.1:8080"}
	if rec := doRequest(mux, http.MethodPost, "/api/apps/external", jsonBody(t, req), "application/json"); rec.Code != http.StatusOK {
		t.Fatalf("register: %d body=%s", rec.Code, rec.Body.String())
	}

	rec := doRequest(mux, http.MethodDelete, "/api/apps/external/ankang", jsonBody(t, map[string]bool{"confirm": false}), "application/json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("无 confirm 应 400, got %d body=%s", rec.Code, rec.Body.String())
	}

	a, _ := getExternalApp(reg.db, "ankang")
	if a == nil {
		t.Fatal("未确认不应删除登记")
	}

	rec = doRequest(mux, http.MethodDelete, "/api/apps/external/ankang", jsonBody(t, map[string]bool{"confirm": true}), "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm=true 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	a, _ = getExternalApp(reg.db, "ankang")
	if a != nil {
		t.Fatal("confirm 后登记应被删除")
	}
}

func TestExternalDeleteUnknownAppReturns404(t *testing.T) {
	mux, _ := setupExternalTest(t)
	rec := doRequest(mux, http.MethodDelete, "/api/apps/external/nope", jsonBody(t, map[string]bool{"confirm": true}), "application/json")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ─────────────────────────────────────────────────────────────
// GET /api/apps 统一列表
// ─────────────────────────────────────────────────────────────

func TestUnifiedAppsListMergesManagedAndExternal(t *testing.T) {
	mux, _ := setupExternalTest(t)

	if err := insertInstalledApp(reg.db, installedApp{
		ID: "photos", Name: "照片", Version: "1.0.0", Spec: "{}",
		Port: 18110, Unit: appUnitName("photos"), Status: statusInstalled,
	}); err != nil {
		t.Fatalf("insertInstalledApp: %v", err)
	}

	req := externalRegisterRequest{ID: "ankang", Name: "安康", URL: "http://127.0.0.1:8080"}
	if rec := doRequest(mux, http.MethodPost, "/api/apps/external", jsonBody(t, req), "application/json"); rec.Code != http.StatusOK {
		t.Fatalf("register: %d body=%s", rec.Code, rec.Body.String())
	}

	rec := doRequest(mux, http.MethodGet, "/api/apps", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d body=%s", rec.Code, rec.Body.String())
	}
	var listResp struct {
		Apps []map[string]interface{} `json:"apps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	items := listResp.Apps
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d: %+v", len(items), items)
	}

	var sawManaged, sawExternal bool
	for _, it := range items {
		switch it["id"] {
		case "photos":
			if it["kind"] != "managed" {
				t.Fatalf("photos kind 应为 managed: %+v", it)
			}
			sawManaged = true
		case "ankang":
			if it["kind"] != "external" {
				t.Fatalf("ankang kind 应为 external: %+v", it)
			}
			sawExternal = true
		}
	}
	if !sawManaged || !sawExternal {
		t.Fatalf("列表应同时包含 managed 与 external: %+v", items)
	}
}

// ─────────────────────────────────────────────────────────────
// 健康翻转（healthzUrl 探测）
// ─────────────────────────────────────────────────────────────

func TestExternalHealthThreeFailuresBeforeUnhealthy(t *testing.T) {
	_, _ = setupExternalTest(t)
	const healthzURL = "http://127.0.0.1:19001/healthz"

	restore := stubExternalHealthHTTP(t, map[string]bool{healthzURL: false})
	defer restore()

	a := externalApp{ID: "ankang", Name: "安康", URL: "http://127.0.0.1:19001", HealthzURL: healthzURL}
	if err := insertExternalApp(reg.db, a); err != nil {
		t.Fatalf("insertExternalApp: %v", err)
	}
	resetExternalHealthState(a.ID)

	for i := 0; i < 2; i++ {
		pollExternalAppsOnce()
		common.DrainEventsForTest()
		st, ok := getExternalHealthSnapshot("ankang")
		if !ok || st.Status == externalStatusUnhealthy {
			t.Fatalf("第 %d 次失败后不应已翻转: %+v ok=%v", i+1, st, ok)
		}
	}

	pollExternalAppsOnce()
	common.DrainEventsForTest()
	st, _ := getExternalHealthSnapshot("ankang")
	if st.Status != externalStatusUnhealthy || st.ConsecutiveFail != healthFailThreshold {
		t.Fatalf("第 3 次失败后应翻转为 unhealthy: %+v", st)
	}
}

func TestExternalHealthEmitsExactlyOneEventOnFlip(t *testing.T) {
	_, _ = setupExternalTest(t)
	const healthzURL = "http://127.0.0.1:19002/healthz"

	restore := stubExternalHealthHTTP(t, map[string]bool{healthzURL: false})
	defer restore()

	ch := common.Subscribe()
	defer common.Unsubscribe(ch)

	a := externalApp{ID: "ankang", Name: "安康", URL: "http://127.0.0.1:19002", HealthzURL: healthzURL}
	if err := insertExternalApp(reg.db, a); err != nil {
		t.Fatalf("insertExternalApp: %v", err)
	}
	resetExternalHealthState(a.ID)

	for i := 0; i < 2; i++ {
		pollExternalAppsOnce()
		common.DrainEventsForTest()
		select {
		case ev := <-ch:
			t.Fatalf("第 %d 次失败不应发事件，却收到: %+v", i+1, ev)
		default:
		}
	}

	pollExternalAppsOnce()
	common.DrainEventsForTest()
	select {
	case ev := <-ch:
		if ev.Code != "external.unhealthy" {
			t.Fatalf("want code=external.unhealthy, got %s", ev.Code)
		}
		if ev.Type != common.EventWarn {
			t.Fatalf("want type=warn, got %s", ev.Type)
		}
		var params map[string]interface{}
		if err := json.Unmarshal([]byte(ev.Params), &params); err != nil {
			t.Fatalf("decode params: %v", err)
		}
		if params["id"] != "ankang" {
			t.Fatalf("params.id 应为 ankang: %+v", params)
		}
	default:
		t.Fatal("第 3 次失败应发一条 external.unhealthy 事件")
	}

	// 已处于 unhealthy，继续失败不应重复发事件。
	pollExternalAppsOnce()
	common.DrainEventsForTest()
	select {
	case ev := <-ch:
		t.Fatalf("已 unhealthy 后继续失败不应重复发事件，却收到: %+v", ev)
	default:
	}
}

func TestExternalHealthRecoveryEmitsRecoveredEvent(t *testing.T) {
	_, _ = setupExternalTest(t)
	const healthzURL = "http://127.0.0.1:19003/healthz"

	restore := stubExternalHealthHTTP(t, map[string]bool{healthzURL: false})

	ch := common.Subscribe()
	defer common.Unsubscribe(ch)

	a := externalApp{ID: "ankang", Name: "安康", URL: "http://127.0.0.1:19003", HealthzURL: healthzURL}
	if err := insertExternalApp(reg.db, a); err != nil {
		t.Fatalf("insertExternalApp: %v", err)
	}
	resetExternalHealthState(a.ID)

	for i := 0; i < 3; i++ {
		pollExternalAppsOnce()
	}
	common.DrainEventsForTest()
	drainEvents(ch) // 清掉 unhealthy 事件，只关心恢复事件
	restore()

	restore2 := stubExternalHealthHTTP(t, map[string]bool{healthzURL: true})
	defer restore2()

	pollExternalAppsOnce()
	common.DrainEventsForTest()

	select {
	case ev := <-ch:
		if ev.Code != "external.recovered" {
			t.Fatalf("want code=external.recovered, got %s", ev.Code)
		}
		if ev.Type != common.EventInfo {
			t.Fatalf("want type=info, got %s", ev.Type)
		}
	default:
		t.Fatal("恢复后应发 external.recovered 事件")
	}

	st, _ := getExternalHealthSnapshot("ankang")
	if st.Status != externalStatusRunning || st.ConsecutiveFail != 0 {
		t.Fatalf("恢复后状态不对: %+v", st)
	}
}

func TestExternalHealthNoHealthzNoUnitIsUnknownAndNotPolled(t *testing.T) {
	_, _ = setupExternalTest(t)

	a := externalApp{ID: "ankang", Name: "安康", URL: "http://127.0.0.1:19004"}
	if err := insertExternalApp(reg.db, a); err != nil {
		t.Fatalf("insertExternalApp: %v", err)
	}
	resetExternalHealthState(a.ID)

	pollExternalAppsOnce()
	common.DrainEventsForTest()

	st, ok := getExternalHealthSnapshot("ankang")
	if !ok || st.Status != externalStatusUnknown {
		t.Fatalf("无 healthzUrl/unit 应恒为 unknown: %+v ok=%v", st, ok)
	}
}

func TestExternalStatusFromUnitActiveStateWhenNoHealthz(t *testing.T) {
	mux, fe := setupExternalTest(t)

	req := externalRegisterRequest{ID: "ankang", Name: "安康", URL: "http://127.0.0.1:8080", Unit: "ankang.service"}
	if rec := doRequest(mux, http.MethodPost, "/api/apps/external", jsonBody(t, req), "application/json"); rec.Code != http.StatusOK {
		t.Fatalf("register: %d body=%s", rec.Code, rec.Body.String())
	}

	// 尚未 start，unitActive 里没有记录 → UnitActiveState 返回 "inactive" → stopped。
	a, _ := getExternalApp(reg.db, "ankang")
	if got := externalAppStatus(fe, *a); got != externalStatusStopped {
		t.Fatalf("未启动时应为 stopped, got %s", got)
	}

	if rec := doRequest(mux, http.MethodPost, "/api/apps/external/ankang/start", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("start: %d body=%s", rec.Code, rec.Body.String())
	}
	if got := externalAppStatus(fe, *a); got != externalStatusRunning {
		t.Fatalf("启动后应为 running, got %s", got)
	}
}

// ─────────────────────────────────────────────────────────────
// 事件：注册/注销/操作
// ─────────────────────────────────────────────────────────────

func TestExternalLifecycleEvents(t *testing.T) {
	mux, _ := setupExternalTest(t)
	ch := common.Subscribe()
	defer common.Unsubscribe(ch)

	req := externalRegisterRequest{ID: "ankang", Name: "安康", URL: "http://127.0.0.1:8080", Unit: "ankang.service"}
	if rec := doRequest(mux, http.MethodPost, "/api/apps/external", jsonBody(t, req), "application/json"); rec.Code != http.StatusOK {
		t.Fatalf("register: %d body=%s", rec.Code, rec.Body.String())
	}
	assertNextEventCode(t, ch, "external.registered")

	if rec := doRequest(mux, http.MethodPost, "/api/apps/external/ankang/start", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("start: %d body=%s", rec.Code, rec.Body.String())
	}
	assertNextEventCode(t, ch, "external.action")

	rec := doRequest(mux, http.MethodDelete, "/api/apps/external/ankang", jsonBody(t, map[string]bool{"confirm": true}), "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d body=%s", rec.Code, rec.Body.String())
	}
	assertNextEventCode(t, ch, "external.unregistered")
}

func assertNextEventCode(t *testing.T, ch chan common.Event, want string) {
	t.Helper()
	common.DrainEventsForTest()
	select {
	case ev := <-ch:
		if ev.Code != want {
			t.Fatalf("want code=%s, got %s", want, ev.Code)
		}
	default:
		t.Fatalf("应收到事件 code=%s，但没收到", want)
	}
}
