// sign.go — OTA 签名工具（发布时用，绝不部署到 NAS）
//
// 用法:
//   OTA_SIGN_KEY=<64字节hex私钥> go run scripts/sign.go <二进制文件> <版本号> <下载URL>
//
// 输出一个 JSON manifest 到 stdout:
//   {"version":"v1.4.0","sha256":"...","sig":"...","url":"..."}
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "用法: OTA_SIGN_KEY=<hex私钥> go run scripts/sign.go <二进制> <版本> <URL>")
		os.Exit(1)
	}
	file := os.Args[1]
	version := os.Args[2]
	url := os.Args[3]

	keyHex := os.Getenv("OTA_SIGN_KEY")
	if keyHex == "" {
		fmt.Fprintln(os.Stderr, "错误: 未设置 OTA_SIGN_KEY 环境变量")
		os.Exit(1)
	}
	privBytes, err := hex.DecodeString(keyHex)
	if err != nil || len(privBytes) != ed25519.PrivateKeySize {
		fmt.Fprintln(os.Stderr, "错误: OTA_SIGN_KEY 不是合法的 ed25519 私钥")
		os.Exit(1)
	}
	priv := ed25519.PrivateKey(privBytes)

	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取文件失败:", err)
		os.Exit(1)
	}

	sum := sha256.Sum256(data)
	sig := ed25519.Sign(priv, data)

	m := map[string]string{
		"version": version,
		"sha256":  hex.EncodeToString(sum[:]),
		"sig":     hex.EncodeToString(sig),
		"url":     url,
	}
	out, _ := json.Marshal(m)
	fmt.Println(string(out))
}
