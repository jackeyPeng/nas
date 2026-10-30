package system

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"nas-panel/common"

	_ "modernc.org/sqlite"
)

// RegistryItem is a single configuration item in the system registry
type RegistryItem struct {
	ID          int    `json:"id"`
	Category    string `json:"category"`
	Name        string `json:"name"`
	Status      string `json:"status"` // "pass", "fail", "warn", "pending"
	Detail      string `json:"detail"`
	CheckedAt   string `json:"checked_at"`
	Description string `json:"description"`
}

// RegistryReport is the full registry check report
type RegistryReport struct {
	Total     int            `json:"total"`
	Passed    int            `json:"passed"`
	Failed    int            `json:"failed"`
	Warn      int            `json:"warn"`
	Pending   int            `json:"pending"`
	Items     []RegistryItem `json:"items"`
	Summary   string         `json:"summary"`
	CheckedAt string         `json:"checked_at"`
}

var (
	registryDB     *sql.DB
	registryDBOnce sync.Once
)

// registryDBPath returns the path to the registry database
func registryDBPath() string {
	dir := "/opt/nas/data"
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "registry.db")
}

// initRegistryDB opens the registry database and seeds it on first run
func initRegistryDB() *sql.DB {
	registryDBOnce.Do(func() {
		var err error
		registryDB, err = common.OpenSQLite(registryDBPath())
		if err != nil {
			log.Printf("[REGISTRY] 无法打开注册表数据库: %v", err)
			return
		}

		registryDB.Exec(`CREATE TABLE IF NOT EXISTS registry (
			id INTEGER PRIMARY KEY,
			category TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			detail TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			checked_at TEXT NOT NULL DEFAULT ''
		)`)

		// Seed if empty
		var count int
		registryDB.QueryRow("SELECT COUNT(*) FROM registry").Scan(&count)
		if count == 0 {
			seedRegistry(registryDB)
			log.Printf("[REGISTRY] 注册表已初始化: %d 项", countAllItems())
		}
	})
	return registryDB
}

// countAllItems returns the total number of registry items
func countAllItems() int {
	return len(allRegistryItems())
}

