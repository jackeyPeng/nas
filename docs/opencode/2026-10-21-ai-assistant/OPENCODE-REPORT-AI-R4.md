# AI 助手 M1 第四轮（Task 8-9，前端）执行报告

分支：`feat/ai-assistant`　提交：`209a29b`（T8）、`193b104`（T9）

## 一、新增 i18n 键数

- `nav.ai` 1 个 + 顶层 `ai.*` 区块 40 个，共 **41 个新键**，zh-CN/en-US 同步。
- `python3 scripts/check-i18n.py` 通过（zh-CN 1310 keys / en-US 1310 keys，key 集合一致）。
- 全部键一次性在 T8 提交落地（Task 8 清单 18 个 + Task 9 清单 13 个 + `nav.ai`/`ai.not_enabled`/`ai.save_ok`/`ai.turn_on_failed`/`ai.onboarding_title`/`ai.onboarding_desc`/`ai.no_messages`/`ai.tool_call_label`/`ai.tool_result_label`/`ai.error_prefix` 等补充项），T9 提交未再新增 i18n 文件改动——语义只增不减，check-i18n.py 两轮均通过。

键清单（zh 侧原文）：

```
nav.ai = "AI 助手"
ai.title / ai.tab_chat / ai.tab_settings / ai.enabled / ai.disabled_hint / ai.not_enabled
ai.preset / ai.preset_deepseek / ai.preset_ollama / ai.base_url / ai.api_key / ai.model
ai.test_conn / ai.testing / ai.test_ok / ai.save / ai.save_ok
ai.turn_on / ai.turn_off / ai.turn_on_failed / ai.privacy_note
ai.onboarding_title / ai.onboarding_desc
ai.placeholder / ai.send / ai.thinking / ai.tool_running
ai.conversations / ai.new_chat / ai.no_conversations / ai.no_messages
ai.tool_call_label / ai.tool_result_label / ai.pending_hint / ai.error_prefix
ai.confirm_title / ai.confirm_word_label / ai.confirm_execute / ai.cancel
```

## 二、app.js 新增函数清单

状态字段：`aiStatus`、`aiTab`、`aiConfigForm`、`aiTesting`、`aiConversations`、`aiConversationId`、`aiMessages`、`aiInput`、`aiThinking`。

| 函数 | Task | 作用 |
|---|---|---|
| `loadAIStatus()` | T8 | GET `/api/ai/status`；驱动导航入口显隐；`enabled=true` 时联动拉取 `loadAIConfig()` |
| `loadAIConfig()` | T8 | GET `/api/ai/config`（仅在已启用时调用，否则 403）；回显表单，`api_key` 始终留空（不回显掩码值，避免用户误以为是可编辑明文） |
| `applyAIPreset(preset)` | T8 | deepseek/ollama 预设联动 base_url/model；ollama 清空+禁用 api_key 输入 |
| `saveAIConfig()` | T8 | POST `/api/ai/config` |
| `testAIConnection()` | T8 | 先 `saveAIConfig()` 再 POST `/api/ai/test-connection`（该端点测的是服务端已保存配置，不接受请求体） |
| `toggleAI(enabled)` | T8 | POST `/api/ai/toggle`；失败时后端中文原因由 `api()` 自动 toast |
| `loadAIConversations()` | T9 | GET `/api/ai/conversations` |
| `newAIConversation()` | T9 | 清空当前会话 ID/消息/输入框，开始新对话（无需调接口，conversation_id 留空由后端首次 chat 请求时生成） |
| `loadAIConversation(convID)` | T9 | GET `/api/ai/conversations/{id}`；过滤只回放 `role in (user, assistant)`（落库里的 role=tool 回填消息和临时 UI 态不参与历史回放） |
| `sendAIMessage()` | T9 | fetch POST `/api/ai/chat`（`Accept: text/event-stream`）+ `getReader()` 逐行解析，对齐 `resetStorage()` 的 SSE 消费先例 |
| `handleAISSEEvent(ev, assistantMsg)` | T9 | 按 `type` 分发六种 SSE 事件到当前 assistant 消息对象 |

