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

// TestZipDirDoesNotFollowSymlinks 覆盖两个场景:
//  1. zipDir 直接打包池根(含 setupFakePool 预置的 public/evil -> /etc 池外逃逸链接):
//     evil 条目只应存链接本身(stub 内容是目标路径字符串),绝不读取 /etc 下的真实内容。
//  2. handleZip 对"池内符号链接目录"的请求(path=public/inner -> real):
//     修复前 os.Stat 判定 IsDir=true 放行,但 filepath.Walk 对根走 Lstat 语义不进树,
//     产出空壳 zip;修复后应正确打包出 inner/x.txt。
func TestZipDirDoesNotFollowSymlinks(t *testing.T) {
	root := setupFakePool(t)

	// 场景一:池根打包,evil 链接不得被跟随读取 /etc 内容。
	var buf bytes.Buffer
	if err := zipDir(&buf, root, "nas1"); err != nil {
		t.Fatalf("zipDir(池根) err=%v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip 不可读: %v", err)
	}
	found := false
	for _, f := range zr.File {
		if f.Name != "nas1/public/evil" {
			continue
		}
		found = true
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		if string(b) != "/etc" {
			t.Fatalf("evil 条目应是链接 stub \"/etc\", got %q", string(b))
		}
	}
	if !found {
		t.Fatalf("zip 内缺少 public/evil 条目")
	}

	// 场景二:池内符号链接目录,handleZip 修复后应进树打包。
	os.MkdirAll(filepath.Join(root, "real"), 0755)
	os.WriteFile(filepath.Join(root, "real", "x.txt"), []byte("realdata"), 0644)
	os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "public", "inner"))

	req := httptest.NewRequest("GET", "/api/files/zip?path=public/inner", nil)
	rec := httptest.NewRecorder()
	handleZip(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handleZip(池内符号链接目录) got %d", rec.Code)
	}
	zr2, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("zip 不可读: %v", err)
	}
	content, ok := "", false
	for _, f := range zr2.File {
		if f.Name == "inner/x.txt" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			content, ok = string(b), true
		}
	}
	if !ok || content != "realdata" {
		t.Fatalf("期望 zip 内含 inner/x.txt 且内容为 realdata, got ok=%v content=%q", ok, content)
	}
}
