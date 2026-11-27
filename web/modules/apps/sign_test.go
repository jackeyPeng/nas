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
// 与 sign.go 中 DevPublicKeyB64 常量一致，且能完成签名往返。注意：这只验证
// VerifyAppSignature 这个底层原语本身能正确往返，不代表 DevPublicKeyB64 是
// 生产信任锚——resolveTrustedPublicKey 从不读取它（见下方
// TestResolveTrustedPublicKey_* 系列）。
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

// setTestTrustedPublicKey 是测试专用入口：写入 sign.go 的
// testTrustedPublicKeyOverride（该变量只应由 _test.go 文件写入，生产代码
// 路径从不写它）。pub 为 nil 时清空覆盖，让 resolveTrustedPublicKey 回落到
// TrustedPublicKeyB64（通常为空 → fail-closed），用于测试"未配置信任锚"
// 场景。测试结束自动恢复为写入前的值，互不污染。
func setTestTrustedPublicKey(t *testing.T, pub ed25519.PublicKey) {
	t.Helper()
	prev := testTrustedPublicKeyOverride
	if pub == nil {
		testTrustedPublicKeyOverride = nil
	} else {
		testTrustedPublicKeyOverride = []byte(pub)
	}
	t.Cleanup(func() { testTrustedPublicKeyOverride = prev })
}

// TestResolveTrustedPublicKey_NotConfigured 验证 TrustedPublicKeyB64 为空、
// 且无测试覆盖时 fail-closed：拒绝，不回退到 DevPublicKeyB64 或任何默认值。
func TestResolveTrustedPublicKey_NotConfigured(t *testing.T) {
	setTestTrustedPublicKey(t, nil)
	prevTrusted := TrustedPublicKeyB64
	TrustedPublicKeyB64 = ""
	t.Cleanup(func() { TrustedPublicKeyB64 = prevTrusted })

	if _, err := resolveTrustedPublicKey(); err != errTrustedKeyNotConfigured {
		t.Errorf("未配置信任锚应返回 errTrustedKeyNotConfigured，got %v", err)
	}
}

// TestResolveTrustedPublicKey_Malformed 验证 TrustedPublicKeyB64 配置成非法
// base64 或长度不对时 fail-closed，而不是 panic 或悄悄放行。
func TestResolveTrustedPublicKey_Malformed(t *testing.T) {
	setTestTrustedPublicKey(t, nil)

	cases := []string{"не-base64!!!", base64.StdEncoding.EncodeToString([]byte("too-short"))}
	for _, c := range cases {
		prevTrusted := TrustedPublicKeyB64
		TrustedPublicKeyB64 = c
		if _, err := resolveTrustedPublicKey(); err != errTrustedKeyMalformed {
			t.Errorf("非法公钥 %q 应返回 errTrustedKeyMalformed，got %v", c, err)
		}
		TrustedPublicKeyB64 = prevTrusted
	}
}

// TestResolveTrustedPublicKey_DevKeyNeverDefault 验证即便 DevPublicKeyB64
// 是合法的 ed25519 公钥，resolveTrustedPublicKey 在没有显式配置
// TrustedPublicKeyB64（也没有测试覆盖）时绝不会把它当成信任锚返回——
// 这是本次修复的核心断言：开发公钥不是、也不能是隐式默认信任锚。
func TestResolveTrustedPublicKey_DevKeyNeverDefault(t *testing.T) {
	setTestTrustedPublicKey(t, nil)
	prevTrusted := TrustedPublicKeyB64
	TrustedPublicKeyB64 = ""
	t.Cleanup(func() { TrustedPublicKeyB64 = prevTrusted })

	pub, err := resolveTrustedPublicKey()
	if err == nil {
		t.Fatalf("预期 fail-closed 报错，实际返回了公钥: %x", []byte(pub))
	}
	devPub, decErr := base64.StdEncoding.DecodeString(DevPublicKeyB64)
	if decErr != nil {
		t.Fatalf("DevPublicKeyB64 解码失败: %v", decErr)
	}
	if pub != nil && ed25519.PublicKey(pub).Equal(ed25519.PublicKey(devPub)) {
		t.Fatal("resolveTrustedPublicKey 不应返回 DevPublicKeyB64")
	}
}

// TestResolveTrustedPublicKey_InjectedFormalKey 验证"正式构建注入
// TrustedPublicKeyB64"这条路径：配置后 resolveTrustedPublicKey 返回对应
// 公钥，且用该公钥能验证该私钥签的包（模拟正式发布构建场景）。
func TestResolveTrustedPublicKey_InjectedFormalKey(t *testing.T) {
	setTestTrustedPublicKey(t, nil)
	formalPub, formalPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	prevTrusted := TrustedPublicKeyB64
	TrustedPublicKeyB64 = base64.StdEncoding.EncodeToString(formalPub)
	t.Cleanup(func() { TrustedPublicKeyB64 = prevTrusted })

	tarball := []byte("formal release tarball bytes")
	sig := ed25519.Sign(formalPriv, tarball)
	if err := verifyAppSignatureTrusted(tarball, sig); err != nil {
		t.Errorf("正式注入公钥对应私钥的签名应通过校验，got err=%v", err)
	}

	// 用开发私钥签的包在这一构建（信任锚是正式公钥）下必须被拒绝。
	devPriv, rerr := os.ReadFile("testdata/devkey/dev.key")
	if rerr != nil {
		t.Fatalf("读取开发私钥失败: %v", rerr)
	}
	devSig := ed25519.Sign(ed25519.PrivateKey(devPriv), tarball)
	if err := verifyAppSignatureTrusted(tarball, devSig); err == nil {
		t.Error("开发私钥签名在正式信任锚配置下应被拒绝")
	}
}