// allRegistryItems returns the complete list of 41 registry items
func allRegistryItems() []RegistryItem {
	items := []RegistryItem{
		// 一、系统服务 (8项)
		{ID: 1, Category: "服务", Name: "smbd — Samba 文件共享", Description: "端口 139/445，systemd 服务"},
		{ID: 2, Category: "服务", Name: "nmbd — NetBIOS 名称解析", Description: "端口 137/138，systemd 服务"},
		{ID: 3, Category: "服务", Name: "nfs-kernel-server — NFS 服务", Description: "端口 2049，systemd 服务"},
		{ID: 4, Category: "服务", Name: "vsftpd — FTP 服务", Description: "端口 21 + 被动 30000-31000"},
		{ID: 5, Category: "服务", Name: "rclone-webdav — WebDAV", Description: "端口 8080，rclone serve webdav"},
		{ID: 7, Category: "服务", Name: "rclone-s3 — S3 对象存储", Description: "端口 9000，rclone serve s3"},
		{ID: 8, Category: "服务", Name: "fail2ban — 入侵防护", Description: "SSH + FTP 暴力破解防护"},
		{ID: 9, Category: "服务", Name: "nas-panel — 管理面板", Description: "端口 8090，Go 单二进制"},

		// 二、配置文件 (10项)
		{ID: 10, Category: "配置", Name: "/etc/samba/smb.conf", Description: "Samba 共享配置，应含 Z1 托管段 + public 共享"},
		{ID: 11, Category: "配置", Name: "/etc/exports", Description: "NFS 导出配置，应导出 /data/nas1/public"},
		{ID: 12, Category: "配置", Name: "/etc/nfs.conf", Description: "NFS 固定端口 (lockd/mountd/statd)"},
		{ID: 13, Category: "配置", Name: "/etc/vsftpd.conf", Description: "FTP 配置，本地用户 + 被动端口"},
		{ID: 14, Category: "配置", Name: "/etc/vsftpd.userlist", Description: "FTP 允许用户列表"},
		{ID: 15, Category: "配置", Name: "/etc/fail2ban/jail.local", Description: "Fail2ban 规则"},
		{ID: 16, Category: "配置", Name: "/etc/rclone-htpasswd", Description: "WebDAV bcrypt 认证"},
		{ID: 17, Category: "配置", Name: "/etc/rclone/s3-env", Description: "S3 认证密钥"},
		{ID: 18, Category: "配置", Name: "/etc/sudoers.d/nas-panel", Description: "sudo 免密命令白名单"},
		{ID: 19, Category: "配置", Name: "/etc/fstab — /data/nas1 条目", Description: "应有存储池挂载条目"},

		// 三、systemd 服务文件 (3项)
		{ID: 20, Category: "systemd", Name: "rclone-webdav.service", Description: "/etc/systemd/system/"},
		{ID: 22, Category: "systemd", Name: "rclone-s3.service", Description: "/etc/systemd/system/"},
		{ID: 23, Category: "systemd", Name: "nas-panel.service", Description: "/etc/systemd/system/"},

		// 四、二进制文件 (1项)
		{ID: 25, Category: "二进制", Name: "/usr/local/bin/nas-panel", Description: "NAS 管理面板可执行文件"},

		// 五、系统用户/密码 (3项)
		{ID: 26, Category: "用户", Name: "系统用户密码", Description: "passwd -S，用于 FTP/SSH 登录"},
		{ID: 27, Category: "用户", Name: "Samba 用户密码", Description: "smbpasswd，用于 SMB 共享"},
		{ID: 28, Category: "用户", Name: "WebDAV 认证", Description: "htpasswd，用于 rclone webdav"},

		// 六、面板状态文件 (3项)
		{ID: 30, Category: "状态", Name: "folders.db — 共享文件夹元数据", Description: "/opt/nas/data/，应有 public + home 记录"},
		{ID: 31, Category: "状态", Name: ".last_reload — 配置重载时间戳", Description: "/opt/nas/data/，应存在"},
		{ID: 32, Category: "状态", Name: "operations.db — 操作日志", Description: "/opt/nas/data/，重置后应删除"},

		// 七、存储层 (6项，按池模式感知判定)
		{ID: 34, Category: "存储", Name: "LVM 卷组 (VG)", Description: "vgs --noheadings，按池模式判定：LVM 模式应存在，RAID/其他模式不适用"},
		{ID: 35, Category: "存储", Name: "LVM 逻辑卷 (LV)", Description: "lvs --noheadings，按池模式判定：LVM 模式应存在，RAID/其他模式不适用"},
		{ID: 36, Category: "存储", Name: "LVM 物理卷 (PV)", Description: "pvs --noheadings，按池模式判定：LVM 模式应存在，RAID/其他模式不适用"},
		{ID: 37, Category: "存储", Name: "RAID 阵列 (/dev/md*)", Description: "按池模式判定：RAID 池查阵列健康(降级 fail)；merge/未建池时存在 md 设备即 fail"},
		{ID: 38, Category: "存储", Name: "磁盘签名 (wipefs)", Description: "数据盘应有签名，LVM 模式认 LVM 签名、RAID 模式认 RAID 成员签名"},
		{ID: 39, Category: "存储", Name: "/data/nas1 存储池挂载", Description: "df -h，存储池应已挂载"},

		// 八、防火墙 (1项)
		{ID: 40, Category: "防火墙", Name: "UFW 防火墙", Description: "应启用，默认 deny 入站"},

		// 九、定时任务 (2项)
		{ID: 41, Category: "定时任务", Name: "监控 cron (每5分钟)", Description: "crontab 含 monitor.sh"},
		{ID: 42, Category: "定时任务", Name: "备份 cron (每周日)", Description: "crontab 含 backup-config.sh"},

		// 十、目录结构 (3项)
		{ID: 43, Category: "目录", Name: "/data 目录", Description: "存在，owner=NAS_USER"},
		{ID: 44, Category: "目录", Name: "/opt/nas 软链接", Description: "→ ~/soft/nas"},
		{ID: 45, Category: "目录", Name: "/var/lib/nas-monitor", Description: "监控告警状态目录"},

		// 十一、软件包 (1项)
		{ID: 46, Category: "软件包", Name: "核心软件包 (10个)", Description: "samba, nfs-*, vsftpd, rclone, fail2ban, ufw, smartmontools, xfsprogs, mdadm, lvm2"},
	}
	return items
}

// procMdstatPath is the path to the kernel's RAID status file.
// It is a variable so tests can keep it unreferenced and exercise
// parseMdstat directly against fixture strings instead.
var procMdstatPath = "/proc/mdstat"

// detectPoolMode classifies the storage pool mode from the /data/nasN
// mount's source device string.
//   - "/dev/md0", "/dev/md/0"            → "raid"
//   - "/dev/mapper/*", "/dev/dm-*"       → "lvm"
//   - "" (not mounted / not found)       → "none"
//   - anything else (e.g. /dev/sdb1)     → "other"
func detectPoolMode(device string) string {
	switch {
	case device == "":
		return "none"
	case strings.HasPrefix(device, "/dev/md"):
		return "raid"
	case strings.HasPrefix(device, "/dev/mapper/"), strings.HasPrefix(device, "/dev/dm-"):
		return "lvm"
	default:
		return "other"
	}
}

