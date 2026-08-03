# Abwen NAS — Architecture v1.0

> 2026-07-31 · 依据外部讨论稿《Abwen NAS 存储架构与系统架构建议 V1.0》（docs/external/）逐条评审后形成。
> 本文档是项目架构宪法：后续功能开发、代码评审、方案取舍以本文为准。修改本文需要明确记录理由。

---

## 0. 设计原则（不可动摇）

1. **Debian First** — 目标平台是 Debian 13，不为特定硬件（如 Raspberry Pi）定制架构；硬件差异由 setup.sh 探测吸收。
2. **Native Linux First** — 系统能力（Samba/NFS/SSH/SMART/mdadm/LVM/systemd/firewall）全部原生直管，不经过容器层。
3. **Go Single Binary** — 面板是单个 Go 二进制，前端 embed，无运行时外部依赖；内网环境不依赖任何 CDN。
4. **systemd First** — 所有常驻服务以 systemd unit 管理，面板通过 D-Bus/systemctl 操作，不发明自己的进程守护。
5. **Storage First** — 存储是产品核心，UI/API 设计以"用户管理数据"为中心，不以"管理协议/RAID"为中心。
6. **Web First** — 唯一官方客户端是 Web UI（含移动端浏览器/PWA），不开发原生 App。

---

## 1. 对外部建议二十条的逐条结论

状态图例：✅ 已采纳（已落地或立即可做） · 🔶 部分采纳（方向对，按我们的方式做） · 📅 采纳排期（进 TODO，中期） · ❌ 不采纳（附理由）

### 一、整体评价（原生 NAS OS 路线）
**结论：✅ 已采纳 —— 现状即如此。**
面板从第一天就是 Go 单二进制直管 Samba/NFS/mdadm/LVM/systemd，与 CasaOS（Docker First）/ TrueNAS SCALE（K8s）路线明确区隔。这是项目最有价值的差异化定位，写入 §0 原则 2/3/4。

### 二、系统定位（Debian 13 NAS OS，非树莓派专用）
**结论：✅ 已采纳。**
setup.sh 已支持 x86_64 普通 PC / N100 小主机 / VM，与树莓派共用同一安装路径。README 定位描述按"基于 Debian 13 的现代化 NAS 操作系统"修订。ARM64 支持保持"不主动适配、不拒绝 PR"的态度——磁盘探测、盘位图等已兼容 VirtIO/NVMe 命名，架构无关。

### 三、系统分层（Web UI → REST API → Go Service → 六个 Layer）
**结论：✅ 已采纳 —— 与现状一致。**
当前 web/modules/ 下的模块划分（dashboard/services/users/storage/diskmgmt/system/monitor/firewall/rclone）与建议的 Storage/Network/Users/Apps/System/Monitor 六层基本同构。后续新模块按这六层归类，不再新增顶层分类。

### 四、Docker 定位（系统原生，第三方应用 Docker）
**结论：🔶 部分采纳 —— 关键分歧点，明确我们的路线。**
- 采纳："系统核心完全不依赖 Docker" —— 现状已满足，永远不满足"面板依赖 Docker"。
- 不采纳："面板把 Docker 作为应用运行平台来管理"。面板**不做 Docker 管理器**，不提供容器列表/compose 编辑/卷管理。一旦面板管 Docker，就会被拖进 compose/网络/存储驱动的无底洞（CasaOS 的前车之鉴）。
- 我们的替代路线：**插件系统（TODO #21）= 原生二进制 + systemd 模板 + 面板插件页**。aria2 这类单二进制应用原生安装。
- 个案豁免：Jellyfin/Immich 这类重依赖应用（ffmpeg 全家桶、ML 模型），原生打包代价过高时，允许插件以"调用用户自己装的 Docker"方式部署——面板只发一条 docker run，不管理容器生命周期。
- 用户自行安装 Docker 使用不受任何限制，只是面板看不见也不管。

### 五、存储四层模型（Disk → Pool → Volume → Shared Folder）
**结论：📅 采纳排期 —— 全文最有价值的一条，进 TODO 中期重构。**
现状"存储空间"把 RAID（mdadm）+ VG/LV（LVM）+ 文件系统三层职责揉在一起，快照/配额/压缩将来无处挂靠。增加 Volume 层后职责清晰：
- **Physical Disk**：真实硬件（型号/SMART/温度/序列号/通电时间），UI 不出现 md0/vg0 等逻辑设备名——盘位图已是这个视角。
- **Storage Pool**：RAID/LVM 组成的数据池，UI 不暴露底层是 mdadm 还是 LVM。
- **Volume**：池上划出的逻辑卷，未来的快照/配额/压缩都挂在 Volume 上。
- **Shared Folder**：用户真正使用的对象，落在 Volume 上。
迁移策略：现有部署（1盘LVM/2盘RAID1 等）在模型上映射为"1 Pool = 1 Volume"的特例，重构时保证存量数据零迁移。

