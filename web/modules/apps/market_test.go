package apps

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"nas-panel/modules/version"
)

// fakeMarketAsset 是 marketHTTPGet 的假响应描述：按 URL 匹配返回固定内容/
// 状态码/错误，供测试控制索引拉取与包/签名下载的各种场景而不碰真实网络。
type fakeMarketAsset struct {
	body       []byte
	statusCode int
	err        error
}

// stubMarketHTTP 把包级 marketHTTPGet 替换为按 URL 查表的假实现，返回的
// restore 函数必须 defer 调用还原（marketHTTPGet 是包级变量，被全部测试
// 共享，不还原会污染后续用例）。
func stubMarketHTTP(t *testing.T, assets map[string]fakeMarketAsset) func() {
	t.Helper()
	prev := marketHTTPGet
	marketHTTPGet = func(url string, timeout time.Duration) (*http.Response, error) {
		a, ok := assets[url]
		if !ok {
			t.Fatalf("未预期的市场 HTTP 请求: %s", url)
		}
		if a.err != nil {
			return nil, a.err
		}
		code := a.statusCode
		if code == 0 {
			code = http.StatusOK
		}
		return &http.Response{
			StatusCode: code,
			Body:       io.NopCloser(bytes.NewReader(a.body)),
		}, nil
	}
	return func() { marketHTTPGet = prev }
}

// setCoreVersionForTest 临时把 version.DisplayVersion 改成一个合法 semver
// （测试默认值 "dev" 不是合法 semver，会导致 minCoreVersion 过滤逻辑整体
// 短路为"全部兼容"，无法测出过滤效果），返回函数用于还原。
func setCoreVersionForTest(t *testing.T, v string) func() {
	t.Helper()
	prev := version.DisplayVersion
	version.DisplayVersion = v
	return func() { version.DisplayVersion = prev }
}

func sampleIndexJSON(extraVersion string) string {
	v2 := ""
	if extraVersion != "" {
		v2 = `,{"version":"2.0.0","minCoreVersion":"` + extraVersion + `","tarball":"https://get.z1.sale/apps/photos-2.0.0.tar.gz","sha256":"deadbeef","sig":"https://get.z1.sale/apps/photos-2.0.0.sig","changelog":"v2","size":222}`
	}
	return `{
	  "spec": "1.0",
	  "apps": [{
	    "id": "photos", "name": "照片", "icon": "https://example.com/icon.png", "summary": "相册",
	    "latest": "1.0.0",
	    "versions": [{
	      "version": "1.0.0", "minCoreVersion": "1.0.0",
	      "tarball": "https://get.z1.sale/apps/photos-1.0.0.tar.gz", "sha256": "abc", "sig": "https://get.z1.sale/apps/photos-1.0.0.sig",
	      "changelog": "首发", "size": 111
	    }` + v2 + `]
	  }]
	}`
}

