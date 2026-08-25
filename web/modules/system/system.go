package system

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"nas-panel/common"
)

// RegisterRoutes registers system settings routes
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/system/overview", common.AuthMiddleware(handleSystemOverview))
	mux.HandleFunc("/api/system/hostname", common.AuthMiddleware(handleHostname))
	mux.HandleFunc("/api/system/timezone", common.AuthMiddleware(handleTimezone))
	mux.HandleFunc("/api/system/ssh-config", common.AuthMiddleware(handleSSHConfig))
	mux.HandleFunc("/api/system/sysctl", common.AuthMiddleware(handleSysctl))
	mux.HandleFunc("/api/system/updates", common.AuthMiddleware(handleUpdates))
	mux.HandleFunc("/api/system/services", common.AuthMiddleware(handleServices))
	mux.HandleFunc("/api/system/reset", common.AuthMiddleware(handleReset))
	mux.HandleFunc("/api/system/check", common.AuthMiddleware(handleSystemCheck))
}

// ═══════════════════════════════════════
// 系统总览 — 一次调用返回所有基本信息
// ═══════════════════════════════════════

func handleSystemOverview(w http.ResponseWriter, r *http.Request) {
	result := map[string]interface{}{}

	// Hostname
	hostname, _ := os.Hostname()
	result["hostname"] = hostname

	// Network: IP, gateway, DNS
	result["network"] = getNetworkInfo()

	// Time & timezone
	result["time"] = getTimeInfo()

	// SSH config
	result["ssh"] = getSSHConfig()

	// Sysctl key params
	result["sysctl"] = getSysctlParams()

	// Updates
	result["updates"] = getUpdateInfo()

	// Services
	result["services"] = getServiceList()

	common.JSONResponse(w, result)
}

// ═══════════════════════════════════════
// 网络信息
// ═══════════════════════════════════════

func getNetworkInfo() map[string]interface{} {
	info := map[string]interface{}{}

	// IP addresses (non-loopback)
	out, _ := common.ExecOutput("ip", "-4", "-o", "addr", "show")
	var ips []map[string]string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 {
			iface := fields[1]
			addr := fields[3]
			if !strings.HasPrefix(addr, "127.") {
				ips = append(ips, map[string]string{
					"interface": iface,
					"address":   addr,
				})
			}
		}
	}
	info["interfaces"] = ips

	// Default gateway
	routeOut, _ := common.ExecOutput("ip", "route", "show", "default")
	if fields := strings.Fields(routeOut); len(fields) >= 3 {
		info["gateway"] = fields[2]
	}

	// DNS
	if data, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		var dns []string
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "nameserver") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					dns = append(dns, fields[1])
				}
			}
		}
		info["dns"] = dns
	}

	return info
}

// ═══════════════════════════════════════
// 时间与时区
// ═══════════════════════════════════════

func getTimeInfo() map[string]interface{} {
	info := map[string]interface{}{}

	// Current time
	out, _ := common.ExecOutput("date", "+%Y-%m-%d %H:%M:%S")
	info["current"] = strings.TrimSpace(out)

	// Timezone
	out, _ = common.ExecOutput("timedatectl", "show", "--property=Timezone", "--value")
	info["timezone"] = strings.TrimSpace(out)

	// NTP status
	out, _ = common.ExecOutput("timedatectl", "show", "--property=NTPSynchronized", "--value")
	info["ntp_synced"] = strings.TrimSpace(out) == "yes"

	return info
}

func handleTimezone(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		common.JSONResponse(w, getTimeInfo())
		return
	}
	if r.Method == http.MethodPost {
		tz := r.FormValue("timezone")
		if tz == "" {
			http.Error(w, `{"error":"timezone required"}`, http.StatusBadRequest)
			return
		}
		// Validate timezone exists
		if _, err := os.Stat("/usr/share/zoneinfo/" + tz); err != nil {
			http.Error(w, `{"error":"无效的时区: `+tz+`"}`, http.StatusBadRequest)
			return
		}
		_, err := common.SudoExec("timedatectl", "set-timezone", tz)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		common.JSONResponse(w, map[string]interface{}{
			"message":  "时区已修改为 " + tz,
			"timezone": tz,
		})
	}
}

// ═══════════════════════════════════════
// 主机名
// ═══════════════════════════════════════

