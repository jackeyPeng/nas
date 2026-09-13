# NAS 面板 功能清单 + 操作项分类（测试验证清单）

> 最后更新: 2026-09-13
> 代码基准: /home/jacky/soft/nas（Go 后端 web/ + Alpine.js 前端 web/frontend/）
> 用途: 逐项测试验证。每个「操作项」标注三种分类之一，测试时按行逐个过。
>
> 分类定义:
> - **只显示** —— 纯只读展示，无任何写动作（状态卡片 / 硬件档案 / 版本信息 / 日志查询等）
> - **用户操作** —— 用户在 UI 上主动触发的写操作（点按钮 / 提交表单 / 切开关，含 SSE 进度流程）
> - **系统自动** —— 无需用户触发，由定时器 / cron / 启动时 / 后台自动执行

---

## 1. 登录与认证

**功能说明**: 面板登录入口（端口 8090）。用户名密码登录，出厂默认账号 `fm` + 默认密码，首次登录强制改密；支持 TOTP 两步验证（2FA，默认关闭）。所有 `/api/*` 写操作经 `AuthMiddleware` 鉴权，公开接口仅 `/api/login`、`/api/version`、`/api/system/check`。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 登录表单（用户名 + 密码） | 用户操作 | `POST /api/login`，成功后返回 token，写入本地会话 |
| 首登强制改密 | 用户操作 | 登录时若密码仍为出厂默认，前端强制进入改密流程 |
| 2FA 状态查看 | 只显示 | `GET /api/2fa/status` |
| 2FA 初始化（生成 TOTP 密钥 + 二维码） | 用户操作 | `POST /api/2fa/setup` |
| 启用 2FA | 用户操作 | `POST /api/2fa/enable`，写入 2fa.json，写审计 |
| 禁用 2FA | 用户操作 | `POST /api/2fa/disable` |
| 登录时 TOTP 二次校验 | 用户操作 | 登录流程内嵌：启用 2FA 后需输 6 位动态码 |

---

## 2. 仪表盘（Dashboard）

**功能说明**: 登录后的首页健康摘要。系统信息、服务运行状态、盘位图（NAS 机箱式 4 槽位）、存储池容量卡片、硬件档案。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 系统信息 + 服务状态摘要 | 只显示 | `GET /api/dashboard` |
| 盘位图（4 槽位状态） | 只显示 | 深蓝=已安装 / 黄=待配置 / 灰=空闲 / 红=故障 |
| 存储池总览卡片（容量/健康/进度条） | 只显示 | 随 dashboard 数据渲染 |
| 硬件档案 | 只显示 | `GET /api/hardware/profile`（CPU/内存/网卡/接口等） |

---

## 3. 监控告警（Monitor）

**功能说明**: 实时系统状态 + 告警。CPU/内存/磁盘/网络实时曲线、服务健康、分区表、Top 进程；多通道告警（钉钉/Telegram/Bark/Email）。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 实时监控面板（CPU/内存/磁盘/网络/负载/进程） | 只显示 | `GET /api/monitor`，SVG 环形仪表盘 |
| 服务状态检查（8 项） | 只显示 | 结构化列表 |
| 分区使用率表（颜色进度条） | 只显示 | 所有挂载点 |
| 告警配置查看 | 只显示 | 渠道开关 / 阈值 / 各渠道状态 |
| 告警配置编辑保存 | 用户操作 | `POST /api/alert-config` |
| 每 5 分钟定时检查 | 系统自动 | cron `*/5 * * * * monitor.sh`，超阈值触发告警 |
| 告警去重 | 系统自动 | 1 小时内同一告警不重复推送（/var/lib/nas-monitor 状态） |
| 磁盘空间 / SMART 异常告警 | 系统自动 | monitor.sh 内部判断 |

---

## 4. 健康中心（Health）

**功能说明**: 硬件健康状态聚合展示（磁盘 SMART、RAID、温度等体检结果）。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 健康状态查看 | 只显示 | `GET /api/health` |

---

## 5. 存储管理（DiskMgmt）

