# OPENCODE-REPORT-F2-R3：建议接受链（F2 Task 5）

分支 `feat/agent-f2`，提交 `c523618`。

## 1. CreatePendingAction helper 抽取说明（chat 路径如何共用）

新增 `web/modules/ai/export.go`，核心是一个私有共享函数：

```go
func buildPendingAction(toolName, argsJSON, summary string) PendingAction {
    action := PendingAction{ToolName: toolName, ArgsJSON: argsJSON, Summary: summary}
    action.ID = newPendingActionID()
    action.ConfirmWord = pendingActionConfirmWord
    action.CreatedAt = nowFunc()
    defaultPendingStore.Put(action)
    return action
}
```

`chat.go` 原来在非流式分支（`handleChat`）和 SSE 分支（`handleChatSSE`）各有一份几乎相同的 5 行代码（ID 生成 + Summary 计算 + ConfirmWord + CreatedAt + Put）。本轮把这 5 行收敛成对 `buildPendingAction` 的一次调用，两个分支现在都是：

```go
summary := pendingActionSummary(result.PendingAction.ToolName, result.PendingAction.ArgsJSON)
action := buildPendingAction(result.PendingAction.ToolName, result.PendingAction.ArgsJSON, summary)
result.PendingAction = &action
```

`summary` 的**计算方式**两条路径不同（chat 路径用 `pendingActionSummary(toolName, argsJSON)` 自动按工具名分发；`CreatePendingAction` 的调用方自己算好 summary 传进来），但"摘要算出来之后怎么落地成一张 PendingAction"这一步完全共用 `buildPendingAction`，没有第二份复制粘贴的逻辑。

导出面：

```go
func CreatePendingAction(toolName, argsJSON, summary string) (id, confirmWord string, err error)
```

内部先查 `toolRegistry.Find(toolName)`：
- 未注册 → 拒绝（`"未知工具 %s"`）
- 已注册但 `Write == false`（只读工具）→ 拒绝（`"工具 %s 不是写工具，无需也不允许走确认门"`）
- 通过才调 `buildPendingAction`

ID 生成（`newPendingActionID`）、ConfirmWord（`pendingActionConfirmWord` 常量）、TTL（`pendingActionTTL` 常量 + `memoryPendingStore.Take` 的惰性过期判断）、存储单例（`defaultPendingStore`）全部是 chat.go 原有的那一套，`CreatePendingAction` 没有新建任何一个这四样东西的平行实现。

`chat.go` 的既有确认门测试（`actions_test.go`/`security_test.go`/`chat_test.go`/`chat_stream_test.go`）未改动一行，跑下来全绿——确认这次重构没有改变 chat 路径的可观察行为。

## 2. summary 同源实现

新增导出函数：

```go
func StartTaskSummary(job tasks.Job) string {
    return startTaskSummary(job)
}
```

`startTaskSummary` 是 chat.go 里原本只给 `pendingActionSummary` 的 `start_task` 分支用的私有函数（`"启动任务：%s（%d 组文件，释放 %s）"`）。`StartTaskSummary` 只是把它整个原样导出，不复制文案、不重写格式字符串。

`triggers.AcceptSuggestion` 拿到 `tasks.PlanPhotoDedupe` 返回的 `job` 后，直接调 `ai.StartTaskSummary(job)` 拿到与模型主动请求 `start_task` 时完全一致的摘要文案，然后按场景追加后缀（见第 3 节），不是另起一套模板。

`export_test.go` 里有一条直接断言这一点：

```go
got := StartTaskSummary(job)
want := startTaskSummary(job) // chat.go 内部函数，同一份实现的直接调用
if got != want { ... }
```

## 3. 0 组计划实际语义（读代码确认）

读 `tasks/kinds_dedupe.go` + `tasks/tasks.go` 确认：`PlanPhotoDedupe` 内部调 `CreateJob(kind, scopeRel, plan, impact)`，而 `CreateJob` **不对 plan/impact 做任何非空校验**——`plan=[]Step{}`、`impact=Impact{}` 时同样正常插入一行 `pending_confirm` 状态的 job，只是 `Impact.Groups=0`、`len(Plan)=0`。也就是说**没有"0 组则不建 job"这条路径**，`PlanPhotoDedupe` 对任何扫描结果（包括 0 组）都返回 `(job, false, nil)`。

