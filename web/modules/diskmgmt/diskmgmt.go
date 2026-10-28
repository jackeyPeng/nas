package diskmgmt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"nas-panel/common"
)

// jsonError 统一错误响应体的 Content-Type：diskmgmt 下大量 handler 此前直调
// http.Error 手拼 `{"error":"..."}` 字符串，但 http.Error 固定写 text/plain，
// 导致前端 api() 的 JSON 自动 toast 守卫（按 Content-Type 判断）失效——错误体
// 虽然长得像 JSON，实际走的是 app.js api() 的字符串兜底分支（非空字符串视为
// truthy，调用方 if(data) 判定会误把失败当成功，典型案例：handlePoolDelete
// 的 pvs 失败 500 时前端弹"删除成功"）。这里统一设 application/json 并用
// json.Marshal 生成体（而非 fmt.Sprintf 拼接），err 字符串含引号/反斜杠等
// 字符时也能产出合法 JSON，不需要调用方自己处理转义。参照 modules/ai/ai.go
// 的同名 helper，两个包各自私有一份（不新增跨包依赖）。
func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	body, err := json.Marshal(map[string]string{"error": msg})
	if err != nil {
		// json.Marshal 对 map[string]string 实际不会失败；兜底仍给出合法 JSON。
		body = []byte(`{"error":"内部错误"}`)
	}
	w.Write(body)
}

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
	// Operations (deprecated low-level APIs — kept for debugging, not used by frontend;
	// B1：专用整机定位下这几个接口不再走正常产品路径，默认关闭，只有显式开调试
	// 开关才放行，避免被当成面板支持的"通用挂载/格式化"能力误用)
	mux.HandleFunc("/api/disk/format", common.AuthMiddleware(debugDiskGate(handleFormat)))
	mux.HandleFunc("/api/disk/mount", common.AuthMiddleware(debugDiskGate(handleMount)))
	mux.HandleFunc("/api/disk/unmount", common.AuthMiddleware(debugDiskGate(handleUnmount)))
	mux.HandleFunc("/api/disk/mkdir", common.AuthMiddleware(debugDiskGate(handleMkdir)))
	// Storage pool (LVM)
	mux.HandleFunc("/api/disk/pool/status", common.AuthMiddleware(handlePoolStatus))
	mux.HandleFunc("/api/disk/pool/create", common.AuthMiddleware(handlePoolCreate))
	mux.HandleFunc("/api/disk/pool/extend", common.AuthMiddleware(handlePoolExtend))
	mux.HandleFunc("/api/disk/pool/extend-stream", common.AuthMiddleware(handlePoolExtendStream))
	mux.HandleFunc("/api/disk/pool/delete", common.AuthMiddleware(handlePoolDelete))
	// Import existing pool (换机恢复)
	mux.HandleFunc("/api/disk/import", common.AuthMiddleware(handleImportList))
	mux.HandleFunc("/api/disk/import/pool", common.AuthMiddleware(handleImportPool))
	// RAID expansion
	mux.HandleFunc("/api/disk/raid/expand-stream", common.AuthMiddleware(handleRAIDExpandStream))
	mux.HandleFunc("/api/disk/raid/expand-fs", common.AuthMiddleware(handleRAIDExpandFS))
	mux.HandleFunc("/api/disk/raid/reshape-status", common.AuthMiddleware(handleRAIDReshapeStatus))
	// Maintenance operations log
	mux.HandleFunc("/api/disk/operations", common.AuthMiddleware(handleOperationsLog))
	// Replace disk
	mux.HandleFunc("/api/disk/replace", common.AuthMiddleware(handleReplaceDisk))
	// Scrub
	mux.HandleFunc("/api/disk/scrub", common.AuthMiddleware(handleScrub))
	mux.HandleFunc("/api/disk/scrub/status", common.AuthMiddleware(handleScrubStatus))
	// SMART scan
	mux.HandleFunc("/api/disk/smart-scan", common.AuthMiddleware(handleSMARTScan))
	// Folder quota
	mux.HandleFunc("/api/disk/folders/quota", common.AuthMiddleware(handleFolderQuota))
	// Shared folders
	mux.HandleFunc("/api/disk/folders", common.AuthMiddleware(handleListFolders))
	mux.HandleFunc("/api/disk/folders/create", common.AuthMiddleware(handleCreateFolder))
	mux.HandleFunc("/api/disk/folders/delete", common.AuthMiddleware(handleDeleteFolder))
	mux.HandleFunc("/api/disk/folders/permission", common.AuthMiddleware(handleFolderPermission))
	// Config sync
	mux.HandleFunc("/api/disk/config/check", common.AuthMiddleware(handleConfigCheck))
	mux.HandleFunc("/api/disk/config/sync", common.AuthMiddleware(handleConfigSync))
	// Pending operations
	mux.HandleFunc("/api/disk/pending", common.AuthMiddleware(handlePendingList))
	mux.HandleFunc("/api/disk/pending/apply", common.AuthMiddleware(handlePendingApply))
	mux.HandleFunc("/api/disk/pending/discard", common.AuthMiddleware(handlePendingDiscard))
	// Operation logs
	mux.HandleFunc("/api/disk/oplogs", common.AuthMiddleware(handleOperationLogs))
	mux.HandleFunc("/api/disk/oplogs/clear", common.AuthMiddleware(handleOperationLogsClear))
	// Wizard (simple storage setup)
	mux.HandleFunc("/api/disk/wizard/status", common.AuthMiddleware(handleWizardStatus))
	// Wizard setup (non-stream) deprecated — use setup-stream instead
	mux.HandleFunc("/api/disk/wizard/setup", common.AuthMiddleware(handleWizardSetup))
	mux.HandleFunc("/api/disk/wizard/reset", common.AuthMiddleware(handleWizardReset))
	// Stream (progressive setup/reset)
	mux.HandleFunc("/api/disk/wizard/setup-stream", common.AuthMiddleware(handleWizardSetupStream))
	mux.HandleFunc("/api/disk/wizard/reset-stream", common.AuthMiddleware(handleWizardResetStream))
	// Per-share recycle bin (TODO #25)
	RegisterRecycleRoutes(mux)
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
		// Support sd*, nvme*, vd* devices
		if !strings.HasPrefix(name, "sd") && !strings.HasPrefix(name, "nvme") && !strings.HasPrefix(name, "vd") {
			continue
		}
		// Skip partitions (nvme0n1p1, sda1, vda1)
		if regexp_match(`\d+p\d+`, name) || (strings.HasPrefix(name, "sd") && regexp_match(`sd[a-z]+\d+$`, name)) || (strings.HasPrefix(name, "vd") && regexp_match(`vd[a-z]+\d+$`, name)) {
			continue
		}
		// Skip partition numbers for nvme (nvme0n1 is whole disk, nvme0n1p1 is partition)
		if strings.HasPrefix(name, "nvme") && strings.Contains(name, "p") {
			continue
		}
		out, err := cachedSmartctl(smartctlCacheTTL, "-a", "/dev/"+name)
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

