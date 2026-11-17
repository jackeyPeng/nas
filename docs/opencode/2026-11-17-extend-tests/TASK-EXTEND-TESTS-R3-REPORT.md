# TASK-EXTEND-TESTS R3 报告 — ctx 取消 / SSE 断连感知（P7/P8/R8）

> 对应设计：`docs/opencode/2026-11-17-extend-tests/DESIGN.md`，R2 交接：
> `docs/opencode/2026-11-17-extend-tests/TASK-EXTEND-TESTS-R2-REPORT.md`。
> 本轮范围：`handlePoolExtendStream`/`handleRAIDExpandStream` 感知 ctx 取消
> 与 SSE 写失败，命令链即时中止；新增用例 P7/P8/R8；R6/R7（`waitForResyncCore`
> 抽取）明确不在本轮范围，交接见文末。

commit: `d08c6487f43630462fb3cb8b2a74a8ccaec9e798`
（未 push；本报告文件本身未 `git add`）

## 1. 改动清单

| 文件 | 改动 |
|---|---|
| `web/modules/diskmgmt/step_tracker.go` | **新文件**。`stepTracker`/`newStepTracker`（绑定 `PoolExtendEvent`，签名与任务书给定一致）+ `raidStepTracker`/`newRAIDStepTracker`（绑定 `RAIDExpandEvent` 的同构版本）。`Running`/`Done`/`Error` 三方法统一先查 `ctx.Err()`，取消则直接返回 error 不发事件；否则调用 `emit`，把写失败（SSE 断连）也当同一种中止信号向上传播。`raidStepTracker` 额外有 `CurrentStep()`/`SkipStep()`。 |
| `web/modules/diskmgmt/pool_extend.go` | `sendPoolExtendProgress` 改为返回 `error`（`fmt.Fprintf` 失败即返回，不再丢弃）。`handlePoolExtendStream` 新增 `ctx := r.Context()`，构造 `stepTracker` 包裹 `sendPoolExtendProgress`，原来的直接调用改为 `tracker.Running/Done/Error`，每次都检查返回值并提前 `return`。终态 `complete` 事件仍是直接调用（无后续步骤，语义不变）。 |
| `web/modules/diskmgmt/raid_expand.go` | `sendRAIDExpandProgress` 同样改为返回 `error`。`handleRAIDExpandStream` 新增 `ctx := r.Context()`，构造 `raidStepTracker`，三条主命令（wipe/add/grow）及 RAID1 分支（resync 等待 + growFS）全部改走 `tracker.Running/Done/Error`；`checkRAIDError`/`growFilesystem` 两个 helper 签名从 `w http.ResponseWriter` 改为 `*raidStepTracker`，错误事件经 `tracker.Error` 发送。`waitForResync` 签名**未变**（仍是 `(w, mdDev, currentStep, totalSteps) error`），调用处传入 `tracker.CurrentStep()`；RAID1 分支里原来手写的 `step++`（resync 完成后，补偏移给“数据同步完成”done 事件）用 `tracker.SkipStep()` 显式替代，保持 Index 数值与原行为一致。终态 `complete`/`reshaping` 事件保持直接调用。 |
| `web/modules/diskmgmt/pool_extend_test.go` | 新增 `withCancelableSSE` 测试帮手（带 `context.CancelFunc` 的 `withSSE` 变体）；新增 `TestHandlePoolExtendStream_P7_CtxCancelStopsCommandChain`、`TestHandlePoolExtendStream_P8_RetryAfterPartialRejectedByDiskInUse`。P1-P6 未改动。 |
| `web/modules/diskmgmt/raid_expand_stream_test.go` | 新增 `withCancelableRAIDSSE`；新增 `TestHandleRAIDExpandStream_R8_GrowFailsAndCtxCancel`（含两个子场景：`mdadm_grow失败`、`ctx取消阻止后续命令`）。R1-R5 未改动。 |
| `CHANGELOG.md` | `[未发布]` 新增一条，文案与任务书给定逐字一致。 |

未触碰：`growfs.go`、`storage.go`、`raid_sysfs.go`、`seams.go`、`stream_stubs_test.go`、`stream.go`（wizard 路径）、`common` 包、任何路由注册。

## 2. 构建/测试真实输出（尾部）

```
$ go build ./...
（无输出，exit=0）

$ go vet ./modules/diskmgmt/...
（无输出，exit=0）

$ go test ./modules/diskmgmt/ -count=1 -v 2>&1 | tail -5
--- PASS: TestHandleUnmount_RejectsInvalidTarget (0.00s)
=== RUN   TestHandleUnmount_RejectsEmptyTarget
--- PASS: TestHandleUnmount_RejectsEmptyTarget (0.00s)
PASS
ok  	nas-panel/modules/diskmgmt	5.368s

top-level PASS: 165
sub PASS: 214
FAIL lines: 0
```

P7/P8/R8 单独运行（含 5 次重复，确认无 ctx 取消时序引发的 flaky）：

