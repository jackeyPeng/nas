# F1 R2 执行报告：filemgmt 内核抽取（Task 3）+ photo_dedupe（Task 4）

对应 TASK-F1-R2.md；计划见 `docs/plans/2026-10-22-agent-foundation-f1-plan.md`。
分支 `feat/agent-foundation`，Go module 在 `web/`。两个 Task 各自独立提交：

- `8553d13` `refactor(filemgmt): 抽取MoveToRecycle/ResolvePoolPath内核导出(export.go模式)...(F1 T3)`
- `389739c` `feat(tasks): photo_dedupe — 两级哈希扫描/最短路径保留策略/预算partial/回收站执行接线(幂等skip)(F1 T4)`

## 一、Task 3：抽取守卫链对照表（原 handleDelete 行为 vs 内核+映射）

`web/modules/filemgmt/export.go` 新增 `ResolvePoolPath`（薄导出）+ `MoveToRecycle`
（机械抽取 handleDelete 全部守卫，顺序逐字保留）；`mutate.go` 的 `handleDelete`
改为"解析路径 → 调 MoveToRecycle → 哨兵错误映射回原 HTTP 码/文案"。

| 步骤 | 原 handleDelete（抽取前） | 现状（抽取后） | HTTP 码/文案 |
|---|---|---|---|
| 1. 解析路径 | `resolvePoolPath(req.Path)` | 不变，仍在 handler 层（内核不重复做，调用方先过 ResolvePoolPath） | 400「resolvePoolPath 原错误文本」 |
| 2. 池根判定 | `srcAbs == normalizedRoot()` → 400 | 内核 `ErrNotInPool` | 400「不能删除池根」 |
| 3. .recycle 字面段 | `pathHasRecycleSegment(litRel)` → 400 | 内核 `ErrIsRecycleContent` | 400「回收站内容请在回收站页管理」 |
| 4. .recycle EvalSymlinks 双判定 | 同上逻辑，真实路径再判一次 → 400 | 内核同一哨兵 `ErrIsRecycleContent` | 同上 |
| 5. 共享根判定 | `ownerShareOfVia` 命中且等于 share.Path → 400 | 内核 `ErrIsShareRoot` | 400「不能删除共享文件夹本身,请在存储页操作」 |
| 6. 源存在性 | `os.Lstat` 失败 → 404 | 内核 `ErrNotFound` | 404「源不存在」 |
| 7. actor 白名单 | `recycleAdminUser` 取用户名后校验含 `/`/`..` → 400 | 内核 `ErrBadActor`（actor 由 handler 传入，内核内再校验一次） | 400「删除者用户名非法」 |
| 8. 计算回收站目标 | `resolveRecycleTarget` + `recycleConflictFree` | 内核内调用，逻辑未动（函数仍在 mutate.go，export.go 同包直接调） | — |
| 9. 落盘 | `os.MkdirAll` + `os.Rename` | 内核内执行，失败原样包装返回（非哨兵，走 default 500 分支） | 500「err.Error()」 |
| 10. 成功响应 | `recycle_path` 相对路径 + 审计 | handler 层不变（`common.LogAuditRequest` + `common.JSONResponse`） | 200 |

验证：`TestHandleDelete*` 全部 16 个既有测试抽取前后行为不变（抽取后仍全绿，逐个
核对过"同名冲突追加后缀/.recycle 防套娃/符号链接指向回收站/共享根拒绝/RecycleBin
=false 回退池根 keeptree/符号链接本身删除"等场景）。新增 `export_test.go` 12 个
用例直接对 `MoveToRecycle`/`ResolvePoolPath` 做哨兵错误断言（不经 HTTP 层），
覆盖计划 Step 1 列出的全部场景（含"共享根拒绝"——用 `withPublicFolderMeta` fixture
真测，不是跳过）。

## 二、Task 4：photo_dedupe 测试矩阵

`web/modules/tasks/kinds_dedupe.go`：`PlanPhotoDedupe(scopeRel, budget)` 扫描器 +
`execMoveToRecycle`（`move_to_recycle` op 真实实现）+ `buildDedupeResult`
（Result JSON 汇总）。`kinds_dedupe_test.go` 共 13 个测试：