func handleHostname(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		h, _ := os.Hostname()
		common.JSONResponse(w, map[string]interface{}{
			"hostname": h,
		})
		return
	}
	if r.Method == http.MethodPost {
		hostname := r.FormValue("hostname")
		if hostname == "" {
			http.Error(w, `{"error": "hostname required"}`, http.StatusBadRequest)
			return
		}
		_, err := common.SudoExec("hostnamectl", "set-hostname", hostname)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		common.JSONResponse(w, map[string]interface{}{
			"message":  "主机名已修改为 " + hostname,
			"hostname": hostname,
		})
	}
}

// ═══════════════════════════════════════
// SSH 配置 — 条目化读写
// ═══════════════════════════════════════

type SSHConfig struct {
	Port               int  `json:"port"`
	PermitRootLogin    bool `json:"permit_root_login"`
	PasswordAuth       bool `json:"password_auth"`
	PubkeyAuth         bool `json:"pubkey_auth"`
	MaxAuthTries       int  `json:"max_auth_tries"`
	AllowTcpForwarding bool `json:"allow_tcp_forwarding"`
}

func getSSHConfig() SSHConfig {
	cfg := SSHConfig{
		Port:               22,
		PermitRootLogin:    true,
		PasswordAuth:       true,
		PubkeyAuth:         true,
		MaxAuthTries:       6,
		AllowTcpForwarding: true,
	}

	data, err := os.ReadFile("/etc/ssh/sshd_config")
	if err != nil {
		out, _ := common.SudoOutput("cat", "/etc/ssh/sshd_config")
		data = []byte(out)
	}

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}
		key := strings.ToLower(fields[0])
		val := strings.ToLower(fields[1])

		switch key {
		case "port":
			if p, err := strconv.Atoi(val); err == nil {
				cfg.Port = p
			}
		case "permitrootlogin":
			cfg.PermitRootLogin = val == "yes" || val == "prohibit-password"
		case "passwordauthentication":
			cfg.PasswordAuth = val == "yes"
		case "pubkeyauthentication":
			cfg.PubkeyAuth = val == "yes"
		case "maxauthtries":
			if n, err := strconv.Atoi(val); err == nil {
				cfg.MaxAuthTries = n
			}
		case "allowtcpforwarding":
			cfg.AllowTcpForwarding = val == "yes"
		}
	}
	return cfg
}

func handleSSHConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		common.JSONResponse(w, getSSHConfig())
		return
	}

	if r.Method == http.MethodPost {
		port := r.FormValue("port")
		permitRoot := r.FormValue("permit_root_login") // yes/no
		passwordAuth := r.FormValue("password_auth")   // yes/no
		maxAuthTries := r.FormValue("max_auth_tries")

		// Read current config
		data, _ := os.ReadFile("/etc/ssh/sshd_config")
		if len(data) == 0 {
			out, _ := common.SudoOutput("cat", "/etc/ssh/sshd_config")
			data = []byte(out)
		}

		lines := strings.Split(string(data), "\n")
		var newLines []string

		// Track which settings we've handled
		handled := map[string]bool{}

		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				newLines = append(newLines, line)
				continue
			}
			fields := strings.Fields(trimmed)
			if len(fields) < 1 {
				newLines = append(newLines, line)
				continue
			}
			key := strings.ToLower(fields[0])

			switch key {
			case "port":
				if port != "" {
					newLines = append(newLines, "Port "+port)
					handled["port"] = true
					continue
				}
			case "permitrootlogin":
				if permitRoot != "" {
					newLines = append(newLines, "PermitRootLogin "+permitRoot)
					handled["permitrootlogin"] = true
					continue
				}
			case "passwordauthentication":
				if passwordAuth != "" {
					newLines = append(newLines, "PasswordAuthentication "+passwordAuth)
					handled["passwordauthentication"] = true
					continue
				}
			case "maxauthtries":
				if maxAuthTries != "" {
					newLines = append(newLines, "MaxAuthTries "+maxAuthTries)
					handled["maxauthtries"] = true
					continue
				}
			}
			newLines = append(newLines, line)
		}

		// Append unhandled settings
		if port != "" && !handled["port"] {
			newLines = append(newLines, "Port "+port)
		}
		if permitRoot != "" && !handled["permitrootlogin"] {
			newLines = append(newLines, "PermitRootLogin "+permitRoot)
		}
		if passwordAuth != "" && !handled["passwordauthentication"] {
			newLines = append(newLines, "PasswordAuthentication "+passwordAuth)
		}
		if maxAuthTries != "" && !handled["maxauthtries"] {
			newLines = append(newLines, "MaxAuthTries "+maxAuthTries)
		}

		// Write config
		newConf := strings.Join(newLines, "\n")
		common.SafeWriteFile("/etc/ssh/sshd_config", newConf)

		// Restart SSH
		common.SudoExec("systemctl", "restart", "sshd")

		common.JSONResponse(w, map[string]interface{}{
			"message": "SSH 配置已更新并重启",
		})
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

