package filemgmt

import (
	"crypto/sha256"
	"encoding/hex"
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

// copiedFileRecord 记录 copyTree 复制过的单个普通文件:相对路径、复制时读到的
// 源 mtime/size(复制开始前取,用于事后判断复制期间源是否被并发改动)、以及
// 复制期间流式计算的源内容 SHA-256(与目标文件落盘后的 SHA-256 比对,能发现
// "字节数不变但内容变了"的篡改/损坏——纯字节数比较对这种情况完全失明,
// 这正是 issue #2 要修的洞:之前 moveCrossFS 只比 treeByteSize,同长度不同
// 内容的变化永远比不出来)。
type copiedFileRecord struct {
	rel        string
	srcSize    int64
	srcModTime time.Time
	srcSHA256  string
}

// copyTree 把 src 整树流式复制到 dst,返回复制的普通文件清单(含校验用的
// 源 mtime/size/SHA-256,参见 copiedFileRecord)。
//
// 规则:
//   - 目录:os.MkdirAll 建出,mode 取源目录 mode(显式 chmod,消解进程 umask 影响)。
//   - 普通文件:os.OpenFile(O_WRONLY|O_CREATE|O_EXCL, 0666) + 流式复制并同时
//     计算源内容 SHA-256,写完后 chmod 为源文件 mode(先按源 mode 落地,调用方
//     可再用 alignNewFilePerms 覆盖为目标共享语义——copy 两段式对齐与上传一致)。
//   - 符号链接:只拷链接本身(os.Readlink + os.Symlink),不跟随、不计入清单。
//
// 任何一步失败立即返回错误;半成品清理由调用方负责(copyTree 本身不做自清)。
func copyTree(src, dst string) ([]copiedFileRecord, error) {
	var records []copiedFileRecord
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
			// 复制开始前先取一次源 mtime/size——如果复制完之后重新 Stat 源
			// 发现这两个值变了,说明复制期间源被并发写过,内容可能已经和
			// 刚才拷出去的字节不一致(TOCTOU 窗口),调用方据此拒绝删源。
			preModTime := info.ModTime()
			sum, n, cerr := copyFile(path, target, info.Mode().Perm())
			if cerr != nil {
				return cerr
			}
			records = append(records, copiedFileRecord{
				rel:        rel,
				srcSize:    n,
				srcModTime: preModTime,
				srcSHA256:  sum,
			})
			return nil
		}
	})
	if err != nil {
		return records, err
	}
	return records, nil
}

