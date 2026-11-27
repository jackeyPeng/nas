package filemgmt

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"nas-panel/common"
	"nas-panel/modules/diskmgmt"
)

// getFolderMetaFn 仅供测试注入,对照 poolRootOverride 模式(path.go)。
// 生产走 diskmgmt.GetAllFolderMeta;测试可替换为返回固定/空列表的 fake。
var getFolderMetaFn = diskmgmt.GetAllFolderMeta

// maxUploadBytes 限制单次上传请求体大小,防止单个请求长时间占满磁盘 IO/磁盘空间。
// 声明为变量(非 const)仅为了让单测能注入一个小值断言 413 映射逻辑,
// 生产路径恒为 10 GiB,不对外提供运行期配置。
var maxUploadBytes int64 = 10 << 30

// ownerShareOfVia 与 path.go 的 ownerShareOf 逻辑一致,但经 getFolderMetaFn 取数据,
// 使上传权限对齐在单测里可注入 fake folders.db 结果。
func ownerShareOfVia(absPath string) (diskmgmt.FolderMeta, bool) {
	var best diskmgmt.FolderMeta
	found := false
	for _, m := range getFolderMetaFn() {
		clean := filepath.Clean(m.Path)
		prefix := clean + string(filepath.Separator)
		if hasPathPrefix(absPath, prefix) {
			if !found || len(clean) > len(filepath.Clean(best.Path)) {
				best, found = m, true
			}
		}
	}
	return best, found
}

// hasPathPrefix 判断 absPath(+分隔符)是否以 prefix 为前缀;抽成小函数避免重复 import strings 的字符串拼接易错写法。
func hasPathPrefix(absPath, prefix string) bool {
	full := absPath + string(filepath.Separator)
	return len(full) >= len(prefix) && full[:len(prefix)] == prefix
}

// alignPermsDecision 是权限对齐的纯判定函数,不做任何系统调用,便于单测表驱动覆盖。
//
// 拍板决策(TASK-8):
//   - found 且 meta.Name == "public" → (0664, nasUser, "nasusers")
//   - found 且非 public            → (0600, meta.Name, meta.Name)(home 与普通共享同规则)
//   - !found(池根直属,无所属共享)  → (0644, "", "")(空串 = 不 chown,只 chmod)
func alignPermsDecision(meta diskmgmt.FolderMeta, found bool, nasUser string) (fileMode os.FileMode, ownerName, groupName string) {
	if !found {
		return 0644, "", ""
	}
	if meta.Name == "public" {
		return 0664, nasUser, "nasusers"
	}
	return 0600, meta.Name, meta.Name
}

// alignNewFilePerms 对刚写完的新文件执行权限对齐:判定目标目录所属共享 → 纯函数算出
// mode/owner/group → chmod(必做) + 按需 chown(ownerName/groupName 非空才做)。
// chmod 失败是硬错误(调用方需清理半成品并 500);chown 查不到系统用户时跳过,只记日志。
func alignNewFilePerms(targetAbs string) error {
	return alignFilePerms(targetAbs, targetAbs)
}

// alignFilePerms 是 alignNewFilePerms 的通用版本:policyPath 用于判定所属共享
// (ownerShareOfVia 按路径前缀匹配 folders.db 元数据),inodePath 是实际执行
// chmod/chown 的文件路径——两者可以不同。
//
// Bug 1 修复(覆盖写权限对齐时序):旧代码在 writePartToFileOverwrite 完成
// os.Rename 之后才调用 alignNewFilePerms(targetAbs),即在"覆盖写已经把新
// 内容原子替换上线"之后再对齐权限,这里有两个问题:
//  1. rename 到 chmod/chown 完成之间,文件以 writePartToFileOverwrite 写入时
//     的临时权限(覆盖写之前是硬编码 0666)对外可见——存在一个权限过松的
//     时间窗口。
//  2. 更严重的是,若这步 chmod/chown 失败,handleUpload 会 os.Remove(targetAbs)
//     "清理"——但此时 targetAbs 已经是 rename 过去的新文件,原文件内容早已
//     不存在于任何路径下。Remove 直接把刚覆盖进去的新数据也删了,新旧两份
//     数据全部丢失,且找不回来(修复前的严重数据丢失 bug)。
//
// 现在 writePartToFileOverwrite 改为在 rename 之前,对还叫临时文件名的
// inode 执行本函数(policyPath=targetAbs 做共享判定,inodePath=tmpPath 做
// chmod/chown),失败时直接返回错误、只清理临时文件,原文件保持完全不动
// (fail-closed:对齐权限做不到就不覆盖,不是"覆盖了但权限可能不对")。
// rename 成功之后,新文件在被任何其他请求看到之前权限已经是最终正确值,
// 不存在中间态窗口;handleUpload 里 rename 之后不再需要、也不再调用
// alignNewFilePerms,避免重新引入上述删除已替换文件的路径。
//
// Point 3 补充说明(本轮修订,见 captureOrigOwnership/restoreOrigOwnership):
// 上述"rename 前对临时文件对齐权限"的机制本身继续保留(避免宽权限窗口、
// 避免误删已替换文件两个问题依然存在),但 writePartToFileOverwrite 现在
// 只在"原文件确实不存在"(本次其实是新建,hasOwnership=false)时才调用
// alignFilePerms 走本函数的策略对齐;只要原文件存在,就改为调用
// restoreOrigOwnership 恢复原文件自己的 mode/uid/gid,不再套用池策略——
// 覆盖写时原文件的有效权限是权威值,不应该因为内容被换了一次就被重置
// 成策略默认值(推翻 TASK-8 的旧结论)。alignFilePerms 对"新建文件"场景
// (alignNewFilePerms 调用方、以及覆盖写摁到了"原文件不存在"分支)行为不变。
func alignFilePerms(policyPath, inodePath string) error {
	meta, found := ownerShareOfVia(policyPath)
	mode, ownerName, groupName := alignPermsDecision(meta, found, common.GetNASUser())
	if err := os.Chmod(inodePath, mode); err != nil {
		return err
	}
	if ownerName == "" && groupName == "" {
		return nil
	}
	uid, uidOK := lookupUID(ownerName)
	gid, gidOK := lookupGID(groupName)
	if !uidOK || !gidOK {
		log.Printf("[FILEMGMT] 上传权限对齐跳过 chown(用户/组不存在): path=%s owner=%s group=%s",
			inodePath, ownerName, groupName)
		return nil
	}
	if err := os.Chown(inodePath, uid, gid); err != nil {
		log.Printf("[FILEMGMT] 上传权限对齐 chown 失败: path=%s err=%v", inodePath, err)
	}
	return nil
}

