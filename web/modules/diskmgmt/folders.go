package diskmgmt

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"nas-panel/common"
)

// SharedFolder represents a shared directory in a storage space
type SharedFolder struct {
	Name        string `json:"name"`          // photos (用户可见)
	Path       string `json:"path,omitempty"` // /data/nas1/photos (仅高级模式返回)
	Pool       string `json:"pool"`          // 存储空间1
	PoolName   string `json:"pool_name"`     // nas1 (内部)
	Size       string `json:"size"`
	SambaShare bool   `json:"samba_share"`
	Permission string `json:"permission"`    // readwrite, readonly, noaccess
	ValidUsers string `json:"valid_users"`
	RecycleBin bool   `json:"recycle_bin"`   // 回收站是否开启
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
		cmd := exec.Command("sudo", "tee", "-a", "/etc/samba/smb.conf")
		cmd.Stdin = strings.NewReader(smbConf)
		cmd.Run()
		// Create recycle bin directory if needed
		if recycleBin == "yes" {
			recyclePath := filepath.Join(folderPath, "#recycle")
			common.SudoExec("mkdir", "-p", recyclePath)
			common.SudoExec("chmod", "2777", recyclePath)
		}
		common.SudoExec("systemctl", "restart", "smbd")
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
			cmd := fmt.Sprintf("echo '%s' | sudo tee /etc/samba/smb.conf", newConf)
			common.SudoExec("bash", "-c", cmd)
			common.SudoExec("systemctl", "restart", "smbd")
		}
	}

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
			cmd := fmt.Sprintf("echo '%s' | sudo tee /etc/samba/smb.conf", newConf)
			common.SudoExec("bash", "-c", cmd)
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
	cmd := fmt.Sprintf("echo '%s' | sudo tee /etc/samba/smb.conf", content)
	common.SudoExec("bash", "-c", cmd)
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
