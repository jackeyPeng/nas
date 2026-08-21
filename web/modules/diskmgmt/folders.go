package diskmgmt

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"nas-panel/common"
)

// SharedFolder represents a shared directory in a storage volume
type SharedFolder struct {
	Name         string `json:"name"`          // photos (用户可见)
	Path         string `json:"path,omitempty"` // /data/nas1/photos (仅高级模式返回)
	Pool         string `json:"pool"`          // 存储池1
	PoolName     string `json:"pool_name"`     // nas1 (内部)
	Source       string `json:"source"`        // local, usb, remote_smb, remote_nfs, s3
	Owner        string `json:"owner,omitempty"`         // 文件夹属主
	Size         string `json:"size"`
	SambaShare   bool   `json:"samba_share"`             // SMB 协议开关
	NFSExport    bool   `json:"nfs_export,omitempty"`    // NFS 协议开关
	FTPAccess    bool   `json:"ftp_access,omitempty"`    // FTP 协议开关
	WebDAVAccess bool   `json:"webdav_access,omitempty"` // WebDAV 协议开关
	S3Access     bool   `json:"s3_access,omitempty"`     // S3 协议开关
	Permission   string `json:"permission"`             // readwrite, readonly, noaccess
	ValidUsers   string `json:"valid_users"`
	RecycleBin   bool   `json:"recycle_bin"`              // 回收站是否开启
	QuotaGB      int    `json:"quota_gb"`                 // 配额限制 (GB), 0=无限制
	QuotaUsed    string `json:"quota_used"`               // 已用配额
	Description  string `json:"description,omitempty"`   // 备注
}

// handleListFolders lists all shared folders under storage spaces
func handleListFolders(w http.ResponseWriter, r *http.Request) {
	var folders []SharedFolder

	// Get all data mount points
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
				folder := SharedFolder{
					Name:      name,
					Pool:      displayName,
					PoolName:  poolName,
				}
				folders = append(folders, folder)
			}
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			// Skip recycle bin directories
			if entry.Name() == "#recycle" {
				continue
			}
			folderPath := filepath.Join(mountPoint, entry.Name())
			folder := SharedFolder{
				Name:      entry.Name(),
				Path:      folderPath,
				Pool:      displayName,
				PoolName:  poolName,
			}
			// Get directory size
			sizeOut, _ := common.SudoOutput("du", "-sh", folderPath)
			folder.Size = parseDuSize(sizeOut)
			folders = append(folders, folder)
		}
	}

	// Parse Samba config to check shares
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
			// Check for recycle bin in Samba config
			folders[i].RecycleBin = hasRecycleBin(smbConf, f.Name)
		} else {
			folders[i].Permission = "noaccess"
		}
	}

	// Batch query quota for all mount points (avoid N×M xfs_quota calls)
	quotaMap := make(map[string]map[string][2]string) // mountPoint -> projName -> [usedGB, limitGB]
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

	// Apply quota info to folders
	for i, f := range folders {
		// Find the mount point for this folder
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

	common.JSONResponse(w, map[string]interface{}{
		"folders": folders,
	})
}

// hasRecycleBin checks if a Samba share has recycle bin configured
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
		if inShare {
			lower := strings.ToLower(trimmed)
			if strings.Contains(lower, "recycle") {
				return true
			}
		}
	}
	return false
}

