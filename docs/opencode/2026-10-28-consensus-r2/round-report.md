# consensus R2 任务报告 — RAID 状态链 sysfs 化 + extractVGName mapper 形态 + inactive 阵列纳管

基线：HEAD = 7d0c417。改动范围：仅 `web/modules/diskmgmt/`（新增 1 个源文件 + 1 个测试文件，
修改 5 个既有源文件 + 3 个既有测试文件）。三个 Item 各自独立提交，commit message 原样照抄任务书。

```
58fe99c fix(diskmgmt): RAID 状态链 sysfs 优先采集 — 健康/活动分离,mdstat 降为回退 (consensus 阶段1)
04fc279 fix(diskmgmt): extractVGName 支持 mapper/LV 路径形态 + 删池 lvm 分支 VG 校验与空成员安全拒绝 (consensus minor3)
4ff79fd fix(diskmgmt): 重置纳管集合覆盖 inactive MD 阵列成员 — 防幽灵阵列残留 (consensus minor4)
```

---

## Item A：RAID 状态链 sysfs 优先采集

### 根因 / 修法选择

`getRAIDStatus`（overview.go）与 `raid_expand.go` 的三个调用点（`waitForResync`/
`handleRAIDReshapeStatus`/`handleRAIDExpandFS`）此前全部依赖 `/proc/mdstat` 文本解析——
mdstat 是给人看的格式化输出，字段语义松散、需要正则抠进度条文本。内核在
`/sys/block/<md>/md/` 下暴露的是结构化标量文件（`level`/`array_state`/`degraded`/
`sync_action`/`sync_completed`/`sync_speed`/`reshape_position`/`rd*/state`），字段
稳定、健康与活动语义天然分离，应优先使用。

选择"sysfs 优先、mdstat 保留做回退"而不是"直接替换"：
- 容器环境、受限权限、或极旧内核可能没有完整的 `/sys/block/<md>/md/` 结构；
- mdstat 解析（`mdstat_parser.go` 的 `parseMdstatBlocks` 等）是上一轮 consensus
  （P0-D）刚修好的統一块解析，逐字节保留不删，只是改变调用优先级。

新增文件 `raid_sysfs.go`：
- `readRAIDSysfsValues(mdName)`：读标量文件 + `rd*/state` 成员表，目录不存在返回
  `ok=false`。
- `enumerateSysfsMDNames()`：`filepath.Glob("/sys/block/md*/md")` 枚举，用
  `mdNameRe`（`^md\d+$`，mdstat_parser.go 既有）过滤。
- `buildRAIDStatusFromSysfs(mdName, vals, members) RAIDStatus`：纯函数，供表驱动单测
  直接注入 fixture map。
- `parseSyncCompleted`/`syncCompletedPercent`/`parseSyncSpeed`：sysfs 标量文件的纯
  解析函数。
- `raidHealthFromCounts`：Health 三态推导（见下方语义对应说明）。
- `sysfsCleanStreakResult`/`reshapeETAMinutes`：供 `waitForResync`/
  `handleRAIDReshapeStatus` 的轮询/估算逻辑抽出的可单测纯函数核心。

`RAIDStatus` 新增两个字段（已有字段名/语义/json tag 全部保留，向后兼容追加）：
- `Health string json:"health"`：`"normal"/"degraded"/"failed"`。
- `Activity string json:"activity"`：sysfs `sync_action` 原值
  （`idle`/`resync`/`recover`/`reshape`/`check`/`repair`），mdstat 回退路径按进度行
  Kind 映射到同名语义（新增 `mdstatProgressKindToActivity`：`recovery→recover`，其余
  原样透传）。

`State`/`SyncPercent` 兼容派生规则（sysfs 与 mdstat 两条路径语义统一）：
- `State`：Health 为 degraded/failed → `"degraded"`；`sync_action`/进度 Kind 非 idle →
  `"resyncing"`；否则沿用底层状态原值（sysfs 的 `array_state`，mdstat 的头行第3字段）。
- `SyncPercent`：sysfs 路径从 `sync_completed` 算（`syncCompletedPercent`），mdstat
  路径沿用既有的进度行 `Percent` 字段，两者都保持 `"N.N%"` 字符串形态。

