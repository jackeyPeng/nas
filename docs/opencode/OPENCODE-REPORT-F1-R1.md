# OPENCODE-REPORT-F1-R1：任务引擎核心（F1 Task 1 + Task 2）

分支：`feat/agent-foundation`　仓库：`/home/jacky/soft/nas`
对应 brief：TASK-F1-R1.md　对应计划：docs/plans/2026-10-22-agent-foundation-f1-plan.md（Task 1/2）

两次提交：
- `e6dcb3a` feat(tasks): 任务引擎骨架 — jobs表/状态机/checkpoint/崩溃恢复paused + 架构边界测试(import图锁定分层) (F1 T1)
- `ab9dcd9` feat(tasks): worker执行器(checkpoint续跑/暂停恢复取消/里程碑事件)+REST与SSE任务API(enabled守卫) (F1 T2)

---

## 一、边界测试基线 map（全量，现有跨模块依赖清单）

扫描方法：`grep -rn "nas-panel/modules/" web/modules --include='*.go'`，逐文件核对导入块
（排除 `_test.go`，排除指向自身包的注释字符串如 version.go 里的 `-X nas-panel/modules/version...`
构建参数注释、非真实 import）。扫描结果与 `web/arch_boundary_test.go` 里写死的
`allowedModuleDeps` 逐一对照，完全一致：