```
$ go test ./modules/diskmgmt/ -run 'TestHandlePoolExtendStream_P7|TestHandlePoolExtendStream_P8|TestHandleRAIDExpandStream_R8' -count=1 -v
=== RUN   TestHandlePoolExtendStream_P7_CtxCancelStopsCommandChain
--- PASS: TestHandlePoolExtendStream_P7_CtxCancelStopsCommandChain (0.08s)
=== RUN   TestHandlePoolExtendStream_P8_RetryAfterPartialRejectedByDiskInUse
--- PASS: TestHandlePoolExtendStream_P8_RetryAfterPartialRejectedByDiskInUse (0.08s)
=== RUN   TestHandleRAIDExpandStream_R8_GrowFailsAndCtxCancel
=== RUN   TestHandleRAIDExpandStream_R8_GrowFailsAndCtxCancel/mdadm_grow失败
=== RUN   TestHandleRAIDExpandStream_R8_GrowFailsAndCtxCancel/ctx取消阻止后续命令
--- PASS: TestHandleRAIDExpandStream_R8_GrowFailsAndCtxCancel (0.13s)
    --- PASS: TestHandleRAIDExpandStream_R8_GrowFailsAndCtxCancel/mdadm_grow失败 (0.07s)
    --- PASS: TestHandleRAIDExpandStream_R8_GrowFailsAndCtxCancel/ctx取消阻止后续命令 (0.09s)
PASS
ok  	nas-panel/modules/diskmgmt	0.132s
```
（重复 5 次均 `ok`，未见 flaky。）

`-race` 全量：

```
$ go test ./modules/diskmgmt/ -race -count=1
ok  	nas-panel/modules/diskmgmt	6.036s
```

`gofmt` 仅检查本轮触碰的 5 个文件（`pool_extend.go`/`pool_extend_test.go`/
`raid_expand.go`/`raid_expand_stream_test.go`/`step_tracker.go`）：输出为空，
全部已是标准格式。包内另两个文件（`export_test.go`/`overview.go`）本轮未
触碰，`gofmt -l` 对它们报不规范是既有状态，不属于本轮引入。

## 3. 测试数量对比（vs 162 基线）

| 维度 | R2 基线 | R3 本次 | 差值 |
|---|---|---|---|
| 顶层 `--- PASS` | 162 | 165 | +3（P7、P8、R8） |
| 子用例 `    --- PASS` | 212 | 214 | +2（R8 的两个子场景：`mdadm_grow失败`、`ctx取消阻止后续命令`；P7/P8 本身不含子用例） |
| `--- FAIL` / `    --- FAIL` | 0 | 0 | 不变 |

逐条 diff 确认 162 条基线 `--- PASS` 全部原样保留（仅耗时数字随机波动，
测试名与通过状态逐条一致，`TestHandlePoolExtendStream_P1..P6`、
`TestHandleRAIDExpandStream_R1..R5` 均在列且 PASS）。

## 4. 偏离设计的声明

1. **`raidStepTracker` 是本轮自行引入的类型，设计文档未直接命名**。任务书
   给定的 `newStepTracker` 固定签名绑定 `PoolExtendEvent`
   （`func newStepTracker(total int, emit func(PoolExtendEvent) error, sleep func(time.Duration), ctx context.Context) *stepTracker`），
   而 `RAIDExpandEvent` 是另一个结构体类型，Go 不支持把两种具体事件类型
   塞进同一个非泛型 `emit func(X) error` 签名。为保持对任务书字面签名的
   最小偏离（`stepTracker` 本身完全照抄），RAID 侧另起一个同构但独立命名
   的 `raidStepTracker`/`newRAIDStepTracker`，字段与方法形态逐一对应
   `stepTracker`，调用点改动量与可读性都优于引入泛型或 `interface{}`。
2. **`raidStepTracker.SkipStep()` 是新增方法，设计文档未提及**。原
   `handleRAIDExpandStream` 在 RAID1 分支里，resync 等待完成后手写了一次
   `step++`，用来补偏移——因为 resync 轮询期间发的是 `waitForResync` 自己
   直接调用 `sendRAIDExpandProgress` 的 running 事件（不经过 handler 本级
   的 `stepDone` 计数），如果不补这一步，“数据同步完成” done 事件的
   `Index` 会比原行为少 1。引入 `tracker` 后这个手写计数被封进了
   `raidStepTracker.step` 私有字段，调用点没法再直接 `step++`，所以加了
   `SkipStep()` 这个最小方法把原行为原样搬过来。效果已用 R1
   （`TestHandleRAIDExpandStream_R1_RAID1FullSuccess`，断言 `complete`
   事件 `Index=5/Total=5`）验证，Index 数值与 R2 基线完全一致。
3. **`checkRAIDError`/`growFilesystem` 两个私有 helper 的签名从
   `w http.ResponseWriter` 改为 `*raidStepTracker`**。任务书明确“授权
   把 `sendPoolExtendProgress`/`sendRAIDExpandProgress` 改为返回
   `error`，并在两个 handler 内部引入 `stepTracker`”；这两个 helper 是
   `raid_expand.go` 内部专用的私有函数（仅被 `handleRAIDExpandStream`
   调用，无对外导出、无其他调用方、无直接单测针对它们的旧签名断言），
   不改签名就没法让它们发出的 error 事件同样享有 ctx 取消/写失败抑制，
   会在 handler 整体切换到 tracker 语义后出现“一半经 tracker 一半还是
   直接写 `w`”的不一致。判定为任务书授权范围内的最小必要联动改动，非
   独立越界。