// --- Item 5 修复: 覆盖写必须保留原文件的 ACL/xattr --------------------------
//
// 问题:writePartToFileOverwrite 原实现只对齐 owner/mode(alignFilePerms 按
// 目标目录所属共享的策略重新计算 owner/mode,与原文件实际 owner/mode 无关),
// 完全没有处理 POSIX ACL(setfacl 设置的扩展权限)和 xattr(如 user.* 命名空间
// 下应用自定义的元数据、SELinux label 等)。覆盖写走"同目录临时文件 + rename"
// 路径,新 inode 默认没有这些扩展属性,rename 后原文件的 ACL/xattr 全部丢失,
// 即使权限位本身对齐正确。
//
// 修复:rename 之前,把原文件(targetAbs,覆盖写发生前的旧内容)的 ACL 与
// xattr dump 下来,应用到临时文件(tmpPath)上,再 rename。capture/restore
// 任一步失败都是硬错误,整个覆盖写失败,只清理临时文件,原文件保持完全不动
// (与 alignFilePerms 失败路径一致的 fail-closed 语义:宁可不覆盖,不可覆盖
// 后属性对不上)。
//
// 原文件不存在(本次上传实际是新建,不是真正覆盖)时 capture 直接返回空
// dump、无错误,restore 收到空 dump 时跳过(nil 操作),不报错。

// getfaclFn/setfaclFn/getfattrFn/setfattrFn 默认指向下面的"先尝试无 sudo 直接
// 执行,EPERM/EACCES 才回退 sudo"包装函数,对照 perms.go 的 chmodOne/chownOne
// 同款降级策略。测试注入替身以验证 capture/restore 失败时覆盖写整体失败、
// 原文件不受影响,不依赖真机是否装有 acl/attr 工具包。
var (
	getfaclFn  = runGetfacl
	setfaclFn  = runSetfacl
	getfattrFn = runGetfattr
	setfattrFn = runSetfattr
)

// runWithSudoFallback 先尝试 common.ExecOutput(无 sudo,面板进程对自己属主的
// 文件足够),失败再回退 common.SudoOutput——与 perms.go 的 isPermissionDenied
// 判定不同,这里是外部命令而非系统调用,exec.Error 不直接满足 os.ErrPermission,
// 保守地对任何失败都回退一次 sudo 再报错(sudo 对不需要提权的场景只是多一次
// 无害调用,换来对"面板进程非文件属主"场景的覆盖,与 ApplyHomeACL 的
// "失败只影响便利层"定位不同,这里 ACL/xattr 丢失是数据完整性问题,必须确认
// 两次尝试都失败才放弃)。
func runWithSudoFallback(name string, args ...string) (string, error) {
	out, err := common.ExecOutput(name, args...)
	if err == nil {
		return out, nil
	}
	sudoOut, sudoErr := common.SudoOutput(name, args...)
	if sudoErr == nil {
		return sudoOut, nil
	}
	return "", fmt.Errorf("%s %v 失败(直接执行: %v; sudo: %v)", name, args, err, sudoErr)
}

func runGetfacl(path string) (string, error) {
	return runWithSudoFallback("getfacl", "--absolute-names", "-p", path)
}

