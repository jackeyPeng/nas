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
}

// NasServices is the list of known NAS services
var NasServices = []ServiceDef{
	{Name: "smbd", DisplayName: "Samba SMB", Port: "139, 445", Description: "Windows/Mac/Linux 文件共享"},
	{Name: "nmbd", DisplayName: "Samba NetBIOS", Port: "137-138", Description: "NetBIOS 名称服务"},
	{Name: "nfs-kernel-server", DisplayName: "NFS", Port: "2049", Description: "Linux 文件共享"},
	{Name: "vsftpd", DisplayName: "FTP", Port: "21", Description: "文件传输协议"},
	{Name: "rclone-webdav", DisplayName: "WebDAV", Port: "8080", Description: "WebDAV 文件服务"},
	{Name: "filebrowser", DisplayName: "FileBrowser", Port: "8081", Description: "Web 文件管理"},
	{Name: "rclone-s3", DisplayName: "S3 对象存储", Port: "9000", Description: "S3 兼容对象存储 (rclone serve s3)"},
	{Name: "fail2ban", DisplayName: "Fail2ban", Port: "-", Description: "入侵防护"},
}

// SystemInfo holds system overview data
type SystemInfo struct {
	Hostname  string `json:"hostname"`
	OS        string `json:"os"`
	Kernel    string `json:"kernel"`
	Uptime    string `json:"uptime"`
	CPUUsage  string `json:"cpu_usage"`
	MemTotal  string `json:"mem_total"`
	MemUsed   string `json:"mem_used"`
	MemPct    string `json:"mem_pct"`
	DiskTotal string `json:"disk_total"`
	DiskUsed  string `json:"disk_used"`
	DiskPct   string `json:"disk_pct"`
	CPUCores  int    `json:"cpu_cores"`
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

	// Memory
	info.MemTotal, info.MemUsed, info.MemPct = getMemInfo()

	// Disk
	info.DiskTotal, info.DiskUsed, info.DiskPct = getDiskInfo()

	// CPU usage
	info.CPUUsage = getCPUUsage()

	return info
}

// GetServices returns all NAS services with their status
func GetServices() []map[string]interface{} {
	var result []map[string]interface{}
	for _, svc := range NasServices {
		active := "unknown"
		out, err := common.ExecOutput("systemctl", "is-active", svc.Name)
		if err == nil {
			active = strings.TrimSpace(out)
		}
		result = append(result, map[string]interface{}{
			"name":         svc.Name,
			"display_name": svc.DisplayName,
			"port":         svc.Port,
			"description":  svc.Description,
			"active":       active,
		})
	}
	return result
}

// handleDashboard returns system overview
func handleDashboard(w http.ResponseWriter, r *http.Request) {
	info := GetSystemInfo()
	services := GetServices()

	activeCount := 0
	for _, svc := range services {
		if svc["active"] == "active" {
			activeCount++
		}
	}

	response := map[string]interface{}{
		"hostname":       info.Hostname,
		"os":             info.OS,
		"kernel":         info.Kernel,
		"uptime":         info.Uptime,
		"cpu_usage":      info.CPUUsage,
		"cpu_cores":      info.CPUCores,
		"mem_total":      info.MemTotal,
		"mem_used":       info.MemUsed,
		"mem_pct":        info.MemPct,
		"disk_total":     info.DiskTotal,
		"disk_used":      info.DiskUsed,
		"disk_pct":       info.DiskPct,
		"services":       services,
		"active_count":   activeCount,
		"total_services": len(services),
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

func getUptime() string {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return ""
	}
	parts := strings.Fields(string(data))
	if len(parts) < 1 {
		return ""
	}
	secs, _ := strconv.ParseFloat(parts[0], 64)
	days := int(secs) / 86400
	hours := (int(secs) % 86400) / 3600
	mins := (int(secs) % 3600) / 60
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
	out, err := common.ExecOutput("df", "-h", "/data")
	if err != nil {
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
