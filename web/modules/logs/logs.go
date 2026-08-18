package logs

import (
	"fmt"
	"net/http"
	"strconv"

	"nas-panel/common"
)

// RegisterRoutes registers audit log API routes
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/logs", common.AuthMiddleware(handleQueryLogs))
	mux.HandleFunc("/api/logs/clear", common.AuthMiddleware(handleClearLogs))
	mux.HandleFunc("/api/logs/config", common.AuthMiddleware(handleLogConfig))
	mux.HandleFunc("/api/logs/count", common.AuthMiddleware(handleLogCount))
}

func handleQueryLogs(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("user")
	action := r.URL.Query().Get("action")
	result := r.URL.Query().Get("result")
	daysStr := r.URL.Query().Get("days")
	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")

	days := 0
	if daysStr != "" {
		days, _ = strconv.Atoi(daysStr)
	}
	limit := 50
	if limitStr != "" {
		limit, _ = strconv.Atoi(limitStr)
	}
	offset := 0
	if offsetStr != "" {
		offset, _ = strconv.Atoi(offsetStr)
	}

	entries, total, err := common.QueryAuditLog(username, action, result, days, limit, offset)
	if err != nil {
		common.JSONResponse(w, map[string]interface{}{"error": err.Error()})
		return
	}
	if entries == nil {
		entries = []common.AuditEntry{}
	}

	common.JSONResponse(w, map[string]interface{}{
		"logs":   entries,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func handleClearLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	daysStr := r.FormValue("days")
	days := 0
	if daysStr != "" {
		days, _ = strconv.Atoi(daysStr)
	}

	confirm := r.FormValue("confirm")
	if confirm != "yes" {
		http.Error(w, `{"error":"请加 confirm=yes 确认"}`, http.StatusBadRequest)
		return
	}

	n, err := common.ClearAuditLog(days)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	msg := "所有日志已清除"
	if days > 0 {
		msg = fmt.Sprintf("已清除 %d 天前的日志，共 %d 条", days, n)
	} else {
		msg = fmt.Sprintf("所有日志已清除，共 %d 条", n)
	}

	common.JSONResponse(w, map[string]interface{}{
		"message": msg,
		"count":   n,
	})
}

func handleLogConfig(w http.ResponseWriter, r *http.Request) {
	retention := common.GetRetentionDays()
	count := common.GetAuditLogCount()

	common.JSONResponse(w, map[string]interface{}{
		"retention_days": retention,
		"total_entries":  count,
		"config_hint":    "Set NAS_LOG_RETENTION_DAYS in .env to change retention (default 90 days)",
	})
}

func handleLogCount(w http.ResponseWriter, r *http.Request) {
	count := common.GetAuditLogCount()
	common.JSONResponse(w, map[string]interface{}{"count": count})
}