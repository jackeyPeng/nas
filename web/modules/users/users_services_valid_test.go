package users

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// item E 修复：handleUserAction 的 username 直接取自 URL path 段
// （parts[0]），此前在分发到 services 子路由前零校验，最终流入
// updateUserServices → enableFTPUser/disableFTPUser（create.go），该函数用
// appendLine 把 username 原样写进 /etc/vsftpd.userlist 白名单文件。含换行的
// username 可在该文件里注入额外整行，等价于越权授予任意用户名 FTP 访问权限
// （与 users_remove_test.go 覆盖的 removeUser 路径同根因，这里是 services
// 子路由此前漏补的同一个洞）。必须在分发到任何子路由之前就拒绝非法用户名。
func TestHandleUserAction_Services_RejectsInvalidUsername(t *testing.T) {
	cases := []struct {
		name     string
		username string
	}{
		{"换行注入", "alice\nmallory"},
		{"空格", "ali ce"},
		{"分号", "alice;rm"},
		{"路径穿越片段", ".."},
		{"太短", "a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/api/users/placeholder/services", nil)
			req.URL.Path = "/api/users/" + tc.username + "/services"
			rec := httptest.NewRecorder()

			handleUserAction(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("非法用户名应在入口被拒绝（400），got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}
