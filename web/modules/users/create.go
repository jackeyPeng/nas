package users

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"nas-panel/common"
	"nas-panel/modules/diskmgmt"
)

// CreateUserRequest 向导创建用户的请求参数
type CreateUserRequest struct {
	Username   string
	Password   string
	Services   map[string]bool   // samba/ftp/webdav
	QuotaGB    int               // 私有目录配额，0=无限制
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
	if err := common.ValidSecret(req.Password); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
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

	// 1-2. 创建系统用户 + 密码 + 主目录 + 服务开关（机械抽取进 CreateUserWithPassword，
	// 行为不变——handler 只负责表单解析与响应，内核与导出函数共享同一实现）。
	steps, err := CreateUserWithPassword(req.Username, req.Password, req.Services["samba"], req.Services["ftp"], req.Services["webdav"])
	addStep := func(format string, args ...interface{}) {
		steps = append(steps, fmt.Sprintf(format, args...))
	}
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
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

// CreateUserWithPassword 是 handleCreateUser 步骤 1/1.5/2 的内核：创建系统
// 用户、设置密码、建主目录、按服务开关启用/关闭 Samba/FTP/WebDAV——从
// handleCreateUser 机械抽取而来，行为不变（原函数改调它）。导出供 AI 工具域
// （create_user）等进程内调用方直接复用；密码参数由调用方负责生成/校验
// （AI 路径下密码由前端生成器生成，经确认端点 common.ValidSecret 校验后
// 注入，永不进 LLM 上下文）。
func CreateUserWithPassword(username, password string, svcSamba, svcFtp, svcWebdav bool) (steps []string, err error) {
	// 用户名白名单校验下沉进内核本身（而不是只留在 handleCreateUser 里）：
	// CreateUserWithPassword 现在有两个调用方——HTTP 向导 handler 和 AI
	// create_user 确认执行路径（executeCreateUserConfirmed）。两者共用同一
	// 内核就必须共用同一道校验，否则任何新增调用方都要自己记得重新校验
	// 一次，一旦漏了就是 useradd 命令注入/路径穿越（如 username="../etc"）。
	if !isValidUsername(username) {
		return nil, fmt.Errorf("用户名只能包含小写字母、数字、下划线和短横线，且以字母开头")
	}

	addStep := func(format string, args ...interface{}) {
		steps = append(steps, fmt.Sprintf(format, args...))
	}

	// 1. Create system user (inline, no external script dependency)
	addStep("创建系统用户 %s", username)
	out, cerr := common.SudoExec("useradd", "-m", "-s", "/bin/bash", "-G", "nasusers", username)
	if cerr != nil {
		// Check if user already exists
		if out2, err2 := common.SudoExec("id", username); err2 == nil && out2 != "" {
			addStep("用户 %s 已存在，跳过系统用户创建", username)
		} else {
			return steps, fmt.Errorf("创建系统用户失败: %s", out+": "+cerr.Error())
		}
	}
	// Set password
	out, cerr = common.SudoExecStdin(username+":"+password+"\n", "chpasswd")
	if cerr != nil {
		return steps, fmt.Errorf("设置密码失败: %s", out+": "+cerr.Error())
	}
	addStep("设置密码")

	// 1.5 创建用户主目录 + 默认子目录 + 写入 folders.db + 生成托管配置
	if herr := diskmgmt.EnsureUserHome(username); herr != nil {
		addStep("⚠️ 创建主目录失败: %v", herr)
	} else {
		addStep("创建用户主目录 %s", username)
	}

	// 2. 更新服务开关（默认全关，按需开启）
	if svcSamba {
		if serr := addSambaUser(username, password); serr != nil {
			addStep("⚠️ Samba 启用失败: %v", serr)
		} else {
			addStep("启用 Samba 服务")
		}
	} else {
		addStep("关闭 Samba 服务")
		disableSambaUser(username)
	}
	if svcFtp {
		addStep("启用 FTP 服务")
		enableFTPUser(username)
	} else {
		addStep("关闭 FTP 服务")
		disableFTPUser(username)
	}
	if svcWebdav {
		if werr := addWebDAVUser(username, password); werr != nil {
			addStep("⚠️ WebDAV 启用失败: %v", werr)
		} else {
			addStep("启用 WebDAV 服务")
		}
	} else {
		addStep("关闭 WebDAV 服务")
		disableWebDAVUser(username)
	}

	return steps, nil
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

// parseServiceToggles 只解析请求中明确出现的服务字段。
// 缺失字段 = 不变更（前端快捷开关一次只发一个字段，若把缺失当 false
// 会导致「开 FTP 顺手关掉 Samba/WebDAV」的静默互斥）。
func parseServiceToggles(r *http.Request) map[string]bool {
	services := map[string]bool{}
	for _, svc := range []string{"samba", "ftp", "webdav"} {
		if v := r.FormValue(svc); v != "" {
			services[svc] = v == "true"
		}
	}
	return services
}

// updateUserServices 更新已有用户的服务开关（只处理 services 中明确给出的键）
func updateUserServices(username string, services map[string]bool) error {
	// 只处理请求中明确给出的服务；未提及的保持原状
	if on, ok := services["samba"]; ok {
		if on {
			// smbpasswd -e 只能启用密码库中已有的账户；后补开启的用户
			// 不在库里会静默失败（UI 报成功但 SMB 拒绝登录）。改密流程
			// (changePassword) 会 smbpasswd -a 同步，故指引用户先重置密码。
			if !inSambaPasswd(username) {
				return fmt.Errorf("用户 %s 不在 Samba 密码库：请先在用户页为该用户修改一次密码（会自动同步 Samba/WebDAV），再开启服务", username)
			}
			if err := enableSambaUser(username); err != nil {
				return err
			}
		} else {
			if err := disableSambaUser(username); err != nil {
				return err
			}
		}
	}
	if on, ok := services["ftp"]; ok {
		if on {
			if err := enableFTPUser(username); err != nil {
				return err
			}
		} else {
			if err := disableFTPUser(username); err != nil {
				return err
			}
		}
	}
	if on, ok := services["webdav"]; ok {
		if on {
			// WebDAV 认证只认 /etc/rclone-htpasswd；条目由 changePassword 写入。
			// 不在文件里时开启是空操作（401），明确拒绝并指引改密。
			if !inHtpasswd(username) {
				return fmt.Errorf("用户 %s 不在 WebDAV 密码文件：请先在用户页为该用户修改一次密码（会自动同步 Samba/WebDAV），再开启服务", username)
			}
		} else {
			if err := disableWebDAVUser(username); err != nil {
				return err
			}
		}
	}
	return nil
}

// countUserLines 统计 content 中以 "prefix:" 开头的行数，
// 等价于原来的 `grep -c '^prefix:'`。纯函数，便于单测。
func countUserLines(content, prefix string) int {
	if prefix == "" {
		return 0
	}
	needle := prefix + ":"
	n := 0
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, needle) {
			n++
		}
	}
	return n
}

