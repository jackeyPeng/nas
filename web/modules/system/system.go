package system

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"nas-panel/common"
)

// RegisterRoutes registers system settings routes
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/system/network", common.AuthMiddleware(handleNetwork))
	mux.HandleFunc("/api/system/time", common.AuthMiddleware(handleTime))
	mux.HandleFunc("/api/system/hostname", common.AuthMiddleware(handleHostname))
	mux.HandleFunc("/api/system/ssh-config", common.AuthMiddleware(handleSSHConfig))
	mux.HandleFunc("/api/system/sysctl", common.AuthMiddleware(handleSysctl))
	mux.HandleFunc("/api/system/updates", common.AuthMiddleware(handleUpdates))
	mux.HandleFunc("/api/system/services-enabled", common.AuthMiddleware(handleEnabledServices))
}

// handleNetwork returns network configuration
func handleNetwork(w http.ResponseWriter, r *http.Request) {
	var result string
	// IP addresses
	if out, err := common.ExecOutput("ip", "addr"); err == nil {
		result += "=== IP 地址 ===\n" + out + "\n\n"
	}
	// Routing table
	if out, err := common.ExecOutput("ip", "route"); err == nil {
		result += "=== 路由表 ===\n" + out + "\n\n"
	}
	// DNS
	if data, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		result += "=== DNS 配置 ===\n" + string(data) + "\n"
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(result))
}

// handleTime returns system time and timezone
func handleTime(w http.ResponseWriter, r *http.Request) {
	var result string
	// Current time
	if out, err := common.Exec("date"); err == nil {
		result += "当前时间: " + out + "\n"
	}
	// Timezone
	if out, err := common.Exec("timedatectl"); err == nil {
		result += out + "\n"
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(result))
}

// handleHostname returns hostname (GET) or sets it (POST)
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

// handleSSHConfig returns SSH configuration
func handleSSHConfig(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile("/etc/ssh/sshd_config")
	if err != nil {
		// Try with sudo
		out, _ := common.SudoOutput("cat", "/etc/ssh/sshd_config")
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(out))
		return
	}
	// Filter comments for brevity
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lines = append(lines, line)
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(strings.Join(lines, "\n")))
}

// handleSysctl returns key kernel parameters
func handleSysctl(w http.ResponseWriter, r *http.Request) {
	keys := []string{
		"net.ipv4.ip_forward",
		"net.core.somaxconn",
		"vm.swappiness",
		"vm.dirty_ratio",
		"vm.dirty_background_ratio",
		"net.ipv4.tcp_congestion_control",
		"net.core.default_qdisc",
		"fs.file-max",
	}
	var result string
	for _, key := range keys {
		out, _ := exec.Command("sysctl", key).Output()
		result += string(out)
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(result))
}

// handleUpdates returns system update status
func handleUpdates(w http.ResponseWriter, r *http.Request) {
	var result string
	// Check for available updates
	out, _ := common.SudoOutput("apt", "list", "--upgradable", "2>/dev/null")
	if out != "" {
		result += "=== 可升级包 ===\n" + out + "\n\n"
	}
	// Last upgrade time
	if data, err := os.ReadFile("/var/log/apt/history.log"); err == nil {
		lines := strings.Split(string(data), "\n")
		start := len(lines)
		if start > 20 {
			start = len(lines) - 20
		}
		result += "=== 最近更新记录 ===\n" + strings.Join(lines[start:], "\n")
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(result))
}

// handleEnabledServices returns list of enabled systemd services
func handleEnabledServices(w http.ResponseWriter, r *http.Request) {
	out, _ := common.SudoOutput("systemctl", "list-unit-files", "--type=service", "--state=enabled", "--no-pager")
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(out))
}
