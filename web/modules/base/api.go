// api.go 实现 M2 基座服务的对外 HTTP 接口：
//
//	GET  /api/base                     列出三组件当前状态
//	POST /api/base/<component>/install    apt 安装（装完默认停，不自启）
//	POST /api/base/<component>/uninstall  apt 卸载（purge 配置）
//	POST /api/base/<component>/enable     systemctl enable --now（一键启用）
//	POST /api/base/<component>/disable    systemctl disable --now（一键停用）
//
// 全部接口仅限管理员（common.AdminMiddleware），装/卸/启/停均幂等——
// 重复调用不应报错中止（§需求）。
package base

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"nas-panel/common"
)

// registry 聚合 base 模块的运行时状态：db 连接、执行器（与 apps.registry
// 同一接线模式）。
type registry struct {
	db       *sql.DB
	dataDir  string
	executor Executor
}

var reg *registry

// InitBase 打开 base.db 并初始化模块级单例，main.go 启动时调用一次。
func InitBase(dataDir string) error {
	db, err := openBaseDB(dataDir)
	if err != nil {
		return fmt.Errorf("打开 base.db 失败: %w", err)
	}
	reg = &registry{
		db:       db,
		dataDir:  dataDir,
		executor: NewDefaultExecutor(),
	}
	return nil
}

// RegisterRoutes 注册 base 模块全部路由（均需管理员会话），并启动后台
// 健康轮询器（与 apps.RegisterRoutes 同一接线模式）。
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/base", common.AdminMiddleware(handleBaseList))
	mux.HandleFunc("/api/base/", common.AdminMiddleware(handleBaseDispatch))
	startHealthPoller()
}

// RegisterRoutesBare 跳过 AdminMiddleware，只挂裸 handler，供单测直接发
// 请求（对齐 apps.RegisterRoutesBare）。
func RegisterRoutesBare(mux *http.ServeMux) {
	mux.HandleFunc("/api/base", handleBaseList)
	mux.HandleFunc("/api/base/", handleBaseDispatch)
}

func httpError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ─────────────────────────────────────────────────────────────
// GET /api/base
// ─────────────────────────────────────────────────────────────

// componentSummary 是单个基座组件的对外展示结构。
type componentSummary struct {
	Component   string `json:"component"` // pgsql | cache | mqtt
	Name        string `json:"name"`      // PostgreSQL | Valkey | Mosquitto
	Installed   bool   `json:"installed"`
	Enabled     bool   `json:"enabled"`
	UnitActive  string `json:"unitActive"`  // systemctl is-active 真实状态
	Health      string `json:"health"`      // running | unhealthy | stopped
	Description string `json:"description"` // 一句话用途说明
}

func componentDescription(c Component) string {
	switch c {
	case ComponentPostgres:
		return "关系型数据库，供托管应用按需申领独立 database/角色（M2）"
	case ComponentValkey:
		return "键值缓存，供托管应用按需分配独立 db 编号（0-15，M2）"
	case ComponentMosquitto:
		return "MQTT 消息代理，供托管应用按需申领独立账号与主题前缀（M2）"
	default:
		return ""
	}
}

func buildComponentSummary(executor Executor, st componentState) componentSummary {
	c := Component(st.Name)
	unit, _ := unitName(c)
	active := ""
	if unit != "" {
		active = executor.UnitActiveState(unit)
	}
	health := healthStopped
	if st.Installed || executor.PackageInstalled(c) {
		if hst, ok := getComponentHealthSnapshot(c); ok {
			health = hst.Status
		} else {
			health = healthRunning
		}
	}
	return componentSummary{
		Component:   st.Name,
		Name:        componentDisplayName(c),
		Installed:   st.Installed || executor.PackageInstalled(c),
		Enabled:     st.Enabled || strings.EqualFold(strings.TrimSpace(executor.UnitEnabledState(unit)), "enabled"),
		UnitActive:  active,
		Health:      health,
		Description: componentDescription(c),
	}
}

func handleBaseList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	states, err := listComponentStates(reg.db)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询台账失败: "+err.Error())
		return
	}
	summaries := make([]componentSummary, 0, len(states))
	for _, st := range states {
		summaries = append(summaries, buildComponentSummary(reg.executor, st))
	}
	common.JSONResponse(w, map[string]interface{}{"components": summaries})
}

// ─────────────────────────────────────────────────────────────
// POST /api/base/<component>/<action>
// ─────────────────────────────────────────────────────────────

