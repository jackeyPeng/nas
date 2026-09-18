package monitor

import "testing"

func TestExtractIP(t *testing.T) {
	cases := map[string]string{
		"ipv4:10.216.10.5:56789": "10.216.10.5",
		"10.216.10.5:56789":      "10.216.10.5",
		"ipv4:192.168.1.10":      "192.168.1.10",
		"192.168.1.10":           "192.168.1.10",
		"[fe80::1]:445":          "fe80::1",
		"DESKTOP-ABC":            "", // 主机名不是 IP
		"":                       "",
	}
	for in, want := range cases {
		if got := extractIP(in); got != want {
			t.Errorf("extractIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeMachine(t *testing.T) {
	if got := normalizeMachine("ipv4:10.216.10.5:56789"); got != "10.216.10.5" {
		t.Errorf("normalizeMachine ipv4 form = %q", got)
	}
	if got := normalizeMachine("DESKTOP-ABC"); got != "DESKTOP-ABC" {
		t.Errorf("normalizeMachine hostname = %q", got)
	}
}

func TestGetSMBConnectionsParse(t *testing.T) {
	// 真实 Samba 4.22 smbstatus --json schema（.48 实测采样）
	sample := `{"sessions":{"4144658025":{"session_id":"4144658025","username":"carol","groupname":"carol","auth_time":"2026-09-18T16:20:05.975637+08:00","remote_machine":"10.0.0.2","hostname":"ipv4:10.0.0.2:54770","session_dialect":"SMB3_11"}},"tcons":{"3435579529":{"service":"docs","session_id":"4144658025","machine":"10.0.0.2","connected_at":"2026-09-18T16:20:06.007849+08:00"}}}`
	conns := parseSMBStatusJSON(sample)
	if len(conns) != 1 {
		t.Fatalf("want 1 connection, got %d", len(conns))
	}
	c := conns[0]
	if c.User != "carol" || c.IP != "10.0.0.2" || c.Detail != "docs" {
		t.Errorf("parsed connection = %+v", c)
	}
	if c.ConnectedAt != "2026-09-18T16:20:05.975637+08:00" {
		t.Errorf("connected_at = %q", c.ConnectedAt)
	}
}

func TestGetSMBConnectionsLoopbackFiltered(t *testing.T) {
	// 本机 smbclient 回环连接（remote_machine=::1）应被过滤
	sample := `{"sessions":{"1":{"session_id":"1","username":"carol","remote_machine":"::1","hostname":"ipv6:::1:54770","auth_time":"2026-09-18T16:20:05.975637+08:00"}},"tcons":{}}`
	conns := parseSMBStatusJSON(sample)
	if len(conns) != 0 {
		t.Errorf("loopback session should be filtered, got %+v", conns)
	}
}

func TestGetSMBConnectionsMultiShare(t *testing.T) {
	// 同一 session 连两个共享 → detail 合并
	sample := `{"sessions":{"9":{"session_id":"9","username":"alice","remote_machine":"ipv4:10.1.2.3:50000","auth_time":"2026-09-18T10:00:00Z"}},"tcons":{"1":{"service":"public","session_id":"9"},"2":{"service":"docs","session_id":"9"}}}`
	conns := parseSMBStatusJSON(sample)
	if len(conns) != 1 {
		t.Fatalf("want 1, got %d", len(conns))
	}
	if conns[0].Detail != "docs, public" || conns[0].IP != "10.1.2.3" {
		t.Errorf("got %+v", conns[0])
	}
}

func TestNormalizeClientIP(t *testing.T) {
	cases := map[string]string{
		"::1":                 "::1",
		"ipv6:::1:54770":      "::1", // host ::1 + port（无方括号 IPv6 形态）
		"ipv4:10.1.2.3:54770": "10.1.2.3",
		"10.1.2.3":            "10.1.2.3",
		"::ffff:10.0.0.1":     "10.0.0.1", // IPv4-mapped 归一
		"DESKTOP-ABC":         "",         // 主机名不是 IP
		"":                    "",
	}
	for in, want := range cases {
		if got := normalizeClientIP(in); got != want {
			t.Errorf("normalizeClientIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGetSMBConnectionsEmpty(t *testing.T) {
	// 无会话时返回空 slice（JSON 序列化为 []，不是 null）
	conns := parseSMBStatusJSON(`{"sessions":{},"tcons":{},"open_files":{}}`)
	if conns == nil {
		t.Error("want non-nil empty slice, got nil")
	}
	if len(conns) != 0 {
		t.Errorf("want 0, got %d", len(conns))
	}
}
