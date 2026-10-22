package filemgmt

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"nas-panel/common"
	"nas-panel/modules/diskmgmt"
)

// F1 Task 3 Step 1：export_test.go 失败测试（抽取前先红，抽取后变绿）。
// t.TempDir 做池根 override 复用既有 setupFakePool/withPublicFolderMeta
// 测试模式（path_test.go / mutate_test.go），不新增测试基建。

func TestResolvePoolPathExported(t *testing.T) {
	root := setupFakePool(t)
	got, err := ResolvePoolPath("public")
	if err != nil {
		t.Fatalf("ResolvePoolPath(public) 应合法, got err=%v", err)
	}
	want := filepath.Clean(filepath.Join(root, "public"))
	if got != want {
		t.Fatalf("ResolvePoolPath(public) = %q, want %q", got, want)
	}
}

func TestResolvePoolPathExportedRejectsTraversal(t *testing.T) {
	setupFakePool(t)
	if _, err := ResolvePoolPath("../etc"); err == nil {
		t.Fatalf("ResolvePoolPath(../etc) 应拒绝")
	}
}

// 正常文件移动→落 .recycle/<actor>/<相对路径>、原路径消失。
func TestMoveToRecycleMovesFileUnderActorDir(t *testing.T) {
	root := setupFakePool(t)
	withPublicFolderMeta(t, root)

	src := filepath.Join(root, "public", "a.jpg")
	os.WriteFile(src, []byte("hello"), 0644)

	got, err := MoveToRecycle(src, "")
	if err != nil {
		t.Fatalf("MoveToRecycle: %v", err)
	}
	if _, err := os.Lstat(src); err == nil {
		t.Fatalf("源应消失")
	}
	admin := common.GetNASUser()
	want := filepath.Join(root, "public", recycleDirName, admin, "a.jpg")
	if got != want {
		t.Fatalf("返回路径 = %q, want %q", got, want)
	}
	data, err := os.ReadFile(want)
	if err != nil || string(data) != "hello" {
		t.Fatalf("内容应保留, data=%q err=%v", data, err)
	}
}

// 同名冲突→recycleConflictFree 后缀。
func TestMoveToRecycleConflictAppendsSuffix(t *testing.T) {
	root := setupFakePool(t)
	withPublicFolderMeta(t, root)

	admin := common.GetNASUser()
	conflict := filepath.Join(root, "public", recycleDirName, admin, "a.jpg")
	os.MkdirAll(filepath.Dir(conflict), 0700)
	os.WriteFile(conflict, []byte("existing"), 0644)

	src := filepath.Join(root, "public", "a.jpg")
	os.WriteFile(src, []byte("new"), 0644)

	got, err := MoveToRecycle(src, "")
	if err != nil {
		t.Fatalf("MoveToRecycle: %v", err)
	}
	if got == conflict {
		t.Fatalf("应生成带后缀的新路径而非覆盖冲突项, got=%q", got)
	}
	data, err := os.ReadFile(conflict)
	if err != nil || string(data) != "existing" {
		t.Fatalf("原冲突文件应未动, data=%q err=%v", data, err)
	}
	newData, err := os.ReadFile(got)
	if err != nil || string(newData) != "new" {
		t.Fatalf("新文件内容应保留, data=%q err=%v", newData, err)
	}
}

// `../` 穿越拒绝（走调用方约定路径：MoveToRecycle 自己不做 ResolvePoolPath，
// 这里断言调用方必须先 ResolvePoolPath 失败，不会把非法相对路径带进内核）。
func TestResolvePoolPathExportedRejectsTraversalBeforeMoveToRecycle(t *testing.T) {
	setupFakePool(t)
	if _, err := ResolvePoolPath("../../etc/passwd"); err == nil {
		t.Fatalf("穿越路径应被 ResolvePoolPath 拒绝,不应进入 MoveToRecycle")
	}
}

