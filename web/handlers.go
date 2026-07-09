package main

import (
	"fmt"
	"net/http"
	"strings"
)

// handleDashboard returns system overview
func handleDashboard(w http.ResponseWriter, r *http.Request) {
	info := getSystemInfo()
	services := getServices()

	activeCount := 0
	for _, svc := range services {
		if svc["active"] == "active" {
			activeCount++
		}
	}

	response := map[string]interface{}{
		"hostname":      info.Hostname,
		"os":            info.OS,
		"kernel":        info.Kernel,
		"uptime":        info.Uptime,
		"cpu_usage":     info.CPUUsage,
		"cpu_cores":     info.CPUCores,
		"mem_total":     info.MemTotal,
		"mem_used":      info.MemUsed,
		"mem_pct":       info.MemPct,
		"disk_total":    info.DiskTotal,
		"disk_used":     info.DiskUsed,
		"disk_pct":      info.DiskPct,
		"services":      services,
		"active_count":  activeCount,
		"total_services": len(services),
	}

	jsonResponse(w, response)
}

// handleServiceList returns all services with status
func handleServiceList(w http.ResponseWriter, r *http.Request) {
	services := getServices()
	jsonResponse(w, map[string]interface{}{
		"services": services,
	})
}

// handleServiceAction handles start/stop/restart/logs
func handleServiceAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/services/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.Error(w, `{"error": "invalid path"}`, http.StatusBadRequest)
		return
	}

	svcName := parts[0]
	action := parts[1]

	switch action {
	case "start", "stop", "restart":
		output, err := controlService(svcName, action)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		jsonResponse(w, map[string]interface{}{
			"message": fmt.Sprintf("%s %s: %s", svcName, action, output),
			"output":  output,
		})

	case "logs":
		logs := getServiceLogs(svcName)
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(logs))

	default:
		http.Error(w, `{"error": "unknown action"}`, http.StatusBadRequest)
	}
}

// handleUsers handles GET (list) and POST (create)
func handleUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		users := getUsers()
		jsonResponse(w, map[string]interface{}{
			"users": users,
		})
		return
	}

	if r.Method == http.MethodPost {
		username := r.FormValue("username")
		password := r.FormValue("password")
		if username == "" || password == "" {
			http.Error(w, `{"error": "username and password required"}`, http.StatusBadRequest)
			return
		}
		if len(password) < 12 {
			http.Error(w, `{"error": "password must be at least 12 characters"}`, http.StatusBadRequest)
			return
		}
		err := addUser(username, password)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		jsonResponse(w, map[string]interface{}{
			"message": "用户 " + username + " 添加成功",
		})
	}
}

// handleUserAction handles DELETE (remove) and PUT (password change)
func handleUserAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/users/")
	parts := strings.Split(path, "/")
	if len(parts) < 1 {
		http.Error(w, `{"error": "invalid path"}`, http.StatusBadRequest)
		return
	}

	username := parts[0]

	if len(parts) >= 2 && parts[1] == "password" {
		// Change password
		if r.Method != http.MethodPut && r.Method != http.MethodPost {
			http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		password := r.FormValue("password")
		if password == "" || len(password) < 12 {
			http.Error(w, `{"error": "password must be at least 12 characters"}`, http.StatusBadRequest)
			return
		}
		err := changePassword(username, password)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		jsonResponse(w, map[string]interface{}{
			"message": "密码修改成功",
		})
		return
	}

	// Delete user
	if r.Method == http.MethodDelete {
		deleteData := r.URL.Query().Get("delete_data") == "true"
		err := removeUser(username, deleteData)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		jsonResponse(w, map[string]interface{}{
			"message": "用户 " + username + " 已删除",
		})
		return
	}
}

// handleStorage returns storage information
func handleStorage(w http.ResponseWriter, r *http.Request) {
	info := getSystemInfo()
	dirs := getDirSizes()
	samba := getSambaShares()
	nfs := getNFSExports()

	jsonResponse(w, map[string]interface{}{
		"disk_total":    info.DiskTotal,
		"disk_used":     info.DiskUsed,
		"disk_pct":      info.DiskPct,
		"directories":   dirs,
		"samba_shares":  samba,
		"nfs_exports":   nfs,
	})
}

// handleSmart returns SMART status
func handleSmart(w http.ResponseWriter, r *http.Request) {
	smart := getSmartStatus()
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(smart))
}

// handleFirewall returns firewall status
func handleFirewall(w http.ResponseWriter, r *http.Request) {
	status := getFirewallStatus()
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(status))
}

// handleFirewallAllow allows a port
func handleFirewallAllow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	port := r.FormValue("port")
	proto := r.FormValue("proto")
	if proto == "" {
		proto = "tcp"
	}
	if port == "" {
		http.Error(w, `{"error": "port required"}`, http.StatusBadRequest)
		return
	}
	output, err := firewallAllow(port, proto)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]interface{}{
		"message": output,
	})
}

// handleFirewallDeny denies a port
func handleFirewallDeny(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	port := r.FormValue("port")
	proto := r.FormValue("proto")
	if proto == "" {
		proto = "tcp"
	}
	if port == "" {
		http.Error(w, `{"error": "port required"}`, http.StatusBadRequest)
		return
	}
	output, err := firewallDeny(port, proto)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]interface{}{
		"message": output,
	})
}

// handleMonitor returns current monitoring check results
func handleMonitor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	status := getMonitorStatus()
	jsonResponse(w, status)
}

// handleAlertConfig handles GET (read alert config) and POST (update alert config)
func handleAlertConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		config := getAlertConfig()
		jsonResponse(w, map[string]interface{}{
			"config": config,
		})
		return
	}

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, `{"error": "failed to parse form"}`, http.StatusBadRequest)
			return
		}

		values := map[string]string{}
		// Collect all ALERT_* form values
		for key, vals := range r.Form {
			if !strings.HasPrefix(key, "ALERT_") {
				continue
			}
			if len(vals) > 0 {
				values[key] = strings.TrimSpace(vals[0])
			}
		}

		if len(values) == 0 {
			http.Error(w, `{"error": "no ALERT_* values provided"}`, http.StatusBadRequest)
			return
		}

		if err := saveAlertConfig(values); err != nil {
			http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		jsonResponse(w, map[string]interface{}{
			"message": "告警配置已保存",
			"saved":   values,
		})
		return
	}

	http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
}
