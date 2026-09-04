package users

import (
	"net/http"
	"strings"

	"nas-panel/common"
)

// PermissionMatrix 权限矩阵
type PermissionMatrix struct {
	Folders []string                     `json:"folders"` // 共享文件夹列表
	Users   []string                     `json:"users"`   // 用户列表
	Matrix  map[string]map[string]string `json:"matrix"`  // user -> folder -> permission
}

// handleMatrix 权限矩阵 API
func handleMatrix(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		matrix := buildPermissionMatrix()
		common.JSONResponse(w, matrix)
		return
	}

	if r.Method == http.MethodPut || r.Method == http.MethodPost {
		username := r.FormValue("username")
		folder := r.FormValue("folder")
		perm := r.FormValue("permission") // readwrite/readonly/noaccess

		if username == "" || folder == "" || perm == "" {
			http.Error(w, `{"error":"username, folder, permission required"}`, http.StatusBadRequest)
			return
		}

		if perm != "readwrite" && perm != "readonly" && perm != "noaccess" {
			http.Error(w, `{"error":"permission must be readwrite/readonly/noaccess"}`, http.StatusBadRequest)
			return
		}

		if err := setSharePermission(username, folder, perm); err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}

		common.JSONResponse(w, map[string]interface{}{
			"message": "权限已更新",
		})
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

// buildPermissionMatrix 构建权限矩阵
func buildPermissionMatrix() PermissionMatrix {
	// 获取所有共享文件夹
	folders := listSharedFolders()

	// 获取所有用户
	users := listAllUsers()

	// 解析 smb.conf 构建矩阵
	smbConf, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")
	matrix := make(map[string]map[string]string)

	for _, user := range users {
		matrix[user] = make(map[string]string)
		for _, folder := range folders {
			matrix[user][folder] = getUserFolderPermission(smbConf, user, folder)
		}
	}

	return PermissionMatrix{
		Folders: folders,
		Users:   users,
		Matrix:  matrix,
	}
}

// listSharedFolders 列出所有 Samba 共享文件夹（排除用户私有目录）
func listSharedFolders() []string {
	smbConf, err := common.SudoOutput("cat", "/etc/samba/smb.conf")
	if err != nil {
		return []string{}
	}

	var folders []string
	for _, line := range strings.Split(smbConf, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.Trim(line, "[]")
			// 排除特殊共享和用户私有目录
			if name != "global" && name != "homes" && name != "printers" &&
				!strings.HasPrefix(name, "private") && name != "print$" {
				// 检查是否是用户私有目录（通过 path 判断）
				folders = append(folders, name)
			}
		}
	}
	return folders
}

// listAllUsers 列出所有 NAS 用户（系统用户 ∩ Samba 用户）
func listAllUsers() []string {
	userSet := make(map[string]bool)

	// Samba 用户
	out, _ := common.SudoOutput("pdbedit", "-L")
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		userSet[parts[0]] = true
	}

	// FTP 用户
	data, _ := common.ExecOutput("cat", "/etc/vsftpd.userlist")
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			userSet[line] = true
		}
	}

	var users []string
	for u := range userSet {
		users = append(users, u)
	}
	return users
}

// getUserFolderPermission 获取用户对文件夹的权限
func getUserFolderPermission(smbConf, username, folder string) string {
	lines := strings.Split(smbConf, "\n")
	shareTag := "[" + folder + "]"
	inShare := false
	validUsers := ""
	writeList := ""
	readOnly := "no"

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == shareTag {
			inShare = true
			continue
		}
		if inShare && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			break
		}
		if !inShare {
			continue
		}

		if strings.HasPrefix(trimmed, "valid users") {
			parts := strings.SplitN(trimmed, "=", 2)
			if len(parts) == 2 {
				validUsers = strings.TrimSpace(parts[1])
			}
		}
		if strings.HasPrefix(trimmed, "write list") {
			parts := strings.SplitN(trimmed, "=", 2)
			if len(parts) == 2 {
				writeList = strings.TrimSpace(parts[1])
			}
		}
		if strings.HasPrefix(trimmed, "read only") {
			parts := strings.SplitN(trimmed, "=", 2)
			if len(parts) == 2 {
				readOnly = strings.TrimSpace(parts[1])
			}
		}
	}

	// 检查用户是否在 valid users 里
	if validUsers == "" {
		return "noaccess"
	}

	userInList := false
	for _, u := range strings.Split(validUsers, ",") {
		if strings.TrimSpace(u) == username {
			userInList = true
			break
		}
	}

	if !userInList {
		return "noaccess"
	}

	// 在 write list 里 → 读写（write list 覆盖 read only，见 Samba 手册）
	for _, u := range strings.Split(writeList, ",") {
		if strings.TrimSpace(u) == username {
			return "readwrite"
		}
	}

	if readOnly == "yes" {
		return "readonly"
	}
	return "readwrite"
}
