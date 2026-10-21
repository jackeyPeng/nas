package system

import "testing"

// TestCountUpgradableLines 等价于
// `apt list --upgradable 2>/dev/null | grep -c upgradable`。
func TestCountUpgradableLines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{
			"两个可升级包",
			"Listing...\npkg1/stable 1.2 amd64 [upgradable from: 1.1]\npkg2/stable 2.0 amd64 [upgradable from: 1.9]\n",
			2,
		},
		{"无可升级包", "Listing...\n", 0},
		{"空输出", "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := countUpgradableLines(c.in); got != c.want {
				t.Errorf("countUpgradableLines(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}
