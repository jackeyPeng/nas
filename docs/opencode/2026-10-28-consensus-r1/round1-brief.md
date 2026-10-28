# 任务：diskmgmt 共识审查遗留修复（consensus 第 1 轮）

你在 ~/soft/nas 仓库工作。基线 = HEAD (64f857a)。背景：docs/opencode/consensus-report.md 是多模型审查报告，但其基线（7243f4d）早于最近五轮加固，部分问题已修；本任务书里的四个条目是**已对照当前 HEAD 逐条实锤仍然成立**的遗留项。先读每条列出的源文件——代码现实优先于本文本；发现本文与代码不符，在报告里申报，不要静默绕过或自行扩大范围。

## 硬约束（违反 = 返工）

1. 禁止读取或修改仓库外任何文件（包括 ~/go/pkg/mod、/tmp、任何仓库根之外的路径）——沙箱会 auto-reject 并导致整个 run 中止。所有验证只通过 web/ 目录内的 go build / go vet / go test。
2. 只修改 web/modules/diskmgmt/ 下的 Go 代码与测试。不碰 frontend/、scripts/、其他模块。认为必须动别处时写进报告申报，不得直接改。
3. 禁止执行任何真实磁盘 / sudo / systemctl / mdadm / lvm 命令，禁止启动本机服务。外部命令行为只能靠单测 fixture 模拟。
4. 报告写仓库根 TASK-REPORT.md（覆盖写，不 git add）。任务书文件本身也不要 git add。
5. `git add` 只加你自己改动的源码/测试文件；4 个条目各自独立 commit，message 原样使用下文给出的。
6. 规格矛盾或缺口 → 报告里申报，由 parent 裁决。

## Item A：handlePoolDelete 服务端重解析 + 系统盘闸门（P0-A1）

现状（已核实 pool.go:455 起）：handler 直接信任表单 pool_type/pool_device/pool_name；single 分支对客户端传入的任意设备执行 `wipefs -a`，无系统盘/受管性复核。lvm/raid 分支同样按客户端给的 poolDevice 直接拆。

修法：
1. 在 LockDiskOp 与 confirm/confirm_name 检查之后、任何破坏性命令之前，服务端重解析：调用 buildStorageOverview() 取实时池列表，要求提交的 (Type, Device) 与某个实时池**精确匹配**（LVM 的 Device 是 /dev/mapper 路径，与前端提交同源，见 frontend/app.js deletePool——前端原样回传 overview 数据）。匹配不到 → jsonError(HTTP 409, "存储池不存在或状态已变化，请刷新后重试")，审计日志记 rejected。
2. 统一系统盘闸门：对每个将被 wipefs/pvremove/lvremove/mdadm --stop/--zero-superblock 的目标设备，执行前用 isSystemDisk(dev)（storage.go:334）检查；任一命中 → 放弃整个删除（在任何破坏动作之前判断），jsonError 拒绝。lvm 分支检查 memberPVs（pvs 拿到之后、vgremove 之前），raid 分支检查 parseMdMemberDisks 的成员，single 分支检查 poolDevice 本身及其分区（lsblk -n -o NAME 展开）。
3. single 分支：umount 之后、wipefs 之前，验证该设备已无任何挂载（findmnt 输出为空），否则拒绝并报错。
4. 闸门与匹配逻辑尽量拆成可注入命令输出的纯函数/小 helper，配表驱动测试；handler 拒绝路径测试参照现有 diskmgmt_gate_test.go / pool_test.go 的模式。注意：buildStorageOverview() 会重置包级缓存，与 Item C 的互斥修复兼容（C 完成后并发安全）。

commit message（原样）：

```
fix(diskmgmt): 删池服务端重解析+破坏性目标系统盘闸门 (consensus P0-A1)
```

## Item B：出厂重置擦盘集合收敛为面板纳管盘（P0-A2）

现状（已核实）：stream.go:603 与 reset.go:105 都对 getDataDisks()（storage.go:433，返回所有非系统整盘）执行 `mdadm --zero-superblock` + `wipefs -a`——外来 USB 备份盘/含数据的外来盘会被误擦。产品前提：专用整机、数据盘由面板独占管理，但外来含数据盘必须防误擦。

修法：
1. 新增 collectManagedWipeTargets()（两条路径共享），在 reset 的任何拆除动作**之前**收集纳管目标集合（并集、去重）：
   a. pvs 列出的全部 PV；
   b. 活跃 MD 阵列的全部成员盘（mdstat / mdadm --detail）；
   c. /data/nas* 前缀挂载点的源设备：LV（/dev/mapper/* 或 /dev/vg*/*）解析为所属 VG 的 PV；/dev/md* 解析为阵列成员；整盘/分区直接纳入。
2. zero-superblock 与 wipefs 只作用于该集合；每个目标落命令前再过 isSystemDisk（命中则跳过该盘并记日志，不中止整个 reset——与 Item A 的"拒绝整个操作"语义不同，这里是尽力拆除）。
3. getDataDisks() 中不在集合内的盘：完全不触碰，step 日志记「跳过未纳管盘 <dev>」。
4. stream.go 与 reset.go 两条路径都改用该 helper，行为保持一致。收集/解析逻辑拆纯函数（pvs/mdstat/findmnt/lsblk 输出文本 → 目标集合），fixture 表驱动测试，必须含「外来 USB 盘有数据签名但从未纳管 → 不在目标集合」用例。

