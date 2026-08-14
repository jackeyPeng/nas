# Z1 存储管理界面与管理逻辑设计方案

> 版本: v1.0  
> 日期: 2026-08-14  
> 适用: Z1 NAS Web 管理面板 — 存储管理模块重构

---

## 一、设计理念

### 1.1 核心转变：从"技术视角"到"数据视角"

现有存储管理的问题在于**以技术组件为中心**（磁盘、RAID、LVM、文件系统各管各的），用户需要理解这些技术名词才能操作。新设计的核心是：

> **用户只关心三件事：我的盘在哪？我的数据安全吗？我能存多少？**

因此界面按 **"总览 → 物理 → 逻辑 → 操作"** 四层组织，而非按技术组件分。

### 1.2 设计原则

| 原则 | 说明 |
|------|------|
| **拓扑可视化** | 用树状结构一次性展示"共享文件夹 → 卷 → 池 → 磁盘"的完整关系 |
| **状态即操作** | 每个对象的状态直接决定可执行的操作，减少误操作 |
| **向导化创建** | 存储池创建用分步向导，隐藏 RAID 技术细节，用"目标"替代"级别选择" |
| **协议绑定** | 共享文件夹的协议开关直接绑定到文件夹对象，不再分散管理 |
| **渐进式披露** | 高级选项（条带大小、PE 大小等）默认隐藏，高级用户可展开 |

---

## 二、界面架构

### 2.1 导航结构

```
存储管理 (5个Tab)
├── 📊 存储总览      ← 默认页，展示四层拓扑
├── 💾 物理磁盘      ← 盘位图 + 磁盘详情
├── 🗄️ 存储池        ← 池列表 + 详情 + 维护操作
├── 🧙 创建向导      ← 分步创建存储池
└── 🔧 维护          ← 替换盘/扩容/清理/日志
```

### 2.2 存储总览页

#### 顶部统计卡片

横向排列 4 个关键指标：

| 指标 | 示例值 | 颜色 |
|------|--------|------|
| 物理磁盘 | 4 | 绿色 |
| 存储池 | 1 | 蓝色 |
| 逻辑卷 | 2 | 紫色 |
| 共享文件夹 | 5 | 灰色 |

#### 全局告警栏

当存在异常时，在统计卡片下方显示醒目的告警条：

```
⚠️ 注意：磁盘 sdd 温度 52°C，建议检查机箱散热
```

#### 存储拓扑树

核心可视化组件，用缩进 + 连线展示四层关系：

```
🗄️ pool-main  [RAID5] [健康]
   │
   ├── 📦 vol-data    3.5TB/4TB  XFS  快照:关
   │   ├── 📁 shared     1.2TB  [SMB✓] [NFS✓] [WebDAV✓] [S3✓]
   │   └── 📁 photos     800GB  [SMB✓] [NFS✗] [WebDAV✗] [S3✗]
   │
   └── 📦 vol-backup  200GB/500GB  XFS  快照:关
       └── 📁 backup-remote  150GB  [SMB✗] [NFS✗] [FTP✓]
```

**设计要点**：
- 每层的图标和颜色固定，培养用户认知
- 共享文件夹右侧显示协议开关状态（✓ 开 / ✗ 关）
- 点击任意对象可跳转到对应管理页

---

### 2.3 物理磁盘页（盘位图）

#### 盘位图网格

以机箱盘位为单位展示，而非按设备名排序：

```
┌────────┐  ┌────────┐  ┌────────┐  ┌────────┐  ┌────────┐  ┌────────┐
│  💾    │  │  💾    │  │  💾    │  │  💾⚠️  │  │   ➕   │  │   ➕   │
│  sda   │  │  sdb   │  │  sdc   │  │  sdd   │  │  Bay5  │  │  Bay6  │
│ 系统盘 │  │ 数据盘 │  │ 数据盘 │  │ 数据盘 │  │  空闲  │  │  空闲  │
│ 41°C  │  │ 38°C  │  │ 40°C  │  │ 52°C  │  │        │  │        │
│ ████░ │  │ ██████│  │ ██████│  │ ██████│  │        │  │        │
│210/465│  │1.5/1.8│  │1.5/1.8│  │1.5/1.8│  │        │  │        │
└────────┘  └────────┘  └────────┘  └────────┘  └────────┘  └────────┘
   绿色       蓝色        蓝色        黄色(警告)   虚线框      虚线框
```

