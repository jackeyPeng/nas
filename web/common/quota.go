package common

import (
	"fmt"
	"strconv"
	"strings"
)

// quota.go — 配额能力探测。
// XFS project quota 需要两个前提：文件系统是 xfs，且挂载时启用 prjquota。
// ext4 或没开 prjquota 的 xfs 上，xfs_quota 会静默失败/返回空，
// 旧行为是查询失败返回 (0,0) 被 UI 当"无限制"——配额形同虚设且无提示。
// 这里统一探测，调用方必须根据结果给用户明确提示。

// QuotaStatus 配额能力状态
type QuotaStatus struct {
	Supported bool   `json:"supported"` // 是否支持 project quota
	FSType    string `json:"fstype"`    // 挂载点文件系统类型
	Reason    string `json:"reason"`    // 不支持/探测失败的原因（supported=true 时为空）
}

// MountQuotaSupport 探测挂载点是否支持 XFS project quota。
// 通过 findmnt 读取真实挂载信息（fstype + 挂载选项），不猜测。
func MountQuotaSupport(mountPoint string) QuotaStatus {
	mountPoint = strings.TrimSpace(mountPoint)
	if mountPoint == "" {
		return QuotaStatus{Supported: false, Reason: "挂载点为空"}
	}
	out, err := ExecOutput("findmnt", "-n", "-o", "FSTYPE,OPTIONS", "--target", mountPoint)
	if err != nil {
		return QuotaStatus{Supported: false, Reason: fmt.Sprintf("探测挂载点失败: %v", err)}
	}
	line := strings.TrimSpace(out)
	if line == "" {
		return QuotaStatus{Supported: false, Reason: "找不到挂载点 " + mountPoint}
	}
	// findmnt 输出 "xfs rw,relatime,attr2,prjquota"（第一个空格分隔 FSTYPE 与 OPTIONS）
	parts := strings.SplitN(line, " ", 2)
	fstype := strings.TrimSpace(parts[0])
	opts := ""
	if len(parts) == 2 {
		opts = parts[1]
	}
	st := QuotaStatus{FSType: fstype}
	if fstype != "xfs" {
		st.Reason = fmt.Sprintf("文件系统 %s 不支持 project quota（需要 xfs）", fstype)
		return st
	}
	if !mountOptionsHavePrjquota(opts) {
		st.Reason = "挂载点未启用 prjquota（需在 fstab 挂载选项中加 prjquota 并重新挂载）"
		return st
	}
	st.Supported = true
	return st
}

// mountOptionsHavePrjquota 检查逗号分隔的挂载选项中是否启用了 project quota。
// xfs 的 prjquota 选项历史别名：prjquota / pquota / quota（quota 同时开 usr+prj 视内核而定，
// 保守只认 prjquota 和 pquota）。
func mountOptionsHavePrjquota(opts string) bool {
	for _, o := range strings.Split(opts, ",") {
		switch strings.TrimSpace(o) {
		case "prjquota", "pquota":
			return true
		}
	}
	return false
}

// ParseProjectsLine 解析 /etc/projects 的一行。
// 标准格式是两段 "projectID:/path"（XFS 官方格式，ID 在前）。
// 历史脏数据是三段 "name:id:path"（和 /etc/projid 的 name:id 搞混了），
// 这里兼容两种格式，读出后统一按标准两段格式回写归一。
// 返回 ok=false 表示该行为空行/注释/无法解析，调用方应原样保留。
func ParseProjectsLine(line string) (id int, path string, name string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return 0, "", "", false
	}
	parts := strings.Split(trimmed, ":")
	switch len(parts) {
	case 2:
		// 标准格式：id:path
		n, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			return 0, "", "", false
		}
		return n, strings.TrimSpace(parts[1]), "", true
	case 3:
		// 脏数据格式：name:id:path
		n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return 0, "", "", false
		}
		return n, strings.TrimSpace(parts[2]), strings.TrimSpace(parts[0]), true
	default:
		return 0, "", "", false
	}
}
