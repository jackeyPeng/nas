package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

// photo 是单张照片的索引记录（sidecar JSON，见 indexPath）。id 的落盘文件名
// 形态是 `<unixms>_<4字节hex>`（任务要求「落盘文件名永不信任客户端文件名
// 进路径」），ext 另存，拼成 `<id>.<ext>` 即实际文件名。
type photo struct {
	ID       string `json:"id"`
	OrigName string `json:"origName"`
	Ext      string `json:"ext"`
	Size     int64  `json:"size"`
	MTime    int64  `json:"mtime"` // unix seconds，用于列表排序与展示。
}

// idPattern 对应任务 §5「严格校验 id 形态」：`^[0-9]+_[0-9a-f]{8}$`，路径穿越
// 零容忍——任何不匹配该形态的输入直接拒绝，不进入文件系统层。
var idPattern = regexp.MustCompile(`^[0-9]+_[0-9a-f]{8}$`)

// allowedExt 是上传白名单（任务 §2），键为小写扩展名（不含点）。
var allowedExt = map[string]bool{
	"jpg":  true,
	"jpeg": true,
	"png":  true,
	"gif":  true,
}

const maxUploadSize = 50 << 20 // 50MB（任务 §2「单文件上限 50MB」）。

// app 持有运行态：storageDir 存原图（manifest permissions.storage 声明的池
// 内路径），dataDir 存缩略图缓存与索引（manifest dataDir:true）。index 是
// sidecar JSON 的内存镜像，mu 保护并发访问（任务要求并发上传无竞争）。
type app struct {
	storageDir string
	dataDir    string
	thumbDir   string
	indexDir   string
	indexFile  string

	mu    sync.Mutex
	index []photo // 内存索引，按插入顺序；列表接口按 mtime 倒序返回。
}

func newApp(dataDir, storageDir string) (*app, error) {
	a := &app{
		storageDir: storageDir,
		dataDir:    dataDir,
	}
	if dataDir != "" {
		a.thumbDir = filepath.Join(dataDir, "thumbs")
		a.indexDir = filepath.Join(dataDir, "index")
		a.indexFile = filepath.Join(a.indexDir, "photos.json")
	}

	for _, dir := range []string{a.storageDir, a.thumbDir, a.indexDir} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("创建目录 %s 失败: %w", dir, err)
		}
	}

	if err := a.loadIndex(); err != nil {
		return nil, err
	}
	return a, nil
}

// loadIndex 从 sidecar JSON 恢复内存索引；文件不存在视为空索引（首次启动），
// 不是错误。
func (a *app) loadIndex() error {
	if a.indexFile == "" {
		return nil
	}
	data, err := os.ReadFile(a.indexFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取索引失败: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var idx []photo
	if err := json.Unmarshal(data, &idx); err != nil {
		return fmt.Errorf("解析索引失败: %w", err)
	}
	a.index = idx
	return nil
}

// saveIndexLocked 把内存索引写回 sidecar JSON；调用方必须已持有 a.mu。
// 先写临时文件再 rename，防止并发写入/进程崩溃导致索引文件截断损坏。
func (a *app) saveIndexLocked() error {
	if a.indexFile == "" {
		return nil
	}
	data, err := json.MarshalIndent(a.index, "", "  ")
	if err != nil {
		return err
	}
	tmp := a.indexFile + ".tmp-" + randomHex(4)
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, a.indexFile)
}

// genID 生成落盘文件名主体：`<unixms>_<4字节hex>`（任务 §2「永不信任客户端
// 文件名进路径」）。4 字节 hex 防止同毫秒并发上传碰撞。
func genID() string {
	return fmt.Sprintf("%d_%s", time.Now().UnixMilli(), randomHex(4))
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 失败极罕见（内核随机源不可用）；退化用时间戳纳秒位，
		// 不 panic——上传场景宁可 ID 弱随机也不要整请求 500。
		return hex.EncodeToString([]byte(fmt.Sprintf("%08x", time.Now().UnixNano())))[:n*2]
	}
	return hex.EncodeToString(buf)
}

// validExt 规范化并校验扩展名是否在白名单内，返回小写形式（不含点）。
func validExt(name string) (string, bool) {
	ext := filepath.Ext(name)
	if len(ext) < 2 {
		return "", false
	}
	ext = toLowerASCII(ext[1:])
	return ext, allowedExt[ext]
}

func toLowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// originalPath 返回 id+ext 对应的原图绝对路径；调用方必须先用 idPattern 和
// allowedExt 校验过 id/ext（本函数不重复校验，只负责拼路径——路径穿越防线
// 在校验阶段，不在这里）。
func (a *app) originalPath(id, ext string) string {
	return filepath.Join(a.storageDir, id+"."+ext)
}

func (a *app) thumbPath(id string) string {
	return filepath.Join(a.thumbDir, id+".jpg")
}

// addPhoto 把一条记录加入内存索引并落盘，返回失败时调用方应回滚已写入的
// 文件（由 handleUpload 负责）。
func (a *app) addPhoto(p photo) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.index = append(a.index, p)
	if err := a.saveIndexLocked(); err != nil {
		// 回滚内存索引，保持与磁盘一致。
		a.index = a.index[:len(a.index)-1]
		return err
	}
	return nil
}

// findPhoto 按 id 查索引，未找到返回 (photo{}, false)。
func (a *app) findPhoto(id string) (photo, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range a.index {
		if p.ID == id {
			return p, true
		}
	}
	return photo{}, false
}

// listPhotos 返回按 mtime 倒序的索引快照（拷贝，避免调用方持有内部切片
// 引用引发的并发问题）。
func (a *app) listPhotos() []photo {
	a.mu.Lock()
	snapshot := make([]photo, len(a.index))
	copy(snapshot, a.index)
	a.mu.Unlock()

	sort.SliceStable(snapshot, func(i, j int) bool {
		return snapshot[i].MTime > snapshot[j].MTime
	})
	return snapshot
}

// removePhoto 从索引中删除 id 对应记录并落盘；id 不存在时返回 false（调用方
// 据此实现「不存在→200 幂等」而非报错）。
func (a *app) removePhoto(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, p := range a.index {
		if p.ID == id {
			removed := p
			a.index = append(a.index[:i:i], a.index[i+1:]...)
			if err := a.saveIndexLocked(); err != nil {
				// 落盘失败：恢复内存索引，保持与磁盘一致，当作删除未发生。
				a.index = append(a.index[:i:i], append([]photo{removed}, a.index[i:]...)...)
				return false
			}
			return true
		}
	}
	return false
}

// storageWritable 检查 storageDir 是否可写：healthz 用它判定「storage 目录
// 不可写时返回 500」（任务 §8，真实健康语义）。用临时文件测试写入权限，
// 立即删除，不留痕迹。
func (a *app) storageWritable() bool {
	if a.storageDir == "" {
		return false
	}
	probe := filepath.Join(a.storageDir, ".healthz-probe-"+randomHex(4))
	f, err := os.Create(probe)
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(probe)
	return true
}
