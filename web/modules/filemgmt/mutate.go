package filemgmt

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"nas-panel/common"
)

// validateFileName 校验新建/重命名用的裸文件名(不含路径分隔符)。
// 拒绝:空串、含 "/"、含 ".." 子串、含控制字符(< 0x20)、字节长度 > 255、
// 全名恰为 "." 或 ".."。其余放行。
func validateFileName(name string) error {
	if name == "" {
		return fmt.Errorf("名称不能为空")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("名称不能为 . 或 ..")
	}
	if strings.Contains(name, "/") {
		return fmt.Errorf("名称不能包含 /")
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("名称不能包含 ..")
	}
	for _, r := range name {
		if r < 0x20 {
			return fmt.Errorf("名称不能包含控制字符")
		}
	}
	if len(name) > 255 {
		return fmt.Errorf("名称过长")
	}
	return nil
}

func handleMkdir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体不是合法 JSON", http.StatusBadRequest)
		return
	}
	if err := validateFileName(req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	parentAbs, err := resolvePoolPath(req.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// 双重路径锚定:父目录通过后,拼接目标名的完整路径再过一次 resolvePoolPath,
	// 防止 join 后的字面路径绕过安全校验(拼接逃逸)。
	targetRel := filepath.Join(req.Path, req.Name)
	targetAbs, err := resolvePoolPath(targetRel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// targetAbs 必须等于 filepath.Join(parentAbs, req.Name);两次解析之间
	// 若出现语义漂移(例如符号链接竞态改变了归一化结果),拒绝继续,防止
	// 双重锚定退化为摆设。
	if targetAbs != filepath.Join(parentAbs, req.Name) {
		http.Error(w, "路径解析不一致", http.StatusInternalServerError)
		return
	}
	if _, err := os.Lstat(targetAbs); err == nil {
		http.Error(w, "同名已存在", http.StatusConflict)
		return
	}
	if err := os.Mkdir(targetAbs, 0755); err != nil {
		if os.IsExist(err) {
			http.Error(w, "同名已存在", http.StatusConflict)
			return
		}
		if os.IsNotExist(err) {
			http.Error(w, "父目录不存在", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// 显式 chmod,消除进程 umask(systemd 服务 umask 不定)对目录权限的影响。
	if err := os.Chmod(targetAbs, 0755); err != nil {
		os.Remove(targetAbs)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.LogAuditRequest(r, "files", "mkdir",
		fmt.Sprintf("path=%s name=%s", req.Path, req.Name), "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}

func handleRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path    string `json:"path"`
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体不是合法 JSON", http.StatusBadRequest)
		return
	}
	if err := validateFileName(req.NewName); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	srcAbs, err := resolvePoolPath(req.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// 池根本身不可重命名(path="" 或 "." 都归一化为池根)。
	if srcAbs == normalizedRoot() {
		http.Error(w, "不能重命名池根", http.StatusBadRequest)
		return
	}
	if _, err := os.Lstat(srcAbs); err != nil {
		http.Error(w, "源不存在", http.StatusNotFound)
		return
	}
	// 双重路径锚定:目标 = 源的父目录 + 新名,拼接后再过一次 resolvePoolPath
	// (防止拼接逃逸;源父目录天然在池内,但仍走统一入口保持一致性)。
	parentRel := filepath.Dir(req.Path)
	if parentRel == "." {
		parentRel = ""
	}
	dstRel := filepath.Join(parentRel, req.NewName)
	dstAbs, err := resolvePoolPath(dstRel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// 同名 no-op:POSIX rename 到同一路径本就是无操作成功,不发起 syscall。
	if srcAbs == dstAbs {
		common.LogAuditRequest(r, "files", "rename",
			fmt.Sprintf("path=%s new_name=%s same-name no-op", req.Path, req.NewName), "success")
		common.JSONResponse(w, map[string]interface{}{"ok": true})
		return
	}
	if _, err := os.Lstat(dstAbs); err == nil {
		http.Error(w, "目标已存在", http.StatusConflict)
		return
	}
	// renameat2(RENAME_NOREPLACE):Lstat 预检之后仍有 TOCTOU 窗口,
	// 用内核原子的"目标存在即失败"兜底,避免静默覆盖目标数据。
	if err := unix.Renameat2(unix.AT_FDCWD, srcAbs, unix.AT_FDCWD, dstAbs, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) || errors.Is(err, unix.ENOTEMPTY) {
			http.Error(w, "目标已存在", http.StatusConflict)
			return
		}
		if errors.Is(err, unix.ENOENT) {
			http.Error(w, "源不存在", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.LogAuditRequest(r, "files", "rename",
		fmt.Sprintf("path=%s new_name=%s", req.Path, req.NewName), "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}

// ---------------------------------------------------------------------------
// Task 9: move + copy
// ---------------------------------------------------------------------------

// sameFS 判断 a、b 两个已存在路径是否位于同一文件系统(比较 syscall.Stat_t.Dev)。
// 任一路径 stat 失败时保守返回 false(走 copy 路径,不冒险当作同 fs 零拷贝 rename)。
// 本包是 Linux-only 既定约束(renameat2 已先例使用 golang.org/x/sys/unix),Stat_t
// 字段按 Linux ABI 直接取用。
func sameFS(a, b string) bool {
	var sa, sb syscall.Stat_t
	if err := syscall.Stat(a, &sa); err != nil {
		return false
	}
	if err := syscall.Stat(b, &sb); err != nil {
		return false
	}
	return sa.Dev == sb.Dev
}

// copyTree 把 src 整树流式复制到 dst,返回写入的普通文件总字节数。
//
// 规则:
//   - 目录:os.MkdirAll 建出,mode 取源目录 mode(显式 chmod,消解进程 umask 影响)。
//   - 普通文件:os.OpenFile(O_WRONLY|O_CREATE|O_EXCL, 0666) + io.Copy,写完后
//     chmod 为源文件 mode(先按源 mode 落地,调用方可再用 alignNewFilePerms 覆盖
//     为目标共享语义——copy 两段式对齐与上传一致)。
//   - 符号链接:只拷链接本身(os.Readlink + os.Symlink),不跟随、不计入返回字节数。
//
// 任何一步失败立即返回错误;半成品清理由调用方负责(copyTree 本身不做自清)。
func copyTree(src, dst string) (int64, error) {
	var total int64
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)

		info, ierr := d.Info() // Lstat 语义,不跟随符号链接
		if ierr != nil {
			return ierr
		}

		switch {
		case info.Mode()&os.ModeSymlink != 0:
			linkTarget, lerr := os.Readlink(path)
			if lerr != nil {
				return lerr
			}
			if serr := os.Symlink(linkTarget, target); serr != nil {
				return serr
			}
			return nil
		case d.IsDir():
			if merr := os.MkdirAll(target, info.Mode().Perm()); merr != nil {
				return merr
			}
			return os.Chmod(target, info.Mode().Perm())
		default:
			n, cerr := copyFile(path, target, info.Mode().Perm())
			if cerr != nil {
				return cerr
			}
			total += n
			return nil
		}
	})
	if err != nil {
		return total, err
	}
	return total, nil
}

// copyFile 流式复制单个普通文件 src → dst,写完后 chmod 为 mode。返回写入字节数。
func copyFile(src, dst string, mode os.FileMode) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		return 0, err
	}
	n, cerr := io.Copy(out, in)
	if cerr != nil {
		out.Close()
		return n, cerr
	}
	if cerr := out.Close(); cerr != nil {
		return n, cerr
	}
	if cerr := os.Chmod(dst, mode); cerr != nil {
		return n, cerr
	}
	return n, nil
}

// treeByteSize 统计 root 整树下普通文件的总字节数(符号链接不计,与 copyTree 的
// 字节统计口径对齐,供 moveCrossFS 做复制后校验)。
func treeByteSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		if info.Mode()&os.ModeSymlink != 0 || d.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

// moveCrossFS 是跨文件系统移动的纯编排函数:copyTree(src,dst) → 统计源树总字节 →
// 与 copyTree 返回的写入字节数比对 → 不一致立即返回错误(绝不删源)→ 一致才
// os.RemoveAll(src)。单测直接调用,不经过 HTTP 层。
func moveCrossFS(srcAbs, dstAbs string) error {
	written, err := copyTree(srcAbs, dstAbs)
	if err != nil {
		return fmt.Errorf("跨文件系统复制失败: %w", err)
	}
	srcBytes, err := treeByteSize(srcAbs)
	if err != nil {
		return fmt.Errorf("源树字节统计失败,已保留源: %w", err)
	}
	if written != srcBytes {
		return fmt.Errorf("跨文件系统移动字节校验失败(写入=%d 源=%d),已保留源,请检查目标磁盘空间", written, srcBytes)
	}
	if err := os.RemoveAll(srcAbs); err != nil {
		return fmt.Errorf("校验通过但删除源失败: %w", err)
	}
	return nil
}

// resolveMoveCopyTarget 承载 handleMove/handleCopy 共同的前置校验链:
// src 过 resolvePoolPath 并拒绝池根;dst_dir 过 resolvePoolPath 并校验存在且为目录;
// 拼接目标完整路径再过一次 resolvePoolPath + 双重锚定一致性断言;拒绝目标已存在;
// 拒绝"移动/复制到自己"。校验全部通过返回 (srcAbs, dstAbs),否则直接写错误响应并
// 返回 ok=false(调用方据此 return)。
func resolveMoveCopyTarget(w http.ResponseWriter, src, dstDir string) (srcAbs, dstAbs string, ok bool) {
	srcAbs, err := resolvePoolPath(src)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", "", false
	}
	if srcAbs == normalizedRoot() {
		http.Error(w, "不能移动池根", http.StatusBadRequest)
		return "", "", false
	}
	if _, err := os.Lstat(srcAbs); err != nil {
		http.Error(w, "源不存在", http.StatusNotFound)
		return "", "", false
	}

	dstDirAbs, err := resolvePoolPath(dstDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", "", false
	}
	dstDirInfo, err := os.Stat(dstDirAbs)
	if err != nil {
		http.Error(w, "目标目录不存在", http.StatusNotFound)
		return "", "", false
	}
	if !dstDirInfo.IsDir() {
		http.Error(w, "目标路径不是目录", http.StatusBadRequest)
		return "", "", false
	}

	base := filepath.Base(srcAbs)
	targetRel := filepath.Join(dstDir, base)
	targetAbs, err := resolvePoolPath(targetRel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", "", false
	}
	// 双重路径锚定一致性断言,对照 handleMkdir 模式。
	if targetAbs != filepath.Join(dstDirAbs, base) {
		http.Error(w, "路径解析不一致", http.StatusInternalServerError)
		return "", "", false
	}

	if srcAbs == targetAbs {
		http.Error(w, "不能移动/复制到自身", http.StatusBadRequest)
		return "", "", false
	}
	if _, err := os.Lstat(targetAbs); err == nil {
		http.Error(w, "目标已存在", http.StatusConflict)
		return "", "", false
	}

	return srcAbs, targetAbs, true
}

// handleMove — POST /api/files/move,JSON body {src, dst_dir}。
// 同 fs 用 os.Rename 零拷贝;跨 fs 走 moveCrossFS(copy+校验+删源)。
func handleMove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Src    string `json:"src"`
		DstDir string `json:"dst_dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体不是合法 JSON", http.StatusBadRequest)
		return
	}
	srcAbs, dstAbs, ok := resolveMoveCopyTarget(w, req.Src, req.DstDir)
	if !ok {
		return
	}

	dstDirAbs := filepath.Dir(dstAbs)
	if sameFS(srcAbs, dstDirAbs) {
		if err := os.Rename(srcAbs, dstAbs); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		if err := moveCrossFS(srcAbs, dstAbs); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	common.LogAuditRequest(r, "files", "move",
		fmt.Sprintf("src=%s dst_dir=%s", req.Src, req.DstDir), "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}

// handleCopy — POST /api/files/copy,JSON body {src, dst_dir}。
// 一律 copyTree(同 fs 也走复制语义,不借 rename 偷懒);新落盘文件逐个走
// alignNewFilePerms 对齐目标共享权限,失败则清理本次新建的目标半成品。
func handleCopy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Src    string `json:"src"`
		DstDir string `json:"dst_dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体不是合法 JSON", http.StatusBadRequest)
		return
	}
	srcAbs, dstAbs, ok := resolveMoveCopyTarget(w, req.Src, req.DstDir)
	if !ok {
		return
	}

	if _, err := copyTree(srcAbs, dstAbs); err != nil {
		os.RemoveAll(dstAbs) // 目标是本次新建的,RemoveAll 安全;源不受影响。
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := alignCopiedTreePerms(dstAbs); err != nil {
		os.RemoveAll(dstAbs)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	common.LogAuditRequest(r, "files", "copy",
		fmt.Sprintf("src=%s dst_dir=%s", req.Src, req.DstDir), "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}

// alignCopiedTreePerms 对 copyTree 刚落盘的目标树逐个新文件走 alignNewFilePerms
// (upload.go 既有函数,权限对齐语义与上传一致)。符号链接跳过(Lchown 不做,
// 对照拍板记录"符号链接跳过 chmod/chown");目录本身不重新 chmod(copyTree 已按
// 源目录 mode 落地,保留目录 mode 语义,仅对普通文件做共享权限对齐)。
func alignCopiedTreePerms(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		if d.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		return alignNewFilePerms(path)
	})
}