`getRAIDStatus`：先 `enumerateSysfsMDNames()`，枚举到任意 md 设备则逐个
`readRAIDSysfsValues`+`buildRAIDStatusFromSysfs`；枚举为空或全部 `readRAIDSysfsValues`
失败（结果为空切片）时，回退 `os.ReadFile("/proc/mdstat")` + `buildRAIDStatusFromMdstat`
（完整保留，未删除任何既有逻辑）。

`buildStorageOverview`（overview.go:214-222 原 203-209 区域，行号因注释增加略有偏移）
池健康判定改为：

```go
healthy = ra.Health == "normal" && ra.Activity == "idle"
```

对 mdstat 回退路径同样成立——`buildRAIDStatusFromMdstat` 现在也填充 Health/Activity
（Activity 默认 `"idle"`，有进度行时取该行 Kind 映射值；Health 复用
`raidHealthFromCounts`，`arrayState` 参数用 mdstat 头行第3字段顶替 sysfs 的
`array_state`，`raidDisks` 参数用本阵列解析出的 `Disks` 顶替 sysfs 的 `raid_disks`——
mdstat 的成员 token 在阵列失活前不会消失，`Disks` 即可代表设计成员数）。Issues/告警
文案（"RAID %s 正在同步 %s" / "状态: %s"）未改动，仍读 `State`/`SyncPercent`，行为不变。

`raid_expand.go` 三处改动（均遵循"sysfs 优先、mdstat 完整回退"）：

1. **`waitForResync`**：优先读 `readRAIDSysfsValues`，取 `sync_action` 判断是否
   `sysfsResyncDone`（`""`或`"idle"`）；完成判据仍是连续 `cleanStreak`（=3）轮都是
   idle/无进度；首轮 `sleep 2s`、600 轮上限（10 分钟）、超时返回 error 语义**原样保留**。
   sysfs 目录不存在时回退原 `parseMdstatBlocks` 判定路径。进度事件 `Detail` 字段格式
   `"同步进度: " + percent` 不变，percent 来源按路径分别是 `syncCompletedPercent` 或
   mdstat 进度行的 `Percent`。
2. **`handleRAIDReshapeStatus`**：sysfs 路径下 `reshaping = sync_action != idle`；
   `progress` 用 `syncCompletedPercent`；`speed` = `sync_speed + "K/sec"`（sysfs
   `sync_speed` 是裸数字，拼接单位字符串）；`eta` 由 `reshapeETAMinutes((total-done),
   speed)` 估算，一位小数分钟数，`speed<=0`/`total<=done` 时置空（不展示误导性的
   "0.0"）。sysfs 不可用时完整回退 mdstat 路径（`finish=` 字段），这条回退路径逐字节
   未改动。
3. **`handleRAIDExpandFS`** 闸门：sysfs 路径下 `sync_action != idle` 或
   `reshape_position` 不是 `""/"none"/"0"` 即拒绝 growfs（错误响应体 JSON 形态
   `{"error": "阵列仍在重构中..."}` 不变）；sysfs 不可用时回退
   `parseMdstatBlocks`+`blockHasProgress` 判定（P0-D 轮已修好的块内进度行判据，完整
   保留）。

### 真机 fixture 与实现语义对应

| 真机状态 | array_state | degraded | sync_action | 本实现 Health | 本实现 Activity | 本实现 State |
|---|---|---|---|---|---|---|
| 干净 | clean | 0 | idle | normal | idle | clean |
| 降级 | **clean（陷阱）** | 1 | idle | **degraded** | idle | degraded |
| 重建 | clean | 1 | **recover**（sysfs 值） | degraded | recover | degraded |
| 恢复完成 | clean | 0 | idle | normal | idle | clean |

- **array_state=clean + degraded=1 陷阱**：`raidHealthFromCounts` 的判定条件是
  `degraded > 0 || failedDisks > 0`（不检查 arrayState 是否"异常"），`arrayState`
  只在显式为 `"inactive"` 或 `degraded>=raidDisks` 时参与"failed"判定，正常路径下
  完全不信任 `array_state` 的"看起来正常"。四态 fixture 测试
  `TestBuildRAIDStatusFromSysfs_FourStates/降级态_array_state仍是clean` 直接断言
  `Health=="degraded"`。
