package notifications

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nas-panel/common"
)

func resetForTest(t *testing.T) {
	t.Helper()
	closeDBForTest()
	if err := InitNotifications(t.TempDir()); err != nil {
		t.Fatalf("InitNotifications: %v", err)
	}
	SetEnabledGuard(nil)
	t.Cleanup(closeDBForTest)
	t.Cleanup(func() { SetEnabledGuard(nil) })
}

func alwaysEnabled() bool  { return true }
func alwaysDisabled() bool { return false }

// ── 1. AddNotification→List 返回、unread 过滤、MarkRead/MarkAllRead/ClearRead 语义、UnreadCount 准确 ──

func TestAddAndListNotifications(t *testing.T) {
	resetForTest(t)

	id1, err := AddNotification("task", "notif.task_done_title", "notif.task_done_body", `{"job_id":"job-1"}`)
	if err != nil {
		t.Fatalf("AddNotification: %v", err)
	}
	time.Sleep(2 * time.Millisecond) // 保证 created_at 有序差异
	id2, err := AddNotification("suggestion", "notif.suggestion_title", "notif.suggestion_body", `{"suggestion_id":"sg-1"}`)
	if err != nil {
		t.Fatalf("AddNotification: %v", err)
	}

	all := ListNotifications(10, false)
	if len(all) != 2 {
		t.Fatalf("want 2 notifications, got %d", len(all))
	}
	if all[0].ID != id2 || all[1].ID != id1 {
		t.Fatalf("应按 created_at 倒序: %+v", all)
	}
	if all[0].Kind != "suggestion" || all[0].DataJSON != `{"suggestion_id":"sg-1"}` {
		t.Fatalf("字段不符: %+v", all[0])
	}
	if all[0].Read {
		t.Fatalf("新通知应为未读")
	}

	if UnreadCount() != 2 {
		t.Fatalf("want unread=2, got %d", UnreadCount())
	}

	if err := MarkRead(id1); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if UnreadCount() != 1 {
		t.Fatalf("want unread=1 after MarkRead, got %d", UnreadCount())
	}

	unread := ListNotifications(10, true)
	if len(unread) != 1 || unread[0].ID != id2 {
		t.Fatalf("unread 过滤错误: %+v", unread)
	}

	if err := MarkAllRead(); err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	if UnreadCount() != 0 {
		t.Fatalf("want unread=0 after MarkAllRead, got %d", UnreadCount())
	}

	n, err := ClearRead()
	if err != nil {
		t.Fatalf("ClearRead: %v", err)
	}
	if n != 2 {
		t.Fatalf("want cleared=2, got %d", n)
	}
	if len(ListNotifications(10, false)) != 0 {
		t.Fatalf("ClearRead 后列表应为空")
	}
}

func TestMarkReadNotFound(t *testing.T) {
	resetForTest(t)
	if err := MarkRead("notif-doesnotexist"); err == nil {
		t.Fatalf("不存在的通知 MarkRead 应报错")
	}
}

// ── 2. limit 截断按 created_at 倒序 ──

func TestListNotificationsLimit(t *testing.T) {
	resetForTest(t)
	var lastID string
	for i := 0; i < 5; i++ {
		id, err := AddNotification("system", "notif.x_title", "notif.x_body", "{}")
		if err != nil {
			t.Fatalf("AddNotification: %v", err)
		}
		lastID = id
		time.Sleep(2 * time.Millisecond)
	}
	got := ListNotifications(2, false)
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
	if got[0].ID != lastID {
		t.Fatalf("limit 截断应保留最新的, got %+v", got)
	}
}

// ── 3. 订阅者：EmitEvent → 轮询收件箱出现对应通知；task.progress 不出现；guard=false 丢弃 ──

// setupEventBusForTest 建事件库，手动 DrainEventsForTest 消费场景用（同步，
// 不起后台 writer goroutine）。
func setupEventBusForTest(t *testing.T) {
	t.Helper()
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)
}

// setupRealEventBusForTest 起真实后台 writer goroutine（对齐
// tasks.resetWorkerForTest 的模式）：订阅者异步广播场景必须用这个，
// InitEventBusForTest 不起 goroutine 不会真正广播。
func setupRealEventBusForTest(t *testing.T) {
	t.Helper()
	common.InitEventBus(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)
}

// startSubscriberForTest 启动真实订阅循环并在用例结束时停止，供「真正端到端」
// 的订阅者测试使用（对齐 tasks.StartWorker/StopWorkerForTest 的测试生命周期模式）。
func startSubscriberForTest(t *testing.T) {
	t.Helper()
	StartSubscriber()
	t.Cleanup(StopSubscriberForTest)
}

func TestSubscriberTaskDoneProducesNotification(t *testing.T) {
	resetForTest(t)
	setupRealEventBusForTest(t)
	startSubscriberForTest(t)

	common.EmitEvent("tasks", common.EventSuccess, "task.done", map[string]interface{}{"job_id": "job-1", "kind": "photo_dedupe"}, "任务完成")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(ListNotifications(10, false)) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	got := ListNotifications(10, false)
	if len(got) != 1 {
		t.Fatalf("want 1 notification within 2s, got %d: %+v", len(got), got)
	}
	if got[0].Title != "notif.task_done_title" || got[0].Kind != "task" {
		t.Fatalf("通知键/kind 不符: %+v", got[0])
	}
}

