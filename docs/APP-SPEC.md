# NAS 应用开发规范（App Developer Spec）v0.1

> 状态：v0.1 定稿（2026-11-20），随实现演进由主线维护；应用与市场索引以本文件为契约依据
> 关联设计：docs/plans/2026-11-20-app-platform-design.md

## 0. 术语与总原则

- **核心（core）**：nas-panel 面板进程，托管应用的管理者与统一入口
- **托管应用（app）**：按本规范打包、由核心安装管理的服务
- **spec 版本**：本规范以 `spec: "1.x"` 声明，兼容性窗口内旧应用可继续运行；市场索引按 spec 过滤
- **最小信任原则**：应用只拿 manifest 声明且用户确认过的资源；端口只绑 127.0.0.1；不假设核心内部实现
- 不限实现语言，合规判据只有：监听分配端口、遵守本规范协议、前端相对路径
- **目录分层不变式（2026-11-24 拍板）**：系统数据与文件一律放系统盘固定目录——面板数据 `/opt/nas/data`、应用程序 `/var/lib/nas/apps/<id>`、应用私有数据 `/var/lib/nas/apps-data/<id>`、基座服务数据 `/var/lib/<svc>`（postgresql/valkey/mosquitto）、配置 `/etc/nas`、`/etc/mosquitto`、备份 `/opt/nas/backups`。**池路径（`/data/*`，含 `/data/system` 系统盘共享目录）只放用户数据**——共享目录（smb/NFS 导出的文件夹及其子树）对家庭用户可见可删，任何系统数据（面板库、事件库、市场缓存、基座数据、应用程序目录）落入共享目录即违规。应用 manifest `storage` 声明的 `/data/*` 路径是唯一例外：那是应用面向用户的业务数据（照片/影片等），安装时经用户逐条确认，且属主是应用用户而非系统组件。

## 1. 应用包格式

应用包 = tar.gz，解包后根目录布局：

```
<id>-<version>/
├── manifest.json      # 见 §2，必须
├── icon.png           # 256x256，门户卡用，必须
├── app/               # 程序与资源（内容自定），必须
│   └── （可执行/静态站/脚本，入口由 unit 模板指定）
├── unit.tpl           # systemd unit 模板，见 §3.4，必须
└── README.md          # 面向用户的功能说明，建议
```

发布物三件套：
- `<id>-<version>.tar.gz`
- `sha256` 摘要
- **ed25519 签名**（对 tar.gz 的 detached signature；验签公钥编译进核心，市场方为 v0.1 唯一签发者）

核心安装时：验签失败 → 拒装；manifest 校验失败 → 拒装。

## 2. manifest.json 契约

```jsonc
{
  "id": "photos",                 // [a-z0-9-]{2,24}，全局唯一，安装后不可变
  "name": "照片",                  // 展示名
  "icon": "icon.png",
  "version": "1.0.0",             // semver
  "spec": "1.0",                  // 遵循的本规范版本
  "minCoreVersion": "1.5.0",      // 最低核心版本
  "port": 18010,                  // 核心分配（见 §5.1），申请值，核心有权改派
  "entry": "/",                   // 门户卡打开的相对路径
  "healthz": "/healthz",          // 200 即健康，其余视为故障
  "permissions": {
    "storage": ["/data/nas1/photos"],  // 池内路径；安装时用户确认，核心建目录+chown 应用用户
    "db": "none|sqlite|pgsql",         // pgsql：核心自动建库/账号/授权（库名 = app_<id>）
    "cache": false,                    // true：核心分配 Valkey db 号（0-15 中 4-15 段）
    "mqtt": [],                        // 授权的 MQTT 主题过滤器（发布/订阅分别授权：{"sub":[],"pub":[]}）
    "notifications": false,            // true：可经核心发通知（ntfy）
    "requires": []                     // 候补能力声明（如 "ai-runtime"），v0.1 仅校验并提示
  },
  "dataDir": true,                // true：/var/lib/nas/apps-data/<id>/ 存持久数据，卸载时可保留
  "upgrade": { "minUpgradeFrom": "1.0.0" }  // 低于此版本要求先卸载重装
}
```

校验规则：未知字段拒绝（前向兼容靠 spec 版本）；`id`/`port`/权限不得超出核心授权。

## 3. 运行环境与生命周期

### 3.1 用户与文件
- 每应用独立系统用户 `nasapp-<id>`（无登录 shell）
- 程序目录 `/var/lib/nas/apps/<id>/`（应用用户只读）——升级时整体替换
- 数据目录 `/var/lib/nas/apps-data/<id>/`（应用用户可写）——升级保留、卸载按用户选择
- manifest 声明的 storage 路径：核心创建并 chown 应用用户

