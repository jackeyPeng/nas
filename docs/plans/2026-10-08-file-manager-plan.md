# 面板文件管理器 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 NAS 面板内提供管理员视角的整池文件管理(浏览/上传/下载/移动/复制/删除/改权限),取代 FileBrowser。

**Architecture:** 新增 `web/modules/filemgmt/` Go 模块,REST API 全部挂 `common.AuthMiddleware`,路径参数一律池根相对路径、经唯一安全入口 `resolvePoolPath` 锚定 `/data/nas1`。前端新增 `files` 导航页(Alpine.js + 既有 CSS 体系)。删除落既有 `.recycle/<user>/` 与回收站页同源联动。

**Tech Stack:** Go 1.x(net/http + archive/zip + io 流式)、Alpine.js、i18n JSON(zh-CN 真相源)。

**Spec:** `docs/plans/2026-10-08-file-manager-design.md`(本计划从 spec 推导,执行者两份都要读)

## Global Constraints

- 模块根是 `web/`(go.mod 在此);构建验证 `cd web && go build ./...`;仓库根目录不在 go module 内,**任何 .go 都不许放仓库根**
- 池根常量 `/data/nas1`(与 diskmgmt/import.go:115、config_sync.go:832 一致);文件管理器**只允许池内**,比 `common.ValidateDataPath`(允许整个 /data/)更严
- 写操作(非 GET `/api/*`)由 main.go loggingMiddleware 自动审计,handler 内**不要**再调 LogAudit,只在需要富 detail 时调 `common.LogAuditRequest(r, category, action, detail, result)`(它打 ctx flag 防双写)
- nas-panel 进程以 root 跑(service 无 User=):chmod/chown 用 `os.Chmod`/`os.Chown`(免 sudo);sudoers 白名单有 `chown -R` 但**没有** `chmod -R`,递归 chmod 必须 filepath.Walk + os.Chmod
- 测试坑(照抄 diskmgmt 惯例):audit.db 测试用包级持久目录 + TestMain 清理,**不用 t.TempDir()**;LogAudit 异步落库,断言前 10ms 轮询等待
- 单测必须调真函数,禁止在测试体里复刻 if/else
- i18n:所有前端文案走 `$t('files.*')`,zh-CN.json 先写、en-US.json 同步,`python3 scripts/check-i18n.py` 必须过
- 提交节奏:每 Task 一个 commit;提交前 `cd web && go build ./... && go test ./modules/filemgmt/ ./modules/diskmgmt/ -count=1`
- 前端依赖零新增(不引 CDN、不引新 JS 库)
- 红线:代码/前端/脚本里不得出现真实内网 IP(10.216./10.187./192.168.213.)与真实密码

---

## File Structure

```
web/modules/filemgmt/
├── filemgmt.go      # RegisterRoutes + 路由表(唯一注册点)
├── path.go          # resolvePoolPath / poolRoot / ownerShareOf(安全核心)
├── path_test.go     # 穿越攻击用例全集
├── browse.go        # handleList(列目录/面包屑/truncated)
├── browse_test.go
├── transfer.go      # handleDownload / handleZip / handleUpload
├── transfer_test.go
├── mutate.go        # handleMkdir / handleRename / handleMove / handleCopy / handleDelete
├── mutate_test.go
├── perms.go         # handleStat / handleChmod / handleChown / handleFixHomePerms
├── perms_test.go
└── preview.go       # handlePreview / handleUsage

web/main.go                    # +1 行 filemgmt.RegisterRoutes(mux)
web/frontend/index.html        # 导航项 + files 页 div(替换 FileBrowser 外链)
web/frontend/app.js            # filesPage 状态 + 方法
web/frontend/i18n/zh-CN.json   # files.* 文案(真相源)
web/frontend/i18n/en-US.json   # files.* 文案(同步)
web/frontend/styles.css(或既有样式文件)  # 仅在缺组件时补,优先复用 .table-dense/.empty-state/.badge-*
```

职责边界:`path.go` 是唯一安全入口,其他所有 handler 的第一个动作必须是 `resolvePoolPath`;`browse/transfer/mutate/perms` 各自只管一类操作,互相不 import 对方内部函数(共享件放 path.go/filemgmt.go)。

---

## M1 — v1 只读基线

### Task 1: path.go 安全核心(TDD)

**Files:**
- Create: `web/modules/filemgmt/path.go`
- Test: `web/modules/filemgmt/path_test.go`

**Interfaces:**
- Produces:
  - `func poolRoot() string` → `/data/nas1`(测试用 `poolRootOverride` 包变量可替换为 t.TempDir,见实现)
  - `func resolvePoolPath(rel string) (string, error)` — rel 是池根相对 POSIX 路径(如 `public/photos`),返回池内绝对路径;拒绝空(仅 `""` 返回池根本身,合法)、绝对路径、`..`、符号链接逃逸、池外
  - `func ownerShareOf(absPath string) (diskmgmt.FolderMeta, bool)` — folders.db 中 Path 前缀最长匹配的共享(删除时定位 .recycle 用)
  - `var poolRootOverride string`(仅测试置位;空则用 `/data/nas1`)

- [ ] **Step 1: 写失败测试**

```go
package filemgmt

import (
	"os"
	"path/filepath"
	"testing"
)

func setupFakePool(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "public", "photos"), 0755)
	os.MkdirAll(filepath.Join(root, "fm"), 0700)
	os.Symlink("/etc", filepath.Join(root, "public", "evil")) // 指向池外
	poolRootOverride = root
	t.Cleanup(func() { poolRootOverride = "" })
	return root
}

func TestResolvePoolPathAcceptsLegal(t *testing.T) {
	root := setupFakePool(t)
	for _, rel := range []string{"", "public", "public/photos", "fm", "./public"} {
		got, err := resolvePoolPath(rel)
		if err != nil {
			t.Fatalf("resolvePoolPath(%q) 应合法, got err=%v", rel, err)
		}
		want := filepath.Clean(filepath.Join(root, rel))
		if got != want {
			t.Fatalf("resolvePoolPath(%q) = %q, want %q", rel, got, want)
		}
	}
}

func TestResolvePoolPathRejectsTraversal(t *testing.T) {
	setupFakePool(t)
	bad := []string{
		"..", "../..", "public/../../etc", "/etc/passwd", "/data/nas1/public",
		"public/../../../root", "..%2fetc", "public/./../../x",
	}
	for _, rel := range bad {
		if got, err := resolvePoolPath(rel); err == nil {
			t.Fatalf("resolvePoolPath(%q) 应被拒, got %q", rel, got)
		}
	}
}

func TestResolvePoolPathRejectsSymlinkEscape(t *testing.T) {
	setupFakePool(t)
	if got, err := resolvePoolPath("public/evil/passwd"); err == nil {
		t.Fatalf("符号链接逃逸应被拒, got %q", got)
	}
	if got, err := resolvePoolPath("public/evil"); err == nil {
		t.Fatalf("指向池外的符号链接本身应被拒, got %q", got)
	}
}

func TestResolvePoolPathKeepsInnerSymlink(t *testing.T) {
	root := setupFakePool(t)
	os.Symlink(filepath.Join(root, "fm"), filepath.Join(root, "public", "inner"))
	if _, err := resolvePoolPath("public/inner"); err != nil {
		t.Fatalf("池内符号链接应放行: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && go test ./modules/filemgmt/ -run TestResolvePoolPath -count=1`
