// Package triggers 实现主动管家骨架（F2 Task 2）：建议卡（suggestions）
// 表 + 冷却/上限内核 + cron 基建。本 Task 只搭骨架（内核/REST/校验），
// 事件→建议规则表留给 Task 3（loops.go 的订阅循环本 Task 先建空壳）。
//
// 依赖单向：triggers → common/tasks/ai/diskmgmt/notifications（Task 2
// 只用到 common；tasks/ai/diskmgmt/notifications 的真实依赖从 Task 3/4/5
// 起落地）。禁止反向：tasks/notifications/ai 不得 import triggers。
package triggers

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nas-panel/common"
)

// Suggestion 是一张主动管家建议卡。
type Suggestion struct {
	ID          string `json:"id"`
	TriggerKey  string `json:"trigger_key"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	ActionsJSON string `json:"actions_json"`
	State       string `json:"state"` // new/accepted/dismissed/expired
	ExpireAt    string `json:"expire_at"`
	CreatedAt   string `json:"created_at"`
}

// 建议卡状态常量。
const (
	StateNew       = "new"
	StateAccepted  = "accepted"
	StateDismissed = "dismissed"
	StateExpired   = "expired"
)

// suggestionCooldown 是同 trigger_key 的冷却窗口：24h 内已有任意状态记录
// 就拒绝新建（硬约束红线 10：反骚扰）。
const suggestionCooldown = 24 * time.Hour

// activeSuggestionLimit 是 state=new 的活跃建议上限（硬约束红线 10）。
const activeSuggestionLimit = 5

var (
	dbMu sync.Mutex
	db   *sql.DB
)

// InitTriggers 打开 <dataDir>/triggers.db 并建表。幂等。
func InitTriggers(dataDir string) error {
	dbMu.Lock()
	defer dbMu.Unlock()

	if db != nil {
		db.Close()
		db = nil
	}

	opened, err := openTriggersDB(dataDir)
	if err != nil {
		return err
	}
	db = opened

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS suggestions (
			id TEXT PRIMARY KEY,
			trigger_key TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL DEFAULT '',
			actions_json TEXT NOT NULL DEFAULT '[]',
			state TEXT NOT NULL DEFAULT 'new',
			expire_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_suggestions_created_at ON suggestions(created_at);
		CREATE INDEX IF NOT EXISTS idx_suggestions_trigger_key ON suggestions(trigger_key);
		CREATE INDEX IF NOT EXISTS idx_suggestions_state ON suggestions(state);
	`); err != nil {
		return fmt.Errorf("create suggestions table: %w", err)
	}

	return nil
}

func openTriggersDB(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	return common.OpenSQLite(filepath.Join(dataDir, "triggers.db"))
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

func newSuggestionID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "sg-" + hex.EncodeToString(b), nil
}

// validActionTypes 是 actions_json 里允许的 type（计划 Task 2 schema 逐字）。
var validActionTypes = map[string]bool{
	"task_plan": true,
	"navigate":  true,
}

// validateActions 校验 actions_json：必须是非空数组，每项 type 必须在
// validActionTypes 内。坏 JSON/空数组/未知 type 全部拒绝。
func validateActions(actionsJSON string) error {
	var actions []map[string]interface{}
	if err := json.Unmarshal([]byte(actionsJSON), &actions); err != nil {
		return fmt.Errorf("actions_json 不是合法 JSON 数组: %w", err)
	}
	if len(actions) == 0 {
		return fmt.Errorf("actions_json 不能为空数组")
	}
	for i, a := range actions {
		typ, ok := a["type"].(string)
		if !ok || !validActionTypes[typ] {
			return fmt.Errorf("actions_json[%d].type 未知或缺失: %v", i, a["type"])
		}
	}
	return nil
}

