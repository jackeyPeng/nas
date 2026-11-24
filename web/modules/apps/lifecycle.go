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
	"nas-panel/modules/base"
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

// RegisterRoutes 注册 apps 模块全部路由（均需管理员会话），并启动后台
// 健康轮询器（M1-4，与 monitor.startSampler 同一接线模式：谁注册路由谁
// 负责启动对应的后台 goroutine）。
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/apps", common.AdminMiddleware(handleAppsList))
	mux.HandleFunc("/api/apps/install", common.AdminMiddleware(handleInstall))
	mux.HandleFunc("/api/apps/market", common.AdminMiddleware(handleMarketList))
	mux.HandleFunc("/api/apps/market/install", common.AdminMiddleware(handleMarketInstall))
	mux.HandleFunc("/api/apps/", common.AdminMiddleware(handleAppsDispatch))
	startHealthPoller()
}

// RegisterRoutesBare 跳过 AdminMiddleware，只挂裸 handler，供单测直接发
// 请求（对齐 modules/tasks/api.go 的 registerAPIRoutesBare 模式，不需要
// 伪造登录态）。
func RegisterRoutesBare(mux *http.ServeMux) {
	mux.HandleFunc("/api/apps", handleAppsList)
	mux.HandleFunc("/api/apps/install", handleInstall)
	mux.HandleFunc("/api/apps/market", handleMarketList)
	mux.HandleFunc("/api/apps/market/install", handleMarketInstall)
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
		case "health":
			handleAppHealth(w, r, id)
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
	Cache        bool     `json:"cache"` // permissions.cache 是否申领 Valkey（供前端确认步骤展示基座就绪徽标）
	Mqtt         bool     `json:"mqtt"`  // permissions.mqtt.sub/pub 非空即视为申领 Mosquitto
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

	resp, instErr := installFromTarball(r, tarball, sig, "安装应用")
	if instErr != nil {
		httpError(w, instErr.status, instErr.message)
		return
	}
	common.JSONResponse(w, resp)
}

// httpStatusError 是 installFromTarball 等共享链路函数的错误返回形态：
// 携带 HTTP 状态码，调用方（手工上传 handler / 市场安装 handler）各自
// 决定怎么写响应（httpError 的参数形状，便于直接转发）。
type httpStatusError struct {
	status  int
	message string
}

func (e *httpStatusError) Error() string { return e.message }

