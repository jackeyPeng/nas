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
	// 直接测 JSON 解析路径：getSMBConnections 依赖 sudo smbstatus，
	// 此处只验证 smbstatus JSON 结构与解析函数契约一致。
	sample := `{"sessions":{"1234":{"user":"alice","machine":"ipv4:10.0.0.2:50000","address":"ipv4:10.0.0.2:445","connected_at":"2026-09-18T10:00:00.000Z"}},"tcons":{"5678":{"service":"public","machine":"ipv4:10.0.0.2:50000","connected_at":"2026-09-18T10:00:01.000Z"}}}`
	conns := parseSMBStatusJSON(sample)
	if len(conns) != 1 {
		t.Fatalf("want 1 connection, got %d", len(conns))
	}
	c := conns[0]
	if c.User != "alice" || c.IP != "10.0.0.2" || c.Detail != "public" {
		t.Errorf("parsed connection = %+v", c)
	}
	if c.ConnectedAt != "2026-09-18T10:00:00.000Z" {
		t.Errorf("connected_at = %q", c.ConnectedAt)
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
