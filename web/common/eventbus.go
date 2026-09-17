package common

// Event Bus 事件中心（P1）
//
// 与 audit log 的分工：audit 记录「谁通过 API 做了什么」（请求审计），
// event 记录「系统发生了什么」（健康异常、磁盘事件、池状态变化、升级结果、
// 备份/同步成败、防火墙变更等），由各模块主动 EmitEvent 上报。
//
// 设计参照 logger.go 的成熟模式：
//   - 投递非阻塞（buffered channel，满则丢弃，绝不阻塞业务路径）
//   - 后台 writer 单 goroutine 串行写 SQLite（events.db，WAL）
//   - 进程内 pub/sub：Subscribe/Unsubscribe，订阅者收同一份事件流
//   - 自动清理：默认保留 90 天

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Event 是一条系统事件。
type Event struct {
	ID        int64  `json:"id"`
	Timestamp string `json:"time"`
	Source    string `json:"source"` // 模块名：health/diskmgmt/storage/update/rclone/firewall...
	Type      string `json:"type"`   // info | success | warn | error
	Code      string `json:"code"`   // i18n 键，如 health.disk_offline
	Params    string `json:"params"` // JSON 字符串，前端插值用
	Detail    string `json:"detail"` // 可选的自由文本补充
}

// 事件类型常量（前端按此着色，勿随意加值）
const (
	EventInfo    = "info"
	EventSuccess = "success"
	EventWarn    = "warn"
	EventError   = "error"
)

// eventRetentionDays 是事件保留天数。
const eventRetentionDays = 90

var (
	eventDB   *sql.DB
	eventChan chan Event

	eventSubMu sync.Mutex
	eventSubs  = map[chan Event]struct{}{}
)

// InitEventBus 打开 events.db 并启动后台 writer / 清理任务。
// 幂等：重复调用只初始化一次。
func InitEventBus(dataDir string) {
	if initEventBusCore(dataDir) {
		go eventWriter()
		go eventCleanupLoop()
	}
}

// InitEventBusForTest 只建库不起后台 goroutine（测试用，配合 DrainEventsForTest 同步消费）。
func InitEventBusForTest(dataDir string) {
	initEventBusCore(dataDir)
}

// initEventBusCore 打开库建表；返回 true 表示本次调用完成了初始化。
func initEventBusCore(dataDir string) bool {
	if eventDB != nil {
		return false
	}
	os.MkdirAll(dataDir, 0755)
	dbPath := filepath.Join(dataDir, "events.db")

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Printf("WARNING: cannot open events db: %v", err)
		return false
	}
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT '',
			type TEXT NOT NULL DEFAULT 'info',
			code TEXT NOT NULL DEFAULT '',
			params TEXT NOT NULL DEFAULT '{}',
			detail TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_events_time ON events(timestamp);
		CREATE INDEX IF NOT EXISTS idx_events_source ON events(source);
		CREATE INDEX IF NOT EXISTS idx_events_type ON events(type);
	`)
	if err != nil {
		log.Printf("WARNING: cannot create events table: %v", err)
		return false
	}
	eventDB = db
	eventChan = make(chan Event, 256)
	log.Printf("Event bus initialized: %s", dbPath)
	return true
}

// EmitEvent 上报一条系统事件（非阻塞，channel 满则丢弃）。
// params 会被序列化为 JSON；nil 存 "{}"。
func EmitEvent(source, typ, code string, params map[string]interface{}, detail string) {
	if eventChan == nil {
		return
	}
	if typ != EventInfo && typ != EventSuccess && typ != EventWarn && typ != EventError {
		typ = EventInfo
	}
	pj := "{}"
	if params != nil {
		if b, err := json.Marshal(params); err == nil {
			pj = string(b)
		}
	}
	ev := Event{
		Timestamp: time.Now().Format(time.RFC3339),
		Source:    source,
		Type:      typ,
		Code:      code,
		Params:    pj,
		Detail:    detail,
	}
	select {
	case eventChan <- ev:
	default:
		// 背压：丢弃而不是阻塞业务路径
	}
}

// Subscribe 注册一个进程内订阅者，收到后续所有事件。
// 订阅者 channel 有缓冲；消费过慢时新事件对该订阅者丢弃（不影响持久化）。
// 用完必须 Unsubscribe，否则泄漏。
func Subscribe() chan Event {
	ch := make(chan Event, 64)
	eventSubMu.Lock()
	eventSubs[ch] = struct{}{}
	eventSubMu.Unlock()
	return ch
}

// Unsubscribe 注销订阅者并关闭其 channel。
func Unsubscribe(ch chan Event) {
	eventSubMu.Lock()
	if _, ok := eventSubs[ch]; ok {
		delete(eventSubs, ch)
		close(ch)
	}
	eventSubMu.Unlock()
}

// eventWriter 串行消费 eventChan：广播给订阅者 + 写库。
func eventWriter() {
	for ev := range eventChan {
		// 1. 广播（非阻塞）
		eventSubMu.Lock()
		for ch := range eventSubs {
			select {
			case ch <- ev:
			default:
			}
		}
		eventSubMu.Unlock()
		// 2. 持久化
		if eventDB == nil {
			continue
		}
		_, err := eventDB.Exec(
			`INSERT INTO events (timestamp, source, type, code, params, detail)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			ev.Timestamp, ev.Source, ev.Type, ev.Code, ev.Params, ev.Detail,
		)
		if err != nil {
			log.Printf("WARNING: event insert failed: %v", err)
		}
	}
}

