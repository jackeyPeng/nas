package filemgmt

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"nas-panel/common"
)

// recycleDirName 与 diskmgmt/recycle.go 的 recycleDirName 同值同源
// (diskmgmt 那份未导出,故在本模块定义同值常量)。
const recycleDirName = ".recycle"

// validateFileName 校验新建/重命名用的裸文件名(不含路径分隔符)。
// 拒绝:空串、含 "/"、含 ".." 子串、含控制字符(< 0x20)、字节长度 > 255、
// 全名恰为 "." 或 ".."。其余放行。
func validateFileName(name string) error {
	if name == "" {
		return fmt.Errorf("名称不能为空")
	}
	if name == "." || name == ".." {
		//lint:ignore ST1005 消息以 .. 字面量结尾是语义需要，非句读
		return fmt.Errorf("名称不能为 . 或 ..")
	}
	if strings.Contains(name, "/") {
		return fmt.Errorf("名称不能包含 /")
	}
	if strings.Contains(name, "..") {
		//lint:ignore ST1005 同上
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
	// Lstat 口径(Task 9 review Minor#1):rename/copyTree 操作的都是链接本身,
	// Stat 跟随链接会对跨 fs 链接误判 fs。
	var sa, sb syscall.Stat_t
	if err := syscall.Lstat(a, &sa); err != nil {
		return false
	}
	if err := syscall.Lstat(b, &sb); err != nil {
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
	// 竞态说明(Task 9 review Minor#2):treeByteSize 在 copyTree 之后统计,
	// 复制期间源被并发修改会导致字节比对不一致——失败方向安全(保留源),接受该竞态。
	srcBytes, err := treeByteSize(srcAbs)
	if err != nil {
		return fmt.Errorf("源树字节统计失败,已保留源: %w", err)
	}
	if written != srcBytes {
		return fmt.Errorf("跨文件系统移动字节校验失败(写入=%d 源=%d),已保留源,请检查目标磁盘空间", written, srcBytes)
	}
	// 权限对齐(Task 9 review Important#3):拍板「copy 与跨 fs move 新落盘文件
	// 逐个走 alignNewFilePerms」。必须在 RemoveAll(src) 之前——对齐失败则清理
	// 目标、保留源,任何失败点源都还在。
	if err := alignCopiedTreePerms(dstAbs); err != nil {
		os.RemoveAll(dstAbs)
		return fmt.Errorf("跨文件系统移动权限对齐失败,已保留源并清理目标: %w", err)
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
	// Task 9 review Critical#1:目标目录不能在源内部(含源本身)——否则
	// copyTree/递归 rename 会在新建的 dst 内看见自己继续向下自我嵌套,
	// 失控写满磁盘(reviewer 沙箱实证)。GNU cp 同款语义:dst 恰为 src 或
	// 是 src 的子目录都拒绝。该校验在 copyTree/renameat2 调用之前生效,
	// move/copy 共用本函数,改一处两边生效。
	// follow-up(Task 9 复审 Important#1):containment 用 EvalSymlinks 解析后
	// 的真实路径比较——resolvePoolPath 放行池内符号链接且返回字面路径,
	// 池内 link → src 子树的构造可绕过字面前缀检查(两路径此处均已确认
	// 存在,解析必成功;解析失败保守拒绝)。
	srcReal, errS := filepath.EvalSymlinks(srcAbs)
	dstDirReal, errD := filepath.EvalSymlinks(dstDirAbs)
	if errS != nil || errD != nil {
		http.Error(w, "路径解析失败", http.StatusBadRequest)
		return "", "", false
	}
	if dstDirReal == srcReal || strings.HasPrefix(dstDirReal, srcReal+string(filepath.Separator)) {
		http.Error(w, "目标目录不能在源内部", http.StatusBadRequest)
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
// 同 fs 用 renameat2(RENAME_NOREPLACE) 零拷贝;跨 fs 走 moveCrossFS(copy+校验+删源)。
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
		// renameat2(RENAME_NOREPLACE)(Task 9 review Important#1):裸 os.Rename
		// 在 409 预检与 rename 之间的 TOCTOU 窗口会静默覆盖目标,违反
		// 「目标存在一律 409」拍板;与 handleRename(Task 7)同款内核原子兜底。
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
	} else {
		if err := moveCrossFS(srcAbs, dstAbs); err != nil {
			// 半成品清理(Task 9 review Important#4):失败后不清目标会被 409 卡死重试。
			// EEXIST 例外(Task 9 review Important#2 同款):目标树内有并发方创建的内容
			// 时不得 RemoveAll 误删。
			if errors.Is(err, fs.ErrExist) {
				http.Error(w, "目标已存在", http.StatusConflict)
				return
			}
			os.RemoveAll(dstAbs)
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
		// EEXIST 分流(Task 9 review Important#2):copyFile(O_EXCL)/os.Symlink 撞
		// 并发方已建目标时,目标树内含非本次创建的内容,不得 RemoveAll 误删;
		// 其余错误目标确为本次新建(已过 409 预检),RemoveAll 安全。
		if errors.Is(err, fs.ErrExist) {
			http.Error(w, "目标已存在", http.StatusConflict)
			return
		}
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

// ---------------------------------------------------------------------------
// Task 10: delete → .recycle(与 diskmgmt/recycle.go 回收站页同构 keeptree 布局)
// ---------------------------------------------------------------------------

// pathHasRecycleSegment 判断相对路径(按 "/" 切分)是否任一段恰为 recycleDirName,
// 用于防止删除 .recycle 自身内容(应在回收站页管理,不走本端点)。
func pathHasRecycleSegment(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if seg == recycleDirName {
			return true
		}
	}
	return false
}

// recycleAdminUser 解析审计用户名:优先 common.RequestUsername(JWT 身份),
// 空则兜底 common.GetNASUser()(测试环境无 JWT 走此分支)。
func recycleAdminUser(r *http.Request) string {
	if u := common.RequestUsername(r); u != "" {
		return u
	}
	return common.GetNASUser()
}

// resolveRecycleTarget 算出 srcAbs 应落入的回收站目标路径(未处理冲突):
//   - found(ownerShareOfVia 命中所属共享)且 RecycleBin=true → <share.Path>/.recycle/<admin>/<srcAbs 相对 share.Path 的原相对路径...>(keeptree)
//   - 其余情形(RecycleBin=false 共享回退 / 池根散落文件)→ <poolRoot>/.recycle/<admin>/<srcAbs 相对池根的原相对路径...>(keeptree)
//
// D1 修复(2026-10-13 同构机验收):池根回退分支此前只存 filepath.Base(srcAbs),
// 丢失原路径信息,导致回收站页(diskmgmt recycleRoots 纳入池根伪共享后)无法按
// 原路径 restore。改为相对池根的 keeptree,池根散落文件(rel 无斜杠)retains
// 与旧 basename 布局等价,天然向后兼容。
func resolveRecycleTarget(srcAbs, admin string) (string, error) {
	meta, found := ownerShareOfVia(srcAbs)
	// Task 10 review Important#2:所属共享未启用回收站(RecycleBin=false)时回退池根
	// .recycle——share 级 .recycle 对回收站页不可见(recycleRoots 只枚举 RecycleBin
	// 共享)且 smb.conf 无 hide files 会对客户端可见;池根回收站是既定兜底语义。
	if found && meta.RecycleBin {
		shareClean := filepath.Clean(meta.Path)
		rel, err := filepath.Rel(shareClean, srcAbs)
		if err != nil {
			return "", err
		}
		return filepath.Join(shareClean, recycleDirName, admin, rel), nil
	}
	// srcAbs 已经过 resolvePoolPath 锚定在池根内,rel 正常情况下不可能上逃;
	// 以 ".." 开头仍作防御性拒绝,不信任该不变式到底。
	rel, err := filepath.Rel(normalizedRoot(), srcAbs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("路径解析异常: 相对池根路径越界")
	}
	return filepath.Join(normalizedRoot(), recycleDirName, admin, rel), nil
}

// recycleConflictFree 若 target 已存在(os.Lstat 口径,目录/文件/符号链接均算冲突),
// 对 basename 追加 ".<unixnano>" 后缀重试,直到不冲突。目录整树带后缀同理
// (后缀加在最终叶子名上,父目录结构不变)。
func recycleConflictFree(target string) string {
	candidate := target
	for {
		if _, err := os.Lstat(candidate); err != nil {
			return candidate
		}
		candidate = fmt.Sprintf("%s.%d", target, time.Now().UnixNano())
	}
}

// handleDelete — POST /api/files/delete,JSON body {path}。
// 删除 = os.Rename 到所属共享(或池根)的 .recycle/<admin>/ 下,零拷贝;
// 与回收站页(diskmgmt/recycle.go)keeptree 布局同构,删除后天然可见可 restore。
//
// F1 Task 3:守卫链已抽取进 export.go 的 MoveToRecycle(内核用哨兵错误),
// 本函数只负责"解析路径 → 调内核 → 把哨兵错误映射回原 HTTP 码/文案"，
// 逐字保留原行为(前端与验收套件依赖这些文案)。
func handleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体不是合法 JSON", http.StatusBadRequest)
		return
	}
	srcAbs, err := resolvePoolPath(req.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	admin := recycleAdminUser(r)
	recycleTarget, err := MoveToRecycle(srcAbs, admin)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotInPool):
			http.Error(w, "不能删除池根", http.StatusBadRequest)
		case errors.Is(err, ErrIsRecycleContent):
			http.Error(w, "回收站内容请在回收站页管理", http.StatusBadRequest)
		case errors.Is(err, ErrIsShareRoot):
			http.Error(w, "不能删除共享文件夹本身,请在存储页操作", http.StatusBadRequest)
		case errors.Is(err, ErrNotFound):
			http.Error(w, "源不存在", http.StatusNotFound)
		case errors.Is(err, ErrBadActor):
			http.Error(w, "删除者用户名非法", http.StatusBadRequest)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	recycleRel, rerr := filepath.Rel(normalizedRoot(), recycleTarget)
	if rerr != nil {
		recycleRel = recycleTarget
	}
	common.LogAuditRequest(r, "files", "delete",
		fmt.Sprintf("path=%s recycle=%s", req.Path, recycleRel), "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true, "recycle_path": recycleRel})
}
