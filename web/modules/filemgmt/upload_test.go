package filemgmt

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"nas-panel/common"
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

// 计划修订记录(TASK-DU,2026-10-15):Go stdlib mime/multipart.Part 并无
// FullFileName() 方法(已核实本机 go1.26 stdlib 源码),改为直接解析
// Content-Disposition 头取原始 filename(partFullFileName,不经 Part.FileName()
// 的 filepath.Base() 剥路径)。因此 "../pwn" 不再被 stdlib 静默剥成单段名
// "pwn",而是被 sanitizeUploadRelPath 按 "/" 切成 [".." , "pwn"] 两段,".."
// 段触发 validateFileName 拒绝 → 400。新语义比旧版"剥路径后放行"更严格:
// 穿越尝试直接拒绝,而不是依赖剥离后巧合落在目标目录内。
func TestHandleUploadRejectsTraversalFilename(t *testing.T) {
	root := setupFakePool(t)
	// 注意:夹具 public/ 里已有指向池外的 evil 符号链接(browse 测试用),
	// 上传名避开它——若撞上,resolvePoolPath 会以"符号链接指向池外"正确拒绝。
	body, ct := buildMultipart(t, "../pwn", []byte("x"))

	req := httptest.NewRequest("POST", "/api/files/upload?path=public", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("../pwn 含 .. 段应被 sanitizeUploadRelPath 拒 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "public", "pwn")); err == nil {
		t.Fatalf("pwn 不应落在 public/ 内(请求已被拒绝)")
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

// TASK-8-FIX 修复项 1:writePartToFile 纯函数行为单测,不靠并发构造真实竞争窗口
// (httptest 单线程调用 handler,无法在 Lstat 预检与 OpenFile 之间插入另一方写入)。
// 直接预置目标文件模拟"并发方已创建"的后果状态,验证 OpenFile(O_EXCL) 碰撞 EEXIST 时:
//   - 返回错误且 errors.Is(err, fs.ErrExist) 为真
//   - 原文件内容未被删除/改动(调用方/函数内部均不得 os.Remove 非我所建的文件)
//
// handler 层的 409 映射(upload.go 的 errors.Is(werr, fs.ErrExist) 分支)因为 Lstat
// 预检已经先于 writePartToFile 拦截了"目标已存在"的常规路径,在不改产品代码的前提下
// 无法从 httptest 单线程请求里构造出"Lstat 预检通过但 OpenFile 时已被别的请求创建"的
// 真实竞争窗口(需要两个真实并发 goroutine 在 Lstat 和 OpenFile 之间抢时机,httptest
// 的单次 handler 调用做不到)。该 handler 分支的语义正确性由本文件的
// writePartToFile 直接单测 + 代码审查保证:两者共用同一错误类型判定
// (errors.Is(_, fs.ErrExist))和同一 409 body 构造函数(writeUploadConflictBody),
// 产品代码里两处逻辑直接复用,不存在"函数测对但 handler 接错"的中间状态。
func TestWritePartToFileExclCollisionDoesNotRemoveExisting(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "a.txt")
	original := []byte("concurrent writer got here first")
	if err := os.WriteFile(target, original, 0644); err != nil {
		t.Fatalf("预置目标文件失败: %v", err)
	}

	buf := make([]byte, 32*1024)
	n, err := writePartToFile(target, bytes.NewReader([]byte("my content")), buf, false)

	if err == nil {
		t.Fatalf("overwrite=false 且目标已存在应返回错误,got nil, n=%d", n)
	}
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err 应满足 errors.Is(err, fs.ErrExist), got %v (%T)", err, err)
	}
	data, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatalf("原文件应仍存在(不得被误删): %v", rerr)
	}
	if !bytes.Equal(data, original) {
		t.Fatalf("原文件内容应未被改动, got %q want %q", data, original)
	}
}

