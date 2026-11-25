package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// --- 测试基础设施（httptest 全覆盖，禁 /tmp 探针：全部用 t.TempDir()） ---

const testSecret = "test-secret-for-nas-photos"

// newTestApp 在 t.TempDir() 下建一个全新 app 实例（storageDir/dataDir 都是
// 临时目录，测试间互不干扰）。
func newTestApp(t *testing.T) *app {
	t.Helper()
	root := t.TempDir()
	storageDir := filepath.Join(root, "storage")
	dataDir := filepath.Join(root, "data")
	a, err := newApp(dataDir, storageDir)
	if err != nil {
		t.Fatalf("newApp failed: %v", err)
	}
	return a
}

func testCtx(a *app) secretCtx {
	return secretCtx{secret: testSecret, app: a}
}

// signFor 构造测试用的合法签名，算法与 verifyNASAuth 一致。
func signFor(secret, user string, unixSeconds int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(user + "\n" + strconv.FormatInt(unixSeconds, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// authHeaders 把合法 SSO 头设置到请求上。
func authHeaders(req *http.Request, user string) {
	now := time.Now().Unix()
	req.Header.Set("X-NAS-User", user)
	req.Header.Set("X-NAS-Auth", signFor(testSecret, user, now))
	req.Header.Set("X-NAS-Time", strconv.FormatInt(now, 10))
}

// encodeJPEG/PNG/GIF 生成一张 w x h 纯色测试图，返回编码后的字节。
func encodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fillSolid(img, color.RGBA{R: 200, G: 50, B: 50, A: 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fillSolid(img, color.RGBA{R: 50, G: 200, B: 50, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func encodeGIF(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, w, h), []color.Color{
		color.RGBA{R: 50, G: 50, B: 200, A: 255},
		color.RGBA{R: 255, G: 255, B: 255, A: 255},
	})
	fillSolid(img, color.RGBA{R: 50, G: 50, B: 200, A: 255})
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return buf.Bytes()
}

func fillSolid(img draw1, c color.Color) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			img.Set(x, y, c)
		}
	}
}

// draw1 是 fillSolid 需要的最小接口（image.Image + Set），避免引入
// image/draw 只为这一个工具函数。
type draw1 interface {
	Bounds() image.Rectangle
	Set(x, y int, c color.Color)
}

// buildMultipart 构造多文件上传的 multipart 请求体，files 为 (文件名, 内容)
// 对列表。
func buildMultipart(t *testing.T, files [][2]any) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	for _, f := range files {
		name := f[0].(string)
		content := f[1].([]byte)
		part, err := mw.CreateFormFile("files", name)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatalf("write form file: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return body, mw.FormDataContentType()
}

func doUpload(t *testing.T, a *app, files [][2]any) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := buildMultipart(t, files)
	req := httptest.NewRequest(http.MethodPost, "/api/upload", body)
	req.Header.Set("Content-Type", contentType)
	authHeaders(req, "alice")
	w := httptest.NewRecorder()
	handleUpload(w, req, testCtx(a))
	return w
}

func listPhotosViaAPI(t *testing.T, a *app) []photoListItem {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/photos", nil)
	authHeaders(req, "alice")
	w := httptest.NewRecorder()
	handleList(w, req, testCtx(a))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}
	var items []photoListItem
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return items
}

// --- 上传 ---

func TestUploadValidFormats(t *testing.T) {
	a := newTestApp(t)
	files := [][2]any{
		{"a.jpg", encodeJPEG(t, 40, 30)},
		{"b.png", encodePNG(t, 40, 30)},
		{"c.gif", encodeGIF(t, 40, 30)},
	}
	w := doUpload(t, a, files)
	if w.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body=%s", w.Code, w.Body.String())
	}

	var results []uploadResult
	if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode upload results: %v", err)
	}
	for _, r := range results {
		if !r.OK {
			t.Errorf("upload %q failed: %s", r.OrigName, r.Error)
		}
	}

	items := listPhotosViaAPI(t, a)
	if len(items) != 3 {
		t.Fatalf("列表应有 3 张照片，实际 %d", len(items))
	}
}

