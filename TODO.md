# NAS 项目优化路线图

> 最后更新: 2026-07-21
>
> 完成 11/14 项 (78.6%)，剩余: 性能调优 / HTTPS / 官网上线

## 📋 待优化清单

### 🔴 高优先级

#### 1. 密码策略统一
**状态**: ✅ 已完成  
**优先级**: 高  
**描述**:  
所有服务密码统一为至少 12 位，通过 .env 文件管理，不再硬编码在脚本中。

**完成内容**:
- ✅ 统一所有服务密码最少 12 位
- ✅ 密码从 .env 文件读取，带长度校验
- ✅ .env.example 提供模板
- ✅ git 历史已清洗，无明文密码残留

---

#### 2. setup.sh 脚本加入 FileBrowser 部署
**状态**: ✅ 已完成  
**优先级**: 高  
**描述**:  
setup.sh 已包含 FileBrowser 的完整安装和配置。

**完成内容**:
- ✅ FileBrowser 下载逻辑（多源回退：file.abwen.com / GitHub / 镜像）
- ✅ 自动初始化数据库和创建管理员用户
- ✅ 配置 systemd 服务并启用
- ✅ 防火墙规则开放 8081 端口

---

### 🟡 中优先级

#### 3. 数据盘扩容方案
**状态**: ✅ 已完成  
**优先级**: 中  
**描述**:  
LVM 扩容和 RAID 扩容均已完成，支持在线扩容不丢数据。

**完成内容**:
- ✅ LVM 扩容 (vgextend + lvextend + xfs_growfs, SSE 实时进度)
- ✅ RAID1 扩容 (mdadm --add + --grow --raid-devices=N, 等 resync)
- ✅ RAID5/6 在线扩容 (mdadm reshape, 后台异步, expand-fs 扩展文件系统)
- ✅ RAID 重构进度查询 API (百分比/速度/ETA)
- ✅ 前端扩容按钮 (LVM 蓝色 / RAID 绿色, 弹窗区分提示)
- ✅ 存储配置向导支持 7 种模式 (single/merge/raid0/1/5/6/separate)

---

#### 4. 备份策略
**状态**: ✅ 已完成  
**优先级**: 中  
**描述**:  
配置备份恢复系统已完成，支持手动/自动备份和一键恢复。

**完成内容**:
- ✅ 配置备份脚本 (backup-config.sh)
- ✅ 恢复脚本 (restore-config.sh)
- ✅ 升级前自动备份 (setup.sh 集成)
- ✅ 定期自动备份 (cron 每周日凌晨3点)
- ✅ 保留最近 5 个备份，自动清理
- ✅ Web 面板备份恢复模块（创建/列表/恢复/删除）
- ✅ 备份内容: 系统配置/服务文件/项目配置/Samba用户数据库/crontab/状态快照

---

#### 5. 监控告警
**状态**: ✅ 已完成  
**优先级**: 中  
**描述**:  
监控告警系统已完成，包含 Web 面板实时监控和 Shell 脚本定时告警。

**完成内容**:
- ✅ CPU、内存、磁盘使用率（Web 面板实时显示）
- ✅ 系统负载、进程数、登录用户
- ✅ 服务状态检查（8个服务）
- ✅ 日志监控（journalctl 系统错误日志）
- ✅ 磁盘空间使用率告警（超过阈值告警）
- ✅ 磁盘健康状态（SMART）
- ✅ 物理磁盘/Inode/LVM 信息
- ✅ 内存占用 Top 10 进程
- ✅ 网络流量监控（实时上下行速率）
- ✅ 多通道告警（钉钉/Telegram/Bark/Email）
- ✅ 告警去重（1小时内不重复）
- ✅ Web 页面可编辑告警配置
- ✅ cron 每5分钟自动检查

---

#### 8. 性能调优
**状态**: ⏳ 待规划  
**优先级**: 中  
**描述**:  
优化网络和 I/O 性能，提升 NAS 响应速度和吞吐量。

**优化方向**:

**8.1 网络优化**
- [ ] 调整 TCP 窗口大小和缓冲区
- [ ] 启用 TCP BBR 拥塞控制
- [ ] 优化 MTU 设置（如支持 Jumbo Frame）
- [ ] 配置网络队列和流量控制

**8.2 I/O 优化**
- [ ] 调整文件系统挂载参数（noatime、nobarrier 等）
- [ ] 配置 I/O 调度器（deadline/mq-deadline）
- [ ] 启用预读缓存
- [ ] 优化 NFS 和 Samba 的读写缓冲区

**8.3 缓存策略**
- [ ] 配置页面缓存和目录缓存
- [ ] Samba 启用 oplocks 和 write cache
- [ ] NFS 调整 rsize/wsize
- [ ] 考虑使用 SSD 作为缓存层

**待办**:
- [ ] 测试基准性能（iperf3、fio）
- [ ] 逐项应用优化配置
- [ ] 对比优化前后性能
- [ ] 记录最佳实践参数

---

#### 9. HTTPS 加密访问
**状态**: ⏳ 待规划  
**优先级**: 中  
**描述**:  
为 Web 服务启用 HTTPS 加密，保护数据传输安全。