func handleBaseDispatch(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/base/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		httpError(w, http.StatusNotFound, "路径格式应为 /api/base/<component>/<action>")
		return
	}
	component := Component(parts[0])
	action := parts[1]

	if !validComponents[component] {
		httpError(w, http.StatusNotFound, "未知组件: "+parts[0])
		return
	}
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	switch action {
	case "install":
		handleComponentInstall(w, r, component)
	case "uninstall":
		handleComponentUninstall(w, r, component)
	case "enable":
		handleComponentEnable(w, r, component)
	case "disable":
		handleComponentDisable(w, r, component)
	default:
		httpError(w, http.StatusNotFound, "未知操作: "+action)
	}
}

// handleComponentInstall 安装组件对应的 apt 包，装完默认停（不自启，
// §需求「面板一键启用」才 systemctl start）。幂等：已安装时跳过 apt 调用
// 直接回报当前状态。
func handleComponentInstall(w http.ResponseWriter, r *http.Request, c Component) {
	st, found, err := getComponentState(reg.db, c)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询组件状态失败: "+err.Error())
		return
	}
	if found && st.Installed {
		common.JSONResponse(w, map[string]interface{}{"message": componentDisplayName(c) + " 已安装", "installed": true})
		return
	}
	// 运行时对帐：setup.sh 基座段装的组件台账未记，但包在系统上——
	// 直接落库补记（apt-get install 幂等，重跑也是空操作）。
	if reg.executor.PackageInstalled(c) {
		if err := setComponentState(reg.db, c, true, st.Enabled); err != nil {
			httpError(w, http.StatusInternalServerError, "落库失败: "+err.Error())
			return
		}
		common.JSONResponse(w, map[string]interface{}{"message": componentDisplayName(c) + " 已安装", "installed": true})
		return
	}

	out, err := reg.executor.AptInstall(c)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "安装失败: "+out+": "+err.Error())
		return
	}
	if err := setComponentState(reg.db, c, true, false); err != nil {
		httpError(w, http.StatusInternalServerError, "落库失败: "+err.Error())
		return
	}

	common.LogAuditRequest(r, "BASE", "安装基座服务", componentDisplayName(c), "成功")
	common.JSONResponse(w, map[string]interface{}{
		"message":   componentDisplayName(c) + " 已安装（默认停止，需手动启用）",
		"installed": true,
	})
}

// handleComponentUninstall 卸载组件对应的 apt 包；幂等：未安装时直接回报。
func handleComponentUninstall(w http.ResponseWriter, r *http.Request, c Component) {
	st, found, err := getComponentState(reg.db, c)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询组件状态失败: "+err.Error())
		return
	}
	if !found || !st.Installed {
		common.JSONResponse(w, map[string]interface{}{"message": componentDisplayName(c) + " 未安装", "installed": false})
		return
	}

	unit, _ := unitName(c)
	if unit != "" {
		reg.executor.StopUnit(unit)
		reg.executor.DisableUnit(unit)
	}
	if err := setComponentState(reg.db, c, false, false); err != nil {
		httpError(w, http.StatusInternalServerError, "落库失败: "+err.Error())
		return
	}

	common.LogAuditRequest(r, "BASE", "卸载基座服务", componentDisplayName(c), "成功")
	common.JSONResponse(w, map[string]interface{}{
		"message":   componentDisplayName(c) + " 已卸载",
		"installed": false,
	})
}

// handleComponentEnable 一键启用：systemctl enable + start；幂等：
// 已启用时直接回报当前状态。要求组件已安装，否则 400。
func handleComponentEnable(w http.ResponseWriter, r *http.Request, c Component) {
	st, found, err := getComponentState(reg.db, c)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询组件状态失败: "+err.Error())
		return
	}
	if found && st.Installed {
		// 已有台账记录，直接走
	} else if reg.executor.PackageInstalled(c) {
		// 运行时对帐：包在系统上（setup.sh 装的），补记台账后继续启用。
		if err := setComponentState(reg.db, c, true, st.Enabled); err != nil {
			httpError(w, http.StatusInternalServerError, "落库失败: "+err.Error())
			return
		}
	} else {
		httpError(w, http.StatusBadRequest, componentDisplayName(c)+" 尚未安装，请先安装")
		return
	}

	unit, err := unitName(c)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := reg.executor.EnableUnit(unit); err != nil {
		httpError(w, http.StatusInternalServerError, "启用失败: "+err.Error())
		return
	}
	if _, err := reg.executor.StartUnit(unit); err != nil {
		httpError(w, http.StatusInternalServerError, "启动失败: "+err.Error())
		return
	}
	if err := setComponentState(reg.db, c, true, true); err != nil {
		httpError(w, http.StatusInternalServerError, "落库失败: "+err.Error())
		return
	}

	common.LogAuditRequest(r, "BASE", "启用基座服务", componentDisplayName(c), "成功")
	common.JSONResponse(w, map[string]interface{}{
		"message": componentDisplayName(c) + " 已启用",
		"enabled": true,
	})
}

