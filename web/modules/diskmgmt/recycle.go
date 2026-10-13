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

// buildRecycleRoots 是 recycleRoots 的纯函数核心：先建真实共享 map
// （RecycleBin=true），再对 metas 的全部 distinct Pool 值注入池根伪共享条目
// （key="(池根 <base(pool)>)"，value=pool），仅当该 key 尚不存在时注入——
// 真实共享优先。
//
// D1 修复(2026-10-13 同构机验收):默认 recycle_bin=0 的共享删除走池根 .recycle
// 回退(filemgmt resolveRecycleTarget)，若回收站页只枚举 RecycleBin=true 共享，
// 这些删除在 items/shares 里完全不可见、无法 restore。注入池根伪共享后，
// listRecycleDir 对伪条目天然生效(.recycle 为空时返回空列表，无害)。
func buildRecycleRoots(metas []FolderMeta) map[string]string {
	roots := map[string]string{}
	for _, m := range metas {
		if m.RecycleBin {
			roots[m.Name] = m.Path
		}
	}
	for _, m := range metas {
		if m.Pool == "" {
			continue
		}
		key := fmt.Sprintf("(池根 %s)", filepath.Base(m.Pool))
		if _, exists := roots[key]; !exists {
			roots[key] = m.Pool
		}
	}
	return roots
}

// recycleRoots 返回所有启用回收站的共享目录（name → path），并纳入池根伪共享
// （见 buildRecycleRoots）。
func recycleRoots() map[string]string {
	return buildRecycleRoots(GetAllFolderMeta())
}

// poolRootForPseudoShare 判断 share 是否为某 distinct pool 的伪共享名
// （"(池根 <base(pool)>)"），命中则返回该 pool 路径。
func poolRootForPseudoShare(share string, metas []FolderMeta) (string, bool) {
	if share == "" {
		return "", false
	}
	seen := map[string]bool{}
	for _, m := range metas {
		if m.Pool == "" || seen[m.Pool] {
			continue
		}
		seen[m.Pool] = true
		if fmt.Sprintf("(池根 %s)", filepath.Base(m.Pool)) == share {
			return m.Pool, true
		}
	}
	return "", false
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

// resolveShareOrPoolRootPath 先按真实共享名查 findShareMetaPath，未命中再查
// 池根伪共享名——与 buildRecycleRoots 的「真实共享优先」注入顺序保持一致
// （若管理员真建了名为「(池根 xxx)」的共享，list 展示真实共享，restore/delete/
// clear 也必须解析到同一路径，不能伪名抢先后不一致）。错误语义不变：两处都
// 未命中 → 共享不存在 400。restore/delete/clear 三个 handler 共用。
func resolveShareOrPoolRootPath(share string) (string, error) {
	if sharePath, err := findShareMetaPath(share); err == nil {
		return sharePath, nil
	}
	if poolRoot, ok := poolRootForPseudoShare(share, GetAllFolderMeta()); ok {
		return poolRoot, nil
	}
	return findShareMetaPath(share)
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
	sharePath, err := resolveShareOrPoolRootPath(share)
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
	sharePath, err := resolveShareOrPoolRootPath(share)
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
	sharePath, err := resolveShareOrPoolRootPath(share)
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
