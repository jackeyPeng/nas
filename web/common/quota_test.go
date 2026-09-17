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
