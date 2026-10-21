package monitor

// Snapshot 导出最近一次后台采样结果，供 AI 工具域（get_monitor_snapshot）
// 等进程内调用方直接复用监控内核，不走 HTTP 自环。薄导出层，逻辑全部在
// getMonitorSnapshot 里，这里不做任何二次处理。
func Snapshot() map[string]interface{} {
	return getMonitorSnapshot()
}
