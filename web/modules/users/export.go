package users

// ListUsers 导出用户列表内核，供 AI 工具域（get_user_list）等进程内调用方
// 直接复用，不走 HTTP 自环。薄导出层，逻辑全部在 getUsers 里。
func ListUsers() []NASUser {
	return getUsers()
}

// SetQuota 导出私有目录配额设置内核，供 AI 工具域（set_user_quota）等进程内
// 调用方直接复用，不走 HTTP 自环。薄导出层，逻辑全部在 setPrivateDirQuota 里
// （= PUT /api/users/{name}/quota 的内核，quota_gb<=0 表示移除限制）。
func SetQuota(username string, quotaGB int) error {
	return setPrivateDirQuota(username, quotaGB)
}

// QuotaOf 返回用户私有目录当前配额（已用 GB、限额 GB、状态），供
// set_user_quota 确认卡"旧值→新值"展示复用，不走 HTTP 自环。薄导出层，
// 逻辑全部在 privateDirQuota 里（= GET /api/users/{name}/quota 的内核）。
func QuotaOf(username string) (usedGB float64, limitGB int, status, reason string) {
	return privateDirQuota(username)
}