// writePartToFile 在 OpenFile 成功后 CopyBuffer 失败时,必须自清半成品(函数内部
// os.Remove),不留下部分写入的垂死文件。用一个在读取中途返回错误的 io.Reader 模拟
// 磁盘满/网络中断等 copy 阶段失败。
type failingAfterNReader struct {
	data      []byte
	pos       int
	failAfter int
}

func (r *failingAfterNReader) Read(p []byte) (int, error) {
	if r.pos >= r.failAfter {
		return 0, errors.New("模拟 copy 阶段失败(磁盘满/中断)")
	}
	remain := r.failAfter - r.pos
	n := copy(p, r.data[r.pos:r.pos+min(len(p), remain)])
	r.pos += n
	return n, nil
}

func TestWritePartToFileCopyFailureSelfCleansHalfWritten(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "half.bin")
	reader := &failingAfterNReader{data: []byte("0123456789"), failAfter: 5}

	buf := make([]byte, 2) // 小 buffer 确保多次 Read,制造半成品
	_, err := writePartToFile(target, reader, buf, false)

	if err == nil {
		t.Fatalf("copy 阶段失败应返回错误")
	}
	if errors.Is(err, fs.ErrExist) {
		t.Fatalf("copy 阶段失败不应被误判为 EEXIST, got %v", err)
	}
	if _, serr := os.Stat(target); !os.IsNotExist(serr) {
		t.Fatalf("copy 失败后半成品应已被 writePartToFile 自清, stat err=%v", serr)
	}
}

// TASK-8-FIX 修复项 2:chown root-gated 真实断言(替换原死 t.Skip 后零断言的问题)。
// 非 root 环境:断言 chown 被跳过(uid/gid 仍为运行者,即测试进程的 euid/egid),
// 这是 alignNewFilePerms 在 lookupUID/lookupGID 查不到系统用户/组时的真实跳过语义;
// mode 断言在非 root 下同样生效(chmod 不需要 root)。
// root 环境:进一步按 common.GetNASUser()/"nasusers" 用 user.Lookup/LookupGroup
// 解析期望 uid/gid 并比对(若该环境 fm 用户或 nasusers 组不存在,则退化为"chown 被
// 跳过"分支的同款断言并在失败信息里注明,不强行假设生产账号存在)。
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
	target := filepath.Join(root, "public", "pub.txt")
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("文件未落盘: %v", err)
	}
	if info.Mode().Perm() != 0664 {
		t.Fatalf("public 共享时 mode=%o, want 0664", info.Mode().Perm())
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("无法取 syscall.Stat_t(非 Linux?本项目 Linux-only)")
	}
	gotUID, gotGID := int(stat.Uid), int(stat.Gid)

	if os.Geteuid() != 0 {
		// 非 root:alignNewFilePerms 的 os.Chown 不会被调用到能生效的程度——实际是
		// lookupUID/lookupGID 大概率查不到生产账号(fm/nasusers 在开发机不存在),
		// chown 被跳过,文件 uid/gid 仍为创建者(当前测试进程)本身。
		wantUID, wantGID := os.Geteuid(), os.Getegid()
		if gotUID != wantUID || gotGID != wantGID {
			t.Fatalf("非 root 环境 chown 应被跳过,文件 uid/gid 应仍为运行者 (%d,%d), got (%d,%d)",
				wantUID, wantGID, gotUID, gotGID)
		}
		return
	}

	// root 环境:尝试解析生产账号的期望 uid/gid 做真实比对;账号不存在则退化为
	// "chown 被跳过"同款断言(root 身份下 lookupUID/lookupGID 失败同样会跳过 chown,
	// 此时文件 uid/gid 保持 root 创建时的默认值,即运行者本身)。
	u, uerr := user.Lookup(common.GetNASUser())
	g, gerr := user.LookupGroup("nasusers")
	if uerr != nil || gerr != nil {
		wantUID, wantGID := os.Geteuid(), os.Getegid()
		if gotUID != wantUID || gotGID != wantGID {
			t.Fatalf("root 环境但 fm/nasusers 账号不存在,chown 应被跳过,uid/gid 应仍为运行者 (%d,%d), got (%d,%d) (lookup err: user=%v group=%v)",
				wantUID, wantGID, gotUID, gotGID, uerr, gerr)
		}
		return
	}
	wantUID, _ := strconv.Atoi(u.Uid)
	wantGID, _ := strconv.Atoi(g.Gid)
	if gotUID != wantUID {
		t.Fatalf("root 环境 chown 后 uid 应为 %s(%d), got %d", common.GetNASUser(), wantUID, gotUID)
	}
	if gotGID != wantGID {
		t.Fatalf("root 环境 chown 后 gid 应为 nasusers(%d), got %d", wantGID, gotGID)
	}
}

