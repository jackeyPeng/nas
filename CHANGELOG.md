# NAS 项目变更日志

## [2026-07-17] - 存储管理三层抽象模型 + RAID方案引擎 + 盘位图

### 存储管理架构：三层抽象模型

参考群晖/TrueNAS的专业建议，重构为经典三层抽象：

- **物理磁盘层（硬件）**：磁盘识别、健康监测、接口类型
- **存储空间层（容量与冗余）**：RAID组合、LVM管理、文件系统
- **共享文件夹层（用户访问）**：权限管理、回收站

核心设计：屏蔽底层路径（/data/nas1），用户只看到"存储空间1"。

#### 后端 overview.go 重构

- StorageOverview 结构体重构：
  - `Pools []PoolSummary` — 存储空间（嵌套磁盘和文件夹）
  - `SystemDisks []DiskSummary` — 系统盘（单独分组）
  - `FreeDisks []DiskSummary` — 空闲盘（待配置）
  - 不再平铺所有磁盘到 Disks 字段
- PoolSummary 新增字段：
  - `DisplayName` — 用户可见名（存储空间1、存储空间2）
  - `RaidLevel` — RAID级别（1/0/5/6）
  - `Healthy` — 健康状态（RAID同步中=false）
  - `Disks []DiskSummary` — 属于该空间的磁盘列表
  - `Folders []SharedFolder` — 该空间下的共享文件夹
- DiskSummary 新增字段：
  - `Interface` — 接口类型（SATA/NVMe/VirtIO/USB）
  - `Temp` — 磁盘温度（smartctl解析，支持ATA和NVME格式）
  - `Smart` — SMART健康状态（PASSED/FAILED）
  - `Serial` — 序列号
  - `Partitions []PartitionInfo` — 分区/卷列表
- 磁盘自动归类逻辑：
  - `isSystemDisk()` 检测系统盘（挂载/或/boot/efi）
  - 系统盘 → SystemDisks
  - 空闲盘 → FreeDisks + Unconfigured++
  - 数据盘 → 查找所属存储空间（通过PV→VG→LV或mdadm成员）
- `detectPoolTypeEx()` 返回 (type, raidLevel)，从 /proc/mdstat 解析RAID级别
- `findDiskPoolDisplay()` 返回友好名（存储空间1）而非内部名（nas1）
- 异常检测：SMART失败、RAID degraded/resyncing
- 过滤 zram0/loop/ram 设备

#### 后端 options.go（新建）— RAID方案推荐引擎

根据空闲磁盘数量动态计算可用方案：

| 磁盘数 | 可用方案 | 推荐方案 |
|--------|---------|---------|
| 1块 | LVM单盘 | ★ LVM单盘 |
| 2块 | LVM合并/RAID0/RAID1/独立 | ★ RAID1 |
| 3块 | +RAID5 | ★ RAID5 |
| 4块 | +RAID6 | ★ RAID5 |

每个方案返回：
- `ID` — 模式标识（single/merge/raid0/raid1/raid5/raid6/separate）
- `Name` — 中文名（LVM单盘/容量合并/RAID0条带/RAID1镜像/RAID5/RAID6/独立模式）
- `Safety/SafetyText` — 安全等级（无冗余/基本安全/安全/极高安全）
- `UsableSize/UsableRatio` — 可用容量和利用率（如RAID5三盘：100G，2/3）
- `Description` — 方案说明
- `Warning` — 注意事项（红色显示）
- `Recommended` — 是否推荐（蓝色边框+推荐标签）

关键设计：
- 1块盘用 **LVM**（非直接格式化），方便后期加盘扩容（vgextend）
- RAID0 可选但不推荐，有醒目警告
- 容量计算取所有空闲盘的最小盘容量（混合盘以最小为准）

#### 后端 stream.go 重构 — SSE流式配置

支持全部7种模式的实时进度推送：

- `single` — LVM单盘：wipe→pvcreate→vgcreate→lvcreate→mkfs.xfs→mount+fstab+Samba
- `merge` — LVM合并：多盘pvcreate→vgcreate→lvcreate→格式化→挂载
- `raid0` — RAID0条带：多盘wipe→mdadm --create level=0→格式化→挂载
- `raid1` — RAID1镜像：2盘wipe→mdadm --create level=1→格式化→挂载
- `raid5` — RAID5：3+盘wipe→mdadm --create level=5→格式式→挂载
- `raid6` — RAID6：4+盘wipe→mdadm --create level=6→格式化→挂载
- `separate` — 独立模式：每盘独立分区→格式化→挂载→Samba

