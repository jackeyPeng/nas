package storage

import (
	"net/http"
	"os"
	"strings"

	"nas-panel/common"
)

func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/storage", common.AuthMiddleware(handleStorage))
	mux.HandleFunc("/api/storage/smart", common.AuthMiddleware(handleSmart))
}

func handleStorage(w http.ResponseWriter, r *http.Request) {
	diskTotal, diskUsed, diskPct := getDiskInfo()
	dirs := getDirSizes()
	samba := getSambaShares()
	nfs := getNFSExports()
	common.JSONResponse(w, map[string]interface{}{
		"disk_total":   diskTotal,
		"disk_used":    diskUsed,
		"disk_pct":     diskPct,
		"directories":  dirs,
		"samba_shares": samba,
		"nfs_exports":  nfs,
	})
}

func handleSmart(w http.ResponseWriter, r *http.Request) {
	smart := getSmartStatus()
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(smart))
}

func getDiskInfo() (total, used, pct string) {
	out, _ := common.ExecOutput("df", "-h", "/data")
	if out == "" {
		out, _ = common.ExecOutput("df", "-h", "/")
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		return
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 5 {
		return
	}
	return fields[1], fields[2], fields[4]
}

func getDirSizes() []map[string]string {
	out, err := common.ExecOutput("du", "-sh", "/data/*/")
	if err != nil {
		return nil
	}
	var dirs []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			dirs = append(dirs, map[string]string{"path": fields[1], "size": fields[0]})
		}
	}
	return dirs
}

func getSambaShares() string {
	out, err := common.ExecOutput("testparm", "-s")
	if err != nil {
		data, _ := os.ReadFile("/etc/samba/smb.conf")
		return string(data)
	}
	return out
}

func getNFSExports() string {
	out, _ := common.SudoOutput("exportfs", "-v")
	return out
}

func getSmartStatus() string {
	entries, err := os.ReadDir("/dev")
	if err != nil {
		return ""
	}
	var result strings.Builder
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "sd") || strings.Contains(name, "0") {
			continue
		}
		out, err := common.SudoOutput("smartctl", "-H", "/dev/"+name)
		if err == nil {
			result.WriteString("--- /dev/" + name + " ---\n")
			result.WriteString(out)
			result.WriteString("\n")
		}
	}
	return result.String()
}