func TestUploadOversizeRejected(t *testing.T) {
	a := newTestApp(t)
	big := make([]byte, maxUploadSize+1024)
	// 填充非零内容，避免被当成空文件/高度可压缩内容影响（这里只测大小
	// 上限，不要求是合法 jpeg 内容——handleOnePart 在扩展名通过后先检查
	// 大小才解码，大小检查发生在 io.Copy 阶段，不依赖内容合法性）。
	for i := range big {
		big[i] = byte(i % 251)
	}
	w := doUpload(t, a, [][2]any{{"huge.jpg", big}})

	var results []uploadResult
	if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(results) != 1 || results[0].OK {
		t.Fatalf("超限文件应上传失败: %+v", results)
	}
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("状态码 = %d，期望 413", w.Code)
	}

	items := listPhotosViaAPI(t, a)
	if len(items) != 0 {
		t.Errorf("超限文件不应进入列表，实际 %d 条", len(items))
	}
}

func TestUploadBadFormatRejected(t *testing.T) {
	a := newTestApp(t)
	w := doUpload(t, a, [][2]any{{"evil.exe", []byte("not an image")}})

	var results []uploadResult
	if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(results) != 1 || results[0].OK {
		t.Fatalf("非白名单格式应被拒绝: %+v", results)
	}
	if w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("状态码 = %d，期望 415", w.Code)
	}
}

// --- 路径穿越 ---

func TestPhotoPathTraversalBlocked(t *testing.T) {
	a := newTestApp(t)
	doUpload(t, a, [][2]any{{"a.jpg", encodeJPEG(t, 20, 20)}})
	items := listPhotosViaAPI(t, a)
	if len(items) != 1 {
		t.Fatalf("前置上传失败")
	}
	validID := items[0].ID

	cases := []string{
		"/photo/../../../../etc/passwd",
		"/photo/%2e%2e/%2e%2e/etc/passwd",
		"/photo/..%2f..%2f" + validID + ".jpg",
	}

	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			authHeaders(req, "alice")
			w := httptest.NewRecorder()
			handlePhoto(w, req, testCtx(a))
			if w.Code != http.StatusNotFound {
				t.Errorf("路径穿越请求 %q 应返回 404，实际 %d", p, w.Code)
			}
		})
	}

	// 空字节场景：httptest.NewRequest/url.Parse 本身就会拒绝 URL 里的裸控制
	// 字符（无法构造出这种请求打到 handler），这里改为直接用 URL{Path:...}
	// 绕过 URL 解析，模拟空字节绕过上游校验后真正打到 handler 的情况——
	// 验证 parsePhotoRequestPath 自身的防线（任务要求显式覆盖空字节注入）。
	t.Run("null byte in path", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/photo/"+validID+".jpg", nil)
		req.URL.Path = "/photo/" + validID + ".jpg\x00.png"
		authHeaders(req, "alice")
		w := httptest.NewRecorder()
		handlePhoto(w, req, testCtx(a))
		if w.Code != http.StatusNotFound {
			t.Errorf("路径中含空字节应返回 404，实际 %d", w.Code)
		}
	})

	// 根目录之外没有任何文件被意外创建/读取——用 storage 根目录之外没有
	// 探测文件来验证未逃出根目录（本测试全程只在 storageDir 内操作文件，
	// 因此只要 404 即说明没有触发任何文件系统穿越读取）。
}

func TestPhotoRequestPathParsing(t *testing.T) {
	cases := []struct {
		path   string
		prefix string
		wantOK bool
	}{
		{"/photo/1234_abcd1234.jpg", "/photo/", true},
		{"/photo/1234_abcd1234.png", "/photo/", true},
		{"/photo/../etc/passwd", "/photo/", false},
		{"/photo/%2e%2e/etc/passwd", "/photo/", false}, // 字面字符串形态（未解码）本身也不合法形态
		{"/photo/1234_abcd1234.exe", "/photo/", false},
		{"/photo/notanid.jpg", "/photo/", false},
		{"/photo/1234_abcd1234.jpg\x00", "/photo/", false},
		{"/photo/a/b.jpg", "/photo/", false},
	}
	for _, c := range cases {
		_, _, ok := parsePhotoRequestPath(c.path, c.prefix)
		if ok != c.wantOK {
			t.Errorf("parsePhotoRequestPath(%q) ok=%v，期望 %v", c.path, ok, c.wantOK)
		}
	}
}

