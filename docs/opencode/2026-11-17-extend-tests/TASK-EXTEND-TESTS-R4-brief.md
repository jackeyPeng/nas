# TASK: extend/expand 流式写路径单测 R4 — waitForResyncCore 抽取 + R6/R7（收尾轮）

## 依据与前置

- 设计方案：`docs/opencode/2026-11-17-extend-tests/DESIGN.md`（§2.2 R6/R7、§3 第 9/10 项）。
- R2/R3 已落地（26ac2f4 / d08c648）：seam 设施、P1-P8/R1-R5/R8 全绿（165 顶层 PASS 基线）。R3 报告：`docs/opencode/2026-11-17-extend-tests/TASK-EXTEND-TESTS-R3-REPORT.md`（§6 是给你的移交清单，重点：waitForResync 轮询期写错误仍被忽略的声明缺口）。
- 本轮目标：抽 `waitForResyncCore` + `resyncPollOnce`（设计 §3 第 9/10 项），补 R6/R7 用例，把 R3 声明的缺口（resync 轮询期间 ctx 取消/写失败应停止继续轮询）一并收口。**这是该测试线的最后一轮。**

## 已核实事实（parent 验证过）

- `waitForResync` 当前签名 `(w http.ResponseWriter, mdDev string, currentStep, totalSteps int) error`（raid_expand.go，R3 未改）；内部轮询发 running 事件经 `sendRAIDExpandProgress`（其返回 error 在 430 行附近一处仍被忽略——R3 报告 §4.4）。
- `sysfsCleanStreakResult` 是既有的纯函数判定核心（R2 前已存在，有测试）。
- R3 交付的 `raidStepTracker`（step_tracker.go）：Running/Done/Error 统一查 ctx + 写失败抑制；`CurrentStep()`/`SkipStep()` 是过渡方法（R3 报告建议本轮视实现收敛）。
- 桩设施：`withCancelableRAIDSSE`（R3 新增）、`stubExec`、假 sysfs（`withFakeSysfsMD`，注意 R2 报告 §3.3 的目录名特性）。
- Chrome E2E 已真机验证 R3 主链路（DESIGN.md 附录）：正常流与断连中止均闭环，你的改动不得破坏这两条。

## 设计要求

### 1. 抽取（设计 §3 第 9/10 项，照签名实现）

```go
// waitForResyncCore 判据经 poll 注入、睡眠经 sleepFn 注入、轮数上限参数化；
// 不依赖 http.ResponseWriter——进度事件的发送经 emit 回调（返回 error 时立即停止轮询并返回该 error）。
func waitForResyncCore(poll func() (done bool, detail string), maxRounds, requiredCleanStreak int, sleepFn func(time.Duration), emit func(detail string) error) error
// resyncPollOnce 单轮判据（读 sysfsRoot/mdstatPath），供 poll 闭包与 handleRAIDExpandFS/handleRAIDReshapeStatus 复用。
func resyncPollOnce(mdName string, readSysfs func(string) (map[string]string, bool), mdstatText string) (done bool, detail string)
```

- `waitForResync` 外壳保留原名原签名（既有测试 `TestWaitForResyncReturnsNilWhenNoResyncLine` 直调它），内部改为：构造 poll 闭包（sysfs 优先、mdstat 回退）+ emit（经 `sendRAIDExpandProgress`/`raidStepTracker`）→ 调 `waitForResyncCore`。
- **R3 缺口收口**：轮询期间 emit 返回 error（断连/取消）→ 立即停止轮询并向上返回；handler 侧对该返回值的处理与现有超时分支一致（不发新事件、命令链不再继续——RAID1 分支 growFS 在 waitForResync 返回非 nil 后不执行，验证既有代码已如此，如不是则修正并申报）。
- `resyncPollOnce` 与既有 `sysfsCleanStreakResult`/`resyncProgressDetail` 的分工：不重复实现判定逻辑，纯函数层尽量复用/委托；冲突处申报。
- `CurrentStep()`/`SkipStep()` 若不再需要，删掉并同步调用点（最小 diff 优先，也可保留——报告里说明裁决）。

### 2. 用例（R6/R7，按 DESIGN.md §2.2）

- **R6 超时上限 + 单独扩 FS 语义**：`waitForResyncCore` 直测（poll 恒 not-done、maxRounds=3、sleepFn no-op）→ 返回非 nil；handler 层 error 事件 Detail 含「已中止、文件系统未扩展」与 expand-fs 入口指引；**文件系统绝不在 resync 未完成时被扩**。配套 `handleRAIDExpandFS` 闸门用例：sysfs sync_action=resync → 拒绝 JSON；idle+reshape_position=none → 放行走到 growFS 桩。
- **R7 streak 竞态（Finding 3 回归）**：poll 序列 [busy,busy,clean,busy,clean,clean,clean]（TempDir sysfs 内容按轮次切换）→ 仅连续 3 轮 clean 后返回 nil；中途 clean 后再 busy 则 streak 归零；busy 轮 emit 收到 running Detail（「同步进度: x%」或「同步中（<action>）」）。
- **新增：R3 缺口的回归用例**：emit 恒返回 error（模拟断连）→ waitForResyncCore 立即返回 error 且 sleepFn 后续无调用（不再空转到超时）。

### 3. 文档

CHANGELOG.md `[未发布]` 段补一条：
`- refactor(diskmgmt): waitForResync 抽出 waitForResyncCore/resyncPollOnce 纯核 — resync 轮询期间的断连/取消即时停止（R3 声明缺口收口），resync 超时上限与 streak 判定参数化（R6/R7 用例锁定）`

## 硬约束

- 只动：raid_expand.go、step_tracker.go、raid_expand_test.go（既有直测用例适配）、raid_expand_stream_test.go、CHANGELOG.md。其他文件默认不碰（必要时报经说明的最小改动）。
- 存量基线 165 顶层 PASS 不许掉；P1-P8/R1-R5/R8 断言不变（R6/R7 改到公共路径时允许等价适配，逐条申报）。
- **禁止读取仓库外任何文件**；验证只走 `go build ./...` + `go test ./modules/diskmgmt/ -count=1`（+ `-race` 一次）。
- 冲突申报进报告，不静默绕过。
- 单笔 commit，信息精确用：`refactor(diskmgmt): waitForResyncCore 抽取 — resync 轮询断连即时停止（R3 缺口收口）+ 超时/streak 参数化（R6/R7）`
- 禁 git push。

## 产出报告

写 `docs/opencode/TASK-EXTEND-TESTS-R4-REPORT.md`（不 git add）：改动清单、build/test/-race 真实输出尾行、用例数对比（165 基线）、偏离申报、该测试线收尾总结（P/R 全 16+ 条用例最终清单）。
