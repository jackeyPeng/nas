package main

import "testing"

func TestParseVersionFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"long flag", []string{"nas-panel", "--version"}, true},
		{"short flag", []string{"nas-panel", "-v"}, true},
		{"no args", []string{"nas-panel"}, false},
		{"other subcommand", []string{"nas-panel", "serve"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseVersionFlag(c.args); got != c.want {
				t.Errorf("parseVersionFlag(%v) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}
