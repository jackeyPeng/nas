package config

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"nas-panel/common"
)

// RegisterRoutes registers config module routes
func RegisterRoutes(mux *http.ServeMux) {
	// .env config
	mux.HandleFunc("/api/config/env", common.AuthMiddleware(handleEnvConfig))
	// Samba shares
	mux.HandleFunc("/api/config/samba", common.AuthMiddleware(handleSambaConfig))
	mux.HandleFunc("/api/config/samba/share", common.AuthMiddleware(handleSambaShare))
	// FTP users
	mux.HandleFunc("/api/config/vsftpd-users", common.AuthMiddleware(handleVsftpdUsers))
	// Service enable/disable
	mux.HandleFunc("/api/config/services", common.AuthMiddleware(handleServiceList))
	mux.HandleFunc("/api/config/service-toggle", common.AuthMiddleware(handleServiceToggle))
	// Config file editor
	mux.HandleFunc("/api/config/file", common.AuthMiddleware(handleConfigFile))
}

// ═══════════════════════════════════════
// .env 配置管理
// ═══════════════════════════════════════

func handleEnvConfig(w http.ResponseWriter, r *http.Request) {
	envPath := common.GetEnvFilePath()
	if envPath == "" {
		common.JSONResponse(w, map[string]interface{}{"error": "no .env file"})
		return
	}

	if r.Method == http.MethodGet {
		config := common.ReadAllEnv(envPath)
		common.JSONResponse(w, map[string]interface{}{"config": config})
		return
	}

	if r.Method == http.MethodPost {
		r.ParseForm()
		values := map[string]string{}
		for key, vals := range r.Form {
			if len(vals) > 0 {
				values[key] = strings.TrimSpace(vals[0])
			}
		}
		if len(values) == 0 {
			http.Error(w, `{"error": "no values provided"}`, http.StatusBadRequest)
			return
		}
		err := updateEnvFile(envPath, values)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		common.JSONResponse(w, map[string]interface{}{"message": "配置已保存"})
	}
}

// ═══════════════════════════════════════
// Samba 共享管理
// ═══════════════════════════════════════

// handleSambaConfig GET: 返回解析后的共享列表, POST: 保存完整 smb.conf
func handleSambaConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		shares := parseSambaShares()
		common.JSONResponse(w, map[string]interface{}{"shares": shares})
		return
	}

	if r.Method == http.MethodPost {
		content := r.FormValue("content")
		if content == "" {
			http.Error(w, `{"error": "content required"}`, http.StatusBadRequest)
			return
		}
		err := writeFileWithSudo("/etc/samba/smb.conf", content)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		// 重启 smbd
		common.SudoExec("systemctl", "restart", "smbd", "nmbd")
		common.JSONResponse(w, map[string]interface{}{"message": "Samba 配置已保存并重启服务"})
	}
}

