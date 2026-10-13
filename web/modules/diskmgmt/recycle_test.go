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

// D1 修复:resolveRecyclePath 以池根（伪共享的 value）为 sharePath 时，
// 穿越/非法参数同样全拒——该函数本就只认 sharePath 字符串，对伪共享
// 调用方式零差异，此处用 /data/nas1 这个池根路径验证既有表驱动模式扩展成立。
func TestResolveRecyclePathRejectsTraversalWithPoolRootAsSharePath(t *testing.T) {
	poolRoot := "/data/nas1"

	got, err := resolveRecyclePath(poolRoot, ".recycle", "fm", "public/sub/x.txt")
	if err != nil || got != "/data/nas1/.recycle/fm/public/sub/x.txt" {
		t.Errorf("pool root keeptree: got %q err %v", got, err)
	}

	bad := []struct{ user, rel string }{
		{"fm", "../secret"},
		{"fm", "a/../../secret"},
		{"fm", "/etc/passwd"},
		{"fm", ""},
		{"../fm", "x.txt"},
		{"fm/x", "x.txt"},
	}
	for _, b := range bad {
		if _, err := resolveRecyclePath(poolRoot, ".recycle", b.user, b.rel); err == nil {
			t.Errorf("resolveRecyclePath(poolRoot, %q, %q) should fail", b.user, b.rel)
		}
	}
}

func metaWithPool(name, path, pool string, recycleBin bool) FolderMeta {
	return FolderMeta{Name: name, Path: path, Pool: pool, RecycleBin: recycleBin}
}

// D1 修复:buildRecycleRoots 纳入池根伪共享，使默认 recycle_bin=0 共享回退池根
// .recycle 的删除在回收站页可见。
func TestBuildRecycleRootsIncludesRealShares(t *testing.T) {
	metas := []FolderMeta{
		metaWithPool("movies", "/data/nas1/movies", "/data/nas1", true),
	}
	roots := buildRecycleRoots(metas)
	if roots["movies"] != "/data/nas1/movies" {
		t.Errorf("RecycleBin=true 共享应在列, got %v", roots)
	}
}

func TestBuildRecycleRootsExcludesRecycleBinFalseShare(t *testing.T) {
	metas := []FolderMeta{
		metaWithPool("photos", "/data/nas1/photos", "/data/nas1", false),
	}
	roots := buildRecycleRoots(metas)
	if _, ok := roots["photos"]; ok {
		t.Errorf("RecycleBin=false 共享不应在列, got %v", roots)
	}
}

func TestBuildRecycleRootsInjectsPseudoPoolRootEntry(t *testing.T) {
	metas := []FolderMeta{
		metaWithPool("photos", "/data/nas1/photos", "/data/nas1", false),
	}
	roots := buildRecycleRoots(metas)
	key := "(池根 nas1)"
	if roots[key] != "/data/nas1" {
		t.Errorf("应注入池根伪共享 %q=/data/nas1, got %v", key, roots)
	}
}

func TestBuildRecycleRootsRealShareTakesPriorityOverPseudo(t *testing.T) {
	// 真实共享恰好同名「(池根 nas1)」时，真实共享的 path 优先，不被伪条目覆盖。
	metas := []FolderMeta{
		metaWithPool("(池根 nas1)", "/data/nas1/special", "/data/nas1", true),
	}
	roots := buildRecycleRoots(metas)
	if roots["(池根 nas1)"] != "/data/nas1/special" {
		t.Errorf("真实共享应优先于伪条目, got %v", roots)
	}
}

func TestBuildRecycleRootsDistinctPoolsEachGetOneEntry(t *testing.T) {
	metas := []FolderMeta{
		metaWithPool("movies", "/data/nas1/movies", "/data/nas1", true),
		metaWithPool("photos", "/data/nas1/photos", "/data/nas1", false),
		metaWithPool("docs", "/data/nas2/docs", "/data/nas2", false),
	}
	roots := buildRecycleRoots(metas)
	if roots["(池根 nas1)"] != "/data/nas1" {
		t.Errorf("nas1 伪条目缺失或错误, got %v", roots)
	}
	if roots["(池根 nas2)"] != "/data/nas2" {
		t.Errorf("nas2 伪条目缺失或错误, got %v", roots)
	}
	if roots["movies"] != "/data/nas1/movies" {
		t.Errorf("真实共享 movies 应保留, got %v", roots)
	}
	// nas1 只应有一条伪条目，不因两个 FolderMeta 同池而重复/冲突
	count := 0
	for k := range roots {
		if k == "(池根 nas1)" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("nas1 伪条目应恰好一条, got count=%d map=%v", count, roots)
	}
}

func TestPoolRootForPseudoShareHit(t *testing.T) {
	metas := []FolderMeta{
		metaWithPool("movies", "/data/nas1/movies", "/data/nas1", true),
	}
	path, ok := poolRootForPseudoShare("(池根 nas1)", metas)
	if !ok || path != "/data/nas1" {
		t.Errorf("应命中池根伪共享, got path=%q ok=%v", path, ok)
	}
}

func TestPoolRootForPseudoShareMiss(t *testing.T) {
	metas := []FolderMeta{
		metaWithPool("movies", "/data/nas1/movies", "/data/nas1", true),
	}
	if _, ok := poolRootForPseudoShare("movies", metas); ok {
		t.Error("真实共享名不应被 poolRootForPseudoShare 命中")
	}
	if _, ok := poolRootForPseudoShare("(池根 nas2)", metas); ok {
		t.Error("不存在的池不应命中")
	}
	if _, ok := poolRootForPseudoShare("", metas); ok {
		t.Error("空字符串不应命中")
	}
}

// 伪名带非法字符（如 "../x"）本就不会等于任何合法生成的伪名字符串，
// 自然走未命中分支，交由原 findShareMetaPath 处理（400 拒绝）。
func TestPoolRootForPseudoShareMalformedPseudoNameFallsThroughToOriginalPath(t *testing.T) {
	metas := []FolderMeta{
		metaWithPool("movies", "/data/nas1/movies", "/data/nas1", true),
	}
	if _, ok := poolRootForPseudoShare("(池根 ../nas1)", metas); ok {
		t.Error("非法伪名不应命中,应走原 findShareMetaPath 拒绝路径")
	}
	if _, ok := poolRootForPseudoShare("(池根 nas1", metas); ok {
		t.Error("格式不完整的伪名不应命中")
	}
}
