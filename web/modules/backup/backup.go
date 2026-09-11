package backup

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"nas-panel/common"
)

func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/backup/list", common.AuthMiddleware(handleBackupList))
	mux.HandleFunc("/api/backup/create", common.AuthMiddleware(handleBackupCreate))
	mux.HandleFunc("/api/backup/restore", common.AuthMiddleware(handleBackupRestore))
	mux.HandleFunc("/api/backup/delete", common.AuthMiddleware(handleBackupDelete))
	mux.HandleFunc("/api/backup/data", common.AuthMiddleware(handleBackupData))
}

type BackupInfo struct {
	Name string `json:"name"`
	Size string `json:"size"`
	Time string `json:"time"`
	Path string `json:"path"`
}

// handleBackupList returns list of available backups
func handleBackupList(w http.ResponseWriter, r *http.Request) {
	backupDir := "/opt/nas/backups"
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		common.JSONResponse(w, map[string]interface{}{"backups": []BackupInfo{}, "error": "no backup dir"})
		return
	}

	var backups []BackupInfo
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "config-") || !strings.HasSuffix(name, ".tar.gz") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		// Parse timestamp from filename: config-YYYYMMDD-HHMMSS.tar.gz
		tsStr := strings.TrimSuffix(strings.TrimPrefix(name, "config-"), ".tar.gz")
		t, err := time.Parse("20060102-150405", tsStr)
		timeStr := tsStr
		if err == nil {
			timeStr = t.Format("2006-01-02 15:04:05")
		}
		// Human readable size
		size := fmt.Sprintf("%dKB", info.Size()/1024)
		if info.Size() >= 1024*1024 {
			size = fmt.Sprintf("%.1fMB", float64(info.Size())/1024/1024)
		}
		backups = append(backups, BackupInfo{
			Name: name,
			Size: size,
			Time: timeStr,
			Path: backupDir + "/" + name,
		})
	}

	// Sort by time descending (newest first)
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Time > backups[j].Time
	})

	common.JSONResponse(w, map[string]interface{}{
		"backups": backups,
		"total":   len(backups),
	})
}

// handleBackupCreate triggers a manual backup
func handleBackupCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	out, err := common.SudoExec("/opt/nas/scripts/backup-config.sh")
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, out), http.StatusInternalServerError)
		return
	}
	common.JSONResponse(w, map[string]interface{}{
		"message": "备份已创建",
		"output":  out,
	})
}

// handleBackupRestore restores from a backup file
func handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	backupFile := r.FormValue("file")
	if backupFile == "" {
		http.Error(w, `{"error": "file parameter required"}`, http.StatusBadRequest)
		return
	}
	// Safety: only allow files in /opt/nas/backups/
	if !strings.HasPrefix(backupFile, "/opt/nas/backups/") || !strings.HasSuffix(backupFile, ".tar.gz") {
		http.Error(w, `{"error": "invalid backup file path"}`, http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(backupFile); err != nil {
		http.Error(w, `{"error": "file not found"}`, http.StatusBadRequest)
		return
	}

	// Run restore script (needs confirmation, pipe "yes")
	cmd := exec.Command("sudo", "/opt/nas/scripts/restore-config.sh", backupFile)
	cmd.Stdin = strings.NewReader("yes\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, string(out)), http.StatusInternalServerError)
		return
	}
	common.JSONResponse(w, map[string]interface{}{
		"message": "配置已从备份恢复，服务已重启",
		"output":  string(out),
	})
}

// handleBackupDelete deletes a backup file
func handleBackupDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	file := r.FormValue("file")
	if file == "" {
		http.Error(w, `{"error": "file parameter required"}`, http.StatusBadRequest)
		return
	}
	// Safety check
	if !strings.HasPrefix(file, "/opt/nas/backups/") || !strings.HasSuffix(file, ".tar.gz") {
		http.Error(w, `{"error": "invalid file path"}`, http.StatusBadRequest)
		return
	}
	_, err := common.SudoExec("rm", "-f", file)
	if err != nil {
		http.Error(w, `{"error": "delete failed"}`, http.StatusInternalServerError)
		return
	}
	common.JSONResponse(w, map[string]interface{}{"message": "备份已删除"})
}

// handleBackupData runs a data backup (rsync via backup-data.sh) to the given target
func handleBackupData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	target := strings.TrimSpace(r.FormValue("target"))
	if target == "" {
		http.Error(w, `{"error":"请指定备份目标路径"}`, http.StatusBadRequest)
		return
	}
	// 安全：拒绝根目录/面板自身路径/伪文件系统
	if target == "/" || target == "/data" || strings.HasPrefix(target, "/opt/nas") ||
		strings.HasPrefix(target, "/proc") || strings.HasPrefix(target, "/sys") {
		http.Error(w, `{"error":"无效的备份目标路径"}`, http.StatusBadRequest)
		return
	}
	out, err := common.SudoExec("/opt/nas/scripts/backup-data.sh", target)
	if err != nil {
		b, _ := json.Marshal(map[string]string{"error": "数据备份失败: " + out})
		http.Error(w, string(b), http.StatusInternalServerError)
		return
	}
	common.LogAudit("system", "数据备份", "BACKUP", "/api/backup/data", "target="+target, "success", "")
	common.JSONResponse(w, map[string]interface{}{
		"message": "数据备份完成",
		"output":  out,
	})
}