`navigate()` 新增 `case 'ai'`：`loadAIStatus()` + `loadAIConversations()`；`init()` 里已登录（`token` 非空）时额外拉一次 `loadAIStatus()`，确保刷新页面后侧栏入口显隐立即正确，不需要先进 ai 页才知道。

## 三、SSE 事件处理对照表

与 `web/modules/ai/chat.go` 的 `sseEvent` 结构逐字段对照（已读源码确认，非猜测）：

| 事件 type | 后端字段 | 前端处理 |
|---|---|---|
| `delta` | `text` | `assistantMsg.content += text` 增量拼接 |
| `tool_call` | `name`, `args_digest` | 追加 `{name, digest: args_digest}` 到 `assistantMsg.toolTrace` |
| `tool_result` | `name`, `digest` | 找到同名且未 `resultApplied` 的条目，补全 digest 并标记；找不到则新开一条（容错：理论上 tool_call 必先于 tool_result，但不假设强顺序保证） |
| `pending_action` | `PendingAction` 全字段摊平（`id/tool_name/args_json/summary/confirm_word/created_at`） | 存进 `assistantMsg.pendingAction`；页面渲染只读信息卡 + `ai.pending_hint` 占位文案（未做确认交互，R5 接入） |
| `done` | `conversation_id` | 记录 `aiConversationId`，刷新会话列表 |
| `error` | `message` | `assistantMsg.error` 内嵌红条 + `showToast` |

## 四、与后端实际响应形状的差异/发现

读代码确认的后端契约（非假设）：

1. **`/api/ai/config`（GET+POST）整体挂了 `requireEnabled`**（`web/modules/ai/ai.go:28`），只有 `/api/ai/toggle` 和 `/api/ai/test-connection` 在未启用态也可调用（`ai.go:29-31`）。而 `handleTestConnection` 测的是 `LoadConfig()` 读到的**已保存**配置（`llm.go:245`），不接受请求体里的待测值。

   **影响**：设计 spec §三「填端点→连通性测试→开启」的首次开启向导，字面意义上要求「未启用态下填完表单直接测/存」，但当前后端：未启用时 POST `/api/ai/config` 会 403，用户填的表单内容根本存不进去，`test-connection` 也测不到它。本轮前端完全按现状对接（`api()` 会把 403 的中文 `error` 字段 toast 出来，不做静默绕过或猜测性修补），`T8` 的设置 tab 在未启用态下功能上是"摆设"——填表单点保存会失败。
   - **未碰任何 Go 逻辑文件**（遵守硬约束 8），这是后端 T1/T2 遗留的路由豁免范围问题，建议 parent 评估是否需要补一轮后端改动：把 `/api/ai/config` 的 POST（或 GET+POST）也加入 enabled 豁免名单（类似 toggle/test-connection），使"配置→测试→开启"三步在禁用态下可走通。
   - 本轮未做任何前端"假装能保存"的误导性 UI；引导卡文案（`ai.onboarding_desc`）只说"填写→测试→开启"，没有承诺具体某一步在当前后端下必然成功。

2. **`GET /api/ai/config` 的 `api_key` 字段是掩码值**（`****` + 后 4 位或全 `****`），前端 `loadAIConfig()` 特意**不**把它塞进 `aiConfigForm.api_key`（否则用户点保存会把掩码字符串当成真 key 存回去，破坏 key）。表单里 `api_key` 始终留空待用户重新输入；这意味着"已配置过 key 但不想改"的场景下，每次进设置页都要重新填 key 才能保存——这是刻意的安全取舍（优先避免误覆盖为掩码值），但体验上值得在后续版本加一个"未修改则保留原 key"的后端语义（当前 `handleConfigSave` 对空 `api_key` 没有特殊处理，会直接存成空字符串，需要 parent 确认是否要补）。

3. **`handleStatus` 的 `configured` 字段在前端未被直接使用**：引导卡/入口显隐只认 `aiStatus.enabled`（按计划原文 `x-show="aiStatus.enabled || page==='ai'"`），`configured` 是后端算出来的派生值（`BaseURL+Model 非空`），本轮没有场景需要单独展示它（未启用态引导卡统一展示，不区分"未配置"和"已配置未开启"两种未启用子状态）。如果后续想做更细的引导文案（如"已配置但未测试"vs"完全没填"），需要用上 `configured` 字段，当前实现未使用但也未遗漏契约——只是没有消费它。