// inSambaPasswd 判断用户是否已有 Samba 密码库记录。
func inSambaPasswd(username string) bool {
	out, _ := common.SudoOutput("pdbedit", "-L")
	return countUserLines(out, username) > 0
}

// inHtpasswd 判断用户是否已有 WebDAV htpasswd 记录。
func inHtpasswd(username string) bool {
	out, _ := common.SudoOutput("cat", "/etc/rclone-htpasswd")
	return countUserLines(out, username) > 0
}

// --- Samba 服务开关 ---

// addSambaUser 将用户加入 Samba 密码库并设置密码（新建用户时必须用 -a，不能用 -e）
func addSambaUser(username, password string) error {
	out, err := common.SudoExecStdin(password+"\n"+password+"\n", "smbpasswd", "-a", username, "-s")
	if err != nil {
		return fmt.Errorf("smbpasswd -a 失败: %s: %v", out, err)
	}
	return nil
}

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
// 注意：vsftpd.conf 里 userlist_deny=NO，即 userlist 是【白名单】——
// 在列表里 = 允许登录，不在列表里 = 拒绝登录。

// appendLine 等价于 `echo line >> file`：直接在现有内容末尾追加一行
// （不会在追加前补一个多余的换行，与 shell >> 的字节级行为一致）。
// 纯函数，便于单测。
func appendLine(content, line string) string {
	return content + line + "\n"
}

