# NAS 存储与磁盘管理多模型审查综合报告

## 1. 审查基线与有效性控制

- 仓库：`~/soft/nas`
- 基线：`7243f4df1d72a6f0bf350d35737c8c05ed4cd210`
- 产品前提：专用 NAS 整机，数据盘由面板独占管理；外来含数据盘仍需防误擦。
- 有效独立报告：
  - **Qwen3.8-Max**：架构、领域模型、API 收敛。
  - **DeepSeek-V4-Pro**：数据安全、参数校验、写入链。
  - **GLM-5.3**：LVM/RAID/XFS 语义、故障恢复、测试矩阵。
- Kimi 独立复核全部关键论断，并结合此前源码检查发现补充问题。
- 仅采纳日志中有实际源码读取行为的报告。另一个 Qwen-Max 初稿未读源码且引用不存在的文件，已剔除；不可用的模型别名也未计入结果。
- 没有修改项目源码，没有执行任何真实磁盘、RAID、LVM、挂载或 systemctl 操作。

验证：

- `go test -race ./modules/diskmgmt -count=1`：PASS。
- `diskmgmt` 语句覆盖率：**11.1%**。
- `handleWizardSetupStream`、`handlePoolExtendStream`、`handlePoolDelete`、`handleRAIDExpandStream` 等关键写路径覆盖率为 0%。
- 工作区没有产生源码 diff。
- Linux 语义参考：Kernel RAID sysfs 文档、Debian mdadm(8) 手册。

> 本报告中的“代码确认”表示静态代码路径可以成立，不代表已经在真实磁盘上复现。需要真实 mdstat、LVM/RAID 行为才能定性的内容单独标为“待真机验证”。

---

## 2. 当前实际逻辑与机制

### 2.1 拓扑发现与展示

```text
lsblk / smartctl / sysfs
        ↓
物理盘、分区、接口、温度、SMART
        ↓
df / findmnt / pvs / mdstat / mdadm --detail
        ↓
每次请求现场反推 Pool、Volume、成员盘、健康状态
        ↓
StorageOverview
```

当前没有持久化的 Pool 注册表。Pool/Volume 名称和归属主要由 `overview.go` 根据挂载点及设备关系临时推导。Volume 在现有创建流程中基本固定为一个 `data` LV 或直接挂载的 MD/分区设备；快照、压缩等字段属于预留展示能力。

### 2.2 写操作

```text
客户端参数
  → handler 内做校验（各路径强度不一致）
  → LockDiskOp 全局互斥
  → 顺序调用 wipefs / pvcreate / vg* / lv* / mdadm / mkfs / mount
  → 写 fstab、mdadm.conf、共享配置
  → 返回 JSON 或在同一 HTTP 请求中推送 SSE
```

这套机制能完成正常流程，但缺少统一的“写前计划、写后验证、持久化操作状态”。部分失败时，代码只能依靠各 handler 自己判断，现有实现对错误处理不一致。

### 2.3 共享配置

```text
folders.db（共享与权限权威源）
  → GenerateSambaConfig / GenerateNFSConfig
  → smb.conf / exports 的 Z1 托管段
  → reload/restart 服务
```

共享层的“数据库元数据 → 配置投影”是当前较完整的一条架构链。磁盘拓扑层可以借鉴其明确所有权和配置生成方式，但不能照搬成“数据库替代内核状态”。

---

## 3. 三个模型的主要观点与复核结果

| 模型 | 有价值的主要贡献 | 复核后的取舍 |
|---|---|---|
| Qwen3.8-Max | 指出三套空闲盘口径、客户端拓扑信任、Volume 名实不符、双轨 API、包级缓存竞态；提出统一擦盘闸门和 pools 注册表 | 大部分采纳。**不采纳“pools 表单独成为拓扑真相源”**：注册表应记录所有权和意图，LVM/mdadm/内核仍记录实时实际状态 |
| DeepSeek-V4-Pro | 找到 import、pool extend、replace 等路径校验不一致，以及旧非流式 API 的错误传播问题 | 参数校验和旧 API 收敛建议采纳。`import vg_name` 被列为可直接利用的 P0 路径穿越，证据不足，降级为“必须补名称白名单”；`SafeWriteFile 原子写入`的说法与实现不符，不采纳 |
| GLM-5.3 | 系统发现 mdstat 解析问题、RAID 健康误判、长任务锁问题，提出意图日志、幂等恢复和负向测试矩阵 | RAID 与恢复方向基本采纳。**“LVM 流式扩容失败即中止”不成立**：前四条写命令错误实际被忽略；报告中的这一结论被源码否决 |
| Kimi 复核 | 补出选盘失败回退全量、建池 error 后仍 complete、LVM 删池成员收集顺序、fstab 过宽清理、包级缓存并发风险 | 与三模型形成互补，纳入最终优先级 |

