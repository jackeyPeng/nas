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
		registryDB, err = sql.Open("sqlite", registryDBPath())
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

// allRegistryItems returns the complete list of 46 registry items
func allRegistryItems() []RegistryItem {
	items := []RegistryItem{
		// 一、系统服务 (9项)
		{ID: 1, Category: "服务", Name: "smbd — Samba 文件共享", Description: "端口 139/445，systemd 服务"},
		{ID: 2, Category: "服务", Name: "nmbd — NetBIOS 名称解析", Description: "端口 137/138，systemd 服务"},
		{ID: 3, Category: "服务", Name: "nfs-kernel-server — NFS 服务", Description: "端口 2049，systemd 服务"},
		{ID: 4, Category: "服务", Name: "vsftpd — FTP 服务", Description: "端口 21 + 被动 30000-31000"},
		{ID: 5, Category: "服务", Name: "rclone-webdav — WebDAV", Description: "端口 8080，rclone serve webdav"},
		{ID: 6, Category: "服务", Name: "filebrowser — 网页文件管理", Description: "端口 8081，独立二进制"},
		{ID: 7, Category: "服务", Name: "rclone-s3 — S3 对象存储", Description: "端口 9000，rclone serve s3"},
		{ID: 8, Category: "服务", Name: "fail2ban — 入侵防护", Description: "SSH + FTP 暴力破解防护"},
		{ID: 9, Category: "服务", Name: "nas-panel — 管理面板", Description: "端口 8090，Go 单二进制"},

		// 二、配置文件 (10项)
		{ID: 10, Category: "配置", Name: "/etc/samba/smb.conf", Description: "Samba 共享配置，不应含 Z1 托管段"},
		{ID: 11, Category: "配置", Name: "/etc/exports", Description: "NFS 导出配置，不应含 Z1 托管段"},
		{ID: 12, Category: "配置", Name: "/etc/nfs.conf", Description: "NFS 固定端口 (lockd/mountd/statd)"},
		{ID: 13, Category: "配置", Name: "/etc/vsftpd.conf", Description: "FTP 配置，本地用户 + 被动端口"},
		{ID: 14, Category: "配置", Name: "/etc/vsftpd.userlist", Description: "FTP 允许用户列表"},
		{ID: 15, Category: "配置", Name: "/etc/fail2ban/jail.local", Description: "Fail2ban 规则"},
		{ID: 16, Category: "配置", Name: "/etc/rclone-htpasswd", Description: "WebDAV bcrypt 认证"},
		{ID: 17, Category: "配置", Name: "/etc/rclone/s3-env", Description: "S3 认证密钥"},
		{ID: 18, Category: "配置", Name: "/etc/sudoers.d/nas-panel", Description: "sudo 免密命令白名单"},
		{ID: 19, Category: "配置", Name: "/etc/fstab — /data 条目", Description: "不应有数据盘挂载条目"},

		// 三、systemd 服务文件 (4项)
		{ID: 20, Category: "systemd", Name: "rclone-webdav.service", Description: "/etc/systemd/system/"},
		{ID: 21, Category: "systemd", Name: "filebrowser.service", Description: "/etc/systemd/system/"},
		{ID: 22, Category: "systemd", Name: "rclone-s3.service", Description: "/etc/systemd/system/"},
		{ID: 23, Category: "systemd", Name: "nas-panel.service", Description: "/etc/systemd/system/"},

		// 四、二进制文件 (2项)
		{ID: 24, Category: "二进制", Name: "/usr/local/bin/filebrowser", Description: "FileBrowser 可执行文件"},
		{ID: 25, Category: "二进制", Name: "/usr/local/bin/nas-panel", Description: "NAS 管理面板可执行文件"},

		// 五、系统用户/密码 (4项)
		{ID: 26, Category: "用户", Name: "系统用户密码", Description: "passwd -S，用于 FTP/SSH 登录"},
		{ID: 27, Category: "用户", Name: "Samba 用户密码", Description: "smbpasswd，用于 SMB 共享"},
		{ID: 28, Category: "用户", Name: "WebDAV 认证", Description: "htpasswd，用于 rclone webdav"},
		{ID: 29, Category: "用户", Name: "FileBrowser 用户", Description: "FileBrowser 数据库中的 admin 用户"},

		// 六、面板状态文件 (4项)
		{ID: 30, Category: "状态", Name: "folders.db — 共享文件夹元数据", Description: "/opt/nas/data/，重置后应删除"},
		{ID: 31, Category: "状态", Name: ".last_reload — 配置重载时间戳", Description: "/opt/nas/data/，重置后应删除"},
		{ID: 32, Category: "状态", Name: "operations.db — 操作日志", Description: "/opt/nas/data/，重置后应删除"},
		{ID: 33, Category: "状态", Name: "filebrowser.db — FileBrowser 数据库", Description: "/etc/filebrowser/，重置后保留"},

		// 七、存储层 (6项)
		{ID: 34, Category: "存储", Name: "LVM 卷组 (VG)", Description: "vgs --noheadings，重置后应为空"},
		{ID: 35, Category: "存储", Name: "LVM 逻辑卷 (LV)", Description: "lvs --noheadings，重置后应为空"},
		{ID: 36, Category: "存储", Name: "LVM 物理卷 (PV)", Description: "pvs --noheadings，重置后应为空"},
		{ID: 37, Category: "存储", Name: "RAID 阵列 (/dev/md*)", Description: "ls /dev/md*，重置后应为空"},
		{ID: 38, Category: "存储", Name: "磁盘签名 (wipefs)", Description: "数据盘不应有 LVM/RAID 签名"},
		{ID: 39, Category: "存储", Name: "/data 数据盘挂载", Description: "df -h，不应有数据盘挂载"},

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

// seedRegistry populates the registry with all items
func seedRegistry(db *sql.DB) {
	for _, item := range allRegistryItems() {
		db.Exec(
			"INSERT INTO registry (id, category, name, status, description) VALUES (?, ?, ?, 'pending', ?)",
			item.ID, item.Category, item.Name, item.Description,
		)
	}
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

	now := time.Now().Format("2006-01-02 15:04:05")

	updateItem := func(id int, status, detail string) {
		db.Exec("UPDATE registry SET status=?, detail=?, checked_at=? WHERE id=?", status, detail, now, id)
	}

	checkService := func(id int, svcName string) {
		out, _ := common.ExecOutput("systemctl", "is-active", svcName)
		active := strings.TrimSpace(out) == "active"
		enabledOut, _ := common.ExecOutput("systemctl", "is-enabled", svcName)
		enabled := strings.TrimSpace(enabledOut) == "enabled"
		if active && enabled {
			updateItem(id, "pass", "active + enabled")
		} else if active {
			updateItem(id, "warn", "active but not enabled")
		} else {
			updateItem(id, "fail", fmt.Sprintf("状态: %s, 自启: %s", strings.TrimSpace(out), strings.TrimSpace(enabledOut)))
		}
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

	// 一、系统服务 (9项)
	checkService(1, "smbd")
	checkService(2, "nmbd")
	checkService(3, "nfs-kernel-server")
	checkService(4, "vsftpd")
	checkService(5, "rclone-webdav")
	checkService(6, "filebrowser")
	checkService(7, "rclone-s3")
	checkService(8, "fail2ban")
	checkService(9, "nas-panel")

	// 二、配置文件 (10项)
	checkFileContent(10, "/etc/samba/smb.conf", "Z1 MANAGED SHARES", false,
		"不含 Z1 托管段 (已清理)", "仍含 Z1 托管段")
	checkFileContent(11, "/etc/exports", "Z1 MANAGED SHARES", false,
		"不含 Z1 托管段 (已清理)", "仍含 Z1 托管段")
	checkFileState(12, "/etc/nfs.conf", true, "存在", "不存在")
	checkFileState(13, "/etc/vsftpd.conf", true, "存在", "不存在")
	checkFileState(14, "/etc/vsftpd.userlist", true, "存在", "不存在")
	checkFileState(15, "/etc/fail2ban/jail.local", true, "存在", "不存在")
	checkFileState(16, "/etc/rclone-htpasswd", true, "存在", "不存在")
	checkFileState(17, "/etc/rclone/s3-env", true, "存在", "不存在")
	checkFileState(18, "/etc/sudoers.d/nas-panel", true, "存在", "不存在")

	// 19. fstab
	fstabData, _ := common.SudoOutput("cat", "/etc/fstab")
	hasDataDisk := false
	for _, line := range strings.Split(fstabData, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "/data") && !strings.HasPrefix(trimmed, "#") {
			hasDataDisk = true
			break
		}
	}
	if hasDataDisk {
		updateItem(19, "fail", "存在数据盘挂载条目，应清除")
	} else {
		updateItem(19, "pass", "无数据盘挂载条目")
	}

	// 三、systemd 服务文件 (4项)
	checkFileState(20, "/etc/systemd/system/rclone-webdav.service", true, "存在", "不存在")
	checkFileState(21, "/etc/systemd/system/filebrowser.service", true, "存在", "不存在")
	checkFileState(22, "/etc/systemd/system/rclone-s3.service", true, "存在", "不存在")
	checkFileState(23, "/etc/systemd/system/nas-panel.service", true, "存在", "不存在")

	// 四、二进制文件 (2项)
	checkFileState(24, "/usr/local/bin/filebrowser", true, "存在", "不存在")
	checkFileState(25, "/usr/local/bin/nas-panel", true, "存在", "不存在")

	// 五、系统用户/密码 (4项)
	nasUser := os.Getenv("NAS_USER")
	if nasUser == "" {
		nasUser, _ = common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
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

	if _, err := os.Stat("/etc/filebrowser/filebrowser.db"); err == nil {
		out, _ = common.SudoOutput("sqlite3", "/etc/filebrowser/filebrowser.db",
			"SELECT username FROM users WHERE username='"+nasUser+"'")
		if strings.TrimSpace(out) != "" {
			updateItem(29, "pass", "已创建 ("+nasUser+")")
		} else {
			updateItem(29, "fail", "未找到用户 "+nasUser)
		}
	} else {
		updateItem(29, "fail", "FileBrowser 数据库不存在")
	}

	// 六、面板状态文件 (4项)
	checkFileState(30, "/opt/nas/data/folders.db", false, "已清理 (不存在)", "仍存在")
	checkFileState(31, "/opt/nas/data/.last_reload", false, "已清理 (不存在)", "仍存在")
	checkFileState(32, "/opt/nas/data/operations.db", false, "已清理 (不存在)", "仍存在")
	checkFileState(33, "/etc/filebrowser/filebrowser.db", true, "存在", "不存在")

	// 七、存储层 (6项)
	vgsOut, _ := common.SudoOutput("/usr/sbin/vgs", "--noheadings")
	if strings.TrimSpace(vgsOut) == "" {
		updateItem(34, "pass", "无 VG (已清理)")
	} else {
		updateItem(34, "fail", "存在 VG: "+strings.TrimSpace(vgsOut))
	}

	lvsOut, _ := common.SudoOutput("/usr/sbin/lvs", "--noheadings")
	if strings.TrimSpace(lvsOut) == "" {
		updateItem(35, "pass", "无 LV (已清理)")
	} else {
		updateItem(35, "fail", "存在 LV")
	}

	pvsOut, _ := common.SudoOutput("/usr/sbin/pvs", "--noheadings")
	if strings.TrimSpace(pvsOut) == "" {
		updateItem(36, "pass", "无 PV (已清理)")
	} else {
		updateItem(36, "fail", "存在 PV")
	}

	mdMatches, _ := filepath.Glob("/dev/md[0-9]*")
	mdRe := regexp.MustCompile(`^/dev/md\d+$`)
	mdCount := 0
	for _, dev := range mdMatches {
		if mdRe.MatchString(dev) {
			mdCount++
		}
	}
	if mdCount == 0 {
		updateItem(37, "pass", "无 RAID 设备")
	} else {
		updateItem(37, "fail", fmt.Sprintf("存在 %d 个 RAID 设备", mdCount))
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
	if !hasSignatures {
		updateItem(38, "pass", "数据盘无残留签名")
	} else {
		updateItem(38, "fail", "存在残留磁盘签名")
	}

	dataMounts := getDataMounts()
	dataDiskMounts := 0
	for _, m := range dataMounts {
		mp := m["mount"]
		if mp == "/data" || isDataNasMount(mp) {
			if strings.HasPrefix(m["device"], "/dev/") {
				dataDiskMounts++
			}
		}
	}
	if dataDiskMounts == 0 {
		updateItem(39, "pass", "无数据盘挂载")
	} else {
		updateItem(39, "fail", fmt.Sprintf("存在 %d 个数据盘挂载", dataDiskMounts))
	}

	// 八、防火墙 (1项)
	ufwOut, _ := common.SudoOutput("ufw", "status")
	if strings.Contains(ufwOut, "Status: active") {
		updateItem(40, "pass", "已启用")
	} else {
		updateItem(40, "fail", "未启用或状态异常")
	}

	// 九、定时任务 (2项)
	cronOut, _ := common.ExecOutput("crontab", "-l")
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
		30: {"pass", "已删除 folders.db"},
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
