package apps

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nas-panel/common"
	"nas-panel/modules/version"
)

// maxUploadSize 是 multipart 上传表单的硬上限（§1 单包 256MB + 一点编码余量）。
// 用 http.MaxBytesReader 提前截断，不走 filemgmt 那种分片流式上传路径——
// 应用包是一次性整包上传，256MB 远小于 filemgmt 的 10GB 大文件场景。
const maxUploadSize = maxPackageSize + 1<<20

// auditCategory 是本模块全部审计日志使用的分类标签。
const auditCategory = "APPS"

// appsTmpDir 返回 <dataDir>/tmp/apps，staged 包（验签解包后、confirm 前）落盘位置。
func appsTmpDir(dataDir string) string {
	return filepath.Join(dataDir, "tmp", "apps")
}

// stagedTarballPath 返回 staged 包的路径：<dataDir>/tmp/apps/<id>.tar.gz。
func stagedTarballPath(dataDir, id string) string {
	return filepath.Join(appsTmpDir(dataDir), id+".tar.gz")
}

// registry 聚合 apps 模块的运行时状态：db 连接、数据目录、执行器。
// main.go 调 InitApps(dataDir) 后得到的单例，供全部 handler 共用。
type registry struct {
	db       *sql.DB
	dataDir  string
	executor Executor

	// installMu 序列化"分配端口+落 pending 台账"这段关键区——两个并发
	// install 请求必须不能分配到同一端口（allocatePort 的 bind 探测 +
	// port_alloc 占位之间本身不是原子的，用锁兜底代替真正的 DB 事务，
	// 因为 common.OpenSQLite 已 SetMaxOpenConns(1)，锁与单连接语义一致）。
	installMu sync.Mutex
}

var reg *registry

// InitApps 打开 apps.db 并初始化模块级单例，main.go 启动时调用一次
// （与 tasks.InitTasks(dataDir) 同一接线模式）。
func InitApps(dataDir string) error {
	db, err := openAppsDB(dataDir)
	if err != nil {
		return fmt.Errorf("打开 apps.db 失败: %w", err)
	}
	reg = &registry{
		db:       db,
		dataDir:  dataDir,
		executor: NewDefaultExecutor(),
	}
	return nil
}

// RegisterRoutes 注册 apps 模块全部路由（均需管理员会话）。
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/apps", common.AdminMiddleware(handleAppsList))
	mux.HandleFunc("/api/apps/install", common.AdminMiddleware(handleInstall))
	mux.HandleFunc("/api/apps/", common.AdminMiddleware(handleAppsDispatch))
}

// RegisterRoutesBare 跳过 AdminMiddleware，只挂裸 handler，供单测直接发
// 请求（对齐 modules/tasks/api.go 的 registerAPIRoutesBare 模式，不需要
// 伪造登录态）。
func RegisterRoutesBare(mux *http.ServeMux) {
	mux.HandleFunc("/api/apps", handleAppsList)
	mux.HandleFunc("/api/apps/install", handleInstall)
	mux.HandleFunc("/api/apps/", handleAppsDispatch)
}

// handleAppsDispatch 分发 /api/apps/<id>[/action] 下的全部路由：
// confirm 挂在 install 下（/api/apps/install/<id>/confirm），其余
// （详情/start/stop/restart/upgrade/delete）挂在 /api/apps/<id>/<action>。
func handleAppsDispatch(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/apps/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		httpError(w, http.StatusNotFound, "缺少应用 id")
		return
	}

	if parts[0] == "install" {
		// /api/apps/install/<id>/confirm
		if len(parts) == 3 && parts[2] == "confirm" {
			handleInstallConfirm(w, r, parts[1])
			return
		}
		httpError(w, http.StatusNotFound, "未知路由")
		return
	}

	id := parts[0]
	if len(parts) == 1 {
		if r.Method == http.MethodDelete {
			handleAppDelete(w, r, id)
			return
		}
		handleAppDetail(w, r, id)
		return
	}
	if len(parts) == 2 {
		switch parts[1] {
		case "start":
			handleAppAction(w, r, id, "start")
		case "stop":
			handleAppAction(w, r, id, "stop")
		case "restart":
			handleAppAction(w, r, id, "restart")
		case "upgrade":
			handleUpgrade(w, r, id)
		default:
			httpError(w, http.StatusNotFound, "未知路由")
		}
		return
	}
	httpError(w, http.StatusNotFound, "未知路由")
}