### 3.2 unit 模板（unit.tpl）
- 服务名固定 `nas-app-<id>.service`；`User=nasapp-<id>`
- 必须接受核心注入的模板变量：`__PORT__`（实际分配端口）、`__DATA_DIR__`、`__DB_URL__`（如申请 pgsql）、`__CACHE_DB__`、`__MQTT_USER__/__MQTT_PASS__`、`__NAS_SECRET__`（HMAC 验签密钥，见 §5.3）
- 核心统一附加硬化指令（应用模板里的弱化声明无效）：`ProtectSystem=strict`（对程序/数据目录例外读写）、`PrivateTmp=yes`、`NoNewPrivileges=yes`、`RestrictAddressFamilies=AF_UNIX AF_INET`

### 3.3 生命周期（核心 API，均需管理员会话）
- `POST /api/apps/install`（上传包或市场源 URL）→ 验签 → 解包 → 权限确认（两步：install 返回待确认权限清单 → `POST /api/apps/install/<id>/confirm`）→ 建用户/目录/unit → 启动 → healthz 首过 → 门户卡点亮
- `POST /api/apps/<id>/start|stop|restart`
- `POST /api/apps/<id>/upgrade`（新包验签 → 旧程序目录备份 → 替换 → 失败自动回滚）
- `DELETE /api/apps/<id>`（body 带 `keepData: bool`）
- `GET /api/apps`（列表：安装态/运行态/版本/健康）；`GET /api/apps/<id>`（详情含资源用量）

### 3.4 端口分配
- 应用申请端口，核心在 18000-18999 段核派；冲突由核心改派并回写 `__PORT__`，应用禁止硬编码端口（必须读环境变量/模板注入）

## 4. 前端要求

- 所有页面资源必须**相对路径**（`./api/...`，不得 `/api/...`），核心反代前缀为 `/apps/<id>/`
- 页面不得自行实现登录页：收到 401 时按 SPA 惯例引导回面板登录（核心反代对未认证会话统一返回 401，见 §5.2）
- 静态资源建议 `Cache-Control` 友好值；核心不做资源改写（无注入、无 iframe）

## 5. 协议与资源接口

### 5.1 网络
- 应用只监听 `127.0.0.1:<port>`；外网不可达是硬约束（防火墙由核心联动）

### 5.2 反代与 SSO
- 路由：`/apps/<id>/*` → `127.0.0.1:<port>/*`（路径前缀原样透传，应用自己处理前缀）
- 核心在转发前校验面板会话；无效会话 → 401 + `WWW-Authenticate` 语义（前端跳面板登录）
- 转发附加请求头：
  - `X-NAS-User: <用户名>`
  - `X-NAS-Auth: <sig>`，`sig = HMAC-SHA256(key, user + "\n" + unix_time)`，容差 ±60s
  - `X-NAS-Roles: <角色>`
- 应用侧最低合规：信任 `X-NAS-User`（内网模型）；推荐合规：按 §5.3 验签
- 应用自身可再做细粒度授权（角色/目录级），核心只保证「这是谁」

### 5.3 验签密钥
- 安装时核心为每个应用生成专属 `__NAS_SECRET__` 注入 unit（Environment=）
- 验签算法即上式；SDK 提供参考实现；时间依赖 NTP/本机时钟

### 5.4 数据库
- `db: sqlite`：数据目录内自建自管，核心不感知 schema
- `db: pgsql`：核心建库 `app_<id>`、账号 `app_<id>`、随机密码（仅注入 `__DB_URL__`，不落其它文件）；应用 schema 自管，卸载时库随数据选择删除/保留
- **禁止**应用连接核心自身 SQLite 或其它应用的库

### 5.5 缓存
- `cache: true` 时注入 `__CACHE_URL__`（unix socket 或 127.0.0.1:6379）+ `__CACHE_DB__`（专属 db 号）；只用自己 db 号

### 5.6 消息（MQTT）
- Broker：核心管理的 Mosquitto，仅本机；应用账号 `app_<id>` + 随机密码（注入）
- 主题命名空间：
  - `nas/events/**`：核心系统事件（只读桥，只授权订阅）
  - `nas/apps/<id>/**`：本应用专属（订阅+发布）
  - `nas/notify/<id>`：应用→核心通知通道（JSON：`{"title","body","level"}`）
- ACL 由核心按 manifest 生成；未授权主题一律拒绝

### 5.7 对象存储
- 大文件（照片/视频/备份类）一律走 S3 API（基座网关），凭据注入 `__S3_KEY__/__S3_SECRET__`，bucket 命名 `app-<id>`，核心创建并授权
- 应用不得绕过 S3 直写池路径，除非该路径在 manifest storage 中显式声明并经用户确认

## 6. 健康与可观测

- `healthz`：启动后 30s 内须 200；核心每 60s 轮询，连续 3 次失败 → 事件+告警（走现有 monitor/健康中心），**不自动重启**（避免崩溃循环），由用户决策
- 应用日志：stdout/stderr → journald（`journalctl -u nas-app-<id>`），核心日志页按服务名聚合
- 资源用量（内存/CPU）经 systemd 采集入应用详情页

