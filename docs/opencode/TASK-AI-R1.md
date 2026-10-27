# TASK-AI-R1: AI 助手 M1 第一轮（计划 Task 1-3）

仓库：/home/jacky/soft/nas（分支 feat/ai-assistant，已建好）。Go module 在 web/。

## 必读（先读完再动手）
1. docs/plans/2026-10-21-ai-assistant-plan.md —— 本轮只做 **Task 1、Task 2、Task 3**，严格按每个 Task 的 Files/Interfaces/Steps 执行（Interfaces 是后续轮次的契约，签名不得偏离；发现计划与代码现实矛盾时 report 标记，不得静默改接口）。
2. docs/plans/2026-10-21-ai-assistant-design.md §三（架构）与 §五（安全）。
3. 计划 Global Constraints 全部生效。

## 硬约束
1. 禁止读取/执行仓库外任何文件（沙箱拒绝即中止 run）；验证只用仓库内 go build / go test。
2. 只做 Task 1-3：main.go 只加一行 ai.RegisterRoutes(mux) + import；**不碰** Task 4 的六个模块 export.go、不写任何工具实现、不碰 frontend。
3. go.mod 零新增依赖、零 CGO。
4. 每个 Task 单独提交，提交信息用计划里给出的原文。
5. 测试先写先跑红再实现（计划 Steps 顺序）；测试必须调真函数，httptest 假 LLM 服务器脚本化响应。
6. 不 git add 本 brief 与 OPENCODE-REPORT-AI-R1.md。

## 关键落点提醒（已核实的仓库事实，勿再猜测）
- 模块注册：web/main.go 137-156 行是 RegisterRoutes 块，filemgmt 之后加 ai；import 路径 nas-panel/modules/ai。
- common 已有：JSONResponse、AuthMiddleware、SafeWriteFile(destPath, content)、LogAuditRequest(r, category, action, detail, result)、ValidSecret(s) error、OpenSQLite(path)。
- 数据目录落盘先例：web/modules/vault/vault.go 的 vaultDBPath()/配置读写模式；ai.json 与 ai.db 放 /opt/nas/data/，路径做成包变量（aiConfigPath/aiDBPath）供测试注入（对齐 filemgmt poolRootOverride 模式）。
- Config 需要 Verified bool 字段（Task 1 Interfaces 里 toggle 的开启条件依赖它）。
- LLM 客户端：SSE 行解析 bufio.Scanner + 1MB buffer 上限；响应体 8MB 上限；baseURL 容忍尾斜杠；超时用 context（TestConnection 5s）。
- ToolDomain：ValidateArgs 未知参数必须拒绝；ParamSpec.Type 只认 string/number/integer/boolean。

## 验证与提交
1. cd web && go build ./... && go vet ./modules/ai/ && go test ./modules/ai/ -count=1 -race 全绿。
2. 三个提交（每 Task 一个），信息用计划原文：
   - `feat(ai): 模块骨架+配置存储(0600/key掩码)+enabled守卫+toggle开关 (ai M1 T1)`
   - `feat(ai): OpenAI-compatible LLM客户端(流式/非流式/连通性测试) (ai M1 T2)`
   - `feat(ai): ToolDomain抽象+参数schema校验(未知参数拒绝) (ai M1 T3)`
3. 报告写 /home/jacky/soft/nas/OPENCODE-REPORT-AI-R1.md：逐 Task 说明、接口签名最终形态（如与计划有出入必须列出并说明理由）、测试输出摘要、偏离与疑虑。
