package users

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"nas-panel/common"
)

// NASUser 用户完整信息（列表 API 返回）
type NASUser struct {
	Username    string          `json:"username"`
	Services    map[string]bool `json:"services"`               // samba/ftp/webdav/nfs
	PrivateDir  string          `json:"private_dir"`            // /data/nas1/xxx（用户主目录）
	PrivateUsed string          `json:"private_used"`           // 已用容量，如 "1.2G"
	QuotaGB     int             `json:"quota_gb"`               // 私有目录配额，0=无限制
	QuotaUsed   string          `json:"quota_used"`             // 配额已用
	QuotaStatus string          `json:"quota_status,omitempty"` // ok/unsupported/error/none（加固轮1 #4）
	QuotaReason string          `json:"quota_reason,omitempty"` // 非 ok 时的原因
	ShareCount  int             `json:"share_count"`            // 有权限的共享文件夹数
	Groups      []string        `json:"groups"`                 // 所属组
	CreatedAt   string          `json:"created_at"`             // 创建时间（私有目录 ctime）
}

// isShareIdentity 判定「共享影子身份」：diskmgmt 创建共享文件夹时会
// useradd -M -s /usr/sbin/nologin -g <同名组> 建占位用户（目录属主/force group 用，
// 见 pending_ops.go），它不是可登录可管理的真实用户，不应出现在用户页。
// 特征 = nologin 类 shell + 主组与用户名同名；面板真实用户一律 /bin/bash（create.go）。
func isShareIdentity(name, shell, primaryGroup string) bool {
	switch shell {
	case "/usr/sbin/nologin", "/usr/bin/nologin", "/bin/false":
		return primaryGroup != "" && primaryGroup == name
	}
	return false
}

// gidToGroupName 读取 /etc/group 建 gid→组名映射。
func gidToGroupName() map[string]string {
	m := map[string]string{}
	if data, err := common.ExecOutput("cat", "/etc/group"); err == nil {
		for _, line := range strings.Split(data, "\n") {
			parts := strings.Split(strings.TrimSpace(line), ":")
			if len(parts) >= 3 {
				m[parts[2]] = parts[0]
			}
		}
	}
	return m
}

// shareIdentityNames 返回全部共享影子身份用户名集合。
func shareIdentityNames() map[string]bool {
	res := map[string]bool{}
	gids := gidToGroupName()
	if data, err := common.ExecOutput("cat", "/etc/passwd"); err == nil {
		for _, line := range strings.Split(data, "\n") {
			parts := strings.Split(strings.TrimSpace(line), ":")
			if len(parts) >= 7 && isShareIdentity(parts[0], parts[6], gids[parts[3]]) {
				res[parts[0]] = true
			}
		}
	}
	return res
}