### 一个关键架构分歧

- Qwen 倾向： pools 表作为拓扑真相源，系统命令用于健康对账。
- GLM 倾向：LVM/mdadm superblock 和内核状态作为真相源，面板记录操作意图。

**综合结论：采用双权威、明确分工。**

- 数据库回答：“这个池是否归面板管理、目标是什么、成员身份是什么、操作进行到哪一步”。
- 内核回答：“当前设备、VG/LV/MD、挂载、降级、reshape 实际是什么状态”。
- 启动和每次写前必须对账。数据库不能与内核漂移后继续发命令，内核存在外来池也不能自动视为面板所有。

---

## 4. 经源码复核的高优先级问题

### P0-A：破坏范围由客户端参数或过宽扫描决定

1. `handlePoolDelete` 直接信任表单的 `pool_type/pool_device`。声明为 `single` 时会对传入设备执行 `wipefs -a`，服务端没有从受管池记录中重新解析目标，也缺少统一的系统盘/在用盘闸门（`pool.go:457-548`）。
2. reset 的 `getDataDisks()` 返回所有非系统整盘，随后执行 zero-superblock/wipefs；未纳管的 USB 或外来含数据盘也在范围内（`storage.go:375-390`、`stream.go:609-625`）。
3. 低层 `format/mount/mkdir` 等 API 仍注册，旧非流式建池/扩容接口也仍可调用。即使当前主 UI 使用流式弹窗，这些接口仍是有效攻击和误操作面（`diskmgmt.go:26-36,72-77`）。

**建议**：所有破坏性目标都由服务端按受管 Pool ID 和实时库存重新解析。reset 只处理面板登记成员；外来盘必须走独立、显式的擦除流程。

### P0-B：磁盘分类与选盘规则不统一

1. 整盘无 FSTYPE、子分区有未挂载文件系统时，父盘可能仍为 `unused`（`storage.go:218-263`）。不同 API 对子分区的补救检查不一致。
2. `isSystemDisk()` 对典型 `/dev/sda` 整盘会检查 `/dev/sd1` 等错误名称，不能作为唯一的 SATA 系统盘防线（`storage.go:302-330`）。
3. 流式向导收到 `disks` 后，如果没有任何所选盘通过过滤，代码保留原来的全部空闲盘，而非拒绝（`stream.go:74-91`）。

**建议**：只保留一个 DiskInventory 和 `canWipe()`。获取锁后重新扫描，按计划中的 StableID/序列号/容量复核全部目标；任何缺失或身份变化都中止。

### P0-C：错误传播不完整，存在假成功

1. 建池 helper 遇到错误只返回，外层仍发送 `Status=complete`（`stream.go:133-185`）。
2. LVM 流式扩容对 `wipefs/pvcreate/vgextend/lvextend` 的错误全部忽略，先把四步标为 done，只检查最后的 growfs（`pool_extend.go:81-119`）。growfs 在尺寸未变化时也可能正常返回，因此最终 complete 不能证明新盘已加入。
3. 旧非流式路径还有大量忽略命令错误并返回成功描述的链条。

**建议**：每一步返回 `error`，失败后禁止继续后续写命令，禁止发送 complete。最后必须重新读取内核状态做 Verify，命令退出码只能证明命令本身结束。

### P0-D：RAID 状态采集与扩容门槛存在系统性偏差

