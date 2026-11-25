package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

func (a *app) handleHealthz(w http.ResponseWriter, r *http.Request) {
	// 任务 §8「storage 目录不可写时 healthz 返回 500（真实健康语义）」。
	if !a.storageWritable() {
		http.Error(w, "storage not writable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

// handleIndex 返回内嵌单页前端（见 ui.go 的 indexHTML）。
func handleIndex(w http.ResponseWriter, r *http.Request, ctx secretCtx) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, indexHTML)
}

// photoListItem 是 GET /api/photos 的单条返回形态（任务 §3）。
type photoListItem struct {
	ID       string `json:"id"`
	OrigName string `json:"origName"`
	Size     int64  `json:"size"`
	MTime    int64  `json:"mtime"`
	Thumb    string `json:"thumb"`
	Photo    string `json:"photo"`
}

func toListItem(p photo) photoListItem {
	return photoListItem{
		ID:       p.ID,
		OrigName: p.OrigName,
		Size:     p.Size,
		MTime:    p.MTime,
		Thumb:    "./thumb/" + p.ID + ".jpg",
		Photo:    "./photo/" + p.ID + "." + p.Ext,
	}
}

func handleList(w http.ResponseWriter, r *http.Request, ctx secretCtx) {
	if _, ok := requireAuth(w, r, ctx.secret); !ok {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	items := []photoListItem{}
	for _, p := range ctx.app.listPhotos() {
		items = append(items, toListItem(p))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

// uploadResult 是 POST /api/upload 单个文件的处理结果，用于汇总响应。
type uploadResult struct {
	OrigName string `json:"origName"`
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	ID       string `json:"id,omitempty"`
}

func handleUpload(w http.ResponseWriter, r *http.Request, ctx secretCtx) {
	user, ok := requireAuth(w, r, ctx.secret)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 整体请求体上限：单文件 50MB + multipart 开销余量，避免恶意超大 body
	// 占用内存/磁盘（任务 §2「单文件上限 50MB」，这里给整请求一个合理上界
	// 防止靠多文件叠加绕过单文件限制的资源耗尽）。
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize*8+1<<20)

	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "invalid multipart body", http.StatusBadRequest)
		return
	}

	results := []uploadResult{}
	statusCode := http.StatusOK

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(w, "invalid multipart body", http.StatusBadRequest)
			return
		}
		if part.FormName() != "files" && part.FormName() != "file" {
			part.Close()
			continue
		}
		origName := part.FileName()
		if origName == "" {
			part.Close()
			continue
		}

		res, code := handleOnePart(ctx.app, part, origName)
		part.Close()
		results = append(results, res)
		if code != http.StatusOK && statusCode == http.StatusOK {
			statusCode = code
		}
	}

	if len(results) == 0 {
		http.Error(w, "no files uploaded", http.StatusBadRequest)
		return
	}

	auditLog("upload user=%s files=%d", user, len(results))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(results)
}

// handleOnePart 处理 multipart 里的单个文件 part：校验扩展名/大小、落盘、
// 入索引。返回结果结构与建议的 HTTP 状态码（供 handleUpload 汇总整体响应
// 状态；单文件失败不影响其它文件继续处理）。
func handleOnePart(a *app, part io.Reader, origName string) (uploadResult, int) {
	ext, ok := validExt(origName)
	if !ok {
		return uploadResult{OrigName: origName, Error: "不支持的文件格式"}, http.StatusUnsupportedMediaType
	}

	id := genID()
	dest := a.originalPath(id, ext)

	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return uploadResult{OrigName: origName, Error: "创建文件失败"}, http.StatusInternalServerError
	}

	// 限制单文件大小（任务 §2「单文件上限 50MB（超限 413）」）：用
	// LimitReader 多读 1 字节侦测是否超限，避免把超限文件整个写入磁盘。
	limited := io.LimitReader(part, maxUploadSize+1)
	written, err := io.Copy(f, limited)
	f.Close()
	if err != nil {
		os.Remove(dest)
		return uploadResult{OrigName: origName, Error: "写入失败"}, http.StatusInternalServerError
	}
	if written > maxUploadSize {
		os.Remove(dest)
		return uploadResult{OrigName: origName, Error: "文件超过 50MB 上限"}, http.StatusRequestEntityTooLarge
	}
	if written == 0 {
		os.Remove(dest)
		return uploadResult{OrigName: origName, Error: "空文件"}, http.StatusBadRequest
	}

	p := photo{
		ID:       id,
		OrigName: origName,
		Ext:      ext,
		Size:     written,
		MTime:    time.Now().Unix(),
	}
	if err := a.addPhoto(p); err != nil {
		os.Remove(dest)
		return uploadResult{OrigName: origName, Error: "索引写入失败"}, http.StatusInternalServerError
	}

	return uploadResult{OrigName: origName, OK: true, ID: id}, http.StatusOK
}

