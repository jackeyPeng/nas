package main

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"

	"nas-panel/common"
	"nas-panel/modules/backup"
	"nas-panel/modules/config"
	"nas-panel/modules/dashboard"
	"nas-panel/modules/diagnostics"
	"nas-panel/modules/diskmgmt"
	"nas-panel/modules/firewall"
	"nas-panel/modules/logs"
	"nas-panel/modules/monitor"
	"nas-panel/modules/rclone"
	"nas-panel/modules/services"
	"nas-panel/modules/storage"
	"nas-panel/modules/system"
	"nas-panel/modules/users"
	"nas-panel/modules/version"
)

//go:embed frontend/*
var frontendFS embed.FS

var (
	nasUser    string
	listenAddr = ":8090"
)

func main() {
	// Load config
	nasUser = os.Getenv("NAS_USER")
	if nasUser == "" {
		nasUser = "admin"
	}

	nasPass := os.Getenv("NAS_PASS")
	if nasPass == "" {
		if pass, err := common.ReadEnvFile("/opt/nas/.env", "NAS_PASS"); err == nil && pass != "" {
			nasPass = pass
		}
	}
	if nasPass == "" {
		log.Fatal("NAS_PASS not set. Set it via env or /opt/nas/.env")
	}
	common.SetNasPass(nasPass)

	// Init JWT — use env var, or generate random secret and persist to .env
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		// Try reading from .env
		if s, err := common.ReadEnvFile("/opt/nas/.env", "JWT_SECRET"); err == nil && s != "" {
			secret = s
		}
	}
	if secret == "" {
		// Generate cryptographically random secret (32 bytes = 64 hex chars)
		b := make([]byte, 32)
		if _, err := rand.Read(b); err == nil {
			secret = hex.EncodeToString(b)
			// Persist to .env so it survives restarts
			envFile := "/opt/nas/.env"
			if data, err := os.ReadFile(envFile); err == nil {
				content := string(data)
				if !strings.Contains(content, "JWT_SECRET=") {
					f, err := os.OpenFile(envFile, os.O_APPEND|os.O_WRONLY, 0600)
					if err == nil {
						fmt.Fprintf(f, "\nJWT_SECRET=%s\n", secret)
						f.Close()
					}
				}
			}
		} else {
			// Fallback: use NAS_PASS as seed (better than hardcoded string)
			secret = "nas-panel-secret-" + nasPass
		}
	}
	common.InitAuth(secret)

	// Init audit log
	dataDir := "/opt/nas/data"
	if d := os.Getenv("NAS_DATA_DIR"); d != "" {
		dataDir = d
	}
	common.InitAuditLog(dataDir)

	if addr := os.Getenv("LISTEN_ADDR"); addr != "" {
		listenAddr = addr
	}

	// Routes
	mux := http.NewServeMux()

	// Login (no auth required)
	mux.HandleFunc("/api/login", handleLogin)

	// Register module routes
	dashboard.RegisterRoutes(mux)
	services.RegisterRoutes(mux)
	users.RegisterRoutes(mux)
	storage.RegisterRoutes(mux)
	firewall.RegisterRoutes(mux)
	monitor.RegisterRoutes(mux)
	config.RegisterRoutes(mux)
	diskmgmt.RegisterRoutes(mux)
	system.RegisterRoutes(mux)
	backup.RegisterRoutes(mux)
	rclone.RegisterRoutes(mux)
	version.RegisterRoutes(mux)
	logs.RegisterRoutes(mux)
	diagnostics.RegisterRoutes(mux)

	// Serve frontend
	frontendRoot, _ := fs.Sub(frontendFS, "frontend")
	mux.Handle("/", http.FileServer(http.FS(frontendRoot)))

	// Start
	fmt.Printf("NAS Web Panel starting on %s\n", listenAddr)
	fmt.Printf("User: %s\n", nasUser)
	log.Fatal(http.ListenAndServe(listenAddr, loggingMiddleware(mux)))
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s %s", r.Method, r.URL.Path, r.RemoteAddr)

		// Extract username for audit log
		username := ""
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			if user, err := common.VerifyToken(auth[7:]); err == nil {
				username = user
			}
		}

		// Get client IP
		ip := r.RemoteAddr
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			ip = strings.Split(fwd, ",")[0]
		}
		ip = strings.Split(ip, ":")[0]

		next.ServeHTTP(w, r)

		// Only audit write operations (POST/PUT/DELETE) — GET polling would flood the log
		path := r.URL.Path
		if strings.HasPrefix(path, "/api/") && r.Method != http.MethodGet {
			action := classifyAction(r.Method, path)
			detail := buildLogDetail(r)
			common.LogAudit(username, action, r.Method, path, detail, "success", ip)
		}
	})
}

