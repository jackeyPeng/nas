package base

// 健康纳管（M2，与 apps/health.go 同一模式）
//
// 后台轮询器每 60s 对三个基座组件各探测一次就绪状态（仅对「已安装」的
// 组件探测；未安装的不探测，展示为 stopped）：
//   - PostgreSQL: pg_isready（经 nas-base-ops.sh pg-isready）
//   - Valkey:     valkey-cli -h 127.0.0.1 ping
//   - Mosquitto:  systemctl is-active（§services.go 注释：避免健康轮询
//     本身对 broker 产生持续订阅连接噪音，以服务活跃态近似代替真实
//     pub/sub 探测）
//
// 显式不做的事：不自动重启、不调用任何 StartUnit（§需求「无自动重启」，
// 与 apps 模块的健康轮询策略一致，由用户在面板手动一键启停决策）。

import (
	"fmt"
	"log"
	"sync"
	"time"

	"nas-panel/modules/system"
)

// healthFailThreshold 是翻转为 unhealthy 前要求的连续失败次数（对齐 apps 模块）。
const healthFailThreshold = 3

// healthPollInterval 是轮询周期（§需求「60s 轮询」）。
var healthPollInterval = 60 * time.Second

// 组件健康状态机取值，与 apps/health.go 的 healthRunning/healthUnhealthy/
// healthStopped 同名同义，用独立常量避免跨包耦合。
const (
	healthRunning   = "running"
	healthUnhealthy = "unhealthy"
	healthStopped   = "stopped"
)

// componentHealthState 是单个组件的轮询状态。
type componentHealthState struct {
	Status          string // running | unhealthy | stopped
	ConsecutiveFail int
	LastProbeAt     string // RFC3339；从未探测过为空串
	LastError       string
}

// healthPoller 持有轮询器的全部运行时状态。
type healthPoller struct {
	mu     sync.Mutex
	states map[Component]componentHealthState

	started sync.Once
}

var poller = &healthPoller{states: map[Component]componentHealthState{}}

// startHealthPoller 启动后台健康轮询 goroutine（幂等，可多次调用）。
func startHealthPoller() {
	poller.started.Do(func() {
		go runHealthPollerLoop(time.Tick(healthPollInterval))
	})
}

// runHealthPollerLoop 是轮询主循环，由 tick channel 驱动每一轮探测；
// 测试传入手动控制的 channel，驱动确定次数的轮询，不依赖真实 60s 睡眠。
func runHealthPollerLoop(tick <-chan time.Time) {
	for range tick {
		pollOnce()
	}
}

// pollOnce 对三个组件各探测一次（仅已安装的），更新状态机，并把当前快照
// 同步给 system 模块的注册表。
func pollOnce() {
	if reg == nil {
		return
	}
	states, err := listComponentStates(reg.db)
	if err != nil {
		log.Printf("[BASE] 健康轮询读取台账失败: %v", err)
		return
	}

	flipped := false
	for _, st := range states {
		c := Component(st.Name)
		if !st.Installed {
			if clearComponentHealthState(c, healthStopped) {
				flipped = true
			}
			continue
		}
		if probeComponentHealth(c) {
			flipped = true
			if c == ComponentMosquitto {
				// Mosquitto 探测成功：确保事件总线→MQTT 桥在跑。
				// 不只在「翻转」时调——状态机首轮失败会把 status 记成
				// running（阈值未到不翻 unhealthy），mosquitto 恢复后
				// running→running 无翻转，靠翻转触发会漏掉桥重试
				// （真机实锤：停 mosquitto 重启面板再恢复，桥不自愈）。
				// StartEventsBridgeIfReady 自身幂等（running 检查 + 失败
				// 重试收口），每轮调用是廉价的。
				StartEventsBridgeIfReady()
			}
		}
	}

	publishBaseHealthRegistry(states)
	if flipped {
		system.ScheduleRegistryRefresh()
	}
}

// probeHealth 按组件类型分发到对应的 Executor 探测方法。
func probeHealth(executor Executor, c Component) bool {
	switch c {
	case ComponentPostgres:
		return executor.PgIsReady()
	case ComponentValkey:
		return executor.ValkeyPing()
	case ComponentMosquitto:
		return executor.MqttPing()
	default:
		return false
	}
}

