# diskmgmt Consensus Review 修复报告

范围：`web/modules/diskmgmt/`，基线 `64f857a`。四项均已独立提交，提交信息与
任务书给定文本逐字一致。本报告只覆盖实际改动与验证结果，对任务书与实际代码
不一致之处单独列出，未在修复中静默吞掉或借机扩大范围。

## 提交记录

| 项 | commit | 文件 |
|---|---|---|
| A | `ad27508` | `pool.go`, `pool_test.go` |
| B | `5304c05` | `managed_wipe.go`(新), `managed_wipe_test.go`(新), `reset.go`, `stream.go` |
| C | `7f4005a` | `overview.go`, `overview_test.go`, `config_sync.go`, `config_sync_consistency_test.go`(新) |
| D | `5f9be5c` | `mdstat_parser.go`(新), `mdstat_parser_test.go`(新), `overview.go`, `raid_expand.go` |

---

## A（consensus P0-A1）删池服务端重解析 + 破坏性目标系统盘闸门

**根因**（`pool.go` `handlePoolDelete`，修复前）：

1. 直接信任表单携带的 `pool_type`/`pool_device`，未与服务端实时状态核验——
   池可能已被删除/变化，或表单值被篡改为任意设备路径，而 `wipefs`/`vgremove`/
   `mdadm --stop --zero-superblock` 会直接对该值执行。
2. 系统盘检查（原来是否存在、位置不详，经核查三个分支——lvm/raid/single——
   原代码里完全没有对"即将被擦除的成员盘"做系统盘闸门，只有建池/扩容路径
   （`handlePoolCreate`/`handlePoolExtend`）有 `isSystemDisk` 检查）。

**修法**：

- 新增纯函数 `findLivePoolByTypeDevice(pools []PoolSummary, poolType, poolDevice string) (PoolSummary, bool)`，
  在 `buildStorageOverview()` 的实时结果中按 `(Type, Device)` 精确匹配；未匹配到
  返回 HTTP 409（`存储池不存在或状态已变化，请刷新后重试`），不执行任何后续动作。
- 三个分支（lvm / raid* / single）都在**第一条破坏性命令之前**插入系统盘闸门：
  - lvm：对 `pvs -S vg_name=<vg>` 查出的全部成员 PV 跑 `anySystemDisk`；
    `pvs` 本身失败时也拒绝删除（安全默认，避免误判"无成员"后照常执行拆除）。
  - raid*：对 `mdadm --detail` 解析出的成员盘跑 `anySystemDisk`。
  - single：对 `expandPartitions(poolDevice)`（整盘 + `1..9`/`p1..p9` 分区展开）
    跑 `anySystemDisk`，覆盖"分区是系统盘但整盘设备名本身不是"的 legacy MBR 场景。
- single 分支额外在 `umount -l` 之后、`wipefs` 之前插入二次 `findmnt` 核验：
  `umount -l` 是 lazy umount，设备可能仍被其他挂载命名空间/bind mount 引用，
  仍有挂载点存在时拒绝并返回 409，不执行 `wipefs`。

**任务书与实际代码不一致之处（已核实，未按任务书假设实现）**：

任务书原文声称"LVM 的 Device 是 `/dev/mapper` 路径"。实际核查 `PoolSummary.Device`
对 LVM 池的取值来自 `detectPoolDevice`/`extractVGName`，格式是 `/dev/vg_nas`
风格（VG 路径），不是 `/dev/mapper/vg_nas-data` 风格的 mapper 路径。
`findLivePoolByTypeDevice` 的匹配逻辑按实际字段取值（`/dev/vg_nas`）构建，
而非按任务书描述的 mapper 路径构建。

**拒绝路径自查表**（分支 → HTTP 状态 → jsonError 文案）：

