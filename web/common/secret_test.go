package common

import "testing"

func TestValidSecret(t *testing.T) {
	ok := []string{
		"abcdefgh1234",        // 12位基本密码
		"P@ssw0rd 123 \"ok\"", // 含空格/引号
		"pa$$word&pipe|12",    // 含 $ & |
		"中文密码也可以通过呀12", // 含中文
	}
	for _, p := range ok {
		if err := ValidSecret(p); err != nil {
			t.Errorf("ValidSecret(%q) 意外报错: %v", p, err)
		}
	}

	bad := map[string]string{
		"":                "空字符串",
		"abcdefgh1234\n":  "含换行 \\n",
		"abcdefgh1234\r":  "含回车 \\r",
		"abcdefgh1234\t":  "含 tab \\t",
		"abcdefgh1234\x00": "含 NUL \\x00",
		"abcdefgh1234\x7f": "含 DEL \\x7f",
	}
	for p, desc := range bad {
		if err := ValidSecret(p); err == nil {
			t.Errorf("ValidSecret(%s) 应被拒绝", desc)
		}
	}
}