// installFromTarball 是 install 第一步"验签→解包→核心版本校验→端口分配
// →落 pending 台账"的共享实现，供手工上传（handleInstall）与市场一键
// 安装（market.go 的 handleMarketInstall）两条入口复用——两者唯一差异
// 只是 tarball/签名的来源（HTTP 表单字段 vs 市场索引下载），验签通过后
// 的整条链路必须完全一致，不应该为市场来源的包开一条绕过校验的近路。
// actionCN 用于审计日志动作名（"安装应用" vs "市场安装应用"），便于审计
// 页区分来源。
func installFromTarball(r *http.Request, tarball, sig []byte, actionCN string) (*pendingInstallResponse, *httpStatusError) {
	// TODO(发布前): M1 验签公钥固定用 DevPublicKeyB64（开发密钥对）。正式
	// 上架前须切换为编译进核心的市场正式公钥（§1「验签公钥编译进核心，
	// 市场方为 v0.1 唯一签发者」）——届时这里改为引用一个不可由构建参数
	// 覆盖的正式公钥常量，并移除/隔离开发密钥对，避免开发私钥签出的包
	// 被生产环境误信任。
	pubBytes, err := base64.StdEncoding.DecodeString(DevPublicKeyB64)
	if err != nil {
		return nil, &httpStatusError{http.StatusInternalServerError, "内部错误：开发公钥解码失败"}
	}
	if !VerifyAppSignature(tarball, sig, pubBytes) {
		common.LogAuditRequest(r, auditCategory, actionCN, "签名校验失败", "失败")
		return nil, &httpStatusError{http.StatusBadRequest, "签名校验失败，拒绝安装"}
	}

	pkg, err := UnpackAppPackage(tarball)
	if err != nil {
		common.LogAuditRequest(r, auditCategory, actionCN, "应用包校验失败: "+err.Error(), "失败")
		return nil, &httpStatusError{http.StatusBadRequest, "应用包校验失败: " + err.Error()}
	}

	if err := checkCoreCompat(pkg.Manifest); err != nil {
		common.LogAuditRequest(r, auditCategory, actionCN, "核心版本不兼容: "+err.Error(), "失败")
		return nil, &httpStatusError{http.StatusBadRequest, err.Error()}
	}

	id := pkg.Manifest.ID

	reg.installMu.Lock()
	defer reg.installMu.Unlock()

	existing, err := getInstalledApp(reg.db, id)
	if err != nil {
		return nil, &httpStatusError{http.StatusInternalServerError, "查询台账失败: " + err.Error()}
	}
	if existing != nil && existing.Status != statusPending {
		// 幂等拒绝：已安装（非 pending）的应用重复 install 直接拒绝，
		// 升级走专门的 /api/apps/<id>/upgrade（§3.3 明确区分两条路径）。
		return nil, &httpStatusError{http.StatusConflict, fmt.Sprintf("应用 %s 已安装（版本 %s），请使用升级接口", id, existing.Version)}
	}

	if err := os.MkdirAll(appsTmpDir(reg.dataDir), 0700); err != nil {
		return nil, &httpStatusError{http.StatusInternalServerError, "创建临时目录失败: " + err.Error()}
	}
	if err := os.WriteFile(stagedTarballPath(reg.dataDir, id), tarball, 0600); err != nil {
		return nil, &httpStatusError{http.StatusInternalServerError, "暂存应用包失败: " + err.Error()}
	}

	port, err := allocatePort(reg.db, reg.executor, pkg.Manifest.Port)
	if err != nil {
		return nil, &httpStatusError{http.StatusConflict, "端口分配失败: " + err.Error()}
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
			Healthz: pkg.Manifest.Healthz,
			Status:  statusPending,
		}); err != nil {
			return nil, &httpStatusError{http.StatusInternalServerError, "写入台账失败: " + err.Error()}
		}
	} else {
		// 已存在 pending 记录（上次 install 后未 confirm，重新上传覆盖）：
		// 释放旧端口占位，按新分配端口更新。
		releasePort(reg.db, existing.Port)
		if _, err := reg.db.Exec(`UPDATE installed SET name=?, version=?, spec=?, port=?, healthz=?, status=?, updated_at=? WHERE id=?`,
			pkg.Manifest.Name, pkg.Manifest.Version, pkg.Manifest.Spec, port, pkg.Manifest.Healthz, statusPending, time.Now().Format(time.RFC3339), id); err != nil {
			return nil, &httpStatusError{http.StatusInternalServerError, "更新台账失败: " + err.Error()}
		}
	}
	if err := reservePort(reg.db, port); err != nil {
		return nil, &httpStatusError{http.StatusInternalServerError, "端口占位失败: " + err.Error()}
	}

	common.LogAuditRequest(r, auditCategory, actionCN,
		fmt.Sprintf("%s v%s 验签解包成功，分配端口 %d，待确认", id, pkg.Manifest.Version, port), "成功")
	common.EmitEvent("apps", common.EventInfo, "apps.install_pending", map[string]interface{}{
		"id": id, "version": pkg.Manifest.Version, "port": port,
	}, "")

	return &pendingInstallResponse{
		ID:           id,
		Name:         pkg.Manifest.Name,
		Version:      pkg.Manifest.Version,
		Port:         port,
		Entry:        pkg.Manifest.Entry,
		StoragePaths: pkg.Manifest.Permissions.Storage,
		DB:           pkg.Manifest.Permissions.DB,
		Cache:        pkg.Manifest.Permissions.Cache,
		Mqtt:         len(pkg.Manifest.Permissions.MQTT.Sub) > 0 || len(pkg.Manifest.Permissions.MQTT.Pub) > 0,
		DataDir:      pkg.Manifest.DataDir,
		Status:       statusPending,
	}, nil
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

