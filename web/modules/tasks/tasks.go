// Package tasks 实现任务引擎内核（spec v4 §四，F1 Task 1/2）：
// jobs 表持久化、状态机、断点续传、崩溃恢复，以及（Task 2 起）worker 执行器
// 与 REST/SSE API。本包纯 Go + 既有 common 基建，不依赖 ai 模块的任何部分
// （架构边界测试 arch_boundary_test.go 的 TestArchBoundaries 机器强制）。
package tasks

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Status 是任务生命周期状态。
type Status string

const (
	StatusPendingConfirm Status = "pending_confirm"
	StatusRunning        Status = "running"
	StatusPaused         Status = "paused"
	StatusDone           Status = "done"
	StatusFailed         Status = "failed"
	StatusCancelled      Status = "cancelled"
)

// Step 是计划里的一个可执行步骤，带幂等 key。
type Step struct {
	Key  string            `json:"key"`  // 幂等键，如 "mv:<srcAbs>"
	Op   string            `json:"op"`   // F1 只有 "move_to_recycle"
	Args map[string]string `json:"args"` // {"src": "<池内相对路径>"}
}

// Impact 是确认卡展示的影响面预估。
type Impact struct {
	Groups int   `json:"groups"` // 重复组数
	Files  int   `json:"files"`  // 将移动的文件数
	Bytes  int64 `json:"bytes"`  // 将释放字节
}

// Job 是一个任务引擎任务（落库形态的内存表示）。
type Job struct {
	ID            string     `json:"id"`   // "job-" + 16hex
	Kind          string     `json:"kind"` // F1 只有 "photo_dedupe"
	Status        Status     `json:"status"`
	ScopeRel      string     `json:"scope_rel"` // 池内相对路径，"" = 全池
	Plan          []Step     `json:"plan"`
	Impact        Impact     `json:"impact"`
	ProgressCur   int        `json:"progress_cur"`
	ProgressTotal int        `json:"progress_total"`
	ProgressNote  string     `json:"progress_note"`
	Result        string     `json:"result"` // JSON: {"moved":N,"freed":M,"skipped":K}
	Error         string     `json:"error"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

// crashRecoveryNote 是崩溃恢复时追加到 progress_note 的提示文案。
// 家电安全语义：不自动续传，用户点"继续"——无人值守移动文件的风险
// 大于自动续传带来的便利（理由详见 OPENCODE-REPORT-F1-R1.md）。
const crashRecoveryNote = "面板重启已暂停，可继续"

// legalTransitions 是状态机的合法迁移表：from -> 允许迁到的 to 集合。
var legalTransitions = map[Status]map[Status]bool{
	StatusPendingConfirm: {StatusRunning: true, StatusCancelled: true},
	StatusRunning:        {StatusPaused: true, StatusDone: true, StatusFailed: true, StatusCancelled: true},
	StatusPaused:         {StatusRunning: true, StatusCancelled: true},
	// done/failed/cancelled 是终态，无合法出边。
}

var (
	dbMu sync.Mutex
	db   *sql.DB
)

// InitTasks 建库建表并执行崩溃恢复（面板重启时 status=running 的行 -> paused）。
// 幂等：对已存在的库重复调用不丢数据、不报错。
func InitTasks(dataDir string) error {
	dbMu.Lock()
	defer dbMu.Unlock()

	if db != nil {
		db.Close()
		db = nil
	}

	opened, err := openTasksDB(dataDir)
	if err != nil {
		return err
	}
	db = opened

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			status TEXT NOT NULL,
			scope_rel TEXT NOT NULL DEFAULT '',
			plan_json TEXT NOT NULL DEFAULT '[]',
			impact_json TEXT NOT NULL DEFAULT '{}',
			progress_cur INTEGER NOT NULL DEFAULT 0,
			progress_total INTEGER NOT NULL DEFAULT 0,
			progress_note TEXT NOT NULL DEFAULT '',
			checkpoint_json TEXT NOT NULL DEFAULT '{}',
			result_json TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			started_at TEXT,
			finished_at TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_jobs_created_at ON jobs(created_at);
		CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
	`); err != nil {
		return fmt.Errorf("create jobs table: %w", err)
	}

	if err := recoverCrashedJobs(); err != nil {
		return fmt.Errorf("crash recovery: %w", err)
	}

	return nil
}

// recoverCrashedJobs 把所有 status=running 的行改为 paused，并在
// progress_note 追加 crashRecoveryNote（已追加过则不重复追加，InitTasks
// 本身幂等的一部分——不太可能发生，因为 running 态会在本函数内立即清空，
// 但防御性保留）。
func recoverCrashedJobs() error {
	rows, err := db.Query(`SELECT id, progress_note FROM jobs WHERE status = ?`, string(StatusRunning))
	if err != nil {
		return err
	}
	type pending struct {
		id   string
		note string
	}
	var items []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.note); err != nil {
			rows.Close()
			return err
		}
		items = append(items, p)
	}
	rows.Close()

	for _, p := range items {
		note := p.note
		if !hasSuffixNote(note, crashRecoveryNote) {
			if note != "" {
				note = note + "；" + crashRecoveryNote
			} else {
				note = crashRecoveryNote
			}
		}
		if _, err := db.Exec(
			`UPDATE jobs SET status = ?, progress_note = ? WHERE id = ?`,
			string(StatusPaused), note, p.id,
		); err != nil {
			return err
		}
	}
	return nil
}