func runSetfacl(path, dump string) error {
	cmd := exec.Command("setfacl", "--set-file=-", path)
	cmd.Stdin = strings.NewReader(dump)
	if out, err := cmd.CombinedOutput(); err != nil {
		sudoCmd := exec.Command("sudo", "setfacl", "--set-file=-", path)
		sudoCmd.Stdin = strings.NewReader(dump)
		if sudoOut, sudoErr := sudoCmd.CombinedOutput(); sudoErr != nil {
			return fmt.Errorf("setfacl 恢复失败: %s (sudo 回退: %s: %v)", out, sudoOut, sudoErr)
		}
	}
	return nil
}

// runGetfattr 读取 path 的 user.* 命名空间 xattr,返回本包内部可解析的
// "name=0x<hex>\n" 逐行格式(非 getfattr --dump 原生格式——原生 dump 带
// `# file: <path>` 头,setfattr --restore=- 会把它当成恢复目标路径,见下方
// runSetfattr 注释)。只枚举 user.*,不碰 security.selinux/system.posix_acl_*
// (SELinux label 与 POSIX ACL 各有专门子系统管理,这里不越界接管)。
func runGetfattr(path string) (string, error) {
	// -m '^user\.' 只枚举 user.* 的属性名(无 -d 时 getfattr 只打印名字,不含值)。
	names, err := runWithSudoFallback("getfattr", "--absolute-names", "-m", `^user\.`, path)
	if err != nil {
		return "", err
	}
	var lines []string
	for _, line := range strings.Split(names, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.HasPrefix(line, "user.") {
			continue
		}
		name := line
		// 单独取值,-e hex 输出 "name=0x<hex>" 一行,二进制值 round-trip 安全
		// (--only-values 会忽略 -e 编码直出原始字节,不适合拼进本包内部文本格式)。
		valLine, verr := runWithSudoFallback("getfattr", "--absolute-names", "-n", name, "-e", "hex", path)
		if verr != nil {
			return "", fmt.Errorf("读取 xattr %s 失败: %w", name, verr)
		}
		for _, vl := range strings.Split(valLine, "\n") {
			vl = strings.TrimSpace(vl)
			if strings.HasPrefix(vl, name+"=") {
				lines = append(lines, vl)
				break
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}

// runSetfattr 把 runGetfattr 产出的 "name=0x<hex>\n" 逐行格式应用到 path。
//
// 历史 bug:原实现用 `setfattr --restore=-` 消费 getfattr --dump 的原生格式,
// 该格式自带 `# file: <path>` 头,--restore=- 实际写入目标是 dump 里这个
// 嵌入路径,而不是调用方传入的 path 参数(命令行里根本没有传 path)——
// 覆盖写场景下 dump 来自原文件(targetAbs),调用方想要写入的却是临时文件
// (tmpPath),二者路径不同时,属性会被错误地写回原文件路径,而不是临时文件,
// 且 runSetfattr 的 path 形参被静默忽略。
//
// 修复:改为逐属性 `setfattr -n NAME -v 0xHEX <path>`,path 由本函数显式
// 传给 exec.Command 的参数列表,不依赖 dump 内容里的任何路径字符串——
// 恢复目标恒等于调用方传入的 path,无论 dump 来自哪个原文件。
// 任一属性恢复失败按 fail-closed 处理,整体返回错误(调用方据此中止覆盖写,
// 不得假成功)。
func runSetfattr(path, dump string) error {
	dump = strings.TrimSpace(dump)
	if dump == "" {
		return nil
	}
	for _, line := range strings.Split(dump, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		eq := strings.Index(line, "=0x")
		if eq < 0 {
			return fmt.Errorf("setfattr 恢复失败: 无法解析属性行 %q", line)
		}
		name := line[:eq]
		hexVal := line[eq+1:] // 含 "0x" 前缀,setfattr -v 原生支持
		cmd := exec.Command("setfattr", "-n", name, "-v", hexVal, path)
		if out, err := cmd.CombinedOutput(); err != nil {
			sudoCmd := exec.Command("sudo", "setfattr", "-n", name, "-v", hexVal, path)
			if sudoOut, sudoErr := sudoCmd.CombinedOutput(); sudoErr != nil {
				return fmt.Errorf("setfattr 恢复属性 %s 失败: %s (sudo 回退: %s: %v)", name, out, sudoOut, sudoErr)
			}
		}
	}
	return nil
}

// origOwnership 是覆盖写之前对原文件(targetAbs)拍下的"权威快照":mode 位 +
// uid/gid。见 Point 3 拍板决策(本轮修订,推翻 TASK-8 的旧结论):覆盖写时,
// 原文件实际的 mode/ACL/uid/gid 对"覆盖写"这个操作而言是权威值,池策略
// (alignFilePerms/alignPermsDecision 算出的 0644/0664/0600 等)只对"新建
// 文件"生效——覆盖写不是新建,不应该让一个已经存在、可能被管理员/用户用
// setfacl/chown 手工调整过权限的文件,因为内容被换了一次就被重置回策略默认值。
//
// hasOwnership=false 表示原文件不存在(本次其实是新建),调用方应落回
// alignNewFilePerms 的策略对齐路径,而不是"恢复"一个不存在的权威值。
type origOwnership struct {
	mode         os.FileMode
	uid, gid     int
	hasOwnership bool
}

// statOrigFn 仅供测试注入,默认指向真实 os.Lstat 的瘦包装。
var statOrigFn = func(path string) (os.FileMode, int, int, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, 0, 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, 0, fmt.Errorf("无法读取原文件 uid/gid(平台不支持 syscall.Stat_t): %s", path)
	}
	return info.Mode(), int(st.Uid), int(st.Gid), nil
}

// chownFn/chmodFn 仅供测试注入,默认指向真实系统调用,与 getfaclFn 等同款
// seam 模式——测试断言"恢复原 uid/gid/mode 失败 → 整个覆盖写失败,原文件
// 不受影响",不依赖真机是否能真的 chown 到任意 uid(非 root 测试进程下
// chown 到别的 uid 必然失败,用这个性质反过来触发失败路径断言)。
var (
	chownFn = os.Chown
	chmodFn = os.Chmod
)

// captureOrigOwnership 在覆盖写之前读取原文件(targetAbs)的 mode/uid/gid。
// 原文件不存在(本次其实是新建)时返回 hasOwnership=false、无错误——没有
// 原值需要保留,调用方应走新建文件的策略对齐路径(alignNewFilePerms)。
// 原文件存在但读取失败(真正的 I/O/权限错误)则是硬错误,调用方必须中止覆盖写。
func captureOrigOwnership(targetAbs string) (origOwnership, error) {
	mode, uid, gid, err := statOrigFn(targetAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return origOwnership{}, nil
		}
		return origOwnership{}, fmt.Errorf("检查原文件 mode/uid/gid 失败: %w", err)
	}
	return origOwnership{mode: mode.Perm(), uid: uid, gid: gid, hasOwnership: true}, nil
}

// restoreOrigOwnership 把 captureOrigOwnership 读到的原 mode/uid/gid 恢复到
// 临时文件(inodePath)上,必须在 rename 之前调用,且必须在 restoreACLXattr
// 之后、不再有任何后续 chmod 调用——顺序原因与 restoreACLXattr 必须排在
// alignFilePerms 之前完全相同(chmod 对带 ACL 的文件会按 POSIX 语义重算
// mask,这里 chmod 回原 mode 必须是覆盖写流程里最后一次改权限的调用,否则
// 恢复的 mask 又会被后续动作覆盖)。
//
// Point 3 拍板决策:原 mode/ACL/uid/gid 对覆盖写是权威值,不可恢复
// (chmod/chown 失败)是硬错误,调用方必须中止整个覆盖写、不得 rename——
// 不存在"恢复不了就退回策略值"的降级路径,那等于默默丢弃用户此前设置的
// 权限,与"必须保留原有效权限"的要求直接矛盾。
func restoreOrigOwnership(inodePath string, orig origOwnership) error {
	if !orig.hasOwnership {
		return nil
	}
	if err := chownFn(inodePath, orig.uid, orig.gid); err != nil {
		return fmt.Errorf("恢复原文件 uid/gid 失败: %w", err)
	}
	if err := chmodFn(inodePath, orig.mode); err != nil {
		return fmt.Errorf("恢复原文件 mode 失败: %w", err)
	}
	return nil
}

// captureACLXattr 在覆盖写之前读取原文件(targetAbs)的 ACL 与 xattr dump。
// 原文件不存在(本次其实是新建)时返回空 dump、无错误——没有旧属性需要保留。
// 原文件存在但读取失败(真正的 I/O/权限错误)则是硬错误,调用方必须中止覆盖写。
func captureACLXattr(targetAbs string) (aclDump, xattrDump string, err error) {
	if _, statErr := os.Lstat(targetAbs); statErr != nil {
		if os.IsNotExist(statErr) {
			return "", "", nil
		}
		return "", "", fmt.Errorf("检查原文件失败: %w", statErr)
	}
	aclDump, err = getfaclFn(targetAbs)
	if err != nil {
		return "", "", fmt.Errorf("读取原文件 ACL 失败: %w", err)
	}
	xattrDump, err = getfattrFn(targetAbs)
	if err != nil {
		return "", "", fmt.Errorf("读取原文件 xattr 失败: %w", err)
	}
	return aclDump, xattrDump, nil
}

// restoreACLXattr 把 captureACLXattr 读到的 dump 应用到临时文件(inodePath)上,
// 必须在 rename 之前调用。aclDump 为空(如原文件不存在/本次是新建)时跳过,
// 不报错——没有旧属性需要恢复不是失败。
func restoreACLXattr(inodePath, aclDump, xattrDump string) error {
	if aclDump != "" {
		if err := setfaclFn(inodePath, aclDump); err != nil {
			return fmt.Errorf("恢复 ACL 失败: %w", err)
		}
	}
	if xattrDump != "" {
		if err := setfattrFn(inodePath, xattrDump); err != nil {
			return fmt.Errorf("恢复 xattr 失败: %w", err)
		}
	}
	return nil
}

func lookupUID(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	u, err := user.Lookup(name)
	if err != nil {
		return 0, false
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, false
	}
	return uid, true
}

func lookupGID(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, false
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return 0, false
	}
	return gid, true
}

