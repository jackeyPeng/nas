package users

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"nas-panel/common"
)

// NASUser 用户完整信息（列表 API 返回）
type NASUser struct {
	Username   string            `json:"username"`
	Services   map[string]bool   `json:"services"`    // samba/ftp/webdav/nfs
	PrivateDir string            `json:"private_dir"` // /data/private/xxx
	PrivateUsed string           `json:"private_used"` // 已用容量，如 "1.2G"
	QuotaGB    int               `json:"quota_gb"`    // 私有目录配额，0=无限制
	QuotaUsed  string            `json:"quota_used"`  // 配额已用
	ShareCount int               `json:"share_count"` // 有权限的共享文件夹数
	Groups     []string          `json:"groups"`      // 所属组
	CreatedAt  string            `json:"created_at"`  // 创建时间（私有目录 ctime）
}

// getUsers 返回增强版用户列表：系统用户 ∩ NAS 服务用户
func getUsers() []NASUser {
	userMap := map[string]*NASUser{}

	// Include all Linux users (UID >= 1000, filter system accounts)
	if data, err := common.ExecOutput("cat", "/etc/passwd"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
			parts := strings.Split(line, ":")
			if len(parts) >= 4 {
				uid := parts[2]
				// Skip system users (UID < 1000 or specific system accounts)
				if uidInt, err := strconv.Atoi(uid); err == nil && uidInt < 1000 {
					continue
				}
				name := parts[0]
				// Skip common system-ish accounts
				if name == "nobody" || name == "nogroup" {
					continue
				}
				getOrCreate(userMap, name)
			}
		}
	}

	// Samba 用户（pdbedit）
	out, err := common.SudoOutput("pdbedit", "-L")
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			name := parts[0]
			u := getOrCreate(userMap, name)
			u.Services["samba"] = true
		}
	}

	// FTP 用户（vsftpd userlist）
	if data, err := common.ExecOutput("cat", "/etc/vsftpd.userlist"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
			name := strings.TrimSpace(line)
			if name == "" {
				continue
			}
			u := getOrCreate(userMap, name)
			u.Services["ftp"] = true
		}
	}

	// WebDAV 用户（rclone htpasswd）
	if data, err := common.SudoOutput("cat", "/etc/rclone-htpasswd"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			u := getOrCreate(userMap, parts[0])
			u.Services["webdav"] = true
		}
	}

	// 补充每个用户的附加信息
	smbConf, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")
	shareCount := countSharesPerUser(smbConf)

	for name, u := range userMap {
		// 私有目录 + 容量
		privDir := "/data/private/" + name
		if dirExists(privDir) {
			u.PrivateDir = privDir
			u.PrivateUsed = dirSize(privDir)
		}
		// 组信息
		u.Groups = userGroups(name)
		// 共享文件夹数量
		u.ShareCount = shareCount[name]
		// 创建时间（用私有目录的创建时间近似）
		if u.PrivateDir != "" {
			u.CreatedAt = dirCtime(privDir)
		}
		// 配额（查私有目录的 project quota）
		usedGB, limitGB := privateDirQuota(name)
		u.QuotaGB = limitGB
		if limitGB > 0 {
			u.QuotaUsed = fmt.Sprintf("%.1fG", usedGB)
		}
	}

	// 转 slice
	users := make([]NASUser, 0, len(userMap))
	for _, u := range userMap {
		users = append(users, *u)
	}
	return users
}

func getOrCreate(m map[string]*NASUser, name string) *NASUser {
	if u, ok := m[name]; ok {
		return u
	}
	u := &NASUser{
		Username: name,
		Services: map[string]bool{},
		Groups:   []string{},
	}
	m[name] = u
	return u
}

func dirExists(path string) bool {
	out, err := common.SudoOutput("test", "-d", path)
	_ = out
	return err == nil
}

func dirSize(path string) string {
	out, err := common.SudoOutput("du", "-sh", path)
	if err != nil {
		return "-"
	}
	fields := strings.Fields(out)
	if len(fields) >= 1 {
		return fields[0]
	}
	return "-"
}

func dirCtime(path string) string {
	out, err := common.SudoOutput("stat", "-c", "%y", path)
	if err != nil {
		return ""
	}
	fields := strings.Fields(out)
	if len(fields) >= 1 {
		return fields[0]
	}
	return ""
}

func userGroups(username string) []string {
	out, err := common.ExecOutput("id", "-nG", username)
	if err != nil {
		return []string{}
	}
	groups := strings.Fields(out)
	// 过滤掉和用户同名的主组，减少噪音
	var result []string
	for _, g := range groups {
		if g != username {
			result = append(result, g)
		}
	}
	return result
}

// countSharesPerUser 解析 smb.conf，统计每个用户在多少个共享的 valid_users 里
func countSharesPerUser(conf string) map[string]int {
	counts := map[string]int{}
	currentShare := ""
	for _, line := range strings.Split(conf, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentShare = line
			continue
		}
		if strings.HasPrefix(line, "valid users") && currentShare != "" {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			for _, u := range strings.Split(parts[1], ",") {
				u = strings.TrimSpace(u)
				if u != "" && !strings.HasPrefix(u, "@") {
					counts[u]++
				}
			}
		}
	}
	return counts
}

