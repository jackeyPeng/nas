# TASK-F2-R1: notifications 收件箱 + triggers 骨架（F2 计划 Task 1+2）

仓库：/home/jacky/soft/nas（分支 feat/agent-f2）。Go module 在 web/。
执行方式：严格按 docs/plans/2026-10-23-agent-foundation-f2-plan.md 的 **Task 1 与 Task 2** 逐条执行（含 Global Constraints 与「代码现实锚点」节——锚点给了 eventbus/tasks/ai/rclone 的真实签名与文件位置，写码前先读锚点指向的文件核对）。

## 硬约束（计划 Global Constraints 全文适用，重点重申）
1. 禁止读取/执行仓库外文件；验证只用 go build / go vet / go test / scripts/check-i18n.py。
2. 新增 Go 依赖 0；零 CGO。
3. TDD：计划里两个 Task 的测试清单为最小集，先红后绿；边界测试红灯自检（故意违规→红→还原）两个 Task 各做一次并在报告记录。
4. 架构边界：notifications 只准 import common；triggers 允许表按本轮实际用到的最小面开（后续 Task 需要时再扩）；禁止反向依赖（tasks/notifications/ai 不得 import triggers）。
5. cronMatch 从 rclone 抽到 common.CronMatch 是**机械迁移**：rclone/scheduler.go 改调 common，行为逐字不变，rclone 既有测试必须不动仍绿。
6. CreateSuggestion 的冷却（同 key 24h）与上限（活跃 new ≤5）是红线 10 的机器守卫，测试必须覆盖边界值（恰好 24h、第 5/6 条）。
7. enabledGuard 接线照 main.go:127-134 的 tasks 模式（InitXxx → SetEnabledGuard(ai.IsEnabled) → RegisterRoutes(mux, ai.IsEnabled)）；goroutine 常驻但动作前查 guard。
8. 每 Task 单独提交，提交信息用计划原文（两条）。不 git add 本任务书与 OPENCODE-REPORT-F2-R1.md。

## 验证与提交
1. cd web && go build ./... && go vet ./modules/notifications/ ./modules/triggers/ ./common/ ./modules/rclone/ 全绿
2. go test ./modules/notifications/ ./modules/triggers/ ./common/ ./modules/rclone/ -count=1 -race 绿；go test ./... -count=1 全量无回归；go test -run TestArchBoundaries . 绿
3. 报告写 /home/jacky/soft/nas/OPENCODE-REPORT-F2-R1.md：两 Task 各自的接口落地对照（签名与计划差异如有）、边界红灯自检记录、测试计数（前后对比）、事件订阅映射表落地形状、偏离与疑虑。