// httpError 写一条 JSON 错误响应（与模块内其它 handler 的错误格式一致）。
func httpError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ─────────────────────────────────────────────────────────────
// 安装 Step 1: POST /api/apps/install（上传包，验签解包，返回待确认权限）
// ─────────────────────────────────────────────────────────────

// pendingInstallResponse 是 install 第一步的返回体：manifest 摘要 + 待确认
// 权限清单（§3.3「install 返回待确认权限清单 → confirm」）。
type pendingInstallResponse struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Port         int      `json:"port"`
	Entry        string   `json:"entry"`
	StoragePaths []string `json:"storagePaths"`
	DB           string   `json:"db"`
	DataDir      bool     `json:"dataDir"`
	Status       string   `json:"status"`
}

func handleInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		httpError(w, http.StatusBadRequest, "上传解析失败（可能超过 256MB 上限）: "+err.Error())
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, _, err := r.FormFile("package")
	if err != nil {
		httpError(w, http.StatusBadRequest, "缺少 package 文件字段: "+err.Error())
		return
	}
	defer file.Close()

	tarball, err := io.ReadAll(file)
	if err != nil {
		httpError(w, http.StatusBadRequest, "读取上传内容失败: "+err.Error())
		return
	}

	sigB64 := r.FormValue("signature")
	if sigB64 == "" {
		httpError(w, http.StatusBadRequest, "缺少 signature 字段（ed25519 detached 签名，base64）")
		return
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "signature 不是合法 base64: "+err.Error())
		return
	}

	// TODO(发布前): M1 验签公钥固定用 DevPublicKeyB64（开发密钥对）。正式
	// 上架前须切换为编译进核心的市场正式公钥（§1「验签公钥编译进核心，
	// 市场方为 v0.1 唯一签发者」）——届时这里改为引用一个不可由构建参数
	// 覆盖的正式公钥常量，并移除/隔离开发密钥对，避免开发私钥签出的包
	// 被生产环境误信任。
	pubBytes, err := base64.StdEncoding.DecodeString(DevPublicKeyB64)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "内部错误：开发公钥解码失败")
		return
	}
	if !VerifyAppSignature(tarball, sig, pubBytes) {
		common.LogAuditRequest(r, auditCategory, "安装应用", "签名校验失败", "失败")
		httpError(w, http.StatusBadRequest, "签名校验失败，拒绝安装")
		return
	}

	pkg, err := UnpackAppPackage(tarball)
	if err != nil {
		common.LogAuditRequest(r, auditCategory, "安装应用", "应用包校验失败: "+err.Error(), "失败")
		httpError(w, http.StatusBadRequest, "应用包校验失败: "+err.Error())
		return
	}

	if err := checkCoreCompat(pkg.Manifest); err != nil {
		common.LogAuditRequest(r, auditCategory, "安装应用", "核心版本不兼容: "+err.Error(), "失败")
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	id := pkg.Manifest.ID

	reg.installMu.Lock()
	defer reg.installMu.Unlock()

	existing, err := getInstalledApp(reg.db, id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询台账失败: "+err.Error())
		return
	}
	if existing != nil && existing.Status != statusPending {
		// 幂等拒绝：已安装（非 pending）的应用重复 install 直接拒绝，
		// 升级走专门的 /api/apps/<id>/upgrade（§3.3 明确区分两条路径）。
		httpError(w, http.StatusConflict, fmt.Sprintf("应用 %s 已安装（版本 %s），请使用升级接口", id, existing.Version))
		return
	}

	if err := os.MkdirAll(appsTmpDir(reg.dataDir), 0700); err != nil {
		httpError(w, http.StatusInternalServerError, "创建临时目录失败: "+err.Error())
		return
	}
	if err := os.WriteFile(stagedTarballPath(reg.dataDir, id), tarball, 0600); err != nil {
		httpError(w, http.StatusInternalServerError, "暂存应用包失败: "+err.Error())
		return
	}

	port, err := allocatePort(reg.db, reg.executor, pkg.Manifest.Port)
	if err != nil {
		httpError(w, http.StatusConflict, "端口分配失败: "+err.Error())
		return
	}

	if existing == nil {
		if err := insertInstalledApp(reg.db, installedApp{
			ID:      id,
			Name:    pkg.Manifest.Name,
			Version: pkg.Manifest.Version,
			Spec:    pkg.Manifest.Spec,
			Port:    port,
			Unit:    appUnitName(id),
			Secret:  "",
			Status:  statusPending,
		}); err != nil {
			httpError(w, http.StatusInternalServerError, "写入台账失败: "+err.Error())
			return
		}
	} else {
		// 已存在 pending 记录（上次 install 后未 confirm，重新上传覆盖）：
		// 释放旧端口占位，按新分配端口更新。
		releasePort(reg.db, existing.Port)
		if _, err := reg.db.Exec(`UPDATE installed SET name=?, version=?, spec=?, port=?, status=?, updated_at=? WHERE id=?`,
			pkg.Manifest.Name, pkg.Manifest.Version, pkg.Manifest.Spec, port, statusPending, time.Now().Format(time.RFC3339), id); err != nil {
			httpError(w, http.StatusInternalServerError, "更新台账失败: "+err.Error())
			return
		}
	}
	if err := reservePort(reg.db, port); err != nil {
		httpError(w, http.StatusInternalServerError, "端口占位失败: "+err.Error())
		return
	}

	common.LogAuditRequest(r, auditCategory, "安装应用",
		fmt.Sprintf("%s v%s 验签解包成功，分配端口 %d，待确认", id, pkg.Manifest.Version, port), "成功")
	common.EmitEvent("apps", common.EventInfo, "apps.install_pending", map[string]interface{}{
		"id": id, "version": pkg.Manifest.Version, "port": port,
	}, "")

	common.JSONResponse(w, pendingInstallResponse{
		ID:           id,
		Name:         pkg.Manifest.Name,
		Version:      pkg.Manifest.Version,
		Port:         port,
		Entry:        pkg.Manifest.Entry,
		StoragePaths: pkg.Manifest.Permissions.Storage,
		DB:           pkg.Manifest.Permissions.DB,
		DataDir:      pkg.Manifest.DataDir,
		Status:       statusPending,
	})
}