4. **`pending_action` SSE 事件的 `created_at` 字段**（`time.Time` 序列化为 RFC3339 字符串）前端收到后未使用（只读信息卡只展示 `summary`），不影响功能，提前记录以免被认为遗漏。

## 五、偏离与疑虑

1. **组头部常驻小入口的交互设计是本轮自行决定**：计划原文只说"组头部加「AI 助手」常驻小入口"，没有给出具体视觉/交互细节。实现为工具箱组标题行右侧追加一个 🤖 图标按钮（`@click.stop` 避免触发组展开/折叠），点击直接 `navigate('ai')`。未使用额外 CSS class（复用现有 inline style 模式），没有引入新组件。
2. **`testAIConnection()` 内部先调用 `saveAIConfig()` 再测试**：计划原文"连通性测试按钮（转圈+toast）"没明确规定测试前是否要先保存。由于后端 `test-connection` 只认已保存配置，若不先保存，用户点"测试"测的是旧配置而非表单当前值，体验上会误导（改了端点但点测试却测旧值通过）。因此前端做了"测试=保存+测试"的组合动作，这是为贴合后端真实行为做的合理推断，但与计划"测试"和"保存"是两个独立按钮的字面描述有出入——测试按钮实际上会连带触发保存。已在代码注释里写明原因；如果 parent 认为这个耦合不该默认发生，需要明确反馈再拆开（拆开的代价是：未启用态下测试按钮会先触发一次 403 保存失败 toast，体验更差）。
3. **会话历史回放过滤 `role=tool` 消息**：`LoadConversation` 落库结构里 `SaveMessage` 目前（读 `chat.go`）每轮只存 user+assistant 两条消息（`role=tool` 的工具回填消息是 agent loop 内部多轮往返的产物，没有单独落库），所以这个过滤目前是防御性的（哪怕以后 store.go 改成落库 role=tool 也不会破坏前端），不是臆测契约——按当前代码读到的实际落库内容，这个过滤目前是空操作，但保留以防未来扩展。
4. **工具轨迹折叠条目在历史回放时为空数组**：`aiMessages`（来自 `loadAIConversation`）的 `toolTrace` 固定给 `[]`，因为 `StoredMessage` 落库结构里没有保存工具调用轨迹的 digest（只有 `tool_calls` 列，存的是 `[]ToolCall` 的参数，不是执行后的 digest）。只有当前这一轮通过 SSE 实时收到的消息才有 `toolTrace` 内容；刷新页面重新加载历史后，旧消息的工具调用轨迹UI会"消失"（内容仍在，只是折叠条目不再显示）。这是当前后端落库粒度决定的限制，不是前端遗漏。

## 六、验证记录

- `python3 scripts/check-i18n.py` — 两次提交后均通过。
- `cd web && go build ./...` — 通过（embed 重编译，两次提交后均重新验证）。
- `cd web && go vet ./...` — 通过。
- `cd web && go test ./... -count=1` — 全绿（22 个有测试的包全部 ok，含 `modules/ai`）。
- `node --check web/frontend/app.js` — 语法检查通过。
- HTML 新增块手工核对 `<div>`/`<template>` 标签配对（28/28、8/8），无悬挂标签。
- 浏览器实机测试**不在本轮范围**（按 brief 要求，R5 后 parent 统一做）。

## 七、未做事项（按 brief 明确排除）

- 未实现 `pending_action` 的确认交互（type-to-confirm），仅渲染只读信息卡 + `ai.pending_hint` 占位文案，确认门逻辑在 R5（对应计划 Task 10）。
- 未触碰任何 `web/modules/ai/*.go` 或其他 Go 逻辑文件。
- 未处理 `.gitignore` 的 `tmp/` 改动（非本轮产物，未纳入提交）。
- 未 `git add` 本 brief（TASK-AI-R4.md）与本报告文件。
