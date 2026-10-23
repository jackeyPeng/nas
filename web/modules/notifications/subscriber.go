package notifications

import (
	"strings"

	"nas-panel/common"
)

// eventCodeMapping 把 eventbus code 映射为通知 i18n 键根（Task 1 事件→通知
// 映射表，计划 §Task1 Consumes 节逐字）。task.progress 不在表中——故意不入
// 收件箱，防刷屏。
//
// i18n 键命名对齐计划 Task 3 规则表展示的 "_title"/"_body" 后缀约定
// （如 trig.watermark_title/trig.watermark_body）：这里 title/body 共用
// 同一个语义键根，分别加 _title/_body 后缀；具体文案由 Task 3 补齐
// zh-CN.json/en-US.json，Task 1 先把键名管道打通。
//
// health.* 用通配前缀匹配（Task 3 才落地具体 check_id 规则，这里先按计划
// 锚点的 health.<checkID> 现实约定，按事件 Type 分流 warn/error 两档）。
const (
	codeTaskDone         = "task.done"
	codeTaskFailed       = "task.failed"
	codeSyncFailed       = "rclone.sync_failed"
	codeSuggestionCreate = "suggestion.created"
	codeReportGenerated  = "report.generated"
)

// notifKeyRootForEvent 返回事件 code 对应的通知 i18n 键根（不带 _title/_body
// 后缀）；空字符串表示该事件不入收件箱（如 task.progress，或未在订阅清单
// 内的事件）。
func notifKeyRootForEvent(ev common.Event) string {
	switch {
	case ev.Code == codeTaskDone:
		return "notif.task_done"
	case ev.Code == codeTaskFailed:
		return "notif.task_failed"
	case strings.HasPrefix(ev.Code, "health."):
		if ev.Type == common.EventError {
			return "notif.health_error"
		}
		return "notif.health_warn"
	case ev.Code == codeSyncFailed:
		return "notif.sync_failed"
	case ev.Code == codeSuggestionCreate:
		return "notif.suggestion"
	// report.generated 刻意不入收件箱：周报由 triggers.GenerateWeeklyReport
	// 直接 AddNotification(kind=report, 完整报告负载) 落箱，订阅侧再映射一次
	// 会产生一条只有 notification_id 的空壳重复通知（F2 真机 E2E 实抓）。
	// 事件本身照发——将来的微信网关等消费者从事件拿 notification_id 自取。
	default:
		return ""
	}
}

// kindForEvent 把事件 code 映射为 Notification.Kind（task/suggestion/report/system）。
func kindForEvent(ev common.Event) string {
	switch {
	case ev.Code == codeTaskDone || ev.Code == codeTaskFailed:
		return "task"
	case ev.Code == codeSuggestionCreate:
		return "suggestion"
	// report.generated 不入箱（见 notifKeyRootForEvent 注释：周报走直连
	// AddNotification，订阅侧再映射会重复）。
	default:
		return "system"
	}
}

// subscriberStop 用于单测停止订阅循环（StartSubscriber 的测试专用镜像，
// 对齐 tasks.StopWorkerForTest 模式）。
var subscriberStop chan struct{}

// StartSubscriber 启动 eventbus 订阅循环：收到 task.done/task.failed/
// health.*/rclone.sync_failed/suggestion.created/report.generated 时写入
// 收件箱。enabledGuard=false 时事件被丢弃，不入箱（零足迹语义）。
// 幂等：重复调用不会启动第二个循环。
func StartSubscriber() {
	if subscriberStop != nil {
		return
	}
	stop := make(chan struct{})
	subscriberStop = stop
	ch := common.Subscribe()
	go subscriberLoop(ch, stop)
}

// StopSubscriberForTest 停止订阅循环并清空单例状态，仅供单测在用例末尾
// 调用（对齐 tasks.StopWorkerForTest / common.CloseEventBusForTest 的
// "测试专用生命周期控制"模式）。生产路径（main.go）从不调用它。
func StopSubscriberForTest() {
	if subscriberStop == nil {
		return
	}
	close(subscriberStop)
	subscriberStop = nil
}

func subscriberLoop(ch chan common.Event, stop chan struct{}) {
	defer common.Unsubscribe(ch)
	for {
		select {
		case <-stop:
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			handleEvent(ev)
		}
	}
}

func handleEvent(ev common.Event) {
	if !guardAllows() {
		return
	}
	root := notifKeyRootForEvent(ev)
	if root == "" {
		return
	}
	dataJSON := ev.Params
	if dataJSON == "" {
		dataJSON = "{}"
	}
	AddNotification(kindForEvent(ev), root+"_title", root+"_body", dataJSON)
}
