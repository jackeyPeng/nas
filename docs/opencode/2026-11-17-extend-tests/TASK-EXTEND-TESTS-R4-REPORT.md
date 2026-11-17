# TASK-EXTEND-TESTS R4 报告 — waitForResyncCore 抽取 / resync 断连即时停止（R3 缺口收口）+ R6/R7 用例

> 对应设计：`docs/opencode/2026-11-17-extend-tests/DESIGN.md`（§2.2 R6/R7、§3 第 9/10 项）。
> R3 交接：`docs/opencode/2026-11-17-extend-tests/TASK-EXTEND-TESTS-R3-REPORT.md` §6 — 本轮关闭的缺口：
> resync 轮询期间的写失败此前被忽略，断连后仍会空转到超时；本轮改为 emit 返回 error 立即停止。
> 本轮范围：`waitForResync` 抽取出 `waitForResyncCore`/`resyncPollOnce` 纯函数核心，R6（超时参数化）、
> R7（streak 判定竞态）用例，以及该缺口的回归用例。**本轮是整条测试扩展任务线（P1-P8 / R1-R8）的收尾轮。**

## 1. 改动范围

只触碰了任务书允许的 5 个文件，无额外改动：

| 文件 | 改动 | 说明 |
|---|---|---|
| `web/modules/diskmgmt/raid_expand.go` | 修改 | `waitForResync` 外壳签名不变，内部改为构造 `poll`/`emit` 闭包后委托新抽取的 `waitForResyncCore`；新增 `waitForResyncCore`、`resyncPollOnce` 两个纯函数 |
| `web/modules/diskmgmt/step_tracker.go` | 未改动 | `raidStepTracker.CurrentStep()`/`SkipStep()` 保留，理由见 §4 |
| `web/modules/diskmgmt/raid_expand_test.go` | 未改动 | `TestWaitForResyncReturnsNilWhenNoResyncLine` 直调 `waitForResync` 的既有用例在新实现下原样通过，不需要适配，见 §4 |
| `web/modules/diskmgmt/raid_expand_stream_test.go` | 修改 | 文件末尾新增 6 个 R6/R7 相关测试函数；更新了文件头部的范围说明注释 |
| `CHANGELOG.md` | 修改 | `[未发布]` 段新增一行本轮 refactor 条目 |

未触碰 `raid_sysfs.go`、`growfs.go`、`seams.go`、`stream_stubs_test.go`、`raid_sysfs_test.go`、`pool_extend_test.go` 等文件——新测试全部复用这些文件里已有的测试基础设施（`withFakeSysfsMD`、`withFakeMdDev`、`withStubExec`、`withNoopSleep`、`withSSE`、`parseSSE`、`cleanRAID1SysfsMembers`、`sysfsCleanStreakResult` 等），没有重复实现。

## 2. 核心改动：`waitForResyncCore` / `resyncPollOnce`

### 2.1 签名（与任务书要求的签名完全一致）

```go
func waitForResyncCore(
    poll func() (done bool, detail string),
    maxRounds, requiredCleanStreak int,
    sleepFn func(time.Duration),
    emit func(detail string) error,
) error

func resyncPollOnce(
    mdName string,
    readSysfs func(string) (map[string]string, bool),
    mdstatText string,
) (done bool, detail string)
```

`waitForResync(w http.ResponseWriter, mdDev string, currentStep, totalSteps int) error` 的外部签名原样保留（硬约束：既有测试 `TestWaitForResyncReturnsNilWhenNoResyncLine` 直调它）。内部改为：

```go
readSysfs := func(name string) (map[string]string, bool) {
    vals, _, ok := readRAIDSysfsValues(name)
    return vals, ok
}
poll := func() (bool, string) {
    mdstatBytes, _ := os.ReadFile(mdstatPath)
    return resyncPollOnce(mdName, readSysfs, string(mdstatBytes))
}
emit := func(detail string) error {
    return sendRAIDExpandProgress(w, RAIDExpandEvent{...})
}
return waitForResyncCore(poll, 600, 3, sleepFn, emit)
```