Expected: 编译失败 `undefined: resolvePoolPath / poolRootOverride`

- [ ] **Step 3: 最小实现**

```go
package filemgmt

import (
	"fmt"
	"path/filepath"
	"strings"
)

// poolRootOverride 仅供测试注入;生产为空,走默认池根(与 diskmgmt 一致)。
var poolRootOverride string

func poolRoot() string {
	if poolRootOverride != "" {
		return poolRootOverride
	}
	return "/data/nas1"
}

// resolvePoolPath 把池根相对路径锚定为池内绝对路径。
// 这是 filemgmt 唯一的路径入口,所有 handler 第一步必须调它。
// 拒绝:绝对路径、".." 片段(含 URL 编码残留)、符号链接解析后逃出池根。
// 允许:空串(=池根)、"." 片段、池内符号链接。
func resolvePoolPath(rel string) (string, error) {
	root := filepath.Clean(poolRoot())
	rel = strings.TrimSpace(rel)
	if strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("不接受绝对路径")
	}
	// 先按字面拒绝任何 ".." 片段(Clean 会消解它,这里故意不消解——
	// 请求里出现 ".." 即视为攻击,不做"善意解释")
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return "", fmt.Errorf("路径包含非法片段")
		}
	}
	full := filepath.Clean(filepath.Join(root, rel))
	if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return "", fmt.Errorf("路径越界")
	}
	// 符号链接逃逸:对已存在的部分逐级 EvalSymlinks
	if resolved, err := filepath.EvalSymlinks(full); err == nil {
		if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
			return "", fmt.Errorf("符号链接指向池外")
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("路径解析失败: %v", err)
	}
	return full, nil
}
```

(实现文件需 `import "os"`;`os.IsNotExist` 判断"目标尚不存在"是合法场景——mkdir/上传的目标路径。)

`ownerShareOf` 放同文件:

```go
import "nas-panel/modules/diskmgmt"

// ownerShareOf 返回 absPath 所属共享(folders.db Path 前缀最长匹配)。
// 删除时定位对应 .recycle;无匹配返回 false(池根散落文件)。
func ownerShareOf(absPath string) (diskmgmt.FolderMeta, bool) {
	var best diskmgmt.FolderMeta
	found := false
	for _, m := range diskmgmt.GetAllFolderMeta() {
		prefix := filepath.Clean(m.Path) + string(filepath.Separator)
		if strings.HasPrefix(absPath+string(filepath.Separator), prefix) ||
			filepath.Clean(m.Path) == absPath {
			if !found || len(m.Path) > len(best.Path) {
				best, found = m, true
			}
		}
	}
	return best, found
}
```

注意:若 `diskmgmt.GetAllFolderMeta` 触发 folders.db 打开失败(测试环境无 DB),它返回空 slice(非 nil,既有约定),ownerShareOf 返回 false——测试里不依赖真 DB。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && go test ./modules/filemgmt/ -run TestResolvePoolPath -count=1 -v`
Expected: 4 个测试全 PASS

- [ ] **Step 5: Commit**

```bash
git add web/modules/filemgmt/path.go web/modules/filemgmt/path_test.go
git commit -m "feat(filemgmt): 路径锚定安全核心 resolvePoolPath — 拒绝穿越/绝对路径/符号链接逃逸"
```

---

### Task 2: browse.go 列目录

**Files:**
- Create: `web/modules/filemgmt/browse.go`
- Test: `web/modules/filemgmt/browse_test.go`
- Create: `web/modules/filemgmt/filemgmt.go`(本 Task 先只注册 list,后续 Task 追加)

**Interfaces:**
- Consumes: Task 1 的 `resolvePoolPath`
- Produces:
  - `type FileEntry struct`(JSON: name/type/size/mode/mode_text/owner/group/mtime/target/truncated 见下)
  - `func listDir(absPath string) ([]FileEntry, bool /*truncated*/, error)` — 排序:目录在前,同类按名称;上限 `maxListEntries = 2000`
  - `func handleList(w, r)` — GET `/api/files/list?path=`;响应 `{"path": rel, "entries": [...], "truncated": bool, "breadcrumb": [...], "pool_usage": {...}}`
  - `func RegisterRoutes(mux *http.ServeMux)`

- [ ] **Step 1: 写失败测试**

```go
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
	// 目录在前(zdir),文件按名称(a.txt, b.txt);evil 符号链接排文件区
	if entries[0].Name != "zdir" || entries[0].Type != "dir" {
		t.Fatalf("首项应为目录 zdir, got %+v", entries[0])
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
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && go test ./modules/filemgmt/ -run 'TestListDir|TestHandleList' -count=1`
Expected: 编译失败 `undefined: listDir / handleList`

- [ ] **Step 3: 实现**

```go
package filemgmt

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"nas-panel/common"
)

const maxListEntries = 2000

type FileEntry struct {
	Name     string `json:"name"`
	Type     string `json:"type"`      // file/dir/symlink/other
	Size     int64  `json:"size"`
	Mode     string `json:"mode"`      // 八进制如 "0644"
	ModeText string `json:"mode_text"` // 符号如 "-rw-r--r--"
	Owner    string `json:"owner"`
	Group    string `json:"group"`
	MTime    int64  `json:"mtime"` // unix 秒
	Target   string `json:"target,omitempty"` // symlink 目标(展示用,不跟随)
}

func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/files/list", common.AuthMiddleware(handleList))
	// 后续 Task 追加: download/zip/preview/usage/upload/mkdir/rename/move/copy/delete/stat/chmod/chown/fix-home-perms
}

func handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel := r.FormValue("path")
	abs, err := resolvePoolPath(rel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	entries, truncated, err := listDir(abs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	common.JSONResponse(w, map[string]interface{}{
		"path":       filepath.Clean("/" + rel),
		"entries":    entries,
		"truncated":  truncated,
		"breadcrumb": breadcrumb(rel),
	})
}
```

```go
func listDir(abs string) ([]FileEntry, bool, error) {
	dirEntries, err := os.ReadDir(abs)
	if err != nil {
		return nil, false, err
	}
	truncated := false
	if len(dirEntries) > maxListEntries {
		dirEntries = dirEntries[:maxListEntries]
		truncated = true
	}
	out := make([]FileEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		info, err := de.Info() // Lstat 语义,不跟随符号链接
		if err != nil {
			continue // 竞态删除的条目跳过
		}
		e := FileEntry{
			Name:     de.Name(),
			Size:     info.Size(),
			Mode:     "0" + strconv.FormatInt(int64(info.Mode().Perm()), 8),
			ModeText: info.Mode().String(),
			MTime:    info.ModTime().Unix(),
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			e.Type = "symlink"
			if tgt, terr := os.Readlink(filepath.Join(abs, de.Name())); terr == nil {
				e.Target = tgt
			}
		case info.IsDir():
			e.Type = "dir"
			e.Size = 0
		case info.Mode().IsRegular():
			e.Type = "file"
		default:
			e.Type = "other"
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			if u, uerr := user.LookupId(strconv.Itoa(int(stat.Uid))); uerr == nil {
				e.Owner = u.Username
			} else {
				e.Owner = strconv.Itoa(int(stat.Uid))
			}
			if g, gerr := user.LookupGroupId(strconv.Itoa(int(stat.Gid))); gerr == nil {
				e.Group = g.Name
			} else {
				e.Group = strconv.Itoa(int(stat.Gid))
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := out[i].Type == "dir", out[j].Type == "dir"
		if di != dj {
			return di // 目录在前
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, truncated, nil
}

// breadcrumb 把相对路径切成 [{"name":"池根","path":""},{"name":"public","path":"public"}...]
func breadcrumb(rel string) []map[string]string {
	bc := []map[string]string{{"name": "池根", "path": ""}}
	rel = strings.Trim(filepath.Clean("/"+rel), "/")
	if rel == "" || rel == "." {
		return bc
	}
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		bc = append(bc, map[string]string{"name": p, "path": strings.Join(parts[:i+1], "/")})
	}
	return bc
}
```

(池根的 i18n 名称由前端用 `$t('files.pool_root')` 渲染,breadcrumb 里的 "池根" 仅作兜底——实现时后端返回 `name:""` 给池根,前端判空显示 i18n 文案。)

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && go test ./modules/filemgmt/ -count=1 -v`
Expected: 全 PASS(含 Task 1 的 4 个)

- [ ] **Step 5: main.go 接线**

`web/main.go` 第 138 行 `events.RegisterRoutes(mux)` 之后加:

```go
	filemgmt.RegisterRoutes(mux)
```

import 块加 `"nas-panel/modules/filemgmt"`。

Run: `cd web && go build ./...`
Expected: 编译通过

- [ ] **Step 6: Commit**

```bash
git add web/modules/filemgmt/ web/main.go
git commit -m "feat(filemgmt): 列目录 API — 排序/属主/权限位/符号链接标注/2000 条截断"
```

---

### Task 3: transfer.go 下载(单文件 + 目录 zip 流)

**Files:**
- Create: `web/modules/filemgmt/transfer.go`
- Test: `web/modules/filemgmt/transfer_test.go`
- Modify: `web/modules/filemgmt/filemgmt.go`(RegisterRoutes 追加 2 行)

**Interfaces:**
- Consumes: `resolvePoolPath`
- Produces:
  - `func handleDownload(w, r)` — GET `/api/files/download?path=`;文件用 `http.ServeContent`(支持 Range);目录返回 400(目录走 zip)
  - `func handleZip(w, r)` — GET `/api/files/zip?path=`;`archive/zip` 流式写 ResponseWriter,不落地临时文件;Content-Disposition attachment 文件名=目录名.zip
  - `func zipDir(w io.Writer, absRoot, zipPrefix string) error`(可单测的纯逻辑件)

- [ ] **Step 1: 写失败测试**

```go
package filemgmt

import (
	"archive/zip"
	"bytes"
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
		b, _ := readAll(rc)
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
```

(测试里 `readAll` 直接用 `io.ReadAll`,import "io" 后替换。)

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && go test ./modules/filemgmt/ -run 'TestHandleDownload|TestZipDir|TestHandleZip' -count=1`
Expected: 编译失败 `undefined: handleDownload / handleZip / zipDir`

- [ ] **Step 3: 实现**

```go
package filemgmt

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	abs, err := resolvePoolPath(r.FormValue("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		http.Error(w, "文件不存在", http.StatusNotFound)
		return
	}
	if info.IsDir() {
		http.Error(w, "目录请用 zip 下载", http.StatusBadRequest)
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		http.Error(w, "打开失败", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, escapeFilename(info.Name())))
	http.ServeContent(w, r, info.Name(), info.ModTime(), f) // Range/Content-Type 自动
}

// escapeFilename 处理 Content-Disposition 文件名的引号与非 ASCII(RFC 5987)。
func escapeFilename(name string) string {
	if strings.ContainsRune(name, '"') {
		name = strings.ReplaceAll(name, `"`, `'`)
	}
	return name
}

func handleZip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel := r.FormValue("path")
	abs, err := resolvePoolPath(rel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		http.Error(w, "目录不存在", http.StatusNotFound)
		return
	}
	base := filepath.Base(abs)
	if rel == "" || filepath.Clean("/"+rel) == "/" {
		base = "nas1" // 池根打包
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s.zip"`, escapeFilename(base)))
	if err := zipDir(w, abs, base); err != nil {
		// header 已发出,只能断流;审计由中间件按状态码记录
		return
	}
}

// zipDir 流式打包 absRoot 到 w,zip 内路径前缀 zipPrefix。不落地临时文件。
func zipDir(w io.Writer, absRoot, zipPrefix string) error {
	zw := zip.NewWriter(w)
	defer zw.Close()
	return filepath.Walk(absRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 跳过不可读条目(竞态/权限),不中断整包
		}
		rel, rerr := filepath.Rel(absRoot, p)
		if rerr != nil {
			return nil
		}
		zipName := filepath.ToSlash(filepath.Join(zipPrefix, rel))
		// 符号链接只存链接本身信息,不跟随(防池外内容进包)
		if info.Mode()&os.ModeSymlink != 0 {
			tgt, terr := os.Readlink(p)
			if terr != nil {
				return nil
			}
			hdr := &zip.FileHeader{Name: zipName, Method: zip.Store}
			hdr.SetMode(info.Mode())
			fw, ferr := zw.CreateHeader(hdr)
			if ferr != nil {
				return nil
			}
			fw.Write([]byte(tgt))
			return nil
		}
		if info.IsDir() {
			if rel == "." {
				return nil
			}
			_, cerr := zw.Create(zipName + "/")
			return cerr
		}
		hdr := &zip.FileHeader{Name: zipName, Method: zip.Deflate}
		hdr.SetMode(info.Mode())
		hdr.Modified = info.ModTime()
		fw, ferr := zw.CreateHeader(hdr)
		if ferr != nil {
			return ferr
		}
		src, oerr := os.Open(p)
		if oerr != nil {
			return nil // 打开失败跳过该文件
		}
		defer src.Close()
		_, cerr := io.Copy(fw, src)
		return cerr
	})
}
```

- [ ] **Step 4: 跑测试确认通过 + RegisterRoutes 追加**

filemgmt.go RegisterRoutes 加:

```go
	mux.HandleFunc("/api/files/download", common.AuthMiddleware(handleDownload))
	mux.HandleFunc("/api/files/zip", common.AuthMiddleware(handleZip))
```

Run: `cd web && go build ./... && go test ./modules/filemgmt/ -count=1`
Expected: 全 PASS

- [ ] **Step 5: Commit**

```bash
git add web/modules/filemgmt/
git commit -m "feat(filemgmt): 文件下载(ServeContent+Range)与目录 zip 流式打包(符号链接不跟随)"
```

---

### Task 4: preview.go + usage

**Files:**
- Create: `web/modules/filemgmt/preview.go`
- Test: `web/modules/filemgmt/preview_test.go`
- Modify: `web/modules/filemgmt/filemgmt.go`(注册 2 路由)

**Interfaces:**
- Produces:
  - `const maxTextPreview = 1 << 20`(1MB)、`const maxImagePreview = 10 << 20`(10MB)
  - `func handlePreview(w, r)` — GET `/api/files/preview?path=`;文本(按扩展名白名单 .txt/.md/.json/.yaml/.yml/.log/.conf/.ini/.csv/.sh)返回 `{"kind":"text","content":...,"truncated":bool}`;图片(.jpg/.jpeg/.png/.gif/.webp/.bmp)直接 ServeContent;**SVG 一律拒绝**(XSS,对齐 spec §十四.5)返回 415;其他类型 415 `{"kind":"unsupported"}`
  - `func handleUsage(w, r)` — GET `/api/files/usage?path=`;df 池挂载点总量/已用 + 当前目录 du -sb(超时 5s,超时返回 `"du":null,"du_status":"timeout"`)

- [ ] **Step 1: 写失败测试**(文本预览/截断/SVG 拒绝/usage 结构)

```go
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
```

- [ ] **Step 2: 确认失败** — `cd web && go test ./modules/filemgmt/ -run TestPreview -count=1` → `undefined: handlePreview`

- [ ] **Step 3: 实现**(要点)

```go
var textExts = map[string]bool{".txt": true, ".md": true, ".json": true, ".yaml": true,
	".yml": true, ".log": true, ".conf": true, ".ini": true, ".csv": true, ".sh": true}
var imageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".webp": true, ".bmp": true}

func handlePreview(w http.ResponseWriter, r *http.Request) {
	// GET 校验 + resolvePoolPath + os.Stat(同 handleDownload 前置,400/404)
	ext := strings.ToLower(filepath.Ext(abs))
	switch {
	case ext == ".svg": // 显式拒绝:SVG 可内联脚本,预览即存储型 XSS
		http.Error(w, "SVG 不支持预览,请下载", http.StatusUnsupportedMediaType)
		return
	case imageExts[ext]:
		if info.Size() > maxImagePreview {
			http.Error(w, "图片过大,请下载", http.StatusUnsupportedMediaType)
			return
		}
		// ServeContent;Content-Type 由扩展名嗅探
	case textExts[ext]:
		f, _ := os.Open(abs)
		defer f.Close()
		buf := make([]byte, maxTextPreview+1)
		n, _ := io.ReadFull(f, buf)
		truncated := n > maxTextPreview
		content := string(buf[:min(n, maxTextPreview)])
		common.JSONResponse(w, map[string]interface{}{
			"kind": "text", "content": content, "truncated": truncated})
		return
	default:
		http.Error(w, "该类型不支持预览", http.StatusUnsupportedMediaType)
		return
	}
}
```

handleUsage:df 用 `common.SudoOutput` 免 sudo 也可(面板 root,直接 `exec.Command("df", "--output=size,used,avail,pcent", mountpoint)`);du 用 `exec.CommandContext`(5s 超时)`du -sb --apparent-size <abs>`,超时返回 du:null + du_status:"timeout"。响应 `{"pool":{"total_kb":...,"used_kb":...,"avail_kb":...,"use_pct":"42%"},"dir":{"bytes":123,"du_status":"ok"}}`。

- [ ] **Step 4: 测试过 + 注册路由 + build** — RegisterRoutes 加 preview/usage 两行;`cd web && go build ./... && go test ./modules/filemgmt/ -count=1` 全绿

- [ ] **Step 5: Commit** — `git commit -m "feat(filemgmt): 文本/图片预览(SVG 拒绝防 XSS)+ 池/目录用量(du 超时兜底)"`

---

### Task 5: 前端 files 页(v1 只读)

**Files:**
- Modify: `web/frontend/index.html`(导航 2 处 + 新 page div + 替换 FileBrowser 外链)
- Modify: `web/frontend/app.js`(filesPage 状态 + loadFiles/downloadFile/previewFile 方法)
- Modify: `web/frontend/i18n/zh-CN.json`、`web/frontend/i18n/en-US.json`(files.* 文案)

**Interfaces:**
- Consumes: Task 2-4 的 4 个 GET 端点
- Produces: 可用的 files 页(浏览/下载/预览)

- [ ] **Step 1: 导航项替换 FileBrowser 外链**

index.html:42(移动端菜单)当前是:

```html
<a :href="'http://'+location.hostname+':8081'" target="_blank" ...>📁 <span x-text="$t('nav.file_manager')"></span></a>
```

改为(复用既有 `nav.file_manager` key,文案不变,行为改内部导航):

```html
<a href="#" @click.prevent="navigate('files'); mobileMenu=false" style="display:block;padding:var(--sp-3);background: var(--primary);color:white;text-align:center;border-radius:var(--radius-sm);text-decoration:none;font-weight:600">📁 <span x-text="$t('nav.file_manager')"></span></a>
```

侧边栏(index.html:115 附近,diskmgmt 的 `<li>` 旁)加同款 `<li :class="{active: page === 'files'}" @click="navigate('files')">📁 $t('nav.file_manager')</li>`(结构照抄相邻 li)。

- [ ] **Step 2: files 页 div**

在 diskmgmt 页 div(index.html:1188)之后加(骨架,样式类全部复用既有):

```html
<div x-show="page === 'files'" class="page" x-init="loadFiles('')" x-transition.opacity.duration.300ms>
    <!-- 面包屑 + 用量条 -->
    <div style="display:flex;align-items:center;gap:var(--sp-2);flex-wrap:wrap;margin-bottom:var(--sp-3)">
        <template x-for="(bc, i) in filesPage.breadcrumb" :key="i">
            <span>
                <a href="#" @click.prevent="loadFiles(bc.path)" style="color:var(--primary);text-decoration:none"
                   x-text="bc.path === '' ? $t('files.pool_root') : bc.name"></a>
                <span x-show="i < filesPage.breadcrumb.length - 1" style="color:var(--text-light)"> / </span>
            </span>
        </template>
        <span style="margin-left:auto;color:var(--text-light)" x-text="filesPage.usageText"></span>
    </div>
    <!-- 文件表 -->
    <table class="table-dense" style="width:100%">
        <thead><tr>
            <th x-text="$t('files.name')"></th><th x-text="$t('files.size')"></th>
            <th x-text="$t('files.owner')"></th><th x-text="$t('files.mode')"></th>
            <th x-text="$t('files.mtime')"></th><th x-text="$t('files.actions')"></th>
        </tr></thead>
        <tbody>
            <template x-for="e in filesPage.entries" :key="e.name">
                <tr>
                    <td>
                        <a href="#" x-show="e.type==='dir'" @click.prevent="loadFiles(filesPage.relJoin(e.name))"
                           style="color:var(--text-primary);text-decoration:none">📁 <span x-text="e.name"></span></a>
                        <span x-show="e.type==='file'">📄 <span x-text="e.name"></span></span>
                        <span x-show="e.type==='symlink'" :title="e.target">🔗 <span x-text="e.name"></span> → <span x-text="e.target" style="color:var(--text-light)"></span></span>
                        <span x-show="e.type==='other'">• <span x-text="e.name"></span></span>
                    </td>
                    <td x-text="e.type==='dir' ? '—' : fmtSize(e.size)"></td>
                    <td x-text="e.owner + ':' + e.group"></td>
                    <td><code x-text="e.mode_text"></code></td>
                    <td x-text="fmtTime(e.mtime)"></td>
                    <td>
                        <button class="btn-xs" x-show="e.type!=='dir'" @click="downloadFile(e)" x-text="$t('files.download')"></button>
                        <button class="btn-xs" x-show="previewable(e)" @click="previewFile(e)" x-text="$t('files.preview')"></button>
                    </td>
                </tr>
            </template>
        </tbody>
    </table>
    <div class="empty-state" x-show="filesPage.entries.length === 0 && !filesPage.loading" x-cloak>
        <div x-text="$t('files.empty_dir')"></div>
    </div>
    <div x-show="filesPage.truncated" style="color:var(--warning);margin-top:var(--sp-2)" x-text="$t('files.truncated_hint')"></div>
    <!-- 预览弹层 -->
    <div x-show="filesPage.preview.open" style="position:fixed;inset:0;background:rgba(0,0,0,.5);z-index:100" @click.self="filesPage.preview.open=false">
        <div style="max-width:80vw;max-height:80vh;margin:5vh auto;background:var(--card-bg);border-radius:var(--radius-md);padding:var(--sp-4);overflow:auto">
            <div style="display:flex;justify-content:space-between;margin-bottom:var(--sp-2)">
                <b x-text="filesPage.preview.name"></b>
                <button class="btn-xs" @click="filesPage.preview.open=false">✕</button>
            </div>
            <pre x-show="filesPage.preview.kind==='text'" style="white-space:pre-wrap;word-break:break-all;font-size:var(--fs-sm)" x-text="filesPage.preview.content"></pre>
            <img x-show="filesPage.preview.kind==='image'" :src="filesPage.preview.url" style="max-width:100%">
        </div>
    </div>
</div>
```

注意:文本预览用 `x-text`(自动转义,防 XSS);图片 `src` 指向 `/api/files/preview?path=...`(带认证 cookie/token 的方式与既有 download 一致——查 app.js 里既有认证请求怎么带 token,照抄同款 fetch 封装,预览图片改用 blob URL:fetch → blob → URL.createObjectURL)。

- [ ] **Step 3: app.js 状态与方法**

在 Alpine data 里加(位置照抄 diskmgmt 状态附近):

```javascript
filesPage: {
    path: '', entries: [], breadcrumb: [], truncated: false, loading: false,
    usageText: '',
    preview: { open: false, kind: '', name: '', content: '', url: '' },
    relJoin(name) { return this.path ? this.path + '/' + name : name; },
},
```

方法区加(认证 fetch 封装照抄 app.js 既有 `api()`/`fetch` 模式——先 `grep -n 'Authorization\|Bearer\|api(' frontend/app.js | head` 确认既有模式再写,禁止另起一套):

```javascript
async loadFiles(rel) {
    this.filesPage.loading = true;
    try {
        const resp = await this.api('/api/files/list?path=' + encodeURIComponent(rel));
        if (!resp.ok) throw new Error(await resp.text());
        const data = await resp.json();
        this.filesPage.path = rel;
        this.filesPage.entries = data.entries || [];
        this.filesPage.breadcrumb = data.breadcrumb || [];
        this.filesPage.truncated = !!data.truncated;
        // 用量懒加载,不阻塞列表
        this.loadFilesUsage(rel);
    } catch (e) { this.toast(e.message, 'error'); }
    finally { this.filesPage.loading = false; }
},
async loadFilesUsage(rel) {
    try {
        const resp = await this.api('/api/files/usage?path=' + encodeURIComponent(rel));
        if (resp.ok) {
            const d = await resp.json();
            if (d.pool) this.filesPage.usageText = `${this.fmtSize(d.pool.used_kb*1024)} / ${this.fmtSize(d.pool.total_kb*1024)}`;
        }
    } catch (_) {}
},
downloadFile(e) {
    const rel = this.filesPage.relJoin(e.name);
    // 带认证下载:fetch blob 后触发 a[download](照抄项目里既有下载实现,若有)
    this.api('/api/files/download?path=' + encodeURIComponent(rel))
        .then(r => r.blob()).then(b => {
            const a = document.createElement('a');
            a.href = URL.createObjectURL(b); a.download = e.name; a.click();
            URL.revokeObjectURL(a.href);
        }).catch(err => this.toast(String(err), 'error'));
},
previewable(e) {
    if (e.type !== 'file') return false;
    const ext = ('.' + e.name.split('.').pop()).toLowerCase();
    return ['.txt','.md','.json','.yaml','.yml','.log','.conf','.ini','.csv','.sh',
            '.jpg','.jpeg','.png','.gif','.webp','.bmp'].includes(ext) && e.size <= 10*1024*1024;
},
async previewFile(e) {
    const rel = this.filesPage.relJoin(e.name);
    const ext = ('.' + e.name.split('.').pop()).toLowerCase();
    const imgs = ['.jpg','.jpeg','.png','.gif','.webp','.bmp'];
    this.filesPage.preview = { open: true, kind: imgs.includes(ext) ? 'image' : 'text', name: e.name, content: '', url: '' };
    const resp = await this.api('/api/files/preview?path=' + encodeURIComponent(rel));
    if (this.filesPage.preview.kind === 'image') {
        const b = await resp.blob();
        this.filesPage.preview.url = URL.createObjectURL(b);
    } else {
        const d = await resp.json();
        this.filesPage.preview.content = d.content + (d.truncated ? '\n\n... [已截断,请下载查看全文]' : '');
    }
},
fmtSize(n) {
    if (n == null) return '—';
    const u = ['B','KB','MB','GB','TB']; let i = 0;
    while (n >= 1024 && i < u.length-1) { n /= 1024; i++; }
    return (i === 0 ? n : n.toFixed(1)) + ' ' + u[i];
},
fmtTime(ts) { return new Date(ts * 1000).toLocaleString(); },
```

(`this.api(...)` / `this.toast(...)` 是既有封装——实现前 grep app.js 确认方法名,若既有封装叫别的名(如 `req`/`notify`),照既有名用。)

- [ ] **Step 4: i18n 文案**

zh-CN.json 加(位置按 key 字母序/邻近 nav 段):

```json
"files": {
    "pool_root": "池根",
    "name": "名称", "size": "大小", "owner": "属主", "mode": "权限",
    "mtime": "修改时间", "actions": "操作",
    "download": "下载", "preview": "预览",
    "empty_dir": "空目录",
    "truncated_hint": "目录条目过多,仅显示前 2000 项;完整内容请用 SMB 访问"
}
```

en-US.json 对应英文("Pool root"/"Name"/"Size"/"Owner"/"Permissions"/"Modified"/"Actions"/"Download"/"Preview"/"Empty directory"/"Too many entries, showing first 2000. Use SMB for the full listing.")。

Run: `python3 scripts/check-i18n.py`
Expected: 0 缺失

- [ ] **Step 5: 浏览器实测**

本地起面板(或部署到测试机),验证:导航进 files 页 → 列池根(public + 各用户 home)→ 点进 public → 下载文件 → 预览 txt/图片 → 面包屑回退 → 中英文切换文案完整 → 符号链接显示 target 且不可点进池外。

- [ ] **Step 6: Commit**

```bash
git add web/frontend/
git commit -m "feat(filemgmt): 前端文件页 v1 — 浏览/面包屑/下载/预览/用量,导航替换 FileBrowser 外链"
```

---

### Task 6: M1 文档同步 + 发版

**Files:**
- Modify: `docs/feature-operation-checklist.md`(新增文件管理模块操作项;**必须用文档末尾附录 awk 命令重算汇总统计**,分类计数=统计口径)
- Modify: `CHANGELOG.md`(beta.17 段:文件管理器 v1 只读基线 + FileBrowser 入口移除)
- Modify: `TODO.md`(FileBrowser 取代项标进行中)
- Modify: `docs/nas-product-manual`(文件管理章节,若手册有对应结构)

- [ ] **Step 1:** 按 nas-storage-architecture skill 的"更新文档和 KB"清单执行:checklist(含重算统计)+ 产品手册 + CHANGELOG + TODO
- [ ] **Step 2:** 红线 grep:`grep -rnE "10\.216\.|10\.187\.|192\.168\.213\.|nas123456" scripts/ configs/ web/frontend/ .env.example` 必须空
- [ ] **Step 3:** `cd web && go build ./... && go test ./... -count=1` 全绿(无关包存量失败用 git stash 对照确认后在 commit message 注明)
- [ ] **Step 4:** Commit + tag v1.4.0-beta.17 + push;是否跑 release.sh 由用户拍板(spec §十:M1 完成即可对外宣布)
- [ ] **Step 5:** KB 同步:新文档 kb_write 到 `NAS项目/产品文档/`,CHANGELOG 更新 `NAS项目/CHANGELOG.md`

---

## M2 — v2 写操作

### Task 7: mkdir + rename

**Files:** Create `web/modules/filemgmt/mutate.go` + `mutate_test.go`;Modify `filemgmt.go`(注册)

**Interfaces:** Produces `handleMkdir`(POST `/api/files/mkdir` body `{path, name}`)、`handleRename`(POST `/api/files/rename` body `{path, new_name}`)、`func validateFileName(name string) error`(禁 `/`、`..`、控制字符、>255 字节、空、`.`/`..` 全名)

- [ ] Step 1: 失败测试 — mkdir 成功建目录且 mode 0755;mkdir 名称含 `/` 或 `..` 拒绝 400;rename 成功;rename 目标已存在 409;rename 到 `..` 名 400;两 handler 对 GET 返回 405
- [ ] Step 2: 确认编译失败
- [ ] Step 3: 实现 — validateFileName 纯函数 + 两 handler(resolvePoolPath 双路径:父目录 + 目标名 join 后**再过一次** resolvePoolPath 防拼接逃逸);handler 内 `common.LogAuditRequest(r, "files", "mkdir"/"rename", "path=... name=...", "success")` 富 detail
- [ ] Step 4: 测试绿 + build
- [ ] Step 5: Commit `feat(filemgmt): 新建目录与重命名 — 名称白名单校验 + 双重路径锚定`

### Task 8: 上传(流式)

**Files:** Modify `mutate.go`(或新 `upload.go`)+ 测试

**Interfaces:** Produces `handleUpload`(POST `/api/files/upload?path=<目标目录>` multipart;`r.MultipartReader()` 流式;同名无 `overwrite=yes` → 409;写盘后按目标目录 setgid/属主对齐:public 下 0664 + 组 nasusers,home 下 0600 + 属主=home 用户,普通共享按 folders.db force user 同名)

- [ ] Step 1: 失败测试 — httptest 构造 multipart 上传单文件成功且内容一致;同名冲突 409;overwrite=yes 覆盖成功;上传到不存在目录 404;文件名 `../evil` 被 validateFileName 拒 400;大文件(构造 5MB reader)不整体进内存(断言无 ParseMultipartForm 调用——实现用 MultipartReader 即可,测试跑 5MB 流验证功能)
- [ ] Step 2: 确认失败
- [ ] Step 3: 实现 — `mr, err := r.MultipartReader()`;逐 `mr.NextPart()`;part.FileName() 过 validateFileName;目标 = resolvePoolPath(path) + filename 再 resolvePoolPath;`io.Copy(f, part)` 32KB buffer;磁盘满 write 错误 → 507 + os.Remove 半成品;完成后 chmod/chown 对齐(复用 Task 11 的 alignNewFilePerms 辅助——若 Task 11 未先行,本 Task 内实现该辅助并让 Task 11 复用)
- [ ] Step 4: 测试绿 + build
- [ ] Step 5: Commit `feat(filemgmt): 流式上传 — MultipartReader 不撑内存 + 同名 409/overwrite + 权限对齐`

### Task 9: move + copy(同 fs rename,跨 fs copy+delete)

**Files:** Modify `mutate.go` + 测试

**Interfaces:** Produces `handleMove`(POST `{src, dst_dir}`)、`handleCopy`(POST `{src, dst_dir}`)、`func sameFS(a, b string) bool`(比较 syscall.Stat_t.Dev)、`func copyTree(src, dst string) error`(io.Copy 流式递归,先建目录后拷文件,符号链接拷链接本身 os.Symlink 不跟随)

- [ ] Step 1: 失败测试 — 同 fs move = rename(源消失目标在);move 目标已存在同名 409;copy 后源仍在;copy 目录递归内容一致;move/copy 目标目录不存在 404;src 穿越拒 400;copyTree 遇符号链接不跟随(目标处是链接不是内容)
- [ ] Step 2: 确认失败
- [ ] Step 3: 实现 — sameFS 用 Stat_t.Dev;move:同 fs os.Rename,跨 fs copyTree+校验字节数+os.RemoveAll(源)(校验失败保留源并 500 告警,不静默丢数据);copy:一律 copyTree;两者 dst 完整路径再过 resolvePoolPath
- [ ] Step 4: 测试绿 + build
- [ ] Step 5: Commit `feat(filemgmt): 移动/复制 — 同 fs 零拷贝 rename,跨 fs copy+delete 带校验`

### Task 10: 删除(进 .recycle,与回收站页联动)

**Files:** Modify `mutate.go` + 测试

**Interfaces:** Produces `handleDelete`(POST `{path}`);删除目标 = `ownerShareOf(abs)`(Task 1)定位所属 share → `<share.Path>/.recycle/<admin>/<unixnano>/<basename>`;无所属 share → `<poolRoot>/.recycle/<admin>/<unixnano>/<basename>`;admin 用户名 = `common.RequestUsername(r)` 的 JWT 身份(兜底 common.GetNASUser());os.Rename 零拷贝;父目录不存在则 MkdirAll(mode 0700)

- [ ] Step 1: 失败测试 — 删 public 下文件后:源消失、`public/.recycle/<user>/` 下出现且内容一致;删 home(fm)下文件落 fm 的 .recycle;目录删除保留子树;池根散落文件(不属于任何 share,测试里 folders.db 空)落池根 .recycle;删除路径穿越拒;`.recycle` 自身不可删(防套娃,拒绝 path 含 `.recycle` 段)
- [ ] Step 2: 确认失败
- [ ] Step 3: 实现(注意:`.recycle/<user>` 目录名与 diskmgmt/recycle.go 的 `recycleDirName` 常量同源——import diskmgmt 用其导出常量,若未导出则在本模块定义同值并注释指向;删除后回收站页 handleRecycleList 天然可见,无需改 diskmgmt)
- [ ] Step 4: 测试绿 + build;**联动实测**:起面板删一个文件 → 存储页回收站 tab 可见可 restore
- [ ] Step 5: Commit `feat(filemgmt): 删除进 .recycle — ownerShareOf 定位所属共享,与回收站页同源联动`

### Task 11: 前端写操作(v2 UI)

**Files:** Modify `index.html`(工具栏 + 上传控件 + 确认对话框)、`app.js`(selected[] 多选状态 + upload/mkdir/rename/move/copy/delete 方法)、i18n 两文件

- [ ] Step 1: 表格加复选框列(全选/单选),工具栏:上传(input[type=file] multiple + 进度条 xhr.upload.onprogress——fetch 无上传进度,上传专用 XMLHttpRequest)、新建目录(prompt 对话框)、选中后 [移动][复制][删除] 按钮
- [ ] Step 2: 移动/复制对话框:简易目录选择器(loadFiles 数据复用,逐级下拉/点击选择目标目录)
- [ ] Step 3: 删除确认:展示条目数 + 名称列表,确认文案 `$t('files.confirm_delete')`("删除的文件进入回收站,可在存储页恢复");递归删除目录额外 type-to-confirm(输入目录名,对齐既有危险操作模式)
- [ ] Step 4: i18n 全部文案双语 + check-i18n.py 过
- [ ] Step 5: 浏览器实测全流程:上传(含大文件进度)/新建/重命名/移动/复制/删除 → 回收站页恢复 → 审计日志页可见每条写操作
- [ ] Step 6: Commit `feat(filemgmt): 前端写操作 — 多选工具栏/流式上传进度/移动复制选择器/删除确认`

### Task 12: M2 文档同步 + 发版(beta.18)

同 Task 6 清单(checklist 重算统计/CHANGELOG/TODO/红线/KB)。CHANGELOG 注明:setup.sh 本期起**新装默认不装 FileBrowser**(spec §十一 M2 节点)——Modify `scripts/setup.sh` [7/10] 段加开关(默认跳过安装,已装的不动;`--with-filebrowser` 保留安装能力一个版本周期)。

---

## M3 — v3 权限修复

### Task 13: stat + chmod + chown

**Files:** Create `web/modules/filemgmt/perms.go` + `perms_test.go`;Modify `filemgmt.go`

**Interfaces:** Produces:
- `handleStat`(GET `/api/files/stat?path=`)→ `{"owner","group","mode","mode_text","recursive_possible":bool}`(owner/group 解析复用 listDir 的 user.LookupId 逻辑,抽公共函数 `resolveOwnerGroup(info)`)
- `handleChmod`(POST `{path, mode, recursive}`)— mode 必须 `^[0-7]{3,4}$` 且 ≤0777;recursive 用 filepath.Walk+os.Chmod(**禁 SudoExec chmod**,sudoers 无 chmod -R);符号链接跳过不 chmod(Lstat 判别)
- `handleChown`(POST `{path, owner, group, recursive}`)— owner/group 必须 user.Lookup/.LookupGroup 存在的系统账号;os.Chown(uid,gid);recursive Walk;符号链接用 os.Lchown(不改目标)
- `func parseMode(s string) (os.FileMode, error)`(纯函数,单测)

- [ ] Step 1: 失败测试 — parseMode("0644")=0644 / ("755")=0755 / ("9999")err / ("rwx")err;chmod 单文件生效;chmod recursive 子树生效且符号链接目标 mode 不变;chown 到不存在用户 400;chown recursive 生效(Lchown 语义:链接本身 owner 变、目标不变);stat 回显与 os.Lstat 一致
- [ ] Step 2: 确认失败
- [ ] Step 3: 实现(注意:测试 chown 需要 root 才能成功——测试机跑测试是 root 则直测;非 root 环境 skip 并注释说明,生产同构机验收必测)
- [ ] Step 4: 测试绿 + build
- [ ] Step 5: Commit `feat(filemgmt): stat/chmod/chown — mode 白名单校验,递归 Walk 免 sudo,符号链接 Lchown`

### Task 14: fix-home-perms 一键模板

**Files:** Modify `perms.go` + 测试

**Interfaces:** Produces `handleFixHomePerms`(POST `{username}`)— 权限基准**照抄** config_sync.go 的 EnsurePoolStructure/EnsureUserHome:home `/data/nas1/<username>` 递归 0700 + chown username:username;校验 username 是系统用户(user.Lookup)且 home 目录存在,否则 400。**不含 public 模板**(public 2775 属 EnsurePoolStructure 职责,面板已有建池流程;本端点只管用户 home——如需 public 修复另加 `{target:"public"}` 参数,实现时与用户确认)

- [ ] Step 1: 失败测试 — 构造假 home(乱权限 0777/错属主)→ 调 fix → 递归 0700 + owner 正确;不存在用户 400;无 home 目录 400
- [ ] Step 2-4: 红→实现→绿(测试环境非 root 时 chown 断言 skip,同 Task 13)
- [ ] Step 5: Commit `feat(filemgmt): fix-home-perms 一键修复 — 与 EnsureUserHome 权限基准同源(0700 owner:owner)`

### Task 15: 前端 v3(改权限对话框 + 批量)

**Files:** Modify `index.html`、`app.js`、i18n

- [ ] Step 1: 行内 ⋮ 菜单加"属性/权限":对话框回显 stat(owner/group/mode_text)+ mode 八进制输入(实时预览符号位)+ recursive 勾选 + owner/group 下拉(数据源:既有 users 模块的用户列表端点,grep `/api/users` 确认)+ 预设按钮(0644/0755/0700)
- [ ] Step 2: 用户页(users)每个用户行加"修复 home 权限"按钮 → fix-home-perms,危险确认(type-to-confirm 输入用户名)
- [ ] Step 3: 批量:多选后 [改权限] 对话框对每条逐一调用(前端串行 + 进度 N/M),结果汇总 toast
- [ ] Step 4: i18n 双语 + check-i18n.py
- [ ] Step 5: 浏览器实测 + **SMB 端验证**:面板 chown/chmod 后,smbclient 以该用户登录验证读写行为符合预期(force user 映射下 mask 0700/0775 的实际效果)
- [ ] Step 6: Commit `feat(filemgmt): 前端权限管理 — stat 回显/chmod chown 对话框/fix-home-perms/批量串行`

### Task 16: M3 文档同步 + 生产同构机全链路验收 + 发版(beta.19)

- [ ] Step 1: 文档清单同 Task 6;CHANGELOG 注明 FileBrowser 卸载提示(cleanup 路径)
- [ ] Step 2: **Debian 13 同构机全链路验收**(spec §十三 清单逐项打勾):2 用户 + home → 浏览/上传/下载/移动/删除/回收站 restore → chown 后 smbclient 实测 → 穿越攻击用例(`?path=../../etc/passwd`、符号链接、`..%2f`)全拒 → 审计日志逐条可查 → i18n 完整
- [ ] Step 3: 验收抓到缺陷:修 + 单测 + commit + 重部署重跑受影响项,全绿才 tag
- [ ] Step 4: tag v1.4.0-beta.19 + push;release.sh 与否用户拍板
- [ ] Step 5: KB 同步 + nas-storage-architecture skill 更新(新增 filemgmt 模块的坑与约定:resolvePoolPath 唯一入口、.recycle 同源、os.Chmod 免 sudo 等)

---

## Self-Review 记录

- **Spec 覆盖**:§二 范围→Task 2-15 全覆盖;§四 API 17 端点→list/download/zip/preview/usage(T2-4)、upload/mkdir/rename/move/copy/delete(T7-10)、stat/chmod/chown/fix-home-perms(T13-14),无遗漏;§五 数据流→T1/T9/T10 实现;§六 安全 7 条→T1(锚定)/T2(审计自动)/T3(zip 不跟链)/T4(SVG 拒)/T7(名称校验)/T13(sudo 约束);§七 前端→T5/T11/T15;§八 错误处理→各 Task 测试用例;§九 测试策略→每 Task TDD + T16 同构机;§十 里程碑=M1/M2/M3 三段;§十一 FileBrowser 淘汰→T5(入口替换)/T12(setup 默认不装)/T16(卸载提示);§十三 验收→T16 Step 2
- **占位符扫描**:Task 4 handleUsage、Task 11 目录选择器为实现要点而非逐行代码——两处均给出精确的响应结构/交互定义与依赖确认动作(grep 既有封装),属"细节完备但代码留实现自由度"的刻意取舍;其余 Task 均含可编译代码/测试
- **类型一致性**:`resolvePoolPath/ownerShareOf/validateFileName/parseMode/copyTree/sameFS/alignNewFilePerms/resolveOwnerGroup` 在定义与引用 Task 间签名一致;FileEntry JSON 字段与前端模板逐一对应(name/type/size/mode_text/owner/group/mtime/target);`.recycle/<user>/<unixnano>/` 布局与 diskmgmt/recycle.go 的 listRecycleDir 新布局(`.recycle/<user>/<rel...>`)兼容——restore 按 rel 恢复,unixnano 层只是防同名冲突的额外一级,恢复语义不变(T10 实现时验证回收站页对该布局的 list/restore 正确性,若 recycle.go 对多一级目录不兼容,则在 T10 内改为与 SMB vfs recycle 完全同构的布局)