因此 `AcceptSuggestion` 的实现是：
- 正常调 `PlanPhotoDedupe` → 拿到 job（可能是 0 步骤）
- `job.Impact.Groups == 0` 时，在 `ai.StartTaskSummary(job)` 产出的摘要后面追加：`（没有发现重复文件，可以直接确认完成，不会移动任何文件）`
- 继续正常走 `CreatePendingAction` → 建议照常迁到 `accepted`

**不是**计划草案设想的"若不建 job，返回『无需清理』语义错误且建议保持 new"那个分支——那个分支在当前 F1 实现下不存在（没有"不建 job"这种结果）。本轮按计划 T5 测试清单第 5 条"若建 0 步 job，Summary 明示『没有发现重复文件』"的那半句话落地，并在 `rules.go`/`kinds_dedupe.go` 之外没有改任何 F1 代码（硬约束 7：F1 实现为准）。

对应测试：`TestAcceptSuggestionNoDuplicatesBuildsZeroStepJobWithNoteSummary`（空池根，断言 Summary 含"没有发现重复文件"、job 是 0 步骤、建议仍正常迁到 accepted）。

## 4. 对抗用例结果清单（6 项，逐条对应计划 T5 清单）

| # | 用例 | 文件/测试名 | 结果 |
|---|------|------|------|
| 1 | accept 成功链：种重复文件→CreateSuggestion→accept→job pending_confirm+action_id 可 confirm→confirm 后 job running→done | `TestAcceptSuggestionSuccessChainPendingConfirmToConfirmToDone` | PASS |
| 2 | 重放：同建议二次 accept→409 语义；dismissed/expired accept→拒绝 | `TestAcceptSuggestionReplaySecondAcceptRejected`（断言 error 含"非法状态迁移"）/ `TestAcceptSuggestionDismissedRejected` / `TestAcceptSuggestionExpiredRejected` / `TestAcceptSuggestionNotFoundRejected`（额外补的"不存在"分支） | 4 条全 PASS |
| 3 | action TTL/一次性协议：accept 链产出的 action_id 走既有 Take 一次性+TTL 机制，不是平行新规则 | `TestAcceptSuggestionActionIsOneTimeConfirmSecondCallGone`（第一次 confirm 成功，第二次同 id 410；不存在的 id 同样 410） | PASS |
| 4 | CreatePendingAction 未注册工具名拒绝；chat.go 既有确认门测试全部不改动仍绿 | `ai/export_test.go` 的 `TestCreatePendingActionUnknownToolRejected`/`TestCreatePendingActionReadOnlyToolRejected`；`ai` 全包测试（含 `actions_test.go`/`security_test.go`/`chat_test.go`/`chat_stream_test.go`）`go test ./modules/ai/ -race` 全绿 | PASS |
| 5 | 无可清理文件：0 组行为按 F1 实现为准（见第 3 节），Summary 明示"没有发现重复文件" | `TestAcceptSuggestionNoDuplicatesBuildsZeroStepJobWithNoteSummary` | PASS |
| 6 | 泄漏面：accept 响应/审计 detail 不含 api_key/密码形状 | `TestAcceptSuggestionResponseAndAuditDoNotLeakSecretShapes`（文件名夹带"看起来像密钥"的字符串，断言 accept 响应全文不含该片段，且响应 JSON 恰好是 action_id/confirm_word/summary/job_id 四字段） | PASS |

额外补的边界用例（不在原始 6 项编号内，但计划文字里提到）：
- `TestAcceptSuggestionUnknownKindRejectedNoStateChange`：`kind≠photo_dedupe` 拒绝且不改 state
- `TestAcceptSuggestionNavigateOnlyRejectedNoStateChange`：navigate-only 卡 accept 明确拒绝且不改 state
- `TestAPIGuardFalseForbidsAllEndpoints` 补了 `/accept` 路径（guard=false→403）

## 5. 测试计数

- `web/modules/ai/export_test.go`：5 个新测试（`TestCreatePendingActionRegisteredWriteToolSucceeds`/`UnknownToolRejected`/`ReadOnlyToolRejected`/`TTLSameAsChatPath`/`TestStartTaskSummaryMatchesChatPathFormat`）
- `web/modules/triggers/accept_test.go`：10 个新测试
- `web/modules/triggers/triggers_test.go`：原有 `TestAPIGuardFalseForbidsAllEndpoints` 表格追加 1 条 `/accept` 用例（非新增测试函数）
- `go test ./modules/triggers/ ./modules/ai/ -count=1 -v` 共 154 个 `--- PASS`（两包合计，含本轮新增与此前 R1/R2/F1 遗留全部用例）