// handleSambaShare 添加/删除单个共享
func handleSambaShare(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		// 添加共享
		name := r.FormValue("name")
		path := r.FormValue("path")
		comment := r.FormValue("comment")
		readOnly := r.FormValue("read_only") == "true"
		validUsers := r.FormValue("valid_users")

		if name == "" || path == "" {
			http.Error(w, `{"error": "name and path required"}`, http.StatusBadRequest)
			return
		}

		// 安全检查：共享名不能含特殊字符
		if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(name) {
			http.Error(w, `{"error": "share name must be alphanumeric"}`, http.StatusBadRequest)
			return
		}

		shareBlock := fmt.Sprintf("\n[%s]\n", name)
		if comment != "" {
			shareBlock += fmt.Sprintf("   comment = %s\n", comment)
		}
		shareBlock += fmt.Sprintf("   path = %s\n", path)
		shareBlock += fmt.Sprintf("   browseable = yes\n")
		if readOnly {
			shareBlock += "   read only = yes\n"
		} else {
			shareBlock += "   read only = no\n"
			shareBlock += "   create mask = 0775\n"
			shareBlock += "   directory mask = 0775\n"
		}
		if validUsers != "" {
			shareBlock += fmt.Sprintf("   valid users = %s\n", validUsers)
		}
		// 与托管共享一致：补 force user/group，避免 valid_users 非属主时写不进
		nasUser := getNASUser()
		shareBlock += fmt.Sprintf("   force user = %s\n", nasUser)
		shareBlock += fmt.Sprintf("   force group = %s\n", nasUser)

		// 追加到 smb.conf
		cmd := exec.Command("sudo", "tee", "-a", "/etc/samba/smb.conf")
		cmd.Stdin = strings.NewReader(shareBlock)
		cmd.Run()

		// 创建目录（如果不存在）
		common.SudoExec("mkdir", "-p", path)
		// 重启
		common.SudoExec("systemctl", "restart", "smbd", "nmbd")
		common.JSONResponse(w, map[string]interface{}{"message": "共享 " + name + " 已添加"})
		return
	}

	if r.Method == http.MethodDelete {
		name := r.FormValue("name")
		if name == "" {
			http.Error(w, `{"error": "name required"}`, http.StatusBadRequest)
			return
		}
		// 读取 smb.conf，删除对应 [name] 段
		current := readFileWithSudo("/etc/samba/smb.conf")
		lines := strings.Split(current, "\n")
		var result []string
		skip := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
				if trimmed == "["+name+"]" {
					skip = true
					continue
				}
				skip = false
			}
			if !skip {
				result = append(result, line)
			}
		}
		newContent := strings.Join(result, "\n")
		writeFileWithSudo("/etc/samba/smb.conf", newContent)
		common.SudoExec("systemctl", "restart", "smbd", "nmbd")
		common.JSONResponse(w, map[string]interface{}{"message": "共享 " + name + " 已删除"})
	}
}

// parseSambaShares 解析 smb.conf 返回共享列表
func parseSambaShares() []map[string]string {
	content := readFileWithSudo("/etc/samba/smb.conf")
	var shares []map[string]string
	var current map[string]string
	var section string

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if current != nil {
				shares = append(shares, current)
			}
			section = strings.Trim(trimmed, "[]")
			current = map[string]string{"name": section}
			continue
		}
		if current != nil {
			parts := strings.SplitN(trimmed, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				current[key] = val
			}
		}
	}
	if current != nil {
		shares = append(shares, current)
	}
	return shares
}

// ═══════════════════════════════════════
// FTP 用户白名单管理
// ═══════════════════════════════════════

func handleVsftpdUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		out := readFileWithSudo("/etc/vsftpd.userlist")
		users := []string{}
		for _, u := range strings.Split(strings.TrimSpace(out), "\n") {
			u = strings.TrimSpace(u)
			if u != "" {
				users = append(users, u)
			}
		}
		common.JSONResponse(w, map[string]interface{}{"users": users})
		return
	}

	if r.Method == http.MethodPost {
		// 添加用户到白名单
		username := r.FormValue("username")
		if username == "" {
			http.Error(w, `{"error": "username required"}`, http.StatusBadRequest)
			return
		}
		cmd := exec.Command("sudo", "tee", "-a", "/etc/vsftpd.userlist")
		cmd.Stdin = strings.NewReader(username + "\n")
		cmd.Run()
		common.SudoExec("systemctl", "restart", "vsftpd")
		common.JSONResponse(w, map[string]interface{}{"message": "用户 " + username + " 已添加到 FTP 白名单"})
		return
	}

	if r.Method == http.MethodDelete {
		username := r.FormValue("username")
		if username == "" {
			http.Error(w, `{"error": "username required"}`, http.StatusBadRequest)
			return
		}
		current := readFileWithSudo("/etc/vsftpd.userlist")
		var result []string
		for _, line := range strings.Split(current, "\n") {
			if strings.TrimSpace(line) != username && strings.TrimSpace(line) != "" {
				result = append(result, strings.TrimSpace(line))
			}
		}
		writeFileWithSudo("/etc/vsftpd.userlist", strings.Join(result, "\n")+"\n")
		common.SudoExec("systemctl", "restart", "vsftpd")
		common.JSONResponse(w, map[string]interface{}{"message": "用户 " + username + " 已从 FTP 白名单移除"})
	}
}