### 2.2 R3 缺口收口：emit 返回 error 立即停止

`waitForResyncCore` 循环体：

```go
if !done {
    if err := emit(detail); err != nil {
        return err
    }
}
```

此前（R3 及更早）`waitForResync` 内部直接调 `sendRAIDExpandProgress(...)` 并丢弃返回值，SSE 写失败（客户端已断连）不会中止轮询，会一直空转到 600 轮超时才退出。现在 `emit` 返回的 error 被立即 `return`，不再继续 `sleepFn` 空转、不再继续轮询。回归用例 `TestWaitForResyncCore_EmitErrorStopsImmediately` 锁定：emit 第一次返回 error 后，`poll` 只被调用过 1 次，`sleepFn` 只被调用过 1 次（循环前置的那一次），返回值严格等于 emit 返回的那个 error 对象。

### 2.3 `resyncPollOnce` 与既有函数的分工（声明：无重复实现）

`resyncPollOnce` 只负责"这一轮是否 done + 本轮 detail 文案"：

- sysfs 分支：复用 `sysfsSyncAction`（判断是否有 key）、`sysfsResyncDone`（判断 action 是否代表完成）、`syncCompletedPercent` + `resyncProgressDetail`（文案拼接）——没有重新实现这些判据或文案逻辑。
- mdstat 回退分支：复用 `parseMdstatBlocks`、`findBlockByMdName`、`firstProgress`——文案沿用原来的 `"同步进度: " + percent` 拼接方式，不经过 `resyncProgressDetail`（该函数只服务 sysfs 分支，mdstat 分支的文案格式在 R2 consensus 回合已经锁定为这个形态，本轮不改）。

`sysfsCleanStreakResult`（`raid_sysfs.go`）是另一个独立的纯函数，职责是"给定多轮 action 序列，判断哪一轮达成连续 streak"，用于别处的多轮批量判定场景。`resyncPollOnce`/`waitForResyncCore` 的 streak 累计逻辑是在 `waitForResyncCore` 循环里逐轮计数（`cleanStreak++` / 归零），没有调用也没有重复实现 `sysfsCleanStreakResult` 的逐轮判定算法——两者判据来源相同（`sysfsSyncAction`/`sysfsResyncDone`）但各自独立实现一遍简单的计数逻辑，确属并存而非重复抄写同一段代码。为了证明两套 streak 判定在同一份输入上结果一致，新增的 `TestResyncPollOnce_R7_StreakRace` 用例特意在断言末尾额外调用 `sysfsCleanStreakResult` 对同一个 `actionSeq` 做交叉验证，确认两者在 R7 的竞态序列上给出相同的"第几轮达成"结果（见 §3.2）。

未发现本函数与 `sysfsCleanStreakResult`/`resyncProgressDetail` 之间存在需要declare的冲突或裸重复。

## 3. 新增测试用例（R6/R7 + 回归）

全部追加在 `raid_expand_stream_test.go` 文件末尾，文件头部范围注释已同步更新为"R6/R7 见文件末尾"。

### 3.1 R6 — resync 超时上限参数化

| 测试 | 验证点 |
|---|---|
| `TestWaitForResyncCore_R6_TimeoutAfterMaxRounds` | 直测核心：`poll` 恒 not-done，`maxRounds=3` → 必须返回超时 error；`emit` 被调用恰好 3 次（每轮一次），detail 原样转发；`sleepFn` 被调用 4 次（1 次循环前置 + 3 轮各 1 次末尾） |
| `TestHandleRAIDExpandStream_R6_ResyncTimeoutStopsBeforeFS` | handler 级：sysfs 恒报 `sync_action=resync`（一直"忙"），配合 `withNoopSleep` 跑满真实 600 轮硬编码上限 → 最终 error 事件 `Step="等待数据同步"`，`Detail` 同时含"已中止"、"文件系统未扩展"与 `/api/disk/raid/expand-fs` 入口指引；全程不出现"数据同步完成"/"扩展文件系统"事件；`xfs_growfs`/`resize2fs` 命令调用次数为 0（A1 不变式：文件系统绝不在 resync 未完成时被扩） |
| `TestHandleRAIDExpandFS_R6_SysfsGate` | 配套闸门用例（子测试）：`sync_action=resync` → JSON error 含"重构中"被拒绝；`sync_action=idle`+`reshape_position=none` → 放行并实际调用 `xfs_growfs <挂载点>` |

