package system

import "testing"

func TestValidateSSHParams_Valid(t *testing.T) {
	cases := []struct {
		port, permitRoot, passwordAuth, maxAuthTries string
	}{
		{"", "", "", ""},
		{"22", "yes", "no", "6"},
		{"2222", "no", "yes", "100"},
		{"1", "", "", "1"},
		{"65535", "", "", ""},
	}
	for _, c := range cases {
		if err := validateSSHParams(c.port, c.permitRoot, c.passwordAuth, c.maxAuthTries); err != nil {
			t.Errorf("validateSSHParams(%q,%q,%q,%q) 应通过, got err: %v",
				c.port, c.permitRoot, c.passwordAuth, c.maxAuthTries, err)
		}
	}
}

func TestValidateSSHParams_NewlineInjection(t *testing.T) {
	if err := validateSSHParams("22\nPermitRootLogin yes", "", "", ""); err == nil {
		t.Error("port 换行注入应被拒绝")
	}
}

func TestValidateSSHParams_PortOutOfRange(t *testing.T) {
	for _, p := range []string{"0", "65536", "-1", "abc", "1 2"} {
		if err := validateSSHParams(p, "", "", ""); err == nil {
			t.Errorf("port=%q 应被拒绝", p)
		}
	}
}

func TestValidateSSHParams_PermitRootLoginInvalid(t *testing.T) {
	for _, v := range []string{"maybe", "yes\nFoo bar", "1"} {
		if err := validateSSHParams("", v, "", ""); err == nil {
			t.Errorf("permit_root_login=%q 应被拒绝", v)
		}
	}
}

func TestValidateSSHParams_PasswordAuthInvalid(t *testing.T) {
	if err := validateSSHParams("", "", "maybe", ""); err == nil {
		t.Error("password_auth=maybe 应被拒绝")
	}
}

func TestValidateSSHParams_MaxAuthTriesOutOfRange(t *testing.T) {
	for _, n := range []string{"0", "101", "-5", "abc"} {
		if err := validateSSHParams("", "", "", n); err == nil {
			t.Errorf("max_auth_tries=%q 应被拒绝", n)
		}
	}
}
