package common

import "fmt"

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