### 3.2 R7 — streak 判定竞态（中途 busy 打断后 streak 归零重新累计）

| 测试 | 验证点 |
|---|---|
| `TestResyncPollOnce_R7_StreakRace` | 直测 `resyncPollOnce`：序列 `[resync,resync,idle,resync,idle,idle,idle]` → done 序列 `[false,false,true,false,true,true,true]`（只有最后连续 3 轮 idle 才算真正"连续"，中途第 3 轮 idle 被第 4 轮 resync 打断不算数）；busy 轮 detail 必须匹配"同步进度:"或"同步中（"两种形态之一；额外用 `sysfsCleanStreakResult(actionSeq, 3)` 交叉验证，确认两套判据在同一序列上一致给出 `(true, 6)`（下标 6，即第 7 轮达成） |
| `TestWaitForResyncCore_R7_StreakRaceViaEmit` | 端到端核心测试：用与上面相同的 done 序列驱动 `waitForResyncCore`，断言返回 `nil`、`poll` 恰好被调用 7 次（跑完整条序列才达成 streak=3）、`emit` 恰好被调用 3 次（对应 3 个 busy 轮） |

### 3.3 回归 — R3 声明缺口收口

| 测试 | 验证点 |
|---|---|
| `TestWaitForResyncCore_EmitErrorStopsImmediately` | `emit` 恒返回一个固定的 disconnect error → `waitForResyncCore` 返回值必须严格等于该 error；`poll` 只被调用 1 次；`sleepFn` 只被调用 1 次（循环前置那次，第一轮判定后立即因 emit 出错退出，不再有末尾 sleep，也不会继续下一轮） |

## 4. 偏差声明

### 4.1 `raidStepTracker.CurrentStep()`/`SkipStep()` 保留不删

**决定：保留，不删除。**

理由：`waitForResync` 的外部签名被硬约束锁定为 `(w http.ResponseWriter, mdDev string, currentStep, totalSteps int) error`，仍然以 `currentStep int`（而非 tracker 对象）的形式接收当前步数。这意味着调用方 `handleRAIDExpandStream` 仍然需要在调用 `waitForResync` 前，用某种方式知道/传入当前步数，而 `CurrentStep()`（读取已完成步数）和 `SkipStep()`（resync 完成后手动把 tracker 内部计数前进一步，因为 resync 轮询期间发送的是 `waitForResync` 自己的 running 事件，不经过 `tracker.Done`）是这条"状态计数不丢失"链路里仍在被实际调用的方法。删除它们需要同时改造 `handleRAIDExpandStream` 的调用方式（比如把 `waitForResync` 签名改成接收 tracker 本身），这超出了本轮任务书允许的最小改动范围（且会牵连到 `waitForResync` 签名，与硬约束冲突）。因此本轮选择保留现状，不做這项改造。

### 4.2 `raid_expand_test.go` 无需改动

`TestWaitForResyncReturnsNilWhenNoResyncLine` 直接调用 `waitForResync(...)`，在新的委托实现下无需任何适配即可通过（已在 §5 的全量测试结果中验证）。`git diff --stat` 确认该文件本轮 diff 为零。

### 4.3 mdstat 分支 detail 文案未经 `resyncProgressDetail`

