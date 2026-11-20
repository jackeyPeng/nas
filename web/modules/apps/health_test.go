package apps

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"nas-panel/common"
	"nas-panel/modules/system"
)

// toggleExecutor 是专供本文件使用的 Executor 测试替身：只实现本测试需要
// 断言的 CheckHealthz（按端口独立控制健康/故障，不像 fakeExecutor 那样
// 全局共享一个 failers 开关），其余方法留空实现——health 轮询器只会调用
// CheckHealthz，其它方法不会被触达。
type toggleExecutor struct {
	mu      sync.Mutex
	healthy map[int]bool // port -> 是否健康；未登记的端口默认健康
}

func newToggleExecutor() *toggleExecutor {
	return &toggleExecutor{healthy: map[int]bool{}}
}

func (f *toggleExecutor) setHealthy(port int, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.healthy[port] = ok
}

func (f *toggleExecutor) CheckHealthz(port int, healthzPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ok, set := f.healthy[port]; set && !ok {
		return &healthzProbeError{port: port}
	}
	return nil
}

// healthzProbeError 是探测失败时返回的错误（内容无关紧要，只需非 nil）。
type healthzProbeError struct{ port int }

func (e *healthzProbeError) Error() string { return "healthz probe failed" }

func (f *toggleExecutor) AddAppUser(username string) (string, error) { return "", nil }
func (f *toggleExecutor) DelAppUser(username string) (string, error) { return "", nil }
func (f *toggleExecutor) DaemonReload() (string, error)              { return "", nil }
func (f *toggleExecutor) EnableUnit(unit string) (string, error)     { return "", nil }
func (f *toggleExecutor) DisableUnit(unit string) (string, error)    { return "", nil }
func (f *toggleExecutor) StartUnit(unit string) (string, error)      { return "", nil }
func (f *toggleExecutor) StopUnit(unit string) (string, error)       { return "", nil }
func (f *toggleExecutor) RestartUnit(unit string) (string, error)    { return "", nil }
func (f *toggleExecutor) UnitActiveState(unit string) string         { return "active" }
func (f *toggleExecutor) ProbePort(port int) bool                    { return true }
func (f *toggleExecutor) MkdirAllChown(path string, mode os.FileMode, uid, gid int) error {
	return nil
}
func (f *toggleExecutor) RemoveAll(path string) error              { return nil }
func (f *toggleExecutor) Rename(oldPath, newPath string) error     { return nil }
func (f *toggleExecutor) WriteUnitFile(unit, content string) error { return nil }
func (f *toggleExecutor) WriteFile(path string, data []byte, mode os.FileMode) error {
	return nil
}
func (f *toggleExecutor) LookupUser(username string) (int, int, error) { return 0, 0, nil }

// setupHealthTest 打开一个全新 apps.db，装入 toggleExecutor，并初始化
// 事件总线（同步消费模式，配合 common.DrainEventsForTest）。不调用
// RegisterRoutesBare/startHealthPoller——测试直接调 pollOnce()/
// handleAppHealth 驱动，不依赖真实 60s ticker 或路由注册。
func setupHealthTest(t *testing.T) (*toggleExecutor, func()) {
	t.Helper()
	db, err := openAppsDB(t.TempDir())
	if err != nil {
		t.Fatalf("openAppsDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	fe := newToggleExecutor()
	reg = &registry{db: db, dataDir: t.TempDir(), executor: fe}

	// 轮询状态是包级单例（poller），每个测试重置，避免跨测试串状态。
	poller = &healthPoller{states: map[string]appHealthState{}}

	common.InitEventBusForTest(t.TempDir())
	ch := common.Subscribe()
	cleanup := func() {
		common.Unsubscribe(ch)
		// 事件总线是包级单例，不会在测试间自动重置；兜底清空本测试可能
		// 残留在共享 channel 里的事件，避免被下一个测试误收。
		common.DrainEventsForTest()
	}
	return fe, cleanup
}

// insertRunningApp 往台账插入一条 status=installed 的应用记录，供轮询器探测。
func insertRunningApp(t *testing.T, id string, port int) {
	t.Helper()
	if err := insertInstalledApp(reg.db, installedApp{
		ID:      id,
		Name:    "测试应用-" + id,
		Version: "1.0.0",
		Spec:    "{}",
		Port:    port,
		Unit:    appUnitName(id),
		Healthz: "/healthz",
		Status:  statusInstalled,
	}); err != nil {
		t.Fatalf("insertInstalledApp: %v", err)
	}
}

func drainEvents(ch chan common.Event) []common.Event {
	var out []common.Event
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		default:
			return out
		}
	}
}

