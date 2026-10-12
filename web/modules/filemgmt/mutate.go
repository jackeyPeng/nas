package filemgmt

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