**卡片状态颜色**：
- **绿色边框**：系统盘（不可选入 RAID）
- **蓝色边框**：正常数据盘
- **黄色边框**：警告状态（温度高、SMART 预警）
- **红色边框**：故障/离线
- **虚线框**：空闲盘位

**点击卡片后展开详情面板**：

```
┌─────────────────────────────────────────┐
│ 磁盘详情: sdd                    [关闭] │
├─────────────────────────────────────────┤
│  型号:    Seagate IronWolf 2TB          │
│  序列号:  Z1F2A3B4C5D6                  │
│  容量:    1.82 TB                       │
│  接口:    SATA III                      │
│  温度:    52°C ⚠️                       │
│  通电时间: 8,320 小时                   │
│  SMART:   通过                          │
│  坏扇区:  0                             │
├─────────────────────────────────────────┤
│ [查看 SMART 详情] [运行自检]    [安全卸载]│
└─────────────────────────────────────────┘
```

---

### 2.4 存储池页

#### 池列表

```
┌─────────────────────────────────────────────────────────────────────┐
│ 🗄️ pool-main  [RAID5] [健康]                              [详情]   │
│    sdb + sdc + sdd · 4TB 可用 / 6TB 总容量 · XFS                    │
│    ████████████████████████████████████████░░░░░░░░  67%            │
└─────────────────────────────────────────────────────────────────────┘
```

#### 池详情面板

左右分栏展示 RAID 信息和 LVM 信息：

```
RAID 信息                          LVM 信息
─────────                          ────────
级别:     RAID 5                   VG 名:   vg-pool-main
条带大小: 64KB                     PE 大小:  4MB
冗余:     1 盘容错                 总 PE:    1,536,000
重建速度: 正常                     空闲 PE:  0
状态:     同步完成
```

---

### 2.5 创建向导（5步）

#### Step 1: 选择磁盘

以盘位图形式展示**空闲磁盘**，用户点击选择（可多选）：

```
┌────────┐  ┌────────┐  ┌────────┐  ┌────────┐
│  💾    │  │  💾    │  │  💾    │  │  💾    │
│  sdb   │  │  sdc   │  │  sdd   │  │  sde   │
│ WD Red │  │ WD Red │  │Seagate│  │Toshiba│
│  空闲  │  │  空闲  │  │  空闲  │  │  空闲  │
│  2TB   │  │  2TB   │  │  2TB   │  │  4TB   │
└────────┘  └────────┘  └────────┘  └────────┘
```

选中后卡片高亮，底部显示"已选 X 块盘"。

#### Step 2: 定目标

三个大卡片，用户单选：

```
┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│    🛡️        │  │    💾        │  │    ⚡        │
│  数据安全    │  │  最大容量    │  │  读写性能    │
│              │  │              │  │              │
│ 能容忍磁盘   │  │ 最大化可用   │  │ 追求传输     │
│ 损坏         │  │ 空间         │  │ 速度         │
│              │  │              │  │              │
│ 推荐 RAID    │  │ 推荐 RAID    │  │ 推荐 RAID    │
│ 1/5/6        │  │ 0 / JBOD     │  │ 0/10         │
└──────────────┘  └──────────────┘  └──────────────┘
```

#### Step 3: 确认方案

系统根据选择推荐 RAID 级别，同时展示详细对比：

```
┌─────────────────────────────────────────────┐
│ 🛡️ RAID 5 — 兼顾安全与容量 (推荐)           │
├─────────────────────────────────────────────┤
│ • 可用容量: 6TB (3盘 × 2TB，1盘冗余)        │
│ • 容错能力: 1 块盘损坏不丢数据               │
│ • 读取性能: 优秀 (多盘并发)                  │
│ • 写入性能: 良好 (需计算校验)                │
│ • 重建时间: 约 4-8 小时 (2TB 盘)            │
├─────────────────────────────────────────────┤
│ ⚠️ 检测到不同容量磁盘，4TB 盘将有 2TB 未使用  │
│    建议单独创建池以充分利用空间              │
├─────────────────────────────────────────────┤
│ 手动切换: [RAID 5 ▼]                        │
└─────────────────────────────────────────────┘
```

#### Step 4: 创建逻辑卷