| 分支 | 拒绝条件 | HTTP | 文案 |
|---|---|---|---|
| 入口 | `confirm != "yes"` | 400 | 请加 confirm=yes 确认 |
| 入口 | `confirm_name` 与 `poolName` 不匹配 | 400 | 确认名不匹配，请输入存储池名称 |
| 入口 | 实时 overview 未匹配到池 | 409 | 存储池不存在或状态已变化，请刷新后重试 |
| lvm | `pvs` 查询失败 | 500 | 无法确认卷组 X 的物理卷成员（pvs 执行失败），为安全起见拒绝删除 |
| lvm | 成员 PV 命中系统盘 | 409 | 拒绝删除：卷组成员 X 是系统盘 |
| raid* | 成员盘命中系统盘 | 409 | 拒绝删除：阵列成员 X 是系统盘 |
| single | 展开分区命中系统盘 | 409 | 拒绝删除：X 是系统盘 |
| single | umount 后仍有残留挂载点 | 409 | 拒绝删除：X 仍有挂载点 Y 未卸载 |
| default | 未知 `pool_type` | 400 | 未知池类型: X |

**新增/修改测试**：`pool_test.go` 新增表驱动用例覆盖
`findLivePoolByTypeDevice`（匹配/类型不符/设备不符/空池列表）、`expandPartitions`
（整盘+9 个数字分区+9 个 p 前缀分区）、`anySystemDisk`（命中/不命中/空列表）。

**更正（consensus R1 审查发现）**：上一版本报告在此处声称"对 `handlePoolDelete`
三分支系统盘闸门的集成验证（通过可注入 `isSystemDisk` 替身）"，经复核**不实**：
`isSystemDisk` 是包级普通函数，不是接口/函数变量字段，测试代码无法注入替身
改写它的行为。实际测试覆盖如下，如实更正：

- 系统盘闸门本身（`anySystemDisk` 的命中/短路逻辑）只有**helper 级**覆盖
  （`TestAnySystemDisk`，三个子用例：命中整盘、命中分区、无命中/空列表），
  不是针对 `handlePoolDelete` 三个分支的集成测试。
- `handlePoolDelete` 的**handler 级**覆盖目前只有两条路径，且都是闸门之前
  的入口拒绝，不触及 lvm/raid/single 任一分支内部的系统盘判定：
  `TestHandlePoolDeleteRejectsUnmatchedLivePool`（表单池与实时 overview
  不匹配 → 409）、`TestHandlePoolDeleteRejectsConfirmNameMismatch`
  （确认名不匹配 → 400）。
- 测试环境没有真实 `mdadm`/`lvm`/`pvs`，`buildStorageOverview()` 的实时池
  列表恒为空，表单提交的任何 `(pool_type, pool_device)` 必然在
  `findLivePoolByTypeDevice` 这一步就被拒绝（409），根本走不到 switch 内部
  的 lvm/raid/single 分支，因此三分支内部的系统盘闸门、新增的 Fix 1 安全
  拒绝逻辑都**无法通过现有 handler 级测试验证**——这是本仓库现有系统命令
  包装层不做 mock 的既有测试模式局限（见 D 节说明），不是本轮引入的缺口，
  但上一版本报告"集成验证"的措辞夸大了实际覆盖范围，本轮予以更正。
- consensus R1 Fix 1 新增的安全拒绝逻辑（raid 分支 `mdadm --detail` 失败 /
  `parseMdMemberDisks` 解析为空 → 安全拒绝，见下方「本轮审查修复」小节）
  同样只有 helper 级覆盖（`parseMdMemberDisks` 的空输出/畸形输出用例），
  handler 级拒绝路径受限于上述同一个无 exec 注入点的问题，无法直接测——
  如实说明，不虚报已覆盖。

---

## B（consensus P0-A2）出厂重置擦盘集合收敛为面板纳管盘

**根因**（`reset.go`/`stream.go`，修复前）：两处出厂重置流程都对
`getDataDisks()`（返回全部"非系统盘的整盘"）执行 `mdadm --zero-superblock` +
`wipefs -a`。专用整机场景下这约等于"全部非系统盘"，但用户插一块外来 USB
备份盘（带真实数据、从未被面板纳管）做重置时会被一起误擦——误删用户数据。

**修法**：新增 `managed_wipe.go`，核心是纯函数 `collectManagedWipeTargets`，
在任何拆除动作之前，从三路来源的并集收敛出"面板实际纳管"的设备集合：

