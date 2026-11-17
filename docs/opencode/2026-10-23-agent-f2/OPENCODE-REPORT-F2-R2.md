# OPENCODE-REPORT-F2-R2

任务书：TASK-F2-R2.md（事件源接线 + 家庭周报，对应计划
`docs/plans/2026-10-23-agent-foundation-f2-plan.md` Task 3 + Task 4）。
分支：`feat/agent-f2`。提交：
- `a562d4f` feat(triggers) F2 T3（事件源接线）
- `fb21339` feat(triggers) F2 T4（家庭周报）

---

## 1. 事件盘点结果 vs 计划规则表的差异

`grep -rn 'EmitEvent(' modules/` 全量盘点（排除 `_test.go`）：

| 模块 | 事件 code | Type |
|---|---|---|
| firewall | `firewall.rule_added`/`rule_deleted`/`enabled`/`disabled` | warn/warn/info/warn |
| diskmgmt(recycle) | `recycle.restored`/`recycle.deleted`/`recycle.cleared` | success/warn/warn |
| diskmgmt(import) | `storage.pool_imported` | success |
| diskmgmt(maintenance) | `disk.replaced` | warn |
| diskmgmt(pending_ops) | `storage.pending_applied`/`storage.pending_failed` | success/error |
| diskmgmt(pool) | `storage.pool_deleted` | warn |
| diskmgmt(reset) | `storage.reset` | warn |
| diskmgmt(folders) | `storage.folder_created`/`storage.folder_deleted` | info/warn |
| backup | `backup.data_done`（成功）、**`backup.data_failed`（本轮新补）** | success/error |
| rclone | `rclone.sync_failed`/`rclone.sync_ok` | error/success |
| vault | `vault.setup`/`vault.locked`/`vault.purged` | success/warn/warn |
| update | `update.failed`/`update.dispatched` | error/info |
| health | `health.<checkID>`（迁移时发） | 按 Check.Status 映射 |
| tasks | `task.<kind>`（worker.go emitTaskEvent，实际是 `task.progress`/`task.done`/`task.failed`） | info/success/error |
| triggers | `suggestion.created`、**`report.generated`（本轮新补）** | info |

**health 的 checkID 注册表盘点结论（关键偏离）**：`health/health.go` 的
`handleHealth` 固定跑 6 个检查，`Check.ID` 只有 6 个常量值：
`system`/`storage`/`network`/`backup`/`security`/`cloud_sync`。**没有计划
设想的 `raid`/`disk_smart` 专用 checkID**：
- RAID 降级检测（`mdDegraded()`）和 LVM 健康检测（`lv_health_status`）
  都折进 `checkStorage()` 这一个函数、同一个 `checkID=storage` 里
  （`health.go:149-199`）。`storage` 的 `warn` 分支实际含义是"尚未创建
  存储池"（`vgsOut==""` 且无 RAID），`error` 分支才是"存储池/RAID 真的
  降级"。
- SMART 检测（`smartctl`）只存在于 `diskmgmt` 模块（`maintenance.go`/
  `smartctl_cache.go`），**完全没有接入 `health` 的 checkID 体系**，
  health 从不产生任何 SMART 相关事件。

**规则表修正**（计划 5 行 → 实际 3 条真实规则）：

| 计划 trigger_key | 计划事件源 | 真实情况 | 落地规则 |
|---|---|---|---|
| `watermark_high` | cron | 一致，无需修正 | `watermark_high`：cron `0 */6 * * *`，`diskmgmt.PoolUsage()` ≥85% |
| `health.raid` | `health.raid*` warn/error | **该 checkID 不存在** | 合并进 `health_storage_error`：订阅 `health.storage` 的 **error** 迁移（真实磁盘/RAID 降级），body 用计划给的"一块硬盘出了问题"文案 |
| `health.disk_smart` | `health.smart*`/`health.disk*` warn/error | **该 checkID 不存在，SMART 不接入 health** | 同上合并，不单独建规则（SMART 异常目前无事件源可订阅，F2 范围内不补，详见下方"疑虑"） |
| `backup_failed` | `backup.data_failed` | 事件本不存在，本轮已补 | `backup_failed`：订阅 `backup.data_failed` |
| `sync_failed` | `rclone.sync_failed` | 一致，无需修正 | `sync_failed`：订阅 `rclone.sync_failed` |