```
卷名称:     [vol-data          ]
文件系统:   [XFS (推荐)    ▼]
           [Btrfs (支持快照/压缩/校验)]

容量分配:   [全部可用空间]  [自定义]

共享文件夹 (可选): [shared          ]
```

#### Step 5: 完成

显示初始化进度条，后台异步执行：

```
✅ 存储池创建成功
pool-main 已创建，正在初始化 RAID 5...

初始化进度
████████████████████░░░░░░░░░░░░░░░░░░░░  23%
预计剩余 45 分钟

[返回总览]
```

---

### 2.6 维护页

#### 快捷操作卡片

```
┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│    🔄        │  │    ➕        │  │    🧹        │  │    📊        │
│ 替换故障盘   │  │ 扩容存储池   │  │ 数据清理     │  │ SMART 检测   │
│              │  │              │  │              │  │              │
│ 拔出坏盘     │  │ 添加新磁盘   │  │ 扫描静默     │  │ 批量扫描     │
│ 插入新盘     │  │ 到现有池     │  │ 数据损坏     │  │ 所有磁盘     │
│ 自动重建     │  │              │  │              │  │              │
└──────────────┘  └──────────────┘  └──────────────┘  └──────────────┘
```

#### 操作日志

```
2026-08-14 10:23  [成功]  pool-main 扩容完成，新增 2TB 可用空间
2026-08-10 03:15  [成功]  每周自动数据清理完成，0 错误
2026-08-08 14:32  [警告]  磁盘 sdd 温度超过阈值 (52°C)
2026-08-01 09:00  [成功]  存储池 pool-main 创建成功 (RAID 5)
```

---

## 三、管理逻辑设计

### 3.1 四层模型详细定义

```
┌─────────────────────────────────────────────────────────┐
│  第4层: 共享文件夹 (Shared Folder)                      │
│  ├── shared    → vol-data    → pool-main                │
│  ├── photos    → vol-data    → pool-main                │
│  └── backup    → vol-backup  → pool-main                │
├─────────────────────────────────────────────────────────┤
│  第3层: 逻辑卷 (Volume)                                 │
│  ├── vol-data    [XFS, 3.5TB/4TB, 快照:关]             │
│  └── vol-backup  [XFS, 200GB/500GB, 快照:关]           │
├─────────────────────────────────────────────────────────┤
│  第2层: 存储池 (Storage Pool)                           │
│  └── pool-main   [RAID5, 3盘, 健康]                     │
├─────────────────────────────────────────────────────────┤
│  第1层: 物理磁盘 (Physical Disk)                        │
│  ├── sda [系统盘, 500GB, Samsung]                       │
│  ├── sdb [数据盘, 2TB, WD Red, pool-main]              │
│  ├── sdc [数据盘, 2TB, WD Red, pool-main]              │
│  └── sdd [数据盘, 2TB, Seagate, pool-main]             │
└─────────────────────────────────────────────────────────┘
```

**层级职责**：

| 层级 | 用户操作 | 系统行为 | 技术映射 |
|------|---------|---------|---------|
| 共享文件夹 | 增删改、开关协议、设权限 | 更新 Samba/NFS/FTP/WebDAV/S3 配置 | 目录 + 各服务配置 |
| 逻辑卷 | 创建、扩容、删除、开关快照 | LVM lvcreate/lvresize/lvremove | LVM LV |
| 存储池 | 创建、扩容、替换盘、清理 | mdadm + LVM VG 管理 | mdadm RAID + LVM VG |
| 物理磁盘 | 查看状态、安全卸载、替换 | 扫描、挂载、SMART 监控 | /dev/sdX + smartctl |

---

### 3.2 磁盘状态机

```
                    ┌─────────────┐
                    │   未识别    │  ← 新插入的盘
                    └──────┬──────┘
                           │ 系统扫描 (udev 触发)
                           ▼
                    ┌─────────────┐
                    │   已识别    │  ← 显示型号/容量/温度
                    └──────┬──────┘
                           │ 用户选择用途
              ┌────────────┼────────────┐
              ▼            ▼            ▼
         ┌────────┐  ┌────────┐  ┌────────┐
         │ 系统盘  │  │ 数据盘  │  │ 外部盘  │
         │ (sda)  │  │ (RAID) │  │ (USB)  │
         └────────┘  └────┬───┘  └────────┘
                            │ 加入存储池
                            ▼
                     ┌─────────────┐
                     │  池成员盘    │  ← 受 RAID 保护
                     └──────┬──────┘
                            │ 故障/拔出
                            ▼
                     ┌─────────────┐
                     │   故障/离线  │  ← 触发告警，允许替换
                     └─────────────┘
```