// partFullFileName 返回 part 的 Content-Disposition filename 原始参数值,不经
// stdlib Part.FileName() 的 filepath.Base() 剥路径处理。
//
// 偏差说明(对照任务设计 "part.FileName() → part.FullFileName()"):Go 标准库
// mime/multipart.Part 并不存在 FullFileName() 方法——已直接读取本机 Go 1.26
// stdlib 源码(mime/multipart/multipart.go)确认,该类型自始至终只有 FileName()
// 一个方法,其实现固定调用 filepath.Base()(RFC 7578 §4.2 要求),从未有保留原始
// 路径的变体方法。为达成设计意图(part filename 携带子路径、单段名行为与现状
// 一致),改为直接解析 part.Header 的 Content-Disposition 头取原始 filename 参数
// (与 Part.FileName() 内部用的同一个 mime.ParseMediaType 解析器,仅不做 Base()
// 剥离)。multipart 请求体 wire format、CreateFormFile 构造方式、单段文件名的
// 行为均与设计原意完全一致,只是取值的 Go 内部机制不同。
func partFullFileName(part *multipart.Part) string {
	cd := part.Header.Get("Content-Disposition")
	if cd == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(cd)
	if err != nil {
		return ""
	}
	return params["filename"]
}

// sanitizeUploadRelPath 校验并净化目录上传携带的子路径 filename(partFullFileName
// 原值,webkitRelativePath 风格,如 "d1/d2/a.txt")。
//
// 拒绝:空串、以 "/" 开头(绝对路径)、任何空段(连续 "//" 或尾随 "/")。
// 按 "/" 切段,每段过 validateFileName(既有白名单:拒 "."、".."、控制字符、>255 字节;
// 段内不可能再含 "/",故 validateFileName 的"不含/"校验在此总是通过)。
// 反斜杠不做特殊处理(Linux 文件名合法字符,浏览器不会发)。
// 返回净化后按 "/" 重新拼接的相对路径。
func sanitizeUploadRelPath(relFile string) (string, error) {
	if relFile == "" {
		return "", fmt.Errorf("文件名不能为空")
	}
	if strings.HasPrefix(relFile, "/") {
		return "", fmt.Errorf("文件名不能是绝对路径")
	}
	segments := strings.Split(relFile, "/")
	for _, seg := range segments {
		if seg == "" {
			return "", fmt.Errorf("文件名不能包含空路径段")
		}
		if err := validateFileName(seg); err != nil {
			return "", fmt.Errorf("路径段 %q 不合法: %w", seg, err)
		}
	}
	return strings.Join(segments, "/"), nil
}