// TestPollOnceThreeFailuresBeforeUnhealthy 验证：连续 3 次失败才翻转为
// unhealthy 并发事件；第 2 次失败不发事件（避免事件风暴，§6 硬约束）。
func TestPollOnceThreeFailuresBeforeUnhealthy(t *testing.T) {
	fe, cleanup := setupHealthTest(t)
	defer cleanup()

	insertRunningApp(t, "photos", 18001)
	fe.setHealthy(18001, false)

	// 第 1、2 次失败：不应翻转，不应发事件。
	pollOnce()
	common.DrainEventsForTest()
	st, ok := getAppHealthSnapshot("photos")
	if !ok || st.ConsecutiveFail != 1 || st.Status != healthRunning {
		t.Fatalf("第 1 次失败后状态不对: %+v ok=%v", st, ok)
	}

	pollOnce()
	common.DrainEventsForTest()
	st, _ = getAppHealthSnapshot("photos")
	if st.ConsecutiveFail != 2 || st.Status != healthRunning {
		t.Fatalf("第 2 次失败后状态不对（应仍为 running）: %+v", st)
	}

	// 第 3 次失败：翻转为 unhealthy，发一条事件。
	pollOnce()
	common.DrainEventsForTest()
	st, _ = getAppHealthSnapshot("photos")
	if st.ConsecutiveFail != 3 || st.Status != healthUnhealthy {
		t.Fatalf("第 3 次失败后应翻转为 unhealthy: %+v", st)
	}
}

// TestPollOnceEmitsExactlyOneEventOnFlip 用订阅 channel 直接断言：3 次失败
// 过程中只收到 1 条 apps.unhealthy 事件（第 2 次失败那一轮绝不发）。
func TestPollOnceEmitsExactlyOneEventOnFlip(t *testing.T) {
	fe, cleanup := setupHealthTest(t)
	defer cleanup()

	ch := common.Subscribe()
	defer common.Unsubscribe(ch)

	insertRunningApp(t, "photos", 18002)
	fe.setHealthy(18002, false)

	for i := 0; i < 2; i++ {
		pollOnce()
		common.DrainEventsForTest()
		select {
		case ev := <-ch:
			t.Fatalf("第 %d 次失败不应发事件，却收到: %+v", i+1, ev)
		default:
		}
	}

	pollOnce()
	common.DrainEventsForTest()
	select {
	case ev := <-ch:
		if ev.Code != "apps.unhealthy" {
			t.Fatalf("want code=apps.unhealthy, got %s", ev.Code)
		}
		if ev.Type != common.EventError {
			t.Fatalf("want type=error, got %s", ev.Type)
		}
		if ev.Source != "apps" {
			t.Fatalf("want source=apps, got %s", ev.Source)
		}
		var params map[string]interface{}
		if err := json.Unmarshal([]byte(ev.Params), &params); err != nil {
			t.Fatalf("decode event params: %v", err)
		}
		if params["id"] != "photos" {
			t.Fatalf("事件 params.id 应为 photos: %+v", params)
		}
		if got, want := params["consecutiveFail"], float64(3); got != want {
			t.Fatalf("事件 params.consecutiveFail 应为 3: got %v", got)
		}
	default:
		t.Fatal("第 3 次失败应发一条 apps.unhealthy 事件，但没收到")
	}

	// 再跑一轮（第 4 次失败）：已经是 unhealthy，不应再发事件。
	pollOnce()
	common.DrainEventsForTest()
	select {
	case ev := <-ch:
		t.Fatalf("已处于 unhealthy 后继续失败不应重复发事件，却收到: %+v", ev)
	default:
	}
}

