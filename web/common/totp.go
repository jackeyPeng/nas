package common

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"time"
)

// RFC 6238 TOTP（无第三方依赖）。
// secret 为 base32（无填充、大写）编码的 20 字节密钥。

var b32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateTOTPSecret 生成一个新的 base32 编码 TOTP 密钥（20 字节 / 160 位）。
func GenerateTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b32NoPad.EncodeToString(b), nil
}

// TOTPCode 按 RFC 6238 计算给定时刻 t 对应的 6 位动态验证码。
func TOTPCode(secret string, t time.Time) (string, error) {
	key, err := b32NoPad.DecodeString(secret)
	if err != nil {
		return "", err
	}
	counter := uint64(t.Unix() / 30)
	msg := make([]byte, 8)
	binary.BigEndian.PutUint64(msg, counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(msg)
	hash := mac.Sum(nil)

	offset := hash[len(hash)-1] & 0x0f
	code := (uint32(hash[offset]&0x7f)<<24 |
		uint32(hash[offset+1])<<16 |
		uint32(hash[offset+2])<<8 |
		uint32(hash[offset+3])) % 1000000
	return fmt.Sprintf("%06d", code), nil
}

// VerifyTOTP 校验 code 是否命中当前或上一个 30 秒时间窗（允许 ±30s 时钟偏差）。
func VerifyTOTP(secret, code string) bool {
	now := time.Now()
	for _, t := range []time.Time{now, now.Add(-30 * time.Second)} {
		if c, err := TOTPCode(secret, t); err == nil && c == code {
			return true
		}
	}
	return false
}
