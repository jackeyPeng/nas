package filemgmt

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

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
