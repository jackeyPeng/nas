package filemgmt

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleDownloadServesFile(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "a.txt"), []byte("hello"), 0644)
	req := httptest.NewRequest("GET", "/api/files/download?path=public/a.txt", nil)
	rec := httptest.NewRecorder()
	handleDownload(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "hello" {
		t.Fatalf("download got %d %q", rec.Code, rec.Body.String())
	}
}

func TestHandleDownloadSupportsRange(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "a.txt"), []byte("hello world"), 0644)
	req := httptest.NewRequest("GET", "/api/files/download?path=public/a.txt", nil)
	req.Header.Set("Range", "bytes=0-4")
	rec := httptest.NewRecorder()
	handleDownload(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "hello" {
		t.Fatalf("range got %d %q", rec.Code, rec.Body.String())
	}
}

func TestHandleDownloadRejectsDir(t *testing.T) {
	setupFakePool(t)
	req := httptest.NewRequest("GET", "/api/files/download?path=public", nil)
	rec := httptest.NewRecorder()
	handleDownload(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("目录 download 应 400, got %d", rec.Code)
	}
}

func TestZipDirStreamIntegrity(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "photos", "x.jpg"), []byte("imgdata"), 0644)
	os.MkdirAll(filepath.Join(root, "public", "photos", "sub"), 0755)
	os.WriteFile(filepath.Join(root, "public", "photos", "sub", "y.txt"), []byte("yy"), 0644)

	var buf bytes.Buffer
	if err := zipDir(&buf, filepath.Join(root, "public", "photos"), "photos"); err != nil {
		t.Fatalf("zipDir err=%v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip 不可读: %v", err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		files[f.Name] = string(b)
		rc.Close()
	}
	if files["photos/x.jpg"] != "imgdata" || files["photos/sub/y.txt"] != "yy" {
		t.Fatalf("zip 内容不符: %v", files)
	}
}

func TestHandleZipRejectsTraversal(t *testing.T) {
	setupFakePool(t)
	req := httptest.NewRequest("GET", "/api/files/zip?path=../..", nil)
	rec := httptest.NewRecorder()
	handleZip(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("穿越应 400, got %d", rec.Code)
	}
}
