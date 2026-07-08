package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
)

//go:embed frontend/*
var frontendFS embed.FS

var (
	jwtSecret  []byte
	nasUser    string
	nasPass    string
	listenAddr = ":8090"
)

// Service definitions
var nasServices = []ServiceDef{
	{Name: "smbd", DisplayName: "Samba SMB", Port: "139, 445", Description: "Windows/Mac/Linux 文件共享"},
	{Name: "nmbd", DisplayName: "Samba NetBIOS", Port: "137-138", Description: "NetBIOS 名称服务"},
	{Name: "nfs-kernel-server", DisplayName: "NFS", Port: "2049", Description: "Linux 文件共享"},
	{Name: "vsftpd", DisplayName: "FTP", Port: "21", Description: "文件传输协议"},
	{Name: "rclone-webdav", DisplayName: "WebDAV", Port: "8080", Description: "WebDAV 文件服务"},
	{Name: "filebrowser", DisplayName: "FileBrowser", Port: "8081", Description: "Web 文件管理"},
	{Name: "minio", DisplayName: "MinIO", Port: "9000, 9002", Description: "S3 兼容对象存储"},
	{Name: "fail2ban", DisplayName: "Fail2ban", Port: "-", Description: "入侵防护"},
}

type ServiceDef struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Port        string `json:"port"`
	Description string `json:"description"`
}

func main() {
	// Load config from environment
	nasUser = os.Getenv("NAS_USER")
	if nasUser == "" {
		nasUser = "admin"
	}

	nasPass = os.Getenv("NAS_PASS")
	if nasPass == "" {
		// Try reading from .env file
		if pass, err := readEnvFile("/opt/nas/.env", "NAS_PASS"); err == nil {
			nasPass = pass
		}
	}
	if nasPass == "" {
		log.Fatal("NAS_PASS not set. Set it via env or /opt/nas/.env")
	}

	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "nas-panel-secret-" + nasPass
	}
	jwtSecret = []byte(secret)

	if addr := os.Getenv("LISTEN_ADDR"); addr != "" {
		listenAddr = addr
	}

	// Routes
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/login", handleLogin)
	mux.HandleFunc("/api/dashboard", authMiddleware(handleDashboard))
	mux.HandleFunc("/api/services", authMiddleware(handleServiceList))
	mux.HandleFunc("/api/services/", authMiddleware(handleServiceAction))
	mux.HandleFunc("/api/users", authMiddleware(handleUsers))
	mux.HandleFunc("/api/users/", authMiddleware(handleUserAction))
	mux.HandleFunc("/api/storage", authMiddleware(handleStorage))
	mux.HandleFunc("/api/storage/smart", authMiddleware(handleSmart))
	mux.HandleFunc("/api/firewall", authMiddleware(handleFirewall))
	mux.HandleFunc("/api/firewall/allow", authMiddleware(handleFirewallAllow))
	mux.HandleFunc("/api/firewall/deny", authMiddleware(handleFirewallDeny))

	// Serve frontend
	frontendRoot, _ := fs.Sub(frontendFS, "frontend")
	mux.Handle("/", http.FileServer(http.FS(frontendRoot)))

	// CORS + logging wrapper
	handler := loggingMiddleware(mux)

	fmt.Printf("NAS Web Panel starting on %s\n", listenAddr)
	fmt.Printf("User: %s\n", nasUser)
	log.Fatal(http.ListenAndServe(listenAddr, handler))
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s %s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}

func readEnvFile(path, key string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == key {
			return strings.TrimSpace(parts[1]), nil
		}
	}
	return "", fmt.Errorf("key %s not found", key)
}

// jsonResponse writes a JSON response
func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(toJSON(data)))
}

// toJSON is a simple JSON encoder
func toJSON(v interface{}) string {
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("%q", val)
	case int:
		return fmt.Sprintf("%d", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case map[string]string:
		parts := []string{}
		for k, v := range val {
			parts = append(parts, fmt.Sprintf("%q: %q", k, v))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case map[string]interface{}:
		return mapToJSON(val)
	case []map[string]interface{}:
		return sliceToJSON(val)
	case []map[string]string:
		parts := []string{}
		for _, item := range val {
			parts = append(parts, toJSON(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []interface{}:
		parts := []string{}
		for _, item := range val {
			parts = append(parts, toJSON(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprintf("%v", v)
	}
}

func mapToJSON(m map[string]interface{}) string {
	parts := []string{}
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%q: %s", k, toJSON(v)))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func sliceToJSON(s []map[string]interface{}) string {
	parts := []string{}
	for _, item := range s {
		parts = append(parts, mapToJSON(item))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