`resyncPollOnce` 的 mdstat 回退分支沿用 R2 consensus 回合锁定的 `"同步进度: " + p.Percent` 拼接方式，没有改走 `resyncProgressDetail`（该函数专门服务 sysfs 分支的"空百分比退化为同步中（action）"场景，mdstat 分支没有这个退化需求）。这不是遗漏，是分工边界，§2.3 已声明。

## 5. 验证结果（真实命令输出）

### 5.1 `go build ./...`

```
$ cd web && go build ./...
(no output, exit 0)
```

### 5.2 `go test ./modules/diskmgmt/ -count=1`

```
$ go test ./modules/diskmgmt/ -count=1
ok  	nas-panel/modules/diskmgmt	5.773s
```

### 5.3 `go test ./modules/diskmgmt/ -count=1 -v`（计数口径）

```
...
=== RUN   TestHandleUnmount_RejectsInvalidTarget
--- PASS: TestHandleUnmount_RejectsInvalidTarget (0.00s)
=== RUN   TestHandleUnmount_RejectsEmptyTarget
--- PASS: TestHandleUnmount_RejectsEmptyTarget (0.00s)
PASS
ok  	nas-panel/modules/diskmgmt	5.835s
```

统计（对 `-v` 完整输出做 grep 计数）：

- 顶层 `--- PASS`：**171**
- 子测试 `--- PASS`（含顶层）：**216**
- `--- FAIL`（顶层 + 子测试）：**0**

### 5.4 `go test ./modules/diskmgmt/ -race -count=1`

```
$ go test ./modules/diskmgmt/ -race -count=1
ok  	nas-panel/modules/diskmgmt	6.263s
```

### 5.5 `gofmt -l`（四个改动的 Go 文件）

```
$ gofmt -l web/modules/diskmgmt/raid_expand.go web/modules/diskmgmt/step_tracker.go \
           web/modules/diskmgmt/raid_expand_test.go web/modules/diskmgmt/raid_expand_stream_test.go
(no output)
```

## 6. 测试计数对比（相对 R3 结束时的基线）

| 口径 | R4 前基线 | R4 后（本次） | 差值 |
|---|---|---|---|
| 顶层 `--- PASS` | 165 | 171 | **+6** |
| 子测试 `--- PASS`（含顶层） | 214 | 216 | **+2**（净增子测试数，顶层新增的 6 个里有子测试的只有 `TestHandleRAIDExpandFS_R6_SysfsGate`，含 2 个子用例，其余 5 个均为无子测试的平测） |
| `--- FAIL` | 0 | 0 | 0 |

用排序后的测试名集合做 `comm` 差集验证，确认：

- **新增的 6 个顶层测试**（基线中不存在，本轮新增）：
  - `TestWaitForResyncCore_R6_TimeoutAfterMaxRounds`
  - `TestHandleRAIDExpandStream_R6_ResyncTimeoutStopsBeforeFS`
  - `TestHandleRAIDExpandFS_R6_SysfsGate`
  - `TestResyncPollOnce_R7_StreakRace`
  - `TestWaitForResyncCore_R7_StreakRaceViaEmit`
  - `TestWaitForResyncCore_EmitErrorStopsImmediately`
- **基线中丢失的测试**：无（空集）。165 个基线顶层测试全部原样保留在最终结果里，无一个被删除、改名或变红。

## 7. 全测试线收尾总览（P1-P8 / R1-R8，含本轮 R6/R7）

本轮是 `docs/opencode/2026-11-17-extend-tests/DESIGN.md` 规划的整条流式写路径测试扩展任务线的最后一轮，此处汇总全部已落地的用例，作为该任务线的收尾记录：

### P 系列 — `handlePoolExtendStream`（`pool_extend_test.go`）

