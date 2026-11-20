// appsign — 托管应用包签名工具（APP-SPEC v0.1 §1 发布物三件套：tar.gz + sha256 + ed25519 签名）。
//
// 用法:
//
//	go run scripts/appsign/main.go keygen <out-dir>              生成 .key/.pub 到 out-dir，并打印 base64 公钥常量
//	go run scripts/appsign/main.go sign <tarball> <key> <out.sig> 用私钥对 tarball 生成 detached 签名
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "keygen":
		if len(os.Args) != 3 {
			usage()
			os.Exit(1)
		}
		cmdKeygen(os.Args[2])
	case "sign":
		if len(os.Args) != 5 {
			usage()
			os.Exit(1)
		}
		cmdSign(os.Args[2], os.Args[3], os.Args[4])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "用法:")
	fmt.Fprintln(os.Stderr, "  go run scripts/appsign/main.go keygen <out-dir>")
	fmt.Fprintln(os.Stderr, "  go run scripts/appsign/main.go sign <tarball> <key> <out.sig>")
}

// cmdKeygen 生成 ed25519 密钥对，写出 <out-dir>/dev.key（私钥原始 64 字节）
// 与 <out-dir>/dev.pub（公钥原始 32 字节），并打印公钥的 base64 常量形式，
// 供填入 web/modules/apps/sign.go 的 DevPublicKeyB64。
func cmdKeygen(outDir string) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fatal("生成密钥对失败: %v", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatal("创建输出目录失败: %v", err)
	}
	keyPath := filepath.Join(outDir, "dev.key")
	pubPath := filepath.Join(outDir, "dev.pub")
	if err := os.WriteFile(keyPath, priv, 0o600); err != nil {
		fatal("写入私钥失败: %v", err)
	}
	if err := os.WriteFile(pubPath, pub, 0o644); err != nil {
		fatal("写入公钥失败: %v", err)
	}
	fmt.Printf("私钥: %s\n", keyPath)
	fmt.Printf("公钥: %s\n", pubPath)
	fmt.Printf("公钥 base64 常量: %s\n", base64.StdEncoding.EncodeToString(pub))
}

// cmdSign 用私钥文件对 tarball 生成 detached ed25519 签名，写入 out.sig（原始 64 字节）。
func cmdSign(tarballPath, keyPath, outPath string) {
	tarball, err := os.ReadFile(tarballPath)
	if err != nil {
		fatal("读取 tarball 失败: %v", err)
	}
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		fatal("读取私钥失败: %v", err)
	}
	if len(keyBytes) != ed25519.PrivateKeySize {
		fatal("私钥长度非法: 期望 %d 字节，实际 %d 字节", ed25519.PrivateKeySize, len(keyBytes))
	}
	priv := ed25519.PrivateKey(keyBytes)
	sig := ed25519.Sign(priv, tarball)
	if err := os.WriteFile(outPath, sig, 0o644); err != nil {
		fatal("写入签名失败: %v", err)
	}
	fmt.Printf("签名: %s\n", outPath)
}

func fatal(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
