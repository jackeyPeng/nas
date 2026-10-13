package filemgmt

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"nas-panel/common"
)

// modeRe 匹配 3~4 位八进制权限字符串(如 "644"、"0644"、"0777")。
var modeRe = regexp.MustCompile(`^[0-7]{3,4}$`)

// parseMode 校验并解析 chmod 请求里的 mode 字符串。
// 必须匹配 ^[0-7]{3,4}$ 且数值 ≤ 0777(即不放行 setuid/setgid/sticky 等高位)。
func parseMode(s string) (os.FileMode, error) {
	if !modeRe.MatchString(s) {
		return 0, fmt.Errorf("mode 必须是 3~4 位八进制数字(如 0644)")
	}
	v, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("mode 解析失败: %w", err)
	}
	if v > 0777 {
		return 0, fmt.Errorf("mode 不能超过 0777(不支持 setuid/setgid/sticky 位)")
	}
	return os.FileMode(v), nil
}

// resolveOwnerGroup 把 fs.FileInfo(Lstat 语义)的 uid/gid 解析为用户名/组名。
// 解析失败(系统无对应账号)时回退数字串,与 browse.go 既有 listDir 行为一致。
// browse.go 的 listDir 与本文件的 handleStat 共用此实现。
func resolveOwnerGroup(info fs.FileInfo) (owner, group string) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ""
	}
	if u, uerr := user.LookupId(strconv.Itoa(int(stat.Uid))); uerr == nil {
		owner = u.Username
	} else {
		owner = strconv.Itoa(int(stat.Uid))
	}
	if g, gerr := user.LookupGroupId(strconv.Itoa(int(stat.Gid))); gerr == nil {
		group = g.Name
	} else {
		group = strconv.Itoa(int(stat.Gid))
	}
	return owner, group
}

// ---------------------------------------------------------------------------
// handleStat
// ---------------------------------------------------------------------------

// handleStat — GET /api/files/stat?path=,只读,池根放行。
// 一律 Lstat 语义:符号链接报自身的 owner/mode,不跟随。
func handleStat(w http.ResponseWriter, r *http.Request) {
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
	info, err := os.Lstat(abs)
	if err != nil {
		http.Error(w, "目标不存在", http.StatusNotFound)
		return
	}
	owner, group := resolveOwnerGroup(info)
	common.JSONResponse(w, map[string]interface{}{
		"owner":              owner,
		"group":              group,
		"mode":               "0" + strconv.FormatInt(int64(info.Mode().Perm()), 8),
		"mode_text":          info.Mode().String(),
		"recursive_possible": info.IsDir(),
	})
}

// ---------------------------------------------------------------------------
// handleChmod
// ---------------------------------------------------------------------------

