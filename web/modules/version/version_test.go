package version

import "testing"

// TestFirstOutputLine 等价于 `| head -1` 的行为。
func TestFirstOutputLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"单行", "vsftpd: version 3.0.5", "vsftpd: version 3.0.5"},
		{"多行取第一行", "vsftpd: version 3.0.5\nsome warning\n", "vsftpd: version 3.0.5"},
		{"空输出", "", ""},
		{"只有空白", "   \n  \n", ""},
		{"首行带前后空白", "  vsftpd: version 3.0.5  \nrest", "vsftpd: version 3.0.5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := firstOutputLine(c.in); got != c.want {
				t.Errorf("firstOutputLine(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestParseOSPrettyName 等价于
// `awk -F'"' '/^PRETTY_NAME/{print $2}' /etc/os-release`。
func TestParseOSPrettyName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"标准Debian格式",
			"PRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\nNAME=\"Debian GNU/Linux\"\n",
			"Debian GNU/Linux 13 (trixie)",
		},
		{"无PRETTY_NAME行", "NAME=\"Debian GNU/Linux\"\n", ""},
		{"空内容", "", ""},
		{"PRETTY_NAME在中间", "NAME=\"X\"\nPRETTY_NAME=\"Fedora Linux 44\"\nVERSION_ID=\"44\"\n", "Fedora Linux 44"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseOSPrettyName(c.in); got != c.want {
				t.Errorf("parseOSPrettyName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
