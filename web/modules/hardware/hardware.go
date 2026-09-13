package hardware

// Hardware Profile 硬件抽象层（P1）
//
// 目的：把散落在 monitor/dashboard/diskmgmt/version 各处的硬件读取收拢到一个
// 统一的、可缓存的硬件档案 API，并做「平台识别」——同一套 Z1 软件跑在
// Mini PC / 4-Bay / 6-Bay / ARM / 虚拟机上时，前端不再各自去解析 /proc。
//
// v1 最小范围（按「先小后扩」原则）：
//   - 平台识别：Intel N 系列 / 通用 x86_64 / ARM64 / 树莓派 / 虚拟机
//   - 统一档案：CPU（型号/核/线程/主频）+ 内存总量 + 网卡（名/IP/MAC/状态）
//     + 物理盘（名/容量/型号/总线）+ CPU 温度（若传感器可用）
//   - 单一只读 API：GET /api/hardware/profile（带 30s 内存缓存，避免重复读 /proc）
// 风扇控制 / LED / 槽位映射（slot mapping）等依赖真实背板硬件的部分留在 P2+，
// 等有对应硬件可测再上。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"nas-panel/common"
)

// NetworkInterface 是一张网卡的档案。
type NetworkInterface struct {
	Name  string `json:"name"`
	MAC   string `json:"mac"`
	IP    string `json:"ip"`
	State string `json:"state"` // up | down
}

