package diskmgmt

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"nas-panel/common"
)

// ═══ per-share 回收站（TODO #25 / Architecture v1.0 第十条）═══
// 新方案：每个共享目录下 .recycle/<user>/<原相对路径>（vfs_recycle keeptree）
// 旧方案兼容读取：#recycle/（集中式，只读展示，可恢复/删除，不再写入）
// 删除 = rename（同卷零拷贝），恢复 = rename 回原路径。

// RecycleItem 回收站中的一个条目
type RecycleItem struct {
	Share     string `json:"share"`      // 共享文件夹名
	SharePath string `json:"share_path"` // 共享目录绝对路径
	User      string `json:"user"`       // 删除者（.recycle/<user>）
	Name      string `json:"name"`       // 条目名（原相对路径首段）
	RelPath   string `json:"rel_path"`   // .recycle 内相对路径 = 原相对路径
	IsDir     bool   `json:"is_dir"`
	Size      int64  `json:"size"`   // 字节（目录为递归大小）
	MTime     int64  `json:"mtime"`  // Unix 秒（删除时间）
	Legacy    bool   `json:"legacy"` // 旧 #recycle 布局
}

const recycleDirName = ".recycle"
const legacyRecycleDirName = "#recycle"

func RegisterRecycleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/disk/recycle", common.AuthMiddleware(handleRecycleList))
	mux.HandleFunc("/api/disk/recycle/restore", common.AuthMiddleware(handleRecycleRestore))
	mux.HandleFunc("/api/disk/recycle/delete", common.AuthMiddleware(handleRecycleDelete))
	mux.HandleFunc("/api/disk/recycle/clear", common.AuthMiddleware(handleRecycleClear))
}

// recycleRoots 返回所有启用回收站的共享目录（name → path）
func recycleRoots() map[string]string {
	roots := map[string]string{}
	for _, m := range GetAllFolderMeta() {
		if m.RecycleBin {
			roots[m.Name] = m.Path
		}
	}
	return roots
}

// handleRecycleList GET /api/disk/recycle[?share=xxx]
func handleRecycleList(w http.ResponseWriter, r *http.Request) {
	shareFilter := r.FormValue("share")
	items := make([]RecycleItem, 0)
	for name, sharePath := range recycleRoots() {
		if shareFilter != "" && shareFilter != name {
			continue
		}
		items = append(items, listRecycleDir(name, sharePath, recycleDirName, false)...)
		items = append(items, listRecycleDir(name, sharePath, legacyRecycleDirName, true)...)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].MTime > items[j].MTime })
	common.JSONResponse(w, map[string]interface{}{
		"items":  items,
		"total":  len(items),
		"shares": recycleRoots(),
	})
}

// listRecycleDir 枚举一个回收站根目录的条目。
// 新布局 .recycle/<user>/<rel...>；旧布局 #recycle/<rel...>（user 记为 "-"）。
func listRecycleDir(shareName, sharePath, dirName string, legacy bool) []RecycleItem {
	items := make([]RecycleItem, 0)
	root := filepath.Join(sharePath, dirName)
	userDirs := []struct{ user, base string }{}
	if legacy {
		userDirs = append(userDirs, struct{ user, base string }{"-", root})
	} else {
		entries, err := common.SudoOutput("ls", "-1", root)
		if err != nil || strings.TrimSpace(entries) == "" {
			return items
		}
		for _, u := range strings.Split(strings.TrimSpace(entries), "\n") {
			u = strings.TrimSpace(u)
			if u == "" || strings.Contains(u, "/") || u == "." || u == ".." {
				continue
			}
			userDirs = append(userDirs, struct{ user, base string }{u, filepath.Join(root, u)})
		}
	}
	for _, ud := range userDirs {
		// stat 每个顶层条目：类型/大小/mtime。目录大小不递归（避免慢），置 0 由前端显示 "-"
		out, err := common.SudoOutput("find", ud.base, "-mindepth", "1", "-maxdepth", "1", "-printf", "%y\t%s\t%T@\t%P\n")
		if err != nil || strings.TrimSpace(out) == "" {
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimRight(line, "\r")
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "\t", 4)
			if len(parts) < 4 {
				continue
			}
			size, _ := strconv.ParseInt(parts[1], 10, 64)
			mtimeF, _ := strconv.ParseFloat(parts[2], 64)
			name := parts[3]
			if name == "" {
				continue
			}
			rel := name // 顶层条目相对路径 = 名称（keeptree 的子树随条目整体恢复）
			if legacy {
				rel = name
			}
			items = append(items, RecycleItem{
				Share:     shareName,
				SharePath: sharePath,
				User:      ud.user,
				Name:      name,
				RelPath:   rel,
				IsDir:     parts[0] == "d",
				Size:      size,
				MTime:     int64(mtimeF),
				Legacy:    legacy,
			})
		}
	}
	return items
}

