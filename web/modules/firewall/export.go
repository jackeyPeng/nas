package firewall

// Status 导出防火墙状态内核，供 AI 工具域（get_firewall_rules）等进程内
// 调用方直接复用，不走 HTTP 自环。薄导出层，逻辑全部在 getStatus 里。
func Status() FirewallStatus {
	return getStatus()
}
