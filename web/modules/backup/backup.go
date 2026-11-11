package backup

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"nas-panel/common"
)

const backupDirPrefix = "/opt/nas/backups/"

var backupTargetPrefixes = []string{"/mnt", "/data/backup"}

var backupRemoteTargetPattern = regexp.MustCompile(`^(?:[A-Za-z0-9._-]+@)?[A-Za-z0-9._-]+:(.*)$`)

func resolveExistingBackupPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", err
	}
	resolvedParent, parentErr := resolveExistingBackupPath(parent)
	if parentErr != nil {
		return "", parentErr
	}
	return filepath.Join(resolvedParent, filepath.Base(path)), nil
}

func validateBackupTarget(target string) (string, error) {
	target = strings.TrimSpace(target)
	if strings.Contains(target, ":") {
		if strings.HasPrefix(target, "-") {
			return "", fmt.Errorf("invalid backup target")
		}
		matches := backupRemoteTargetPattern.FindStringSubmatch(target)
		if matches == nil {
			return "", fmt.Errorf("invalid backup target")
		}
		remotePath := matches[1]
		if remotePath == "" || strings.HasPrefix(remotePath, "-") {
			return "", fmt.Errorf("invalid backup target")
		}
		for _, r := range remotePath {
			if unicode.IsSpace(r) || unicode.IsControl(r) {
				return "", fmt.Errorf("invalid backup target")
			}
		}
		for _, part := range strings.Split(remotePath, "/") {
			if part == ".." {
				return "", fmt.Errorf("invalid backup target")
			}
		}
		return target, nil
	}

	clean := filepath.Clean(target)
	allowed := func(path string) bool {
		for _, prefix := range backupTargetPrefixes {
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				if prefix == "/mnt" && path == "/mnt" {
					continue
				}
				return true
			}
		}
		return false
	}
	if !allowed(clean) {
		return "", fmt.Errorf("invalid backup target")
	}
	// 目标条目本身不能是符号链接 —— 在 EvalSymlinks 展开之前检查，
	// 否则解析后拿到的是链接指向的真实路径，Lstat 查不到链接本身。
	if linkInfo, lerr := os.Lstat(clean); lerr == nil && linkInfo.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("invalid backup target")
	}
	resolved, err := resolveExistingBackupPath(clean)
	if err != nil {
		return "", fmt.Errorf("invalid backup target")
	}
	clean = resolved
	if !allowed(clean) {
		return "", fmt.Errorf("invalid backup target")
	}
	info, err := os.Lstat(clean)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(clean, 0755); err != nil {
			return "", fmt.Errorf("invalid backup target")
		}
		info, err = os.Lstat(clean)
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("invalid backup target")
	}
	return clean, nil
}

// validateBackupPath 校验备份文件路径，防止目录穿越（如 ../../ 逃出 backups 目录）
// 或通过双斜杠等未归一化输入绕过前缀/后缀检查。返回归一化后的路径与是否合法。
func validateBackupPath(p string) (string, bool) {
	clean := filepath.Clean(p)
	if clean != p {
		return "", false
	}
	if !strings.HasPrefix(clean, backupDirPrefix) {
		return "", false
	}
	if !strings.HasSuffix(clean, ".tar.gz") {
		return "", false
	}
	if clean == backupDirPrefix {
		return "", false
	}
	return clean, true
}

func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/backup/list", common.AuthMiddleware(handleBackupList))
	mux.HandleFunc("/api/backup/create", common.AdminMiddleware(handleBackupCreate))
	mux.HandleFunc("/api/backup/restore", common.AdminMiddleware(handleBackupRestore))
	mux.HandleFunc("/api/backup/delete", common.AdminMiddleware(handleBackupDelete))
	mux.HandleFunc("/api/backup/data", common.AdminMiddleware(handleBackupData))
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
	backupFile, ok := validateBackupPath(backupFile)
	if !ok {
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
		common.LogAuditRequest(r, "BACKUP", "restore_config", "file="+backupFile, "failed")
		http.Error(w, fmt.Sprintf(`{"error": %q}`, string(out)), http.StatusInternalServerError)
		return
	}
	common.LogAuditRequest(r, "BACKUP", "restore_config", "file="+backupFile, "success")
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
	file, ok := validateBackupPath(file)
	if !ok {
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
	target, err := validateBackupTarget(target)
	if err != nil {
		http.Error(w, `{"error":"invalid backup target"}`, http.StatusBadRequest)
		return
	}
	out, err := common.SudoExec("/opt/nas/scripts/backup-data.sh", target)
	if err != nil {
		common.LogAuditRequest(r, "BACKUP", "数据备份", "target="+target, "failed")
		common.EmitEvent("backup", common.EventError, "backup.data_failed",
			map[string]interface{}{"target": target}, "")
		b, _ := json.Marshal(map[string]string{"error": "数据备份失败: " + out})
		http.Error(w, string(b), http.StatusInternalServerError)
		return
	}
	common.LogAuditRequest(r, "BACKUP", "数据备份", "target="+target, "success")
	common.EmitEvent("backup", common.EventSuccess, "backup.data_done",
		map[string]interface{}{"target": target}, "")
	common.JSONResponse(w, map[string]interface{}{
		"message": "数据备份完成",
		"output":  out,
	})
}