// MdArray describes a single RAID array as reported by /proc/mdstat.
type MdArray struct {
	Name      string // "md0"
	Level     string // "raid1"
	Slots     string // "[UU]", raw slot string
	Degraded  bool   // slot string contains '_'
	Resync    string // "30.1%", empty if not resyncing/recovering/reshaping/checking
	ResyncETA string // "2.9min" from "finish=2.9min", empty if not present/not resyncing
}

var (
	mdstatHeaderRe = regexp.MustCompile(`^(md\d+)\s*:\s*\S+\s+(\S+)`)
	mdstatSlotsRe  = regexp.MustCompile(`(\[[U_]+\])\s*$`)
	mdstatPctRe    = regexp.MustCompile(`(resync|recovery|reshape|check)\s*=\s*([0-9.]+%)`)
	mdstatFinishRe = regexp.MustCompile(`finish=(\S+)`)
)

// parseMdstat parses the textual contents of /proc/mdstat into a slice of
// MdArray. Returns an empty slice when no "mdN : ..." array lines are present.
func parseMdstat(content string) []MdArray {
	var arrays []MdArray
	var current *MdArray

	for _, line := range strings.Split(content, "\n") {
		if m := mdstatHeaderRe.FindStringSubmatch(line); m != nil {
			arrays = append(arrays, MdArray{Name: m[1], Level: m[2]})
			current = &arrays[len(arrays)-1]
			continue
		}
		if current == nil {
			continue
		}
		if m := mdstatSlotsRe.FindStringSubmatch(line); m != nil {
			current.Slots = m[1]
			current.Degraded = strings.Contains(current.Slots, "_")
		}
		if m := mdstatPctRe.FindStringSubmatch(line); m != nil {
			current.Resync = m[2]
			if fm := mdstatFinishRe.FindStringSubmatch(line); fm != nil {
				current.ResyncETA = fm[1]
			}
		}
	}

	return arrays
}

// classifyMdstat classifies a single RAID array's health from the raw
// /proc/mdstat text block describing it (header line + slot/status lines,
// as produced for one "mdN : ..." entry). It distinguishes three cases:
//   - all slots present ([UUUU])                              → pass
//   - some slots missing but a recovery/resync/reshape/check
//     progress line is present (rebuild still running)        → warn
//   - some slots missing and no active rebuild progress line
//     (rebuild finished short, or never started)               → fail "阵列降级"
//
// This exists as a standalone pure function (operating directly on mdstat
// text) per the P2 audit brief, in addition to the MdArray-based path used
// by storageItemStates for the already-parsed multi-array case.
func classifyMdstat(devBlock string) (status, detail string) {
	arrays := parseMdstat(devBlock)
	if len(arrays) == 0 {
		return "fail", "无法解析 RAID 阵列状态"
	}
	a := arrays[0]
	return classifyMdArray(a)
}

// classifyMdArray applies the same pass/warn/fail judgement as
// classifyMdstat to an already-parsed MdArray.
func classifyMdArray(a MdArray) (status, detail string) {
	if !a.Degraded {
		return "pass", fmt.Sprintf("%s %s [%s] 正常", a.Name, a.Level, strings.Trim(a.Slots, "[]"))
	}
	if a.Resync != "" {
		detail = fmt.Sprintf("初始重建 %s", a.Resync)
		if a.ResyncETA != "" {
			detail += fmt.Sprintf("，预计剩余 %s", a.ResyncETA)
		}
		return "warn", detail
	}
	return "fail", fmt.Sprintf("阵列降级: %s [%s]", a.Name, strings.Trim(a.Slots, "[]"))
}

// storageState is the pass/fail/warn verdict for a single registry item.
type storageState struct {
	status string
	detail string
}

// classifyOptionalProtocolService 是四个默认关闭协议服务（NFS/vsftpd/
// rclone-webdav/rclone-s3，注册表 ID 3/4/5/7）的注册表判定，三态：
//   - disabled 且 inactive → pass，「默认关闭，可在服务管理页开启」
//     （新装机四协议全关，不应把产品默认态报成失败，建池后仍要能到 41/41）；
//   - enabled 或 active → 沿用原有 checkService 语义：active 且 enabled 才 pass；
//   - enabled 但 inactive（或 active 但未 enabled）→ warn/fail，与旧版一致。
//
// 抽成纯函数（两个布尔入参）方便单测覆盖三态，不依赖真实 systemctl 调用。
func classifyOptionalProtocolService(enabled, active bool) (status, detail string) {
	if !enabled && !active {
		return "pass", "默认关闭，可在服务管理页开启"
	}
	if active && enabled {
		return "pass", "active + enabled"
	}
	if active {
		return "warn", "active but not enabled"
	}
	return "fail", fmt.Sprintf("状态: inactive, 自启: %s", map[bool]string{true: "enabled", false: "disabled"}[enabled])
}

