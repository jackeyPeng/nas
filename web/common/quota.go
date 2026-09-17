package common

import (
	"fmt"
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