| 用例 | 覆盖点 |
|---|---|
| `TestPlanPhotoDedupeGroupsExactDuplicatesAcrossDirs` | 3 份同内容 jpg 跨目录 → 1 组，Impact.Groups/Files/Bytes 精确，保留者=最短路径不出现在 plan |
| `TestPlanPhotoDedupeTwoLevelHashSeparatesSameSizeDifferentContent` | **两级哈希分流**：同 size、头 64KB 相同、全量不同 → 不成组 |
| `TestPlanPhotoDedupeGroupsDuplicateVideos` | 视频扩展名（.mp4）同样走两级哈希成组 |
| `TestPlanPhotoDedupeSkipsRecycleSegment` | `.recycle`/`#recycle` 段内重复内容跳过 |
| `TestPlanPhotoDedupeSkipsHiddenFiles` | 隐藏文件（`.` 开头）跳过 |
| `TestPlanPhotoDedupeSkipsSymlinks` | **符号链接跳过**（Lstat 判定，不跟随）：真实文件+1 份重复成组，链接本身不参与 |
| `TestPlanPhotoDedupeBudgetTimeoutReturnsPartial` | **预算 partial**：budget=1ns，200 个候选文件场景下返回 `partial=true` |
| `TestPlanPhotoDedupeSecondCallReturnsSamePendingJob` | **二次调用命中 FindPendingJob**：扫描后再加新重复文件，二次调用返回同一 job（plan 不变，证明未重扫） |
| `TestPlanPhotoDedupeRejectsTraversalScope` | `scopeRel="../etc"` 直接报错（ResolvePoolPath 拒绝） |
| `TestPlanPhotoDedupeIgnoresNonWhitelistedExtensions` | 扩展名白名单外（.txt）不参与扫描/成组 |
| `TestPhotoDedupeExecutionChainMovesToRecycleAndReportsResult` | **执行链单测（真 op）**：3 份重复 → Plan→StartJob→worker 跑完 → 2 份进 `.recycle`、1 份（最短路径）原位、Result JSON `{"moved":2,"freed":2×len,"skipped":0}` 精确、`task.done` eventbus 事件收到 |
| `TestPhotoDedupeExecutionChainIdempotentSkipWhenSourceAlreadyGone` | **幂等 skip**：plan 确认后源文件被手动移走（模拟确认与执行之间的时间差）→ job 仍 done（不因此 failed）、Result `{"moved":0,"skipped":1}` |
| （filemgmt 侧）`TestMoveToRecycle*`/`TestResolvePoolPathExported*` 12 个 | Task 3 节已列，Task 4 的 `execMoveToRecycle` 直接复用这条链路 |

扩展名白名单（jpg/jpeg/png/heic/gif/webp/bmp/mp4/mov/avi/mkv/webm，小写比较）、
两级哈希（头 64KB sha256 → 全量 sha256）、32KB 读缓冲、保留策略（路径最短；并列
mtime 最早）均按落点提醒逐条实现。

## 三、边界白名单变更

无变更。`tasks → filemgmt` 已在 R1（Task 1）的 `allowedModuleDeps` 落下：
```go
"tasks": {"filemgmt"},
```
`TestArchBoundaries` 全程保持绿，未新增任何跨模块依赖（kinds_dedupe.go 只 import
`nas-panel/modules/filemgmt`，未触碰 `ai`）。

## 四、测试计数

- `go test ./modules/filemgmt/ -count=1 -v`：150 个 `--- PASS`（含本轮新增
  `export_test.go` 12 个 + 既有全部 138 个均保持绿）。
- `go test ./modules/tasks/ -count=1 -v`：33 个 `--- PASS`（R1 既有 20 个 +
  本轮新增 `kinds_dedupe_test.go` 13 个）。
- `go test ./... -count=1 -v`：511 个 `--- PASS`，全仓库无回归。
- `go build ./...`、`go vet ./...`、`go test -run TestArchBoundaries .` 全绿。
- `go test ./modules/filemgmt/ ./modules/tasks/ -count=1 -race`：绿；
  `go test ./... -count=1 -race`：绿（额外跑 14 轮 `go test ./modules/tasks/
  -count=1 -race` 连续无 flake，见下节说明为何需要多轮验证）。

## 五、偏离与疑虑

1. **worker.go 新增 `ResultBuilder` 注册机制（Task 2 签名之外的新增，非修改）**：
   Task 2 的 `executeJob` 原本给出占位汇总 `{"moved":doneCount,"freed":0,"skipped":0}`，
   代码注释已写明"本 Task 2 先给出空汇总占位；Task 4 接线时会在这里/
   kinds_dedupe.go 补完整统计"——即 Task 2 当时已预留此缺口。本轮加了
   `RegisterOp` 平行的 `RegisterResultBuilder(kind, fn)` 注册表，`buildJobResult`
   按 `job.Kind` 查表，未注册的 kind（现有 worker 测试用的 fake op）落回原占位
   逻辑，**不破坏 Task 2 任何既有测试契约**（worker_test.go 全部用例未改动，
   仍绿）。同时在 `context.Context` 里塞了 `jobID`（新增包级 `ctxJobIDKeyType`），
   因为 `OpExec` 签名（`func(ctx, step) error`）是 Task 2 逐字定义、不可改，
   `move_to_recycle` 的真实执行体需要知道"当前在跑哪个 job"才能按 jobID 累计
   moved/freed/skipped——用 ctx 传值是在不碰签名前提下满足这个需求的最小改动。
   如果评审认为这个设计不合适（比如倾向于把统计也塞进 checkpoint_json 落库），
   请指出，可在后续轮次调整。