// storageItemStates computes the pool-mode-aware verdicts for registry
// items 34-39 (LVM VG/LV/PV, RAID arrays, disk signature, pool mount).
func storageItemStates(mode string, hasVG, hasLV, hasPV bool, mdCount int, arrays []MdArray, hasSig bool, mountCount int) map[int]storageState {
	states := make(map[int]storageState)

	// 34/35/36: LVM VG/LV/PV
	lvmNames := map[int]string{34: "VG", 35: "LV", 36: "PV"}
	lvmNoneTexts := map[int]string{
		34: "无 LVM 卷组 (存储池未创建或非 LVM 模式)",
		35: "无 LVM 逻辑卷 (存储池未创建或非 LVM 模式)",
		36: "无 LVM 物理卷 (存储池未创建或非 LVM 模式)",
	}
	lvmPresent := map[int]bool{34: hasVG, 35: hasLV, 36: hasPV}
	for id, present := range lvmPresent {
		switch mode {
		case "lvm":
			if present {
				states[id] = storageState{"pass", "存在 " + lvmNames[id]}
			} else {
				states[id] = storageState{"fail", "池挂载为 LVM 但无 " + lvmNames[id] + "（状态矛盾）"}
			}
		case "raid":
			states[id] = storageState{"pass", "RAID 模式池，LVM 不适用"}
		case "none":
			// 池未挂载不等于 LVM 不存在：VG/LV/PV 在而未挂载是有价值的
			// 诊断信号（mount 失败/待重启），保持旧版「存在即 pass」语义。
			if present {
				states[id] = storageState{"pass", "存在 " + lvmNames[id] + "（但池未挂载）"}
			} else {
				states[id] = storageState{"warn", lvmNoneTexts[id]}
			}
		default: // other
			states[id] = storageState{"warn", "非标准池模式(设备直挂)，LVM 不适用"}
		}
	}

	// 37: RAID arrays
	switch mode {
	case "lvm":
		if mdCount > 0 {
			states[37] = storageState{"fail", "merge 模式不应存在 RAID 阵列(疑似重置残留)"}
		} else {
			states[37] = storageState{"pass", "无 RAID 设备"}
		}
	case "raid":
		if len(arrays) == 0 {
			states[37] = storageState{"fail", "挂载源为 RAID 但未见阵列（状态矛盾）"}
		} else {
			// 降级阵列区分「初始重建中」(resync/recovery 进度行存在，warn)
			// 与「真降级」(无重建进度，fail)，避免建池后 ~5 分钟重建窗口被误报故障。
			failDetail := ""
			warnDetail := ""
			for _, a := range arrays {
				if !a.Degraded {
					continue
				}
				st, d := classifyMdArray(a)
				if st == "fail" && failDetail == "" {
					failDetail = d
				} else if st == "warn" && warnDetail == "" {
					warnDetail = d
				}
			}
			switch {
			case failDetail != "":
				states[37] = storageState{"fail", failDetail}
			case warnDetail != "":
				states[37] = storageState{"warn", warnDetail}
			default:
				// 列出全部健康阵列（多阵列机器不只报第一个）
				parts := make([]string, 0, len(arrays))
				for _, a := range arrays {
					s := fmt.Sprintf("%s %s [%s]", a.Name, a.Level, strings.Trim(a.Slots, "[]"))
					if a.Resync != "" {
						s += fmt.Sprintf("(resync %s)", a.Resync)
					}
					parts = append(parts, s)
				}
				states[37] = storageState{"pass", "RAID 模式池使用中: " + strings.Join(parts, "; ")}
			}
		}
	case "none":
		if mdCount > 0 {
			states[37] = storageState{"fail", fmt.Sprintf("存在 %d 个 RAID 设备(未建池，重置后应为空)", mdCount)}
		} else {
			states[37] = storageState{"pass", "无 RAID 设备"}
		}
	default: // other
		if mdCount > 0 {
			states[37] = storageState{"warn", fmt.Sprintf("存在 %d 个 RAID 设备（非池挂载源）", mdCount)}
		} else {
			states[37] = storageState{"pass", "无 RAID 设备"}
		}
	}

	// 38: disk signature
	switch mode {
	case "lvm":
		if hasSig {
			states[38] = storageState{"pass", "数据盘已有 LVM 签名"}
		} else {
			states[38] = storageState{"warn", "数据盘无签名 (存储池未创建)"}
		}
	case "raid":
		if hasSig {
			// hasSig 是「任一数据盘有任一 wipefs 签名」的既有粗检查，文案不过度承诺成员签名类型
			states[38] = storageState{"pass", "数据盘已有签名 (RAID 模式预期为 linux_raid_member)"}
		} else {
			states[38] = storageState{"warn", "数据盘无签名"}
		}
	case "none":
		if hasSig {
			states[38] = storageState{"pass", "数据盘已有签名"}
		} else {
			states[38] = storageState{"warn", "数据盘无签名 (存储池未创建)"}
		}
	default: // other
		if hasSig {
			states[38] = storageState{"pass", "数据盘已有签名"}
		} else {
			states[38] = storageState{"warn", "数据盘无签名"}
		}
	}

	// 39: pool mount — mode-independent
	if mountCount > 0 {
		states[39] = storageState{"pass", fmt.Sprintf("存储池已挂载 (%d 个)", mountCount)}
	} else {
		states[39] = storageState{"warn", "存储池 /data/nas1 未挂载 (待配置)"}
	}

	return states
}