// handleCreateFolder creates a shared folder under a storage space
func handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	pool := r.FormValue("pool")         // /data/nas1 (mountpoint)
	name := r.FormValue("name")        // photos
	permission := r.FormValue("permission") // readwrite, readonly, noaccess
	validUsers := r.FormValue("valid_users")
	recycleBin := r.FormValue("recycle_bin") // yes/no
	quotaGBStr := r.FormValue("quota_gb")   // optional, GB, 0=unlimited

	nfsExport := r.FormValue("nfs") // yes/no

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

	// Parse quota
	quotaGB := 0
	if quotaGBStr != "" {
		var err error
		quotaGB, err = strconv.Atoi(quotaGBStr)
		if err != nil || quotaGB < 0 {
			http.Error(w, `{"error":"quota_gb 必须是非负整数（0=无限制）"}`, http.StatusBadRequest)
			return
		}
	}

	folderPath := filepath.Join(pool, name)

	// Create directory
	common.SudoExec("mkdir", "-p", folderPath)

	nasUser, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
	if nasUser == "" {
		nasUser = "root"
	}
	common.SudoExec("chown", "-R", nasUser+":"+nasUser, folderPath)

	// Add Samba share if permission is not noaccess
	if permission != "noaccess" {
		writeMode := "writable = yes"
		if permission == "readonly" {
			writeMode = "read only = yes"
		}
		users := validUsers
		if users == "" {
			users = nasUser
		}
		var smbConf string
		if recycleBin == "yes" {
			smbConf = fmt.Sprintf(`
[%s]
   path = %s
   browseable = yes
   %s
   valid users = %s
   vfs objects = recycle
   recycle:repository = #recycle
   recycle:keeptree = yes
   recycle:versions = yes
   recycle:touch = yes
`, name, folderPath, writeMode, users)
		} else {
			smbConf = fmt.Sprintf(`
[%s]
   path = %s
   browseable = yes
   %s
   valid users = %s
`, name, folderPath, writeMode, users)
		}
		common.SafeAppendFile("/etc/samba/smb.conf", smbConf)
		// Create recycle bin directory if needed
		if recycleBin == "yes" {
			recyclePath := filepath.Join(folderPath, "#recycle")
			common.SudoExec("mkdir", "-p", recyclePath)
			common.SudoExec("chmod", "2777", recyclePath)
		}
		common.SudoExec("systemctl", "restart", "smbd")
	}

	// Add NFS export if requested
	if nfsExport == "yes" {
		nfsOpts := "rw,sync,no_subtree_check,no_root_squash"
		if permission == "readonly" {
			nfsOpts = "ro,sync,no_subtree_check"
		}
		exportLine := fmt.Sprintf("%s 192.168.0.0/16(%s)\n", folderPath, nfsOpts)
		common.SafeAppendFile("/etc/exports", exportLine)
		common.SudoExec("exportfs", "-a")
	}

	// Set quota if requested (non-fatal: log warning if it fails)
	if quotaGB > 0 {
		poolName := shareNameFromMount(pool)
		if err := setFolderQuota(pool, folderPath, poolName, name, quotaGB); err != nil {
			log.Printf("[WARN] 文件夹 %s 配额设置失败: %v", name, err)
			common.JSONResponse(w, map[string]interface{}{
				"message": fmt.Sprintf("文件夹 %s 已创建（配额设置失败: %v，请检查 XFS 是否启用 prjquota）", name, err),
				"name":    name,
				"pool":    pool,
				"warning": fmt.Sprintf("配额设置失败: %v", err),
			})
			return
		}
	}

	common.JSONResponse(w, map[string]interface{}{
		"message": fmt.Sprintf("文件夹 %s 已创建", name),
		"name":    name,
		"pool":    pool,
	})
}

// handleDeleteFolder deletes a shared folder
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
		http.Error(w, `{"error":"请加 confirm=yes 确认删除（数据不可恢复）"}`, http.StatusBadRequest)
		return
	}

	// Remove Samba share if exists
	smbConf, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")
	if smbConf != "" {
		newConf := removeSambaShare(smbConf, filepath.Base(path))
		if newConf != smbConf {
			common.SafeWriteFile("/etc/samba/smb.conf", newConf)
			common.SudoExec("systemctl", "restart", "smbd")
		}
	}

	// Remove NFS export if exists — precise match on first field
	exportsData, _ := common.SudoOutput("cat", "/etc/exports")
	if exportsData != "" {
		var newLines []string
		for _, line := range strings.Split(exportsData, "\n") {
			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) >= 1 && fields[0] == path {
				continue // skip this export
			}
			newLines = append(newLines, line)
		}
		newExports := strings.Join(newLines, "\n")
		if newExports != exportsData {
			common.SafeWriteFile("/etc/exports", newExports)
			common.SudoExec("exportfs", "-a")
		}
	}

	// Remove quota if exists
	poolName := shareNameFromMount(filepath.Dir(path))
	removeFolderQuota(poolName, filepath.Base(path))

	// Delete directory
	common.SudoExec("rm", "-rf", path)

	common.JSONResponse(w, map[string]interface{}{
		"message": fmt.Sprintf("文件夹已删除"),
	})
}

