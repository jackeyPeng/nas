package diskmgmt

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"nas-panel/common"
)

// RegisterRoutes registers disk management routes
func RegisterRoutes(mux *http.ServeMux) {
	// Overview
	mux.HandleFunc("/api/disk/overview", common.AuthMiddleware(handleStorageOverview))
	// Read-only
	mux.HandleFunc("/api/disk/info", common.AuthMiddleware(handleDiskInfo))
	mux.HandleFunc("/api/disk/status", common.AuthMiddleware(handleDiskStatus))
	mux.HandleFunc("/api/disk/free", common.AuthMiddleware(handleDiskListFree))
	mux.HandleFunc("/api/disk/mounts", common.AuthMiddleware(handleMounts))
	mux.HandleFunc("/api/disk/lvm", common.AuthMiddleware(handleLVM))
	mux.HandleFunc("/api/disk/iostat", common.AuthMiddleware(handleIOStat))
	mux.HandleFunc("/api/disk/smart-detail", common.AuthMiddleware(handleSmartDetail))
	mux.HandleFunc("/api/disk/partitions", common.AuthMiddleware(handlePartitions))
	mux.HandleFunc("/api/disk/fstab", common.AuthMiddleware(handleFstab))
	// Operations
	mux.HandleFunc("/api/disk/format", common.AuthMiddleware(handleFormat))
	mux.HandleFunc("/api/disk/mount", common.AuthMiddleware(handleMount))
	mux.HandleFunc("/api/disk/unmount", common.AuthMiddleware(handleUnmount))
	mux.HandleFunc("/api/disk/mkdir", common.AuthMiddleware(handleMkdir))
	mux.HandleFunc("/api/disk/quick-setup", common.AuthMiddleware(handleQuickSetup))
	// Storage pool (LVM)
	mux.HandleFunc("/api/disk/pool/status", common.AuthMiddleware(handlePoolStatus))
	mux.HandleFunc("/api/disk/pool/create", common.AuthMiddleware(handlePoolCreate))
	mux.HandleFunc("/api/disk/pool/extend", common.AuthMiddleware(handlePoolExtend))
	mux.HandleFunc("/api/disk/pool/extend-stream", common.AuthMiddleware(handlePoolExtendStream))
	// Shared folders
	mux.HandleFunc("/api/disk/folders", common.AuthMiddleware(handleListFolders))
	mux.HandleFunc("/api/disk/folders/create", common.AuthMiddleware(handleCreateFolder))
	mux.HandleFunc("/api/disk/folders/delete", common.AuthMiddleware(handleDeleteFolder))
	mux.HandleFunc("/api/disk/folders/permission", common.AuthMiddleware(handleFolderPermission))
	// Wizard (simple storage setup)
	mux.HandleFunc("/api/disk/wizard/status", common.AuthMiddleware(handleWizardStatus))
	mux.HandleFunc("/api/disk/wizard/setup", common.AuthMiddleware(handleWizardSetup))
	mux.HandleFunc("/api/disk/wizard/reset", common.AuthMiddleware(handleWizardReset))
	// Stream (progressive setup/reset)
	mux.HandleFunc("/api/disk/wizard/setup-stream", common.AuthMiddleware(handleWizardSetupStream))
	mux.HandleFunc("/api/disk/wizard/reset-stream", common.AuthMiddleware(handleWizardResetStream))
}

// ═══════════════════════════════════════
// 只读 API
// ═══════════════════════════════════════

func handleDiskInfo(w http.ResponseWriter, r *http.Request) {
	out, _ := common.ExecOutput("lsblk", "-o", "NAME,SIZE,TYPE,MOUNTPOINT,MODEL,ROTA,FSTYPE")
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(out))
}

func handleMounts(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		out, _ := common.Exec("mount")
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(out))
		return
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "/data") || strings.Contains(line, "/ ") || strings.Contains(line, "sd") || strings.Contains(line, "nvme") {
			lines = append(lines, line)
		}
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(strings.Join(lines, "\n")))
}

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

func handleIOStat(w http.ResponseWriter, r *http.Request) {
	out, _ := common.Exec("iostat", "-x", "1", "3")
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(out))
}

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

func handlePartitions(w http.ResponseWriter, r *http.Request) {
	out, _ := common.SudoOutput("fdisk", "-l")
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(out))
}

// ═══════════════════════════════════════
// 操作 API
// ═══════════════════════════════════════

// handleFormat 格式化分区
func handleFormat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	device := r.FormValue("device")
	fstype := r.FormValue("fstype")
	if fstype == "" {
		fstype = "ext4"
	}
	if device == "" {
		http.Error(w, `{"error": "device required (e.g. /dev/sdb1)"}`, http.StatusBadRequest)
		return
	}
	// 安全检查：不允许格式化系统盘
	if strings.Contains(device, "sda") || strings.Contains(device, "nvme0n1p") {
		http.Error(w, `{"error": "不允许格式化系统盘"}`, http.StatusBadRequest)
		return
	}
	// 需要二次确认
	confirm := r.FormValue("confirm")
	if confirm != "yes" {
		http.Error(w, `{"error": "请加 confirm=yes 确认格式化操作"}`, http.StatusBadRequest)
		return
	}
	out, err := common.SudoExec("mkfs."+fstype, "-F", device)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, out+": "+err.Error()), http.StatusInternalServerError)
		return
	}
	common.JSONResponse(w, map[string]interface{}{"message": device + " 已格式化为 " + fstype, "output": out})
}

// handleMount 挂载分区
func handleMount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	device := r.FormValue("device")
	mountPoint := r.FormValue("mountpoint")
	fstype := r.FormValue("fstype")
	if device == "" || mountPoint == "" {
		http.Error(w, `{"error": "device and mountpoint required"}`, http.StatusBadRequest)
		return
	}
	// 创建挂载点
	common.SudoExec("mkdir", "-p", mountPoint)
	// 挂载
	var out string
	var err error
	if fstype != "" {
		out, err = common.SudoExec("mount", "-t", fstype, device, mountPoint)
	} else {
		out, err = common.SudoExec("mount", device, mountPoint)
	}
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, out+": "+err.Error()), http.StatusInternalServerError)
		return
	}
	common.JSONResponse(w, map[string]interface{}{"message": device + " 已挂载到 " + mountPoint})
}

// handleUnmount 卸载分区
func handleUnmount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	target := r.FormValue("target")
	if target == "" {
		http.Error(w, `{"error": "target (mountpoint or device) required"}`, http.StatusBadRequest)
		return
	}
	out, err := common.SudoExec("umount", target)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, out+": "+err.Error()), http.StatusInternalServerError)
		return
	}
	common.JSONResponse(w, map[string]interface{}{"message": target + " 已卸载"})
}

// handleMkdir 创建目录（用于挂载点或数据目录）
func handleMkdir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	path := r.FormValue("path")
	if path == "" {
		http.Error(w, `{"error": "path required"}`, http.StatusBadRequest)
		return
	}
	// 安全检查：只允许在 /data 下创建
	if !strings.HasPrefix(path, "/data/") && path != "/data" {
		http.Error(w, `{"error": "只允许在 /data/ 下创建目录"}`, http.StatusBadRequest)
		return
	}
	out, err := common.SudoExec("mkdir", "-p", path)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, out+": "+err.Error()), http.StatusInternalServerError)
		return
	}
	common.JSONResponse(w, map[string]interface{}{"message": "目录 " + path + " 已创建"})
}
