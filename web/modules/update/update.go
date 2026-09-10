package update

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"nas-panel/common"
	"nas-panel/modules/version"
)

// OTA 签名公钥（ed25519，hex）。私钥在发布环境（见 scripts/sign.go / release.sh），
// 绝不入库。公钥内嵌在此，即使更新服务器被攻陷也无法伪造有效签名。
const publicKeyHex = "af048a1224405d29d06a5e97d795f525a78ef239b9bcf6c051c9cea633f5457e"

// controlBase 更新通道根地址。UPDATE_BASE 环境变量可覆盖（本地测试用）。
func controlBase() string {
	if b := os.Getenv("UPDATE_BASE"); b != "" {
		return strings.TrimRight(b, "/")
	}
	return "https://get.z1.sale/control"
}

// Manifest 是更新通道的版本清单（release.sh 生成并上传）。
type Manifest struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Sig     string `json:"sig"`
	URL     string `json:"url"`
}

var (
	updateMu     sync.Mutex
	updateStatus = "idle" // idle / running / done / failed
	updateDetail = ""
)

func publicKey() ed25519.PublicKey {
	b, _ := hex.DecodeString(publicKeyHex)
	return ed25519.PublicKey(b)
}

func arch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "amd64"
}

func fetchManifest() (*Manifest, error) {
	url := fmt.Sprintf("%s/latest-%s.json", controlBase(), arch())
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var m Manifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/update/check", common.AuthMiddleware(handleCheck))
	mux.HandleFunc("/api/update/status", common.AuthMiddleware(handleStatus))
	mux.HandleFunc("/api/update/upgrade", common.AuthMiddleware(handleUpgrade))
}

func handleCheck(w http.ResponseWriter, r *http.Request) {
	cur := version.DisplayVersion
	m, err := fetchManifest()
	if err != nil {
		common.JSONResponse(w, map[string]interface{}{
			"current": cur, "latest": "", "has_update": false,
			"error": "无法获取更新信息（" + err.Error() + "）",
		})
		return
	}
	common.JSONResponse(w, map[string]interface{}{
		"current":    cur,
		"latest":     m.Version,
		"has_update": cur != "" && m.Version != "" && cur != m.Version,
	})
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	updateMu.Lock()
	defer updateMu.Unlock()
	common.JSONResponse(w, map[string]interface{}{"status": updateStatus, "detail": updateDetail})
}

func handleUpgrade(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if r.FormValue("confirm") != "yes" {
		http.Error(w, `{"error":"请确认升级"}`, http.StatusBadRequest)
		return
	}
	updateMu.Lock()
	if updateStatus == "running" {
		updateMu.Unlock()
		http.Error(w, `{"error":"升级正在进行中"}`, http.StatusConflict)
		return
	}
	updateStatus = "running"
	updateDetail = ""
	updateMu.Unlock()

	go runUpgrade()

	common.JSONResponse(w, map[string]interface{}{"message": "升级已启动"})
}

func setStatus(status, detail string) {
	updateMu.Lock()
	updateStatus = status
	updateDetail = detail
	updateMu.Unlock()
}

// runUpgrade 在面板进程内完成「下载 + 校验签名」，通过后派发分离的 apply 脚本执行
// 「替换 + 重启 + 健康检查 + 回滚」。分离脚本作为独立进程，能跨过面板重启存活。
func runUpgrade() {
	defer func() {
		updateMu.Lock()
		if updateStatus == "running" {
			updateStatus = "failed"
		}
		updateMu.Unlock()
	}()

	m, err := fetchManifest()
	if err != nil {
		setStatus("failed", "获取更新信息失败: "+err.Error())
		return
	}

	setStatus("running", "下载新版本...")
	binURL := m.URL
	if binURL == "" {
		binURL = fmt.Sprintf("%s/nas-panel-%s.latest", controlBase(), arch())
	}
	bin, err := download(binURL)
	if err != nil {
		setStatus("failed", "下载失败: "+err.Error())
		return
	}

	// 1. SHA256 校验（防传输损坏）
	sum := sha256.Sum256(bin)
	if m.SHA256 != "" && hex.EncodeToString(sum[:]) != m.SHA256 {
		setStatus("failed", "SHA256 校验失败，拒绝安装")
		return
	}

	// 2. ed25519 签名校验（防供应链攻击，用内嵌公钥）
	sigBytes, err := hex.DecodeString(m.Sig)
	if err != nil || !ed25519.Verify(publicKey(), bin, sigBytes) {
		setStatus("failed", "签名校验失败，拒绝安装")
		return
	}

	// 3. ELF 魔数检查
	if len(bin) < 4 || bin[0] != 0x7f || bin[1] != 'E' || bin[2] != 'L' || bin[3] != 'F' {
		setStatus("failed", "下载的不是有效 ELF 二进制")
		return
	}

	// 4. 写已校验的二进制到 /tmp
	tmpFile := fmt.Sprintf("/tmp/nas-panel-upgrade-%d", time.Now().Unix())
	if err := os.WriteFile(tmpFile, bin, 0755); err != nil {
		setStatus("failed", "写入临时文件失败: "+err.Error())
		return
	}

	// 5. 派发分离的 apply 脚本。用 systemd-run 放进独立 transient unit（自己的 cgroup），
	// 这样 systemctl restart nas-panel 不会把它一起杀掉——健康检查 + 回滚才能正常执行。
	setStatus("running", "签名校验通过，开始替换并重启...")
	if _, err := common.SudoExec("systemd-run", "--unit=nas-panel-upgrade", "--collect", "bash", "/opt/nas/scripts/apply-upgrade.sh", tmpFile); err != nil {
		setStatus("failed", "启动升级脚本失败: "+err.Error())
		return
	}

	common.LogAudit("system", "面板升级", "UPDATE", "/api/update/upgrade", "→ "+m.Version, "pending", "")
	// 状态交给 apply-upgrade.sh 完成（它写 /tmp/nas-upgrade.log，前端轮询 /api/update/status）
	// 但 apply 脚本完成后面板已重启，状态会被新进程重置为 idle；这里先标记 running，由前端按需刷新。
}

func download(url string) ([]byte, error) {
	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
