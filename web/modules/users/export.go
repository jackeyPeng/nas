package users

// ListUsers 导出用户列表内核，供 AI 工具域（get_user_list）等进程内调用方
// 直接复用，不走 HTTP 自环。薄导出层，逻辑全部在 getUsers 里。
func ListUsers() []NASUser {
	return getUsers()
}