// ensureUploadParents 为 cleaned(已经过 sanitizeUploadRelPath 净化的相对路径)的
// 父目录链逐段创建:dirAbs/seg1、dirAbs/seg1/seg2…
//
//   - 段不存在 → os.Mkdir(0755) + 显式 os.Chmod(0755)(消解 umask,与 handleMkdir
//     "mode 恒 0755"一致)。
//   - 段已存在且为目录 → 一律不动,尤其不 chmod 已有目录(上传合并不改既有数据权限)。
//   - 段已存在但非目录 → 返回带段路径的错误,调用方映射 400。
//   - Mkdir 失败(并发/权限等)→ 返回带路径的错误,调用方映射 500。
func ensureUploadParents(dirAbs, cleaned string) error {
	segments := strings.Split(cleaned, "/")
	parents := segments[:len(segments)-1]
	if len(parents) == 0 {
		return nil
	}
	cur := dirAbs
	for _, seg := range parents {
		cur = filepath.Join(cur, seg)
		info, err := os.Lstat(cur)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("上传路径与现有非目录冲突: %s", cur)
			}
			continue
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("检查目录失败: %s: %w", cur, err)
		}
		if merr := os.Mkdir(cur, 0755); merr != nil {
			if os.IsExist(merr) {
				// 并发上传同树:对方先建。重查——是目录则视同已存在(不 chmod,
				// 与「既有目录一律不动」语义一致),非目录则走冲突 400。
				if info2, lerr := os.Lstat(cur); lerr == nil && info2.IsDir() {
					continue
				}
				return fmt.Errorf("上传路径与现有非目录冲突: %s", cur)
			}
			return fmt.Errorf("创建目录失败: %s: %w", cur, merr)
		}
		if cerr := os.Chmod(cur, 0755); cerr != nil {
			return fmt.Errorf("设置目录权限失败: %s: %w", cur, cerr)
		}
	}
	return nil
}

