# 基座服务使用指南 / Base Services Guide

> 面向两类读者：(1) 使用面板的家庭管理员，想知道「这三个服务是干什么的、能不能关」；
> (2) 往 NAS 上装应用的开发者，想知道「我的应用怎么拿到数据库/缓存/MQTT」。
> 仅适用于**家用服务器版**（master 分支，应用中心 + 基座服务功能）。
> **家用 NAS 版**（basic 分支，纯文件共享）不包含基座服务和应用中心，本文档不适用。
>
> Audience: (1) home admins using the panel who want to know what these three
> services are for and whether it's safe to turn them off; (2) developers
> packaging an app for the NAS who want to know how to get a database/cache/MQTT
> connection. Applies only to the **Home Server edition** (`master` branch, app
> center + base services). The **Home NAS edition** (`basic` branch, file-sharing
> only) has no base services or app center at all — this document does not apply there.

---

## 1. 这三个服务是什么 / What are these three services

基座服务页面（面板左侧「基座服务」）管理三个系统级组件，它们本身**不是给你直接用的工具**，
而是预先装好、按需出租给「托管应用」（应用中心里安装的那些应用）用的公共设施。
类比：它们像小区里统一安装的水电表基础设施，而不是哪个住户自己的水龙头。

| 组件 | 是什么 | 典型用途 |
|---|---|---|
| **PostgreSQL**（`pgsql`） | 关系型数据库 | 需要结构化数据存储的应用（账单、相册索引、博客文章等） |
| **Valkey**（`cache`） | 键值缓存（Redis 协议兼容） | 需要会话缓存、排行榜、队列等高速读写的应用 |
| **Mosquitto**（`mqtt`） | MQTT 消息代理 | 智能家居/IoT 类应用做设备间消息收发 |

Base services page (左侧「基座服务」 in the panel) manages three system-level
components. They are not tools you use directly — they're pre-installed shared
infrastructure that "managed apps" (apps installed via the App Center) lease
from on demand, similar to building-wide water/electricity meters rather than
any single tenant's own tap.

| Component | What it is | Typical use |
|---|---|---|
| **PostgreSQL** (`pgsql`) | Relational database | Apps needing structured storage (billing, photo index, blog posts) |
| **Valkey** (`cache`) | Key-value cache (Redis-protocol compatible) | Apps needing session cache, leaderboards, queues |
| **Mosquitto** (`mqtt`) | MQTT message broker | Smart-home/IoT apps exchanging device messages |

**关键点 / Key points：**
- 三者都只监听 `127.0.0.1`（本机回环地址），**不对外网/局域网开放任何端口**，无法从其他设备直连。
  All three bind only to `127.0.0.1` (loopback) — **no port is exposed to the LAN or internet**; no other device can connect directly.
- 它们服务的对象是装在这台 NAS 上的应用，不是给你（管理员）手动连接使用的数据库客户端入口。
  They serve apps hosted on this NAS, not a database client endpoint for you to connect to manually.
- 面板任何页面都**不会以明文展示密码/连接串**；具体原因见第 4 节「安全边界」。
  No panel page ever displays a password or connection string in plaintext; see §4 "Security boundaries" for why.

---

## 2. 启用/停用的影响 / Impact of enabling or disabling

每个组件独立安装、独立启用/停用、独立卸载，互不影响。

Each component installs, enables/disables, and uninstalls independently; they don't affect each other.

### 安装（Install）
只装软件包、写最小化安全配置（本机回环监听 + 鉴权），**装完默认停止**，不会自动启动、不会自动被任何应用使用。

Installs the package and writes a minimal secure config (loopback-only + auth enabled). **Stopped by default after install** — it won't auto-start or be used by any app until you enable it.

### 启用（Enable）
等价于 `systemctl enable --now`：开机自启 + 立即启动。启用后，声明了对应能力（`db`/`cache`/`mqtt`）的应用在安装时才能真正申领到连接凭据；没有应用用到时，启用了也只是空转，占用少量内存（PostgreSQL ~100-200MB，Valkey/Mosquitto 各几 MB 到几十 MB，视负载而定）。

Equivalent to `systemctl enable --now`: starts now and on boot. Once enabled, apps that declare the matching capability (`db`/`cache`/`mqtt`) can actually be granted credentials at install time. If no app uses it, enabling just idles and uses a small amount of memory (PostgreSQL ~100-200MB; Valkey/Mosquitto a few MB to a few dozen MB depending on load).

### 停用（Disable）
停止服务 + 取消开机自启。**正在使用该服务的已安装应用会连不上数据库/缓存/MQTT，相当于那部分功能瞬间失效**（应用进程本身不会被杀，但它的 DB/缓存/MQTT 调用会报错或超时）。停用前请检查应用中心里是否有应用依赖该组件（每个应用的「详情」会显示它申领的能力）。

