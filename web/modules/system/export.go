package system

// RestartService 导出服务重启内核，供 AI 工具域（restart_service）等进程内
// 调用方直接复用 handleServices POST 分支抽出的 performServiceAction 内核
// （action 固定为 "restart"），不走 HTTP 自环、不复制白名单/systemctl 逻辑。
func RestartService(name string) (string, error) {
	return performServiceAction(name, "restart")
}