// handleFolderPermission updates Samba permissions for a folder
func handleFolderPermission(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	path := r.FormValue("path")
	permission := r.FormValue("permission") // readwrite, readonly, noaccess
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
	smbConf, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")
	if smbConf == "" {
		http.Error(w, `{"error":"无法读取 Samba 配置"}`, http.StatusInternalServerError)
		return
	}

	// If noaccess, remove the share entirely
	if permission == "noaccess" {
		newConf := removeSambaShare(smbConf, shareName)
		if newConf != smbConf {
			common.SafeWriteFile("/etc/samba/smb.conf", newConf)
			common.SudoExec("systemctl", "restart", "smbd")
		}
		common.JSONResponse(w, map[string]interface{}{
			"message": fmt.Sprintf("共享 %s 已关闭", shareName),
		})
		return
	}

	// Rebuild config with updated share
	lines := strings.Split(smbConf, "\n")
	var newLines []string
	inShare := false
	found := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "["+shareName+"]" {
			inShare = true
			found = true
			newLines = append(newLines, line)
			continue
		}
		if inShare && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inShare = false
		}
		if inShare {
			lower := strings.ToLower(trimmed)
			if strings.HasPrefix(lower, "writable") || strings.HasPrefix(lower, "read only") {
				if permission == "readonly" {
					newLines = append(newLines, "   read only = yes")
				} else {
					newLines = append(newLines, "   writable = yes")
				}
				continue
			}
			if strings.HasPrefix(lower, "valid users") {
				if validUsers != "" {
					newLines = append(newLines, "   valid users = "+validUsers)
				}
				continue
			}
			if strings.HasPrefix(lower, "path") {
				newLines = append(newLines, line)
				continue
			}
			// Skip old recycle/vfs lines, will re-add below
			if strings.HasPrefix(lower, "vfs") || strings.Contains(lower, "recycle") {
				continue
			}
			continue
		}
		newLines = append(newLines, line)
	}

	// If not found, add new share
	if !found {
		nasUser, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
		if nasUser == "" {
			nasUser = "root"
		}
		writeMode := "writable = yes"
		if permission == "readonly" {
			writeMode = "read only = yes"
		}
		users := validUsers
		if users == "" {
			users = nasUser
		}
		newShare := fmt.Sprintf("\n[%s]\n   path = %s\n   browseable = yes\n   %s\n   valid users = %s\n",
			shareName, path, writeMode, users)
		newLines = append(newLines, newShare)
	}

	// Add recycle bin if requested
	if recycleBin == "yes" {
		// Find the share section and append vfs objects
		for i, line := range newLines {
			if strings.TrimSpace(line) == "["+shareName+"]" {
				// Find end of this share (next [section] or EOF)
				insertAt := i + 1
				for j := i + 1; j < len(newLines); j++ {
					trimmed := strings.TrimSpace(newLines[j])
					if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
						insertAt = j
						break
					}
					insertAt = j + 1
				}
				recycleLines := []string{
					"   vfs objects = recycle",
					"   recycle:repository = #recycle",
					"   recycle:keeptree = yes",
					"   recycle:versions = yes",
					"   recycle:touch = yes",
				}
				// Insert before next section
				newLines = append(newLines[:insertAt], append(recycleLines, newLines[insertAt:]...)...)
				break
			}
		}
		// Create recycle directory
		recyclePath := filepath.Join(path, "#recycle")
		common.SudoExec("mkdir", "-p", recyclePath)
		common.SudoExec("chmod", "2777", recyclePath)
	}

	content := strings.Join(newLines, "\n")
	common.SafeWriteFile("/etc/samba/smb.conf", content)
	common.SudoExec("systemctl", "restart", "smbd")

	common.JSONResponse(w, map[string]interface{}{
		"message": fmt.Sprintf("共享 %s 权限已更新", shareName),
	})
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
			}
		}
	}
	return result
}

// parseDuSize extracts size from "du -sh" output
func parseDuSize(out string) string {
	fields := strings.Fields(out)
	if len(fields) >= 1 {
		return fields[0]
	}
	return ""
}

// removeSambaShare removes a share section from smb.conf content
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
