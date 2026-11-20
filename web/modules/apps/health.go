package apps

// 健康纳管（M1-4，APP-SPEC §6）
//
// 后台轮询器每 60s 对全部"台账状态=installed（运行中）"的应用发一次
// healthz 探测（§6「核心每 60s 轮询」），探测复用 Executor.CheckHealthz
// （与 waitHealthz 同一套 HTTP 调用，2s 超时）。连续 3 次失败才翻转为
// unhealthy 并发一条事件（§6「连续 3 次失败 → 事件+告警」）；之后只要
// 还在失败就不再重复发事件，直到恢复——避免崩溃循环刷屏事件中心
// （规范未明说但"连续失败"隐含只在状态翻转时上报，否则 60s 一次的
// 轮询会在应用挂掉期间持续炸事件）。
//
// 显式不做的事：不自动重启、不调用任何 StartUnit/RestartUnit
// （§6「不自动重启（避免崩溃循环），由用户决策」——用户仍可在面板手动
// start/restart，那条路径走 handleAppAction，与健康轮询完全独立）。

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"nas-panel/common"
	"nas-panel/modules/system"
)

// healthFailThreshold 是翻转为 unhealthy 前要求的连续失败次数（§6「连续 3 次失败」）。
const healthFailThreshold = 3

// healthPollInterval 是轮询周期（§6「核心每 60s 轮询」）。
var healthPollInterval = 60 * time.Second

// 应用健康状态机取值。running/unhealthy 对应 §6 的健康判定结果；
// stopped 用于台账状态非 installed（未运行）时的展示态，这类应用不参与
// 轮询（poller 只对 status=installed 的台账记录发探测）。
const (
	healthRunning   = "running"
	healthUnhealthy = "unhealthy"
	healthStopped   = "stopped"
)

// appHealthState 是单个应用的轮询状态（仅由 healthPoller 的轮询 goroutine
// 读写，外部通过 getAppHealthSnapshot 取一份拷贝，不共享底层数据）。
type appHealthState struct {
	Status          string // running | unhealthy | stopped
	ConsecutiveFail int
	LastProbeAt     string // RFC3339；从未探测过为空串
	LastError       string
}

// healthPoller 持有轮询器的全部运行时状态：每应用当前健康态 + 测试注入点。
type healthPoller struct {
	mu     sync.Mutex
	states map[string]appHealthState // app id -> state

	started sync.Once
}

var poller = &healthPoller{states: map[string]appHealthState{}}

// startHealthPoller 启动后台健康轮询 goroutine（幂等，可多次调用）。
// tick 为 nil 时用 time.NewTicker(healthPollInterval) 驱动真实 60s 节奏；
// 测试通过 runHealthPollerLoop 直接注入一个可控 channel，不经这个入口。
func startHealthPoller() {
	poller.started.Do(func() {
		go runHealthPollerLoop(time.Tick(healthPollInterval))
	})
}

// runHealthPollerLoop 是轮询主循环，由 tick channel 驱动每一轮探测
// （每收到一个 tick 跑一轮全部应用，串行探测——§6 未要求并发，<10 个应用
// 量级下串行足够简单且避免并发写 states 的额外同步）。测试传入手动控制
// 的 channel，驱动确定次数的轮询，不依赖真实 60s 睡眠。
func runHealthPollerLoop(tick <-chan time.Time) {
	for range tick {
		pollOnce()
	}
}

// pollOnce 对全部 status=installed（运行中）的台账应用各探测一次 healthz，
// 更新状态机，并把当前快照同步给 system 模块的注册表。
func pollOnce() {
	if reg == nil {
		return
	}
	apps, err := listInstalledApps(reg.db)
	if err != nil {
		log.Printf("[APPS] 健康轮询读取台账失败: %v", err)
		return
	}

	flipped := false
	for _, a := range apps {
		if a.Status != statusInstalled {
			// 未运行（pending/stopped）：不探测，状态展示为 stopped，
			// 也清掉之前可能残留的 unhealthy 计数（重新 start 后应从
			// 零开始计连续失败，不沿用上次运行期间的计数）。
			if clearAppHealthState(a.ID, healthStopped) {
				flipped = true
			}
			continue
		}
		if probeAppHealth(a) {
			flipped = true
		}
	}

	publishAppHealthRegistry(apps)
	if flipped {
		system.ScheduleRegistryRefresh()
	}
}

