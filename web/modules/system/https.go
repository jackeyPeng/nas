package system

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"nas-panel/common"
)

// CertStatus represents the current HTTPS certificate state
type CertStatus struct {
	Enabled  bool   `json:"enabled"`
	Type     string `json:"type"` // "none", "self-signed", "custom"
	Domain   string `json:"domain"`
	IssuedTo string `json:"issued_to"`
	Expires  string `json:"expires"`
	DaysLeft int    `json:"days_left"`
	Services []CertService `json:"services"`
}

// CertService shows which services have HTTPS enabled
type CertService struct {
	Name    string `json:"name"`
	Port    string `json:"port"`
	HTTPS   bool   `json:"https"`
}

const (
	certDir  = "/etc/nas/certs"
	certFile = "/etc/nas/certs/server.crt"
	keyFile  = "/etc/nas/certs/server.key"
)

// ═══════════════════════════════════════
// HTTPS 证书状态
// ═══════════════════════════════════════

func getCertStatus() CertStatus {
	status := CertStatus{
		Type: "none",
		Services: []CertService{
			{Name: "NAS 管理面板", Port: "8090"},
			{Name: "FileBrowser", Port: "8081"},
			{Name: "WebDAV", Port: "8080"},
			{Name: "S3 对象存储", Port: "9000"},
		},
	}

	// Check if cert exists
	if _, err := os.Stat(certFile); err != nil {
		return status
	}

	status.Enabled = true

	// Determine type
	metaFile := certDir + "/.meta"
	if meta, err := os.ReadFile(metaFile); err == nil {
		lines := strings.Split(string(meta), "\n")
		for _, line := range lines {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				switch strings.TrimSpace(parts[0]) {
				case "type":
					status.Type = strings.TrimSpace(parts[1])
				case "domain":
					status.Domain = strings.TrimSpace(parts[1])
				}
			}
		}
	}

	// Parse cert details
	out, err := common.SudoOutput("openssl", "x509", "-in", certFile, "-noout",
		"-subject", "-enddate")
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "subject=") {
				// Extract CN
				cn := extractCN(line)
				status.IssuedTo = cn
			}
			if strings.HasPrefix(line, "notAfter=") {
				dateStr := strings.TrimPrefix(line, "notAfter=")
				status.Expires = dateStr
				// Parse and calculate days left
				if t, err := time.Parse("Jan  2 15:04:05 2006 MST", dateStr); err == nil {
					status.DaysLeft = int(time.Until(t).Hours() / 24)
				}
			}
		}
	}

	// Check which services are using HTTPS (check systemd units for --cert/--key)
	for i, svc := range status.Services {
		status.Services[i].HTTPS = isServiceUsingHTTPS(svc.Name)
	}

	return status
}

func extractCN(subject string) string {
	// subject= /CN=nas.local
	parts := strings.Split(subject, "/CN=")
	if len(parts) > 1 {
		cn := strings.Split(parts[1], "/")[0]
		return strings.TrimSpace(cn)
	}
	return ""
}

func isServiceUsingHTTPS(displayName string) bool {
	var svcName string
	switch displayName {
	case "WebDAV":
		svcName = "rclone-webdav"
	case "S3 对象存储":
		svcName = "rclone-s3"
	case "FileBrowser":
		svcName = "filebrowser"
	case "NAS 管理面板":
		svcName = "nas-panel"
	default:
		return false
	}

	unitFile := "/etc/systemd/system/" + svcName + ".service"
	out, err := common.SudoOutput("cat", unitFile)
	if err != nil {
		return false
	}
	return strings.Contains(out, "--cert") || strings.Contains(out, "-t ") || strings.Contains(out, "TLS_CERT")
}

// ═══════════════════════════════════════
// API handlers
// ═══════════════════════════════════════

func handleHTTPSCert(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		common.JSONResponse(w, getCertStatus())
		return
	}
	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

func handleHTTPSGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	domain := r.FormValue("domain")
	if domain == "" {
		// Auto-detect: use hostname + .local
		hostname, _ := os.Hostname()
		domain = hostname + ".local"
	}

	// Create cert directory
	common.SudoExec("mkdir", "-p", certDir)

	// Generate self-signed certificate (valid 10 years)
	script := fmt.Sprintf(
		`openssl req -x509 -newkey rsa:2048 -keyout %s -out %s -days 3650 -nodes -subj "/CN=%s" 2>&1`,
		keyFile, certFile, domain,
	)
	out, err := common.SudoExec("bash", "-c", script)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"证书生成失败: %s"}`, out), http.StatusInternalServerError)
		return
	}

	// Set permissions
	common.SudoExec("chmod", "600", keyFile)
	common.SudoExec("chmod", "644", certFile)

	// Write metadata
	meta := fmt.Sprintf("type=self-signed\ndomain=%s\ncreated=%s\n", domain, time.Now().Format("2006-01-02 15:04:05"))
	common.SafeWriteFile(certDir+"/.meta", meta)

	common.JSONResponse(w, map[string]interface{}{
		"message": "自签名证书已生成，域名为 " + domain,
		"status":  getCertStatus(),
	})
}

func handleHTTPSUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	domain := r.FormValue("domain")
	certPEM := r.FormValue("cert")
	keyPEM := r.FormValue("key")

	if domain == "" || certPEM == "" || keyPEM == "" {
		http.Error(w, `{"error":"请提供 domain, cert, key 三个参数"}`, http.StatusBadRequest)
		return
	}

	// Validate cert
	common.SudoExec("mkdir", "-p", certDir)
	common.SafeWriteFile(certFile, certPEM)
	common.SafeWriteFile(keyFile, keyPEM)

	// Verify cert with openssl
	out, err := common.SudoOutput("openssl", "x509", "-in", certFile, "-noout", "-subject")
	if err != nil {
		common.SudoExec("rm", "-f", certFile, keyFile)
		http.Error(w, fmt.Sprintf(`{"error":"证书格式无效: %s"}`, out), http.StatusBadRequest)
		return
	}

	// Check key matches cert
	certMod, _ := common.SudoOutput("openssl", "x509", "-in", certFile, "-noout", "-modulus")
	keyMod, _ := common.SudoOutput("openssl", "rsa", "-in", keyFile, "-noout", "-modulus")
	if strings.TrimSpace(certMod) != strings.TrimSpace(keyMod) {
		common.SudoExec("rm", "-f", certFile, keyFile)
		http.Error(w, `{"error":"证书和私钥不匹配"}`, http.StatusBadRequest)
		return
	}

	// Set permissions
	common.SudoExec("chmod", "600", keyFile)
	common.SudoExec("chmod", "644", certFile)

	// Write metadata
	meta := fmt.Sprintf("type=custom\ndomain=%s\ncreated=%s\n", domain, time.Now().Format("2006-01-02 15:04:05"))
	common.SafeWriteFile(certDir+"/.meta", meta)

	common.JSONResponse(w, map[string]interface{}{
		"message": "证书已上传",
		"status":  getCertStatus(),
	})
}

func handleHTTPSApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Check cert exists
	if _, err := os.Stat(certFile); err != nil {
		http.Error(w, `{"error":"请先生成或上传证书"}`, http.StatusBadRequest)
		return
	}

	// Read domain from meta
	domain := ""
	if meta, err := os.ReadFile(certDir + "/.meta"); err == nil {
		for _, line := range strings.Split(string(meta), "\n") {
			parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
			if len(parts) == 2 && parts[0] == "domain" {
				domain = parts[1]
			}
		}
	}

	var results []string

	// 1. NAS Panel - use Go TLS
	results = append(results, applyPanelTLS()...)

	// 2. FileBrowser - use -t (cert) and -k (key) flags
	results = append(results, applyFileBrowserTLS()...)

	// 3. WebDAV (rclone) - use --cert and --key
	results = append(results, applyRcloneTLS("rclone-webdav", "8080")...)

	// 4. S3 (rclone) - use --cert and --key
	results = append(results, applyRcloneTLS("rclone-s3", "9000")...)

	// Open firewall port 443
	common.SudoExec("ufw", "allow", "443/tcp")

	common.JSONResponse(w, map[string]interface{}{
		"message": fmt.Sprintf("证书已应用到所有服务 (域名: %s)", domain),
		"details": results,
		"status":  getCertStatus(),
	})
}

func handleHTTPSRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Revert services to HTTP
	revertServiceTLS("nas-panel")
	revertServiceTLS("filebrowser")
	revertServiceTLS("rclone-webdav")
	revertServiceTLS("rclone-s3")

	// Remove certs
	common.SudoExec("rm", "-f", certFile, keyFile, certDir+"/.meta")

	common.JSONResponse(w, map[string]interface{}{
		"message": "证书已移除，所有服务恢复为 HTTP",
		"status":  getCertStatus(),
	})
}

// ═══════════════════════════════════════
// Service-specific TLS application
// ═══════════════════════════════════════

func applyPanelTLS() []string {
	var results []string
	// NAS Panel uses Go's http.ListenAndServeTLS
	// Write TLS config to environment file
	envContent := fmt.Sprintf("TLS_CERT=%s\nTLS_KEY=%s\n", certFile, keyFile)
	common.SafeWriteFile("/etc/default/nas-panel-tls", envContent)
	common.SudoExec("chmod", "640", "/etc/default/nas-panel-tls")

	// Update systemd unit to add EnvironmentFile
	unitFile := "/etc/systemd/system/nas-panel.service"
	unitOut, _ := common.SudoOutput("cat", unitFile)
	if !strings.Contains(unitOut, "nas-panel-tls") {
		// Insert EnvironmentFile before ExecStart
		lines := strings.Split(unitOut, "\n")
		var newLines []string
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "ExecStart=") {
				newLines = append(newLines, "EnvironmentFile=-/etc/default/nas-panel-tls")
			}
			newLines = append(newLines, line)
		}
		common.SafeWriteFile(unitFile, strings.Join(newLines, "\n"))
		common.SudoExec("systemctl", "daemon-reload")
	}
	common.SudoExec("systemctl", "restart", "nas-panel")
	results = append(results, "NAS 管理面板: HTTPS 已启用")
	return results
}

func applyFileBrowserTLS() []string {
	var results []string
	unitFile := "/etc/systemd/system/filebrowser.service"
	unitOut, _ := common.SudoOutput("cat", unitFile)

	// Replace ExecStart to add -t/-k
	lines := strings.Split(unitOut, "\n")
	var newLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ExecStart=") {
			// Remove existing -t/-k flags if present
			execLine := strings.TrimPrefix(trimmed, "ExecStart=")
			// Remove old TLS flags
			for _, flag := range []string{"-t ", "-k "} {
				for {
					idx := strings.Index(execLine, flag)
					if idx < 0 {
						break
					}
					// Find end of value (next space or end)
					end := idx + len(flag)
					rest := execLine[end:]
					if spaceIdx := strings.Index(rest, " "); spaceIdx >= 0 {
						execLine = execLine[:idx] + rest[spaceIdx+1:]
					} else {
						execLine = execLine[:idx]
					}
					execLine = strings.TrimSpace(execLine)
				}
			}
			// Add TLS flags
			execLine = fmt.Sprintf("%s -t %s -k %s", execLine, certFile, keyFile)
			newLines = append(newLines, "ExecStart="+execLine)
		} else {
			newLines = append(newLines, line)
		}
	}
	common.SafeWriteFile(unitFile, strings.Join(newLines, "\n"))
	common.SudoExec("systemctl", "daemon-reload")
	common.SudoExec("systemctl", "restart", "filebrowser")
	results = append(results, "FileBrowser: HTTPS 已启用")
	return results
}

func applyRcloneTLS(svcName, port string) []string {
	var results []string
	unitFile := "/etc/systemd/system/" + svcName + ".service"
	unitOut, _ := common.SudoOutput("cat", unitFile)

	lines := strings.Split(unitOut, "\n")
	var newLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ExecStart=") {
			execLine := strings.TrimPrefix(trimmed, "ExecStart=")
			// Remove existing --cert/--key flags
			for _, flag := range []string{"--cert ", "--key "} {
				for {
					idx := strings.Index(execLine, flag)
					if idx < 0 {
						break
					}
					end := idx + len(flag)
					rest := execLine[end:]
					if spaceIdx := strings.Index(rest, " "); spaceIdx >= 0 {
						execLine = execLine[:idx] + rest[spaceIdx+1:]
					} else {
						execLine = execLine[:idx]
					}
					execLine = strings.TrimSpace(execLine)
				}
			}
			// Add TLS flags
			execLine = fmt.Sprintf("%s --cert %s --key %s", execLine, certFile, keyFile)
			newLines = append(newLines, "ExecStart="+execLine)
		} else {
			newLines = append(newLines, line)
		}
	}
	common.SafeWriteFile(unitFile, strings.Join(newLines, "\n"))
	common.SudoExec("systemctl", "daemon-reload")
	common.SudoExec("systemctl", "restart", svcName)
	results = append(results, fmt.Sprintf("%s (端口 %s): HTTPS 已启用", svcName, port))
	return results
}

func revertServiceTLS(svcName string) {
	unitFile := "/etc/systemd/system/" + svcName + ".service"
	unitOut, _ := common.SudoOutput("cat", unitFile)
	if unitOut == "" {
		return
	}

	lines := strings.Split(unitOut, "\n")
	var newLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ExecStart=") {
			execLine := strings.TrimPrefix(trimmed, "ExecStart=")
			// Remove TLS flags
			for _, flag := range []string{"-t ", "-k ", "--cert ", "--key "} {
				for {
					idx := strings.Index(execLine, flag)
					if idx < 0 {
						break
					}
					end := idx + len(flag)
					rest := execLine[end:]
					if spaceIdx := strings.Index(rest, " "); spaceIdx >= 0 {
						execLine = execLine[:idx] + rest[spaceIdx+1:]
					} else {
						execLine = execLine[:idx]
					}
					execLine = strings.TrimSpace(execLine)
				}
			}
			newLines = append(newLines, "ExecStart="+execLine)
		} else if strings.TrimSpace(line) == "EnvironmentFile=-/etc/default/nas-panel-tls" {
			// Remove TLS env file reference
			continue
		} else {
			newLines = append(newLines, line)
		}
	}
	common.SafeWriteFile(unitFile, strings.Join(newLines, "\n"))
	common.SudoExec("systemctl", "daemon-reload")
	common.SudoExec("systemctl", "restart", svcName)
}