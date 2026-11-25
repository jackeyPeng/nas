package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"
)

// verifyNASAuth 复刻核心 web/modules/apps/proxy.go 的 VerifyNASAuth 算法
// （§5.3）：sig = hex(hmac-sha256(secret, user + "\n" + unix_time))，ts 与
// now 差值须在 ±60s 内。与 hello-app/main.go 的同名函数逐字一致（独立 Go
// module，不共享依赖，纯靠规范文档对齐）。
func verifyNASAuth(secret, user, ts, sig string, now int64) bool {
	if secret == "" || user == "" || ts == "" || sig == "" {
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
	if delta > nasAuthSkew {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(user + "\n" + ts))
	want := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(want), []byte(sig)) == 1
}

// requireAuth 是本应用的强制 SSO 校验中间件：与 hello-app 的「验签失败仍
// 200，valid:false」演示姿态不同——nas-photos 是处理真实用户文件的生产
// 应用，上传/删除/查看原图都是需要鉴权的操作，验签失败必须 401（任务
// 要求「失败 401」）。返回 (user, ok)；ok=false 时已写好 401 响应，调用方
// 直接 return。
func requireAuth(w http.ResponseWriter, r *http.Request, secret string) (string, bool) {
	user := r.Header.Get("X-NAS-User")
	sig := r.Header.Get("X-NAS-Auth")
	ts := r.Header.Get("X-NAS-Time")
	now := time.Now().Unix()

	if !verifyNASAuth(secret, user, ts, sig, now) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return "", false
	}
	return user, true
}
