package version

import (
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
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

// VersionInfo represents the version response
type VersionInfo struct {
	Version   string `json:"version"`
	BuildTime string `json:"build_time"`
	GitCommit string `json:"git_commit"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
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
}

// handleVersion returns build version info
func handleVersion(w http.ResponseWriter, r *http.Request) {
	info := VersionInfo{
		Version:   Version,
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
	comps = append(comps, Component{"nas-panel", "网页管理", Version,
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
			Version:   Version,
			BuildTime: BuildTime,
			GitCommit: GitCommit,
			GoVersion: runtime.Version(),
			OS:        runtime.GOOS,
			Arch:      runtime.GOARCH,
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
