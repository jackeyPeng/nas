package apps

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"nas-panel/common"
)

// installedApp 对应 installed 表一行：已安装应用的台账记录。
// Status 是落库的最后已知状态（install/confirm/start/stop 等操作后写入），
// 不是运行态的权威来源——API 层展示状态必须另行调用
// Executor.UnitActiveState 查真实 systemd 状态（见 lifecycle.go 的
// buildAppDetail/buildAppSummary），Status 只用于「从未启动过」等
// 台账语义（如区分 pending confirm 与已 confirm 过的应用）。
type installedApp struct {
	ID          string
	Name        string
	Version     string
	Spec        string
	Port        int
	Unit        string
	Secret      string
	Healthz     string
	Status      string
	InstalledAt string
	UpdatedAt   string
}

// 台账状态机取值：pending（install 已解包验签，待 confirm）、
// installed（confirm 成功，服务已启动过）、stopped（手动 stop 后的台账值，
// 仅用于展示 hint，真实状态仍查 systemctl）。
const (
	statusPending   = "pending"
	statusInstalled = "installed"
	statusStopped   = "stopped"
)

// openAppsDB 打开 <dataDir>/apps.db（WAL，common.OpenSQLite 内置）并建表。
func openAppsDB(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	db, err := common.OpenSQLite(filepath.Join(dataDir, "apps.db"))
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS installed (
			id           TEXT PRIMARY KEY,
			name         TEXT NOT NULL,
			version      TEXT NOT NULL,
			spec         TEXT NOT NULL,
			port         INTEGER NOT NULL,
			unit         TEXT NOT NULL,
			secret       TEXT NOT NULL DEFAULT '',
			status       TEXT NOT NULL,
			installed_at TEXT NOT NULL,
			updated_at   TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS port_alloc (
			port INTEGER PRIMARY KEY
		);
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("建表失败: %w", err)
	}
	// secret/healthz 列是后补的（历史库可能没有）：忽略"duplicate column"
	// 错误，兼容已存在未带该列的 installed 表（防御性迁移，当前 M1-2/M1-4
	// 均是新建库场景，此分支在全新安装下不会触发，仅为未来演进留口）。
	db.Exec(`ALTER TABLE installed ADD COLUMN secret TEXT NOT NULL DEFAULT ''`)
	// healthz 是 M1-4 新增：持久化 manifest.Healthz 路径供后台健康轮询器
	// 使用（轮询器不持有 pkg.Manifest，只能从台账读取）。
	db.Exec(`ALTER TABLE installed ADD COLUMN healthz TEXT NOT NULL DEFAULT ''`)
	return db, nil
}