// handleUpload — POST /api/files/upload?path=<目标目录>[&overwrite=yes],multipart/form-data。
// 用 r.MultipartReader() 逐 part 流式写盘,不走 ParseMultipartForm/FormFile(禁止把整包撑进内存)。
func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// 限制单请求体积，防止单次上传耗尽磁盘 IO。
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	rel := r.URL.Query().Get("path")
	overwrite := r.URL.Query().Get("overwrite") == "yes"

	dirAbs, err := resolvePoolPath(rel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	info, err := os.Stat(dirAbs)
	if err != nil {
		http.Error(w, "目标目录不存在", http.StatusNotFound)
		return
	}
	if !info.IsDir() {
		http.Error(w, "目标路径不是目录", http.StatusBadRequest)
		return
	}

	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "请求体不是合法 multipart/form-data", http.StatusBadRequest)
		return
	}

	buf := make([]byte, 32*1024)
	written := make([]string, 0)
	var totalBytes int64

	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			if isMaxBytesError(perr) {
				http.Error(w, `{"error":"file too large"}`, http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "读取 multipart part 失败", http.StatusBadRequest)
			return
		}

		relFile := partFullFileName(part)
		if relFile == "" {
			// 普通表单字段(非文件 part),跳过。
			part.Close()
			continue
		}
		cleaned, serr := sanitizeUploadRelPath(relFile)
		if serr != nil {
			part.Close()
			http.Error(w, serr.Error(), http.StatusBadRequest)
			return
		}

		targetRel := filepath.Join(rel, cleaned)
		targetAbs, rerr := resolvePoolPath(targetRel)
		if rerr != nil {
			part.Close()
			http.Error(w, rerr.Error(), http.StatusBadRequest)
			return
		}
		// 双重路径锚定一致性断言,对照 mutate.go handleMkdir(cleaned 已不含 ..,
		// Join 语义与 resolvePoolPath 一致)。
		if targetAbs != filepath.Join(dirAbs, cleaned) {
			part.Close()
			http.Error(w, "路径解析不一致", http.StatusInternalServerError)
			return
		}

		// 缺失父目录逐段创建(目录上传场景);已存在目录权限一律不动。
		if perr := ensureUploadParents(dirAbs, cleaned); perr != nil {
			part.Close()
			if strings.Contains(perr.Error(), "上传路径与现有非目录冲突") {
				http.Error(w, perr.Error(), http.StatusBadRequest)
				return
			}
			http.Error(w, perr.Error(), http.StatusInternalServerError)
			return
		}

		if _, lerr := os.Lstat(targetAbs); lerr == nil && !overwrite {
			part.Close()
			// 拍板决策 1:多 part 冲突语义——立即停止,响应体注明冲突文件名
			// 与已写入的文件名列表;已写入的 part 不回滚。
			writeUploadConflictBody(w, cleaned, written, totalBytes)
			return
		}

		n, werr := writePartToFile(targetAbs, part, buf, overwrite)
		part.Close()
		if werr != nil {
			// 清理责任已下沉到 writePartToFile 内部(copy 失败自清半成品);
			// handler 只按错误类型映射状态码,不在此处 os.Remove。
			if errors.Is(werr, fs.ErrExist) {
				// OpenFile(O_EXCL) 碰撞:文件非我所建(并发方已创建),
				// 与 Lstat 预检同款 409 JSON body,不得 Remove 并发方文件。
				writeUploadConflictBody(w, cleaned, written, totalBytes)
				return
			}
			if isMaxBytesError(werr) {
				http.Error(w, `{"error":"file too large"}`, http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, werr.Error(), http.StatusInsufficientStorage)
			return
		}

		// Bug 1 修复:覆盖写(overwrite=true)的权限对齐已经在 writePartToFile→
		// writePartToFileOverwrite 内部、rename 之前针对临时文件完成——此处
		// 不再重复对齐,更不能在失败时 os.Remove(targetAbs):rename 已经把
		// 新内容原子替换上线,targetAbs 此时就是刚写入的新文件,Remove 会把
		// 覆盖写的成果连同原文件一起删除,新旧数据全部丢失(此前的严重 bug,
		// 详见 alignFilePerms 注释)。
		//
		// 非覆盖写(新建文件,O_EXCL 创建)不受影响:targetAbs 在这里必然是
		// 本次请求自己创建的新文件(并发碰撞已被 fs.ErrExist 分支拦截并
		// 提前 return),Remove 清理的是自己刚创建的半成品,不会波及任何
		// 其他数据,维持原有行为。
		if !overwrite {
			if aerr := alignNewFilePerms(targetAbs); aerr != nil {
				os.Remove(targetAbs)
				http.Error(w, aerr.Error(), http.StatusInternalServerError)
				return
			}
		}

		written = append(written, cleaned)
		totalBytes += n
	}

	common.LogAuditRequest(r, "files", "upload",
		fmt.Sprintf("path=%s files=%d bytes=%d", rel, len(written), totalBytes), "success")
	common.JSONResponse(w, map[string]interface{}{
		"ok":    true,
		"files": len(written),
		"bytes": totalBytes,
	})
}

