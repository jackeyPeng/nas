package common

import "testing"

func TestIsTrustedProxyHost(t *testing.T) {
	trusted := []string{
		"127.0.0.1", "::1",
		"10.0.0.1", "10.255.255.255",
		"172.16.0.1", "172.31.255.255",
		"192.168.1.1", "192.168.255.255",
	}
	for _, h := range trusted {
		if !IsTrustedProxyHost(h) {
			t.Errorf("IsTrustedProxyHost(%q) = false, want true", h)
		}
	}

	untrusted := []string{
		"1.2.3.4",    // 公网
		"172.32.0.1", // 刚好在 172.16.0.0/12 之外
		"8.8.8.8",
		"not-an-ip",
		"",
	}
	for _, h := range untrusted {
		if IsTrustedProxyHost(h) {
			t.Errorf("IsTrustedProxyHost(%q) = true, want false", h)
		}
	}
}

func TestRequestIPFrom(t *testing.T) {
	cases := []struct {
		name      string
		remote    string
		forwarded string
		want      string
	}{
		{
			name:      "公网直连+XFF_不采信XFF",
			remote:    "1.2.3.4:5555",
			forwarded: "9.9.9.9",
			want:      "1.2.3.4",
		},
		{
			name:      "私网代理+XFF_采信XFF首段",
			remote:    "192.168.1.10:443",
			forwarded: "9.9.9.9, 10.0.0.1",
			want:      "9.9.9.9",
		},
		{
			name:      "无XFF_用remote",
			remote:    "192.168.1.10:443",
			forwarded: "",
			want:      "192.168.1.10",
		},
		{
			name:      "IPv6_RemoteAddr带端口_无XFF",
			remote:    "[::1]:1234",
			forwarded: "",
			want:      "::1",
		},
		{
			name:      "IPv6可信代理+XFF",
			remote:    "[::1]:1234",
			forwarded: "2001:db8::1",
			want:      "2001:db8::1",
		},
		{
			name:      "loopback代理+XFF空白项回退remote",
			remote:    "127.0.0.1:9000",
			forwarded: "   ",
			want:      "127.0.0.1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := requestIPFrom(c.remote, c.forwarded)
			if got != c.want {
				t.Errorf("requestIPFrom(%q, %q) = %q, want %q", c.remote, c.forwarded, got, c.want)
			}
		})
	}
}
