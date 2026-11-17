# TASK-F1-R1: 任务引擎核心（F1 计划 Task 1 + Task 2）

仓库：/home/jacky/soft/nas（分支 feat/agent-foundation）。Go module 在 web/。

## 必读
1. docs/plans/2026-10-22-agent-foundation-f1-plan.md 的 **Task 1 与 Task 2**（接口签名/测试清单/步骤逐字执行，签名不得偏离——后续 Task 3-7 依赖它们）。
2. docs/plans/2026-10-22-agent-foundation-design.md §三/§三b/§四（架构语境）。

## 硬约束
1. 禁止读取/执行仓库外文件；验证只用 go build / go vet / go test。
2. 新增 Go 依赖 0（stdlib + 既有 common 基建）。
3. TDD：先写失败测试再实现（计划里每个 Step 的测试清单是最小集，可加不可减）。
4. **架构边界测试的基线 map 必须按现状真实 import 生成**（先扫描现有代码得出，再写死进测试；别凭记忆编——写完后故意加一行违规 import 验证测试会红，然后还原，把这次红灯验证写进报告）。
5. 崩溃恢复语义按计划收紧版：InitTasks 时 status=running 的 job → paused + progress_note 追加"面板重启已暂停，可继续"（不自动续传）。
6. worker 生命周期挂 ai enabled 守卫：关断时拒绝新任务入队、运行中任务在当前步骤边界暂停落库；无后台 goroutine 泄漏（测试断言）。
7. 进度事件里程碑制：task.progress 只在 ≥10%/25%/50%/75% 各发一次（每里程碑至多一次）+ task.done/task.failed 必发；用 common.EmitEvent("tasks", ...)，事件 code=kind。
8. api.go 错误响应一律 JSON content-type（参照 modules/ai/ai.go 的 jsonError helper 模式，tasks 包内自建同款 helper，不跨包借）；非法状态迁移 409。
9. main.go 注册走既有模块注册模式（对照 ai 模块接法）；ai 模块导出 IsEnabled()（config.go 既有内部判断的薄导出，不改行为）。
10. 每 Task 单独提交，信息用计划原文（T1/T2 各一条）。不 git add 本 brief 与 OPENCODE-REPORT-F1-R1.md。

## 落点提醒（已核实）
- common.OpenSQLite(path string) (*sql.DB, error) — common/sqlite.go:32（WAL 已内置）
- common.EmitEvent(source, typ, code string, params map[string]interface{}, detail string) — common/eventbus.go:112
- common.Subscribe() chan Event — common/eventbus.go:143
- SSE 模式参照 modules/diskmgmt/stream.go（Content-Type/Cache-Control/Connection 三头 + Flush）
- ai 模块 enabled 判断在 modules/ai/config.go；路由注册模式见 modules/ai/ai.go RegisterRoutes
- jobs 表字段按 spec §四：id/kind/status/scope_rel/plan_json/impact_json/progress_cur/progress_total/progress_note/checkpoint_json/result_json/error/created_at/started_at/finished_at（impact_json 为计划 Impact 结构的落库形态）
- 数据目录：/opt/nas/data（测试用 t.TempDir() + InitTasks(dir) 注入）

## 验证与提交
1. cd web && go build ./... && go vet ./modules/tasks/ ./modules/ai/ . 全绿
2. go test ./modules/tasks/ -count=1 -race 绿；go test -run TestArchBoundaries . 绿；go test ./... -count=1 全量无回归
3. 提交信息（两条）：
   - `feat(tasks): 任务引擎骨架 — jobs表/状态机/checkpoint/崩溃恢复paused + 架构边界测试(import图锁定分层) (F1 T1)`
   - `feat(tasks): worker执行器(checkpoint续跑/暂停恢复取消/里程碑事件)+REST与SSE任务API(enabled守卫) (F1 T2)`
4. 报告写 /home/jacky/soft/nas/OPENCODE-REPORT-F1-R1.md：边界测试基线 map 全量（现有跨模块依赖清单）、红灯验证记录、状态机迁移表、worker 信号机制说明、测试计数、偏离与疑虑。