// --- 缩略图 ---

func TestThumbnailGenerateAndCache(t *testing.T) {
	a := newTestApp(t)
	doUpload(t, a, [][2]any{{"a.jpg", encodeJPEG(t, 800, 400)}})
	items := listPhotosViaAPI(t, a)
	id := items[0].ID

	// 首请求：生成并写入缓存。
	req1 := httptest.NewRequest(http.MethodGet, "/thumb/"+id+".jpg", nil)
	authHeaders(req1, "alice")
	w1 := httptest.NewRecorder()
	handleThumb(w1, req1, testCtx(a))
	if w1.Code != http.StatusOK {
		t.Fatalf("首次缩略图请求状态 = %d", w1.Code)
	}
	thumbFile := a.thumbPath(id)
	info1, err := os.Stat(thumbFile)
	if err != nil {
		t.Fatalf("缩略图缓存文件未生成: %v", err)
	}

	decoded, err := jpeg.Decode(bytes.NewReader(w1.Body.Bytes()))
	if err != nil {
		t.Fatalf("解码缩略图失败: %v", err)
	}
	b := decoded.Bounds()
	if b.Dx() > thumbMaxEdge || b.Dy() > thumbMaxEdge {
		t.Errorf("缩略图尺寸 %dx%d 超过最长边 %d", b.Dx(), b.Dy(), thumbMaxEdge)
	}

	// 二请求：应命中缓存，mtime 不变（没有重新生成）。
	time.Sleep(10 * time.Millisecond)
	req2 := httptest.NewRequest(http.MethodGet, "/thumb/"+id+".jpg", nil)
	authHeaders(req2, "alice")
	w2 := httptest.NewRecorder()
	handleThumb(w2, req2, testCtx(a))
	if w2.Code != http.StatusOK {
		t.Fatalf("二次缩略图请求状态 = %d", w2.Code)
	}
	info2, err := os.Stat(thumbFile)
	if err != nil {
		t.Fatalf("缓存文件消失: %v", err)
	}
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Errorf("二次请求应命中缓存（mtime 不应变化）：%v -> %v", info1.ModTime(), info2.ModTime())
	}
}

