package diskmgmt

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"nas-panel/common"
)

// SharedFolder represents a shared directory in a storage volume
type SharedFolder struct {
	Name         string `json:"name"`
	Path         string `json:"path,omitempty"`
	Pool         string `json:"pool"`
	PoolName     string `json:"pool_name"`
	Source       string `json:"source"`
	Owner        string `json:"owner,omitempty"`
	Size         string `json:"size"`
	SambaShare   bool   `json:"samba_share"`
	NFSExport    bool   `json:"nfs_export,omitempty"`
	FTPAccess    bool   `json:"ftp_access,omitempty"`
	WebDAVAccess bool   `json:"webdav_access,omitempty"`
	S3Access     bool   `json:"s3_access,omitempty"`
	Permission   string `json:"permission"`
	ValidUsers   string `json:"valid_users"`
	RecycleBin   bool   `json:"recycle_bin"`
	QuotaGB      int    `json:"quota_gb"`
	QuotaUsed    string `json:"quota_used"`
	Description  string `json:"description,omitempty"`
}

// handleListFolders lists all shared folders under storage spaces
func handleListFolders(w http.ResponseWriter, r *http.Request) {
	var folders []SharedFolder
	mounts := getExistingDataMounts()
	poolSeq := 0
	for _, m := range mounts {
		poolSeq++
		mountPoint := m["mount"]
		if !strings.HasPrefix(mountPoint, "/data") {
			continue
		}
		displayName := fmt.Sprintf("存储空间%d", poolSeq)
		poolName := extractPoolName(mountPoint)

		entries, err := os.ReadDir(mountPoint)
		if err != nil {
			out, _ := common.SudoOutput("ls", "-1", mountPoint)
			for _, name := range strings.Split(out, "\n") {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				folder := SharedFolder{Name: name, Pool: displayName, PoolName: poolName}
				folders = append(folders, folder)
			}
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == "#recycle" {
				continue
			}
			folderPath := filepath.Join(mountPoint, entry.Name())
			folder := SharedFolder{Name: entry.Name(), Path: folderPath, Pool: displayName, PoolName: poolName}
			sizeOut, _ := common.SudoOutput("du", "-sh", folderPath)
			folder.Size = parseDuSize(sizeOut)
			folders = append(folders, folder)
		}
	}

	smbConf, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")
	smbMap := parseSambaShares(smbConf)
	for i, f := range folders {
		if smb, ok := smbMap[f.Path]; ok {
			folders[i].SambaShare = true
			if smb["read_only"] == "yes" {
				folders[i].Permission = "readonly"
			} else {
				folders[i].Permission = "readwrite"
			}
			folders[i].ValidUsers = smb["valid_users"]
			folders[i].RecycleBin = hasRecycleBin(smbConf, f.Name)
		} else {
			folders[i].Permission = "noaccess"
		}
	}

	quotaMap := make(map[string]map[string][2]string)
	for _, m := range mounts {
		mp := m["mount"]
		if !strings.HasPrefix(mp, "/data") {
			continue
		}
		out, err := common.SudoOutput("/usr/sbin/xfs_quota", "-x", "-c", "report -p -N", mp)
		if err != nil {
			continue
		}
		quotaMap[mp] = make(map[string][2]string)
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 4 && fields[0] != "#0" {
				usedKB, _ := strconv.ParseFloat(fields[1], 64)
				hardKB, _ := strconv.Atoi(fields[3])
				usedGB := fmt.Sprintf("%.1f", usedKB/1024/1024)
				limitGB := fmt.Sprintf("%d", hardKB/1024/1024)
				quotaMap[mp][fields[0]] = [2]string{usedGB, limitGB}
			}
		}
	}

	for i, f := range folders {
		for mp, projects := range quotaMap {
			if strings.HasPrefix(f.Path, mp+"/") {
				poolName := extractPoolName(mp)
				projName := projectName(poolName, f.Name)
				if quota, ok := projects[projName]; ok {
					usedGB, _ := strconv.ParseFloat(quota[0], 64)
					limitGB, _ := strconv.Atoi(quota[1])
					folders[i].QuotaGB = limitGB
					folders[i].QuotaUsed = fmt.Sprintf("%.1fG", usedGB)
				}
				break
			}
		}
	}

	common.JSONResponse(w, map[string]interface{}{"folders": folders})
}

func hasRecycleBin(conf, shareName string) bool {
	inShare := false
	for _, line := range strings.Split(conf, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "["+shareName+"]" {
			inShare = true
			continue
		}
		if inShare && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			break
		}
		if inShare && strings.Contains(strings.ToLower(trimmed), "recycle") {
			return true
		}
	}
	return false
}