### 六、Storage 模型细分（Disk 只描述硬件 / Pool 隐藏实现 / Volume 承载能力 / SharedFolder 面向用户）
**结论：✅ 已采纳 —— 作为第五条重构的设计细则。**
- Disk 只描述硬件 ✅（盘位图/SMART 页面已符合）
- Pool 不暴露 mdadm/LVM 细节 ✅（动态 RAID 方案引擎已按盘数自动推荐，用户不需要知道是 mdadm）
- Volume 承载快照/配额/压缩 📅（随第五条落地）
- SharedFolder 面向用户 ✅（共享文件夹管理已是独立页面）

### 七、协议是共享目录的属性，不是一级菜单
**结论：✅ 已采纳 —— 已超前落地。**
用户思维是"Movies 怎么共享"而不是"Samba 有几个共享"。现状：
- 权限矩阵（users-matrix）就是"共享 × 用户"二维视角；
- rclone 本地路径白名单以 smb.conf 共享段落为权威数据源；
- 防火墙预设按服务入口而不是按协议配置文件。
后续新增协议（如 S3 网关）一律挂到 SharedFolder 属性上，不再开"协议一级菜单"。

### 八、共享目录独立配置（名称/路径/权限/回收站/协议/索引/压缩/快照/备注）
**结论：✅ 已采纳 —— 字段逐步补齐。**
现有：名称/路径/权限/协议（SMB）/备注。📅 待补：回收站开关（见第十条）、配额（用户配额已有，目录配额随 Volume 层）、索引/压缩/快照（随 Volume 层）。配置存储沿用"权威在系统配置文件，面板解析+回写"模式，不引数据库。

### 九、权限模型（No Access / RO / RW / Custom ACL）
**结论：✅ 已采纳 —— 现状已符合。**
权限矩阵已实现 No Access / Read Only / Read Write 三态；Custom ACL 留作扩展位（setfacl），不在 V1 暴露给用户。

### 十、回收站按共享目录分散（Movies/.recycle/）
**结论：📅 采纳排期 —— 小改，优于现状，进 TODO。**
现状是集中回收站，跨设备移动变成 copy+delete，且恢复时丢失原路径上下文。改为每个共享目录下 `.recycle/`，删除 = rename，恢复 = rename 回去。Samba 侧用 `vfs_recycle` 的 %RELATIVE% 配置即可。

### 十一、Storage 首页从 Pool 视角展示
**结论：🔶 部分采纳 —— 双视图。**
盘位图（Disk 视角）保留——它是硬件健康状态的最直观表达，用户爱看。在此之上**加一层 Pool 总览卡片**（容量/已用/健康状态）放在存储首页顶部。用户思维（Pool）和运维思维（Disk）各取所需，不二选一。

### 十二、Shared Folder 增加 Source（Local/USB/Remote SMB/NFS/S3/iSCSI）
**结论：📅 采纳排期 —— 数据模型预留。**
V1 只有 Local。字段先在内部模型加上（默认 local），USB 挂载是最近的一个真实场景（插 U 盘自动出现为只读共享），Remote SMB/NFS 挂载另一台 NAS 随四层模型一起做。iSCSI 远期。

### 十三、不要围绕 RAID 设计（目标导向：安全/容量/性能）
**结论：✅ 已采纳 —— 引擎已符合，UI 话术待包一层。**
动态 RAID 方案引擎（1盘LVM/2盘RAID1/3盘RAID5/4盘RAID6 自动推荐）已经是"系统替用户选"。📅 待办：创建向导首步改成目标选择（数据安全/最大容量/更高性能/平衡容量与容错），推荐结果附一句人话解释（"两块盘镜像，坏一块不丢数据"），不出现"RAID1"字样直到确认页。

### 十四、移动设备（不做 App，PWA + 第三方生态）
**结论：✅ 已采纳 —— 与 TODO #12 完全一致。**
官方只做 Web UI 响应式 + PWA（manifest/service worker/"添加到主屏幕"）。第三方 App 推荐清单直接采用建议版：
- Android：CX File Explorer / Solid Explorer / FolderSync
- iPhone：FE File Explorer / Documents / PhotoSync / Infuse
前提：SMB/SFTP/WebDAV/HTTPS 四协议可用——HTTPS 正好在 TODO #9，两条互相成就。