**状态规则**：

| 状态 | 允许的操作 | 禁止的操作 |
|------|-----------|-----------|
| 未识别 | 无 | 所有操作（等待扫描） |
| 已识别 | 标记为系统盘/数据盘/外部盘 | 直接格式化 |
| 系统盘 | 查看详情、SMART 检测 | 加入 RAID、卸载 |
| 数据盘(空闲) | 加入池、查看详情 | 直接创建文件系统 |
| 池成员盘 | 查看详情、替换、扩容 | 直接卸载、格式化 |
| 外部盘 | 挂载/卸载、查看详情 | 加入 RAID |
| 故障/离线 | 替换 | 所有写操作 |

---

### 3.3 存储池生命周期

#### 创建流程

```
选盘 → 定目标 → 推荐RAID → 确认 → 初始化 → 可用
                    │
                    └── 异步后台执行，前端轮询进度
```

**初始化阶段细分**：
1. **校验磁盘**（5%）：容量一致性检查、SMART 快速扫描
2. **创建 RAID**（60%）：mdadm --create，大容量盘耗时较长
3. **创建 VG**（10%）：vgcreate
4. **创建 LV**（10%）：lvcreate
5. **格式化**（15%）：mkfs.xfs / mkfs.btrfs

#### 维护流程

**扩容**：
```
加盘 → 选择扩展方式 → 在线扩容

扩展方式:
├── 扩容容量    → vgextend + lvextend
└── 增加冗余    → 如 RAID5 → RAID6（需停服务）
```

**替换盘**：
```
标记故障盘 → 拔出 → 插入新盘 → 自动重建

自动流程:
1. mdadm --fail + --remove
2. 检测新盘插入
3. mdadm --add
4. 后台重建，UI 显示进度
```

**清理 (Scrub)**：
```
触发 scrub → 后台扫描 → 报告结果

周期:
- 手动触发
- 每周日凌晨 3 点自动执行（可配置）
- RAID 降级后自动触发一次
```

**删除**：
```
确认无共享文件夹 → 卸载 → 删除池 → 盘变空闲

强制检查:
- 池下是否有卷？
- 卷下是否有共享文件夹？
- 共享文件夹是否被服务引用？
```

---

### 3.4 目标导向 RAID 推荐引擎

#### 输入参数

```go
type RAIDRecommendationInput struct {
    Disks       []DiskInfo   // 磁盘列表（容量、型号）
    Goal        string       // "safety" | "capacity" | "performance"
    MinRedundancy int        // 最小冗余盘数（默认 1）
}
```

#### 推荐逻辑

```
输入: disks = [2TB, 2TB, 2TB, 4TB], goal = "safety"

处理:
1. 容量一致性检查
   → 检测到 4TB 盘，最小盘 = 2TB
   → 警告: "4TB 盘将有 2TB 未使用"

2. 根据盘数推荐基础方案
   ┌─────────┬────────────────────────────────────────┐
   │ 盘数    │ 基础推荐                               │
   ├─────────┼────────────────────────────────────────┤
   │ 1       │ single (无 RAID)                       │
   │ 2       │ RAID 1 (镜像)                          │
   │ 3       │ RAID 5 (单冗余)                        │
   │ 4+      │ RAID 6 (双冗余) 或 RAID 10 (性能)       │
   └─────────┴────────────────────────────────────────┘

3. 根据目标微调
   goal="safety"  → RAID 6 (≥4盘) 或 RAID 5 (3盘)
   goal="capacity" → RAID 0 (无冗余) 或 RAID 5
   goal="performance" → RAID 10 (≥4盘且偶数) 或 RAID 0

4. 生成推荐报告
   → 推荐: RAID 5
   → 可用容量: 6TB (3盘 × 2TB，1盘冗余)
   → 容错: 1盘
   → 读取性能: 优秀
   → 写入性能: 良好
   → 重建时间: 约 4-8 小时
   → 警告: 容量不一致提示
```

