package base

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"nas-panel/common"
)

// AllComponents 是基座三组件的枚举顺序，db.go 的 listComponentStates 用它
// 补齐从未落库过的组件记录，api.go 的 GET /api/base 用它保证恒返回三条。
var AllComponents = []Component{ComponentPostgres, ComponentValkey, ComponentMosquitto}

// baseOpsScript 是 nas-base-ops.sh 收口脚本的绝对路径（与 apps.appProvisionScript
// 同一收口模式：整脚本进 sudoers 白名单，脚本内部做身份校验后才拼 SQL/改配置，
// 面板侧永不拼接任意 SQL 片段）。
const baseOpsScript = "/opt/nas/scripts/nas-base-ops.sh"

// Executor 聚合 base 模块对外部世界的全部副作用调用（apt 安装、systemctl、
// PostgreSQL/Mosquitto 的建号建库/删号删库、健康探测）。生产代码恒用
// defaultExecutor；单测注入 fakeExecutor，断言调用方传给 common.SudoExec 的
// 参数序列与 sudoers 白名单形状逐字匹配，同时避免 CI（无 root）里真的跑
// apt-get/systemctl/psql。
//
// 方法形状直接对应 scripts/setup.sh 新增的 sudoers 白名单条目；新增/修改
// 任一方法的参数形状都必须同步检查白名单是否仍覆盖。
type Executor interface {
	// AptInstall 安装组件对应的 apt 包（postgresql 元包 / valkey-server /
	// mosquitto+mosquitto-clients），幂等（apt-get install 对已安装包是空操作）。
	AptInstall(component Component) (string, error)

	// EnableUnit 执行 systemctl enable <unit>。
	EnableUnit(unit string) (string, error)
	// DisableUnit 执行 systemctl disable <unit>。
	DisableUnit(unit string) (string, error)
	// StartUnit 执行 systemctl start <unit>。
	StartUnit(unit string) (string, error)
	// StopUnit 执行 systemctl stop <unit>。
	StopUnit(unit string) (string, error)
	// UnitActiveState 返回 systemctl is-active <unit> 的真实状态字符串。
	UnitActiveState(unit string) string

	// WriteConfigFile 把渲染好的配置文件内容落盘（Valkey/Mosquitto 的
	// /etc/valkey/valkey.conf、/etc/mosquitto/conf.d/nas-panel.conf 等）。
	WriteConfigFile(path, content string) error

	// PgCreateDB 创建 database+role（幂等：已存在跳过创建，但恒执行一次
	// GRANT，对应 nas-base-ops.sh 的 pg-create-db 子命令）。
	PgCreateDB(dbName, roleName string) (string, error)
	// PgSetPassword 通过 stdin 设置角色登录密码（从不经 argv/日志），
	// 对应 nas-base-ops.sh 的 pg-set-password 子命令。
	PgSetPassword(roleName, password string) (string, error)
	// PgDropDB 删除 database+role（卸载清理，幂等——不存在不报错），
	// 对应 nas-base-ops.sh 的 pg-drop-db 子命令。
	PgDropDB(dbName, roleName string) (string, error)
	// PgIsReady 探测 PostgreSQL 是否已就绪（pg_isready），用于 health.go 轮询。
	PgIsReady() bool

	// MqttSetPassword 通过 stdin 设置 Mosquitto 用户密码（mosquitto_passwd），
	// 对应 nas-base-ops.sh 的 mqtt-set-password 子命令。
	MqttSetPassword(username, password string) (string, error)
	// MqttDeleteUser 删除 Mosquitto 用户，对应 mqtt-delete-user 子命令。
	MqttDeleteUser(username string) (string, error)
	// MqttAclAdd 为用户追加/覆盖 ACL 段（该用户可读写 topicPrefix 下全部主题），
	// 对应 mqtt-acl-add 子命令。
	MqttAclAdd(username, topicPrefix string) (string, error)
	// MqttAclRemove 删除用户的 ACL 段，对应 mqtt-acl-remove 子命令。
	MqttAclRemove(username string) (string, error)
	// MqttPing 探测 Mosquitto 是否已就绪（供 health.go 轮询；当前以
	// systemctl is-active 近似替代真实 pub/sub 探测，避免健康轮询本身
	// 在 broker 上产生持续订阅连接噪音）。
	MqttPing() bool

	// ValkeyPing 探测 Valkey 是否已就绪（valkey-cli -h 127.0.0.1 ping）。
	ValkeyPing() bool
}

// defaultExecutor 是生产环境实现：真实调用 common.SudoExec/systemctl/
// nas-base-ops.sh。
type defaultExecutor struct{}

// NewDefaultExecutor 返回生产用的真实 Executor。
func NewDefaultExecutor() Executor {
	return defaultExecutor{}
}

func (defaultExecutor) AptInstall(component Component) (string, error) {
	pkgs, err := aptPackages(component)
	if err != nil {
		return "", err
	}
	args := append([]string{"install", "-y", "-qq"}, pkgs...)
	return common.SudoExec("apt-get", args...)
}

// aptPackages 返回组件对应的 apt 包名列表。
func aptPackages(component Component) ([]string, error) {
	switch component {
	case ComponentPostgres:
		// Debian trixie 官方源提供 postgresql-17（16 不在源内）；跟随发行版
		// 大版本，不硬编码过时包名（设计文档 PG16 → 实际 PG17 同为官方源）。
		return []string{"postgresql"}, nil
	case ComponentValkey:
		return []string{"valkey-server"}, nil
	case ComponentMosquitto:
		return []string{"mosquitto", "mosquitto-clients"}, nil
	default:
		return nil, fmt.Errorf("未知组件: %s", component)
	}
}