// seedRegistry populates the registry with all items
func seedRegistry(db *sql.DB) {
	for _, item := range allRegistryItems() {
		db.Exec(
			"INSERT INTO registry (id, category, name, status, description) VALUES (?, ?, ?, 'pending', ?)",
			item.ID, item.Category, item.Name, item.Description,
		)
	}
}

// syncRegistryMeta keeps name/description aligned with the canonical list in code.
// Status/detail are preserved so upgraded machines don't lose their last check result,
// but renamed/redescribed items reflect the current model. New items are inserted as pending.
func syncRegistryMeta(db *sql.DB) {
	items := allRegistryItems()
	for _, item := range items {
		db.Exec(`INSERT INTO registry (id, category, name, status, detail, description, checked_at)
			VALUES (?, ?, ?, 'pending', '', ?, '')
			ON CONFLICT(id) DO UPDATE SET category=excluded.category, name=excluded.name, description=excluded.description`,
			item.ID, item.Category, item.Name, item.Description,
		)
	}
	// 剪除已从目录退役的条目（如 FileBrowser ID 6/21/24/29/33，beta.24 移除）：
	// 升级后旧 registry.db 的残留行不再被任何检查刷新，会变成幽灵项永远出现在报告里。
	// ID 全部来自代码内目录常量，拼接无注入面。
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, fmt.Sprintf("%d", item.ID))
	}
	db.Exec("DELETE FROM registry WHERE id NOT IN (" + strings.Join(ids, ",") + ")")
}

// GetRegistry returns all registry items with current status
func GetRegistry() RegistryReport {
	db := initRegistryDB()
	if db == nil {
		return RegistryReport{Summary: "注册表数据库不可用"}
	}

	report := RegistryReport{}
	report.CheckedAt = time.Now().Format("2006-01-02 15:04:05")

	rows, err := db.Query("SELECT id, category, name, status, detail, description, checked_at FROM registry ORDER BY id")
	if err != nil {
		report.Summary = fmt.Sprintf("查询失败: %v", err)
		return report
	}
	defer rows.Close()

	for rows.Next() {
		var item RegistryItem
		rows.Scan(&item.ID, &item.Category, &item.Name, &item.Status, &item.Detail, &item.Description, &item.CheckedAt)
		report.Total++
		switch item.Status {
		case "pass":
			report.Passed++
		case "fail":
			report.Failed++
		case "warn":
			report.Warn++
		case "pending":
			report.Pending++
		}
		report.Items = append(report.Items, item)
	}

	if report.Failed == 0 && report.Warn == 0 && report.Pending == 0 {
		report.Summary = fmt.Sprintf("全部 %d 项通过 ✅", report.Total)
	} else if report.Failed == 0 && report.Pending == 0 {
		report.Summary = fmt.Sprintf("%d 通过, %d 警告 ⚠️", report.Passed, report.Warn)
	} else {
		report.Summary = fmt.Sprintf("%d 通过, %d 失败 ❌, %d 警告 ⚠️, %d 待检", report.Passed, report.Failed, report.Warn, report.Pending)
	}

	return report
}

