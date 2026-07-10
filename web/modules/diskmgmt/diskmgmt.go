package diskmgmt

import (
	"net/http"
	"os"
	"strings"

	"nas-panel/common"
)

// RegisterRoutes registers disk management routes
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/disk/info", common.AuthMiddleware(handleDiskInfo))
	mux.HandleFunc("/api/disk/mounts", common.AuthMiddleware(handleMounts))
	mux.HandleFunc("/api/disk/lvm", common.AuthMiddleware(handleLVM))
	mux.HandleFunc("/api/disk/iostat", common.AuthMiddleware(handleIOStat))
	mux.HandleFunc("/api/disk/smart-detail", common.AuthMiddleware(handleSmartDetail))
}

// handleDiskInfo returns lsblk output
func handleDiskInfo(w http.ResponseWriter, r *http.Request) {
	out, _ := common.ExecOutput("lsblk", "-o", "NAME,SIZE,TYPE,MOUNTPOINT,MODEL,ROTA,FSTYPE")
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(out))
}

// handleMounts returns current mount points
func handleMounts(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		out, _ := common.Exec("mount")
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(out))
		return
	}
	// Filter to relevant mount points
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "/data") || strings.Contains(line, "/ ") || strings.Contains(line, "sd") || strings.Contains(line, "nvme") {
			lines = append(lines, line)
		}
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(strings.Join(lines, "\n")))
}

// handleLVM returns LVM info (pvs/vgs/lvs)
func handleLVM(w http.ResponseWriter, r *http.Request) {
	var result string
	if out, err := common.SudoOutput("pvs", "--noheadings", "-o", "pv_name,vg_name,pv_size,pv_free"); err == nil && len(strings.TrimSpace(out)) > 0 {
		result += "物理卷 (PV):\n" + out + "\n"
	}
	if out, err := common.SudoOutput("vgs", "--noheadings", "-o", "vg_name,vg_size,vg_free"); err == nil && len(strings.TrimSpace(out)) > 0 {
		result += "卷组 (VG):\n" + out + "\n"
	}
	if out, err := common.SudoOutput("lvs", "--noheadings", "-o", "lv_name,vg_name,lv_size,lv_path"); err == nil && len(strings.TrimSpace(out)) > 0 {
		result += "逻辑卷 (LV):\n" + out + "\n"
	}
	if result == "" {
		result = "无 LVM 配置"
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(result))
}

// handleIOStat returns disk I/O statistics
func handleIOStat(w http.ResponseWriter, r *http.Request) {
	out, _ := common.Exec("iostat", "-x", "1", "3")
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(out))
}

// handleSmartDetail returns detailed SMART info for all disks
func handleSmartDetail(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir("/dev")
	if err != nil {
		w.Write([]byte("无法读取 /dev"))
		return
	}
	var result string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "sd") || strings.Contains(name, "0") {
			continue
		}
		out, err := common.SudoOutput("smartctl", "-a", "/dev/"+name)
		if err == nil && len(out) > 0 {
			result += "=== /dev/" + name + " ===\n" + out + "\n\n"
		}
	}
	if result == "" {
		result = "无 SMART 数据"
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(result))
}