4. **`waitForResync` 内部对 `sendRAIDExpandProgress` 返回值仍是忽略
   的**（第 430 行 `sendRAIDExpandProgress(w, RAIDExpandEvent{...})`，
   未接收返回的 `error`）。这是刻意保留的缺口，原因：
   - `waitForResync` 的签名本轮明确不动（`raid_expand_test.go` 的
     `TestWaitForResyncReturnsNilWhenNoResyncLine` 直接调用它，改签名
     属于越界）；
   - 它的"ctx 取消/写失败后中止轮询"属于 `waitForResyncCore` 抽取
     （R4/R6/R7 范围）要解决的问题，本轮改了也无法通过现有测试验证
     其效果（没有专门测 resync 轮询期间取消的用例，且任务书把这部分
     明确划给下一轮）；
   - 实际影响范围有限：`waitForResync` 的调用方
     （`handleRAIDExpandStream`）在它返回后立即检查 `err`，若 resync
     超时会调用 `tracker.Error`（已受 ctx/写失败保护）；真正没被保护
     的只是"resync 轮询期间，每一轮发一次 running 事件"这几次写，若
     客户端在轮询期间断连，这几次 `Fprintf` 会继续尝试写入失败的连接
     （返回 error 被丢弃）直到轮询自然结束或超时，但不会 panic、不会
     让命令链继续执行新的 `sudoExecFn`（resync 轮询本身只读 sysfs/
     mdstat，不发命令）。这与任务书"无进一步 `sudoExecFn` 调用、无
     进一步 SSE 事件、handler 正常返回"三条验收标准里的第一条和第三条
     不冲突，只有"无进一步 SSE 事件"这一条在 resync 轮询这个特定窗口
     内未被严格满足。已在交接笔记中标出，留给 R4 随
     `waitForResyncCore` 抽取一并处理。

以上四点均为任务书授权范围内的最小必要选择，没有发生任何需要单独升级
确认的越界改动（未碰 `growfs.go`/`storage.go`/`raid_sysfs.go`/
`seams.go`/`stream_stubs_test.go`/`stream.go`/`common`/路由）。

## 5. 验收标准对照

- ctx 取消/SSE 写失败后：
  - ✅ 不再有新的 `sudoExecFn` 调用——P7、R8 的 ctx 取消子场景均用
    `stub.countCalls(...)`/`stub.calledNames()` 断言取消点之后没有新命令。
  - ✅ 不再发送新的 SSE 事件——P7、R8 均断言取消点之后相关 Step 的事件
    （如 pvcreate/添加磁盘）未出现；已知例外见上文第 4 条第 4 点
    （`waitForResync` 轮询期间的 running 事件，范围有限且已声明）。
  - ✅ handler 正常返回，无 panic、无 500——所有新增用例均调用
    `handlePoolExtendStream(rec, req)`/`handleRAIDExpandStream(rec, req)`
    后继续执行断言代码，未发生 panic；`httptest.ResponseRecorder` 不会
    对写入失败的连接抛异常，但 `tracker` 的提前 `return` 保证了 handler
    函数体本身是正常退出路径，不是靠 recover 掩盖的异常退出。
- 正常路径事件序列字节级不变：P1-P6/R1-R5 全部保持原断言不变且全绿
  （见第 3 节 diff 结果）。

## 6. 交接给 R4

1. **`waitForResyncCore` 抽取**：把 `waitForResync` 内部的轮询判定逻辑
   抽成一个不依赖 `http.ResponseWriter` 的纯函数/可测试核心，让 resync
   轮询期间的进度事件也能经过 `raidStepTracker`（或等价机制）发送，从而
   补上本轮第 4 节第 4 点声明的缺口——轮询期间 ctx 取消/写失败也应立即
   停止继续轮询（当前虽不会执行新命令，但会继续空转到超时或 resync
   自然完成才返回）。
2. **R6/R7**：按设计文档归属，具体用例内容本轮未细读/未实现，需要 R4
   自行对照 `DESIGN.md` §2.2 确认范围（大概率与 `waitForResyncCore`
   抽取后的行为验证相关）。
3. `raidStepTracker.CurrentStep()`/`SkipStep()` 是本轮为兼容现有
   `waitForResync(w, mdDev, currentStep, totalSteps)` 签名新增的过渡性
   方法；如果 R4 把 `waitForResync` 整个改造为内部使用 tracker，这两个
   方法可能不再需要独立存在，可以视 R4 的具体实现决定是否收敛掉。
4. 本轮未新建任何 mock/stub 基础设施改动（`stream_stubs_test.go`
   原样未碰），R4 如果要测"resync 轮询期间 ctx 取消"，可以直接复用
   `withCancelableRAIDSSE`（本轮在 `raid_expand_stream_test.go` 新增）。
