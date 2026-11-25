package apps

// 外部应用注册（APP-SPEC §10，#37 收编）
//
// 外部应用 = 机器上已有、自行部署的服务（如安康），管理员登记进应用中心；
// 核心不装不卸、不碰其文件与数据（§10.1）。注册是纯 DB 行插入——零文件
// 落盘：不建目录、不建用户、不写 unit（§10.4）。入口是门户卡新标签页
// 直连（window.open），不反代、不 iframe、不做鉴权透传，这部分在前端
// apps.js 实现，核心侧只存 url 原样返回。
//
// 生命周期：注册/注销 + （可选）unit 启停 + 健康监测。健康监测复用
// health.go 的 healthFailThreshold（连续 3 次失败才翻转，只在翻转时
// 发事件），探测方式按 §10.2：
//   - 提供 healthzUrl：HTTP GET 200 视为健康；
//   - 未提供 healthzUrl 但提供 unit：按 systemctl is-active 结果映射；
//   - 都未提供：状态恒为 unknown，不参与轮询。

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"nas-panel/common"
)

// externalIDPattern 对应 §10.2 id 规则，与托管应用的 idPattern（manifest.go，
// [a-z0-9-]{2,24}）不同——外部应用允许更长、但必须以字母数字开头。
var externalIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// externalUnitPattern 对应 §10.2 unit 字段约束。
var externalUnitPattern = regexp.MustCompile(`^[a-zA-Z0-9@:_-]+\.service$`)

// 外部应用健康状态机取值（§10.3「unknown / running / stopped / unhealthy」）。
const (
	externalStatusUnknown   = "unknown"
	externalStatusRunning   = "running"
	externalStatusStopped   = "stopped"
	externalStatusUnhealthy = "unhealthy"
)

// externalApp 对应 external_apps 表一行。
type externalApp struct {
	ID           string
	Name         string
	Icon         string
	URL          string
	Unit         string
	HealthzURL   string
	RegisteredAt string
	UpdatedAt    string
}

// ─────────────────────────────────────────────────────────────
// 建表（InitApps → openAppsDB 同一处）
// ─────────────────────────────────────────────────────────────