2. **跨包测试注入点 `filemgmt.SetPoolRootOverrideForTest`**：落点提醒预期的是
   "没有则加 poolRootOverrideForTest 包变量"，但现状 `poolRootOverride` 变量
   本身已存在（R1 之前就有，filemgmt 自己的测试直接写包级变量）。问题是它是
   **包内私有变量**，`tasks` 包的跨包测试（`kinds_dedupe_test.go` 需要给
   `filemgmt.ResolvePoolPath`/`filemgmt.MoveToRecycle` 一个真实池根 fixture）
   没法从外部赋值。于是在 `export.go` 加了一个导出的薄 setter
   `SetPoolRootOverrideForTest(root string)`，直接写同一个包级变量——语义与
   filemgmt 自己测试里直写变量完全一致，只是多了一层导出函数给跨包测试用。
   生产代码不会调用（名字里有 `ForTest` 后缀，和 `StopWorkerForTest` 等既有
   命名约定一致）。
3. **`common/eventbus.go` 既有 test-cleanup 竞争被再次触发**：R1 报告已记录
   `TestWorkerConcurrentStartPauseCancelNoRace` 为规避这个问题改用了
   `InitEventBusForTest`（无后台 writer goroutine）。本轮新增的
   `TestPhotoDedupeExecutionChainMovesToRecycleAndReportsResult` 需要真实断言
   `task.done` 事件投递，必须用 `resetWorkerForTest`（真实 `InitEventBus`）；
   另一个幂等 skip 用例原本也用了这个真实 eventbus 模式，结果在"多个真实
   InitEventBus 测试背靠背跑"时概率性触发 `-race` 报警（跟 `TestWorkerRunsSteps
   InOrderAndFinishes` 等既有 worker 测试撞车，不是新代码的 bug——已用
   `git stash` 验证：只有 kinds_dedupe.go + 新 ResultBuilder 机制本身单独跑
   `-race` 多轮不触发；触发条件是"新增的第二个真实 InitEventBus 测试"增加了
   碰撞概率）。处理方式：**把不需要断言事件投递的那个用例改成
   `InitEventBusForTest`**（对照 `TestWorkerConcurrentStartPauseCancelNoRace`
   的既有选择），之后连续跑了 14 轮 `go test ./modules/tasks/ -count=1 -race`
   全绿无 flake。根因仍在 `common/eventbus.go`（`CloseEventBusForTest` 对真实
   `InitEventBus` 场景的 writer goroutine 收尾不完整），本轮按 R1 既定原则
   不动这份基础设施代码，只在新测试里规避。如果后续要彻底修，需要专门开一个
   任务改 `common/eventbus.go` 的测试收尾语义。
4. **`filemgmt.RelPoolPath` 是本轮新增的第三个导出函数**（计划 Interfaces
   只写了 `ResolvePoolPath`/`MoveToRecycle`）：扫描器需要把 `filepath.WalkDir`
   拿到的绝对路径换算回 `Step.Args["src"]` 要求的池内相对路径格式，这个换算
   逻辑（`filepath.Rel(normalizedRoot(), absPath)`）本应留在 filemgmt 内部
   （它才知道池根布局细节），不应在 tasks 包里重新猜测/复制 `normalizedRoot()`
   的实现。属于"机械抽取"精神的自然延伸，不是新业务逻辑。
5. **`.recycle`/`#recycle` 跳过只做目录名精确匹配**：计划原文"跳过 .recycle/
   #recycle 段"，实现是对每个目录项按 `recycleSegmentNames[name]` 查表（整段
   名精确等于 `.recycle` 或 `#recycle` 才跳过，不是子串匹配），与 filemgmt 的
   `pathHasRecycleSegment`（按 `/` 分段精确比较）语义一致，没有引入新的匹配
   规则。
6. **执行链幂等测试覆盖的是"确认后、执行前手动删除源"场景，不是"进程崩溃
   断点续传"场景**：计划 Task 4 Step 3 的幂等语义写的是"源已不存在且
   checkpoint 已记 → skip；源不存在且未记 → 记 skipped 计数继续"。我的测试
   `TestPhotoDedupeExecutionChainIdempotentSkipWhenSourceAlreadyGone` 测的是
   第二种情形（未记但源已不在，worker 第一次真正执行这一步时发现源没了）。
   第一种情形（已记但源不在——典型的"进程崩溃后重启续传，上次其实已经
   搬过了，这次 checkpoint 命中直接跳过不会再调用 execMoveToRecycle"）天然
   由 `worker.go` 既有的 `if checkpoint[step.Key] { continue }` 保证，
   **根本不会调用到 op 函数**，所以不需要也不可能在 `execMoveToRecycle` 内部
   测——这部分幂等性是 Task 2 的 checkpoint 机制保证的，Task 4 只需要处理
   "worker 决定要跑这一步，但源已经不在了"这一种情形，已覆盖。

## 六、未改动项确认

- `handleDelete` 的 HTTP 状态码、错误文案、JSON 响应体字段（`ok`/
  `recycle_path`）与审计日志格式逐字未变（第一节对照表 + 既有 16 个
  `TestHandleDelete*` 测试全绿为证）。
- `tasks.go`/`api.go`/`db.go`（Task 1/2 产物）除 `worker.go` 的 `buildJobResult`
  替换 + `ctxJobIDKey` 新增外，未做其它改动；`RegisterOp`/`StartWorker`/
  `StartJob`/`PauseJob`/`ResumeJob`/`CancelJob`/REST API 签名与行为不变。