| 模块 | 依赖的其它 modules/* | 来源文件 |
|---|---|---|
| ai | diagnostics, diskmgmt, firewall, logs, monitor, system, users | tools_nas_read.go（diagnostics/diskmgmt/firewall/logs/monitor/system/users）、tools_nas_write.go（diagnostics/diskmgmt/firewall/system/users）、actions.go（users） |
| filemgmt | diskmgmt | upload.go |
| health | dashboard | health.go |
| monitor | dashboard | monitor.go |
| services | dashboard | services.go |
| update | version | update.go |
| users | diskmgmt | create.go、matrix.go |

本次新增（Task 1/2 落地后）：
| 模块 | 依赖 | 来源 |
|---|---|---|
| ai | + tasks（预留，Task 5 才会真正 import；本轮未写任何 ai→tasks 的 import，先写进白名单避免 Task 5 时再改测试） | — |
| tasks | filemgmt（预留，Task 3/4 真正落地 MoveToRecycle 后才会 import；本轮 tasks 未实际 import filemgmt，占位同理） | — |
| triggers | tasks, ai（F2 预留，占位） | — |

其余未出现在上表的模块（diskmgmt、firewall、monitor 自身、diagnostics、logs、system、
vault、rclone、events、hardware、update 自身、storage、version 自身、dashboard 自身）
均无跨模块 import，白名单留空集（`allowedModuleDeps` 里不出现 = 缺省空集，测试逻辑按
`contains(allowed, d)` 判断，未在表里的模块一旦出现任何跨模块 import 就会直接报违规）。

## 二、红灯验证记录

按计划 Step 2 要求，故意引入违规 import 验证测试会红，验证后逐一还原（`git diff --stat`
确认无残留改动）：

1. **规则 2（tasks 不得 import ai）的验证路径**：在 `modules/firewall/firewall.go` 加
   `"nas-panel/modules/ai"`。结果：触发的是 Go 自身的包级 import cycle 检测
   （`ai → ... → firewall → ai` 在 `go test` 加载阶段就报
   `import cycle not allowed`，测试没跑起来就先红在 build 阶段）。这证明了现有
   `ai→firewall` 的既有依赖 + 新增的 `firewall→ai` 会直接形成环，Go 工具链本身就是
   第一道红线；随后把改动原样 revert，`git diff --stat` 确认零残留，`go test
   -run TestArchBoundaries . -v` 重新绿。
2. **规则 1（跨模块依赖必须在白名单内）的验证路径**：改用不涉及已有依赖关系、不会
   触发 import cycle 的组合——在 `modules/diskmgmt/diskmgmt.go` 加
   `"nas-panel/modules/logs"` 并通过 `var _ = logs.RegisterRoutes` 让它编译通过
   （避免 unused import 在编译期先报错，掩盖了我们真正要验证的测试逻辑）。结果：
   `TestArchBoundaries` 在测试逻辑内部报出清晰的违规信息：
   ```
   arch_boundary_test.go:140: 规则1违规: modules/diskmgmt 非测试文件 import 了
   modules/logs，不在白名单 allowedModuleDeps["diskmgmt"]=[] 内。若这是有意的
   新依赖，需更新 allowedModuleDeps（过 review）
   ```
   验证后还原两处改动（import 行 + 占位引用行），`git diff --stat` 确认零残留，
   重新跑测试绿。

两次验证都指向同一个结论：测试在"编译期（import cycle）"和"测试逻辑期（白名单
判断）"两道关口都能拦住违规依赖，不存在只看表面 import 语句就放过真实风险的死角。

## 三、状态机迁移表

```
pending_confirm -> running     合法（StartJob）
pending_confirm -> cancelled   合法（CancelJob，未确认即可取消）
running         -> paused      合法（PauseJob，worker 步骤边界落库）
running         -> done        合法（FinishJob 正常完成）
running         -> failed      合法（FinishJob 步骤出错）
running         -> cancelled   合法（CancelJob，worker 步骤边界落库）
paused          -> running     合法（ResumeJob，重新入队从 checkpoint 续传）
paused          -> cancelled   合法（CancelJob）
done/failed/cancelled -> *     非法（终态，legalTransitions 表里无出边）
pending_confirm -> paused/done/failed  非法（必须先 running）
```

实现：`tasks.go` 里 `legalTransitions map[Status]map[Status]bool`，`UpdateJobStatus`
查表校验，不在白名单内直接返回 error、不改库。`FinishJob` 只接受三个终态参数，内部
仍走 `UpdateJobStatus` 做校验（例如 pending_confirm 状态下调 `FinishJob(..., StatusDone)`
会被状态机拒绝，不会出现"确认前就显示已完成"的错误态）。

## 四、崩溃恢复语义（收紧版，偏离 spec 的"自动续传"）

`InitTasks` 启动时对 `status='running'` 的行执行 `recoverCrashedJobs`：
- 迁到 `paused`
- `progress_note` 追加「面板重启已暂停，可继续」（已含该提示则不重复追加，防御性
  幂等；正常场景不会触发，因为一个 job 只会经历一次"running→崩溃"）
- **不清零 progress_cur/progress_total**，用户点"继续"（ResumeJob）时 worker 仍从
  checkpoint 查已完成步骤续传，不重做副作用

理由（代码注释 `crashRecoveryNote` 旁 + `TestInitTasksCrashRecoveryPausesRunningJobs`
测试注释已记录）：任务引擎默认操作是把文件移进回收站，无人值守自动续传意味着面板
一重启就可能在用户不知情时继续挪文件；对纯家庭小白用户，"不自动做可能出错的事，
等一个明确的继续点击"比"尽快恢复进度"更安全。这比 spec §四原文的"崩溃/重启后从
checkpoint 续传"更保守一步——spec 说的是"从 checkpoint 续传"（隐含自动），这里拆成
"重启先暂停，续传动作仍要走，但由用户显式触发"，`pathTo`/`ResumeJob` 路径本身的
"从 checkpoint 跳过已完成步骤续传"是完全遵照 spec 实现的，只是触发时机从"自动"改成
"用户确认"。

## 五、worker 信号机制说明

- **队列**：`chan string`，容量 16（`queueCapacity`），单 goroutine（`workerLoop`）
  串行消费，`StartWorker` 用 `workerMu` + `workerStarted` 布尔保证进程内只启动一次。
- **控制信号**：`map[string]controlSignal`（`signalMu` 保护），`PauseJob`/`CancelJob`
  对 `running` 状态的任务只是"设置信号"，不直接改状态；真正的状态迁移发生在
  `executeJob` 的步骤循环里——每执行完一步、进入下一步之前检查信号：
  - `signalCancel` → `FinishJob(..., StatusCancelled)` 并清除信号，循环返回（不执行
    已入队但未跑的后续步骤）
  - `signalPause` → `pauseAndNote` 落 `StatusPaused`，清除信号，循环返回
  - 都不会中断"正在执行中"的单步 `OpExec` 调用——保证文件操作不会被从中间砍断。
- **enabled 守卫**：`SetEnabledGuard(fn func() bool)` 由 main.go 注入
  `ai.IsEnabled`；`executeJob` 每步开始前调 `guardAllows()`，返回 false 时直接
  `pauseAndNote("AI 功能已关闭，任务已暂停")` 并返回，不消费后续步骤。REST 层另有
  一层独立守卫（`requireTasksEnabled`，`api.go`），两层守卫语义一致但物理上互不依赖
  （REST 层挡新请求，worker 层挡正在跑的任务），对齐"关闭=零足迹"的双重保证。
- **测试专用生命周期控制**：`StartWorker` 在生产路径是进程内单例（main.go 只调一次），
  但单测需要"每个用例一个干净 worker"。加了 `StopWorkerForTest`（对齐
  `common.InitEventBusForTest`/`CloseEventBusForTest` 的既有模式）：关闭队列 channel
  并**阻塞等待 worker goroutine 真正退出**（用 `workerDone chan struct{}`），再清空
  信号表。R1 阶段 `-race` 实测踩过两个坑，均已修复并在下面"偏离与疑虑"记录细节。
- **无 goroutine 泄漏断言**：`worker_test.go` 的 `resetWorkerForTest`/
  `TestWorkerConcurrentStartPauseCancelNoRace` 把 `StopWorkerForTest` 注册进
  `t.Cleanup`（LIFO 顺序确保"先停 worker 再关 eventbus"），`-race` 全绿本身就是
  "没有 goroutine 在测试结束后还持有共享状态"的间接证明；`go test -count=5 -race`
  跑了 5 轮确认无 flake。

## 六、里程碑事件口径

`emitTaskEvent` 固定 `source="tasks"`，`code` 用 `task.progress`/`task.done`/
`task.failed`（= spec 要求的"事件 code=kind"，这里 kind 指 EmitEvent 签名里的
"事件类别码"参数位，不是 Job.Kind）。`task.progress` 只在进度比例跨过 10/25/50/75%
四个里程碑时各发一次（`reportedMilestones` 去重，支持跳级——比如一步直接从 0% 跳到
60%，只发一次 progress 而不是补发 10/25/50 三次）；`task.done`/`task.failed` 终态
必发，不受里程碑表约束。`TestWorkerProgressMilestonesCapped` 用 20 步任务断言
`task.progress` 事件数 ≤ 4、`task.done` 恰好 1 次、总数 ≤ 6，符合计划 Step 1 的
"20 步任务断言 EmitEvent 次数 ≤ 6"要求。

## 七、测试计数

- `web/modules/tasks/`：21 个测试全绿（`tasks_test.go` 9 个 + `worker_test.go` 6 个 +
  `api_test.go` 6 个），`-race` 下跑 5 轮无 flake。
- `web/arch_boundary_test.go`：`TestArchBoundaries` 1 个，绿；两次红灯验证（规则 1/
  规则 2）均按预期触发后还原。
- 全量回归：`go test ./... -count=1` 和 `go test ./... -count=1 -race` 均全绿，
  `modules/ai` 仍为 91 个测试（无回归、无改动其行为，只加了一个 `IsEnabled()` 薄导出）。
- `go build ./...`、`go vet ./modules/tasks/ ./modules/ai/ .` 均无输出（全绿）。
- `gofmt -l` 确认本次新增/改动文件（`tasks.go` 等）无格式问题（修过一次 tasks.go 的
  字段对齐空格）。
- 新增 Go 依赖：0（`git diff --stat web/go.mod web/go.sum` 无变化）。

## 八、偏离与疑虑

1. **api.go 在 Task 1 先占位，Task 2 覆写**：计划的 File Structure 把 api.go 归在
   Task 2，但 `tasks.go` 的 `RegisterRoutes` 签名（Task 1 产出）内部调用
   `registerAPIRoutes`——为了让 Task 1 独立可编译、可提交，Task 1 commit 里的
   `api.go` 只是一个什么都不做的占位函数（`_ = enabledGuard`），Task 2 直接整体
   重写该文件为真实实现。这不是接口层面的偏离（签名、调用方式与计划一致），只是
   实现顺序上的工程手段，记录在此供 review 核对两次提交的 diff 时不至于困惑
   "api.go 为什么在 T1 就出现"。
2. **worker 新增了计划没写的两个导出函数**：`SetEnabledGuard(fn func() bool)` 和
   `StopWorkerForTest()`。前者是因为"worker 生命周期挂 ai enabled 守卫"这个硬约束
   （brief 第 6 条）必须有个注入点——tasks 包不能 import ai（架构边界测试规则 2），
   所以守卫函数必须由 main.go 反向注入，这个函数签名计划原文没给出，是实现
   "tasks 不依赖 ai 但仍能被 ai 状态驱动"这条约束时必然推出的设计，不是我随意新增
   能力。后者纯测试基建（对齐 common 包 InitXxxForTest/CloseXxxForTest 既有模式），
   不影响生产路径。两者都不改变计划里逐字给出的公开 API 签名（Job/Step/Impact/
   Status/CreateJob/.../StartJob/PauseJob/ResumeJob/CancelJob/RegisterOp/StartWorker/
   EnqueueJob 均与计划原文一致）。
3. **`common/eventbus.go` 的既有 test-cleanup 竞争**（非本次引入，但本次测试把它
   暴露出来了）：`InitEventBusForTest` 不启动后台 `eventWriter` goroutine，
   `CloseEventBusForTest` 只是把 `eventDB`/`eventChan` 置 nil，不等待也不关闭
   `eventChan`。如果某个测试改用真实 `InitEventBus`（启动了 `eventWriter`
   goroutine）又需要在 `-race` 下安全收尾，`eventWriter` 的 `for ev := range
   eventChan` 永远不会自然退出（`eventChan` 只被置 nil，没被 `close`），导致该
   goroutine 在测试结束后仍存活，和下一个测试的 `CloseEventBusForTest` 写
   `eventDB=nil` 产生 `-race` 报警。本次踩坑的是
   `TestWorkerConcurrentStartPauseCancelNoRace`（它本来想测 worker 并发安全，不是
   测事件投递），**处理方式是让这个用例改用 `InitEventBusForTest`（无后台
   goroutine）而不是修 `common/eventbus.go`**——因为那是现有基础设施代码，改它
   超出本次 brief 授权范围（硬约束 1：禁止读取/执行仓库外文件，但仓库内其它模块
   的既有实现改动也应走单独的 review/授权，不在"顺手"范围内）。记录在此供后续
   决定是否要专门开一个任务修 `common/eventbus.go` 的测试收尾语义（比如
   `CloseEventBusForTest` 改成真正 `close(eventChan)` 并等待 writer 退出，同款
   `workerDone` 模式）。
4. **`FinishJob` 的 result_json 空值语义**：计划没明确规定 `FinishJob` 在
   `resultJSON==""` 时该不该写库。当前实现是"空字符串时跳过 UPDATE result_json
   这一步，只做状态迁移"——这样 `StatusCancelled`（往往没有汇总结果）不会把
   `result_json` 字段冲成空字符串覆盖掉可能已有的中间结果；`StatusFailed`/
   `StatusDone` 调用方应始终传非空 JSON。Task 4（photo_dedupe 执行链）接线时需要
   确认这个约定对它的调用方式（`{"moved":N,"freed":M,"skipped":K}`）够用——目前
   `worker.go` 的占位汇总逻辑（`doneCount` 当 moved，freed/skipped 填 0）会在
   Task 4 被真实的 move_to_recycle 执行结果替换，不影响本次验收范围。
5. **SSE 流对运行中任务用 500ms 轮询而非事件驱动**：计划原文"复用向导既有 SSE
   模式"，diskmgmt/stream.go 的参照实现本身也是"循环 + sendProgress"而非真正基于
   eventbus 订阅推送。本实现选择轮询 `GetJob` 而非 `common.Subscribe()` 订阅
   `task.progress` 事件，理由：① 轮询实现更简单、与现有 SSE 模式一致；② 订阅模式
   需要处理"打开流时任务已经进了一半，错过了之前的里程碑事件"的补发逻辑，轮询天然
   没有这个问题（每次都读最新状态）。代价是轮询间隔内进度更新有最多 500ms 延迟，
   对家庭用户体感可接受。`TestAPIStreamSendsTerminalEventForDoneJobImmediately`
   覆盖了"任务已是终态时立即发送并关闭"的快路径（不进入轮询循环）。

## 九、文件清单

**新增：**
- `web/arch_boundary_test.go`
- `web/modules/tasks/tasks.go`
- `web/modules/tasks/tasks_test.go`
- `web/modules/tasks/db.go`
- `web/modules/tasks/worker.go`
- `web/modules/tasks/worker_test.go`
- `web/modules/tasks/api.go`（Task 1 占位 → Task 2 整体重写为真实实现）
- `web/modules/tasks/api_test.go`

**修改：**
- `web/main.go`（tasks 模块注册：InitTasks/SetEnabledGuard/StartWorker/RegisterRoutes）
- `web/modules/ai/ai.go`（新增 `IsEnabled()` 薄导出，不改 `aiEnabled()` 内部逻辑）

未改动 go.mod/go.sum（零新增依赖）。未改动任何仓库外文件（未读取/执行仓库外路径）。