验证命令与结果：
```
cd web && go build ./...                                   # 通过
go vet ./modules/triggers/ ./modules/ai/                   # 无输出
go test ./modules/triggers/ ./modules/ai/ -count=1 -race   # ok ok
go test ./... -count=1                                     # 全绿
go test ./... -count=1 -race                                # tasks 包命中已知沙箱 flake（SQLite OOM）
go test ./... -count=1 -race -p 1                            # 全绿，确认 flake 非本轮引入
gofmt -l <本轮改动/新增文件>                                  # 空输出，格式干净
go test . -run TestArchBoundaries                           # PASS（triggers→ai 边界本就在白名单内，未改边界表）
```

`-race` 默认并行跑全量包时，`modules/tasks` 命中任务书描述的已知沙箱 flake（`unable to open database file: out of memory (14)`），与本轮改动的 `triggers`/`ai` 两包无关——`git status` 确认本次提交未改动 `web/modules/tasks/` 任何文件；单独 `-p 1` 重跑 `./modules/tasks/` 与全量 `./... -p 1` 均全绿，确认是并行文件描述符/内存压力导致的环境问题，不是本轮引入的回归。

## 6. 偏离与疑虑

1. **AcceptResult 的"空计划"分支与计划草案文字有出入**（已在第 3 节详细说明）：计划 T5 测试清单第 5 条写的是"若不建 job，AcceptSuggestion 返回『无需清理』语义错误且建议保持 new"，但读 F1 代码后确认 `CreateJob` 不会因为空 plan 拒绝，`PlanPhotoDedupe` 永远成功返回一个 job（哪怕 0 步骤）。本轮按清单第 5 条后半句"若建 0 步 job，Summary 明示『没有发现重复文件』"实现，这是两个 if 分支里代码现实唯一会发生的那个分支。没有改动 F1 的 `tasks` 包任何代码去"制造"出一个会返回 nil job 的分支——计划 Global Constraints 明确"硬约束 7：PlanPhotoDedupe 0 组行为以 F1 实现为准"，本轮严格照办。

2. **摘要追加文案的位置**：0 组时在 `ai.StartTaskSummary(job)` 产出的"启动任务：清理重复照片（0 组文件，释放 0B）"后面追加括号说明，而不是整句替换——这样摘要仍然是"同源"的（前半句与模型路径完全一致），只是追加了 triggers 场景特有的说明。`partial=true`（扫描预算内未扫完）时同样走追加后缀的模式，不覆盖原摘要。

3. **TTL 过期对抗用例的实现方式**：原计划写法隐含"构造一个已过期 CreatedAt 的 action 再 confirm"，但 `ai.nowFunc`/`defaultPendingStore` 都是包私有变量，triggers 包的测试无法从外部注入过期时间，也不应该为了这一个测试新增跨包的测试专用导出（会扩大 ai 包的公开面，偏离"只导出 CreatePendingAction/StartTaskSummary 两个函数"的最小化原则）。改用"一次性协议"的等价验证：accept 链产出的 action_id 第一次 confirm 成功、第二次必 410，外加不存在的 action_id 同样 410——这两种触发方式和"TTL 过期"命中的是 `memoryPendingStore.Take` 里完全相同的判断分支（一次性删除 + 时间比较都在同一个函数里），`ai/actions_test.go` 的 `TestConfirmActionExpiredReturns410` 已经独立覆盖了纯 TTL 维度，triggers 侧测的是"accept 链是否复用了这同一套协议"，两边互补，没有留验证空白。

4. **测试里新增了 `t.Setenv("NAS_DATA_DIR", t.TempDir())`**：因为 `ai.SaveConfig`/`ai.LoadConfig`/`ai` 会话库的路径注入变量（`aiConfigPath`/`aiDBPath`）是包私有的，triggers 包测试无法直接设置；`ai` 包自己对外的"路径可配置"机制就是读 `NAS_DATA_DIR` 环境变量（`defaultConfigPath`/`defaultDBPath` 都读它），所以用 `t.Setenv` 是复用 ai 包既有的公开配置口径，不是新开的测试后门。各用例互相独立的临时目录，不会相互污染，也不会碰真实 `/opt/nas/data`。

5. **没有触碰的范围**：本轮没有改 `web/modules/triggers/rules.go`/`report.go`/`loops.go`，没有改 `web/arch_boundary_test.go`（`triggers→ai` 边界 Task 2 起已经在白名单内，T5 不需要新增边）。前端通知中心（Task 6）按计划不在本轮范围内。
