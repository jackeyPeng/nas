package system

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"nas-panel/common"
)

// CheckItem represents a single configuration item check
type CheckItem struct {
	ID       int    `json:"id"`
	Category string `json:"category"`
	Name     string `json:"name"`
	Status   string `json:"status"` // "pass", "fail", "warn", "skip"
	Detail   string `json:"detail"`
	Action   string `json:"action,omitempty"` // fix action if failed
}

// CheckReport is the full system configuration check report
type CheckReport struct {
	Total   int         `json:"total"`
	Passed  int         `json:"passed"`
	Failed  int         `json:"failed"`
	Warn    int         `json:"warn"`
	Items   []CheckItem `json:"items"`
	Summary string      `json:"summary"`
}

// RunSystemCheck runs all 46 configuration checks and returns a report
func RunSystemCheck() CheckReport {
	report := CheckReport{}

	// Helper to add a check item
	add := func(id int, cat, name, status, detail string) {
		item := CheckItem{ID: id, Category: cat, Name: name, Status: status, Detail: detail}
		switch status {
		case "pass":
			report.Passed++
		case "fail":
			report.Failed++
		case "warn":
			report.Warn++
		}
		report.Total++
		report.Items = append(report.Items, item)
	}

	checkService := func(id int, name string) {
		out, _ := common.ExecOutput("systemctl", "is-active", name)
		active := strings.TrimSpace(out) == "active"
		enabledOut, _ := common.ExecOutput("systemctl", "is-enabled", name)
		enabled := strings.TrimSpace(enabledOut) == "enabled"
		if active && enabled {
			add(id, "服务", name, "pass", "active + enabled")
		} else if active {
			add(id, "服务", name, "warn", "active but not enabled")
		} else {
			add(id, "服务", name, "fail", fmt.Sprintf("状态: %s, 自启: %s", strings.TrimSpace(out), strings.TrimSpace(enabledOut)))
		}
	}

	checkFile := func(id int, cat, path, desc string, required bool) {
		info, err := os.Stat(path)
		if err == nil {
			add(id, cat, desc, "pass", fmt.Sprintf("存在 (%s)", info.Mode()))
		} else if required {
			add(id, cat, desc, "fail", "缺失: "+path)
		} else {
			add(id, cat, desc, "pass", "已清理 (不存在)")
		}
	}

	checkFileContent := func(id int, cat, path, desc, contains string, shouldContain bool) {
		data, err := common.SudoOutput("cat", path)
		if err != nil || data == "" {
			if os.IsNotExist(err) {
				add(id, cat, desc, "fail", "文件不存在: "+path)
			} else {
				// Try without sudo
				if d, e := os.ReadFile(path); e == nil {
					data = string(d)
				} else {
					add(id, cat, desc, "fail", "无法读取: "+path)
					return
				}
			}
		}
		has := strings.Contains(data, contains)
		if shouldContain && has {
			add(id, cat, desc, "pass", "包含预期内容")
		} else if !shouldContain && !has {
			add(id, cat, desc, "pass", "不含预期内容 (已清理)")
		} else if shouldContain && !has {
			add(id, cat, desc, "fail", "缺少: "+contains)
		} else {
			add(id, cat, desc, "warn", "残留: "+contains)
		}
	}

	// ═══════════════════════════════════════
	// 一、系统服务 (9项)
	// ═══════════════════════════════════════
	services := []struct {
		id   int
		name string
	}{
		{1, "smbd"}, {2, "nmbd"}, {3, "nfs-kernel-server"},
		{4, "vsftpd"}, {5, "rclone-webdav"}, {6, "filebrowser"},
		{7, "rclone-s3"}, {8, "fail2ban"}, {9, "nas-panel"},
	}
	for _, s := range services {
		checkService(s.id, s.name)
	}

	// ═══════════════════════════════════════
	// 二、配置文件 (10项)
	// ═══════════════════════════════════════
	checkFileContent(10, "配置", "/etc/samba/smb.conf", "Samba 配置", "Z1 MANAGED SHARES", false)
	checkFileContent(11, "配置", "/etc/exports", "NFS 导出", "Z1 MANAGED SHARES", false)
	checkFile(12, "配置", "/etc/nfs.conf", "NFS 固定端口", true)
	checkFile(13, "配置", "/etc/vsftpd.conf", "FTP 配置", true)
	checkFile(14, "配置", "/etc/vsftpd.userlist", "FTP 用户列表", true)
	checkFile(15, "配置", "/etc/fail2ban/jail.local", "Fail2ban 规则", true)
	checkFile(16, "配置", "/etc/rclone-htpasswd", "WebDAV 认证", true)
	checkFile(17, "配置", "/etc/rclone/s3-env", "S3 认证密钥", true)
	checkFile(18, "配置", "/etc/sudoers.d/nas-panel", "sudo 免密权限", true)

	// 19. fstab
	fstabData, _ := common.SudoOutput("cat", "/etc/fstab")
	hasDataMount := strings.Contains(fstabData, "/data")
	if hasDataMount {
		// Only flag if it's a data disk mount, not system
		lines := strings.Split(fstabData, "\n")
		hasDataDisk := false
		for _, line := range lines {
			if strings.Contains(line, "/data") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
				hasDataDisk = true
				break
			}
		}
		if hasDataDisk {
			add(19, "配置", "fstab /data 条目", "fail", "存在数据盘挂载条目，应清除")
		} else {
			add(19, "配置", "fstab /data 条目", "pass", "无数据盘挂载条目")
		}
	} else {
		add(19, "配置", "fstab /data 条目", "pass", "无 /data 挂载条目")
	}

	// ═══════════════════════════════════════
	// 三、systemd 服务文件 (4项)
	// ═══════════════════════════════════════
	systemdFiles := []struct {
		id   int
		path string
		name string
	}{
		{20, "/etc/systemd/system/rclone-webdav.service", "rclone-webdav"},
		{21, "/etc/systemd/system/filebrowser.service", "filebrowser"},
		{22, "/etc/systemd/system/rclone-s3.service", "rclone-s3"},
		{23, "/etc/systemd/system/nas-panel.service", "nas-panel"},
	}
	for _, sf := range systemdFiles {
		checkFile(sf.id, "systemd", sf.path, sf.name+" 服务文件", true)
	}

	// ═══════════════════════════════════════
	// 四、二进制文件 (2项)
	// ═══════════════════════════════════════
	checkFile(24, "二进制", "/usr/local/bin/filebrowser", "FileBrowser 二进制", true)
	checkFile(25, "二进制", "/usr/local/bin/nas-panel", "NAS Panel 二进制", true)

	// ═══════════════════════════════════════
	// 五、系统用户/密码 (4项)
	// ═══════════════════════════════════════
	nasUser := os.Getenv("NAS_USER")
	if nasUser == "" {
		nasUser, _ = common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
	}
	if nasUser == "" {
		nasUser = "root"
	}

	// 26. 系统用户密码
	out, _ := common.SudoOutput("passwd", "-S", nasUser)
	if strings.Contains(out, " P ") {
		add(26, "用户", "系统用户密码 ("+nasUser+")", "pass", "已设置")
	} else {
		add(26, "用户", "系统用户密码 ("+nasUser+")", "warn", "状态: "+strings.TrimSpace(out))
	}

	// 27. Samba 用户
	out, _ = common.SudoOutput("pdbedit", "-L")
	if strings.Contains(out, nasUser) {
		add(27, "用户", "Samba 用户 ("+nasUser+")", "pass", "已创建")
	} else {
		add(27, "用户", "Samba 用户 ("+nasUser+")", "fail", "未找到")
	}

	// 28. WebDAV 认证
	data, err := common.SudoOutput("cat", "/etc/rclone-htpasswd")
	if err == nil && strings.Contains(data, nasUser) {
		add(28, "用户", "WebDAV 认证 ("+nasUser+")", "pass", "已配置")
	} else {
		add(28, "用户", "WebDAV 认证 ("+nasUser+")", "fail", "未配置")
	}

	// 29. FileBrowser 用户
	if _, err := os.Stat("/etc/filebrowser/filebrowser.db"); err == nil {
		out, _ = common.SudoOutput("sqlite3", "/etc/filebrowser/filebrowser.db",
			"SELECT username FROM users WHERE username='"+nasUser+"'")
		if strings.TrimSpace(out) != "" {
			add(29, "用户", "FileBrowser 用户 ("+nasUser+")", "pass", "已创建")
		} else {
			add(29, "用户", "FileBrowser 用户 ("+nasUser+")", "fail", "未找到")
		}
	} else {
		add(29, "用户", "FileBrowser 用户", "fail", "FileBrowser 数据库不存在")
	}

	// ═══════════════════════════════════════
	// 六、面板状态文件 (4项)
	// ═══════════════════════════════════════
	checkFile(30, "状态", "/opt/nas/data/folders.db", "共享文件夹元数据库", false)
	checkFile(31, "状态", "/opt/nas/data/.last_reload", "配置重载时间戳", false)
	checkFile(32, "状态", "/opt/nas/data/operations.db", "操作日志数据库", false)
	checkFile(33, "状态", "/etc/filebrowser/filebrowser.db", "FileBrowser 数据库", true)

	// ═══════════════════════════════════════
	// 七、存储层 (6项)
	// ═══════════════════════════════════════
	vgsOut, _ := common.SudoOutput("/usr/sbin/vgs", "--noheadings")
	if strings.TrimSpace(vgsOut) == "" {
		add(34, "存储", "LVM 卷组", "pass", "无 VG (已清理)")
	} else {
		vgCount := len(strings.Fields(vgsOut))
		add(34, "存储", "LVM 卷组", "fail", fmt.Sprintf("存在 %d 个 VG: %s", vgCount, strings.TrimSpace(vgsOut)))
	}

	lvsOut, _ := common.SudoOutput("/usr/sbin/lvs", "--noheadings")
	if strings.TrimSpace(lvsOut) == "" {
		add(35, "存储", "LVM 逻辑卷", "pass", "无 LV (已清理)")
	} else {
		add(35, "存储", "LVM 逻辑卷", "fail", "存在 LV: "+strings.TrimSpace(lvsOut))
	}

	pvsOut, _ := common.SudoOutput("/usr/sbin/pvs", "--noheadings")
	if strings.TrimSpace(pvsOut) == "" {
		add(36, "存储", "LVM 物理卷", "pass", "无 PV (已清理)")
	} else {
		add(36, "存储", "LVM 物理卷", "fail", "存在 PV: "+strings.TrimSpace(pvsOut))
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
		add(37, "存储", "RAID 阵列", "pass", "无 RAID 设备")
	} else {
		add(37, "存储", "RAID 阵列", "fail", fmt.Sprintf("存在 %d 个 RAID 设备", mdCount))
	}

	// 38. 磁盘签名
	disks := getDataDiskDevices()
	wipedDisks := 0
	hasSignatures := false
	for _, dev := range disks {
		wipeOut, _ := common.SudoOutput("/usr/sbin/wipefs", dev)
		if strings.TrimSpace(wipeOut) == "" {
			wipedDisks++
		} else {
			hasSignatures = true
		}
	}
	if !hasSignatures {
		add(38, "存储", "磁盘签名", "pass", fmt.Sprintf("%d 个数据盘无签名", wipedDisks))
	} else {
		add(38, "存储", "磁盘签名", "fail", "存在残留签名")
	}

	// 39. /data 挂载
	dataMounts := getDataMounts()
	dataDiskMounts := 0
	for _, m := range dataMounts {
		mp := m["mount"]
		if mp == "/data" || isDataNasMount(mp) {
			// Check if it's a real mount (not just a directory)
			dev := m["device"]
			if strings.HasPrefix(dev, "/dev/") {
				dataDiskMounts++
			}
		}
	}
	if dataDiskMounts == 0 {
		add(39, "存储", "/data 挂载", "pass", "无数据盘挂载")
	} else {
		add(39, "存储", "/data 挂载", "fail", fmt.Sprintf("存在 %d 个数据盘挂载", dataDiskMounts))
	}

	// ═══════════════════════════════════════
	// 八、防火墙 (1项)
	// ═══════════════════════════════════════
	ufwOut, _ := common.SudoOutput("ufw", "status")
	if strings.Contains(ufwOut, "Status: active") {
		add(40, "防火墙", "UFW 防火墙", "pass", "已启用")
	} else {
		add(40, "防火墙", "UFW 防火墙", "fail", "未启用或状态异常")
	}

	// ═══════════════════════════════════════
	// 九、定时任务 (2项)
	// ═══════════════════════════════════════
	cronOut, _ := common.ExecOutput("crontab", "-l")
	if strings.Contains(cronOut, "monitor.sh") {
		add(41, "定时任务", "监控 cron", "pass", "已配置")
	} else {
		add(41, "定时任务", "监控 cron", "warn", "未配置")
	}
	if strings.Contains(cronOut, "backup-config.sh") {
		add(42, "定时任务", "备份 cron", "pass", "已配置")
	} else {
		add(42, "定时任务", "备份 cron", "warn", "未配置")
	}

	// ═══════════════════════════════════════
	// 十、目录结构 (3项)
	// ═══════════════════════════════════════
	if info, err := os.Stat("/data"); err == nil && info.IsDir() {
		add(43, "目录", "/data 目录", "pass", "存在")
	} else {
		add(43, "目录", "/data 目录", "fail", "不存在")
	}

	if info, err := os.Lstat("/opt/nas"); err == nil && info.Mode()&os.ModeSymlink != 0 {
		link, _ := os.Readlink("/opt/nas")
		add(44, "目录", "/opt/nas 软链接", "pass", "→ "+link)
	} else {
		add(44, "目录", "/opt/nas 软链接", "warn", "不存在或不是软链接")
	}

	if info, err := os.Stat("/var/lib/nas-monitor"); err == nil && info.IsDir() {
		add(45, "目录", "监控状态目录", "pass", "存在")
	} else {
		add(45, "目录", "监控状态目录", "warn", "不存在")
	}

	// ═══════════════════════════════════════
	// 十一、软件包 (1项)
	// ═══════════════════════════════════════
	requiredPkgs := []string{"samba", "nfs-kernel-server", "vsftpd", "rclone", "fail2ban", "ufw", "smartmontools", "xfsprogs", "mdadm", "lvm2"}
	missingPkgs := []string{}
	for _, pkg := range requiredPkgs {
		cmdOut, err := exec.Command("dpkg", "-s", pkg).Output()
		if err != nil || !strings.Contains(string(cmdOut), "Status: install ok installed") {
			missingPkgs = append(missingPkgs, pkg)
		}
	}
	if len(missingPkgs) == 0 {
		add(46, "软件包", "核心软件包", "pass", "全部已安装")
	} else {
		add(46, "软件包", "核心软件包", "fail", "缺失: "+strings.Join(missingPkgs, ", "))
	}

	// Summary
	if report.Failed == 0 && report.Warn == 0 {
		report.Summary = fmt.Sprintf("全部 %d 项通过 ✅", report.Total)
	} else if report.Failed == 0 {
		report.Summary = fmt.Sprintf("%d 通过, %d 警告 ⚠️", report.Passed, report.Warn)
	} else {
		report.Summary = fmt.Sprintf("%d 通过, %d 失败 ❌, %d 警告 ⚠️", report.Passed, report.Failed, report.Warn)
	}

	return report
}