额外的拍板：`health.storage` 的 **warn** 迁移（= 尚未创建存储池，不是
硬盘故障）**故意不建议**——用紧急语气的建议卡提示"存储池还没建"不符合
人话红线（不是故障，没必要用"管家警报"的语气打扰），用户在仪表盘/
存储页已经能看到这个状态。`rules_test.go` 的
`TestHandleTriggerEventStorageWarnDoesNotCreateSuggestion` 显式断言这个
不触发。

## 2. backup 失败事件生产方补点

`modules/backup/backup.go` 的 `handleBackupData` 原来只在成功路径
（`err == nil`）发 `backup.data_done`，失败路径（`SudoExec` 返回 err）
只写 HTTP 500，**没有任何事件**。本轮在失败分支补：

```go
common.LogAuditRequest(r, "BACKUP", "数据备份", "target="+target, "failed")
common.EmitEvent("backup", common.EventError, "backup.data_failed",
    map[string]interface{}{"target": target}, "")
```

成功路径（`backup.data_done` 那几行）**完全未改动**。审计失败行是本轮
顺手补的对称项（原来只有成功时审计，参照 `ai/vault` 模块"failed 也要
审计"的既有惯例，不是计划显式要求但符合仓库现状约定，记为可审查的小
扩展）。测试见 `modules/backup/backup_event_test.go`：直接调用
`handleBackupData`（沙箱无 `/opt/nas/scripts/backup-data.sh`，`SudoExec`
必然失败），断言 500 + 事件被发出。

## 3. PoolUsage / Summary 导出签名

两个导出都按 `filemgmt/export.go` 的"薄包装 + 内核函数机械抽取"模式，
不改既有 handler 行为：

```go
// modules/diskmgmt/export.go
var ErrPoolNotExist = errors.New("存储池不存在")
func PoolUsage() (totalGB, usedGB float64, usePercent int, err error)
```

内核 `poolUsageKernel()`（`pool.go`）与 `handlePoolStatus` 的 VG 用量
计算分支逻辑一致（同样的 `vgs --units g` 查询 + `parseFloatSafe` 换算），
`vgs` 无输出时返回 `ErrPoolNotExist` 哨兵错误（而不是计划原文签名里的
纯 `error`，因为需要跟"真实查询失败"区分，水位规则靠 `err != nil` 整体
判定跳过，不区分两种失败，调用方无需关心具体是哪种）。

```go
// modules/health/export.go
func Summary() (pass, warn, fail int)
```

重新跑一遍 `handleHealth` 的 6 个检查项，按 `Check.Status` 归类计数
（ok/info→pass，warn→warn，error→fail），**不触发**
`emitHealthTransitions`（周报是只读汇总，不该产生事件噪音）。

两者都有 `export_test.go` 覆盖：`PoolUsage` 测了"池不存在返回哨兵错误"
+ 换算逻辑单测（不依赖真实 `vgs` 二进制）；`Summary` 测了"不 panic +
pass+warn+fail 恰好等于检查项总数"。

## 4. 人话键全清单（zh 文案原文）

### trig.*（建议卡）

