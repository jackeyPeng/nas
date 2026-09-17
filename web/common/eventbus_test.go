package common

import (
	"strings"
	"testing"
	"time"
)

func TestEventBusEmitAndQuery(t *testing.T) {
	dir := t.TempDir()
	InitEventBusForTest(dir)
	defer CloseEventBusForTest()

	EmitEvent("health", EventWarn, "health.disk_hot", map[string]interface{}{"disk": "sda"}, "温度 55C")
	EmitEvent("diskmgmt", EventError, "disk.offline", nil, "sdb 掉盘")
	EmitEvent("update", EventSuccess, "update.applied", map[string]interface{}{"version": "v1.4.0-beta.9"}, "")
	DrainEventsForTest()

	evs, total, err := QueryEvents("", "", 0, 50, 0)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if total != 3 || len(evs) != 3 {
		t.Fatalf("want 3 events, got total=%d len=%d", total, len(evs))
	}
	// 最新在前
	if evs[0].Source != "update" {
		t.Errorf("want newest first (update), got %s", evs[0].Source)
	}
	if evs[0].Params == "" || evs[0].Params == "{}" {
		t.Errorf("update event params lost: %q", evs[0].Params)
	}

	// source 过滤
	evs, total, err = QueryEvents("health", "", 0, 50, 0)
	if err != nil || total != 1 || evs[0].Code != "health.disk_hot" {
		t.Fatalf("source filter: %v total=%d", err, total)
	}
	// type 过滤
	_, total, err = QueryEvents("", EventError, 0, 50, 0)
	if err != nil || total != 1 {
		t.Fatalf("type filter: %v total=%d", err, total)
	}
	// 分页
	evs, total, err = QueryEvents("", "", 0, 1, 1)
	if err != nil || total != 3 || len(evs) != 1 {
		t.Fatalf("paging: %v total=%d len=%d", err, total, len(evs))
	}
}

func TestEventBusBadTypeNormalized(t *testing.T) {
	dir := t.TempDir()
	InitEventBusForTest(dir)
	defer CloseEventBusForTest()

	EmitEvent("test", "banana", "x.y", nil, "")
	DrainEventsForTest()
	evs, _, err := QueryEvents("", "", 0, 10, 0)
	if err != nil || len(evs) != 1 || evs[0].Type != EventInfo {
		t.Fatalf("bad type not normalized to info: %v %+v", err, evs)
	}
}

func TestEventBusStats(t *testing.T) {
	dir := t.TempDir()
	InitEventBusForTest(dir)
	defer CloseEventBusForTest()

	EmitEvent("a", EventInfo, "a.b", nil, "")
	EmitEvent("a", EventWarn, "a.c", nil, "")
	EmitEvent("a", EventWarn, "a.d", nil, "")
	EmitEvent("a", EventError, "a.e", nil, "")
	DrainEventsForTest()

	st, err := QueryEventStats(7)
	if err != nil {
		t.Fatalf("QueryEventStats: %v", err)
	}
	if st.Info != 1 || st.Warn != 2 || st.Error != 1 || st.Total != 4 {
		t.Fatalf("stats wrong: %+v", st)
	}
}

func TestEventBusSubscribe(t *testing.T) {
	dir := t.TempDir()
	InitEventBusForTest(dir)
	defer CloseEventBusForTest()

	ch := Subscribe()
	defer Unsubscribe(ch)

	go func() {
		time.Sleep(10 * time.Millisecond)
		EmitEvent("sub", EventInfo, "sub.test", nil, "")
		DrainEventsForTest()
	}()
	select {
	case ev := <-ch:
		if ev.Code != "sub.test" {
			t.Fatalf("got wrong event: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber did not receive event")
	}
}

func TestEventBusEmitBeforeInitIsNoop(t *testing.T) {
	// 未 Init 时 Emit 不应 panic
	CloseEventBusForTest()
	EmitEvent("x", EventInfo, "x.y", nil, "")
}

func TestEventBusQueryParamsJSON(t *testing.T) {
	dir := t.TempDir()
	InitEventBusForTest(dir)
	defer CloseEventBusForTest()

	EmitEvent("rclone", EventError, "rclone.sync_failed", map[string]interface{}{
		"task": "backup-daily", "err": "connection refused", "n": 42,
	}, "")
	DrainEventsForTest()
	evs, _, err := QueryEvents("rclone", "", 0, 10, 0)
	if err != nil || len(evs) != 1 {
		t.Fatalf("query: %v", err)
	}
	for _, want := range []string{`"task":"backup-daily"`, `"err":"connection refused"`, `"n":42`} {
		if !strings.Contains(evs[0].Params, want) {
			t.Errorf("params missing %s: %s", want, evs[0].Params)
		}
	}
}