**涉及服务**:
- [ ] FileBrowser (8081 → 8443)
- [ ] MinIO Console (9002 → 9443)
- [ ] MinIO S3 API (9000 → 9443)

**实现方案**:

**方案 A: 自签名证书**
- 优点: 简单快速
- 缺点: 浏览器会警告
- 实现: openssl 生成证书

**方案 B: Let's Encrypt**
- 优点: 免费、受信任
- 缺点: 需要公网域名
- 实现: certbot 自动续签

**方案 C: Caddy 反向代理**
- 优点: 自动 HTTPS、统一管理
- 缺点: 多一层代理
- 实现: Caddy 配置反向代理

**待办**:
- [ ] 确定证书方案
- [ ] 生成/获取 SSL 证书
- [ ] 配置各服务启用 HTTPS
- [ ] 更新防火墙规则
- [ ] 更新文档

---

### 🟢 低优先级

#### 10. 多用户配额管理
**状态**: ✅ 已完成  
**优先级**: 低  
**描述**:  
XFS project quota 硬限制，按共享文件夹设置存储配额。

**完成内容**:
- ✅ XFS project quota 内核级硬限制
- ✅ fstab 挂载参数加 prjquota（两台机器）
- ✅ 自动分配 project ID（从 1000 起）
- ✅ 写入 /etc/projects + /etc/projid
- ✅ 新建文件夹时可选设置配额（GB，0=无限制）
- ✅ 删除文件夹时自动清理配额
- ✅ 文件夹列表显示配额标签（已用/上限）
- ✅ 批量查询所有挂载点配额（避免 N×M 调用）
- ✅ GET/POST /api/disk/folders/quota API

---

#### 11. 自动化运维脚本
**状态**: ✅ 已完成  
**优先级**: 低  
**描述**:  
4 个核心运维脚本，覆盖健康检查、数据备份、磁盘清理、用户配额。

**完成内容**:
- ✅ `system-health.sh` — 一键体检（CPU/内存/磁盘/RAID/SMART/服务/网络）
  - 彩色输出，通过/警告/失败三档，退出码反映状态
  - 支持中文 locale（free 内存：/交换：）
  - 兼容 VMware 虚拟盘（SMART OK）
- ✅ `backup-data.sh` — rsync 数据备份到本地/远程
  - --dry-run 预览模式，--source 指定源
  - 自动排除回收站/临时文件/lost+found
- ✅ `disk-cleanup.sh` — 清理系统日志/临时文件/缩略图/APT缓存
  - --dry-run 预览，--aggressive 深度清理
  - 清理前后磁盘状态对比
- ✅ `list-users.sh` — Samba/FTP 用户 + 数据目录 + XFS 配额 + 共享权限

**部署**: 两台机器 /opt/nas/scripts/

---

#### 12. Web 面板"关于我们"页面
**状态**: ⏳ 待规划  
**优先级**: 低  
**描述**:  
在 Web 管理面板添加"关于我们"页面，展示项目信息，方便用户了解项目背景和参与贡献。

**页面内容**:
- 项目简介（NAS 家用存储系统，开源 Debian 13 方案）
- 版本信息（当前版本号、更新日期）
- 技术栈概览（Go + Alpine.js + 原生服务）
- 开源地址（Gitee 仓库链接）
- 贡献指南链接（CONTRIBUTING.md）
- 开发文档链接（DEVELOPMENT.md）
- 硬件方案链接（HARDWARE_SPEC.md）
- 许可证信息（AGPLv3 + 第三方组件）
- 功能模块一览表
- 更新日志摘要

**实现方案**:
- [ ] 后端: modules/about/about.go，返回项目信息 JSON
- [ ] 前端: 左侧导航新增"关于"项，展示项目卡片
- [ ] 内容从 CHANGELOG.md / README.md 自动提取版本和描述

---

#### 13. 存储管理（三层抽象模型）
**状态**: ✅ 已完成  
**优先级**: 中  
**描述**:  
存储管理重构为三层抽象模型（物理磁盘→存储空间→共享文件夹），参考群晖/TrueNAS专业建议，屏蔽底层路径。

**架构设计**:
- 物理磁盘层：接口识别(SATA/NVMe/VirtIO)、温度、SMART、序列号、分区信息
- 存储空间层：RAID组合(LVM/RAID0/1/5/6/独立)、文件系统(xfs)、健康状态
- 共享文件夹层：三级权限(读写/只读/禁止)、回收站(Samba vfs recycle)

**RAID方案推荐引擎**:
- 根据空闲盘数动态推荐：1盘→LVM单盘，2盘→RAID1，3盘→RAID5，4盘→RAID5
- 每方案显示：安全等级/可用容量/利用率/注意事项/推荐标记
- 1盘用LVM(非直接格式化)方便后期vgextend扩容

**存储配置向导(SSE流式)**:
- 7种模式：single(LVM)/merge(LVM合并)/raid0/raid1/raid5/raid6/separate
- 实时进度推送：每步完成即推送事件
- 自动选择下一个可用挂载点(/data/nas2,nas3...)，避免覆盖已有空间