// debugDiskGate 包一层守卫：只有显式开启 NAS_DISK_DEBUG=1 才放行底层
// format/mount/unmount/mkdir 四个接口，否则一律 403。这四个接口在专用整机
// 定位下已无正常产品入口（前端零引用，B1），保留 handler 本体只是方便调试，
// 不应作为面板对外的常规能力继续暴露。
func debugDiskGate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("NAS_DISK_DEBUG") != "1" {
			jsonError(w, "低级别磁盘接口已禁用（调试模式未开启）", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

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
	// 白名单校验：只允许 /dev/ 下真实存在的块设备（加固轮1 #1）
	if err := common.ValidateBlockDevice(device); err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusBadRequest)
		return
	}
	// 安全检查：不允许格式化系统盘
	if isSystemDisk(device) {
		http.Error(w, `{"error": "不允许格式化系统盘"}`, http.StatusBadRequest)
		return
	}
	// 需要二次确认：必须输入设备路径
	confirm := r.FormValue("confirm")
	confirmToken := r.FormValue("confirm_token")
	if confirm != "yes" || confirmToken != device {
		http.Error(w, `{"error": "请输入设备路径确认格式化"}`, http.StatusBadRequest)
		return
	}
	common.LockDiskOp()
	defer common.UnlockDiskOp()
	out, err := common.SudoExec("mkfs."+fstype, mkfsArgs(fstype, device)...)
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
	// 白名单校验：设备 + 挂载点（加固轮1 #1）
	if err := common.ValidateBlockDevice(device); err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusBadRequest)
		return
	}
	if cleaned, err := common.ValidateMountPoint(mountPoint); err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusBadRequest)
		return
	} else {
		mountPoint = cleaned
	}
	common.LockDiskOp()
	defer common.UnlockDiskOp()
	// 创建挂载点
	common.SudoExec("mkdir", "-p", mountPoint)
	// 挂载
	var out string
	var err error
	if fstype == "xfs" {
		out, err = common.SudoExec("mount", "-t", fstype, "-o", "prjquota", device, mountPoint)
	} else if fstype != "" {
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
	// 安全检查：target 可以是挂载点或设备路径，此前直接把 FormValue 拼进
	// umount 命令，没有走 handleMount 那条 ValidateMountPoint/ValidateBlockDevice
	// 校验链（加固轮1 #1 漏补的一处）——umount 本身不能改写 /etc/shadow，但
	// 未经校验的 target 能卸载任意已挂载路径（如 /、/boot），属于可用面板
	// API 发起的系统级 DoS。按两种合法形状之一校验，都不满足才拒绝。
	if cleaned, err := common.ValidateMountPoint(target); err == nil {
		target = cleaned
	} else if devErr := common.ValidateBlockDevice(target); devErr != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusBadRequest)
		return
	}
	common.LockDiskOp()
	defer common.UnlockDiskOp()
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
	// 安全检查：Clean 后仍须在 /data 下（加固轮1 #1，阻断 /data/../etc 穿越）
	cleanedPath, err := common.ValidateDataPath(path, true)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusBadRequest)
		return
	}
	path = cleanedPath
	out, err := common.SudoExec("mkdir", "-p", path)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, out+": "+err.Error()), http.StatusInternalServerError)
		return
	}
	common.JSONResponse(w, map[string]interface{}{"message": "目录 " + path + " 已创建"})
}