// resolveRecyclePath 校验并拼出回收站条目的绝对路径（防穿越）。
// dirName: ".recycle" 或 "#recycle"；user 仅新布局使用。
func resolveRecyclePath(sharePath, dirName, user, relPath string) (string, error) {
	if relPath == "" || strings.HasPrefix(relPath, "/") || strings.Contains(relPath, "..") {
		return "", fmt.Errorf("rel_path 非法")
	}
	if strings.Contains(user, "/") || strings.Contains(user, "..") {
		return "", fmt.Errorf("user 非法")
	}
	var root string
	if dirName == legacyRecycleDirName {
		root = filepath.Join(sharePath, dirName)
	} else {
		if user == "" {
			return "", fmt.Errorf("user 必填")
		}
		root = filepath.Join(sharePath, dirName, user)
	}
	full, err := common.ValidateDataPath(filepath.Join(root, relPath), false)
	if err != nil {
		return "", err
	}
	// 双重保险：必须在回收站根内
	if !strings.HasPrefix(full, filepath.Clean(root)+string(filepath.Separator)) {
		return "", fmt.Errorf("路径越界")
	}
	return full, nil
}

// findShareMetaPath 按共享名查 path
func findShareMetaPath(share string) (string, error) {
	for _, m := range GetAllFolderMeta() {
		if m.Name == share {
			return m.Path, nil
		}
	}
	return "", fmt.Errorf("共享文件夹 %q 不存在", share)
}

// handleRecycleRestore POST share, rel_path, user, legacy=yes|""
func handleRecycleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	share := r.FormValue("share")
	relPath := r.FormValue("rel_path")
	user := r.FormValue("user")
	dirName := recycleDirName
	if r.FormValue("legacy") == "yes" {
		dirName = legacyRecycleDirName
	}
	sharePath, err := findShareMetaPath(share)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	src, err := resolveRecyclePath(sharePath, dirName, user, relPath)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	// 目标 = 共享目录内原相对路径
	dst, err := common.ValidateDataPath(filepath.Join(sharePath, relPath), false)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(dst, filepath.Clean(sharePath)+string(filepath.Separator)) {
		http.Error(w, `{"error":"恢复目标越界"}`, http.StatusBadRequest)
		return
	}
	// 存在性检查 + 冲突处理（目标已存在时加 .restored 后缀，不覆盖用户数据）
	if _, err := common.SudoOutput("test", "-e", src); err != nil {
		http.Error(w, `{"error":"回收站条目不存在"}`, http.StatusNotFound)
		return
	}
	finalDst := dst
	if _, err := common.SudoOutput("test", "-e", dst); err == nil {
		finalDst = dst + ".restored"
	}
	common.SudoExec("mkdir", "-p", filepath.Dir(finalDst))
	out, err := common.SudoExec("mv", src, finalDst)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, out+": "+err.Error()), http.StatusInternalServerError)
		return
	}
	common.EmitEvent("storage", common.EventSuccess, "recycle.restored",
		map[string]interface{}{"share": share, "path": relPath}, "")
	common.JSONResponse(w, map[string]interface{}{"message": "已恢复", "restored_to": finalDst})
}

// handleRecycleDelete POST share, rel_path, user, legacy — 彻底删除条目
func handleRecycleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if r.FormValue("confirm") != "yes" {
		http.Error(w, `{"error":"需要 confirm=yes"}`, http.StatusBadRequest)
		return
	}
	share := r.FormValue("share")
	relPath := r.FormValue("rel_path")
	user := r.FormValue("user")
	dirName := recycleDirName
	if r.FormValue("legacy") == "yes" {
		dirName = legacyRecycleDirName
	}
	sharePath, err := findShareMetaPath(share)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	src, err := resolveRecyclePath(sharePath, dirName, user, relPath)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	out, err := common.SudoExec("rm", "-rf", src)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, out+": "+err.Error()), http.StatusInternalServerError)
		return
	}
	common.EmitEvent("storage", common.EventWarn, "recycle.deleted",
		map[string]interface{}{"share": share, "path": relPath}, "")
	common.JSONResponse(w, map[string]interface{}{"message": "已彻底删除"})
}

// handleRecycleClear POST share — 清空某共享的全部回收站（新旧布局都清）
func handleRecycleClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	share := r.FormValue("share")
	// 破坏性操作：确认词 = 共享名（同删池模式）
	if r.FormValue("confirm_name") != share || share == "" {
		http.Error(w, `{"error":"confirm_name 必须等于共享名"}`, http.StatusBadRequest)
		return
	}
	sharePath, err := findShareMetaPath(share)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	cleaned, err := common.ValidateDataPath(sharePath, false)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	for _, dirName := range []string{recycleDirName, legacyRecycleDirName} {
		target := filepath.Join(cleaned, dirName)
		// rm -rf 目录本身，下次删除时 vfs_recycle 自动重建
		common.SudoExec("rm", "-rf", target)
	}
	common.EmitEvent("storage", common.EventWarn, "recycle.cleared",
		map[string]interface{}{"share": share}, "")
	common.JSONResponse(w, map[string]interface{}{"message": share + " 回收站已清空"})
}