#### 用户可手动覆盖

推荐结果下方提供下拉框，允许用户手动选择：
- RAID 0
- RAID 1
- RAID 5
- RAID 6
- RAID 10（仅偶数盘时可用）
- JBOD

选择后实时重新计算容量和容错信息。

---

### 3.5 共享文件夹与协议绑定

现有方案中共享文件夹和协议分散管理，新设计把**协议开关直接绑定到共享文件夹**：

```
共享文件夹: "photos"
├── 位置:     vol-data (pool-main/RAID5)
├── 权限:
│   ├── admin    → 读写
│   └── guest    → 只读
└── 协议:
    ├── SMB      → ✅ 开 (端口 445)
    ├── NFS      → ❌ 关
    ├── FTP      → ❌ 关
    ├── WebDAV   → ✅ 开 (端口 8080)
    └── S3       → ❌ 关
```

**配置变更流程**：

```
用户操作: 关闭 photos 的 SMB 协议
    │
    ▼
后端: 从 smb.conf 中移除 [photos] 段落
    │
    ▼
后端: systemctl reload smbd
    │
    ▼
前端: 协议 tag 变为灰色
```

**好处**：
- 用户创建共享文件夹时**一次性配置完所有访问方式**
- 协议状态在总览页一目了然
- 关闭协议时自动清理对应服务配置，避免残留

---

## 四、数据模型（Go）

```go
package models

import "time"

// ========== 物理磁盘 ==========
type Disk struct {
    ID           string `json:"id"`           // 设备名，如 "sdb"
    Name         string `json:"name"`         // 用户自定义标签
    Model        string `json:"model"`        // 型号
    Serial       string `json:"serial"`       // 序列号
    Capacity     uint64 `json:"capacity"`     // 字节
    Type         string `json:"type"`         // SSD / HDD
    Status       string `json:"status"`       // uninitialized/idle/data/system/external/member/failed/offline
    Temperature  int    `json:"temperature"`  // °C
    PowerOnHours int    `json:"power_on_hours"`
    SmartStatus  string `json:"smart_status"` // passed/failing/failed/unknown
    PoolID       string `json:"pool_id"`      // 空表示空闲
    Slot         int    `json:"slot"`         // 物理盘位号
    IsSystemDisk bool   `json:"is_system_disk"`
    MountPoint   string `json:"mount_point"`  // 挂载点
    FsType       string `json:"fs_type"`      // 文件系统类型
}

// ========== 存储池 ==========
type Pool struct {
    ID              string    `json:"id"`
    Name            string    `json:"name"`
    Level           string    `json:"level"`            // raid0/1/5/6/10/jbod/single
    Status          string    `json:"status"`         // healthy/degraded/rebuilding/failed/creating
    Disks           []string  `json:"disks"`          // 磁盘ID列表
    TotalSize       uint64    `json:"total_size"`     // 字节
    UsedSize        uint64    `json:"used_size"`      // 字节
    AvailableSize   uint64    `json:"available_size"` // 字节
    VGName          string    `json:"vg_name"`
    FileSystem      string    `json:"file_system"`    // xfs/btrfs
    CreatedAt       time.Time `json:"created_at"`
    UpdatedAt       time.Time `json:"updated_at"`
    RebuildProgress float64   `json:"rebuild_progress"` // 0-100
    RebuildETA      int       `json:"rebuild_eta"`      // 秒
}

// ========== 逻辑卷 ==========
type Volume struct {
    ID               string    `json:"id"`
    Name             string    `json:"name"`
    PoolID           string    `json:"pool_id"`
    Size             uint64    `json:"size"`             // 字节
    Used             uint64    `json:"used"`             // 字节
    FS               string    `json:"fs"`               // xfs/btrfs
    MountPath        string    `json:"mount_path"`
    SnapshotsEnabled bool      `json:"snapshots_enabled"`
    SnapshotCount    int       `json:"snapshot_count"`
    CreatedAt        time.Time `json:"created_at"`
}

// ========== 共享文件夹 ==========
type Share struct {
    ID          string            `json:"id"`
    Name        string            `json:"name"`
    VolumeID    string            `json:"volume_id"`
    Path        string            `json:"path"`        // 绝对路径
    Protocols   map[string]bool   `json:"protocols"`   // smb/nfs/ftp/webdav/s3
    Permissions []Permission      `json:"permissions"`
    RecycleBin  bool              `json:"recycle_bin"`
    Quota       uint64            `json:"quota"`       // 0 表示无限制
    CreatedAt   time.Time         `json:"created_at"`
}

type Permission struct {
    UserID string `json:"user_id"`
    Read   bool   `json:"read"`
    Write  bool   `json:"write"`
}

// ========== 异步任务 ==========
type Task struct {
    ID        string    `json:"id"`
    Type      string    `json:"type"`      // pool_create/pool_expand/disk_replace/scrub/volume_create
    Status    string    `json:"status"`    // pending/running/done/failed/cancelled
    Progress  float64   `json:"progress"`  // 0-100
    Message   string    `json:"message"`   // 用户友好的描述
    Detail    string    `json:"detail"`    // 技术详情（日志）
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}

// ========== 存储拓扑（总览页用）==========
type StorageTopology struct {
    Disks  []Disk         `json:"disks"`
    Pools  []PoolTopology `json:"pools"`
    Stats  StorageStats   `json:"stats"`
    Alerts []Alert        `json:"alerts"`
}

type PoolTopology struct {
    Pool     Pool           `json:"pool"`
    Volumes  []VolumeTopology `json:"volumes"`
}

type VolumeTopology struct {
    Volume Volume `json:"volume"`
    Shares []Share `json:"shares"`
}

type StorageStats struct {
    TotalDisks      int `json:"total_disks"`
    TotalPools      int `json:"total_pools"`
    TotalVolumes    int `json:"total_volumes"`
    TotalShares     int `json:"total_shares"`
    TotalCapacity   uint64 `json:"total_capacity"`
    TotalUsed       uint64 `json:"total_used"`
}

type Alert struct {
    Level   string `json:"level"`   // info/warning/critical
    Message string `json:"message"`
    DiskID  string `json:"disk_id,omitempty"`
    PoolID  string `json:"pool_id,omitempty"`
}
```