重要修复：
- **自动选择下一个可用挂载点**：遍历 /data/nas1 到 /data/nas9，找第一个未挂载且空目录的路径。避免新配置覆盖已有的 /data/nas1（之前固定写死 nas1 导致 RAID 存储空间被 LVM 覆盖）
- `validateMode()` 校验每种模式的最少磁盘数
- `getModeStepCount()` 根据模式计算总步骤数

#### 后端 wizard.go 重构

- `handleWizardStatus` 新增返回 `raid_options` 方案列表
- 空闲盘使用全局编号（磁盘4），与物理磁盘区域编号一致，不再重新从1编号
- 过滤 zram0/loop 设备

#### 后端 pool_extend.go（新建）— LVM扩容SSE

- `/api/disk/pool/extend-stream` 流式推送扩容进度
- 5步：清除签名→pvcreate→vgextend→lvextend→xfs_growfs
- 前端扩容弹窗含风险提示："任意一块磁盘故障，整个存储空间的数据将全部丢失"

#### 后端 folders.go（新建）— 共享文件夹管理

- `/api/disk/folders` — 列出所有存储空间下的文件夹，含大小/权限/回收站状态
- `/api/disk/folders/create` — 在指定空间下建文件夹，支持权限选择和回收站
- `/api/disk/folders/delete` — 删除文件夹+清理Samba配置
- `/api/disk/folders/permission` — 修改Samba权限
- 权限三级：`readwrite`（读写）/ `readonly`（只读）/ `noaccess`（禁止访问，移除Samba共享）
- 回收站：Samba `vfs objects = recycle`，删除文件进入 #recycle 目录
- `parseSambaShares()` 解析 smb.conf 获取共享配置
- `hasRecycleBin()` 检测共享是否配置了回收站

### 前端布局重构

#### 树状结构（替代原来的平铺区块）

- **存储总览**：深蓝渐变卡片，总容量/已用/可用/进度条/空间数/磁盘数/异常
- **盘位图**：独立居中区域（见下文详述）
- **存储空间区域**：蓝色下划线标题，每个空间一张展开卡片
  - 卡片头部：存储空间名 + RAID级别标签 + 文件系统 + 容量进度条
  - 卡片内容：
    - 磁盘详情列表（设备路径 + 容量 + 接口 + 型号 + 温度 + SMART）
    - 共享文件夹列表（名称 + 大小 + 权限标签 + 回收站图标 + 权限/删除按钮）
    - 新建文件夹按钮（预选所属空间）
    - 操作按钮：扩容（仅LVM/单盘模式显示）/ 重置
- **其它磁盘区域**：灰色下划线标题
  - 系统盘：灰色标签 + 设备路径 + 分区列表
  - 待配置盘：红色标签 + 设备路径 + 配置按钮（点击平滑滚到向导）
- **存储配置向导**：动态方案卡片 + 安全等级 + 容量计算 + 推荐标记

#### 盘位图（NAS机箱式，迭代多版）

设计参考 TrueNAS 产品正面原型图：

