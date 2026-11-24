package apps

// 应用市场一键安装（M2（二），APP-SPEC §7）
//
// GET /api/apps/market：拉取市场索引（§7 index.json），按 minCoreVersion/spec
// 过滤出当前核心能装的版本，失败时优雅降级为返回上一次成功的本地缓存
// （§7「索引仅元数据」——网络不可达不该导致市场页整体报错，而是提示陈旧）。
//
// POST /api/apps/market/install：按 {id, version} 解析出索引里对应的
// tarball/sig URL，下载后复用 lifecycle.go 既有的验签→解包→核心版本校验→
// 分配端口→落 pending 台账整条链路（§3.3 install 第一步语义不因来源是
// 市场还是手工上传而改变，返回同样的 pendingInstallResponse，前端复用
// 既有 confirm 流程）。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nas-panel/common"
	"nas-panel/modules/version"
)

// sha256Hex 返回 data 的 sha256 十六进制摘要（小写）。
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// marketIndexURL 是市场索引地址（§7「v1 自托管」），固定值不经环境变量
// 覆盖——与 update.go 的 controlBase 不同，市场索引 URL 不是部署期可配项
// （规范标注为单一权威来源），避免引入又一个隐式配置面。
const marketIndexURL = "https://get.z1.sale/apps/index.json"

// marketFetchTimeout 是拉取索引的超时。
const marketFetchTimeout = 5 * time.Second

// marketCacheTTL 是本地缓存 data/market-index.json 的有效期：命中期内
// 不重新发请求，直接用缓存应答（减少对市场源站的轮询压力）。
const marketCacheTTL = time.Hour

// marketIndex / marketApp / marketVersion 对应 §7 index.json 的嵌套结构。
type marketIndex struct {
	Spec string      `json:"spec"`
	Apps []marketApp `json:"apps"`
}

type marketApp struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Icon     string          `json:"icon"`
	Summary  string          `json:"summary"`
	Latest   string          `json:"latest"`
	Versions []marketVersion `json:"versions"`
}

type marketVersion struct {
	Version        string `json:"version"`
	MinCoreVersion string `json:"minCoreVersion"`
	Tarball        string `json:"tarball"`
	SHA256         string `json:"sha256"`
	Sig            string `json:"sig"`
	Changelog      string `json:"changelog"`
	Size           int64  `json:"size"`
}

// marketCacheFile 返回本地缓存落盘路径：<dataDir>/market-index.json。
func marketCacheFile(dataDir string) string {
	return filepath.Join(dataDir, "market-index.json")
}

// marketCacheEnvelope 是缓存文件的落盘格式：索引内容 + 抓取时间，供 TTL 判定。
type marketCacheEnvelope struct {
	FetchedAt time.Time   `json:"fetchedAt"`
	Index     marketIndex `json:"index"`
}

// marketHTTPGet 是实际发起索引/包下载请求的函数变量，测试注入假实现替换
// 真实网络调用（与 modules/update 的做法不同——update 没有单测覆盖网络层，
// 这里按 executor.go 的测试注入惯例处理，保证 market_test.go 不依赖外网）。
var marketHTTPGet = func(url string, timeout time.Duration) (*http.Response, error) {
	client := &http.Client{Timeout: timeout}
	return client.Get(url)
}

// fetchMarketIndex 拉取并解析市场索引（§7），5s 超时。
func fetchMarketIndex() (*marketIndex, error) {
	resp, err := marketHTTPGet(marketIndexURL, marketFetchTimeout)
	if err != nil {
		return nil, fmt.Errorf("拉取市场索引失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("市场索引返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("读取市场索引失败: %w", err)
	}
	var idx marketIndex
	if err := json.Unmarshal(body, &idx); err != nil {
		return nil, fmt.Errorf("市场索引格式错误: %w", err)
	}
	return &idx, nil
}

// loadMarketCache 读取本地缓存；缓存不存在或内容损坏返回 (nil, false)。
func loadMarketCache(dataDir string) (*marketCacheEnvelope, bool) {
	data, err := os.ReadFile(marketCacheFile(dataDir))
	if err != nil {
		return nil, false
	}
	var env marketCacheEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, false
	}
	return &env, true
}