### 十五、WebDAV 降级为兼容协议
**结论：✅ 已采纳 —— 现状如此，写明即可。**
官方通道是 Web UI + REST API；WebDAV 保留（rclone serve webdav）但定位是"给第三方程序/挂载用"的兼容协议。SMB 是桌面端主协议。未来若开发 App，走 REST API，不受 WebDAV 限制。

### 十六、统一 FileService 抽象（List/Upload/Download/Delete/Move/Copy/Share）
**结论：📅 采纳排期 —— 长期方向，不提前抽象。**
等 SMB/NFS/WebDAV/FTP/REST 至少三条通道都要改同一套权限逻辑时自然抽出。现在只有 SMB 有完整权限，提前抽象只会猜错接口形状。先在代码里留 `fileservice` 包名占位。

### 十七、未来扩展预留（Snapshots/Migration/SSD Cache/Cloud Sync/Remote NAS/S3/iSCSI/Encryption/Compression/Dedup）
**结论：✅ 已采纳 —— 数据模型预留，V1 不实现。**
- Cloud Sync 已超前落地（rclone 远端同步模块）
- S3 已超前落地（rclone serve s3）
- Snapshots/Compression/Dedup 依赖 Volume 层（第五条）或 Btrfs 选型，四层模型重构时统一评估
- SSD Cache / Storage Migration / Remote NAS / iSCSI / Encryption：内部模型留扩展位，不进 UI

### 十八、总体建议（六个 First / 四个不 First / 用户管理数据而非协议）
**结论：✅ 已采纳 —— 整体写入 §0 设计原则。**
"协议只是系统能力，数据才是系统核心"——这句话作为产品理念写进 README 开头。

### 十九、最终目录结构（Storage 树：Disks/Pools/Volumes/SharedFolders/Snapshots）
**结论：✅ 已采纳 —— 作为存储管理页的目标信息架构。**
当前存储页已是树状布局（Disk→存储空间→共享文件夹），四层模型重构后升级为建议的五节点树。Snapshots 节点占位隐藏，随 Volume 层启用。

### 二十、先完成《Architecture v1.0》文档
**结论：✅ 已采纳 —— 即本文档。**
涵盖建议列出的 8 项中的 6 项：模块边界（§第三条）、存储对象模型（§第五/六条）、权限模型（§第九条）、插件与应用运行模型（§第四条）、移动端方案（§第十四条）、Docker 职责边界（§第四条）。剩余 2 项如下补充。

---

## 2. 本文档额外固化的两项

### 2.1 配置中心策略
**权威数据永远在系统原生配置文件**（smb.conf、exports、fstab、ufw），面板角色是"解析 + 校验 + 回写"。不引入面板自有数据库作为配置源——这样 SSH 进机器手工改配置的人和面板永远看到同一份真相。面板侧只允许缓存和 UI 状态（如 rclone tasks.json 这类系统原生没有的东西才自建存储）。

### 2.2 OTA 更新与配置迁移
- 面板更新 = 替换二进制 + systemctl restart（deploy-nas-panel.sh 已实现：备份→替换→重启→验证）。
- 二进制内嵌版本号，启动时执行配置迁移钩子（如 rclone tasks.json 加 direction 字段这类 schema 演进，启动时补默认值）。
- 配置备份恢复走 backup-config.sh / restore-config.sh（已有，cron 每周）。
- 版本更新检查与一键升级：TODO #14，联网检查 Gitee Release，预留但不阻塞当前迭代。

---

## 3. 本次评审产生的 TODO 变更

| 条目 | 来源 | 类型 |
|---|---|---|
| 回收站改为按共享目录 .recycle/ 分散 | 第十条 | 小改，新增 TODO |
| 存储四层模型重构（Disk→Pool→Volume→SharedFolder） | 第五/六条 | 中期，新增 TODO |
| RAID 向导改目标导向话术 | 第十三条 | 小改，并入四层模型 TODO |
| TODO #12 移动设备补第三方 App 清单 | 第十四条 | 文档更新 |
| README 开头补产品理念（数据是核心，协议是能力） | 第十八条 | 文档更新 |
| docs/external/ 归档讨论稿 docx+pdf | 第二十条 | 已完成 |

---

*评审人：彭胜文 / Hermes · 2026-07-31*
