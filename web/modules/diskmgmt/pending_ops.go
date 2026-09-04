package diskmgmt

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"nas-panel/common"
)

// ═══════════════════════════════════════════════════════════
// 待操作队列 — 用户操作先暂存，确认后统一应用
// ═══════════════════════════════════════════════════════════

// PendingOp represents a pending folder operation
type PendingOp struct {
	ID         int    `json:"id"`
	Action     string `json:"action"` // create, update, delete
	FolderName string `json:"folder_name"`
	FolderPath string `json:"folder_path"`
	Pool       string `json:"pool"`
	Permission string `json:"permission"`
	ValidUsers string `json:"valid_users"`
	RecycleBin bool   `json:"recycle_bin"`
	SambaShare bool   `json:"samba_share"`
	NFSExport  bool   `json:"nfs_export"`
	QuotaGB    int    `json:"quota_gb"`
	CreatedAt  string `json:"created_at"`
}

// OperationLog represents a completed operation log entry
type OperationLog struct {
	ID        int    `json:"id"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Detail    string `json:"detail"`
	Result    string `json:"result"` // success, error
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"created_at"`
}

func initPendingDB() *sql.DB {
	db := initConfigDB()
	if db == nil {
		return nil
	}
	db.Exec(`CREATE TABLE IF NOT EXISTS pending_ops (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		action TEXT NOT NULL,
		folder_name TEXT NOT NULL,
		folder_path TEXT NOT NULL,
		pool TEXT NOT NULL DEFAULT '',
		permission TEXT NOT NULL DEFAULT 'readwrite',
		valid_users TEXT NOT NULL DEFAULT '',
		recycle_bin INTEGER NOT NULL DEFAULT 0,
		samba_share INTEGER NOT NULL DEFAULT 1,
		nfs_export INTEGER NOT NULL DEFAULT 0,
		quota_gb INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS operation_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		action TEXT NOT NULL,
		target TEXT NOT NULL,
		detail TEXT NOT NULL DEFAULT '',
		result TEXT NOT NULL DEFAULT 'success',
		error TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`)

	// Auto-cleanup: keep only last 1000 logs
	cleanupOldLogs(db)

	return db
}

// cleanupOldLogs keeps only the last 1000 operation log entries
func cleanupOldLogs(db *sql.DB) {
	var count int
	db.QueryRow("SELECT COUNT(*) FROM operation_logs").Scan(&count)
	if count > 1000 {
		db.Exec("DELETE FROM operation_logs WHERE id NOT IN (SELECT id FROM operation_logs ORDER BY id DESC LIMIT 1000)")
		log.Printf("[LOG] 日志清理: %d → 1000 条", count)
	}
}

