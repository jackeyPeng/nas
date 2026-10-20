package common

import "testing"

func TestMountOptionsHavePrjquota(t *testing.T) {
	yes := []string{
		"rw,relatime,attr2,inode64,prjquota",
		"rw,prjquota",
		"prjquota",
		"rw,pquota",
	}
	for _, o := range yes {
		if !mountOptionsHavePrjquota(o) {
			t.Errorf("mountOptionsHavePrjquota(%q) = false, want true", o)
		}
	}
	no := []string{
		"rw,relatime,attr2,inode64",
		"rw",
		"",
		"rw,usrquota", // 只开 user quota 不算
		"rw,grpquota", // 只开 group quota 不算
		"rw,quota_x",  // 相似但不是
	}
	for _, o := range no {
		if mountOptionsHavePrjquota(o) {
			t.Errorf("mountOptionsHavePrjquota(%q) = true, want false", o)
		}
	}
}

func TestMountQuotaSupportEmpty(t *testing.T) {
	st := MountQuotaSupport("")
	if st.Supported {
		t.Error("空挂载点不应返回 supported")
	}
	if st.Reason == "" {
		t.Error("空挂载点应给出 reason")
	}
}

func TestParseProjectsLine(t *testing.T) {
	cases := []struct {
		line     string
		wantID   int
		wantPath string
		wantName string
		wantOK   bool
	}{
		// 标准两段格式：id:path
		{"1000:/data/nas1/public", 1000, "/data/nas1/public", "", true},
		{"  1001:/data/nas2/alice  ", 1001, "/data/nas2/alice", "", true},
		// 脏数据三段格式：name:id:path
		{"pool_public:1000:/data/nas1/public", 1000, "/data/nas1/public", "pool_public", true},
		{"home_alice:1001:/data/nas2/alice", 1001, "/data/nas2/alice", "home_alice", true},
		// 坏行
		{"", 0, "", "", false},
		{"# comment", 0, "", "", false},
		{"not-a-valid-line", 0, "", "", false},
		{"abc:/data/nas1/public", 0, "", "", false},
		{"a:b:c:d", 0, "", "", false},
	}
	for _, c := range cases {
		id, path, name, ok := ParseProjectsLine(c.line)
		if ok != c.wantOK {
			t.Errorf("ParseProjectsLine(%q) ok = %v, want %v", c.line, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if id != c.wantID || path != c.wantPath || name != c.wantName {
			t.Errorf("ParseProjectsLine(%q) = (%d, %q, %q), want (%d, %q, %q)",
				c.line, id, path, name, c.wantID, c.wantPath, c.wantName)
		}
	}
}
