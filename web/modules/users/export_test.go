package users

import "testing"

// TestListUsersDoesNotPanic 验证 ListUsers() 在开发机（无 /etc/passwd 之外的
// 系统用户管理命令、pdbedit/htpasswd 等多半失败)下不 panic。getUsers 本身
// 对每个数据源的错误都是静默跳过（err == nil 才处理），所以这里只需要断言
// 调用安全，不对返回内容的具体形状做强假设。
func TestListUsersDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ListUsers() panicked: %v", r)
		}
	}()
	_ = ListUsers()
}