**功能说明**: 存储管理核心模块，四层抽象（物理磁盘 → 存储池 → 逻辑卷 → 共享文件夹）。5 个 Tab：存储总览 / 物理磁盘 / 存储池 / 创建向导 / 维护。

### 5.1 存储总览 Tab

| 操作项 | 分类 | 说明 |
|---|---|---|
| 四层嵌套结构展示（Pool→Volume→Folder） | 只显示 | `GET /api/disk/overview` |
| 磁盘信息 / 状态 / 空闲盘 / 挂载 / LVM / iostat / SMART 详情 / 分区 / fstab | 只显示 | info/status/free/mounts/lvm/iostat/smart-detail/partitions/fstab 各读接口 |

### 5.2 物理磁盘 Tab

| 操作项 | 分类 | 说明 |
|---|---|---|
| 格式化磁盘 | 用户操作 | `POST /api/disk/format`，危险操作含 type-to-confirm |
| 挂载磁盘 | 用户操作 | `POST /api/disk/mount` |
| 卸载磁盘 | 用户操作 | `POST /api/disk/unmount` |
| 新建目录 | 用户操作 | `POST /api/disk/mkdir` |

### 5.3 存储池 Tab

| 操作项 | 分类 | 说明 |
|---|---|---|
| 存储池状态 | 只显示 | `GET /api/disk/pool/status` |
| 创建存储池 | 用户操作 | `POST /api/disk/pool/create` |
| LVM 扩容（在线） | 用户操作 | `POST /api/disk/pool/extend` + `extend-stream`（SSE：wipe→pvcreate→vgextend→lvextend→xfs_growfs） |
| RAID 扩容 | 用户操作 | `POST /api/disk/raid/expand-stream`（mdadm --add + --grow，支持 RAID1/5/6）+ `expand-fs` |
| RAID reshape 进度查询 | 只显示 | `GET /api/disk/raid/reshape-status`（百分比/速度/ETA） |
| 删除存储池 | 用户操作 | `POST /api/disk/pool/delete`，危险操作确认词 |
| 导入存储池（换机 Import Pool） | 用户操作 | `GET /api/disk/import` 列表 + `POST /api/disk/import/pool` |
| 替换故障盘（RAID） | 用户操作 | `POST /api/disk/replace`（下拉框选盘，后端 `isDiskInUse` 拦截在用盘） |
| 操作日志（存储操作） | 只显示 | `GET /api/disk/operations` / `/api/disk/oplogs` |

### 5.4 创建向导 Tab

| 操作项 | 分类 | 说明 |
|---|---|---|
| 向导状态 + 方案列表 | 只显示 | `GET /api/disk/wizard/status`（含 goal：safety/capacity/performance/balance） |
| 执行存储配置向导（7 种模式） | 用户操作 | `POST /api/disk/wizard/setup` / `setup-stream`（SSE 实时进度） |
| 重置向导 / 清空存储池配置 | 用户操作 | `POST /api/disk/wizard/reset` / `reset-stream` |

### 5.5 维护 Tab

| 操作项 | 分类 | 说明 |
|---|---|---|
| RAID scrub（数据清理） | 用户操作 | `POST /api/disk/scrub` |
| scrub 状态 | 只显示 | `GET /api/disk/scrub/status` |
| SMART 扫描 | 用户操作 | `POST /api/disk/smart-scan` |
| 共享文件夹列表 | 只显示 | `GET /api/disk/folders` |
| 创建共享文件夹 | 用户操作 | `POST /api/disk/folders/create`（走 pending，需「应用配置」生效） |
| 删除共享文件夹 | 用户操作 | `POST /api/disk/folders/delete` |
| 修改共享文件夹权限 | 用户操作 | `POST /api/disk/folders/permission` |
| 共享文件夹配额 | 用户操作 | `GET/POST /api/disk/folders/quota`（XFS project quota） |
| 配置一致性检查 | 用户操作 | `GET /api/disk/config/check` |
| 配置同步 | 用户操作 | `POST /api/disk/config/sync` |
| 待应用变更列表（pending） | 只显示 | `GET /api/disk/pending` |
| 应用待生效配置（Apply） | 用户操作 | `POST /api/disk/pending/apply`，统一重生成 smb.conf 等 |
| 丢弃待生效配置（Discard） | 用户操作 | `POST /api/disk/pending/discard` |
| 操作日志清空 | 用户操作 | `POST /api/disk/oplogs/clear` |

