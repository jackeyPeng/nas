// nas-photos 是 APP-SPEC v0.1（docs/APP-SPEC.md）的标杆实现：家庭照片应用
// （上传 + 缩略图画廊 + 查看 + 删除）。零第三方依赖，纯标准库（image/jpeg、
// image/png、image/gif、image/draw 做缩略图）。结构、前缀处理、SSO 验签、
// healthz 全照 examples/hello-app 的模式（见该目录 README 的条款对照表）。
package main

import (
	"log"
	"net/http"
	"os"
)

// nasAuthSkew 是 §5.3「容差 ±60s」，与 hello-app/main.go 逐字一致的独立实现
// （不同 Go module，不共享代码，纯靠规范文档对齐）。
const nasAuthSkew = 60

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8082" // 裸跑调试默认值；核心托管时 unit.tpl 恒注入 __PORT__（§3.2）。
	}
	secret := os.Getenv("NAS_SECRET")
	dataDir := os.Getenv("DATA_DIR")
	storageDir := os.Getenv("STORAGE_DIR")
	if storageDir == "" {
		storageDir = "/data/nas1/photos" // manifest permissions.storage 声明的约定值。
	}

	app, err := newApp(dataDir, storageDir)
	if err != nil {
		log.Fatalf("初始化失败: %v", err)
	}

	ctx := secretCtx{secret: secret, app: app}
	mux := http.NewServeMux()

	// healthz：核心只认 200，不解析内容（§6）。双路径（裸跑 + 反代前缀）。
	mux.HandleFunc("/healthz", app.handleHealthz)
	mux.HandleFunc("/apps/nas-photos/healthz", app.handleHealthz)

	// §5.2「路径前缀原样透传，应用自己处理前缀」：核心反代把
	// /apps/nas-photos/* 原样转发到本进程，正式路由挂在该前缀下；同时保留
	// 裸路径方便本机直接起进程调试，不经核心反代。
	registerRoutes(mux, "/apps/nas-photos", ctx)
	registerRoutes(mux, "", ctx)

	addr := "127.0.0.1:" + port // §5.1：只监听 127.0.0.1，外网不可达是硬约束。
	log.Printf("nas-photos listening on %s (DATA_DIR=%q STORAGE_DIR=%q)", addr, dataDir, storageDir)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

// secretCtx 把 NAS_SECRET 与 app 状态传给 handler（避免全局变量，main_test.go
// 需要用不同 secret/app 实例反复构造 handler 做测试，照 hello-app 的模式）。
type secretCtx struct {
	secret string
	app    *app
}

// registerRoutes 把业务路由同时挂到前缀路径（核心反代）与裸路径（本机调试）
// 下，prefix 为空字符串时即裸路径模式。
func registerRoutes(mux *http.ServeMux, prefix string, ctx secretCtx) {
	mux.HandleFunc(prefix+"/", withSecret(ctx, handleIndex))
	mux.HandleFunc(prefix+"/api/upload", withSecret(ctx, handleUpload))
	mux.HandleFunc(prefix+"/api/photos", withSecret(ctx, handleList))
	mux.HandleFunc(prefix+"/api/delete", withSecret(ctx, handleDelete))
	mux.HandleFunc(prefix+"/photo/", withSecret(ctx, handlePhoto))
	mux.HandleFunc(prefix+"/thumb/", withSecret(ctx, handleThumb))
}

func withSecret(ctx secretCtx, next func(http.ResponseWriter, *http.Request, secretCtx)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		next(w, r, ctx)
	}
}
