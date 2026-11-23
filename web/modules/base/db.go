// Package base 实现 M2 基座服务（一）：PostgreSQL 16 / Valkey 8 / Mosquitto
// 的纳管（安装/卸载/就绪探测/健康轮询），供 apps 模块声明的 pgsql/cache/mqtt
// 能力在生命周期（provisionApp/upgradeApp/handleAppDelete）里消费
// （docs/plans/2026-11-20-app-platform-design.md §七）。
//
// 本文件负责 base.db：三类状态持久化——
//  1. component 表：三个基座组件（pgsql/valkey/mosquitto）的安装/启用状态；
//  2. valkey_alloc 表：每应用分配的 Valkey db number（0-15，§5.5 单实例多 db 隔离）；
//  3. pgsql_db 表：每应用在 PostgreSQL 里创建的 database/role 台账（卸载时据此清理）。
package base

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"nas-panel/common"
)

// Component 是基座服务三选一的标识符，贯穿本模块与 apps 模块的接口。
type Component string

const (
	ComponentPostgres  Component = "pgsql"
	ComponentValkey    Component = "cache"
	ComponentMosquitto Component = "mqtt"
)

// validComponents 是合法 Component 取值集合，供入参校验用。
var validComponents = map[Component]bool{
	ComponentPostgres:  true,
	ComponentValkey:    true,
	ComponentMosquitto: true,
}

// componentState 对应 component 表一行：某基座组件的纳管状态。
// Installed 表示 apt 包已装；Enabled 表示 systemd 服务已 enable+start
// （setup.sh 默认装完即停，面板一键启用才置真，§需求"安装+停止"默认态）。
type componentState struct {
	Name      string
	Installed bool
	Enabled   bool
	UpdatedAt string
}

