// hello-app 是 APP-SPEC v0.1（docs/APP-SPEC.md）的参考实现：演示托管应用
// 应该如何读取核心注入的环境变量、响应 healthz、处理反代前缀、以及校验
// 核心转发的 SSO 签名头（§5.2/§5.3）。零第三方依赖，纯标准库。
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

// nasAuthSkew 是 §5.3「容差 ±60s」——与核心 web/modules/apps/proxy.go 的
// nasAuthSkew 保持一致，应用侧独立实现（不依赖核心代码，不同 Go module）。
const nasAuthSkew = 60 * time.Second

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081" // 裸跑调试默认值；核心托管时 unit.tpl 恒注入 __PORT__（§3.2）。
	}
	secret := os.Getenv("NAS_SECRET")
	dataDir := os.Getenv("DATA_DIR")

	mux := http.NewServeMux()

	// healthz：核心只认 200，不解析内容（§6「healthz：启动后 30s 内须 200」）。
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("/apps/hello/healthz", handleHealthz)

	// §5.2「路径前缀原样透传，应用自己处理前缀」：核心反代把 /apps/hello/*
	// 原样转发到本进程，所以正式路由必须挂在 /apps/hello/ 前缀下；同时
	// 保留裸路径（/ 与 /api/whoami）方便本机直接起进程调试，不经核心反代。
	mux.HandleFunc("/apps/hello/", withSecret(secret, dataDir, handleIndex))
	mux.HandleFunc("/apps/hello/api/whoami", withSecret(secret, dataDir, handleWhoami))
	mux.HandleFunc("/", withSecret(secret, dataDir, handleIndex))
	mux.HandleFunc("/api/whoami", withSecret(secret, dataDir, handleWhoami))

	addr := "127.0.0.1:" + port // §5.1：只监听 127.0.0.1，外网不可达是硬约束。
	log.Printf("hello-app listening on %s (DATA_DIR=%q)", addr, dataDir)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

// secretCtx 把 NAS_SECRET/DATA_DIR 传给 handler，避免用全局变量
// （main_test.go 需要用不同 secret 反复构造 handler 做测试）。
type secretCtx struct {
	secret  string
	dataDir string
}

func withSecret(secret, dataDir string, next func(http.ResponseWriter, *http.Request, secretCtx)) http.HandlerFunc {
	ctx := secretCtx{secret: secret, dataDir: dataDir}
	return func(w http.ResponseWriter, r *http.Request) {
		next(w, r, ctx)
	}
}

// whoamiResponse 是 /api/whoami 的返回体：演示报告性质，验签失败也 200
// （任务要求「valid:false 但仍 200，不演示二次拒绝」）。
type whoamiResponse struct {
	User         string `json:"user"`
	Valid        bool   `json:"valid"`
	UnixTime     int64  `json:"unix_time"`
	DataDirFiles int    `json:"data_dir_files"`
}

func handleWhoami(w http.ResponseWriter, r *http.Request, ctx secretCtx) {
	user := r.Header.Get("X-NAS-User")
	sig := r.Header.Get("X-NAS-Auth")
	ts := r.Header.Get("X-NAS-Time")
	now := time.Now().Unix()

	valid := ctx.secret != "" && verifyNASAuth(ctx.secret, user, ts, sig, now)

	resp := whoamiResponse{
		User:         user,
		Valid:        valid,
		UnixTime:     now,
		DataDirFiles: countDataDirFiles(ctx.dataDir),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// verifyNASAuth 复刻核心 web/modules/apps/proxy.go 的 VerifyNASAuth
// 算法（§5.3）：sig = hex(hmac-sha256(secret, user + "\n" + unix_time))，
// ts 与 now 差值须在 ±60s 内。独立实现（hello-app 不依赖核心 module），
// 但算法必须与核心逐字一致——这是本文件演示的核心规范条款。
func verifyNASAuth(secret, user, ts, sig string, now int64) bool {
	if user == "" || ts == "" || sig == "" {
		return false
	}
	tsNum, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	delta := now - tsNum
	if delta < 0 {
		delta = -delta
	}
	if time.Duration(delta)*time.Second > nasAuthSkew {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(user + "\n" + ts))
	want := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(want), []byte(sig)) == 1
}

// countDataDirFiles 列 DATA_DIR 的文件数，验证数据目录确实挂载/可读
// （DATA_DIR 未设置或不存在时返回 0，不报错——演示应用不因此失败）。
func countDataDirFiles(dataDir string) int {
	if dataDir == "" {
		return 0
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return 0
	}
	return len(entries)
}

// handleIndex 返回内嵌单页前端：无外部 CDN 依赖，全部内联在本文件里
// （§4「所有页面资源必须相对路径」——这里干脆零额外资源请求）。
// whoami 结果由页面内嵌脚本调用相对路径 ./api/whoami 异步拉取。
func handleIndex(w http.ResponseWriter, r *http.Request, ctx secretCtx) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, indexHTML)
}

var indexHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>hello-app · APP-SPEC 演示应用</title>
<style>
  body { font-family: -apple-system, system-ui, sans-serif; max-width: 640px; margin: 48px auto; padding: 0 16px; color: #222; }
  h1 { font-size: 20px; }
  code { background: #f2f2f2; padding: 2px 6px; border-radius: 4px; }
  #result { white-space: pre-wrap; background: #f8f8f8; border: 1px solid #ddd; border-radius: 6px; padding: 12px; }
  .ok { color: #1a7f37; }
  .fail { color: #cf222e; }
  p.note { color: #666; font-size: 13px; }
</style>
</head>
<body>
  <h1>hello-app</h1>
  <p>这是 NAS 应用平台的演示应用（APP-SPEC v0.1 参考实现），没有外部 CDN 依赖，全部资源内嵌本页。</p>
  <p id="status">正在调用 <code>./api/whoami</code> …</p>
  <pre id="result"></pre>
  <p class="note">本页仅做演示，不做任何持久化操作。</p>
  <script>
    fetch('./api/whoami').then(function (r) { return r.json(); }).then(function (data) {
      var status = document.getElementById('status');
      var result = document.getElementById('result');
      status.textContent = 'whoami 结果：';
      status.className = data.valid ? 'ok' : 'fail';
      result.textContent = JSON.stringify(data, null, 2);
    }).catch(function (err) {
      document.getElementById('status').textContent = '调用失败: ' + err;
    });
  </script>
</body>
</html>
`
