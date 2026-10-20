package system

import "testing"

func TestValidateCertDomain(t *testing.T) {
	cases := []struct {
		domain string
		want   bool
	}{
		{"nas.local", true},
		{"nas-panel.local", true},
		{"example.com", true},
		{"sub.example.com", true},
		{"192.168.1.1", true},
		{"a", true},
		{"", false},
		{"$(id)", false},
		{`"; reboot #`, false},
		{"a b", false},
		{"a;b", false},
		{"a$b", false},
		{"a(b)", false},
		{"a/b", false},
		{"a\nb", false},
		{"a'b", false},
	}
	for _, c := range cases {
		if got := validateCertDomain(c.domain); got != c.want {
			t.Errorf("validateCertDomain(%q) = %v, want %v", c.domain, got, c.want)
		}
	}

	// 253 字符上限
	long := make([]byte, 254)
	for i := range long {
		long[i] = 'a'
	}
	if validateCertDomain(string(long)) {
		t.Error("超长域名 (254) 应被拒绝")
	}
}