1. `waitForResync` 和 `handleRAIDReshapeStatus` 只检查 MD 头行后一行；典型 mdstat 进度位于 blocks 行之后，可能漏掉进度（`raid_expand.go:264-329`）。
2. `handleRAIDExpandFS` 只在 MD 头行本身寻找 reshape/resync，不能可靠阻止未完成时 growfs（`raid_expand.go:219-229`）。
3. 总览在 `]` 之前的片段中找百分比，而典型 resync/recovery 百分比位于 `]` 之后（`overview.go:814-820`）。
4. 健康主要依据 `state=active/clean`，但降级阵列仍可显示 active；`(F)` 成员还被计入 SpareDisks（`overview.go:197-205,783-826`）。
5. 修复等待逻辑后，全局锁长时间覆盖 resync 会阻塞其他磁盘操作；当前超时后仍会按完成继续。

**建议**：优先读取内核结构化 sysfs：`degraded`、`sync_action`、`sync_completed`、`sync_speed`、`reshape_position` 及成员 `state`，mdstat 仅作兼容回退。健康与活动状态分开：`health=normal/degraded/failed`，`activity=idle/resync/recovery/reshape/check/repair`。

### P1-A：删池的持久化清理不正确

1. LVM 删池先 `vgremove`，再用 `pvs -S vg_name=<旧VG>` 查成员，通常无法取得原成员，导致 PV/磁盘签名清理遗漏（`pool.go:503-514`）。
2. fstab 以 `/data/nas` 前缀清理，删除一个池可能删除其他池的挂载持久化（`pool.go:555-577`）。

**建议**：拆池前保存 VG UUID、LV UUID、FS UUID 和成员 StableID；按精确 UUID/挂载点清理。删池后通过 pvs/vgs/lvs、findmnt、blkid 验证目标确实释放。

### P1-B：并发、配置和恢复机制仍有结构性缺口

- `cachedPVMappings/cachedRAIDMembers` 是包级全局变量，却在每次 overview 请求入口重置，所谓“请求级缓存”实际存在跨请求竞态（`overview.go:142-144,683-728`）。
- 配置一致性检查生成了 expectedSamba/expectedNFS 却未真正与现有内容比较（`config_sync.go:553-611`）。
- 共享删除 pending 队列会清空，即使个别执行失败；文件系统命令和 DB 写入也存在忽略错误的路径。
- 导入仅支持 LVM，且只取第一个 LV；不等于通用换机恢复（`import.go:23-66,101-110`）。

---

## 5. 建议的目标机制

### 5.1 统一库存与安全闸门

```text
DiskInventory
  ├─ 设备树：整盘 → 分区 → holders
  ├─ 身份：path + by-id/WWN + serial + size
  ├─ 使用状态：mount / swap / PV / RAID / fstype / signatures
  ├─ 所有权：system / blank / foreign / managed
  └─ 判定：canWipe / canExtend / canReplace + 拒绝原因
```

主向导只允许 `blank` 一键建池。含旧数据的 `foreign` 盘先进入独立“擦除并重新扫描”流程。所有 `wipefs/mkfs/pvcreate -f/zero-superblock` 强制经过同一个闸门。

### 5.2 注册表记录所有权，内核记录实际状态

建议增加 pools 注册信息，至少包括：

- pool_id、显示名、类型。
- VG UUID / MD UUID / FS UUID。
- 成员盘 StableID。
- managed 挂载点。
- source=panel/import。
- 当前操作意图和 schema 版本。

禁止把数据库中的 path 当作无需复核的执行依据。每次操作前由内核重新确认 UUID 与成员身份。

### 5.3 写操作统一为计划、执行、验证

```text
POST 创建 Plan
  → Preflight（锁后重扫）
  → Execute（持久化 Step Journal）
  → Verify（读取内核目标状态）
  → Succeeded / FailedPartial / WaitingRebuild

GET 查询操作
GET SSE 订阅进度
```

- 客户端只提交 plan_id 和确认词。
- SSE 不再直接执行破坏性 GET。
- 浏览器断开不影响后台操作。
- 每条命令都有输入、结果、开始/结束时间和验证谓词。
- 不可逆操作不承诺回滚；恢复采用“检查当前状态 → 跳过已完成步骤 → 收敛后续步骤”。
- 对已存在 UUID 的文件系统，恢复时绝不能盲目重放 mkfs。

