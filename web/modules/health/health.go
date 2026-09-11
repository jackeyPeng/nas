package health

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"nas-panel/common"
	"nas-panel/modules/dashboard"
)

// Health Center：聚合各子系统健康状态，输出 ok / info / warn / error 四级。
// 每个检查都是独立、只读、轻量的系统查询，不与其它模块强耦合。

// Check 是单条健康检查结果。
type Check struct {
	ID     string `json:"id"`     // 稳定标识，前端据此做 i18n
	Label  string `json:"label"`  // 中文兜底文案
	Status string `json:"status"` // ok | info | warn | error
	Detail string `json:"detail"` // 一句话明细
	Icon   string `json:"icon"`   // 前端展示用 emoji
}

func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/health", common.AuthMiddleware(handleHealth))
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	checks := []Check{
		checkSystem(),
		checkStorage(),
		checkNetwork(),
		checkBackup(),
		checkSecurity(),
		checkCloudSync(),
	}

	overall := "ok"
	for _, c := range checks {
		if c.Status == "error" {
			overall = "error"
			break
		}
	}
	if overall != "error" {
		for _, c := range checks {
			if c.Status == "warn" {
				overall = "warn"
				break
			}
		}
	}

	common.JSONResponse(w, map[string]interface{}{
		"overall":    overall,
		"checks":     checks,
		"checked_at": time.Now().Format("2006-01-02 15:04:05"),
	})
}

// checkSystem：核心服务运行状态。
func checkSystem() Check {
	services := dashboard.GetServices()
	total := len(services)
	active, failed, notInstalled := 0, 0, 0
	for _, s := range services {
		switch s["active"] {
		case "active":
			active++
		case "failed":
			failed++
		case "not-installed":
			notInstalled++
		}
	}

	c := Check{ID: "system", Label: "系统服务", Icon: "🖥️"}
	switch {
	case failed > 0:
		c.Status = "error"
		c.Detail = fmt.Sprintf("%d/%d 服务运行中，%d 个异常", active, total, failed)
	case notInstalled > 0:
		c.Status = "warn"
		c.Detail = fmt.Sprintf("%d/%d 服务运行中，%d 个未安装", active, total, notInstalled)
	default:
		c.Status = "ok"
		c.Detail = fmt.Sprintf("%d/%d 服务全部运行中", active, total)
	}
	return c
}

// checkStorage：存储池存在性 + LV 健康 + RAID 降级检测。
func checkStorage() Check {
	c := Check{ID: "storage", Label: "存储", Icon: "💾"}

	// LVM 池存在性
	vgsOut, _ := common.SudoOutput("vgs", "--noheadings", "-o", "vg_name,vg_size,vg_free")
	vgsOut = strings.TrimSpace(vgsOut)
	if vgsOut == "" {
		// 无 LVM 池时再看是否有 RAID 阵列
		if hasRAID() {
			c.Status = "ok"
			c.Detail = "RAID 阵列存在"
			return c
		}
		c.Status = "warn"
		c.Detail = "尚未创建存储池"
		return c
	}

	fields := strings.Fields(vgsOut)
	vgName := fields[0]
	size := "未知"
	if len(fields) >= 2 {
		size = fields[1]
	}

	// LV 健康状态（RAID-backed LV 才会返回非空，普通 LVM 为空 = 正常）
	degraded := false
	lvsOut, _ := common.SudoOutput("lvs", "--noheadings", "-o", "lv_health_status")
	for _, line := range strings.Split(lvsOut, "\n") {
		hs := strings.TrimSpace(line)
		if hs != "" && hs != "ok" {
			degraded = true
			break
		}
	}

	// mdadm RAID 降级检测
	if mdDegraded() {
		degraded = true
	}

	if degraded {
		c.Status = "error"
		c.Detail = fmt.Sprintf("存储池 %s 降级（容量 %s）", vgName, size)
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("存储池 %s 健康（容量 %s）", vgName, size)
	return c
}

// checkNetwork：网络连通性（有 IP 即视为在线）。
func checkNetwork() Check {
	c := Check{ID: "network", Label: "网络", Icon: "🌐"}
	out, _ := common.ExecOutput("hostname", "-I")
	ip := ""
	if trimmed := strings.TrimSpace(out); trimmed != "" {
		ip = strings.Fields(trimmed)[0]
	}
	if ip == "" {
		c.Status = "error"
		c.Detail = "未检测到网络连接"
		return c
	}
	c.Status = "ok"
	c.Detail = "已连接，IP " + ip
	return c
}

// checkBackup：最近一次配置备份的时效。
func checkBackup() Check {
	c := Check{ID: "backup", Label: "备份", Icon: "🗂️"}
	files, _ := filepath.Glob("/opt/nas/backups/config-*.tar.gz")
	if len(files) == 0 {
		c.Status = "error"
		c.Detail = "尚未创建过配置备份"
		return c
	}
	sort.Slice(files, func(i, j int) bool {
		// 按文件名里的时间戳排序（config-YYYYMMDD-HHMMSS.tar.gz）
		return files[i] > files[j]
	})
	newest := files[0]
	info, err := os.Stat(newest)
	if err != nil {
		c.Status = "warn"
		c.Detail = "备份状态未知"
		return c
	}
	age := time.Since(info.ModTime())
	days := int(age.Hours() / 24)
	switch {
	case days < 7:
		c.Status = "ok"
	case days < 30:
		c.Status = "warn"
	default:
		c.Status = "error"
	}
	if days == 0 {
		c.Detail = "最近备份在今天"
	} else {
		c.Detail = fmt.Sprintf("最近备份 %d 天前", days)
	}
	return c
}

// checkSecurity：防火墙 + fail2ban。
func checkSecurity() Check {
	c := Check{ID: "security", Label: "安全", Icon: "🔒"}
	ufwOut, _ := common.SudoOutput("ufw", "status")
	ufwActive := strings.Contains(ufwOut, "Status: active")

	f2bOut, _ := common.ExecOutput("systemctl", "is-active", "fail2ban")
	f2bActive := strings.TrimSpace(f2bOut) == "active"

	switch {
	case ufwActive && f2bActive:
		c.Status = "ok"
		c.Detail = "防火墙已启用，Fail2ban 运行中"
	case ufwActive || f2bActive:
		c.Status = "warn"
		c.Detail = "部分防护未启用"
	default:
		c.Status = "error"
		c.Detail = "防火墙与 Fail2ban 均未启用"
	}
	return c
}

// checkCloudSync：rclone 远端配置数量。
func checkCloudSync() Check {
	c := Check{ID: "cloud_sync", Label: "云端同步", Icon: "☁️"}
	out, err := common.ExecOutput("rclone", "listremotes")
	if err != nil {
		c.Status = "warn"
		c.Detail = "rclone 不可用"
		return c
	}
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	if n == 0 {
		c.Status = "info"
		c.Detail = "未配置远端（可选功能）"
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("已配置 %d 个远端", n)
	return c
}

// hasRAID：检查 /proc/mdstat 是否含活动阵列。
func hasRAID() bool {
	data, err := os.ReadFile("/proc/mdstat")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "md") {
			return true
		}
	}
	return false
}

// mdDegraded：检查 RAID 阵列是否有降级（missing/failed 盘）。
func mdDegraded() bool {
	data, err := os.ReadFile("/proc/mdstat")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "md") {
			continue
		}
		// 状态形如 [UUU_] 或 [UU__]，含 _ 表示缺盘
		if strings.Contains(line, "_") {
			return true
		}
	}
	return false
}
