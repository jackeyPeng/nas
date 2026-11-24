package base

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"nas-panel/common"
)

// fakeMQTTConnector 是 mqttConnector 的测试替身：记录 Publish 调用的
// 主题/payload，不连真实 broker。connectErr/publishErr 可控地模拟失败，
// 断言桥的"连接失败不启动""发布失败丢弃不阻塞"行为。
type fakeMQTTConnector struct {
	mu sync.Mutex

	connectCalls int
	connectErr   error

	published    []fakePublishCall
	publishErr   error
	publishCalls int
}

type fakePublishCall struct {
	topic    string
	qos      byte
	retained bool
	payload  []byte
}

func (f *fakeMQTTConnector) Connect() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connectCalls++
	return f.connectErr
}

func (f *fakeMQTTConnector) Publish(topic string, qos byte, retained bool, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publishCalls++
	if f.publishErr != nil {
		return f.publishErr
	}
	f.published = append(f.published, fakePublishCall{topic: topic, qos: qos, retained: retained, payload: append([]byte(nil), payload...)})
	return nil
}

func (f *fakeMQTTConnector) publishAttempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.publishCalls
}

func (f *fakeMQTTConnector) snapshot() []fakePublishCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakePublishCall(nil), f.published...)
}

func (f *fakeMQTTConnector) connectCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connectCalls
}