---

## 五、API 设计

### 5.1 REST API

```
========== 磁盘 ==========
GET    /api/disks              → []Disk
GET    /api/disks/:id          → Disk
POST   /api/disks/:id/identify → 闪烁指示灯（硬件支持时）
POST   /api/disks/:id/wipe     → 安全擦除（确认后执行）
POST   /api/disks/rescan       → 重新扫描所有磁盘

========== 存储池 ==========
GET    /api/pools              → []Pool
GET    /api/pools/:id          → Pool (含 Volumes)
POST   /api/pools              → 创建池（异步，返回 task_id）
       Body: {name, level, disk_ids, fs}
POST   /api/pools/:id/expand   → 扩容
       Body: {disk_id, mode: "capacity"|"redundancy"}
POST   /api/pools/:id/replace  → 替换盘
       Body: {old_disk_id, new_disk_id}
POST   /api/pools/:id/scrub    → 触发清理
DELETE /api/pools/:id          → 删除（强制检查无卷）

========== 逻辑卷 ==========
GET    /api/volumes            → []Volume
POST   /api/volumes            → 创建卷
       Body: {pool_id, name, size, fs}
POST   /api/volumes/:id/resize → 扩容/缩容
       Body: {size}
DELETE /api/volumes/:id        → 删除（强制检查无共享文件夹）

========== 共享文件夹 ==========
GET    /api/shares             → []Share
POST   /api/shares             → 创建
PUT    /api/shares/:id          → 更新（名称、权限、配额）
PUT    /api/shares/:id/protocols → 更新协议开关
       Body: {smb: true, nfs: false, ...}
DELETE /api/shares/:id         → 删除

========== 存储拓扑（总览页）==========
GET    /api/storage/topology   → StorageTopology

========== RAID 推荐 ==========
POST   /api/raid/recommend     → RAIDRecommendation
       Body: {disk_ids, goal}

========== 任务队列 ==========
GET    /api/tasks              → []Task
GET    /api/tasks/:id          → Task
DELETE /api/tasks/:id          → 取消任务（仅 pending 状态）
```

### 5.2 WebSocket（可选，用于实时进度）

```
WS /api/ws/tasks
→ 订阅任务进度更新，避免前端轮询

消息格式:
{
    "task_id": "task-xxx",
    "type": "pool_create",
    "status": "running",
    "progress": 23.5,
    "message": "RAID 初始化中... 23%",
    "eta": 2700  // 秒
}
```

