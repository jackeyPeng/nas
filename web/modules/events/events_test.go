package events

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nas-panel/common"
)

// setupTestMux 建库、灌事件、注册路由（绕过 AuthMiddleware 的 JWT：直接注册裸 handler）
func setupTestMux(t *testing.T) *http.ServeMux {
	t.Helper()
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)

	common.EmitEvent("health", common.EventWarn, "health.disk_hot", map[string]interface{}{"disk": "sda"}, "")
	common.EmitEvent("diskmgmt", common.EventError, "disk.offline", nil, "sdb 掉盘")
	common.EmitEvent("update", common.EventSuccess, "update.applied", nil, "")
	common.DrainEventsForTest()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/events", handleQueryEvents)
	mux.HandleFunc("/api/events/stats", handleEventStats)
	return mux
}

func TestHandleQueryEvents(t *testing.T) {
	mux := setupTestMux(t)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/events?limit=10", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Events []common.Event `json:"events"`
		Total  int            `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if resp.Total != 3 || len(resp.Events) != 3 {
		t.Fatalf("want 3, got total=%d len=%d", resp.Total, len(resp.Events))
	}
	if resp.Events[0].Source != "update" {
		t.Errorf("newest-first violated: %s", resp.Events[0].Source)
	}
}

func TestHandleQueryEventsFilter(t *testing.T) {
	mux := setupTestMux(t)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/events?type=error", nil))
	var resp struct {
		Events []common.Event `json:"events"`
		Total  int            `json:"total"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Total != 1 || resp.Events[0].Code != "disk.offline" {
		t.Fatalf("type filter wrong: %+v", resp)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/events?source=health", nil))
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Total != 1 || resp.Events[0].Code != "health.disk_hot" {
		t.Fatalf("source filter wrong: %+v", resp)
	}
}

func TestHandleQueryEventsBadType(t *testing.T) {
	mux := setupTestMux(t)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/events?type=banana", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestHandleQueryEventsMethodNotAllowed(t *testing.T) {
	mux := setupTestMux(t)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/events", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
}

func TestHandleEventStats(t *testing.T) {
	mux := setupTestMux(t)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/events/stats?days=7", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var st common.EventStats
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if st.Warn != 1 || st.Error != 1 || st.Success != 1 || st.Total != 3 {
		t.Fatalf("stats wrong: %+v", st)
	}
}