// provisionBaseServices 按 manifest 声明的 pgsql/cache/mqtt 能力向 base 模块
// 申领凭据，组装成 "Environment=KEY=value" 行供 buildAppUnit 注入 unit。
// 调用前 manifest 校验层（manifest.go Permissions.validate）已经按同样的
// base.Ready() 判定拒绝过未就绪的声明，这里再次检查是防御性的——manifest
// 可能在 install 时就绪、confirm/upgrade 时基座服务被面板管理员临时
// 停用，必须在真正申领凭据前再确认一次，拒绝时的报错同样指名具体组件状态
// （§需求「检查组件就绪（不就绪拒绝安装），错误信息必须指名具体组件」）。
// 凭据只通过返回的 env 行流向 unit 文件，不写日志、不进 HTTP 响应体
// （§需求红线）。
func provisionBaseServices(id string, perm Permissions) ([]string, error) {
	var env []string

	if perm.DB == "pgsql" {
		if !base.Ready(base.ComponentPostgres) {
			return nil, fmt.Errorf("permissions.db=pgsql 但 %s", base.ComponentStateDescription(base.ComponentPostgres))
		}
		creds, err := base.ProvisionPostgres(id)
		if err != nil {
			return nil, fmt.Errorf("申领 PostgreSQL 数据库失败: %w", err)
		}
		env = append(env, "Environment=POSTGRES_DSN="+creds.DSN)
	}

	if perm.Cache {
		if !base.Ready(base.ComponentValkey) {
			return nil, fmt.Errorf("permissions.cache=true 但 %s", base.ComponentStateDescription(base.ComponentValkey))
		}
		creds, err := base.ProvisionValkey(id)
		if err != nil {
			return nil, fmt.Errorf("申领 Valkey db 编号失败: %w", err)
		}
		env = append(env, "Environment=VALKEY_URL="+creds.URL)
	}

	if len(perm.MQTT.Sub) > 0 || len(perm.MQTT.Pub) > 0 {
		if !base.Ready(base.ComponentMosquitto) {
			return nil, fmt.Errorf("permissions.mqtt 非空但 %s", base.ComponentStateDescription(base.ComponentMosquitto))
		}
		creds, err := base.ProvisionMQTT(id)
		if err != nil {
			return nil, fmt.Errorf("申领 MQTT 账号失败: %w", err)
		}
		env = append(env,
			"Environment=MQTT_USER="+creds.Username,
			"Environment=MQTT_PASS="+creds.Password,
			"Environment=MQTT_TOPIC_PREFIX="+creds.TopicPrefix,
		)
	}

	return env, nil
}

