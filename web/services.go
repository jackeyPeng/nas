package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// ServiceStatus holds the status of a service
type ServiceStatus struct {
	ServiceDef
	Active string `json:"active"`
}

// getServices returns all NAS services with their status
func getServices() []map[string]interface{} {
	var result []map[string]interface{}
	for _, svc := range nasServices {
		active := "unknown"
		out, err := exec.Command("systemctl", "is-active", svc.Name).Output()
		if err == nil {
			active = strings.TrimSpace(string(out))
		}
		result = append(result, map[string]interface{}{
			"name":         svc.Name,
			"display_name": svc.DisplayName,
			"port":         svc.Port,
			"description":  svc.Description,
			"active":       active,
		})
	}
	return result
}

// controlService performs start/stop/restart on a service
func controlService(name, action string) (string, error) {
	// Validate service name is in our list
	valid := false
	for _, svc := range nasServices {
		if svc.Name == name {
			valid = true
			break
		}
	}
	if !valid {
		return "", fmt.Errorf("unknown service: %s", name)
	}

	out, err := exec.Command("systemctl", action, name).CombinedOutput()
	if err != nil {
		return string(out), err
	}
	return strings.TrimSpace(string(out)), nil
}

// getServiceLogs returns recent journal logs for a service
func getServiceLogs(name string) string {
	out, err := exec.Command("journalctl", "-u", name, "-n", "50", "--no-pager").Output()
	if err != nil {
		// Try without journalctl
		return ""
	}
	return string(out)
}

// getUsers returns NAS users from Samba
func getUsers() []map[string]string {
	var users []map[string]string

	// Get Samba users
	out, err := exec.Command("pdbedit", "-L").Output()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
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
	if data, err := exec.Command("cat", "/etc/vsftpd.userlist").Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
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
	// Use the add-user.sh script
	out, err := exec.Command("/opt/nas/scripts/add-user.sh", username, password).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v", string(out), err)
	}
	return nil
}

// removeUser deletes a NAS user
func removeUser(username string, deleteData bool) error {
	args := []string{username}
	if deleteData {
		args = append(args, "--delete-data")
	}
	out, err := exec.Command("/opt/nas/scripts/remove-user.sh", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v", string(out), err)
	}
	return nil
}

// changePassword changes user password for Samba and system
func changePassword(username, password string) error {
	// System password
	cmd := exec.Command("chpasswd")
	cmd.Stdin = strings.NewReader(username + ":" + password)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("chpasswd failed: %v", err)
	}

	// Samba password
	cmd = exec.Command("smbpasswd", "-a", username, "-s")
	cmd.Stdin = strings.NewReader(password + "\n" + password + "\n")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("smbpasswd failed: %v", err)
	}

	// Update htpasswd for WebDAV
	exec.Command("htpasswd", "-b", "/etc/rclone-htpasswd", username, password).Run()

	return nil
}

// getFirewallStatus returns UFW status
func getFirewallStatus() string {
	out, err := exec.Command("ufw", "status", "numbered").Output()
	if err != nil {
		return "UFW not available"
	}
	return string(out)
}

// firewallAllow allows a port
func firewallAllow(port, proto string) (string, error) {
	out, err := exec.Command("ufw", "allow", port+"/"+proto).CombinedOutput()
	if err != nil {
		return string(out), err
	}
	return strings.TrimSpace(string(out)), nil
}

// firewallDeny denies a port
func firewallDeny(port, proto string) (string, error) {
	out, err := exec.Command("ufw", "deny", port+"/"+proto).CombinedOutput()
	if err != nil {
		return string(out), err
	}
	return strings.TrimSpace(string(out)), nil
}