func TestMarketList_FetchesAndFiltersByMinCoreVersion(t *testing.T) {
	mux, _ := setupLifecycleTest(t)
	restoreCore := setCoreVersionForTest(t, "1.2.0")
	defer restoreCore()
	restoreHTTP := stubMarketHTTP(t, map[string]fakeMarketAsset{
		marketIndexURL: {body: []byte(sampleIndexJSON("9.0.0"))}, // 2.0.0 要求核心 9.0.0，当前 1.2.0 不兼容
	})
	defer restoreHTTP()

	rec := doRequest(mux, http.MethodGet, "/api/apps/market", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("market list: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Spec  string          `json:"spec"`
		Apps  []marketAppView `json:"apps"`
		Stale bool            `json:"stale"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Stale {
		t.Fatalf("新鲜拉取不应标记 stale")
	}
	if len(resp.Apps) != 1 || resp.Apps[0].ID != "photos" {
		t.Fatalf("apps mismatch: %+v", resp.Apps)
	}
	app := resp.Apps[0]
	if !app.Installable {
		t.Fatalf("1.0.0 应兼容当前核心版本 1.2.0，expected installable=true")
	}
	if len(app.Versions) != 2 {
		t.Fatalf("期望 2 个版本条目，got %d", len(app.Versions))
	}
	var v1, v2 *marketVersionView
	for i := range app.Versions {
		switch app.Versions[i].Version {
		case "1.0.0":
			v1 = &app.Versions[i]
		case "2.0.0":
			v2 = &app.Versions[i]
		}
	}
	if v1 == nil || !v1.Compatible {
		t.Fatalf("1.0.0 应标记 compatible=true, got %+v", v1)
	}
	if v2 == nil || v2.Compatible {
		t.Fatalf("2.0.0（要求核心 9.0.0）应标记 compatible=false, got %+v", v2)
	}
}

func TestMarketList_CachesWithinTTL(t *testing.T) {
	mux, _ := setupLifecycleTest(t)
	defer setCoreVersionForTest(t, "1.2.0")()

	calls := 0
	prev := marketHTTPGet
	marketHTTPGet = func(url string, timeout time.Duration) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(sampleIndexJSON(""))),
		}, nil
	}
	defer func() { marketHTTPGet = prev }()

	for i := 0; i < 3; i++ {
		rec := doRequest(mux, http.MethodGet, "/api/apps/market", nil, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次请求失败: %d body=%s", i, rec.Code, rec.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("TTL 内重复请求应只拉取一次网络索引，实际拉取 %d 次", calls)
	}

	// 缓存文件确实落盘在 dataDir 下。
	if _, err := os.Stat(marketCacheFile(reg.dataDir)); err != nil {
		t.Fatalf("期望缓存文件存在: %v", err)
	}
}

func TestMarketList_DegradesToStaleCacheOnFetchFailure(t *testing.T) {
	mux, _ := setupLifecycleTest(t)
	defer setCoreVersionForTest(t, "1.2.0")()

	// 第一次成功拉取并落盘缓存。
	restore1 := stubMarketHTTP(t, map[string]fakeMarketAsset{
		marketIndexURL: {body: []byte(sampleIndexJSON(""))},
	})
	rec := doRequest(mux, http.MethodGet, "/api/apps/market", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("首次拉取失败: %d body=%s", rec.Code, rec.Body.String())
	}
	restore1()

	// 强制缓存过期，且网络拉取失败：应优雅降级回退到陈旧缓存，而不是报错。
	expireMarketCacheForTest(t)
	restore2 := stubMarketHTTP(t, map[string]fakeMarketAsset{
		marketIndexURL: {err: fmt.Errorf("连接超时")},
	})
	defer restore2()

	rec = doRequest(mux, http.MethodGet, "/api/apps/market", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("网络失败时应优雅降级返回陈旧缓存而非报错: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Stale bool `json:"stale"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Stale {
		t.Fatalf("降级返回的缓存应标记 stale=true")
	}
}

func TestMarketList_ErrorsWhenNoNetworkAndNoCache(t *testing.T) {
	mux, _ := setupLifecycleTest(t)
	restore := stubMarketHTTP(t, map[string]fakeMarketAsset{
		marketIndexURL: {err: fmt.Errorf("连接被拒绝")},
	})
	defer restore()

	rec := doRequest(mux, http.MethodGet, "/api/apps/market", nil, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("无缓存且网络失败应返回 503，got %d body=%s", rec.Code, rec.Body.String())
	}
}

// expireMarketCacheForTest 把落盘缓存的 fetchedAt 改写到 TTL 之外，驱动
// loadMarketIndex 认为缓存已过期需要重新拉取。
func expireMarketCacheForTest(t *testing.T) {
	t.Helper()
	env, ok := loadMarketCache(reg.dataDir)
	if !ok {
		t.Fatalf("expireMarketCacheForTest: 缓存不存在")
	}
	env.FetchedAt = time.Now().Add(-2 * marketCacheTTL)
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(marketCacheFile(reg.dataDir), data, 0644); err != nil {
		t.Fatalf("write cache: %v", err)
	}
}

func TestMarketInstall_DownloadsAndReusesInstallChain(t *testing.T) {
	mux, fe := setupLifecycleTest(t)
	defer setCoreVersionForTest(t, "1.5.0")()

	tb, sigB64 := buildSignedPackage(t, appEntriesWithID("photos", "1.0.0"))
	sigRaw, err := decodeSigForTest(sigB64)
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}

	idxJSON := fmt.Sprintf(`{
	  "spec": "1.0",
	  "apps": [{
	    "id": "photos", "name": "照片", "icon": "https://example.com/icon.png", "summary": "相册",
	    "latest": "1.0.0",
	    "versions": [{
	      "version": "1.0.0", "minCoreVersion": "1.0.0",
	      "tarball": "https://get.z1.sale/apps/photos-1.0.0.tar.gz",
	      "sha256": %q,
	      "sig": "https://get.z1.sale/apps/photos-1.0.0.sig",
	      "changelog": "首发", "size": %d
	    }]
	  }]
	}`, sha256Hex(tb), len(tb))

	restore := stubMarketHTTP(t, map[string]fakeMarketAsset{
		marketIndexURL: {body: []byte(idxJSON)},
		"https://get.z1.sale/apps/photos-1.0.0.tar.gz": {body: tb},
		"https://get.z1.sale/apps/photos-1.0.0.sig":    {body: sigRaw},
	})
	defer restore()

	body := bytes.NewBufferString(`{"id":"photos","version":"1.0.0"}`)
	rec := doRequest(mux, http.MethodPost, "/api/apps/market/install", body, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("market install: %d body=%s", rec.Code, rec.Body.String())
	}
	var pending pendingInstallResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &pending); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pending.ID != "photos" || pending.Status != statusPending {
		t.Fatalf("pending response mismatch: %+v", pending)
	}
	// 复用的是同一条 install 链路：此阶段不应产生任何 confirm 才有的
	// Executor 副作用调用（建用户/目录/unit 等）。
	if len(fe.calls) != 0 {
		t.Fatalf("market install 第一步不应触发 Executor 副作用, got: %v", fe.calls)
	}

	// 复用 confirm 接口完成剩余流程，证明两条 install 入口汇合到同一条
	// 后续链路（无需为市场来源的应用单独走一套 confirm）。
	rec = doRequest(mux, http.MethodPost, "/api/apps/install/photos/confirm", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm after market install: %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMarketInstall_RejectsSHA256Mismatch(t *testing.T) {
	mux, _ := setupLifecycleTest(t)
	defer setCoreVersionForTest(t, "1.5.0")()

	tb, sigB64 := buildSignedPackage(t, appEntriesWithID("photos", "1.0.0"))
	sigRaw, err := decodeSigForTest(sigB64)
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}

	idxJSON := `{
	  "spec": "1.0",
	  "apps": [{
	    "id": "photos", "name": "照片", "icon": "https://example.com/icon.png", "summary": "相册",
	    "latest": "1.0.0",
	    "versions": [{
	      "version": "1.0.0", "minCoreVersion": "1.0.0",
	      "tarball": "https://get.z1.sale/apps/photos-1.0.0.tar.gz",
	      "sha256": "0000000000000000000000000000000000000000000000000000000000beef",
	      "sig": "https://get.z1.sale/apps/photos-1.0.0.sig",
	      "changelog": "首发", "size": 1
	    }]
	  }]
	}`

	restore := stubMarketHTTP(t, map[string]fakeMarketAsset{
		marketIndexURL: {body: []byte(idxJSON)},
		"https://get.z1.sale/apps/photos-1.0.0.tar.gz": {body: tb},
		"https://get.z1.sale/apps/photos-1.0.0.sig":    {body: sigRaw},
	})
	defer restore()

	body := bytes.NewBufferString(`{"id":"photos","version":"1.0.0"}`)
	rec := doRequest(mux, http.MethodPost, "/api/apps/market/install", body, "application/json")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("sha256 不匹配应拒绝为 502，got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMarketInstall_UnknownAppReturns404(t *testing.T) {
	mux, _ := setupLifecycleTest(t)
	defer setCoreVersionForTest(t, "1.5.0")()
	restore := stubMarketHTTP(t, map[string]fakeMarketAsset{
		marketIndexURL: {body: []byte(sampleIndexJSON(""))},
	})
	defer restore()

	body := bytes.NewBufferString(`{"id":"does-not-exist"}`)
	rec := doRequest(mux, http.MethodPost, "/api/apps/market/install", body, "application/json")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知应用应返回 404，got %d body=%s", rec.Code, rec.Body.String())
	}
}

// decodeSigForTest 把 buildSignedPackage 返回的 base64 签名解回原始字节
// （市场下载链路里 .sig 文件是原始二进制，不是 base64，与手工上传表单
// 字段的编码不同，见 scripts/appsign/main.go cmdSign）。
func decodeSigForTest(sigB64 string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(sigB64)
}
