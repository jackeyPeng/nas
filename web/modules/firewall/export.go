package firewall

// Status 导出防火墙状态内核，供 AI 工具域（get_firewall_rules）等进程内
// 调用方直接复用，不走 HTTP 自环。薄导出层，逻辑全部在 getStatus 里。
func Status() FirewallStatus {
	return getStatus()
}

// AddRule 导出添加防火墙规则内核，供 AI 工具域（add_firewall_rule）等进程内
// 调用方直接复用，不走 HTTP 自环。薄导出层，逻辑全部在 addRuleKernel 里
// （= handleAddRule 的内核，含 buildAddRuleArgs 校验与参数构造）。
func AddRule(port, proto, action, from, comment string) (string, error) {
	return addRuleKernel(port, proto, action, from, comment)
}
