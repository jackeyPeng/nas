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
	root := filepath.Clean(poolRoot())
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