// checkCoreCompat 校验核心版本是否满足 manifest.minCoreVersion（§2）。
// version.DisplayVersion 在未注入 ldflags 的开发/测试构建下固定为 "dev"，
// 不是合法 semver——这种情况下跳过兼容性校验（开发环境视为"永远兼容"，
// 避免本地 make dev / go test 场景因为版本号格式直接拒装，与生产环境
// 发布时 ldflags 注入真实 vX.Y.Z 后才生效的强校验互不冲突）。
func checkCoreCompat(m *Manifest) error {
	core := version.DisplayVersion
	if !isValidSemver(core) {
		return nil
	}
	return m.ValidateCompat(core)
}

// ─────────────────────────────────────────────────────────────
// 安装 Step 2: POST /api/apps/install/<id>/confirm（建用户/目录/unit/启动）
// ─────────────────────────────────────────────────────────────

func handleInstallConfirm(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	app, err := getInstalledApp(reg.db, id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询台账失败: "+err.Error())
		return
	}
	if app == nil {
		httpError(w, http.StatusNotFound, "未找到待确认的安装: "+id)
		return
	}
	if app.Status != statusPending {
		httpError(w, http.StatusConflict, fmt.Sprintf("应用 %s 当前状态为 %s，不是待确认", id, app.Status))
		return
	}

	tarball, err := os.ReadFile(stagedTarballPath(reg.dataDir, id))
	if err != nil {
		httpError(w, http.StatusGone, "暂存的应用包已丢失，请重新上传安装: "+err.Error())
		return
	}
	pkg, err := UnpackAppPackage(tarball)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "重新校验暂存包失败: "+err.Error())
		return
	}

	secret, err := generateAppSecret()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := provisionApp(reg.executor, id, pkg, app.Port, secret); err != nil {
		common.LogAuditRequest(r, auditCategory, "确认安装应用", fmt.Sprintf("%s: %v", id, err), "失败")
		httpError(w, http.StatusInternalServerError, "安装失败: "+err.Error())
		return
	}

	if _, err := reg.db.Exec(`UPDATE installed SET secret=?, status=?, updated_at=? WHERE id=?`,
		secret, statusInstalled, time.Now().Format(time.RFC3339), id); err != nil {
		httpError(w, http.StatusInternalServerError, "更新台账失败: "+err.Error())
		return
	}
	os.Remove(stagedTarballPath(reg.dataDir, id))

	common.LogAuditRequest(r, auditCategory, "确认安装应用", fmt.Sprintf("%s v%s 已启动，端口 %d", id, pkg.Manifest.Version, app.Port), "成功")
	common.EmitEvent("apps", common.EventSuccess, "apps.installed", map[string]interface{}{
		"id": id, "version": pkg.Manifest.Version, "port": app.Port,
	}, "")

	common.JSONResponse(w, buildAppDetail(reg.executor, mustGetInstalledApp(id)))
}