- a) `pvs` 列出的全部 PV（`pvsListAll`）；
- b) 活跃 MD 阵列的全部成员盘（`mdstatMemberDisks`）；
- c) `/data/nas*` 前缀挂载点的源设备解析出的底层物理盘
  （`resolveMountSourceToPhysicalDevices`：LV → 该 VG 的全部 PV；MD → 成员盘；
  整盘/分区直接挂载 → 自身）。

`reset.go`/`stream.go` 改为：**先**调用 `collectManagedWipeTargets` 采集好目标
集合，**再**执行任何 `umount`/`lvremove`/`mdadm --stop` 等拆除动作，最后用
`wipeManagedTargets` 只对这个集合执行擦除；集合外的盘（含外来 USB 盘）完全
不触碰。这个顺序是关键修复点——若先拆后采集，LVM/RAID 的 teardown 完成后
`pvs`/`mdstat` 就查不到任何成员信息了，集合会变空。

**新增测试**：`managed_wipe_test.go`，覆盖
`mdstatMemberDisks`/`pvsListAll`/`pvsPVToVGMap`/`mdstatMembersByDevice`/
`resolveMountSourceToPhysicalDevices`（LV/MD/整盘三种来源分别测）、
`collectManagedWipeTargets` 的并集去重，以及任务书要求的关键场景——
**"外来 USB 盘有数据签名但从未纳管"**：不在 pvs/mdstat/data mount 任何一路
来源中出现的盘，`skipUnmanagedDisks` 能正确识别并排除在 `wipeManagedTargets`
操作范围外。

---

## C（consensus P1-B）包级缓存并发竞态修复 + 配置一致性检查托管段真比对

### C1 并发竞态

**根因**：`overview.go` 的 `getCachedPVMappings`/`getCachedRAIDMembers` 是包级
变量缓存（"按请求缓存"），但缓存变量本身无锁保护，多个并发 HTTP 请求同时调
`buildStorageOverview()` 时存在数据竞争（读写包级 map 无同步）。

**修法**：核查两个缓存函数的实际调用路径——只有 `findDiskPoolDevice`（由
`buildStorageOverview` 调用）这一条路径，不是需要按请求隔离的场景，选择
**包级 `sync.Mutex`**（`overviewCacheMu`）而非请求级重构（更小改动、不改变
缓存语义）。新增 `resetOverviewCaches()` 辅助函数，在 `buildStorageOverview`
入口统一清空两个缓存后重新查询，锁覆盖读、写、清空三处。

**新增测试**：`overview_test.go` 新增并发测试，用 `-race` 跑多个 goroutine
并发调用缓存读写路径，确认无数据竞争。

### C2 配置一致性检查托管段真比对

**根因**：`CheckConfigConsistency` 第 3/4 步原来调用 `generateManagedBlock`
算出 `expectedSamba`/`expectedNFS`，但**直接 `_ = expectedSamba` 丢弃**，只检查
托管段 START/END 标记是否存在，从未真正比对文件实际内容与 `folders.db`
是否一致——标记还在、内容已经漂移（如管理员手动改过 smb.conf 内 share 定义，
或 folders.db 更新后配置未同步）的场景完全检测不出来。

进一步核查发现 `generateManagedBlock` 本身也是问题的一部分：它是一个"简化版
预览"（samba 只写 `[name]` 一行、nfs 只写 `path ...` 一行），与
`GenerateSambaConfig`/`GenerateNFSConfig` 真正写盘的完整内容（含 force
user/group、mask、recycle bin vfs 配置、NFS 选项等）完全不是一回事——即使
真的拿它去比对，"期望"和"实际"也会因为两套独立实现而必然漂移，比对毫无意义。

**修法**：

- 从 `GenerateSambaConfig`/`GenerateNFSConfig` 中抽出纯函数
  `buildSambaManagedBlock`/`buildNFSManagedBlock`，两个真实生成器与
  `generateManagedBlock`（供一致性检查用）复用同一份实现，保证"期望输出"与
  "实际写盘内容"由同一套代码生成，不会独立漂移。
