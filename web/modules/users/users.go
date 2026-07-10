package users

import (
	"fmt"
	"net/http"
	"strings"

	"nas-panel/common"
)

// getUsers returns NAS users from Samba and FTP
func getUsers() []map[string]string {
	var users []map[string]string

	// Get Samba users (requires root for passdb.tdb access)
	out, err := common.SudoOutput("pdbedit", "-L")
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			username := parts[0]
			users = append(users, map[string]string{
				"username": username,
				"type":     "samba",
			})
		}
	}

	// Also check vsftpd userlist
	if data, err := common.ExecOutput("cat", "/etc/vsftpd.userlist"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			found := false
			for _, u := range users {
				if u["username"] == line {
					found = true
					break
				}
			}
			if !found {
				users = append(users, map[string]string{
					"username": line,
					"type":     "ftp",
				})
			}
		}
	}

	return users
}

// addUser creates a new NAS user
func addUser(username, password string) error {
	out, err := common.SudoExec("/opt/nas/scripts/add-user.sh", username, password)
	if err != nil {
		return fmt.Errorf("%s: %v", out, err)
	}
	return nil
}

// removeUser deletes a NAS user
func removeUser(username string, deleteData bool) error {
	args := []string{username}
	if deleteData {
		args = append(args, "--delete-data")
	}
	out, err := common.SudoExec("/opt/nas/scripts/remove-user.sh", args...)
	if err != nil {
		return fmt.Errorf("%s: %v", out, err)
	}
	return nil
}

// changePassword changes user password for system, Samba, and WebDAV.
// Since common.SudoExec/SudoOutput don't support stdin, we pipe via bash -c.
func changePassword(username, password string) error {
	// System password (requires root)
	cred := username + ":" + password
	out, err := common.SudoExec("bash", "-c", fmt.Sprintf("printf '%%s\\n' %s | chpasswd", shellQuote(cred)))
	if err != nil {
		return fmt.Errorf("chpasswd failed: %s: %v", out, err)
	}

	// Samba password (requires root) — smbpasswd -a reads password twice from stdin
	out, err = common.SudoExec("bash", "-c", fmt.Sprintf(
		"printf '%%s\\n%%s\\n' %s %s | smbpasswd -a %s -s",
		shellQuote(password), shellQuote(password), shellQuote(username),
	))
	if err != nil {
		return fmt.Errorf("smbpasswd failed: %s: %v", out, err)
	}

	// Update htpasswd for WebDAV (requires root) — best-effort
	common.SudoExec("htpasswd", "-b", "/etc/rclone-htpasswd", username, password)

	return nil
}

// handleUsers handles GET (list) and POST (create)
func handleUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		users := getUsers()
		common.JSONResponse(w, map[string]interface{}{
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
		common.JSONResponse(w, map[string]interface{}{
			"message": "用户 " + username + " 添加成功",
		})
	}
}

// handleUserAction handles DELETE (remove) and PUT/POST (password change)
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
		common.JSONResponse(w, map[string]interface{}{
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
		common.JSONResponse(w, map[string]interface{}{
			"message": "用户 " + username + " 已删除",
		})
		return
	}
}

// RegisterRoutes registers user routes on the given mux
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/users", common.AuthMiddleware(handleUsers))
	mux.HandleFunc("/api/users/", common.AuthMiddleware(handleUserAction))
}

// --- helpers ---

// shellQuote wraps a string in single quotes, escaping any embedded single
// quotes so it is safe to interpolate into a bash -c command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
