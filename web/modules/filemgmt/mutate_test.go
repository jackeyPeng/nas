package filemgmt

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"nas-panel/common"
	"nas-panel/modules/diskmgmt"
)

func TestHandleMkdirSuccess(t *testing.T) {
	root := setupFakePool(t)
	body, _ := json.Marshal(map[string]string{"path": "public", "name": "newdir"})
	req := httptest.NewRequest("POST", "/api/files/mkdir", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleMkdir(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mkdir 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	info, err := os.Stat(filepath.Join(root, "public", "newdir"))
	if err != nil {
		t.Fatalf("目录未建出: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("newdir 不是目录")
	}
	if info.Mode().Perm() != 0755 {
		t.Fatalf("mode=%o want 0755", info.Mode().Perm())
	}
}

func TestHandleMkdirRejectsSlashInName(t *testing.T) {
	setupFakePool(t)
	body, _ := json.Marshal(map[string]string{"path": "public", "name": "a/b"})
	req := httptest.NewRequest("POST", "/api/files/mkdir", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleMkdir(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("名称含 / 应 400, got %d", rec.Code)
	}
}

func TestHandleMkdirRejectsDotDotInName(t *testing.T) {
	setupFakePool(t)
	body, _ := json.Marshal(map[string]string{"path": "public", "name": ".."})
	req := httptest.NewRequest("POST", "/api/files/mkdir", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleMkdir(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("名称为 .. 应 400, got %d", rec.Code)
	}
}

func TestHandleMkdirConflict(t *testing.T) {
	root := setupFakePool(t)
	os.MkdirAll(filepath.Join(root, "public", "exists"), 0755)
	body, _ := json.Marshal(map[string]string{"path": "public", "name": "exists"})
	req := httptest.NewRequest("POST", "/api/files/mkdir", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleMkdir(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("已存在同名应 409, got %d", rec.Code)
	}
}

func TestHandleMkdirRejectsGet(t *testing.T) {
	setupFakePool(t)
	req := httptest.NewRequest("GET", "/api/files/mkdir", nil)
	rec := httptest.NewRecorder()
	handleMkdir(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应 405, got %d", rec.Code)
	}
}

func TestHandleRenameSuccess(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "old.txt"), []byte("content"), 0644)
	body, _ := json.Marshal(map[string]string{"path": "public/old.txt", "new_name": "new.txt"})
	req := httptest.NewRequest("POST", "/api/files/rename", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleRename(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "public", "old.txt")); err == nil {
		t.Fatalf("旧名应消失")
	}
	data, err := os.ReadFile(filepath.Join(root, "public", "new.txt"))
	if err != nil {
		t.Fatalf("新名应存在: %v", err)
	}
	if string(data) != "content" {
		t.Fatalf("内容应保留, got %q", data)
	}
}

func TestHandleRenameConflict(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "src.txt"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(root, "public", "dst.txt"), []byte("y"), 0644)
	body, _ := json.Marshal(map[string]string{"path": "public/src.txt", "new_name": "dst.txt"})
	req := httptest.NewRequest("POST", "/api/files/rename", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleRename(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("目标已存在应 409, got %d", rec.Code)
	}
}

func TestHandleRenameRejectsDotDotNewName(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "src.txt"), []byte("x"), 0644)
	body, _ := json.Marshal(map[string]string{"path": "public/src.txt", "new_name": ".."})
	req := httptest.NewRequest("POST", "/api/files/rename", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleRename(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("new_name=.. 应 400, got %d", rec.Code)
	}
}

func TestHandleRenameSourceNotFound(t *testing.T) {
	setupFakePool(t)
	body, _ := json.Marshal(map[string]string{"path": "public/missing.txt", "new_name": "x.txt"})
	req := httptest.NewRequest("POST", "/api/files/rename", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleRename(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("源不存在应 404, got %d", rec.Code)
	}
}

func TestHandleRenameRejectsPoolRootEmptyPath(t *testing.T) {
	setupFakePool(t)
	body, _ := json.Marshal(map[string]string{"path": "", "new_name": "x"})
	req := httptest.NewRequest("POST", "/api/files/rename", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleRename(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("path=\"\" (池根) 应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleRenameRejectsPoolRootDotPath(t *testing.T) {
	setupFakePool(t)
	body, _ := json.Marshal(map[string]string{"path": ".", "new_name": "x"})
	req := httptest.NewRequest("POST", "/api/files/rename", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleRename(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("path=\".\" (池根) 应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleRenameSameNameNoOp(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "same.txt"), []byte("content"), 0644)
	body, _ := json.Marshal(map[string]string{"path": "public/same.txt", "new_name": "same.txt"})
	req := httptest.NewRequest("POST", "/api/files/rename", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleRename(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("同名 rename 应 200 no-op, got %d body=%s", rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "public", "same.txt"))
	if err != nil {
		t.Fatalf("文件应仍在: %v", err)
	}
	if string(data) != "content" {
		t.Fatalf("内容应不变, got %q", data)
	}
}

func TestHandleRenameRejectsGet(t *testing.T) {
	setupFakePool(t)
	req := httptest.NewRequest("GET", "/api/files/rename", nil)
	rec := httptest.NewRecorder()
	handleRename(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应 405, got %d", rec.Code)
	}
}

func TestValidateFileName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"空串", "", true},
		{"含斜杠", "a/b", true},
		{"含两个点子串前缀", "..foo", true},
		{"含两个点子串中间", "foo..bar", true},
		{"含两个点子串后缀", "foo..", true},
		{"控制字符", "foo\x00bar", true},
		{"控制字符tab", "foo\tbar", true},
		{"256字节", strings.Repeat("x", 256), true},
		{"255字节合法", strings.Repeat("x", 255), false},
		{"全名为点", ".", true},
		{"全名为两个点", "..", true},
		{"合法中文", "照片目录", false},
		{"合法含空格", "my photo", false},
		{"合法普通名", "newdir", false},
	}
	for _, c := range cases {
		err := validateFileName(c.input)
		if c.wantErr && err == nil {
			t.Errorf("%s: validateFileName(%q) 应返回错误, got nil", c.name, c.input)
		}
		if !c.wantErr && err != nil {
			t.Errorf("%s: validateFileName(%q) 应合法, got err=%v", c.name, c.input, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Task 9: move + copy
// ---------------------------------------------------------------------------

// Test 1: 同 fs move = rename——源消失、目标在、内容一致、mode 保留。
func TestHandleMoveSameFSActsLikeRename(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "photos", "src.txt"), []byte("move me"), 0640)
	body, _ := json.Marshal(map[string]string{"src": "public/photos/src.txt", "dst_dir": "fm"})
	req := httptest.NewRequest("POST", "/api/files/move", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleMove(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("move 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Lstat(filepath.Join(root, "public", "photos", "src.txt")); err == nil {
		t.Fatalf("源应已消失")
	}
	info, err := os.Stat(filepath.Join(root, "fm", "src.txt"))
	if err != nil {
		t.Fatalf("目标应存在: %v", err)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("mode 应保留 0640, got %o", info.Mode().Perm())
	}
	data, _ := os.ReadFile(filepath.Join(root, "fm", "src.txt"))
	if string(data) != "move me" {
		t.Fatalf("内容应一致, got %q", data)
	}
}

// Test 2: move 目标已存在同名 → 409,源和目标都原样未动。
func TestHandleMoveConflictLeavesBothUntouched(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "src.txt"), []byte("source"), 0644)
	os.WriteFile(filepath.Join(root, "fm", "src.txt"), []byte("already here"), 0644)
	body, _ := json.Marshal(map[string]string{"src": "public/src.txt", "dst_dir": "fm"})
	req := httptest.NewRequest("POST", "/api/files/move", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleMove(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("目标已存在应 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	srcData, err := os.ReadFile(filepath.Join(root, "public", "src.txt"))
	if err != nil || string(srcData) != "source" {
		t.Fatalf("源应原样未动, err=%v data=%q", err, srcData)
	}
	dstData, err := os.ReadFile(filepath.Join(root, "fm", "src.txt"))
	if err != nil || string(dstData) != "already here" {
		t.Fatalf("目标应原样未动, err=%v data=%q", err, dstData)
	}
}

// Test 3: copy 后源仍在、目标内容一致。
func TestHandleCopyKeepsSourceAndMatchesContent(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "src.txt"), []byte("copy me"), 0644)
	body, _ := json.Marshal(map[string]string{"src": "public/src.txt", "dst_dir": "fm"})
	req := httptest.NewRequest("POST", "/api/files/copy", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleCopy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("copy 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	srcData, err := os.ReadFile(filepath.Join(root, "public", "src.txt"))
	if err != nil || string(srcData) != "copy me" {
		t.Fatalf("源应仍在, err=%v data=%q", err, srcData)
	}
	dstData, err := os.ReadFile(filepath.Join(root, "fm", "src.txt"))
	if err != nil || string(dstData) != "copy me" {
		t.Fatalf("目标内容应一致, err=%v data=%q", err, dstData)
	}
}

// Test 4: copy 目录递归——多层目录 + 文件内容逐一一致;目录 mode 保留。
func TestHandleCopyDirRecursive(t *testing.T) {
	root := setupFakePool(t)
	srcDir := filepath.Join(root, "public", "tree")
	os.MkdirAll(filepath.Join(srcDir, "sub"), 0750)
	os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("aaa"), 0644)
	os.WriteFile(filepath.Join(srcDir, "sub", "b.txt"), []byte("bbb"), 0644)

	body, _ := json.Marshal(map[string]string{"src": "public/tree", "dst_dir": "fm"})
	req := httptest.NewRequest("POST", "/api/files/copy", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleCopy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("copy 目录应 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	dstDir := filepath.Join(root, "fm", "tree")
	info, err := os.Stat(filepath.Join(dstDir, "sub"))
	if err != nil {
		t.Fatalf("子目录未复制: %v", err)
	}
	if info.Mode().Perm() != 0750 {
		t.Fatalf("子目录 mode 应保留 0750, got %o", info.Mode().Perm())
	}
	a, err := os.ReadFile(filepath.Join(dstDir, "a.txt"))
	if err != nil || string(a) != "aaa" {
		t.Fatalf("a.txt 内容不符, err=%v data=%q", err, a)
	}
	b, err := os.ReadFile(filepath.Join(dstDir, "sub", "b.txt"))
	if err != nil || string(b) != "bbb" {
		t.Fatalf("sub/b.txt 内容不符, err=%v data=%q", err, b)
	}
	// 源仍完整存在
	if _, err := os.Stat(filepath.Join(srcDir, "sub", "b.txt")); err != nil {
		t.Fatalf("源树应仍完整: %v", err)
	}
}

// Test 5: move/copy 目标目录不存在 → 404;dst_dir 指向文件 → 400。
func TestHandleMoveDstDirNotFound(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "src.txt"), []byte("x"), 0644)
	body, _ := json.Marshal(map[string]string{"src": "public/src.txt", "dst_dir": "nope"})
	req := httptest.NewRequest("POST", "/api/files/move", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleMove(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("dst_dir 不存在应 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleCopyDstDirNotFound(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "src.txt"), []byte("x"), 0644)
	body, _ := json.Marshal(map[string]string{"src": "public/src.txt", "dst_dir": "nope"})
	req := httptest.NewRequest("POST", "/api/files/copy", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleCopy(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("dst_dir 不存在应 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleMoveDstDirIsFile(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "src.txt"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(root, "public", "notadir.txt"), []byte("y"), 0644)
	body, _ := json.Marshal(map[string]string{"src": "public/src.txt", "dst_dir": "public/notadir.txt"})
	req := httptest.NewRequest("POST", "/api/files/move", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleMove(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("dst_dir 是文件应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleCopyDstDirIsFile(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "src.txt"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(root, "public", "notadir.txt"), []byte("y"), 0644)
	body, _ := json.Marshal(map[string]string{"src": "public/src.txt", "dst_dir": "public/notadir.txt"})
	req := httptest.NewRequest("POST", "/api/files/copy", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleCopy(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("dst_dir 是文件应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// Test 6: src 穿越 → 400;src == 池根("" 与 ".") → 400。
func TestHandleMoveRejectsSrcTraversal(t *testing.T) {
	setupFakePool(t)
	body, _ := json.Marshal(map[string]string{"src": "../../etc", "dst_dir": "fm"})
	req := httptest.NewRequest("POST", "/api/files/move", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleMove(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("src 穿越应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleCopyRejectsSrcTraversal(t *testing.T) {
	setupFakePool(t)
	body, _ := json.Marshal(map[string]string{"src": "../../etc", "dst_dir": "fm"})
	req := httptest.NewRequest("POST", "/api/files/copy", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleCopy(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("src 穿越应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleMoveRejectsSrcPoolRootEmpty(t *testing.T) {
	setupFakePool(t)
	body, _ := json.Marshal(map[string]string{"src": "", "dst_dir": "fm"})
	req := httptest.NewRequest("POST", "/api/files/move", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleMove(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("src=\"\" (池根) 应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleMoveRejectsSrcPoolRootDot(t *testing.T) {
	setupFakePool(t)
	body, _ := json.Marshal(map[string]string{"src": ".", "dst_dir": "fm"})
	req := httptest.NewRequest("POST", "/api/files/move", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleMove(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("src=\".\" (池根) 应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleCopyRejectsSrcPoolRootEmpty(t *testing.T) {
	setupFakePool(t)
	body, _ := json.Marshal(map[string]string{"src": "", "dst_dir": "fm"})
	req := httptest.NewRequest("POST", "/api/files/copy", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleCopy(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("src=\"\" (池根) 应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleCopyRejectsSrcPoolRootDot(t *testing.T) {
	setupFakePool(t)
	body, _ := json.Marshal(map[string]string{"src": ".", "dst_dir": "fm"})
	req := httptest.NewRequest("POST", "/api/files/copy", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleCopy(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("src=\".\" (池根) 应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// Test 7: copyTree 遇符号链接不跟随——目标处是链接(os.Readlink 值一致)不是内容。
func TestCopyTreeSymlinkNotFollowed(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	os.MkdirAll(src, 0755)
	os.WriteFile(filepath.Join(src, "real.txt"), []byte("real content"), 0644)
	if err := os.Symlink("real.txt", filepath.Join(src, "link.txt")); err != nil {
		t.Fatalf("建符号链接失败: %v", err)
	}

	n, err := copyTree(src, dst)
	if err != nil {
		t.Fatalf("copyTree err=%v", err)
	}
	// 只有 real.txt 的字节数被计入,符号链接不计字节。
	if n != int64(len("real content")) {
		t.Fatalf("copyTree 返回字节数=%d, want %d", n, len("real content"))
	}
	info, err := os.Lstat(filepath.Join(dst, "link.txt"))
	if err != nil {
		t.Fatalf("目标处链接未创建: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("目标处应是符号链接,而非普通文件(不应跟随拷内容)")
	}
	target, err := os.Readlink(filepath.Join(dst, "link.txt"))
	if err != nil {
		t.Fatalf("Readlink 失败: %v", err)
	}
	if target != "real.txt" {
		t.Fatalf("符号链接目标应一致, got %q want %q", target, "real.txt")
	}
}

// Test 8: sameFS——同目录内两路径 true。
// 跨 fs 分支说明(拍板允许的降级):测试环境(容器/CI)通常只有单一挂载点可写,
// 无法可靠构造"真实跨文件系统"的第二个路径(/proc 是特殊文件系统但不可比较
// 常规文件 Stat_t.Dev 语义,bind mount 需要特权);按 brief 第 8 条拍板,
// 本轮只测 true 分支,跨 fs(Dev 不同)分支的正确性由 sameFS 实现本身的
// Stat_t.Dev 直接比较逻辑保证(单行比较,审查即可确认正确性),同构机 M2
// 收尾轮用真实多盘环境做手工验收覆盖。
func TestSameFSWithinSameDirectory(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "sub", "b")
	os.MkdirAll(filepath.Dir(b), 0755)
	os.WriteFile(a, []byte("x"), 0644)
	os.WriteFile(b, []byte("y"), 0644)
	if !sameFS(a, b) {
		t.Fatalf("同一文件系统内两路径 sameFS 应为 true")
	}
}

// sameFS 对 stat 失败的路径保守返回 false(走 copy 路径,不冒险当作同 fs rename)。
func TestSameFSStatFailureReturnsFalse(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	os.WriteFile(real, []byte("x"), 0644)
	missing := filepath.Join(root, "does-not-exist")
	if sameFS(real, missing) {
		t.Fatalf("stat 失败的路径应保守返回 false")
	}
}

// Test 9: moveCrossFS 字节校验——正常路径(内容一致 → 源被删、目标完整)。
func TestMoveCrossFSNormalPath(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	os.MkdirAll(src, 0755)
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0644)
	os.MkdirAll(filepath.Join(src, "sub"), 0755)
	os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("world"), 0644)

	if err := moveCrossFS(src, dst); err != nil {
		t.Fatalf("moveCrossFS err=%v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("源应已被删除, stat err=%v", err)
	}
	a, err := os.ReadFile(filepath.Join(dst, "a.txt"))
	if err != nil || string(a) != "hello" {
		t.Fatalf("a.txt 应完整复制, err=%v data=%q", err, a)
	}
	b, err := os.ReadFile(filepath.Join(dst, "sub", "b.txt"))
	if err != nil || string(b) != "world" {
		t.Fatalf("sub/b.txt 应完整复制, err=%v data=%q", err, b)
	}
}

// moveCrossFS 在 copyTree 阶段失败时(目标父目录不存在导致 MkdirAll 失败场景以外的
// 真实失败不易在无特权环境构造,这里用"源不存在"制造 copyTree 必然失败的输入)
// 必须返回错误且不触碰(不存在的)源 —— 验证"失败时绝不删源"的编排语义。
func TestMoveCrossFSCopyTreeFailureKeepsSource(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src") // 不存在的源,copyTree 必然失败
	dst := filepath.Join(root, "dst")

	if err := moveCrossFS(src, dst); err == nil {
		t.Fatalf("copyTree 失败时 moveCrossFS 应返回错误")
	}
	// "源保留" 对不存在的源而言即保持不存在、不被意外创建;
	// 核心断言是 RemoveAll 分支绝不会被触发(err != nil 已覆盖该路径未执行到 RemoveAll)。
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("源状态应保持原样(不存在), got err=%v", err)
	}
}

// Test 10: GET /api/files/move、/api/files/copy → 405。
func TestHandleMoveRejectsGet(t *testing.T) {
	setupFakePool(t)
	req := httptest.NewRequest("GET", "/api/files/move", nil)
	rec := httptest.NewRecorder()
	handleMove(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应 405, got %d", rec.Code)
	}
}

func TestHandleCopyRejectsGet(t *testing.T) {
	setupFakePool(t)
	req := httptest.NewRequest("GET", "/api/files/copy", nil)
	rec := httptest.NewRecorder()
	handleCopy(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应 405, got %d", rec.Code)
	}
}

// Test 11: copy 落盘文件走 alignNewFilePerms——注入 fake getFolderMetaFn 断言 mode 对齐。
func TestHandleCopyAlignsPermsWithPublicFolderMeta(t *testing.T) {
	root := setupFakePool(t)
	old := getFolderMetaFn
	getFolderMetaFn = func() []diskmgmt.FolderMeta {
		return []diskmgmt.FolderMeta{{Name: "public", Path: filepath.Join(root, "public")}}
	}
	t.Cleanup(func() { getFolderMetaFn = old })

	os.WriteFile(filepath.Join(root, "fm", "src.txt"), []byte("x"), 0600)
	body, _ := json.Marshal(map[string]string{"src": "fm/src.txt", "dst_dir": "public"})
	req := httptest.NewRequest("POST", "/api/files/copy", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleCopy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("copy 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	target := filepath.Join(root, "public", "src.txt")
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("目标未落盘: %v", err)
	}
	if info.Mode().Perm() != 0664 {
		t.Fatalf("public 共享对齐后 mode 应为 0664, got %o", info.Mode().Perm())
	}

	if os.Geteuid() == 0 {
		return // root-gated chown 断言由 upload_test.go 的同款模式覆盖,这里只验证 mode 对齐链路接通
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("无法取 syscall.Stat_t")
	}
	if int(stat.Uid) != os.Geteuid() {
		t.Fatalf("非 root 环境 chown 应被跳过, got uid=%d want %d", stat.Uid, os.Geteuid())
	}
}

// Test 12: 移动到自己(src==dst 完整路径) → 400。
func TestHandleMoveToSelfRejected(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "src.txt"), []byte("x"), 0644)
	body, _ := json.Marshal(map[string]string{"src": "public/src.txt", "dst_dir": "public"})
	req := httptest.NewRequest("POST", "/api/files/move", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleMove(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("移动到自己应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "public", "src.txt")); err != nil {
		t.Fatalf("源应仍在: %v", err)
	}
}

func TestHandleCopyToSelfRejected(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "src.txt"), []byte("x"), 0644)
	body, _ := json.Marshal(map[string]string{"src": "public/src.txt", "dst_dir": "public"})
	req := httptest.NewRequest("POST", "/api/files/copy", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleCopy(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("复制到自己应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Task 9 review C1: 复制/移动到 src 自身子目录 → 400(防无限递归写满磁盘)。
// ---------------------------------------------------------------------------

// copy dst_dir=a/b、src=a(dst 在 src 树内)必须被拒 400,且 a/b 内无新增内容
// (证明 copyTree 从未被调用,不是跑了一半才失败)。
func TestHandleCopyRejectsDstInsideSrc(t *testing.T) {
	root := setupFakePool(t)
	srcDir := filepath.Join(root, "public", "a")
	subDir := filepath.Join(srcDir, "b")
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(srcDir, "file.txt"), []byte("x"), 0644)

	body, _ := json.Marshal(map[string]string{"src": "public/a", "dst_dir": "public/a/b"})
	req := httptest.NewRequest("POST", "/api/files/copy", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleCopy(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("copy 到 src 子目录应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	entries, err := os.ReadDir(subDir)
	if err != nil {
		t.Fatalf("子目录应仍在: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("子目录内应无新增内容(拒绝应发生在 copyTree 之前), got %v", entries)
	}
}

// move dst_dir=a/b、src=a 同样必须被拒 400,源树原样未动。
func TestHandleMoveRejectsDstInsideSrc(t *testing.T) {
	root := setupFakePool(t)
	srcDir := filepath.Join(root, "public", "a")
	subDir := filepath.Join(srcDir, "b")
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(srcDir, "file.txt"), []byte("x"), 0644)

	body, _ := json.Marshal(map[string]string{"src": "public/a", "dst_dir": "public/a/b"})
	req := httptest.NewRequest("POST", "/api/files/move", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleMove(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("move 到 src 子目录应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(srcDir, "file.txt")); err != nil {
		t.Fatalf("源应原样未动: %v", err)
	}
	entries, err := os.ReadDir(subDir)
	if err != nil {
		t.Fatalf("子目录应仍在: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("子目录内应无新增内容, got %v", entries)
	}
}

// dst_dir 恰为 src 自身(非子目录,是同一目录)已由"目标已存在"409 覆盖
// (base 相同会撞 409),这里补一个 dst_dir 深层嵌套于 src 的用例,确认
// HasPrefix 判断对多级子目录同样生效。
func TestHandleCopyRejectsDstDeepInsideSrc(t *testing.T) {
	root := setupFakePool(t)
	srcDir := filepath.Join(root, "public", "a")
	deepDir := filepath.Join(srcDir, "b", "c", "d")
	os.MkdirAll(deepDir, 0755)

	body, _ := json.Marshal(map[string]string{"src": "public/a", "dst_dir": "public/a/b/c/d"})
	req := httptest.NewRequest("POST", "/api/files/copy", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleCopy(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("copy 到 src 深层子目录应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// Task 9 review Important#2:copyTree 撞已存在目标必须返回可用 errors.Is(err, fs.ErrExist)
// 判别的错误(handler 据此 409 且不清理),且预置目标内容原样不动。
func TestCopyTreeExistingTargetReturnsErrExist(t *testing.T) {
	root := setupFakePool(t)
	src := filepath.Join(root, "public", "src.txt")
	os.WriteFile(src, []byte("new"), 0644)
	dst := filepath.Join(root, "public", "dst.txt")
	os.WriteFile(dst, []byte("existing"), 0644)

	_, err := copyTree(src, dst)
	if err == nil {
		t.Fatalf("copyTree 目标已存在应报错")
	}
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("错误应满足 errors.Is(err, fs.ErrExist), got %v", err)
	}
	data, rerr := os.ReadFile(dst)
	if rerr != nil || string(data) != "existing" {
		t.Fatalf("预置目标应原样未动, got %q err=%v", data, rerr)
	}
}

// Task 9 review Important#3:跨 fs move 必须在删源前对新落盘文件做权限对齐
// (alignCopiedTreePerms → alignNewFilePerms,权限跟目标共享)。注入 public meta,
// 断言移动后文件 mode==0664 且源已被删。
func TestMoveCrossFSAlignsPermsBeforeSourceRemoval(t *testing.T) {
	root := setupFakePool(t)
	old := getFolderMetaFn
	getFolderMetaFn = func() []diskmgmt.FolderMeta {
		return []diskmgmt.FolderMeta{{Name: "public", Path: filepath.Join(root, "public")}}
	}
	t.Cleanup(func() { getFolderMetaFn = old })

	srcDir := filepath.Join(root, "fm", "d")
	os.MkdirAll(srcDir, 0700)
	srcFile := filepath.Join(srcDir, "f.txt")
	os.WriteFile(srcFile, []byte("data"), 0600)
	dst := filepath.Join(root, "public", "d")

	if err := moveCrossFS(srcDir, dst); err != nil {
		t.Fatalf("moveCrossFS 应成功: %v", err)
	}
	if _, err := os.Lstat(srcDir); err == nil {
		t.Fatalf("校验通过后源应被删除")
	}
	info, err := os.Stat(filepath.Join(dst, "f.txt"))
	if err != nil {
		t.Fatalf("目标文件应在: %v", err)
	}
	if info.Mode().Perm() != 0664 {
		t.Fatalf("跨 fs move 落盘文件应对齐 public 共享 0664, got %o", info.Mode().Perm())
	}
}

// ---------------------------------------------------------------------------
// Task 10: delete → .recycle(与回收站页 diskmgmt/recycle.go 同构 keeptree 布局)
// ---------------------------------------------------------------------------

// withPublicFolderMeta 注入 fake getFolderMetaFn,返回 public(root/public)与
// fm(root/fm,充当用户 home)两个共享 meta,对照 upload_test.go 注入模式。
func withPublicFolderMeta(t *testing.T, root string) {
	t.Helper()
	old := getFolderMetaFn
	getFolderMetaFn = func() []diskmgmt.FolderMeta {
		return []diskmgmt.FolderMeta{
			{Name: "public", Path: filepath.Join(root, "public"), RecycleBin: true},
			{Name: "fm", Path: filepath.Join(root, "fm"), RecycleBin: true},
		}
	}
	t.Cleanup(func() { getFolderMetaFn = old })
}

// Test 13: 删 public 下文件 → 源消失,public/.recycle/<admin>/a.txt 出现且内容一致。
// 测试环境无 JWT,admin 兜底 common.GetNASUser()。
func TestHandleDeleteMovesToRecycleUnderOwningShare(t *testing.T) {
	root := setupFakePool(t)
	withPublicFolderMeta(t, root)

	srcPath := filepath.Join(root, "public", "a.txt")
	os.WriteFile(srcPath, []byte("hello"), 0644)

	body, _ := json.Marshal(map[string]string{"path": "public/a.txt"})
	req := httptest.NewRequest("POST", "/api/files/delete", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleDelete(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Lstat(srcPath); err == nil {
		t.Fatalf("源应消失")
	}
	admin := common.GetNASUser()
	wantPath := filepath.Join(root, "public", recycleDirName, admin, "a.txt")
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("回收站内应出现 %s: %v", wantPath, err)
	}
	if string(data) != "hello" {
		t.Fatalf("内容应保留, got %q", data)
	}
}

// Test 14: 多级子目录下文件 → keeptree,原相对路径整段保留。
func TestHandleDeleteKeepsTreeForNestedFile(t *testing.T) {
	root := setupFakePool(t)
	withPublicFolderMeta(t, root)

	nestedDir := filepath.Join(root, "public", "sub", "dir")
	os.MkdirAll(nestedDir, 0755)
	srcPath := filepath.Join(nestedDir, "f.txt")
	os.WriteFile(srcPath, []byte("nested"), 0644)

	body, _ := json.Marshal(map[string]string{"path": "public/sub/dir/f.txt"})
	req := httptest.NewRequest("POST", "/api/files/delete", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleDelete(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	admin := common.GetNASUser()
	wantPath := filepath.Join(root, "public", recycleDirName, admin, "sub", "dir", "f.txt")
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("keeptree 回收站内应出现 %s: %v", wantPath, err)
	}
	if string(data) != "nested" {
		t.Fatalf("内容应保留, got %q", data)
	}
}

// Test 15: 删 fm home 下文件 → 落 fm 的 .recycle,不落 public 的。
func TestHandleDeleteUsesOwningShareNotOthers(t *testing.T) {
	root := setupFakePool(t)
	withPublicFolderMeta(t, root)

	srcPath := filepath.Join(root, "fm", "b.txt")
	os.WriteFile(srcPath, []byte("fm-data"), 0600)

	body, _ := json.Marshal(map[string]string{"path": "fm/b.txt"})
	req := httptest.NewRequest("POST", "/api/files/delete", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleDelete(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	admin := common.GetNASUser()
	wantPath := filepath.Join(root, "fm", recycleDirName, admin, "b.txt")
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("应落 fm 的 .recycle: %v", err)
	}
	notWantPath := filepath.Join(root, "public", recycleDirName, admin, "b.txt")
	if _, err := os.Stat(notWantPath); err == nil {
		t.Fatalf("不应落 public 的 .recycle")
	}
}

// Test 16: 删目录 → 整子树随 rename 保留(目录内文件内容一致)。
func TestHandleDeleteDirectoryKeepsSubtree(t *testing.T) {
	root := setupFakePool(t)
	withPublicFolderMeta(t, root)

	srcDir := filepath.Join(root, "public", "folder")
	os.MkdirAll(filepath.Join(srcDir, "inner"), 0755)
	os.WriteFile(filepath.Join(srcDir, "top.txt"), []byte("top"), 0644)
	os.WriteFile(filepath.Join(srcDir, "inner", "deep.txt"), []byte("deep"), 0644)

	body, _ := json.Marshal(map[string]string{"path": "public/folder"})
	req := httptest.NewRequest("POST", "/api/files/delete", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleDelete(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Lstat(srcDir); err == nil {
		t.Fatalf("源目录应消失")
	}
	admin := common.GetNASUser()
	recycled := filepath.Join(root, "public", recycleDirName, admin, "folder")
	data1, err := os.ReadFile(filepath.Join(recycled, "top.txt"))
	if err != nil || string(data1) != "top" {
		t.Fatalf("top.txt 应保留, data=%q err=%v", data1, err)
	}
	data2, err := os.ReadFile(filepath.Join(recycled, "inner", "deep.txt"))
	if err != nil || string(data2) != "deep" {
		t.Fatalf("inner/deep.txt 应保留, data=%q err=%v", data2, err)
	}
}

// Test 17: 池根散落文件(fake meta 返回空列表 → !found)→ 落 <poolRoot>/.recycle/<admin>/<basename>。
func TestHandleDeleteNoOwningShareFallsBackToPoolRootRecycle(t *testing.T) {
	root := setupFakePool(t)
	old := getFolderMetaFn
	getFolderMetaFn = func() []diskmgmt.FolderMeta { return nil }
	t.Cleanup(func() { getFolderMetaFn = old })

	srcPath := filepath.Join(root, "loose.txt")
	os.WriteFile(srcPath, []byte("loose"), 0644)

	body, _ := json.Marshal(map[string]string{"path": "loose.txt"})
	req := httptest.NewRequest("POST", "/api/files/delete", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleDelete(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	admin := common.GetNASUser()
	wantPath := filepath.Join(root, recycleDirName, admin, "loose.txt")
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("应落池根 .recycle: %v", err)
	}
}

// Test 18: 路径穿越 → 400;池根("" 与 ".") → 400。
func TestHandleDeleteRejectsTraversalAndPoolRoot(t *testing.T) {
	root := setupFakePool(t)
	withPublicFolderMeta(t, root)

	for _, p := range []string{"../../etc", "", "."} {
		body, _ := json.Marshal(map[string]string{"path": p})
		req := httptest.NewRequest("POST", "/api/files/delete", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handleDelete(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("path=%q 应 400, got %d body=%s", p, rec.Code, rec.Body.String())
		}
	}
	_ = root
}

// Test 19: .recycle 防套娃 — 删 public/.recycle/<admin>/a.txt → 400 且文件仍在。
func TestHandleDeleteRejectsRecycleItself(t *testing.T) {
	root := setupFakePool(t)
	withPublicFolderMeta(t, root)

	admin := common.GetNASUser()
	recycleFile := filepath.Join(root, "public", recycleDirName, admin, "a.txt")
	os.MkdirAll(filepath.Dir(recycleFile), 0700)
	os.WriteFile(recycleFile, []byte("x"), 0644)

	relPath := filepath.ToSlash(filepath.Join("public", recycleDirName, admin, "a.txt"))
	body, _ := json.Marshal(map[string]string{"path": relPath})
	req := httptest.NewRequest("POST", "/api/files/delete", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleDelete(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf(".recycle 内容应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(recycleFile); err != nil {
		t.Fatalf("文件应仍在: %v", err)
	}
}

// Test 20: 删共享文件夹本身(path=="public") → 400 且目录仍在。
func TestHandleDeleteRejectsShareRootItself(t *testing.T) {
	root := setupFakePool(t)
	withPublicFolderMeta(t, root)

	body, _ := json.Marshal(map[string]string{"path": "public"})
	req := httptest.NewRequest("POST", "/api/files/delete", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleDelete(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("删共享文件夹本身应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "public")); err != nil {
		t.Fatalf("共享目录应仍在: %v", err)
	}
}

// Test 21: 冲突后缀 — 预先在回收站目标位置放同名文件,再删源 → 成功且回收站内
// 出现 a.txt.<数字后缀>,原冲突文件未动。
func TestHandleDeleteConflictAppendsSuffix(t *testing.T) {
	root := setupFakePool(t)
	withPublicFolderMeta(t, root)

	admin := common.GetNASUser()
	conflictPath := filepath.Join(root, "public", recycleDirName, admin, "a.txt")
	os.MkdirAll(filepath.Dir(conflictPath), 0700)
	os.WriteFile(conflictPath, []byte("existing"), 0644)

	srcPath := filepath.Join(root, "public", "a.txt")
	os.WriteFile(srcPath, []byte("new"), 0644)

	body, _ := json.Marshal(map[string]string{"path": "public/a.txt"})
	req := httptest.NewRequest("POST", "/api/files/delete", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleDelete(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	// 原冲突文件未动
	data, err := os.ReadFile(conflictPath)
	if err != nil || string(data) != "existing" {
		t.Fatalf("原冲突文件应未动, data=%q err=%v", data, err)
	}
	// 回收站目录下应多出一个带数字后缀的 a.txt.<n>
	entries, err := os.ReadDir(filepath.Dir(conflictPath))
	if err != nil {
		t.Fatalf("读回收站目录失败: %v", err)
	}
	found := false
	for _, e := range entries {
		name := e.Name()
		if name != "a.txt" && strings.HasPrefix(name, "a.txt.") {
			data, rerr := os.ReadFile(filepath.Join(filepath.Dir(conflictPath), name))
			if rerr == nil && string(data) == "new" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("应出现带后缀的新回收文件, entries=%v", entries)
	}
	if _, err := os.Lstat(srcPath); err == nil {
		t.Fatalf("源应消失")
	}
}

// Test 22: GET → 405。
func TestHandleDeleteRejectsGet(t *testing.T) {
	setupFakePool(t)
	req := httptest.NewRequest("GET", "/api/files/delete", nil)
	rec := httptest.NewRecorder()
	handleDelete(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应 405, got %d", rec.Code)
	}
}

// Task 9 复审 Minor#2:同前缀异目录必须放行——src=public/ab、dst_dir=public/abc
// 时 HasPrefix 拼接分隔符后不应误判为"目标在源内部"。
func TestHandleMoveAllowsSamePrefixDifferentDir(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "ab"), []byte("data"), 0644)
	os.MkdirAll(filepath.Join(root, "public", "abc"), 0755)

	body, _ := json.Marshal(map[string]string{"src": "public/ab", "dst_dir": "public/abc"})
	req := httptest.NewRequest("POST", "/api/files/move", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleMove(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("同前缀异目录应放行 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "public", "abc", "ab")); err != nil {
		t.Fatalf("文件应移入 abc/: %v", err)
	}
}

// Task 9 复审 Important#1:containment 检查须用 EvalSymlinks 真实路径——
// 池内符号链接 dst_dir → src 子树时,字面前缀检查会被绕过导致无限递归,
// 解析后必须 400 拒绝。
func TestHandleCopyRejectsDstInsideSrcViaSymlink(t *testing.T) {
	root := setupFakePool(t)
	srcDir := filepath.Join(root, "public", "a")
	os.MkdirAll(filepath.Join(srcDir, "b"), 0755)
	// 池内链接:public/link → public/a/b(dst_dir 字面不在 src 内,真实路径在)
	os.Symlink(filepath.Join(srcDir, "b"), filepath.Join(root, "public", "link"))

	body, _ := json.Marshal(map[string]string{"src": "public/a", "dst_dir": "public/link"})
	req := httptest.NewRequest("POST", "/api/files/copy", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleCopy(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("经符号链接指向 src 内部的 dst_dir 应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	entries, _ := os.ReadDir(filepath.Join(srcDir, "b"))
	if len(entries) != 0 {
		t.Fatalf("拒绝后 src 子树内不应有新增, got %v", entries)
	}
}

// Task 10 review Important#1(a):前导空格绕过——resolvePoolPath 会 TrimSpace,
// 防套娃守卫必须基于归一后路径判定," .recycle/..." 不得放行。
func TestHandleDeleteRejectsLeadingSpaceRecyclePath(t *testing.T) {
	root := setupFakePool(t)
	withPublicFolderMeta(t, root)
	admin := common.GetNASUser()
	victim := filepath.Join(root, ".recycle", admin, "victim.txt")
	os.MkdirAll(filepath.Dir(victim), 0700)
	os.WriteFile(victim, []byte("recycled"), 0644)

	body, _ := json.Marshal(map[string]string{"path": " .recycle/" + admin + "/victim.txt"})
	req := httptest.NewRequest("POST", "/api/files/delete", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleDelete(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("前导空格 .recycle 路径应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("回收站内容应原样未动: %v", err)
	}
}

// Task 10 review Important#1(b):池内符号链接绕过——public/hole → public/.recycle,
// 删 public/hole/<admin>/v.txt 字面无 .recycle 段,真实落点在回收站内,必须 400。
func TestHandleDeleteRejectsSymlinkIntoRecycle(t *testing.T) {
	root := setupFakePool(t)
	withPublicFolderMeta(t, root)
	admin := common.GetNASUser()
	recycle := filepath.Join(root, "public", ".recycle", admin)
	os.MkdirAll(recycle, 0700)
	v := filepath.Join(recycle, "v.txt")
	os.WriteFile(v, []byte("recycled"), 0644)
	os.Symlink(filepath.Join(root, "public", ".recycle"), filepath.Join(root, "public", "hole"))

	body, _ := json.Marshal(map[string]string{"path": "public/hole/" + admin + "/v.txt"})
	req := httptest.NewRequest("POST", "/api/files/delete", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleDelete(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("经池内符号链接指向回收站应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(v); err != nil {
		t.Fatalf("回收站内文件应原样未动: %v", err)
	}
}

// Task 10 review Important#2:所属共享 RecycleBin=false 时回退池根 .recycle
// (share 级 .recycle 对回收站页不可见且 SMB 无 hide files)。
func TestHandleDeleteShareWithoutRecycleBinFallsBackToPoolRoot(t *testing.T) {
	root := setupFakePool(t)
	old := getFolderMetaFn
	getFolderMetaFn = func() []diskmgmt.FolderMeta {
		return []diskmgmt.FolderMeta{
			{Name: "photos", Path: filepath.Join(root, "public", "photos"), RecycleBin: false},
		}
	}
	t.Cleanup(func() { getFolderMetaFn = old })

	src := filepath.Join(root, "public", "photos", "x.txt")
	os.WriteFile(src, []byte("data"), 0644)
	body, _ := json.Marshal(map[string]string{"path": "public/photos/x.txt"})
	req := httptest.NewRequest("POST", "/api/files/delete", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleDelete(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("删除应成功, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "public", "photos", ".recycle")); err == nil {
		t.Fatalf("RecycleBin=false 的共享内不应创建 .recycle")
	}
	moved := filepath.Join(root, ".recycle", common.GetNASUser(), "x.txt")
	data, err := os.ReadFile(moved)
	if err != nil || string(data) != "data" {
		t.Fatalf("应回退池根 .recycle/<admin>/x.txt, err=%v got=%q", err, data)
	}
}

// 删除符号链接本身:搬链接不解引用,链接目标原样保留。
func TestHandleDeleteMovesSymlinkItself(t *testing.T) {
	root := setupFakePool(t)
	withPublicFolderMeta(t, root)
	target := filepath.Join(root, "public", "a.txt")
	os.WriteFile(target, []byte("target"), 0644)
	link := filepath.Join(root, "public", "lnk")
	os.Symlink(target, link)

	body, _ := json.Marshal(map[string]string{"path": "public/lnk"})
	req := httptest.NewRequest("POST", "/api/files/delete", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleDelete(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("删除符号链接应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Lstat(link); err == nil {
		t.Fatalf("链接应已消失")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "target" {
		t.Fatalf("链接目标应原样保留, got=%q err=%v", data, err)
	}
	moved := filepath.Join(root, "public", ".recycle", common.GetNASUser(), "lnk")
	if fi, err := os.Lstat(moved); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("回收站内应是链接本身, err=%v", err)
	}
}