---

## 6. 远端同步（rclone Cloud Engine）

**功能说明**: rclone 远端管理 + 同步任务。5 种远端类型（S3/SFTP/WebDAV/FTP/Local），同步任务 CRUD、手动运行、定时调度。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 远端列表 | 只显示 | `GET /api/rclone/remotes` |
| 创建远端 | 用户操作 | `POST /api/rclone/remotes` |
| 编辑远端 | 用户操作 | `PUT /api/rclone/remotes/{name}`（敏感字段脱敏，留空=不变） |
| 删除远端 | 用户操作 | `DELETE /api/rclone/remotes/{name}` |
| 测试远端连接 | 用户操作 | `POST /api/rclone/remotes/test` |
| 共享目录列表（白名单） | 只显示 | `GET /api/rclone/shared-dirs` |
| 创建子目录 | 用户操作 | `POST /api/rclone/mkdir`（防目录穿越） |
| 同步任务列表 | 只显示 | `GET /api/rclone/tasks` |
| 创建同步任务 | 用户操作 | `POST /api/rclone/tasks` |
| 编辑同步任务 | 用户操作 | `PUT /api/rclone/tasks/{id}` |
| 删除同步任务 | 用户操作 | `DELETE /api/rclone/tasks/{id}` |
| 手动运行任务 | 用户操作 | `POST /api/rclone/tasks/{id}/run` |
| 启用/禁用任务 | 用户操作 | `POST /api/rclone/tasks/{id}/toggle` |
| 同步日志查看 | 只显示 | `GET /api/rclone/logs`（保留 200 条） |
| 清空同步日志 | 用户操作 | `DELETE /api/rclone/logs` |
| 同步引擎状态 | 只显示 | `GET /api/rclone/status` |
| 定时调度器 | 系统自动 | `StartScheduler`：每 30s 扫描，命中 5 段 cron 且未运行则执行 |

---

## 7. 用户管理（Users）

**功能说明**: 用户/用户组/权限矩阵/登录日志四 Tab。向导式添加用户、服务级开关、私有目录配额。

### 7.1 用户列表 Tab

| 操作项 | 分类 | 说明 |
|---|---|---|
| 用户列表（服务徽章/配额/共享数/创建时间） | 只显示 | `GET /api/users` |
| 添加用户（4 步向导） | 用户操作 | `POST /api/users/`（基本信息→服务权限→存储配额→共享权限） |
| 编辑 / 删除用户 | 用户操作 | `POST /api/users/`（action 区分） |
| 用户服务开关（Samba/FTP/WebDAV 单开单关） | 用户操作 | `/api/users/{name}/services` |
| 私有目录配额 | 用户操作 | `/api/users/{name}/quota`（/data/private/xxx XFS quota） |

### 7.2 用户组 Tab

| 操作项 | 分类 | 说明 |
|---|---|---|
| 用户组列表（过滤系统组） | 只显示 | `GET /api/user-groups` |
| 创建用户组 | 用户操作 | `POST /api/user-groups/` |
| 删除用户组 | 用户操作 | `POST /api/user-groups/`（action 区分） |

### 7.3 权限矩阵 Tab

| 操作项 | 分类 | 说明 |
|---|---|---|
| 用户 × 共享文件夹 权限矩阵 | 只显示 | `GET /api/users-matrix` |
| 切换单元格权限（读写/只读/禁止） | 用户操作 | `POST /api/users-matrix`，落到 valid_users/write_users |

### 7.4 登录日志 Tab

| 操作项 | 分类 | 说明 |
|---|---|---|
| 登录日志（last + journalctl 聚合） | 只显示 | `GET /api/users-login-log` |

---

## 8. 防火墙（Firewall）