// provisionApp 执行 confirm 的全部落地副作用（建用户/目录/unit/daemon-reload/
// enable/start），均通过 Executor（§3.1-3.2）。任一步失败立即返回错误，
// 调用方（confirm/upgrade）负责决定是否需要回滚已完成的步骤。
func provisionApp(exec Executor, id string, pkg *AppPackage, port int, secret string) error {
	username := appSystemUser(id)
	if _, err := exec.AddAppUser(username); err != nil {
		// 用户可能已存在（重试 confirm 的场景）：LookupUser 能查到就放行，
		// 查不到才是真失败。
		if _, _, lookErr := exec.LookupUser(username); lookErr != nil {
			return fmt.Errorf("创建应用用户失败: %w", err)
		}
	}
	uid, gid, err := exec.LookupUser(username)
	if err != nil {
		return fmt.Errorf("查询应用用户 uid/gid 失败: %w", err)
	}

	programDir := appProgramDir(id)
	dataDir := appDataDir(id)

	// 程序目录：root 属主，应用用户只读（§3.1）——不 chown 给应用用户，
	// 用 -1/-1 表示跳过 chown，目录保持创建者（root，因为 MkdirAllChown
	// 在生产 Executor 下由面板进程直接调用 os.MkdirAll，面板进程非 root
	// 时这里会失败；M1 面板仍以具备写 /var/lib/nas 权限的身份运行，
	// 与 diskmgmt 等模块对 /var/lib/nas-monitor 等路径的既有假设一致）。
	if err := exec.MkdirAllChown(programDir, 0755, -1, -1); err != nil {
		return fmt.Errorf("创建程序目录失败: %w", err)
	}
	if err := writeAppFiles(exec, programDir, pkg.Files); err != nil {
		return fmt.Errorf("写入程序文件失败: %w", err)
	}

	// 数据目录：chown 给应用用户（§3.1「应用用户可写」）。
	if err := exec.MkdirAllChown(dataDir, 0750, uid, gid); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}

	// manifest 声明的 storage 路径：核心创建并 chown 应用用户（§2/§3.1）。
	for _, p := range pkg.Manifest.Permissions.Storage {
		if err := exec.MkdirAllChown(p, 0750, uid, gid); err != nil {
			return fmt.Errorf("创建 storage 路径 %s 失败: %w", p, err)
		}
	}

	readWritePaths := append([]string{programDir, dataDir}, pkg.Manifest.Permissions.Storage...)
	unitContent := buildAppUnit(string(pkg.Files["unit.tpl"]), port, dataDir, secret, username, readWritePaths)
	unit := appUnitName(id)
	if err := exec.WriteUnitFile(unit, unitContent); err != nil {
		return fmt.Errorf("写入 unit 文件失败: %w", err)
	}

	if _, err := exec.DaemonReload(); err != nil {
		return fmt.Errorf("daemon-reload 失败: %w", err)
	}
	if _, err := exec.EnableUnit(unit); err != nil {
		return fmt.Errorf("enable 失败: %w", err)
	}
	if _, err := exec.StartUnit(unit); err != nil {
		return fmt.Errorf("start 失败: %w", err)
	}

	if err := waitHealthz(exec, port, pkg.Manifest.Healthz, 30*time.Second); err != nil {
		return fmt.Errorf("启动后 healthz 未通过: %w", err)
	}
	return nil
}