- **sync_action=recover vs mdstat 文本=recovery**：两者不是同一字符串，`buildRAIDStatusFromSysfs`
  对 sysfs 路径直接透传 `sync_action` 原值到 `Activity`（已经是 `recover`，无需转换）；
  `buildRAIDStatusFromMdstat`（回退路径）用 `mdstatProgressKindToActivity` 显式把
  mdstat 的 `Kind=="recovery"` 映射成 `"recover"`，让两条路径对同一真实状态产出一致
  的 `Activity` 值。`TestMdstatProgressKindToActivity` 单独覆盖这个映射。
- **sync_completed 单位**：sysfs 格式 `"<done> / <total>"`（KB），百分比 =
  done/total*100，与 mdstat 文本里现成的百分比字符串是两条独立计算路径，各自内部
  一致，不互相替换验证（`parseSyncCompleted`/`syncCompletedPercent` 单测用真机采样
  `"0 / 104790016"` 验证 0.0%，用构造值 `"52395008 / 104790016"` 验证 50.0%）。
- **sync_speed 单位 K/sec**：`handleRAIDReshapeStatus` 拼接字符串时显式加
  `"K/sec"`后缀；`reshapeETAMinutes` 按 KB/（K/sec）=秒 再 /60 转分钟。

### 新增与修改测试清单

- `overview_test.go`：
  - 修改既有 `TestBuildRAIDStatusFromMdstat`（8 个子用例）的 `want` fixture，补
    `Health`/`Activity` 期望值（字段新增后原有结构体相等比较需要同步更新，行为本身
    未变，只是断言更完整）。
  - 新增 `TestExtractVGName`（5 子用例，Item B 用，见下）、
    `TestUnescapeMapperVGName`（5 子用例，Item B 用）。
- `raid_sysfs_test.go`（新文件，14 个测试函数）：
  - `TestBuildRAIDStatusFromSysfs_FourStates`（4 子用例，真机四态 fixture 逐字段构造）
  - `TestParseSyncCompleted`（7 子用例）
  - `TestSyncCompletedPercent`（4 组）
  - `TestRaidHealthFromCounts`（5 子用例，含降级/array_state陷阱/inactive/failed）
  - `TestEnumerateSysfsMDNames_NoSysMdInTestEnv`
  - `TestReadRAIDSysfsValues_DirMissing`
  - `TestMdstatProgressKindToActivity`（5 组）
  - `TestSysfsCleanStreakResult`（5 子用例，覆盖"中途非idle streak清零重新累计"）
  - `TestReshapeETAMinutes`（5 子用例）
  - `TestParseSyncSpeed`（4 组）

### waitForResync/reshape-status 回退路径保留说明

`waitForResync` 没有暴露 sysfs/mdstat 路径为参数，无法在不改生产函数签名的前提下
mock `/sys/block/.../sync_action` 的真实轮询序列走到"sysfs 可用"分支的端到端测试
——测试沙箱环境没有真实 `/sys/block/md*`，`readRAIDSysfsValues` 恒返回 `ok=false`，
`waitForResync`/`handleRAIDReshapeStatus`/`handleRAIDExpandFS` 在单测环境下实际走的
都是 mdstat 回退分支（与上一轮 consensus 的既有测试
`TestWaitForResyncReturnsNilWhenNoResyncLine` 覆盖的路径一致，未改动、仍通过）。
"连续 cleanStreak 轮 idle 才完成"判定的核心逻辑已抽成纯函数
`sysfsCleanStreakResult` 并有独立表驱动单测（含"中途出现 recover，streak 清零重新
累计"用例），sysfs 真实轮询分支的正确性以这个纯函数单测 + 代码审查方式验证，报告
在此明确说明该覆盖边界。

---

## Item B：extractVGName mapper 形态 + 删池 lvm 分支空成员安全拒绝

### 根因 / 修法选择

