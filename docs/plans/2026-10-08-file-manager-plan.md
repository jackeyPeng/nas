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

- [x] **Step 1: 写失败测试**

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

- [x] **Step 2: 跑测试确认失败**

Run: `cd web && go test ./modules/filemgmt/ -run TestResolvePoolPath -count=1`
Expected: 编译失败 `undefined: resolvePoolPath / poolRootOverride`

- [x] **Step 3: 最小实现**

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
	root := normalizedRoot()
	rel = strings.TrimSpace(rel)
	if strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("不接受绝对路径")
	}
	// 先按字面拒绝任何 ".." 片段或子串(Clean 会消解它,这里故意不消解——
	// 请求里出现 ".." 即视为攻击,不做"善意解释";fail-closed,
	// 副作用是 my..dir 类文件名也被拒,家用场景可接受)
	if strings.Contains(rel, "..") {
		return "", fmt.Errorf("路径包含非法片段")
	}
	full := filepath.Clean(filepath.Join(root, rel))
	if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return "", fmt.Errorf("路径越界")
	}
	// 符号链接逃逸:对【最深已存在祖先】EvalSymlinks(Task 1 review Critical#1 修订:
	// 叶子不存在时中间目录仍可能是逃逸符号链接,写盘会被内核解析落到池外)
	existing := full
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing { // 走到文件系统根仍不存在,异常
			return "", fmt.Errorf("路径解析失败")
		}
		existing = parent
	}
	if resolved, err := filepath.EvalSymlinks(existing); err == nil {
		// resolved 与 root 都须在同一解析层级(root 已 EvalSymlinks 归一,Important#2)
		if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
			return "", fmt.Errorf("符号链接指向池外")
		}
		// 校验通过后返回【解析前】的字面路径(full),池内符号链接按原路径使用
	} else {
		return "", fmt.Errorf("路径解析失败: %v", err)
	}
	return full, nil
}
```

**修订记录(2026-10-08 Task 1 review)**:初版代码只对 `full` 整体存在时做 EvalSymlinks,存在两个缺陷,已按 review findings 修订:
- Critical#1:叶子不存在但中间目录是逃逸符号链接时放行,后续写盘 handler 会把文件落到池外(reviewer 沙箱实证)。修订为向上找最深已存在祖先做校验。
- Important#2:`root` 未 EvalSymlinks,若生产 `/data/nas1` 本身是符号链接,已存在路径的 resolved 前缀比较全部失败(全量误拒)。修订为 `normalizedRoot()`:

```go
// normalizedRoot 返回 EvalSymlinks 归一后的池根(失败回落字面值)。
// 与 resolvePoolPath 的 resolved 比较双方须在同一解析层级。
func normalizedRoot() string {
	root := filepath.Clean(poolRoot())
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		return resolved
	}
	return root
}
```

(实现文件需 `import "os"`。测试补用例:`TestResolvePoolPathRejectsSymlinkEscapeNonexistentLeaf` —— `public/evil -> /etc`(池外),请求 `public/evil/newfile.txt`(叶子不存在)必须被拒。)

`ownerShareOf` 放同文件:

```go
import "nas-panel/modules/diskmgmt"

// ownerShareOf 返回 absPath 所属共享(folders.db Path 前缀最长匹配)。
// 删除时定位对应 .recycle;无匹配返回 false(池根散落文件)。
func ownerShareOf(absPath string) (diskmgmt.FolderMeta, bool) {
	var best diskmgmt.FolderMeta
	found := false
	for _, m := range diskmgmt.GetAllFolderMeta() {
		clean := filepath.Clean(m.Path)
		prefix := clean + string(filepath.Separator)
		if strings.HasPrefix(absPath+string(filepath.Separator), prefix) {
			// 最长匹配用 Clean 后长度(Task 1 review Minor#3:原始 m.Path 可能带尾/重复斜杠)
			if !found || len(clean) > len(filepath.Clean(best.Path)) {
				best, found = m, true
			}
		}
	}
	return best, found
}
```

注:`absPath+sep` 与 `prefix` 的比较在 share 路径恰为 absPath 本身时也成立(相等加 sep 即前缀),无需单独 OR 条件。`GetAllFolderMeta` 无 DB 时返回 nil(range nil 与空 slice 行为一致),ownerShareOf 返回 false——测试不依赖真 DB。

- [x] **Step 4: 跑测试确认通过**

Run: `cd web && go test ./modules/filemgmt/ -run TestResolvePoolPath -count=1 -v`
Expected: 4 个测试全 PASS

- [x] **Step 5: Commit**

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

- [x] **Step 1: 写失败测试**

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

- [x] **Step 2: 跑测试确认失败**

Run: `cd web && go test ./modules/filemgmt/ -run 'TestListDir|TestHandleList' -count=1`
Expected: 编译失败 `undefined: listDir / handleList`

- [x] **Step 3: 实现**

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

- [x] **Step 4: 跑测试确认通过**

Run: `cd web && go test ./modules/filemgmt/ -count=1 -v`
Expected: 全 PASS(含 Task 1 的 4 个)

- [x] **Step 5: main.go 接线**

`web/main.go` 第 138 行 `events.RegisterRoutes(mux)` 之后加:

```go
	filemgmt.RegisterRoutes(mux)
```

import 块加 `"nas-panel/modules/filemgmt"`。

Run: `cd web && go build ./...`
Expected: 编译通过

- [x] **Step 6: Commit**

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

- [x] **Step 1: 写失败测试**

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

- [x] **Step 2: 跑测试确认失败**

Run: `cd web && go test ./modules/filemgmt/ -run 'TestHandleDownload|TestZipDir|TestHandleZip' -count=1`
Expected: 编译失败 `undefined: handleDownload / handleZip / zipDir`

- [x] **Step 3: 实现**

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

- [x] **Step 4: 跑测试确认通过 + RegisterRoutes 追加**

filemgmt.go RegisterRoutes 加:

```go
	mux.HandleFunc("/api/files/download", common.AuthMiddleware(handleDownload))
	mux.HandleFunc("/api/files/zip", common.AuthMiddleware(handleZip))
