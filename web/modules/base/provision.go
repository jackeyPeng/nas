// provision.go 导出 apps 模块在 provisionApp/upgradeApp/handleAppDelete
// 消费基座服务能力所需的全部函数：创建/复用 PostgreSQL database+role、
// 分配/复用 Valkey db 编号、创建/复用 Mosquitto 账号+ACL，以及对应的
// 卸载清理（drop db/role、回收 db 编号、删 mqtt 账号+acl）。
//
// 全部函数内部都先查台账（pgsql_db/valkey_alloc/mqtt_account），已存在
// 则直接复用，不重新生成密码/重新分配——保证 upgradeApp 多次调用幂等，
// 同一应用恒拿到同一套凭据/编号（§需求「升级：只新增授权，不自动降级」）。
package base

import "fmt"

// PgsqlCredentials 是 provisionApp 注入 POSTGRES_DSN 所需的全部信息。
type PgsqlCredentials struct {
	DBName   string
	RoleName string
	Password string
	// DSN 是组装好的 postgres://<role>:<password>@127.0.0.1:5432/<db> 连接串，
	// 供 apps 模块直接注入 unit 的 POSTGRES_DSN 环境变量。
	DSN string
}

func (c PgsqlCredentials) buildDSN() string {
	return fmt.Sprintf("postgres://%s:%s@127.0.0.1:5432/%s", c.RoleName, c.Password, c.DBName)
}

// ProvisionPostgres 为 appID 创建（或复用既有）database/role，返回注入
// POSTGRES_DSN 所需的凭据。调用前应先用 Ready(ComponentPostgres) 确认
// 基座已就绪——本函数不做该检查，检查与拒绝逻辑属于 apps 模块的业务
// 决策（§需求「检查组件就绪（不就绪拒绝安装）」在 lifecycle.go 完成）。
func ProvisionPostgres(appID string) (PgsqlCredentials, error) {
	if err := validateAppID(appID); err != nil {
		return PgsqlCredentials{}, err
	}
	if existing, err := getPgsqlDB(reg.db, appID); err != nil {
		return PgsqlCredentials{}, fmt.Errorf("查询 pgsql 台账失败: %w", err)
	} else if existing != nil {
		creds := PgsqlCredentials{DBName: existing.DBName, RoleName: existing.RoleName, Password: existing.Password}
		creds.DSN = creds.buildDSN()
		return creds, nil
	}

	dbName, roleName := appPgsqlNames(appID)
	password, err := generateSecret()
	if err != nil {
		return PgsqlCredentials{}, err
	}

	if out, err := reg.executor.PgCreateDB(dbName, roleName); err != nil {
		return PgsqlCredentials{}, fmt.Errorf("创建数据库失败: %s: %w", out, err)
	}
	if out, err := reg.executor.PgSetPassword(roleName, password); err != nil {
		return PgsqlCredentials{}, fmt.Errorf("设置数据库密码失败: %s: %w", out, err)
	}

	if err := insertPgsqlDB(reg.db, pgsqlDBRecord{AppID: appID, DBName: dbName, RoleName: roleName, Password: password}); err != nil {
		return PgsqlCredentials{}, fmt.Errorf("落库 pgsql 台账失败: %w", err)
	}

	creds := PgsqlCredentials{DBName: dbName, RoleName: roleName, Password: password}
	creds.DSN = creds.buildDSN()
	return creds, nil
}

// DeprovisionPostgres 删除 appID 的 database/role 并清理台账（卸载流程
// 调用；幂等——appID 未曾申领过时直接返回 nil）。
func DeprovisionPostgres(appID string) error {
	rec, err := getPgsqlDB(reg.db, appID)
	if err != nil {
		return fmt.Errorf("查询 pgsql 台账失败: %w", err)
	}
	if rec == nil {
		return nil
	}
	if out, err := reg.executor.PgDropDB(rec.DBName, rec.RoleName); err != nil {
		return fmt.Errorf("删除数据库失败: %s: %w", out, err)
	}
	return deletePgsqlDB(reg.db, appID)
}