// RefreshRegistry re-checks all items and updates the database
func RefreshRegistry() RegistryReport {
	db := initRegistryDB()
	if db == nil {
		return RegistryReport{Summary: "注册表数据库不可用"}
	}

	// 同步 canonical 名称/描述（升级后旧注册表不会停留在旧模型的文案上）
	syncRegistryMeta(db)

	now := time.Now().Format("2006-01-02 15:04:05")

	updateItem := func(id int, status, detail string) {
		db.Exec("UPDATE registry SET status=?, detail=?, checked_at=? WHERE id=?", status, detail, now, id)
	}

	checkService := func(id int, svcName string) {
		out, _ := common.ExecOutput("systemctl", "is-active", svcName)
		active := strings.TrimSpace(out) == "active"
		enabledOut, _ := common.ExecOutput("systemctl", "is-enabled", svcName)
		enabled := common.ResolveServiceEnabled(svcName)
		if active && enabled {
			updateItem(id, "pass", "active + enabled")
		} else if active {
			updateItem(id, "warn", "active but not enabled")
		} else {
			updateItem(id, "fail", fmt.Sprintf("状态: %s, 自启: %s", strings.TrimSpace(out), strings.TrimSpace(enabledOut)))
		}
	}

	// checkOptionalProtocolService 是四个默认关闭协议服务(ID 3/4/5/7)专用，
	// 走 classifyOptionalProtocolService 的三态判定(disabled+inactive = pass)。
	checkOptionalProtocolService := func(id int, svcName string) {
		out, _ := common.ExecOutput("systemctl", "is-active", svcName)
		active := strings.TrimSpace(out) == "active"
		// alias 单元（nfs-kernel-server → nfs-server）is-enabled 恒报 "alias"，
		// 直接采信会把默认关闭的 NFS 判成 enabled+inactive=fail
		enabled := common.ResolveServiceEnabled(svcName)
		status, detail := classifyOptionalProtocolService(enabled, active)
		updateItem(id, status, detail)
	}

	checkFileState := func(id int, path string, shouldExist bool, okMsg, failMsg string) {
		_, err := os.Stat(path)
		if err == nil && shouldExist {
			updateItem(id, "pass", okMsg)
		} else if err != nil && !shouldExist {
			updateItem(id, "pass", okMsg)
		} else if shouldExist {
			updateItem(id, "fail", failMsg+" (不存在)")
		} else {
			updateItem(id, "fail", failMsg+" (仍存在)")
		}
	}

	checkFileContent := func(id int, path, contains string, shouldContain bool, okMsg, failMsg string) {
		data, err := common.SudoOutput("cat", path)
		if err != nil {
			if d, e := os.ReadFile(path); e == nil {
				data = string(d)
			} else {
				updateItem(id, "fail", "无法读取: "+path)
				return
			}
		}
		has := strings.Contains(data, contains)
		if shouldContain == has {
			updateItem(id, "pass", okMsg)
		} else {
			updateItem(id, "fail", failMsg)
		}
	}

	// 一、系统服务 (8项)
	checkService(1, "smbd")
	checkService(2, "nmbd")
	checkOptionalProtocolService(3, "nfs-kernel-server")
	checkOptionalProtocolService(4, "vsftpd")
	checkOptionalProtocolService(5, "rclone-webdav")
	checkOptionalProtocolService(7, "rclone-s3")
	checkService(8, "fail2ban")
	checkService(9, "nas-panel")

	// 二、配置文件 (10项)
	smbData, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")
	if strings.Contains(smbData, "# === Z1 MANAGED SHARES START ===") {
		updateItem(10, "pass", "含 Z1 托管段")
	} else {
		updateItem(10, "warn", "缺 Z1 托管段 (存储池未配置或未同步)")
	}
	checkFileContent(11, "/etc/exports", "/data/nas1/public", true,
		"已导出 /data/nas1/public", "未导出 /data/nas1/public")
	checkFileState(12, "/etc/nfs.conf", true, "存在", "不存在")
	checkFileState(13, "/etc/vsftpd.conf", true, "存在", "不存在")
	checkFileState(14, "/etc/vsftpd.userlist", true, "存在", "不存在")
	checkFileState(15, "/etc/fail2ban/jail.local", true, "存在", "不存在")
	checkFileState(16, "/etc/rclone-htpasswd", true, "存在", "不存在")
	checkFileState(17, "/etc/rclone/s3-env", true, "存在", "不存在")
	checkFileState(18, "/etc/sudoers.d/nas-panel", true, "存在", "不存在")

	// 19. fstab — 存储池 /data/nas1 挂载条目应存在
	fstabData, _ := common.SudoOutput("cat", "/etc/fstab")
	hasPoolMount := false
	for _, line := range strings.Split(fstabData, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "/data/nas1") && !strings.HasPrefix(trimmed, "#") {
			hasPoolMount = true
			break
		}
	}
	if hasPoolMount {
		updateItem(19, "pass", "存在 /data/nas1 挂载条目")
	} else {
		updateItem(19, "warn", "存储池未创建，待配置 /data/nas1 挂载")
	}

	// 三、systemd 服务文件 (3项)
	checkFileState(20, "/etc/systemd/system/rclone-webdav.service", true, "存在", "不存在")
	checkFileState(22, "/etc/systemd/system/rclone-s3.service", true, "存在", "不存在")
	checkFileState(23, "/etc/systemd/system/nas-panel.service", true, "存在", "不存在")

	// 四、二进制文件 (1项)
	checkFileState(25, "/usr/local/bin/nas-panel", true, "存在", "不存在")

	// 五、系统用户/密码 (3项)
	nasUser := os.Getenv("NAS_USER")
	if nasUser == "" {
		nasUser = common.GetNASUser()
	}
	if nasUser == "" {
		nasUser = "root"
	}

	out, _ := common.SudoOutput("passwd", "-S", nasUser)
	if strings.Contains(out, " P ") {
		updateItem(26, "pass", "已设置 ("+nasUser+")")
	} else {
		updateItem(26, "warn", "状态异常: "+strings.TrimSpace(out))
	}

	out, _ = common.SudoOutput("pdbedit", "-L")
	if strings.Contains(out, nasUser) {
		updateItem(27, "pass", "已创建 ("+nasUser+")")
	} else {
		updateItem(27, "fail", "未找到 Samba 用户 "+nasUser)
	}

	data, err := common.SudoOutput("cat", "/etc/rclone-htpasswd")
	if err == nil && strings.Contains(data, nasUser) {
		updateItem(28, "pass", "已配置 ("+nasUser+")")
	} else {
		updateItem(28, "fail", "未配置")
	}

	// 六、面板状态文件 (3项)
	// 30. folders.db — 应有 public + home 记录
	if _, err := os.Stat("/opt/nas/data/folders.db"); err == nil {
		var folderCount int
		if db, err := common.OpenSQLite("/opt/nas/data/folders.db"); err == nil {
			db.QueryRow("SELECT COUNT(*) FROM folders").Scan(&folderCount)
			db.Close()
		}
		if folderCount > 0 {
			updateItem(30, "pass", fmt.Sprintf("已就绪 (%d 条记录)", folderCount))
		} else {
			updateItem(30, "warn", "存储池未初始化，folders.db 无记录")
		}
	} else {
		updateItem(30, "warn", "folders.db 不存在")
	}
	if _, err := os.Stat("/opt/nas/data/.last_reload"); err == nil {
		updateItem(31, "pass", "存在")
	} else {
		updateItem(31, "warn", "不存在 (首次配置同步后生成)")
	}
	checkFileState(32, "/opt/nas/data/operations.db", false, "已清理 (不存在)", "仍存在")

	// 七、存储层 (6项) — 按池模式感知判定
	vgsOut, _ := common.SudoOutput("/usr/sbin/vgs", "--noheadings")
	hasVG := strings.TrimSpace(vgsOut) != ""

	lvsOut, _ := common.SudoOutput("/usr/sbin/lvs", "--noheadings")
	hasLV := strings.TrimSpace(lvsOut) != ""

	pvsOut, _ := common.SudoOutput("/usr/sbin/pvs", "--noheadings")
	hasPV := strings.TrimSpace(pvsOut) != ""

	mdMatches, _ := filepath.Glob("/dev/md[0-9]*")
	mdRe := regexp.MustCompile(`^/dev/md\d+$`)
	mdCount := 0
	for _, dev := range mdMatches {
		if mdRe.MatchString(dev) {
			mdCount++
		}
	}

	disks := getDataDiskDevices()
	hasSignatures := false
	for _, dev := range disks {
		wipeOut, _ := common.SudoOutput("/usr/sbin/wipefs", dev)
		if strings.TrimSpace(wipeOut) != "" {
			hasSignatures = true
			break
		}
	}

	dataMounts := getDataMounts()
	poolDevice := ""
	dataDiskMounts := 0
	for _, m := range dataMounts {
		mp := m["mount"]
		if !isDataNasMount(mp) {
			continue
		}
		if strings.HasPrefix(m["device"], "/dev/") {
			dataDiskMounts++
		}
		if mp == "/data/nas1" {
			poolDevice = m["device"]
		} else if poolDevice == "" {
			poolDevice = m["device"]
		}
	}
	// 模式判定只认 /data/nas1 的挂载源；nas1 未挂载时回退取 df 序第一个
	// /data/nasN（GNU df 按挂载点排序，实际为最小 nasN）。单池产品形态下
	// 无歧义；未来支持多池时需改为显式读取池配置（见 TODO #34 遗留）。
	poolMode := detectPoolMode(poolDevice)

	var mdArrays []MdArray
	mdContent, mdReadErr := os.ReadFile(procMdstatPath)
	if mdReadErr == nil {
		mdArrays = parseMdstat(string(mdContent))
	}

	states := storageItemStates(poolMode, hasVG, hasLV, hasPV, mdCount, mdArrays, hasSignatures, dataDiskMounts)
	for _, id := range []int{34, 35, 36, 38, 39} {
		updateItem(id, states[id].status, states[id].detail)
	}
	if poolMode == "raid" && mdReadErr != nil {
		updateItem(37, "fail", "无法读取 mdstat")
	} else {
		updateItem(37, states[37].status, states[37].detail)
	}

	// 八、防火墙 (1项)
	ufwOut, _ := common.SudoOutput("ufw", "status")
	if strings.Contains(ufwOut, "Status: active") {
		updateItem(40, "pass", "已启用")
	} else {
		updateItem(40, "fail", "未启用或状态异常")
	}

	// 九、定时任务 (2项)
	cronOut, _ := common.SudoOutput("crontab", "-u", nasUser, "-l")
	if strings.Contains(cronOut, "monitor.sh") {
		updateItem(41, "pass", "已配置")
	} else {
		updateItem(41, "warn", "未配置")
	}
	if strings.Contains(cronOut, "backup-config.sh") {
		updateItem(42, "pass", "已配置")
	} else {
		updateItem(42, "warn", "未配置")
	}

	// 十、目录结构 (3项)
	if _, err := os.Stat("/data"); err == nil {
		updateItem(43, "pass", "存在")
	} else {
		updateItem(43, "fail", "不存在")
	}

	if info, err := os.Lstat("/opt/nas"); err == nil && info.Mode()&os.ModeSymlink != 0 {
		link, _ := os.Readlink("/opt/nas")
		updateItem(44, "pass", "→ "+link)
	} else {
		updateItem(44, "warn", "不存在或不是软链接")
	}

	if _, err := os.Stat("/var/lib/nas-monitor"); err == nil {
		updateItem(45, "pass", "存在")
	} else {
		updateItem(45, "warn", "不存在")
	}

	// 十一、软件包 (1项)
	requiredPkgs := []string{"samba", "nfs-kernel-server", "vsftpd", "rclone", "fail2ban", "ufw", "smartmontools", "xfsprogs", "mdadm", "lvm2"}
	missingPkgs := []string{}
	for _, pkg := range requiredPkgs {
		cmdOut, err := exec.Command("dpkg", "-s", pkg).Output()
		if err != nil || !strings.Contains(string(cmdOut), "Status: install ok installed") {
			missingPkgs = append(missingPkgs, pkg)
		}
	}
	if len(missingPkgs) == 0 {
		updateItem(46, "pass", "全部已安装")
	} else {
		updateItem(46, "fail", "缺失: "+strings.Join(missingPkgs, ", "))
	}

	return GetRegistry()
}