// AddPendingOp adds a pending operation
func AddPendingOp(action, name, path, pool, permission, validUsers string, samba, nfs, recycle bool, quotaGB int) error {
	db := initPendingDB()
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	_, err := db.Exec(`INSERT INTO pending_ops (action, folder_name, folder_path, pool, permission, valid_users, recycle_bin, samba_share, nfs_export, quota_gb)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		action, name, path, pool, permission, validUsers, boolToInt(recycle), boolToInt(samba), boolToInt(nfs), quotaGB)
	if err != nil {
		log.Printf("[PENDING] 添加待操作失败: %v", err)
		return err
	}
	log.Printf("[PENDING] 已暂存: %s %s", action, name)
	return nil
}

// GetPendingOps returns all pending operations
func GetPendingOps() []PendingOp {
	db := initPendingDB()
	if db == nil {
		return nil
	}
	rows, err := db.Query("SELECT id, action, folder_name, folder_path, pool, permission, valid_users, recycle_bin, samba_share, nfs_export, quota_gb, created_at FROM pending_ops ORDER BY id")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var ops []PendingOp
	for rows.Next() {
		var op PendingOp
		var rb, smb, nfs int
		rows.Scan(&op.ID, &op.Action, &op.FolderName, &op.FolderPath, &op.Pool, &op.Permission, &op.ValidUsers, &rb, &smb, &nfs, &op.QuotaGB, &op.CreatedAt)
		op.RecycleBin = rb != 0
		op.SambaShare = smb != 0
		op.NFSExport = nfs != 0
		ops = append(ops, op)
	}
	return ops
}

// PendingCount returns the number of pending operations
func PendingCount() int {
	db := initPendingDB()
	if db == nil {
		return 0
	}
	var count int
	db.QueryRow("SELECT COUNT(*) FROM pending_ops").Scan(&count)
	return count
}

// ApplyPendingOps applies all pending operations and clears the queue
func ApplyPendingOps() ([]string, error) {
	db := initPendingDB()
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}

	ops := GetPendingOps()
	if len(ops) == 0 {
		return nil, nil
	}

	var results []string
	for _, op := range ops {
		var err error
		switch op.Action {
		case "create":
			err = executeCreateFolder(op)
		case "update":
			err = executeUpdateFolder(op)
		case "delete":
			err = executeDeleteFolder(op)
		}

		detail := fmt.Sprintf("%s %s (pool=%s, perm=%s, users=%s)", op.Action, op.FolderName, op.Pool, op.Permission, op.ValidUsers)
		if err != nil {
			logOp(op.Action, op.FolderName, detail, "error", err.Error())
			results = append(results, fmt.Sprintf("✗ %s %s: %v", op.Action, op.FolderName, err))
		} else {
			logOp(op.Action, op.FolderName, detail, "success", "")
			results = append(results, fmt.Sprintf("✓ %s %s", op.Action, op.FolderName))
		}
		// Audit log entry for the actual filesystem change
		result := "success"
		auditDetail := fmt.Sprintf("%s 共享文件夹 %s (pool=%s, perm=%s)", map[string]string{"create": "创建", "update": "更新", "delete": "删除"}[op.Action], op.FolderName, op.Pool, op.Permission)
		if err != nil {
			result = "error"
			auditDetail += " 失败: " + err.Error()
		}
		common.LogAudit("system", "存储变更", "APPLY", "/api/disk/pending/apply", auditDetail, result, "")
	}

	// Clear pending queue
	db.Exec("DELETE FROM pending_ops")

	// Sync configs and reload services
	SyncAllConfigs()
	reloadServices()

	return results, nil
}

// DiscardPendingOps clears the pending queue without applying
func DiscardPendingOps() {
	db := initPendingDB()
	if db == nil {
		return
	}
	db.Exec("DELETE FROM pending_ops")
	log.Printf("[PENDING] 已清空待操作队列")
}

// executeCreateFolder actually creates a folder
func executeCreateFolder(op PendingOp) error {
	folderPath := op.FolderPath
	if folderPath == "" {
		folderPath = filepath.Join(op.Pool, op.FolderName)
	}

	// Create directory
	common.SudoExec("mkdir", "-p", folderPath)
	nasUser := getNASUser()
	common.SudoExec("chown", "-R", nasUser+":"+nasUser, folderPath)

	// Sync metadata
	SyncFolderMeta(op.FolderName, folderPath, op.Pool, op.Permission, op.ValidUsers, "", op.SambaShare, op.NFSExport, op.RecycleBin, op.QuotaGB)

	return nil
}

// executeUpdateFolder updates permissions
func executeUpdateFolder(op PendingOp) error {
	SyncFolderMeta(op.FolderName, op.FolderPath, op.Pool, op.Permission, op.ValidUsers, "", op.SambaShare, op.NFSExport, op.RecycleBin, op.QuotaGB)
	return nil
}

// executeDeleteFolder deletes a folder
func executeDeleteFolder(op PendingOp) error {
	path := op.FolderPath
	if path == "" {
		return fmt.Errorf("路径为空")
	}
	if !strings.HasPrefix(path, "/data/") {
		return fmt.Errorf("只允许删除 /data/ 下的文件夹")
	}

	// Remove Samba share
	smbConf, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")
	if smbConf != "" {
		newConf := removeSambaShare(smbConf, op.FolderName)
		if newConf != smbConf {
			common.SafeWriteFile("/etc/samba/smb.conf", newConf)
		}
	}

	// Remove NFS export
	exportsData, _ := common.SudoOutput("cat", "/etc/exports")
	if exportsData != "" {
		var newLines []string
		for _, line := range strings.Split(exportsData, "\n") {
			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) >= 1 && fields[0] == path {
				continue
			}
			newLines = append(newLines, line)
		}
		common.SafeWriteFile("/etc/exports", strings.Join(newLines, "\n"))
		common.SudoExec("exportfs", "-a")
	}

	// Delete directory
	common.SudoExec("rm", "-rf", path)

	// Remove metadata
	RemoveFolderMeta(path)

	return nil
}

// ═══════════════════════════════════════════════════════════
// 操作日志
// ═══════════════════════════════════════════════════════════

func logOp(action, target, detail, result, errMsg string) {
	db := initPendingDB()
	if db == nil {
		return
	}
	db.Exec("INSERT INTO operation_logs (action, target, detail, result, error) VALUES (?, ?, ?, ?, ?)",
		action, target, detail, result, errMsg)
}

// GetOperationLogs returns recent operation logs
func GetOperationLogs(limit int) []OperationLog {
	db := initPendingDB()
	if db == nil {
		return nil
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.Query("SELECT id, action, target, detail, result, COALESCE(error,''), created_at FROM operation_logs ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var logs []OperationLog
	for rows.Next() {
		var l OperationLog
		rows.Scan(&l.ID, &l.Action, &l.Target, &l.Detail, &l.Result, &l.Error, &l.CreatedAt)
		logs = append(logs, l)
	}
	return logs
}

// ═══════════════════════════════════════════════════════════
// API Handlers
// ═══════════════════════════════════════════════════════════

func handlePendingList(w http.ResponseWriter, r *http.Request) {
	ops := GetPendingOps()
	if ops == nil {
		ops = []PendingOp{}
	}
	common.JSONResponse(w, map[string]interface{}{
		"pending": ops,
		"count":   len(ops),
	})
}

func handlePendingApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	results, err := ApplyPendingOps()
	if err != nil {
		common.JSONResponse(w, map[string]interface{}{
			"error": err.Error(),
		})
		return
	}
	common.JSONResponse(w, map[string]interface{}{
		"message": "配置已应用",
		"results": results,
	})
}

func handlePendingDiscard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	DiscardPendingOps()
	common.JSONResponse(w, map[string]interface{}{
		"message": "待操作已清空",
	})
}

func handleOperationLogs(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}
	logs := GetOperationLogs(limit)
	if logs == nil {
		logs = []OperationLog{}
	}
	common.JSONResponse(w, map[string]interface{}{
		"logs": logs,
	})
}

func handleOperationLogsClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	db := initPendingDB()
	if db != nil {
		db.Exec("DELETE FROM operation_logs")
	}
	common.JSONResponse(w, map[string]interface{}{
		"message": "日志已清空",
	})
}
