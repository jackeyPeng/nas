// bridge.go 实现 M2（二）事件总线→MQTT 桥：把面板内部事件总线
// （common.EmitEvent/Subscribe）的事件转发到 Mosquitto，供托管应用按
// docs/APP-SPEC.md §5.6 的约定订阅 nas/events/** 获知核心系统事件
// （只读——应用只可订阅，不可往这个前缀发布，由 ACL 保证）。
//
// 启动时机：health.go 的健康轮询探测到 Mosquitto 由不可用翻转为就绪
// （probeComponentHealth 返回 flipped=true 且组件是 ComponentMosquitto）
// 时触发启动；若 Mosquitto 从未就绪过，桥不启动（静默，不报错刷屏——
// §需求"不就绪不启动，不报错"）。启动动作本身用 sync.Once 收口，
// 保证即便健康轮询反复探测到就绪也只订阅一次（§需求"幂等，只启动一次"）。
//
// 背压策略：订阅 channel 有缓冲（common.Subscribe 内置 64 缓冲，满则
// eventWriter 广播时丢给本订阅者的事件，不阻塞 eventWriter 本身）；
// 发布到 MQTT 失败（broker 断线等）时记一行日志后丢弃该事件，不重试、
// 不阻塞桥自身的消费循环——事件总线对事件投递本就是"尽力而为"语义
// （EmitEvent 本身满则丢），桥转发失败沿用同一容忍度。
package base

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"nas-panel/common"
)

// eventsBridgeBrokerURL 是面板自身连接本机 Mosquitto 的地址（与
// buildMosquittoConfig 的 "listener 1883 127.0.0.1" 一致，只监听本机）。
const eventsBridgeBrokerURL = "tcp://127.0.0.1:1883"

// eventsBridgeClientID 是面板作为 MQTT 客户端的 client id。
const eventsBridgeClientID = "nas-panel-events-bridge"

// eventsBridgeQoS 是事件转发的发布 QoS（至少一次送达；§需求 QoS 1）。
const eventsBridgeQoS = 1

// mqttConnector 抽象桥所需的最小 MQTT 客户端能力（Connect/Publish），
// 供单测注入 fake 实现断言主题形状/payload 字段，不必真的起 Mosquitto。
// 生产路径由 pahoConnector 用真实 paho.mqtt.golang 客户端实现。
type mqttConnector interface {
	// Connect 建立到 broker 的连接；返回 error 时桥放弃本次启动（下一次
	// 健康轮询翻转 ready 会重试）。
	Connect() error
	// Publish 发布一条消息；返回 error 表示发布失败（桥记日志后丢弃，
	// 不重试、不阻塞）。
	Publish(topic string, qos byte, retained bool, payload []byte) error
}

// pahoConnector 是 mqttConnector 的生产实现，内部持有一个 paho Client。
type pahoConnector struct {
	client mqtt.Client
}

// newPahoConnector 用给定凭据构建（未连接的）paho 客户端。
func newPahoConnector(username, password string) *pahoConnector {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(eventsBridgeBrokerURL)
	opts.SetClientID(eventsBridgeClientID)
	opts.SetUsername(username)
	opts.SetPassword(password)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectTimeout(5 * time.Second)
	return &pahoConnector{client: mqtt.NewClient(opts)}
}

// pahoConnectWaitTimeout 是等待 Connect 完成的上限（略高于
// ClientOptions.SetConnectTimeout 的 5s，留出 token 内部调度余量）；
// 超时后放弃本次启动，不无限期占住 runEventsBridgeStartup 的 goroutine
// （该 goroutine 本身已经与调用方异步，这里的上限是双重保险，防止
// paho 在网络异常场景下的连接重试把 goroutine 永久挂住、累积泄漏）。
const pahoConnectWaitTimeout = 8 * time.Second

func (c *pahoConnector) Connect() error {
	token := c.client.Connect()
	if !token.WaitTimeout(pahoConnectWaitTimeout) {
		return fmt.Errorf("连接 MQTT broker 超时（%s）", pahoConnectWaitTimeout)
	}
	return token.Error()
}

func (c *pahoConnector) Publish(topic string, qos byte, retained bool, payload []byte) error {
	token := c.client.Publish(topic, qos, retained, payload)
	token.Wait()
	return token.Error()
}

// eventsBridge 持有事件总线→MQTT 桥的全部运行时状态。
//
// startupDone 在 runEventsBridgeStartup 返回时关闭（无论成功/失败），
// 仅供 resetEventsBridgeForTest 在测试间等用——sync.Once.Do 本身只保证
// "派发 goroutine" 这一步完成后返回，不保证 goroutine 已经跑完；若不等，
// 上一个测试遗留的 goroutine 可能在下一个测试重置了包级状态
// （newConnectorForBridge/reg.db）之后才真正执行到 ProvisionMQTTEventsBridge
// 等调用，读到的是别的测试的环境，产生 flaky 失败或无意义的报错日志。
type eventsBridge struct {
	mu          sync.Mutex
	started     sync.Once
	running     bool
	dispatched  bool
	startupDone chan struct{}
}

var bridge = &eventsBridge{startupDone: make(chan struct{})}

