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

	service := r.FormValue("service")
	steps := []string{}

	if service != "" {
		// Install single service
		msg, err := installSingleService(service)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
		steps = append(steps, msg)
	} else {
		// Install all services
		out, err := common.SudoExec("apt-get", "update", "-qq")
		if err == nil {
			steps = append(steps, "apt update 完成")
		}

		out, err = common.SudoExec("apt-get", "install", "-y", "-qq",
			"samba", "nfs-kernel-server", "vsftpd", "rclone", "fail2ban", "apache2-utils")
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"安装失败: %s"}`, out+": "+err.Error()), http.StatusInternalServerError)
			return
		}
		steps = append(steps, "apt-get 安装完成")

		for _, svc := range []string{"smbd", "nmbd", "nfs-kernel-server", "vsftpd", "fail2ban"} {
			common.SudoExec("systemctl", "enable", svc)
			common.SudoExec("systemctl", "reset-failed", svc)
			common.SudoExec("systemctl", "start", svc)
			steps = append(steps, svc+" 已启动")
		}

		// Read NAS user/pass from .env for WebDAV/S3 auth
		nasUser, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
		nasPass, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_PASS")

		// WebDAV with htpasswd auth
		common.SudoExec("bash", "-c", fmt.Sprintf("htpasswd -cb /etc/rclone-htpasswd %s %s", nasUser, nasPass))
		common.SudoExec("sh", "-c", "cat > /etc/systemd/system/rclone-webdav.service << 'UNIT'\n[Unit]\nDescription=Rclone WebDAV Server\nAfter=network.target\n[Service]\nType=simple\nExecStart=/usr/bin/rclone serve webdav /data --addr :8080 --htpasswd /etc/rclone-htpasswd\nRestart=on-failure\n[Install]\nWantedBy=multi-user.target\nUNIT")
		common.SudoExec("systemctl", "daemon-reload")
		common.SudoExec("systemctl", "enable", "rclone-webdav")
		common.SudoExec("systemctl", "reset-failed", "rclone-webdav")
		common.SudoExec("systemctl", "start", "rclone-webdav")
		steps = append(steps, "WebDAV 已启动")

		// S3 with auth-key
		common.SudoExec("sh", "-c", fmt.Sprintf("cat > /etc/systemd/system/rclone-s3.service << 'UNIT'\n[Unit]\nDescription=Rclone S3 Server\nAfter=network.target\n[Service]\nType=simple\nExecStart=/usr/bin/rclone serve s3 /data --addr :9000 --auth-key %s,%s\nRestart=on-failure\n[Install]\nWantedBy=multi-user.target\nUNIT", nasUser, nasPass))
		common.SudoExec("systemctl", "enable", "rclone-s3")
		common.SudoExec("systemctl", "reset-failed", "rclone-s3")
		common.SudoExec("systemctl", "start", "rclone-s3")
		steps = append(steps, "S3 已启动")

		// FileBrowser (v2.63.17 with full init)
		out, _ = common.SudoExec("bash", "-c", `
			ARCH=$(uname -m | sed "s/x86_64/amd64/;s/aarch64/arm64/")
			DL_ERR=""
			curl -fsSL --connect-timeout 10 --max-time 600 "https://github.com/filebrowser/filebrowser/releases/download/v2.63.17/linux-${ARCH}-filebrowser.tar.gz" -o /tmp/fb.tar.gz 2>/tmp/fb.err || DL_ERR="github: $(cat /tmp/fb.err)"
			if [ ! -f /tmp/fb.tar.gz ]; then
				curl -fsSL --connect-timeout 10 --max-time 30 "https://get.z1.sale/filebroswer/linux-${ARCH}-filebrowser.tar.gz" -o /tmp/fb.tar.gz 2>/tmp/fb.err || DL_ERR="${DL_ERR}; get.z1.sale: $(cat /tmp/fb.err)"
			fi
			if [ ! -f /tmp/fb.tar.gz ]; then
				curl -fsSL --connect-timeout 10 --max-time 30 "https://file.abwen.com/control/filebrowser_v2.63.17_linux_${ARCH}.tar.gz" -o /tmp/fb.tar.gz 2>/tmp/fb.err || DL_ERR="${DL_ERR}; file.abwen.com: $(cat /tmp/fb.err)"
			fi
			if [ -f /tmp/fb.tar.gz ]; then tar xzf /tmp/fb.tar.gz -C /usr/local/bin filebrowser && chmod +x /usr/local/bin/filebrowser && echo "ok"; else echo "FAIL:${DL_ERR}"; fi
		`)
		if strings.TrimSpace(out) == "ok" {
			common.SudoExec("mkdir", "-p", "/etc/filebrowser")
			common.SudoExec("/usr/local/bin/filebrowser", "config", "init", "--database", "/etc/filebrowser/filebrowser.db")
			common.SudoExec("/usr/local/bin/filebrowser", "config", "set", "--database", "/etc/filebrowser/filebrowser.db", "--address", "0.0.0.0", "--port", "8081", "--root", "/data", "--log", "/var/log/filebrowser.log")
			common.SudoExec("/usr/local/bin/filebrowser", "users", "add", nasUser, nasPass, "--database", "/etc/filebrowser/filebrowser.db", "--perm.admin")
			common.SudoExec("sh", "-c", "cat > /etc/systemd/system/filebrowser.service << 'UNIT'\n[Unit]\nDescription=FileBrowser\nAfter=network.target\n[Service]\nType=simple\nExecStart=/usr/local/bin/filebrowser --database /etc/filebrowser/filebrowser.db\nRestart=on-failure\n[Install]\nWantedBy=multi-user.target\nUNIT")
			common.SudoExec("systemctl", "daemon-reload")
			common.SudoExec("systemctl", "enable", "filebrowser")
			common.SudoExec("systemctl", "reset-failed", "filebrowser")
			common.SudoExec("systemctl", "start", "filebrowser")
			steps = append(steps, "FileBrowser 已启动")
		} else {
			steps = append(steps, "FileBrowser 下载失败")
		}
	}

	common.JSONResponse(w, map[string]interface{}{
		"message": fmt.Sprintf("安装完成，共 %d 步", len(steps)),
		"steps":   steps,
	})
}

func installSingleService(name string) (string, error) {
	switch name {
	case "smbd", "nmbd":
		out, err := common.SudoExec("apt-get", "install", "-y", "-qq", "samba")
		if err != nil {
			return "", fmt.Errorf("samba install failed: %s", out)
		}
		common.SudoExec("systemctl", "enable", "smbd", "nmbd")
		common.SudoExec("systemctl", "reset-failed", "smbd", "nmbd")
		common.SudoExec("systemctl", "start", "smbd", "nmbd")
		return "Samba 已安装并启动", nil

	case "nfs-kernel-server":
		out, err := common.SudoExec("apt-get", "install", "-y", "-qq", "nfs-kernel-server")
		if err != nil {
			return "", fmt.Errorf("nfs install failed: %s", out)
		}
		common.SudoExec("systemctl", "enable", "nfs-kernel-server")
		common.SudoExec("systemctl", "reset-failed", "nfs-kernel-server")
		common.SudoExec("systemctl", "start", "nfs-kernel-server")
		return "NFS 已安装并启动", nil

	case "vsftpd":
		out, err := common.SudoExec("apt-get", "install", "-y", "-qq", "vsftpd")
		if err != nil {
			return "", fmt.Errorf("ftp install failed: %s", out)
		}
		common.SudoExec("systemctl", "enable", "vsftpd")
		common.SudoExec("systemctl", "reset-failed", "vsftpd")
		common.SudoExec("systemctl", "start", "vsftpd")
		return "FTP 已安装并启动", nil

	case "rclone-webdav":
		out, err := common.SudoExec("apt-get", "install", "-y", "-qq", "rclone", "apache2-utils")
		if err != nil {
			return "", fmt.Errorf("rclone install failed: %s", out)
		}
		// Read NAS user/pass from .env
		nasUser, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
		nasPass, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_PASS")
		common.SudoExec("bash", "-c", fmt.Sprintf("htpasswd -cb /etc/rclone-htpasswd %s %s", nasUser, nasPass))
		common.SudoExec("sh", "-c", "cat > /etc/systemd/system/rclone-webdav.service << 'UNIT'\n[Unit]\nDescription=Rclone WebDAV Server\nAfter=network.target\n[Service]\nType=simple\nExecStart=/usr/bin/rclone serve webdav /data --addr :8080 --htpasswd /etc/rclone-htpasswd\nRestart=on-failure\n[Install]\nWantedBy=multi-user.target\nUNIT")
		common.SudoExec("systemctl", "daemon-reload")
		common.SudoExec("systemctl", "enable", "rclone-webdav")
		common.SudoExec("systemctl", "reset-failed", "rclone-webdav")
		common.SudoExec("systemctl", "start", "rclone-webdav")
		return "WebDAV 已安装并启动", nil

	case "rclone-s3":
		out, err := common.SudoExec("apt-get", "install", "-y", "-qq", "rclone")
		if err != nil {
			return "", fmt.Errorf("rclone install failed: %s", out)
		}
		nasUser, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
		nasPass, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_PASS")
		common.SudoExec("sh", "-c", fmt.Sprintf("cat > /etc/systemd/system/rclone-s3.service << 'UNIT'\n[Unit]\nDescription=Rclone S3 Server\nAfter=network.target\n[Service]\nType=simple\nExecStart=/usr/bin/rclone serve s3 /data --addr :9000 --auth-key %s,%s\nRestart=on-failure\n[Install]\nWantedBy=multi-user.target\nUNIT", nasUser, nasPass))
		common.SudoExec("systemctl", "daemon-reload")
		common.SudoExec("systemctl", "enable", "rclone-s3")
		common.SudoExec("systemctl", "reset-failed", "rclone-s3")
		common.SudoExec("systemctl", "start", "rclone-s3")
		return "S3 已安装并启动", nil

	case "filebrowser":
		nasUser, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
		nasPass, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_PASS")
		out, _ := common.SudoExec("bash", "-c", `
			ARCH=$(uname -m | sed "s/x86_64/amd64/;s/aarch64/arm64/")
			DL_ERR=""
			curl -fsSL --connect-timeout 10 --max-time 600 "https://github.com/filebrowser/filebrowser/releases/download/v2.63.17/linux-${ARCH}-filebrowser.tar.gz" -o /tmp/fb.tar.gz 2>/tmp/fb.err || DL_ERR="github: $(cat /tmp/fb.err)"
			if [ ! -f /tmp/fb.tar.gz ]; then
				curl -fsSL --connect-timeout 10 --max-time 30 "https://get.z1.sale/filebroswer/linux-${ARCH}-filebrowser.tar.gz" -o /tmp/fb.tar.gz 2>/tmp/fb.err || DL_ERR="${DL_ERR}; get.z1.sale: $(cat /tmp/fb.err)"
			fi
			if [ ! -f /tmp/fb.tar.gz ]; then
				curl -fsSL --connect-timeout 10 --max-time 30 "https://file.abwen.com/control/filebrowser_v2.63.17_linux_${ARCH}.tar.gz" -o /tmp/fb.tar.gz 2>/tmp/fb.err || DL_ERR="${DL_ERR}; file.abwen.com: $(cat /tmp/fb.err)"
			fi
			if [ -f /tmp/fb.tar.gz ]; then tar xzf /tmp/fb.tar.gz -C /usr/local/bin filebrowser && chmod +x /usr/local/bin/filebrowser && echo "ok"; else echo "FAIL:${DL_ERR}"; fi
		`)
		if strings.TrimSpace(out) != "ok" {
			errMsg := "FileBrowser download failed"
			if strings.HasPrefix(strings.TrimSpace(out), "FAIL:") {
				errMsg = strings.TrimPrefix(strings.TrimSpace(out), "FAIL:")
			}
			return "", fmt.Errorf("%s", errMsg)
		}
		// Initialize FileBrowser
		common.SudoExec("mkdir", "-p", "/etc/filebrowser")
		common.SudoExec("/usr/local/bin/filebrowser", "config", "init", "--database", "/etc/filebrowser/filebrowser.db")
		common.SudoExec("/usr/local/bin/filebrowser", "config", "set", "--database", "/etc/filebrowser/filebrowser.db", "--address", "0.0.0.0", "--port", "8081", "--root", "/data", "--log", "/var/log/filebrowser.log")
		common.SudoExec("/usr/local/bin/filebrowser", "users", "add", nasUser, nasPass, "--database", "/etc/filebrowser/filebrowser.db", "--perm.admin")
		common.SudoExec("sh", "-c", "cat > /etc/systemd/system/filebrowser.service << 'UNIT'\n[Unit]\nDescription=FileBrowser\nAfter=network.target\n[Service]\nType=simple\nExecStart=/usr/local/bin/filebrowser --database /etc/filebrowser/filebrowser.db\nRestart=on-failure\n[Install]\nWantedBy=multi-user.target\nUNIT")
		common.SudoExec("systemctl", "daemon-reload")
		common.SudoExec("systemctl", "enable", "filebrowser")
		common.SudoExec("systemctl", "reset-failed", "filebrowser")
		common.SudoExec("systemctl", "start", "filebrowser")
		return "FileBrowser 已安装并启动", nil

	case "fail2ban":
		out, err := common.SudoExec("apt-get", "install", "-y", "-qq", "fail2ban")
		if err != nil {
			return "", fmt.Errorf("fail2ban install failed: %s", out)
		}
		common.SudoExec("systemctl", "enable", "fail2ban")
		common.SudoExec("systemctl", "reset-failed", "fail2ban")
		common.SudoExec("systemctl", "start", "fail2ban")
		return "Fail2ban 已安装并启动", nil

	default:
		return "", fmt.Errorf("unknown service: %s", name)
	}
}
