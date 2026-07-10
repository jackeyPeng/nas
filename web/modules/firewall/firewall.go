package firewall

import (
	"fmt"
	"net/http"
	"strings"

	"nas-panel/common"
)

func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/firewall", common.AuthMiddleware(handleFirewall))
	mux.HandleFunc("/api/firewall/allow", common.AuthMiddleware(handleFirewallAllow))
	mux.HandleFunc("/api/firewall/deny", common.AuthMiddleware(handleFirewallDeny))
}

func handleFirewall(w http.ResponseWriter, r *http.Request) {
	status := getFirewallStatus()
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(status))
}

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
	common.JSONResponse(w, map[string]interface{}{"message": output})
}

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
	common.JSONResponse(w, map[string]interface{}{"message": output})
}

func getFirewallStatus() string {
	out, _ := common.SudoOutput("ufw", "status", "numbered")
	if out == "" {
		return "UFW not available"
	}
	return out
}

func firewallAllow(port, proto string) (string, error) {
	return common.SudoExec("ufw", "allow", port+"/"+proto)
}

func firewallDeny(port, proto string) (string, error) {
	return common.SudoExec("ufw", "deny", port+"/"+proto)
}

var _ = strings.TrimSpace
