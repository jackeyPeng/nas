package filemgmt

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"nas-panel/common"
	"nas-panel/modules/diskmgmt"
)

// getFolderMetaFn 仅供测试注入,对照 poolRootOverride 模式(path.go)。
// 生产走 diskmgmt.GetAllFolderMeta;测试可替换为返回固定/空列表的 fake。
var getFolderMetaFn = diskmgmt.GetAllFolderMeta

// ownerShareOfVia 与 path.go 的 ownerShareOf 逻辑一致,但经 getFolderMetaFn 取数据,
// 使上传权限对齐在单测里可注入 fake folders.db 结果。
func ownerShareOfVia(absPath string) (diskmgmt.FolderMeta, bool) {
	var best diskmgmt.FolderMeta
	found := false
	for _, m := range getFolderMetaFn() {
		clean := filepath.Clean(m.Path)
		prefix := clean + string(filepath.Separator)
		if hasPathPrefix(absPath, prefix) {
			if !found || len(clean) > len(filepath.Clean(best.Path)) {
				best, found = m, true
			}
		}
	}
	return best, found
}

// hasPathPrefix 判断 absPath(+分隔符)是否以 prefix 为前缀;抽成小函数避免重复 import strings 的字符串拼接易错写法。
func hasPathPrefix(absPath, prefix string) bool {
	full := absPath + string(filepath.Separator)
	return len(full) >= len(prefix) && full[:len(prefix)] == prefix
}

// alignPermsDecision 是权限对齐的纯判定函数,不做任何系统调用,便于单测表驱动覆盖。
//
// 拍板决策(TASK-8):
//   - found 且 meta.Name == "public" → (0664, nasUser, "nasusers")
//   - found 且非 public            → (0600, meta.Name, meta.Name)(home 与普通共享同规则)
//   - !found(池根直属,无所属共享)  → (0644, "", "")(空串 = 不 chown,只 chmod)
func alignPermsDecision(meta diskmgmt.FolderMeta, found bool, nasUser string) (fileMode os.FileMode, ownerName, groupName string) {
	if !found {
		return 0644, "", ""
	}
	if meta.Name == "public" {
		return 0664, nasUser, "nasusers"
	}
	return 0600, meta.Name, meta.Name
}

// alignNewFilePerms 对刚写完的新文件执行权限对齐:判定目标目录所属共享 → 纯函数算出
// mode/owner/group → chmod(必做) + 按需 chown(ownerName/groupName 非空才做)。
// chmod 失败是硬错误(调用方需清理半成品并 500);chown 查不到系统用户时跳过,只记日志。
func alignNewFilePerms(targetAbs string) error {
	meta, found := ownerShareOfVia(targetAbs)
	mode, ownerName, groupName := alignPermsDecision(meta, found, common.GetNASUser())
	if err := os.Chmod(targetAbs, mode); err != nil {
		return err
	}
	if ownerName == "" && groupName == "" {
		return nil
	}
	uid, uidOK := lookupUID(ownerName)
	gid, gidOK := lookupGID(groupName)
	if !uidOK || !gidOK {
		log.Printf("[FILEMGMT] 上传权限对齐跳过 chown(用户/组不存在): path=%s owner=%s group=%s",
			targetAbs, ownerName, groupName)
		return nil
	}
	if err := os.Chown(targetAbs, uid, gid); err != nil {
		log.Printf("[FILEMGMT] 上传权限对齐 chown 失败: path=%s err=%v", targetAbs, err)
	}
	return nil
}

func lookupUID(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	u, err := user.Lookup(name)
	if err != nil {
		return 0, false
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, false
	}
	return uid, true
}

func lookupGID(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, false
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return 0, false
	}
	return gid, true
}

// partFullFileName 返回 part 的 Content-Disposition filename 原始参数值,不经
// stdlib Part.FileName() 的 filepath.Base() 剥路径处理。
//
// 偏差说明(对照任务设计 "part.FileName() → part.FullFileName()"):Go 标准库
// mime/multipart.Part 并不存在 FullFileName() 方法——已直接读取本机 Go 1.26
// stdlib 源码(mime/multipart/multipart.go)确认,该类型自始至终只有 FileName()
// 一个方法,其实现固定调用 filepath.Base()(RFC 7578 §4.2 要求),从未有保留原始
// 路径的变体方法。为达成设计意图(part filename 携带子路径、单段名行为与现状
// 一致),改为直接解析 part.Header 的 Content-Disposition 头取原始 filename 参数
// (与 Part.FileName() 内部用的同一个 mime.ParseMediaType 解析器,仅不做 Base()
// 剥离)。multipart 请求体 wire format、CreateFormFile 构造方式、单段文件名的
// 行为均与设计原意完全一致,只是取值的 Go 内部机制不同。
func partFullFileName(part *multipart.Part) string {
	cd := part.Header.Get("Content-Disposition")
	if cd == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(cd)
	if err != nil {
		return ""
	}
	return params["filename"]
}

