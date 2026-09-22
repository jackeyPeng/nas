package users

import (
	"net/http"
	"sort"
	"strings"

	"nas-panel/common"
	"nas-panel/modules/diskmgmt"
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

// buildPermissionMatrix 构建权限矩阵。
// 事实源 = folders.db（GetAllFolderMeta），不再解析 smb.conf（生成物）：
// SyncAllConfigs 失败/滞后时旧实现会与 DB 漂移（storage-permission-model §八 #4）。
func buildPermissionMatrix() PermissionMatrix {
	metas := diskmgmt.GetAllFolderMeta()
	folders := listSharedFolders(metas)
	users := listAllUsers()

	matrix := make(map[string]map[string]string)
	for _, user := range users {
		matrix[user] = make(map[string]string)
		for _, f := range folders {
			if m := findMetaByName(metas, f); m != nil {
				matrix[user][f] = FolderMetaUserPermission(*m, user)
			} else {
				matrix[user][f] = "noaccess"
			}
		}
	}

	return PermissionMatrix{
		Folders: folders,
		Users:   users,
		Matrix:  matrix,
	}
}

func findMetaByName(metas []diskmgmt.FolderMeta, name string) *diskmgmt.FolderMeta {
	for i := range metas {
		if metas[i].Name == name {
			return &metas[i]
		}
	}
	return nil
}

// listSharedFolders 列出所有 Samba 共享文件夹（排除特殊共享）
func listSharedFolders(metas []diskmgmt.FolderMeta) []string {
	var folders []string
	for _, m := range metas {
		if !m.SambaShare {
			continue
		}
		// 排除特殊共享（旧种子数据残留防御；folders.db 正常不会有这些）
		if m.Name == "global" || m.Name == "homes" || m.Name == "printers" ||
			strings.HasPrefix(m.Name, "private") || m.Name == "print$" {
			continue
		}
		folders = append(folders, m.Name)
	}
	sort.Strings(folders)
	return folders
}

// FolderMetaUserPermission 按 Samba 语义从 folders.db 元数据计算单用户权限。
// 判定规则（与 GenerateSambaConfig/smbShareParams 的生成语义一一对应）：
//   - !samba_share 或 permission=noaccess → noaccess
//   - 开放共享（valid_users 与 write_users 双空，如 public）→ 文件夹级 permission
//     决定读写（writable=yes / read only=yes），所有认证用户同权
//   - write_users 命中 → readwrite（write list 覆盖 read only，见 Samba 手册）
//   - valid_users 命中 → readonly；但 write_users 为空（旧数据未物化）时
//     回退文件夹级 permission（writable=yes 仍放行写）
//   - 两个列表都未命中 → noaccess
func FolderMetaUserPermission(m diskmgmt.FolderMeta, username string) string {
	if !m.SambaShare || m.Permission == "noaccess" {
		return "noaccess"
	}

	valid := splitUserList(m.ValidUsers)
	write := splitUserList(m.WriteUsers)

	// 开放共享：任何可认证用户按文件夹级 permission 读写
	if len(valid) == 0 && len(write) == 0 {
		if m.Permission == "readonly" {
			return "readonly"
		}
		return "readwrite"
	}

	if listHasStr(write, username) {
		return "readwrite"
	}
	if listHasStr(valid, username) {
		if len(write) == 0 && m.Permission == "readwrite" {
			return "readwrite"
		}
		return "readonly"
	}
	return "noaccess"
}

func listHasStr(list []string, u string) bool {
	for _, x := range list {
		if x == u {
			return true
		}
	}
	return false
}

func splitUserList(s string) []string {
	var out []string
	for _, u := range strings.Split(s, ",") {
		if u = strings.TrimSpace(u); u != "" {
			out = append(out, u)
		}
	}
	return out
}

// listAllUsers 列出所有 NAS 用户（Samba ∪ FTP）
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
	sort.Strings(users)
	return users
}