---

## 六、错误处理与边界情况

| 场景 | 前端行为 | 后端保护 |
|------|---------|---------|
| **拔出池中的盘** | 弹窗警告："此盘属于 pool-main (RAID5)，拔出会导致降级。请先使用「替换盘」功能。" | udev 规则检测到移除后，标记为 offline，触发告警；拒绝卸载已挂载的池成员盘 |
| **RAID 降级时删除池** | 二次确认："RAID 已降级，数据处于风险中。确定删除？" + 要求输入池名确认 | 允许删除，但记录审计日志 |
| **创建卷时容量超过池** | 输入框实时校验，红色提示"超出可用空间" | 后端校验 `requested > pool.AvailableSize`，返回 400 |
| **不同容量盘组 RAID** | 推荐页显示黄色警告，允许但提示浪费 | 按最小盘容量计算 RAID 容量 |
| **热插拔新盘** | 自动识别，状态变为"已识别"，推送通知 | udev 规则触发 `rescan` API |
| **RAID 重建中关机** | 提示："RAID 正在重建，关机可能导致数据不一致。建议等待完成。" | 支持 mdadm 的 reshape 中断恢复，下次启动自动继续 |
| **系统盘误选入 RAID** | 系统盘卡片灰色不可选 | 后端校验 `IsSystemDisk`，拒绝加入 |
| **单盘创建 RAID 5** | 向导 Step 1 禁用"下一步"，提示"RAID 5 至少需要 3 块盘" | 后端校验 `len(disks) >= minDisksForLevel[level]` |
| **共享文件夹删除时协议未关** | 自动清理所有协议配置，无需用户手动操作 | 删除 Share 时，后端自动移除 smb.conf、exports、vsftpd 等配置 |

---

## 七、分阶段实现建议

### Phase 1: MVP（1-2 周）

**目标**：总览页拓扑 + 磁盘盘位图 + 创建向导

| 模块 | 内容 |
|------|------|
| 总览页 | 统计卡片 + 告警栏 + 拓扑树（只读） |
| 磁盘页 | 盘位图网格 + 点击展开详情 |
| 向导 | 5 步创建流程（Step 1-3 完整，Step 4 简化） |
| 后端 | Disk/Pool/Volume/Share 模型 + 基础 API |

**技术要点**：
- 拓扑树用纯 CSS Flex/Grid 实现，不需要引入图表库
- 盘位图根据 `Disk.Slot` 定位，响应式适配
- 向导用 Alpine.js 的 `x-show` 切换步骤

### Phase 2: 维护能力（1 周）

| 模块 | 内容 |
|------|------|
| 存储池页 | 池列表 + 详情面板（RAID/LVM 信息） |
| 维护页 | 替换盘/扩容/清理 操作入口 |
| 任务队列 | 异步任务模型 + 进度轮询 |

### Phase 3: 共享文件夹重构（3-4 天）

| 模块 | 内容 |
|------|------|
| 共享文件夹 | 协议绑定开关 + 权限矩阵 |
| 配置联动 | 修改协议时自动更新各服务配置 |
| 总览页增强 | 协议 tag 实时状态显示 |

### Phase 4: 打磨（3-4 天）

| 模块 | 内容 |
|------|------|
| 操作日志 | 维护页日志列表 |
| 边界处理 | 所有错误场景的弹窗和校验 |
| 性能优化 | 大容量磁盘列表懒加载 |

---

## 八、前端技术建议

### 8.1 保持 Alpine.js

不需要引入 React/Vue，Alpine.js 足够：

```html
<!-- 向导步骤切换 -->
<div x-data="{ step: 1 }">
    <div x-show="step === 1">...</div>
    <div x-show="step === 2">...</div>
    <!-- ... -->
    <button @click="step++">下一步</button>
</div>

<!-- 盘位图选择 -->
<div x-data="{ selectedDisks: [] }">
    <template x-for="disk in disks">
        <div @click="toggleDisk(disk.id)"
             :class="selectedDisks.includes(disk.id) ? 'selected' : ''">
            <!-- 磁盘卡片 -->
        </div>
    </template>
</div>
```

### 8.2 拓扑树纯 CSS 实现

