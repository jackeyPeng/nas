package apps

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
)

// DevPublicKeyB64 是开发密钥对（web/modules/apps/testdata/devkey/）的公钥，base64 标准编码。
// 由 scripts/appsign（go run）keygen 子命令生成。
//
// 警示：该密钥对的私钥已入仓（testdata/devkey/dev.key），属于公开已知材料，
// 仅供本地开发时手工签包使用（scripts/mkapp.sh 显式传 --dev-key 才会用它，
// 不再是省略 key-dir 时的隐式默认）。禁止把它当作生产信任锚：
// resolveTrustedPublicKey 不会读取此常量，
// 任何用开发私钥签出的包在设置了正式 TrustedPublicKeyB64 的构建上都会被
// 拒绝（验签不匹配）。
const DevPublicKeyB64 = "5mxVAYf9wWNaqdRHwG9c63EsrpGJRrT0SWAmwtjW/NM="

// TrustedPublicKeyB64 是正式构建注入的验签公钥（base64 标准编码），决定
// 「安装/升级信任谁签的包」（§1「验签公钥编译进核心，市场方为 v0.1 唯一
// 签发者」）。
//
// 注入方式：
//
//	-ldflags "-X nas-panel/modules/apps.TrustedPublicKeyB64=<市场正式公钥 base64>"
//
// 与 modules/version 的 DisplayVersion/BuildTime 同一套 ldflags -X 注入
// 约定（见 scripts/release.sh），由正式/发布构建显式传入，本地 `make dev`/
// `make build` 默认留空。
//
// 留空是预期的本地开发态：resolveTrustedPublicKey 在留空时fail-closed——
// 拒绝一切安装/升级请求，而不是回退到 DevPublicKeyB64。生产部署必须用
// 正式私钥对应的公钥显式注入这个变量，否则面板将拒绝所有应用安装/升级。
var TrustedPublicKeyB64 = ""

// testTrustedPublicKeyOverride 仅供 _test.go 文件通过 setTestTrustedPublicKey
// 写入：让测试可以注入一枚专用测试密钥对（见 lifecycle_test.go 的
// testKeypair），不依赖、也不等同于 DevPublicKeyB64 或任何真实正式私钥。
// 生产代码路径不写这个变量，默认 nil 即不生效。
var testTrustedPublicKeyOverride []byte

// errTrustedKeyNotConfigured / errTrustedKeyMalformed 是 resolveTrustedPublicKey
// 的失败原因，调用方统一按「验签失败，拒绝安装/升级」处理，不向客户端泄露
// 是「没配」还是「配错了」的区别（避免给攻击者多余信息），但日志里保留。
var (
	errTrustedKeyNotConfigured = errors.New("未配置正式验签公钥（TrustedPublicKeyB64 为空），拒绝安装/升级")
	errTrustedKeyMalformed     = errors.New("正式验签公钥格式非法（非标准 base64 或长度不是 ed25519 公钥长度），拒绝安装/升级")
)

// resolveTrustedPublicKey 是安装（手工上传 + 市场一键）与升级三条入口共用
// 的唯一信任锚解析点：fail-closed——没配、配错都直接拒绝，绝不回退到
// DevPublicKeyB64 或其它默认值。
//
// 优先级：测试注入覆盖（testTrustedPublicKeyOverride，仅 _test.go 可写）>
// 正式构建注入的 TrustedPublicKeyB64。两者都没有 → 拒绝。
func resolveTrustedPublicKey() (ed25519.PublicKey, error) {
	if testTrustedPublicKeyOverride != nil {
		return ed25519.PublicKey(testTrustedPublicKeyOverride), nil
	}
	if TrustedPublicKeyB64 == "" {
		return nil, errTrustedKeyNotConfigured
	}
	pub, err := base64.StdEncoding.DecodeString(TrustedPublicKeyB64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, errTrustedKeyMalformed
	}
	return ed25519.PublicKey(pub), nil
}

// VerifyAppSignature 校验 tarball 的 ed25519 detached 签名（§1 发布物三件套）。
// pub 长度不合法或签名不匹配均返回 false，不 panic、不泄露细节（调用方按拒装处理）。
func VerifyAppSignature(tarball, sig []byte, pub ed25519.PublicKey) bool {
	if len(pub) != ed25519.PublicKeySize {
		return false
	}
	if len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, tarball, sig)
}

// verifyAppSignatureTrusted 是 installFromTarball/handleUpgrade 共用的「解析
// 信任锚 + 验签」一步到位封装，失败时返回统一错误（不区分具体原因，调用方
// 按拒装处理）。这是手工安装、市场安装、升级三条入口唯一应该调用的验签函数。
func verifyAppSignatureTrusted(tarball, sig []byte) error {
	pub, err := resolveTrustedPublicKey()
	if err != nil {
		return err
	}
	if !VerifyAppSignature(tarball, sig, pub) {
		return errors.New("签名校验失败")
	}
	return nil
}
