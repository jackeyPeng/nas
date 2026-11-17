# TASK-AI-R1 执行报告：AI 助手 M1 第一轮（Task 1-3）

分支：`feat/ai-assistant`。本轮严格限定在计划的 Task 1、Task 2、Task 3，未触碰 Task 4 的六个导出 wrapper、未写任何 nas 工具实现、未改 frontend。

## 提交记录

```
eeee1f4 feat(ai): ToolDomain抽象+参数schema校验(未知参数拒绝) (ai M1 T3)
eae5da5 feat(ai): OpenAI-compatible LLM客户端(流式/非流式/连通性测试) (ai M1 T2)
c2b9db4 feat(ai): 模块骨架+配置存储(0600/key掩码)+enabled守卫+toggle开关 (ai M1 T1)
```

三个提交信息逐字采用计划原文。

## Task 1：模块骨架 + 配置存储 + enabled 守卫

**文件**：`web/modules/ai/config.go`、`ai.go`、`config_test.go`、`ai_test.go`；`web/main.go` 加一行 import + `ai.RegisterRoutes(mux)`。

**接口签名最终形态**（与计划逐项对照）：

- `type Config struct { Enabled bool; Preset string; BaseURL string; APIKey string; Model string; MaxToolCalls int; Verified bool }` —— 与计划一致，补了计划文字里提到但没写进 struct 定义代码块的 `Verified bool`（计划正文："Config 里加 `Verified bool` 标记"，Interfaces 代码块本身漏写，这里按计划意图补齐，非偏离）。
- `func LoadConfig() Config`、`func SaveConfig(c Config) error`、`func MaskAPIKey(key string) string`、`func aiEnabled() bool`、`func requireEnabled(next http.HandlerFunc) http.HandlerFunc` —— 签名与计划一致。
- 路由：`GET /api/ai/status`（免守卫）、`GET/POST /api/ai/config`（requireEnabled）、`POST /api/ai/toggle`（例外，不挂 requireEnabled）—— 与计划一致。

**一处落盘方式偏离计划原文，已按仓库事实修正**：计划 Interfaces 写"经 common.SafeWriteFile + os.Chmod 或临时文件模式，与 vault 落盘方式对齐"。实地核查后发现：

- `common.SafeWriteFile` 内部走 `sudo cp`（`web/common/sudo.go:48`），是给 `/etc/...` 等 root-only 系统配置文件设计的写入路径；
- vault 的落盘方式实际是 SQLite db 文件 `os.Chmod(path, 0600)`（`vault.go:119`），不是 JSON 配置文件先例；
- 仓库里真正的"面板自身 JSON 配置文件、0600"先例是 `web/modules/system/2fa.go`：`os.WriteFile(path, data, 0600)`，没有经过 sudo。

`ai.json` 落在 `/opt/nas/data/`，这个目录是面板进程自己起的（`main.go` 里 `common.InitAuditLog(dataDir)` 同目录），面板对它有直接写权限，不需要 sudo。沿用 `SafeWriteFile` 反而会在没有 sudoers 配置 `cp` 到 `/opt/nas/data/` 的场景下制造不必要的权限依赖，且与"AI 模块不新增任何特权命令调用面"的硬约束精神相悖（SudoExec 调用面即使指向自己的数据目录也是多余的面）。最终实现采用 `2fa.json` 模式：`os.WriteFile(configPath(), data, 0600)`，不经 sudo。此偏离已在此处说明，未静默处理；函数签名（`SaveConfig(c Config) error`）本身零偏离。

**测试要点**：`config_test.go` 用包变量 `aiConfigPath` 注入 `t.TempDir()`，对齐 filemgmt `poolRootOverride` 模式；覆盖缺文件默认值、损坏文件默认值、Save→Load 往返一致、文件权限 0600、MaskAPIKey 三态。`ai_test.go` 用 `httptest` 跑真实 mux：未启用时 `GET /api/ai/config` 403 + 错误文案逐字匹配、`GET /api/ai/status` 200 且响应体不含 `api_key` 字段、`POST /api/ai/toggle enabled=true` 未配置/未验证时 400 + 错误文案逐字匹配、已验证配置开启成功且立即生效（无需重启）、关闭开关不清空已保存端点配置、`GET /api/ai/config` 回显 key 掩码且不含明文子串。

