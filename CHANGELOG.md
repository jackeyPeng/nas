# NAS 项目变更日志

## 版本变更一览

| 版本 | 日期 | 主要变化 |
|------|------|---------|
| v1.4.0-beta.13 | 2026-09-19 | NFS 导出对所有网段开放（跨网段客户端可挂载） |
| v1.4.0-beta.12 | 2026-09-18 | 创建向导按已选磁盘实时预估容量（选目标后预览面板 + 各方案可用容量/注意事项）+ 登录页品牌 slogan |
| v1.4.0-beta.11 | 2026-09-18 | 当前连接展示（监控页，SMB 会话 + 各协议端口归类）+ 回收站改 per-share `.recycle/`（删除=rename 零拷贝，恢复保留目录树，旧 `#recycle` 兼容）+ 存储页 tab 切换跳动修复 |

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