// saveMarketCache 把索引落盘，失败不算错误（缓存只是优化，写不进去下次
// 再拉一次网络即可，不应该因为落盘失败就让这次请求报错）。
func saveMarketCache(dataDir string, idx marketIndex) {
	env := marketCacheEnvelope{FetchedAt: time.Now(), Index: idx}
	data, err := json.Marshal(env)
	if err != nil {
		return
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return
	}
	_ = os.WriteFile(marketCacheFile(dataDir), data, 0644)
}

// loadMarketIndex 是 GET /api/apps/market 的核心逻辑：TTL 内命中缓存直接
// 返回；否则尝试拉网络，成功则刷新缓存，失败则优雅降级回退到陈旧缓存
// （若存在），全无可用数据才报错（§7「索引仅元数据」隐含市场页在网络
// 不通时不应该整体不可用，只是看到的可能不是最新）。
func loadMarketIndex(dataDir string) (idx marketIndex, stale bool, err error) {
	cache, hasCache := loadMarketCache(dataDir)
	if hasCache && time.Since(cache.FetchedAt) < marketCacheTTL {
		return cache.Index, false, nil
	}

	fresh, ferr := fetchMarketIndex()
	if ferr == nil {
		saveMarketCache(dataDir, *fresh)
		return *fresh, false, nil
	}

	if hasCache {
		return cache.Index, true, nil
	}
	return marketIndex{}, false, ferr
}

// marketAppView / marketVersionView 是 GET /api/apps/market 响应体：在
// index.json 原始字段基础上，按当前核心版本标注每个版本是否可装
// （minCoreVersion 过滤，§7「核心按 minCoreVersion/spec 过滤展示」），
// 并标注已安装版本（供前端画「已安装/可升级」态），不要求前端自己重算。
type marketAppView struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Icon         string              `json:"icon"`
	Summary      string              `json:"summary"`
	Latest       string              `json:"latest"`
	Installed    bool                `json:"installed"`
	InstalledVer string              `json:"installedVersion"`
	Installable  bool                `json:"installable"` // 至少一个版本满足 minCoreVersion
	Versions     []marketVersionView `json:"versions"`
}

type marketVersionView struct {
	Version        string `json:"version"`
	MinCoreVersion string `json:"minCoreVersion"`
	Changelog      string `json:"changelog"`
	Size           int64  `json:"size"`
	Compatible     bool   `json:"compatible"`
}

// buildMarketAppView 按 spec 过滤（当前核心仅支持 spec 主版本 1，复用
// specPattern 同一口径）+ minCoreVersion 过滤，组装单个应用的展示视图。
func buildMarketAppView(app marketApp, installed map[string]string) marketAppView {
	core := version.DisplayVersion
	coreValid := isValidSemver(core)

	views := make([]marketVersionView, 0, len(app.Versions))
	installable := false
	for _, v := range app.Versions {
		compatible := true
		if coreValid && isValidSemver(v.MinCoreVersion) {
			cmp, err := CompareSemver(core, v.MinCoreVersion)
			compatible = err == nil && cmp >= 0
		}
		if compatible {
			installable = true
		}
		views = append(views, marketVersionView{
			Version:        v.Version,
			MinCoreVersion: v.MinCoreVersion,
			Changelog:      v.Changelog,
			Size:           v.Size,
			Compatible:     compatible,
		})
	}

	instVer, isInstalled := installed[app.ID]
	return marketAppView{
		ID:           app.ID,
		Name:         app.Name,
		Icon:         app.Icon,
		Summary:      app.Summary,
		Latest:       app.Latest,
		Installed:    isInstalled,
		InstalledVer: instVer,
		Installable:  installable,
		Versions:     views,
	}
}

func handleMarketList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	idx, stale, err := loadMarketIndex(reg.dataDir)
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, "市场索引不可用: "+err.Error())
		return
	}

	apps, err := listInstalledApps(reg.db)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询台账失败: "+err.Error())
		return
	}
	installed := make(map[string]string, len(apps))
	for _, a := range apps {
		installed[a.ID] = a.Version
	}

	views := make([]marketAppView, 0, len(idx.Apps))
	for _, app := range idx.Apps {
		views = append(views, buildMarketAppView(app, installed))
	}

	common.JSONResponse(w, map[string]interface{}{
		"spec":  idx.Spec,
		"apps":  views,
		"stale": stale,
	})
}

