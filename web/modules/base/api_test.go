package base

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// setupAPITest 打开全新 base.db，注入 fakeExecutor，挂裸路由（不经
// AdminMiddleware，与 apps 模块 setupLifecycleTest 同一模式）。
func setupAPITest(t *testing.T) (*http.ServeMux, *fakeExecutor) {
	t.Helper()
	db, err := openBaseDB(t.TempDir())
	if err != nil {
		t.Fatalf("openBaseDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	fe := newFakeExecutor()
	reg = &registry{db: db, dataDir: t.TempDir(), executor: fe}

	mux := http.NewServeMux()
	RegisterRoutesBare(mux)
	return mux, fe
}

func doJSONRequest(mux *http.ServeMux, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHandleBaseListAlwaysReturnsThreeComponents(t *testing.T) {
	mux, _ := setupAPITest(t)
	rec := doJSONRequest(mux, http.MethodGet, "/api/base")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/base: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Components []componentSummary `json:"components"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v, body=%s", err, rec.Body.String())
	}
	if len(resp.Components) != len(AllComponents) {
		t.Fatalf("应恒返回 %d 个组件，实际 %d", len(AllComponents), len(resp.Components))
	}
	for _, c := range resp.Components {
		if c.Installed || c.Enabled {
			t.Errorf("从未安装过的组件应为全未安装态: %+v", c)
		}
	}
}

func TestHandleBaseListRejectsNonGet(t *testing.T) {
	mux, _ := setupAPITest(t)
	rec := doJSONRequest(mux, http.MethodPost, "/api/base")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/base 应 405，实际 %d", rec.Code)
	}
}

func TestHandleBaseDispatchUnknownComponent404(t *testing.T) {
	mux, _ := setupAPITest(t)
	rec := doJSONRequest(mux, http.MethodPost, "/api/base/bogus/install")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知组件应 404，实际 %d", rec.Code)
	}
}

func TestHandleBaseDispatchUnknownAction404(t *testing.T) {
	mux, _ := setupAPITest(t)
	rec := doJSONRequest(mux, http.MethodPost, "/api/base/pgsql/bogus-action")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知操作应 404，实际 %d", rec.Code)
	}
}

func TestInstallComponentFlow(t *testing.T) {
	mux, fe := setupAPITest(t)

	rec := doJSONRequest(mux, http.MethodPost, "/api/base/pgsql/install")
	if rec.Code != http.StatusOK {
		t.Fatalf("install: %d body=%s", rec.Code, rec.Body.String())
	}
	if !fe.containsCallPrefix("apt-get install -y -qq <pgsql packages>") {
		t.Fatalf("未调用 AptInstall: %v", fe.calls)
	}
	st, found, err := getComponentState(reg.db, ComponentPostgres)
	if err != nil || !found || !st.Installed || st.Enabled {
		t.Fatalf("安装后应为 installed=true enabled=false，实际 found=%v st=%+v err=%v", found, st, err)
	}
}

func TestInstallComponentIsIdempotent(t *testing.T) {
	mux, fe := setupAPITest(t)
	doJSONRequest(mux, http.MethodPost, "/api/base/pgsql/install")
	callsAfterFirst := len(fe.calls)

	rec := doJSONRequest(mux, http.MethodPost, "/api/base/pgsql/install")
	if rec.Code != http.StatusOK {
		t.Fatalf("重复 install: %d body=%s", rec.Code, rec.Body.String())
	}
	if len(fe.calls) != callsAfterFirst {
		t.Fatalf("已安装时重复 install 不应再触发 AptInstall: %v", fe.calls)
	}
}

func TestUninstallComponentFlow(t *testing.T) {
	mux, fe := setupAPITest(t)
	doJSONRequest(mux, http.MethodPost, "/api/base/pgsql/install")

	rec := doJSONRequest(mux, http.MethodPost, "/api/base/pgsql/uninstall")
	if rec.Code != http.StatusOK {
		t.Fatalf("uninstall: %d body=%s", rec.Code, rec.Body.String())
	}
	if !fe.containsCallPrefix("systemctl stop postgresql") || !fe.containsCallPrefix("systemctl disable postgresql") {
		t.Fatalf("卸载应先停用再卸载: %v", fe.calls)
	}
	st, found, err := getComponentState(reg.db, ComponentPostgres)
	if err != nil || !found || st.Installed {
		t.Fatalf("卸载后应为 installed=false，实际 found=%v st=%+v err=%v", found, st, err)
	}
}

func TestUninstallComponentNeverInstalledIsNoop(t *testing.T) {
	mux, fe := setupAPITest(t)
	rec := doJSONRequest(mux, http.MethodPost, "/api/base/pgsql/uninstall")
	if rec.Code != http.StatusOK {
		t.Fatalf("uninstall (从未安装): %d body=%s", rec.Code, rec.Body.String())
	}
	if len(fe.calls) != 0 {
		t.Fatalf("从未安装时卸载不应触发任何执行器调用: %v", fe.calls)
	}
}

func TestEnableComponentRequiresInstalledFirst(t *testing.T) {
	mux, _ := setupAPITest(t)
	rec := doJSONRequest(mux, http.MethodPost, "/api/base/pgsql/enable")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未安装时 enable 应 400，实际 %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestEnableComponentFlow(t *testing.T) {
	mux, fe := setupAPITest(t)
	doJSONRequest(mux, http.MethodPost, "/api/base/cache/install")

	rec := doJSONRequest(mux, http.MethodPost, "/api/base/cache/enable")
	if rec.Code != http.StatusOK {
		t.Fatalf("enable: %d body=%s", rec.Code, rec.Body.String())
	}
	if !fe.containsCallPrefix("systemctl enable valkey-server") || !fe.containsCallPrefix("systemctl start valkey-server") {
		t.Fatalf("启用应 enable+start: %v", fe.calls)
	}
	if !Ready(ComponentValkey) {
		t.Fatalf("启用后 Ready() 应为 true")
	}
}

func TestEnableComponentIsIdempotent(t *testing.T) {
	mux, fe := setupAPITest(t)
	doJSONRequest(mux, http.MethodPost, "/api/base/cache/install")
	doJSONRequest(mux, http.MethodPost, "/api/base/cache/enable")

	rec := doJSONRequest(mux, http.MethodPost, "/api/base/cache/enable")
	if rec.Code != http.StatusOK {
		t.Fatalf("重复 enable: %d body=%s", rec.Code, rec.Body.String())
	}
	_ = fe
}

func TestDisableComponentFlow(t *testing.T) {
	mux, fe := setupAPITest(t)
	doJSONRequest(mux, http.MethodPost, "/api/base/mqtt/install")
	doJSONRequest(mux, http.MethodPost, "/api/base/mqtt/enable")

	rec := doJSONRequest(mux, http.MethodPost, "/api/base/mqtt/disable")
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: %d body=%s", rec.Code, rec.Body.String())
	}
	if !fe.containsCallPrefix("systemctl stop mosquitto") || !fe.containsCallPrefix("systemctl disable mosquitto") {
		t.Fatalf("停用应 stop+disable: %v", fe.calls)
	}
	if Ready(ComponentMosquitto) {
		t.Fatalf("停用后 Ready() 应为 false")
	}
	// 停用后仍应保持 installed=true（只是不再 enabled）。
	st, found, err := getComponentState(reg.db, ComponentMosquitto)
	if err != nil || !found || !st.Installed {
		t.Fatalf("停用不应卸载: found=%v st=%+v err=%v", found, st, err)
	}
}

func TestReadyFalseWhenRegistryNotInitialized(t *testing.T) {
	prev := reg
	reg = nil
	defer func() { reg = prev }()
	if Ready(ComponentPostgres) {
		t.Fatalf("reg 为 nil 时 Ready() 应返回 false")
	}
}

func TestReadyRequiresBothInstalledAndEnabled(t *testing.T) {
	mux, _ := setupAPITest(t)
	if Ready(ComponentPostgres) {
		t.Fatalf("从未安装时 Ready() 应为 false")
	}

	doJSONRequest(mux, http.MethodPost, "/api/base/pgsql/install")
	if Ready(ComponentPostgres) {
		t.Fatalf("仅安装未启用时 Ready() 应为 false")
	}

	doJSONRequest(mux, http.MethodPost, "/api/base/pgsql/enable")
	if !Ready(ComponentPostgres) {
		t.Fatalf("安装且启用后 Ready() 应为 true")
	}
}

func TestComponentStateDescriptionNamesSpecificComponent(t *testing.T) {
	mux, _ := setupAPITest(t)

	notInstalled := ComponentStateDescription(ComponentPostgres)
	if !containsSubstr(notInstalled, "PostgreSQL") || !containsSubstr(notInstalled, "未安装") {
		t.Fatalf("未安装时描述应指名 PostgreSQL 且含“未安装”，实际: %s", notInstalled)
	}

	doJSONRequest(mux, http.MethodPost, "/api/base/pgsql/install")
	installedNotEnabled := ComponentStateDescription(ComponentPostgres)
	if !containsSubstr(installedNotEnabled, "PostgreSQL") || !containsSubstr(installedNotEnabled, "未启用") {
		t.Fatalf("已装未启用时描述应指名 PostgreSQL 且含“未启用”，实际: %s", installedNotEnabled)
	}

	doJSONRequest(mux, http.MethodPost, "/api/base/pgsql/enable")
	ready := ComponentStateDescription(ComponentPostgres)
	if !containsSubstr(ready, "PostgreSQL") || !containsSubstr(ready, "就绪") {
		t.Fatalf("就绪后描述应指名 PostgreSQL 且含“就绪”，实际: %s", ready)
	}
}

func TestComponentStateDescriptionDiffersByComponent(t *testing.T) {
	setupAPITest(t)
	pg := ComponentStateDescription(ComponentPostgres)
	valkey := ComponentStateDescription(ComponentValkey)
	mqtt := ComponentStateDescription(ComponentMosquitto)
	if pg == valkey || valkey == mqtt || pg == mqtt {
		t.Fatalf("三个组件的状态描述必须互不相同（各自指名）: pg=%s valkey=%s mqtt=%s", pg, valkey, mqtt)
	}
}

func containsSubstr(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
