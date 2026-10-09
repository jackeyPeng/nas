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
