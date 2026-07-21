package diskmgmt

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"nas-panel/common"
)

// QuotaManager handles XFS project quotas for shared folders
// Uses /etc/projects and /etc/projid to map folder paths to project IDs

const (
	quotaProjectsFile = "/etc/projects"
	quotaProjidFile   = "/etc/projid"
	quotaBaseID       = 1000 // start project IDs from 1000 to avoid system conflicts
)

// getNextProjectID finds the next available project ID
func getNextProjectID() int {
	data, err := os.ReadFile(quotaProjidFile)
	if err != nil {
		return quotaBaseID
	}
	maxID := quotaBaseID - 1
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Split(strings.TrimSpace(line), ":")
		if len(parts) >= 2 {
			if id, err := strconv.Atoi(parts[1]); err == nil && id > maxID {
				maxID = id
			}
		}
	}
	return maxID + 1
}

// projectName generates a project name from pool name and folder name
func projectName(poolName, folderName string) string {
	// Sanitize: only alphanumeric and underscore
	name := poolName + "_" + folderName
	var result []rune
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			result = append(result, r)
		}
	}
	if len(result) == 0 {
		return "folder_" + folderName
	}
	return string(result)
}

// setFolderQuota sets XFS project quota on a folder
// quotaGB: 0 = remove quota (unlimited), >0 = hard limit in GB
func setFolderQuota(mountPoint, folderPath, poolName, folderName string, quotaGB int) error {
	projName := projectName(poolName, folderName)

	if quotaGB <= 0 {
		// Remove quota: remove from projects and projid
		removeProjectEntry(projName)
		// Clear the project ID on the directory
		common.SudoExec("/usr/sbin/xfs_quota", "-x", "-c", fmt.Sprintf("project -C %s", projName), mountPoint)
		return nil
	}

	// Find or create project ID
	projID := findProjectID(projName)
	if projID < 0 {
		projID = getNextProjectID()
		// Add to /etc/projects: name:id:path
		entry := fmt.Sprintf("%s:%d:%s\n", projName, projID, folderPath)
		if err := appendToFile(quotaProjectsFile, entry); err != nil {
			return fmt.Errorf("写入 %s 失败: %v", quotaProjectsFile, err)
		}
		// Add to /etc/projid: name:id
		entry = fmt.Sprintf("%s:%d\n", projName, projID)
		if err := appendToFile(quotaProjidFile, entry); err != nil {
			return fmt.Errorf("写入 %s 失败: %v", quotaProjidFile, err)
		}
	} else {
		// Update path in /etc/projects if changed
		updateProjectPath(projName, projID, folderPath)
	}

	// Initialize project on the directory
	out, err := common.SudoExec("/usr/sbin/xfs_quota", "-x", "-c", fmt.Sprintf("project -s %s", projName), mountPoint)
	if err != nil {
		return fmt.Errorf("xfs_quota project 失败: %s: %v", out, err)
	}

	// Set hard limit (bhard = block hard limit, in KB)
	limitKB := quotaGB * 1024 * 1024
	out, err = common.SudoExec("/usr/sbin/xfs_quota", "-x", "-c",
		fmt.Sprintf("limit -p bhard=%dk %s", limitKB, projName), mountPoint)
	if err != nil {
		return fmt.Errorf("xfs_quota limit 失败: %s: %v", out, err)
	}

	return nil
}

// removeFolderQuota removes quota when a folder is deleted
func removeFolderQuota(poolName, folderName string) {
	projName := projectName(poolName, folderName)
	removeProjectEntry(projName)
}

// getFolderQuota reads current quota for a folder from xfs_quota report
func getFolderQuota(mountPoint, poolName, folderName string) (usedGB float64, limitGB int, err error) {
	projName := projectName(poolName, folderName)

	out, err := common.SudoOutput("/usr/sbin/xfs_quota", "-x", "-c", "report -p -N", mountPoint)
	if err != nil {
		return 0, 0, err
	}

	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[0] == projName {
			// fields: name, used(KB), soft, hard
			if used, err := strconv.ParseFloat(fields[1], 64); err == nil {
				usedGB = used / 1024 / 1024
			}
			if hard, err := strconv.Atoi(fields[3]); err == nil {
				limitGB = hard / 1024 / 1024
			}
			return usedGB, limitGB, nil
		}
	}
	return 0, 0, nil // no quota set
}

