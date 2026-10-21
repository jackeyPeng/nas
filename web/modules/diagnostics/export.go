package diagnostics

// StatusSnapshot 导出诊断项状态快照内核，供 AI 工具域
// （get_diagnostics_status）等进程内调用方直接复用。薄导出层，逻辑全部在
// buildStatusSnapshot 里（由 handleStatus 的既有构造体机械抽取而来，行为
// 不变）。
func StatusSnapshot() map[string]interface{} {
	return buildStatusSnapshot()
}

// RunItem 导出诊断任务触发内核，供 AI 工具域（run_diagnostics）等进程内
// 调用方直接复用，不走 HTTP 自环。薄导出层，逻辑全部在 runItemKernel 里
// （= handleRun 的内核：并发/温度/IO/时间窗口校验通过后异步启动，返回时
// 任务尚未完成——与既有 POST /api/diagnostics/run 语义一致）。
func RunItem(itemID string) error {
	return runItemKernel(itemID)
}
