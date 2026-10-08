package filemgmt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestListDirSortsAndStats(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "b.txt"), []byte("hello"), 0644)
	os.WriteFile(filepath.Join(root, "public", "a.txt"), []byte("hi"), 0644)
	os.MkdirAll(filepath.Join(root, "public", "zdir"), 0755)

	entries, truncated, err := listDir(filepath.Join(root, "public"))
	if err != nil || truncated {
		t.Fatalf("listDir err=%v truncated=%v", err, truncated)
	}
	// 目录在前(photos, zdir 按名称排在 dir 分组内),文件按名称(a.txt, b.txt);evil 符号链接排文件区
	if entries[0].Type != "dir" {
		t.Fatalf("首项应为目录, got %+v", entries[0])
	}
	dirSeen, fileSeenAfterDir := false, false
	for _, e := range entries {
		if e.Type == "dir" {
			dirSeen = true
			if fileSeenAfterDir {
				t.Fatalf("目录应全部排在文件之前, got %+v", entries)
			}
		} else if dirSeen {
			fileSeenAfterDir = true
		}
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name)
	}
	want := map[string]bool{"zdir": true, "a.txt": true, "b.txt": true, "photos": true, "evil": true}
	if len(names) != len(want) {
		t.Fatalf("条目数=%d want %d (%v)", len(names), len(want), names)
	}
	for _, e := range entries {
		if e.Name == "a.txt" && e.Size != 2 {
			t.Fatalf("a.txt size=%d want 2", e.Size)
		}
		if e.Name == "evil" && e.Type != "symlink" {
			t.Fatalf("evil type=%s want symlink", e.Type)
		}
		if e.Name == "a.txt" && e.ModeText != "-rw-r--r--" {
			t.Fatalf("a.txt mode_text=%s", e.ModeText)
		}
	}
}

func TestHandleListRejectsTraversal(t *testing.T) {
	setupFakePool(t)
	req := httptest.NewRequest("GET", "/api/files/list?path=../../etc", nil)
	rec := httptest.NewRecorder()
	handleList(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("穿越请求应 400, got %d", rec.Code)
	}
}

func TestHandleListReturnsJSON(t *testing.T) {
	setupFakePool(t)
	req := httptest.NewRequest("GET", "/api/files/list?path=public", nil)
	rec := httptest.NewRecorder()
	handleList(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("合法请求应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非 JSON: %v", err)
	}
	if resp["path"] != "public" {
		t.Fatalf("path 回显错误: %v", resp["path"])
	}
	bc, ok := resp["breadcrumb"].([]interface{})
	if !ok || len(bc) != 2 { // 池根 + public
		t.Fatalf("breadcrumb=%v", resp["breadcrumb"])
	}
}