// findProjectID looks up project ID by name from /etc/projid
func findProjectID(name string) int {
	data, err := os.ReadFile(quotaProjidFile)
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Split(strings.TrimSpace(line), ":")
		if len(parts) >= 2 && parts[0] == name {
			if id, err := strconv.Atoi(parts[1]); err == nil {
				return id
			}
		}
	}
	return -1
}

// removeProjectEntry removes entries from /etc/projects and /etc/projid
func removeProjectEntry(name string) {
	// Remove from /etc/projects
	if data, err := os.ReadFile(quotaProjectsFile); err == nil {
		var lines []string
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), name+":") {
				lines = append(lines, line)
			}
		}
		common.SafeWriteFile(quotaProjectsFile, strings.Join(lines, "\n"))
	}
	// Remove from /etc/projid
	if data, err := os.ReadFile(quotaProjidFile); err == nil {
		var lines []string
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), name+":") {
				lines = append(lines, line)
			}
		}
		common.SafeWriteFile(quotaProjidFile, strings.Join(lines, "\n"))
	}
}

// updateProjectPath updates the path in /etc/projects for an existing project
func updateProjectPath(name string, id int, newPath string) {
	data, err := os.ReadFile(quotaProjectsFile)
	if err != nil {
		return
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Split(strings.TrimSpace(line), ":")
		if len(parts) >= 3 && parts[0] == name {
			lines = append(lines, fmt.Sprintf("%s:%d:%s", name, id, newPath))
		} else {
			lines = append(lines, line)
		}
	}
	common.SafeWriteFile(quotaProjectsFile, strings.Join(lines, "\n"))
}

// appendToFile appends a line to a file (creates if not exists)
func appendToFile(path, content string) error {
	existing, _ := os.ReadFile(path)
	newContent := string(existing)
	if newContent != "" && !strings.HasSuffix(newContent, "\n") {
		newContent += "\n"
	}
	newContent += content
	return common.SafeWriteFile(path, newContent)
}

// handleFolderQuota handles GET (read quota) and POST (set quota) for a folder
func handleFolderQuota(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		poolName := r.URL.Query().Get("pool")
		folderName := r.URL.Query().Get("folder")
		mountPoint := r.URL.Query().Get("mountpoint")
		if poolName == "" || folderName == "" || mountPoint == "" {
			http.Error(w, `{"error":"pool, folder, mountpoint required"}`, http.StatusBadRequest)
			return
		}

		usedGB, limitGB, err := getFolderQuota(mountPoint, poolName, folderName)
		if err != nil {
			common.JSONResponse(w, map[string]interface{}{
				"folder":    folderName,
				"used_gb":   0,
				"limit_gb":  0,
				"unlimited": true,
			})
			return
		}

		common.JSONResponse(w, map[string]interface{}{
			"folder":    folderName,
			"used_gb":   fmt.Sprintf("%.1f", usedGB),
			"limit_gb":  limitGB,
			"unlimited": limitGB == 0,
		})
		return
	}

	if r.Method == http.MethodPost {
		poolName := r.FormValue("pool")
		folderName := r.FormValue("folder")
		mountPoint := r.FormValue("mountpoint")
		folderPath := r.FormValue("path")
		quotaStr := r.FormValue("quota_gb")

		if poolName == "" || folderName == "" || mountPoint == "" || folderPath == "" {
			http.Error(w, `{"error":"pool, folder, mountpoint, path required"}`, http.StatusBadRequest)
			return
		}

		quotaGB := 0
		if quotaStr != "" {
			var err error
			quotaGB, err = strconv.Atoi(quotaStr)
			if err != nil || quotaGB < 0 {
				http.Error(w, `{"error":"quota_gb must be a non-negative integer (0=unlimited)"}`, http.StatusBadRequest)
				return
			}
		}

		if err := setFolderQuota(mountPoint, folderPath, poolName, folderName, quotaGB); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		msg := fmt.Sprintf("文件夹 %s 配额已设置为 %dGB", folderName, quotaGB)
		if quotaGB == 0 {
			msg = fmt.Sprintf("文件夹 %s 配额已取消（无限制）", folderName)
		}
		common.JSONResponse(w, map[string]interface{}{"message": msg})
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}
