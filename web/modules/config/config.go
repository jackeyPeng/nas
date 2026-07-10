package config

import (
	"fmt"
	"net/http"
	"os/exec"
	"strings"

	"nas-panel/common"
)

// RegisterRoutes registers config module routes
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/config/env", common.AuthMiddleware(handleEnvConfig))
	mux.HandleFunc("/api/config/samba-shares", common.AuthMiddleware(handleSambaShares))
	mux.HandleFunc("/api/config/vsftpd-users", common.AuthMiddleware(handleVsftpdUsers))
	mux.HandleFunc("/api/config/services", common.AuthMiddleware(handleServiceList))
}

// handleEnvConfig returns full .env config (GET) or updates it (POST)
func handleEnvConfig(w http.ResponseWriter, r *http.Request) {
	envPath := common.GetEnvFilePath()
	if envPath == "" {
		common.JSONResponse(w, map[string]interface{}{"error": "no .env file"})
		return
	}

	if r.Method == http.MethodGet {
		config := common.ReadAllEnv(envPath)
		common.JSONResponse(w, map[string]interface{}{
			"config": config,
		})
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
		common.JSONResponse(w, map[string]interface{}{
			"message": "配置已保存",
		})
	}
}

// handleSambaShares returns Samba share configuration
func handleSambaShares(w http.ResponseWriter, r *http.Request) {
	out, err := common.ExecOutput("testparm", "-s")
	if err != nil {
		// Try reading directly
		out = readfile("/etc/samba/smb.conf")
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(out))
}

// handleVsftpdUsers returns vsftpd user whitelist
func handleVsftpdUsers(w http.ResponseWriter, r *http.Request) {
	out := readfile("/etc/vsftpd.userlist")
	common.JSONResponse(w, map[string]interface{}{
		"users": strings.Split(strings.TrimSpace(out), "\n"),
	})
}

// handleServiceList returns systemd service enable/disable status
func handleServiceList(w http.ResponseWriter, r *http.Request) {
	out, _ := common.SudoExec("systemctl", "list-unit-files", "--type=service", "--state=enabled", "--no-pager")
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(out))
}

// updateEnvFile writes key-value pairs back to .env
func updateEnvFile(path string, values map[string]string) error {
	// Use sudo tee to write
	content := ""
	for k, v := range values {
		content += fmt.Sprintf("%s=%s\n", k, v)
	}
	cmd := exec.Command("sudo", "tee", path)
	cmd.Stdin = strings.NewReader(content)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

func readfile(path string) string {
	out, _ := common.Exec("cat", path)
	return out
}