`extractVGName` 此前两个分支都是 `filepath.Base(device)`：对 `/dev/mapper/vg_nas-data`
会得出整段 `"vg_nas-data"` 当 VG 名——`pvs -S vg_name=` 用这个错误值查询永远查空但不
报错（命令本身执行成功，只是过滤条件没有命中任何记录），导致 `handlePoolDelete` 的
lvm 分支成员闸门"空转通过"：`memberPVs` 恒为空切片但没有触发任何错误路径，继续走
破坏性命令（lvremove/vgremove），真实的 PV 盘因为从未被正确识别而不会被
pvremove/wipefs，留下 LVM2_member 签名复现幽灵盘。

重写 `extractVGName` 为纯函数，按设备形态分三支：
1. `/dev/mapper/` 前缀 → 走 `unescapeMapperVGName`（mapper 命名专用解析）；
   `control` 设备特判返回 `""`。
2. 非 mapper 且 `filepath.Base(device) == device`（没有路径分隔符）→ 原样返回 base
   （`/dev/vg_nas` 这种整盘/VG 路径本身，行为与此前一致）。
3. 其余（LV 直路径 `/dev/vgname/lvname`）→ 返回父目录名（`filepath.Base(filepath.Dir(device))`），
   但排除父目录名是 `""`/`"."`/`"/"`/`"dev"` 的退化情况（这些情况回退到原 base 逻辑，
   避免对不符合预期的奇怪路径产生误判）。

`unescapeMapperVGName`：逐字符扫描 mapper 名，遇到 `"--"` 视为转义对（还原成一个
`"-"`，跳两个字符），遇到单个 `"-"`（后面不是另一个 `"-"`）视为 VG/LV 分隔符，停止
扫描——这正是 LVM dm-mapper 的官方命名规则（VG/LV 名内字面 `-` 转义为 `--`，VG 与
LV 之间用单个未转义 `-` 分隔）。

### 各调用点影响说明

`extractVGName` 有三个调用点，行为变化对它们的影响：

- **`overview.go:216`（池显示名，`buildStorageOverview` 内 `poolName = extractVGName(poolDev)`）**：
  `poolDev` 来自 `detectPoolDevice`，对 mapper 设备已经提前解析成 `/dev/<vgName>`
  形态（见 `detectPoolDevice` 的 mapper 分支，调用 `matchVGForMapper` 查真实 vgs 输出）
  ——这里传进 `extractVGName` 的参数实际上几乎不会是原始 mapper 路径，而是已经规整
  过的 `/dev/vgname`，走分支2，行为与此前一致，无实质影响。
- **`pool.go:546`（删池 lvm 分支，`vgName := extractVGName(poolDevice)`）**：
  `poolDevice` 同样来自服务端重解析后的实时 `PoolSummary.Device`（P0-A1 闸门产出），
  形态同上——分支2为主。但本次新增的两道闸门（vgs 存在性校验 + pvs 空成员拒绝）
  是独立于 `extractVGName` 具体返回值的防御层，即使解析出意外的 VG 名也会被这两道
  闸门拦住，不会裸奔到破坏性命令。
- **`pool.go:699`（扩容路径 `lvmVgName := extractVGName(poolDevice)`，用于
  `filterFstabForDeletedPool` 的 fstab 行匹配）**：同样的 `poolDevice` 来源，分支2
  为主，行为不变。

本次改动的直接受益场景是：如果未来有调用点直接传入原始 `/dev/mapper/vgname-lvname`
路径（而不是先过 `detectPoolDevice` 规整），现在能正确解析出 VG 名，不会再静默得出
错误值。

### handlePoolDelete lvm 分支新增闸门

在**第一条破坏性命令（lvremove 循环）之前**新增两道检查（均在已有的 pvs 调用
前后，不改变既有系统盘闸门的相对顺序）：

1. **vgs 存在性校验**：`vgs --noheadings -o vg_name` 失败 → 500（既有 pvs-失败模式
   对齐，"为安全起见拒绝删除"）；输出中无该 `vgName`（逐行精确 trim 比较，新增纯
   函数 `vgsOutputContainsVGName`，不用子串匹配避免 `vg_nas` 误命中 `vg_nas_backup`）
   → 409 `"卷组 %s 不存在或名称解析失败"`，审计记 `rejected`。