// ═══════════════════════════════════════
// 内核参数 — 条目化
// ═══════════════════════════════════════

type SysctlParam struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Description string `json:"description"`
}

func getSysctlParams() []SysctlParam {
	params := []SysctlParam{
		{Key: "vm.swappiness", Description: "交换分区使用倾向 (0-100, 越低越少用swap)"},
		{Key: "vm.dirty_ratio", Description: "脏页比例上限 (%)"},
		{Key: "vm.dirty_background_ratio", Description: "后台脏页比例 (%)"},
		{Key: "net.core.somaxconn", Description: "TCP 连接队列长度"},
		{Key: "net.ipv4.tcp_congestion_control", Description: "TCP 拥塞控制算法"},
		{Key: "fs.file-max", Description: "系统最大文件句柄数"},
	}

	for i, p := range params {
		out, err := exec.Command("sysctl", "-n", p.Key).Output()
		if err == nil {
			params[i].Value = strings.TrimSpace(string(out))
		}
	}
	return params
}

func handleSysctl(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		common.JSONResponse(w, map[string]interface{}{
			"params": getSysctlParams(),
		})
		return
	}

	if r.Method == http.MethodPost {
		key := r.FormValue("key")
		value := r.FormValue("value")
		if key == "" || value == "" {
			http.Error(w, `{"error":"key and value required"}`, http.StatusBadRequest)
			return
		}

		// Whitelist allowed keys
		allowed := map[string]bool{
			"vm.swappiness":             true,
			"vm.dirty_ratio":            true,
			"vm.dirty_background_ratio": true,
			"net.core.somaxconn":        true,
			"fs.file-max":               true,
		}
		if !allowed[key] {
			http.Error(w, `{"error":"不允许修改此参数"}`, http.StatusForbidden)
			return
		}

		out, err := common.SudoExec("sysctl", "-w", key+"="+value)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, out+": "+err.Error()), http.StatusInternalServerError)
			return
		}

		// Persist to /etc/sysctl.d/99-nas.conf
		persistLine := fmt.Sprintf("%s = %s\n", key, value)
		confPath := "/etc/sysctl.d/99-nas.conf"
		existing, _ := common.SudoOutput("cat", confPath)
		if existing == "" {
			common.SafeWriteFile(confPath, "# NAS Panel managed sysctl\n"+persistLine)
		} else {
			// Update or append
			found := false
			var newLines []string
			for _, line := range strings.Split(existing, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), key+" ") || strings.HasPrefix(strings.TrimSpace(line), key+"=") {
					newLines = append(newLines, persistLine)
					found = true
				} else {
					newLines = append(newLines, line)
				}
			}
			if !found {
				newLines = append(newLines, persistLine)
			}
			content := strings.Join(newLines, "\n")
			// Clean up double newlines
			for strings.Contains(content, "\n\n\n") {
				content = strings.ReplaceAll(content, "\n\n\n", "\n\n")
			}
			common.SafeWriteFile(confPath, content)
		}

		common.JSONResponse(w, map[string]interface{}{
			"message": fmt.Sprintf("%s 已设置为 %s（已持久化）", key, value),
		})
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

// ═══════════════════════════════════════
// 系统更新
// ═══════════════════════════════════════

func getUpdateInfo() map[string]interface{} {
	info := map[string]interface{}{}

	// Count upgradable packages
	out, _ := common.SudoOutput("bash", "-c", "apt list --upgradable 2>/dev/null | grep -c upgradable || echo 0")
	count := 0
	fmt.Sscanf(strings.TrimSpace(out), "%d", &count)
	info["upgradable_count"] = count

	// Last update time
	if data, err := os.ReadFile("/var/log/apt/history.log"); err == nil {
		lines := strings.Split(string(data), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			if strings.HasPrefix(lines[i], "Start-Date:") {
				info["last_update"] = strings.TrimPrefix(lines[i], "Start-Date: ")
				break
			}
		}
	}

	return info
}