- 新增 `extractManagedSection(content) (string, bool)`：从文件内容中提取
  `managedStart...managedEnd`（含标记）之间的原文；找不到完整区间返回
  `("", false)`，调用方按"无托管段"而非"空托管段"处理。
- 新增 `normalizeManagedBlock`：统一行尾空白裁剪 + 末尾换行，避免 `cat` 读取
  差异或个别行尾空格被误报为"漂移"。
- 新增 `managedSectionDrifted(actual, expected) bool`：归一化后精确比较。
- `CheckConfigConsistency` 新增判断：标记存在且 `len(metas) > 0` 时，调用
  `managedSectionDrifted` 做真实内容比对，不一致则追加问题文案
  `"Samba/NFS 托管段与 folders.db 不一致（配置漂移）"`。保持本函数"只报告
  不自动修复"的既有语义不变。

**新增测试**：`config_sync_consistency_test.go`，覆盖四种场景——
一致（内容比对通过）、漂移（手工改过 smb.conf 内容后检测出不一致）、
无托管段（标记缺失）、多段残留（重复 START/END 标记）；另在 `overview_test.go`
补充并发/race 测试（见 C1）。

---

## D（consensus P0-D）mdstat 统一块解析

**根因**：四处独立、互不一致的 `/proc/mdstat` 临时字符串解析，逐一核查实锤：

1. `getRAIDStatus`（`overview.go`）把 `(F)` 失败成员计入 `SpareDisks`，`(S)`
   真 spare 反而不计入任何桶——前端看到的"spare 盘数"虚高，真正的失败盘数
   无处体现。
2. `SyncPercent` 用 `extractPercent(line[:idx])`，在 `]` **之前**找百分比数字；
   真实 mdstat 进度行格式是 `[=>...]  resync = 15.2% (...) finish=... speed=...`，
   百分比在 `]` **之后**——这个实现从未能正确提取过百分比。
3. `handleRAIDExpandFS` 的重构中闸门只在 MD 头行本身找 `reshape`/`resync`
   关键词——头行格式是 `mdX : active raidN dev[i] dev[j]`，从不携带这些词，
   闸门形同虚设，用户可能在阵列仍在重构时就扩展文件系统（数据损坏风险）。
4. `handleRAIDReshapeStatus`/`waitForResync` 只看头行的下一行
   （`lines[i+1]`），真实进度行通常在"blocks 行"之后（`i+2` 及以后），会漏检；
   `waitForResync` 漏检会导致阵列还在重构时就被判定"完成"进而执行
   `growFilesystem`，直接损坏数据。

**修法**：新增 `mdstat_parser.go`，统一按"行首非空白"切块解析 `/proc/mdstat`
全文（每个阵列一块：头行 + 后续全部缩进行），核心类型/函数：

- `MdstatBlock{MdName, HeaderFields, MemberTokens, StatusLine, Progress}`
- `MdstatProgressLine{Kind, Percent, Speed, Finish}`
- `parseMdstatBlocks(content string) []MdstatBlock`——纯函数主解析入口
- `memberFlag(token) string`——返回 `"F"`/`"S"`/`"W"`/`""`
- `findBlockByMdName`/`blockHasProgress`/`firstProgress`——供四个调用点复用

四个调用点全部重写为基于 `parseMdstatBlocks` 的解析结果：

- `getRAIDStatus`：`(F)` → `FailedDisks`（新增字段），`(S)` → `SpareDisks`，
  其余 → `ActiveDisks`；`FailedDisks > 0` 时 `State` 置 `"degraded"`；
  `SyncPercent`/`"resyncing"` 只依据属于本阵列块的进度行，不会被其他阵列
  的进度行污染（块级隔离）。
- `handleRAIDExpandFS`：改为 `blockHasProgress(block)` 判断该阵列块内是否
  存在任一进度行（resync/recovery/reshape/check/repair），闸住重构中的
  文件系统扩展请求。
- `handleRAIDReshapeStatus`：改为 `firstProgress(block)`，在块内任意位置
  （不限定 `i+1`）命中进度行。
