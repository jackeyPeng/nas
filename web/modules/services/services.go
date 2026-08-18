package services

import (
	"fmt"
	"net/http"
	"strings"

	"nas-panel/common"
	"nas-panel/modules/dashboard"
)

// controlService performs start/stop/restart on a service
func controlService(name, action string) (string, error) {
	// Validate service name is in our list
	valid := false
	for _, svc := range dashboard.NasServices {
		if svc.Name == name {
			valid = true
			break
		}
	}
	if !valid {
		return "", fmt.Errorf("unknown service: %s", name)
	}

	out, err := common.SudoExec("systemctl", action, name)
	if err != nil {
		return out, err
	}
	return out, nil
}

// getServiceLogs returns recent journal logs for a service
func getServiceLogs(name string) string {
	out, err := common.ExecOutput("journalctl", "-u", name, "-n", "50", "--no-pager")
	if err != nil {
		return ""
	}
	return out
}

// handleServiceList returns all services with status
func handleServiceList(w http.ResponseWriter, r *http.Request) {
	services := dashboard.GetServices()
	common.JSONResponse(w, map[string]interface{}{
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
		common.JSONResponse(w, map[string]interface{}{
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

// RegisterRoutes registers services routes
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/services", common.AuthMiddleware(handleServiceList))
	mux.HandleFunc("/api/services/", common.AuthMiddleware(handleServiceAction))
	mux.HandleFunc("/api/services/install", common.AuthMiddleware(handleInstallServices))
}

// handleInstallServices installs all NAS services
func handleInstallServices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	steps := []string{}

	out, err := common.SudoExec("apt-get", "update", "-qq")
	if err != nil {
		steps = append(steps, "apt update 完成")
	}

	out, err = common.SudoExec("apt-get", "install", "-y", "-qq",
		"samba", "nfs-kernel-server", "vsftpd", "rclone", "fail2ban")
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"安装失败: %s"}`, out+": "+err.Error()), http.StatusInternalServerError)
		return
	}
	steps = append(steps, "apt-get 安装完成")

	// Enable and start services
	for _, svc := range []string{"smbd", "nmbd", "nfs-kernel-server", "vsftpd", "fail2ban"} {
		common.SudoExec("systemctl", "enable", svc)
		common.SudoExec("systemctl", "start", svc)
		steps = append(steps, svc+" 已启动")
	}

	// WebDAV
	common.SudoExec("sh", "-c", "cat > /etc/systemd/system/rclone-webdav.service << 'UNIT'\n[Unit]\nDescription=Rclone WebDAV Server\nAfter=network.target\n[Service]\nType=simple\nExecStart=/usr/bin/rclone serve webdav /data --addr :8080\nRestart=on-failure\n[Install]\nWantedBy=multi-user.target\nUNIT")
	common.SudoExec("systemctl", "daemon-reload")
	common.SudoExec("systemctl", "enable", "rclone-webdav")
	common.SudoExec("systemctl", "start", "rclone-webdav")
	steps = append(steps, "WebDAV 已启动")

	// S3
	common.SudoExec("sh", "-c", "cat > /etc/systemd/system/rclone-s3.service << 'UNIT'\n[Unit]\nDescription=Rclone S3 Server\nAfter=network.target\n[Service]\nType=simple\nExecStart=/usr/bin/rclone serve s3 /data --addr :9000\nRestart=on-failure\n[Install]\nWantedBy=multi-user.target\nUNIT")
	common.SudoExec("systemctl", "daemon-reload")
	common.SudoExec("systemctl", "enable", "rclone-s3")
	common.SudoExec("systemctl", "start", "rclone-s3")
	steps = append(steps, "S3 已启动")

	common.JSONResponse(w, map[string]interface{}{
		"message": fmt.Sprintf("服务安装完成，共 %d 步", len(steps)),
		"steps":   steps,
	})
}