// deprovisionBaseServices 卸载流程里清理 appID 申领过的全部基座服务资源
// （drop database/role、回收 Valkey db 编号、删 Mosquitto 账号+ACL）。
// 幂等：某能力从未申领过时 base.Deprovision* 直接返回 nil（见 provision.go
// 注释），因此这里不需要先判断 manifest declares 了哪些能力——统一调用
// 三个 Deprovision* 即可，未申领的那些是空操作。keepData=true 时调用方
// （handleAppDelete）直接跳过整个调用，不进这个函数。
func deprovisionBaseServices(id string) []error {
	var errs []error
	if err := base.DeprovisionPostgres(id); err != nil {
		errs = append(errs, fmt.Errorf("清理 PostgreSQL 数据库失败: %w", err))
	}
	if err := base.DeprovisionValkey(id); err != nil {
		errs = append(errs, fmt.Errorf("清理 Valkey db 编号失败: %w", err))
	}
	if err := base.DeprovisionMQTT(id); err != nil {
		errs = append(errs, fmt.Errorf("清理 MQTT 账号失败: %w", err))
	}
	return errs
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
	// LookupUser 仅用于确认用户确实存在（ProvisionAppDirs 内部也会校验，
	// 这里提前查一次给出更早、更明确的错误信息）；uid/gid 本身不再需要——
	// chown 改由 ProvisionAppDirs/ChownApp 内的收口脚本完成（M1-6b）。
	if _, _, err := exec.LookupUser(username); err != nil {
		return fmt.Errorf("查询应用用户失败: %w", err)
	}

	programDir := appProgramDir(id)
	dataDir := appDataDir(id)

	// 程序目录 + 数据目录：M1-6b 真机实测（.48）发现面板以 fm 运行时
	// 对 /var/lib 无写权，裸 MkdirAllChown（面板进程直接 os.MkdirAll/
	// os.Chown）在真机上失败（"mkdir /var/lib/nas: permission denied"）。
	// 改经 ProvisionAppDirs 走 nas-app-provision.sh 收口脚本：程序目录
	// chown 给面板自身的 NAS_USER（默认 fm）0755（面板进程紧接着要用
	// 自己的身份把程序文件写进这个目录，见下面 writeAppFiles，不能是
	// root:root）+ chown nasapp-<id> 0750 建数据目录，一次调用两步都做
	// （脚本内部固定形状，不接受分拆调用）。运行时应用用户对程序目录的
	// 隔离由 unit 的 ProtectSystem=strict 硬化兜底，不依赖静态属主。
	if err := exec.ProvisionAppDirs(id); err != nil {
		return fmt.Errorf("供给应用目录失败: %w", err)
	}
	// writeAppFiles 内的 MkdirAllChown(..., -1, -1) 是面板进程直接
	// os.MkdirAll（非 sudo），programDir 已在上一步 chown 给面板自身
	// 用户，面板对其下建子目录/写文件天然有权限，无需再走收口脚本。
	if err := writeAppFiles(exec, programDir, pkg.Files); err != nil {
		return fmt.Errorf("写入程序文件失败: %w", err)
	}

	// manifest 声明的 storage 路径：mkdir 走既有 /bin/mkdir -p /data/*
	// 白名单条目（面板侧已有，storage 路径恒以 /data/ 开头，见
	// manifest.go 校验），chown 给应用用户则经 ChownApp（M1-6b：裸
	// chown *:* 任意路径是 root-equivalent，不进白名单）。
	for _, p := range pkg.Manifest.Permissions.Storage {
		if _, err := common.SudoExec("mkdir", "-p", p); err != nil {
			return fmt.Errorf("创建 storage 路径 %s 失败: %w", p, err)
		}
		if err := exec.ChownApp(id, p); err != nil {
			return fmt.Errorf("设置 storage 路径 %s 属主失败: %w", p, err)
		}
	}

	extraEnv, err := provisionBaseServices(id, pkg.Manifest.Permissions)
	if err != nil {
		return err
	}

	readWritePaths := append([]string{programDir, dataDir}, pkg.Manifest.Permissions.Storage...)
	unitContent := buildAppUnit(string(pkg.Files["unit.tpl"]), port, dataDir, secret, username, readWritePaths, extraEnv)
	unit := appUnitName(id)
	if err := exec.WriteUnitFile(unit, unitContent); err != nil {
		return fmt.Errorf("写入 unit 文件失败: %w", err)
	}
	// unit 指向的程序文件补执行位（写文件统一 0644，ExecStart 二进制必须可执行）
	if err := markExecutables(programDir, unitContent); err != nil {
		return fmt.Errorf("标记可执行文件失败: %w", err)
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
// 普通文件统一 0644；可执行位不依赖打包方是否 chmod——由 markExecutables
// 从渲染后的 unit 解析 ExecStart 路径补 0755（真机实测：tar 内 644 二进制
// 导致 systemd 203/EXEC，见 2026-11-23 .48 日志）。
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

// markExecutables 把渲染后 unit 里 Exec* 行指向的程序文件补上 0755 执行位。
// 只处理位于 programDir 前缀内的路径（包外路径不动，防御性跳过）；文件由
// 面板自身用户写入，os.Chmod 无需提权。真机背景：tar 内 644 二进制导致
// systemd 203/EXEC（2026-11-23 .48 日志）——执行位由安装器保证，不依赖
// 打包方是否记得 chmod。
func markExecutables(programDir, unitContent string) error {
	for _, raw := range strings.Split(unitContent, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "Exec") || strings.HasPrefix(line, "ExecReload") {
			continue // ExecReload 常为 systemctl reload 等系统命令，不属程序文件
		}
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		fields := strings.Fields(line[eq+1:])
		if len(fields) == 0 {
			continue
		}
		p := strings.TrimPrefix(fields[0], "-") // 前导 '-'（失败容忍标记）
		rel, err := filepath.Rel(programDir, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue // 包外路径：不归安装器管
		}
		if err := os.Chmod(p, 0755); err != nil {
			if os.IsNotExist(err) {
				continue // ExecStartPre 的 helper 可能不存在，让 systemd 报错
			}
			return fmt.Errorf("补执行位失败 %s: %w", p, err)
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

	updateInstalledAppVersion(reg.db, id, pkg.Manifest.Name, pkg.Manifest.Version, pkg.Manifest.Spec, pkg.Manifest.Healthz, statusInstalled)
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
	// ProvisionAppDirs 是幂等的（mkdir -p + chown 对已存在目录重复执行无
	// 副作用），升级时旧程序目录已被 Rename 挪到 backupDir，这里相当于
	// 重新创建一个空的程序目录（与原 MkdirAllChown(..., -1, -1) 语义一致，
	// 数据目录本就已存在，provision 脚本重复 mkdir+chown 不改变其内容）。
	if err := exec.ProvisionAppDirs(id); err != nil {
		return rollback(fmt.Errorf("创建新程序目录失败: %w", err))
	}
	if err := writeAppFiles(exec, programDir, newPkg.Files); err != nil {
		return rollback(fmt.Errorf("写入新程序文件失败: %w", err))
	}

	if _, _, err := exec.LookupUser(username); err != nil {
		return rollback(fmt.Errorf("查询应用用户失败: %w", err))
	}
	dataDir := appDataDir(id)
	for _, p := range newPkg.Manifest.Permissions.Storage {
		if _, err := common.SudoExec("mkdir", "-p", p); err != nil {
			return rollback(fmt.Errorf("创建 storage 路径 %s 失败: %w", p, err))
		}
		if err := exec.ChownApp(id, p); err != nil {
			return rollback(fmt.Errorf("设置 storage 路径 %s 属主失败: %w", p, err))
		}
	}

	extraEnv, err := provisionBaseServices(id, newPkg.Manifest.Permissions)
	if err != nil {
		return rollback(err)
	}

	readWritePaths := append([]string{programDir, dataDir}, newPkg.Manifest.Permissions.Storage...)
	unitContent := buildAppUnit(string(newPkg.Files["unit.tpl"]), old.Port, dataDir, old.Secret, username, readWritePaths, extraEnv)
	if err := exec.WriteUnitFile(old.Unit, unitContent); err != nil {
		return rollback(fmt.Errorf("写入新 unit 文件失败: %w", err))
	}
	// 升级路径同样补执行位（与 provisionApp 一致）
	if err := markExecutables(programDir, unitContent); err != nil {
		return rollback(fmt.Errorf("标记可执行文件失败: %w", err))
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

	// pending 态（install 上传/验签通过，但 confirm 未执行或执行前失败）
	// 尚未建用户/目录/unit，没有任何 provisionApp 副作用需要清理——只需
	// 删台账行、放回端口、清掉暂存的 tarball。此前 DELETE 对 pending 应用
	// 走的是下面已安装应用的完整清理路径，会卡在 StopUnit 等调用上
	// （unit 文件压根不存在），导致 pending 装失败后无法通过面板删除，
	// 用户只能手工改数据库——M1-6b 真机验收发现的死锁 bug。
	if a.Status == statusPending {
		os.Remove(stagedTarballPath(reg.dataDir, id))
		releasePort(reg.db, a.Port)
		deleteInstalledApp(reg.db, id)

		common.LogAuditRequest(r, auditCategory, "卸载应用", fmt.Sprintf("%s（pending 态清理）", id), "成功")
		common.EmitEvent("apps", common.EventInfo, "apps.deleted", map[string]interface{}{
			"id": id, "keepData": req.KeepData,
		}, "")

		common.JSONResponse(w, map[string]interface{}{"id": id, "deleted": true, "keepData": req.KeepData})
		return
	}

	exec := reg.executor
	exec.StopUnit(a.Unit)
	exec.DisableUnit(a.Unit)
	exec.RemoveAll(systemdUnitPath(a.Unit))
	exec.DaemonReload()
	// 数据目录属主是 nasapp-<id>，面板（fm）对其无 RemoveAll 权限，与
	// 程序目录（属主即面板自身 NAS_USER，面板可直接 RemoveAll）一并经
	// RemoveAppDirs 走 nas-app-provision.sh 收口脚本统一删除（M1-6b）。
	exec.RemoveAppDirs(id, req.KeepData)
	// manifest 声明的 storage 路径是用户数据区，卸载时永不删除——仅在详情页
	// 里标记（§3.1「数据目录…卸载按用户选择」只提及 dataDir，storage 路径
	// 属于池内用户数据，核心不得代为删除）。
	exec.DelAppUser(appSystemUser(id))

	// 基座服务资源（PostgreSQL database/role、Valkey db 编号、MQTT 账号+ACL）
	// 与 dataDir 同属"应用数据"范畴：keepData=true 时一并保留（应用可能
	// 重装后复用同一套凭据），仅 keepData=false 才清理。三个 Deprovision*
	// 对未申领过的能力是空操作，不需要先查 manifest 声明了哪些能力。
	if !req.KeepData {
		for _, derr := range deprovisionBaseServices(id) {
			common.LogAuditRequest(r, auditCategory, "卸载应用", fmt.Sprintf("%s: 基座服务资源清理失败: %v", id, derr), "失败")
		}
	}

	releasePort(reg.db, a.Port)
	deleteInstalledApp(reg.db, id)

	common.LogAuditRequest(r, auditCategory, "卸载应用", fmt.Sprintf("%s（保留数据: %v）", id, req.KeepData), "成功")
	common.EmitEvent("apps", common.EventInfo, "apps.deleted", map[string]interface{}{
		"id": id, "keepData": req.KeepData,
	}, "")

	common.JSONResponse(w, map[string]interface{}{"id": id, "deleted": true, "keepData": req.KeepData})
}
