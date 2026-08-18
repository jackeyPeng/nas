package users

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"nas-panel/common"
)

// CreateUserRequest 向导创建用户的请求参数
type CreateUserRequest struct {
	Username   string
	Password   string
	Services   map[string]bool // samba/ftp/webdav
	QuotaGB    int             // 私有目录配额，0=无限制
	SharePerms map[string]string // 共享文件夹 -> 权限 (readwrite/readonly/noaccess)
}

// handleCreateUser 向导式创建用户
func handleCreateUser(w http.ResponseWriter, r *http.Request) {
	req := CreateUserRequest{
		Username:   strings.TrimSpace(r.FormValue("username")),
		Password:   r.FormValue("password"),
		Services:   map[string]bool{},
		SharePerms: map[string]string{},
	}

	// 校验用户名
	if req.Username == "" {
		http.Error(w, `{"error":"用户名不能为空"}`, http.StatusBadRequest)
		return
	}
	if !isValidUsername(req.Username) {
		http.Error(w, `{"error":"用户名只能包含小写字母、数字、下划线和短横线，且以字母开头"}`, http.StatusBadRequest)
		return
	}

	// 校验密码
	if len(req.Password) < 12 {
		http.Error(w, `{"error":"密码至少12位"}`, http.StatusBadRequest)
		return
	}

	// 服务开关
	for _, svc := range []string{"samba", "ftp", "webdav"} {
		req.Services[svc] = r.FormValue("svc_"+svc) == "true"
	}

	// 配额
	quotaStr := r.FormValue("quota_gb")
	if quotaStr != "" {
		q, err := strconv.Atoi(quotaStr)
		if err != nil || q < 0 {
			http.Error(w, `{"error":"配额必须是非负整数"}`, http.StatusBadRequest)
			return
		}
		req.QuotaGB = q
	}

	// 共享权限 (格式: share_<folder>=readwrite/readonly/noaccess)
	for key, values := range r.Form {
		if strings.HasPrefix(key, "share_") && len(values) > 0 {
			folder := strings.TrimPrefix(key, "share_")
			perm := values[0]
			if perm == "readwrite" || perm == "readonly" || perm == "noaccess" {
				req.SharePerms[folder] = perm
			}
		}
	}

	// 执行创建
	steps := []string{}
	addStep := func(format string, args ...interface{}) {
		steps = append(steps, fmt.Sprintf(format, args...))
	}

	// 1. Create system user (inline, no external script dependency)
	addStep("创建系统用户 %s", req.Username)
	out, err := common.SudoExec("useradd", "-m", "-s", "/bin/bash", req.Username)
	if err != nil {
		// Check if user already exists
		if out2, err2 := common.SudoExec("id", req.Username); err2 == nil && out2 != "" {
			addStep("用户 %s 已存在，跳过系统用户创建", req.Username)
		} else {
			http.Error(w, fmt.Sprintf(`{"error":"创建系统用户失败: %s"}`, out+": "+err.Error()), http.StatusInternalServerError)
			return
		}
	}
	// Set password
	out, err = common.SudoExec("sh", "-c", fmt.Sprintf("echo '%s:%s' | chpasswd", req.Username, req.Password))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"设置密码失败: %s"}`, out+": "+err.Error()), http.StatusInternalServerError)
		return
	}
	addStep("设置密码")

		// 2. 更新服务开关（默认全关，按需开启）
	if !req.Services["samba"] {
		addStep("关闭 Samba 服务")
		disableSambaUser(req.Username)
	}
	if !req.Services["ftp"] {
		addStep("关闭 FTP 服务")
		disableFTPUser(req.Username)
	}
	if !req.Services["webdav"] {
		addStep("关闭 WebDAV 服务")
		disableWebDAVUser(req.Username)
	}

	// 3. 设置私有目录配额
	if req.QuotaGB > 0 {
		addStep("设置私有目录配额 %dGB", req.QuotaGB)
		if err := setPrivateDirQuota(req.Username, req.QuotaGB); err != nil {
			// 配额失败不阻塞创建，记录警告
			addStep("⚠️ 配额设置失败: %v", err)
		}
	}

	// 4. 设置共享文件夹权限
	for folder, perm := range req.SharePerms {
		addStep("设置共享 %s 权限为 %s", folder, perm)
		if err := setSharePermission(req.Username, folder, perm); err != nil {
			addStep("⚠️ 共享 %s 权限设置失败: %v", folder, err)
		}
	}

	common.JSONResponse(w, map[string]interface{}{
		"message": fmt.Sprintf("用户 %s 创建成功", req.Username),
		"steps":   steps,
	})
}

// isValidUsername 白名单校验用户名
func isValidUsername(name string) bool {
	if len(name) < 2 || len(name) > 32 {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
				return false
			}
			continue
		}
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// updateUserServices 更新已有用户的服务开关
func updateUserServices(username string, services map[string]bool) error {
	if services["samba"] {
		enableSambaUser(username)
	} else {
		disableSambaUser(username)
	}
	if services["ftp"] {
		enableFTPUser(username)
	} else {
		disableFTPUser(username)
	}
	if services["webdav"] {
		enableWebDAVUser(username)
	} else {
		disableWebDAVUser(username)
	}
	return nil
}

// --- Samba 服务开关 ---

func enableSambaUser(username string) error {
	// smbpasswd -e 启用
	out, err := common.SudoExec("smbpasswd", "-e", username)
	if err != nil && !strings.Contains(out, "Failed") {
		return fmt.Errorf("启用 Samba 失败: %s: %v", out, err)
	}
	return nil
}

func disableSambaUser(username string) error {
	out, err := common.SudoExec("smbpasswd", "-d", username)
	if err != nil && !strings.Contains(out, "Failed") {
		return fmt.Errorf("禁用 Samba 失败: %s: %v", out, err)
	}
	return nil
}

// --- FTP 服务开关 ---

func enableFTPUser(username string) error {
	// 从 userlist 移除 = 允许登录
	out, err := common.SudoExec("bash", "-c",
		fmt.Sprintf("sed -i '/^%s$/d' /etc/vsftpd.userlist", shellQuote(username)))
	if err != nil {
		return fmt.Errorf("启用 FTP 失败: %s: %v", out, err)
	}
	// 重新加载 vsftpd
	common.SudoExec("systemctl", "reload", "vsftpd")
	return nil
}

func disableFTPUser(username string) error {
	// 加入 userlist = 拒绝登录（vsftpd 配置 userlist_deny=YES）
	// 先检查是否已存在
	data, _ := common.ExecOutput("cat", "/etc/vsftpd.userlist")
	for _, line := range strings.Split(data, "\n") {
		if strings.TrimSpace(line) == username {
			return nil // 已禁用
		}
	}
	out, err := common.SudoExec("bash", "-c",
		fmt.Sprintf("echo %s >> /etc/vsftpd.userlist", shellQuote(username)))
	if err != nil {
		return fmt.Errorf("禁用 FTP 失败: %s: %v", out, err)
	}
	common.SudoExec("systemctl", "reload", "vsftpd")
	return nil
}

// --- WebDAV 服务开关 ---

func enableWebDAVUser(username string) error {
	// 重新添加 htpasswd 条目（密码无法恢复，需要用户重新设置）
	// 这里只是标记启用，实际密码需要用户重新设置
	return nil
}

func disableWebDAVUser(username string) error {
	out, err := common.SudoExec("bash", "-c",
		fmt.Sprintf("sed -i '/^%s:/d' /etc/rclone-htpasswd", shellQuote(username)))
	if err != nil {
		return fmt.Errorf("禁用 WebDAV 失败: %s: %v", out, err)
	}
	return nil
}

// --- 共享文件夹权限 ---

func setSharePermission(username, folder, perm string) error {
	smbConf, err := common.SudoOutput("cat", "/etc/samba/smb.conf")
	if err != nil {
		return fmt.Errorf("读取 Samba 配置失败: %v", err)
	}

	// 找到共享段落
	shareTag := "[" + folder + "]"
	if !strings.Contains(smbConf, shareTag) {
		return fmt.Errorf("共享 %s 不存在", folder)
	}

	// 修改 valid users 和 read only
	lines := strings.Split(smbConf, "\n")
	inShare := false
	shareStart := -1
	shareEnd := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == shareTag {
			inShare = true
			shareStart = i
			continue
		}
		if inShare && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			shareEnd = i
			break
		}
	}
	if shareEnd == -1 {
		shareEnd = len(lines)
	}

	// 在共享段落中处理权限
	var newLines []string
	validUsersLine := -1
	readOnlyLine := -1

	for i := shareStart; i < shareEnd; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "valid users") {
			validUsersLine = i
		}
		if strings.HasPrefix(trimmed, "read only") {
			readOnlyLine = i
		}
	}

	// 构建新的 valid users 列表
	if validUsersLine >= 0 {
		parts := strings.SplitN(lines[validUsersLine], "=", 2)
		if len(parts) == 2 {
			users := strings.Split(parts[1], ",")
			var newUsers []string
			for _, u := range users {
				u = strings.TrimSpace(u)
				if u != "" && u != username {
					newUsers = append(newUsers, u)
				}
			}
			if perm != "noaccess" {
				newUsers = append(newUsers, username)
			}
			lines[validUsersLine] = "   valid users = " + strings.Join(newUsers, ", ")
		}
	}

	// 设置 read only
	if readOnlyLine >= 0 {
		if perm == "readonly" {
			lines[readOnlyLine] = "   read only = yes"
		} else {
			lines[readOnlyLine] = "   read only = no"
		}
	}

	newLines = lines
	newConf := strings.Join(newLines, "\n")

	// 写回配置
	if err := common.SafeWriteFile("/etc/samba/smb.conf", newConf); err != nil {
		return fmt.Errorf("写入 Samba 配置失败: %v", err)
	}

	// 重载 Samba
	common.SudoExec("systemctl", "reload", "smbd")
	return nil
}