func classifyAction(method, path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/api/"), "/")
	if len(parts) == 0 {
		return "api"
	}
	module := parts[0]

	switch {
	case path == "/api/login":
		return "login"
	case module == "users":
		return "users"
	case module == "services":
		return "services"
	case module == "disk":
		if len(parts) > 1 && parts[1] == "wizard" {
			if strings.Contains(path, "reset") {
				return "storage_reset"
			}
			return "storage_create"
		}
		if len(parts) > 1 && parts[1] == "pool" {
			if strings.Contains(path, "delete") {
				return "storage_delete"
			}
			if strings.Contains(path, "extend") {
				return "storage_extend"
			}
			return "storage_pool"
		}
		if len(parts) > 1 && parts[1] == "folders" {
			return "storage_folder"
		}
		if len(parts) > 1 && parts[1] == "scrub" {
			return "storage_scrub"
		}
		if len(parts) > 1 && parts[1] == "replace" {
			return "storage_replace"
		}
		if len(parts) > 1 && parts[1] == "smart-scan" {
			return "storage_smart"
		}
		return "storage"
	case module == "firewall":
		return "firewall"
	case module == "monitor":
		return "monitor"
	case module == "system":
		return "system"
	case module == "backup":
		return "backup"
	case module == "rclone":
		return "rclone"
	case module == "config":
		return "config"
	case module == "logs":
		return "logs"
	case module == "dashboard":
		return "dashboard"
	case module == "version":
		return "version"
	default:
		return module
	}
}

// buildLogDetail extracts human-readable detail from the request
func buildLogDetail(r *http.Request) string {
	if r.Method == "GET" {
		return ""
	}
	r.ParseForm()
	// Try common field names
	for _, key := range []string{"name", "username", "path", "folder_name", "service", "pool"} {
		if v := r.FormValue(key); v != "" {
			return key + "=" + v
		}
	}
	// Storage-specific detail: mode/disks for wizard, pool_type for delete
	if v := r.FormValue("mode"); v != "" {
		detail := "mode=" + v
		if disks := r.FormValue("disks"); disks != "" {
			detail += " disks=" + disks
		}
		return detail
	}
	if v := r.FormValue("pool_type"); v != "" {
		return "pool_type=" + v + " device=" + r.FormValue("pool_device")
	}
	if v := r.FormValue("vg_name"); v != "" {
		return "vg=" + v + " disk=" + r.FormValue("disk")
	}
	return ""
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	// Constant-time comparison to prevent timing side-channel attacks
	userMatch := subtle.ConstantTimeCompare([]byte(username), []byte(nasUser))
	passMatch := subtle.ConstantTimeCompare([]byte(password), []byte(common.GetNasPass()))
	if userMatch != 1 || passMatch != 1 {
		http.Error(w, `{"error": "invalid credentials"}`, http.StatusUnauthorized)
		return
	}

	token, err := common.CreateToken(username)
	if err != nil {
		http.Error(w, `{"error": "token creation failed"}`, http.StatusInternalServerError)
		return
	}

	common.JSONResponse(w, map[string]interface{}{
		"token":    token,
		"username": username,
	})
}
