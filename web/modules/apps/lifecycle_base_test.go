package apps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nas-panel/modules/base"
)

// fakeBaseExecutor 是 base.Executor 的测试替身：provisionBaseServices/
// deprovisionBaseServices 经 base.Provision*/Deprovision* 间接调用它，
// 本文件只需保证各方法成功返回（不断言具体 sudoers 形状——那部分已由
// modules/base 自己的 provision_test.go/services_test.go 覆盖），
// 让 lifecycle 测试能在不触达真实系统命令的前提下走通基座服务申领/清理。
type fakeBaseExecutor struct{}

func (fakeBaseExecutor) AptInstall(base.Component) (string, error) { return "", nil }
func (fakeBaseExecutor) EnableUnit(string) (string, error)         { return "", nil }
func (fakeBaseExecutor) DisableUnit(string) (string, error)        { return "", nil }
func (fakeBaseExecutor) StartUnit(string) (string, error)          { return "", nil }
func (fakeBaseExecutor) StopUnit(string) (string, error)           { return "", nil }
func (fakeBaseExecutor) UnitActiveState(string) string             { return "active" }
func (fakeBaseExecutor) WriteConfigFile(string, string) error      { return nil }
func (fakeBaseExecutor) PgCreateDB(string, string) (string, error) { return "", nil }
func (fakeBaseExecutor) PgSetPassword(string, string) (string, error) {
	return "", nil
}
func (fakeBaseExecutor) PgDropDB(string, string) (string, error) { return "", nil }
func (fakeBaseExecutor) PgIsReady() bool                         { return true }
func (fakeBaseExecutor) MqttSetPassword(string, string) (string, error) {
	return "", nil
}
func (fakeBaseExecutor) MqttDeleteUser(string) (string, error)     { return "", nil }
func (fakeBaseExecutor) MqttAclAdd(string, string) (string, error) { return "", nil }
func (fakeBaseExecutor) MqttAclRemove(string) (string, error)      { return "", nil }
func (fakeBaseExecutor) MqttPing() bool                            { return true }
func (fakeBaseExecutor) ValkeyPing() bool                          { return true }

// withBaseReadyForTest 初始化一个临时 base.db + fakeBaseExecutor，并把
// ready 中列出的组件置为 installed&&enabled（其余保持未安装），供本文件
// 的 lifecycle 测试驱动 base.Ready()/base.Provision* 在可控状态下运行。
// 返回的 restore 必须 defer 调用，还原 lifecycle_test.go 其它用例不触碰
// base 单例时的"未初始化"默认状态。
func withBaseReadyForTest(t *testing.T, ready ...base.Component) func() {
	t.Helper()
	prev, err := base.InitBaseForTest(t.TempDir(), fakeBaseExecutor{})
	if err != nil {
		t.Fatalf("初始化 base 测试单例失败: %v", err)
	}
	readySet := map[base.Component]bool{}
	for _, c := range ready {
		readySet[c] = true
	}
	for _, c := range base.AllComponents {
		want := readySet[c]
		if err := base.SetComponentStateForTest(c, want, want); err != nil {
			t.Fatalf("设置组件 %s 状态失败: %v", c, err)
		}
	}
	return func() { base.RestoreBaseForTest(prev) }
}

// appEntriesWithBasePerms 基于 appEntriesWithID 产出一套合法包条目，并把
// manifest 的 permissions.db/cache/mqtt 替换为给定值（dbPerm 为空字符串
// 时保留原 "sqlite"）。用于驱动 provisionBaseServices 的三条分支。
func appEntriesWithBasePerms(id, version, dbPerm string, cache bool, mqttSub, mqttPub []string) []tarEntry {
	entries := appEntriesWithID(id, version)
	for i, e := range entries {
		if !strings.HasSuffix(e.name, "manifest.json") {
			continue
		}
		m := string(e.content)
		if dbPerm != "" {
			m = strings.Replace(m, `"db": "sqlite",`, fmt.Sprintf(`"db": %q,`, dbPerm), 1)
		}
		if cache {
			m = strings.Replace(m, `"cache": false,`, `"cache": true,`, 1)
		}
		if len(mqttSub) > 0 || len(mqttPub) > 0 {
			subJSON, _ := json.Marshal(mqttSub)
			pubJSON, _ := json.Marshal(mqttPub)
			m = strings.Replace(m, `"mqtt": {"sub": [], "pub": []},`,
				fmt.Sprintf(`"mqtt": {"sub": %s, "pub": %s},`, subJSON, pubJSON), 1)
		}
		entries[i].content = []byte(m)
	}
	return entries
}

