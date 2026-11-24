package base

import (
	"testing"

	"nas-panel/modules/system"
)

// setupHealthTest 打开全新 base.db，注入 fakeExecutor，重置包级轮询器
// 状态（poller 是包级单例，测试间必须互相隔离，否则上一个测试留下的
// ConsecutiveFail/Status 会污染下一个测试的断言）。
//
// 同时重置事件总线→MQTT 桥（bridge.go）的包级状态并把其连接器钩子替换
// 为不连真实网络的 no-op fake——本文件多个测试会让 Mosquitto 从未知
// 翻转为 running（probeComponentHealth/pollOnce 的正常路径），若不隔离，
// 会触发桥真的去连 127.0.0.1:1883（本机大概率没有真实 Mosquitto），
// 虽然 bridge.go 已经改成异步+带超时不会把测试拖死，但仍会产生无意义的
// 网络尝试和日志噪音，与 bridge_test.go 自己的 setupBridgeTest 同一收口
// 模式。
func setupHealthTest(t *testing.T) *fakeExecutor {
	t.Helper()
	db, err := openBaseDB(t.TempDir())
	if err != nil {
		t.Fatalf("openBaseDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	fe := newFakeExecutor()
	reg = &registry{db: db, dataDir: t.TempDir(), executor: fe}
	poller.mu.Lock()
	poller.states = map[Component]componentHealthState{}
	poller.mu.Unlock()

	resetEventsBridgeForTest()
	newConnectorForBridge = func() (mqttConnector, error) {
		return &noopMQTTConnectorForTest{}, nil
	}
	t.Cleanup(resetEventsBridgeForTest)

	return fe
}

// noopMQTTConnectorForTest 是不连真实网络的 mqttConnector 替身，专供
// health_test.go 这类"不关心桥本身行为、只是意外触发了 ready 翻转"的
// 测试场景使用；真正测桥行为的用例在 bridge_test.go 的 fakeMQTTConnector。
type noopMQTTConnectorForTest struct{}

func (*noopMQTTConnectorForTest) Connect() error                                      { return nil }
func (*noopMQTTConnectorForTest) Publish(string, byte, bool, []byte) error { return nil }

func TestProbeComponentHealthRunningOnSuccess(t *testing.T) {
	setupHealthTest(t)
	if err := setComponentState(reg.db, ComponentPostgres, true, true); err != nil {
		t.Fatalf("setComponentState: %v", err)
	}

	probeComponentHealth(ComponentPostgres)
	st, ok := getComponentHealthSnapshot(ComponentPostgres)
	if !ok || st.Status != healthRunning {
		t.Fatalf("探测成功应为 running，实际 ok=%v status=%s", ok, st.Status)
	}
	if st.ConsecutiveFail != 0 {
		t.Fatalf("成功探测应清零连续失败计数，实际 %d", st.ConsecutiveFail)
	}
}

func TestProbeComponentHealthFlipsToUnhealthyAfterThreshold(t *testing.T) {
	fe := setupHealthTest(t)
	if err := setComponentState(reg.db, ComponentPostgres, true, true); err != nil {
		t.Fatalf("setComponentState: %v", err)
	}
	fe.pgReady = false

	var flipped bool
	for i := 0; i < healthFailThreshold; i++ {
		flipped = probeComponentHealth(ComponentPostgres)
	}
	st, ok := getComponentHealthSnapshot(ComponentPostgres)
	if !ok || st.Status != healthUnhealthy {
		t.Fatalf("连续失败 %d 次后应翻转为 unhealthy，实际 ok=%v status=%s", healthFailThreshold, ok, st.Status)
	}
	if !flipped {
		t.Fatalf("达到阈值的那一次探测应报告发生了翻转")
	}
}

func TestProbeComponentHealthDoesNotFlipBeforeThreshold(t *testing.T) {
	fe := setupHealthTest(t)
	if err := setComponentState(reg.db, ComponentPostgres, true, true); err != nil {
		t.Fatalf("setComponentState: %v", err)
	}
	// 先探测一次成功，建立 running 基线。
	probeComponentHealth(ComponentPostgres)

	fe.pgReady = false
	for i := 0; i < healthFailThreshold-1; i++ {
		probeComponentHealth(ComponentPostgres)
	}
	st, _ := getComponentHealthSnapshot(ComponentPostgres)
	if st.Status != healthRunning {
		t.Fatalf("未达阈值前应仍为 running，实际 %s", st.Status)
	}
}

func TestProbeComponentHealthRecoversToRunning(t *testing.T) {
	fe := setupHealthTest(t)
	if err := setComponentState(reg.db, ComponentPostgres, true, true); err != nil {
		t.Fatalf("setComponentState: %v", err)
	}
	fe.pgReady = false
	for i := 0; i < healthFailThreshold; i++ {
		probeComponentHealth(ComponentPostgres)
	}
	st, _ := getComponentHealthSnapshot(ComponentPostgres)
	if st.Status != healthUnhealthy {
		t.Fatalf("前置条件失败：应已为 unhealthy")
	}

	fe.pgReady = true
	flipped := probeComponentHealth(ComponentPostgres)
	st, _ = getComponentHealthSnapshot(ComponentPostgres)
	if st.Status != healthRunning {
		t.Fatalf("恢复成功后应回到 running，实际 %s", st.Status)
	}
	if !flipped {
		t.Fatalf("unhealthy→running 应报告发生了翻转")
	}
}

func TestProbeHealthDispatchesByComponent(t *testing.T) {
	fe := setupHealthTest(t)
	fe.pgReady = true
	fe.valkeyUp = false
	fe.mqttActive = true

	if !probeHealth(fe, ComponentPostgres) {
		t.Errorf("probeHealth(pgsql) 应走 PgIsReady")
	}
	if probeHealth(fe, ComponentValkey) {
		t.Errorf("probeHealth(cache) 应走 ValkeyPing")
	}
	if !probeHealth(fe, ComponentMosquitto) {
		t.Errorf("probeHealth(mqtt) 应走 MqttPing")
	}
}

func TestClearComponentHealthStateNeverProbesUninstalled(t *testing.T) {
	setupHealthTest(t)
	changed := clearComponentHealthState(ComponentValkey, healthStopped)
	if !changed {
		t.Fatalf("从未记录过状态时，首次设为 stopped 应报告发生了变化")
	}
	st, ok := getComponentHealthSnapshot(ComponentValkey)
	if !ok || st.Status != healthStopped {
		t.Fatalf("未安装组件应标记为 stopped，实际 ok=%v status=%s", ok, st.Status)
	}

	// 重复设置同一状态不应报告变化。
	changed = clearComponentHealthState(ComponentValkey, healthStopped)
	if changed {
		t.Fatalf("重复设置同一状态不应报告发生变化")
	}
}

func TestPollOnceSkipsUninstalledComponents(t *testing.T) {
	fe := setupHealthTest(t)
	// 只安装 PostgreSQL，Valkey/Mosquitto 均未安装。
	if err := setComponentState(reg.db, ComponentPostgres, true, true); err != nil {
		t.Fatalf("setComponentState: %v", err)
	}
	fe.pgReady = true

	pollOnce()

	if _, ok := getComponentHealthSnapshot(ComponentValkey); !ok {
		t.Fatalf("未安装组件也应被标记为 stopped（clearComponentHealthState），而非完全没有记录")
	}
	stValkey, _ := getComponentHealthSnapshot(ComponentValkey)
	if stValkey.Status != healthStopped {
		t.Fatalf("未安装组件应为 stopped，实际 %s", stValkey.Status)
	}
	stPg, ok := getComponentHealthSnapshot(ComponentPostgres)
	if !ok || stPg.Status != healthRunning {
		t.Fatalf("已安装且探测成功的组件应为 running，实际 ok=%v status=%s", ok, stPg.Status)
	}
}

func TestPollOncePublishesToSystemRegistry(t *testing.T) {
	setupHealthTest(t)
	if err := setComponentState(reg.db, ComponentMosquitto, true, true); err != nil {
		t.Fatalf("setComponentState: %v", err)
	}

	pollOnce()

	items := system.GetBaseHealthItemsForTest()
	if len(items) != len(AllComponents) {
		t.Fatalf("应发布 %d 条基座健康项，实际 %d", len(AllComponents), len(items))
	}
	foundMosquitto := false
	for _, it := range items {
		if it.Name == "Mosquitto 健康状态" {
			foundMosquitto = true
		}
	}
	if !foundMosquitto {
		t.Fatalf("未找到 Mosquitto 健康项: %+v", items)
	}
}

func TestRegistryStatusForHealthMapping(t *testing.T) {
	cases := map[string]string{
		healthRunning:   "pass",
		healthUnhealthy: "fail",
		healthStopped:   "warn",
		"unknown":       "warn",
	}
	for status, want := range cases {
		if got := registryStatusForHealth(status); got != want {
			t.Errorf("registryStatusForHealth(%s) = %s, want %s", status, got, want)
		}
	}
}

func TestComponentDisplayNameFixedShape(t *testing.T) {
	cases := map[Component]string{
		ComponentPostgres:  "PostgreSQL",
		ComponentValkey:    "Valkey",
		ComponentMosquitto: "Mosquitto",
	}
	for c, want := range cases {
		if got := componentDisplayName(c); got != want {
			t.Errorf("componentDisplayName(%s) = %s, want %s", c, got, want)
		}
	}
}