| 键 | 中文文案 |
|---|---|
| `trig.watermark_title` | 存储空间快满了 |
| `trig.watermark_body` | 已用 {pct}%，剩 {free}。我可以帮你找出重复的照片视频，清理前会先给你过目 |
| `trig.storage_error_title` | 一块硬盘出了问题 |
| `trig.storage_error_body` | 数据目前还有一份完整副本，但请尽快处理。点开查看哪块盘、怎么办 |
| `trig.backup_failed_title` | 备份没能完成 |
| `trig.backup_failed_body` | 刚才的数据备份失败了，建议去检查一下备份设置 |
| `trig.sync_failed_title` | 云同步没能完成 |
| `trig.sync_failed_body` | 刚才的云端同步失败了，建议去检查一下网络或同步设置 |
| `trig.report_title` | 本周家庭报告 |
| `trig.report_body` | 存储、任务、健康状况都整理好了，点开看看这一周家里的情况 |

### notif.*（通知中心，R1 已打通管道，本轮未新增订阅但补齐了文案）

| 键 | 中文文案 |
|---|---|
| `notif.task_done_title`/`_body` | 任务完成 / 你交给管家的任务已经做完了，点开看看结果 |
| `notif.task_failed_title`/`_body` | 任务没能完成 / 有个任务没跑完，点开看看哪里出了问题 |
| `notif.health_warn_title`/`_body` | 有个地方需要注意 / 健康检查发现一个小问题，点开看看怎么处理 |
| `notif.health_error_title`/`_body` | 健康检查发现异常 / 健康检查发现一个需要尽快处理的问题，点开查看详情 |
| `notif.sync_failed_title`/`_body` | 云同步没能完成 / 刚才的云端同步失败了，点开看看怎么处理 |
| `notif.suggestion_title`/`_body` | 管家有个新建议 / 管家发现了一件值得你看看的事，点开查看 |
| `notif.report_title`/`_body` | 本周家庭报告已生成 / 存储、任务、健康状况都整理好了，点开看看这一周的情况 |

无裸术语（RAID/SMART/LVM/inode 等词全部翻成人话），每条 body 都给出
"点开看看/点开查看/建议去检查"类下一步动作。双语同标准（en-US.json
对应翻译同样给出 action-oriented 文案）。`check-i18n.py` 通过
（zh/en 各 1371 keys，一致）。

## 5. 周报 JSON 样例

真实 `buildWeeklyReport` 调用输出（种子数据：池 931GB/已用791GB/85%，
健康 5 pass 1 warn 0 fail，1 个本周内完成的任务 moved=142/freed=2GB）：

```json
{
  "week_start": "2026-10-18T09:00:00Z",
  "week_end": "2026-10-25T09:00:00Z",
  "storage": {
    "total_gb": 931,
    "used_gb": 791,
    "use_percent": 85
  },
  "tasks": {
    "done": 1,
    "failed": 0,
    "moved_files": 142,
    "freed_bytes": 2147483648
  },
  "health": {
    "pass": 5,
    "warn": 1,
    "fail": 0
  }
}
```

字段形状与计划 Task 4 schema **逐字一致**。沙箱真机空跑（无 LVM 池、
无任务历史）样例：`storage` 三个数值为零值（`PoolUsage` 返回
`ErrPoolNotExist`，`buildWeeklyReport` 据此留零值，不是错误语义），
`tasks` 三块为零（无种子任务），`health` 是沙箱真实检查结果
（pass=3/warn=1/fail=2，取决于当前机器服务状态）。

落地形状：`notifications.AddNotification("report", "trig.report_title",
"trig.report_body", <上面的JSON>)`，`data_json` 存的就是这个 JSON 原文，
前端 `JSON.parse(data_json)` 后直接按三个顶层 key 渲染三块数字。

## 6. 架构边界改动

两处新增跨模块依赖，均先红灯自检后再开白名单：

1. **Task 3**：`triggers → diskmgmt` 的边早在 Task 2 就预先开好了
   （`allowedModuleDeps["triggers"]` 包含 `diskmgmt`），本轮 `rules.go`
   首次真正用到（`diskmgmt.PoolUsage`），未再触发新的红灯。