- `waitForResync`：同上改为块级解析，循环前置 2 秒延迟与"连续 3 轮无
  resync/recovery 行才判定完成"的既有防竞态逻辑保持不变，只替换了底层
  解析方式。

`RAIDStatus` 新增 `FailedDisks int` 字段（JSON key `failed_disks`），向后
兼容新增字段，不改动既有 `spare_disks` 语义。

**实现过程中发现并修复的 bug（属于本次新写代码，非既有代码缺陷）**：
`parseMdstatBlocks` 最初实现中，遇到非阵列头行（如 `Personalities :` 或
收尾的 `unused devices: <none>`）时直接 `current = nil` 把正在累积的块丢弃，
未先 flush 进 `blocks` 切片。真实 `/proc/mdstat` 必然以 `unused devices:`
结尾，这会导致**每个阵列的块都被丢弃、`parseMdstatBlocks` 永远返回空切片**。
单测首次运行时 100% 复现（9 个用例全部失败/panic），修复为"遇到非阵列头行
先 flush 当前块再置空"，重跑单测全部通过。记录在此，供复核确认已修复。

**新增测试**：`mdstat_parser_test.go`，表驱动覆盖：干净 RAID1（无进度行）、
RAID1 resync 中（验证百分比从 `]` 之后提取）、RAID5 reshape 含
bitmap/backup-file 噪声行（验证不污染成员 token 或被误判为进度行）、降级
阵列含 `(F)` 失败成员、含 `(S)` 真 spare 成员（两者不混淆）、check/repair
场景、多阵列并存时的块级隔离（验证 md1 的进度行不污染 md0 判定）、空输入/
无阵列输入不 panic、`memberFlag` 四种标志、`findBlockByMdName` 找到/找不到
两种路径。共 **10** 个测试函数（经 consensus R1 审查复核，实测为 10，
上一版本报告误报为 11，已更正）。

说明：`getRAIDStatus`/`handleRAIDExpandFS`/`handleRAIDReshapeStatus`/
`waitForResync` 四个调用点本身仍通过 `common.ExecOutput`/`os.ReadFile("/proc/mdstat")`
发起真实系统调用，与本仓库既有测试模式一致（系统命令包装层不做 mock，只对
其下的纯解析逻辑做单测覆盖）；既有的 `TestWaitForResyncReturnsNilWhenNoResyncLine`
（`raid_expand_test.go`，依赖真实 sleep 的时序测试）重跑确认仍通过，签名未变。

---

## 最终验证

```
cd web && go build ./... && go vet ./modules/diskmgmt && go test -race ./modules/diskmgmt -count=1 -cover
```

```
ok  	nas-panel/modules/diskmgmt	12.322s	coverage: 20.8% of statements
```

- `go build ./...`：通过
- `go vet ./modules/diskmgmt`：无警告
- `go test -race`：全部通过，无数据竞争
- 覆盖率：基线 14.9% → 20.8%（不降反升）

沙箱环境噪声说明：测试输出中出现
`[CONFIG_SYNC] 查询元数据失败: unable to open database file: out of memory (14)`
——这是 `GetAllFolderMeta()` 在当前沙箱环境下 SQLite 文件打开失败的既有行为
（`log.Printf` 记录后 `continue`，不是本次改动引入，不影响测试通过与覆盖率）。

## 范围自查（上一轮四项）

- 仅修改/新增 `web/modules/diskmgmt/` 下的 Go 源码与测试文件，未触及其他模块、
  前端、脚本。
- 未执行任何真实磁盘/sudo/systemctl/mdadm/lvm 命令；全部验证基于单测 fixture
  （字符串/table 驱动输入），不依赖真实系统环境。
- 四个提交均只 `git add` 该项涉及的具体文件，未用 `git add .`；提交信息与
  任务书给定文本逐字一致。
- 本报告（`TASK-REPORT.md`）未执行 `git add`；任务书文件本身（若仓库内存在）
  全程未被 `git add`。

---

## 本轮审查修复（consensus R1）

