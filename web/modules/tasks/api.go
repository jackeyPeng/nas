package tasks

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nas-panel/common"
)

// jsonError 统一错误响应体的 Content-Type（同款 ai 模块 jsonError 模式，
// tasks 包内自建，不跨包借用——两个模块互不依赖彼此内部实现）。
func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	body, err := json.Marshal(map[string]string{"error": msg})
	if err != nil {
		body = []byte(`{"error":"内部错误"}`)
	}
	w.Write(body)
}

// requireTasksEnabled 包裹需要 enabledGuard() 为真才能访问的 handler；
// guard 为 nil 时视为始终允许（不应在生产出现，main.go 恒传 ai.IsEnabled）。
func requireTasksEnabled(guard func() bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if guard != nil && !guard() {
			jsonError(w, "任务功能未启用", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// registerAPIRoutes 是生产路径：包 AuthMiddleware（JWT）+ enabledGuard。
func registerAPIRoutes(mux *http.ServeMux, enabledGuard func() bool) {
	mux.HandleFunc("/api/tasks", common.AuthMiddleware(requireTasksEnabled(enabledGuard, handleListJobs)))
	mux.HandleFunc("/api/tasks/", common.AuthMiddleware(requireTasksEnabled(enabledGuard, handleJobSubroute)))
}

// registerAPIRoutesBare 跳过 AuthMiddleware，只挂 enabledGuard，供单测
// 直接发请求（对齐 modules/events/events_test.go 的裸 handler 测试模式，
// 不需要伪造 JWT）。
func registerAPIRoutesBare(mux *http.ServeMux, enabledGuard func() bool) {
	mux.HandleFunc("/api/tasks", requireTasksEnabled(enabledGuard, handleListJobs))
	mux.HandleFunc("/api/tasks/", requireTasksEnabled(enabledGuard, handleJobSubroute))
}

// handleListJobs: GET /api/tasks?limit=20 → {"jobs":[...]}
func handleListJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	common.JSONResponse(w, map[string]interface{}{"jobs": ListJobs(limit)})
}

// handleJobSubroute 分发 /api/tasks/{id}、/api/tasks/{id}/pause|resume|cancel、
// /api/tasks/{id}/stream。net/http 1.22 前的 ServeMux 不支持路径参数，手动解析。
func handleJobSubroute(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		jsonError(w, "缺少任务 ID", http.StatusBadRequest)
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]

	if len(parts) == 1 {
		handleJobDetail(w, r, id)
		return
	}

	switch parts[1] {
	case "pause":
		handleJobAction(w, r, id, PauseJob)
	case "resume":
		handleJobAction(w, r, id, ResumeJob)
	case "cancel":
		handleJobAction(w, r, id, CancelJob)
	case "stream":
		handleJobStream(w, r, id)
	default:
		jsonError(w, "未知子路由", http.StatusNotFound)
	}
}

// handleJobDetail: GET /api/tasks/{id} → Job JSON
func handleJobDetail(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	job, ok := GetJob(id)
	if !ok {
		jsonError(w, "任务不存在", http.StatusNotFound)
		return
	}
	common.JSONResponse(w, job)
}

// handleJobAction: POST /api/tasks/{id}/pause|resume|cancel → {"ok":true}
// 非法状态迁移（状态机校验失败）映射为 409；任务不存在映射为 404；
// 其它错误 400。
func handleJobAction(w http.ResponseWriter, r *http.Request, id string, action func(string) error) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := action(id); err != nil {
		jsonError(w, err.Error(), actionErrorStatus(err))
		return
	}
	common.LogAuditRequest(r, "TASKS", "任务操作", "job_id="+id, "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}

// actionErrorStatus 把内核错误文案映射为 HTTP 状态码：
//   - "任务不存在" → 404
//   - "非法状态迁移" → 409（状态机校验失败，前端据此提示"状态已变化，请刷新"）
//   - 其它（如队列已满） → 400
func actionErrorStatus(err error) int {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "任务不存在"):
		return http.StatusNotFound
	case strings.Contains(msg, "非法状态迁移"):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

// streamEvent 是 SSE 推送给前端的进度事件载荷。
type streamEvent struct {
	ProgressCur   int    `json:"progress_cur"`
	ProgressTotal int    `json:"progress_total"`
	Note          string `json:"note"`
	Status        string `json:"status"`
}

// handleJobStream: GET /api/tasks/{id}/stream → SSE 进度流
// 复用向导既有 SSE 模式（Content-Type/Cache-Control/Connection 三头 +
// Flush，见 modules/diskmgmt/stream.go）；任务已处于终态时立即发送终态
// 事件并关闭，不进入轮询循环。运行中任务按轮询间隔推送进度直到终态。
func handleJobStream(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	job, ok := GetJob(id)
	if !ok {
		jsonError(w, "任务不存在", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, _ := w.(http.Flusher)
	sendEvent := func(j Job) {
		data, _ := json.Marshal(streamEvent{
			ProgressCur:   j.ProgressCur,
			ProgressTotal: j.ProgressTotal,
			Note:          j.ProgressNote,
			Status:        string(j.Status),
		})
		w.Write([]byte("data: "))
		w.Write(data)
		w.Write([]byte("\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}

	if isTerminal(job.Status) {
		sendEvent(job)
		return
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			j, ok := GetJob(id)
			if !ok {
				return
			}
			sendEvent(j)
			if isTerminal(j.Status) {
				return
			}
		}
	}
}

func isTerminal(s Status) bool {
	return s == StatusDone || s == StatusFailed || s == StatusCancelled
}