2. **pvs 成功但 memberPVs 为空的安全拒绝**：既有的 "pvs 执行失败→500" 分支保留不变；
   新增在 pvs 成功但 `strings.Fields(pvsOut)` 为空时 → 500
   `"卷组 %s 没有查到任何物理卷成员，为安全起见拒绝删除"`，审计记 `rejected`。
   真实存在的 VG 必有至少一个成员 PV，空集合意味着名称解析异常或卷组状态异常，
   这种情况继续执行 lvremove/vgremove 会"成功删除一个空的操作对象"而真正的 PV
   盘完全没被触碰，复现幽灵盘。

### 测试清单

- `overview_test.go`：`TestExtractVGName`（5 子用例：VG路径本身/LV直路径/mapper命名/
  mapper转义还原/control特判）、`TestUnescapeMapperVGName`（5 子用例：无连字符/单个
  转义/多个转义/无分隔符兜底/空字符串）。
- `pool_test.go`：`TestVgsOutputContainsVGName`（7 子用例：精确命中×2/前缀相似不
  误命中/不存在/空输出/空VG名不命中空行）。

handlePoolDelete 的 lvm 分支新增闸门本身（vgs/pvs 调用）依赖真实 `common.SudoOutput`，
测试沙箱没有真实 lvm 环境无法端到端覆盖；现有
`TestHandlePoolDeleteRejectsUnmatchedLivePool` 验证的是更早的 P0-A1 服务端重解析闸门
（在到达 switch 分支之前就 409），lvm 分支内部新增的两道闸门以纯函数
`vgsOutputContainsVGName` 单测 + 代码审查方式验证，报告在此说明覆盖边界。

---

## Item C：inactive MD 阵列成员纳入重置擦盘集合

### 根因 / 修法选择

`managed_wipe.go` 的 `mdstatMemberDisks`（供 `collectManagedWipeTargets` path (b)：
"全部 MD 阵列成员盘"）此前用字符串 `strings.Contains(trimmed, " : active")` 过滤
阵列块头行——inactive 阵列（成员盘仍带 RAID 超级块，常见于掉盘后未完全组装、或
人为 `mdadm --stop` 后盘未清理的场景）的头行是 `"md0 : inactive sdb[1](S) sda[0](S)"`，
不含 `" : active"`，整个块被跳过，成员盘不进纳管集合。出厂重置流程
（reset.go/stream.go）只对 `collectManagedWipeTargets` 返回的集合执行
`zero-superblock`+`wipefs`，inactive 阵列的成员盘因此完全不会被擦除，残留的 RAID
超级块签名可能在下次开机时被 `mdadm --assemble --scan` 自动重组，形成"幽灵阵列"
（用户以为已恢复出厂设置，重启后却又看到一个旧阵列）。

修法：改走既有 `parseMdstatBlocks`（`mdstat_parser.go`，上一轮 consensus P0-D 已经
做好的统一块解析）。核实其实现（`mdstat_parser.go:59-77`）：块切分只按"行首非空白
即头行"识别，头行只要 `fields[1]==":"` 且 `fields[0]` 匹配 `^md\d+$` 就开新块，**不
区分 `fields[2]` 是 `active` 还是 `inactive`**，`MemberTokens` 从 `fields[3:]` 提取、
用 `memberTokenRe` 匹配——inactive 头行 `"md0 : inactive sdb[1](S) sda[0](S)"` 的
`fields[3:]` 正是 `["sdb[1](S)", "sda[0](S)"]`，`memberTokenRe`（`^[a-zA-Z0-9]+\[\d+\]`）
对带 `(S)`/`(F)` 后缀的 token 同样能匹配前缀。**核实结论：`parseMdstatBlocks` 本身
不需要任何修改**，它对 inactive 头行的处理天然正确，问题完全出在
`mdstatMemberDisks` 自己维护的那套独立的 `" : active"` 字符串过滤上。

`mdstatMemberDisks` 改为遍历 `parseMdstatBlocks` 产出的块、从每块的 `MemberTokens`
提取盘名加 `/dev/` 前缀，去重逻辑（`seen` map）与返回形态（`[]string`）完全不变。

### 范围扩展说明（申报）

