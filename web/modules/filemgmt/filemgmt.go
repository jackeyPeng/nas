package filemgmt

import (
	"net/http"

	"nas-panel/common"
)

// RegisterRoutes 注册 filemgmt 模块的全部 HTTP 路由。
// 本 Task 先只注册 list/download/zip/preview/usage/mkdir/rename/upload/move/copy/delete,
// 后续 Task 追加 stat/chmod/chown/fix-home-perms。
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/files/list", common.AuthMiddleware(handleList))
	mux.HandleFunc("/api/files/download", common.AuthMiddleware(handleDownload))
	mux.HandleFunc("/api/files/zip", common.AuthMiddleware(handleZip))
	mux.HandleFunc("/api/files/preview", common.AuthMiddleware(handlePreview))
	mux.HandleFunc("/api/files/usage", common.AuthMiddleware(handleUsage))
	mux.HandleFunc("/api/files/mkdir", common.AuthMiddleware(handleMkdir))
	mux.HandleFunc("/api/files/rename", common.AuthMiddleware(handleRename))
	mux.HandleFunc("/api/files/upload", common.AuthMiddleware(handleUpload))
	mux.HandleFunc("/api/files/move", common.AuthMiddleware(handleMove))
	mux.HandleFunc("/api/files/copy", common.AuthMiddleware(handleCopy))
	mux.HandleFunc("/api/files/delete", common.AuthMiddleware(handleDelete))
}