// getInstalledApp 按 id 查台账记录；不存在返回 (nil, nil)。
func getInstalledApp(db *sql.DB, id string) (*installedApp, error) {
	row := db.QueryRow(`SELECT id, name, version, spec, port, unit, secret, healthz, status, installed_at, updated_at
		FROM installed WHERE id = ?`, id)
	var a installedApp
	err := row.Scan(&a.ID, &a.Name, &a.Version, &a.Spec, &a.Port, &a.Unit, &a.Secret, &a.Healthz, &a.Status, &a.InstalledAt, &a.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// listInstalledApps 返回全部已安装应用（含 pending），按 id 排序。
func listInstalledApps(db *sql.DB) ([]installedApp, error) {
	rows, err := db.Query(`SELECT id, name, version, spec, port, unit, secret, healthz, status, installed_at, updated_at
		FROM installed ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []installedApp
	for rows.Next() {
		var a installedApp
		if err := rows.Scan(&a.ID, &a.Name, &a.Version, &a.Spec, &a.Port, &a.Unit, &a.Secret, &a.Healthz, &a.Status, &a.InstalledAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// insertInstalledApp 插入一条新台账记录（install/confirm 流程首次落库用）。
func insertInstalledApp(db *sql.DB, a installedApp) error {
	now := time.Now().Format(time.RFC3339)
	a.InstalledAt = now
	a.UpdatedAt = now
	_, err := db.Exec(`INSERT INTO installed (id, name, version, spec, port, unit, secret, healthz, status, installed_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.Version, a.Spec, a.Port, a.Unit, a.Secret, a.Healthz, a.Status, a.InstalledAt, a.UpdatedAt)
	return err
}

// updateInstalledAppStatus 更新台账状态位（start/stop/restart/upgrade 后调用）。
func updateInstalledAppStatus(db *sql.DB, id, status string) error {
	_, err := db.Exec(`UPDATE installed SET status = ?, updated_at = ? WHERE id = ?`,
		status, time.Now().Format(time.RFC3339), id)
	return err
}

// updateInstalledAppVersion 升级成功后更新版本号与状态（保留原 id/port/unit/secret）。
// healthz 一并更新：新版本 manifest 可能变更了健康检查路径，轮询器必须跟随。
func updateInstalledAppVersion(db *sql.DB, id, name, version, spec, healthz, status string) error {
	_, err := db.Exec(`UPDATE installed SET name = ?, version = ?, spec = ?, healthz = ?, status = ?, updated_at = ? WHERE id = ?`,
		name, version, spec, healthz, status, time.Now().Format(time.RFC3339), id)
	return err
}

// deleteInstalledApp 删除台账记录（卸载流程用）。
func deleteInstalledApp(db *sql.DB, id string) error {
	_, err := db.Exec(`DELETE FROM installed WHERE id = ?`, id)
	return err
}

// allocatePort 在 [minPort, maxPort] 内分配一个端口：
//  1. 优先尝试 manifest 申请的端口（requestedPort），落在合法范围内且未被
//     table 记录占用、且真实 bind 探测成功时直接采用（§3.4「应用申请端口，
//     核心在 18000-18999 段核派」）；
//  2. 冲突（table 已占用，或 bind 探测失败——真实世界已有进程监听但本地
//     台账不知道）则在整段范围内顺序寻找下一个同时满足两个条件的端口
//     （核心有权改派，见 §3.4）。
//
// 表记录 + 真实 bind 探测双重校验，避免只认表项导致的两类脏读：
//   - 表项存在但进程早已不在监听（表项是权威分配记录，不能被探测结果误删，
//     这里只用探测防新分配冲突，不反向清理表）
//   - 表项不存在但端口被系统其它进程占用（纯表查询会误判为空闲）
//
// 调用方必须在同一事务/锁下插入 port_alloc 记录，防止并发分配出同一端口
// （见 lifecycle.go 的 install handler：allocatePort 与 INSERT INTO port_alloc
// 在同一次 db 调用序列内完成，installMu 包裹整个两步安装的第一步）。
func allocatePort(db *sql.DB, exec Executor, requestedPort int) (int, error) {
	try := func(port int) (bool, error) {
		if port < minPort || port > maxPort {
			return false, nil
		}
		var exists int
		if err := db.QueryRow(`SELECT COUNT(*) FROM port_alloc WHERE port = ?`, port).Scan(&exists); err != nil {
			return false, err
		}
		if exists > 0 {
			return false, nil
		}
		if !exec.ProbePort(port) {
			return false, nil
		}
		return true, nil
	}

	if ok, err := try(requestedPort); err != nil {
		return 0, err
	} else if ok {
		return requestedPort, nil
	}

	for port := minPort; port <= maxPort; port++ {
		if port == requestedPort {
			continue
		}
		ok, err := try(port)
		if err != nil {
			return 0, err
		}
		if ok {
			return port, nil
		}
	}
	return 0, fmt.Errorf("端口段 %d-%d 已无可用端口", minPort, maxPort)
}

// reservePort 把端口写入 port_alloc 表（分配成功后立即占位，防止同一批
// 并发安装请求重复分配到 allocatePort 刚探测通过、尚未写台账的端口）。
func reservePort(db *sql.DB, port int) error {
	_, err := db.Exec(`INSERT INTO port_alloc (port) VALUES (?)`, port)
	return err
}

// releasePort 释放端口占位（卸载/升级改派失败回滚时调用）。
func releasePort(db *sql.DB, port int) error {
	_, err := db.Exec(`DELETE FROM port_alloc WHERE port = ?`, port)
	return err
}

// secretByteLen 是 __NAS_SECRET__ 的原始字节数（32 字节 = 256 位，十六进制
// 编码后 64 字符），供 §5.3 HMAC-SHA256 验签密钥使用。
const secretByteLen = 32

// generateAppSecret 生成每应用专属的随机密钥（hex 编码），注入 unit 的
// __NAS_SECRET__（§5.3）。用 crypto/rand，不用 math/rand（必须密码学安全）。
func generateAppSecret() (string, error) {
	buf := make([]byte, secretByteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成应用密钥失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// appUnitName 返回固定格式的 systemd 服务名（§3.2「服务名固定 nas-app-<id>.service」）。
func appUnitName(id string) string {
	return "nas-app-" + id + ".service"
}

// appSystemUser 返回固定格式的系统用户名（§3.1「nasapp-<id>」）。
func appSystemUser(id string) string {
	return "nasapp-" + id
}

// appProgramDir 返回程序目录（§3.1，应用用户只读，升级整体替换）。
func appProgramDir(id string) string {
	return filepath.Join(appsBaseDir, id)
}

// appDataDir 返回数据目录（§3.1，应用用户可写，升级保留、卸载按用户选择）。
func appDataDir(id string) string {
	return filepath.Join(appsDataBaseDir, id)
}

const (
	appsBaseDir     = "/var/lib/nas/apps"
	appsDataBaseDir = "/var/lib/nas/apps-data"
)

// randomSuffix 生成一个用于备份目录名的短随机数字串，避免同秒内两次升级
// 的 .bak-<ts> 目录名冲突（时间戳精度仅到秒）。
func randomSuffix() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "0"
	}
	return n.String()
}