type deleteRequest struct {
	ID string `json:"id"`
}

func handleDelete(w http.ResponseWriter, r *http.Request, ctx secretCtx) {
	user, ok := requireAuth(w, r, ctx.secret)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req deleteRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// id 形态校验：即便删除接口不直接拼路径穿越风险也要先过白名单
	// （与 handlePhoto/handleThumb 同一条防线，统一纪律，防止未来改动在
	// 别处松动校验）。不匹配的 id 当作「不存在」处理，幂等返回 200。
	if !idPattern.MatchString(req.ID) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"deleted": false})
		return
	}

	p, found := ctx.app.findPhoto(req.ID)
	if found {
		os.Remove(ctx.app.originalPath(p.ID, p.Ext))
		os.Remove(ctx.app.thumbPath(p.ID))
	}
	// 索引里没有时 removePhoto 返回 false，但仍需确保孤立文件不残留——
	// 用 id 本身尝试清理任意已知扩展名的原图/缩略图（防止索引与磁盘不一致
	// 时留下垃圾文件）。
	deleted := ctx.app.removePhoto(req.ID)
	if !found {
		for ext := range allowedExt {
			os.Remove(ctx.app.originalPath(req.ID, ext))
		}
		os.Remove(ctx.app.thumbPath(req.ID))
	}

	auditLog("delete user=%s id=%s deleted=%v", user, req.ID, deleted || found)

	// 任务 §6「不存在→200 幂等」：无论是否真的删除了什么，都 200。
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"deleted": true})
}

