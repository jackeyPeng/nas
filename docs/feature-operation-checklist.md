# NAS 面板 功能清单 + 操作项分类（测试验证清单）

> 最后更新: 2026-10-21
> 代码基准: /home/jacky/soft/nas（Go 后端 web/ + Alpine.js 前端 web/frontend/）
> 用途: 逐项测试验证。每个「操作项」标注三种分类之一，测试时按行逐个过。
>
> 分类定义:
> - **只显示** —— 纯只读展示，无任何写动作（状态卡片 / 硬件档案 / 版本信息 / 日志查询等）
> - **用户操作** —— 用户在 UI 上主动触发的写操作（点按钮 / 提交表单 / 切开关，含 SSE 进度流程）
> - **系统自动** —— 无需用户触发，由定时器 / cron / 启动时 / 后台自动执行
>
> **审计约定（2026-09-21 起）**: 所有「用户操作」均自动写入审计日志——全局 `loggingMiddleware`
> 拦截一切非 GET 的 `/api/*` 请求（含失败请求，result=failed），登录成功/失败单独记录；
> handler 内用 `common.LogAuditRequest` 补记富 detail 并防双写；SSE 流式接口由前端 GET 触发，
> 在 handler 内显式补记（wizard setup/reset-stream、pool extend-stream、raid expand-stream）。

---

## 1. 登录与认证

**功能说明**: 面板登录入口（端口 8090）。用户名密码登录，出厂默认账号 `fm` + 默认密码，首次登录强制改密；支持 TOTP 两步验证（2FA，默认关闭）。所有 `/api/*` 写操作经 `AuthMiddleware` 鉴权，公开接口仅 `/api/login`、`/api/version`、`/api/system/check`。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 登录表单（用户名 + 密码） | 用户操作 | `POST /api/login`，成功后返回 token，写入本地会话；成功/失败均写审计（action=login，失败记 attempted user + IP） |
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
| 当前连接卡片（协议/用户/IP/详情/时长） | 只显示 | `GET /api/monitor/connections`，smbstatus+ss 聚合，SMB 有用户名/共享/连接时间 |
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

**功能说明**: 存储管理核心模块，四层抽象（物理磁盘 → 存储池 → 逻辑卷 → 共享文件夹）。6 个 Tab：存储总览 / 物理磁盘 / 存储池 / 创建向导 / 维护 / 回收站。

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
| NFS 导出选项生成 | 系统行为 | 默认 `no_root_squash,insecure`（2026-09-24 起，家用可用性优先：挂载即可写、NAT 后客户端可挂）；readonly 文件夹一律 `ro,...,insecure`；`nfs_no_root_squash` 列保留但不再生效 |
| 共享文件夹配额 | 用户操作 | `GET/POST /api/disk/folders/quota`（XFS project quota） |
| 配置一致性检查 | 用户操作 | `GET /api/disk/config/check` |
| 配置同步 | 用户操作 | `POST /api/disk/config/sync` |
| 待应用变更列表（pending） | 只显示 | `GET /api/disk/pending` |
| 应用待生效配置（Apply） | 用户操作 | `POST /api/disk/pending/apply`，统一重生成 smb.conf 等 |
| 丢弃待生效配置（Discard） | 用户操作 | `POST /api/disk/pending/discard` |
| 操作日志清空 | 用户操作 | `POST /api/disk/oplogs/clear` |

### 5.6 回收站 Tab（per-share，TODO #25）

| 操作项 | 分类 | 说明 |
|---|---|---|
| 回收站条目列表（按共享筛选） | 只显示 | `GET /api/disk/recycle[?share=]`；新布局 .recycle/<user>/ + 旧 #recycle 兼容（「旧版」徽章）；shares 含「(池根 xxx)」伪共享（面板删除的池根回退条目可见可恢复，D1 修复） |
| 恢复条目 | 用户操作 | `POST /api/disk/recycle/restore`；rename 零拷贝回原路径；原位已有同名目录时**合并式恢复**（逐子项回原位，子项碰撞才落 .restored 后缀不覆盖数据）；非目录碰撞加 .restored 后缀 |
| 彻底删除条目 | 用户操作 | `POST /api/disk/recycle/delete`（confirm=yes） |
| 清空共享回收站 | 用户操作 | `POST /api/disk/recycle/clear`（confirm_name=共享名，服务端校验） |
| Samba 删除文件进回收站 | 系统自动 | vfs_recycle：repository=.recycle/%U，keeptree，对 SMB 客户端隐藏 |
| 回收站事件上报 | 系统自动 | eventbus：recycle.restored/deleted/cleared |

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
| 手动运行任务 | 用户操作 | `POST /api/rclone/tasks/{id}/run`（启动即标 running，前端实时进度面板 3s 轮询） |
| 任务实时进度 | 只显示 | `GET /api/rclone/tasks/{id}/progress`（percent/speed/ETA/elapsed + 最近 40 行输出） |
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

