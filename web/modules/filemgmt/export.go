package filemgmt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"nas-panel/common"
)

// ResolvePoolPath 导出既有 resolvePoolPath（池根解析+穿越/符号链接
// fail-closed），签名不变，供任务引擎等跨模块调用方直接复用，不走 HTTP
// 自环。薄导出层，逻辑全部在 resolvePoolPath 里。
func ResolvePoolPath(rel string) (string, error) {
	return resolvePoolPath(rel)
}

// RelPoolPath 把池内绝对路径换算为池根相对路径（ResolvePoolPath 的逆
// 运算），供扫描器（如 tasks 模块的 photo_dedupe）把 filepath.WalkDir
// 拿到的绝对路径转回 Step.Args["src"] 要求的池内相对形式。absPath 必须
// 已经在池根内，否则返回 ErrNotInPool。
func RelPoolPath(absPath string) (string, error) {
	root := normalizedRoot()
	clean := filepath.Clean(absPath)
	if clean != root && !strings.HasPrefix(clean, root+string(filepath.Separator)) {
		return "", ErrNotInPool
	}
	rel, err := filepath.Rel(root, clean)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return "", nil
	}
	return filepath.ToSlash(rel), nil
}

// SetPoolRootOverrideForTest 覆盖池根（仅供测试）。filemgmt 自己的测试
// 直接写包级变量 poolRootOverride（path_test.go 既有模式）；本函数是
// 导出入口，供 tasks 等跨包测试（F1 Task 4 的 photo_dedupe 扫描器需要
// 一个真实池根 fixture 供 ResolvePoolPath/MoveToRecycle 联调）在不进本
// 包内部也能注入同一套覆盖机制。生产代码不得调用。
func SetPoolRootOverrideForTest(root string) {
	poolRootOverride = root
}

// 哨兵错误（F1 Task 3）：MoveToRecycle 的调用方用 errors.Is 判定并映射
// HTTP 码/文案，而不是解析错误文本——handleDelete 重构后的调用点示范。
var (
	// ErrNotInPool 等同 resolvePoolPath 的失败语义（路径越界/穿越/符号链接
	// 逃逸）；MoveToRecycle 自身不重复做这层校验（调用方应先过
	// ResolvePoolPath，这里命中通常是池根本身）。
	ErrNotInPool = errors.New("不在池根内")
	// ErrIsRecycleContent 对应 handleDelete 的 ".recycle 内容拒绝"守卫
	// （字面路径 + EvalSymlinks 双判定）。
	ErrIsRecycleContent = errors.New("回收站内容请在回收站页管理")
	// ErrIsShareRoot 对应"不能删除共享文件夹本身"守卫。
	ErrIsShareRoot = errors.New("不能删除共享文件夹本身,请在存储页操作")
	// ErrNotFound 对应源不存在（os.IsNotExist 判）。
	ErrNotFound = errors.New("源不存在")
	// ErrBadActor 对应 actor 用户名含 "/" 或 ".." 的非法校验。
	ErrBadActor = errors.New("删除者用户名非法")
)

// MoveToRecycle 把池内文件/目录移入回收站（.recycle keeptree 既有布局），
// 返回回收站内绝对路径。
//
// 内含 handleDelete 的全部守卫（Task 3 机械抽取，顺序逐字保留）：
// 池根不可删、.recycle 内容拒绝（字面路径+EvalSymlinks 双判定）、
// 共享文件夹根拒绝、源不存在 404 语义（os.IsNotExist 判）、
// actor 用户名白名单校验、resolveRecycleTarget+recycleConflictFree 防覆盖。
//
// actor 为空时用 common.GetNASUser()（对齐 recycleAdminUser 的 HTTP
// 层兜底，内核调用方没有 *http.Request 可取 JWT 身份）。
//
// 错误语义：返回值用 errors.Is 判定 ErrNotInPool/ErrIsRecycleContent/
// ErrIsShareRoot/ErrNotFound/ErrBadActor 之一；其余（文件系统 I/O 错误等）
// 原样包装返回，不归入哨兵集合。
func MoveToRecycle(srcAbs, actor string) (string, error) {
	if srcAbs == normalizedRoot() {
		return "", fmt.Errorf("不能删除池根: %w", ErrNotInPool)
	}
	// 防套娃(对照 handleDelete Task 10 review Important#1)：基于解析后
	// 路径判定，先字面段再 EvalSymlinks 双判定，堵池内符号链接指向
	// .recycle 的绕过。
	if litRel, relErr := filepath.Rel(normalizedRoot(), srcAbs); relErr == nil && pathHasRecycleSegment(litRel) {
		return "", ErrIsRecycleContent
	}
	if realSrc, evalErr := filepath.EvalSymlinks(srcAbs); evalErr == nil {
		realRel, relErr := filepath.Rel(normalizedRoot(), realSrc)
		if relErr == nil && pathHasRecycleSegment(realRel) {
			return "", ErrIsRecycleContent
		}
	}
	// (源不存在时 EvalSymlinks 报错跳过真实路径判定，由下方 Lstat 统一 404)
	meta, found := ownerShareOfVia(srcAbs)
	if found && srcAbs == filepath.Clean(meta.Path) {
		return "", ErrIsShareRoot
	}
	if _, err := os.Lstat(srcAbs); err != nil {
		return "", ErrNotFound
	}

	if actor == "" {
		actor = common.GetNASUser()
	}
	// actor 用户名纵深校验(对照 handleDelete Task 10 review Minor#3)。
	if strings.Contains(actor, "/") || strings.Contains(actor, "..") {
		return "", ErrBadActor
	}

	recycleTarget, err := resolveRecycleTarget(srcAbs, actor)
	if err != nil {
		return "", err
	}
	recycleTarget = recycleConflictFree(recycleTarget)

	if err := os.MkdirAll(filepath.Dir(recycleTarget), 0700); err != nil {
		return "", err
	}
	if err := os.Rename(srcAbs, recycleTarget); err != nil {
		return "", err
	}

	return recycleTarget, nil
}
