package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"

	"nas-panel/common"
	"nas-panel/modules/backup"
	"nas-panel/modules/config"
	"nas-panel/modules/dashboard"
	"nas-panel/modules/diskmgmt"
	"nas-panel/modules/firewall"
	"nas-panel/modules/monitor"
	"nas-panel/modules/rclone"
	"nas-panel/modules/services"
	"nas-panel/modules/storage"
	"nas-panel/modules/system"
	"nas-panel/modules/users"
)

//go:embed frontend/*
var frontendFS embed.FS

var (
	nasUser    string
	nasPass    string
	listenAddr = ":8090"
)

func main() {
	// Load config
	nasUser = os.Getenv("NAS_USER")
	if nasUser == "" {
		nasUser = "admin"
	}

	nasPass = os.Getenv("NAS_PASS")
	if nasPass == "" {
		if pass, err := common.ReadEnvFile("/opt/nas/.env", "NAS_PASS"); err == nil && pass != "" {
			nasPass = pass
		}
	}
	if nasPass == "" {
		log.Fatal("NAS_PASS not set. Set it via env or /opt/nas/.env")
	}

	// Init JWT
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "nas-panel-secret-" + nasPass
	}
	common.InitAuth(secret)

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
		next.ServeHTTP(w, r)
	})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	if username != nasUser || password != nasPass {
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
