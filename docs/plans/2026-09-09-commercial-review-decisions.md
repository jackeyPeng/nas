# Z1 / Abwen NAS 商业化评审 — 决策记录

> 日期: 2026-09-09
> 依据: docs/external/20260909/Z1 - Abwen NAS 商业化架构与开源许可证审查报告.md
> 方式: 逐条对照仓库实际代码/文档核实，再定「接受 / 已在做 / 部分接受 / 暂缓 / 拍板」
> 结论: 路线成立、不重写；报告部分 P0 项已在代码中实现（报告只读了 README/architecture，未读源码）

## 一、逐条结论

符号：[接受]=照做  [已做]=方向对且已实现  [部分]=方向对但缩范围  [暂缓]=对但缓排  [拍板]=商业决策

### 路线与架构

| 报告条目 | 结论 | 说明 |
|---|---|---|
| §1/2/59/65 保留架构不重写 | 接受 | 现状即此方向 |
| §11 Storage 四层模型正式化 | 已在做 | diskmgmt 模块 + TODO #26 |
| §25 不引入 K8s/HA/集群 | 接受 | 现状即无 |
| §26 Docker 只做可选能力 | 接受 | 现状即无依赖 |

### rclone

| 报告条目 | 结论 | 说明 |
|---|---|---|
| §4/5/62 保持独立进程、不 embed/fork | 已做 | 现状独立二进制 + systemd |
| §42 锁定 rclone 版本 | 已做 | setup.sh 硬编码 v1.74.4 |
| §18/19 rclone 抽象成 Cloud Engine + 数据模型 | 接受 | 做 Remote Sync（TODO #18）时按 adapter 做 |
| §43 rclone OAuth 完整流程 | 暂缓 | 与 Cloud Engine 一起设计 |

### 许可证与合规

| 报告条目 | 结论 | 说明 |
|---|---|---|
| §6/7 自动生成 manifest/SBOM | 暂缓 | 仅 3 个 Go 直接依赖 + 系统包，手工文件够用；商业化 release 再上 |
| §9 不急着改 AGPL | 接受 | AGPL 有防 fork 闭源的战略价值 |
| §10/35 商业版/开源版边界 | 接受 | Z1 Community(AGPL) / Abwen Product(硬件) / Abwen Cloud 三层 |
| §36 License 页面 | 已做(本次补齐) | 已补 licenses section |
| §37 About + 源码链接 | 已做(本次补齐) | 已补 Gitee/GitHub 源码链接 |
| §63/64 许可证分类 + 版权行漂移 | 部分 | 版权行已修；自动分类暂缓 |

### 存储与数据安全

| 报告条目 | 结论 | 说明 |
|---|---|---|
| §12 Pool ≠ RAID | 已做 | diskmgmt 已隐藏 mdadm/LVM |
| §13 RAID Wizard 产品化 | 已做 | wizard.go + 前端向导 |
| §14 危险操作安全系统 | 部分 | 已有 confirm + 输入池名 + operation_logs + audit；缺「显示影响范围」和更重确认词，P0 补强 |
| §40/41 Shell 迁 Go | 已在做 | diskmgmt 已在 Go，高危操作优先迁 |
| §46/47 Operation ID + 日志 | 部分 | operation_logs 已有一级对象；缺进度查询接口，优先级降 |
| §50/54 不引入 DB 做配置源 | 已做 | config_sync.go + 原生配置文件权威 |
| §55 Desired/Actual State | 部分 | config_sync.go 接近，可显式加 drift 检测 |
| §52/53 Storage metadata 可从磁盘重扫 | 接受 | 真缺口，P0 |

### 备份 / 恢复 / OTA

| 报告条目 | 结论 | 说明 |
|---|---|---|
| §17 配置备份 ≠ 数据备份 | 部分 | 脚本已分 backup-config.sh / backup-data.sh，缺 UI 区分 |
| §47/51/52 恢复场景 + 换机 Import Pool | 接受 | 真缺口，P0 |
| §39 OTA + 自动回滚 | 已做 | upgrade.sh 已实现备份+健康检查+自动回滚 |
| §40 OTA 签名 | 接受 | 真缺口，P0 |
| §41 安全更新拆 Debian/Abwen | 接受 | Debian 侧 unattended-upgrades 已在做 |
| §38 出厂 Factory Image + First Boot Wizard | 接受 | curl|bash 保留给 DIY |

