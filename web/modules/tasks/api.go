package tasks

import "net/http"

// registerAPIRoutes 的真正实现在 Task 2（REST + SSE，enabledGuard 403 包裹）。
// Task 1 先占位，保证 tasks.go 的 RegisterRoutes 签名可编译；main.go 在
// Task 1 即可完成注册接线，Task 2 落地后行为自动生效，无需再改 main.go。
func registerAPIRoutes(mux *http.ServeMux, enabledGuard func() bool) {
	_ = enabledGuard
}
