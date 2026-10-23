// Package notifications 实现家庭管家通知中心的收件箱内核（F2 Task 1）。
//
// 设计定位：轻量叶子模块，只 import common——不感知 tasks/ai/triggers 等
// 生产方的任何内部实现，靠 eventbus 订阅解耦（见 subscriber.go）。
// title/body 存 i18n 键（不存自然语言），data_json 带插值参数，人话渲染
// 全部留给前端 t(key, params)。
package notifications

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"nas-panel/common"
)

// Notification 是收件箱里的一条通知。
type Notification struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"` // task/suggestion/report/system
	Title     string `json:"title"`
	Body      string `json:"body"`
	DataJSON  string `json:"data_json"`
	Read      bool   `json:"read"`
	CreatedAt string `json:"created_at"`
}

var (
	dbMu sync.Mutex
	db   *sql.DB
)

// InitNotifications 打开 <dataDir>/notifications.db 并建表。幂等。
func InitNotifications(dataDir string) error {
	dbMu.Lock()
	defer dbMu.Unlock()

	if db != nil {
		db.Close()
		db = nil
	}

	opened, err := openNotificationsDB(dataDir)
	if err != nil {
		return err
	}
	db = opened

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS notifications (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL DEFAULT '',
			data_json TEXT NOT NULL DEFAULT '{}',
			read INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_notifications_created_at ON notifications(created_at);
		CREATE INDEX IF NOT EXISTS idx_notifications_read ON notifications(read);
	`); err != nil {
		return fmt.Errorf("create notifications table: %w", err)
	}

	return nil
}

func openNotificationsDB(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	return common.OpenSQLite(filepath.Join(dataDir, "notifications.db"))
}

// closeDBForTest 关闭当前库连接，仅供单测在切换数据目录前清理单例状态。
func closeDBForTest() {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db != nil {
		db.Close()
		db = nil
	}
}

// newNotificationID 生成 "notif-" + 16 hex（8 随机字节）形式的 ID。
func newNotificationID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "notif-" + hex.EncodeToString(b), nil
}

// AddNotification 落一条新通知（title/body 存 i18n 键，dataJSON 带插值参数）。
func AddNotification(kind, title, body, dataJSON string) (string, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return "", fmt.Errorf("notifications: 未初始化（InitNotifications 未调用）")
	}

	id, err := newNotificationID()
	if err != nil {
		return "", fmt.Errorf("生成通知 ID 失败: %w", err)
	}
	if dataJSON == "" {
		dataJSON = "{}"
	}

	_, err = db.Exec(
		`INSERT INTO notifications (id, kind, title, body, data_json, read, created_at)
		 VALUES (?, ?, ?, ?, ?, 0, ?)`,
		id, kind, title, body, dataJSON, formatTime(time.Now().UTC()),
	)
	if err != nil {
		return "", fmt.Errorf("插入通知失败: %w", err)
	}
	return id, nil
}

// ListNotifications 按 created_at 新->旧返回最多 limit 条；unreadOnly=true
// 时只返回未读。limit<=0 时默认 20。
func ListNotifications(limit int, unreadOnly bool) []Notification {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}

	query := `SELECT id, kind, title, body, data_json, read, created_at FROM notifications`
	args := []interface{}{}
	if unreadOnly {
		query += ` WHERE read = 0`
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := []Notification{}
	for rows.Next() {
		var n Notification
		var readInt int
		if err := rows.Scan(&n.ID, &n.Kind, &n.Title, &n.Body, &n.DataJSON, &readInt, &n.CreatedAt); err != nil {
			continue
		}
		n.Read = readInt != 0
		out = append(out, n)
	}
	return out
}

// MarkRead 把单条通知标记已读。不存在返回 error。
func MarkRead(id string) error {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return fmt.Errorf("notifications: 未初始化")
	}
	res, err := db.Exec(`UPDATE notifications SET read = 1 WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("通知不存在: %s", id)
	}
	return nil
}

// MarkAllRead 把所有通知标记已读。
func MarkAllRead() error {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return fmt.Errorf("notifications: 未初始化")
	}
	_, err := db.Exec(`UPDATE notifications SET read = 1 WHERE read = 0`)
	return err
}

// ClearRead 删除所有已读通知，返回删除条数。
func ClearRead() (int, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return 0, fmt.Errorf("notifications: 未初始化")
	}
	res, err := db.Exec(`DELETE FROM notifications WHERE read = 1`)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// UnreadCount 返回未读通知数。