```html
<div class="topology">
    <div class="pool-row">🗄️ pool-main</div>
    <div class="tree-children">
        <div class="tree-line"></div>
        <div class="volume-list">
            <div class="volume-row">📦 vol-data</div>
            <div class="share-list">
                <div class="share-row">📁 shared [SMB✓] [NFS✓]</div>
            </div>
        </div>
    </div>
</div>
```

```css
.tree-children { display: flex; margin-left: 28px; }
.tree-line { width: 2px; background: #e5e7eb; }
.volume-list { flex: 1; margin-left: 12px; }
```

### 8.3 响应式

- 桌面端：盘位图 4-6 列
- 平板：3 列
- 手机：2 列，拓扑树横向滚动

---

## 九、后端技术建议

### 9.1 磁盘信息获取

```bash
# 基础信息（JSON 输出）
lsblk -J -o NAME,SIZE,MODEL,SERIAL,TYPE,MOUNTPOINT,ROTA

# SMART 信息（JSON 输出，smartmontools 7.0+）
smartctl --json -a /dev/sdb

# RAID 状态
cat /proc/mdstat
mdadm --detail /dev/md0 --export

# LVM 信息
lvs -o lv_name,vg_name,lv_size,lv_attr --reportformat json
vgs -o vg_name,vg_size,vg_free --reportformat json
```

### 9.2 异步任务执行

所有写操作（创建池、扩容、替换盘）用**任务队列**异步执行：

```go
// 任务执行器
type TaskExecutor struct {
    tasks map[string]*Task
    mu    sync.RWMutex
}

func (e *TaskExecutor) RunPoolCreate(taskID string, req PoolCreateRequest) {
    task := e.tasks[taskID]
    task.Status = "running"

    // Step 1: 校验 (5%)
    e.updateProgress(taskID, 5, "校验磁盘...")

    // Step 2: 创建 RAID (60%)
    e.updateProgress(taskID, 5, "创建 RAID...")
    cmd := exec.Command("mdadm", "--create", "/dev/md0", "--level=5", ...)
    // 监控进度...

    // Step 3: 创建 VG (10%)
    // Step 4: 创建 LV (10%)
    // Step 5: 格式化 (15%)

    task.Status = "done"
    task.Progress = 100
}
```

前端轮询 `GET /api/tasks/:id` 或 WebSocket 推送进度。

### 9.3 配置联动

修改共享文件夹协议时，自动更新各服务配置：

```go
func (s *ShareService) UpdateProtocols(shareID string, protocols map[string]bool) error {
    share := s.repo.Get(shareID)

    // 更新 SMB
    if protocols["smb"] {
        s.samba.AddShare(share)
    } else {
        s.samba.RemoveShare(share.Name)
    }
    s.samba.Reload()

    // 更新 NFS
    if protocols["nfs"] {
        s.nfs.AddExport(share)
    } else {
        s.nfs.RemoveExport(share.Path)
    }
    s.nfs.Reload()

    // 更新 FTP / WebDAV / S3 同理

    return s.repo.UpdateProtocols(shareID, protocols)
}
```

---

## 十、与现有系统的兼容性

| 现有组件 | 兼容方式 |
|---------|---------|
| setup.sh 部署 | 不变，新增存储管理模块路由 |
| 现有 smb.conf | 迁移脚本：解析现有共享，导入为 Share 对象 |
| 现有 RAID (mdadm) | 自动识别已有 md 设备，导入为 Pool |
| 现有 LVM | 自动识别已有 VG/LV，导入为 Pool/Volume |
| .env 配置 | 不变，存储管理配置存入 SQLite/JSON |
| JWT 认证 | 复用现有 common/auth.go |
| sudo 封装 | 复用现有 common/sudo.go |

---

## 附录：界面原型交互说明

上文描述的界面原型已以交互式 Widget 形式展示，包含以下可交互元素：

1. **Tab 切换**：点击顶部 5 个 Tab 切换不同视图
2. **磁盘选择**：在"物理磁盘"页点击磁盘卡片展开详情
3. **向导步骤**：在"创建向导"页点击"下一步"体验 5 步流程
4. **目标选择**：Step 2 点击三个目标卡片体验推荐逻辑
5. **协议开关**：总览页中共享文件夹右侧的 tag 表示协议状态

---

*文档结束*
