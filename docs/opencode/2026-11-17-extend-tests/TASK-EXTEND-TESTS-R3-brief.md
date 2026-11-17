# TASK: extend/expand 流式写路径单测 R3 — stepTracker + ctx 取消语义（P7/P8/R8）

## 依据与前置

- 设计方案：`docs/opencode/2026-11-17-extend-tests/DESIGN.md`（通读；代码现实优先，冲突须申报）。
- R2 已落地（commit 26ac2f4）：seams.go 七个包级 seam、stream_stubs_test.go 桩、P1-P6/R1-R5 全绿。R2 报告：`docs/opencode/2026-11-17-extend-tests/TASK-EXTEND-TESTS-R2-REPORT.md`（§5 是给你的移交清单）。
- 本轮目标：设计 §3 的第 3/4/11 项（SSE 发送可失败 + stepTracker + ctx 感知）+ 用例 P7/P8/R8。**R6/R7 与 waitForResyncCore 抽取（设计 §3 第 9/10 项）仍留给下一轮，本轮不做。**

## 本轮授权（R2 红线解除部分）

- **允许改 sendPoolExtendProgress / sendRAIDExpandProgress 的签名为返回 error**（设计 §3 第 3/11 项）——调用点全部在两 handler 内，机械改。
- 允许在两 handler 内引入 stepTracker（设计 §3 第 4 项）。
- 不改路由、不改 JSON 形态、不碰 common 包、不碰 stream.go（向导路径）、不动 seams.go 既有变量。

## 设计要求（照此实现，偏离须申报）

### 1. sendXxx 返回 error（pool_extend.go / raid_expand.go）

`fmt.Fprintf` 返回值不再丢弃：写失败（客户端断连）返回非 nil error。

### 2. stepTracker（新结构，建议放 pool_extend.go 或独立文件）

```go
func newStepTracker(total int, emit func(PoolExtendEvent) error, sleep func(time.Duration), ctx context.Context) *stepTracker
// 方法：
//   Running(name string) error  // 检查 ctx.Err() → emit running 事件 → sleepFn(100ms)
//   Done(name string) error     // 同上，done 事件
//   Error(step, detail string) error
```

每次操作先查 `ctx.Err()`；ctx 已取消 → 直接返回 error（不发事件——连接已死），调用方据此中止命令链。

### 3. handler 改造（行为语义：目标行为）

- 两 handler 开头取 `ctx := r.Context()`，传给 stepTracker。
- 命令执行循环：每条 `sudoExecFn(...)` 前检查 tracker 的错误状态（上一步 Running/Done 失败或 ctx 取消 → 中止，不再执行后续命令）。
- **ctx 取消 / SSE 写失败后的行为（P7/R8 断言的目标）**：① 不再执行后续 sudoExecFn；② 不再发送任何新 SSE 事件（连接已死，写了也白发）；③ handler 正常返回（不发 panic、不写 500）。
- 现有 SSE 事件序列与字段（running/done/error/complete 的 Step/Index/Total/Detail）在正常路径下**逐字节不变**——R2 的 11 个用例必须继续全绿（这是回归红线）。

### 4. 用例（新建或并入 R2 测试文件）

- **P7（ctx 取消中断）**：`httptest.NewRequest` 带 `context.WithCancel`；在 handler 开始执行后、第 2 条命令执行前 cancel（可用 stubExec 的 hook：第 1 条命令调用时触发 cancel）。断言：cancel 后 stubExec 录制序列里不再有新命令；响应体在 cancel 时点后不再出现新事件。
- **P8（中断后重试恢复语义）**：P5/P7 半途状态（device 已成 PV——用 execOutputFn 桩 pvs 分支不可控，就用 lsblk FSTYPE 或 mdstat 桩构造「已占用」读数）后再次请求 → 被 isDiskInUse 拒绝；断言第二次 wipefs 未被调用。
- **R8（RAID 命令失败 + 断连）**：mdadm --grow 桩返回 error → error 事件含原始输出、后续命令不执行；ctx cancel 子用例同 P7 断言。
- 每个 stub 替换 `t.Cleanup` 还原（R2 桩设施直接复用）。

### 5. 文档

CHANGELOG.md `[未发布]` 段加一条：
`- fix(diskmgmt): 流式扩容/RAID 扩容 handler 感知客户端断连与 ctx 取消 — SSE 写错误不再被丢弃、命令链即时中止（此前客户端 kill 后后台命令继续跑到底）；正常路径事件序列不变，P7/P8/R8 用例锁定语义`

## 已核实事实（parent 验证过，勿查外部）

- R2 的 seam/桩设施：seams.go（sudoExecFn 等 7 变量）、stream_stubs_test.go（stubExec 记录调用序列、withSSE、parseSSE、withNoopSleep）。
- 两 handler 现状：`fmt.Fprintf` 返回值被丢弃；无任何 r.Context() 读取（R2 报告 §5.1 确认）。
- R2 全部 11 个用例名：TestHandlePoolExtendStream_P1..P6 / TestHandleRAIDExpandStream_R1..R5 —— 回归基线。
- stepRunning/stepDone 形态（raid_expand.go:148/152 附近 100ms sleep + 发事件）可直接被 stepTracker 取代或并存，以最小 diff 自行裁决并在报告说明。

## 硬约束

- 只动：pool_extend.go、raid_expand.go、（可选新建 step_tracker.go）、pool_extend_test.go、raid_expand_stream_test.go、CHANGELOG.md。growfs.go/storage.go/raid_sysfs.go/seams.go/stream_stubs_test.go 仅在必要时报经说明的最小改动（默认不碰）。
- **禁止读取仓库外任何文件**（~/go/pkg/mod、/tmp、repo 外路径）——一次拒绝即中止整个 run。
- 验证：`go build ./...` + `go test ./modules/diskmgmt/ -count=1`（全绿，162+ 存量 PASS 不许掉）+ `go vet ./modules/diskmgmt/...`。
- 冲突申报进报告，不静默绕过。
- 单笔 commit，信息精确用：`fix(diskmgmt): 流式扩容/RAID 扩容感知 ctx 取消与 SSE 断连 — 命令链即时中止，事件序列不变（P7/P8/R8）`
- 禁 git push。

## 产出报告

写 `docs/opencode/TASK-EXTEND-TESTS-R3-REPORT.md`（不 git add）：改动清单、build/test 真实输出尾行、存量用例数对比（162 基线）、偏离申报、留给 R4（waitForResyncCore）的移交注意点。