// copyFile 流式复制单个普通文件 src → dst,边读边算源内容 SHA-256,写完后
// chmod 为 mode。返回源内容的十六进制 SHA-256、写入字节数。
func copyFile(src, dst string, mode os.FileMode) (string, int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", 0, err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, cerr := io.Copy(out, io.TeeReader(in, h))
	if cerr != nil {
		out.Close()
		return "", n, cerr
	}
	if cerr := out.Close(); cerr != nil {
		return "", n, cerr
	}
	if cerr := os.Chmod(dst, mode); cerr != nil {
		return "", n, cerr
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// fileSHA256 计算 path 的 SHA-256(十六进制),用于比对目标文件落盘后的内容
// 与 copyTree 复制期间记录的源内容哈希是否一致。
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// dirEntryKind 标识 dirEntrySnapshot 中一个条目的类型,用于前后快照比较时
// 区分"同名但类型变了"(例如文件被换成了目录)与"类型未变"的情况。
type dirEntryKind int

const (
	dirEntryKindFile dirEntryKind = iota
	dirEntryKindEmptyDir
	dirEntryKindSymlink
)

// dirEntrySnapshot 是 src 整树在某一时刻的条目清单:key 为相对 src 的路径,
// value 描述该条目的类型(普通文件/空目录/符号链接)及符号链接的目标。
//
// 非空目录不单独记一条"目录"条目——它的存在完全由其子项的相对路径前缀
// 体现(子项路径里天然包含了父目录名),额外记一条反而在"目录下新增/删除
// 子项"时产生冗余的虚假差异。只有空目录才需要单独记录,因为空目录没有
// 任何子项路径能替它证明自己存在。
type dirEntrySnapshot struct {
	kind       dirEntryKind
	linkTarget string // 仅 dirEntryKindSymlink 有效
}

// snapshotDirEntries 走一遍 root 整树,返回"相对路径 → 条目描述"的快照,
// 用于 moveCrossFS 在 copyTree 前后各拍一次照、比较两次快照是否完全相同
// (regular files、empty dirs、symlink 的类型+目标)。
//
// 这是本函数要解决的洞的核心:copyTree 用 filepath.WalkDir 做单次遍历,
// 只能看到"遍历那一刻"src 的样子——如果某个已经遍历过的目录在遍历完成
// 之后又被塞进新文件/新目录/新符号链接,copyTree 根本不知道这些条目
// 存在,dst 里永远不会有它们,而 moveCrossFS 原来的校验只回看 copyTree
// 记录过的 records,对"records 之外凭空出现的条目"是瞎的。前后快照直接
// 对比 src 的完整目录结构(而不是只回看已记录的文件),才能捕获这整类
// "遍历窗口之外发生的变化"——不管是新增、删除,还是条目类型被替换。
func snapshotDirEntries(root string) (map[string]dirEntrySnapshot, error) {
	snap := make(map[string]dirEntrySnapshot)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil // 根自身不记录,只记录相对路径非空的子项
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		info, ierr := d.Info() // Lstat 语义,不跟随符号链接
		if ierr != nil {
			return ierr
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, lerr := os.Readlink(path)
			if lerr != nil {
				return lerr
			}
			snap[rel] = dirEntrySnapshot{kind: dirEntryKindSymlink, linkTarget: target}
		case d.IsDir():
			entries, derr := os.ReadDir(path)
			if derr != nil {
				return derr
			}
			if len(entries) == 0 {
				snap[rel] = dirEntrySnapshot{kind: dirEntryKindEmptyDir}
			}
			// 非空目录不记录(见类型注释:由子项相对路径前缀隐含表达)。
		default:
			snap[rel] = dirEntrySnapshot{kind: dirEntryKindFile}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}

// diffDirEntrySnapshots 比较 copyTree 前后两次 src 快照,返回描述首个差异
// 的错误;两次快照完全相同时返回 nil。能捕获:
//   - 新增条目(前没有、后有):文件、空目录、符号链接均覆盖;
//   - 删除条目(前有、后没有);
//   - 类型变化(同一相对路径,前后 kind 不同,例如文件被替换成目录);
//   - 符号链接目标变化(kind 相同均为 symlink,但 linkTarget 不同)。
//
// 不比较 before 快照里已经记录的普通文件此后内容/mtime 是否变化——那部分
// 由 moveCrossFS 步骤 2(mtime/size)和步骤 3(内容 SHA-256)负责,职责不重叠。
func diffDirEntrySnapshots(before, after map[string]dirEntrySnapshot) error {
	for rel, b := range before {
		a, ok := after[rel]
		if !ok {
			return fmt.Errorf("源目录项 %s 在复制期间被删除", rel)
		}
		if a.kind != b.kind {
			return fmt.Errorf("源目录项 %s 在复制期间类型发生变化", rel)
		}
		if b.kind == dirEntryKindSymlink && a.linkTarget != b.linkTarget {
			return fmt.Errorf("源符号链接 %s 在复制期间指向目标发生变化", rel)
		}
	}
	for rel := range after {
		if _, ok := before[rel]; !ok {
			return fmt.Errorf("源目录项 %s 在复制期间被新增,未被纳入本次复制", rel)
		}
	}
	return nil
}

// moveCrossFSResult 是 moveCrossFS 校验全部通过后的返回值。跨文件系统
// 移动不再有"真正意义上的移动完成"这一状态——源永远不会被自动删除
// (见下方 Item 1 说明),所以调用方(handleMove)必须把这个结果当作
// "已复制,源仍保留,需要人工核实后自行删除源"来展示,不能等同于同文件系统
// rename 的那种"移动已完成"。
type moveCrossFSResult struct {
	// SourceRetained 恒为 true——保留字段是为了让调用方/测试在读代码时
	// 不必去翻注释确认语义,一眼能看到"源保留"这个事实在类型里有名字。
	SourceRetained bool
}

// moveCrossFS 是跨文件系统移动的纯编排函数。流程:
//
//  1. copyTree(src, dst) 流式复制整树,边拷边记录每个文件的源内容 SHA-256、
//     写入字节数、复制前观察到的源 mtime。
//  2. 对每个文件重新 Lstat 源:若 mtime 或 size 与复制前观察到的不同,说明
//     复制期间源被并发改写过——刚才拷出去的内容可能已经和"现在的源"不是
//     同一份数据了(TOCTOU 窗口,见下方竞态说明)。
//  3. 对每个文件重新计算目标文件的 SHA-256,与 copyTree 记录的源内容哈希
//     比对——这一步捕获"写入过程本身出错但字节数碰巧没变"或"同长度不同
//     内容"的情况,纯字节数比较对这种情况是瞎的(issue #2 要修的洞)。
//  4. 对齐权限。
//
// Item 1(本轮验收修复,取代此前"校验通过就 os.RemoveAll(src)"的设计):
// 跨文件系统这一侧永远不自动删除源,不区分成功/失败——校验通过只代表
// "目标内容已确认与源一致",不代表"删源是安全的"。原因:
//   - stdlib 没有跨文件系统的原子 rename,copy+delete 之间必然存在一个
//     "目标已写完但源还没删"的窗口,这个窗口本身不可消除;
//   - 真正的风险不是这个窗口会不会被看到,而是"自动删源"这个动作本身
//     一旦执行就不可逆——任何校验逻辑的疏漏(本次修复前就有过een"同长度
//     不同内容"检测不到的洞)都会直接表现为用户数据被删除且无法恢复。
//   - 保留源、交给用户确认后自行删除,把"数据是否可以安全删除"这个判断
//     从程序交还给人,是本系统能做到的最强保证。
//
// 因此 moveCrossFS 的返回语义变成:err == nil 时,目标已创建且内容/权限
// 均已确认,但源文件树原样保留在原地,调用方(handleMove)必须把这个结果
// 展示为"已复制到目标,源已保留,请手动核对后自行删除源",不能展示为与
// 同文件系统 rename 等价的"移动成功"。
//
// 竞态说明(仍然存在,且是设计上无法完全消除的):mtime 在 ext4/xfs 上是
// 纳秒级粒度,但如果源在"copyTree 读完最后一个字节"和"步骤 2 重新 Lstat"
// 之间的极短窗口内被再次写入、且写入后的 mtime/size 恰好与复制前完全相同
// (理论可构造,真实误操作/并发写入概率极低),本方案无法检测到。这是在不
// 引入文件锁或写时复制快照(本系统没有这类机制)前提下能做到的最强保证:
// 任何可观察的变化都会导致保守失败;唯一漏网的是人为构造的"改了又在极窄
// 窗口内改回同样 mtime+size+内容"场景——但无论是否漏网,源都不会被删除。
//
// 步骤 0(本轮修复,补上 copyTree 单次遍历看不见的洞):copyTree 用
// filepath.WalkDir 对 src 做单次遍历,只能捕获"遍历那一刻"的目录结构——
// 如果某个已遍历过的目录之后又新增/删除/替换了子项(文件、空目录或符号
// 链接),copyTree 根本不知道,dst 永远不会体现这些变化,而原来的校验
// (步骤 2/3)只回看 copyTree 实际记录过的 records,对"records 之外凭空
// 出现或消失的条目"是瞎的。因此在 copyTree 前后各对 src 拍一次完整目录
// 结构快照(snapshotDirEntries),两次快照只要有任何差异(新增/删除/
// 类型变化/符号链接目标变化),就视为与步骤 2 同等级的"复制期间被并发
// 修改",走相同的保留源、拒绝成功的 fail-closed 路径。
//
// 单测直接调用,不经过 HTTP 层。
func moveCrossFS(srcAbs, dstAbs string) (moveCrossFSResult, error) {
	before, err := snapshotDirEntries(srcAbs)
	if err != nil {
		return moveCrossFSResult{}, fmt.Errorf("跨文件系统移动前置快照失败: %w", err)
	}

	records, err := copyTree(srcAbs, dstAbs)
	if err != nil {
		return moveCrossFSResult{}, fmt.Errorf("跨文件系统复制失败: %w", err)
	}

	after, err := snapshotDirEntries(srcAbs)
	if err != nil {
		return moveCrossFSResult{}, fmt.Errorf("跨文件系统移动校验失败:复制后无法重新确认源目录结构,已复制到目标但保留源,请手动核对后自行删除/复制: %w", err)
	}
	if derr := diffDirEntrySnapshots(before, after); derr != nil {
		return moveCrossFSResult{}, fmt.Errorf(
			"跨文件系统移动校验失败:%w,为安全起见不会自动删除源,已复制到目标(可能不完整),请手动核对内容后自行删除源",
			derr,
		)
	}

	// 步骤 2:逐文件重新 Lstat 源,检测复制期间是否被并发改写。
	for _, rec := range records {
		srcPath := filepath.Join(srcAbs, rec.rel)
		var st syscall.Stat_t
		if err := syscall.Lstat(srcPath, &st); err != nil {
			return moveCrossFSResult{}, fmt.Errorf("跨文件系统移动校验失败:复制后无法重新确认源文件 %s 状态,已复制到目标但保留源,请手动核对后自行删除/复制: %w", rec.rel, err)
		}
		curSize := st.Size
		curModTime := time.Unix(st.Mtim.Sec, st.Mtim.Nsec)
		if curSize != rec.srcSize || !curModTime.Equal(rec.srcModTime) {
			return moveCrossFSResult{}, fmt.Errorf(
				"跨文件系统移动校验失败:源文件 %s 在复制期间被并发修改(内容可能已与已复制内容不一致),为安全起见不会自动删除源,已复制到目标,请手动核对内容后自行删除源",
				rec.rel,
			)
		}
	}

	// 步骤 3:逐文件比对目标内容 SHA-256 与复制期间记录的源内容 SHA-256——
	// 这是本次修复的核心:纯字节数比较(治理前的旧逻辑)对"同长度不同内容"
	// 完全失明,改用内容哈希后能可靠识别写入过程中的任何数据差异。
	for _, rec := range records {
		dstPath := filepath.Join(dstAbs, rec.rel)
		dstSum, err := fileSHA256(dstPath)
		if err != nil {
			return moveCrossFSResult{}, fmt.Errorf("跨文件系统移动校验失败:无法读取目标文件 %s 校验内容,已保留源: %w", rec.rel, err)
		}
		if dstSum != rec.srcSHA256 {
			return moveCrossFSResult{}, fmt.Errorf(
				"跨文件系统移动内容校验失败:文件 %s 复制后内容哈希与源不一致,已保留源,请检查目标磁盘空间/完整性后重试",
				rec.rel,
			)
		}
	}

	// 权限对齐(Task 9 review Important#3)。对齐失败则清理目标、保留源。
	if err := alignCopiedTreePerms(dstAbs); err != nil {
		os.RemoveAll(dstAbs)
		return moveCrossFSResult{}, fmt.Errorf("跨文件系统移动权限对齐失败,已保留源并清理目标: %w", err)
	}
	// Item 1(验收修复):校验/权限对齐全部通过后,不再 os.RemoveAll(srcAbs)。
	// 源永久保留在原地,由调用方(handleMove)明确告知用户"已复制,源已保留,
	// 需要人工核对后自行删除源"——理由见函数头部注释。
	return moveCrossFSResult{SourceRetained: true}, nil
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
		common.LogAuditRequest(r, "files", "move",
			fmt.Sprintf("src=%s dst_dir=%s", req.Src, req.DstDir), "success")
		common.JSONResponse(w, map[string]interface{}{"ok": true})
		return
	}

	result, err := moveCrossFS(srcAbs, dstAbs)
	if err != nil {
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

	// Item 1(验收修复):跨文件系统移动永不删源,响应必须明确区分于同文件
	// 系统的"移动成功"——前端据此展示"源已保留,需人工核实后自行删除",
	// 不能展示为与 rename 等价的"移动成功"(ok:true 会被前端当作已完成
	// 处理)。status="copied_source_retained" 是本次新增的、调用方和前端
	// 都应据此分支渲染的信号值。
	common.LogAuditRequest(r, "files", "move",
		fmt.Sprintf("src=%s dst_dir=%s cross_fs=true source_retained=%v", req.Src, req.DstDir, result.SourceRetained), "success")
	common.JSONResponse(w, map[string]interface{}{
		"ok":              true,
		"status":          "copied_source_retained",
		"source_retained": result.SourceRetained,
		"message":         "已复制到目标，源文件已保留，请手动核对内容后自行删除源",
	})
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
