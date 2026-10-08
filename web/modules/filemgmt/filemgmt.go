package filemgmt

import (
	"net/http"

	"nas-panel/common"
)

// RegisterRoutes 注册 filemgmt 模块的全部 HTTP 路由。
// 本 Task 先只注册 list,后续 Task 追加 download/zip/preview/usage/upload/
// mkdir/rename/move/copy/delete/stat/chmod/chown/fix-home-perms。
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/files/list", common.AuthMiddleware(handleList))
}
