# NAS 应用平台架构设计（家庭数字化底座）

> 状态：设计已与用户对齐（2026-11-20），待评审后出实施计划
> 前置讨论：本设计基于 2026-11-17 提案主线 A（#37）扩展，并推翻 #37 中「不反代不 iframe」对托管应用的适用范围（外部应用仍维持原拍板）

## 一、定位与发行版

**定位**：面板升级为「家庭数字化底座核心」——文件共享是第一个内置功能，照片、多媒体影院、下载中心、家庭 IoT、健康管理、家庭财务、知识库、本地 AI 等以「应用」形态接入。

**两发行版 = 仓库两套分支**（用户拍板 2026-11-20）：
- `basic` 分支：文件共享基础版，稳定线，从主线切出后仅收快修
- 主线（master）：数字化底座全功能版，应用平台开发都在这条线
- 基础版（basic 画像）：现有文件共享面板，不带应用中心入口，硬件门槛 ≥1GB
- 全功能版（full 画像）：基础 + 应用中心 + 基座服务（PG/Valkey/MQTT/S3），≥4GB（推荐 8GB）
- 发行物各自从对应分支出 tag：basic 线继续 v1.4.x，full 线启用 v1.5.0 起
- 升级关系：basic 机器可切换到 full 线（安装基座服务 + 启用 apps 模块）；full 线快修若涉及共享模块，cherry-pick 回 basic 线
- 分支纪律：主线功能不回流 basic（basic 只收缺陷修复与安全修复）；文档明确两分支支持窗口

## 二、分层架构

```
应用层     托管应用（反代 /apps/<id>/ + SSO） + 外部应用（端口门户卡）
           照片/影院/下载/IoT/健康/财务/知识库/本地AI/备份/NVR/电子书/播客/日历/看护/VPN/...
开放接口层 《NAS 应用开发规范》(spec 版本化)：manifest 契约、包格式、SSO 协议、
           资源接口(S3/PG/Valkey/MQTT)、SDK；市场索引按 spec 过滤
基座服务层 仅 full 版拉起：PostgreSQL 16、Valkey 8、Mosquitto(MQTT)、S3 对象存储、ntfy
           （本地 AI 运行时为候补，一期只在 manifest 留 requires 字段）
核心层     nas-panel core：SSO 认证、应用生命周期、权限确认、事件总线(+MQTT 桥)、
           注册表、监控/告警/备份 —— 收敛为「核心 API」，兼容窗口制度化管理
系统层     Debian 13 + systemd（现状不动）
硬件层     basic ≥1GB；full ≥4GB（基座基线约 800MB），推荐 8GB，SSD 系统盘，
           应用数据一律进存储池；本地 AI 画像 16GB+/NPU 单独标注
```

## 三、两类应用，一张注册表

| | 托管应用 | 外部应用（#37 原样保留） |
|---|---|---|
| 来源 | 应用包（tar.gz）安装 | 机器上已有服务，登记注册 |
| 入口 | 反代 `/apps/<id>/`，统一域名统一登录 | 门户卡直连端口，不反代不 iframe |
| 生命周期 | 面板全管（装/卸/启停/升级/回滚） | 仅 healthz 纳管 |
| 典型 | 照片、下载中心、知识库 | 安康（License 隔离） |

安康仍按外部应用迁入（原拍板不变）。

## 四、应用契约（manifest.json）

```jsonc
{
  "id": "photos", "name": "照片", "icon": "icon.png",
  "version": "1.0.0", "minPanelVersion": "1.5.0", "spec": "1.x",
  "port": 18010,              // 面板分配，只绑 127.0.0.1
  "entry": "/", "healthz": "/healthz",
  "permissions": {
    "storage": ["/data/nas1/photos"],   // 安装时用户确认，面板建目录授权
    "db": "sqlite|pgsql",               // pgsql: 面板自动建库建账号
    "cache": true,                      // Valkey 专属 db 号
    "mqtt": ["nas/events/#", "own/#"],  // 主题授权
    "notifications": true,
    "requires": []                      // 候补：["ai-runtime"]
  },
  "dataDir": true            // 卸载时问「保留数据吗」
}
```

不限实现语言（静态站/Go/Rust 均可）：合规条件 = 监听分配的本机端口 + 前端相对路径（适配 `/apps/<id>/` 前缀）+ 遵守 SSO/资源接口。

## 五、包格式与生命周期