// probeComponentHealth 对单个组件探测一次并更新其状态机；返回是否发生了
// running<->unhealthy 的状态翻转。
func probeComponentHealth(c Component) bool {
	ok := probeHealth(reg.executor, c)
	now := time.Now().Format(time.RFC3339)

	poller.mu.Lock()
	prev := poller.states[c]
	next := prev
	next.LastProbeAt = now

	if !ok {
		next.LastError = fmt.Sprintf("%s 就绪探测失败", c)
		next.ConsecutiveFail = prev.ConsecutiveFail + 1
		if next.ConsecutiveFail < healthFailThreshold {
			if next.Status == "" {
				next.Status = healthRunning
			}
		} else {
			next.Status = healthUnhealthy
		}
	} else {
		next.LastError = ""
		next.ConsecutiveFail = 0
		next.Status = healthRunning
	}
	poller.states[c] = next
	flipped := prev.Status != next.Status
	poller.mu.Unlock()

	return flipped
}

// clearComponentHealthState 把组件的健康态设为 stopped（未安装不探测）；
// 返回是否发生了变化。
func clearComponentHealthState(c Component, status string) bool {
	poller.mu.Lock()
	defer poller.mu.Unlock()
	prev, ok := poller.states[c]
	if ok && prev.Status == status && prev.ConsecutiveFail == 0 {
		return false
	}
	poller.states[c] = componentHealthState{Status: status}
	return !ok || prev.Status != status
}

// getComponentHealthSnapshot 返回单个组件当前的健康状态拷贝；从未被轮询
// 过时返回零值 state 与 ok=false。
func getComponentHealthSnapshot(c Component) (componentHealthState, bool) {
	poller.mu.Lock()
	defer poller.mu.Unlock()
	st, ok := poller.states[c]
	return st, ok
}

// registryStatusForHealth 把健康状态机的取值映射为 system.RegistryItem 的
// pass/fail/warn 三态。
func registryStatusForHealth(status string) string {
	switch status {
	case healthRunning:
		return "pass"
	case healthUnhealthy:
		return "fail"
	default: // stopped 或未知
		return "warn"
	}
}

// componentDisplayName 返回组件的中文展示名。
func componentDisplayName(c Component) string {
	switch c {
	case ComponentPostgres:
		return "PostgreSQL"
	case ComponentValkey:
		return "Valkey"
	case ComponentMosquitto:
		return "Mosquitto"
	default:
		return string(c)
	}
}

// baseHealthRegistryID 把组件索引映射为注册表行 ID，取一个明显越过静态
// 目录（1-46）与应用健康行区段（100000+）的区段以防混淆。
func baseHealthRegistryID(index int) int {
	return 200000 + index
}

// publishBaseHealthRegistry 把三组件的健康状态组装为 system.RegistryItem
// 切片并整体推给 system 模块（全量覆盖）。
func publishBaseHealthRegistry(states []componentState) {
	items := make([]system.RegistryItem, 0, len(states))
	now := time.Now().Format("2006-01-02 15:04:05")
	for i, st := range states {
		c := Component(st.Name)
		status := healthStopped
		detail := "未安装"
		checkedAt := now
		if st.Installed {
			detail = "未运行"
			if hst, ok := getComponentHealthSnapshot(c); ok {
				status = hst.Status
				checkedAt = hst.LastProbeAt
				switch hst.Status {
				case healthRunning:
					detail = "就绪探测正常"
				case healthUnhealthy:
					detail = fmt.Sprintf("连续 %d 次就绪探测失败: %s", hst.ConsecutiveFail, hst.LastError)
				default:
					detail = "未运行"
				}
			}
		}
		items = append(items, system.RegistryItem{
			ID:          baseHealthRegistryID(i),
			Category:    "基座服务",
			Name:        fmt.Sprintf("%s 健康状态", componentDisplayName(c)),
			Status:      registryStatusForHealth(status),
			Detail:      detail,
			CheckedAt:   checkedAt,
			Description: "基座服务（PostgreSQL/Valkey/Mosquitto）就绪轮询结果（M2）",
		})
	}
	system.SetBaseHealthItems(items)
}