// writeAppFiles 把 AppPackage.Files（相对路径 -> 内容）落盘到 programDir 下。
// unit.tpl/manifest.json/icon.png/README.md 一并落盘（供详情页展示与审计），
// 与 app/ 下的实际程序文件同等对待——§1 规范未要求区分哪些文件该留存。
func writeAppFiles(exec Executor, programDir string, files map[string][]byte) error {
	for relPath, data := range files {
		full := filepath.Join(programDir, relPath)
		if err := exec.MkdirAllChown(filepath.Dir(full), 0755, -1, -1); err != nil {
			return err
		}
		if err := exec.WriteFile(full, data, 0644); err != nil {
			return err
		}
	}
	return nil
}

// waitHealthz 轮询 127.0.0.1:<port><healthzPath> 直到 200 或超时
// （§3.3「启动 → healthz 首过 → 门户卡点亮」；§6「启动后 30s 内须 200」）。
// 实际的单次 HTTP 探测经 exec.CheckHealthz，供测试注入假健康检查结果。
func waitHealthz(exec Executor, port int, healthzPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := exec.CheckHealthz(port, healthzPath); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("超时")
	}
	return lastErr
}

// ─────────────────────────────────────────────────────────────
// 列表 / 详情
// ─────────────────────────────────────────────────────────────

// appSummary 是列表视图的一条记录（§3.3「列表：安装态/运行态/版本/健康」）。
type appSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Port        int    `json:"port"`
	Status      string `json:"status"`      // 台账状态：pending/installed/stopped
	ActiveState string `json:"activeState"` // 真实 systemctl is-active 结果
}

// appDetail 是详情视图（§3.3「详情含资源用量」；M1 暂不采集内存/CPU，
// 留给后续里程碑接 systemd 资源统计，这里先给出台账 + 真实运行态）。
type appDetail struct {
	appSummary
	Spec         string   `json:"spec"`
	Unit         string   `json:"unit"`
	InstalledAt  string   `json:"installedAt"`
	UpdatedAt    string   `json:"updatedAt"`
	StoragePaths []string `json:"storagePaths"`
}

func buildAppSummary(exec Executor, a installedApp) appSummary {
	active := ""
	if a.Status != statusPending {
		active = exec.UnitActiveState(a.Unit)
	}
	return appSummary{
		ID:          a.ID,
		Name:        a.Name,
		Version:     a.Version,
		Port:        a.Port,
		Status:      a.Status,
		ActiveState: active,
	}
}