- 应用包 = tar.gz（manifest + 程序 + 前端静态资源 + systemd unit 模板）
- 发布必须带 sha256 + ed25519 签名；**验签公钥编译进面板**
- 安装：验签 → 解包 `/var/lib/nas/apps/<id>/`（程序），数据固定 `/var/lib/nas/apps-data/<id>/` → 声明的基座依赖检查（缺 PG 等则先拉起）→ 权限确认页 → 生成 `nas-app-<id>.service`（专用系统用户、ProtectSystem/PrivateTmp 等硬化）→ 启动 → healthz 通过后门户卡点亮
- 升级：保留 data 目录，旧版备份，失败自动回滚 unit
- 卸载：程序目录删除，data 目录按用户选择保留/清除

## 六、统一入口与 SSO

- 反代 `/apps/<id>/*` → `127.0.0.1:<port>`；转发前验面板会话（无效 401 → 登录页）
- 转发附加 `X-NAS-User` + `X-NAS-Auth`（HMAC 签名 header，密钥在面板 .env）
- 应用侧可信任 X-NAS-User（内网够用）或按规范/SDK 验 HMAC
- healthz 纳入现有 monitor/健康中心，应用故障走现有告警链路

## 七、基座服务

| 组件 | 选型 | 说明 |
|---|---|---|
| 数据库 | 面板自身 SQLite 不动；应用库 PostgreSQL 16（Debian 官方源） | manifest `db: pgsql` 时面板自动建库/账号/授权；弃 MariaDB（家用生态弱） |
| 缓存 | Valkey 8（Redis 开源继任，官方源） | `cache: true` 分配专属 db 号 |
| 消息 | Mosquitto MQTT | 家庭 IoT 既成标准；面板进程内 eventbus 桥接到 `nas/events/**` 主题；应用间解耦通信同走 MQTT；弃 NATS（无设备直接说 NATS） |
| 对象存储 | 现有 S3 网关升格基座服务 | 大文件应用一律走 S3 API，不各建存储 |
| 推送 | ntfy（已有计划） | 应用统一推送出口 |
| 本地 AI | 候补不内置 | manifest `requires: ai-runtime`，有真实应用再定 Ollama/外置推理 |

full 版安装时一次性拉起（用户已拍板）；basic 版完全不装。

## 八、应用市场

- v1 自托管：`get.z1.sale/apps/index.json` 静态索引（应用/版本/tar.gz URL/sha256/签名/spec 兼容）
- 面板应用中心页拉取展示、一键安装
- 第三方开放（提交/签名准入）二期；索引格式与验签从第一天按多人设计，避免日后改格式

## 九、前端策略

- 面板单页仅新增：应用中心页 + 应用详情页（启停/升级/卸载/权限确认），估 200 行级 Alpine
- 托管应用 UI 一行不进面板单页——控制 index.html/app.js 膨胀的关键约束

## 十、场景清单与试点

已列：照片、多媒体影院、下载中心、IoT 控制中心、健康管理、家庭财务、知识库、本地 AI
补充：手机/电脑备份中心、NVR/门口摄像头、电子书库、播客/RSS、家庭日历提醒、老人儿童看护、VPN 回家、DNS 广告过滤、家庭留言板、智能家居自动化规则引擎（依托 MQTT）

优先级维度 = 使用频率 × 数据落 NAS 的天然性 × 复用基座比例。
**首个试点：nas-photos**（Go 小应用：手机备份 → S3 → 相册流），全链路真机验证「装→授权→门户卡→反代 SSO→升级→卸载留数据」；同期市场索引上线演示包。

## 十一、明确不做（YAGNI）

前端运行时插件加载、应用间互调私有 API、跨机器应用编排、应用沙箱容器化（systemd 硬化够用，容器留待真实需求）。

## 十二、实施顺序（规范先行，已拍板）

1. **《NAS 应用开发规范 v0.1》定稿**（契约/包格式/SSO/资源接口/SDK 约定）——先于任何 apps 模块代码
2. apps 模块核心（注册表 + 装卸启停 + 反代 + SSO header）
3. 基座服务纳管（PG/Valkey/Mosquitto 单元 + 面板 eventbus→MQTT 桥）
4. 发行分支：basic 稳定线 + 主线 full 线切分，basic→full 切换与回流纪律
5. nas-photos 试点 + 市场索引 v1
6. 外部应用注册（#37 收编为应用中心第二类）+ 安康 M4 迁入