任务书只点名 `mdstatMemberDisks`，但核实发现 `mdstatMembersByDevice`
（同文件，供 `collectManagedWipeTargets` path (c)：`/data/nas*` 挂载点源设备是 MD
阵列时的成员解析）是**同一个 `" : active"` 过滤器 bug 的另一个独立实例**。两个函数
此前各自维护一份几乎相同但略有差异的字符串解析逻辑，`mdstatMemberDisks` 修好后若
不同步修 `mdstatMembersByDevice`，会出现"同一份 mdstat 文本，两个函数对 inactive
阵列给出不一致结果"的维护陷阱，且与任务书"防幽灵阵列残留"的既定目标不完全一致
（虽然 inactive 阵列通常不会被挂载，path (c) 命中它的概率低于 path (b)，但两者
本该同源同逻辑）。已一并改走 `parseMdstatBlocks`，去重/映射返回形态不变。这是本次
在既定 Item C 范围内的最小必要扩展，不涉及其他文件/模块。

### 测试清单

- `managed_wipe_test.go`：
  - `TestMdstatMemberDisks_InactiveArray`（inactive 单阵列，验证成员纳入）
  - `TestMdstatMemberDisks_ActiveAndInactiveMixed`（active+inactive 混合阵列，
    验证互不遗漏）
  - `TestMdstatMemberDisks_FailedAndSpareFlags`（成员带 (F)/(S) 标志不影响归集）
  - `TestMdstatMembersByDevice_InactiveArray`（范围扩展部分的对应验证）
  - `TestCollectManagedWipeTargets_InactiveArrayMembersIncluded`（端到端：inactive
    阵列未挂载场景下，成员盘仍通过 path (b) 进入最终纳管集合）
  - 既有 `TestMdstatMemberDisks`/`TestMdstatMemberDisks_NoArrays`/
    `TestMdstatMemberDisks_MultiArray`/`TestMdstatMembersByDevice` 等全部保留不改，
    逐字节通过（验证本次改动对 active 阵列场景的行为零回归）。

---

## 验证输出

```
$ cd web && go build ./... && go vet ./modules/diskmgmt && go test -race ./modules/diskmgmt -count=1 -cover
ok  	nas-panel/modules/diskmgmt	12.552s	coverage: 23.3% of statements
```

`go build ./...`、`go vet ./modules/diskmgmt` 均无输出（通过）。`go test -race` 全部
通过，无 data race 报告。覆盖率 23.3%，高于基线 21.6%（三个 Item 均为新增纯函数 +
对应表驱动单测，覆盖率只增不减）。

分段验证记录（开发过程中）：
- Item A 提交前单独验证：23.0%
- Item B 提交前单独验证：23.5%
- Item C 提交前单独验证：23.3%（`buildRAIDStatusFromSysfs`/`extractVGName` 等新函数
  占比稀释，三项合并后仍高于基线）

## 偏差与申报

1. **Item C 范围扩展**：`mdstatMembersByDevice` 一并修复，详见 Item C"范围扩展说明"
   小节。未扩展到其他文件/模块。
2. **Item A 的 waitForResync/handleRAIDReshapeStatus sysfs 分支未端到端测试**：
   测试沙箱无真实 `/sys/block/md*`，这两个函数的 sysfs 轮询/估算核心逻辑已抽成
   独立纯函数（`sysfsCleanStreakResult`/`reshapeETAMinutes`/`parseSyncCompleted`
   等）并有完整表驱动单测，sysfs 分支的集成正确性以"纯函数单测 + 代码审查"方式
   验证，详见 Item A 对应小节说明。这与既有 `waitForResync` 测试
   （`TestWaitForResyncReturnsNilWhenNoResyncLine`）在上一轮 consensus 报告中
   已申报的覆盖边界一致，未引入新的未知风险。
3. 未发现与任务书描述不符的代码现实（overview.go/raid_expand.go/pool.go/
   managed_wipe.go 的改动前状态均与任务书"现状"描述核实一致）。
4. 硬约束 1-6 均已遵守：未读写仓库外文件；只改动 `web/modules/diskmgmt/` 下文件
   （含新增 `raid_sysfs.go`/`raid_sysfs_test.go`）；未执行真实磁盘/sudo/mdadm/lvm
   命令（所有验证基于 fixture 字符串构造的纯函数单测）；三个 Item 已各自独立
   commit，message 原样；既有全部测试通过；覆盖率 23.3% > 21.6% 基线。
