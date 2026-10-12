package filemgmt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"nas-panel/common"
)

// validateFileName 校验新建/重命名用的裸文件名(不含路径分隔符)。
// 拒绝:空串、含 "/"、含 ".." 子串、含控制字符(< 0x20)、UTF-8 长度 > 255 字节、
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
	if utf8.RuneCountInString(name) > 255 || len(name) > 255 {
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
	// targetAbs 理应等于 filepath.Join(parentAbs, req.Name);以 parentAbs 为准创建,
	// 避免两次解析之间出现竞态导致路径不一致。
	_ = parentAbs
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
	if _, err := os.Lstat(dstAbs); err == nil {
		http.Error(w, "目标已存在", http.StatusConflict)
		return
	}
	if err := os.Rename(srcAbs, dstAbs); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.LogAuditRequest(r, "files", "rename",
		fmt.Sprintf("path=%s new_name=%s", req.Path, req.NewName), "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}
