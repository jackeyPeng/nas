package filemgmt

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

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

		filename := part.FileName()
		if filename == "" {
			// 普通表单字段(非文件 part),跳过。
			part.Close()
			continue
		}
		if err := validateFileName(filename); err != nil {
			part.Close()
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		targetRel := filepath.Join(rel, filename)
		targetAbs, rerr := resolvePoolPath(targetRel)
		if rerr != nil {
			part.Close()
			http.Error(w, rerr.Error(), http.StatusBadRequest)
			return
		}
		// 双重路径锚定一致性断言,对照 mutate.go handleMkdir。
		if targetAbs != filepath.Join(dirAbs, filename) {
			part.Close()
			http.Error(w, "路径解析不一致", http.StatusInternalServerError)
			return
		}

		if _, lerr := os.Lstat(targetAbs); lerr == nil && !overwrite {
			part.Close()
			// 拍板决策 1:多 part 冲突语义——立即停止,响应体注明冲突文件名
			// 与已写入的文件名列表;已写入的 part 不回滚。
			writeUploadConflictBody(w, filename, written, totalBytes)
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
				writeUploadConflictBody(w, filename, written, totalBytes)
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

		written = append(written, filename)
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
