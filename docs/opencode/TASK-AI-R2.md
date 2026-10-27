# TASK-AI-R2: AI 助手 M1 第二轮（计划 Task 4-5）

仓库：/home/jacky/soft/nas（分支 feat/ai-assistant）。Go module 在 web/。
前置：R1 已落地 web/modules/ai/{ai,config,llm,domain}.go（Tool/ParamSpec/Domain/Registry/ValidateArgs、Config、ChatOnce 等）——先读这些文件确认实际签名，以**代码现实为准**衔接。

## 必读
1. docs/plans/2026-10-21-ai-assistant-plan.md 的 **Task 4、Task 5**（Files/Interfaces/Steps 严格执行）。
2. docs/plans/2026-10-21-ai-assistant-design.md §四（工具表）。

## 硬约束
1. 禁止读取/执行仓库外文件；验证只用仓库内 go build / go test。
2. 只做 Task 4-5。**不碰** web/modules/ai/ 下 R1 已交付文件的既有逻辑（tools_nas_read.go 是新文件；如 Registry 组装需要小改 ai.go 允许，report 说明）；不碰 frontend；不实现任何写工具（Task 10 的事）。
3. Task 4 的抽取（diskmgmt/overview.go、diagnostics/diagnostics.go）是**纯机械重构**：抽函数后 handler 调它，行为逐字节不变；抽取前后各跑一次相关包测试证明。
4. export.go wrapper 一律薄导出（= 调既有未导出函数），禁止复制逻辑；每个 wrapper 配 export_test.go（开发机无系统命令环境下不 panic、返回空值或 error）。
5. logs.QueryAudit 的 AuditEntry 字段与 SQL 按计划表定义；limit 上限 200、sinceHours 上限在工具层 clamp（168）。
6. 每 Task 单独提交，信息用计划原文。
7. 不 git add 本 brief 与 OPENCODE-REPORT-AI-R2.md。

## 落点提醒（已核实）
- system 域已有导出：system.GetRegistry()/RefreshRegistry() RegistryReport（system_registry.go:441/486），直接用不动。
- monitor.getMonitorSnapshot()（sampler.go:83）、users.getUsers()（users.go:71）、firewall.getStatus()（firewall.go:48）、diskmgmt.getDiskStatus()（storage.go）、logs.handleQueryLogs（logs.go:19，查询内核在里面，抽出复用）。
- diagnostics 状态构造在 handleStatus（diagnostics.go:142）内联——按计划抽 buildStatusSnapshot()。
- 工具输出统一 json.Marshal + 8000 字符截断（超出尾部加 `...(截断)`）；search_files_by_name 本期返回计划里定义的占位说明 JSON（M3 替换实现）。
- get_health_report 的 items 裁剪字段：id/name/status/detail。

## 验证与提交
1. cd web && go build ./... && go vet ./modules/... && go test ./modules/ai/ ./modules/monitor/ ./modules/users/ ./modules/diskmgmt/ ./modules/firewall/ ./modules/diagnostics/ ./modules/logs/ ./modules/system/ -count=1 全绿；go test -race ./modules/ai/ 绿。
2. 提交信息：
   - `refactor(modules): 为AI工具域导出内核wrapper(monitor/users/diskmgmt/firewall/diagnostics/logs),overview与diagnostics状态构造抽纯函数 (ai M1 T4)`
   - `feat(ai): nas域9个只读工具(统一JSON输出+8K截断+参数clamp) (ai M1 T5)`
3. 报告写 /home/jacky/soft/nas/OPENCODE-REPORT-AI-R2.md：wrapper 清单+对应内核函数、抽取重构前后测试对照、工具表最终形态、偏离与疑虑。