commit message（原样）：

```
fix(diskmgmt): 出厂重置擦盘集合收敛为面板纳管盘,外来数据盘不再误擦 (consensus P0-A2)
```

## Item C：包级缓存竞态 + 配置一致性真比对（P1-B）

C1 现状（已核实）：overview.go:144-145 在 buildStorageOverview 入口把包级变量 cachedPVMappings/cachedRAIDMembers 置 nil；overview.go:714-728 与 732 起的 getter 懒填充——跨请求并发读写同一组变量，data race。
修法：加包级 sync.Mutex 保护全部读写点（getter 在锁内执行外部命令并填充；并发 overview 构建被串行化，可接受），或重构为请求级传参。二选一，报告说明理由。新增并发测试：多 goroutine 并发调 getter / buildStorageOverview 相关路径，go test -race 必须干净。

C2 现状（已核实）：config_sync.go:553-611 CheckConfigConsistency 算出 expectedSamba/expectedNFS 后 `_ =` 丢弃，只检查托管段标记存在性。
修法：从实际文件内容提取 managedStart…managedEnd（含标记行）之间的托管段，与 generateManagedBlock 的期望输出比对（行尾空白与末尾换行归一化后精确比较）；不一致 → 追加 issue「Samba/NFS 托管段与 folders.db 不一致（配置漂移）」。标记存在但内容一致 → 不报。本函数保持只报告不自动修复。删除 `_ =` 两行。托管段提取/比对拆纯函数，fixture 表驱动测试（一致/漂移/无托管段/多段残留四种）。

commit message（原样）：

```
fix(diskmgmt): 包级缓存并发竞态修复+配置一致性检查托管段真比对 (consensus P1-B)
```

## Item D：mdstat 统一块解析（P0-D 文本层；sysfs 重写不在本轮）

现状（已核实）：
- overview.go:830-845 getRAIDStatus 把 (F) 失败成员计入 SpareDisks，(S) 真 spare 反而不计入任何桶；
- overview.go:846-855 SyncPercent 用 extractPercent(line[:idx]) 在 `]` **之前**找百分比——真实 mdstat 的百分比在 `]` 之后（形如 `[=>.........]  resync = 15.2% (298560640/1953382464) finish=17.0min speed=162144K/sec`）；
- raid_expand.go:231-241 handleRAIDExpandFS 只在 MD 头行本身找 reshape/resync 关键词——头行从不携带这些词，闸门形同虚设；
- raid_expand.go:275 起 handleRAIDReshapeStatus 与 raid_expand.go:329 起 waitForResync 只看头行的**下一行**（lines[i+1]）——真实进度行通常在 blocks 行之后（i+2 及以后），会漏检。

修法：
1. 新增 parseMdstatBlocks(content string)：按「行首非空白」切块，每块输出 {mdName、头行字段、成员 token 列表（盘名+槽位+标志 F/S/W 等）、[UU]/[_U] 状态行、进度行列表（kind=resync/recovery/reshape/check/repair、percent、speed、finish）}；percent 从 `]` 之后的片段用正则 `=\s*([0-9.]+)%` 提取。
2. 四个调用点全部改走 parser：
   - getRAIDStatus：RAIDStatus 新增 FailedDisks int `json:"failed_disks"`（已核实前端未引用 spare_disks，加字段向后兼容）；(F)→FailedDisks，(S)→SpareDisks，其余带 [n] token→ActiveDisks；FailedDisks>0 → State="degraded"；SyncPercent 与 State="resyncing" 只依据**属于本阵列块**的进度行。其余 json 字段名不变。
   - handleRAIDExpandFS：该阵列块内存在任一进度行（resync/recovery/reshape/check/repair）→ 拒绝 growfs，保持现有错误响应形态。
   - handleRAIDReshapeStatus / waitForResync：进度行在阵列块内任意缩进行位置都能命中；waitForResync 保留现有语义（首轮 sleep 2s、cleanStreak≥3、600 轮超时返回 error），只把检测源换成 parser。
3. 表驱动测试 fixture 至少覆盖：干净 raid1；raid1 resync 中（进度行在 blocks 行之后）；raid5 reshape（含 backup-file 行）；降级阵列（含 (F)）；含 (S) spare；check/repair 行；**多数组并存**（其他阵列的进度行不得污染本阵列判定）。
4. 不改 raid_expand.go 中与解析无关的流程语义（锁、审计、SSE 事件顺序）。

commit message（原样）：

```
fix(diskmgmt): mdstat 统一块解析 — (F)/(S) 计数修正/百分比取 ] 后/expandfs 闸门与 resync 检测看对行 (consensus P0-D)
```

## 验证（必须执行，输出贴进报告）

```
cd web && go build ./... && go vet ./modules/diskmgmt && go test -race ./modules/diskmgmt -count=1 -cover
```

基线覆盖率 14.9%（go test -race 实测）。要求：全部既有测试通过、覆盖率不降；有测试因行为修正必须调整时，报告里逐个说明理由。

## 报告（TASK-REPORT.md，不 git add）

逐 Item 给出：根因（文件:行）、修法选择、新增/修改测试清单（名称+数量）、handler 错误路径自查表（各拒绝分支 → jsonError 对应关系）、验证命令完整输出、覆盖率数字、偏差与申报事项（含发现的本文与代码不符之处）。