// createExternalAppsTable 在既有 apps.db 连接上建 external_apps 表
// （§10.4「落 apps.db 独立表，与 managed installed 表并列」）。由
// openAppsDB 在打开 installed/port_alloc 表之后调用。
func createExternalAppsTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS external_apps (
			id            TEXT PRIMARY KEY,
			name          TEXT NOT NULL,
			icon          TEXT NOT NULL DEFAULT '',
			url           TEXT NOT NULL,
			unit          TEXT NOT NULL DEFAULT '',
			healthz_url   TEXT NOT NULL DEFAULT '',
			status        TEXT NOT NULL DEFAULT 'unknown',
			registered_at TEXT NOT NULL,
			updated_at    TEXT NOT NULL
		);
	`)
	return err
}

// ─────────────────────────────────────────────────────────────
// CRUD 辅助（与 registry.go 的 installedApp 存取函数同一模式）
// ─────────────────────────────────────────────────────────────

func getExternalApp(db *sql.DB, id string) (*externalApp, error) {
	row := db.QueryRow(`SELECT id, name, icon, url, unit, healthz_url, registered_at, updated_at
		FROM external_apps WHERE id = ?`, id)
	var a externalApp
	err := row.Scan(&a.ID, &a.Name, &a.Icon, &a.URL, &a.Unit, &a.HealthzURL, &a.RegisteredAt, &a.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func listExternalApps(db *sql.DB) ([]externalApp, error) {
	rows, err := db.Query(`SELECT id, name, icon, url, unit, healthz_url, registered_at, updated_at
		FROM external_apps ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []externalApp
	for rows.Next() {
		var a externalApp
		if err := rows.Scan(&a.ID, &a.Name, &a.Icon, &a.URL, &a.Unit, &a.HealthzURL, &a.RegisteredAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func insertExternalApp(db *sql.DB, a externalApp) error {
	now := time.Now().Format(time.RFC3339)
	a.RegisteredAt = now
	a.UpdatedAt = now
	_, err := db.Exec(`INSERT INTO external_apps (id, name, icon, url, unit, healthz_url, registered_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.Icon, a.URL, a.Unit, a.HealthzURL, a.RegisteredAt, a.UpdatedAt)
	return err
}

func deleteExternalApp(db *sql.DB, id string) error {
	_, err := db.Exec(`DELETE FROM external_apps WHERE id = ?`, id)
	return err
}

// ─────────────────────────────────────────────────────────────
// 字段校验（§10.2）
// ─────────────────────────────────────────────────────────────

// validateExternalURL 校验 url/healthzUrl 字段：须是 http(s):// 开头的合法地址。
func validateExternalURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("不是合法的 URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("必须是 http(s):// 开头的地址")
	}
	if u.Host == "" {
		return fmt.Errorf("缺少 host")
	}
	return nil
}

// externalRegisterRequest 是 POST /api/apps/external 的请求体（§10.2）。
type externalRegisterRequest struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	Unit       string `json:"unit"`
	HealthzURL string `json:"healthzUrl"`
	Icon       string `json:"icon"`
}

// validate 校验注册请求全部字段（§10.2 的必填/约束表）；返回首个校验
// 失败的人话错误信息。
func (req externalRegisterRequest) validate() error {
	if !externalIDPattern.MatchString(req.ID) {
		return fmt.Errorf("id 不合法，需匹配 ^[a-z0-9][a-z0-9-]{0,31}$")
	}
	name := strings.TrimSpace(req.Name)
	if len(name) == 0 || len([]rune(name)) > 32 {
		return fmt.Errorf("name 长度须为 1-32 字符")
	}
	if err := validateExternalURL(req.URL); err != nil {
		return fmt.Errorf("url %v", err)
	}
	if req.Unit != "" && !externalUnitPattern.MatchString(req.Unit) {
		return fmt.Errorf("unit 不合法，需匹配 ^[a-zA-Z0-9@:_-]+\\.service$")
	}
	if req.HealthzURL != "" {
		if err := validateExternalURL(req.HealthzURL); err != nil {
			return fmt.Errorf("healthzUrl %v", err)
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────
// externalSummary（供 GET /api/apps 统一列表 + 注册/操作接口响应复用）
// ─────────────────────────────────────────────────────────────

// externalSummary 是外部应用对外展示结构，字段形状对齐 appSummary 但
// 额外带 kind 区分（§10.3「统一列表：托管应用带 kind:"managed"，外部应用
// 带 kind:"external"（同一页展示；外部应用无 port/version 字段语义）」）。
type externalSummary struct {
	Kind       string `json:"kind"` // 恒为 "external"
	ID         string `json:"id"`
	Name       string `json:"name"`
	Icon       string `json:"icon"`
	URL        string `json:"url"`
	Unit       string `json:"unit,omitempty"`
	HealthzURL string `json:"healthzUrl,omitempty"`
	Status     string `json:"status"` // unknown/running/stopped/unhealthy
}

// managedSummary 是 appSummary 补上 kind 字段后的统一列表条目（§10.3）。
type managedSummary struct {
	appSummary
	Kind string `json:"kind"` // 恒为 "managed"
}

func buildExternalSummary(exec Executor, a externalApp) externalSummary {
	return externalSummary{
		Kind:       "external",
		ID:         a.ID,
		Name:       a.Name,
		Icon:       a.Icon,
		URL:        a.URL,
		Unit:       a.Unit,
		HealthzURL: a.HealthzURL,
		Status:     externalAppStatus(exec, a),
	}
}

// externalAppStatus 返回外部应用当前展示状态（§10.2/§10.3）：
//   - 有 healthzUrl：取健康轮询器最近一次探测结果（未探测过时 unknown）；
//   - 无 healthzUrl 但有 unit：取 systemctl is-active 的实时结果；
//   - 都没有：unknown。
func externalAppStatus(exec Executor, a externalApp) string {
	if a.HealthzURL != "" {
		if st, ok := getExternalHealthSnapshot(a.ID); ok {
			return st.Status
		}
		return externalStatusUnknown
	}
	if a.Unit != "" {
		switch exec.UnitActiveState(a.Unit) {
		case "active":
			return externalStatusRunning
		case "":
			return externalStatusUnknown
		default:
			return externalStatusStopped
		}
	}
	return externalStatusUnknown
}

// ─────────────────────────────────────────────────────────────
// POST /api/apps/external（注册）
// ─────────────────────────────────────────────────────────────

func handleExternalRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req externalRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return
	}
	if err := req.validate(); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 与托管应用共用同一命名空间：id 撞上已安装的托管应用拒绝注册（§10.2）。
	managed, err := getInstalledApp(reg.db, req.ID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询托管应用台账失败: "+err.Error())
		return
	}
	if managed != nil {
		httpError(w, http.StatusConflict, fmt.Sprintf("id %q 已被托管应用占用", req.ID))
		return
	}

	existing, err := getExternalApp(reg.db, req.ID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询外部应用登记失败: "+err.Error())
		return
	}
	if existing != nil {
		httpError(w, http.StatusConflict, fmt.Sprintf("外部应用 %q 已注册", req.ID))
		return
	}

	a := externalApp{
		ID:         req.ID,
		Name:       strings.TrimSpace(req.Name),
		Icon:       req.Icon,
		URL:        req.URL,
		Unit:       req.Unit,
		HealthzURL: req.HealthzURL,
	}
	if err := insertExternalApp(reg.db, a); err != nil {
		httpError(w, http.StatusInternalServerError, "写入登记失败: "+err.Error())
		return
	}
	resetExternalHealthState(a.ID)

	common.LogAuditRequest(r, auditCategory, "注册外部应用", a.ID, "成功")
	common.EmitEvent("apps", common.EventInfo, "external.registered", map[string]interface{}{
		"id": a.ID, "name": a.Name, "url": a.URL,
	}, "")

	common.JSONResponse(w, buildExternalSummary(reg.executor, a))
}

// ─────────────────────────────────────────────────────────────
// DELETE /api/apps/external/<id>（注销登记，body { "confirm": true }）
// ─────────────────────────────────────────────────────────────

type externalDeleteRequest struct {
	Confirm bool `json:"confirm"`
}

func handleExternalDelete(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodDelete {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a, err := getExternalApp(reg.db, id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询外部应用登记失败: "+err.Error())
		return
	}
	if a == nil {
		httpError(w, http.StatusNotFound, "未注册的外部应用: "+id)
		return
	}

	var req externalDeleteRequest
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}
	if !req.Confirm {
		httpError(w, http.StatusBadRequest, `注销登记需在请求体内确认 {"confirm": true}`)
		return
	}

	if err := deleteExternalApp(reg.db, id); err != nil {
		httpError(w, http.StatusInternalServerError, "删除登记失败: "+err.Error())
		return
	}
	clearExternalHealthState(id)

	common.LogAuditRequest(r, auditCategory, "注销外部应用登记", id, "成功")
	common.EmitEvent("apps", common.EventInfo, "external.unregistered", map[string]interface{}{
		"id": id,
	}, "")

	common.JSONResponse(w, map[string]interface{}{"id": id, "deleted": true})
}

// ─────────────────────────────────────────────────────────────
// POST /api/apps/external/<id>/start|stop|restart（仅注册了 unit 时可用）
// ─────────────────────────────────────────────────────────────

func handleExternalAction(w http.ResponseWriter, r *http.Request, id, action string) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a, err := getExternalApp(reg.db, id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询外部应用登记失败: "+err.Error())
		return
	}
	if a == nil {
		httpError(w, http.StatusNotFound, "未注册的外部应用: "+id)
		return
	}
	if a.Unit == "" {
		httpError(w, http.StatusConflict, "该外部应用未登记 unit，不支持启停操作")
		return
	}

	var actionErr error
	switch action {
	case "start":
		_, actionErr = reg.executor.StartUnit(a.Unit)
	case "stop":
		_, actionErr = reg.executor.StopUnit(a.Unit)
	case "restart":
		_, actionErr = reg.executor.RestartUnit(a.Unit)
	default:
		httpError(w, http.StatusBadRequest, "未知操作: "+action)
		return
	}

	actionCN := map[string]string{"start": "启动", "stop": "停止", "restart": "重启"}[action]
	if actionErr != nil {
		common.LogAuditRequest(r, auditCategory, actionCN+"外部应用", fmt.Sprintf("%s: %v", id, actionErr), "失败")
		httpError(w, http.StatusInternalServerError, actionCN+"失败: "+actionErr.Error())
		return
	}

	common.LogAuditRequest(r, auditCategory, actionCN+"外部应用", id, "成功")
	common.EmitEvent("apps", common.EventInfo, "external.action", map[string]interface{}{
		"id": id, "action": action,
	}, "")

	common.JSONResponse(w, buildExternalSummary(reg.executor, *a))
}

// ─────────────────────────────────────────────────────────────
// GET /api/apps 统一列表（托管 + 外部，§10.3）
// ─────────────────────────────────────────────────────────────

// buildUnifiedAppsList 组装 GET /api/apps 的统一列表：托管应用在前
// （保持既有行为不变），外部应用在后，各自带 kind 字段。
func buildUnifiedAppsList(db *sql.DB, exec Executor) ([]interface{}, error) {
	managedApps, err := listInstalledApps(db)
	if err != nil {
		return nil, fmt.Errorf("查询托管应用台账失败: %w", err)
	}
	externalApps, err := listExternalApps(db)
	if err != nil {
		return nil, fmt.Errorf("查询外部应用登记失败: %w", err)
	}

	out := make([]interface{}, 0, len(managedApps)+len(externalApps))
	for _, a := range managedApps {
		out = append(out, managedSummary{appSummary: buildAppSummary(exec, a), Kind: "managed"})
	}
	for _, a := range externalApps {
		out = append(out, buildExternalSummary(exec, a))
	}
	return out, nil
}

// ─────────────────────────────────────────────────────────────
// 健康监测（§10.1/§10.5，复用 health.go 的翻转阈值与事件语义）
// ─────────────────────────────────────────────────────────────

// externalHealthState 是单个外部应用的轮询状态，形状对齐 appHealthState。
type externalHealthState struct {
	Status          string
	ConsecutiveFail int
	LastProbeAt     string
	LastError       string
}

// externalPoller 持有外部应用轮询器的全部运行时状态，与 apps 模块的
// poller（health.go）各自独立——外部应用的健康语义（unknown 起点、
// 仅 healthzUrl 参与 HTTP 轮询）与托管应用（stopped 起点、全部 installed
// 应用参与轮询）不同，不合并进同一状态机，避免 status 取值空间混用。
type externalPollerState struct {
	mu     sync.Mutex
	states map[string]externalHealthState
}

var externalPoller = &externalPollerState{states: map[string]externalHealthState{}}

// externalHealthHTTPGet 是外部应用 healthzUrl 探测用的 HTTP GET 注入点
// （与 market.go 的 marketHTTPGet 同一变量注入模式），供测试替换为假探测
// 结果，不依赖真实网络。
var externalHealthHTTPGet = func(url string) (*http.Response, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	return client.Get(url)
}

// resetExternalHealthState 在注册新外部应用时初始化其轮询状态为 unknown
// （与 clearExternalHealthState 共享同一清零逻辑，仅语义上用于"首次注册"）。
func resetExternalHealthState(id string) {
	externalPoller.mu.Lock()
	defer externalPoller.mu.Unlock()
	externalPoller.states[id] = externalHealthState{Status: externalStatusUnknown}
}

// clearExternalHealthState 注销登记时清掉轮询状态，避免同名 id 重新注册后
// 继承上次登记的历史计数。
func clearExternalHealthState(id string) {
	externalPoller.mu.Lock()
	defer externalPoller.mu.Unlock()
	delete(externalPoller.states, id)
}

func getExternalHealthSnapshot(id string) (externalHealthState, bool) {
	externalPoller.mu.Lock()
	defer externalPoller.mu.Unlock()
	st, ok := externalPoller.states[id]
	return st, ok
}

// pollExternalAppsOnce 对全部注册了 healthzUrl 的外部应用各探测一次
// （§10.2「提供则纳入健康轮询（200=健康）」；未提供 healthzUrl 的应用
// 状态改由 unit active 实时查询或 unknown，不需要轮询）。由 health.go
// 的 runHealthPollerLoop 每一轮 tick 顺带调用，复用同一 60s 节奏，不另开
// 一个 ticker goroutine。
func pollExternalAppsOnce() {
	if reg == nil {
		return
	}
	apps, err := listExternalApps(reg.db)
	if err != nil {
		return
	}
	for _, a := range apps {
		if a.HealthzURL == "" {
			continue
		}
		probeExternalAppHealth(a)
	}
}

// probeExternalAppHealth 对单个外部应用的 healthzUrl 探测一次并更新状态机，
// 仅在连续失败达到 healthFailThreshold（与托管应用共用同一阈值常量，
// health.go）才翻转为 unhealthy，且只在状态翻转时发一次事件
// （§10.5「健康翻转：external.unhealthy / external.recovered」）。
func probeExternalAppHealth(a externalApp) {
	err := checkExternalHealthz(a.HealthzURL)
	now := time.Now().Format(time.RFC3339)

	externalPoller.mu.Lock()
	prev := externalPoller.states[a.ID]
	next := prev
	next.LastProbeAt = now

	if err != nil {
		next.LastError = err.Error()
		next.ConsecutiveFail = prev.ConsecutiveFail + 1
		if next.ConsecutiveFail < healthFailThreshold {
			if next.Status == "" || next.Status == externalStatusUnknown {
				next.Status = externalStatusRunning
			}
		} else {
			next.Status = externalStatusUnhealthy
		}
	} else {
		next.LastError = ""
		next.ConsecutiveFail = 0
		next.Status = externalStatusRunning
	}
	externalPoller.states[a.ID] = next
	flipped := prev.Status != next.Status
	externalPoller.mu.Unlock()

	if !flipped {
		return
	}
	switch next.Status {
	case externalStatusUnhealthy:
		common.EmitEvent("apps", common.EventWarn, "external.unhealthy", map[string]interface{}{
			"id": a.ID, "consecutiveFail": next.ConsecutiveFail, "error": next.LastError,
		}, "")
	case externalStatusRunning:
		if prev.Status == externalStatusUnhealthy {
			common.EmitEvent("apps", common.EventInfo, "external.recovered", map[string]interface{}{
				"id": a.ID,
			}, "")
		}
	}
}

// checkExternalHealthz 对 healthzUrl 发一次 HTTP GET，200 视为健康
// （§10.2「200=健康」），经 externalHealthHTTPGet 注入点，供测试替换。
func checkExternalHealthz(healthzURL string) error {
	resp, err := externalHealthHTTPGet(healthzURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz 返回状态码 %d", resp.StatusCode)
	}
	return nil
}