// ValkeyCredentials 是 provisionApp 注入 VALKEY_URL 所需的全部信息。
type ValkeyCredentials struct {
	DBNum int
	// URL 是组装好的 redis://127.0.0.1:6379/<db> 连接串（Valkey 协议兼容
	// Redis，客户端库统一用 redis:// scheme），供 apps 模块直接注入
	// unit 的 VALKEY_URL 环境变量。
	URL string
}

func buildValkeyURL(dbNum int) string {
	return fmt.Sprintf("redis://127.0.0.1:6379/%d", dbNum)
}

// ProvisionValkey 为 appID 分配（或复用既有）Valkey db 编号（0-15）。
func ProvisionValkey(appID string) (ValkeyCredentials, error) {
	if err := validateAppID(appID); err != nil {
		return ValkeyCredentials{}, err
	}
	dbNum, err := allocateValkeyDB(reg.db, appID)
	if err != nil {
		return ValkeyCredentials{}, err
	}
	return ValkeyCredentials{DBNum: dbNum, URL: buildValkeyURL(dbNum)}, nil
}

// DeprovisionValkey 回收 appID 的 db 编号分配（卸载流程调用；幂等）。
func DeprovisionValkey(appID string) error {
	return releaseValkeyDB(reg.db, appID)
}

// MQTTCredentials 是 provisionApp 注入 MQTT_USER/MQTT_PASS/MQTT_TOPIC_PREFIX
// 所需的全部信息。
type MQTTCredentials struct {
	Username    string
	Password    string
	TopicPrefix string
}

// ProvisionMQTT 为 appID 创建（或复用既有）Mosquitto 账号+ACL（主题前缀
// nas/apps/<id>/，账号可读写该前缀下全部主题）。
func ProvisionMQTT(appID string) (MQTTCredentials, error) {
	if err := validateAppID(appID); err != nil {
		return MQTTCredentials{}, err
	}
	if existing, err := getMQTTAccount(reg.db, appID); err != nil {
		return MQTTCredentials{}, fmt.Errorf("查询 mqtt 台账失败: %w", err)
	} else if existing != nil {
		return MQTTCredentials{Username: existing.Username, Password: existing.Password, TopicPrefix: existing.TopicPrefix}, nil
	}

	username := appMQTTUsername(appID)
	topicPrefix := appMQTTTopicPrefix(appID)
	password, err := generateSecret()
	if err != nil {
		return MQTTCredentials{}, err
	}

	if out, err := reg.executor.MqttSetPassword(username, password); err != nil {
		return MQTTCredentials{}, fmt.Errorf("创建 mqtt 账号失败: %s: %w", out, err)
	}
	if out, err := reg.executor.MqttAclAdd(username, topicPrefix); err != nil {
		return MQTTCredentials{}, fmt.Errorf("配置 mqtt acl 失败: %s: %w", out, err)
	}

	if err := insertMQTTAccount(reg.db, mqttAccountRecord{AppID: appID, Username: username, Password: password, TopicPrefix: topicPrefix}); err != nil {
		return MQTTCredentials{}, fmt.Errorf("落库 mqtt 台账失败: %w", err)
	}

	return MQTTCredentials{Username: username, Password: password, TopicPrefix: topicPrefix}, nil
}

// DeprovisionMQTT 删除 appID 的 Mosquitto 账号+ACL 并清理台账（卸载流程
// 调用；幂等——appID 未曾申领过时直接返回 nil）。
func DeprovisionMQTT(appID string) error {
	rec, err := getMQTTAccount(reg.db, appID)
	if err != nil {
		return fmt.Errorf("查询 mqtt 台账失败: %w", err)
	}
	if rec == nil {
		return nil
	}
	if out, err := reg.executor.MqttAclRemove(rec.Username); err != nil {
		return fmt.Errorf("删除 mqtt acl 失败: %s: %w", out, err)
	}
	if out, err := reg.executor.MqttDeleteUser(rec.Username); err != nil {
		return fmt.Errorf("删除 mqtt 账号失败: %s: %w", out, err)
	}
	return deleteMQTTAccount(reg.db, appID)
}