| 编号 | 测试函数 | 覆盖点 |
|---|---|---|
| P1 | `TestHandlePoolExtendStream_P1_FullSuccess` | 全链路成功路径 |
| P2 | `TestHandlePoolExtendStream_P2_DefaultParams` | 默认参数行为 |
| P3 | `TestHandlePoolExtendStream_P3_MissingParams` | 必填参数缺失拒绝 |
| P4 | `TestHandlePoolExtendStream_P4_SafetyRejection` | 安全校验拒绝 |
| P5 | `TestHandlePoolExtendStream_P5_VgextendFails` | `vgextend` 失败路径 |
| P6 | `TestHandlePoolExtendStream_P6_GrowFSFails` | 扩文件系统失败路径 |
| P7 | `TestHandlePoolExtendStream_P7_CtxCancelStopsCommandChain` | ctx 取消即时中止命令链（R3 落地） |
| P8 | `TestHandlePoolExtendStream_P8_RetryAfterPartialRejectedByDiskInUse` | 部分失败后重试、磁盘占用拒绝（R3 落地） |

### R 系列 — `handleRAIDExpandStream`/`handleRAIDExpandFS`/`waitForResync*`（`raid_expand_stream_test.go`）

| 编号 | 测试函数 | 覆盖点 |
|---|---|---|
| R1 | `TestHandleRAIDExpandStream_R1_RAID1FullSuccess` | RAID1 全链路成功 |
| R2 | `TestHandleRAIDExpandStream_R2_RAID5ReshapeOnly` | RAID5 仅 reshape 路径 |
| R3 | `TestHandleRAIDExpandStream_R3_DeviceOrDetailFailure` | 设备/detail 查询失败路径 |
| R4 | `TestHandleRAIDExpandStream_R4_LevelRejections` | RAID level 不支持拒绝 |
| R5 | `TestHandleRAIDExpandStream_R5_TargetDiskNotFree` | 目标盘非空闲拒绝 |
| R6 | `TestWaitForResyncCore_R6_TimeoutAfterMaxRounds`、`TestHandleRAIDExpandStream_R6_ResyncTimeoutStopsBeforeFS`、`TestHandleRAIDExpandFS_R6_SysfsGate` | resync 超时上限参数化（本轮新增） |
| R7 | `TestResyncPollOnce_R7_StreakRace`、`TestWaitForResyncCore_R7_StreakRaceViaEmit` | streak 判定竞态：中途 busy 打断后归零重新累计（本轮新增） |
| R8 | `TestHandleRAIDExpandStream_R8_GrowFailsAndCtxCancel` | growFS 失败 + ctx 取消（R3 落地） |
| 回归 | `TestWaitForResyncCore_EmitErrorStopsImmediately` | R3 声明缺口收口：emit 断连/取消时立即停止轮询（本轮新增） |

### 直测用例（既有，保持不变）

| 测试函数 | 覆盖点 |
|---|---|
| `TestWaitForResyncReturnsNilWhenNoResyncLine`（`raid_expand_test.go`） | `waitForResync` 无 resync 行时返回 nil，本轮委托给 `waitForResyncCore` 后原样通过，无需适配 |

**合计：P 系列 8 个 + R 系列 11 个（R1-R5、R8 共 6 个 + R6/R7 共 5 个 + 回归 1 个）+ 既有直测 1 个 = 20 个顶层测试，构成本条测试线的完整用例清单。** 至此，这条针对流式写路径（`handlePoolExtendStream`/`handleRAIDExpandStream`/`waitForResync`）的测试扩展任务线闭环。

## 8. 提交

单次提交，仅覆盖本轮允许的 5 个文件（`raid_expand.go`、`step_tracker.go`、`raid_expand_test.go`、`raid_expand_stream_test.go`、`CHANGELOG.md`），提交信息：

```
refactor(diskmgmt): waitForResyncCore 抽取 — resync 轮询断连即时停止（R3 缺口收口）+ 超时/streak 参数化（R6/R7）
```

本报告文件（`docs/opencode/TASK-EXTEND-TESTS-R4-REPORT.md`）不纳入该提交。