// --- HTTP handlers ---

func handleUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		users := getUsers()
		common.JSONResponse(w, map[string]interface{}{"users": users})
		return
	}

	if r.Method == http.MethodPost {
		handleCreateUser(w, r)
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

func handleUserAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/users/")
	parts := strings.Split(path, "/")
	if len(parts) < 1 || parts[0] == "" {
		http.Error(w, `{"error":"invalid path"}`, http.StatusBadRequest)
		return
	}
	username := parts[0]

	// PUT /api/users/{name}/password
	if len(parts) >= 2 && parts[1] == "password" {
		if r.Method != http.MethodPut && r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		password := r.FormValue("password")
		if password == "" || len(password) < 12 {
			http.Error(w, `{"error":"密码至少12位"}`, http.StatusBadRequest)
			return
		}
		if err := changePassword(username, password); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		common.JSONResponse(w, map[string]interface{}{"message": "密码修改成功"})
		return
	}

	// PUT /api/users/{name}/services — 服务开关
	if len(parts) >= 2 && parts[1] == "services" {
		if r.Method != http.MethodPut && r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		services := map[string]bool{}
		for _, svc := range []string{"samba", "ftp", "webdav"} {
			services[svc] = r.FormValue(svc) == "true"
		}
		if err := updateUserServices(username, services); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		common.JSONResponse(w, map[string]interface{}{"message": "服务权限已更新"})
		return
	}

	// GET/PUT /api/users/{name}/quota — 私有目录配额
	if len(parts) >= 2 && parts[1] == "quota" {
		if r.Method == http.MethodGet {
			usedGB, limitGB := privateDirQuota(username)
			common.JSONResponse(w, map[string]interface{}{
				"username": username,
				"used_gb":  usedGB,
				"limit_gb": limitGB,
			})
			return
		}
		if r.Method != http.MethodPut && r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		quotaStr := r.FormValue("quota_gb")
		quotaGB, err := strconv.Atoi(quotaStr)
		if err != nil || quotaGB < 0 {
			http.Error(w, `{"error":"quota_gb 必须是非负整数(0=无限制)"}`, http.StatusBadRequest)
			return
		}
		if err := setPrivateDirQuota(username, quotaGB); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		msg := fmt.Sprintf("用户 %s 私有目录配额已设置为 %dGB", username, quotaGB)
		if quotaGB == 0 {
			msg = fmt.Sprintf("用户 %s 私有目录配额已取消", username)
		}
		common.JSONResponse(w, map[string]interface{}{"message": msg})
		return
	}

	// DELETE /api/users/{name}
	if r.Method == http.MethodDelete {
		deleteData := r.URL.Query().Get("delete_data") == "true"
		out, err := removeUser(username, deleteData)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		_ = out
		common.JSONResponse(w, map[string]interface{}{"message": "用户 " + username + " 已删除"})
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

// changePassword 修改系统 + Samba + WebDAV 密码
func changePassword(username, password string) error {
	cred := username + ":" + password
	out, err := common.SudoExec("bash", "-c", fmt.Sprintf("printf '%%s\n' %s | chpasswd", shellQuote(cred)))
	if err != nil {
		return fmt.Errorf("chpasswd 失败: %s: %v", out, err)
	}

	out, err = common.SudoExec("bash", "-c", fmt.Sprintf(
		"printf '%%s\n%%s\n' %s %s | smbpasswd -a %s -s",
		shellQuote(password), shellQuote(password), shellQuote(username),
	))
	if err != nil {
		return fmt.Errorf("smbpasswd 失败: %s: %v", out, err)
	}

	common.SudoExec("htpasswd", "-b", "/etc/rclone-htpasswd", username, password)

	// Update .env NAS_PASS (read by panel on startup)
	common.SudoExec("sed", "-i", fmt.Sprintf("s/^NAS_PASS=.*/NAS_PASS=%s/", password),
		common.GetEnvFilePath())

	// Update FileBrowser password
	common.SudoExec("filebrowser", "users", "update", username,
		"--password", password,
		"--database", "/etc/filebrowser/filebrowser.db")

	// Update in-memory panel password (takes effect immediately, no restart)
	common.UpdateNasPass(password)

	return nil
}

func removeUser(username string, deleteData bool) (string, error) {
	args := []string{username}
	if deleteData {
		args = append(args, "--delete-data")
	}
	out, err := common.SudoExec("/opt/nas/scripts/remove-user.sh", args...)
	if err != nil {
		return out, fmt.Errorf("%s: %v", out, err)
	}
	return out, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// RegisterRoutes 注册用户模块路由
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/users", common.AuthMiddleware(handleUsers))
	mux.HandleFunc("/api/users/", common.AuthMiddleware(handleUserAction))
	mux.HandleFunc("/api/user-groups", common.AuthMiddleware(handleGroups))
	mux.HandleFunc("/api/user-groups/", common.AuthMiddleware(handleGroupAction))
	mux.HandleFunc("/api/users-matrix", common.AuthMiddleware(handleMatrix))
	mux.HandleFunc("/api/users-login-log", common.AuthMiddleware(handleLoginLog))
}