func TestSubscriberTaskProgressDoesNotProduceNotification(t *testing.T) {
	resetForTest(t)
	setupEventBusForTest(t)

	ev := common.Event{Source: "tasks", Type: common.EventInfo, Code: "task.progress", Params: "{}"}
	handleEvent(ev)

	if len(ListNotifications(10, false)) != 0 {
		t.Fatalf("task.progress 不应入收件箱防刷屏")
	}
}

func TestSubscriberGuardFalseDropsEvent(t *testing.T) {
	resetForTest(t)
	setupEventBusForTest(t)
	SetEnabledGuard(alwaysDisabled)

	ev := common.Event{Source: "tasks", Type: common.EventSuccess, Code: "task.done", Params: `{"job_id":"job-1"}`}
	handleEvent(ev)

	if len(ListNotifications(10, false)) != 0 {
		t.Fatalf("enabledGuard=false 时事件应被丢弃，不入箱")
	}
}

func TestSubscriberFullMapping(t *testing.T) {
	cases := []struct {
		name     string
		ev       common.Event
		wantRoot string
		wantKind string
	}{
		{"task_failed", common.Event{Code: "task.failed", Type: common.EventError}, "notif.task_failed", "task"},
		{"health_warn", common.Event{Code: "health.raid", Type: common.EventWarn}, "notif.health_warn", "system"},
		{"health_error", common.Event{Code: "health.disk_smart", Type: common.EventError}, "notif.health_error", "system"},
		{"sync_failed", common.Event{Code: "rclone.sync_failed", Type: common.EventError}, "notif.sync_failed", "system"},
		{"suggestion_created", common.Event{Code: "suggestion.created", Type: common.EventInfo}, "notif.suggestion", "suggestion"},
		{"report_generated", common.Event{Code: "report.generated", Type: common.EventInfo}, "notif.report", "report"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resetForTest(t)
			handleEvent(c.ev)
			got := ListNotifications(10, false)
			if len(got) != 1 {
				t.Fatalf("%s: want 1 notification, got %d", c.name, len(got))
			}
			if got[0].Title != c.wantRoot+"_title" || got[0].Body != c.wantRoot+"_body" {
				t.Fatalf("%s: title/body 键不符: %+v", c.name, got[0])
			}
			if got[0].Kind != c.wantKind {
				t.Fatalf("%s: kind 不符, want %s got %s", c.name, c.wantKind, got[0].Kind)
			}
		})
	}
}

// ── 4. REST：无 token 401、guard false 403、正常 200 JSON 形状断言 ──

func setupAPITestMux(t *testing.T, guard func() bool) *http.ServeMux {
	t.Helper()
	resetForTest(t)
	mux := http.NewServeMux()
	registerRoutesBare(mux, guard)
	return mux
}

func TestAPIGuardFalseForbidsAllEndpoints(t *testing.T) {
	mux := setupAPITestMux(t, alwaysDisabled)

	cases := []struct {
		method string
		path   string
	}{
		{"GET", "/api/notifications"},
		{"GET", "/api/notifications/unread-count"},
		{"POST", "/api/notifications/read-all"},
		{"POST", "/api/notifications/clear"},
		{"POST", "/api/notifications/notif-x/read"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s: guard=false 应 403, got %d body=%s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}

func TestAPIListHappyPath(t *testing.T) {
	mux := setupAPITestMux(t, alwaysEnabled)
	if _, err := AddNotification("task", "notif.task_done_title", "notif.task_done_body", `{"job_id":"job-1"}`); err != nil {
		t.Fatalf("AddNotification: %v", err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/notifications?limit=5", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/notifications: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Notifications []Notification `json:"notifications"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if len(resp.Notifications) != 1 || resp.Notifications[0].Title != "notif.task_done_title" {
		t.Fatalf("列表形状不符: %+v", resp)
	}
}

func TestAPIUnreadCountHappyPath(t *testing.T) {
	mux := setupAPITestMux(t, alwaysEnabled)
	AddNotification("task", "notif.task_done_title", "notif.task_done_body", "{}")
	AddNotification("task", "notif.task_failed_title", "notif.task_failed_body", "{}")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/notifications/unread-count", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var resp struct {
		Count int `json:"count"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Count != 2 {
		t.Fatalf("want count=2, got %d", resp.Count)
	}
}

func TestAPIMarkReadAndReadAllAndClear(t *testing.T) {
	mux := setupAPITestMux(t, alwaysEnabled)
	id1, _ := AddNotification("task", "notif.task_done_title", "notif.task_done_body", "{}")
	AddNotification("task", "notif.task_failed_title", "notif.task_failed_body", "{}")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/notifications/"+id1+"/read", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST read: %d body=%s", rec.Code, rec.Body.String())
	}
	if UnreadCount() != 1 {
		t.Fatalf("want unread=1 after single read, got %d", UnreadCount())
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/notifications/read-all", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST read-all: %d", rec.Code)
	}
	if UnreadCount() != 0 {
		t.Fatalf("want unread=0 after read-all, got %d", UnreadCount())
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/notifications/clear", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST clear: %d", rec.Code)
	}
	var resp struct {
		OK      bool `json:"ok"`
		Cleared int  `json:"cleared"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.OK || resp.Cleared != 2 {
		t.Fatalf("clear 响应不符: %+v", resp)
	}
}

func TestAPIAuthMiddlewareRequiresToken(t *testing.T) {
	resetForTest(t)
	mux := http.NewServeMux()
	RegisterRoutes(mux, alwaysEnabled)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/notifications", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无 token 应 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}
