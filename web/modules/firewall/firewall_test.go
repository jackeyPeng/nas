package firewall

import (
	"reflect"
	"testing"
)

func TestBuildAddRuleArgsPortRange(t *testing.T) {
	// 端口范围必须原样传给 ufw，不能被截断成起始端口（ufw 原生支持 "port 30000:31000"）
	args := buildAddRuleArgs("30000:31000", "tcp", "allow", "", "")
	want := []string{"allow", "30000:31000/tcp"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildAddRuleArgs(range, no from) = %v, want %v", args, want)
	}

	args = buildAddRuleArgs("30000:31000", "tcp", "allow", "192.168.1.100", "")
	want = []string{"allow", "from", "192.168.1.100", "to", "any", "port", "30000:31000", "proto", "tcp"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildAddRuleArgs(range, with from) = %v, want %v", args, want)
	}
}

func TestBuildAddRuleArgsSinglePort(t *testing.T) {
	args := buildAddRuleArgs("22", "tcp", "deny", "", "")
	want := []string{"deny", "22/tcp"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildAddRuleArgs(single port) = %v, want %v", args, want)
	}
}

func TestBuildAddRuleArgsAnyProto(t *testing.T) {
	args := buildAddRuleArgs("8090", "any", "allow", "", "")
	want := []string{"allow", "8090"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildAddRuleArgs(any proto) = %v, want %v", args, want)
	}
}

func TestBuildAddRuleArgsComment(t *testing.T) {
	args := buildAddRuleArgs("22", "tcp", "allow", "", "allow ssh")
	want := []string{"allow", "22/tcp", "comment", "allow ssh"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildAddRuleArgs(with comment) = %v, want %v", args, want)
	}
}

func TestParseRulesWithComment(t *testing.T) {
	out := "Status: active\n\n" +
		"     To                         Action      From\n" +
		"     --                         ------      ----\n" +
		"[ 1] 22/tcp                     ALLOW IN    Anywhere                   # allow ssh\n"
	rules := parseRules(out)
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	if rules[0].Comment != "allow ssh" {
		t.Errorf("Comment = %q, want %q", rules[0].Comment, "allow ssh")
	}
	if rules[0].Port != "22" || rules[0].Proto != "tcp" {
		t.Errorf("Port/Proto = %q/%q, want 22/tcp", rules[0].Port, rules[0].Proto)
	}
}

func TestParseRulesWithoutComment(t *testing.T) {
	out := "[ 2] 30000:31000/tcp             ALLOW IN    Anywhere\n"
	rules := parseRules(out)
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	if rules[0].Comment != "" {
		t.Errorf("Comment = %q, want empty", rules[0].Comment)
	}
	if rules[0].Port != "30000:31000" {
		t.Errorf("Port = %q, want 30000:31000", rules[0].Port)
	}
}