### 5.4 长任务采用两段锁

- `running`：wipe/create/add/grow/remove 等短命令阶段持磁盘锁。
- `observing`：resync/recovery/reshape/scrub 等待阶段释放磁盘锁，只保留目标池级操作冲突标记。
- 面板重启后，内核继续的任务由恢复器重新附着并继续展示进度。
- RAID5/6 reshape 的备份策略按 mdadm 手册和实际 spare 状态确定；无可用 spare 时，独立 `--backup-file` 的要求不能忽略。必须做崩溃恢复实验。

### 5.5 收敛 API 与产品模型

- 主流程保持一机一主池、一个主 Volume。
- separate 多池、快照、多 Volume、压缩暂缓。
- 非流式旧 API 删除或只做兼容适配；低层 format/mount/mkdir 默认放入 debug gate。
- RAID1 增加成员改称“增加镜像/提高冗余”，不要暗示容量增加。
- RAID0 不展示换盘修复和扩容能力。
- 总览等 GET 保持只读，目录创建、chown 等移到安装/修复流程。

---

## 6. 推荐落地顺序与验收门槛

### 阶段 0：安全止血，不改变既有功能边界

1. 统一 canWipe、系统盘与 foreign 盘判断。
2. 修复选盘回退、流式建池 complete、LVM 扩容错误传播。
3. 修复删池成员收集顺序和 fstab 精确清理。
4. 生产环境关闭低层与重复写 API。
5. 所有破坏性操作在锁后重扫并验证目标。

**验收**：每个写命令注入失败后，后续命令不再执行，界面不出现 complete；外来含数据盘不会进入 wipe 集合。

### 阶段 1：修通 RAID 真实状态链

1. 以 sysfs 为主重写状态采集，保存真实 mdstat/sysfs fixture 做表驱动测试。
2. 健康和活动状态分离。
3. reshape 完成前禁止 growfs；observing 阶段释放全局锁。
4. 验证 mdadm.conf、initramfs、备份文件和重启装配。

**验收**：RAID1/5/6 在 resync/recovery/reshape/degraded 各状态下，面板与内核一致；重启后继续显示真实进度。

### 阶段 2：抽取最小执行内核

```text
inventory.go   唯一库存快照
registry.go    所有权与稳定身份
planner.go     操作计划
executor.go    可注入命令执行器
operations.go  持久化状态机
verify.go      内核状态验证
recovery.go    启动收敛
```

不做大爆炸重写。先让现有 handler 调用这些接口，再逐步删除重复实现。

### 阶段 3：负向验收优先于新功能

必须有以下场景：

- SATA/NVMe 系统盘、含未挂载 XFS 分区的外来盘、USB 备份盘。
- 选盘后换盘、盘序变化、所选盘失效。
- 每条命令分别失败，验证不继续、不假成功。
- 建池和扩容中 kill -9 面板、重启机器。
- 删除 separate 中一个池（若保留），验证其他池重启仍挂载。
- RAID5/6 reshape 中断、重启、备份文件恢复。
- 多并发 overview/AI 查询，跑 race 验证缓存竞态已消除。

当前 11.1% 覆盖率无法证明这些路径安全。测试目标应优先覆盖“每条写命令失败分支和恢复分支”，而不是只追求百分比。

---

## 7. 最终建议

这套系统目前不缺存储功能清单，缺的是统一、可信的控制面。三个模型虽然切入点不同，但有效结论最终指向同四件事：

1. **所有破坏性操作必须经过同一个安全闸门。**
2. **数据库记录所有权和意图，内核提供实时事实，两者持续对账。**
3. **操作完成必须由内核状态验证，不能由命令链或 SSE 文案推断。**
4. **RAID 长任务需要持久化观察与恢复，不能依赖一次 HTTP 请求和文本解析。**

先把这四点做稳，再恢复多池、导入增强、快照和多 Volume 的开发，成本会低很多。

## 附：原始独立报告

- `qwen3.8-max.md`
- `deepseek-v4-pro.md`
- `glm-5.3.md`

原始报告仅作为模型观点存档；如与本文冲突，以本文回查源码后的结论为准。