func TestThumbnailCorruptImage404(t *testing.T) {
	a := newTestApp(t)
	doUpload(t, a, [][2]any{{"a.jpg", encodeJPEG(t, 20, 20)}})
	items := listPhotosViaAPI(t, a)
	id := items[0].ID

	// 篡改原图使其损坏（保留合法扩展名，内容不再是有效 jpeg）。
	if err := os.WriteFile(a.originalPath(id, "jpg"), []byte("not a real jpeg"), 0o640); err != nil {
		t.Fatalf("篡改源文件失败: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/thumb/"+id+".jpg", nil)
	authHeaders(req, "alice")
	w := httptest.NewRecorder()
	handleThumb(w, req, testCtx(a))
	if w.Code != http.StatusNotFound {
		t.Errorf("损坏图片应返回 404 占位，实际 %d", w.Code)
	}
}

// --- 删除 ---

func TestDeleteIdempotentAndCleansUp(t *testing.T) {
	a := newTestApp(t)
	doUpload(t, a, [][2]any{{"a.jpg", encodeJPEG(t, 20, 20)}})
	items := listPhotosViaAPI(t, a)
	id := items[0].ID

	// 先请求一次缩略图，确保缓存文件存在，用于验证删除也清掉缩略图。
	treq := httptest.NewRequest(http.MethodGet, "/thumb/"+id+".jpg", nil)
	authHeaders(treq, "alice")
	handleThumb(httptest.NewRecorder(), treq, testCtx(a))
	if _, err := os.Stat(a.thumbPath(id)); err != nil {
		t.Fatalf("前置缩略图生成失败: %v", err)
	}

	del := func() *httptest.ResponseRecorder {
		body, _ := json.Marshal(deleteRequest{ID: id})
		req := httptest.NewRequest(http.MethodPost, "/api/delete", bytes.NewReader(body))
		authHeaders(req, "alice")
		w := httptest.NewRecorder()
		handleDelete(w, req, testCtx(a))
		return w
	}

	w1 := del()
	if w1.Code != http.StatusOK {
		t.Fatalf("首次删除状态 = %d", w1.Code)
	}

	// 幂等：第二次删除同一 id 仍 200。
	w2 := del()
	if w2.Code != http.StatusOK {
		t.Fatalf("重复删除应仍 200（幂等），实际 %d", w2.Code)
	}

	// 删除后：列表为空，原图/缩略图均 404。
	items = listPhotosViaAPI(t, a)
	if len(items) != 0 {
		t.Errorf("删除后列表应为空，实际 %d 条", len(items))
	}

	preq := httptest.NewRequest(http.MethodGet, "/photo/"+id+".jpg", nil)
	authHeaders(preq, "alice")
	pw := httptest.NewRecorder()
	handlePhoto(pw, preq, testCtx(a))
	if pw.Code != http.StatusNotFound {
		t.Errorf("删除后原图应 404，实际 %d", pw.Code)
	}

	treq2 := httptest.NewRequest(http.MethodGet, "/thumb/"+id+".jpg", nil)
	authHeaders(treq2, "alice")
	tw2 := httptest.NewRecorder()
	handleThumb(tw2, treq2, testCtx(a))
	if tw2.Code != http.StatusNotFound {
		t.Errorf("删除后缩略图应 404，实际 %d", tw2.Code)
	}

	if _, err := os.Stat(a.originalPath(id, "jpg")); !os.IsNotExist(err) {
		t.Errorf("原图文件应已从磁盘删除")
	}
	if _, err := os.Stat(a.thumbPath(id)); !os.IsNotExist(err) {
		t.Errorf("缩略图文件应已从磁盘删除")
	}
}

func TestDeleteNonexistentIsIdempotent(t *testing.T) {
	a := newTestApp(t)
	body, _ := json.Marshal(deleteRequest{ID: "999999999999_deadbeef"})
	req := httptest.NewRequest(http.MethodPost, "/api/delete", bytes.NewReader(body))
	authHeaders(req, "alice")
	w := httptest.NewRecorder()
	handleDelete(w, req, testCtx(a))
	if w.Code != http.StatusOK {
		t.Errorf("删除不存在的 id 应 200（幂等），实际 %d", w.Code)
	}
}

// --- SSO ---

func TestSSOMissingHeaders401(t *testing.T) {
	a := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/api/photos", nil)
	w := httptest.NewRecorder()
	handleList(w, req, testCtx(a))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("缺少 SSO 头应 401，实际 %d", w.Code)
	}
}

func TestSSOWrongSignature401(t *testing.T) {
	a := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/api/photos", nil)
	now := time.Now().Unix()
	req.Header.Set("X-NAS-User", "alice")
	req.Header.Set("X-NAS-Auth", "0000000000000000000000000000000000000000000000000000000000000000")
	req.Header.Set("X-NAS-Time", strconv.FormatInt(now, 10))
	w := httptest.NewRecorder()
	handleList(w, req, testCtx(a))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("错误签名应 401，实际 %d", w.Code)
	}
}

func TestSSOExpiredSignature401(t *testing.T) {
	a := newTestApp(t)
	stale := time.Now().Add(-120 * time.Second).Unix() // 超出 ±60s 容差
	req := httptest.NewRequest(http.MethodGet, "/api/photos", nil)
	req.Header.Set("X-NAS-User", "alice")
	req.Header.Set("X-NAS-Auth", signFor(testSecret, "alice", stale))
	req.Header.Set("X-NAS-Time", strconv.FormatInt(stale, 10))
	w := httptest.NewRecorder()
	handleList(w, req, testCtx(a))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("过期签名（120s 偏移）应 401，实际 %d", w.Code)
	}
}