// removeExactLine 等价于 `sed -i '/^line$/d' file`：删除与 line 完全相等的行，
// 其余行原样保留（包括空行）。纯函数，便于单测。
func removeExactLine(content, line string) string {
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	// 末尾空字符串元素代表原内容以换行结尾；sed 处理时不会把它当成"一行"，
	// 这里按原文件行结构处理后再还原末尾换行语义。
	trailingNewline := strings.HasSuffix(content, "\n")
	var kept []string
	for i, l := range lines {
		if i == len(lines)-1 && trailingNewline && l == "" {
			continue // 不是真实行，是 Split 产生的末尾空元素
		}
		if l == line {
			continue
		}
		kept = append(kept, l)
	}
	result := strings.Join(kept, "\n")
	if len(kept) > 0 {
		result += "\n"
	}
	return result
}

// removeLinesByPrefix 等价于 `sed -i '/^prefix/d' file`：删除以 prefix 开头的行，
// 其余行原样保留。纯函数，便于单测。
func removeLinesByPrefix(content, prefix string) string {
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	trailingNewline := strings.HasSuffix(content, "\n")
	var kept []string
	for i, l := range lines {
		if i == len(lines)-1 && trailingNewline && l == "" {
			continue
		}
		if strings.HasPrefix(l, prefix) {
			continue
		}
		kept = append(kept, l)
	}
	result := strings.Join(kept, "\n")
	if len(kept) > 0 {
		result += "\n"
	}
	return result
}

func enableFTPUser(username string) error {
	// 加入 userlist = 允许登录（白名单模式）
	data, _ := common.ExecOutput("cat", "/etc/vsftpd.userlist")
	for _, line := range strings.Split(data, "\n") {
		if strings.TrimSpace(line) == username {
			return nil // 已在白名单
		}
	}
	current, err := common.SudoOutput("cat", "/etc/vsftpd.userlist")
	if err != nil {
		return fmt.Errorf("启用 FTP 失败: %v", err)
	}
	updated := appendLine(current, username)
	if err := common.SafeWriteFile("/etc/vsftpd.userlist", updated); err != nil {
		return fmt.Errorf("启用 FTP 失败: %v", err)
	}
	// 重新加载 vsftpd
	common.SudoExec("systemctl", "reload", "vsftpd")
	return nil
}

func disableFTPUser(username string) error {
	// 从 userlist 移除 = 拒绝登录（白名单模式）
	data, err := common.SudoOutput("cat", "/etc/vsftpd.userlist")
	if err != nil {
		return fmt.Errorf("禁用 FTP 失败: %v", err)
	}
	updated := removeExactLine(data, username)
	if err := common.SafeWriteFile("/etc/vsftpd.userlist", updated); err != nil {
		return fmt.Errorf("禁用 FTP 失败: %v", err)
	}
	common.SudoExec("systemctl", "reload", "vsftpd")
	return nil
}

// --- WebDAV 服务开关 ---

// addWebDAVUser 将用户加入 rclone-htpasswd 并设置密码
func addWebDAVUser(username, password string) error {
	out, err := common.SudoExec("htpasswd", "-b", "/etc/rclone-htpasswd", username, password)
	if err != nil {
		return fmt.Errorf("htpasswd 失败: %s: %v", out, err)
	}
	return nil
}

func disableWebDAVUser(username string) error {
	data, err := common.SudoOutput("cat", "/etc/rclone-htpasswd")
	if err != nil {
		return fmt.Errorf("禁用 WebDAV 失败: %v", err)
	}
	updated := removeLinesByPrefix(data, username+":")
	if err := common.SafeWriteFile("/etc/rclone-htpasswd", updated); err != nil {
		return fmt.Errorf("禁用 WebDAV 失败: %v", err)
	}
	return nil
}

