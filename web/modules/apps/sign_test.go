package apps

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"testing"
)

func TestVerifyAppSignature_RoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tarball := []byte("fake tarball content for signing test")
	sig := ed25519.Sign(priv, tarball)

	if !VerifyAppSignature(tarball, sig, pub) {
		t.Error("合法签名应验证通过")
	}
}

func TestVerifyAppSignature_TamperedTarball(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tarball := []byte("fake tarball content for signing test")
	sig := ed25519.Sign(priv, tarball)

	tampered := append([]byte{}, tarball...)
	tampered[0] ^= 0xFF
	if VerifyAppSignature(tampered, sig, pub) {
		t.Error("篡改后的 tarball 应验证失败")
	}
}

func TestVerifyAppSignature_WrongPublicKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	wrongPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tarball := []byte("fake tarball content for signing test")
	sig := ed25519.Sign(priv, tarball)

	if VerifyAppSignature(tarball, sig, wrongPub) {
		t.Error("错误公钥应验证失败")
	}
}

func TestVerifyAppSignature_MalformedInputs(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tarball := []byte("content")

	if VerifyAppSignature(tarball, []byte("too-short-sig"), pub) {
		t.Error("长度非法的签名应验证失败，不应 panic")
	}
	if VerifyAppSignature(tarball, make([]byte, ed25519.SignatureSize), []byte("bad-pub")) {
		t.Error("长度非法的公钥应验证失败，不应 panic")
	}
}

// TestDevKeypair_SignVerifyWithDevPublicKey 验证入仓的开发密钥对（testdata/devkey/）
// 与 sign.go 中 DevPublicKeyB64 常量一致，且能完成签名往返。
func TestDevKeypair_SignVerifyWithDevPublicKey(t *testing.T) {
	privBytes, err := os.ReadFile("testdata/devkey/dev.key")
	if err != nil {
		t.Fatalf("读取开发私钥失败: %v", err)
	}
	if len(privBytes) != ed25519.PrivateKeySize {
		t.Fatalf("开发私钥长度非法: %d", len(privBytes))
	}
	priv := ed25519.PrivateKey(privBytes)

	pubBytes, err := base64.StdEncoding.DecodeString(DevPublicKeyB64)
	if err != nil {
		t.Fatalf("DevPublicKeyB64 不是合法 base64: %v", err)
	}
	pub := ed25519.PublicKey(pubBytes)

	// 私钥派生的公钥应与常量一致（确认 testdata 与 sign.go 常量没有脱节）。
	derivedPub := priv.Public().(ed25519.PublicKey)
	if !derivedPub.Equal(pub) {
		t.Fatal("DevPublicKeyB64 与 testdata/devkey/dev.key 派生的公钥不一致")
	}

	tarball := []byte("hello-app demo tarball bytes")
	sig := ed25519.Sign(priv, tarball)
	if !VerifyAppSignature(tarball, sig, pub) {
		t.Error("开发密钥对签名往返应验证通过")
	}
}
