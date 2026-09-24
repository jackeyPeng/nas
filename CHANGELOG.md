# NAS 项目变更日志

## 版本变更一览

| 版本 | 日期 | 主要变化 |
|------|------|---------|
| v1.4.0-beta.15 | 2026-09-24 | 家用可用性优先 — NFS 默认 no_root_squash+insecure（挂载即可写、NAT 后可挂）+ public 共享 guest ok + fail2ban 放宽（ban 10 分钟/10 次）+ S3 短用户名 access key 修复 + 审计日志全覆盖加固 + rclone 同步实时进度 |
| v1.4.0-beta.14 | 2026-09-19 | 在线升级加固 — 二进制原子替换 + CLI 升级验签 + 升级后配置幂等迁移（setup.sh --config-only）+ FileBrowser 版本单一事实源 |
| v1.4.0-beta.13 | 2026-09-19 | NFS 导出对所有网段开放（跨网段客户端可挂载） |
| v1.4.0-beta.12 | 2026-09-18 | 创建向导按已选磁盘实时预估容量（选目标后预览面板 + 各方案可用容量/注意事项）+ 登录页品牌 slogan |
| v1.4.0-beta.11 | 2026-09-18 | 当前连接展示（监控页，SMB 会话 + 各协议端口归类）+ 回收站改 per-share `.recycle/`（删除=rename 零拷贝，恢复保留目录树，旧 `#recycle` 兼容）+ 存储页 tab 切换跳动修复 |

---

## [2026-09-24] - 家用可用性优先 + S3 修复 + 审计加固

### 版本 v1.4.0-beta.15

### 变更（产品决策：家用可用性优先）

- **NFS 导出默认反转为 `no_root_squash,insecure`**：覆盖 09-21 的 root_squash 安全基线方案——客户端 root 挂载后直接可写（不再出现"挂上了却 Permission denied"）；`insecure` 允许 ≥1024 源端口，NAT 后的客户端（跨网段访问常态）不再被 mountd 以 illegal port 拒绝。readonly 文件夹一律 `ro,...,insecure`；`nfs_no_root_squash` 列保留（DB 兼容）但不再生效。需要收紧的部署可将文件夹改只读或手工编辑 /etc/exports（注意配置同步会覆盖托管段）
- **public 共享 `guest ok = yes`**：局域网设备（电视/手机/访客电脑）免账号直接访问，写入经 force user 统一映射到服务账号；管理员一旦按用户编辑过权限，自动恢复账号制
- **fail2ban 放宽**：bantime 3600→600 秒、maxretry 5→10 次——避免家用用户输错密码把自己锁在门外（jail.local + setup.sh 模板同步）

### 修复

- **S3 access key 短于 3 字符被 gofakes3 拒绝**：rclone serve s3 内嵌库强制 accessKeyMinLen=3，短用户名（如 fm）的签名请求一律 InvalidAccessKeyId（403），匿名探测正常故长期未暴露；新增 `common.S3AccessKey()` 派生——<3 字符加 `z1-` 前缀（fm → z1-fm），setup.sh/services.go 两处单元生成/安装提示同步；实际值以 /etc/rclone/s3-env 为准
- **改密后 S3 静默失效**（既有缺口）：changePassword 更新 NAS_USER 密码时从不重写 rclone-s3 的 auth-key；现同步更新 s3-env + service ExecStart 并重启服务
- **审计日志结果徽章永远绿色**：前端固定显示 success，failed 记录也标绿；日志筛选缺"诊断"等分类，补齐
- **GetAllFolderMeta 返回 null**：无文件夹时 JSON 输出 null 而非 []，前端需判空；显式 make 空切片

### 新增

- **审计日志全覆盖加固**（09-21）：loggingMiddleware 拦截所有非 GET `/api/*` 写操作（此前 54 个接口漏记，现 100%）；JWT 真实身份 + client IP；HTTP ≥400 记 failed；/api/login 成败均入档（失败含尝试用户名+IP，可追溯暴力破解）；4 个 SSE 流式接口 handler 内显式审计
- **权限矩阵/总览回显改读 folders.db**（唯一事实源），不再解析 smb.conf 生成物；SyncFolderMeta 签名改整结构体，杜绝写入路径漏字段（write_users 事故的结构性修复）
- **rclone 同步任务实时进度**：进度面板展示传输速率/已传字节，新增文件数统计（海量小文件任务可估算完成度）；事件中心模块补录操作清单（158 项/17 模块）

### 验收

- 两台测试机清空重装：9 服务 active、注册表 46 项 37 过 + 9 警告（存储池未建，建池后消除）；RAID1 建池 + testshare（回收站+NFS）创建，/etc/exports 与 smb.conf 托管段生成正确（no_root_squash,insecure / guest ok 均生效）
- 工作站实测全协议：NFS v4.2 挂载 root 写入成功、SMB guest 免密上传成功、FTP 登录+上传、WebDAV PROPFIND+PUT 201、FileBrowser JWT 登录+列表、S3（z1-fm）boto3 list/put/get/list 全过；账号制隔离验证（fm 连 testshare 被拒，符合预期）

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
