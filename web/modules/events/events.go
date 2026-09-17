package events

// 事件中心查询 API（P1 Event Bus 的读取端）。
// 事件由 common.EmitEvent 写入 events.db，这里只提供只读查询：
//   - GET /api/events        分页 + source/type/days 过滤
//   - GET /api/events/stats  近 N 天各类型计数

import (
	"net/http"
	"strconv"

	"nas-panel/common"
)

// RegisterRoutes registers event center API routes
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/events", common.AuthMiddleware(handleQueryEvents))
	mux.HandleFunc("/api/events/stats", common.AuthMiddleware(handleEventStats))
}

func handleQueryEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	source := q.Get("source")
	typ := q.Get("type")
	if typ != "" && typ != common.EventInfo && typ != common.EventSuccess &&
		typ != common.EventWarn && typ != common.EventError {
		http.Error(w, `{"error":"invalid type"}`, http.StatusBadRequest)
		return
	}
	days, _ := strconv.Atoi(q.Get("days"))
	limit := 50
	if v := q.Get("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	offset := 0
	if v := q.Get("offset"); v != "" {
		offset, _ = strconv.Atoi(v)
	}

	evs, total, err := common.QueryEvents(source, typ, days, limit, offset)
	if err != nil {
		common.JSONResponse(w, map[string]interface{}{"error": err.Error()})
		return
	}
	common.JSONResponse(w, map[string]interface{}{
		"events": evs,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func handleEventStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	st, err := common.QueryEventStats(days)
	if err != nil {
		common.JSONResponse(w, map[string]interface{}{"error": err.Error()})
		return
	}
	common.JSONResponse(w, st)
}