// eventsBridgeAccountKey 是事件总线桥（bridge.go）在 mqtt_account 台账里
// 占用的保留 key——它不是真实的应用 id，而是面板自身的身份，复用
// mqtt_account 表只是为了拿现成的「查已存在则复用」持久化逻辑，不想再起
// 一张专表。必须满足 appIDPattern（[a-z0-9-]{2,24}），且与真实应用 id
// 命名空间（§需求 apps.idPattern 同形状）不会撞——应用市场/手工安装的
// id 由用户/manifest 指定，但"nasapp-panel"是面板讳缺的自称，业务应用
// 不应以此命名（manifest 校验不特别拦，约定层面规避即可）。
const eventsBridgeAccountKey = "nasapp-panel"

// eventsBridgeMQTTUsername 是事件总线桥专用的 Mosquitto 用户名，与
// appMQTTUsername 的派生规则保持视觉一致但走独立常量，避免误用
// appMQTTUsername(eventsBridgeAccountKey) 这种看似合理实则意图不清的写法。
const eventsBridgeMQTTUsername = "nasapp_panel_events"

// eventsBridgeMQTTTopicPrefix 是事件总线桥发布事件用的主题前缀
// （docs/APP-SPEC.md §5.6：nas/events/** 为核心系统事件，面板只发布，
// 应用只可订阅）。与 appMQTTTopicPrefix 的 "nas/apps/<id>/" 命名空间
// 完全不同，因此不能复用 ProvisionMQTT——那个函数的 username/topicPrefix
// 派生规则是为"每应用一个账号+前缀"设计的，不适用于面板自身。
const eventsBridgeMQTTTopicPrefix = "nas/events/"

// ProvisionMQTTEventsBridge 为事件总线→MQTT 桥（bridge.go）创建（或复用
// 既有）专属 Mosquitto 账号，ACL 限定在 nas/events/ 前缀下（而非某个应用
// 的 nas/apps/<id>/ 前缀）。复用 mqtt_account 表的持久化/幂等模式，但
// 绕开 appMQTTUsername/appMQTTTopicPrefix——这两个函数是"每应用"命名空间，
// 面板自身的事件桥账号不属于任何应用。
//
// 调用前应先用 Ready(ComponentMosquitto) 确认基座已就绪；本函数不做该
// 检查（与 ProvisionPostgres 同样的分工：就络判定是调用方的业务决策）。
func ProvisionMQTTEventsBridge() (MQTTCredentials, error) {
	key := eventsBridgeAccountKey
	if existing, err := getMQTTAccount(reg.db, key); err != nil {
		return MQTTCredentials{}, fmt.Errorf("查询 mqtt 台账失败: %w", err)
	} else if existing != nil {
		return MQTTCredentials{Username: existing.Username, Password: existing.Password, TopicPrefix: existing.TopicPrefix}, nil
	}

	username := eventsBridgeMQTTUsername
	topicPrefix := eventsBridgeMQTTTopicPrefix
	password, err := generateSecret()
	if err != nil {
		return MQTTCredentials{}, err
	}

	if out, err := reg.executor.MqttSetPassword(username, password); err != nil {
		return MQTTCredentials{}, fmt.Errorf("创建事件桥 mqtt 账号失败: %s: %w", out, err)
	}
	if out, err := reg.executor.MqttAclAdd(username, topicPrefix); err != nil {
		return MQTTCredentials{}, fmt.Errorf("配置事件桥 mqtt acl 失败: %s: %w", out, err)
	}

	if err := insertMQTTAccount(reg.db, mqttAccountRecord{AppID: key, Username: username, Password: password, TopicPrefix: topicPrefix}); err != nil {
		return MQTTCredentials{}, fmt.Errorf("落库事件桥 mqtt 台账失败: %w", err)
	}

	return MQTTCredentials{Username: username, Password: password, TopicPrefix: topicPrefix}, nil
}