// handleChmod — POST /api/files/chmod,JSON body {path, mode, recursive}。
// 池根拒绝;符号链接非递归拒绝(os.Chmod 会跟随链接改到目标,与递归 Walk
// "跳过链接"语义冲突,防误改);递归用 filepath.Walk + os.Chmod(禁 SudoExec,
// sudoers 白名单无 chmod -R)。
func handleChmod(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path      string `json:"path"`
		Mode      string `json:"mode"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体不是合法 JSON", http.StatusBadRequest)
		return
	}
	mode, err := parseMode(req.Mode)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	targetAbs, err := resolvePoolPath(req.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if targetAbs == normalizedRoot() {
		http.Error(w, "不能修改池根权限", http.StatusBadRequest)
		return
	}
	info, err := os.Lstat(targetAbs)
	if err != nil {
		http.Error(w, "目标不存在", http.StatusNotFound)
		return
	}

	isSymlink := info.Mode()&os.ModeSymlink != 0
	if req.Recursive {
		if !info.IsDir() {
			http.Error(w, "recursive 仅适用于目录", http.StatusBadRequest)
			return
		}
		if err := chmodTree(targetAbs, mode); err != nil {
			http.Error(w, fmt.Sprintf("递归 chmod 失败: %v", err), http.StatusInternalServerError)
			return
		}
	} else {
		if isSymlink {
			http.Error(w, "符号链接不支持 chmod(会作用于目标)", http.StatusBadRequest)
			return
		}
		if err := os.Chmod(targetAbs, mode); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	common.LogAuditRequest(r, "files", "chmod",
		fmt.Sprintf("path=%s mode=0%o recursive=%v", req.Path, mode, req.Recursive), "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}

// chmodTree 对 root 整树递归 chmod。filepath.Walk 不跟随符号链接,用
// d.Type()&os.ModeSymlink 判别并跳过(根自身是链接的情形已在 handleChmod
// 调用前被拦)。单个失败立即停止并返回错误(已完成部分不回滚)。
func chmodTree(root string, mode os.FileMode) error {
	return filepath.Walk(root, func(path string, d os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if d.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if cerr := os.Chmod(path, mode); cerr != nil {
			return fmt.Errorf("chmod %s 失败: %w", path, cerr)
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// handleChown
// ---------------------------------------------------------------------------

// handleChown — POST /api/files/chown,JSON body {path, owner, group, recursive}。
// owner/group 均可选:空串=不变(传 -1);两者全空 400;非空者必须 user.Lookup/
// user.LookupGroup 解析成功,否则 400。符号链接非递归用 os.Lchown(改链接本身,
// 放行);递归用 filepath.Walk,符号链接用 os.Lchown,其余 os.Chown。
func handleChown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path      string `json:"path"`
		Owner     string `json:"owner"`
		Group     string `json:"group"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体不是合法 JSON", http.StatusBadRequest)
		return
	}
	if req.Owner == "" && req.Group == "" {
		http.Error(w, "owner/group 至少提供一项", http.StatusBadRequest)
		return
	}
	uid := -1
	if req.Owner != "" {
		u, err := user.Lookup(req.Owner)
		if err != nil {
			http.Error(w, fmt.Sprintf("owner 不存在: %s", req.Owner), http.StatusBadRequest)
			return
		}
		uid, err = strconv.Atoi(u.Uid)
		if err != nil {
			http.Error(w, fmt.Sprintf("owner uid 解析失败: %s", req.Owner), http.StatusBadRequest)
			return
		}
	}
	gid := -1
	if req.Group != "" {
		g, err := user.LookupGroup(req.Group)
		if err != nil {
			http.Error(w, fmt.Sprintf("group 不存在: %s", req.Group), http.StatusBadRequest)
			return
		}
		gid, err = strconv.Atoi(g.Gid)
		if err != nil {
			http.Error(w, fmt.Sprintf("group gid 解析失败: %s", req.Group), http.StatusBadRequest)
			return
		}
	}

	targetAbs, err := resolvePoolPath(req.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if targetAbs == normalizedRoot() {
		http.Error(w, "不能修改池根所有者", http.StatusBadRequest)
		return
	}
	info, err := os.Lstat(targetAbs)
	if err != nil {
		http.Error(w, "目标不存在", http.StatusNotFound)
		return
	}

	if req.Recursive {
		if !info.IsDir() {
			http.Error(w, "recursive 仅适用于目录", http.StatusBadRequest)
			return
		}
		if err := chownTree(targetAbs, uid, gid); err != nil {
			http.Error(w, fmt.Sprintf("递归 chown 失败: %v", err), http.StatusInternalServerError)
			return
		}
	} else {
		isSymlink := info.Mode()&os.ModeSymlink != 0
		if isSymlink {
			if err := os.Lchown(targetAbs, uid, gid); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			if err := os.Chown(targetAbs, uid, gid); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	common.LogAuditRequest(r, "files", "chown",
		fmt.Sprintf("path=%s owner=%s group=%s recursive=%v", req.Path, req.Owner, req.Group, req.Recursive), "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}

// chownTree 对 root 整树递归 chown。符号链接用 os.Lchown(不改目标),其余用
// os.Chown。单个失败立即停止并返回错误(已完成部分不回滚)。
func chownTree(root string, uid, gid int) error {
	return filepath.Walk(root, func(path string, d os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if d.Mode()&os.ModeSymlink != 0 {
			if cerr := os.Lchown(path, uid, gid); cerr != nil {
				return fmt.Errorf("chown %s 失败: %w", path, cerr)
			}
			return nil
		}
		if cerr := os.Chown(path, uid, gid); cerr != nil {
			return fmt.Errorf("chown %s 失败: %w", path, cerr)
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// handleFixHomePerms
// ---------------------------------------------------------------------------

// usernameRe 校验 fix-home-perms 请求中的 username:禁止 "/" 与 ".."
// (纵深防御,先于 user.Lookup;与 resolvePoolPath 的路径校验互补,此处只约束
// 单个路径片段级别的安全性,不复用 resolvePoolPath——username 不是池内相对路径,
// 而是用来拼接 home 目录名的单段标识符)。
func validUsernameSegment(username string) bool {
	if username == "" || username == ".." {
		return false
	}
	return !strings.Contains(username, "/") && !strings.Contains(username, "..")
}

// handleFixHomePerms — POST /api/files/fix-home-perms,JSON body {username}。
// 一键修复用户 home 目录权限属主,与 config_sync.go 的 EnsureUserHome 权限基准
// 同源(0700 owner:owner)。只改权限/属主,不创建缺失子目录(provisioning 是
// EnsureUserHome 的职责,本端点不越权)。
func handleFixHomePerms(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体不是合法 JSON", http.StatusBadRequest)
		return
	}
	if !validUsernameSegment(req.Username) {
		http.Error(w, "username 不合法", http.StatusBadRequest)
		return
	}
	u, err := user.Lookup(req.Username)
	if err != nil {
		http.Error(w, "用户不存在", http.StatusBadRequest)
		return
	}
	home := filepath.Join(normalizedRoot(), req.Username)
	// Task 14 复审 Minor-1:Lstat 拒绝符号链接 home——os.Stat 会跟随链接,
	// 若池根下 <username> 是指向池外目录的链接,后续递归 chmod/chown 会作用
	// 到池外目标。与 handleChown 根节点的 Lstat 习惯对齐,fail-closed。
	info, err := os.Lstat(home)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		http.Error(w, "home 目录不存在", http.StatusBadRequest)
		return
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		http.Error(w, fmt.Sprintf("uid 解析失败: %s", req.Username), http.StatusInternalServerError)
		return
	}
	// 组解析:优先用户同名组(与 EnsureUserHome 的 chown username:username 基准
	// 一致);LookupGroup 失败(如系统未建同名组)时回退该用户的主组(u.Gid),
	// 比 EnsureUserHome 底层 SudoExec 静默失败更稳——至少保证 gid 落在该用户
	// 确实所属的组上。
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		http.Error(w, fmt.Sprintf("gid 解析失败: %s", req.Username), http.StatusInternalServerError)
		return
	}
	if g, gerr := user.LookupGroup(req.Username); gerr == nil {
		if parsedGid, perr := strconv.Atoi(g.Gid); perr == nil {
			gid = parsedGid
		}
	}

	if err := os.Chmod(home, 0700); err != nil {
		http.Error(w, fmt.Sprintf("chmod %s 失败: %v", home, err), http.StatusInternalServerError)
		return
	}
	if err := os.Chown(home, uid, gid); err != nil {
		http.Error(w, fmt.Sprintf("chown %s 失败: %v", home, err), http.StatusInternalServerError)
		return
	}
	if err := chmodTree(home, 0700); err != nil {
		http.Error(w, fmt.Sprintf("递归 chmod 失败: %v", err), http.StatusInternalServerError)
		return
	}
	if err := chownTree(home, uid, gid); err != nil {
		http.Error(w, fmt.Sprintf("递归 chown 失败: %v", err), http.StatusInternalServerError)
		return
	}

	common.LogAuditRequest(r, "files", "fix-home-perms",
		fmt.Sprintf("username=%s", req.Username), "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}