## Task 2：LLM 客户端

**文件**：`web/modules/ai/llm.go`、`llm_test.go`。

**接口签名**：`ChatMessage`、`ToolCall`、`ChatRequest`、`ChatOnce`、`ChatStream`、`TestConnection` —— 与计划签名完全一致，零偏离。

额外实现了计划 Task 2 Steps 里明确要求但未列入 Interfaces 代码块的 `POST /api/ai/test-connection` 端点（步骤原文："POST /api/ai/test-connection 端点...调 TestConnection，成功置 Config.Verified=true 并 SaveConfig，失败回 502+原因"）。放在 `llm.go`（而非 `ai.go`）是因为它是 LLM 客户端的直接 HTTP 封装层，路由注册仍在 `ai.go` 的 `RegisterRoutes` 里完成，职责边界保持清晰。

**实现要点**：

- SSE 解析用 `bufio.Scanner` + 显式 `Buffer(64KB 初始, 1MB 上限)`，逐行找 `data:` 前缀，`[DONE]` 结束；单帧解析失败不中断整个流（容忍协议噪音）。
- 非流式响应体读取用 `io.LimitReader(resp.Body, 8MB)`。
- `chatCompletionsURL` 用 `strings.TrimRight(baseURL, "/")` 容忍尾斜杠。
- `TestConnection` 用 `context.WithTimeout(ctx, 5*time.Second)` 包一层，不依赖调用方传入的 ctx 本身超时。
- 错误分类：401/403→"鉴权失败"，404→"模型不存在"，网络层错误→"无法连接到模型端点"，超时→"连接超时"；均为中文语义错误，经 `fmt.Errorf("...%w", err)` 包裹原始错误保留排查线索。

**测试要点**：httptest 假 OpenAI 服务器覆盖——非流式 tool_calls 解析（Name/Arguments/ID）、baseURL 尾斜杠去重、流式 3 个 content delta + 跨 chunk 拆分的 tool_call（id 在首块、arguments 分两块聚合）、401/404/非 JSON/连接拒绝/超时五种 TestConnection 错误路径、test-connection 端点成功置位 Verified 与失败 502 且不置位、超长单行（500KB）SSE 验证 1MB buffer 不 panic。

一个测试设计教训记录在此：`TestTestConnectionRespectsContextTimeout` 最初用 `<-r.Context().Done()` 让 handler 阻塞到请求 ctx 取消，但 `httptest.Server.Close()` 会等待所有连接上的 handler 退出，若 handler 退出路径依赖的信号与 `Close()` 的等待时机有竞争会导致测试挂起（实测挂起 5s 后报 "httptest.Server blocked in Close"）。改为 `select { case <-time.After(2s): case <-r.Context().Done(): }` 后问题消失——这是测试基础设施层面的坑，不涉及被测代码逻辑变化。

## Task 3：ToolDomain 抽象 + schema 校验

**文件**：`web/modules/ai/domain.go`、`domain_test.go`。

**接口签名**：`Tool`、`ParamSpec`、`Domain`、`NewRegistry`、`(*Registry) SchemaForLLM()`、`(*Registry) Find(toolName string) (Tool, bool)`、`ValidateArgs(t Tool, raw string) (map[string]interface{}, error)` —— 与计划签名完全一致，零偏离。这是后续轮次（Task 5 只读工具、Task 10 写工具）的契约核心，特别谨慎核对过，字段名/方法签名逐一对照计划文本无误。

**实现要点**：