// openBaseDB 打开 <dataDir>/base.db（WAL，common.OpenSQLite 内置）并建表。
func openBaseDB(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	db, err := common.OpenSQLite(filepath.Join(dataDir, "base.db"))
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS component (
			name       TEXT PRIMARY KEY,
			installed  INTEGER NOT NULL DEFAULT 0,
			enabled    INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS valkey_alloc (
			app_id  TEXT PRIMARY KEY,
			db_num  INTEGER NOT NULL UNIQUE
		);
		CREATE TABLE IF NOT EXISTS pgsql_db (
			app_id    TEXT PRIMARY KEY,
			db_name   TEXT NOT NULL,
			role_name TEXT NOT NULL,
			password  TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS mqtt_account (
			app_id       TEXT PRIMARY KEY,
			username     TEXT NOT NULL,
			password     TEXT NOT NULL,
			topic_prefix TEXT NOT NULL
		);
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("建表失败: %w", err)
	}
	return db, nil
}

// getComponentState 查询单个组件的纳管状态；从未落库过（从未调用过
// setComponentState）时返回 (zero, false)。
func getComponentState(db *sql.DB, name Component) (componentState, bool, error) {
	row := db.QueryRow(`SELECT name, installed, enabled, updated_at FROM component WHERE name = ?`, string(name))
	var st componentState
	var installed, enabled int
	err := row.Scan(&st.Name, &installed, &enabled, &st.UpdatedAt)
	if err == sql.ErrNoRows {
		return componentState{}, false, nil
	}
	if err != nil {
		return componentState{}, false, err
	}
	st.Installed = installed != 0
	st.Enabled = enabled != 0
	return st, true, nil
}

// listComponentStates 返回全部三个组件的当前状态（未落库过的以全零值补齐，
// 保证 API 层恒返回三条记录，不因"从未安装"而缺项）。
func listComponentStates(db *sql.DB) ([]componentState, error) {
	rows, err := db.Query(`SELECT name, installed, enabled, updated_at FROM component`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	known := map[string]componentState{}
	for rows.Next() {
		var st componentState
		var installed, enabled int
		if err := rows.Scan(&st.Name, &installed, &enabled, &st.UpdatedAt); err != nil {
			return nil, err
		}
		st.Installed = installed != 0
		st.Enabled = enabled != 0
		known[st.Name] = st
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]componentState, 0, len(AllComponents))
	for _, c := range AllComponents {
		if st, ok := known[string(c)]; ok {
			out = append(out, st)
		} else {
			out = append(out, componentState{Name: string(c)})
		}
	}
	return out, nil
}

// setComponentState upsert 组件状态（装/卸/启/停都经这一个函数落库）。
func setComponentState(db *sql.DB, name Component, installed, enabled bool) error {
	_, err := db.Exec(`
		INSERT INTO component (name, installed, enabled, updated_at) VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(name) DO UPDATE SET installed = excluded.installed, enabled = excluded.enabled, updated_at = excluded.updated_at
	`, string(name), boolToInt(installed), boolToInt(enabled))
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ─────────────────────────────────────────────────────────────
// Valkey db number 分配（0-15，§5.5：单实例多 db 隔离，每应用固定一个）
// ─────────────────────────────────────────────────────────────

const (
	valkeyMinDB = 0
	valkeyMaxDB = 15
)

// allocateValkeyDB 为 appID 分配一个未占用的 db number；已分配过则直接
// 返回既有分配（幂等，供 provisionApp 重试/upgradeApp 复用同一个 db）。
func allocateValkeyDB(db *sql.DB, appID string) (int, error) {
	var existing int
	err := db.QueryRow(`SELECT db_num FROM valkey_alloc WHERE app_id = ?`, appID).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}

	used := map[int]bool{}
	rows, err := db.Query(`SELECT db_num FROM valkey_alloc`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return 0, err
		}
		used[n] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for n := valkeyMinDB; n <= valkeyMaxDB; n++ {
		if !used[n] {
			if _, err := db.Exec(`INSERT INTO valkey_alloc (app_id, db_num) VALUES (?, ?)`, appID, n); err != nil {
				return 0, err
			}
			return n, nil
		}
	}
	return 0, fmt.Errorf("Valkey db 编号已耗尽（0-15 全部占用）")
}

// releaseValkeyDB 回收 appID 的 db number 分配（卸载流程调用）。
func releaseValkeyDB(db *sql.DB, appID string) error {
	_, err := db.Exec(`DELETE FROM valkey_alloc WHERE app_id = ?`, appID)
	return err
}

// getValkeyDB 查询 appID 已分配的 db number；未分配返回 (0, false)。
func getValkeyDB(db *sql.DB, appID string) (int, bool, error) {
	var n int
	err := db.QueryRow(`SELECT db_num FROM valkey_alloc WHERE app_id = ?`, appID).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return n, true, nil
}

// ─────────────────────────────────────────────────────────────
// PostgreSQL database/role 台账
// ─────────────────────────────────────────────────────────────

type pgsqlDBRecord struct {
	AppID    string
	DBName   string
	RoleName string
	Password string
}

func insertPgsqlDB(db *sql.DB, rec pgsqlDBRecord) error {
	_, err := db.Exec(`INSERT INTO pgsql_db (app_id, db_name, role_name, password) VALUES (?, ?, ?, ?)`,
		rec.AppID, rec.DBName, rec.RoleName, rec.Password)
	return err
}

func getPgsqlDB(db *sql.DB, appID string) (*pgsqlDBRecord, error) {
	row := db.QueryRow(`SELECT app_id, db_name, role_name, password FROM pgsql_db WHERE app_id = ?`, appID)
	var rec pgsqlDBRecord
	err := row.Scan(&rec.AppID, &rec.DBName, &rec.RoleName, &rec.Password)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func deletePgsqlDB(db *sql.DB, appID string) error {
	_, err := db.Exec(`DELETE FROM pgsql_db WHERE app_id = ?`, appID)
	return err
}

// ─────────────────────────────────────────────────────────────
// Mosquitto 账号台账
// ─────────────────────────────────────────────────────────────

type mqttAccountRecord struct {
	AppID       string
	Username    string
	Password    string
	TopicPrefix string
}

func insertMQTTAccount(db *sql.DB, rec mqttAccountRecord) error {
	_, err := db.Exec(`INSERT INTO mqtt_account (app_id, username, password, topic_prefix) VALUES (?, ?, ?, ?)`,
		rec.AppID, rec.Username, rec.Password, rec.TopicPrefix)
	return err
}

func getMQTTAccount(db *sql.DB, appID string) (*mqttAccountRecord, error) {
	row := db.QueryRow(`SELECT app_id, username, password, topic_prefix FROM mqtt_account WHERE app_id = ?`, appID)
	var rec mqttAccountRecord
	err := row.Scan(&rec.AppID, &rec.Username, &rec.Password, &rec.TopicPrefix)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func deleteMQTTAccount(db *sql.DB, appID string) error {
	_, err := db.Exec(`DELETE FROM mqtt_account WHERE app_id = ?`, appID)
	return err
}

// appIDPattern 复刻 apps.idPattern（[a-z0-9-]{2,24}），在 base 包边界再
// 校验一次 appID（防御性：即便未来某个调用方跳过 apps 模块的 manifest
// 校验直接调用 Provision*，也不会有非法字符流入 nas-base-ops.sh 的参数——
// 红线：SQL/shell 拼接面绝不接受未经校验的任意字符）。
var appIDPattern = regexp.MustCompile(`^[a-z0-9-]{2,24}$`)

// validateAppID 校验 appID 是否匹配 apps.idPattern 的形状；不匹配时返回
// 错误，调用方（Provision*/Deprovision*）必须在生成任何标识符/执行任何
// 副作用前先过这一关。
func validateAppID(appID string) error {
	if !appIDPattern.MatchString(appID) {
		return fmt.Errorf("非法应用 id: %q（必须匹配 [a-z0-9-]{2,24}）", appID)
	}
	return nil
}

// appPgsqlNames 返回固定形状的数据库名/角色名（§需求"nasapp_<id>"，数据库名
// 与角色名相同，避免再引入一套命名映射）。调用前应已经过 validateAppID；
// Postgres 标识符不允许连字符，这里把 "-" 替换为 "_"，同时固定前缀防止与
// 手工创建的数据库撞名。
func appPgsqlNames(appID string) (dbName, roleName string) {
	safe := sanitizePgIdentifier(appID)
	name := "nasapp_" + safe
	return name, name
}

func sanitizePgIdentifier(id string) string {
	out := make([]rune, 0, len(id))
	for _, r := range id {
		if r == '-' {
			out = append(out, '_')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// appMQTTUsername 返回固定形状的 Mosquitto 用户名（与应用 id 一一对应）。
func appMQTTUsername(appID string) string {
	return "nasapp_" + sanitizePgIdentifier(appID)
}

// appMQTTTopicPrefix 返回固定形状的 MQTT 主题前缀（§需求 "nas/apps/<id>/"）。
func appMQTTTopicPrefix(appID string) string {
	return "nas/apps/" + appID + "/"
}
