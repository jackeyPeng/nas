package filemgmt

import (
	"net/http"

	"nas-panel/common"
)

// RegisterRoutes 注册 filemgmt 模块的全部 HTTP 路由。
// 本 Task 先只注册 list/download/zip/preview/usage,后续 Task 追加 upload/
// mkdir/rename/move/copy/delete/stat/chmod/chown/fix-home-perms。
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/files/list", common.AuthMiddleware(handleList))
	mux.HandleFunc("/api/files/download", common.AuthMiddleware(handleDownload))
	mux.HandleFunc("/api/files/zip", common.AuthMiddleware(handleZip))
	mux.HandleFunc("/api/files/preview", common.AuthMiddleware(handlePreview))
	mux.HandleFunc("/api/files/usage", common.AuthMiddleware(handleUsage))
}
