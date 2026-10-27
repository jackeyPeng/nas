# OPENCODE-REPORT-AI-KEYFIX

## 根因回顾
1. `handleConfigSave`（web/modules/ai/ai.go）无条件 `APIKey: req.APIKey`，前端保存成功后清空表单 / 刷新页回显掩码串，再次 POST 会把真 key 覆盖成空值或掩码字面量。
2. 模块内所有手写错误响应走 `http.Error`，固定 `Content-Type: text/plain`，前端 `api()` 只对 `application/json` 的错误体自动 toast；`testAIConnection` 对失败响应又没有 else 分支 → 测试失败时完全静默。

## 改动内容

### 后端 key 保留语义（web/modules/ai/ai.go `handleConfigSave`）
```go
if next.APIKey == "" || next.APIKey == MaskAPIKey(prev.APIKey) {
    next.APIKey = prev.APIKey
}
```
空值、以及等于当前配置掩码串（`****`+后4位）的回传值，一律视为"未修改"，保留 `prev.APIKey`。只有非空且不等于当前掩码的值才会真正更新 key。Verified 清除逻辑保持原样：`BaseURL`/`APIKey`（含上面保留后的值）/`Model` 三者都未变才保留 `Verified`，否则清为 `false`——语义上现在是"key 真的变了才清"，而不是"key 字段被回传了就清"。

### jsonError helper（web/modules/ai/ai.go）
```go
func jsonError(w http.ResponseWriter, msg string, code int) {
    w.Header().Set("Content-Type", "application/json; charset=utf-8")
    w.WriteHeader(code)
    body, err := json.Marshal(map[string]string{"error": msg})
    if err != nil {
        body = []byte(`{"error":"内部错误"}`)
    }
    w.Write(body)
}
```
体用 `json.Marshal` 生成，动态内容（上游 LLM 错误原文、`err.Error()`）自动转义，不再手拼 `%q`/字符串拼接。

### 替换的 http.Error 处数
**34 处全部替换**，按文件：
- `web/modules/ai/ai.go`：7 处（handleConfig method-not-allowed、handleConfigSave 请求体非法/保存失败、handleToggle method-not-allowed/请求体非法/未验证拒绝/保存失败）
- `web/modules/ai/llm.go`：4 处（handleTestConnection method-not-allowed、缺端点/模型、**TestConnection 失败——原 `fmt.Sprintf("{\"error\":%q}", err.Error())` 改为 `jsonError(w, err.Error(), ...)`**、保存失败）
- `web/modules/ai/chat.go`：14 处（handleChat 6 处，其中 **RunAgent 失败分支原 `` `{"error":"模型调用失败: `+jsonEscape(err.Error())+`"}` `` 改为 `jsonError(w, "模型调用失败: "+err.Error(), ...)`**，handleListConversations 2 处，handleGetConversation 3 处）
- `web/modules/ai/actions.go`：9 处（handleActionConfirm 5 处，其中 **执行失败原 `fmt.Sprintf` 改为 `jsonError(w, err.Error(), ...)`**；handleActionCancel 3 处）

状态码与错误文案逐字保持不变，只换了 helper 和 Content-Type。`chat.go` 里的 `jsonEscape`（手写转义 helper）仍保留，因为它还用在审计 detail 字符串拼接（`"error="+jsonEscape(err.Error())`），不是 HTTP 响应体，和本次改动范围无关。

`http.Error` 在 `modules/ai` 下已无实际调用（grep 复核：唯一命中是 ai.go 里的注释说明文字）。

SSE 分支（`handleChatSSE` 的 `sendSSEEvent`）本身走 `data: <json>\n\n` 协议而非 `http.Error`，不在 34 处范围内，未改动。

### 新增测试清单（web/modules/ai/ai_test.go）
- `TestConfigSaveEmptyAPIKeyPreservesRealKey`：存真 key → POST 空 `api_key`（base_url/model 不变）→ `LoadConfig().APIKey` 仍是原 key，`Verified` 未被清
- `TestConfigSaveMaskedAPIKeyPreservesRealKey`：存真 key → POST `MaskAPIKey(真key)` 回显值 → key 保留，`Verified` 未被清
- `TestConfigSaveNewRealKeyUpdatesAndResetsVerified`：POST 一个新的真 key → key 更新为新值，`Verified` 被清为 `false`
- `TestJSONErrorSetsJSONContentType`：直调 `jsonError`，断言状态码、`Content-Type` 前缀 `application/json`、响应体是合法 JSON 且含非空 `error` 字段（含引号/换行/反斜杠的 msg 验证转义不出格式错误）
- `TestTestConnectionFailureUsesJSONContentType`：端到端走 `POST /api/ai/test-connection`（故意配一个必然连接失败的端点），断言真实 HTTP 响应（非单测 helper 层面）是 502 + JSON content-type + 合法 JSON body

既有测试（`config_test.go`/`ai_test.go` 原有用例、`llm_test.go`/`chat_test.go`/`chat_stream_test.go`/`actions_test.go`/`agent_test.go`/`security_test.go` 等）未发现断言 `text/plain` 的用例，均原样保留，全部通过。

## 验证

```
cd web && go build ./...
```
无输出，成功。

```
cd web && go vet ./modules/ai/
```
exit 0，无警告。

```
cd web && go test ./modules/ai/ -count=1
```
`ok  	nas-panel/modules/ai	5.725s`（91 个测试全 PASS，0 FAIL）。

补充跑了全仓库 `go test ./...`，全部模块绿（`nas-panel`、`common`、`modules/*` 共 18 个有测试的包全 ok，3 个无测试文件的包按预期跳过）。

```
python3 scripts/check-i18n.py
```
`✅ 所有语言包 key 一致！`——本次未新增 i18n 键，前端改动复用现有 toast 机制和后端直出的中文错误串。

## 前端改动（web/frontend/app.js）
`testAIConnection` 按 brief 给定代码块原样补上 else 分支（text/plane 兜底展示 `data.error` 或原始字符串），`saveAIConfig` 未改动。改动后已 `go build ./...`（embed 进二进制）验证通过。

## 偏离
无实质偏离。唯一需要说明的点：brief 里提到"两处 `fmt.Sprintf(`{"error":%q}`,...)`"，实际 grep 命中 3 处同类手拼动态错误体（llm.go 的 TestConnection 失败、chat.go 的 RunAgent 失败、actions.go 的 executeConfirmedAction 失败），三处均已同等处理为 `jsonError(w, err.Error()/拼接串, code)`，不影响总数（34 处 `http.Error` 调用点的统计含这 3 处）。
