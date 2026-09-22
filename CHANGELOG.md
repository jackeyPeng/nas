# NAS 项目变更日志

## 未发布

### 审计日志全覆盖加固（2026-09-21）

- **全量写操作自动审计**：loggingMiddleware 拦截所有非 GET `/api/*` 请求——此前仅 25/79 个写操作有审计记录（用户/组增删、系统 reset/hostname/HTTPS、磁盘 format/mount、备份增删、配置编辑、rclone 全部等 54 个写接口漏记），现 100% 覆盖
- **真实身份**：审计带 JWT 解出的 username（不再写死 "system"）+ client IP（X-Forwarded-For 优先）；handler 级新增 `common.LogAuditRequest`（富 detail + ctx 标记防中间件双写）
- **失败也入审计**：statusRecorder 捕获响应码，HTTP ≥400 记 result=failed（此前一律 success）
- **登录审计**：`/api/login` 成功/失败均记录，失败含尝试用户名 + IP（可追溯暴力破解）
- **SSE 流式操作补记**：前端以 GET 触发的 4 个流式接口（wizard setup/reset-stream、pool extend-stream、raid expand-stream）在 handler 内显式审计
- 测试：main_audit_test.go（中间件去重/登录成败/失败标记）

### NFS root_squash 安全基线 + 权限矩阵事实源切换（2026-09-21）

- **NFS 导出默认 root_squash**（此前一律 no_root_squash，局域网任意 root 客户端对 rw 导出有全权）；folders 表新增 `nfs_no_root_squash` 列按文件夹显式开启；升级迁移保留既有导出行为，新建默认安全基线（storage-permission-model §八 #3 已修）
- **权限矩阵/总览回显改读 folders.db**（唯一事实源），不再解析 smb.conf 生成物——SyncAllConfigs 失败/滞后不再漂移（§八 #4 已修）
- **SyncFolderMeta 签名改整结构体**：新增字段编译期强制携带，杜绝某条写入路径漏字段抹掉其他路径配置（write_users 事故的结构性修复）
- 测试：nfs_squash_test.go（3 场景）+ matrix_test.go 重写

---

## 版本变更一览

| 版本 | 日期 | 主要变化 |
|------|------|---------|
| v1.4.0-beta.14 | 2026-09-19 | 在线升级加固 — 二进制原子替换 + CLI 升级验签 + 升级后配置幂等迁移（setup.sh --config-only）+ FileBrowser 版本单一事实源 |
| v1.4.0-beta.13 | 2026-09-19 | NFS 导出对所有网段开放（跨网段客户端可挂载） |
| v1.4.0-beta.12 | 2026-09-18 | 创建向导按已选磁盘实时预估容量（选目标后预览面板 + 各方案可用容量/注意事项）+ 登录页品牌 slogan |
| v1.4.0-beta.11 | 2026-09-18 | 当前连接展示（监控页，SMB 会话 + 各协议端口归类）+ 回收站改 per-share `.recycle/`（删除=rename 零拷贝，恢复保留目录树，旧 `#recycle` 兼容）+ 存储页 tab 切换跳动修复 |

---

## [2026-09-19] - 在线升级加固

### 版本 v1.4.0-beta.14

### 新增

- **升级后配置幂等迁移**：`setup.sh --config-only` 只重生成托管配置（Samba/NFS/FTP/WebDAV/S3/防火墙），跳过 apt 安装、目录创建、FileBrowser/面板安装；CLI（upgrade.sh）与 OTA（apply-upgrade.sh）在健康检查通过后自动 `git pull` 更新模板 + 跑一次 --config-only，新版配置格式变更天然完成迁移，失败不回滚二进制（面板已健康）只提示手动重跑；NAS_USER 支持显式传入（systemd-run 环境无 SUDO_USER）
- **CLI 升级验签**：upgrade.sh 下载后拉 `latest-${ARCH}.json` manifest 做 SHA256 + ed25519 校验（openssl，公钥与 OTA update.go 内嵌一致；hex→binary 用纯 bash printf，无 xxd 依赖），与面板 OTA 安全水位拉平；manifest 不可达时降级为 ELF+大小检查并明示警告

### 修复

- **二进制替换原子性**：upgrade.sh / apply-upgrade.sh 原先从 /tmp `mv` 到 /usr/local/bin——跨文件系统时 mv=copy+delete，中途断电会留下半截二进制且旧文件已删；改为同目录 `install .new` + rename（同 fs 原子操作）
- **root 跑 git pull 撞 safe.directory**：apply-upgrade.sh 经 systemd-run 以 root 执行，git 对非 root 属主仓库做所有权检查导致配置模板更新失败；`git -c safe.directory=$(readlink -f /opt/nas)` 显式放行
- **回滚提示误导**：升级完成后的回滚命令从 `cp` 改为 `mv`（运行中二进制 cp 会报「文本文件忙」）
- **FileBrowser 版本三处不一致**（services.go 写死 ×3、setup.sh 变量、upload 脚本 v2.32.0 陈旧）：收敛到 `web/common/versions.go` 单一常量；R2 key 拼写 `filebroswer/` → `filebrowser/`（新旧 key 双上传/双源兜底）；upload-filebrowser.sh 重写为从 Go 常量读版本 + GitHub 官方源下载 + tarball 结构校验