基线 HEAD `5f9be5c`。本轮只做任务书编号的 4 项修复，未扩大范围；单个提交，
只 `git add` 改动的源码/测试文件。

### Fix 1（Important）raid 删池分支 mdadm --detail 失败必须安全拒绝

**改动**：`pool.go` `handlePoolDelete` 的 `"raid1", "raid0", "raid5", "raid6"`
分支，原来 `detailOut, _ := common.SudoOutput(...)` 吞错，`memberDisks` 为空时
系统盘闸门空转通过，umount/`mdadm --stop` 照常执行，成员盘的
`--zero-superblock`/`wipefs` 全部被跳过，留下带 RAID 超级块的幽灵成员盘。

对齐同函数 lvm 分支对 `pvs` 失败的安全拒绝模式，改为：

1. `detailOut, detailErr := common.SudoOutput(...)`；`detailErr != nil` → 审计
   日志记 `rejected` + `jsonError`（HTTP 500，"无法确认阵列 %s 的成员
   （mdadm --detail 执行失败），为安全起见拒绝删除: %s"），`return`，不执行
   任何 umount/`--stop`。
2. `detailErr == nil` 但 `parseMdMemberDisks` 结果为空 → 同样安全拒绝
   （`jsonError` 500，"无法确认阵列 %s 的成员（未能解析出阵列成员），为安全
   起见拒绝删除"）——能被 overview 识别为 raid 池的阵列必然有成员，解析为空
   说明状态异常。
3. 两处拒绝都在该分支第一条破坏性命令（umount）之前，不执行任何拆除动作。

**测试**：`pool_test.go` 新增 `TestParseMdMemberDisks`，覆盖正常输出、空输出、
两种畸形输出（无 `active`/`spare` 关键词；有关键词但无 `/dev/` 路径）——确认
`parseMdMemberDisks` 在异常输入下确实返回空切片，不会意外解析出假成员掩盖
Fix 1 的安全拒绝路径。

**已知缺口（如实申报，未虚报覆盖）**：handler 级的两处新增拒绝分支
（detail 失败 / 解析为空）**无法通过现有测试直接验证**——测试环境没有真实
`mdadm`，`buildStorageOverview()` 的实时池列表恒为空，任何表单提交的池都在
更早的 `findLivePoolByTypeDevice` 匹配步骤被 409 拒绝，根本走不到 switch 内
的 raid 分支。这是本仓库现有系统命令包装层不做 mock 的既有测试模式局限（见
上一轮 D 节、及本报告对 A 节的更正说明），任务书 4 条也明确不要求引入 exec
注入层解决这个问题，故只做 helper 级（`parseMdMemberDisks`）覆盖。

### Fix 2（Important）TASK-REPORT.md 声明修正

- D 节"共 11 个测试函数" → 更正为实际的 **10** 个（`mdstat_parser_test.go`
  只有 10 个 `func Test...`）。
- A 节"对 `handlePoolDelete` 三分支系统盘闸门的集成验证（通过可注入
  `isSystemDisk` 替身）"的不实声明已改写为如实描述：`isSystemDisk` 是普通
  包级函数不可注入，实际只有 helper 级 `TestAnySystemDisk` 覆盖闸门逻辑本身，
  handler 级覆盖限于 `TestHandlePoolDeleteRejectsUnmatchedLivePool`/
  `TestHandlePoolDeleteRejectsConfirmNameMismatch` 两个非破坏性拒绝分支，
  不触及任一池类型分支内部的系统盘判定。详见 A 节内新增的"更正"段落。

### Fix 3（Minor）stream.go 重置路径复用 wipeManagedTargets

**改动**：`stream.go` `handleWizardResetStream` 原来在"5. Wipe disks"步骤内联
复制了一份擦盘循环（`isSystemDisk` 跳过 + `--zero-superblock` + `wipefs`），
与 `reset.go`（`handleWizardReset`）调用的 `wipeManagedTargets` 平行实现，且
跳过系统盘时没有任何日志痕迹。改为直接调用 `wipeManagedTargets(managedTargets)`，
返回的 `steps`（含"跳过系统盘 X"条目）通过 `sendProgress` 的 `Detail` 字段
（用 `"; "` 拼接）一次性呈现；删除内联循环。SSE 事件结构（`ProgressEvent` 字段）
与 `totalSteps`（仍为 7）不变，只是"清除磁盘签名"这一步在有擦除/跳过内容时
多发一条 `running` 状态事件携带 `Detail`。