### 安全与账号

| 报告条目 | 结论 | 说明 |
|---|---|---|
| §20 云凭证脱离 .env（Credential Store） | 接受 | TODO #32 已列；P0 |
| §21 Web 安全升级 + 2FA | 部分 | 2FA 是明确缺口，P0；refresh token 等延后 |
| §22 admin 与 NAS user 分离 | 接受 | 涉及账号模型重构，排期 |
| §23 三态权限模型 | 已做 | permission-model 文档 + quota.go |

### 监控 / 运维 / 体验

| 报告条目 | 结论 | 说明 |
|---|---|---|
| §49 Dashboard 优先 Storage Health | 接受 | 调展示优先级，低成本 |
| §46/50 Health Center | 暂缓 | P1 |
| §48 事件中心 Event Bus | 暂缓 | monitor.sh + alert 已有，P2 |
| §16 故障注入测试 | 暂缓 | 人力重，先靠 storage-test-manual.md |

### 硬件 / 商业化

| 报告条目 | 结论 | 说明 |
|---|---|---|
| §31 Hardware Profile 硬件抽象 | 暂缓 | P1 |
| §32 不追求通用硬件 | 接受 | Community generic + Official 认证 |
| §33 硬件定价策略 | 拍板 | 收入=硬件+配件+延保+支持+Cloud Relay+企业功能 |
| §34 收费分层 free/Plus/Enterprise | 拍板 | 框架参考报告，具体切分待定 |
| §24/25/26 FileBrowser/WebDAV/S3 定位 | 接受 | S3 已选 rclone serve s3 |

## 二、修正后 P0-P3（剔除已实现项）

### P0 — 商业化前必须
1. Pool/Volume 恢复 + 换机 Import Pool（§47/52/53）
2. OTA 签名校验（§40）
3. 2FA/TOTP（§21）
4. 危险操作补强：删池/删卷/格式化加「影响范围展示 + 输入确认词」（§14）
5. 云凭证 Credential Store 落地（§20，TODO #32）
6. License 页面 + 源码链接（§36/37）— 本次已做
7. 配置 vs 数据备份 UI 区分（§17）

### P1
Health Center、Hardware Profile、rclone Cloud Engine 抽象、事件中心、SBOM 自动化。

### P2
Snapshots、故障注入测试、多实例服务端口（TODO #29）。

### P3（不做/最后）
Docker 应用、AI、HA、Cluster、Kubernetes。

## 三、本次已顺手修复（commit 9244684）

- README S3/MinIO 不一致 → 已修
- Go 版本 go.mod vs README → 已修（统一 1.25，以实际在用为准）
- rclone 版权行漂移 → 已修（对齐官方 COPYING）

## 四、待拍板项（需产品/商业负责人定）

1. Go 版本以 1.25 为准，但开发机已升到 go1.26.7 —— 建议加 release gate 自动比对版本号，避免文档/实际漂移（报告 §28）。
2. 硬件定价与收费分层（报告 §33/§34）。
3. 报告末尾明确声明：许可证部分是工程层面分析、不构成法律意见；正式销售前需律师最终确认。

## 五、报告事实性偏差（写本记录时已核实）

报告按 README/architecture 文档写成，以下「P0 必做」实际已在代码中实现，无需重复投入：
- HTTPS 证书管理（modules/system/https.go + 前端系统设置页）
- 配额 quota（modules/users/quota.go + modules/diskmgmt/quota.go）
- 操作日志/待操作队列（modules/diskmgmt/pending_ops.go：operation_logs + audit）
- 升级备份 + 健康检查 + 自动回滚（scripts/upgrade.sh）
- rclone 版本锁定（scripts/setup.sh v1.74.4）
- 系统注册表 46 项验收（modules/system/system_registry.go）
- 关于/隐私页面（modules/version/version.go + index.html）
