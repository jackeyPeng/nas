package rclone

import "testing"

func TestStatsReParsing(t *testing.T) {
	cases := []struct {
		line    string
		percent string
		trans   string
		total   string
		speed   string
		eta     string
	}{
		{
			"2026-09-22 16:50:41 INFO  :    50.024 MiB / 76.294 MiB, 66%, 2.106 MiB/s, ETA 12s",
			"66", "50.024 MiB", "76.294 MiB", "2.106 MiB/s", "12s",
		},
		{
			"Transferred: \t 1.234 GiB / 5.678 GiB, 22%, 10.5 MiB/s, ETA 1m23s (xfr#12, to-chk=3/45)",
			"22", "1.234 GiB", "5.678 GiB", "10.5 MiB/s", "1m23s",
		},
		{
			"2026-09-22 16:50:54 INFO  :    76.294 MiB / 76.294 MiB, 100%, 2.046 MiB/s, ETA 0s",
			"100", "76.294 MiB", "76.294 MiB", "2.046 MiB/s", "0s",
		},
		{
			"2026-09-22 16:50:30 INFO  : big1.bin: Copied (new)",
			"", "", "", "", "",
		},
	}
	for _, c := range cases {
		m := statsRe.FindStringSubmatch(c.line)
		if c.percent == "" {
			if m != nil {
				t.Errorf("line %q should NOT match, got %v", c.line, m)
			}
			continue
		}
		if m == nil {
			t.Errorf("line %q should match", c.line)
			continue
		}
		if m[1] != c.trans || m[2] != c.total || m[3] != c.percent || m[4] != c.speed || m[5] != c.eta {
			t.Errorf("line %q parsed wrong: %v (want %s/%s/%s/%s/%s)", c.line, m[1:], c.trans, c.total, c.percent, c.speed, c.eta)
		}
	}
}

func TestPushLineProgress(t *testing.T) {
	p := &taskProgress{TaskID: "x", OutputTail: []string{}}
	p.pushLine("2026-09-22 16:50:41 INFO  :    50.024 MiB / 76.294 MiB, 66%, 2.106 MiB/s, ETA 12s")
	v := p.view()
	if v.Percent != 66 || v.Speed != "2.106 MiB/s" || v.ETA != "12s" || v.Transferred != "50.024 MiB" || v.Total != "76.294 MiB" {
		t.Fatalf("progress view wrong: %+v", v)
	}
	if p.full.String() == "" {
		t.Fatal("full output not accumulated")
	}
}

func TestTailTrim40(t *testing.T) {
	p := &taskProgress{TaskID: "x", OutputTail: []string{}}
	for i := 0; i < 60; i++ {
		p.pushLine("line")
	}
	if len(p.view().OutputTail) != 40 {
		t.Fatalf("tail should be 40, got %d", len(p.view().OutputTail))
	}
}
