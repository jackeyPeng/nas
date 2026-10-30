package common

import "strings"

// ResolveServiceEnabled 返回 systemd 单元真实的自启状态。
//
// alias 单元（如 nfs-kernel-server → nfs-server）的 `systemctl is-enabled`
// 无论目标单元实际 enable 与否都恒报 "alias"（退出码 0），直接采信会把
// 「默认关闭的协议服务」误判成 enabled（注册表 fail / 仪表盘自启徽章 /
// 升级脚本误重开）。真状态在规范单元上：先 `systemctl show -p Id --value`
// 解析出规范名（nfs-server.service），再对其 is-enabled。
//
// is-enabled 对 disabled 返回非零退出码但 stdout 仍有 "disabled"，
// ExecOutput 出错时输出仍可用，统一按输出串判定。
func ResolveServiceEnabled(name string) bool {
	state := strings.TrimSpace(mustExecOut("systemctl", "is-enabled", name))
	if state == "alias" {
		canon := strings.TrimSuffix(strings.TrimSpace(mustExecOut("systemctl", "show", name, "-p", "Id", "--value")), ".service")
		if canon != "" && canon != name {
			state = strings.TrimSpace(mustExecOut("systemctl", "is-enabled", canon))
		}
	}
	return state == "enabled" || state == "enabled-runtime"
}

// mustExecOut 忽略退出码只取 stdout（is-enabled 对 disabled 返回 1 但输出有效）。
func mustExecOut(cmd string, args ...string) string {
	out, _ := ExecOutput(cmd, args...)
	return out
}