func buildAppDetail(exec Executor, a installedApp) appDetail {
	return appDetail{
		appSummary:  buildAppSummary(exec, a),
		Spec:        a.Spec,
		Unit:        a.Unit,
		InstalledAt: a.InstalledAt,
		UpdatedAt:   a.UpdatedAt,
	}
}

func mustGetInstalledApp(id string) installedApp {
	a, _ := getInstalledApp(reg.db, id)
	if a == nil {
		return installedApp{ID: id}
	}
	return *a
}

func handleAppsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	apps, err := listInstalledApps(reg.db)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询台账失败: "+err.Error())
		return
	}
	summaries := make([]appSummary, 0, len(apps))
	for _, a := range apps {
		summaries = append(summaries, buildAppSummary(reg.executor, a))
	}
	common.JSONResponse(w, map[string]interface{}{"apps": summaries})
}

func handleAppDetail(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a, err := getInstalledApp(reg.db, id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询台账失败: "+err.Error())
		return
	}
	if a == nil {
		httpError(w, http.StatusNotFound, "应用未安装: "+id)
		return
	}
	common.JSONResponse(w, buildAppDetail(reg.executor, *a))
}

// ─────────────────────────────────────────────────────────────
// start / stop / restart
// ─────────────────────────────────────────────────────────────

func handleAppAction(w http.ResponseWriter, r *http.Request, id, action string) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a, err := getInstalledApp(reg.db, id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询台账失败: "+err.Error())
		return
	}
	if a == nil {
		httpError(w, http.StatusNotFound, "应用未安装: "+id)
		return
	}
	if a.Status == statusPending {
		httpError(w, http.StatusConflict, "应用尚未完成安装确认")
		return
	}

	var actionErr error
	var newStatus string
	switch action {
	case "start":
		_, actionErr = reg.executor.StartUnit(a.Unit)
		newStatus = statusInstalled
	case "stop":
		_, actionErr = reg.executor.StopUnit(a.Unit)
		newStatus = statusStopped
	case "restart":
		_, actionErr = reg.executor.RestartUnit(a.Unit)
		newStatus = statusInstalled
	default:
		httpError(w, http.StatusBadRequest, "未知操作: "+action)
		return
	}

	actionCN := map[string]string{"start": "启动", "stop": "停止", "restart": "重启"}[action]
	if actionErr != nil {
		common.LogAuditRequest(r, auditCategory, actionCN+"应用", fmt.Sprintf("%s: %v", id, actionErr), "失败")
		httpError(w, http.StatusInternalServerError, actionCN+"失败: "+actionErr.Error())
		return
	}

	updateInstalledAppStatus(reg.db, id, newStatus)
	common.LogAuditRequest(r, auditCategory, actionCN+"应用", id, "成功")
	common.EmitEvent("apps", common.EventInfo, "apps."+action, map[string]interface{}{"id": id}, "")

	common.JSONResponse(w, buildAppDetail(reg.executor, mustGetInstalledApp(id)))
}

// ─────────────────────────────────────────────────────────────
// 升级（新包验签解包 → 备份旧程序目录 → 替换 → 失败自动回滚，§3.3）
// ─────────────────────────────────────────────────────────────

