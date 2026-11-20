package apps

import "crypto/ed25519"

// DevPublicKeyB64 是开发密钥对（web/modules/apps/testdata/devkey/）的公钥，base64 标准编码。
// 由 scripts/appsign（go run）keygen 子命令生成，供 M1 期测试与安装路径使用。
//
// TODO(发布前): 正式上架前须替换为市场签发者的正式公钥（§1「验签公钥编译进核心，
// 市场方为 v0.1 唯一签发者」），并移除/隔离开发密钥对，避免开发私钥签出的包被
// 生产环境误信任。
const DevPublicKeyB64 = "5mxVAYf9wWNaqdRHwG9c63EsrpGJRrT0SWAmwtjW/NM="

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