// writeUploadConflictBody 写出同名冲突的 409 响应体,供 Lstat 预检与 writePartToFile
// 的 O_EXCL/EEXIST 分支共用同一 JSON body 格式(ok/error/conflict/written/files/bytes)。
func writeUploadConflictBody(w http.ResponseWriter, filename string, written []string, totalBytes int64) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	encodeJSONBody(w, map[string]interface{}{
		"ok":       false,
		"error":    "同名已存在",
		"conflict": filename,
		"written":  written,
		"files":    len(written),
		"bytes":    totalBytes,
	})
}

// encodeJSONBody 在已显式 WriteHeader 自定义状态码之后写 JSON body。
// common.JSONResponse 不接受状态码参数(隐式 200),409 冲突响应需要先设状态码,
// 故这里直接复用同款 json.Encoder 配置(禁用 HTML 转义),不改动 common 包。
func encodeJSONBody(w http.ResponseWriter, data interface{}) {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.Encode(data)
}

// writePartToFile 把 part 流式写入 targetAbs。overwrite=false 时用 O_EXCL 防止并发创建覆盖;
// overwrite=true 时写入同目录临时文件,成功后原子 rename 覆盖目标。返回写入字节数。
//
// 清理责任收敛在本函数内部(Task 8 review Important#1 修订;覆盖写路径修订见下):
//   - overwrite=false:OpenFile 失败(含 O_EXCL 碰撞 EEXIST)→ 未落任何字节,直接返回错误,
//     不做任何 Remove。EEXIST 时目标文件非本函数所建(并发方已创建),调用方(handler)
//     不得 Remove 它。OpenFile 成功后 CopyBuffer/Close 失败 → 本函数内部自清半成品:
//     os.Remove(targetAbs),此时文件确为本函数所建,清理是安全的。
//   - overwrite=true:不再直接 O_TRUNC 目标文件——中途失败会截断/删除原文件,造成用户数据
//     不可恢复丢失。改为在同目录创建临时文件、写满后 Close、再 os.Rename 原子覆盖目标;
//     rename 前原文件始终完整未动,写入/Close 失败只清理临时文件,原文件保持原样。
//     同目录是为了保证 rename 在同一文件系统内(POSIX 原子性前提),不能用 os.TempDir()。
func isMaxBytesError(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr)
}

func writePartToFile(targetAbs string, part io.Reader, buf []byte, overwrite bool) (int64, error) {
	if overwrite {
		return writePartToFileOverwrite(targetAbs, part, buf)
	}
	f, err := os.OpenFile(targetAbs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		return 0, err
	}
	n, err := io.CopyBuffer(f, part, buf)
	if err != nil {
		f.Close()
		os.Remove(targetAbs)
		return n, err
	}
	if cerr := f.Close(); cerr != nil {
		os.Remove(targetAbs)
		return n, cerr
	}
	return n, nil
}