Stops the service and disables autostart. **Any installed app currently using that service will lose its database/cache/MQTT connection immediately** — the app process itself keeps running, but calls into that service start failing or timing out. Before disabling, check the App Center for apps that depend on the component (each app's detail view shows the capabilities it requested).

### 卸载（Uninstall）
删除软件包。**会连带删除该组件上所有应用的数据库/缓存命名空间/MQTT 账号**（不可恢复）。只有在没有任何应用依赖它、且你确认不再需要时才卸载。

Removes the package. **This also deletes every app's database / cache namespace / MQTT account on that component** (unreversible). Only uninstall once no app depends on it and you're sure you no longer need it.

> 对家庭管理员的建议：如果应用中心一个应用都没装，三个基座服务保持「已安装、未启用」或完全不装都可以，
> 不影响文件共享等 NAS 核心功能——基座服务是应用平台的配套设施，与存储/Samba/NFS 完全独立。
>
> Recommendation for home admins: if the App Center has no apps installed, it's
> fine to leave all three base services "installed but disabled," or not
> install them at all — they don't affect core NAS functions like file sharing.
> Base services are app-platform infrastructure, fully independent of
> storage/Samba/NFS.

---

## 3. 开发者指南：应用如何申领资源 / Developer guide: how apps request resources

应用在自己的 `manifest.json` 里声明需要的能力（见 `docs/APP-SPEC.md` 第 5 章完整字段说明），
面板安装时据此自动创建专属资源并把连接信息通过 systemd unit 的环境变量注入给应用进程——
**应用代码里永远读环境变量，永远不要硬编码连接信息**。

Apps declare required capabilities in their own `manifest.json` (see
`docs/APP-SPEC.md` §5 for the full field reference). At install time, the panel
creates dedicated resources accordingly and injects connection info into the
app process via systemd unit environment variables — **app code should always
read environment variables, never hardcode connection details**.

### PostgreSQL（`capability: "db"`）

- 面板为应用创建独立的 database + 角色（role），角色密码用 `crypto/rand` 生成的 32 字节随机值，仅此应用可用。
- 注入环境变量：`__DB_URL__`（形如 `postgres://<role>:<password>@127.0.0.1:5432/<dbname>`）。
- 应用只能访问自己的 database，无法访问其他应用或系统库（权限边界由 PostgreSQL 角色权限保证）。

The panel creates a dedicated database + role per app; the role password is a
32-byte random value from `crypto/rand`, usable only by that app.
Injected env var: `__DB_URL__` (e.g. `postgres://<role>:<password>@127.0.0.1:5432/<dbname>`).
The app can only access its own database — not other apps' or system databases
(enforced by PostgreSQL role privileges).

### Valkey（`capability: "cache"`）

- 面板为应用分配独立的 db 编号（0-15 范围内的一个编号，Valkey 原生的逻辑库隔离）。
- 注入环境变量：`__CACHE_URL__`（形如 `redis://127.0.0.1:6379/<db编号>`）与 `__CACHE_DB__`（纯数字编号）。
- 隔离依赖应用自身只使用分配到的 db 编号；Valkey 的 db 隔离是逻辑隔离而非强制访问控制，应用间理论上可越权访问彼此的 db——因此 Valkey **不适合存放需要强隔离的敏感数据**，仅用于缓存等非敏感场景。

The panel assigns each app a dedicated db index (0-15, Valkey's native logical
database separation). Injected env vars: `__CACHE_URL__` (e.g.
`redis://127.0.0.1:6379/<db-index>`) and `__CACHE_DB__` (the numeric index alone).
Isolation relies on each app only using its assigned index — Valkey's db
separation is logical, not an enforced access boundary, so apps could in theory
reach each other's db. **Don't store strongly sensitive data in Valkey** — it's
meant for caching, not isolation-critical secrets.

### Mosquitto（`capability: "mqtt"`）

- 面板为应用创建独立的 MQTT 账号（用户名/密码）与一个专属主题前缀（topic prefix）。
- 注入环境变量：`__MQTT_USER__`、`__MQTT_PASS__`，以及约定的主题前缀（具体变量名见 `docs/APP-SPEC.md` §5.6）。
- 权限边界通过 Mosquitto ACL 文件实现：该账号只能在自己的主题前缀下发布/订阅，无法读写其他应用的主题。

The panel creates a dedicated MQTT account (username/password) and a topic
prefix per app. Injected env vars: `__MQTT_USER__`, `__MQTT_PASS__`, plus the
agreed topic-prefix variable (see `docs/APP-SPEC.md` §5.6 for exact names).
The permission boundary is enforced via the Mosquitto ACL file — the account
can only publish/subscribe under its own topic prefix, not read or write other
apps' topics.

### 权限边界小结 / Permission boundary summary

| 组件 | 隔离机制 | 强度 |
|---|---|---|
| PostgreSQL | 独立 database + 角色权限 | 强（数据库原生强制） |
| Valkey | 独立 db 编号（逻辑隔离） | 弱（无强制访问控制，自律约定） |
| Mosquitto | ACL 文件按主题前缀限制 | 强（代理原生强制） |

| Component | Isolation mechanism | Strength |
|---|---|---|
| PostgreSQL | Dedicated database + role privileges | Strong (enforced by the database engine) |
| Valkey | Dedicated db index (logical) | Weak (no enforced access control, convention-based) |
| Mosquitto | ACL file scoped to topic prefix | Strong (enforced by the broker) |

### 查日志 / troubleshooting 排障

```bash
# PostgreSQL
sudo journalctl -u postgresql --no-pager -n 50
sudo -u postgres psql -c "\l"          # 列出所有 database（仅确认存在，不代表你能连，权限仍受角色限制）

# Valkey
sudo journalctl -u valkey --no-pager -n 50
valkey-cli -h 127.0.0.1 -p 6379 ping   # 仅本机可执行，验证服务是否就绪

# Mosquitto
sudo journalctl -u mosquitto --no-pager -n 50
sudo cat /etc/mosquitto/nas-acl.conf   # 查看当前 ACL 规则（不含密码）
```

应用自身连不上基座服务时，排查顺序：
1. 面板「基座服务」页确认对应组件是「已启用」且健康徽标为「运行中」。
2. 确认应用的 systemd unit 环境变量里有对应的 `__DB_URL__`/`__CACHE_URL__`/`__MQTT_USER__` 等（`systemctl show <unit> -p Environment`，由运维/管理员执行，不对应用暴露）。
3. 看应用自身日志（应用中心 → 该应用 → 日志）里的具体连接报错。

If an app can't reach a base service, check in this order:
1. On the panel's "Base Services" page, confirm the component is "Enabled" with a "Running" health badge.
2. Confirm the app's systemd unit has the expected env vars (`__DB_URL__` / `__CACHE_URL__` / `__MQTT_USER__` etc.) via `systemctl show <unit> -p Environment` (run by an admin/operator, not exposed to the app itself).
3. Check the app's own logs (App Center → that app → Logs) for the specific connection error.

---

## 4. 安全边界 / Security boundaries

- **本机回环监听**：PostgreSQL（5432）、Valkey（6379）、Mosquitto（1883）均只绑定 `127.0.0.1`，没有任何防火墙规则需要为它们开放——因为它们从不监听非回环地址，局域网设备物理上无法连接。
- **凭据生成与存放**：所有密码/密钥使用 `crypto/rand` 生成随机值，仅写入对应应用的 systemd unit 环境变量；不落盘到应用可读的配置文件之外的任何地方，也不记录到操作日志。
- **面板从不明文展示凭据**：基座服务页面、应用详情页、操作日志里都只显示组件状态（已安装/已启用/健康度）和应用名，**不会出现数据库密码、MQTT 密码等明文**。如需人工核对，必须以管理员身份在服务器本机通过 `systemctl show` 或读取 unit 文件查看，这是刻意的权限收口设计，不是遗漏。
- **跨应用隔离**：PostgreSQL 与 Mosquitto 的隔离由数据库/代理原生机制强制；Valkey 的 db 编号隔离是逻辑约定（见上表），这是已知的设计取舍，Valkey 仅用于非敏感缓存场景。

- **Loopback-only binding**: PostgreSQL (5432), Valkey (6379), and Mosquitto
  (1883) all bind only to `127.0.0.1`. No firewall rule is needed for them
  because they never listen on a non-loopback address — a LAN device cannot
  reach them even by trying.
- **Credential generation and storage**: all passwords/secrets are random
  values from `crypto/rand`, written only into the owning app's systemd unit
  environment. They are never written anywhere else readable by the app, and
  never logged to the operation log.
- **The panel never displays credentials in plaintext**: the Base Services
  page, app detail pages, and operation logs only show component state
  (installed/enabled/health) and app names — **never a database password, MQTT
  password, or similar in plaintext**. Manual verification requires an admin
  with server shell access to run `systemctl show` or read the unit file
  directly; this is an intentional access boundary, not an oversight.
- **Cross-app isolation**: PostgreSQL and Mosquitto isolation is enforced
  natively by the database/broker. Valkey's db-index isolation is a logical
  convention (see table above) — a known, accepted trade-off; Valkey is meant
  for non-sensitive caching only.

---

## 5. 适用版本 / Edition applicability

| | 家用 NAS 版 (`basic`) | 家用服务器版 (`master`, v1.5.0+) |
|---|---|---|
| 基座服务 / Base services | 不提供 / Not available | 提供，装完默认停止 / Available, stopped by default after install |
| 应用中心 / App Center | 不提供 / Not available | 提供 / Available |
| 最低硬件 / Minimum hardware | ≥1GB 内存 | ≥4GB（推荐 8GB）/ ≥4GB RAM (8GB recommended) |

家用 NAS 版可以升级为家用服务器版（安装基座服务并启用应用模块），反之不支持降级回退。

A Home NAS edition machine can be upgraded to the Home Server edition
(installing base services and enabling the app module); there is no supported
downgrade path back.