// ═══════════════════════════════════════
// 服务开机自启管理
// ═══════════════════════════════════════

func handleServiceList(w http.ResponseWriter, r *http.Request) {
	out, _ := common.SudoExec("systemctl", "list-unit-files", "--type=service", "--state=enabled", "--no-pager")
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(out))
}

func handleServiceToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	svcName := r.FormValue("service")
	action := r.FormValue("action") // enable or disable
	if svcName == "" || (action != "enable" && action != "disable") {
		http.Error(w, `{"error": "service and action(enable/disable) required"}`, http.StatusBadRequest)
		return
	}
	out, err := common.SudoExec("systemctl", action, svcName)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, out+": "+err.Error()), http.StatusInternalServerError)
		return
	}
	common.JSONResponse(w, map[string]interface{}{"message": svcName + " " + action + "d"})
}

// ═══════════════════════════════════════
// 配置文件在线编辑
// ═══════════════════════════════════════

var allowedConfigFiles = map[string]string{
	"smb.conf":      "/etc/samba/smb.conf",
	"vsftpd.conf":   "/etc/vsftpd.conf",
	"exports":       "/etc/exports",
	"nfs.conf":      "/etc/nfs.conf",
	"jail.local":    "/etc/fail2ban/jail.local",
	"env":           "/opt/nas/.env",
	"rclone-htpasswd": "/etc/rclone-htpasswd",
}

func handleConfigFile(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		name = r.FormValue("name")
	}
	filePath, ok := allowedConfigFiles[name]
	if !ok {
		http.Error(w, `{"error": "file not allowed, valid: smb.conf, vsftpd.conf, exports, nfs.conf, jail.local, env"}`, http.StatusBadRequest)
		return
	}

	if r.Method == http.MethodGet {
		content := readFileWithSudo(filePath)
		common.JSONResponse(w, map[string]interface{}{"name": name, "path": filePath, "content": content})
		return
	}

	if r.Method == http.MethodPost {
		content := r.FormValue("content")
		if content == "" {
			http.Error(w, `{"error": "content required"}`, http.StatusBadRequest)
			return
		}
		err := writeFileWithSudo(filePath, content)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		common.JSONResponse(w, map[string]interface{}{"message": name + " 已保存"})
	}
}

// ═══════════════════════════════════════
// 工具函数
// ═══════════════════════════════════════

// getNASUser 返回 NAS 管理员用户名（.env > 环境变量 > root）
func getNASUser() string {
	user, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
	if user == "" {
		user = os.Getenv("NAS_USER")
	}
	if user == "" {
		user = "root"
	}
	return user
}

func readFileWithSudo(path string) string {
	out, _ := common.SudoExec("cat", path)
	return out
}

func writeFileWithSudo(path, content string) error {
	cmd := exec.Command("sudo", "tee", path)
	cmd.Stdin = strings.NewReader(content)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

func updateEnvFile(path string, values map[string]string) error {
	// Read current .env, replace matching keys
	current := readFileWithSudo(path)
	var result []string
	for _, line := range strings.Split(current, "\n") {
		trimmed := strings.TrimSpace(line)
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			if val, ok := values[key]; ok {
				result = append(result, key+"="+val)
				continue
			}
		}
		result = append(result, line)
	}
	// Append new keys not in file
	for k, v := range values {
		found := false
		for _, line := range result {
			if strings.HasPrefix(strings.TrimSpace(line), k+"=") {
				found = true
				break
			}
		}
		if !found {
			result = append(result, k+"="+v)
		}
	}
	return writeFileWithSudo(path, strings.Join(result, "\n"))
}