// setupBridgeTest 打开全新 base.db + 事件总线（临时目录），把包级单例
// reg 指向 fakeExecutor，重置桥的 sync.Once 状态，并把 newConnectorForBridge
// 替换为返回给定 fake connector 的钩子。返回 fakeExecutor 供需要时断言
// ProvisionMQTTEventsBridge 的底层调用。
func setupBridgeTest(t *testing.T, conn *fakeMQTTConnector) *fakeExecutor {
	t.Helper()
	dataDir := t.TempDir()
	db, err := openBaseDB(dataDir)
	if err != nil {
		t.Fatalf("openBaseDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	fe := newFakeExecutor()
	reg = &registry{db: db, dataDir: dataDir, executor: fe}

	// 桥是通过 common.Subscribe() 的真实异步广播消费事件（后台 goroutine
	// 消费 eventWriter 的广播），必须用 InitEventBus（起 writer goroutine），
	// 不能用 InitEventBusForTest（那是配合 DrainEventsForTest 同步消费的
	// 场景，不会跑 writer goroutine，桥永远收不到事件）。
	common.InitEventBus(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)

	resetEventsBridgeForTest()
	newConnectorForBridge = func() (mqttConnector, error) {
		return conn, nil
	}
	t.Cleanup(resetEventsBridgeForTest)

	return fe
}

func TestEventsBridgeDoesNotStartWhenMosquittoNotReady(t *testing.T) {
	conn := &fakeMQTTConnector{}
	setupBridgeTest(t, conn)
	// Mosquitto 未安装/未启用：Ready(ComponentMosquitto) 应为 false。

	StartEventsBridgeIfReady()

	if conn.connectCount() != 0 {
		t.Fatalf("未就绪时不应尝试连接 broker，实际 Connect 调用次数: %d", conn.connectCount())
	}
	if eventsBridgeRunningForTest() {
		t.Fatalf("未就绪时桥不应标记为已启动")
	}
}

func TestEventsBridgeStartsExactlyOnceWhenReady(t *testing.T) {
	conn := &fakeMQTTConnector{}
	setupBridgeTest(t, conn)
	if err := SetComponentStateForTest(ComponentMosquitto, true, true); err != nil {
		t.Fatalf("SetComponentStateForTest: %v", err)
	}

	StartEventsBridgeIfReady()
	StartEventsBridgeIfReady()
	StartEventsBridgeIfReady()

	// 启动本身是异步的（见 StartEventsBridgeIfReady 注释），sync.Once
	// 收口的是"派发 goroutine"这一步，真正的 Connect 调用要等那个
	// goroutine 跑到才会发生，断言前要等桥标记为已启动。
	if !waitForCondition(t, 2*time.Second, eventsBridgeRunningForTest) {
		t.Fatalf("超时未观察到桥启动完成")
	}
	if conn.connectCount() != 1 {
		t.Fatalf("就绪时应恰好连接一次（sync.Once 收口），实际: %d", conn.connectCount())
	}
}

func TestEventsBridgeForwardsEventToMQTTTopic(t *testing.T) {
	conn := &fakeMQTTConnector{}
	setupBridgeTest(t, conn)
	if err := SetComponentStateForTest(ComponentMosquitto, true, true); err != nil {
		t.Fatalf("SetComponentStateForTest: %v", err)
	}

	StartEventsBridgeIfReady()

	// 桥的启动（申领账号→连接→Subscribe）发生在独立 goroutine 里；必须
	// 等桥真正订阅上事件总线之后才能发事件，否则 EmitEvent 可能先于
	// Subscribe 到达，事件总线的广播是 select-default 非阻塞发送，
	// 桥这个订阅者还没注册上就会直接丢事件。
	if !waitForCondition(t, 2*time.Second, eventsBridgeRunningForTest) {
		t.Fatalf("超时未观察到桥启动完成")
	}

	common.EmitEvent("apps", common.EventSuccess, "apps.install_pending", map[string]interface{}{"id": "demo-app"}, "等待确认")

	var got fakePublishCall
	if !waitForCondition(t, 2*time.Second, func() bool {
		calls := conn.snapshot()
		if len(calls) == 0 {
			return false
		}
		got = calls[0]
		return true
	}) {
		t.Fatalf("超时未收到转发的发布调用")
	}

	if got.topic != "nas/events/apps.install_pending" {
		t.Fatalf("topic = %s, want nas/events/apps.install_pending", got.topic)
	}
	if got.qos != eventsBridgeQoS {
		t.Fatalf("qos = %d, want %d", got.qos, eventsBridgeQoS)
	}
	if got.retained {
		t.Fatalf("retained 应为 false")
	}

	var ev common.Event
	if err := json.Unmarshal(got.payload, &ev); err != nil {
		t.Fatalf("payload 应为合法 Event JSON: %v", err)
	}
	if ev.Source != "apps" || ev.Type != common.EventSuccess || ev.Code != "apps.install_pending" {
		t.Fatalf("payload 字段不符: %+v", ev)
	}
	if ev.Detail != "等待确认" {
		t.Fatalf("payload Detail 不符: %q", ev.Detail)
	}
	if ev.Params == "" {
		t.Fatalf("payload Params 不应为空")
	}
}

func TestEventsBridgePublishFailureDropsAndContinues(t *testing.T) {
	conn := &fakeMQTTConnector{}
	setupBridgeTest(t, conn)
	if err := SetComponentStateForTest(ComponentMosquitto, true, true); err != nil {
		t.Fatalf("SetComponentStateForTest: %v", err)
	}

	StartEventsBridgeIfReady()

	if !waitForCondition(t, 2*time.Second, eventsBridgeRunningForTest) {
		t.Fatalf("超时未观察到桥启动完成")
	}

	conn.mu.Lock()
	conn.publishErr = &bridgePublishTestError{}
	conn.mu.Unlock()

	// 发布失败时不应 panic/阻塞；EmitEvent 本身是非阻塞投递。
	common.EmitEvent("apps", common.EventInfo, "apps.installed", nil, "")

	if !waitForCondition(t, 2*time.Second, func() bool {
		return conn.publishAttempts() > 0
	}) {
		t.Fatalf("超时未观察到 Publish 调用尝试")
	}

	// 再发一条成功的事件，确认失败后转发循环仍在继续处理，没有卡死。
	conn.mu.Lock()
	conn.publishErr = nil
	conn.mu.Unlock()
	common.EmitEvent("apps", common.EventSuccess, "apps.installed", nil, "")

	if !waitForCondition(t, 2*time.Second, func() bool {
		return len(conn.snapshot()) == 1
	}) {
		t.Fatalf("发布失败后转发循环应继续处理后续事件，未观察到后续成功发布")
	}
}

type bridgePublishTestError struct{}

func (*bridgePublishTestError) Error() string { return "fake publish failure" }

// waitForCondition 轮询 cond 直到为 true 或超时，供测试异步桥转发循环
// （桥的转发经一个独立 goroutine 消费 channel，不能假设 EmitEvent 后
// 立即可见）。
func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}