// sanitizeUploadRelPath 校验并净化目录上传携带的子路径 filename(partFullFileName
// 原值,webkitRelativePath 风格,如 "d1/d2/a.txt")。
//
// 拒绝:空串、以 "/" 开头(绝对路径)、任何空段(连续 "//" 或尾随 "/")。
// 按 "/" 切段,每段过 validateFileName(既有白名单:拒 "."、".."、控制字符、>255 字节;
// 段内不可能再含 "/",故 validateFileName 的"不含/"校验在此总是通过)。
// 反斜杠不做特殊处理(Linux 文件名合法字符,浏览器不会发)。
// 返回净化后按 "/" 重新拼接的相对路径。
func sanitizeUploadRelPath(relFile string) (string, error) {
	if relFile == "" {
		return "", fmt.Errorf("文件名不能为空")
	}
	if strings.HasPrefix(relFile, "/") {
		return "", fmt.Errorf("文件名不能是绝对路径")
	}
	segments := strings.Split(relFile, "/")
	for _, seg := range segments {
		if seg == "" {
			return "", fmt.Errorf("文件名不能包含空路径段")
		}
		if err := validateFileName(seg); err != nil {
			return "", fmt.Errorf("路径段 %q 不合法: %w", seg, err)
		}
	}
	return strings.Join(segments, "/"), nil
}

// ensureUploadParents 为 cleaned(已经过 sanitizeUploadRelPath 净化的相对路径)的
// 父目录链逐段创建:dirAbs/seg1、dirAbs/seg1/seg2…
//
//   - 段不存在 → os.Mkdir(0755) + 显式 os.Chmod(0755)(消解 umask,与 handleMkdir
//     "mode 恒 0755"一致)。
//   - 段已存在且为目录 → 一律不动,尤其不 chmod 已有目录(上传合并不改既有数据权限)。
//   - 段已存在但非目录 → 返回带段路径的错误,调用方映射 400。
//   - Mkdir 失败(并发/权限等)→ 返回带路径的错误,调用方映射 500。
func ensureUploadParents(dirAbs, cleaned string) error {
	segments := strings.Split(cleaned, "/")
	parents := segments[:len(segments)-1]
	if len(parents) == 0 {
		return nil
	}
	cur := dirAbs
	for _, seg := range parents {
		cur = filepath.Join(cur, seg)
		info, err := os.Lstat(cur)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("上传路径与现有非目录冲突: %s", cur)
			}
			continue
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("检查目录失败: %s: %w", cur, err)
		}
		if merr := os.Mkdir(cur, 0755); merr != nil {
			if os.IsExist(merr) {
				// 并发上传同树:对方先建。重查——是目录则视同已存在(不 chmod,
				// 与「既有目录一律不动」语义一致),非目录则走冲突 400。
				if info2, lerr := os.Lstat(cur); lerr == nil && info2.IsDir() {
					continue
				}
				return fmt.Errorf("上传路径与现有非目录冲突: %s", cur)
			}
			return fmt.Errorf("创建目录失败: %s: %w", cur, merr)
		}
		if cerr := os.Chmod(cur, 0755); cerr != nil {
			return fmt.Errorf("设置目录权限失败: %s: %w", cur, cerr)
		}
	}
	return nil
}