// CreateSuggestion 创建一张新建议卡：冷却/上限在这里守。
//   - 同 triggerKey 24h 内已有任意状态记录 → (zero, false, nil)
//   - state=new 的活跃数 ≥ activeSuggestionLimit → (zero, false, nil)
//   - 创建成功时发 suggestion.created 事件（notifications 订阅入收件箱）
//
// guard=false（enabledGuard 注入）时直接拒绝：返回 error，不产生记录、
// 不发事件（零足迹语义）。
func CreateSuggestion(triggerKey, title, body, actionsJSON string, ttl time.Duration) (Suggestion, bool, error) {
	if !guardAllows() {
		return Suggestion{}, false, fmt.Errorf("triggers: 功能未启用")
	}
	if err := validateActions(actionsJSON); err != nil {
		return Suggestion{}, false, err
	}

	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return Suggestion{}, false, fmt.Errorf("triggers: 未初始化（InitTriggers 未调用）")
	}

	now := time.Now().UTC()

	// 冷却：同 key 24h 内任意状态记录存在即拒绝。
	cooldownSince := formatTime(now.Add(-suggestionCooldown))
	var cooldownCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM suggestions WHERE trigger_key = ? AND created_at >= ?`,
		triggerKey, cooldownSince,
	).Scan(&cooldownCount); err != nil {
		return Suggestion{}, false, err
	}
	if cooldownCount > 0 {
		return Suggestion{}, false, nil
	}

	// 上限：state=new 的活跃建议数 ≥ 上限即拒绝（过期的 new 行先惰性清一遍）。
	expireActiveSuggestionsLocked(now)
	var activeCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM suggestions WHERE state = ?`, StateNew,
	).Scan(&activeCount); err != nil {
		return Suggestion{}, false, err
	}
	if activeCount >= activeSuggestionLimit {
		return Suggestion{}, false, nil
	}

	id, err := newSuggestionID()
	if err != nil {
		return Suggestion{}, false, fmt.Errorf("生成建议 ID 失败: %w", err)
	}

	expireAt := ""
	if ttl > 0 {
		expireAt = formatTime(now.Add(ttl))
	}

	sg := Suggestion{
		ID:          id,
		TriggerKey:  triggerKey,
		Title:       title,
		Body:        body,
		ActionsJSON: actionsJSON,
		State:       StateNew,
		ExpireAt:    expireAt,
		CreatedAt:   formatTime(now),
	}

	_, err = db.Exec(
		`INSERT INTO suggestions (id, trigger_key, title, body, actions_json, state, expire_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sg.ID, sg.TriggerKey, sg.Title, sg.Body, sg.ActionsJSON, sg.State, sg.ExpireAt, sg.CreatedAt,
	)
	if err != nil {
		return Suggestion{}, false, fmt.Errorf("插入建议失败: %w", err)
	}

	common.EmitEvent("triggers", common.EventInfo, "suggestion.created", map[string]interface{}{
		"suggestion_id": sg.ID,
		"trigger_key":   sg.TriggerKey,
	}, "")

	return sg, true, nil
}

// expireActiveSuggestionsLocked 把已过期（expire_at 非空且早于 now）的
// state=new 行惰性置为 expired。调用方必须已持有 dbMu。
func expireActiveSuggestionsLocked(now time.Time) {
	if db == nil {
		return
	}
	nowStr := formatTime(now)
	db.Exec(
		`UPDATE suggestions SET state = ? WHERE state = ? AND expire_at != '' AND expire_at < ?`,
		StateExpired, StateNew, nowStr,
	)
}

// ListSuggestions 按 created_at 新->旧返回建议卡。includeInactive=false
// 时只返回 state=new（且先惰性清过期）；true 时返回全部状态。
func ListSuggestions(includeInactive bool) []Suggestion {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return nil
	}
	expireActiveSuggestionsLocked(time.Now().UTC())

	query := `SELECT id, trigger_key, title, body, actions_json, state, expire_at, created_at FROM suggestions`
	if !includeInactive {
		query += ` WHERE state = '` + StateNew + `'`
	}
	query += ` ORDER BY created_at DESC, id DESC`

	rows, err := db.Query(query)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := []Suggestion{}
	for rows.Next() {
		sg, err := scanSuggestion(rows)
		if err != nil {
			continue
		}
		out = append(out, sg)
	}
	return out
}

// scanner 抽象 *sql.Row / *sql.Rows 共用的 Scan 签名。
type scanner interface {
	Scan(dest ...interface{}) error
}

func scanSuggestion(row scanner) (Suggestion, error) {
	var sg Suggestion
	if err := row.Scan(&sg.ID, &sg.TriggerKey, &sg.Title, &sg.Body, &sg.ActionsJSON,
		&sg.State, &sg.ExpireAt, &sg.CreatedAt); err != nil {
		return Suggestion{}, err
	}
	return sg, nil
}

// GetSuggestion 按 ID 读取单条建议（不做惰性过期检查，调用方自行按 State 判断）。
func GetSuggestion(id string) (Suggestion, bool) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return Suggestion{}, false
	}
	row := db.QueryRow(
		`SELECT id, trigger_key, title, body, actions_json, state, expire_at, created_at
		 FROM suggestions WHERE id = ?`, id)
	sg, err := scanSuggestion(row)
	if err != nil {
		return Suggestion{}, false
	}
	return sg, true
}

// DismissSuggestion 把 state=new 的建议置为 dismissed；其余状态返回 error
// （409 语义：状态已变化）。
func DismissSuggestion(id string) error {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return fmt.Errorf("triggers: 未初始化")
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM suggestions WHERE id = ?`, id).Scan(&state); err != nil {
		return fmt.Errorf("建议不存在: %s", id)
	}
	if state != StateNew {
		return fmt.Errorf("非法状态迁移: %s -> %s", state, StateDismissed)
	}
	_, err := db.Exec(`UPDATE suggestions SET state = ? WHERE id = ?`, StateDismissed, id)
	return err
}

