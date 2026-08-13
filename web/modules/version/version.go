package version

import (
	"net/http"
	"runtime"

	"nas-panel/common"
)

// Build-time values injected via ldflags:
//   -X nas-panel/modules/version.Version=v1.3.0
//   -X nas-panel/modules/version.BuildTime=2026-08-13T15:00:00Z
//   -X nas-panel/modules/version.GitCommit=abc1234
var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

// VersionInfo represents the version response
type VersionInfo struct {
	Version   string `json:"version"`
	BuildTime string `json:"build_time"`
	GitCommit string `json:"git_commit"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

// RegisterRoutes registers version routes
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/version", handleVersion)
}

// handleVersion returns build version info
func handleVersion(w http.ResponseWriter, r *http.Request) {
	info := VersionInfo{
		Version:   Version,
		BuildTime: BuildTime,
		GitCommit: GitCommit,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
	common.JSONResponse(w, info)
}