// newConnectorForBridge 是 startEventsBridge 构建真实连接器的钩子；单测
// 替换为返回 fake connector 的函数，断言桥在其上调用 Connect/Publish
// 的参数，不必真的解析凭据/连真实 broker。
var newConnectorForBridge = func() (mqttConnector, error) {
	creds, err := ProvisionMQTTEventsBridge()
	if err != nil {
		return nil, err
	}
	return newPahoConnector(creds.Username, creds.Password), nil
}

// StartEventsBridgeIfReady 在 Mosquitto 就绪时启动事件总线→MQTT 桥；
// 不就绪时静默跳过（不报错、不记日志刷屏——health.go 的轮询周期性调用
// 本函数，不就绪是常态而非异常）。幂等：进程生命周期内只会真正启动一次
// （sync.Once），重复调用直接返回。
//
// 本函数本身不阻塞调用方：真正的申领账号/连接 broker 发生在独立
// goroutine 里（§需求"必须不阻塞 eventWriter"的更强版本——健康轮询器
// 的调用方 pollOnce 同样不能被一次慢连接拖住，60s 轮询周期里还要探测
// 另外两个组件）。sync.Once 的 Do 本身仍是同步的，但其 func 体只做
// "派发 goroutine"这一件轻量操作，占用时间可忽略。
func StartEventsBridgeIfReady() {
	if !Ready(ComponentMosquitto) {
		return
	}
	b := bridge
	b.started.Do(func() {
		b.dispatched = true
		go runEventsBridgeStartup(b)
	})
}

// runEventsBridgeStartup 实际执行申领账号→连接 broker→订阅→常驻转发
// 循环，在独立 goroutine 里跑，连接失败/超时只记日志，不影响调用方。
// 接收启动它那一刻的 bridge 实例作为参数（而非读包级变量 bridge），
// 避免测试重置包级 bridge 后，本 goroutine 还在对着一个已经被替换掉
// 的实例操作状态。
func runEventsBridgeStartup(b *eventsBridge) {
	defer close(b.startupDone)

	conn, err := newConnectorForBridge()
	if err != nil {
		log.Printf("[BASE] 事件总线→MQTT 桥启动失败（申领账号）: %v", err)
		return
	}
	if err := conn.Connect(); err != nil {
		log.Printf("[BASE] 事件总线→MQTT 桥启动失败（连接 broker）: %v", err)
		return
	}
	b.mu.Lock()
	b.running = true
	b.mu.Unlock()
	ch := common.Subscribe()
	go runEventsBridgeLoop(conn, ch)
	log.Printf("[BASE] 事件总线→MQTT 桥已启动（订阅并转发到 nas/events/**）")
}

// eventsBridgeTopic 把事件的 Code 字段映射为发布主题
// （docs/APP-SPEC.md §5.6: nas/events/<code>，如 nas/events/apps.installed）。
func eventsBridgeTopic(code string) string {
	return "nas/events/" + code
}

// runEventsBridgeLoop 是桥的常驻转发循环：从事件总线订阅 channel 取事件，
// 序列化为 JSON 后发布到对应 MQTT 主题。发布失败只记一行日志并继续处理
// 下一条，绝不阻塞——channel 已经是 common.Subscribe 返回的独立缓冲
// channel，阻塞本循环最多导致本订阅者丢事件（eventWriter 的广播是
// select-default 非阻塞发送），不会拖慢 eventWriter 本身或其它订阅者。
func runEventsBridgeLoop(conn mqttConnector, ch chan common.Event) {
	for ev := range ch {
		payload, err := json.Marshal(ev)
		if err != nil {
			log.Printf("[BASE] 事件总线→MQTT 桥序列化事件失败: %v", err)
			continue
		}
		topic := eventsBridgeTopic(ev.Code)
		if err := conn.Publish(topic, eventsBridgeQoS, false, payload); err != nil {
			log.Printf("[BASE] 事件总线→MQTT 桥转发事件失败（已丢弃）: topic=%s err=%v", topic, err)
		}
	}
}

// eventsBridgeRunningForTest 报告桥当前是否已启动（仅供单测断言）。
func eventsBridgeRunningForTest() bool {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return bridge.running
}

// resetEventsBridgeForTest 把桥的运行时状态（包括 sync.Once）重置为初始值，
// 并恢复 newConnectorForBridge 钩子，供每个单测用例从干净状态起步
// （与 common.InitEventBusForTest 等「生产文件里放 ForTest 导出/内部函数」
// 的既有约定一致）。
//
// 先等上一个 bridge 实例的启动 goroutine（如果曾经被派发过）跑完，再
// 替换包级变量——启动是异步的，若不等，上一个测试遗留的 goroutine
// 可能在本次重置（换了 newConnectorForBridge/reg.db）之后才真正执行到
// 账号申领/连接逻辑，读到下一个测试的环境，产生 flaky 失败或无意义的
// 报错日志（曾经观测到的 "sql: database is closed" 即此类泄漏）。
func resetEventsBridgeForTest() {
	if bridge != nil && bridge.dispatched {
		select {
		case <-bridge.startupDone:
		case <-time.After(2 * time.Second):
		}
	}
	bridge = &eventsBridge{startupDone: make(chan struct{})}
	newConnectorForBridge = func() (mqttConnector, error) {
		creds, err := ProvisionMQTTEventsBridge()
		if err != nil {
			return nil, err
		}
		return newPahoConnector(creds.Username, creds.Password), nil
	}
}
