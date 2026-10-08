package filemgmt

import (
	"io/fs"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"nas-panel/common"
)

const maxListEntries = 2000

type FileEntry struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // file/dir/symlink/other
	Size     int64  `json:"size"`
	Mode     string `json:"mode"`      // 八进制如 "0644"
	ModeText string `json:"mode_text"` // 符号如 "-rw-r--r--"
	Owner    string `json:"owner"`
	Group    string `json:"group"`
	MTime    int64  `json:"mtime"`            // unix 秒
	Target   string `json:"target,omitempty"` // symlink 目标(展示用,不跟随)
}

func handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel := r.FormValue("path")
	abs, err := resolvePoolPath(rel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	entries, truncated, err := listDir(abs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	common.JSONResponse(w, map[string]interface{}{
		"path":       strings.Trim(filepath.Clean("/"+rel), "/"),
		"entries":    entries,
		"truncated":  truncated,
		"breadcrumb": breadcrumb(rel),
	})
}

func listDir(abs string) ([]FileEntry, bool, error) {
	dirEntries, err := os.ReadDir(abs)
	if err != nil {
		return nil, false, err
	}
	truncated := false
	if len(dirEntries) > maxListEntries {
		dirEntries = dirEntries[:maxListEntries]
		truncated = true
	}
	out := make([]FileEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		info, err := de.Info() // Lstat 语义,不跟随符号链接
		if err != nil {
			continue // 竞态删除的条目跳过
		}
		e := FileEntry{
			Name:     de.Name(),
			Size:     info.Size(),
			Mode:     "0" + strconv.FormatInt(int64(info.Mode().Perm()), 8),
			ModeText: info.Mode().String(),
			MTime:    info.ModTime().Unix(),
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			e.Type = "symlink"
			if tgt, terr := os.Readlink(filepath.Join(abs, de.Name())); terr == nil {
				e.Target = tgt
			}
		case info.IsDir():
			e.Type = "dir"
			e.Size = 0
		case info.Mode().IsRegular():
			e.Type = "file"
		default:
			e.Type = "other"
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			if u, uerr := user.LookupId(strconv.Itoa(int(stat.Uid))); uerr == nil {
				e.Owner = u.Username
			} else {
				e.Owner = strconv.Itoa(int(stat.Uid))
			}
			if g, gerr := user.LookupGroupId(strconv.Itoa(int(stat.Gid))); gerr == nil {
				e.Group = g.Name
			} else {
				e.Group = strconv.Itoa(int(stat.Gid))
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := out[i].Type == "dir", out[j].Type == "dir"
		if di != dj {
			return di // 目录在前
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, truncated, nil
}

// breadcrumb 把相对路径切成 [{"name":"","path":""},{"name":"public","path":"public"}...]
// 池根项 name 返回空字符串,由前端用 $t('files.pool_root') 判空渲染 i18n 文案。
func breadcrumb(rel string) []map[string]string {
	bc := []map[string]string{{"name": "", "path": ""}}
	rel = strings.Trim(filepath.Clean("/"+rel), "/")
	if rel == "" || rel == "." {
		return bc
	}
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		bc = append(bc, map[string]string{"name": p, "path": strings.Join(parts[:i+1], "/")})
	}
	return bc
}