// DiskDevice 是一块物理盘（不含分区）的档案。
type DiskDevice struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`       // 字节
	SizeHuman string `json:"size_human"` // 如 "50G"
	Model     string `json:"model"`
	Transport string `json:"transport"` // sata | nvme | usb | virtio | ""
}

// CPUInfo 是 CPU 档案。
type CPUInfo struct {
	Model   string  `json:"model"`
	Cores   int     `json:"cores"`
	Threads int     `json:"threads"`
	MHz     float64 `json:"mhz"`
}

// Profile 是完整的硬件档案。
type Profile struct {
	Profile       string             `json:"profile"`        // 稳定标识，前端据此 i18n
	ProfileLabel  string             `json:"profile_label"`  // 中文兜底文案
	Arch          string             `json:"arch"`           // runtime.GOARCH
	Virtualized   bool               `json:"virtualized"`
	CPU           CPUInfo            `json:"cpu"`
	MemoryBytes   int64              `json:"memory_bytes"`
	MemoryHuman   string             `json:"memory_human"`
	Network       []NetworkInterface `json:"network"`
	Disks         []DiskDevice       `json:"disks"`
	Temperature   *float64           `json:"temperature"` // CPU 温度（摄氏度），nil = 不可用
	TempSource    string             `json:"temp_source"`
	Hostname      string             `json:"hostname"`
	OS            string             `json:"os"`
	Kernel        string             `json:"kernel"`
	UptimeSeconds int64              `json:"uptime_seconds"`
	SysDiskUsed   string             `json:"system_disk_used"`
	SysDiskTotal  string             `json:"system_disk_total"`
	SysDiskPct    string             `json:"system_disk_pct"`
	CheckedAt     string             `json:"checked_at"`
}

var (
	cacheMu     sync.Mutex
	cachedPro   *Profile
	cachedAt    time.Time
	cacheTTL    = 30 * time.Second
)

func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hardware/profile", common.AuthMiddleware(handleProfile))
}

func handleProfile(w http.ResponseWriter, r *http.Request) {
	common.JSONResponse(w, GetProfile())
}

// GetProfile 返回带缓存的硬件档案。所有读取都是只读、无副作用的系统查询。
func GetProfile() Profile {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cachedPro != nil && time.Since(cachedAt) < cacheTTL {
		return *cachedPro
	}
	p := collectProfile()
	cachedPro = &p
	cachedAt = time.Now()
	return p
}

func collectProfile() Profile {
	p := Profile{
		Arch:        runtime.GOARCH,
		CheckedAt:   time.Now().Format("2006-01-02 15:04:05"),
		Network:     []NetworkInterface{},
		Disks:       []DiskDevice{},
		Temperature: nil,
	}

	p.CPU = readCPU()
	p.MemoryBytes, p.MemoryHuman = readMemory()
	p.Network = readNetwork()
	p.Disks = readDisks()
	p.Temperature, p.TempSource = readCPUTemperature()
	p.Hostname, _ = os.Hostname()
	p.OS = readOSName()
	p.Kernel = readKernel()
	p.UptimeSeconds = readUptimeSeconds()
	p.SysDiskUsed, p.SysDiskTotal, p.SysDiskPct = readSystemDisk()

	// 平台识别：虚拟机优先，其次 CPU 型号 / 架构
	p.Virtualized = detectVirtualized()
	p.Profile, p.ProfileLabel = classifyProfile(p.CPU.Model, p.Virtualized, p.Arch)

	return p
}

// ── CPU ────────────────────────────────────────────────

func readCPU() CPUInfo {
	c := CPUInfo{}
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return c
	}
	threads := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "processor"):
			threads++
		case strings.HasPrefix(line, "model name") || strings.HasPrefix(line, "Hardware"):
			if c.Model == "" {
				if i := strings.Index(line, ":"); i >= 0 {
					c.Model = strings.TrimSpace(line[i+1:])
				}
			}
		case strings.HasPrefix(line, "cpu MHz"):
			if i := strings.Index(line, ":"); i >= 0 {
				if m, err := strconv.ParseFloat(strings.TrimSpace(line[i+1:]), 64); err == nil {
					c.MHz = m
				}
			}
		}
	}
	c.Threads = threads
	c.Cores = threads // 无可靠方式区分物理核/超线程时，与逻辑核一致
	return c
}

// ── 内存 ───────────────────────────────────────────────

func readMemory() (int64, string) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, "未知"
	}
	var kb int64
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ = strconv.ParseInt(fields[1], 10, 64)
			}
			break
		}
	}
	bytes := kb * 1024
	return bytes, humanBytes(float64(bytes))
}

// ── 网卡 ───────────────────────────────────────────────

func readNetwork() []NetworkInterface {
	out := []NetworkInterface{}
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		if name == "lo" {
			continue
		}
		iface := NetworkInterface{Name: name}
		if b, err := os.ReadFile(filepath.Join("/sys/class/net", name, "address")); err == nil {
			iface.MAC = strings.TrimSpace(string(b))
		}
		if b, err := os.ReadFile(filepath.Join("/sys/class/net", name, "operstate")); err == nil {
			iface.State = strings.TrimSpace(string(b))
		}
		if b, err := os.ReadFile(filepath.Join("/sys/class/net", name, "carrier")); err == nil {
			// carrier 有值且为 1 = 有链路；operstate 通常已能反映
			_ = strings.TrimSpace(string(b))
		}
		// IP：只抓 IPv4 主地址（用 ip -4 -o addr show dev <name>）
		if ipOut, err := common.ExecOutput("ip", "-4", "-o", "addr", "show", "dev", name); err == nil {
			for _, line := range strings.Split(ipOut, "\n") {
				fields := strings.Fields(line)
				for i, f := range fields {
					if f == "inet" && i+1 < len(fields) {
						iface.IP = strings.Split(fields[i+1], "/")[0]
					}
				}
				if iface.IP != "" {
					break
				}
			}
		}
		out = append(out, iface)
	}
	return out
}

// ── 物理盘 ─────────────────────────────────────────────

func readDisks() []DiskDevice {
	out := []DiskDevice{}
	// 用 JSON 输出，避免 MODEL 含空格时按空白切分错位
	raw, err := common.ExecOutput("lsblk", "-d", "-b", "-J", "-o", "NAME,SIZE,MODEL,TRAN,TYPE")
	if err != nil {
		return out
	}
	var parsed struct {
		Blockdevices []struct {
			Name  string  `json:"name"`
			Size  int64   `json:"size"`
			Model *string `json:"model"`
			Tran  *string `json:"tran"`
			Type  string  `json:"type"`
		} `json:"blockdevices"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return out
	}
	for _, bd := range parsed.Blockdevices {
		if bd.Type == "rom" || strings.HasPrefix(bd.Name, "loop") || strings.HasPrefix(bd.Name, "ram") {
			continue
		}
		d := DiskDevice{
			Name:      bd.Name,
			Size:      bd.Size,
			SizeHuman: humanBytes(float64(bd.Size)),
		}
		if bd.Model != nil {
			d.Model = *bd.Model
		}
		if bd.Tran != nil {
			d.Transport = *bd.Tran
		}
		out = append(out, d)
	}
	return out
}

// ── 温度 ───────────────────────────────────────────────