// --- 共享文件夹权限 ---

// setSharePermission 更新用户对某个共享文件夹的访问权限。
// 统一走元数据 + SyncAllConfigs 重生成，避免直接改 smb.conf 被下次配置同步覆盖。
// 权限粒度（TODO #30）：
//
//	readwrite → 加入 valid_users + write_users
//	readonly  → 加入 valid_users，移出 write_users
//	noaccess  → 移出 valid_users + write_users（deny 必须同时清两个列表）
func setSharePermission(username, folder, perm string) error {
	metas := diskmgmt.GetAllFolderMeta()
	var target *diskmgmt.FolderMeta
	for i := range metas {
		if metas[i].Name == folder {
			target = &metas[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("共享 %s 不存在", folder)
	}

	validUsers := target.ValidUsers
	writeUsers := target.WriteUsers
	permission := target.Permission

	// 开放共享物化：valid_users 为空 = 任何可认证用户可连（如 public）。
	// 首次按用户编辑时展开为全部现有用户，避免给一个人设权限把其他人静默锁出共享
	if strings.TrimSpace(validUsers) == "" {
		validUsers = strings.Join(listAllUsers(), ",")
	}

	validUsers, writeUsers, permission = applyPermissionChange(validUsers, writeUsers, permission, username, perm)

	// 拷贝整个 meta 再改权限字段：结构性保证不漏携带其他字段
	// （write_users 抹除事故的教训 — 见 storage-permission-model §写入路径一致性）
	updated := *target
	updated.Permission = permission
	updated.ValidUsers = validUsers
	updated.WriteUsers = writeUsers
	diskmgmt.SyncFolderMeta(updated)

	return diskmgmt.SyncAllConfigs()
}

// applyPermissionChange 计算单用户权限变更后的 (valid_users, write_users, permission)。
// 纯函数，便于单测。关键点：
//   - 物化：write_users 空 + 文件夹级 readwrite 时，先显式化为全部 valid_users，
//     否则「降为只读」在纯文件夹级数据上失效。
//   - 同步 permission：write_users 空 → readonly；valid_users 空 → noaccess；否则 readwrite。
func applyPermissionChange(validUsers, writeUsers, permission, username, perm string) (string, string, string) {
	// 物化
	if strings.TrimSpace(writeUsers) == "" && permission == "readwrite" {
		writeUsers = validUsers
	}

	validUsers = listRemove(validUsers, username)
	writeUsers = listRemove(writeUsers, username)

	switch perm {
	case "readwrite":
		validUsers = listAdd(validUsers, username)
		writeUsers = listAdd(writeUsers, username)
	case "readonly":
		validUsers = listAdd(validUsers, username)
		// writeUsers 保持移除状态
	case "noaccess":
		// 两个列表都已移除
	}

	switch {
	case strings.TrimSpace(writeUsers) != "":
		permission = "readwrite"
	case strings.TrimSpace(validUsers) == "":
		permission = "noaccess"
	default:
		permission = "readonly"
	}

	return validUsers, writeUsers, permission
}

// listAdd 向逗号分隔的用户列表加入一个用户（去重）
func listAdd(list, user string) string {
	user = strings.TrimSpace(user)
	if user == "" {
		return list
	}
	if listHas(list, user) {
		return list
	}
	if strings.TrimSpace(list) == "" {
		return user
	}
	return strings.TrimRight(strings.TrimSpace(list), ",") + "," + user
}

// listRemove 从逗号分隔的用户列表移除一个用户
func listRemove(list, user string) string {
	user = strings.TrimSpace(user)
	var keep []string
	for _, u := range strings.Split(list, ",") {
		u = strings.TrimSpace(u)
		if u != "" && u != user {
			keep = append(keep, u)
		}
	}
	return strings.Join(keep, ",")
}

// listHas 判断逗号分隔列表是否包含某用户
func listHas(list, user string) bool {
	for _, u := range strings.Split(list, ",") {
		if strings.TrimSpace(u) == user {
			return true
		}
	}
	return false
}