### 验收

- 测试机 .48 实测：CLI 全链路（--check 验签 → 备份 → 原子替换 → 健康检查 → git pull → --config-only），升级后 7 服务全 active、SMB 托管段/NFS 导出完好；回滚双向（.bak mv 回退 → 再升级）通过；OTA 面板内升级派发 → apply 独立 unit 存活过重启 → 配置同步完成
- 设计文档：docs/plans/2026-09-19-upgrade-simplification.md

---

## [2026-09-19] - NFS 导出对所有网段开放

### 版本 v1.4.0-beta.13

### 新增

- **NFS 导出对所有网段开放**：导出客户端由「本机网段自动检测」改为 `*`，跨网段/多网卡客户端（如笔记本走路由访问 NAS）不再被 mountd 拒绝 `unmatched host`；setup.sh 移除内网段检测，旧托管导出行安装时统一归一为 `*`；面板 NFS 徽章提示文案同步更新

---

## [2026-09-18] - 创建向导容量预估 + 登录页 slogan

### 版本 v1.4.0-beta.12

### 新增

- **创建向导按已选磁盘实时预估容量**：第 2 步选择目标（安全性/容量/读写性能/平衡）后新增预览面板——「已选 N 块磁盘 · 总容量」摘要 + 该目标下每个方案的可用容量、安全性、注意事项；第 3 步方案卡片与第 4 步确认页的可用容量改为按第 1 步实际所选磁盘重算（后端按全部空闲盘计算，只选部分盘时数值不准）。混合容量盘按最小盘计算 RAID5/6，与 mdadm 实际行为一致（single=第一块盘、raid1=min(前两块)、raid5=min×(n-1)、raid6=min×(n-2)、merge/raid0/separate=总和）
- **登录页品牌 slogan**：「你的AI Nas / Your AI Nas」中英双语

### 修复

- 向导第 4 步确认页「可用容量:」「安全性:」双冒号
- `raidName`/`raidDesc`/`raidWarn`/`safetyLabel` 补 i18n 响应式依赖，语言切换后方案名称/注意事项正确刷新

### 验收

- 测试机真机验证：2/3/4 盘 × 四目标共 7 组容量断言全对（含 50G+2T 混合盘 RAID5=100G、独立模式=2.1T）；真实点击走完 4 步向导全流程；中英切换正常；i18n parity 1141/1141

---

## [2026-09-18] - 当前连接展示 + per-share 回收站 + tab 跳动修复

### 版本 v1.4.0-beta.11

### 新增

- **当前连接展示**（TODO #31）：监控告警页新增「🔗 当前连接」卡片——`GET /api/monitor/connections` 聚合各协议活跃会话：SMB 用 `smbstatus --json`（用户名/IP/共享列表/连接时间，Samba 4.22 schema，tcons 按 session_id 关联），SSH/FTP/NFS/WebDAV/S3/面板/FileBrowser 用 `ss` 按本地端口归类，`who` 补 SSH 用户名；回环过滤 + IPv4-mapped IPv6 归一；协议徽章 + 相对时长，随监控页轮询刷新
- **per-share 回收站**（TODO #25，Architecture v1.0 第十条）：Samba `vfs_recycle` 从集中 `#recycle` 改为每共享 `.recycle/%U`（按删除者隔离）+ keeptree 保留目录树 + `directory_mode 0700` + `hide files` 对 SMB 客户端隐藏。删除=rename（跨设备移动不再 copy+delete），恢复=rename 回原路径零拷贝。存储管理新增「🗑️ 回收站」tab：共享筛选/恢复/彻底删除/清空（confirm_name=共享名服务端校验，恢复冲突加 `.restored` 后缀不覆盖）；旧 `#recycle` 兼容只读展示（「旧版」徽章）；`resolveRecyclePath` 防目录穿越；事件总线接入 recycle.restored/deleted/cleared

### 修复

- 存储管理页 tab 切换跳动（2026-08-20 记录的已知问题）：全部 tab 内容包进 `.tab-stack` grid 层叠容器（`grid-area:1/1`），离开的 tab 淡出期间不再把新内容顶到下方；实测 6 tab 循环切换 stackTop 恒定零跳动
- 监控连接 IPv4-mapped IPv6（`::ffff:x.x.x.x`）显示归一为纯 IPv4
- 全仓回收站扫描排除点同步 `.recycle`：overview/import/config_sync/folders + backup-data/list-users/disk-cleanup 脚本

### 文档

- TODO.md 完成率 21/33 → 23/33（69.7%）；feature-operation-checklist.md 监控页 +1、存储页新增 5.6 回收站 Tab（6 项），汇总 141 → 148

### 验收

- 两台测试机全新安装 + 完整验收通过：注册表 46/46、SMB 真实删除→`.recycle/<user>/` 落盘→API 恢复整树回原路径、越权/穿越/无确认词全拒、事件落库、连接卡片实测显示 SMB 会话