**盘位图(NAS机箱式)**:
- 固定4槽位，浅色机箱外壳
- 四色状态：深蓝(已安装)/黄(待配置)/灰(空闲)/红(故障)
- 槽位号紫色36px，槽位内显示设备路径+容量+接口

**LVM扩容(SSE)**:
- 5步流式进度：wipe→pvcreate→vgextend→lvextend→xfs_growfs
- 风险提示：合并后任一盘故障全丢

**共享文件夹管理**:
- CRUD + 权限(读写/只读/禁止访问) + 回收站
- Samba vfs objects = recycle

**API清单**:
- `/api/disk/overview` — 存储总览(嵌套结构)
- `/api/disk/wizard/status` — 向导状态+方案列表
- `/api/disk/wizard/setup-stream` — SSE配置
- `/api/disk/wizard/reset-stream` — SSE重置
- `/api/disk/pool/extend-stream` — SSE扩容
- `/api/disk/folders` — 文件夹CRUD
- `/api/disk/folders/permission` — 权限管理

**待完善**:
- [ ] RAID1扩容逻辑（不能vgextend，需走独立空间2或RAID5重建）
- [ ] RAID5在线扩容（mdadm --grow）
- [ ] 存储配额(quota)

---

#### 14. 项目官网
**状态**: ⏳ 待规划  
**优先级**: 中  
**描述**:  
为 NAS 项目开发独立官网，作为产品宣传、软件下载、技术支持的对外窗口。独立仓库管理，与 NAS 系统仓库分开。

**页面规划**:
- 首页：项目简介 + 核心卖点 + 产品图片/效果图
- 产品介绍：硬件方案（x86 N150 标准版 / ARM 经济版）、配置对比、竞品分析
- 功能特性：软件功能模块一览、Web 面板截图、技术架构
- 软件下载：nas-panel 二进制、setup.sh 脚本、Release 版本列表
- 服务支持：部署文档、开发手册、FAQ、常见问题
- 开源社区：Gitee 仓库链接、贡献指南、CHANGELOG、更新日志
- 关于我们：团队介绍、联系方式、路线图

**技术选型**:
- 静态站点生成器（Hugo / VitePress / Docusaurus）
- 部署到 GitHub Pages / Gitee Pages / Cloudflare Pages
- 响应式设计，移动端适配
- 暗色/亮色主题切换

**仓库规划**:
- 独立仓库: gitee.com/gitdogcat/nas-website
- 内容与 NAS 系统仓库解耦，通过 CI 自动同步 Release 版本信息
- 软件下载链接指向 file.abwen.com

**待办**:
- [ ] 确定技术选型（Hugo / VitePress / Docusaurus）
- [ ] 创建独立仓库
- [ ] 设计页面布局和视觉风格
- [ ] 编写各页面内容
- [ ] 部署上线
- [ ] 配置自定义域名

---

### 🟢 低优先级

#### 6. 产品手册补充 FileBrowser 章节
**状态**: ✅ 已完成  
**优先级**: 低  
**描述**:  
产品手册已包含 FileBrowser 的安装、配置、使用说明。

**完成内容**:
- ✅ FileBrowser 安装步骤
- ✅ 配置说明
- ✅ 用户管理
- ✅ 访问指南
- ✅ 故障排查

---

#### 7. NFS 固定端口配置
**状态**: ✅ 已完成  
**优先级**: 低  
**描述**:  
NFS 服务已配置固定端口（2049、20048、32768、32769），防火墙规则已更新。

**完成内容**:
- ✅ 修改 `/etc/nfs.conf` 设置固定端口
- ✅ 更新防火墙规则
- ✅ 测试挂载成功

---

## 📝 实施计划

### Phase 1: 基础完善 (已完成 ✅)
- [x] 密码策略统一（.env 文件 + 12位校验）
- [x] setup.sh 加入 FileBrowser
- [x] 用户名自动检测（不再硬编码）

### Phase 2: 数据安全 (已完成 ✅)
- [x] 配置备份脚本 (backup-config.sh)
- [x] 恢复脚本 (restore-config.sh)
- [x] 升级前自动备份
- [x] 定期自动备份 (cron)
- [x] Web 面板备份恢复模块
- [ ] 确定数据盘扩容方案

### Phase 3: 运维优化 (已完成 ✅)
- [x] 建立监控体系（monitor.sh + Web 面板）
- [x] 编写监控脚本
- [x] 配置告警通知（多通道：钉钉/Telegram/Bark/Email）
- [x] Web 管理面板（nas-panel, 端口 8090）

---

## 🔗 相关资源

- 产品手册: `docs/nas-product-manual.md`
- Git 仓库: https://gitee.com/gitdogcat/nas
- 服务器: [REDACTED] (nas.abwen.com)

---

## 📊 进度统计

| 类别 | 总数 | 已完成 | 进行中 | 待办 |
|------|------|--------|--------|------|
| 高优先级 | 2 | 2 | 0 | 0 |
| 中优先级 | 7 | 4 | 0 | 3 |
| 低优先级 | 5 | 2 | 0 | 3 |
| **合计** | **14** | **8** | **0** | **6** |

**完成率**: 57.1%