// eventCleanupLoop 每天清理一次过期事件。
func eventCleanupLoop() {
	cleanupOldEvents()
	for range time.Tick(24 * time.Hour) {
		cleanupOldEvents()
	}
}

func cleanupOldEvents() {
	if eventDB == nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -eventRetentionDays).Format(time.RFC3339)
	res, err := eventDB.Exec(`DELETE FROM events WHERE timestamp < ?`, cutoff)
	if err != nil {
		log.Printf("WARNING: event cleanup failed: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("Event cleanup: removed %d events older than %d days", n, eventRetentionDays)
	}
}

// QueryEvents 按条件分页查询事件。source/type 精确匹配，days>0 限定时间窗。
// 返回 (当页事件, 匹配总数, error)。
func QueryEvents(source, typ string, days, limit, offset int) ([]Event, int, error) {
	if eventDB == nil {
		return nil, 0, fmt.Errorf("event db not initialized")
	}
	where := []string{"1=1"}
	args := []interface{}{}
	if source != "" {
		where = append(where, "source = ?")
		args = append(args, source)
	}
	if typ != "" {
		where = append(where, "type = ?")
		args = append(args, typ)
	}
	if days > 0 {
		where = append(where, "timestamp >= ?")
		args = append(args, time.Now().AddDate(0, 0, -days).Format(time.RFC3339))
	}
	whereClause := strings.Join(where, " AND ")

	var total int
	if err := eventDB.QueryRow(
		fmt.Sprintf("SELECT COUNT(*) FROM events WHERE %s", whereClause), args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := eventDB.Query(
		fmt.Sprintf(`SELECT id, timestamp, source, type, code, params, detail
		           FROM events WHERE %s ORDER BY id DESC LIMIT ? OFFSET ?`, whereClause),
		append(args, limit, offset)...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []Event{}
	for rows.Next() {
		var ev Event
		if err := rows.Scan(&ev.ID, &ev.Timestamp, &ev.Source, &ev.Type, &ev.Code, &ev.Params, &ev.Detail); err != nil {
			return nil, 0, err
		}
		out = append(out, ev)
	}
	return out, total, rows.Err()
}

// EventStats 是近 N 天各类型事件计数（事件中心概览卡用）。
type EventStats struct {
	Info    int `json:"info"`
	Success int `json:"success"`
	Warn    int `json:"warn"`
	Error   int `json:"error"`
	Total   int `json:"total"`
}

// QueryEventStats 统计近 days 天各类型事件数；days<=0 按 7 天。
func QueryEventStats(days int) (EventStats, error) {
	var st EventStats
	if eventDB == nil {
		return st, fmt.Errorf("event db not initialized")
	}
	if days <= 0 {
		days = 7
	}
	since := time.Now().AddDate(0, 0, -days).Format(time.RFC3339)
	rows, err := eventDB.Query(
		`SELECT type, COUNT(*) FROM events WHERE timestamp >= ? GROUP BY type`, since)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var typ string
		var n int
		if err := rows.Scan(&typ, &n); err != nil {
			return st, err
		}
		switch typ {
		case EventInfo:
			st.Info = n
		case EventSuccess:
			st.Success = n
		case EventWarn:
			st.Warn = n
		case EventError:
			st.Error = n
		}
	}
	st.Total = st.Info + st.Success + st.Warn + st.Error
	return st, rows.Err()
}

// DrainEventsForTest 同步排空 eventChan 并写库，仅供单测使用。
func DrainEventsForTest() {
	for {
		select {
		case ev := <-eventChan:
			eventSubMu.Lock()
			for ch := range eventSubs {
				select {
				case ch <- ev:
				default:
				}
			}
			eventSubMu.Unlock()
			if eventDB != nil {
				eventDB.Exec(
					`INSERT INTO events (timestamp, source, type, code, params, detail)
					 VALUES (?, ?, ?, ?, ?, ?)`,
					ev.Timestamp, ev.Source, ev.Type, ev.Code, ev.Params, ev.Detail)
			}
		default:
			return
		}
	}
}

// CloseEventBusForTest 关闭事件库，仅供单测清理。
func CloseEventBusForTest() {
	if eventDB != nil {
		eventDB.Close()
		eventDB = nil
	}
	eventChan = nil
}