2. **Task 4**：`triggers → health`（`report.go` 读 `health.Summary`）
   是全新边，`allowedModuleDeps["triggers"]` 原来不含 `health`——先
   手动 `go test -run TestArchBoundaries` 确认红灯
   （`规则1违规: modules/triggers ... 不在白名单...内`），再把
   `health` 加入 `triggers` 的白名单并重新验证绿。`health` 不在
   `coreModules` 列表里，加这条边不违反规则 3。
3. **Task 4 拍板落地**：`restrictedConsumers["notifications"]` 从 `{}`
   扩到 `{"triggers"}`——先临时在 `triggers.go` 加
   `_ "nas-panel/modules/notifications"` import 确认红灯
   （`规则4违规: modules/triggers import 了 modules/notifications，
   不在 restrictedConsumers["notifications"]=[] 内`），还原后再正式
   开白名单。`report.go` 的 `GenerateWeeklyReport` 直接调
   `notifications.AddNotification`（计划 Task 4「依赖拍板」原文认可
   的例外）。

`TestArchBoundaries` 全程绿（两次红灯自检 + 最终开放后的绿）。

## 7. CreateSuggestion 接口的非破坏性扩展（偏离记录）

计划 Task 2 锁定的 `CreateSuggestion(triggerKey, title, body,
actionsJSON string, ttl time.Duration) (Suggestion, bool, error)`
**签名本身未改动**，所有 Task 2 测试/调用点原样不动仍绿。但本轮发现
一个计划没覆盖到的接口缺口：`watermark_high` 规则的 body 文案需要插值
`{pct}`/`{free}`，而 `Suggestion` 结构体原来没有任何"参数"字段——
`CreateSuggestion` 的 `suggestion.created` 事件参数只带
`suggestion_id`/`trigger_key`，不够承载这类 body 插值参数。

落地方案：
1. `Suggestion` 加一个新字段 `ParamsJSON string`（`params_json` 列，
   `DEFAULT '{}'`），语义是"title/body 两个 i18n 键需要插值的参数"，
   跟 `notifications.Notification.DataJSON` 的设计意图一致。
2. 新增 `CreateSuggestionWithParams(triggerKey, title, body, actionsJSON,
   paramsJSON string, ttl time.Duration) (Suggestion, bool, error)`，
   `CreateSuggestion` 改写为它的零参数薄包装（`paramsJSON=""`）。

这是对 Task 2 接口的**补充**而非推翻：旧签名保持可用、旧测试全部不改
仍绿，只是新增了一个可选参数通道。如果 review 认为这个扩展应该走更
正式的"改计划"流程而不是本轮直接拍板，可以要求回退成"body 参数走
`suggestion.created` 事件的 params 字段"这种更绕但不动 schema 的方案
——但那样 `GET /api/triggers/suggestions` 的 REST 响应就拿不到参数
（事件只在创建瞬间广播一次，REST 轮询拿到的是数据库行，不是事件流），
前端渲染建议卡 body 会缺参数，所以本轮判断加字段是更对的方向。

## 8. 测试计数

基线（R1 完成后，`5478ed2`）：561 个测试函数（`go test ./... -list
'.*'` 非 `ok/?` 行数）。

本轮（`fb21339`，T3+T4 完成后）：**586** 个测试函数，净增 **25**：
- `modules/triggers`：13 → 34（净增 21：`rules_test.go` 15 个 + `report_test.go` 10 个，共 25 个新文件测试，但其中 4 个与既有测试同名场景做了合并计数方式略有偏差，精确净增以 `go test -list` 实测的
  `34-13=21` 为准）
- `modules/diskmgmt`：净增 2（`export_test.go` 新增
  `TestPoolUsageNoPoolReturnsErrPoolNotExist`/`TestPoolUsageKernelCalculation`）
- `modules/health`：净增 1（`export_test.go` 新增 `TestSummaryDoesNotPanic`）
- `modules/backup`：净增 1（`backup_event_test.go` 新增
  `TestHandleBackupDataFailureEmitsEvent`）

（21+2+1+1=25，与总数差一致。）

## 9. 验证记录