func hasSuffixNote(note, suffix string) bool {
	if len(note) < len(suffix) {
		return false
	}
	return note[len(note)-len(suffix):] == suffix
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

// newJobID 生成 "job-" + 16 hex（8 随机字节）形式的任务 ID。
func newJobID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "job-" + hex.EncodeToString(b), nil
}

// CreateJob 落一条新任务，初始状态 pending_confirm。
func CreateJob(kind, scopeRel string, plan []Step, impact Impact) (Job, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return Job{}, fmt.Errorf("tasks: 未初始化（InitTasks 未调用）")
	}

	id, err := newJobID()
	if err != nil {
		return Job{}, fmt.Errorf("生成任务 ID 失败: %w", err)
	}

	planJSON, err := json.Marshal(plan)
	if err != nil {
		return Job{}, fmt.Errorf("序列化 plan 失败: %w", err)
	}
	impactJSON, err := json.Marshal(impact)
	if err != nil {
		return Job{}, fmt.Errorf("序列化 impact 失败: %w", err)
	}

	now := time.Now().UTC()
	job := Job{
		ID:        id,
		Kind:      kind,
		Status:    StatusPendingConfirm,
		ScopeRel:  scopeRel,
		Plan:      plan,
		Impact:    impact,
		CreatedAt: now,
	}

	_, err = db.Exec(
		`INSERT INTO jobs (id, kind, status, scope_rel, plan_json, impact_json,
			progress_cur, progress_total, progress_note, checkpoint_json, result_json, error, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, 0, 0, '', '{}', '', '', ?)`,
		job.ID, job.Kind, string(job.Status), job.ScopeRel, string(planJSON), string(impactJSON),
		formatTime(now),
	)
	if err != nil {
		return Job{}, fmt.Errorf("插入任务失败: %w", err)
	}

	return job, nil
}

// GetJob 按 ID 读取单个任务。
func GetJob(id string) (Job, bool) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return Job{}, false
	}
	return getJobLocked(id)
}

func getJobLocked(id string) (Job, bool) {
	row := db.QueryRow(`SELECT id, kind, status, scope_rel, plan_json, impact_json,
		progress_cur, progress_total, progress_note, result_json, error, created_at, started_at, finished_at
		FROM jobs WHERE id = ?`, id)
	job, err := scanJob(row)
	if err != nil {
		return Job{}, false
	}
	return job, true
}

// scanner 抽象 *sql.Row / *sql.Rows 共用的 Scan 签名。
type scanner interface {
	Scan(dest ...interface{}) error
}

func scanJob(row scanner) (Job, error) {
	var (
		id, kind, status, scopeRel, planJSON, impactJSON string
		progressCur, progressTotal                       int
		progressNote, resultJSON, errText, createdAt     string
		startedAt, finishedAt                            sql.NullString
	)
	if err := row.Scan(&id, &kind, &status, &scopeRel, &planJSON, &impactJSON,
		&progressCur, &progressTotal, &progressNote, &resultJSON, &errText,
		&createdAt, &startedAt, &finishedAt); err != nil {
		return Job{}, err
	}

	var plan []Step
	_ = json.Unmarshal([]byte(planJSON), &plan)
	var impact Impact
	_ = json.Unmarshal([]byte(impactJSON), &impact)

	job := Job{
		ID:            id,
		Kind:          kind,
		Status:        Status(status),
		ScopeRel:      scopeRel,
		Plan:          plan,
		Impact:        impact,
		ProgressCur:   progressCur,
		ProgressTotal: progressTotal,
		ProgressNote:  progressNote,
		Result:        resultJSON,
		Error:         errText,
		CreatedAt:     parseTime(createdAt),
	}
	if startedAt.Valid && startedAt.String != "" {
		t := parseTime(startedAt.String)
		job.StartedAt = &t
	}
	if finishedAt.Valid && finishedAt.String != "" {
		t := parseTime(finishedAt.String)
		job.FinishedAt = &t
	}
	return job, nil
}