**功能说明**: ufw 结构化管理。状态卡 + 规则 CRUD + 常用服务快捷放行。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 防火墙状态（安装/启用/默认策略/规则） | 只显示 | `GET /api/firewall` |
| 添加规则 | 用户操作 | `POST /api/firewall/rules`（端口范围/协议/动作/来源 CIDR/备注，白名单校验） |
| 删除规则 | 用户操作 | `DELETE /api/firewall/rules/{num}` |
| 启用防火墙 | 用户操作 | `POST /api/firewall/enable`（自动放行 SSH 22 + 面板 8090 防锁死） |
| 禁用防火墙 | 用户操作 | `POST /api/firewall/disable` |

---

## 9. 凭证保险箱（Credential Vault）

**功能说明**: AES-256-GCM 加密存储敏感凭据（密码/API KEY/Token），列表脱敏显示，查看需二次确认。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 凭据列表（脱敏 `****`） | 只显示 | `GET /api/vault` |
| 新增凭据 | 用户操作 | `POST /api/vault/create` |
| 查看凭据（reveal，二次确认） | 用户操作 | `POST /api/vault/reveal` |
| 删除凭据 | 用户操作 | `POST /api/vault/delete` |

---

## 10. 服务管理（Services）

**功能说明**: 系统服务状态与启停管理。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 服务列表 | 只显示 | `GET /api/services` |
| 服务启停/重启/启用/禁用 | 用户操作 | `/api/services/{name}/{action}`（start/stop/restart/enable/disable） |
| 服务日志查看 | 只显示 | `/api/services/{name}/logs` |
| 安装/初始化服务 | 用户操作 | `POST /api/services/install` |

---

## 11. 系统设置（System）

**功能说明**: 主机名、时区、SSH、sysctl、系统更新、服务、系统重置、HTTPS 证书、2FA、原始配置文件。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 系统总览 | 只显示 | `GET /api/system/overview` |
| 修改主机名 | 用户操作 | `POST /api/system/hostname` |
| 修改时区 | 用户操作 | `POST /api/system/timezone` |
| SSH 配置 | 用户操作 | `POST /api/system/ssh-config` |
| sysctl 内核参数 | 用户操作 | `POST /api/system/sysctl` |
| 系统软件包更新 | 用户操作 | `POST /api/system/updates` |
| 系统服务管理 | 用户操作 | `POST /api/system/services` |
| 系统重置 | 用户操作 | `POST /api/system/reset`（危险操作） |
| 系统检查（公开） | 只显示 | `GET /api/system/check`，无需认证 |
| HTTPS 证书状态 | 只显示 | `GET /api/system/https` |
| 生成自签名证书 | 用户操作 | `POST /api/system/https/generate` |
| 上传自定义证书 | 用户操作 | `POST /api/system/https/upload` |
| 应用证书到 4 个 Web 服务 | 用户操作 | `POST /api/system/https/apply`（自动改 systemd 并重启） |
| 移除证书（恢复 HTTP） | 用户操作 | `POST /api/system/https/remove` |
| 环境配置读取（env） | 只显示 | `GET /api/config/env` |
| Samba 原始配置读写 | 用户操作 | `GET/POST /api/config/samba`、`/api/config/samba/share` |
| vsftpd 用户配置 | 用户操作 | `POST /api/config/vsftpd-users` |
| 配置文件读写 | 用户操作 | `GET/POST /api/config/file` |

---

## 12. 系统详情 / 关于（About / Notice）

**功能说明**: 版本信息、组件清单、公告通知、License 与源码链接。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 版本信息（公开） | 只显示 | `GET /api/version`，无需认证 |
| 系统组件清单 | 只显示 | `GET /api/system/components` |
| 公告通知 | 只显示 | `GET /api/system/notice` |
| License + 源码链接（AGPL §13） | 只显示 | about 页静态展示 |

---

## 13. 备份恢复（Backup）

**功能说明**: 配置备份与恢复，区分「配置备份」与「数据备份」，支持手动与定时。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 备份列表 | 只显示 | `GET /api/backup/list` |
| 创建配置备份 | 用户操作 | `POST /api/backup/create` |
| 恢复配置备份 | 用户操作 | `POST /api/backup/restore` |
| 删除备份 | 用户操作 | `POST /api/backup/delete` |
| 数据备份（rsync 到本地/远程） | 用户操作 | `POST /api/backup/data` |
| 每周定时备份 | 系统自动 | cron `0 3 * * 0 backup-config.sh`，保留最近 3~5 份 |
| 升级前自动备份 | 系统自动 | upgrade.sh / setup.sh 升级前先跑 backup-config.sh |

