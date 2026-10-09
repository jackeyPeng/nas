package filemgmt

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"nas-panel/common"
)

const (
	maxTextPreview  = 1 << 20  // 1MB
	maxImagePreview = 10 << 20 // 10MB
)

var textExts = map[string]bool{".txt": true, ".md": true, ".json": true, ".yaml": true,
	".yml": true, ".log": true, ".conf": true, ".ini": true, ".csv": true, ".sh": true}
var imageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".webp": true, ".bmp": true}

// handlePreview 预览文本/图片文件。SVG 一律拒绝(可内联脚本,预览即存储型 XSS,
// 对齐 spec §十四.5);其他未在白名单内的类型统一 415,引导用户走下载。
func handlePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	abs, err := resolvePoolPath(r.FormValue("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		http.Error(w, "文件不存在", http.StatusNotFound)
		return
	}
	if info.IsDir() {
		http.Error(w, "目录不支持预览", http.StatusBadRequest)
		return
	}
	ext := strings.ToLower(filepath.Ext(abs))
	switch {
	case ext == ".svg":
		// 显式拒绝:SVG 可内联 <script>/onload,预览等同在面板域下执行攻击者脚本。
		http.Error(w, "SVG 不支持预览,请下载", http.StatusUnsupportedMediaType)
		return
	case imageExts[ext]:
		if info.Size() > maxImagePreview {
			http.Error(w, "图片过大,请下载", http.StatusUnsupportedMediaType)
			return
		}
		f, err := os.Open(abs)
		if err != nil {
			http.Error(w, "打开失败", http.StatusInternalServerError)
			return
		}
		defer f.Close()
		http.ServeContent(w, r, info.Name(), info.ModTime(), f) // Content-Type 由扩展名嗅探
		return
	case textExts[ext]:
		f, err := os.Open(abs)
		if err != nil {
			http.Error(w, "打开失败", http.StatusInternalServerError)
			return
		}
		defer f.Close()
		buf := make([]byte, maxTextPreview+1)
		n, _ := io.ReadFull(f, buf) // EOF/ErrUnexpectedEOF 均正常(文件小于缓冲区)
		truncated := n > maxTextPreview
		content := string(buf[:min(n, maxTextPreview)])
		common.JSONResponse(w, map[string]interface{}{
			"kind": "text", "content": content, "truncated": truncated,
		})
		return
	default:
		http.Error(w, "该类型不支持预览", http.StatusUnsupportedMediaType)
		return
	}
}

// handleUsage 返回池挂载点总量/已用(df)与当前目录占用(du -sb,5s 超时兜底)。
func handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	abs, err := resolvePoolPath(r.FormValue("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		http.Error(w, "目录不存在", http.StatusNotFound)
		return
	}

	pool := map[string]interface{}{
		"total_kb": 0, "used_kb": 0, "avail_kb": 0, "use_pct": "",
	}
	// 面板进程本身是 root,直接 exec 即可,无需 sudo。
	if out, err := exec.Command("df", "--output=size,used,avail,pcent", normalizedRoot()).Output(); err == nil {
		if p := parseDfOutput(string(out)); p != nil {
			pool = p
		}
	}

	dir := map[string]interface{}{"bytes": nil, "du_status": "ok"}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "du", "-sb", "--apparent-size", abs).Output()
	if ctx.Err() == context.DeadlineExceeded {
		dir["bytes"] = nil
		dir["du_status"] = "timeout"
	} else if err != nil {
		dir["bytes"] = nil
		dir["du_status"] = "error"
	} else if n := parseDuSizeBytes(string(out)); n >= 0 {
		dir["bytes"] = n
		dir["du_status"] = "ok"
	} else {
		dir["bytes"] = nil
		dir["du_status"] = "error"
	}

	common.JSONResponse(w, map[string]interface{}{"pool": pool, "dir": dir})
}

// parseDfOutput 解析 `df --output=size,used,avail,pcent` 的两行输出(表头+数据)。
// 失败(fake pool 不在真实挂载点、字段数不符等)返回 nil,调用方保留零值兜底。
func parseDfOutput(out string) map[string]interface{} {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		return nil
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) != 4 {
		return nil
	}
	total, err1 := strconv.ParseInt(fields[0], 10, 64)
	used, err2 := strconv.ParseInt(fields[1], 10, 64)
	avail, err3 := strconv.ParseInt(fields[2], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return nil
	}
	return map[string]interface{}{
		"total_kb": total, "used_kb": used, "avail_kb": avail, "use_pct": fields[3],
	}
}

// parseDuSizeBytes 解析 `du -sb` 输出首列的字节数;失败返回 -1。
func parseDuSizeBytes(out string) int64 {
	fields := strings.Fields(out)
	if len(fields) < 1 {
		return -1
	}
	n, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return -1
	}
	return n
}