// unitName 返回组件对应的 systemd unit 名称。
func unitName(component Component) (string, error) {
	switch component {
	case ComponentPostgres:
		return "postgresql", nil
	case ComponentValkey:
		return "valkey-server", nil
	case ComponentMosquitto:
		return "mosquitto", nil
	default:
		return "", fmt.Errorf("未知组件: %s", component)
	}
}

func (defaultExecutor) EnableUnit(unit string) (string, error) {
	return common.SudoExec("systemctl", "enable", unit)
}

func (defaultExecutor) DisableUnit(unit string) (string, error) {
	return common.SudoExec("systemctl", "disable", unit)
}

func (defaultExecutor) StartUnit(unit string) (string, error) {
	return common.SudoExec("systemctl", "start", unit)
}

func (defaultExecutor) StopUnit(unit string) (string, error) {
	return common.SudoExec("systemctl", "stop", unit)
}

func (defaultExecutor) UnitActiveState(unit string) string {
	out, _ := common.ExecOutput("systemctl", "is-active", unit)
	return strings.TrimSpace(out)
}

func (defaultExecutor) WriteConfigFile(path, content string) error {
	return common.SafeWriteFile(path, content)
}

func (defaultExecutor) PgCreateDB(dbName, roleName string) (string, error) {
	return common.SudoExec(baseOpsScript, "pg-create-db", dbName, roleName)
}

func (defaultExecutor) PgSetPassword(roleName, password string) (string, error) {
	return common.SudoExecStdin(password, baseOpsScript, "pg-set-password", roleName)
}

func (defaultExecutor) PgDropDB(dbName, roleName string) (string, error) {
	return common.SudoExec(baseOpsScript, "pg-drop-db", dbName, roleName)
}

func (defaultExecutor) PgIsReady() bool {
	_, err := common.SudoExec(baseOpsScript, "pg-isready")
	return err == nil
}

func (defaultExecutor) MqttSetPassword(username, password string) (string, error) {
	return common.SudoExecStdin(password, baseOpsScript, "mqtt-set-password", username)
}

func (defaultExecutor) MqttDeleteUser(username string) (string, error) {
	return common.SudoExec(baseOpsScript, "mqtt-delete-user", username)
}

func (defaultExecutor) MqttAclAdd(username, topicPrefix string) (string, error) {
	return common.SudoExec(baseOpsScript, "mqtt-acl-add", username, topicPrefix)
}

func (defaultExecutor) MqttAclRemove(username string) (string, error) {
	return common.SudoExec(baseOpsScript, "mqtt-acl-remove", username)
}

func (defaultExecutor) MqttPing() bool {
	return defaultExecutor{}.UnitActiveState("mosquitto") == "active"
}

func (defaultExecutor) ValkeyPing() bool {
	out, err := common.ExecOutput("valkey-cli", "-h", "127.0.0.1", "-p", "6379", "ping")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "PONG"
}

// ─────────────────────────────────────────────────────────────
// 纯 Go 配置文件构建（无 heredoc；与 services/services.go 的
// buildRcloneWebdavUnit 同一模式）
// ─────────────────────────────────────────────────────────────

// valkeyConfigPath 是面板纳管的 Valkey 配置文件路径（独立于 apt 包自带的
// 默认 /etc/valkey/valkey.conf，避免升级包时被 dpkg conffile 提示覆盖；
// 通过 systemd unit override 或包自带 conf.d include 机制生效——具体生效
// 方式见 setup.sh 的安装步骤，本函数只负责内容构建）。
const valkeyConfigPath = "/etc/valkey/valkey.conf"

// buildValkeyConfig 生成绑定 127.0.0.1:6379、禁止外部访问的最小 Valkey 配置。
func buildValkeyConfig() string {
	return "bind 127.0.0.1 -::1\nport 6379\nprotected-mode yes\nsupervised systemd\ndaemonize no\n"
}

// mosquittoConfigPath 是面板纳管的 Mosquitto 配置片段路径（conf_dir include，
// 不改写 /etc/mosquitto/mosquitto.conf 本体）。
const mosquittoConfigPath = "/etc/mosquitto/conf.d/nas-panel.conf"

// buildMosquittoConfig 生成绑定 127.0.0.1:1883、启用密码文件+ACL 文件的最小
// Mosquitto 配置片段；password_file/acl_file 路径必须与 nas-base-ops.sh 的
// MOSQUITTO_PASSWD_FILE/MOSQUITTO_ACL_FILE 常量逐字一致。
func buildMosquittoConfig() string {
	return "listener 1883 127.0.0.1\nallow_anonymous false\npassword_file /etc/mosquitto/nas-passwd\nacl_file /etc/mosquitto/nas-acl.conf\n"
}

// secretByteLen 是账号密码的原始字节数（与 apps.generateAppSecret 同一量级），
// 十六进制编码后 64 字符，纯 [0-9a-f]——恒安全嵌入 psql -c 的 SQL 字面量/
// mosquitto_passwd 参数，也省去再转义的必要。
const secretByteLen = 32

// generateSecret 生成随机密码（hex 编码），供 PostgreSQL/MQTT 账号密码使用。
// 用 crypto/rand，不用 math/rand（必须密码学安全）。
func generateSecret() (string, error) {
	buf := make([]byte, secretByteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成密码失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