func handleUpdates(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		common.JSONResponse(w, getUpdateInfo())
		return
	}

	if r.Method == http.MethodPost {
		action := r.FormValue("action")
		if action == "update" {
			// apt update only (refresh package list)
			out, err := common.SudoExec("apt", "update", "-qq")
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":%q}`, out), http.StatusInternalServerError)
				return
			}
			common.JSONResponse(w, map[string]interface{}{
				"message": "软件包列表已刷新",
			})
			return
		}
		if action == "upgrade" {
			// apt upgrade (this can take a while)
			out, err := common.SudoExec("apt", "upgrade", "-y", "-qq")
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":%q}`, out), http.StatusInternalServerError)
				return
			}
			common.JSONResponse(w, map[string]interface{}{
				"message": "系统更新完成",
				"output":  out,
			})
			return
		}
		http.Error(w, `{"error":"action must be update or upgrade"}`, http.StatusBadRequest)
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

// ═══════════════════════════════════════
// 服务管理 — 条目化
// ═══════════════════════════════════════

type ServiceInfo struct {
	Name    string `json:"name"`
	Display string `json:"display"`
	Active  bool   `json:"active"`
	Enabled bool   `json:"enabled"`
}

func getServiceList() []ServiceInfo {
	// NAS core services to show
	services := []struct{ name, display string }{
		{"smbd", "Samba 文件共享"},
		{"nmbd", "NetBIOS 名称解析"},
		{"vsftpd", "FTP 服务"},
		{"nfs-server", "NFS 服务"},
		{"nas-panel", "NAS 管理面板"},
		{"fail2ban", "入侵防护"},
		{"cron", "定时任务"},
		{"ssh", "SSH 远程登录"},
		{"filebrowser", "网页文件管理"},
	}

	var result []ServiceInfo
	for _, svc := range services {
		active := false
		enabled := false

		out, _ := common.ExecOutput("systemctl", "is-active", svc.name)
		if strings.TrimSpace(out) == "active" {
			active = true
		}

		out, _ = common.ExecOutput("systemctl", "is-enabled", svc.name)
		if strings.TrimSpace(out) == "enabled" {
			enabled = true
		}

		result = append(result, ServiceInfo{
			Name:    svc.name,
			Display: svc.display,
			Active:  active,
			Enabled: enabled,
		})
	}
	return result
}

func handleServices(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		common.JSONResponse(w, map[string]interface{}{
			"services": getServiceList(),
		})
		return
	}

	if r.Method == http.MethodPost {
		name := r.FormValue("name")
		action := r.FormValue("action") // start, stop, restart, enable, disable
		if name == "" || action == "" {
			http.Error(w, `{"error":"name and action required"}`, http.StatusBadRequest)
			return
		}

		// Whitelist services
		allowed := map[string]bool{
			"smbd": true, "nmbd": true, "vsftpd": true, "nfs-server": true,
			"nas-panel": true, "fail2ban": true, "cron": true, "ssh": true,
			"filebrowser": true,
		}
		if !allowed[name] {
			http.Error(w, `{"error":"不允许操作此服务"}`, http.StatusForbidden)
			return
		}

		// Whitelist actions
		allowedActions := map[string]bool{
			"start": true, "stop": true, "restart": true, "enable": true, "disable": true,
		}
		if !allowedActions[action] {
			http.Error(w, `{"error":"无效操作"}`, http.StatusBadRequest)
			return
		}

		out, err := common.SudoExec("systemctl", action, name)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, out+": "+err.Error()), http.StatusInternalServerError)
			return
		}

		actionText := map[string]string{
			"start": "启动", "stop": "停止", "restart": "重启",
			"enable": "启用自启", "disable": "禁用自启",
		}
		common.JSONResponse(w, map[string]interface{}{
			"message": fmt.Sprintf("服务 %s 已%s", name, actionText[action]),
		})
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

// ═══════════════════════════════════════
// 恢复出厂设置
// ═══════════════════════════════════════

var (
	resetRunning bool
	resetMu      sync.Mutex
)

func handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	confirm := r.FormValue("confirm")
	if confirm != "yes" {
		http.Error(w, `{"error":"请加 confirm=yes 确认重置"}`, http.StatusBadRequest)
		return
	}

	resetMu.Lock()
	if resetRunning {
		resetMu.Unlock()
		http.Error(w, `{"error":"重置正在进行中，请等待完成"}`, http.StatusConflict)
		return
	}
	resetRunning = true
	resetMu.Unlock()

	// 立即返回，后台执行重置
	go func() {
		defer func() {
			resetMu.Lock()
			resetRunning = false
			resetMu.Unlock()
		}()

		// 1. 备份当前配置
		backupDir := "/data/backups"
		common.SudoExec("mkdir", "-p", backupDir)
		timestamp := time.Now().Format("20060102-150405")
		for _, f := range []string{"/etc/samba/smb.conf", "/etc/exports", "/etc/nfs.conf", "/etc/vsftpd.conf", "/etc/fstab", "/etc/mdadm/mdadm.conf"} {
			common.SudoExec("cp", f, backupDir+"/"+filepath.Base(f)+".reset-"+timestamp)
		}

		// 2. 卸载所有数据目录
		dataMounts := getDataMounts()
		for _, m := range dataMounts {
			mount := m["mount"]
			if mount == "/data" || isDataNasMount(mount) {
				common.SudoExec("umount", "-l", mount)
			}
		}

		// 3. 清理 fstab
		fstabData, _ := common.SudoOutput("cat", "/etc/fstab")
		if fstabData != "" {
			lines := strings.Split(fstabData, "\n")
			var newLines []string
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || strings.HasPrefix(trimmed, "#") {
					newLines = append(newLines, line)
					continue
				}
				fields := strings.Fields(trimmed)
				if len(fields) >= 2 {
					mountPoint := fields[1]
					if mountPoint == "/data" || isDataNasMount(mountPoint) {
						continue
					}
				}
				newLines = append(newLines, line)
			}
			content := strings.Join(newLines, "\n")
			if content != fstabData {
				common.SafeWriteFile("/etc/fstab", content)
			}
		}

		// 4. 删除 LVM（逻辑卷 → 卷组 → 物理卷）
		vgsOut, _ := common.SudoOutput("/usr/sbin/vgs", "--noheadings", "-o", "vg_name")
		vgsOut = strings.TrimSpace(vgsOut)
		if vgsOut != "" {
			for _, vgName := range strings.Fields(vgsOut) {
				vgName = strings.TrimSpace(vgName)
				if vgName == "" {
					continue
				}
				lvsOut, _ := common.SudoOutput("/usr/sbin/lvs", "--noheadings", "-o", "lv_name", vgName)
				for _, lvName := range strings.Fields(lvsOut) {
					common.SudoExec("/usr/sbin/lvremove", "-f", "/dev/"+vgName+"/"+lvName)
				}
				common.SudoExec("/usr/sbin/vgremove", "-f", vgName)
			}
		}
		pvsOut, _ := common.SudoOutput("/usr/sbin/pvs", "--noheadings", "-o", "pv_name")
		for _, pvName := range strings.Fields(pvsOut) {
			if strings.HasPrefix(pvName, "/dev/") {
				common.SudoExec("/usr/sbin/pvremove", "-f", pvName)
			}
		}

		// 5. 停止 RAID 并清除超级块
		mdMatches, _ := filepath.Glob("/dev/md[0-9]*")
		mdRe := regexp.MustCompile(`^/dev/md\d+$`)
		for _, dev := range mdMatches {
			if mdRe.MatchString(dev) {
				common.SudoExec("/usr/sbin/mdadm", "--stop", dev)
			}
		}
		dataDisks := getDataDiskDevices()
		for _, dev := range dataDisks {
			common.SudoExec("/usr/sbin/mdadm", "--zero-superblock", dev)
		}
		common.SafeWriteFile("/etc/mdadm/mdadm.conf", "")
		common.SudoExec("update-initramfs", "-u")

		// 6. 清除磁盘签名
		for _, dev := range dataDisks {
			common.SudoExec("/usr/sbin/wipefs", "-a", dev)
		}

		// 7. 删除面板数据库和配置同步状态
		common.SudoExec("rm", "-f", "/opt/nas/data/folders.db")
		common.SudoExec("rm", "-f", "/opt/nas/data/.last_reload")

		// 8. 恢复 Samba 配置
		smbConf, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")
		lines := strings.Split(smbConf, "\n")
		var newSmb []string
		skip := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "# === Z1 MANAGED SHARES START ===" {
				skip = true
				continue
			}
			if trimmed == "# === Z1 MANAGED SHARES END ===" && skip {
				skip = false
				continue
			}
			if !skip {
				newSmb = append(newSmb, line)
			}
		}
		for len(newSmb) > 0 && strings.TrimSpace(newSmb[len(newSmb)-1]) == "" {
			newSmb = newSmb[:len(newSmb)-1]
		}
		common.SafeWriteFile("/etc/samba/smb.conf", strings.Join(newSmb, "\n")+"\n")
		common.SudoExec("systemctl", "reset-failed", "smbd", "nmbd")
		common.SudoExec("systemctl", "restart", "smbd", "nmbd")

		// 9. 恢复 NFS exports
		exports, _ := common.SudoOutput("cat", "/etc/exports")
		lines = strings.Split(exports, "\n")
		var newExports []string
		skip = false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "# === Z1 MANAGED SHARES START ===" {
				skip = true
				continue
			}
			if trimmed == "# === Z1 MANAGED SHARES END ===" && skip {
				skip = false
				continue
			}
			if !skip {
				newExports = append(newExports, line)
			}
		}
		for len(newExports) > 0 && strings.TrimSpace(newExports[len(newExports)-1]) == "" {
			newExports = newExports[:len(newExports)-1]
		}
		common.SafeWriteFile("/etc/exports", strings.Join(newExports, "\n")+"\n")
		common.SudoExec("exportfs", "-a")
		common.SudoExec("systemctl", "reset-failed", "nfs-kernel-server")
		common.SudoExec("systemctl", "restart", "nfs-kernel-server")

		// 10. 重建 /data 目录
		common.SudoExec("mkdir", "-p", "/data")
		nasUser, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
		if nasUser == "" {
			nasUser = os.Getenv("NAS_USER")
		}
		if nasUser == "" {
			nasUser = "root"
		}
		common.SudoExec("chown", "-R", nasUser+":"+nasUser, "/data")

		// 更新注册表
		ResetRegistryAfterFactoryReset()
	}()

	common.JSONResponse(w, map[string]interface{}{
		"status":  "running",
		"message": "恢复出厂设置已开始，正在后台执行...",
	})
}