## 9. 凭证保险箱（Credential Vault，零知识模型 v2）

**功能说明**: 零知识加密保险箱——PBKDF2-SHA256（600,000 次迭代）+ AES-256-GCM 全部在浏览器端完成，服务器只存 salt/verify_hash/密文，永不接触明文、口令或派生密钥；所有查询按 `owner` 行级隔离，管理员不例外。属于侧边栏新增的「🧰 工具箱」分组。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 查询保险箱状态（是否已初始化/锁定剩余时间/条目数） | 只显示 | `GET /api/vault/status` |
| 首次设置口令 | 用户操作 | `POST /api/vault/setup` |
| 解锁保险箱（常数时间比较） | 用户操作 | `POST /api/vault/unlock`（连续 5 次失败锁定 30 分钟，423 响应带剩余秒数） |
| 条目列表（仅密文 + 明文元数据） | 只显示 | `GET /api/vault/items` |
| 新增 / 更新 / 删除条目 | 用户操作 | `POST /api/vault/create`、`/update`、`/delete` |
| 修改保险箱口令（事务性重加密全部条目） | 用户操作 | `POST /api/vault/change-passphrase` |
| 旧版数据迁移查看 / 导出明文 / 清空旧表 | 用户操作 | `GET /api/vault/legacy`、`POST /api/vault/legacy/reveal`、`POST /api/vault/legacy/purge`（需新保险箱已设置口令） |

**前端安全交互**: 查看明文 15 秒后自动遮罩；复制到剪贴板 30 秒后自动清空；15 分钟无操作或切换页面自动锁定（浏览器内存中的派生密钥随之清除）。

---

## 10. 密码生成器（Password Generator）

**功能说明**: 独立工具页面，不依赖保险箱解锁状态，使用 `crypto.getRandomValues()` 在浏览器端生成安全随机密码，零外部依赖、无后端接口。属于侧边栏「🧰 工具箱」分组。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 生成密码（长度 8-64，字符类型可选） | 用户操作 | 纯前端计算，无 API 调用 |
| 六档强度指示器 | 只显示 | 纯前端计算（长度 + 字符种类数） |
| 生成历史（最近 20 条，内存态） | 只显示 | 刷新页面即清空 |
| 复制到剪贴板 | 用户操作 | 浏览器 Clipboard API |

---

## 11. 服务管理（Services）

**功能说明**: 系统服务状态与启停管理。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 服务列表 | 只显示 | `GET /api/services` |
| 服务启停/重启/启用/禁用 | 用户操作 | `/api/services/{name}/{action}`（start/stop/restart/enable/disable） |
| 服务日志查看 | 只显示 | `/api/services/{name}/logs` |
| 安装/初始化服务 | 用户操作 | `POST /api/services/install` |

---

## 12. 系统设置（System）

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

## 13. 系统详情 / 关于（About / Notice）

**功能说明**: 版本信息、组件清单、公告通知、License 与源码链接。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 版本信息（公开） | 只显示 | `GET /api/version`，无需认证 |
| 系统组件清单 | 只显示 | `GET /api/system/components` |
| 公告通知 | 只显示 | `GET /api/system/notice` |
| License + 源码链接（AGPL §13） | 只显示 | about 页静态展示 |

---

## 14. 备份恢复（Backup）

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

## 15. 操作日志（Audit Logs）

**功能说明**: 审计日志查询与清理。所有 `/api/*` 写操作经 `loggingMiddleware` 自动记录（2026-09-21 加固：覆盖全部写接口 + 登录成败 + 失败请求）。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 审计日志查询 | 只显示 | `GET /api/logs` |
| 日志计数 | 只显示 | `GET /api/logs/count` |
| 清空日志 | 用户操作 | `POST /api/logs/clear`（本身也写审计） |
| 日志保留配置 | 用户操作 | `GET/POST /api/logs/config` |
| 写操作自动记录审计 | 系统自动 | `loggingMiddleware` → `LogAudit`：非 GET `/api/*` 全量记录，带真实 username（JWT）/client IP（X-Forwarded-For）/result（HTTP ≥400 记 failed）；handler 内 `LogAuditRequest` 富 detail 并防双写 |
| 登录审计 | 系统自动 | `/api/login` 成功/失败均记录（失败含尝试用户名 + IP，可追溯暴力破解） |
| SSE 流式操作审计 | 系统自动 | 前端以 GET 触发 SSE，handler 显式补记：wizard setup-stream / reset-stream、pool extend-stream、raid expand-stream |
| 审计日志自动清理 | 系统自动 | `cleanupOldLogs`：启动时 goroutine，按 `NAS_LOG_RETENTION_DAYS`（默认 90 天）清理 |