// ─────────────────────────────────────────────────────────────
// POST /api/apps/market/install：解析索引 → 下载包+签名 → 复用 install 链路
// ─────────────────────────────────────────────────────────────

type marketInstallRequest struct {
	ID      string `json:"id"`
	Version string `json:"version"` // 省略则取 latest
}

// resolveMarketVersion 在索引里按 id（+可选 version，省略取 latest）定位
// 具体的版本条目。
func resolveMarketVersion(idx marketIndex, id, wantVersion string) (*marketApp, *marketVersion, error) {
	for i := range idx.Apps {
		app := &idx.Apps[i]
		if app.ID != id {
			continue
		}
		target := wantVersion
		if target == "" {
			target = app.Latest
		}
		for j := range app.Versions {
			if app.Versions[j].Version == target {
				return app, &app.Versions[j], nil
			}
		}
		return nil, nil, fmt.Errorf("应用 %s 不存在版本 %s", id, target)
	}
	return nil, nil, fmt.Errorf("市场索引中未找到应用: %s", id)
}

// downloadMarketAsset 下载 tarball 或签名文件，限制 §1 单包 256MB 上限
// （签名文件本身很小，统一走同一限额即可，不需要单独更窄的上限）。
func downloadMarketAsset(url string) ([]byte, error) {
	resp, err := marketHTTPGet(url, 60*time.Second)
	if err != nil {
		return nil, fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载返回 HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPackageSize+1))
	if err != nil {
		return nil, fmt.Errorf("读取下载内容失败: %w", err)
	}
	if int64(len(data)) > maxPackageSize {
		return nil, fmt.Errorf("下载内容超过单包上限 %dMB", maxPackageSize/1024/1024)
	}
	return data, nil
}

func handleMarketInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req marketInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.ID == "" {
		httpError(w, http.StatusBadRequest, "缺少 id 字段")
		return
	}

	idx, _, err := loadMarketIndex(reg.dataDir)
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, "市场索引不可用: "+err.Error())
		return
	}

	app, ver, err := resolveMarketVersion(idx, req.ID, req.Version)
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}

	tarball, err := downloadMarketAsset(ver.Tarball)
	if err != nil {
		common.LogAuditRequest(r, auditCategory, "市场安装应用", fmt.Sprintf("%s v%s: 下载包失败: %v", app.ID, ver.Version, err), "失败")
		httpError(w, http.StatusBadGateway, "下载应用包失败: "+err.Error())
		return
	}
	// sha256 摘要校验（§1 发布物三件套之一）：先于签名验证做一次完整性
	// 复核，索引里声明的摘要与实际下载内容不符时直接拒绝，错误信息更
	// 直指"下载损坏/被篡改"而非笼统的签名失败。签名校验本身已足以拒绝
	// 被篡改的包，这里是双重防御，对齐发布物三件套的完整契约。
	if ver.SHA256 != "" {
		if got := sha256Hex(tarball); !strings.EqualFold(got, ver.SHA256) {
			common.LogAuditRequest(r, auditCategory, "市场安装应用", fmt.Sprintf("%s v%s: sha256 不匹配（期望 %s，实际 %s）", app.ID, ver.Version, ver.SHA256, got), "失败")
			httpError(w, http.StatusBadGateway, "下载内容 sha256 校验失败，可能被篡改或下载不完整")
			return
		}
	}
	sigRaw, err := downloadMarketAsset(ver.Sig)
	if err != nil {
		common.LogAuditRequest(r, auditCategory, "市场安装应用", fmt.Sprintf("%s v%s: 下载签名失败: %v", app.ID, ver.Version, err), "失败")
		httpError(w, http.StatusBadGateway, "下载签名失败: "+err.Error())
		return
	}

	resp, instErr := installFromTarball(r, tarball, sigRaw, "市场安装应用")
	if instErr != nil {
		httpError(w, instErr.status, instErr.message)
		return
	}
	common.JSONResponse(w, resp)
}