// ── confirm（provisionApp）：pgsql 未就绪应拒绝，就绪则注入 POSTGRES_DSN ──

// TestInstallConfirm_PgsqlNotReady_RejectsWithComponentState 覆盖
// provisionBaseServices 的防御性再检查：install 第一步（manifest 解析）
// 时 PostgreSQL 尚就绪、校验放行，但面板管理员在 confirm 前把它停用——
// confirm 不能信任 install 时的判定，必须重新检查并拒绝，错误信息指名
// 具体组件状态（manifest 解析层的同等拒绝路径已由 manifest_test.go 的
// TestParseManifest_PgsqlRejectedWhenPostgresNotReady 覆盖，本测试专门
// 覆盖 confirm 时的二次检查分支）。
func TestInstallConfirm_PgsqlNotReady_RejectsWithComponentState(t *testing.T) {
	defer withBaseReadyForTest(t, base.ComponentPostgres)()

	mux, _ := setupLifecycleTest(t)
	tb, sig := buildSignedPackage(t, appEntriesWithBasePerms("photos", "1.0.0", "pgsql", false, nil, nil))
	body, ct := multipartInstallBody(t, tb, sig)
	rec := doRequest(mux, http.MethodPost, "/api/apps/install", body, ct)
	if rec.Code != http.StatusOK {
		t.Fatalf("install step1: %d body=%s", rec.Code, rec.Body.String())
	}

	// install 通过后、confirm 前，PostgreSQL 被停用。
	if err := base.SetComponentStateForTest(base.ComponentPostgres, true, false); err != nil {
		t.Fatalf("停用 PostgreSQL 失败: %v", err)
	}

	rec = doRequest(mux, http.MethodPost, "/api/apps/install/photos/confirm", nil, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("PostgreSQL 被停用后 confirm 应失败, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "已安装但未启用") {
		t.Errorf("错误信息应指明 PostgreSQL 已安装但未启用，got: %s", rec.Body.String())
	}
}

func TestInstallConfirm_PgsqlReady_InjectsPostgresDSN(t *testing.T) {
	defer withBaseReadyForTest(t, base.ComponentPostgres)()

	mux, fe := setupLifecycleTest(t)
	tb, sig := buildSignedPackage(t, appEntriesWithBasePerms("photos", "1.0.0", "pgsql", false, nil, nil))
	body, ct := multipartInstallBody(t, tb, sig)
	doRequest(mux, http.MethodPost, "/api/apps/install", body, ct)

	rec := doRequest(mux, http.MethodPost, "/api/apps/install/photos/confirm", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm: %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(fe.lastUnitContent, "Environment=POSTGRES_DSN=postgres://") {
		t.Errorf("unit 应注入 POSTGRES_DSN，got unit:\n%s", fe.lastUnitContent)
	}
	// 凭据只应出现在 unit 内容里，绝不能泄露进 HTTP 响应体。
	if strings.Contains(rec.Body.String(), "POSTGRES_DSN") || strings.Contains(rec.Body.String(), "postgres://") {
		t.Errorf("响应体不应包含数据库凭据，got: %s", rec.Body.String())
	}
}

// ── confirm：cache 未就绪应拒绝，就绪则注入 VALKEY_URL ──

// TestInstallConfirm_CacheNotReady_Rejects 同 PgsqlNotReady 用例，覆盖
// cache 能力在 confirm 时的防御性再检查分支（install 时 Valkey 就绪，
// confirm 前被停用）。
func TestInstallConfirm_CacheNotReady_Rejects(t *testing.T) {
	defer withBaseReadyForTest(t, base.ComponentValkey)()

	mux, _ := setupLifecycleTest(t)
	tb, sig := buildSignedPackage(t, appEntriesWithBasePerms("photos", "1.0.0", "", true, nil, nil))
	body, ct := multipartInstallBody(t, tb, sig)
	doRequest(mux, http.MethodPost, "/api/apps/install", body, ct)

	if err := base.SetComponentStateForTest(base.ComponentValkey, true, false); err != nil {
		t.Fatalf("停用 Valkey 失败: %v", err)
	}

	rec := doRequest(mux, http.MethodPost, "/api/apps/install/photos/confirm", nil, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("Valkey 被停用后 confirm 应失败, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "已安装但未启用") {
		t.Errorf("错误信息应指明 Valkey 已安装但未启用，got: %s", rec.Body.String())
	}
}

func TestInstallConfirm_CacheReady_InjectsValkeyURL(t *testing.T) {
	defer withBaseReadyForTest(t, base.ComponentValkey)()

	mux, fe := setupLifecycleTest(t)
	tb, sig := buildSignedPackage(t, appEntriesWithBasePerms("photos", "1.0.0", "", true, nil, nil))
	body, ct := multipartInstallBody(t, tb, sig)
	doRequest(mux, http.MethodPost, "/api/apps/install", body, ct)

	rec := doRequest(mux, http.MethodPost, "/api/apps/install/photos/confirm", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm: %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(fe.lastUnitContent, "Environment=VALKEY_URL=redis://127.0.0.1:6379/") {
		t.Errorf("unit 应注入 VALKEY_URL，got unit:\n%s", fe.lastUnitContent)
	}
}

// ── confirm：mqtt 未就绪应拒绝，就绪则注入 MQTT_USER/MQTT_PASS/MQTT_TOPIC_PREFIX ──

// TestInstallConfirm_MQTTNotReady_Rejects 同上，覆盖 mqtt 能力在 confirm
// 时的防御性再检查分支（install 时 Mosquitto 就绪，confirm 前被停用）。
func TestInstallConfirm_MQTTNotReady_Rejects(t *testing.T) {
	defer withBaseReadyForTest(t, base.ComponentMosquitto)()

	mux, _ := setupLifecycleTest(t)
	tb, sig := buildSignedPackage(t, appEntriesWithBasePerms("photos", "1.0.0", "", false, []string{"nas/apps/photos/#"}, nil))
	body, ct := multipartInstallBody(t, tb, sig)
	doRequest(mux, http.MethodPost, "/api/apps/install", body, ct)

	if err := base.SetComponentStateForTest(base.ComponentMosquitto, true, false); err != nil {
		t.Fatalf("停用 Mosquitto 失败: %v", err)
	}

	rec := doRequest(mux, http.MethodPost, "/api/apps/install/photos/confirm", nil, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("Mosquitto 被停用后 confirm 应失败, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "已安装但未启用") {
		t.Errorf("错误信息应指明 Mosquitto 已安装但未启用，got: %s", rec.Body.String())
	}
}

func TestInstallConfirm_MQTTReady_InjectsMQTTCredentials(t *testing.T) {
	defer withBaseReadyForTest(t, base.ComponentMosquitto)()

	mux, fe := setupLifecycleTest(t)
	tb, sig := buildSignedPackage(t, appEntriesWithBasePerms("photos", "1.0.0", "", false, []string{"nas/apps/photos/#"}, nil))
	body, ct := multipartInstallBody(t, tb, sig)
	doRequest(mux, http.MethodPost, "/api/apps/install", body, ct)

	rec := doRequest(mux, http.MethodPost, "/api/apps/install/photos/confirm", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm: %d body=%s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Environment=MQTT_USER=", "Environment=MQTT_PASS=", "Environment=MQTT_TOPIC_PREFIX=nas/apps/photos/"} {
		if !strings.Contains(fe.lastUnitContent, want) {
			t.Errorf("unit 应包含 %q，got unit:\n%s", want, fe.lastUnitContent)
		}
	}
}

// ── upgrade：新包上传阶段 manifest 解析即按当前基座状态重新校验 ──

// TestUpgrade_PgsqlDisabledAfterInstall_RejectsAtManifestParse 覆盖升级
// 路径对基座组件就绪状态的判定：旧版本安装时 PostgreSQL 就绪，之后被
// 面板管理员停用；升级上传的新包同样声明 db=pgsql，UnpackAppPackage 内
// 的 ParseManifest 按停用后的当前状态重新校验并拒绝（与 install 第一步
// 走同一校验层，不依赖旧版本安装时的历史判定），unit/程序目录均未被
// upgradeApp 触碰，旧版本台账原样保留。
func TestUpgrade_PgsqlDisabledAfterInstall_RejectsAtManifestParse(t *testing.T) {
	restore := withBaseReadyForTest(t, base.ComponentPostgres)
	defer restore()

	mux, _ := setupLifecycleTest(t)
	tb, sig := buildSignedPackage(t, appEntriesWithBasePerms("photos", "1.0.0", "pgsql", false, nil, nil))
	body, ct := multipartInstallBody(t, tb, sig)
	doRequest(mux, http.MethodPost, "/api/apps/install", body, ct)
	doRequest(mux, http.MethodPost, "/api/apps/install/photos/confirm", nil, "")

	// 面板管理员在 confirm 后停用了 PostgreSQL。
	if err := base.SetComponentStateForTest(base.ComponentPostgres, true, false); err != nil {
		t.Fatalf("停用 PostgreSQL 失败: %v", err)
	}

	tb2, sig2 := buildSignedPackage(t, appEntriesWithBasePerms("photos", "1.1.0", "pgsql", false, nil, nil))
	body2, ct2 := multipartInstallBody(t, tb2, sig2)
	rec := doRequest(mux, http.MethodPost, "/api/apps/photos/upgrade", body2, ct2)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PostgreSQL 被停用后升级应被拒绝, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "已安装但未启用") {
		t.Errorf("错误信息应指明 PostgreSQL 已安装但未启用，got: %s", rec.Body.String())
	}

	// 升级被拒绝在 ParseManifest 阶段，upgradeApp 从未执行：台账应仍是旧版本。
	rec = doRequest(mux, http.MethodGet, "/api/apps/photos", nil, "")
	var detail appDetail
	json.Unmarshal(rec.Body.Bytes(), &detail)
	if detail.Version != "1.0.0" {
		t.Fatalf("台账版本应保持 1.0.0, got %q", detail.Version)
	}
}

// ── 卸载：keepData=false 触发 deprovision，keepData=true 跳过 ──

func TestDelete_KeepDataFalse_DeprovisionsPostgres(t *testing.T) {
	defer withBaseReadyForTest(t, base.ComponentPostgres)()

	mux, _ := setupLifecycleTest(t)
	tb, sig := buildSignedPackage(t, appEntriesWithBasePerms("photos", "1.0.0", "pgsql", false, nil, nil))
	body, ct := multipartInstallBody(t, tb, sig)
	doRequest(mux, http.MethodPost, "/api/apps/install", body, ct)
	doRequest(mux, http.MethodPost, "/api/apps/install/photos/confirm", nil, "")

	if _, err := base.ProvisionPostgres("photos"); err != nil {
		t.Fatalf("前置条件：查询已申领的数据库失败: %v", err)
	}

	// handleAppDelete 用 json.Decode 读 body，空 body（{}）等价于 keepData 默认 false。
	delReq := httptest.NewRequest(http.MethodDelete, "/api/apps/photos", strings.NewReader(`{}`))
	delRec := httptest.NewRecorder()
	mux.ServeHTTP(delRec, delReq)
	rec := delRec
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d body=%s", rec.Code, rec.Body.String())
	}

	// deprovision 后重新 ProvisionPostgres 应得到一套全新凭据（证明旧记录已被清理，
	// 而不是复用台账里的旧 DBName/RoleName/Password）。
	creds, err := base.ProvisionPostgres("photos")
	if err != nil {
		t.Fatalf("重新申领应成功: %v", err)
	}
	if err := base.DeprovisionPostgres("photos"); err != nil {
		t.Fatalf("清理重新申领的记录失败: %v", err)
	}
	_ = creds
}

func TestDelete_KeepDataTrue_SkipsDeprovision(t *testing.T) {
	defer withBaseReadyForTest(t, base.ComponentPostgres)()

	mux, _ := setupLifecycleTest(t)
	tb, sig := buildSignedPackage(t, appEntriesWithBasePerms("photos", "1.0.0", "pgsql", false, nil, nil))
	body, ct := multipartInstallBody(t, tb, sig)
	doRequest(mux, http.MethodPost, "/api/apps/install", body, ct)
	doRequest(mux, http.MethodPost, "/api/apps/install/photos/confirm", nil, "")

	credsBefore, err := base.ProvisionPostgres("photos")
	if err != nil {
		t.Fatalf("前置条件：查询已申领的数据库失败: %v", err)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/apps/photos", strings.NewReader(`{"keepData":true}`))
	delRec := httptest.NewRecorder()
	mux.ServeHTTP(delRec, delReq)
	rec := delRec
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d body=%s", rec.Code, rec.Body.String())
	}

	// keepData=true 应跳过 deprovision：重新 "申领" 应直接复用同一套凭据
	// （ProvisionPostgres 内部先查台账，台账记录未被删除则直接返回旧凭据）。
	credsAfter, err := base.ProvisionPostgres("photos")
	if err != nil {
		t.Fatalf("查询应成功: %v", err)
	}
	if credsAfter.DBName != credsBefore.DBName || credsAfter.Password != credsBefore.Password {
		t.Errorf("keepData=true 时卸载不应清理基座服务资源，got before=%+v after=%+v", credsBefore, credsAfter)
	}
	base.DeprovisionPostgres("photos")
}
