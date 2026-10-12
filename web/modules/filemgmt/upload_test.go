package filemgmt

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"nas-panel/modules/diskmgmt"
)

// buildMultipart 构造一个只含文件 part 的 multipart/form-data body(单文件场景)。
func buildMultipart(t *testing.T, fieldFilename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)
	fw, err := mw.CreateFormFile("file", fieldFilename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("写入 part 失败: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("关闭 multipart writer 失败: %v", err)
	}
	return buf, mw.FormDataContentType()
}

func TestHandleUploadSingleFileSuccess(t *testing.T) {
	root := setupFakePool(t)
	content := []byte("hello upload")
	body, ct := buildMultipart(t, "a.txt", content)

	req := httptest.NewRequest("POST", "/api/files/upload?path=public", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("上传应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "public", "a.txt"))
	if err != nil {
		t.Fatalf("文件未落盘: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Fatalf("内容不一致, got %q want %q", data, content)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非 JSON: %v", err)
	}
	if resp["ok"] != true {
		t.Fatalf("resp.ok != true: %v", resp)
	}
	if f, ok := resp["files"].(float64); !ok || int(f) != 1 {
		t.Fatalf("resp.files = %v, want 1", resp["files"])
	}
	if b, ok := resp["bytes"].(float64); !ok || int64(b) != int64(len(content)) {
		t.Fatalf("resp.bytes = %v, want %d", resp["bytes"], len(content))
	}
}

func TestHandleUploadConflictWithoutOverwrite(t *testing.T) {
	root := setupFakePool(t)
	original := []byte("original content")
	if err := os.WriteFile(filepath.Join(root, "public", "a.txt"), original, 0644); err != nil {
		t.Fatalf("预置文件失败: %v", err)
	}
	body, ct := buildMultipart(t, "a.txt", []byte("new content"))

	req := httptest.NewRequest("POST", "/api/files/upload?path=public", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("同名无 overwrite 应 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "public", "a.txt"))
	if err != nil {
		t.Fatalf("原文件应仍存在: %v", err)
	}
	if !bytes.Equal(data, original) {
		t.Fatalf("原文件内容不应被改动, got %q want %q", data, original)
	}
}

func TestHandleUploadOverwriteYes(t *testing.T) {
	root := setupFakePool(t)
	if err := os.WriteFile(filepath.Join(root, "public", "a.txt"), []byte("original content"), 0644); err != nil {
		t.Fatalf("预置文件失败: %v", err)
	}
	newContent := []byte("brand new data")
	body, ct := buildMultipart(t, "a.txt", newContent)

	req := httptest.NewRequest("POST", "/api/files/upload?path=public&overwrite=yes", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("overwrite=yes 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "public", "a.txt"))
	if err != nil {
		t.Fatalf("文件应存在: %v", err)
	}
	if !bytes.Equal(data, newContent) {
		t.Fatalf("内容应被覆盖, got %q want %q", data, newContent)
	}
}

func TestHandleUploadTargetDirNotFound(t *testing.T) {
	setupFakePool(t)
	body, ct := buildMultipart(t, "a.txt", []byte("x"))

	req := httptest.NewRequest("POST", "/api/files/upload?path=public/missing-dir", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("目标目录不存在应 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleUploadTargetPathIsFile(t *testing.T) {
	root := setupFakePool(t)
	if err := os.WriteFile(filepath.Join(root, "public", "notadir.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("预置文件失败: %v", err)
	}
	body, ct := buildMultipart(t, "a.txt", []byte("x"))

	req := httptest.NewRequest("POST", "/api/files/upload?path=public/notadir.txt", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("目标是文件应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// 计划修订记录(2026-10-12 Task 8):Go stdlib multipart.Part.FileName() 内置 filepath.Base()
// 剥路径——"../evil" 取出即 "evil"(合法名),穿越语义由 stdlib Base() + resolvePoolPath
// 双重兜底(剥完的名只会落在目标目录内);validateFileName 拦全名 ".." 作纵深防御。
func TestHandleUploadStripsTraversalFilename(t *testing.T) {
	root := setupFakePool(t)
	// 注意:夹具 public/ 里已有指向池外的 evil 符号链接(browse 测试用),
	// 上传名避开它——若撞上,resolvePoolPath 会以"符号链接指向池外"正确拒绝。
	body, ct := buildMultipart(t, "../pwn", []byte("x"))

	req := httptest.NewRequest("POST", "/api/files/upload?path=public", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("../pwn 经 FileName() 剥为 pwn 应 200 落在目标目录内, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "public", "pwn")); err != nil {
		t.Fatalf("pwn 应落在 public/ 内: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "pwn")); err == nil {
		t.Fatalf("池外不应出现 pwn 文件")
	}
}

// 上传名撞上池内既有逃逸符号链接时,resolvePoolPath 必须 fail-closed 拒绝(不得跟随写池外)。
func TestHandleUploadRejectsExistingEscapeSymlinkTarget(t *testing.T) {
	setupFakePool(t)
	body, ct := buildMultipart(t, "../evil", []byte("x"))

	req := httptest.NewRequest("POST", "/api/files/upload?path=public", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("目标为逃逸符号链接应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleUploadRejectsDotDotFullname(t *testing.T) {
	root := setupFakePool(t)
	body, ct := buildMultipart(t, "..", []byte("x"))

	req := httptest.NewRequest("POST", "/api/files/upload?path=public", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("全名 .. 应被 validateFileName 拒 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.Name() == ".." {
			t.Fatalf("池内不应出现 .. 文件")
		}
	}
}

func TestHandleUploadStreamsLargeFile(t *testing.T) {
	root := setupFakePool(t)
	const size = 5 * 1024 * 1024 // 5MB
	content := make([]byte, size)
	for i := range content {
		content[i] = byte(i % 256)
	}
	wantSum := sha256.Sum256(content)
	body, ct := buildMultipart(t, "big.bin", content)

	req := httptest.NewRequest("POST", "/api/files/upload?path=public", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("5MB 上传应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	f, err := os.Open(filepath.Join(root, "public", "big.bin"))
	if err != nil {
		t.Fatalf("打开落盘文件失败: %v", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatalf("读取落盘文件失败: %v", err)
	}
	var gotSum [32]byte
	copy(gotSum[:], h.Sum(nil))
	if gotSum != wantSum {
		t.Fatalf("5MB 文件内容 sha256 不一致")
	}
}

func TestHandleUploadRejectsGet(t *testing.T) {
	setupFakePool(t)
	req := httptest.NewRequest("GET", "/api/files/upload?path=public", nil)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应 405, got %d", rec.Code)
	}
}

func TestAlignPermsDecisionTableDriven(t *testing.T) {
	cases := []struct {
		name      string
		meta      diskmgmt.FolderMeta
		found     bool
		nasUser   string
		wantMode  os.FileMode
		wantOwner string
		wantGroup string
	}{
		{
			name:      "public共享",
			meta:      diskmgmt.FolderMeta{Name: "public"},
			found:     true,
			nasUser:   "fm",
			wantMode:  0664,
			wantOwner: "fm",
			wantGroup: "nasusers",
		},
		{
			name:      "普通共享photos",
			meta:      diskmgmt.FolderMeta{Name: "photos"},
			found:     true,
			nasUser:   "fm",
			wantMode:  0600,
			wantOwner: "photos",
			wantGroup: "photos",
		},
		{
			name:      "无所属共享",
			meta:      diskmgmt.FolderMeta{},
			found:     false,
			nasUser:   "fm",
			wantMode:  0644,
			wantOwner: "",
			wantGroup: "",
		},
	}
	for _, c := range cases {
		mode, owner, group := alignPermsDecision(c.meta, c.found, c.nasUser)
		if mode != c.wantMode || owner != c.wantOwner || group != c.wantGroup {
			t.Errorf("%s: alignPermsDecision() = (%o, %q, %q), want (%o, %q, %q)",
				c.name, mode, owner, group, c.wantMode, c.wantOwner, c.wantGroup)
		}
	}
}

func TestHandleUploadPermsWithoutFolderMeta(t *testing.T) {
	root := setupFakePool(t)
	old := getFolderMetaFn
	getFolderMetaFn = func() []diskmgmt.FolderMeta { return nil }
	t.Cleanup(func() { getFolderMetaFn = old })

	body, ct := buildMultipart(t, "nometa.txt", []byte("x"))
	req := httptest.NewRequest("POST", "/api/files/upload?path=public", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("上传应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	info, err := os.Stat(filepath.Join(root, "public", "nometa.txt"))
	if err != nil {
		t.Fatalf("文件未落盘: %v", err)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("无所属共享时 mode=%o, want 0644", info.Mode().Perm())
	}
}

func TestHandleUploadPermsWithPublicFolderMeta(t *testing.T) {
	root := setupFakePool(t)
	old := getFolderMetaFn
	getFolderMetaFn = func() []diskmgmt.FolderMeta {
		return []diskmgmt.FolderMeta{{Name: "public", Path: filepath.Join(root, "public")}}
	}
	t.Cleanup(func() { getFolderMetaFn = old })

	body, ct := buildMultipart(t, "pub.txt", []byte("x"))
	req := httptest.NewRequest("POST", "/api/files/upload?path=public", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("上传应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	info, err := os.Stat(filepath.Join(root, "public", "pub.txt"))
	if err != nil {
		t.Fatalf("文件未落盘: %v", err)
	}
	if info.Mode().Perm() != 0664 {
		t.Fatalf("public 共享时 mode=%o, want 0664", info.Mode().Perm())
	}
	if os.Geteuid() != 0 {
		t.Skip("非 root 环境跳过 chown 断言")
	}
}

func TestHandleUploadMultiPartMidwayConflict(t *testing.T) {
	root := setupFakePool(t)
	if err := os.WriteFile(filepath.Join(root, "public", "second.txt"), []byte("existing"), 0644); err != nil {
		t.Fatalf("预置冲突文件失败: %v", err)
	}

	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)
	fw1, err := mw.CreateFormFile("file", "first.txt")
	if err != nil {
		t.Fatalf("CreateFormFile first: %v", err)
	}
	fw1.Write([]byte("first content"))
	fw2, err := mw.CreateFormFile("file", "second.txt")
	if err != nil {
		t.Fatalf("CreateFormFile second: %v", err)
	}
	fw2.Write([]byte("second content"))
	mw.Close()

	req := httptest.NewRequest("POST", "/api/files/upload?path=public", buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("第二个 part 冲突应 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	// 拍板决策 1:已写入的 part 不回滚,第一个文件应已落盘。
	data, err := os.ReadFile(filepath.Join(root, "public", "first.txt"))
	if err != nil {
		t.Fatalf("第一个文件应已写入: %v", err)
	}
	if string(data) != "first content" {
		t.Fatalf("第一个文件内容不符, got %q", data)
	}
	// 第二个文件(冲突目标)内容应保持预置时的原值,未被覆盖。
	data2, err := os.ReadFile(filepath.Join(root, "public", "second.txt"))
	if err != nil {
		t.Fatalf("第二个文件应仍存在: %v", err)
	}
	if string(data2) != "existing" {
		t.Fatalf("冲突目标内容应未被改动, got %q", data2)
	}
}
