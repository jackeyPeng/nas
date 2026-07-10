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

// RegisterRoutes registers service routes on the given mux
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/services", common.AuthMiddleware(handleServiceList))
	mux.HandleFunc("/api/services/", common.AuthMiddleware(handleServiceAction))
}
