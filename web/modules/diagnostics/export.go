package diagnostics

// StatusSnapshot 导出诊断项状态快照内核，供 AI 工具域
// （get_diagnostics_status）等进程内调用方直接复用。薄导出层，逻辑全部在
// buildStatusSnapshot 里（由 handleStatus 的既有构造体机械抽取而来，行为
// 不变）。
func StatusSnapshot() map[string]interface{} {
	return buildStatusSnapshot()
}