// ListJobs 按 created_at 新->旧返回最多 limit 条任务。limit<=0 时默认 20。
func ListJobs(limit int) []Job {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := db.Query(`SELECT id, kind, status, scope_rel, plan_json, impact_json,
		progress_cur, progress_total, progress_note, result_json, error, created_at, started_at, finished_at
		FROM jobs ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := []Job{}
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			continue
		}
		out = append(out, job)
	}
	return out
}

// FindPendingJob 查找同 (kind, scopeRel) 下仍处于 pending_confirm 的任务
// （plan_task 防重复扫描：已有未确认的计划就直接复用它）。
func FindPendingJob(kind, scopeRel string) (Job, bool) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return Job{}, false
	}
	row := db.QueryRow(`SELECT id, kind, status, scope_rel, plan_json, impact_json,
		progress_cur, progress_total, progress_note, result_json, error, created_at, started_at, finished_at
		FROM jobs WHERE kind = ? AND scope_rel = ? AND status = ?
		ORDER BY created_at DESC LIMIT 1`, kind, scopeRel, string(StatusPendingConfirm))
	job, err := scanJob(row)
	if err != nil {
		return Job{}, false
	}
	return job, true
}

// UpdateJobStatus 执行状态机校验后的迁移；非法迁移返回 error，库不被修改。
// errText 非空时同时写入 error 字段（通常用于 failed 终态）。
func UpdateJobStatus(id string, to Status, errText string) error {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return fmt.Errorf("tasks: 未初始化")
	}

	job, ok := getJobLocked(id)
	if !ok {
		return fmt.Errorf("任务不存在: %s", id)
	}

	allowed := legalTransitions[job.Status]
	if !allowed[to] {
		return fmt.Errorf("非法状态迁移: %s -> %s", job.Status, to)
	}

	now := time.Now().UTC()
	switch to {
	case StatusRunning:
		if job.StartedAt == nil {
			_, err := db.Exec(`UPDATE jobs SET status = ?, error = ?, started_at = ? WHERE id = ?`,
				string(to), errText, formatTime(now), id)
			return err
		}
		_, err := db.Exec(`UPDATE jobs SET status = ?, error = ? WHERE id = ?`, string(to), errText, id)
		return err
	case StatusDone, StatusFailed, StatusCancelled:
		_, err := db.Exec(`UPDATE jobs SET status = ?, error = ?, finished_at = ? WHERE id = ?`,
			string(to), errText, formatTime(now), id)
		return err
	default:
		_, err := db.Exec(`UPDATE jobs SET status = ?, error = ? WHERE id = ?`, string(to), errText, id)
		return err
	}
}

// SetProgress 更新进度游标/总数/备注。total<=0 时按 0 记录（防除零在
// 调用方——里程碑百分比计算），不在此处强行设为非零。
func SetProgress(id string, cur, total int, note string) error {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return fmt.Errorf("tasks: 未初始化")
	}
	if total < 0 {
		total = 0
	}
	if cur < 0 {
		cur = 0
	}
	_, err := db.Exec(`UPDATE jobs SET progress_cur = ?, progress_total = ?, progress_note = ? WHERE id = ?`,
		cur, total, note, id)
	return err
}

// SaveCheckpoint 把 doneKeys 追加合并进既有 checkpoint 集合（幂等，重复
// key 不产生重复记录）。
func SaveCheckpoint(id string, doneKeys []string) error {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return fmt.Errorf("tasks: 未初始化")
	}

	cp := loadCheckpointLocked(id)
	for _, k := range doneKeys {
		cp[k] = true
	}
	data, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE jobs SET checkpoint_json = ? WHERE id = ?`, string(data), id)
	return err
}

// LoadCheckpoint 返回已完成步骤 key 的集合（key 存在且为 true 表示已完成）。
func LoadCheckpoint(id string) map[string]bool {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return map[string]bool{}
	}
	return loadCheckpointLocked(id)
}

func loadCheckpointLocked(id string) map[string]bool {
	var raw string
	err := db.QueryRow(`SELECT checkpoint_json FROM jobs WHERE id = ?`, id).Scan(&raw)
	if err != nil || raw == "" {
		return map[string]bool{}
	}
	var cp map[string]bool
	if err := json.Unmarshal([]byte(raw), &cp); err != nil || cp == nil {
		return map[string]bool{}
	}
	return cp
}

// FinishJob 落终态（done/failed/cancelled）并记录 result/error，走状态机
// 校验（非法迁移如 pending_confirm -> done 会被拒绝）。
func FinishJob(id string, resultJSON, errText string, status Status) error {
	if status != StatusDone && status != StatusFailed && status != StatusCancelled {
		return fmt.Errorf("FinishJob 只接受终态, got %s", status)
	}
	if err := UpdateJobStatus(id, status, errText); err != nil {
		return err
	}
	if resultJSON == "" {
		return nil
	}
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return fmt.Errorf("tasks: 未初始化")
	}
	_, err := db.Exec(`UPDATE jobs SET result_json = ? WHERE id = ?`, resultJSON, id)
	return err
}

// RegisterRoutes 注册 tasks 模块的 HTTP 路由。路由细节在 Task 2 的
// api.go（registerAPIRoutes）里填充；本 Task 1 只固定签名，对齐 ai 模块的
// aiEnabled 守卫模式（enabledGuard() 返回 false 时全部端点 403）。
func RegisterRoutes(mux *http.ServeMux, enabledGuard func() bool) {
	registerAPIRoutes(mux, enabledGuard)
}

func formatTime(t time.Time) string {
	return t.Format(time.RFC3339Nano)
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