// handleCreateFolder creates a shared folder (deferred to pending queue)
func handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	pool := r.FormValue("pool")
	name := r.FormValue("name")
	permission := r.FormValue("permission")
	validUsers := r.FormValue("valid_users")
	recycleBin := r.FormValue("recycle_bin")
	quotaGBStr := r.FormValue("quota_gb")
	nfsExport := r.FormValue("nfs")

	if pool == "" || name == "" {
		http.Error(w, `{"error":"pool 和 name 必填"}`, http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(pool, "/data") {
		http.Error(w, `{"error":"pool 必须在 /data 下"}`, http.StatusBadRequest)
		return
	}
	if strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, `{"error":"名称不能包含 / 或 .."}`, http.StatusBadRequest)
		return
	}
	if permission == "" {
		permission = "readwrite"
	}

	quotaGB := 0
	if quotaGBStr != "" {
		var err error
		quotaGB, err = strconv.Atoi(quotaGBStr)
		if err != nil || quotaGB < 0 {
			http.Error(w, `{"error":"quota_gb 必须是非负整数"}`, http.StatusBadRequest)
			return
		}
	}

	folderPath := filepath.Join(pool, name)
	recycle := recycleBin == "yes"
	nfs := nfsExport == "yes"

	op := PendingOp{
		Action:     "create",
		FolderName: name,
		FolderPath: folderPath,
		Pool:       pool,
		Permission: permission,
		ValidUsers: validUsers,
		RecycleBin: recycle,
		SambaShare: permission != "noaccess",
		NFSExport:  nfs,
		QuotaGB:    quotaGB,
	}
	if err := executeCreateFolder(op); err != nil {
		common.JSONResponse(w, map[string]interface{}{"error": "创建文件夹失败: " + err.Error()})
		return
	}
	if err := SyncAllConfigs(); err != nil {
		common.JSONResponse(w, map[string]interface{}{"error": "配置同步失败: " + err.Error()})
		return
	}

	common.JSONResponse(w, map[string]interface{}{
		"message": fmt.Sprintf("文件夹 %s 已创建", name),
		"name":    name,
		"pool":    pool,
	})
	common.LogAudit("system", "创建共享文件夹", "STORAGE", "/api/disk/folders/create", fmt.Sprintf("%s -> %s (perm=%s)", name, folderPath, permission), "success", "")
}

// handleDeleteFolder deletes a shared folder (deferred to pending queue)
func handleDeleteFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	path := r.FormValue("path")
	confirm := r.FormValue("confirm")

	if path == "" {
		http.Error(w, `{"error":"path 必填"}`, http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(path, "/data/") {
		http.Error(w, `{"error":"只允许删除 /data/ 下的文件夹"}`, http.StatusBadRequest)
		return
	}
	if confirm != "yes" {
		http.Error(w, `{"error":"请加 confirm=yes 确认删除"}`, http.StatusBadRequest)
		return
	}

	common.JSONResponse(w, map[string]interface{}{
		"message": fmt.Sprintf("文件夹 %s 已加入待应用队列", filepath.Base(path)),
		"pending": true,
	})

	AddPendingOp("delete", filepath.Base(path), path, filepath.Dir(path), "", "", true, false, false, 0)
	common.LogAudit("system", "删除共享文件夹", "STORAGE", "/api/disk/folders/delete", fmt.Sprintf("%s -> %s", filepath.Base(path), path), "pending", "")
}

// handleFolderPermission updates folder permissions (deferred to pending queue)
func handleFolderPermission(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	path := r.FormValue("path")
	permission := r.FormValue("permission")
	validUsers := r.FormValue("valid_users")
	recycleBin := r.FormValue("recycle_bin")

	if path == "" {
		http.Error(w, `{"error":"path 必填"}`, http.StatusBadRequest)
		return
	}
	if permission == "" {
		permission = "readwrite"
	}

	shareName := filepath.Base(path)

	op := PendingOp{
		Action:     "update",
		FolderName: shareName,
		FolderPath: path,
		Pool:       filepath.Dir(path),
		Permission: permission,
		ValidUsers: validUsers,
		RecycleBin: recycleBin == "yes",
		SambaShare: permission != "noaccess",
	}
	if err := executeUpdateFolder(op); err != nil {
		common.JSONResponse(w, map[string]interface{}{"error": "更新权限失败: " + err.Error()})
		return
	}
	if err := SyncAllConfigs(); err != nil {
		common.JSONResponse(w, map[string]interface{}{"error": "配置同步失败: " + err.Error()})
		return
	}

	common.JSONResponse(w, map[string]interface{}{
		"message": fmt.Sprintf("共享 %s 权限已更新", shareName),
	})
	common.LogAudit("system", "更新共享文件夹", "STORAGE", "/api/disk/folders/permission", fmt.Sprintf("%s -> %s (perm=%s)", shareName, path, permission), "success", "")
}

// parseSambaShares returns map[sharePath]config map
func parseSambaShares(conf string) map[string]map[string]string {
	result := make(map[string]map[string]string)
	var currentName string
	var currentPath string

	for _, line := range strings.Split(conf, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			currentName = strings.Trim(trimmed, "[]")
			currentPath = ""
			continue
		}
		if currentName == "" {
			continue
		}
		if strings.Contains(trimmed, "=") {
			parts := strings.SplitN(trimmed, "=", 2)
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			lowerKey := strings.ToLower(key)

			if lowerKey == "path" {
				currentPath = val
				result[currentPath] = map[string]string{"name": currentName}
			}
			if currentPath != "" {
				if lowerKey == "read only" || lowerKey == "writable" {
					if lowerKey == "read only" {
						result[currentPath]["read_only"] = val
					} else if lowerKey == "writable" {
						if val == "yes" {
							result[currentPath]["read_only"] = "no"
						} else {
							result[currentPath]["read_only"] = "yes"
						}
					}
				}
				if lowerKey == "valid users" {
					result[currentPath]["valid_users"] = val
				}
				if lowerKey == "write list" {
					result[currentPath]["write_list"] = val
				}
			}
		}
	}
	return result
}

func parseDuSize(out string) string {
	fields := strings.Fields(out)
	if len(fields) >= 1 {
		return fields[0]
	}
	return ""
}

func removeSambaShare(conf, shareName string) string {
	lines := strings.Split(conf, "\n")
	var newLines []string
	skip := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if trimmed == "["+shareName+"]" {
				skip = true
				continue
			} else {
				skip = false
			}
		}
		if !skip {
			newLines = append(newLines, line)
		}
	}
	return strings.Join(newLines, "\n")
}