- 位置：存储总览下方，居中独立区域
- 机箱外壳：浅色渐变(#e8ecf0→#d4dae0)，3px灰色边框，圆角
- 固定4个槽位（机器预留4盘位），横向排列
- 槽位尺寸：CSS class `.disk-slot` 定义 `width:calc((100% - 3vw) / 4)`
  - 用 CSS class 避免 Alpine.js `:style` 绑定覆盖 inline style 的 width
- 槽位结构：编号区（顶部5vw高）+ 盘体信息区（flex:1填充）
- 槽位号1-4：36px紫色(#7c3aed)加粗，在机箱内部槽位顶部
- 四色状态：
  - 深蓝(#1e3a5f→#1e40af) — 已安装（SMART正常）
  - 黄色(#fef3c7→#fde68a) — 待配置（有空闲盘未加入存储空间）
  - 深灰(#6b7280→#4b5563) — 空闲（无盘）
  - 红色(#fee2e2→#fecaca) — 故障（SMART失败）
- 槽位内显示：/dev/sdb + 50G SATA
- 合并所有存储空间的磁盘到盘位图（flatMap），不只取pools[0]
- 底部图例：已安装/待配置/空闲/故障

#### 颜色规范

全面去灰色，保证浅色主题下可读性：

| 元素 | 颜色 |
|------|------|
| 容量 | #475569 深灰蓝 |
| 接口标签 | 底#e0f2fe + 字#0369a1 |
| 型号 | #334155 深石板色 |
| 温度 | #c2410c 深橙色 |
| SMART | #16a34a绿色 / #dc2626红色 |
| 文件夹大小 | #475569 |
| 分区名 | monospace #475569 |
| 分区文件系统 | 底#e0f2fe + 字#0369a1 |
| 分区挂载点 | #2563eb |
| 系统盘（唯一保留灰色） | #6b7280 |

#### 磁盘标识

所有位置从友好名（磁盘2）改为物理路径（/dev/sdb），等宽字体：
- 盘位图槽位内
- 存储空间磁盘详情列表
- 其它磁盘区域（系统盘+待配置盘）
- 向导磁盘列表
- 扩容弹窗下拉选项

### 已知待处理

- RAID1存储空间的扩容逻辑（不能vgextend，需要走独立存储空间2或RAID5重建）
- file.abwen.com 上传新版 nas-panel.latest
- TODO.md 更新完成率

---

## [2026-07-16] - 存储管理重构 + 向导式配置 + 实时进度

### 存储：文件系统改为 xfs
- 所有配置模式（单盘/合并/RAID1/LVM池）默认使用 xfs
- 优势：无 lost+found 目录、大文件性能更好、断电恢复更可靠、在线扩容更快
- setup.sh 安装列表新增 xfsprogs 包

### 存储：依赖包更新
- setup.sh apt 安装列表新增：
  - xfsprogs — xfs 文件系统工具（mkfs.xfs / xfs_growfs）
  - mdadm — RAID 管理工具
  - lvm2 — LVM 逻辑卷管理（pvcreate/vgcreate/lvcreate 等）

### 存储：向导式配置界面
- 左侧导航"磁盘管理"改为"存储管理"
- 不再暴露 /dev/sdb、VG、LV 等技术术语
- 磁盘显示友好名称（磁盘1、磁盘2）
- 单盘：推荐方案卡片，一键配置到 /data/nas1
- 多盘：三选一卡片选择
  - 📦 容量优先：所有盘合并成一个目录（LVM）
  - 🛡️ 安全优先：两盘镜像冗余（RAID1）
  - 📁 独立模式：每盘独立目录 /data/nas1, /data/nas2...
- 卡片选中高亮+缩放动画
- 数据安全警告

### 存储：重新配置功能
- 已配置存储后可点"重新配置存储"按钮
- 自动清除：卸载→fstab→LVM→RAID→磁盘签名→Samba
- 重置后磁盘恢复为"未使用"，回到配置向导

### 存储：实时进度显示（SSE）
- 后端 SSE 流式响应：每完成一步推送事件
- 前端右下角浮动进度面板
- 每步显示：⏳执行中 → ✓完成 → 🎉全部完成
- 步骤：清除签名→分区→格式化→挂载→fstab→Samba
- 重置也支持流式进度

### 存储：后端 API
- /api/disk/wizard/status — 友好磁盘列表 + 存储池状态 + 已挂载目录
- /api/disk/wizard/setup — 按模式一键配置（single/merge/separate/raid1）
- /api/disk/wizard/reset — 重置存储配置
- /api/disk/wizard/setup-stream — SSE 流式配置进度
- /api/disk/wizard/reset-stream — SSE 流式重置进度
- /api/disk/status — 结构化磁盘状态（lsblk JSON 解析）
- /api/disk/pool/status — LVM 存储池状态
- /api/disk/pool/create — 创建 LVM 池
- /api/disk/pool/extend — 扩容 LVM 池
- /api/disk/quick-setup — 单盘快速配置
- /api/disk/fstab — 查看 /etc/fstab

### 存储：安全检查
- isSystemDisk() 检查：跳过系统盘，不误格式化
- 挂载点限制 /data/ 下
- 所有写操作需 confirm=yes
- sudoers 新增：pvremove/vgremove/lvremove/wipefs/mdadm/parted/ls
- LVM 命令用绝对路径 /usr/sbin/

### 验证 (10.216.10.52)
- sdb+sdc RAID1 → /dev/md0 50G xfs → /data/nas1 ✓
- 无 lost+found 目录 ✓
- fstab 持久化 ✓
- Samba 共享自动添加 ✓

## [2026-07-13] - v1.2.0 Release + 市场数据收集

### 版本发布
- 打 tag v1.2.0，创建 Gitee Release (ID: 744432)
- 旧 tag v1.1.0 已删除
- Release: https://gitee.com/gitdogcat/nas/releases/tag/v1.2.0

### 新增：nas-market-research skill
- research 类别 skill，用于 NAS 市场数据收集
- 10 个品牌搜索查询（群晖/威联通/绿联/极空间/海康/联想/铁威马/华硕/开源/N150迷你主机）
- 10 个数据字段（品牌/型号/盘位/CPU/内存/网口/M.2/价格/亮点/场景）
- 按盘位分组对比 + 价格区间分析 + 竞品对比

### 新增：市场数据 cron 定时任务
- 每周五下午 3:00 自动收集 NAS 市场数据
- 输出到 ~/soft/nasdata/nas-market-YYYY-MM-DD.md
- 加载 nas-market-research skill 执行
- 工具集: web + file

### 首次市场数据报告
- 生成 nas-market-2026-07-13.md (13KB)
- 覆盖 8 个成品品牌 + 3 个开源方案 + 4 个 DIY 主板方案
- 约 30+ 款产品
- 含与我们产品(N150标准版/RK3568经济版)的性价比对比
- 注: 首次因 Firecrawl API 未配置，基于已有数据整理，非实时价格

## [2026-07-13] - rclone serve s3 替代 MinIO + v1.2.0 Release

### 重构：MinIO 替换为 rclone serve s3
- 用 `rclone serve s3 /data --addr :9000` 替代 MinIO
- 一份文件六种协议访问：SMB/NFS/FTP/WebDAV/Web UI/S3 API
- /data 下每个目录自动成为 S3 bucket，无需单独配置
- 删除 MinIO 二进制 (~100MB) 和 Console (端口 9002)
- setup.sh [8/10] 从 MinIO 安装改为 rclone 升级 + S3 服务配置
- rclone 从 file.abwen.com/minio/rclone-v1.74.4-linux-amd64.deb 下载
- cleanup.sh 更新：minio → rclone-s3，增加 /etc/rclone/s3-env 清理
- UFW：删除 9002 端口，保留 9000 (S3 API)
- dashboard 模块：minio → rclone-s3 服务定义

### 新增：nas-market-research skill
- research 类别 skill，用于 NAS 市场数据收集
- 10 个品牌搜索查询 + 10 个数据字段
- 按盘位分组对比 + 价格区间分析 + 竞品对比

### 新增：市场数据 cron 定时任务
- 每周五下午 3:00 自动收集 NAS 市场数据
- 输出到 ~/soft/nasdata/nas-market-YYYY-MM-DD.md

### 版本发布
- 打 tag v1.2.0，创建 Gitee Release (ID: 744432)
- 旧 tag v1.1.0 已删除

### 新增：THIRD_PARTY_LICENSES.md
- rclone (MIT) — WebDAV + S3 服务
- FileBrowser (Apache 2.0) — Web 文件管理
- Alpine.js (MIT) — 前端框架
- Go JWT (MIT) — 认证库
- 系统组件许可证表

### 新增：产品宣传 PDF
- nas-product-brochure.pdf (10页)
- 封面/产品概述/技术架构/功能列表/硬件方案/安全运维/技术栈/许可证

## [2026-07-12] - 架构决策敲定：x86 N150 路线

### 决策
- **确认 x86 路线**：Intel N150 标准版先行，ARM 经济版后续
- 6 项关键决策全部锁定（CPU/主板/盘位/机箱/屏幕/硬盘）
- 进入 Phase 0 原型验证阶段

### 采购清单
- 新增 `PURCHASE_LIST.md`：完整采购清单
  - 主板 2 块对比（畅网 ¥450-550 / 倍控 ¥598-698）
  - 配套物料 9 大类（内存/SSD/WiFi/电源/散热/线材/机箱/硬盘）
  - 预算汇总：精简方案 ~¥1,050-1,350，完整方案 ~¥1,510-1,855
  - 淘宝搜索关键词一键复制
  - 四阶段下单顺序建议

### Phase 0 行动清单
- [ ] 采购畅网 + 倍控 N150 主板各 1 块
- [ ] 采购配套物料：DDR4+DDR5 内存各 1、NVMe 128G ×2、MT7922 WiFi、电源 ×2
- [ ] 3D 打印「留白」机箱验证结构
- [ ] Debian 13 兼容性测试 + 功耗/温度基准测试

### 文档
- 更新 `HARDWARE_SPEC.md`：决策表全部标记为已定
- 新增 `PURCHASE_LIST.md`：Phase 0 采购清单
- 更新 `README.md`：文档导航加入采购清单

---

## [2026-07-12] - ARM 架构兼容性修复

### 部署脚本
- `scripts/setup.sh`：新增 `detect_arch()` 函数，自动检测 CPU 架构（amd64/arm64/armv7）
- FileBrowser 下载 URL 从硬编码 `linux-amd64` → 动态 `linux-${ARCH}`
- MinIO 下载 URL 从硬编码 → 动态，armv7 自动映射为 minio 使用的 `arm` 标识
- nas-panel 下载 URL 加入架构后缀 `nas-panel-${ARCH}.latest`
- 手动编译提示加入 `GOARCH=$GOARCH` 参数

### Makefile
- 修复 `build-all` 中 ARMv7 交叉编译 bug：`GOARCH=arm/v7` → 正确解析为 `GOARCH=arm GOARM=7`
- 输出文件名修正：`nas-panel-linux-arm` → `nas-panel-linux-armv7`

### 验证结论
- Go Web Panel：直接 `GOARCH=arm64 go build`，无代码修改
- systemd 服务/配置文件：无架构依赖
- Samba/NFS/FTP：Debian ARM APT 源原生支持
- 所有 systemd 服务文件引用 `/usr/local/bin/` 等通用路径，跨架构兼容

---

## [2026-07-12] - 硬件产品定义

### 硬件规格
- 新增 `HARDWARE_SPEC.md`：完整硬件规格推荐书
  - CPU 选型对比：ARM RK3568 vs x86 N150，**推荐 N150 先行**
  - 双版本 BOM 清单：标准版 ¥1063 / 经济版 ¥455
  - 三套机箱外观方案：「留白」极简 / 「透明探索版」赛博 / 「收音机」复古
  - 自研主板架构设计（芯片连接、背板、散热风道）
  - 四阶段开发路径：原型验证 → 工程样机 → 小批量试产 → 量产发布
  - 竞品对比表 + 定价推演（699-799 / 299-399）
- 更新 `README.md`：加入项目文档导航表

### 待决策
- [ ] CPU 路线：确认 x86 N150 先行
- [ ] 主板策略：确认先采购 ODM 方案
- [ ] 机箱风格：三选一或混合策略
- [ ] 首批数量：建议 100-200 台

---

## [2026-07-12] - 开源基础建设（阶段一）

### 许可证
- LICENSE 修正：GPLv3 → AGPLv3（与 README 声明一致）
- 理由：NAS 核心是 Web 服务，AGPLv3 堵住"改代码不公开"的 ASP 漏洞

### 工程化
- 新增 `Makefile`：12 个目标（build, test, lint, fmt, release 等）
- 新增 `.golangci.yml`：17 项 linter 配置，含启用/禁用理由注释
- 新增 `.editorconfig`：统一 Go/HTML/JS/CSS/Shell/Makefile 缩进与换行
- 新增 `.gitee-ci.yml`：Gitee CI 三阶段流水线（lint → test → build）
- 新增 `.github/workflows/ci.yml`：GitHub Actions 镜像

### 测试
- 新增 `web/common/auth_test.go`：JWT 创建/验证/中间件/过期/benchmark
- 新增 `web/common/common_test.go`：JSONResponse、ReadEnvFile、ReadAllEnv、压测
- 新增 `web/common/module_test.go`：Module 接口编译时检查 + 运行时验证
- 新增 `web/common/sudo_test.go`：sudo/exec 命令测试（仅 Linux 编译）
- 测试覆盖 common/ 包核心逻辑，跨平台可运行

### 文档
- 新增 `CONTRIBUTING.md`：外部贡献流程、代码规范、Commit 格式
- 新增 `DEVELOPMENT.md`：团队开发手册（环境搭建、模块模板、测试指南、跨平台注意事项）
- 更新 `README.md`：加入项目愿景（开源+产品化双目标）、文档导航表

---

## [2026-07-10] - 模块化重构 + 配置管理 + 备份恢复

### 重构：后端模块化架构
- 拆分 common/ 共享包 (auth/json/sudo/env/module)
- 引入 encoding/json 替代手写 toJSON
- 6 个现有模块拆分到 modules/ 目录
- main.go 精简为路由注册 + 启动
- 删除旧的 auth.go/handlers.go/services.go/system.go/monitor.go

### 新增：配置管理模块 (config)
- Samba 共享在线添加/删除（表单提交，自动重启 smbd）
- FTP 用户白名单在线增删（自动重启 vsftpd）
- 配置文件在线编辑器（smb.conf/vsftpd.conf/exports/nfs.conf/jail.local/.env）
- 服务开机自启管理（enable/disable）

### 新增：磁盘管理模块 (diskmgmt)
- 分区信息（fdisk -l）
- 创建目录（限 /data/ 下）
- 挂载/卸载分区
- 格式化分区（禁止系统盘，需二次确认，支持 ext4/xfs/btrfs）
- LVM/I/O/SMART 详情

### 新增：系统设置模块 (system)
- 网络配置（IP/路由/DNS）
- 时间与时区
- 主机名在线修改
- SSH 配置查看
- 内核参数（sysctl）
- 系统更新状态
- 开机自启服务列表

### 新增：备份恢复系统
- backup-config.sh: 备份所有 NAS 配置到 /data/backups/
  - 系统配置/服务文件/项目配置/Samba 用户数据库/crontab/状态快照
  - 打包 tar.gz，保留最近 5 个
- restore-config.sh: 从备份恢复配置
  - 交互式选择，7 步恢复流程（停止服务→恢复配置→重启服务）
- setup.sh 升级前自动备份
- cron 每周日凌晨 3 点定期备份
- Web 面板备份恢复模块（创建/列表/恢复/删除）

### 新增：Web 面板备份恢复模块 (backup)
- /api/backup/list: 列出所有备份
- /api/backup/create: 手动创建备份
- /api/backup/restore: 从指定备份恢复
- /api/backup/delete: 删除备份文件
- 前端: 备份列表表格 + 立即备份按钮 + 恢复/删除操作

### 更新：setup.sh sudoers 白名单
- 新增 systemctl enable/disable
- 新增 tee 读写各配置文件
- 新增 fdisk/mount/umount/mkdir/mkfs 磁盘操作
- 新增 journalctl/cat 配置读取
- 新增 backup-config.sh/restore-config.sh 备份恢复
- 新增 rm 备份文件清理

### 更新：README 开源完善
- Web 面板功能表（10 个模块）
- 从源码编译说明
- 告警通知配置表
- 技术栈表
- 扩展开发指南
- 配置备份与恢复章节
- MinIO 引用全部替换为 rclone serve s3
- 第三方许可证链接

### 更新：产品技术手册
- S3 对象存储：MinIO → rclone serve s3

### 更新：TODO 路线图
- 项12: Web 面板"关于我们"页面（低优先级）
- 项13: 存储管理与新盘引导（中优先级，三层方案）
- 项11: backup-config.sh/restore-config.sh 标记已完成
- 进度统计: 13项, 完成7, 待办6

## [2026-07-09] - Web 管理面板 + 监控告警系统

### 新增：NAS Web 管理面板 (nas-panel)
- Go 单二进制 Web 管理面板，端口 8090，内存占用 2.7MB
- 技术栈: Go + go:embed + Alpine.js + JWT 认证
- 前端浅色主题，大字体高对比度，左侧导航栏
- 二进制多源下载: file.abwen.com/control/nas-panel.latest (主) + GitHub (备)

#### 面板功能模块
- 仪表盘: 主机名/系统/运行时间/CPU/内存/磁盘使用率 + 服务一览
- 服务管理: 8个服务启动/停止/重启 + 日志查看 (journalctl)
- 用户管理: 添加/删除用户 (联动 Samba/系统/htpasswd) + 改密码
- 存储信息: 磁盘使用/目录大小/Samba 配置/NFS 导出/SMART 状态
- 防火墙: UFW 状态/规则查看 + 端口允许/拒绝
- 监控告警: 实时状态 + 网络流量 + 告警配置

#### 监控告警页面
- 当前状态卡片 (每 180 秒自动刷新):
  - 磁盘: 已用/总量 + 进度条 + 百分比
  - 内存: 已用/总量 + 进度条 + 百分比
  - CPU: 负载值 + 核心数
  - 服务: 异常数 + 总数
  - 进程数
- 物理磁盘: lsblk 显示型号/大小/类型/挂载
- Inode 使用: df -i
- LVM 卷管理: pvs/vgs/lvs
- 内存详情: free -h 含 Swap
- CPU 详情: /proc/cpuinfo 型号/频率/缓存/governor
- 内存占用 Top 10 进程: ps aux 按内存排序
- 登录用户: who 显示用户/终端/来源/时间
- 系统错误日志: journalctl -p err 最近 24 小时 10 条
- 网络流量: /proc/net/dev 双采样计算实时上下行速率 + 总流量
- 已配置通知渠道徽章: 钉钉/Telegram/Bark/Email

#### 告警配置 (Web 页面可编辑)
- 钉钉机器人: Webhook URL + 加签密钥
- Telegram Bot: Token + Chat ID
- Bark (iOS 推送): Key + Server (可自建)
- Email (SMTP): 服务器/端口/用户名/密码/收件人
- 告警阈值: 磁盘(80%)/内存(90%)/负载(4) 可自定义
- 配置保存到 .env，页面直接编辑无需 SSH

### 新增：监控告警脚本 (monitor.sh)
- Shell 脚本 + cron 每 5 分钟检查
- 零额外服务、零额外内存
- 监控项: 磁盘空间/服务状态/内存/CPU 负载/SMART 磁盘健康
- 多通道告警: 钉钉/Telegram/Bark/Email (配哪个启用哪个)
- 告警去重: 同一告警 1 小时内不重复发送
- setup.sh 自动配置 cron + sudoers
- cleanup.sh 自动清理

### 新增：setup.sh [10/10] 步骤
- 部署 nas-panel 二进制 (本地/下载/手动编译 三级回退)
- 配置 systemd 服务 (nas-panel.service)
- 配置 sudoers 免密白名单 (pdbedit/systemctl/smartctl/ufw/chpasswd/smbpasswd/htpasswd/journalctl/pvs/vgs/lvs/tee)
- 配置监控 cron (每 5 分钟)
- 创建监控状态目录 /var/lib/nas-monitor

### 修复
- nas-panel 所有系统命令加 sudo 前缀 (pdbedit/smartctl/ufw/systemctl 等)
- toJSON() 支持 map[string]string 类型 (修复用户列表不显示)
- .env.example 完善说明 (表格 + 必填/选填 + 示例)
- setup.sh .env 不存在时显示创建步骤和必填项

### 验证
- 2026-07-09 在 192.168.213.85 (用户 dog) 部署验证通过
- 9/9 服务全部 active (含 nas-panel)
- Web 面板 API 全部正常: 登录/仪表盘/服务/用户/存储/防火墙/监控/告警配置
- 监控数据实时采集: 磁盘/内存/CPU/进程/网络流量/登录用户/错误日志
- 告警配置保存到 .env 正常

## [2026-07-08] - 安全清洗 & 用户名通用化 (v1.0.0)

### 安全
- git filter-repo 重写全部历史，清除所有明文密码
  - nas123456, nas123456789, minioadmin123 等密码从历史中彻底清除
  - Gitee OAuth token 确认从未进入代码库
- 密码改用 .env 文件读取（.env.example 作为模板）
  - setup.sh 从 $SCRIPT_DIR/.env 读取 NAS_PASS
  - 密码长度校验（至少 12 位，FileBrowser 强制要求）
  - .gitignore 已排除 .env
- minio.service 改用 EnvironmentFile=/etc/default/minio 方式
- 文档中密码统一用 <NAS_PASS> 占位符

### 重构
- 用户名从硬编码 jacky 改为自动获取当前用户
  - setup.sh: NAS_USER="${SUDO_USER:-$USER}" 自动检测 sudo 执行者
  - 直接以 root 运行会报错提示
  - 配置文件用 __NAS_USER__ 占位符，setup.sh 部署时 sed 替换
  - configs/smb.conf, vsftpd.userlist, minio.service, rclone-webdav.service 全部改造
- cleanup.sh: 删除 Samba 用户改为动态获取
- remove-user.sh: 保护用户从硬编码 jacky 改为动态获取部署用户

### 新增
- .env.example 环境变量模板

### 验证
- 2026-07-08 在 192.168.213.85 全新系统上 cleanup -> setup 完整验证通过
- 8/8 服务全部 active
- Samba 列出 5 个共享, NFS 挂载成功, FTP 正常, WebDAV HTTP 200
- FileBrowser JWT 登录成功, MinIO Health 200, Console 200
- 用户名自动检测 (jacky)、.env 读取密码、占位符替换全部正常

## [2026-07-02] - WebDAV 认证优化 & 清理脚本完善

### 新增
- cleanup.sh 增加 `/etc/rclone-htpasswd` 文件清理
- cleanup.sh 增加 `apache2-utils` 包卸载（htpasswd 工具）

### 修复
- WebDAV 认证改用 htpasswd 文件方式（Apache 标准 bcrypt 哈希）
  - 原因：rclone obscure 生成的哈希在多次部署验证中出现兼容性问题
  - 方案：使用 `htpasswd -cb` 生成密码文件，rclone 通过 `--htpasswd` 参数读取
  - 影响：setup.sh、configs/rclone-webdav.service 同步更新
- setup.sh 步骤 [6/9] 增加 `apt-get install apache2-utils` 自动安装 htpasswd 工具

### 验证
- cleanup.sh → setup.sh 完整循环测试通过
- WebDAV HTTP 200 认证正常

## [2026-07-02] - 完整部署验证通过

### 新增
- MinIO S3 兼容对象存储服务（端口 9000 API，端口 9002 控制台）
- setup.sh 自动安装 MinIO（多源下载回退机制）
- 防火墙规则：开放 9000/9002 端口

### 修复
- setup.sh 第 238 行语法错误（MinIO service 文件生成时缺少 >> 重定向符）
- 根因：Python 字符串拼接中 `>>` 被解释为 shell 重定向

### 验证结果
所有 8 个服务在 Debian 13 全新系统上部署验证通过：
- ✓ Samba (smbd/nmbd) - 6 个共享目录
- ✓ NFS (nfs-kernel-server) - 5 个导出目录
- ✓ FTP (vsftpd) - 正常响应
- ✓ WebDAV (rclone serve) - 服务运行
- ✓ FileBrowser - Web 界面正常，JWT 认证工作
- ✓ MinIO API - 健康检查返回 200
- ✓ MinIO Console - Web 管理界面正常
- ✓ Fail2ban - sshd/vsftpd 防护已启用

### 访问信息
| 服务 | 地址 | 用户名 | 密码 |
|------|------|--------|------|
| Samba | //192.168.213.85/shared | <NAS_USER> | <NAS_PASS> |
| NFS | mount 192.168.213.85:/data/shared | - | - |
| FTP | ftp://192.168.213.85 | <NAS_USER> | <NAS_PASS> |
| WebDAV | http://192.168.213.85:8080 | <NAS_USER> | <NAS_PASS> |
| FileBrowser | http://192.168.213.85:8081 | <NAS_USER> | <NAS_PASS> |
| MinIO Console | http://192.168.213.85:9002 | admin | <NAS_PASS> |
| MinIO API | http://192.168.213.85:9000 | - | - |

## [2026-07-02] - NFS 端口固定

### 新增
- 在 `/etc/nfs.conf` 中固定 NFS 相关服务端口：
  - mountd: 20048
  - lockd: 32768
  - statd: 32769
- 防火墙规则：开放 111 (rpcbind), 20048 (mountd), 32768-32769 (lockd/statd)

### 修复
- NFS 从外部无法访问的问题（原因：mountd 使用随机端口）

## [2026-07-02] - 初始化项目

### 新增
- 基础 NAS 服务：Samba、NFS、FTP、WebDAV
- FileBrowser Web 文件管理器
- setup.sh 一键部署脚本（9 步流程）
- add-user.sh / remove-user.sh 用户管理脚本
- 产品技术手册 v1.0
- 完整的安全配置（UFW 防火墙 + Fail2ban）

### 首次部署
- 在 Debian 13 (trixie) 全新系统上完成部署验证