func TestSSOValidSignature200(t *testing.T) {
	a := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/api/photos", nil)
	authHeaders(req, "alice")
	w := httptest.NewRecorder()
	handleList(w, req, testCtx(a))
	if w.Code != http.StatusOK {
		t.Errorf("合法签名应 200，实际 %d", w.Code)
	}
}

// --- healthz ---

func TestHealthzOKWhenStorageWritable(t *testing.T) {
	a := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	a.handleHealthz(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("healthz 状态码 = %d，期望 200", w.Code)
	}
}

func TestHealthz500WhenStorageNotWritable(t *testing.T) {
	a := newTestApp(t)
	// 把 storageDir 变为不可写：去掉写权限位。
	if err := os.Chmod(a.storageDir, 0o500); err != nil {
		t.Skipf("无法修改目录权限（可能以 root 运行）: %v", err)
	}
	defer os.Chmod(a.storageDir, 0o750)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	a.handleHealthz(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("storage 不可写时 healthz 应 500，实际 %d", w.Code)
	}
}

// --- 并发上传 ---

func TestConcurrentUploadsNoRace(t *testing.T) {
	a := newTestApp(t)
	const goroutines = 8
	const perGoroutine = 5

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			files := make([][2]any, 0, perGoroutine)
			for i := 0; i < perGoroutine; i++ {
				name := fmt.Sprintf("g%d-%d.jpg", g, i)
				files = append(files, [2]any{name, encodeJPEG(t, 16, 16)})
			}
			w := doUpload(t, a, files)
			if w.Code != http.StatusOK {
				t.Errorf("goroutine %d 上传失败: %d %s", g, w.Code, w.Body.String())
			}
		}(g)
	}
	wg.Wait()

	items := listPhotosViaAPI(t, a)
	want := goroutines * perGoroutine
	if len(items) != want {
		t.Fatalf("并发上传后照片数 = %d，期望 %d", len(items), want)
	}

	// 索引里的 id 必须两两不同（并发生成无碰撞）。
	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.ID] {
			t.Errorf("重复 id: %s", it.ID)
		}
		seen[it.ID] = true
	}
}

// --- 杂项：验签函数与 id 正则本身 ---

func TestVerifyNASAuth(t *testing.T) {
	now := time.Now().Unix()
	sig := signFor("s3cret", "bob", now)
	if !verifyNASAuth("s3cret", "bob", strconv.FormatInt(now, 10), sig, now) {
		t.Error("合法签名应验签通过")
	}
	if verifyNASAuth("s3cret", "bob", strconv.FormatInt(now, 10), "deadbeef", now) {
		t.Error("错误签名不应验签通过")
	}
	if verifyNASAuth("", "bob", strconv.FormatInt(now, 10), sig, now) {
		t.Error("空 secret 不应验签通过")
	}
}

func TestIDPattern(t *testing.T) {
	valid := []string{"1700000000000_deadbeef", "0_00000000"}
	invalid := []string{"abc_deadbeef", "1700000000000_deadbee", "1700000000000_DEADBEEF", "../etc/passwd", ""}
	for _, v := range valid {
		if !idPattern.MatchString(v) {
			t.Errorf("%q 应匹配 idPattern", v)
		}
	}
	for _, v := range invalid {
		if idPattern.MatchString(v) {
			t.Errorf("%q 不应匹配 idPattern", v)
		}
	}
}

// 保留 io 导入的使用点（genThumbnail 签名用到 io.Writer，测试里间接通过
// handleThumb 覆盖，这里不直接调用避免未使用 import 报错——占位确保编译器
// 不会因测试文件改动而抱怨未使用的标准库引用）。
var _ io.Writer = (*bytes.Buffer)(nil)
