package filemgmt

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

// ---------------------------------------------------------------------------
// parseMode
// ---------------------------------------------------------------------------

func TestParseMode(t *testing.T) {
	cases := []struct {
		in      string
		want    os.FileMode
		wantErr bool
	}{
		{"0644", 0644, false},
		{"755", 0755, false},
		{"0755", 0755, false},
		{"0777", 0777, false},
		{"9999", 0, true},
		{"rwx", 0, true},
		{"1755", 0, true}, // setuid 位不放行,>0777
		{"", 0, true},
		{"06440", 0, true}, // 5 位数
	}
	for _, c := range cases {
		got, err := parseMode(c.in)
		if c.wantErr {
			if err == nil {
				t.Fatalf("parseMode(%q) 应报错, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseMode(%q) 应成功, got err=%v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("parseMode(%q)=%o, want %o", c.in, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// resolveOwnerGroup
// ---------------------------------------------------------------------------

func TestResolveOwnerGroupKnownUser(t *testing.T) {
	root := setupFakePool(t)
	target := filepath.Join(root, "public", "owned.txt")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("Lstat 失败: %v", err)
	}
	owner, group := resolveOwnerGroup(info)
	if owner == "" {
		t.Fatalf("owner 不应为空")
	}
	if group == "" {
		t.Fatalf("group 不应为空")
	}
	// 当前进程 euid 对应的 owner 必须能解析出非空字符串(用户名或数字回退都算非空)。
	stat := info.Sys().(*syscall.Stat_t)
	if int(stat.Uid) != os.Geteuid() {
		t.Fatalf("测试环境假设:新建文件 uid 应等于当前进程 euid, got uid=%d euid=%d", stat.Uid, os.Geteuid())
	}
}

// ---------------------------------------------------------------------------
// handleStat
// ---------------------------------------------------------------------------

func statReq(path string) *http.Request {
	return httptest.NewRequest("GET", "/api/files/stat?path="+path, nil)
}

func TestHandleStatFile(t *testing.T) {
	root := setupFakePool(t)
	target := filepath.Join(root, "public", "f.txt")
	if err := os.WriteFile(target, []byte("hello"), 0640); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	rec := httptest.NewRecorder()
	handleStat(rec, statReq("public/f.txt"))
	if rec.Code != http.StatusOK {
		t.Fatalf("stat 文件应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非 JSON: %v", err)
	}
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("Lstat 失败: %v", err)
	}
	wantMode := "0" + strconv.FormatInt(int64(info.Mode().Perm()), 8)
	if resp["mode"] != wantMode {
		t.Fatalf("mode=%v want %v", resp["mode"], wantMode)
	}
	if resp["mode_text"] != info.Mode().String() {
		t.Fatalf("mode_text=%v want %v", resp["mode_text"], info.Mode().String())
	}
	if resp["recursive_possible"] != false {
		t.Fatalf("文件 recursive_possible 应为 false, got %v", resp["recursive_possible"])
	}
	wantOwner, wantGroup := resolveOwnerGroup(info)
	if resp["owner"] != wantOwner {
		t.Fatalf("owner=%v want %v", resp["owner"], wantOwner)
	}
	if resp["group"] != wantGroup {
		t.Fatalf("group=%v want %v", resp["group"], wantGroup)
	}
}

func TestHandleStatDirRecursivePossibleTrue(t *testing.T) {
	root := setupFakePool(t)
	os.MkdirAll(filepath.Join(root, "public", "subdir"), 0755)
	rec := httptest.NewRecorder()
	handleStat(rec, statReq("public/subdir"))
	if rec.Code != http.StatusOK {
		t.Fatalf("stat 目录应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["recursive_possible"] != true {
		t.Fatalf("目录 recursive_possible 应为 true, got %v", resp["recursive_possible"])
	}
}

func TestHandleStatSymlinkReportsSelf(t *testing.T) {
	root := setupFakePool(t)
	target := filepath.Join(root, "public", "real.txt")
	os.WriteFile(target, []byte("x"), 0600)
	link := filepath.Join(root, "public", "lnk")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("建符号链接失败: %v", err)
	}
	lstatInfo, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat 失败: %v", err)
	}
	rec := httptest.NewRecorder()
	handleStat(rec, statReq("public/lnk"))
	if rec.Code != http.StatusOK {
		t.Fatalf("stat 符号链接应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	wantMode := "0" + strconv.FormatInt(int64(lstatInfo.Mode().Perm()), 8)
	if resp["mode"] != wantMode {
		t.Fatalf("符号链接 mode 应是链接自身的 mode=%v, got %v", wantMode, resp["mode"])
	}
	if resp["recursive_possible"] != false {
		t.Fatalf("符号链接 recursive_possible 应为 false, got %v", resp["recursive_possible"])
	}
}

func TestHandleStatNotFound(t *testing.T) {
	setupFakePool(t)
	rec := httptest.NewRecorder()
	handleStat(rec, statReq("public/nope.txt"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在应 404, got %d", rec.Code)
	}
}

func TestHandleStatPoolRootAllowed(t *testing.T) {
	setupFakePool(t)
	rec := httptest.NewRecorder()
	handleStat(rec, statReq(""))
	if rec.Code != http.StatusOK {
		t.Fatalf("池根 stat 应放行 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["recursive_possible"] != true {
		t.Fatalf("池根是目录, recursive_possible 应为 true, got %v", resp["recursive_possible"])
	}
}

// ---------------------------------------------------------------------------
// handleChmod
// ---------------------------------------------------------------------------

func chmodReq(body interface{}) *http.Request {
	b, _ := json.Marshal(body)
	return httptest.NewRequest("POST", "/api/files/chmod", bytes.NewReader(b))
}

func TestHandleChmodSingleFile(t *testing.T) {
	root := setupFakePool(t)
	target := filepath.Join(root, "public", "f.txt")
	os.WriteFile(target, []byte("x"), 0644)
	rec := httptest.NewRecorder()
	handleChmod(rec, chmodReq(map[string]interface{}{"path": "public/f.txt", "mode": "0600", "recursive": false}))
	if rec.Code != http.StatusOK {
		t.Fatalf("chmod 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%o want 0600", info.Mode().Perm())
	}
}

func TestHandleChmodRecursiveSubtree(t *testing.T) {
	root := setupFakePool(t)
	dir := filepath.Join(root, "public", "tree")
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	f1 := filepath.Join(dir, "a.txt")
	f2 := filepath.Join(dir, "sub", "b.txt")
	os.WriteFile(f1, []byte("x"), 0644)
	os.WriteFile(f2, []byte("y"), 0644)

	rec := httptest.NewRecorder()
	handleChmod(rec, chmodReq(map[string]interface{}{"path": "public/tree", "mode": "0700", "recursive": true}))
	if rec.Code != http.StatusOK {
		t.Fatalf("recursive chmod 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	for _, p := range []string{dir, filepath.Join(dir, "sub"), f1, f2} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("Stat(%s) 失败: %v", p, err)
		}
		if info.Mode().Perm() != 0700 {
			t.Fatalf("%s mode=%o want 0700", p, info.Mode().Perm())
		}
	}
}

func TestHandleChmodRecursiveSkipsSymlinkTarget(t *testing.T) {
	root := setupFakePool(t)
	dir := filepath.Join(root, "public", "tree2")
	os.MkdirAll(dir, 0755)
	realFile := filepath.Join(root, "public", "realtarget.txt")
	os.WriteFile(realFile, []byte("x"), 0644)
	link := filepath.Join(dir, "lnk")
	if err := os.Symlink(realFile, link); err != nil {
		t.Fatalf("建符号链接失败: %v", err)
	}

	rec := httptest.NewRecorder()
	handleChmod(rec, chmodReq(map[string]interface{}{"path": "public/tree2", "mode": "0777", "recursive": true}))
	if rec.Code != http.StatusOK {
		t.Fatalf("recursive chmod 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	// 链接目标的 mode 必须保持原值(0644),递归时跳过符号链接。
	info, err := os.Stat(realFile)
	if err != nil {
		t.Fatalf("Stat(realFile) 失败: %v", err)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("符号链接目标 mode 被意外修改: got %o want 0644", info.Mode().Perm())
	}
	// 目录本身应被 chmod。
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat(dir) 失败: %v", err)
	}
	if dirInfo.Mode().Perm() != 0777 {
		t.Fatalf("目录 mode=%o want 0777", dirInfo.Mode().Perm())
	}
}

func TestHandleChmodSymlinkNonRecursiveRejected(t *testing.T) {
	root := setupFakePool(t)
	realFile := filepath.Join(root, "public", "realtarget2.txt")
	os.WriteFile(realFile, []byte("x"), 0644)
	link := filepath.Join(root, "public", "lnk2")
	os.Symlink(realFile, link)

	rec := httptest.NewRecorder()
	handleChmod(rec, chmodReq(map[string]interface{}{"path": "public/lnk2", "mode": "0600", "recursive": false}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非递归 chmod 符号链接应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleChmodPoolRootRejected(t *testing.T) {
	setupFakePool(t)
	rec := httptest.NewRecorder()
	handleChmod(rec, chmodReq(map[string]interface{}{"path": "", "mode": "0755", "recursive": false}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("池根 chmod 应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleChmodInvalidMode(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "f2.txt"), []byte("x"), 0644)
	rec := httptest.NewRecorder()
	handleChmod(rec, chmodReq(map[string]interface{}{"path": "public/f2.txt", "mode": "9999", "recursive": false}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 mode 应 400, got %d", rec.Code)
	}
}

func TestHandleChmodRejectsGet(t *testing.T) {
	setupFakePool(t)
	req := httptest.NewRequest("GET", "/api/files/chmod", nil)
	rec := httptest.NewRecorder()
	handleChmod(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应 405, got %d", rec.Code)
	}
}

func TestHandleChmodRecursiveOnFileRejected(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "f3.txt"), []byte("x"), 0644)
	rec := httptest.NewRecorder()
	handleChmod(rec, chmodReq(map[string]interface{}{"path": "public/f3.txt", "mode": "0700", "recursive": true}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("recursive=true 对文件应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleChmodNotFound(t *testing.T) {
	setupFakePool(t)
	rec := httptest.NewRecorder()
	handleChmod(rec, chmodReq(map[string]interface{}{"path": "public/nope.txt", "mode": "0644", "recursive": false}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在应 404, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleChown
// ---------------------------------------------------------------------------

func chownReq(body interface{}) *http.Request {
	b, _ := json.Marshal(body)
	return httptest.NewRequest("POST", "/api/files/chown", bytes.NewReader(b))
}

func TestHandleChownUnknownUserRejected(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "f.txt"), []byte("x"), 0644)
	rec := httptest.NewRecorder()
	handleChown(rec, chownReq(map[string]interface{}{"path": "public/f.txt", "owner": "nonexistent-user-xyz-123", "group": "", "recursive": false}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("不存在用户应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleChownUnknownGroupRejected(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "f.txt"), []byte("x"), 0644)
	rec := httptest.NewRecorder()
	handleChown(rec, chownReq(map[string]interface{}{"path": "public/f.txt", "owner": "", "group": "nonexistent-group-xyz-123", "recursive": false}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("不存在组应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleChownBothEmptyRejected(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "f.txt"), []byte("x"), 0644)
	rec := httptest.NewRecorder()
	handleChown(rec, chownReq(map[string]interface{}{"path": "public/f.txt", "owner": "", "group": "", "recursive": false}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("owner/group 全空应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleChownPoolRootRejected(t *testing.T) {
	setupFakePool(t)
	currentUser, _ := currentUsernameForTest()
	rec := httptest.NewRecorder()
	handleChown(rec, chownReq(map[string]interface{}{"path": "", "owner": currentUser, "group": "", "recursive": false}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("池根 chown 应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleChownRejectsGet(t *testing.T) {
	setupFakePool(t)
	req := httptest.NewRequest("GET", "/api/files/chown", nil)
	rec := httptest.NewRecorder()
	handleChown(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应 405, got %d", rec.Code)
	}
}

func TestHandleChownNotFound(t *testing.T) {
	setupFakePool(t)
	currentUser, _ := currentUsernameForTest()
	rec := httptest.NewRecorder()
	handleChown(rec, chownReq(map[string]interface{}{"path": "public/nope.txt", "owner": currentUser, "group": "", "recursive": false}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在应 404, got %d", rec.Code)
	}
}

func TestHandleChownRecursiveOnFileRejected(t *testing.T) {
	root := setupFakePool(t)
	os.WriteFile(filepath.Join(root, "public", "f4.txt"), []byte("x"), 0644)
	currentUser, _ := currentUsernameForTest()
	rec := httptest.NewRecorder()
	handleChown(rec, chownReq(map[string]interface{}{"path": "public/f4.txt", "owner": currentUser, "group": "", "recursive": true}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("recursive=true 对文件应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// currentUsernameForTest 返回当前进程 euid 对应的用户名,供需要"合法用户名"的
// 测试用例使用(校验分支测试不依赖 root,但仍需传一个 user.Lookup 能成功的名字)。
func currentUsernameForTest() (string, error) {
	return lookupCurrentUsername()
}

// --- root-gated 测试:只有 euid==0 时断言 chown/chmod 的系统调用生效 ---

func TestHandleChownSingleFileRootOnly(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("非 root 环境: chown 系统调用会 EPERM,跳过生效断言(生产同构机验收必测)")
	}
	root := setupFakePool(t)
	target := filepath.Join(root, "public", "f5.txt")
	os.WriteFile(target, []byte("x"), 0644)
	rec := httptest.NewRecorder()
	handleChown(rec, chownReq(map[string]interface{}{"path": "public/f5.txt", "owner": "nobody", "group": "", "recursive": false}))
	if rec.Code != http.StatusOK {
		t.Fatalf("chown 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("Lstat 失败: %v", err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	u, _ := lookupUIDByName("nobody")
	if int(stat.Uid) != u {
		t.Fatalf("owner 未生效: uid=%d want=%d", stat.Uid, u)
	}
}

func TestHandleChownRecursiveLchownSemanticsRootOnly(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("非 root 环境: chown 系统调用会 EPERM,跳过生效断言(生产同构机验收必测)")
	}
	root := setupFakePool(t)
	dir := filepath.Join(root, "public", "tree3")
	os.MkdirAll(dir, 0755)
	realFile := filepath.Join(root, "public", "realtarget3.txt")
	os.WriteFile(realFile, []byte("x"), 0644)
	link := filepath.Join(dir, "lnk3")
	os.Symlink(realFile, link)

	rec := httptest.NewRecorder()
	handleChown(rec, chownReq(map[string]interface{}{"path": "public/tree3", "owner": "nobody", "group": "", "recursive": true}))
	if rec.Code != http.StatusOK {
		t.Fatalf("recursive chown 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	linkInfo, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat(link) 失败: %v", err)
	}
	linkStat := linkInfo.Sys().(*syscall.Stat_t)
	nobodyUID, _ := lookupUIDByName("nobody")
	if int(linkStat.Uid) != nobodyUID {
		t.Fatalf("链接本身 owner 未变: uid=%d want=%d", linkStat.Uid, nobodyUID)
	}
	// 目标文件 owner 不应被改变(Lchown 不跟随链接)。
	targetInfo, err := os.Lstat(realFile)
	if err != nil {
		t.Fatalf("Lstat(target) 失败: %v", err)
	}
	targetStat := targetInfo.Sys().(*syscall.Stat_t)
	if int(targetStat.Uid) == nobodyUID {
		t.Fatalf("链接目标 owner 被意外修改为 nobody,应保持原值")
	}
}