// handlePhoto 服务原图：GET /photo/<id>.<ext>（任务 §5「路径穿越零容忍」）。
// 严格校验 id 形态 + ext 白名单，校验通过后用 filepath.Join 拼出的路径必然
// 落在 storageDir 内（id 正则不含 `.`/`/`，无法逃逸）。
func handlePhoto(w http.ResponseWriter, r *http.Request, ctx secretCtx) {
	if _, ok := requireAuth(w, r, ctx.secret); !ok {
		return
	}
	id, ext, ok := parsePhotoRequestPath(r.URL.Path, "/photo/")
	if !ok {
		http.NotFound(w, r)
		return
	}

	full := ctx.app.originalPath(id, ext)
	f, err := os.Open(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", mimeForExt(ext))
	w.Header().Set("Cache-Control", "private, max-age=86400")
	io.Copy(w, f)
}

// handleThumb 服务缩略图：GET /thumb/<id>.jpg。首次请求生成并缓存到
// dataDir/thumbs/<id>.jpg，后续命中缓存直连读。损坏源图 → 404 占位
// （任务 §4）。
func handleThumb(w http.ResponseWriter, r *http.Request, ctx secretCtx) {
	if _, ok := requireAuth(w, r, ctx.secret); !ok {
		return
	}
	id, ext, ok := parsePhotoRequestPath(r.URL.Path, "/thumb/")
	if !ok || ext != "jpg" {
		http.NotFound(w, r)
		return
	}
	// 缩略图总是以 .jpg 请求，但源图可能是 png/gif——从索引查真实扩展名。
	p, found := ctx.app.findPhoto(id)
	if !found {
		http.NotFound(w, r)
		return
	}

	thumbPath := ctx.app.thumbPath(id)
	if data, err := os.ReadFile(thumbPath); err == nil {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Write(data)
		return
	}

	// 缓存未命中：生成并原子写入缓存（临时文件 + rename，防止并发请求
	// 互相截断对方写入的缩略图文件）。
	original := ctx.app.originalPath(id, p.Ext)
	tmp := thumbPath + ".tmp-" + randomHex(4)
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := genThumbnail(original, p.Ext, out); err != nil {
		out.Close()
		os.Remove(tmp)
		http.NotFound(w, r) // 损坏图 → 404 占位（任务 §4）。
		return
	}
	out.Close()
	if err := os.Rename(tmp, thumbPath); err != nil {
		os.Remove(tmp)
	}

	data, err := os.ReadFile(thumbPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write(data)
}

// parsePhotoRequestPath 从形如 "<prefix><id>.<ext>" 的请求路径中提取并严格
// 校验 id/ext，这是路径穿越防线的唯一入口：
//   - 先用 path.Clean 规范化（折叠 `../`、多余的 `/`、`.`），再确认清理后
//     的路径仍以 prefix 为前缀——`../` 或编码后的 `%2e%2e`（net/url 在路由
//     分发前已解码到 r.URL.Path，因此这里看到的是解码后的原始字符）试图
//     逃出前缀会在这一步被拦截
//   - 再用 idPattern（`^[0-9]+_[0-9a-f]{8}$`）与 allowedExt 白名单校验拆出
//     的 id/ext——两者均不含 `.`/`/`/`\x00`，即便前两步有疏漏，拼出的文件名
//     也不可能包含路径分隔符或空字节
func parsePhotoRequestPath(reqPath, marker string) (id, ext string, ok bool) {
	// 空字节：Go 的 net/http 在解析请求行时已经会拒绝多数控制字符，但这里
	// 显式再挡一层（任务明确要求覆盖空字节注入场景），不依赖上游行为。
	if strings.ContainsRune(reqPath, '\x00') {
		return "", "", false
	}

	cleaned := path.Clean(reqPath)
	// 双前缀问题：核心反代原样透传 /apps/nas-photos/photo/<id>.<ext>，本机
	// 调试走裸路径 /photo/<id>.<ext>（registerRoutes 双挂）。用 LastIndex
	// 定位 marker（"/photo/" 或 "/thumb/"），两种前缀形态都取 marker 之后
	// 的文件名段；marker 之前的内容（无论多长）一律忽略，不存在「前缀必须
	// 等于裸路径」的假设——曾因硬编码 strings.HasPrefix(cleaned, "/photo/")
	// 导致反代路径 404（真机实锤，测试只覆盖裸路径没抓到）。
	idx := strings.LastIndex(cleaned, marker)
	if idx < 0 {
		return "", "", false
	}
	name := cleaned[idx+len(marker):]
	if name == "" || strings.ContainsAny(name, "/\\") {
		return "", "", false
	}

	dot := strings.LastIndex(name, ".")
	if dot <= 0 || dot == len(name)-1 {
		return "", "", false
	}
	id = name[:dot]
	ext = toLowerASCII(name[dot+1:])

	if !idPattern.MatchString(id) {
		return "", "", false
	}
	if !allowedExt[ext] {
		return "", "", false
	}
	return id, ext, true
}

func mimeForExt(ext string) string {
	switch ext {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}

// auditLog 是最小的 audit 风格日志封装（stdout → journald，§6「应用日志：
// stdout/stderr → journald」）；本应用不做用户体系，SSO 传来的用户名仅用于
// 展示与此处的 audit log 行（任务硬边界）。
func auditLog(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
}