// getDataMounts returns mounts under /data from df
func getDataMounts() []map[string]string {
	var mounts []map[string]string
	out, _ := common.ExecOutput("df", "-h", "--output=source,size,used,avail,pcent,target")
	for _, line := range strings.Split(out, "\n")[1:] {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "/data") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 6 {
			mounts = append(mounts, map[string]string{
				"device":  fields[0],
				"size":    fields[1],
				"used":    fields[2],
				"avail":   fields[3],
				"percent": fields[4],
				"mount":   fields[5],
			})
		}
	}
	return mounts
}

// isDataNasMount checks if a path matches /data/nasN pattern
func isDataNasMount(path string) bool {
	matched, _ := regexp.MatchString(`^/data/nas\d+$`, path)
	return matched
}

// getDataDiskDevices returns non-system block devices (excludes sr0, loop, ram, zram, and root disk)
func getDataDiskDevices() []string {
	var result []string
	// Use lsblk to list all disks
	out, _ := common.ExecOutput("lsblk", "-nd", "-o", "NAME,TYPE,MOUNTPOINT")
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		devType := fields[1]

		// Skip non-disk devices
		if devType != "disk" {
			continue
		}
		// Skip virtual devices
		if name == "sr0" || strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "zram") {
			continue
		}
		dev := "/dev/" + name

		// Skip system disk: check if any partition of this disk is mounted as / or /boot
		partOut, _ := common.ExecOutput("lsblk", "-nlo", "MOUNTPOINT", dev)
		isSystem := false
		for _, mp := range strings.Split(partOut, "\n") {
			mp = strings.TrimSpace(mp)
			if mp == "/" || mp == "/boot" || mp == "/boot/efi" {
				isSystem = true
				break
			}
		}
		if isSystem {
			continue
		}
		result = append(result, dev)
	}
	return result
}

// ═══════════════════════════════════════
// 系统配置检查 API
// ═══════════════════════════════════════

func handleSystemCheck(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("action")
	if action == "refresh" {
		report := RefreshRegistry()
		common.JSONResponse(w, report)
		return
	}
	report := GetRegistry()
	common.JSONResponse(w, report)
}