func handleUpgrade(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a, err := getInstalledApp(reg.db, id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询台账失败: "+err.Error())
		return
	}
	if a == nil {
		httpError(w, http.StatusNotFound, "应用未安装: "+id)
		return
	}
	if a.Status == statusPending {
		httpError(w, http.StatusConflict, "应用尚未完成安装确认，不能升级")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		httpError(w, http.StatusBadRequest, "上传解析失败（可能超过 256MB 上限）: "+err.Error())
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, _, err := r.FormFile("package")
	if err != nil {
		httpError(w, http.StatusBadRequest, "缺少 package 文件字段: "+err.Error())
		return
	}
	defer file.Close()
	tarball, err := io.ReadAll(file)
	if err != nil {
		httpError(w, http.StatusBadRequest, "读取上传内容失败: "+err.Error())
		return
	}

	sigB64 := r.FormValue("signature")
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "signature 不是合法 base64: "+err.Error())
		return
	}
	pubBytes, _ := base64.StdEncoding.DecodeString(DevPublicKeyB64)
	if !VerifyAppSignature(tarball, sig, pubBytes) {
		common.LogAuditRequest(r, auditCategory, "升级应用", fmt.Sprintf("%s: 签名校验失败", id), "失败")
		httpError(w, http.StatusBadRequest, "签名校验失败，拒绝升级")
		return
	}

	pkg, err := UnpackAppPackage(tarball)
	if err != nil {
		httpError(w, http.StatusBadRequest, "应用包校验失败: "+err.Error())
		return
	}
	if pkg.Manifest.ID != id {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("包内 id %q 与目标应用 %q 不一致", pkg.Manifest.ID, id))
		return
	}
	if err := checkCoreCompat(pkg.Manifest); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	// §2 upgrade.minUpgradeFrom：低于此版本要求先卸载重装。
	if pkg.Manifest.Upgrade.MinUpgradeFrom != "" {
		cmp, err := CompareSemver(a.Version, pkg.Manifest.Upgrade.MinUpgradeFrom)
		if err != nil {
			httpError(w, http.StatusBadRequest, "版本比较失败: "+err.Error())
			return
		}
		if cmp < 0 {
			httpError(w, http.StatusConflict, fmt.Sprintf(
				"当前已安装版本 %s 低于新版本要求的最低升级起点 %s，请先卸载后重新安装",
				a.Version, pkg.Manifest.Upgrade.MinUpgradeFrom))
			return
		}
	}

	if err := upgradeApp(reg.executor, id, *a, pkg); err != nil {
		common.LogAuditRequest(r, auditCategory, "升级应用", fmt.Sprintf("%s: %v（已自动回滚）", id, err), "失败")
		httpError(w, http.StatusInternalServerError, "升级失败，已自动回滚: "+err.Error())
		return
	}

	updateInstalledAppVersion(reg.db, id, pkg.Manifest.Name, pkg.Manifest.Version, pkg.Manifest.Spec, statusInstalled)
	common.LogAuditRequest(r, auditCategory, "升级应用", fmt.Sprintf("%s: %s -> %s", id, a.Version, pkg.Manifest.Version), "成功")
	common.EmitEvent("apps", common.EventSuccess, "apps.upgraded", map[string]interface{}{
		"id": id, "from": a.Version, "to": pkg.Manifest.Version,
	}, "")

	common.JSONResponse(w, buildAppDetail(reg.executor, mustGetInstalledApp(id)))
}

// upgradeHealthzTimeout 是升级后等待新版本 healthz 首过的超时（§3.3 失败自动
// 回滚的判定窗口；比安装 confirm 的 30s 更宽松，考虑升级后可能有迁移耗时）。
// 声明为 var（而非 const）仅是为了测试可临时调小，生产路径恒为 90s。
var upgradeHealthzTimeout = 90 * time.Second

