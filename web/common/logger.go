package common

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var auditDB *sql.DB
var auditChan chan auditRecord

type auditRecord struct {
	username, action, method, path, detail, result, ip string
}

// InitAuditLog opens the SQLite audit log database and creates tables
func InitAuditLog(dataDir string) {
	dbPath := filepath.Join(dataDir, "audit.db")
	os.MkdirAll(dataDir, 0755)

	var err error
	auditDB, err = sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Printf("WARNING: cannot open audit db: %v", err)
		return
	}

	_, err = auditDB.Exec(`
		CREATE TABLE IF NOT EXISTS audit_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp TEXT NOT NULL,
			username TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT '',
			method TEXT NOT NULL DEFAULT '',
			path TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			result TEXT NOT NULL DEFAULT 'success',
			ip TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_audit_time ON audit_log(timestamp);
		CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_log(username);
		CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_log(action);
	`)
	if err != nil {
		log.Printf("WARNING: cannot create audit tables: %v", err)
	}

	// Auto-cleanup old logs
	go cleanupOldLogs()

	// Start async audit writer (buffered channel, non-blocking for callers)
	auditChan = make(chan auditRecord, 256)
	go auditWriter()

	log.Printf("Audit log initialized: %s", dbPath)
}

// auditWriter drains the audit channel and writes to SQLite in background
func auditWriter() {
	for rec := range auditChan {
		if auditDB == nil {
			continue
		}
		_, err := auditDB.Exec(
			`INSERT INTO audit_log (timestamp, username, action, method, path, detail, result, ip) 
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			time.Now().Format(time.RFC3339), rec.username, rec.action, rec.method, rec.path, rec.detail, rec.result, rec.ip,
		)
		if err != nil {
			log.Printf("WARNING: audit log insert failed: %v", err)
		}
	}
}

// LogAudit records an operation to the audit log (non-blocking)
func LogAudit(username, action, method, path, detail, result, ip string) {
	if auditChan == nil {
		return
	}
	select {
	case auditChan <- auditRecord{username, action, method, path, detail, result, ip}:
	default:
		// Channel full, drop to avoid blocking the request
	}
}

// AuditEntry represents a single audit log entry
type AuditEntry struct {
	ID        int    `json:"id"`
	Timestamp string `json:"time"`
	Username  string `json:"username"`
	Action    string `json:"action"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Detail    string `json:"detail"`
	Result    string `json:"result"`
	IP        string `json:"ip"`
}

// QueryAuditLog returns audit log entries with filters
func QueryAuditLog(username, action, result string, days, limit, offset int) ([]AuditEntry, int, error) {
	if auditDB == nil {
		return nil, 0, fmt.Errorf("audit db not initialized")
	}

	where := []string{"1=1"}
	args := []interface{}{}

	if username != "" {
		where = append(where, "username = ?")
		args = append(args, username)
	}
	if action != "" {
		where = append(where, "action LIKE ?")
		args = append(args, "%"+action+"%")
	}
	if result != "" {
		where = append(where, "result = ?")
		args = append(args, result)
	}
	if days > 0 {
		since := time.Now().AddDate(0, 0, -days).Format(time.RFC3339)
		where = append(where, "timestamp >= ?")
		args = append(args, since)
	}

	whereClause := strings.Join(where, " AND ")

	// Count total
	var total int
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM audit_log WHERE %s", whereClause)
	auditDB.QueryRow(countQuery, args...).Scan(&total)

	// Query entries
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	query := fmt.Sprintf(
		"SELECT id, timestamp, username, action, method, path, detail, result, ip FROM audit_log WHERE %s ORDER BY id DESC LIMIT ? OFFSET ?",
		whereClause,
	)
	args = append(args, limit, offset)

	rows, err := auditDB.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []AuditEntry
	for rows.Next() {
		var e AuditEntry
		rows.Scan(&e.ID, &e.Timestamp, &e.Username, &e.Action, &e.Method, &e.Path, &e.Detail, &e.Result, &e.IP)
		entries = append(entries, e)
	}
	return entries, total, nil
}

// ClearAuditLog deletes all or old audit log entries
func ClearAuditLog(days int) (int64, error) {
	if auditDB == nil {
		return 0, fmt.Errorf("audit db not initialized")
	}
	if days > 0 {
		since := time.Now().AddDate(0, 0, -days).Format(time.RFC3339)
		result, err := auditDB.Exec("DELETE FROM audit_log WHERE timestamp < ?", since)
		if err != nil {
			return 0, err
		}
		return result.RowsAffected()
	}
	result, err := auditDB.Exec("DELETE FROM audit_log")
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// GetAuditLogCount returns total log entries
func GetAuditLogCount() int {
	if auditDB == nil {
		return 0
	}
	var count int
	auditDB.QueryRow("SELECT COUNT(*) FROM audit_log").Scan(&count)
	return count
}

// GetRetentionDays returns configured retention days
func GetRetentionDays() int {
	daysStr := os.Getenv("NAS_LOG_RETENTION_DAYS")
	if daysStr == "" {
		// Try reading from .env file
		envFile := GetEnvFilePath()
		if val, err := ReadEnvFile(envFile, "NAS_LOG_RETENTION_DAYS"); err == nil && val != "" {
			daysStr = val
		}
	}
	if daysStr == "" {
		return 90 // default
	}
	days, err := strconv.Atoi(daysStr)
	if err != nil || days < 1 {
		return 90
	}
	return days
}

func cleanupOldLogs() {
	retention := GetRetentionDays()
	if retention <= 0 {
		return
	}
	since := time.Now().AddDate(0, 0, -retention).Format(time.RFC3339)
	if auditDB != nil {
		result, err := auditDB.Exec("DELETE FROM audit_log WHERE timestamp < ?", since)
		if err == nil {
			n, _ := result.RowsAffected()
			if n > 0 {
				log.Printf("Audit cleanup: removed %d entries older than %d days", n, retention)
			}
		}
	}
}