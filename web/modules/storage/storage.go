package storage

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// getDirSizes 返回 /data 下各一级子目录的大小。
// 之前用 common.ExecOutput("du", "-sh", "/data/*/") 把 "/data/*/" 当成
// 字面参数传给 exec.Command——Go 的 exec 不经过 shell，不会展开通配符，
// 这个调用在真实环境里一直拿到的是 du 对一个不存在的字面路径报错，
// 返回 nil（没在 P2 范围内被专门点出，但顺带用 filepath.Glob 在 Go 侧
// 做实际展开修正，同时套上 TTL 缓存）。响应字段形状不变：
// []map[string]string{"path": ..., "size": ...}。
func getDirSizes() []map[string]string {
	matches, err := filepath.Glob("/data/*/")
	if err != nil {
		return nil
	}
	var dirs []map[string]string
	for _, m := range matches {
		path := strings.TrimSuffix(m, "/")
		size, sizeErr := common.CachedDirSize(path, 60*time.Second)
		if sizeErr != nil {
			continue
		}
		dirs = append(dirs, map[string]string{"path": path, "size": size})
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