// handleComponentDisable 一键停用：systemctl stop + disable；幂等。
func handleComponentDisable(w http.ResponseWriter, r *http.Request, c Component) {
	st, found, err := getComponentState(reg.db, c)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "查询组件状态失败: "+err.Error())
		return
	}
	if !found || !st.Installed {
		httpError(w, http.StatusBadRequest, componentDisplayName(c)+" 尚未安装")
		return
	}

	unit, err := unitName(c)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	reg.executor.StopUnit(unit)
	reg.executor.DisableUnit(unit)
	if err := setComponentState(reg.db, c, true, false); err != nil {
		httpError(w, http.StatusInternalServerError, "落库失败: "+err.Error())
		return
	}

	common.LogAuditRequest(r, "BASE", "停用基座服务", componentDisplayName(c), "成功")
	common.JSONResponse(w, map[string]interface{}{
		"message": componentDisplayName(c) + " 已停用",
		"enabled": false,
	})
}

// ─────────────────────────────────────────────────────────────
// 供 apps 模块消费的导出接口
// ─────────────────────────────────────────────────────────────

// Ready 报告基座组件是否已安装且已启用（apps 模块声明 pgsql/cache/mqtt
// 能力时据此判定能否放行安装；只有 Installed&&Enabled 才算真正"就绪"——
// 仅装未启用的组件尚不能服务应用，§需求"装完默认停，一键启用"）。
func Ready(c Component) bool {
	if reg == nil {
		return false
	}
	st, found, err := getComponentState(reg.db, c)
	if err != nil || !found {
		return false
	}
	return st.Installed && st.Enabled
}

// ComponentStateDescription 返回组件当前状态的人话描述，供 apps 模块拼
// 「未就绪」错误信息时直接复用（§需求「错误信息必须指名具体未就绪/未安装
// 的组件」）。
func ComponentStateDescription(c Component) string {
	name := componentDisplayNameExported(c)
	if reg == nil {
		return name + " 基座服务未初始化"
	}
	st, found, err := getComponentState(reg.db, c)
	if err != nil || !found || !st.Installed {
		return name + " 基座服务未安装"
	}
	if !st.Enabled {
		return name + " 基座服务已安装但未启用"
	}
	return name + " 基座服务就绪"
}

// componentDisplayNameExported 是 componentDisplayName 的导出包装（保持
// 内部健康轮询代码与对外错误文案共用同一份名称映射，不重复维护两套）。
func componentDisplayNameExported(c Component) string {
	return componentDisplayName(c)
}

// ─────────────────────────────────────────────────────────────
// 测试支持（仅供本仓库其它模块的单测调用，不在生产路径使用）
// ─────────────────────────────────────────────────────────────

// InitBaseForTest 用给定 executor 初始化模块级单例并指向一个临时
// base.db（t.TempDir()），供 apps 模块等消费方的单测驱动 Ready()/
// ComponentStateDescription() 返回可控结果，不需要真的拉起 PostgreSQL/
// Valkey/Mosquitto（与 common.InitEventBusForTest/vault.ResetForTest
// 同一「生产文件里放 ForTest 导出函数」约定）。返回值供 defer 还原
// 旧单例，避免测试间互相污染全局状态。
func InitBaseForTest(dataDir string, executor Executor) (prevReg *registry, err error) {
	db, err := openBaseDB(dataDir)
	if err != nil {
		return nil, fmt.Errorf("打开 base.db 失败: %w", err)
	}
	prevReg = reg
	reg = &registry{db: db, dataDir: dataDir, executor: executor}
	return prevReg, nil
}

// RestoreBaseForTest 还原 InitBaseForTest 替换前的单例（典型用法：
// `defer base.RestoreBaseForTest(prev)`）。
func RestoreBaseForTest(prevReg *registry) {
	reg = prevReg
}

// SetComponentStateForTest 直接落库某组件的安装/启用状态，供单测快速
// 构造「pgsql 就绪但 mqtt 未安装」之类的组合场景，不必真的调用
// AptInstall/EnableUnit 走一遍完整装机流程。
func SetComponentStateForTest(c Component, installed, enabled bool) error {
	return setComponentState(reg.db, c, installed, enabled)
}
