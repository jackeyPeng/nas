package dashboard

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"

	"nas-panel/common"
)

// ServiceDef defines a NAS service
type ServiceDef struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Port        string `json:"port"`
	Description string `json:"description"`
	// Optional 标记该服务是否为"协议服务"：新装机默认关闭，用户可在服务页
	// 一键开启/关闭（on/off 动作）。核心服务（Samba/面板/fail2ban）为 false，
	// 误关会影响面板自身可用性或基础文件共享，不提供一键关闭入口。
	Optional bool `json:"optional"`
}

// NasServices is the list of known NAS services
var NasServices = []ServiceDef{
	{Name: "smbd", DisplayName: "Samba SMB", Port: "139, 445", Description: "Windows/Mac/Linux 文件共享", Optional: false},
	{Name: "nmbd", DisplayName: "Samba NetBIOS", Port: "137-138", Description: "NetBIOS 名称服务", Optional: false},
	{Name: "nfs-kernel-server", DisplayName: "NFS", Port: "2049", Description: "Linux 文件共享", Optional: true},
	{Name: "vsftpd", DisplayName: "FTP", Port: "21", Description: "文件传输协议", Optional: true},
	{Name: "rclone-webdav", DisplayName: "WebDAV", Port: "8080", Description: "WebDAV 文件服务", Optional: true},
	{Name: "rclone-s3", DisplayName: "S3 对象存储", Port: "9000", Description: "S3 兼容对象存储 (rclone serve s3)", Optional: true},
	{Name: "fail2ban", DisplayName: "Fail2ban", Port: "-", Description: "入侵防护", Optional: false},
}

// IsOptionalService reports whether name refers to a registered optional
// (protocol) service — the gate used by the on/off actions.
func IsOptionalService(name string) bool {
	for _, svc := range NasServices {
		if svc.Name == name {
			return svc.Optional
		}
	}
	return false
}

// SystemInfo holds system overview data
type SystemInfo struct {
	Hostname  string `json:"hostname"`
	OS        string `json:"os"`
	Kernel    string `json:"kernel"`
	Uptime    string `json:"uptime"`
	UptimeSec int64  `json:"uptime_seconds"`
	CPUUsage  string `json:"cpu_usage"`
	MemTotal  string `json:"mem_total"`
	MemUsed   string `json:"mem_used"`
	MemPct    string `json:"mem_pct"`
	DiskTotal string `json:"disk_total"`
	DiskUsed  string `json:"disk_used"`
	DiskPct   string `json:"disk_pct"`
	CPUCores  int    `json:"cpu_cores"`
	LoadAvg   string `json:"load_avg"`
	IPAddr    string `json:"ip_addr"`
}

// GetSystemInfo collects system information
func GetSystemInfo() SystemInfo {
	info := SystemInfo{
		CPUCores: runtime.NumCPU(),
	}

	// Hostname
	if h, err := os.Hostname(); err == nil {
		info.Hostname = h
	}

	// OS version
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				info.OS = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
				break
			}
		}
	}

	// Kernel
	info.Kernel = unameR()

	// Uptime
	info.Uptime = getUptime()
	info.UptimeSec = getUptimeSeconds()

	// Memory
	info.MemTotal, info.MemUsed, info.MemPct = getMemInfo()

	// Disk
	info.DiskTotal, info.DiskUsed, info.DiskPct = getDiskInfo()

	// CPU usage
	info.CPUUsage = getCPUUsage()

	// Load average
	info.LoadAvg = getLoadAvg()

	// IP address
	info.IPAddr = getIPAddr()

	return info
}

// classifyServiceActive — systemctl is-active 的 stdout+err 归一为面板四态。
// exit 0: stdout 即状态(active);exit 3: inactive(已停止)或 failed;
// exit 4 或 unknown: 单元不存在 → not-installed。
// 旧实现把 exit 3 一律映射 failed,导致面板停止的服务显示「失败」+健康假告警。
func classifyServiceActive(stdout, errText string) string {
	state := strings.TrimSpace(stdout)
	if errText == "" {
		if state == "" {
			return "unknown"
		}
		return state // active
	}
	if strings.Contains(errText, "exit status 4") || state == "unknown" || state == "" {
		return "not-installed"
	}
	switch state {
	case "failed":
		return "failed"
	case "inactive":
		return "inactive"
	default:
		return state // activating / deactivating 等瞬态原样上报
	}
}

// GetServices returns all NAS services with their status
func GetServices() []map[string]interface{} {
	var result []map[string]interface{}
	for _, svc := range NasServices {
		out, err := common.ExecOutput("systemctl", "is-active", svc.Name)
		errText := ""
		if err != nil {
			errText = err.Error()
		}
		active := classifyServiceActive(out, errText)
		// alias 单元（nfs-kernel-server）is-enabled 恒报 alias，统一走 ResolveServiceEnabled
		enabled := common.ResolveServiceEnabled(svc.Name)
		result = append(result, map[string]interface{}{
			"name":         svc.Name,
			"display_name": svc.DisplayName,
			"port":         svc.Port,
			"description":  svc.Description,
			"active":       active,
			"enabled":      enabled,
			"optional":     svc.Optional,
		})
	}
	return result
}