// upgradeApp 执行"备份旧程序目录 → 替换 → 重启 → healthz 复核"流程；
// healthz 未在 upgradeHealthzTimeout 内通过或重启失败，自动回滚
// （把 .bak 目录移回原位、重启旧版本）后返回错误。
func upgradeApp(exec Executor, id string, old installedApp, newPkg *AppPackage) error {
	programDir := appProgramDir(id)
	backupDir := programDir + fmt.Sprintf(".bak-%d-%s", time.Now().Unix(), randomSuffix())

	if err := exec.Rename(programDir, backupDir); err != nil {
		return fmt.Errorf("备份旧程序目录失败: %w", err)
	}

	rollback := func(cause error) error {
		exec.StopUnit(old.Unit)
		exec.RemoveAll(programDir)
		if rerr := exec.Rename(backupDir, programDir); rerr != nil {
			return fmt.Errorf("升级失败（%v），回滚也失败: %w", cause, rerr)
		}
		if _, err := exec.RestartUnit(old.Unit); err != nil {
			return fmt.Errorf("升级失败（%v），回滚后重启旧版本也失败: %w", cause, err)
		}
		return fmt.Errorf("升级失败已回滚到 %s: %v", old.Version, cause)
	}

	username := appSystemUser(id)
	if err := exec.MkdirAllChown(programDir, 0755, -1, -1); err != nil {
		return rollback(fmt.Errorf("创建新程序目录失败: %w", err))
	}
	if err := writeAppFiles(exec, programDir, newPkg.Files); err != nil {
		return rollback(fmt.Errorf("写入新程序文件失败: %w", err))
	}

	uid, gid, err := exec.LookupUser(username)
	if err != nil {
		return rollback(fmt.Errorf("查询应用用户失败: %w", err))
	}
	dataDir := appDataDir(id)
	for _, p := range newPkg.Manifest.Permissions.Storage {
		if err := exec.MkdirAllChown(p, 0750, uid, gid); err != nil {
			return rollback(fmt.Errorf("创建 storage 路径 %s 失败: %w", p, err))
		}
	}

	readWritePaths := append([]string{programDir, dataDir}, newPkg.Manifest.Permissions.Storage...)
	unitContent := buildAppUnit(string(newPkg.Files["unit.tpl"]), old.Port, dataDir, old.Secret, username, readWritePaths)
	if err := exec.WriteUnitFile(old.Unit, unitContent); err != nil {
		return rollback(fmt.Errorf("写入新 unit 文件失败: %w", err))
	}
	if _, err := exec.DaemonReload(); err != nil {
		return rollback(fmt.Errorf("daemon-reload 失败: %w", err))
	}
	if _, err := exec.RestartUnit(old.Unit); err != nil {
		return rollback(fmt.Errorf("重启新版本失败: %w", err))
	}

	if err := waitHealthz(exec, old.Port, newPkg.Manifest.Healthz, upgradeHealthzTimeout); err != nil {
		return rollback(fmt.Errorf("新版本 healthz 未通过: %w", err))
	}

	exec.RemoveAll(backupDir)
	return nil
}

// ─────────────────────────────────────────────────────────────
// 卸载（DELETE /api/apps/<id>，body { "keepData": bool }）
// ─────────────────────────────────────────────────────────────

type deleteRequest struct {
	KeepData bool `json:"keepData"`
}

func handleAppDelete(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodDelete {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a, err := getInstalledApp(reg.db, id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询台账失败: "+err.Error())
		return
	}
	if a == nil {
		httpError(w, http.StatusNotFound, "应用未安装: "+id)
		return
	}

	var req deleteRequest
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req) // body 可省略，默认不保留数据
	}

	exec := reg.executor
	exec.StopUnit(a.Unit)
	exec.DisableUnit(a.Unit)
	exec.RemoveAll(systemdUnitPath(a.Unit))
	exec.DaemonReload()
	exec.RemoveAll(appProgramDir(id))
	if !req.KeepData {
		exec.RemoveAll(appDataDir(id))
	}
	// manifest 声明的 storage 路径是用户数据区，卸载时永不删除——仅在详情页
	// 里标记（§3.1「数据目录…卸载按用户选择」只提及 dataDir，storage 路径
	// 属于池内用户数据，核心不得代为删除）。
	exec.DelAppUser(appSystemUser(id))
	releasePort(reg.db, a.Port)
	deleteInstalledApp(reg.db, id)

	common.LogAuditRequest(r, auditCategory, "卸载应用", fmt.Sprintf("%s（保留数据: %v）", id, req.KeepData), "成功")
	common.EmitEvent("apps", common.EventInfo, "apps.deleted", map[string]interface{}{
		"id": id, "keepData": req.KeepData,
	}, "")

	common.JSONResponse(w, map[string]interface{}{"id": id, "deleted": true, "keepData": req.KeepData})
}