func readCPUTemperature() (*float64, string) {
	zones, err := filepath.Glob("/sys/class/thermal/thermal_zone*")
	if err != nil || len(zones) == 0 {
		return nil, ""
	}
	// 优先 CPU 相关 zone（x86_pkg_temp / cpu-thermal / acpitz）
	var fallback *float64
	var fallbackSrc string
	for _, z := range zones {
		typB, _ := os.ReadFile(filepath.Join(z, "type"))
		typ := strings.TrimSpace(string(typB))
		tempB, err := os.ReadFile(filepath.Join(z, "temp"))
		if err != nil {
			continue
		}
		ms, err := strconv.ParseInt(strings.TrimSpace(string(tempB)), 10, 64)
		if err != nil {
			continue
		}
		c := float64(ms) / 1000.0
		if c <= 0 || c > 150 {
			continue // 无效读数
		}
		if fallback == nil {
			fb := c
			fallback = &fb
			fallbackSrc = typ
		}
		if strings.Contains(typ, "cpu") || strings.Contains(typ, "x86") || strings.Contains(typ, "acpitz") || strings.Contains(typ, "pkg") {
			return &c, typ
		}
	}
	return fallback, fallbackSrc
}

// ── 系统档案 ───────────────────────────────────────────

func readOSName() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
		}
	}
	return ""
}

func readKernel() string {
	out, _ := common.ExecOutput("uname", "-r")
	return strings.TrimSpace(out)
}

func readUptimeSeconds() int64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(data))
	if len(f) >= 1 {
		sec, _ := strconv.ParseFloat(f[0], 64)
		return int64(sec)
	}
	return 0
}

// readSystemDisk 读取系统盘（根挂载点）容量，返回 used / total / pct。
func readSystemDisk() (used, total, pct string) {
	out, err := common.ExecOutput("df", "-k", "/")
	if err != nil {
		return "", "", ""
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		return "", "", ""
	}
	// Filesystem 1K-blocks Used Available Use% Mounted on
	f := strings.Fields(lines[1])
	if len(f) < 5 {
		return "", "", ""
	}
	totalKB, _ := strconv.ParseInt(f[1], 10, 64)
	usedKB, _ := strconv.ParseInt(f[2], 10, 64)
	return humanBytes(float64(usedKB * 1024)), humanBytes(float64(totalKB * 1024)), f[4]
}

// ── 平台识别 ───────────────────────────────────────────

func detectVirtualized() bool {
	// 1) cpuinfo hypervisor 标志；2) /sys/class/dmi/id/product_name 常见虚拟化厂商
	data, _ := os.ReadFile("/proc/cpuinfo")
	if strings.Contains(strings.ToLower(string(data)), "hypervisor") {
		return true
	}
	for _, p := range []string{
		"/sys/class/dmi/id/product_name",
		"/sys/class/dmi/id/sys_vendor",
	} {
		if b, err := os.ReadFile(p); err == nil {
			v := strings.ToLower(strings.TrimSpace(string(b)))
			for _, k := range []string{"vmware", "qemu", "virtualbox", "kvm", "xen", "microsoft", "bochs"} {
				if strings.Contains(v, k) {
					return true
				}
			}
		}
	}
	return false
}

func classifyProfile(model string, virtualized bool, arch string) (string, string) {
	prefix := ""
	if virtualized {
		prefix = "vm-"
	}
	lower := strings.ToLower(model)

	// 树莓派（/proc/cpuinfo 的 Hardware 字段形如 "BCM2835"）
	if strings.Contains(lower, "raspberry pi") || strings.Contains(lower, "bcm2") || strings.Contains(lower, "bcm3") || strings.Contains(lower, "bcm4") {
		return prefix + "raspberry-pi", "树莓派"
	}
	// Intel N 系列 / 低功耗 NAS 常见 SoC
	for _, k := range []string{"n150", "n100", "n200", "n305", "n95", "n5105", "n6005", "j4125", "j5040", "j4105"} {
		if strings.Contains(lower, k) {
			return prefix + "intel-n", "Intel N 系列（N100/N150 等）"
		}
	}
	if arch == "arm64" || arch == "arm" {
		return prefix + "generic-arm64", "通用 ARM64"
	}
	return prefix + "generic-x86_64", "通用 x86_64"
}

// humanBytes 把字节数格式化成人类可读（GB 取整，带单位）。
func humanBytes(b float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	i := 0
	for b >= 1024 && i < len(units)-1 {
		b /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", b, units[i])
	}
	if b >= 100 {
		return fmt.Sprintf("%.0f %s", b, units[i])
	}
	return fmt.Sprintf("%.1f %s", b, units[i])
}
