package ai

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"nas-panel/common"

	_ "modernc.org/sqlite"
)

// aiDBPath 仅供测试注入，对齐 aiConfigPath 模式；生产环境留空，走
// defaultDBPath() 的 /opt/nas/data/ai.db。
var aiDBPath string

func defaultDBPath() string {
	dir := "/opt/nas/data"
	if d := os.Getenv("NAS_DATA_DIR"); d != "" {
		dir = d
	}
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "ai.db")
}

func dbPath() string {
	if aiDBPath != "" {
		return aiDBPath
	}
	return defaultDBPath()
}

var (
	aiDB   *sql.DB
	aiDBMu sync.Mutex
)

// initAIDB 打开（或复用已打开的）ai.db 连接，建表。common.OpenSQLite 自带
// WAL + busy_timeout，与面板其余模块的 sqlite 落盘方式一致。
// 重复调用是安全的：aiDB 非 nil 时直接返回，不会重新打开连接（避免测试
// 每次改 aiDBPath 后遗留旧连接——调用方需要换库时应先 closeAIDBForTest）。
func initAIDB() *sql.DB {
	aiDBMu.Lock()
	defer aiDBMu.Unlock()
	if aiDB != nil {
		return aiDB
	}
	db, err := common.OpenSQLite(dbPath())
	if err != nil {
		// 兜底：与 diagnostics 模块一致，落盘失败退回当前目录文件。
		db, _ = common.OpenSQLite("ai.db")
	}
	db.Exec(`CREATE TABLE IF NOT EXISTS conversations (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		conv_id TEXT NOT NULL,
		role TEXT NOT NULL,
		content TEXT NOT NULL DEFAULT '',
		tool_calls TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_messages_conv_id ON messages(conv_id)`)
	aiDB = db
	return aiDB
}

// closeAIDBForTest 关闭并清空 aiDB，供测试在切换 aiDBPath 后强制重新打开。
// 生产代码路径不调用它——连接在进程生命周期内只开一次。
func closeAIDBForTest() {
	aiDBMu.Lock()
	defer aiDBMu.Unlock()
	if aiDB != nil {
		aiDB.Close()
		aiDB = nil
	}
}

// StoredMessage 是从 sqlite 读出的一条消息，供 LoadConversation 使用。
type StoredMessage struct {
	Role      string
	Content   string
	ToolCalls string // JSON 编码的 []ToolCall，assistant 发起工具调用时非空
	ToolCallID string
	Name      string
}

// SaveMessage 把一条消息落库。toolCalls 是该消息携带的工具调用（JSON 编码，
// 没有则传空字符串）；role=tool 时 content 是工具结果回填文本。
func SaveMessage(convID, role, content, toolCallsJSON string) error {
	db := initAIDB()
	_, err := db.Exec(
		`INSERT INTO messages (conv_id, role, content, tool_calls, created_at) VALUES (?, ?, ?, ?, ?)`,
		convID, role, content, toolCallsJSON, time.Now().Format(time.RFC3339),
	)
	return err
}

// LoadConversation 读取某会话最近 limit 条消息（按时间升序返回，供直接拼进
// LLM 请求历史）。limit<=0 时默认 50。
func LoadConversation(convID string, limit int) ([]StoredMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	db := initAIDB()
	rows, err := db.Query(
		`SELECT role, content, tool_calls FROM messages WHERE conv_id = ? ORDER BY id DESC LIMIT ?`,
		convID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reversed []StoredMessage
	for rows.Next() {
		var m StoredMessage
		if err := rows.Scan(&m.Role, &m.Content, &m.ToolCalls); err != nil {
			return nil, err
		}
		reversed = append(reversed, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 查询是按 id DESC 取最近 N 条，这里反转成时间升序。
	out := make([]StoredMessage, len(reversed))
	for i, m := range reversed {
		out[len(reversed)-1-i] = m
	}
	return out, nil
}

// ConversationSummary 是会话列表项。
type ConversationSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
}

// ListConversations 返回最近 limit 个会话（按创建时间倒序）。limit<=0 时默认 20。
func ListConversations(limit int) ([]ConversationSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	db := initAIDB()
	rows, err := db.Query(
		`SELECT id, title, created_at FROM conversations ORDER BY created_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ConversationSummary{}
	for rows.Next() {
		var c ConversationSummary
		if err := rows.Scan(&c.ID, &c.Title, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// EnsureConversation 确保 convID 对应的会话行存在（不存在则创建，title 留空，
// 由首条用户消息到达时 EnsureTitle 补上）。
func EnsureConversation(convID string) error {
	db := initAIDB()
	_, err := db.Exec(
		`INSERT INTO conversations (id, title, created_at) VALUES (?, '', ?)
		 ON CONFLICT(id) DO NOTHING`,
		convID, time.Now().Format(time.RFC3339),
	)
	return err
}

// titleMaxLen 是会话标题（首条用户消息截取）的最大字符数。
const titleMaxLen = 30

// EnsureTitle 在会话标题为空时，用 firstUserMsg 的前 30 个字符补上标题。
// 已有标题的会话不会被覆盖（"首条用户消息"的语义只在创建时生效一次）。
func EnsureTitle(convID, firstUserMsg string) error {
	db := initAIDB()
	var title string
	err := db.QueryRow(`SELECT title FROM conversations WHERE id = ?`, convID).Scan(&title)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	if title != "" {
		return nil
	}
	r := []rune(firstUserMsg)
	if len(r) > titleMaxLen {
		r = r[:titleMaxLen]
	}
	_, err = db.Exec(`UPDATE conversations SET title = ? WHERE id = ?`, string(r), convID)
	return err
}

// toolCallsToJSON / toolCallsFromJSON 是 []ToolCall 的落库编解码辅助——
// sqlite 表里 tool_calls 列存的是这个 JSON 字符串。
func toolCallsToJSON(tc []ToolCall) string {
	if len(tc) == 0 {
		return ""
	}
	data, err := json.Marshal(tc)
	if err != nil {
		return ""
	}
	return string(data)
}

func toolCallsFromJSON(s string) []ToolCall {
	if s == "" {
		return nil
	}
	var tc []ToolCall
	if err := json.Unmarshal([]byte(s), &tc); err != nil {
		return nil
	}
	return tc
}