// getUsers 返回增强版用户列表：系统用户 ∩ NAS 服务用户（共享影子身份除外）
func getUsers() []NASUser {
	userMap := map[string]*NASUser{}
	gids := gidToGroupName()

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
				// Skip 共享影子身份（建共享时的占位用户，非真实用户）
				if len(parts) >= 7 && isShareIdentity(name, parts[6], gids[parts[3]]) {
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
		// 主目录 + 容量
		privDir := "/data/nas1/" + name
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
		usedGB, limitGB, qStatus, qReason := privateDirQuota(name)
		u.QuotaGB = limitGB
		u.QuotaStatus = qStatus
		u.QuotaReason = qReason
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
	size, err := common.CachedDirSize(path, 60*time.Second)
	if err != nil {
		return "-"
	}
	return size
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
		if err := common.ValidSecret(password); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
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
		services := parseServiceToggles(r)
		if len(services) == 0 {
			http.Error(w, `{"error":"至少提供一个服务字段 (samba/ftp/webdav=true|false)"}`, http.StatusBadRequest)
			return
		}
		if err := updateUserServices(username, services); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
			return
		}
		common.JSONResponse(w, map[string]interface{}{"message": "服务权限已更新"})
		return
	}

	// GET/PUT /api/users/{name}/quota — 私有目录配额
	if len(parts) >= 2 && parts[1] == "quota" {
		if r.Method == http.MethodGet {
			usedGB, limitGB, qStatus, qReason := privateDirQuota(username)
			common.JSONResponse(w, map[string]interface{}{
				"username":     username,
				"used_gb":      usedGB,
				"limit_gb":     limitGB,
				"quota_status": qStatus,
				"reason":       qReason,
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
	out, err := common.SudoExecStdin(username+":"+password+"\n", "chpasswd")
	if err != nil {
		return fmt.Errorf("chpasswd 失败: %s: %v", out, err)
	}

	out, err = common.SudoExecStdin(password+"\n"+password+"\n", "smbpasswd", "-a", username, "-s")
	if err != nil {
		return fmt.Errorf("smbpasswd 失败: %s: %v", out, err)
	}

	common.SudoExec("htpasswd", "-b", "/etc/rclone-htpasswd", username, password)

	// 仅面板登录用户（NAS_USER）的密码才是 NAS_PASS：改其他用户的密码
	// 绝不能改写 .env 的 NAS_PASS 或内存面板密码，否则管理员会被顶下线
	if username == common.GetNASUser() {
		// Update .env NAS_PASS (read by panel on startup)
		if envPath := common.GetEnvFilePath(); envPath != "" {
			if content, ok := readFileWithFallback(envPath); ok {
				common.SafeWriteFile(envPath, updateNasPassLine(content, password))
			}
		}

		// Update rclone-s3 auth-key (ExecStart embeds access_key,secret literally)
		s3Key := common.S3AccessKey(username)
		const unitFile = "/etc/systemd/system/rclone-s3.service"
		if content, ok := readFileWithFallback(unitFile); ok {
			common.SafeWriteFile(unitFile, updateRcloneS3AuthKey(content, s3Key, password))
		}

		const s3EnvFile = "/etc/rclone/s3-env"
		if _, err := os.Stat(s3EnvFile); err == nil {
			if content, ok := readFileWithFallback(s3EnvFile); ok {
				common.SafeWriteFile(s3EnvFile, updateRcloneS3EnvFile(content, s3Key, password))
			}
		}
		common.SudoExec("systemctl", "daemon-reload")
		common.SudoExec("systemctl", "restart", "rclone-s3")

		// Update in-memory panel password (takes effect immediately, no restart)
		common.UpdateNasPass(password)
	}

	return nil
}

// readFileWithFallback 读取文件内容：先尝试当前进程权限直读，失败则走 sudo cat。
func readFileWithFallback(path string) (string, bool) {
	if data, err := os.ReadFile(path); err == nil {
		return string(data), true
	}
	out, err := common.SudoOutput("cat", path)
	if err != nil {
		return "", false
	}
	return out, true
}

// updateNasPassLine 替换 .env 中 NAS_PASS= 整行，其余行原样保留。
// 纯函数，替代原来的 sed -i 拼接（密码含 sed 特殊字符时会命令注入/语法损坏）。
func updateNasPassLine(content, password string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "NAS_PASS=") {
			lines[i] = "NAS_PASS=" + password
		}
	}
	return strings.Join(lines, "\n")
}

// updateRcloneS3AuthKey 在 rclone-s3.service 的 ExecStart 行中，把 --auth-key
// 及其后一个 token（旧的 access_key,secret）替换为新值。纯函数，替代
// sed -i -E 's|--auth-key [^ ]+|...|' 拼接。
func updateRcloneS3AuthKey(content, accessKey, secret string) string {
	newValue := fmt.Sprintf("%s,%s", accessKey, secret)
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "--auth-key") {
			continue
		}
		fields := strings.Fields(line)
		for j := 0; j < len(fields); j++ {
			if fields[j] == "--auth-key" && j+1 < len(fields) {
				fields[j+1] = newValue
				break
			}
		}
		lines[i] = strings.Join(fields, " ")
	}
	return strings.Join(lines, "\n")
}

// updateRcloneS3EnvFile 替换 /etc/rclone/s3-env 中的 RCLONE_S3_SECRET_KEY=
// 和 RCLONE_S3_ACCESS_KEY= 两行，其余行原样保留。纯函数，替代
// sed -i -E 's|^RCLONE_S3_SECRET_KEY=.*|...|;s|^RCLONE_S3_ACCESS_KEY=.*|...|' 拼接。
func updateRcloneS3EnvFile(content, accessKey, secret string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "RCLONE_S3_SECRET_KEY="):
			lines[i] = "RCLONE_S3_SECRET_KEY=" + secret
		case strings.HasPrefix(line, "RCLONE_S3_ACCESS_KEY="):
			lines[i] = "RCLONE_S3_ACCESS_KEY=" + accessKey
		}
	}
	return strings.Join(lines, "\n")
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

// RegisterRoutes 注册用户模块路由
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/users", common.AuthMiddleware(handleUsers))
	mux.HandleFunc("/api/users/", common.AuthMiddleware(handleUserAction))
	mux.HandleFunc("/api/user-groups", common.AuthMiddleware(handleGroups))
	mux.HandleFunc("/api/user-groups/", common.AuthMiddleware(handleGroupAction))
	mux.HandleFunc("/api/users-matrix", common.AuthMiddleware(handleMatrix))
	mux.HandleFunc("/api/users-login-log", common.AuthMiddleware(handleLoginLog))
}