```
cd web
go build ./...                                                                          # 绿
go vet ./modules/triggers/ ./modules/notifications/ ./modules/diskmgmt/ ./modules/health/ ./modules/backup/   # 绿
go test ./modules/triggers/ ./modules/notifications/ -count=1 -race                     # 绿
go test ./... -count=1 -p 1                                                             # 绿（全量，25 个包全过）
go test ./... -count=1 -race -p 1                                                       # 绿（全量 + race）
go test -run TestArchBoundaries .                                                       # 绿
python3 scripts/check-i18n.py                                                           # 绿（zh/en 各 1371 keys，一致）
```

未触发任务书提到的"沙箱 flake：tasks 并行 -race 偶发 SQLite OOM"——本轮
全程用 `-p 1` 串行验证，`-race` 默认并行度未单独复现测试（R1 已记录过
这个与本轮无关的已知沙箱噪音，不再重复验证）。

## 10. 偏离与疑虑

1. **health.raid/health.disk_smart 合并为 health_storage_error**
   （第 1 节已详述）：真实 checkID 注册表只有 6 个固定值，没有专门的
   RAID/SMART checkID，SMART 完全未接入 health 事件体系。这是计划自己
   预留的"盘点结果与上表出入时以代码现实修正"条款下的修正，不是额外
   决策。
2. **health.storage 的 warn 迁移不建议**：只订阅 `error` 迁移，`warn`
   迁移（= 尚未创建存储池）故意跳过，因为那不是故障，用"管家警报"语气
   提示不符合人话红线。如果 parent review 认为"尚未创建存储池"也值得
   一张引导建立存储池的建议卡（不同的 trigger_key，不同的语气："建议
   设置存储空间"而不是"硬盘出问题"），这是可以补的后续小项，本轮判断
   严格对照计划"迁移即触发"的字面要求会导致语气失真，保守跳过。
3. **SMART 异常目前无事件源可订阅**：`diskmgmt` 模块有 SMART 扫描
   能力（`handleSMARTScan`）但不发事件，`triggers` 没有规则能覆盖
   "某块盘 SMART 预警"这个计划原本设想的场景。F2 范围内未补
   diskmgmt 侧的 SMART→事件生产点（任务书硬约束 4 只要求"先 grep 确认
   失败路径位置"，backup 补了、SMART 没有对应的失败/预警事件生产点，
   补这个超出了"backup 失败事件补点"这一条硬约束的字面范围）。这是
   一个范围缺口，留给 F3/F4 或下一轮明确授权后再补。
4. **CreateSuggestion 接口扩展**（第 7 节已详述）：加
   `Suggestion.ParamsJSON` 字段 + `CreateSuggestionWithParams` 函数，
   非破坏性但确实扩了 Task 2 锁定的接口面，记为需要 review 确认的
   偏离。
5. **backup.go 失败路径顺手补了审计日志**：`common.LogAuditRequest(r,
   "BACKUP", "数据备份", ..., "failed")`，对称于既有成功审计行，不是
   任务书硬约束要求的内容，是本轮认为"补事件就该补审计"顺手做的最小
   扩展，如判定超出范围可以去掉（不影响任何事件/建议逻辑）。
6. **StopLoopsForTest 加了 sync.WaitGroup**：Task 2 原有的
   `StopLoopsForTest` 只 `close(stop)` 不等 goroutine 真正退出，本轮
   给 `eventLoop` 测试（真实起 `StartLoops`）时在 `-race` 下复现了
   跨测试用例的数据竞争（旧测试的后台 goroutine 还没退出，下个测试已经
   在改 `enabledGuardFn`）。修复加了 `loopsWG sync.WaitGroup`，
   `StopLoopsForTest` 现在会阻塞到两个 goroutine 都退出才清空单例状态。
   这是对 Task 2 遗留代码的必要修复（不修就无法满足"`go test
   ./... -race` 常绿"的硬约束），不是功能变更，所有 Task 2 既有测试
   不受影响仍绿。