func formatTime(t time.Time) string {
	return t.Format(time.RFC3339Nano)
}

// enabledGuardFn 由 main.go 经 SetEnabledGuard 注入（通常是 ai.IsEnabled）。
// nil 表示不设限（测试默认态，对齐 tasks/notifications 的 guardAllows 模式）。
var enabledGuardFn func() bool

// SetEnabledGuard 注入模块的运行态守卫：CreateSuggestion、REST 层、
// loops.go 的后台循环都查它。
func SetEnabledGuard(fn func() bool) {
	enabledGuardFn = fn
}

func guardAllows() bool {
	if enabledGuardFn == nil {
		return true
	}
	return enabledGuardFn()
}

// jsonError 统一错误响应体（对齐 tasks/ai/notifications 包内各自建的
// jsonError 模式）。
func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}

// requireTriggersEnabled 包裹需要 enabledGuard() 为真才能访问的 handler。
func requireTriggersEnabled(guard func() bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if guard != nil && !guard() {
			jsonError(w, "主动管家功能未启用", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// RegisterRoutes 注册 triggers 模块的 HTTP 路由（走 AuthMiddleware + enabledGuard）。
// accept 端点在 Task 5 接入。
func RegisterRoutes(mux *http.ServeMux, enabledGuard func() bool) {
	mux.HandleFunc("/api/triggers/suggestions", common.AuthMiddleware(requireTriggersEnabled(enabledGuard, handleListSuggestions)))
	mux.HandleFunc("/api/triggers/suggestions/", common.AuthMiddleware(requireTriggersEnabled(enabledGuard, handleSuggestionSubroute)))
}

// registerRoutesBare 跳过 AuthMiddleware，只挂 enabledGuard，供单测直接发
// 请求（对齐 tasks/notifications 的 registerRoutesBare 模式）。
func registerRoutesBare(mux *http.ServeMux, enabledGuard func() bool) {
	mux.HandleFunc("/api/triggers/suggestions", requireTriggersEnabled(enabledGuard, handleListSuggestions))
	mux.HandleFunc("/api/triggers/suggestions/", requireTriggersEnabled(enabledGuard, handleSuggestionSubroute))
}

// handleListSuggestions: GET /api/triggers/suggestions?all=1
func handleListSuggestions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	includeInactive := r.URL.Query().Get("all") == "1"
	common.JSONResponse(w, map[string]interface{}{"suggestions": ListSuggestions(includeInactive)})
}

// handleSuggestionSubroute 分发 /api/triggers/suggestions/{id}/dismiss。
// accept 子路由在 Task 5 接入。
func handleSuggestionSubroute(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/triggers/suggestions/")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		jsonError(w, "缺少建议 ID", http.StatusBadRequest)
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	if len(parts) != 2 || parts[1] != "dismiss" {
		jsonError(w, "未知子路由", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := DismissSuggestion(id); err != nil {
		status := http.StatusBadRequest
		msg := err.Error()
		if strings.Contains(msg, "不存在") {
			status = http.StatusNotFound
		} else if strings.Contains(msg, "非法状态迁移") {
			status = http.StatusConflict
		}
		jsonError(w, msg, status)
		return
	}
	common.LogAuditRequest(r, "TRIGGERS", "忽略建议", "suggestion_id="+id, "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}