// probeAppHealth 对单个应用探测一次并更新其状态机；返回是否发生了
// running<->unhealthy 的状态翻转（供调用方决定是否要触发注册表刷新/事件，
// 已在内部发了事件，这里的返回值只用于决定要不要 ScheduleRegistryRefresh）。
func probeAppHealth(a installedApp) bool {
	err := reg.executor.CheckHealthz(a.Port, a.Healthz)
	now := time.Now().Format(time.RFC3339)

	poller.mu.Lock()
	prev := poller.states[a.ID]
	next := prev
	next.LastProbeAt = now

	if err != nil {
		next.LastError = err.Error()
		next.ConsecutiveFail = prev.ConsecutiveFail + 1
		if next.ConsecutiveFail < healthFailThreshold {
			// 未达阈值：保持上一次已知的健康态展示（首次探测且未达阈值时
			// 默认视为 running，不因单次失败就抖动成 unhealthy）。
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
	poller.states[a.ID] = next
	flipped := prev.Status != next.Status
	poller.mu.Unlock()

	if !flipped {
		return false
	}

	switch next.Status {
	case healthUnhealthy:
		common.EmitEvent("apps", common.EventError, "apps.unhealthy", map[string]interface{}{
			"id": a.ID, "consecutiveFail": next.ConsecutiveFail, "error": next.LastError,
		}, "")
	case healthRunning:
		// 只有从 unhealthy 恢复才算"恢复事件"；第一次探测刚好通过（prev
		// 为未知态 ""）不算恢复，不发多余的事件噪音。
		if prev.Status == healthUnhealthy {
			common.EmitEvent("apps", common.EventSuccess, "apps.recovered", map[string]interface{}{
				"id": a.ID,
			}, "")
		}
	}
	return true
}

// clearAppHealthState 把应用的健康态设为 stopped（未运行不探测）；
// 返回是否发生了变化（供调用方判断要不要刷新注册表）。
func clearAppHealthState(id, status string) bool {
	poller.mu.Lock()
	defer poller.mu.Unlock()
	prev, ok := poller.states[id]
	if ok && prev.Status == status && prev.ConsecutiveFail == 0 {
		return false
	}
	poller.states[id] = appHealthState{Status: status}
	return !ok || prev.Status != status
}

// getAppHealthSnapshot 返回单个应用当前的健康状态拷贝；应用从未被轮询过
// （poller 还没跑过、或该应用台账状态非 installed 还未被 clear 过一次）
// 时返回零值 state 与 ok=false。
func getAppHealthSnapshot(id string) (appHealthState, bool) {
	poller.mu.Lock()
	defer poller.mu.Unlock()
	st, ok := poller.states[id]
	return st, ok
}

// registryStatusForHealth 把健康状态机的取值映射为 system.RegistryItem 的
// pass/fail/warn 三态（§系统注册表既有语义：pass=健康，fail=故障，warn=未知/过渡态）。
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

// appHealthRegistryID 把应用 id 映射为注册表行 ID。应用健康行是动态内存
// 快照（见 system.SetAppHealthItems 的文档），不写 registry 表，因此不与
// 41 项静态目录的固定 ID 空间冲突；这里的 ID 仅用于 JSON 展示排序，不作为
// 持久化主键，取一个明显越过静态目录（1-46）上限的区段以防混淆。
func appHealthRegistryID(index int) int {
	return 100000 + index
}

// publishAppHealthRegistry 把当前全部已安装应用的健康状态组装为
// system.RegistryItem 切片并整体推给 system 模块（全量覆盖，见
// SetAppHealthItems 文档）。零应用安装时推一个空切片，system 侧不会
// 出现任何 apps 相关行。
func publishAppHealthRegistry(apps []installedApp) {
	items := make([]system.RegistryItem, 0, len(apps))
	now := time.Now().Format("2006-01-02 15:04:05")
	for i, a := range apps {
		st, ok := getAppHealthSnapshot(a.ID)
		status := healthStopped
		detail := "未运行"
		checkedAt := now // 从未探测过时也展示本次汇总时间，避免空字符串
		if ok {
			status = st.Status
			checkedAt = st.LastProbeAt
			switch st.Status {
			case healthRunning:
				detail = "healthz 正常"
			case healthUnhealthy:
				detail = fmt.Sprintf("连续 %d 次 healthz 失败: %s", st.ConsecutiveFail, st.LastError)
			default:
				detail = "未运行"
			}
		}
		items = append(items, system.RegistryItem{
			ID:          appHealthRegistryID(i),
			Category:    "应用",
			Name:        fmt.Sprintf("%s（%s）健康状态", a.Name, a.ID),
			Status:      registryStatusForHealth(status),
			Detail:      detail,
			CheckedAt:   checkedAt,
			Description: "托管应用 healthz 轮询结果（§6）",
		})
	}
	system.SetAppHealthItems(items)
}

// ─────────────────────────────────────────────────────────────
// GET /api/apps/<id>/health
// ─────────────────────────────────────────────────────────────

// appHealthResponse 是 /api/apps/<id>/health 的响应体。
type appHealthResponse struct {
	ID              string `json:"id"`
	Status          string `json:"status"` // running | unhealthy | stopped
	ConsecutiveFail int    `json:"consecutiveFail"`
	LastProbeAt     string `json:"lastProbeAt"`
	LastError       string `json:"lastError,omitempty"`
}

func handleAppHealth(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a, err := getInstalledApp(reg.db, id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询台账失败: "+err.Error())
		return
	}
	if a == nil {
		httpError(w, http.StatusNotFound, "应用未安装: "+id)
		return
	}

	st, ok := getAppHealthSnapshot(id)
	resp := appHealthResponse{ID: id, Status: healthStopped}
	if a.Status != statusInstalled {
		resp.Status = healthStopped
	} else if ok {
		resp.Status = st.Status
		resp.ConsecutiveFail = st.ConsecutiveFail
		resp.LastProbeAt = st.LastProbeAt
		resp.LastError = st.LastError
	} else {
		// 已在运行但轮询器还没来得及跑第一轮：展示为 running（与
		// buildAppSummary 对"尚未探测"的乐观假设一致，避免新装应用在
		// 第一个 60s 窗口内被误读为故障）。
		resp.Status = healthRunning
	}

	common.JSONResponse(w, resp)
}
