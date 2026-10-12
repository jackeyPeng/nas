package filemgmt

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