---

## 14. 操作日志（Audit Logs）

**功能说明**: 审计日志查询与清理。所有 `/api/*` 写操作经 `loggingMiddleware` 自动记录。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 审计日志查询 | 只显示 | `GET /api/logs` |
| 日志计数 | 只显示 | `GET /api/logs/count` |
| 清空日志 | 用户操作 | `POST /api/logs/clear` |
| 日志保留配置 | 用户操作 | `GET/POST /api/logs/config` |
| 写操作自动记录审计 | 系统自动 | `loggingMiddleware` → `LogAudit`（非阻塞，异步写 SQLite） |
| 审计日志自动清理 | 系统自动 | `cleanupOldLogs`：启动时 goroutine，按 `NAS_LOG_RETENTION_DAYS`（默认 90 天）清理 |

---

## 15. 系统诊断（Diagnostics）

**功能说明**: SMART 短检/长检 + RAID scrub，手动触发 + 自动调度，带保护参数与历史记录。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 诊断状态（各项目/进度） | 只显示 | `GET /api/diagnostics/status` |
| 手动触发诊断 | 用户操作 | `POST /api/diagnostics/run` |
| 诊断历史 | 只显示 | `GET /api/diagnostics/history`（SQLite，保留 500 条） |
| 诊断配置（开关/频率/温度上限/IO 上限/限速） | 用户操作 | `POST /api/diagnostics/config` |
| 自动调度 | 系统自动 | 按 schedule（daily/weekly/monthly）到点自动跑，含保护机制（55°C 上限 / IO 70% / RAID 限速） |

---

## 16. 系统自动任务汇总（跨模块）

> 本表把散布在各模块里的「系统自动」项集中列一遍，方便单独对「无人值守」链路做验证。

| 操作项 | 分类 | 说明 |
|---|---|---|
| rclone 定时同步调度器 | 系统自动 | 每 30s 扫描，cronMatch 命中即执行 |
| 监控告警定时检查 | 系统自动 | cron 每 5 分钟 monitor.sh |
| 告警去重 | 系统自动 | 1 小时内同告警不重复 |
| 配置定时备份 | 系统自动 | cron 每周日凌晨 3 点，保留 3~5 份 |
| 升级前自动备份 | 系统自动 | upgrade.sh / setup.sh 触发 |
| 诊断自动调度 | 系统自动 | daily/weekly/monthly + 保护参数 |
| 审计日志自动记录 | 系统自动 | loggingMiddleware 全量写操作 |
| 审计日志自动清理 | 系统自动 | 默认 90 天保留 |

---

## 汇总统计

| 分类 | 操作项数 |
|---|---|
| 只显示 | 45 |
| 用户操作 | 79 |
| 系统自动 | 17 |
| **合计** | **141** |
| 功能模块数 | 16 |

> 注：「系统自动」17 项里，第 16 节「系统自动任务汇总」是对散落在模块 3/6/13/14/15 里的 9 项无人值守任务的跨模块汇总（重复计数 8 项，另「磁盘空间/SMART 异常告警」归在监控模块内、未重复列入汇总表）。**去重后独立的「系统自动」项为 9 项**；测试时可直接按第 16 节这张汇总表一次过完所有无人值守链路。

---

### 附录：如何复算统计

对本文档统计「分类」列出现次数即可。一键命令：

```bash
cd /home/jacky/soft/nas/docs
awk -F'|' '/^\|/{gsub(/ /,"",$3); if($3=="只显示")a++; if($3=="用户操作")b++; if($3=="系统自动")c++} END{print "只显示:",a; print "用户操作:",b; print "系统自动:",c; print "合计:",a+b+c}' feature-operation-checklist.md
```

（「汇总统计」与「附录」本身不含分类列的表格行，不会污染计数。）
