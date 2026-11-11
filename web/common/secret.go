package common

import (
	"fmt"
	"regexp"
	"strings"
)

// MaskedPlaceholder 脱敏后替换真实值的占位字符串，前端仅用于判断"是否已配置"，
// 不依赖具体值做 diff，替换成它是安全的。
const MaskedPlaceholder = "********"

// sensitiveKeyPattern 匹配应视为敏感信息的配置键名：密码/密钥/令牌/凭据类。
// 大小写不敏感，覆盖 .env 里常见的 PASS/SECRET/TOKEN/KEY/WEBHOOK/CREDENTIAL/PRIVATE。
var sensitiveKeyPattern = regexp.MustCompile(`(?i)(PASS|SECRET|TOKEN|KEY|WEBHOOK|CREDENTIAL|PRIVATE)`)

// IsSensitiveEnvKey 判断某个 .env / 配置键名是否属于敏感信息，读取时应脱敏返回。
func IsSensitiveEnvKey(key string) bool {
	return sensitiveKeyPattern.MatchString(key)
}

// MaskSensitiveEnv 对 key=value 映射做脱敏：敏感键的值替换为占位符，键保留
// （前端据此渲染"已配置/未配置"状态，不需要也不应该拿到真实值）。
// 返回新 map，不修改入参。
func MaskSensitiveEnv(values map[string]string) map[string]string {
	masked := make(map[string]string, len(values))
	for k, v := range values {
		if IsSensitiveEnvKey(k) {
			masked[k] = MaskedPlaceholder
		} else {
			masked[k] = v
		}
	}
	return masked
}

// MaskSensitiveEnvLine 对形如 "KEY=VALUE" 的单行配置文本做脱敏，供配置文件
// 在线编辑器（/api/config/file）展示原始文件内容前调用。非 KEY=VALUE 形状
// 或非敏感键的行原样返回。
func MaskSensitiveEnvLine(line string) string {
	idx := strings.IndexByte(line, '=')
	if idx < 0 {
		return line
	}
	key := strings.TrimSpace(line[:idx])
	if !IsSensitiveEnvKey(key) {
		return line
	}
	return line[:idx] + "=" + MaskedPlaceholder
}

// ValidSecret 校验用户提交的密码/密钥类敏感字符串。
// 密码会经 chpasswd/smbpasswd 的 stdin（"user:pass\n"）、.env KEY=VALUE 行、
// systemd unit 的 ExecStart 行等纯文本协议传递，换行或其他控制字符会被当作
// 协议分隔符解析，造成指令注入（如 chpasswd stdin 多塞一行改掉 root 密码）或
// 配置文件结构损坏。本函数只做"拒绝危险字符"的内容校验，不含长度策略——
// 长度下限由各调用点按自身场景维护（当前均为 >=12 位）。
func ValidSecret(s string) error {
	if s == "" {
		return fmt.Errorf("密码不能为空")
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("密码不能包含换行或控制字符")
		}
	}
	return nil
}
