package filemgmt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviewText(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "n.txt"), []byte("line1\nline2"), 0644)
	req := httptest.NewRequest("GET", "/api/files/preview?path=public/n.txt", nil)
	rec := httptest.NewRecorder()
	handlePreview(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["kind"] != "text" || !strings.Contains(resp["content"].(string), "line1") {
		t.Fatalf("resp=%v", resp)
	}
}

func TestPreviewTextExactly1MB(t *testing.T) {
	root := setupFakePool(t)
	exact := strings.Repeat("x", maxTextPreview)
	os.WriteFile(filepath.Join(root, "public", "exact.txt"), []byte(exact), 0644)
	req := httptest.NewRequest("GET", "/api/files/preview?path=public/exact.txt", nil)
	rec := httptest.NewRecorder()
	handlePreview(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["truncated"] != false {
		t.Fatalf("恰好 1MB 不应 truncated, resp=%v", resp)
	}
	content, _ := resp["content"].(string)
	if len(content) != maxTextPreview {
		t.Fatalf("内容应完整 %d 字节, got %d", maxTextPreview, len(content))
	}

	over := strings.Repeat("x", maxTextPreview+1)
	os.WriteFile(filepath.Join(root, "public", "over.txt"), []byte(over), 0644)
	req2 := httptest.NewRequest("GET", "/api/files/preview?path=public/over.txt", nil)
	rec2 := httptest.NewRecorder()
	handlePreview(rec2, req2)
	var resp2 map[string]interface{}
	json.Unmarshal(rec2.Body.Bytes(), &resp2)
	if resp2["truncated"] != true {
		t.Fatalf("1MB+1 应 truncated=true, resp=%v", resp2)
	}
}

func TestPreviewTextTruncates(t *testing.T) {
	root := setupFakePool(t)
	big := strings.Repeat("x", maxTextPreview+100)
	os.WriteFile(filepath.Join(root, "public", "big.log"), []byte(big), 0644)
	req := httptest.NewRequest("GET", "/api/files/preview?path=public/big.log", nil)
	rec := httptest.NewRecorder()
	handlePreview(rec, req)
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["truncated"] != true {
		t.Fatalf("超限应 truncated=true, resp=%v", resp)
	}
}

func TestPreviewRejectsSVG(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "e.svg"), []byte("<svg onload=alert(1)>"), 0644)
	req := httptest.NewRequest("GET", "/api/files/preview?path=public/e.svg", nil)
	rec := httptest.NewRecorder()
	handlePreview(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("SVG 应 415, got %d", rec.Code)
	}
}

func TestPreviewUnsupportedType(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "a.bin"), []byte{0x00, 0x01}, 0644)
	req := httptest.NewRequest("GET", "/api/files/preview?path=public/a.bin", nil)
	rec := httptest.NewRecorder()
	handlePreview(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestHandleUsageReturnsStructure(t *testing.T) {
	setupFakePool(t)
	req := httptest.NewRequest("GET", "/api/files/usage?path=public", nil)
	rec := httptest.NewRecorder()
	handleUsage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非 JSON: %v", err)
	}
	pool, ok := resp["pool"].(map[string]interface{})
	if !ok {
		t.Fatalf("缺少 pool 字段: %v", resp)
	}
	for _, k := range []string{"total_kb", "used_kb", "avail_kb", "use_pct"} {
		if _, ok := pool[k]; !ok {
			t.Fatalf("pool 缺少字段 %s: %v", k, pool)
		}
	}
	dir, ok := resp["dir"].(map[string]interface{})
	if !ok {
		t.Fatalf("缺少 dir 字段: %v", resp)
	}
	if _, ok := dir["du_status"]; !ok {
		t.Fatalf("dir 缺少 du_status: %v", dir)
	}
}