// TestPollOnceRecoveryEmitsRecoveredEvent 验证从 unhealthy 恢复为 running
// 会发 apps.recovered 事件。
func TestPollOnceRecoveryEmitsRecoveredEvent(t *testing.T) {
	fe, cleanup := setupHealthTest(t)
	defer cleanup()

	ch := common.Subscribe()
	defer common.Unsubscribe(ch)

	insertRunningApp(t, "photos", 18003)
	fe.setHealthy(18003, false)
	for i := 0; i < 3; i++ {
		pollOnce()
	}
	common.DrainEventsForTest()
	drainEvents(ch) // 清掉 unhealthy 事件，只关心恢复事件

	fe.setHealthy(18003, true)
	pollOnce()
	common.DrainEventsForTest()

	select {
	case ev := <-ch:
		if ev.Code != "apps.recovered" {
			t.Fatalf("want code=apps.recovered, got %s", ev.Code)
		}
		if ev.Type != common.EventSuccess {
			t.Fatalf("want type=success, got %s", ev.Type)
		}
	default:
		t.Fatal("恢复后应发 apps.recovered 事件，但没收到")
	}

	st, _ := getAppHealthSnapshot("photos")
	if st.Status != healthRunning || st.ConsecutiveFail != 0 {
		t.Fatalf("恢复后状态不对: %+v", st)
	}
}

