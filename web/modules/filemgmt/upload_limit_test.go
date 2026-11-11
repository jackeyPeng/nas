package filemgmt

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// withMaxUploadBytes 临时把上传大小上限替换为一个很小的值，
// 避免为了触发限制真的构造 10 GiB 请求体。
func withMaxUploadBytes(t *testing.T, n int64) {
	t.Helper()
	orig := maxUploadBytes
	maxUploadBytes = n
	t.Cleanup(func() { maxUploadBytes = orig })
}

func TestHandleUploadRejectsOversizedBodyWith413(t *testing.T) {
	root := setupFakePool(t)
	withMaxUploadBytes(t, 16) // 远小于下面构造的 multipart body

	body, ct := buildMultipart(t, "big.bin", []byte("this content is longer than 16 bytes"))

	req := httptest.NewRequest("POST", "/api/files/upload?path=public", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超过上限应 413, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "public", "big.bin")); err == nil {
		t.Fatalf("超限请求不应落盘任何文件")
	}
}

func TestHandleUploadWithinLimitStillSucceeds(t *testing.T) {
	setupFakePool(t)
	withMaxUploadBytes(t, 10<<30) // 恢复生产默认值，确认未破坏正常上传

	content := []byte("small file within limit")
	body, ct := buildMultipart(t, "ok.txt", content)

	req := httptest.NewRequest("POST", "/api/files/upload?path=public", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("限制内上传应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}