---

## 16. 系统诊断（Diagnostics）

**功能说明**: SMART 短检/长检 + RAID scrub，手动触发 + 自动调度，带保护参数与历史记录。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 诊断状态（各项目/进度） | 只显示 | `GET /api/diagnostics/status` |
| 手动触发诊断 | 用户操作 | `POST /api/diagnostics/run` |
| 诊断历史 | 只显示 | `GET /api/diagnostics/history`（SQLite，保留 500 条） |
| 诊断配置（开关/频率/温度上限/IO 上限/限速） | 用户操作 | `POST /api/diagnostics/config` |
| 自动调度 | 系统自动 | 按 schedule（daily/weekly/monthly）到点自动跑，含保护机制（55°C 上限 / IO 70% / RAID 限速） |

---

## 17. 事件中心（Events）

**功能说明**: 面板内事件总线。各模块 `EmitEvent` 上报系统事件（info/success/warn/error），异步写 events.db；前端「维护」组事件中心页（概览统计卡 + 来源/类型/时间窗筛选 + 分页）+ 仪表盘「最近告警」摘要卡。与 audit.db 语义分离：audit 记「谁做了什么」，events 记「系统发生了什么」。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 事件列表查询（来源/类型/天数/分页筛选） | 只显示 | `GET /api/events`（type 非法值 400） |
| 事件统计（近 N 天按类型计数） | 只显示 | `GET /api/events/stats` |
| 仪表盘最近告警摘要 | 只显示 | 近 7 天 error+warn 前 5，`loadRecentAlerts` 静默失败不阻塞 |
| 事件上报（各模块 EmitEvent） | 系统自动 | health 状态迁移 / firewall 规则变更 / rclone 同步成败 / update 失败 / storage 删池重置导入换盘 / backup 完成 / recycle 恢复删除清空 |
| 事件异步落库 + 清理 | 系统自动 | eventbus 后台 writer → events.db（SQLite 独立库） |

---

## 18. 文件管理（FileMgmt）

