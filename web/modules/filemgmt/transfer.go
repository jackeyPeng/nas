package filemgmt

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func handleDownload(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "目录请用 zip 下载", http.StatusBadRequest)
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		http.Error(w, "打开失败", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, escapeFilename(info.Name())))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f) // Range/Content-Type 自动
}

// escapeFilename 只转义文件名中的双引号为单引号,防止其提前闭合
// quoted-string;现代浏览器对 quoted-string 内的 UTF-8 字节已有良好兼容,
// 这里不做 filename* 的百分号编码。
func escapeFilename(name string) string {
	if strings.ContainsRune(name, '"') {
		name = strings.ReplaceAll(name, `"`, `'`)
	}
	return name
}

func handleZip(w http.ResponseWriter, r *http.Request) {
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
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		http.Error(w, "目录不存在", http.StatusNotFound)
		return
	}
	// zip 包名/zip 内前缀用请求路径的字面 basename(如 "inner"),
	// 不能等 EvalSymlinks 之后再取——否则链接目录会被包名顶替成目标目录名(如 "real")。
	base := filepath.Base(abs)
	if rel == "" || filepath.Clean("/"+rel) == "/" {
		base = "nas1" // 池根打包
	}
	// os.Stat 跟随链接判定 IsDir,但下面 filepath.Walk 对根用 Lstat 语义,
	// 若 abs 本身是指向目录的符号链接,Walk 不会进树打包出空壳。
	// 对 abs 做 EvalSymlinks 拿到真实目录路径再传给 zipDir(仅用于遍历,不影响包名);
	// 解析失败(罕见竞态)保持原值,后续 Walk 本来就会因 Lstat 判定非目录而产出空壳,
	// 行为不比修复前更差。resolvePoolPath 已保证池内符号链接解析后仍在池内。
	if resolved, everr := filepath.EvalSymlinks(abs); everr == nil {
		abs = resolved
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s.zip"`, escapeFilename(base)))
	if err := zipDir(w, abs, base); err != nil {
		// header 已发出,只能断流;审计由中间件按状态码记录
		log.Printf("[FILEMGMT] zip 打包中断 path=%s err=%v", rel, err)
		return
	}
}

// zipDir 流式打包 absRoot 到 w,zip 内路径前缀 zipPrefix。不落地临时文件。
func zipDir(w io.Writer, absRoot, zipPrefix string) error {
	zw := zip.NewWriter(w)
	walkErr := filepath.Walk(absRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 跳过不可读条目(竞态/权限),不中断整包
		}
		rel, rerr := filepath.Rel(absRoot, p)
		if rerr != nil {
			return nil
		}
		zipName := filepath.ToSlash(filepath.Join(zipPrefix, rel))
		// 符号链接只存链接本身信息,不跟随(防池外内容进包)
		if info.Mode()&os.ModeSymlink != 0 {
			tgt, terr := os.Readlink(p)
			if terr != nil {
				return nil
			}
			hdr := &zip.FileHeader{Name: zipName, Method: zip.Store}
			hdr.SetMode(info.Mode())
			hdr.Modified = info.ModTime()
			fw, ferr := zw.CreateHeader(hdr)
			if ferr != nil {
				return nil
			}
			fw.Write([]byte(tgt))
			return nil
		}
		if info.IsDir() {
			if rel == "." {
				return nil
			}
			_, cerr := zw.Create(zipName + "/")
			return cerr
		}
		hdr := &zip.FileHeader{Name: zipName, Method: zip.Deflate}
		hdr.SetMode(info.Mode())
		hdr.Modified = info.ModTime()
		fw, ferr := zw.CreateHeader(hdr)
		if ferr != nil {
			return ferr
		}
		src, oerr := os.Open(p)
		if oerr != nil {
			return nil // 打开失败跳过该文件
		}
		defer src.Close()
		_, cerr := io.Copy(fw, src)
		return cerr
	})
	if walkErr != nil {
		zw.Close()
		return walkErr
	}
	return zw.Close()
}