func UnreadCount() int {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return 0
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE read = 0`).Scan(&n); err != nil {
		return 0
	}
	return n
}

func formatTime(t time.Time) string {
	return t.Format(time.RFC3339Nano)
}

// enabledGuardFn 由 main.go 经 SetEnabledGuard 注入（通常是 ai.IsEnabled）。
// nil 表示不设限（测试默认态，对齐 tasks 的 guardAllows 模式）。
var enabledGuardFn func() bool

// SetEnabledGuard 注入模块的运行态守卫：REST 层与 subscriber 都查它。
func SetEnabledGuard(fn func() bool) {
	enabledGuardFn = fn
}

func guardAllows() bool {
	if enabledGuardFn == nil {
		return true
	}
	return enabledGuardFn()
}

// jsonError 统一错误响应体（对齐 tasks/ai 包内各自建的 jsonError 模式）。
func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}

// requireNotificationsEnabled 包裹需要 enabledGuard() 为真才能访问的 handler。
func requireNotificationsEnabled(guard func() bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if guard != nil && !guard() {
			jsonError(w, "通知中心未启用", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// RegisterRoutes 注册 notifications 模块的 HTTP 路由（走 AuthMiddleware + enabledGuard）。
func RegisterRoutes(mux *http.ServeMux, enabledGuard func() bool) {
	mux.HandleFunc("/api/notifications", common.AuthMiddleware(requireNotificationsEnabled(enabledGuard, handleListNotifications)))
	mux.HandleFunc("/api/notifications/unread-count", common.AuthMiddleware(requireNotificationsEnabled(enabledGuard, handleUnreadCount)))
	mux.HandleFunc("/api/notifications/read-all", common.AuthMiddleware(requireNotificationsEnabled(enabledGuard, handleMarkAllRead)))
	mux.HandleFunc("/api/notifications/clear", common.AuthMiddleware(requireNotificationsEnabled(enabledGuard, handleClearRead)))
	mux.HandleFunc("/api/notifications/", common.AuthMiddleware(requireNotificationsEnabled(enabledGuard, handleNotificationSubroute)))
}

// registerRoutesBare 跳过 AuthMiddleware，只挂 enabledGuard，供单测直接发请求
// （对齐 tasks.registerAPIRoutesBare / events.setupTestMux 模式）。
func registerRoutesBare(mux *http.ServeMux, enabledGuard func() bool) {
	mux.HandleFunc("/api/notifications", requireNotificationsEnabled(enabledGuard, handleListNotifications))
	mux.HandleFunc("/api/notifications/unread-count", requireNotificationsEnabled(enabledGuard, handleUnreadCount))
	mux.HandleFunc("/api/notifications/read-all", requireNotificationsEnabled(enabledGuard, handleMarkAllRead))
	mux.HandleFunc("/api/notifications/clear", requireNotificationsEnabled(enabledGuard, handleClearRead))
	mux.HandleFunc("/api/notifications/", requireNotificationsEnabled(enabledGuard, handleNotificationSubroute))
}

// handleListNotifications: GET /api/notifications?limit=&unread=1
func handleListNotifications(w http.ResponseWriter, r *http.Request) {
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
	unreadOnly := r.URL.Query().Get("unread") == "1"
	common.JSONResponse(w, map[string]interface{}{"notifications": ListNotifications(limit, unreadOnly)})
}

// handleUnreadCount: GET /api/notifications/unread-count → {"count":N}
func handleUnreadCount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	common.JSONResponse(w, map[string]interface{}{"count": UnreadCount()})
}

// handleMarkAllRead: POST /api/notifications/read-all → {"ok":true}
func handleMarkAllRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := MarkAllRead(); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	common.LogAuditRequest(r, "NOTIFICATIONS", "全部标记已读", "", "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}

// handleClearRead: POST /api/notifications/clear → {"ok":true,"cleared":N}
func handleClearRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	n, err := ClearRead()
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	common.LogAuditRequest(r, "NOTIFICATIONS", "清空已读通知", fmt.Sprintf("cleared=%d", n), "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true, "cleared": n})
}

// handleNotificationSubroute 分发 /api/notifications/{id}/read。
func handleNotificationSubroute(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/notifications/")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		jsonError(w, "缺少通知 ID", http.StatusBadRequest)
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	if len(parts) != 2 || parts[1] != "read" {
		jsonError(w, "未知子路由", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := MarkRead(id); err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "不存在") {
			status = http.StatusNotFound
		}
		jsonError(w, err.Error(), status)
		return
	}
	common.LogAuditRequest(r, "NOTIFICATIONS", "标记通知已读", "id="+id, "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}