// ServiceState is the minimal per-service state servicesSummary needs to
// compute the dashboard's "N 个服务 · M 个异常" count. Kept separate from
// the full GetServices() map so the counting rule is a pure, testable
// function of (optional, enabled, active) rather than JSON wrangling.
type ServiceState struct {
	Optional bool
	Enabled  bool
	Active   bool // true only for the "active" systemd state
}

// servicesSummary computes (total, bad) for the dashboard service-health
// card using the "核心服务 + 已启用的可选服务" rule:
//   - core service (Optional=false): always counted; bad if not active.
//   - optional service, disabled: not counted at all (not a denominator
//     member, never "bad") — a fresh install with all 4 protocols off must
//     not show "4 个异常".
//   - optional service, enabled but not active (stopped/failed): counted
//     and bad — the user turned it on, so it's expected to be healthy.
func servicesSummary(list []ServiceState) (total, bad int) {
	for _, s := range list {
		if s.Optional && !s.Enabled {
			continue
		}
		total++
		if !s.Active {
			bad++
		}
	}
	return total, bad
}

// handleDashboard returns system overview
func handleDashboard(w http.ResponseWriter, r *http.Request) {
	info := GetSystemInfo()
	services := GetServices()

	states := make([]ServiceState, 0, len(services))
	for _, svc := range services {
		optional, _ := svc["optional"].(bool)
		enabled, _ := svc["enabled"].(bool)
		active, _ := svc["active"].(string)
		states = append(states, ServiceState{
			Optional: optional,
			Enabled:  enabled,
			Active:   active == "active",
		})
	}
	totalServices, badServices := servicesSummary(states)
	activeCount := totalServices - badServices

	response := map[string]interface{}{
		"hostname":       info.Hostname,
		"os":             info.OS,
		"kernel":         info.Kernel,
		"uptime":         info.Uptime,
		"uptime_seconds": info.UptimeSec,
		"cpu_usage":      info.CPUUsage,
		"cpu_cores":      info.CPUCores,
		"mem_total":      info.MemTotal,
		"mem_used":       info.MemUsed,
		"mem_pct":        info.MemPct,
		"disk_total":     info.DiskTotal,
		"disk_used":      info.DiskUsed,
		"disk_pct":       info.DiskPct,
		"load_avg":       info.LoadAvg,
		"ip_addr":        info.IPAddr,
		"services":       services,
		"active_count":   activeCount,
		"total_services": totalServices,
		"bad_services":   badServices,
		// 前端据此在刷新/恢复会话后也弹出强制改密（不依赖登录响应）
		"must_change_password": common.IsDefaultPass(),
		"username":             common.GetNASUser(),
	}

	common.JSONResponse(w, response)
}

// RegisterRoutes registers dashboard routes on the given mux
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/dashboard", common.AuthMiddleware(handleDashboard))
}

// --- helpers ---

func unameR() string {
	out, err := common.ExecOutput("uname", "-r")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// getUptimeSeconds returns total uptime in seconds (for i18n formatting on frontend)
func getUptimeSeconds() int64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	parts := strings.Fields(string(data))
	if len(parts) < 1 {
		return 0
	}
	secs, _ := strconv.ParseFloat(parts[0], 64)
	return int64(secs)
}

// getUptime returns a human-readable uptime string (legacy, kept for backward compat)
func getUptime() string {
	secs := getUptimeSeconds()
	if secs <= 0 {
		return ""
	}
	days := secs / 86400
	hours := (secs % 86400) / 3600
	mins := (secs % 3600) / 60
	if days > 0 {
		return fmt.Sprintf("%d天 %d小时 %d分钟", days, hours, mins)
	}
	return fmt.Sprintf("%d小时 %d分钟", hours, mins)
}

func getMemInfo() (total, used, pct string) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return
	}
	var t, a float64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		val, _ := strconv.ParseFloat(fields[1], 64)
		switch fields[0] {
		case "MemTotal:":
			t = val
		case "MemAvailable:":
			a = val
		}
	}
	if t == 0 {
		return
	}
	u := t - a
	total = fmt.Sprintf("%.1f GB", t/1024/1024)
	used = fmt.Sprintf("%.1f GB", u/1024/1024)
	pct = fmt.Sprintf("%.1f%%", u/t*100)
	return
}

func getDiskInfo() (total, used, pct string) {
	// 系统盘 = 根分区（数据池容量由“存储总容量”单独展示）
	out, err := common.ExecOutput("df", "-h", "/")
	if err != nil {
		return
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		return
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 5 {
		return
	}
	total = fields[1]
	used = fields[2]
	pct = fields[4]
	return
}

func getCPUUsage() string {
	out, err := common.ExecOutput("top", "-bn1")
	if err != nil {
		return "N/A"
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "%Cpu(s)") {
			fields := strings.Fields(line)
			if len(fields) > 1 {
				return fields[1] + "%"
			}
		}
	}
	return "N/A"
}

func getLoadAvg() string {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(data))
	if len(fields) >= 3 {
		return fields[0] + " " + fields[1] + " " + fields[2]
	}
	return ""
}

func getIPAddr() string {
	out, err := common.ExecOutput("hostname", "-I")
	if err != nil {
		return ""
	}
	fields := strings.Fields(out)
	if len(fields) > 0 {
		return fields[0]
	}
	return ""
}