// handleUpload — POST /api/files/upload?path=<目标目录>[&overwrite=yes],multipart/form-data。
// 用 r.MultipartReader() 逐 part 流式写盘,不走 ParseMultipartForm/FormFile(禁止把整包撑进内存)。
func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel := r.URL.Query().Get("path")
	overwrite := r.URL.Query().Get("overwrite") == "yes"

	dirAbs, err := resolvePoolPath(rel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	info, err := os.Stat(dirAbs)
	if err != nil {
		http.Error(w, "目标目录不存在", http.StatusNotFound)
		return
	}
	if !info.IsDir() {
		http.Error(w, "目标路径不是目录", http.StatusBadRequest)
		return
	}

	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "请求体不是合法 multipart/form-data", http.StatusBadRequest)
		return
	}

	buf := make([]byte, 32*1024)
	written := make([]string, 0)
	var totalBytes int64

	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			http.Error(w, "读取 multipart part 失败", http.StatusBadRequest)
			return
		}

		relFile := partFullFileName(part)
		if relFile == "" {
			// 普通表单字段(非文件 part),跳过。
			part.Close()
			continue
		}
		cleaned, serr := sanitizeUploadRelPath(relFile)
		if serr != nil {
			part.Close()
			http.Error(w, serr.Error(), http.StatusBadRequest)
			return
		}

		targetRel := filepath.Join(rel, cleaned)
		targetAbs, rerr := resolvePoolPath(targetRel)
		if rerr != nil {
			part.Close()
			http.Error(w, rerr.Error(), http.StatusBadRequest)
			return
		}
		// 双重路径锚定一致性断言,对照 mutate.go handleMkdir(cleaned 已不含 ..,
		// Join 语义与 resolvePoolPath 一致)。
		if targetAbs != filepath.Join(dirAbs, cleaned) {
			part.Close()
			http.Error(w, "路径解析不一致", http.StatusInternalServerError)
			return
		}

		// 缺失父目录逐段创建(目录上传场景);已存在目录权限一律不动。
		if perr := ensureUploadParents(dirAbs, cleaned); perr != nil {
			part.Close()
			if strings.Contains(perr.Error(), "上传路径与现有非目录冲突") {
				http.Error(w, perr.Error(), http.StatusBadRequest)
				return
			}
			http.Error(w, perr.Error(), http.StatusInternalServerError)
			return
		}

		if _, lerr := os.Lstat(targetAbs); lerr == nil && !overwrite {
			part.Close()
			// 拍板决策 1:多 part 冲突语义——立即停止,响应体注明冲突文件名
			// 与已写入的文件名列表;已写入的 part 不回滚。
			writeUploadConflictBody(w, cleaned, written, totalBytes)
			return
		}

		n, werr := writePartToFile(targetAbs, part, buf, overwrite)
		part.Close()
		if werr != nil {
			// 清理责任已下沉到 writePartToFile 内部(copy 失败自清半成品);
			// handler 只按错误类型映射状态码,不在此处 os.Remove。
			if errors.Is(werr, fs.ErrExist) {
				// OpenFile(O_EXCL) 碰撞:文件非我所建(并发方已创建),
				// 与 Lstat 预检同款 409 JSON body,不得 Remove 并发方文件。
				writeUploadConflictBody(w, cleaned, written, totalBytes)
				return
			}
			http.Error(w, werr.Error(), http.StatusInsufficientStorage)
			return
		}

		if aerr := alignNewFilePerms(targetAbs); aerr != nil {
			os.Remove(targetAbs)
			http.Error(w, aerr.Error(), http.StatusInternalServerError)
			return
		}

		written = append(written, cleaned)
		totalBytes += n
	}

	common.LogAuditRequest(r, "files", "upload",
		fmt.Sprintf("path=%s files=%d bytes=%d", rel, len(written), totalBytes), "success")
	common.JSONResponse(w, map[string]interface{}{
		"ok":    true,
		"files": len(written),
		"bytes": totalBytes,
	})
}

// writeUploadConflictBody 写出同名冲突的 409 响应体,供 Lstat 预检与 writePartToFile
// 的 O_EXCL/EEXIST 分支共用同一 JSON body 格式(ok/error/conflict/written/files/bytes)。
func writeUploadConflictBody(w http.ResponseWriter, filename string, written []string, totalBytes int64) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	encodeJSONBody(w, map[string]interface{}{
		"ok":       false,
		"error":    "同名已存在",
		"conflict": filename,
		"written":  written,
		"files":    len(written),
		"bytes":    totalBytes,
	})
}

// encodeJSONBody 在已显式 WriteHeader 自定义状态码之后写 JSON body。
// common.JSONResponse 不接受状态码参数(隐式 200),409 冲突响应需要先设状态码,
// 故这里直接复用同款 json.Encoder 配置(禁用 HTML 转义),不改动 common 包。
func encodeJSONBody(w http.ResponseWriter, data interface{}) {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.Encode(data)
}

// writePartToFile 把 part 流式写入 targetAbs。overwrite=false 时用 O_EXCL 防止并发创建覆盖;
// overwrite=true 时 O_TRUNC 截断写。返回写入字节数。
//
// 清理责任收敛在本函数内部(Task 8 review Important#1 修订):
//   - OpenFile 失败(含 O_EXCL 碰撞 EEXIST)→ 未落任何字节,直接返回错误,不做任何 Remove。
//     EEXIST 时目标文件非本函数所建(并发方已创建),调用方(handler)不得 Remove 它。
//   - OpenFile 成功后 CopyBuffer 失败 → 本函数内部自清半成品:f.Close() 后 os.Remove(targetAbs),
//     再返回原始 copy 错误(此时文件确为本函数所建,清理是安全的)。
func writePartToFile(targetAbs string, part io.Reader, buf []byte, overwrite bool) (int64, error) {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if overwrite {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(targetAbs, flags, 0666)
	if err != nil {
		return 0, err
	}
	n, err := io.CopyBuffer(f, part, buf)
	if err != nil {
		f.Close()
		os.Remove(targetAbs)
		return n, err
	}
	if cerr := f.Close(); cerr != nil {
		os.Remove(targetAbs)
		return n, cerr
	}
	return n, nil
}