**测试**：`wipeManagedTargets` 本身已有 `managed_wipe_test.go` 覆盖
（`TestWipeManagedTargets_StepsNonEmpty`/`TestWipeManagedTargets_EmptyInput`），
行为不变只是调用方从内联改为复用，未新增针对 `handleWizardResetStream` 的
handler 级测试（该函数是 SSE handler，无现有测试模式可复用，与 Fix 1 同样
受限于系统命令不 mock 的既有局限）。

### Fix 4（Minor）重置采集阶段 pvs/mdstat 失败要出声

**改动**：`reset.go`（非流式）与 `stream.go`（流式）的采集阶段都把
`pvs`/`cat /proc/mdstat` 的 error 从 `_` 改为显式接收：

- `reset.go`：`pvsErrPre`/`mdstatErrPre` 任一非 nil 时，向 `steps` 追加一条
  `"无法枚举纳管盘（pvs/mdstat 失败）: pvs=%v mdstat=%v，擦除集合可能不完整"`，
  不中止重置（无 LVM 的机器上 `pvs` 缺失/报错是合法状态，中止会把这种正常
  场景也挡住）。
- `stream.go`：同样条件下通过 `sendProgress` 发一条 `Step: "采集纳管盘",
  Status: "running"` 的事件，`Detail` 带同样的警告文案；不新增 step、
  `Index`/`Total` 不变，`totalSteps` 仍为 7。

两处文案都明确点出"擦除集合可能不完整"，便于用户/审计从日志/SSE 流里看出
"重置报成功但纳管盘集合可能没采全"这个风险，不再静默吞错。

**测试**：这两处改动是 handler 内联的采集 + 分支判断（`pvsErrPre != nil ||
mdstatErrPre != nil`），逻辑本身足够简单（两个 error 的布尔或），未新增单测
——`collectManagedWipeTargets`/`pvsListAll`/`mdstatMemberDisks` 等下游纯函数
已有 `managed_wipe_test.go` 覆盖，采集失败时这些函数拿到空字符串输入的行为
（返回空集合）已被既有的"空 mdstat/空 pvsOut 不 panic"路径间接覆盖（table
驱动用例的边界输入里包含空串场景）。

### 明确不做（任务书已声明范围外）

- `extractVGName` 对 `/dev/mapper` 形态的 VG 名提取缺陷——另轮处理。
- inactive MD 阵列成员不进纳管集合——符合任务书"活跃阵列"措辞，另轮评估。
- 未引入 exec 注入层/大规模可测性重构（见 Fix 1/Fix 3 的"已知缺口"说明，
  这正是该不做项的直接后果，任务书已预先认可）。

### 最终验证（本轮）

```
cd web && go build ./... && go vet ./modules/diskmgmt && go test -race ./modules/diskmgmt -count=1 -cover
```

```
ok  	nas-panel/modules/diskmgmt	12.116s	coverage: 21.0% of statements
```

- `go build ./...`：通过
- `go vet ./modules/diskmgmt`：无警告
- `go test -race`：全部通过，无数据竞争
- 覆盖率：基线 20.8% → 21.0%（不降反升，满足任务书"不得下降"要求）

### 范围自查（本轮）

- 仅修改 `web/modules/diskmgmt/` 下的 `pool.go`、`pool_test.go`、`reset.go`、
  `stream.go`，以及本报告 `TASK-REPORT.md`；未触及 `frontend/`、`scripts/`、
  其他模块。
- 未执行任何真实磁盘/sudo/systemctl/mdadm/lvm 命令；全部验证基于单测 fixture
  与 `go build`/`go vet`/`go test`。
- 单个提交，只 `git add` 本轮改动的源码/测试文件；提交信息与任务书给定文本
  逐字一致。
- 本报告未执行 `git add`。
