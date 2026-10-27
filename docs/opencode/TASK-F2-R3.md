# TASK-F2-R3: 建议接受链（F2 计划 Task 5）

仓库：/home/jacky/soft/nas（分支 feat/agent-f2）。Go module 在 web/。
前置：R1/R2 已落地 notifications/triggers（CreateSuggestion 冷却24h上限5/validateActions）+ 事件源 + 周报。先读 web/modules/triggers/、web/modules/ai/actions.go（PendingAction/defaultPendingStore/executeConfirmedAction/pendingActionConfirmWord/TTL）与 chat.go 里 start_task 确认卡 summary 的构造处，以代码现实为准。

## 必读
docs/plans/2026-10-23-agent-foundation-f2-plan.md 的 **Task 5**（AcceptSuggestion 流程/AcceptResult 形状/对抗清单逐字）。

## 硬约束
1. 禁止读取/执行仓库外文件；验证只用 go build / go vet / go test。
2. 新增 Go 依赖 0；TDD 先红后绿；单提交，信息用计划 Task 5 原文。
3. **零新确认语义红线**：接受链只负责「建议→plan 固化→pending action」，执行确认走既有 POST /api/ai/action/confirm（type-to-confirm/TTL/一次性 Take 全部既有行为，一字不改）；chat.go 既有确认门测试不许改动仍绿。
4. ai.CreatePendingAction(toolName, argsJSON, summary) 导出：ID 生成/确认词/TTL/存储与 chat 内部路径**共用同一 helper**（抽取重构，不许复制粘贴两份逻辑）；toolName 未注册为写工具即拒绝。
5. start_task 确认卡摘要与 ai 工具路径**同源**：抽取 chat.go/tools_tasks.go 的 summary 构造函数共享，禁止复制文案（计划红线）。
6. AcceptSuggestion 失败路径（plan 错误/工具未注册/建议非 new）不改建议 state；重放（二次 accept）409 语义。
7. PlanPhotoDedupe 0 组行为以 F1 实现为准（读代码确认返回形状后按计划 T5 测试清单第 5 条落实，报告写明实际语义）。
8. 泄漏面：accept 响应与审计 detail 不含 api_key/密码形状（对照 ai/security_test.go 模式加断言）。
9. REST: POST /api/triggers/suggestions/{id}/accept → 200 {"action_id","confirm_word","summary","job_id"}；enabledGuard + 审计（LogAuditRequest 既有模式）。
10. 不 git add 任务书与 OPENCODE-REPORT-F2-R3.md。

## 验证与提交
1. cd web && go build ./... && go vet ./modules/triggers/ ./modules/ai/ 全绿
2. go test ./modules/triggers/ ./modules/ai/ -count=1 -race 绿；go test ./... -count=1 全量无回归（已知沙箱 flake：tasks 并行 -race 偶发 SQLite OOM，-p 1 复跑确认非本轮引入）
3. 提交信息：`feat(triggers): 建议接受链 — ai.CreatePendingAction导出(复用既有确认门/TTL/确认词)+accept端点(plan固化→pending action→confirm即执行)+重放/过期/空计划对抗用例 (F2 T5)`
4. 报告写 OPENCODE-REPORT-F2-R3.md：CreatePendingAction helper 抽取说明（chat 路径如何共用）、summary 同源实现、0 组计划实际语义、对抗用例结果清单（6 项）、测试计数、偏离与疑虑。
