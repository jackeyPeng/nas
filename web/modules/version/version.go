package version

import (
	_ "embed"
	"net/http"
	"regexp"
	"runtime"
	"strings"

	"nas-panel/common"
)

// Build-time values injected via ldflags:
//   -X nas-panel/modules/version.Version=v1.3.0
//   -X nas-panel/modules/version.BuildTime=2026-08-13T15:00:00Z
//   -X nas-panel/modules/version.GitCommit=abc1234
var (
	// DisplayVersion 是对外展示的系统大版本号（如 v1.3.0），取最近的 git tag
	DisplayVersion = "dev"
	// Version 是完整构建版本号（含提交数/commit/dirty 等信息）
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

// VersionInfo represents the version response
type VersionInfo struct {
	DisplayVersion string `json:"display_version"`
	Version        string `json:"version"`
	BuildTime      string `json:"build_time"`
	GitCommit      string `json:"git_commit"`
	GoVersion      string `json:"go_version"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
}

// Component represents one software component's version info
type Component struct {
	Name     string `json:"name"`     // 组件名
	Category string `json:"category"` // 分类: 文件共享/对象存储/网页管理/系统防护/存储管理/运行环境
	Version  string `json:"version"`  // 版本号
	Detail   string `json:"detail"`   // 补充说明（端口/用途）
}

// RegisterRoutes registers version routes
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/version", handleVersion)
	mux.HandleFunc("/api/system/components", common.AuthMiddleware(handleComponents))
	mux.HandleFunc("/api/system/notice", common.AuthMiddleware(handleNotice))
}

// handleVersion returns build version info
func handleVersion(w http.ResponseWriter, r *http.Request) {
	info := VersionInfo{
		DisplayVersion: DisplayVersion,
		Version:        Version,
		BuildTime: BuildTime,
		GitCommit: GitCommit,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
	common.JSONResponse(w, info)
}

// firstMatch extracts the first regex capture group, returns "" if no match
func firstMatch(output string, re *regexp.Regexp) string {
	m := re.FindStringSubmatch(output)
	if len(m) >= 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// cmdVersion runs "<cmd> --version"-style commands and returns trimmed output
func cmdVersion(name string, args ...string) string {
	out, _ := common.ExecOutput(name, args...)
	return strings.TrimSpace(out)
}

// handleComponents returns versions of all NAS software components
func handleComponents(w http.ResponseWriter, r *http.Request) {
	var comps []Component

	// ── 文件共享 ──────────────────────────────────────
	smbVer := firstMatch(cmdVersion("smbd", "--version"), regexp.MustCompile(`Version\s+(\S+)`))
	comps = append(comps, Component{"Samba (smbd)", "文件共享", smbVer, "SMB/CIFS · 端口 139/445"})

	nfsVer := firstMatch(cmdVersion("/usr/sbin/rpc.nfsd", "--version"), regexp.MustCompile(`nfsd\s+version\s+(\S+)`))
	if nfsVer == "" {
		nfsVer = dpkgVersion("nfs-kernel-server")
	}
	comps = append(comps, Component{"NFS (nfs-kernel-server)", "文件共享", nfsVer, "NFS v3/v4 · 端口 2049"})

	ftpVer := firstMatch(cmdVersion("/usr/sbin/vsftpd", "-v"), regexp.MustCompile(`vsftpd:\s+version\s+(\S+)`))
	if ftpVer == "" {
		out, _ := common.ExecOutput("sh", "-c", "/usr/sbin/vsftpd -v 2>&1 | head -1")
		ftpVer = firstMatch(strings.TrimSpace(out), regexp.MustCompile(`version\s+(\S+)`))
	}
	if ftpVer == "" {
		ftpVer = dpkgVersion("vsftpd")
	}
	comps = append(comps, Component{"vsftpd", "文件共享", ftpVer, "FTP · 端口 21 + 被动 30000-31000"})

	// ── 对象存储 / 远程访问 ────────────────────────────
	rcloneVer := firstMatch(cmdVersion("rclone", "version"), regexp.MustCompile(`rclone\s+v?(\S+)`))
	comps = append(comps, Component{"rclone", "对象存储", rcloneVer, "WebDAV :8080 + S3 :9000 服务端"})

	fbVer := firstMatch(cmdVersion("filebrowser", "version"), regexp.MustCompile(`(v\S+|version\s+\S+)`))
	comps = append(comps, Component{"FileBrowser", "网页文件管理", fbVer, "Web 文件管理器 · 端口 8081"})

	// ── 网页管理 ──────────────────────────────────────
	comps = append(comps, Component{"nas-panel", "网页管理", DisplayVersion,
		"管理面板 · 端口 8090 · 构建 " + BuildTime + " · " + GitCommit})

	// ── 系统防护 ──────────────────────────────────────
	f2bVer := firstMatch(cmdVersion("fail2ban-client", "version"), regexp.MustCompile(`(\d+\.\S+)`))
	comps = append(comps, Component{"Fail2ban", "系统防护", f2bVer, "SSH/FTP 防暴力破解"})

	ufwVer := firstMatch(cmdVersion("ufw", "--version"), regexp.MustCompile(`ufw\s+(\S+)`))
	comps = append(comps, Component{"UFW", "系统防护", ufwVer, "防火墙 · 默认拒绝入站"})

	// ── 存储管理 ──────────────────────────────────────
	lvmVer := firstMatch(cmdVersion("lvm", "version"), regexp.MustCompile(`LVM version:\s*(\S+)`))
	if lvmVer == "" {
		lvmVer = firstMatch(cmdVersion("lvs", "--version"), regexp.MustCompile(`LVM version:\s*(\S+)`))
	}
	comps = append(comps, Component{"LVM", "存储管理", lvmVer, "逻辑卷管理"})

	mdadmVer := firstMatch(cmdVersion("/usr/sbin/mdadm", "--version"), regexp.MustCompile(`mdadm\s+-\s+v(\S+)`))
	if mdadmVer == "" {
		mdadmVer = dpkgVersion("mdadm")
	}
	comps = append(comps, Component{"mdadm", "存储管理", mdadmVer, "软 RAID 管理"})

	xfsVer := firstMatch(cmdVersion("xfs_info", "-V"), regexp.MustCompile(`xfs_info\s+version\s+(\S+)`))
	if xfsVer == "" {
		xfsVer = firstMatch(cmdVersion("mkfs.xfs", "-V"), regexp.MustCompile(`mkfs.xfs\s+version\s+(\S+)`))
	}
	comps = append(comps, Component{"xfsprogs", "存储管理", xfsVer, "XFS 文件系统工具"})

	smartVer := firstMatch(cmdVersion("smartctl", "--version"), regexp.MustCompile(`smartctl\s+(\S+)`))
	comps = append(comps, Component{"smartmontools", "存储管理", smartVer, "SMART 磁盘健康监测"})

	// ── 运行环境 ──────────────────────────────────────
	comps = append(comps, Component{"Linux 内核", "运行环境", common.ExecFirstLine("uname", "-r"), "操作系统内核"})
	osPretty := common.ExecFirstLine("sh", "-c", `awk -F'"' '/^PRETTY_NAME/{print $2}' /etc/os-release 2>/dev/null`)
	comps = append(comps, Component{"操作系统", "运行环境", osPretty, "Debian GNU/Linux"})

	common.JSONResponse(w, map[string]interface{}{
		"components": comps,
		"panel": VersionInfo{
			DisplayVersion: DisplayVersion,
			Version:        Version,
			BuildTime:      BuildTime,
			GitCommit:      GitCommit,
			GoVersion:      runtime.Version(),
			OS:             runtime.GOOS,
			Arch:           runtime.GOARCH,
		},
	})
}
// dpkgVersion returns the installed version of a deb package
func dpkgVersion(pkg string) string {
	out, _ := common.ExecOutput("sh", "-c", "dpkg-query -W -f='${Version}' "+pkg+" 2>/dev/null")
	v := strings.TrimSpace(out)
	if v == "" || strings.Contains(v, "dpkg-query") {
		return ""
	}
	return v
}

//go:embed NOTICE.md
var noticeFS []byte

// NoticeSection is one section of the legal/privacy statement
type NoticeSection struct {
	ID       string        `json:"id"`
	Title    string        `json:"title"`    // 中英双语标题
	SubTitle string        `json:"subtitle"` // English subtitle
	Items    []NoticeItem  `json:"items"`
}

// NoticeItem is one bullet or key-value item within a section
type NoticeItem struct {
	Label string `json:"label"` // 条目标题（中英）
	Text  string `json:"text"`  // 正文（中英）
}

// handleNotice returns the structured compliance/privacy statement
func handleNotice(w http.ResponseWriter, r *http.Request) {
	common.JSONResponse(w, map[string]interface{}{
		"sections": buildNoticeSections(),
		"raw":      string(noticeFS), // 兜底纯文本
	})
}

func buildNoticeSections() []NoticeSection {
	return []NoticeSection{
		{
			ID: "privacy", Title: "隐私声明", SubTitle: "Privacy Statement",
			Items: []NoticeItem{
				{Label: "数据归属 / Data Ownership",
					Text: "你的全部数据（文件、账号、配置）仅存储在你自己的机器上，不会上传到任何外部服务器。All your data stays on your own machine and is never uploaded anywhere."},
				{Label: "数据不出网 / No Data Leaves Your Machine",
					Text: "面板与全部文件服务均为本地服务，仅监听局域网；系统不含任何遥测、行为统计或崩溃上报代码。All services run locally on your LAN; no telemetry, analytics, or crash reporting is included."},
				{Label: "凭据存储 / Credential Storage",
					Text: "管理密码以 0600 权限存储于本机，WebDAV 凭据使用 bcrypt 哈希，不外传。Passwords stay local with 0600 permissions; WebDAV credentials are bcrypt-hashed."},
				{Label: "可选外联 / Optional Outbound",
					Text: "远程同步与告警通知默认关闭，仅在你配置后按你的设定工作。Remote sync and alerts are off by default and work only as you configure."},
			},
		},
		{
			ID: "disclaimer", Title: "免责声明", SubTitle: "Disclaimer",
			Items: []NoticeItem{
				{Label: "用户数据 / User Data",
					Text: "Z1 NAS 仅提供系统软件，用户数据的存储、备份与安全由用户自行负责；对任何原因导致的数据丢失或损坏不承担责任，建议对重要数据建立独立备份。Z1 NAS provides system software only. You are responsible for your data's storage, backup, and security; we assume no liability for data loss or corruption. Independent backups are strongly recommended."},
				{Label: "不上传、不修改、不传播 / No Upload, No Modification, No Distribution",
					Text: "我们不上传、不修改、不主动传播任何用户数据。We do not upload, modify, or actively distribute any user data."},
				{Label: "开源组件 / Open-Source Components",
					Text: "本软件基于开源组件构建，遵循各组件许可协议；开源组件按「现状」提供。Built on open-source components under their respective licenses; components are provided as-is."},
				{Label: "责任限制 / Limitation of Liability",
					Text: "在适用法律允许的最大范围内，本软件不提供任何明示或默示的保证，对任何直接、间接、附带或后果性损失不承担责任。Provided as-is without warranty of any kind; no liability for direct, indirect, incidental, or consequential damages."},
			},
		},
		{
			ID: "copyright", Title: "版权", SubTitle: "Copyright",
			Items: []NoticeItem{
				{Label: "软件版权 / Software Copyright",
					Text: "Z1 NAS 系统软件版权归本项目所有，以 AGPL-3.0 许可证发布。The Z1 NAS system software is copyrighted by this project and licensed under AGPL-3.0."},
				{Label: "商标 / Trademarks",
					Text: "Debian 是 SPI Inc. 的注册商标；其他产品名称归各自所有者，仅表明兼容性，不代表关联。Debian is a registered trademark of SPI Inc.; other names belong to their owners, used for compatibility indication only."},
			},
		},
	}
}