## 7. 市场索引（v1 自托管）

`https://get.z1.sale/apps/index.json`：

```jsonc
{
  "spec": "1.0",
  "apps": [{
    "id": "photos", "name": "照片", "icon": "<url>", "summary": "…",
    "latest": "1.0.0",
    "versions": [{
      "version": "1.0.0", "minCoreVersion": "1.5.0",
      "tarball": "<url>", "sha256": "…", "sig": "<url>",
      "changelog": "…", "size": 12345678
    }]
  }]
}
```

- 索引仅元数据；tarball 与签名分离托管
- 核心按 `minCoreVersion`/`spec` 过滤展示；安装走 §3.3 流程
- 第三方接入（提交流程/签名准入）二期，格式不再变更（只加字段）
- 一键安装下载链：拉取 `tarball` → 校验内容 `sha256` 与索引声明一致（三件套完整性复核）→ 校验 `sig`（§2 验签规则，与手工上传同一套验签/解包/核心版本校验链路）→ 走 §3.3 install 流程，产出同一形态的待确认响应
- 核心本地缓存一份索引副本，TTL 1 小时；命中期内不重新请求源站；网络不可达时优雅降级为返回陈旧缓存（并标注陈旧），而非整体报错——无任何可用缓存时才视为市场不可用

## 8. 合规清单（应用上架门槛）

1. manifest 通过校验，无未声明资源访问
2. 包验签通过；README 面向最终用户可读
3. 端口仅 127.0.0.1；前端全相对路径
4. 前台健康：healthz 30s 内 200
5. 卸载后无残留（数据目录按用户选择外，无其它落盘）
6. 无遥测外联（如需更新检查，走市场索引）

## 9. v0.1 未定/已知放宽

- requires（ai-runtime 等）仅提示不校验
- 应用资源限额（MemoryMax 等）暂由核心统一值，未按应用定制
- 市场索引无渠道/灰度概念
- 升级数据迁移 DSL 未定义（应用自管，规范只保证 data 目录保留）

## 10. 外部应用（external apps，#37 收编）

### 10.1 定位与边界

- **外部应用 = 机器上已有、自行部署的服务**（如安康），管理员登记注册进应用中心；核心不装不卸、不碰其文件与数据
- 入口是**门户卡新标签页直连**（`window.open`）：不反代、不 iframe、不做鉴权透传——外部应用保持独立登录与 2FA，核心被攻破不等于外部应用数据泄露（License 与隐私隔离：NAS 与外部应用代码零合并，集成只发生在运行托管层）
- 生命周期：核心仅做**注册/注销 + （可选）unit 启停 + 健康监测**；掉线只改状态徽章与告警事件（`external.unhealthy`），不自动重启外部应用（重启策略属外部应用自己的 systemd 配置）
- 外部应用不出现在市场索引里；spec/包格式/SSO/票据协议均不适用于它

### 10.2 注册字段

| 字段 | 必填 | 约束 |
|---|---|---|
| `id` | 是 | `^[a-z0-9][a-z0-9-]{0,31}$`，与托管应用共用同一命名空间（撞名拒绝注册） |
| `name` | 是 | 展示名 1–32 字符 |
| `url` | 是 | 门户跳转地址，`http(s)://`（含端口），存库与响应原样返回 |
| `unit` | 否 | systemd unit 名（`[a-zA-Z0-9@:_-]+\.service`）；提供则启用面板启停按钮与 active 状态 |
| `healthzUrl` | 否 | 健康探测地址 `http(s)://`；提供则纳入健康轮询（200=健康），未提供时状态=unit active 或 unknown |
| `icon` | 否 | 图标 URL 或空（前端用默认占位图标） |

### 10.3 API（均需管理员会话）

- `POST /api/apps/external`：注册（上表字段）；成功返回注册后的记录
- `GET /api/apps`：统一列表——托管应用带 `kind:"managed"`，外部应用带 `kind:"external"`（同一页展示；外部应用无 port/version 字段语义）
- `POST /api/apps/external/<id>/start|stop|restart`：仅注册了 unit 时可用，否则 409
- `DELETE /api/apps/external/<id>`（body `confirm: true`）：注销登记——只删注册表行，不碰服务与文件（这是与托管应用卸载的本质差异，操作无需二次确认页）
- 健康状态随 `GET /api/apps` 列表返回；外部应用状态字段：`unknown / running / stopped / unhealthy`

### 10.4 存储

- 注册表落 `apps.db` 独立表 `external_apps`（与 managed `installed` 表并列；「一张注册表」指应用中心的统一视图，不要求同表）
- 零文件落盘：不建目录、不建用户、不写 unit——注册是纯登记动作

### 10.5 事件

- 注册/注销/启停：`external.registered` / `external.unregistered` / `external.action`（info）
- 健康翻转：`external.unhealthy` / `external.recovered`（warning/info）——进 eventbus，经既有桥转发 `nas/events/external.*`