// 指向 .recycle 的符号链接拒绝。
func TestMoveToRecycleRejectsSymlinkIntoRecycle(t *testing.T) {
	root := setupFakePool(t)
	withPublicFolderMeta(t, root)
	admin := common.GetNASUser()
	recycle := filepath.Join(root, "public", recycleDirName, admin)
	os.MkdirAll(recycle, 0700)
	v := filepath.Join(recycle, "v.txt")
	os.WriteFile(v, []byte("recycled"), 0644)
	hole := filepath.Join(root, "public", "hole")
	os.Symlink(filepath.Join(root, "public", recycleDirName), hole)

	src := filepath.Join(hole, admin, "v.txt")
	_, err := MoveToRecycle(src, "")
	if !errors.Is(err, ErrIsRecycleContent) {
		t.Fatalf("经符号链接指向回收站应返回 ErrIsRecycleContent, got %v", err)
	}
	if _, statErr := os.Stat(v); statErr != nil {
		t.Fatalf("回收站内文件应原样未动: %v", statErr)
	}
}

// 字面路径含 .recycle 段同样拒绝。
func TestMoveToRecycleRejectsLiteralRecycleSegment(t *testing.T) {
	root := setupFakePool(t)
	withPublicFolderMeta(t, root)
	admin := common.GetNASUser()
	victim := filepath.Join(root, "public", recycleDirName, admin, "victim.txt")
	os.MkdirAll(filepath.Dir(victim), 0700)
	os.WriteFile(victim, []byte("recycled"), 0644)

	_, err := MoveToRecycle(victim, "")
	if !errors.Is(err, ErrIsRecycleContent) {
		t.Fatalf("字面 .recycle 段应返回 ErrIsRecycleContent, got %v", err)
	}
}

// 池根本身拒绝。
func TestMoveToRecycleRejectsPoolRoot(t *testing.T) {
	root := setupFakePool(t)
	_, err := MoveToRecycle(filepath.Clean(root), "")
	if !errors.Is(err, ErrNotInPool) {
		t.Fatalf("池根应返回 ErrNotInPool, got %v", err)
	}
}

// 共享根拒绝（folders.db fixture：withPublicFolderMeta 命中 public 为共享根）。
func TestMoveToRecycleRejectsShareRoot(t *testing.T) {
	root := setupFakePool(t)
	withPublicFolderMeta(t, root)
	_, err := MoveToRecycle(filepath.Join(root, "public"), "")
	if !errors.Is(err, ErrIsShareRoot) {
		t.Fatalf("共享根应返回 ErrIsShareRoot, got %v", err)
	}
}

// 源不存在 → ErrNotFound。
func TestMoveToRecycleRejectsMissingSource(t *testing.T) {
	root := setupFakePool(t)
	withPublicFolderMeta(t, root)
	_, err := MoveToRecycle(filepath.Join(root, "public", "missing.jpg"), "")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("源不存在应返回 ErrNotFound, got %v", err)
	}
}

// actor 含 "/" → ErrBadActor。
func TestMoveToRecycleRejectsBadActor(t *testing.T) {
	root := setupFakePool(t)
	withPublicFolderMeta(t, root)
	src := filepath.Join(root, "public", "a.jpg")
	os.WriteFile(src, []byte("x"), 0644)

	_, err := MoveToRecycle(src, "evil/actor")
	if !errors.Is(err, ErrBadActor) {
		t.Fatalf("actor 含 / 应返回 ErrBadActor, got %v", err)
	}
	if _, statErr := os.Lstat(src); statErr != nil {
		t.Fatalf("校验失败不应移动源文件: %v", statErr)
	}
}

// RecycleBin=false 的共享回退池根 .recycle（folders.db fixture：直接测，
// 复用 mutate_test.go 既有同构场景的注入模式，确认内核层行为与 handler
// 层一致）。
func TestMoveToRecycleFallsBackToPoolRootWhenShareRecycleBinDisabled(t *testing.T) {
	root := setupFakePool(t)
	old := getFolderMetaFn
	getFolderMetaFn = func() []diskmgmt.FolderMeta {
		return []diskmgmt.FolderMeta{
			{Name: "photos", Path: filepath.Join(root, "public", "photos"), RecycleBin: false},
		}
	}
	t.Cleanup(func() { getFolderMetaFn = old })

	src := filepath.Join(root, "public", "photos", "x.jpg")
	os.WriteFile(src, []byte("data"), 0644)

	got, err := MoveToRecycle(src, "")
	if err != nil {
		t.Fatalf("MoveToRecycle: %v", err)
	}
	want := filepath.Join(root, recycleDirName, common.GetNASUser(), "public", "photos", "x.jpg")
	if got != want {
		t.Fatalf("got=%q want=%q", got, want)
	}
}
