package common

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

// JSONResponse writes a JSON response using encoding/json
func JSONResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.Encode(data)
}

// ReadEnvFile reads a key from an env file
func ReadEnvFile(path, key string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == key {
			return strings.TrimSpace(parts[1]), nil
		}
	}
	return "", nil
}

// ReadAllEnv reads all key=value pairs from an env file
func ReadAllEnv(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	result := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return result
}

// GetEnvFilePath returns the .env file path
func GetEnvFilePath() string {
	if _, err := os.Stat("/opt/nas/.env"); err == nil {
		return "/opt/nas/.env"
	}
	return ""
}

// GetNASUser returns the panel's configured NAS user.
// 权威来源是 systemd 的 Environment=NAS_USER（os.Getenv），与 main.go 一致；
// 兜底 "fm"（main.go 的 defaultUser）。
// 注意：不要用 ReadEnvFile 读 .env 里的 NAS_USER——.env 只有 NAS_PASS，不含 NAS_USER。
func GetNASUser() string {
	if u := os.Getenv("NAS_USER"); u != "" {
		return u
	}
	return "fm"
}
