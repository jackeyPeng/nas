package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

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

// getSystemInfo collects system information
func getSystemInfo() SystemInfo {
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
	info.Kernel = uname("-r")

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

func uname(args string) string {
	out, err := exec.Command("uname", strings.Fields(args)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
	out, err := exec.Command("df", "-h", "/data").Output()
	if err != nil {
		out, _ = exec.Command("df", "-h", "/").Output()
	}
	lines := strings.Split(string(out), "\n")
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
	// Use top in batch mode
	out, err := exec.Command("top", "-bn1").Output()
	if err != nil {
		return "N/A"
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "%Cpu(s)") {
			fields := strings.Fields(line)
			if len(fields) > 1 {
				return fields[1] + "%"
			}
		}
	}
	return "N/A"
}

// getDirSizes returns sizes of /data subdirectories
func getDirSizes() []map[string]string {
	out, err := exec.Command("du", "-sh", "/data/*/").Output()
	if err != nil {
		return nil
	}
	var dirs []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			dirs = append(dirs, map[string]string{
				"path": fields[1],
				"size": fields[0],
			})
		}
	}
	return dirs
}

// getSambaShares returns Samba share configuration
func getSambaShares() string {
	out, err := exec.Command("testparm", "-s").Output()
	if err != nil {
		// Try reading config directly
		data, _ := os.ReadFile("/etc/samba/smb.conf")
		return string(data)
	}
	return string(out)
}

// getNFSExports returns NFS export configuration
func getNFSExports() string {
	out, err := exec.Command("sudo", "exportfs", "-v").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// getSmartStatus returns SMART health for disks
func getSmartStatus() string {
	// Find disks
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
		out, err := exec.Command("sudo", "smartctl", "-H", "/dev/"+name).Output()
		if err == nil {
			result.WriteString("--- /dev/" + name + " ---\n")
			result.WriteString(string(out))
			result.WriteString("\n")
		}
	}
	return result.String()
}
