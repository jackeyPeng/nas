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

// IsServiceActive 报告 systemd 单元当前是否 active（alias 单元 is-active 会
// 正确解析到目标单元，无需额外处理）。
func IsServiceActive(name string) bool {
	return strings.TrimSpace(mustExecOut("systemctl", "is-active", name)) == "active"
}

// RestartIfActive 仅在服务运行时 restart。systemctl restart 对 stopped 单元
// 是「启动」语义——协议服务默认关闭后，无条件 restart 会把用户没开的服务
// 偷偷拉起来（实锤：首登改密路径 restart rclone-s3 复活了默认关闭的 S3）。
// 配置文件/unit 文件的更新照常先落盘，服务下次被用户开启时自然生效。
// 返回是否实际执行了 restart。
func RestartIfActive(name string) bool {
	if !IsServiceActive(name) {
		return false
	}
	SudoExec("systemctl", "restart", name)
	return true
}