// TestPollOnceNeverRestartsApp 验证轮询器探测失败（哪怕持续失败多轮）
// 绝不调用任何 Start/Restart 类操作——§6 的硬约束：不自动重启。
// toggleExecutor 的 StartUnit/RestartUnit 没有调用记录机制，这里改用
// 能记录调用的 fakeExecutor 来断言零调用。
func TestPollOnceNeverRestartsApp(t *testing.T) {
	db, err := openAppsDB(t.TempDir())
	if err != nil {
		t.Fatalf("openAppsDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	fe := newFakeExecutor()
	fe.failers["CheckHealthz"] = &healthzProbeError{}
	reg = &registry{db: db, dataDir: t.TempDir(), executor: fe}
	poller = &healthPoller{states: map[string]appHealthState{}}
	common.InitEventBusForTest(t.TempDir())

	insertRunningApp(t, "photos", 18004)

	for i := 0; i < 6; i++ {
		pollOnce()
	}
	common.DrainEventsForTest()

	for _, call := range fe.calls {
		if strings.Contains(call, "systemctl start") || strings.Contains(call, "systemctl restart") {
			t.Fatalf("健康轮询不应触发重启/启动，却记录到调用: %s", call)
		}
	}
}

// TestPollOnceSkipsNonInstalledApps 验证台账状态非 installed（如 pending）
// 的应用不会被探测（不调用 CheckHealthz），健康状态展示为 stopped。
func TestPollOnceSkipsNonInstalledApps(t *testing.T) {
	db, err := openAppsDB(t.TempDir())
	if err != nil {
		t.Fatalf("openAppsDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	fe := newFakeExecutor()
	reg = &registry{db: db, dataDir: t.TempDir(), executor: fe}
	poller = &healthPoller{states: map[string]appHealthState{}}
	common.InitEventBusForTest(t.TempDir())

	if err := insertInstalledApp(reg.db, installedApp{
		ID: "pending1", Name: "待确认应用", Version: "1.0.0", Spec: "{}",
		Port: 18005, Unit: appUnitName("pending1"), Healthz: "/healthz",
		Status: statusPending,
	}); err != nil {
		t.Fatalf("insertInstalledApp: %v", err)
	}

	pollOnce()
	common.DrainEventsForTest()

	for _, call := range fe.calls {
		if strings.Contains(call, "healthz-check") {
			t.Fatalf("pending 应用不应被探测 healthz，却记录到调用: %s", call)
		}
	}
	st, ok := getAppHealthSnapshot("pending1")
	if !ok || st.Status != healthStopped {
		t.Fatalf("pending 应用健康态应为 stopped: %+v ok=%v", st, ok)
	}
}

// TestPublishAppHealthRegistryZeroApps 验证零应用安装时，system 侧不应
// 出现任何应用健康注册表行。
func TestPublishAppHealthRegistryZeroApps(t *testing.T) {
	_, cleanup := setupHealthTest(t)
	defer cleanup()

	pollOnce()

	items := system.GetAppHealthItemsForTest()
	if len(items) != 0 {
		t.Fatalf("零应用安装时应无应用健康注册表行，got: %+v", items)
	}
}

// TestPublishAppHealthRegistryWithApp 验证安装 1 个应用并经历健康翻转后，
// system 侧注册表快照包含该应用的健康行，内容随状态更新。
func TestPublishAppHealthRegistryWithApp(t *testing.T) {
	fe, cleanup := setupHealthTest(t)
	defer cleanup()

	insertRunningApp(t, "photos", 18006)

	pollOnce()
	items := system.GetAppHealthItemsForTest()
	if len(items) != 1 {
		t.Fatalf("1 个应用安装后应有 1 条健康行，got %d: %+v", len(items), items)
	}
	if items[0].Status != "pass" {
		t.Fatalf("健康时注册表行 status 应为 pass，got %s", items[0].Status)
	}

	fe.setHealthy(18006, false)
	for i := 0; i < 3; i++ {
		pollOnce()
	}
	items = system.GetAppHealthItemsForTest()
	if len(items) != 1 {
		t.Fatalf("故障后仍应有 1 条健康行（不是消失），got %d", len(items))
	}
	if items[0].Status != "fail" {
		t.Fatalf("不健康时注册表行 status 应为 fail，got %s", items[0].Status)
	}
}

// TestHandleAppHealthEndpoint 验证 GET /api/apps/<id>/health 的响应形状。
func TestHandleAppHealthEndpoint(t *testing.T) {
	fe, cleanup := setupHealthTest(t)
	defer cleanup()

	insertRunningApp(t, "photos", 18007)
	fe.setHealthy(18007, false)
	for i := 0; i < 3; i++ {
		pollOnce()
	}
	common.DrainEventsForTest()

	r := httptest.NewRequest(http.MethodGet, "/api/apps/photos/health", nil)
	rec := httptest.NewRecorder()
	handleAppHealth(rec, r, "photos")

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp appHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID != "photos" {
		t.Fatalf("want id=photos, got %s", resp.ID)
	}
	if resp.Status != healthUnhealthy {
		t.Fatalf("want status=unhealthy, got %s", resp.Status)
	}
	if resp.ConsecutiveFail != 3 {
		t.Fatalf("want consecutiveFail=3, got %d", resp.ConsecutiveFail)
	}
	if resp.LastProbeAt == "" {
		t.Fatalf("want non-empty lastProbeAt")
	}
	if resp.LastError == "" {
		t.Fatalf("want non-empty lastError")
	}
}

// TestHandleAppHealthEndpointUnknownApp 验证未安装的应用 id 返回 404。
func TestHandleAppHealthEndpointUnknownApp(t *testing.T) {
	_, cleanup := setupHealthTest(t)
	defer cleanup()

	r := httptest.NewRequest(http.MethodGet, "/api/apps/nope/health", nil)
	rec := httptest.NewRecorder()
	handleAppHealth(rec, r, "nope")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRunHealthPollerLoopDrivenByInjectedTick 验证轮询主循环由注入的
// tick channel 驱动（不依赖真实 60s 睡眠）：发 3 个 tick，驱动 3 轮探测，
// 累积 3 次失败后应翻转为 unhealthy。
func TestRunHealthPollerLoopDrivenByInjectedTick(t *testing.T) {
	fe, cleanup := setupHealthTest(t)
	defer cleanup()

	insertRunningApp(t, "photos", 18008)
	fe.setHealthy(18008, false)

	tick := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		runHealthPollerLoop(tick)
		close(done)
	}()

	for i := 0; i < 3; i++ {
		tick <- time.Now()
	}
	// 等最后一轮处理完：轮询循环里没有显式的"处理完成"信号，用小睡眠
	// 加轮询读取状态的方式等待收敛，避免测试本身引入真实定时依赖之外的
	// 脆弱 sleep（这里只是等 goroutine 调度，不是等业务意义上的 60s）。
	waitUntil(t, func() bool {
		st, ok := getAppHealthSnapshot("photos")
		return ok && st.ConsecutiveFail == 3
	})

	close(tick)
	<-done

	st, _ := getAppHealthSnapshot("photos")
	if st.Status != healthUnhealthy {
		t.Fatalf("3 个 tick 后应翻转为 unhealthy: %+v", st)
	}

	// 清空本测试翻转产生的 apps.unhealthy 事件，避免遗留在共享事件总线里
	// 被后续测试误收（事件总线是包级单例，不会在测试间自动重置）。
	common.DrainEventsForTest()
}

// waitUntil 轮询等待 cond 为真，超时 2s 后失败（用于等异步 goroutine 收敛，
// 不是业务逻辑本身依赖真实时间）。
func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}

// TestPollOnceRealHTTPFakeApp 端到端覆盖 defaultExecutor.CheckHealthz 的真实
// HTTP 调用路径（httptest 起一个可控健康/故障的假应用，监听 127.0.0.1，
// 与生产 CheckHealthz 的 "http://127.0.0.1:<port><healthzPath>" 调用形状
// 一致），确认轮询器经真实网络往返也能正确驱动状态机，不只是 fakeExecutor
// 桩调用层面成立。
func TestPollOnceRealHTTPFakeApp(t *testing.T) {
	var healthy atomicBool
	healthy.store(true)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if healthy.load() {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()

	port := serverPort(t, srv)

	db, err := openAppsDB(t.TempDir())
	if err != nil {
		t.Fatalf("openAppsDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	reg = &registry{db: db, dataDir: t.TempDir(), executor: NewDefaultExecutor()}
	poller = &healthPoller{states: map[string]appHealthState{}}
	common.InitEventBusForTest(t.TempDir())
	ch := common.Subscribe()
	defer common.Unsubscribe(ch)

	insertRunningApp(t, "photos", port)

	// 健康：探测通过。
	pollOnce()
	common.DrainEventsForTest()
	st, ok := getAppHealthSnapshot("photos")
	if !ok || st.Status != healthRunning {
		t.Fatalf("健康态下应为 running: %+v ok=%v", st, ok)
	}

	// 故障：连续 3 次失败后翻转 unhealthy，发一条事件。
	healthy.store(false)
	for i := 0; i < 3; i++ {
		pollOnce()
	}
	common.DrainEventsForTest()
	st, _ = getAppHealthSnapshot("photos")
	if st.Status != healthUnhealthy || st.ConsecutiveFail != 3 {
		t.Fatalf("3 次真实 HTTP 503 后应翻转为 unhealthy: %+v", st)
	}
	select {
	case ev := <-ch:
		if ev.Code != "apps.unhealthy" {
			t.Fatalf("want apps.unhealthy, got %s", ev.Code)
		}
	default:
		t.Fatal("应收到 apps.unhealthy 事件")
	}

	// 恢复：探测重新通过后翻转回 running，发恢复事件。
	healthy.store(true)
	pollOnce()
	common.DrainEventsForTest()
	st, _ = getAppHealthSnapshot("photos")
	if st.Status != healthRunning {
		t.Fatalf("恢复后应为 running: %+v", st)
	}
	select {
	case ev := <-ch:
		if ev.Code != "apps.recovered" {
			t.Fatalf("want apps.recovered, got %s", ev.Code)
		}
	default:
		t.Fatal("应收到 apps.recovered 事件")
	}
}

// serverPort 从 httptest.Server 的 URL 中解析出监听端口。
func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("解析 httptest server URL: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("解析端口: %v", err)
	}
	return port
}

// atomicBool 是一个极简的并发安全布尔值（健康假应用的开关由测试主 goroutine
// 设置、由 HTTP handler goroutine 读取，需要跨 goroutine 可见）。
type atomicBool struct {
	mu  sync.Mutex
	val bool
}

func (b *atomicBool) store(v bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.val = v
}

func (b *atomicBool) load() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.val
}