// 新增:普通共享分支(非 public)的 root-gated 集成测试。注入 fake getFolderMetaFn
// 返回 Name="photos" 的 meta,上传到 photos/ 下,断言 mode==0600(非 root 也断言
// chmod);chown 部分遵循与 TestHandleUploadPermsWithPublicFolderMeta 相同的
// root-gated 模式(root 下尝试解析 photos 用户/组,查不到则退化为"跳过"断言)。
func TestHandleUploadPermsWithOrdinaryShareFolderMeta(t *testing.T) {
	root := setupFakePool(t)
	old := getFolderMetaFn
	getFolderMetaFn = func() []diskmgmt.FolderMeta {
		return []diskmgmt.FolderMeta{{Name: "photos", Path: filepath.Join(root, "public", "photos")}}
	}
	t.Cleanup(func() { getFolderMetaFn = old })

	body, ct := buildMultipart(t, "cat.jpg", []byte("x"))
	req := httptest.NewRequest("POST", "/api/files/upload?path=public/photos", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("上传应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	target := filepath.Join(root, "public", "photos", "cat.jpg")
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("文件未落盘: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("普通共享(非 public)时 mode=%o, want 0600", info.Mode().Perm())
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("无法取 syscall.Stat_t(非 Linux?本项目 Linux-only)")
	}
	gotUID, gotGID := int(stat.Uid), int(stat.Gid)

	if os.Geteuid() != 0 {
		wantUID, wantGID := os.Geteuid(), os.Getegid()
		if gotUID != wantUID || gotGID != wantGID {
			t.Fatalf("非 root 环境 chown 应被跳过,文件 uid/gid 应仍为运行者 (%d,%d), got (%d,%d)",
				wantUID, wantGID, gotUID, gotGID)
		}
		return
	}

	// root 环境:alignPermsDecision 对普通共享(非 public)用 meta.Name 同时作为
	// owner 和 group 名(photos:photos)。该账号/组在非生产同构机大概率不存在,
	// 查不到则退化为"chown 被跳过"同款断言。
	u, uerr := user.Lookup("photos")
	g, gerr := user.LookupGroup("photos")
	if uerr != nil || gerr != nil {
		wantUID, wantGID := os.Geteuid(), os.Getegid()
		if gotUID != wantUID || gotGID != wantGID {
			t.Fatalf("root 环境但 photos 用户/组不存在,chown 应被跳过,uid/gid 应仍为运行者 (%d,%d), got (%d,%d) (lookup err: user=%v group=%v)",
				wantUID, wantGID, gotUID, gotGID, uerr, gerr)
		}
		return
	}
	wantUID, _ := strconv.Atoi(u.Uid)
	wantGID, _ := strconv.Atoi(g.Gid)
	if gotUID != wantUID {
		t.Fatalf("root 环境 chown 后 uid 应为 photos(%d), got %d", wantUID, gotUID)
	}
	if gotGID != wantGID {
		t.Fatalf("root 环境 chown 后 gid 应为 photos(%d), got %d", wantGID, gotGID)
	}
}

// ---------------------------------------------------------------------------
// TASK-DU: 目录上传(webkitdirectory + FullFileName 逐段消毒)
// ---------------------------------------------------------------------------

// buildMultipartMulti 构造含多个文件 part 的 multipart/form-data body,字段名统一为 "files"。
// 用 CreateFormFile 保留斜杠(只转义引号/反斜杠),模拟前端 webkitRelativePath 携带的子路径 filename。
func buildMultipartMulti(t *testing.T, filenames []string, contents [][]byte) (*bytes.Buffer, string) {
	t.Helper()
	if len(filenames) != len(contents) {
		t.Fatalf("filenames/contents 长度不一致")
	}
	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)
	for i, name := range filenames {
		fw, err := mw.CreateFormFile("files", name)
		if err != nil {
			t.Fatalf("CreateFormFile(%q): %v", name, err)
		}
		if _, err := fw.Write(contents[i]); err != nil {
			t.Fatalf("写入 part(%q) 失败: %v", name, err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("关闭 multipart writer 失败: %v", err)
	}
	return buf, mw.FormDataContentType()
}

// TestSanitizeUploadRelPathAcceptsNested 覆盖消毒函数对合法多段相对路径的放行,
// 并断言返回值已按 "/" 重新拼接(净化后的相对路径)。
func TestSanitizeUploadRelPathAcceptsNested(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"a.txt", "a.txt"},
		{"d1/a.txt", "d1/a.txt"},
		{"d1/d2/b.txt", "d1/d2/b.txt"},
	}
	for _, c := range cases {
		got, err := sanitizeUploadRelPath(c.in)
		if err != nil {
			t.Fatalf("sanitizeUploadRelPath(%q) 应合法, got err=%v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("sanitizeUploadRelPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSanitizeUploadRelPathRejectsTableDriven 表驱动覆盖所有拒绝场景。
func TestSanitizeUploadRelPathRejectsTableDriven(t *testing.T) {
	longSeg := make([]byte, 256)
	for i := range longSeg {
		longSeg[i] = 'x'
	}
	cases := []struct {
		name string
		in   string
	}{
		{"空串", ""},
		{"前导斜杠(绝对路径)", "/etc/passwd"},
		{"空段(中间)", "a//b"},
		{"空段(尾随斜杠)", "a/"},
		{"段为..", "d1/../x"},
		{"段为.", "d1/./x"},
		{"段含控制字符", "d1/\x01x"},
		{"段过长(>255字节)", "d1/" + string(longSeg)},
	}
	for _, c := range cases {
		if got, err := sanitizeUploadRelPath(c.in); err == nil {
			t.Fatalf("%s: sanitizeUploadRelPath(%q) 应被拒, got %q", c.name, c.in, got)
		}
	}
}

// TestHandleUploadNestedCreatesDirTree 嵌套创建:单请求两个 part(d1/a.txt、d1/d2/b.txt)→
// 目录树建出、内容一致、文件权限按 getFolderMetaFn 注入的共享对齐(public → 0664)。
func TestHandleUploadNestedCreatesDirTree(t *testing.T) {
	root := setupFakePool(t)
	old := getFolderMetaFn
	getFolderMetaFn = func() []diskmgmt.FolderMeta {
		return []diskmgmt.FolderMeta{{Name: "public", Path: filepath.Join(root, "public")}}
	}
	t.Cleanup(func() { getFolderMetaFn = old })

	contentA := []byte("content a")
	contentB := []byte("content b")
	body, ct := buildMultipartMulti(t, []string{"d1/a.txt", "d1/d2/b.txt"}, [][]byte{contentA, contentB})

	req := httptest.NewRequest("POST", "/api/files/upload?path=public", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("嵌套上传应 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	gotA, err := os.ReadFile(filepath.Join(root, "public", "d1", "a.txt"))
	if err != nil {
		t.Fatalf("d1/a.txt 未落盘: %v", err)
	}
	if !bytes.Equal(gotA, contentA) {
		t.Fatalf("d1/a.txt 内容不一致, got %q want %q", gotA, contentA)
	}
	gotB, err := os.ReadFile(filepath.Join(root, "public", "d1", "d2", "b.txt"))
	if err != nil {
		t.Fatalf("d1/d2/b.txt 未落盘: %v", err)
	}
	if !bytes.Equal(gotB, contentB) {
		t.Fatalf("d1/d2/b.txt 内容不一致, got %q want %q", gotB, contentB)
	}

	for _, p := range []string{filepath.Join(root, "public", "d1", "a.txt"), filepath.Join(root, "public", "d1", "d2", "b.txt")} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if info.Mode().Perm() != 0664 {
			t.Fatalf("%s mode=%o, want 0664(public 共享对齐)", p, info.Mode().Perm())
		}
	}
}

// TestHandleUploadNewDirModeIs0755 新建目录 mode 显式 0755(chmod 后断言,umask 无关)。
func TestHandleUploadNewDirModeIs0755(t *testing.T) {
	root := setupFakePool(t)
	oldUmask := syscall.Umask(0077) // 0077 下 Mkdir(0755) 无显式 chmod 只得 0700——能真正锁住「chmod 消解 umask」行为(0022 测不出来,0755 本就不含 group/other 写位)
	t.Cleanup(func() { syscall.Umask(oldUmask) })

	body, ct := buildMultipartMulti(t, []string{"newdir/leaf.txt"}, [][]byte{[]byte("x")})
	req := httptest.NewRequest("POST", "/api/files/upload?path=public", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("上传应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	info, err := os.Stat(filepath.Join(root, "public", "newdir"))
	if err != nil {
		t.Fatalf("newdir 未创建: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("newdir 应为目录")
	}
	if info.Mode().Perm() != 0755 {
		t.Fatalf("新建目录 mode=%o, want 0755", info.Mode().Perm())
	}
}

// TestHandleUploadExistingDirPermsUntouched 已存在目录权限不动:预建 d1 为 0700 →
// 上传 d1/a.txt → d1 仍 0700(ensureUploadParents 对已存在目录绝不 chmod)。
func TestHandleUploadExistingDirPermsUntouched(t *testing.T) {
	root := setupFakePool(t)
	preexisting := filepath.Join(root, "public", "d1")
	if err := os.Mkdir(preexisting, 0700); err != nil {
		t.Fatalf("预建 d1 失败: %v", err)
	}

	body, ct := buildMultipartMulti(t, []string{"d1/a.txt"}, [][]byte{[]byte("x")})
	req := httptest.NewRequest("POST", "/api/files/upload?path=public", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("上传应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	info, err := os.Stat(preexisting)
	if err != nil {
		t.Fatalf("stat d1 失败: %v", err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("既有目录权限应未被改动, mode=%o, want 0700", info.Mode().Perm())
	}
}

// TestHandleUploadMiddleSegmentCollidesWithFile 中间段撞既有文件:预建文件 f.txt →
// 上传 f.txt/x.txt → 400,f.txt 无损。
func TestHandleUploadMiddleSegmentCollidesWithFile(t *testing.T) {
	root := setupFakePool(t)
	fPath := filepath.Join(root, "public", "f.txt")
	original := []byte("i am a file not a dir")
	if err := os.WriteFile(fPath, original, 0644); err != nil {
		t.Fatalf("预建 f.txt 失败: %v", err)
	}

	body, ct := buildMultipartMulti(t, []string{"f.txt/x.txt"}, [][]byte{[]byte("x")})
	req := httptest.NewRequest("POST", "/api/files/upload?path=public", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("中间段撞既有文件应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(fPath)
	if err != nil {
		t.Fatalf("f.txt 应仍存在: %v", err)
	}
	if !bytes.Equal(data, original) {
		t.Fatalf("f.txt 内容应未被改动, got %q want %q", data, original)
	}
}

// TestHandleUploadSanitizeRejectTableDriven 消毒拒绝表驱动(经完整 handler 路径,
// 断言均为 400,且目标目录内无任何残留写入)。
func TestHandleUploadSanitizeRejectTableDriven(t *testing.T) {
	longSeg := make([]byte, 256)
	for i := range longSeg {
		longSeg[i] = 'y'
	}
	cases := []struct {
		name     string
		filename string
	}{
		{"段为..", "d1/../x.txt"},
		{"段为.", "d1/./x.txt"},
		{"前导斜杠", "/etc/x.txt"},
		{"空段", "a//b.txt"},
		{"尾随斜杠", "a/"},
		{"控制字符段", "d1/\x01bad.txt"},
		{"段过长", "d1/" + string(longSeg)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := setupFakePool(t)
			body, ct := buildMultipartMulti(t, []string{c.filename}, [][]byte{[]byte("x")})
			req := httptest.NewRequest("POST", "/api/files/upload?path=public", body)
			req.Header.Set("Content-Type", ct)
			rec := httptest.NewRecorder()
			handleUpload(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: 应 400, got %d body=%s", c.name, rec.Code, rec.Body.String())
			}
			// 拒绝发生在任何写盘/建目录之前:全树与 setupFakePool 初始状态完全一致,零残留
			got := map[string]bool{}
			filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
				if err != nil {
					return nil // evil 符号链接指向池外,Lstat 语义下 Walk 不跟随,不应报错;兜底跳过
				}
				rel, _ := filepath.Rel(root, p)
				got[rel] = true
				return nil
			})
			for _, want := range []string{".", "public", "public/photos", "public/evil", "fm"} {
				if !got[want] {
					t.Fatalf("%s: 初始条目 %s 意外消失", c.name, want)
				}
			}
			if len(got) != 5 {
				extra := []string{}
				for k := range got {
					switch k {
					case ".", "public", "public/photos", "public/evil", "fm":
					default:
						extra = append(extra, k)
					}
				}
				t.Fatalf("%s: 400 后出现残留写入: %v", c.name, extra)
			}
		})
	}
}

// TestHandleUploadNestedConflictFullRelPath 冲突:目标子路径文件已存在 → 409 且
// body conflict 含完整相对路径;overwrite=yes → 覆盖成功。
func TestHandleUploadNestedConflictFullRelPath(t *testing.T) {
	root := setupFakePool(t)
	if err := os.MkdirAll(filepath.Join(root, "public", "d1"), 0755); err != nil {
		t.Fatalf("预建 d1 失败: %v", err)
	}
	original := []byte("existing nested content")
	if err := os.WriteFile(filepath.Join(root, "public", "d1", "a.txt"), original, 0644); err != nil {
		t.Fatalf("预建冲突文件失败: %v", err)
	}

	body, ct := buildMultipartMulti(t, []string{"d1/a.txt"}, [][]byte{[]byte("new content")})
	req := httptest.NewRequest("POST", "/api/files/upload?path=public", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	handleUpload(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("嵌套路径冲突应 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非 JSON: %v", err)
	}
	if resp["conflict"] != "d1/a.txt" {
		t.Fatalf("conflict 应为完整相对路径 d1/a.txt, got %v", resp["conflict"])
	}
	data, err := os.ReadFile(filepath.Join(root, "public", "d1", "a.txt"))
	if err != nil {
		t.Fatalf("原文件应仍存在: %v", err)
	}
	if !bytes.Equal(data, original) {
		t.Fatalf("原文件内容应未被改动, got %q want %q", data, original)
	}

	// overwrite=yes 覆盖成功
	body2, ct2 := buildMultipartMulti(t, []string{"d1/a.txt"}, [][]byte{[]byte("overwritten content")})
	req2 := httptest.NewRequest("POST", "/api/files/upload?path=public&overwrite=yes", body2)
	req2.Header.Set("Content-Type", ct2)
	rec2 := httptest.NewRecorder()
	handleUpload(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("overwrite=yes 应 200, got %d body=%s", rec2.Code, rec2.Body.String())
	}
	data2, err := os.ReadFile(filepath.Join(root, "public", "d1", "a.txt"))
	if err != nil {
		t.Fatalf("覆盖后文件应存在: %v", err)
	}
	if string(data2) != "overwritten content" {
		t.Fatalf("覆盖后内容不符, got %q", data2)
	}
}
