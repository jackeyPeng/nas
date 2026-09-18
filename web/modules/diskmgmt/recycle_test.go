package diskmgmt

import (
	"strings"
	"testing"
)

func TestResolveRecyclePath(t *testing.T) {
	share := "/data/nas1/movies"

	// 新布局正常路径
	got, err := resolveRecyclePath(share, ".recycle", "alice", "film.mkv")
	if err != nil || got != "/data/nas1/movies/.recycle/alice/film.mkv" {
		t.Errorf("new layout: got %q err %v", got, err)
	}
	// keeptree 子目录
	got, err = resolveRecyclePath(share, ".recycle", "alice", "sub/dir/f.txt")
	if err != nil || got != "/data/nas1/movies/.recycle/alice/sub/dir/f.txt" {
		t.Errorf("keeptree: got %q err %v", got, err)
	}
	// 旧布局（无 user）
	got, err = resolveRecyclePath(share, "#recycle", "", "old.txt")
	if err != nil || got != "/data/nas1/movies/#recycle/old.txt" {
		t.Errorf("legacy: got %q err %v", got, err)
	}

	// 穿越攻击必须被拒
	bad := []struct{ user, rel string }{
		{"alice", "../secret"},
		{"alice", "a/../../secret"},
		{"alice", "/etc/passwd"},
		{"alice", ""},
		{"../bob", "x.txt"},
		{"a/b", "x.txt"},
	}
	for _, b := range bad {
		if _, err := resolveRecyclePath(share, ".recycle", b.user, b.rel); err == nil {
			t.Errorf("resolveRecyclePath(%q,%q) should fail", b.user, b.rel)
		}
	}
	// 新布局缺 user
	if _, err := resolveRecyclePath(share, ".recycle", "", "x.txt"); err == nil {
		t.Error("new layout without user should fail")
	}
}

func TestResolveRecyclePathRejectsOutsideData(t *testing.T) {
	// sharePath 本身不在 /data 下（脏元数据）时必须拒绝，而不是拼出危险路径
	_, err := resolveRecyclePath("/etc", ".recycle", "alice", "passwd")
	if err == nil {
		t.Error("share path outside /data should be rejected")
	}
	if err != nil && !strings.Contains(err.Error(), "/data") && !strings.Contains(err.Error(), "非法") && !strings.Contains(err.Error(), "越界") {
		t.Logf("error message: %v", err)
	}
}