- `ValidateArgs` 的校验顺序严格按计划 Steps 给定次序：JSON 解析 → **未知参数拒绝**（提前到类型/枚举校验之前，因为这是安全红线——"参数不合法直接拒绝，不进模型重试循环"）→ required 缺失 → 类型断言 → enum 校验。
- 类型映射：`json.Unmarshal` 把所有 JSON 数字解析成 `float64`，所以 `integer` 类型额外校验"无小数部分"（`f != float64(int64(f))`），不存在 Go 原生 int 断言可用的路径。
- `enum` 当前只处理 string 类型值（计划里六个写工具的 Enum 字段全部是 string 枚举，如 `restart_service` 的 service 名、`create_folder` 的 permission），非 string 值直接判不匹配而非 panic。
- `SchemaForLLM` 保持注册顺序稳定（`order []string` 记录首次注册顺序），避免每次请求 tools 数组顺序随机抖动。

**测试要点**：双域注册验证 `Find` 跨域命中/未命中；`SchemaForLLM` 做 JSON 结构快照断言（type=function、parameters.type=object、properties/required/enum 字段形态）且验证整体可 `json.Marshal`；`ValidateArgs` 覆盖计划点名的六用例（合法/缺 required/类型错/enum 外/未知参数/坏 JSON）外，额外补了 boolean/integer 类型校验、空字符串 arguments（模型未传参数时的常见情况）、无参数工具收到任意参数应拒绝——这些是为后续 Task 5/10 消费这套校验时的边界情况打底，不改变已定稿的接口签名。

## 验证结果摘要

```
cd web && go build ./...                         ✅ 通过
go vet ./modules/ai/                             ✅ 无警告
go test ./modules/ai/ -count=1 -race             ✅ 31 个测试全绿，3.1s
go test ./... -count=1（全仓库）                  ✅ 21 个包全绿，零回归
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build    ✅ 交叉编译通过
git diff web/go.mod web/go.sum                   ✅ 零新增依赖
```

## 偏离与疑虑汇总

1. **ai.json 落盘方式**（Task 1）：计划文字建议走 `common.SafeWriteFile`，实测该函数是 sudo-only 系统文件写入路径，与 vault 的"落盘方式对齐"描述在事实上对不上（vault 是 db 文件 chmod，不是 JSON 写入）。改用 `system/2fa.go` 的 `os.WriteFile+0600` 直写模式，理由已在 Task 1 小节详述。`SaveConfig(c Config) error` 签名本身未变，只是内部实现路径不同，判断不构成"接口偏离"，但按要求在此标记供复核。
2. **Config.Verified 字段**：计划 Interfaces 代码块里的 struct 定义漏写了这个字段（只在正文和 Task 1 最后一条路由说明里提到），已按计划意图在代码里补上，不算偏离，只是指出文档本身有处笔误供计划维护者参考。
3. **POST /api/ai/test-connection** 端点：计划 Interfaces 没有把它列进 Task 2 的"Produces"代码块，只在 Steps 里以自然语言描述。已实现，放在 `llm.go`，路由注册在 `ai.go`。若后续轮次的 Interfaces 契约需要引用这个端点，目前的实现可以直接作为既成事实对齐。
4. 未发现计划与代码现实的实质性矛盾需要阻塞本轮交付；上述三点均为"计划文档笔误/描述不精确"级别，已在实现时按仓库既有模式（2fa.go、filemgmt 测试注入模式）就近对齐，未做任何静默的接口签名改动。

## 未做事项确认（范围边界）

- 未动 Task 4 的六个模块 `export.go`。
- 未写 `tools_nas_read.go` / `tools_nas_write.go` 或任何工具实现。
- 未动 `web/frontend/` 任何文件。
- `go.mod` 零新增依赖、零 CGO（`CGO_ENABLED=0` 交叉编译已验证）。
- 未读取/执行仓库外任何文件；验证只用仓库内 `go build`/`go vet`/`go test`。
- `TASK-AI-R1.md`、`TASK-AI-R2.md`（brief 文件）、本报告文件均未 `git add`。