```

Run: `cd web && go build ./... && go test ./modules/filemgmt/ -count=1`
Expected: 全 PASS

- [x] **Step 5: Commit**

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

- [x] **Step 1: 写失败测试**(文本预览/截断/SVG 拒绝/usage 结构)

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

- [x] **Step 2: 确认失败** — `cd web && go test ./modules/filemgmt/ -run TestPreview -count=1` → `undefined: handlePreview`

- [x] **Step 3: 实现**(要点)

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

- [x] **Step 4: 测试过 + 注册路由 + build** — RegisterRoutes 加 preview/usage 两行;`cd web && go build ./... && go test ./modules/filemgmt/ -count=1` 全绿

- [x] **Step 5: Commit** — `git commit -m "feat(filemgmt): 文本/图片预览(SVG 拒绝防 XSS)+ 池/目录用量(du 超时兜底)"`

---

### Task 5: 前端 files 页(v1 只读)

**Files:**
- Modify: `web/frontend/index.html`(导航 2 处 + 新 page div + 替换 FileBrowser 外链)
- Modify: `web/frontend/app.js`(filesPage 状态 + loadFiles/downloadFile/previewFile 方法)
- Modify: `web/frontend/i18n/zh-CN.json`、`web/frontend/i18n/en-US.json`(files.* 文案)

**Interfaces:**
- Consumes: Task 2-4 的 4 个 GET 端点
- Produces: 可用的 files 页(浏览/下载/预览)

- [x] **Step 1: 导航项替换 FileBrowser 外链**

index.html:42(移动端菜单)当前是:

```html
<a :href="'http://'+location.hostname+':8081'" target="_blank" ...>📁 <span x-text="$t('nav.file_manager')"></span></a>
```

改为(复用既有 `nav.file_manager` key,文案不变,行为改内部导航):

```html
<a href="#" @click.prevent="navigate('files'); mobileMenu=false" style="display:block;padding:var(--sp-3);background: var(--primary);color:white;text-align:center;border-radius:var(--radius-sm);text-decoration:none;font-weight:600">📁 <span x-text="$t('nav.file_manager')"></span></a>
```

侧边栏(index.html:115 附近,diskmgmt 的 `<li>` 旁)加同款 `<li :class="{active: page === 'files'}" @click="navigate('files')">📁 $t('nav.file_manager')</li>`(结构照抄相邻 li)。

- [x] **Step 2: files 页 div**

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

- [x] **Step 3: app.js 状态与方法**

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
// du_status 三态(Task 4 review Important#1):ok=bytes 有值;timeout/error=bytes 为 null,
// 前端对非 ok 一律降级展示"用量计算中/不可用",不 switch 具体态
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

- [x] **Step 4: i18n 文案**

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

- [x] **Step 5: 浏览器实测**

本地起面板(或部署到测试机),验证:导航进 files 页 → 列池根(public + 各用户 home)→ 点进 public → 下载文件 → 预览 txt/图片 → 面包屑回退 → 中英文切换文案完整 → 符号链接显示 target 且不可点进池外。

- [x] **Step 6: Commit**

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

- [x] **Step 1:** 按 nas-storage-architecture skill 的"更新文档和 KB"清单执行:checklist(含重算统计)+ 产品手册 + CHANGELOG + TODO
- [x] **Step 2:** 红线 grep:`grep -rnE "10\.216\.|10\.187\.|192\.168\.213\.|nas123456" scripts/ configs/ web/frontend/ .env.example` 必须空
- [x] **Step 3:** `cd web && go build ./... && go test ./... -count=1` 全绿(无关包存量失败用 git stash 对照确认后在 commit message 注明)
- [x] **Step 4:** Commit + tag v1.4.0-beta.17 + push;是否跑 release.sh 由用户拍板(spec §十:M1 完成即可对外宣布)
- [x] **Step 5:** KB 同步:新文档 kb_write 到 `NAS项目/产品文档/`,CHANGELOG 更新 `NAS项目/CHANGELOG.md`

---

## M2 — v2 写操作

### Task 7: mkdir + rename

**Files:** Create `web/modules/filemgmt/mutate.go` + `mutate_test.go`;Modify `filemgmt.go`(注册)

**Interfaces:** Produces `handleMkdir`(POST `/api/files/mkdir` body `{path, name}`)、`handleRename`(POST `/api/files/rename` body `{path, new_name}`)、`func validateFileName(name string) error`(禁 `/`、`..`、控制字符、>255 字节、空、`.`/`..` 全名)

- [x] Step 1: 失败测试 — mkdir 成功建目录且 mode 0755;mkdir 名称含 `/` 或 `..` 拒绝 400;rename 成功;rename 目标已存在 409;rename 到 `..` 名 400;两 handler 对 GET 返回 405
- [x] Step 2: 确认编译失败
- [x] Step 3: 实现 — validateFileName 纯函数 + 两 handler(resolvePoolPath 双路径:父目录 + 目标名 join 后**再过一次** resolvePoolPath 防拼接逃逸);handler 内 `common.LogAuditRequest(r, "files", "mkdir"/"rename", "path=... name=...", "success")` 富 detail
- [x] Step 4: 测试绿 + build
- [x] Step 5: Commit `feat(filemgmt): 新建目录与重命名 — 名称白名单校验 + 双重路径锚定`

**修订记录(2026-10-12 Task 7 review)**:独立审查结论「需修复后合入」(Critical 0 / Important 2 / Minor 8),拍板如下:
- Important#1:handleRename 未拒绝池根本身——path="" 时 resolvePoolPath 按设计返回池根,随后对池根发起 rename syscall(EINVAL→500)。修订:srcAbs == normalizedRoot() 时 400「不能重命名池根」。另澄清既有文档/代码分歧:design §六第 1 条写 resolvePoolPath「拒绝空路径」,path.go 实际放行空串=池根(list/usage 浏览根合法)——**以代码为准,后续写 handler 一律自行防御根路径**,design 该句待 M3 文档轮修正。
- Important#2:rename 的 Lstat 预检与 os.Rename 之间 TOCTOU 静默覆盖目标(数据丢失类)。修订:改用 `unix.Renameat2(RENAME_NOREPLACE)`(golang.org/x/sys 由 indirect 转直接依赖),EEXIST/ENOTEMPTY 映射 409;Lstat 预检保留(常规路径的友好 409,renameat2 是并发兜底)。
- Minor#1:mkdir 双重锚定退化(第一次解析的 parentAbs 被 `_ =` 丢弃)。修订:断言 `targetAbs == filepath.Join(parentAbs, req.Name)`,不一致 500,让双锚定名副其实。
- Minor#2:validateFileName 的 `utf8.RuneCountInString(name) > 255` 是冗余条件(rune 数恒 ≤ 字节数),删除,只留 `len(name) > 255`。
- Minor#3:rename 同名(srcAbs==dstAbs)原实现 409「目标已存在」。拍板:改 200 no-op(POSIX 同名 rename 即无操作成功),补测试。
- Minor#5:mkdir mode 0755 受进程 umask 影响(systemd 服务 umask 不定)。修订:os.Mkdir 后显式 `os.Chmod(targetAbs, 0755)`,测试断言恒 0755。
- Minor#6(审计类别小写 "files"+英文 action vs 仓内既有大写类别+中文 action 约定):本 Task 维持现约定,延至 M2 收尾统一拍板,不在本轮改。
- Minor#4(反斜杠/首尾空格/Windows 保留名/Unicode NFC 不在白名单):规格未要求,Linux 下合法且双重锚定无路径风险,列为未来规格增强(SMB Windows 客户端体验),不在本轮改。
- 补测试:rename 池根→400;rename 同名→200 no-op(旧名仍在、内容不变);mkdir 显式 chmod 后 mode 恒 0755。

**复审记录(2026-10-12 scoped re-review,commit ba819c8)**:可合入。6 项修复全部落实、顺序语义正确(池根检查在 Lstat 前、同名 no-op 在 dst 预检前——renameat2(NOREPLACE) 对 oldpath==newpath 返回 EEXIST,no-op 分支必须在 syscall 前)。遗留记录:① **filemgmt 包为 Linux-only**(renameat2 使 GOOS=darwin 编译失败;release.sh 只交叉编译 linux amd64/arm64,实测两目标均通过,发布不受影响;未来需 darwin 开发环境再拆 mutate_linux.go + build tag);② 审计 detail 原样拼接用户输入 path(控制字符仅 new_name 被 validateFileName 拦,path 未滤)——与既有 mkdir/rename 审计同模式,M2 收尾轮统一对 detail 转义;③ 存量 flaky:common/TestEventBusSubscribe 的 t.TempDir 清理与异步 eventbus writer 竞态(旧提交同样概率挂,非本轮引入),待按 audit 测试同款方案(包级持久目录+TestMain 清理)修复。

### Task 8: 上传(流式)

**Files:** Modify `mutate.go`(或新 `upload.go`)+ 测试

**Interfaces:** Produces `handleUpload`(POST `/api/files/upload?path=<目标目录>` multipart;`r.MultipartReader()` 流式;同名无 `overwrite=yes` → 409;写盘后按目标目录 setgid/属主对齐:public 下 0664 + 组 nasusers,home 下 0600 + 属主=home 用户,普通共享按 folders.db force user 同名)

- [x] Step 1: 失败测试 — httptest 构造 multipart 上传单文件成功且内容一致;同名冲突 409;overwrite=yes 覆盖成功;上传到不存在目录 404;文件名 `../evil` 被 validateFileName 拒 400;大文件(构造 5MB reader)不整体进内存(断言无 ParseMultipartForm 调用——实现用 MultipartReader 即可,测试跑 5MB 流验证功能)

**修订记录(2026-10-12 Task 8 实现期)**:Step 1 的「文件名 `../evil` 被 validateFileName 拒 400」不可实现——实测 Go 标准库 `multipart.Part.FileName()` 内置 `filepath.Base()` 剥路径:`../evil` 取出即 `evil`(合法名,不会 400),`a/b.txt` 取出 `b.txt`,仅全名 `..` 保持原样被 validateFileName 拒。**拍板**:穿越语义由 stdlib Base() + resolvePoolPath 双重兜底(剥完的名只会落在目标目录内,无逃逸面),validateFileName 仍保留为纵深防御(拦 `..` 全名/控制字符/超长/反斜杠 `..\x` 类)。测试期望修订为:`../evil` → 200 且文件以 `evil` 名落在目标目录内(池外与目标目录外无落盘);`..` → 400。
- [x] Step 2: 确认失败
- [x] Step 3: 实现 — `mr, err := r.MultipartReader()`;逐 `mr.NextPart()`;part.FileName() 过 validateFileName;目标 = resolvePoolPath(path) + filename 再 resolvePoolPath;`io.Copy(f, part)` 32KB buffer;磁盘满 write 错误 → 507 + os.Remove 半成品;完成后 chmod/chown 对齐(复用 Task 11 的 alignNewFilePerms 辅助——若 Task 11 未先行,本 Task 内实现该辅助并让 Task 11 复用)

**修订记录(2026-10-12 Task 8 review)**:独立审查结论「需修复后合入」(Critical 0 / Important 2),拍板如下:
- Important#1:Step 3 的「write 错误 → 507 + os.Remove 半成品」字面实现会误删——Lstat 预检与 OpenFile(O_EXCL) 之间并发创建时 EEXIST 失败,此时文件非我所建,统一 os.Remove 会以 root 删掉并发方文件(数据丢失)且误报 507。**修订语义**:按失败阶段分流——open 阶段 EEXIST → 409(同 Lstat 预检语义,不 Remove);open 阶段其他失败 → 507(未落字节,不 Remove);**仅 open 成功后的 copy/chmod 失败才 Remove 半成品 + 507/500**。清理责任移入 writePartToFile 内部(copy 失败自清),handler 只按错误类型映射状态码。
- Important#2:测试的 `t.Skip` 后无任何断言(死代码),chown 路径文件系统层零验证。修订:root-gated 真实断言(syscall.Stat_t 校验 uid/gid),并补 0600 普通共享分支的 root-gated 断言;非 root 环境维持 skip。
- Minor 遗留(留 Task 9-13 顺带,不阻塞):ownerShareOf/ownerShareOfVia 逻辑重复待抽单一实现;多 part 中途读失败改 JSON body 带 written 列表;chown 跳过时日志升 WARN;空上传(files=0)审计语义再议。
- [x] Step 4: 测试绿 + build
- [x] Step 5: Commit `feat(filemgmt): 流式上传 — MultipartReader 不撑内存 + 同名 409/overwrite + 权限对齐`

### Task 9: move + copy(同 fs rename,跨 fs copy+delete)

**拍板记录(2026-10-12 Task 9 开工前,规格空白裁决)**:
- **JSON 字段名 `dst_dir`(snake_case)**:plan Interfaces 与 design §四表(`dstDir`)不一致,以 plan 为准——仓内既有端点均 snake_case(rename 的 `new_name`);design 该表留 M3 文档轮统一修正。
- **v2 不支持 `overwrite=yes`**:design §八错误表写了「同名冲突(上传/移动/复制/重命名)→409+overwrite 重试」,但 rename(Task 7)已按 409-only 落地并过审;目录覆盖替换语义复杂(非空目录 ENOTEMPTY)且易误删,move/copy 维持 409-only 保持一致,overwrite 扩展留 M3 按需拍板。
- **权限对齐**:同 fs move=rename 零改动(属主/mode 天然保留);copy 与跨 fs move 新落盘文件逐个走 alignNewFilePerms(权限跟目标共享,与上传一致);copyTree 先按源 mode 写(chmod 源 mode),再被 alignNewFilePerms 对齐覆盖——净效果=目标共享语义;符号链接跳过 chmod/chown(Lchown 不做)。
- **跨 fs move 校验**:copyTree 返回写入总字节数,与源树 stat 总字节比对,不一致→保留源+500 告警,绝不 RemoveAll;一致才 os.RemoveAll(src)。跨 fs 分支抽 `moveCrossFS(srcAbs, dstAbs) error` 纯编排函数,单测直接调它(测试环境无第二个 fs,handler 级跨 fs 路径由同构机验收覆盖)。
- **大目录进度**:design §5.3 允许简化——v2 同步实现(HTTP 阻塞至完成),不做 task-id/SSE;前端 Task 11 显示「处理中」。
- **池根防御**:src==池根 → 400(对照 Task 7 rename 池根拒绝);dst_dir 不存在→404、非目录→400;dst 目标已存在→409。

**Files:** Modify `mutate.go` + 测试

**Interfaces:** Produces `handleMove`(POST `{src, dst_dir}`)、`handleCopy`(POST `{src, dst_dir}`)、`func sameFS(a, b string) bool`(比较 syscall.Stat_t.Dev)、`func copyTree(src, dst string) error`(io.Copy 流式递归,先建目录后拷文件,符号链接拷链接本身 os.Symlink 不跟随)

- [x] Step 1: 失败测试 — 同 fs move = rename(源消失目标在);move 目标已存在同名 409;copy 后源仍在;copy 目录递归内容一致;move/copy 目标目录不存在 404;src 穿越拒 400;copyTree 遇符号链接不跟随(目标处是链接不是内容)
- [x] Step 2: 确认失败
- [x] Step 3: 实现 — sameFS 用 Stat_t.Dev;move:同 fs os.Rename,跨 fs copyTree+校验字节数+os.RemoveAll(源)(校验失败保留源并 500 告警,不静默丢数据);copy:一律 copyTree;两者 dst 完整路径再过 resolvePoolPath
- [x] Step 4: 测试绿 + build
- [x] Step 5: Commit `feat(filemgmt): 移动/复制 — 同 fs 零拷贝 rename,跨 fs copy+delete 带校验`

**修订记录(2026-10-12 Task 9 review)**:独立审查结论「需修复后合入」(Critical 1 / Important 4 / Minor 3),拍板如下:
- Critical#1:**复制/移动到 src 自身子目录 → copyTree 失控无限递归可写满磁盘**(dst_dir=a/b、src=a 时 WalkDir 惰性遍历会看见新建的 a/b/a 继续向下自我嵌套;reviewer 沙箱实证 2000+ entry 仍增长)。校验链只拒了 srcAbs==targetAbs,漏了 dst_dir 在 src 树内。修订:resolveMoveCopyTarget 增加 `dstDirAbs == srcAbs || strings.HasPrefix(dstDirAbs, srcAbs+sep)` → 400(GNU cp 同款语义),move/copy 共用;补「copy/move 到 src 子目录被拒 400」测试。
- Important#1:handleMove 同 fs 用裸 os.Rename,409 预检与 rename 之间 TOCTOU 可静默覆盖目标,违反「目标存在一律 409」拍板且与 handleRename(Task 7 renameat2)不一致。修订:同 fs 分支改 `unix.Renameat2(RENAME_NOREPLACE)`,EEXIST/ENOTEMPTY→409,照抄 handleRename 既有分支。
- Important#2:handleCopy 失败无条件 RemoveAll(dstAbs)——Task 8 已修的同款问题重现:copyTree 内 copyFile(O_EXCL)/os.Symlink 撞并发方已建目标报 EEXIST 时,RemoveAll 会误删并发方成品。修订:copy 错误映射区分 `errors.Is(err, fs.ErrExist)` → 409 且不清理;其余错误才 RemoveAll+500。
- Important#3:跨 fs move 未做权限对齐,偏离拍板「copy 与跨 fs move 新落盘文件逐个走 alignNewFilePerms」。修订:moveCrossFS 字节校验通过后、RemoveAll(src) **之前**调 alignCopiedTreePerms(dstAbs);对齐失败 → 保留源、清理目标、返回错误(任何失败点源都还在)。
- Important#4:跨 fs move 失败后目标半成品不清理,重试被 409 卡死。修订:handleMove 跨 fs 错误分支 os.RemoveAll(dstAbs),带 Important#2 同款 EEXIST 例外。
- Minor#1:sameFS 改用 syscall.Lstat(与 rename/copyTree 操作链接本身的口径一致;Stat 跟随链接会对跨 fs 链接误判)。本轮修。
- Minor#2:moveCrossFS 的 treeByteSize 在 copyTree 之后统计,复制期间源被并发修改会误报(失败方向安全:保留源)。本轮加注释说明接受该竞态,不改时序。
- Minor#3:测试缺口在拍板降级许可内(字节校验失败分支/跨 fs sameFS 分支/逃逸链接 handler 层),记录留同构机验收;C1 场景测试随本轮修复补上。

**Follow-up 记录(2026-10-12 复审后)**:复审结论「可合入,7/7 落实」,遗留项本轮补齐——① 复审 Important#1:C1 containment 检查改用 `filepath.EvalSymlinks` 真实路径比较(池内符号链接 dst_dir → src 子树的构造可绕过字面前缀检查导致同款无限递归;解析失败保守 400),补测试 TestHandleCopyRejectsDstInsideSrcViaSymlink;② Minor#1:两处陈旧注释 os.Rename → renameat2;③ Minor#2:补同前缀异目录放行回归测试 TestHandleMoveAllowsSamePrefixDifferentDir。复审 Minor#3(align 时序测试名超出验证力)接受现状不改。

### Task 10: 删除(进 .recycle,与回收站页联动)

**拍板记录(2026-10-12 Task 10 开工前,plan Self-Review 预留验证项裁决)**:
- **recycle.go 对 `<unixnano>` 额外一级不兼容,已实证**:handleRecycleRestore 把 `rel_path` 当原相对路径恢复(`dst = sharePath/relPath`,recycle.go:197),若布局为 `.recycle/<user>/<unixnano>/<basename>`,恢复会落到 `<share>/<unixnano>/<basename>` 错误位置;listRecycleDir 的顶层枚举(:95-114)也会把 Name/RelPath 显示成无意义的纳秒数。**按 plan Self-Review 预案改用与 SMB vfs recycle 完全同构的 keeptree 布局**:`<share.Path>/.recycle/<admin>/<目标在 share 内的原相对路径...>`。
- **同名冲突**:回收站内 rel path 已存在时,basename 追加 `.<unixnano>` 后缀(如 `file.txt.1760...`),restore 语义不变(恢复为带后缀名,与回收站页既有 `.restored` 冲突模式同款思路);目录冲突同理整树带后缀。
- **无所属 share**:落 `<poolRoot>/.recycle/<admin>/<basename>`(池根回收站不进回收站页——recycleRoots 只枚举共享,plan 既定语义;冲突加后缀同款)。
- **`.recycle` 常量**:diskmgmt.recycleDirName 未导出,按 plan 在 filemgmt 定义同值常量 + 注释指向 diskmgmt/recycle.go 同源。
- **池根防御**:path==池根("" / ".")→ 400「不能删除池根」(写 handler 根路径防御既定拍板,Task 7 复审记录)。
- **`.recycle` 自身不可删**:目标相对路径任一段为 `.recycle` → 400(防套娃);池根 .recycle 同样受保护。
- **admin 用户名**:common.RequestUsername(r) JWT 身份,空则兜底 common.GetNASUser();测试环境无 JWT 走兜底,可测。
- **删除 = os.Rename 零拷贝**;.recycle/<admin>/ 父目录 MkdirAll 0700;目录删除整子树随 rename 保留。

**Files:** Modify `mutate.go` + 测试

**Interfaces:** Produces `handleDelete`(POST `{path}`);删除目标 = `ownerShareOf(abs)`(Task 1)定位所属 share → `<share.Path>/.recycle/<admin>/<原相对路径...>`(keeptree,2026-10-12 拍板修订,详见节首拍板记录);无所属 share 或 share 未启用回收站 → `<poolRoot>/.recycle/<admin>/<basename>`;admin 用户名 = `common.RequestUsername(r)` 的 JWT 身份(兜底 common.GetNASUser());os.Rename 零拷贝;父目录不存在则 MkdirAll(mode 0700)

- [x] Step 1: 失败测试 — 删 public 下文件后:源消失、`public/.recycle/<user>/` 下出现且内容一致;删 home(fm)下文件落 fm 的 .recycle;目录删除保留子树;池根散落文件(不属于任何 share,测试里 folders.db 空)落池根 .recycle;删除路径穿越拒;`.recycle` 自身不可删(防套娃,拒绝 path 含 `.recycle` 段)
- [x] Step 2: 确认失败
- [x] Step 3: 实现(注意:`.recycle/<user>` 目录名与 diskmgmt/recycle.go 的 `recycleDirName` 常量同源——import diskmgmt 用其导出常量,若未导出则在本模块定义同值并注释指向;删除后回收站页 handleRecycleList 天然可见,无需改 diskmgmt)
- [x] Step 4: 测试绿 + build;**联动实测**:起面板删一个文件 → 存储页回收站 tab 可见可 restore
- [x] Step 5: Commit `feat(filemgmt): 删除进 .recycle — ownerShareOf 定位所属共享,与回收站页同源联动`

**修订记录(2026-10-13 Task 10 review)**:独立审查结论「需修复后合入」(Critical 0 / Important 2,reviewer 沙箱实证),拍板如下:
- Important#1:防套娃守卫两处实证绕过——(a) 前导空格:`resolvePoolPath` 先 TrimSpace 而守卫用原始 `req.Path` 字面比较,`" .recycle/fm/x"` → 200 把池根回收站内容搬走;(b) 池内符号链接:`public/hole → public/.recycle` 时删 `public/hole/fm/v.txt` 字面无 `.recycle` 段,真实落点却在回收站内(真套娃)。**修订**:守卫改基于解析后真实路径——存在性检查(Lstat 404)前移,`filepath.EvalSymlinks(srcAbs)` 后取相对池根路径做段比较(与 Task 9 follow-up 的 containment EvalSymlinks 同款思路);字面守卫删除。补两绕过场景测试 + 删除符号链接本身(搬链接不解引用)测试。
- Important#2:`meta.RecycleBin=false` 的共享被无条件写入 `<share.Path>/.recycle`——回收站页不可见(recycleRoots 只枚举 RecycleBin 共享)成数据孤儿,且该共享 smb.conf 无 `hide files = /.recycle/`,目录对 SMB 客户端可见。**拍板**:`found && !meta.RecycleBin` 时回退池根 `.recycle`(与无所属 share 同款路径)——面板删除永远可恢复、不在未启用回收站的共享里制造可见 .recycle、不改 diskmgmt 语义;池根回收站不进回收站页为既定语义(shell 可恢复)。补 RecycleBin=false 回退测试。
- Minor#3(顺手修):admin 用户名进路径前校验(含 `/` 或 `..` → 400),对照 diskmgmt resolveRecyclePath 同款纵深;现 users 白名单已挡住,纯防御。
- Minor#4:本节 Interfaces 行旧 `<unixnano>` 布局描述已随本修订同步更正(keeptree + RecycleBin 回退)。
- Minor#5(keeptree 中间目录 0700 与 samba directory_mode 同构,接受)/Minor#6(recycleConflictFree 终止性与失败方向安全,接受)/Minor#7(测试忽略 WriteFile 错误返回、死代码 `_ = root`,不阻塞留后续)记录不改。
- bdc6223(Task 9 follow-up)同轮审查结论「可合入」,无需动作;其 Minor(悬空链接 400 而非 404、bind mount 超威胁模型)记录接受。

### Task 11: 前端写操作(v2 UI)

**Files:** Modify `index.html`(工具栏 + 上传控件 + 确认对话框)、`app.js`(selected[] 多选状态 + upload/mkdir/rename/move/copy/delete 方法)、i18n 两文件

- [x] Step 1: 表格加复选框列(全选/单选),工具栏:上传(input[type=file] multiple + 进度条 xhr.upload.onprogress——fetch 无上传进度,上传专用 XMLHttpRequest)、新建目录(prompt 对话框)、选中后 [移动][复制][删除] 按钮
- [x] Step 2: 移动/复制对话框:简易目录选择器(loadFiles 数据复用,逐级下拉/点击选择目标目录)
- [x] Step 3: 删除确认:展示条目数 + 名称列表,确认文案 `$t('files.confirm_delete')`("删除的文件进入回收站,可在存储页恢复");递归删除目录额外 type-to-confirm(输入目录名,对齐既有危险操作模式)
- [x] Step 4: i18n 全部文案双语 + check-i18n.py 过
- [x] Step 5: 浏览器实测全流程:上传(含大文件进度)/新建/重命名/移动/复制/删除 → 回收站页恢复 → 审计日志页可见每条写操作(2026-10-13 本地 dev 面板+真实 Chrome 完成,详见修订记录;回收站联动验证到 API 条目可见+keeptree 落位,页面 UI 恢复按钮留同构机验收)
- [x] Step 6: Commit `feat(filemgmt): 前端写操作 — 多选工具栏/流式上传进度/移动复制选择器/删除确认`

**修复轮验证记录(2026-10-13,commit 42fc45f)**:6 项修复全部真实浏览器复验通过——Important#1 进度条:state 置 42% 后 DOM 渲染「42% (42.0 MB / 100 MB)」(响应式恢复);64MB 真实上传全链路成功(XHR 完成态 100% 正常);Minor③ rename 后 selected 剔除旧名实测 ✓;Minor④ 选择器进 public 只见 [docs,media](.recycle 存在但被过滤)✓;Minor⑤ batch_partial_fail 键解析、旧键零残留 ✓;Minor①② 代码级核对(parseErrorText 兜底/fileWrite 401 照抄 api() 模式)。修复精确实现审查建议原文,以浏览器实证替代又一轮 scoped re-review(判断依据:改动 37 行全部为 reviewer 指定修法,无可自由发挥面)。

**修订记录(2026-10-13 Task 11 review + E2E)**:独立审查(jsdom+真 Alpine 实证)结论「需修复后合入」,拍板如下:
- Important#1:上传进度条全程冻结 0%——`const progress = this.filesPage.uploadProgress = {...}` 链式赋值捕获 raw 对象,onprogress 对 raw 的 mutation 绕过 Alpine 代理不触发渲染(jsdom 实证 RAW→DOM 停 0%/PROXY→更新;uploadFilesWithOverwrite 的写法是对的)。修订:赋值与取引用拆两行(`this.filesPage.uploadProgress = {...}; const progress = this.filesPage.uploadProgress;`)或 handler 内全走 `this.filesPage.uploadProgress.xxx`。
- Minor 同轮修:① 上传失败/409-取消 toast 直接展示原始 responseText(JSON 串)——解析 `.error` 字段兜底原文;② fileWrite 未对齐 api() 的 401 logout 处理——补齐;③ renameFile 成功后 selected 残留旧名(同目录 loadFiles 不触发清空,后续批量操作按旧名 404)——rename 成功后从 selected 剔除旧名/清空;④ transferPickerDirs 未过滤 `.recycle`(可把文件移动/复制进回收站干扰 restore)——filter 掉;⑤ 删除失败复用 `files.transfer_partial_fail` 键语义错位——改通用名 `files.batch_partial_fail`(双语同步)。
- **浏览器 E2E(Step 5,本地 dev 面板+真实 Chrome,2026-10-13)**:登录→浏览→mkdir(UI→落盘 0755→刷新)→上传(CDP 注入 8MB 真实文件,XHR 全链路,md5 一致,public 对齐 0664)→rename→move(选择器真实点击,路径拼接 public/docs/e2e-dir 正确,落盘验证)→copy(md5 一致)→删除文件+目录(type-to-confirm 输错锁定/输对解锁,keeptree 整树进 public/.recycle/fm/docs/)→回收站页 API 条目可见→上传 409 冲突 confirm 覆盖重试成功→审计日志页 files 全操作富 detail 可见(含 409 失败记录与覆盖成功记录)→i18n EN 切换工具栏/文案正确。**发现并排除一个环境假警报**:手工种的 folders.db 缺 NOT NULL DEFAULT ''(生产 schema 有),NULL 列致 diskmgmt GetAllFolderMeta 的 rows.Scan 静默半填充(Scan 错误未检查)→RecycleBin 误读 false→删除走池根回退;修数据后 keeptree 落位正确。**遗留加固项(留 Task 12/M3 评估)**:GetAllFolderMeta 应检查 rows.Scan 错误并记日志,防 NULL/脏行静默产出半填充 meta。

### Task 12: M2 文档同步 + 发版(beta.18)

**拍板记录(2026-10-13 Task 12 开工前)**:
- **setup.sh FileBrowser 开关语义**:新增 `--with-filebrowser`;默认**整段跳过 [7/10]**(安装+config+systemd+端口校验);`command -v filebrowser` 命中(已装)→ 同样整段不动(不重装不重配不重启,"已装的不动");带开关 → 原行为不变。尾部「访问方式」的 FileBrowser 行仅已装时打印;「服务状态」循环保留(filebrowser 显示 inactive/unknown 无害)。ufw 8081/sudoers filebrowser 条目/services 页安装按钮(用户主动 opt-in)/https.go 均不动。
- **注册表 5 项条件化(连带影响,plan 原文未覆盖)**:46 项注册表含 5 个 FileBrowser 专属项(ID 6 服务/21 systemd unit/24 二进制/29 FileBrowser 用户/33 数据库——ID 29 为实现期补录,拍板原文漏计),默认不装后全新机器会 41/46 打破「注册表 46/46」验收口径。**拍板**:以 `/usr/local/bin/filebrowser` 存在性为门——不存在 → 5 项记 pass + detail「未安装(面板文件管理已取代,beta.18 起默认跳过)」;存在 → 维持真实检查。46 总数不变,新装机 41 真检 + 5 条件通过 = 46/46 口径保留;--with-filebrowser 机器全真检。
- **保留期**:`--with-filebrowser` 保留一个版本周期,beta.19(M3)评估彻底移除(含注册表 5 项删除与 46→41 总数变更、官网/手册/卸载提示,spec §十一)。

同 Task 6 清单(checklist 重算统计/CHANGELOG/TODO/红线/KB)。CHANGELOG 注明:setup.sh 本期起**新装默认不装 FileBrowser**(spec §十一 M2 节点)——Modify `scripts/setup.sh` [7/10] 段加开关(默认跳过安装,已装的不动;`--with-filebrowser` 保留安装能力一个版本周期)。

---

## M3 — v3 权限修复

### Task 13: stat + chmod + chown

**拍板记录(2026-10-13 Task 13 开工前,规格空白裁决)**:
- **Files 行矛盾修正**:抽取 `resolveOwnerGroup(info)` 必然触碰 browse.go(现内联在 listDir 循环里)——授权对 browse.go 做**最小重构**(抽函数+调用点替换,行为逐字节不变,既有 browse 测试保持绿),Files 允许清单相应加 browse.go。
- **池根防御**(写 handler 既定拍板):chmod/chown 目标为池根 → 400;stat 池根放行(只读)。
- **chown owner/group 均可选**:空串=该项不变(os.Chown 传 -1);两者全空 → 400「owner/group 至少提供一项」;非空者必须 user.Lookup/LookupGroup 解析成功,否则 400(含具体项名)。
- **符号链接语义**:非递归 chmod 目标是符号链接 → 400 拒绝(os.Chmod 跟随链接会改目标,与 recursive Walk「跳过链接」语义对齐,防误改);非递归 chown 目标是符号链接 → os.Lchown 改链接本身(放行,与递归 Walk 的 Lchown 语义一致);stat 一律 Lstat 语义(链接报自身)。
- **.recycle 段不加守卫**:chmod/chown/stat 无套娃/数据搬迁风险(管理员 root 视角工具,修复回收站内权限是合法运维动作);仅 delete 需要防套娃(已落地)。
- **审计**:chmod/chown 属 design §六.3 高危写操作,成功路径 handler 内 `common.LogAuditRequest(r, "files", "chmod"/"chown", "path=... mode=.../owner=... group=... recursive=...", "success")`;失败路径留给中间件。
- **recursive 前端确认**:type-to-confirm 属 Task 15 前端职责,后端不加确认词参数(面板内 API,与既有 recursive 删除同模式)。
- **stat 响应**:`recursive_possible` = Lstat 后 IsDir();**mode 为字符串八进制(如 "0644")**(2026-10-13 复审后改判:原措辞「数字」含混,实现落地为字符串八进制——Task 15 前端八进制输入框/预设按钮直接消费,契约以此为准)+ mode_text(如 "-rw-r--r--",对照 listDir 既有格式保持一致)。

**Files:** Create `web/modules/filemgmt/perms.go` + `perms_test.go`;Modify `filemgmt.go`

**Interfaces:** Produces:
- `handleStat`(GET `/api/files/stat?path=`)→ `{"owner","group","mode","mode_text","recursive_possible":bool}`(owner/group 解析复用 listDir 的 user.LookupId 逻辑,抽公共函数 `resolveOwnerGroup(info)`)
- `handleChmod`(POST `{path, mode, recursive}`)— mode 必须 `^[0-7]{3,4}$` 且 ≤0777;recursive 用 filepath.Walk+os.Chmod(**禁 SudoExec chmod**,sudoers 无 chmod -R);符号链接跳过不 chmod(Lstat 判别)
- `handleChown`(POST `{path, owner, group, recursive}`)— owner/group 必须 user.Lookup/.LookupGroup 存在的系统账号;os.Chown(uid,gid);recursive Walk;符号链接用 os.Lchown(不改目标)
- `func parseMode(s string) (os.FileMode, error)`(纯函数,单测)

- [x] Step 1: 失败测试 — parseMode("0644")=0644 / ("755")=0755 / ("9999")err / ("rwx")err;chmod 单文件生效;chmod recursive 子树生效且符号链接目标 mode 不变;chown 到不存在用户 400;chown recursive 生效(Lchown 语义:链接本身 owner 变、目标不变);stat 回显与 os.Lstat 一致
- [x] Step 2: 确认失败
- [x] Step 3: 实现(注意:测试 chown 需要 root 才能成功——测试机跑测试是 root 则直测;非 root 环境 skip 并注释说明,生产同构机验收必测)
- [x] Step 4: 测试绿 + build
- [x] Step 5: Commit `feat(filemgmt): stat/chmod/chown — mode 白名单校验,递归 Walk 免 sudo,符号链接 Lchown`

### Task 14: fix-home-perms 一键模板

**拍板记录(2026-10-13 Task 14 开工前,规格空白裁决)**:
- **修复语义=只改权限属主,不做 provisioning**:递归 0700 + chown 到目标用户;**不创建缺失的默认子目录**(media/photos/documents/downloads 的创建是 EnsureUserHome/建户流程职责,修复模板不越权)。
- **home 路径**:`filepath.Join(normalizedRoot(), username)`(filemgmt 池根函数,不硬编码 /data/nas1;与 EnsureUserHome 的 const pool 语义一致但走本模块统一入口)。
- **校验链**(顺序):非 POST→405;username 含 `/` 或 `..` → 400(纵深防御,先于 Lookup);user.Lookup(username) 失败→400「用户不存在」;home 不存在或非目录→400「home 目录不存在」。
- **组解析**:chown 目标 = username 用户 + **username 同名组**(EnsureUserHome 基准 chown username:username);LookupGroup(username) 失败时回退该用户主组(u.Gid)并注释说明——比 EnsureUserHome 的 SudoExec 静默失败更稳。
- **递归实现**:复用 Task 13 的 chmodTree/chownTree 同款语义(Walk、符号链接 chmod 跳过/chown 用 Lchown);home 根本身显式 Chmod 0700 + Chown。失败即停 500 带路径,不回滚(与 Task 13 一致)。
- **审计**:成功路径 `LogAuditRequest(r, "files", "fix-home-perms", "username=...", "success")`(design §六.3 高危清单点名项)。
- **public 模板不做**(plan 原文「如需 public 修复另加 target 参数,实现时与用户确认」——M3 范围内不加,留用户拍板)。
- **测试**:构造假 home(0777 乱权限+乱属主)→ fix → 递归 0700 断言(非 root 可测)+ chown 断言 root-gated skip;不存在用户/无 home/username 带斜杠 → 400;GET → 405。

**Files:** Modify `perms.go` + 测试

**Interfaces:** Produces `handleFixHomePerms`(POST `{username}`)— 权限基准**照抄** config_sync.go 的 EnsurePoolStructure/EnsureUserHome:home `/data/nas1/<username>` 递归 0700 + chown username:username;校验 username 是系统用户(user.Lookup)且 home 目录存在,否则 400。**不含 public 模板**(public 2775 属 EnsurePoolStructure 职责,面板已有建池流程;本端点只管用户 home——如需 public 修复另加 `{target:"public"}` 参数,实现时与用户确认)

- [x] Step 1: 失败测试 — 构造假 home(乱权限 0777/错属主)→ 调 fix → 递归 0700 + owner 正确;不存在用户 400;无 home 目录 400
- [x] Step 2-4: 红→实现→绿(测试环境非 root 时 chown 断言 skip,同 Task 13)
- [x] Step 5: Commit `feat(filemgmt): fix-home-perms 一键修复 — 与 EnsureUserHome 权限基准同源(0700 owner:owner)`

### Task 15: 前端 v3(改权限对话框 + 批量)

**拍板记录(2026-10-13 Task 15 开工前,规格空白裁决)**:
- **「⋮ 菜单」落地为行内按钮**:v2 实际 UI 是行内 btn-xs 按钮组(下载/预览/重命名/移动/复制/删除),无 ⋮ 菜单结构——在行内按钮组追加「属性」按钮打开权限对话框,不引入新菜单组件(与既有交互模式一致)。
- **对话框数据源**:打开时 GET /api/files/stat 回显 owner/group/mode(字符串八进制)/mode_text;mode 输入框实时 JS 预览符号位(如 0644→-rw-r--r--);预设按钮 0644/0755/0700;recursive 勾选框仅 stat.recursive_possible=true(目录)时显示;owner/group 下拉数据源 = 顶层 this.users / this.userGroups(既有 loadUsers()/loadUserGroups(),对话框打开时若为空则懒加载)。
- **单条保存语义**:mode 有填 → POST chmod;owner/group 有改动 → POST chown(只传改动项,后端空串=不变);两个请求独立,都成功才关对话框刷新列表,失败各自展示错误。
- **批量仅 chmod**(Step 3「改权限」):多选工具栏加「改权限」按钮 → 对话框只含 mode+recursive(不显示 stat——多条无单一 stat;recursive 勾选对其中目录生效、文件自动按非递归处理);保存时前端串行逐条 POST + 进度 N/M + 结果汇总 toast(成功 X 失败 Y+失败原因列表)。批量 chown 不做(owner 变更属高危低频,单条对话框已覆盖)。
- **危险确认(type-to-confirm)**:design §六.4 要求 recursive 操作前端确认——(a) 单条目录勾 recursive、(b) 批量(≥2 条)、(c) 用户页 fix-home-perms 三种场景必须输入确认词才能提交:(a)(b) 确认词=固定 i18n 文案「修改」(files.perms_confirm_word),(c) 确认词=该用户名(plan Step 2 原文)。非递归单条文件直接保存。复用既有 deleteConfirmName 输入框模式(delete 对话框对目录要求输名称,同款交互)。
- **用户页 fix-home-perms 按钮**:users 列表每行加「修复 home 权限」btn-xs,全部用户可见(home 存在性由后端校验,400 错误 toast 展示);POST /api/files/fix-home-perms {username}。
- **视觉纪律**:新 UI 一律 var(--sp-*)/var(--fs-*)/var(--radius-*) token 与 btn-xs/badge-sm 密度类,不写裸 px(nas-frontend-dev rule 6);对话框结构复用既有 files 弹窗模板(遮罩+卡片+标题+体+底部按钮)。
- **i18n**:新键全部挂 files.* 命名空间(约 12-16 键),双语 parity,scripts/check-i18n.py 必须过;锚点式文本插入改 JSON(禁 json.dump 整文件回写,skill pitfall 18a)。
- **Step 5 浏览器实测 + SMB 端验证由父代理执行**(不在 opencode 交付内):opencode 交付 Steps 1-4 + 自检(node --check app.js、check-i18n.py、go build 过 embed);父代理随后 build 二进制起本地 dev 服务器做浏览器 E2E(参照 Task 11 E2E 模式)+ smbclient 以目标用户验证 force user 映射下 0700/0775 实际读写效果。

**修订记录(2026-10-13 Step 5 浏览器 E2E 完成,SMB 端并入 Task 16)**:本地 dev 服务器(9f596de+守卫修复 c072aaa 构建)+ /data/nas1 种池实测,全部通过:① 单条属性对话框 stat 回显(owner/group/mode/mode_text)、预设按钮、modeToSymbolic 实时预览、保存后列表刷新且落盘 755;② 批量改权限:**E2E 抓到真 bug**——直接调用 saveBatchPerms() 可绕过确认词(按钮 :disabled 只是 UI 层,函数内无守卫),修复 c072aaa 补函数级 permsConfirmDisabled 守卫(savePerms/confirmFixHomePerms 同款纵深防御);修复后无确认词拦截(show 保持/落盘不变)、输「修改」后串行 2/2 进度+汇总 toast「成功 2 项,失败 0 项」+落盘 644;③ 递归目录 chmod:错词拦截、正确词后 public 树 750 落盘、**符号链接目标 mode 不变(chmodTree 跳过语义经 UI 验证)**;④ 非法 mode 999 → 后端 400 纯文本展示在对话框 error 区;⑤ 用户页 fix-home-perms:输错用户名拦截、输对后 home 树 0777→0700 落盘、成功 toast;⑥ EN 语言确认词=「confirm」(中文词在 EN 模式被拦、英文词放行,无卡死——审查顾虑#3 实证解除);⑦ 审计日志落 chmod/fix-home-perms 条目;⑧ 弹窗叠加:page 级 x-show 容器天然隔离,未见叠加。**SMB 端验证(smbclient + force user 映射下 0700/0775 实际读写)本机无 smbd,并入 Task 16 生产同构机全链路验收执行**——Step 5 复选框待 Task 16 SMB 部分完成后勾选。测试环境已清理(面板进程停、/data/nas1 还原 root 属主、临时文件删除)。

**修订记录(2026-10-13 复审修复)**:独立审查结论「需修复后合入」,Important 3 条+采纳 Minor 2 条已修复(提交 fix(filemgmt): Task 15 复审 I1-I3/M1-M2)并浏览器复验:I1 stat 失败路径——api() 对 http.Error 纯文本返回真值字符串,原 `if (!data)` 拦不住,错误文本被当 stat 数据开框;修后调用方守卫(非含 mode 对象→toast 错误文本+不开框),实测 404 场景 toast「目标不存在」框不开。I2 保存无 mode 合法性闸门——批量空 mode 可直发 N 条注定 400 的请求;修后 savePerms/saveBatchPerms 开头用 modeToSymbolic 判据拦截写 d.error,实测空 mode/1755 均零请求+错误提示。I3 单条保存 chmod 失败即中止 chown(与拍板「两请求独立、失败各自展示」不符);修后两段各自 try/catch、错误数组 join 展示、全成功才关框。M1 mode 比较归一化(去前导零),"644"≡"0644" 不再触发冗余请求(实测零 fwCalls)。M2 modeToSymbolic 正则收紧 ^0?[0-7]{3}$ 与后端 ≤0777 对齐(1755 前端即拒,不再保存时才吃 400)。**M3 裁定保留现状**:批量 1 条也触发 type-to-confirm,比拍板字面「≥2 条」更严=更安全,不改。**M4/M5/M6 不改**:手写 fetch 401 处理更干净(审查认可)/saving 期可关框与既有弹窗模式一致/懒加载 loading 指示规格未要求。回归:批量合法 mode 保存、单条保存、确认词守卫全部复验通过。

**Files:** Modify `index.html`、`app.js`、i18n

- [x] Step 1: 行内 ⋮ 菜单加"属性/权限":对话框回显 stat(owner/group/mode_text)+ mode 八进制输入(实时预览符号位)+ recursive 勾选 + owner/group 下拉(数据源:既有 users 模块的用户列表端点,grep `/api/users` 确认)+ 预设按钮(0644/0755/0700)
- [x] Step 2: 用户页(users)每个用户行加"修复 home 权限"按钮 → fix-home-perms,危险确认(type-to-confirm 输入用户名)
- [x] Step 3: 批量:多选后 [改权限] 对话框对每条逐一调用(前端串行 + 进度 N/M),结果汇总 toast
- [x] Step 4: i18n 双语 + check-i18n.py
- [x] Step 5: 浏览器实测 + **SMB 端验证**:面板 chown/chmod 后,smbclient 以该用户登录验证读写行为符合预期(force user 映射下 mask 0700/0775 的实际效果)（浏览器实测 2026-10-13 本地过；SMB 端 S1-S6 于同构机 测试机 过：跨用户 ACCESS_DENIED、force user=fm 下 0600 alice 属主文件读拒、面板 chown 回 fm 后立即可读、打坏 bob home→SMB 写拒→fix-home-perms→SMB 写恢复）
- [x] Step 6: Commit `feat(filemgmt): 前端权限管理 — stat 回显/chmod chown 对话框/fix-home-perms/批量串行`

### Task 16: M3 文档同步 + 生产同构机全链路验收 + 发版(beta.19)

**拍板记录(2026-10-13 同构机验收缺陷 D1 修复方案)**:测试机(Debian 13.6 裸机全新安装,beta.19 二进制)验收发现 **D1:默认配置下面板删除的文件在回收站页不可见不可恢复**——向导/建户创建的共享默认 recycle_bin=0,面板删除走池根回退 `.recycle/<admin>/<basename>`(仅存 basename,原位信息丢失),而回收站页 recycleRoots() 只枚举 RecycleBin=true 的共享 → API items:[] shares:{},手册「回收站页可直接恢复到原位置」承诺在默认配置下不成立(本地 E2E 因手工种库问题掩盖,同构机抓出)。**修复拍板**:
- **回退布局改池根相对 keeptree**:resolveRecycleTarget 兜底分支从 `.recycle/<admin>/<basename>` 改为 `.recycle/<admin>/<相对池根原路径...>`(如 `.recycle/fm/public/acc-dir/x.bin`)——restore 可精确还原原位;recycleConflictFree 后缀逻辑不变。
- **recycleRoots() 纳入池根伪共享**:key=`(池根 <base(pool)>)`(如「(池根 nas1)」),value=池根路径;folder meta 全部 distinct pool 去重注入;同名真实共享存在时真实共享优先(先建真实 map 再补伪条目)。空 .recycle 时 listRecycleDir 天然返回空,无害。
- **restore/delete/clear 的 share→path 解析统一**:三 handler 现走 findShareMetaPath(share)(伪名查不到)——抽 helper「伪名→池根路径,否则 findShareMetaPath」,resolveRecyclePath/恢复目标重建(sharePath+rel)对伪共享自动成立(dst=池根/public/... = 原位,前缀断言通过)。
- **前端/i18n 零改动**:回收站页 shares 下拉直接渲染 map key,伪名自解释;EN 界面暂显中文伪名(共享名本就不可翻译,同 skill 19b 惯例),不阻塞。
- **测试**:filemgmt 回退测试改断言 keeptree 布局;diskmgmt 补伪共享 list/restore 单测(按既有 recycle 测试模式构造);既有 RecycleBin=true 共享路径行为不变(回归)。
- **验收重跑项(修复部署后)**:面板删 public 文件 → 回收站 API items 含该文件且 shares 含「(池根 nas1)」→ restore → 文件回原位;RecycleBin=true 共享删除→share 级 .recycle→restore 回归;穿越/user 非法参数对伪共享同样拒绝。

**同构机验收已通过项(2026-10-13,beta.19=4528b93)**:全新安装 [7/10] 跳过 FileBrowser(二进制不存在/HTML 零提及)、注册表 46/46、建池 merge 4×50G、alice/bob 建户(home 0700+samba);§十三:浏览/上传 2MB md5 一致/mkdir/rename/Range 206/zip/move/copy 全过;穿越攻击 6 用例全拒(..%2f 双重编码 404 fail-closed);审计 13 条含 chmod/chown/fix-home-perms;root 实测补齐单测 skip 项(chmod 生效/chown 生效/1755 拒/池根拒/fix-home-perms 777 root:root→700 alice:alice);SMB S1-S6:跨用户 ACCESS_DENIED、force user=fm 下 0600 alice 属主文件读拒(权限位真实生效)、面板 chown 回 fm 后立即可读、打坏 bob home→SMB 写拒→fix-home-perms→SMB 写恢复。

- [x] Step 1: 文档清单同 Task 6;CHANGELOG 注明 FileBrowser 卸载提示(cleanup 路径)（4528b93 + D1 修复条目补充;checklist 177 项/18 模块 awk 实算;TODO#34 M3 全落地;手册 7.8 v3 + FileBrowser 退役说明 + 删除回退伪共享语义同步）
- [x] Step 2: **Debian 13 同构机全链路验收**(spec §十三 清单逐项打勾):2 用户 + home → 浏览/上传/下载/移动/删除/回收站 restore → chown 后 smbclient 实测 → 穿越攻击用例(`?path=../../etc/passwd`、符号链接、`..%2f`)全拒 → 审计日志逐条可查 → i18n 完整（2026-10-13 @ 测试机 Debian 13.6 裸机全新安装,详见节首验收记录;i18n 本地 check-i18n 1213 键 parity;回收站 restore 经 D1 修复后复跑通过）
- [x] Step 3: 验收抓到缺陷:修 + 单测 + commit + 重部署重跑受影响项,全绿才 tag（D1:d7c2a12 + f34795a,重部署 f34795a 二进制后 R1-R5 复跑全绿:回退 keeptree/伪共享 list/原位 restore/穿越拒绝/clear confirm 校验/RecycleBin=true share 级回归）
- [ ] Step 4: tag v1.4.0-beta.19 + push;release.sh 与否用户拍板
- [ ] Step 5: KB 同步 + nas-storage-architecture skill 更新(新增 filemgmt 模块的坑与约定:resolvePoolPath 唯一入口、.recycle 同源、os.Chmod 免 sudo 等)

---

## Self-Review 记录

- **Spec 覆盖**:§二 范围→Task 2-15 全覆盖;§四 API 17 端点→list/download/zip/preview/usage(T2-4)、upload/mkdir/rename/move/copy/delete(T7-10)、stat/chmod/chown/fix-home-perms(T13-14),无遗漏;§五 数据流→T1/T9/T10 实现;§六 安全 7 条→T1(锚定)/T2(审计自动)/T3(zip 不跟链)/T4(SVG 拒)/T7(名称校验)/T13(sudo 约束);§七 前端→T5/T11/T15;§八 错误处理→各 Task 测试用例;§九 测试策略→每 Task TDD + T16 同构机;§十 里程碑=M1/M2/M3 三段;§十一 FileBrowser 淘汰→T5(入口替换)/T12(setup 默认不装)/T16(卸载提示);§十三 验收→T16 Step 2
- **占位符扫描**:Task 4 handleUsage、Task 11 目录选择器为实现要点而非逐行代码——两处均给出精确的响应结构/交互定义与依赖确认动作(grep 既有封装),属"细节完备但代码留实现自由度"的刻意取舍;其余 Task 均含可编译代码/测试
- **类型一致性**:`resolvePoolPath/ownerShareOf/validateFileName/parseMode/copyTree/sameFS/alignNewFilePerms/resolveOwnerGroup` 在定义与引用 Task 间签名一致;FileEntry JSON 字段与前端模板逐一对应(name/type/size/mode_text/owner/group/mtime/target);`.recycle/<user>/<unixnano>/` 布局与 diskmgmt/recycle.go 的 listRecycleDir 新布局(`.recycle/<user>/<rel...>`)兼容——restore 按 rel 恢复,unixnano 层只是防同名冲突的额外一级,恢复语义不变(T10 实现时验证回收站页对该布局的 list/restore 正确性,若 recycle.go 对多一级目录不兼容,则在 T10 内改为与 SMB vfs recycle 完全同构的布局)
