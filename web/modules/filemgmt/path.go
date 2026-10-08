package filemgmt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"nas-panel/modules/diskmgmt"
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
	// 先按字面拒绝任何 ".." 片段(Clean 会消解它,这里故意不消解——
	// 请求里出现 ".." 即视为攻击,不做"善意解释";
	// 子串级检查覆盖 URL 编码残留,如 "..%2fetc")
	if strings.Contains(rel, "..") {
		return "", fmt.Errorf("路径包含非法片段")
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return "", fmt.Errorf("路径包含非法片段")
		}
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

// normalizedRoot 返回 EvalSymlinks 归一后的池根(失败回落字面值)。
// 与 resolvePoolPath 的 resolved 比较双方须在同一解析层级。
func normalizedRoot() string {
	root := filepath.Clean(poolRoot())
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		return resolved
	}
	return root
}

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