// MarkRegistryItem updates a single registry item status
func MarkRegistryItem(id int, status, detail string) {
	db := initRegistryDB()
	if db == nil {
		return
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	db.Exec("UPDATE registry SET status=?, detail=?, checked_at=? WHERE id=?", status, detail, now, id)
}

// MarkRegistryItemsBulk updates multiple items at once
func MarkRegistryItemsBulk(updates map[int][2]string) {
	db := initRegistryDB()
	if db == nil {
		return
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	for id, v := range updates {
		db.Exec("UPDATE registry SET status=?, detail=?, checked_at=? WHERE id=?", v[0], v[1], now, id)
	}
}

// ResetRegistryAfterFactoryReset marks all items that should be clean after factory reset
func ResetRegistryAfterFactoryReset() {
	// Mark storage items as cleaned
	MarkRegistryItemsBulk(map[int][2]string{
		10: {"pass", "已清理 Z1 托管段"},
		11: {"pass", "已清理 Z1 托管段"},
		19: {"pass", "已清理 /data 挂载条目"},
		30: {"pass", "已清空 folders.db"},
		31: {"pass", "已删除 .last_reload"},
		32: {"pass", "已删除 operations.db"},
		34: {"pass", "已删除所有 VG"},
		35: {"pass", "已删除所有 LV"},
		36: {"pass", "已删除所有 PV"},
		37: {"pass", "已停止所有 RAID"},
		38: {"pass", "已清除磁盘签名"},
		39: {"pass", "已卸载数据盘"},
		43: {"pass", "已重建 /data 目录"},
	})
}