// writePartToFileOverwrite 实现 overwrite=true 路径:同目录临时文件写满 + 原子 rename。
// 任何失败路径都只清理临时文件,从不触碰/删除 targetAbs 原有内容。
func writePartToFileOverwrite(targetAbs string, part io.Reader, buf []byte) (int64, error) {
	// Item 5 修复:rename 之前先把原文件(覆盖写发生前的旧内容)的 ACL/xattr
	// dump 下来,留着写完、对齐权限之后恢复到临时文件上。capture 必须在这里
	// (临时文件创建/写入之前)做,此时 targetAbs 还是原文件,内容未被触碰。
	aclDump, xattrDump, cerr := captureACLXattr(targetAbs)
	if cerr != nil {
		return 0, cerr
	}
	// Point 3 拍板决策(本轮修订,推翻 TASK-8):同样在这里把原文件的
	// mode/uid/gid 拍下来——覆盖写必须让原文件的有效权限(mode/ACL/mask/
	// uid/gid)保持权威,不能被池策略(alignFilePerms)重新计算覆盖掉。
	// 原文件不存在(本次其实是新建)时 hasOwnership=false,下面会退回
	// alignFilePerms 的策略对齐路径(与"新建文件按策略定权限"的既有语义
	// 一致,不受本次修订影响)。
	origOwner, oerr := captureOrigOwnership(targetAbs)
	if oerr != nil {
		return 0, oerr
	}

	dir := filepath.Dir(targetAbs)
	tmp, err := os.CreateTemp(dir, ".upload-tmp-*")
	if err != nil {
		return 0, err
	}
	tmpPath := tmp.Name()
	// cleanup 在成功 rename 后会被跳过(见下方 success 分支显式 return)。
	cleanup := func() { os.Remove(tmpPath) }

	n, err := io.CopyBuffer(tmp, part, buf)
	if err != nil {
		tmp.Close()
		cleanup()
		return n, err
	}
	if cerr := tmp.Close(); cerr != nil {
		cleanup()
		return n, cerr
	}
	// Item 5 修复(回归修复:ACL 恢复会反过来冲掉 mode 对齐结果):在 rename
	// 之前把 capture 到的原文件 ACL/xattr 恢复到临时文件上。任一步失败都是
	// 硬错误,只清理临时文件,原文件(targetAbs)此时还是旧内容,完全不受
	// 影响(fail-closed:宁可不覆盖,不可覆盖后属性对不上)。
	//
	// 顺序 bug:这一步必须在 alignFilePerms(chmod)之前执行,不能在之后。
	// getfacl -p 的 dump 总是带显式 mask:: 条目,setfacl --set-file=- 的
	// 默认行为是"dump 里显式给了 mask 就不重新计算"(man setfacl:
	// "unless a mask entry was explicitly given"),于是 restore 会把
	// mask 直接设成 dump 里的旧值——而 Linux 对外展示的"group"权限位
	// (ls -l / stat 看到的 mode 第二组 rwx)在文件带 ACL 时实际读的就是
	// mask 条目,不是 group:: 条目本身。如果先 chmod 再 setfacl 恢复,
	// setfacl 会用旧 mask 把刚 chmod 好的权限位覆盖回去,出现"chmod 显示
	// 成功但最终 stat 出来的 mode 和策略值不一致"的静默错误(已通过真实
	// t.TempDir() 文件 + 真实 getfacl/setfacl 复现:覆盖写前文件是
	// 0640+ACL mask=rwx,alignFilePerms 想把它改成 0644,但如果在它之后
	// 才 restoreACLXattr,最终 stat 出来是 0670,不是 0644)。
	//
	// 反过来,先恢复 ACL/xattr、再 chmod,就没有这个问题:chmod 在文件带
	// ACL 时会根据新的 group 权限位重新计算 mask(这是 chmod(2) 对带 ACL
	// 文件的标准 POSIX 语义,非本包行为),二者天然保持一致,chmod 的
	// 最终结果不会被后续步骤篡改。
	if rerr := restoreACLXattr(tmpPath, aclDump, xattrDump); rerr != nil {
		cleanup()
		return n, rerr
	}
	// 权限/属主对齐必须在 rename 之前、针对临时文件执行,原因见
	// alignFilePerms 的注释——rename 之后再对齐,中间有权限过松的可见窗口,
	// 且失败时调用方若 Remove(targetAbs) 会删掉刚替换上线的新文件,新旧数据
	// 一起丢失。os.CreateTemp 创建的临时文件默认 0600 且属主是运行面板的
	// 进程用户,在对齐完成前既不会比最终策略更松,也不会暴露给其他本地用户,
	// 不存在旧实现"先 chmod 0666 再 rename"那样的宽权限窗口。
	//
	// Point 3 拍板决策(本轮修订,推翻 TASK-8 旧结论):覆盖写(本次确有
	// 原文件,origOwner.hasOwnership=true)时,原文件的 mode/uid/gid 是
	// 权威值,恢复它而不是套用池策略——池策略只对"新建文件"(hasOwnership
	// =false)生效。restoreOrigOwnership 内部 chown 在 chmod 之前执行,
	// 原因与"restoreACLXattr 必须排在 alignFilePerms 之前"完全相同:它是
	// 覆盖写流程里最后一次改 mode 的动作,产出的 mask 才不会被后续步骤
	// 重新计算掉。任一步失败都是硬错误——不存在"恢复不了就退回策略值"的
	// 降级路径,那等于默默丢弃用户此前设置的权限,直接违反"必须保留原有效
	// 权限"的要求。
	if origOwner.hasOwnership {
		if rerr := restoreOrigOwnership(tmpPath, origOwner); rerr != nil {
			cleanup()
			return n, rerr
		}
	} else {
		// 本次其实是新建(原文件不存在,比如并发删除后又被覆盖写请求命中):
		// 没有原权威值可恢复,退回既有的"新建文件按池策略定权限"语义。
		// policyPath 传 targetAbs(按最终落地路径判断所属共享/应用策略),
		// inodePath 传 tmpPath(实际执行 chmod/chown 的文件)。
		if aerr := alignFilePerms(targetAbs, tmpPath); aerr != nil {
			cleanup()
			return n, aerr
		}
	}
	if rerr := os.Rename(tmpPath, targetAbs); rerr != nil {
		// rename 失败:原文件(targetAbs)完全没被触碰,只清理临时文件。
		cleanup()
		return n, rerr
	}
	return n, nil
}