**功能说明**: 面板内置整池文件管理器（v3 权限管理已落地；beta.24 起 FileBrowser 已彻底移除）。前端「文件」页：目录浏览 + 面包屑导航 + 下载 + 预览 + 用量展示 + 多选工具栏（上传/新建/移动/复制/删除/改权限）+ 行内属性对话框（chmod/chown）；用户页每行「修复 home 权限」。安全核心：所有 handler 入参一律池根相对路径，经 `resolvePoolPath`（filemgmt/path.go）锚定——拒绝绝对路径 / `..` 子串 / 符号链接逃逸（对最深已存在祖先 EvalSymlinks）；写操作双重锚定 + renameat2/mkdir O_EXCL 原子兜底；权限操作 mode 白名单（^[0-7]{3,4}$ 且 ≤0777，setuid/setgid/sticky 拒绝）、递归 Walk 符号链接 chmod 跳过/chown 用 Lchown、池根拒绝、fix-home-perms home 根 Lstat 拒符号链接；面板 root 进程直接 os.Chmod/os.Chown 免 sudo（sudoers 无 chmod -R，刻意不扩）。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 面板文件页导航入口 | 只显示 | 面板内 Alpine 页（FileBrowser 已移除） |
| 目录列表浏览（面包屑/排序/属主/权限位/符号链接标注） | 只显示 | `GET /api/files/list`，2000 条截断 + truncated 标志 |
| 目录/池用量展示 | 只显示 | `GET /api/files/usage`，du 异步独立端点；du_status 三态 ok/timeout/error，前端非 ok 一律降级 |
| 单文件下载 | 只显示 | `GET /api/files/download`，ServeContent + Range 断点续传 + nosniff |
| 目录 zip 流式下载 | 只显示 | `GET /api/files/zip`，流式写 ResponseWriter；符号链接存 stub 不跟随 |
| 文本预览 | 只显示 | 限 1MB；JSON 传输 + 前端 x-text 转义（无效 UTF-8 → U+FFFD） |
| 图片预览 | 只显示 | 限 10MB blob URL（closePreview 防泄漏）；SVG 一律 415（防存储型 XSS） |
| 属性回显（stat） | 只显示 | `GET /api/files/stat?path=`，owner/group/mode（字符串八进制）/mode_text/recursive_possible；Lstat 语义（链接报自身）；池根放行 |
| 上传（XHR 流式 + 进度条） | 用户操作 | `POST /api/files/upload?path=` multipart，MultipartReader 流式不撑内存；同名 409 + overwrite=yes 覆盖重试；磁盘满 507 清半成品；落盘按目标共享权限对齐（public 0664/其他 0600） |
| 目录上传（webkitdirectory） | 用户操作 | 前端 filename 携带 webkitRelativePath 子路径；后端自解析 Content-Disposition 取原始值，逐段过 validateFileName 消毒（拒 `..`/绝对路径/空段/控制字符/超长）；缺失父目录逐段 Mkdir 0755+显式 chmod（并发 EEXIST 重查视同已存在），既有目录一律不动；409 冲突与 written 用完整相对路径，overwrite 重试不拍平；浏览器限制：空子目录不上传 |
| 新建目录 | 用户操作 | `POST /api/files/mkdir` {path,name}；validateFileName 白名单；双重锚定；mode 恒 0755（显式 chmod 消 umask） |
| 重命名 | 用户操作 | `POST /api/files/rename` {path,new_name}；renameat2(RENAME_NOREPLACE) 原子防覆盖；同名 no-op 200；池根拒绝 |
| 移动 | 用户操作 | `POST /api/files/move` {src,dst_dir}；同 fs renameat2 零拷贝，跨 fs copyTree+字节校验+权限对齐后才删源；目标在源内部（含符号链接解析后）400 防无限递归 |
| 复制 | 用户操作 | `POST /api/files/copy` {src,dst_dir}；递归 copyTree（符号链接拷链接本身）；新文件按目标共享对齐权限；EEXIST 不误删并发方文件 |
| 删除（进回收站） | 用户操作 | `POST /api/files/delete` {path}；落所属共享 `.recycle/<user>/<原相对路径>` keeptree 与回收站页同源联动；目录前端 type-to-confirm；共享根/池根/.recycle 段拒绝；RecycleBin=false 回退池根 `.recycle/<user>/<池根相对路径>` keeptree（经回收站页「(池根 xxx)」伪共享可见可原位恢复，D1 修复） |
| 改权限（chmod 单条，含递归） | 用户操作 | `POST /api/files/chmod` {path,mode,recursive}；mode 白名单校验；非递归符号链接拒 400；递归 Walk 跳过链接；池根拒 400；前端属性对话框八进制输入+实时符号位预览+预设按钮（0644/0755/0700）；目录递归需 type-to-confirm 确认词 |
| 改属主（chown 单条，含递归） | 用户操作 | `POST /api/files/chown` {path,owner,group,recursive}；owner/group 可选（空=不变传 -1，至少一项）；user.Lookup/LookupGroup 校验；符号链接 Lchown 改链接本身；池根拒 400；前端 owner/group 下拉（数据源 users/user-groups 懒加载） |
| 批量改权限 | 用户操作 | 多选后工具栏「改权限」→ 前端串行逐条 POST chmod + 进度 N/M + 成功/失败汇总 toast；单条失败不中断；递归仅对所选目录生效；批量必输确认词（函数级守卫，UI disabled 之外纵深防御） |
| 修复 home 权限（一键模板） | 用户操作 | `POST /api/files/fix-home-perms` {username}；用户页每行按钮；home=池根/<username> 递归 0700 + chown username:同名组（缺同名组回退主组）；校验链 405→username 含 `/`或`..` 400→用户不存在 400→home 不存在/非目录/符号链接 400；不建缺失子目录（修复不做 provisioning）；前端 type-to-confirm 输入用户名 |
| 文件访问路径锚定校验 | 系统自动 | 所有 /api/files/* 请求过 resolvePoolPath，fail-closed 拒绝池外访问 |

---

## 19. AI 助手（AI Assistant）

**功能说明**: 面板内置的自然语言 NAS 助手（`web/modules/ai/`），默认关闭（零足迹）。对话走 function-calling 直调既有模块内核（只读工具即时执行，写工具必须经服务端 pending 存储 + 前端 type-to-confirm 二次确认才会真正执行）。支持 DeepSeek（云端）/Ollama（本地）两种预设端点；云端端点会接收对话与工具调用上下文，面板内有显式隐私提示。

| 操作项 | 分类 | 说明 |
|---|---|---|
| 开关状态查看（入口显隐） | 只显示 | `GET /api/ai/status`；未启用时工具箱不显示入口，页面内显示引导卡指向设置 tab |
| 配置模型端点（预设/地址/Key/模型名） | 用户操作 | `GET/POST /api/ai/config`；Key 回显掩码，不回显原文；写审计（detail 不含 Key） |
| 连通性测试 | 用户操作 | `POST /api/ai/test-connection`；通过后方可开启，端点/Key/模型任一变更即清除已验证标记 |
| 开启/关闭 AI 助手 | 用户操作 | `POST /api/ai/toggle`；开启要求已配置且已通过连通性测试；关闭不清空已保存配置 |
| 对话（流式 SSE） | 用户操作 | `POST /api/ai/chat`（Accept: text/event-stream）；仅启用态可用（403 守卫），逐事件推送 delta/tool_call/tool_result/pending_action/done/error |
| 会话列表 / 历史回放 | 只显示 | `GET /api/ai/conversations`、`GET /api/ai/conversations/{id}` |
| 只读工具调用（6 项：系统概览/监控快照/磁盘健康/存储池/用户列表/审计日志） | 用户操作 | 模型按需调用，结果即时执行并回填模型，参数/结果均有长度上限，不读取仓库外内容 |
| 写操作确认门（6 个写工具：配额/建用户/防火墙规则/重启服务/磁盘自检/建文件夹） | 用户操作 | 模型请求写工具 → 服务端生成 PendingAction（TTL 5 分钟，参数留在服务端不回传客户端）→ 前端弹窗 type-to-confirm（函数级守卫）→ `POST /api/ai/action/confirm` 校验确认词后直调既有内核执行；`create_user` 的初始密码由前端一次性生成器生成，经 `client_payload.password` 单独通道送达，全程不进模型上下文/消息落库/审计 detail |
| 取消待确认操作 | 用户操作 | `POST /api/ai/action/cancel`；Take 后丢弃，不执行 |
| 工具调用审计 | 系统自动 | 每次工具 Exec（只读）/确认执行（写）均写一条 `AI`/`AI_TOOL` 审计，detail 为脱敏后的工具名+参数摘要 |

---

## 20. 系统自动任务汇总（跨模块）

> 本表把散布在各模块里的「系统自动」项集中列一遍，方便单独对「无人值守」链路做验证。

| 操作项 | 分类 | 说明 |
|---|---|---|
| rclone 定时同步调度器 | 系统自动 | 每 30s 扫描，cronMatch 命中即执行 |
| 监控告警定时检查 | 系统自动 | cron 每 5 分钟 monitor.sh |
| 告警去重 | 系统自动 | 1 小时内同告警不重复 |
| 配置定时备份 | 系统自动 | cron 每周日凌晨 3 点，保留 3~5 份 |
| 升级前自动备份 | 系统自动 | upgrade.sh / setup.sh 触发 |
| 诊断自动调度 | 系统自动 | daily/weekly/monthly + 保护参数 |
| 审计日志自动记录 | 系统自动 | loggingMiddleware 全量写操作 + 登录成败 + SSE 流式操作 |
| 审计日志自动清理 | 系统自动 | 默认 90 天保留 |
| 事件上报与落库 | 系统自动 | 各模块 EmitEvent → eventbus 异步写 events.db |
| 文件访问路径锚定校验 | 系统自动 | filemgmt 所有 /api/files/* 请求过 resolvePoolPath（fail-closed） |

---

## 汇总统计

| 分类 | 操作项数 |
|---|---|
| 只显示 | 64 |
| 用户操作 | 104 |
| 系统自动 | 27 |
| **合计** | **195** |
| 功能模块数 | 20 |

> 注：「系统自动」项里，第 20 节「系统自动任务汇总」是对散落在模块 3/5/6/14/15/16/17/18 里的无人值守任务的跨模块汇总（重复计数），测试时可直接按第 20 节这张汇总表一次过完所有无人值守链路。

---

### 附录：如何复算统计

对本文档统计「分类」列出现次数即可。一键命令：

```bash
cd /home/jacky/soft/nas/docs
awk -F'|' '/^\|/{gsub(/ /,"",$3); if($3=="只显示")a++; if($3=="用户操作")b++; if($3=="系统自动")c++} END{print "只显示:",a; print "用户操作:",b; print "系统自动:",c; print "合计:",a+b+c}' feature-operation-checklist.md
```

（「汇总统计」与「附录」本身不含分类列的表格行，不会污染计数。）
