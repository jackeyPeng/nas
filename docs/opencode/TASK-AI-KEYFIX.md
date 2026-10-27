# TASK: AI 配置保存 key 覆盖 bug + 错误响应 content-type + 测试按钮静默失败

仓库：/home/jacky/soft/nas（分支 master）。Go module 在 web/。

## 背景（用户实报）
57 面板 AI 设置页：保存 DeepSeek 配置成功后点「测试连通性」毫无反应。根因两个：
1. **saveAIConfig 成功后前端清空 `aiConfigForm.api_key`；testAIConnection 先保存再测试** → POST /api/ai/config 带空 api_key → 后端 handleConfigSave 无条件 `APIKey: req.APIKey` 把已存真 key 覆盖成空 → test-connection 用空 key 打 DeepSeek 必 401。刷新页面回显掩码值（`****`+后4位）再保存同理会把掩码串存成真 key。
2. 测试失败时后端 `http.Error`（text/plain）回 JSON 形状错误体 → 前端 api() 只对 application/json 的错误自动 toast，text/plain 原样返回字符串 → testAIConnection 的 `data.ok` 守卫不真且无 else 分支 → 完全静默。

## 修改内容
### A. web/modules/ai 后端
1. **handleConfigSave（ai.go）key 保留语义**：`req.APIKey == "" || req.APIKey == MaskAPIKey(prev.APIKey)` 时 `next.APIKey = prev.APIKey`（空值/掩码回显一律视为「未修改」）；只有非空且不等于当前掩码的值才更新 key。Verified 保持既有逻辑（三字段都没变才保留）。
2. **新增模块内 helper** `jsonError(w http.ResponseWriter, msg string, code int)`：设 `Content-Type: application/json; charset=utf-8` + WriteHeader(code) + 写 `{"error":<msg json转义>}`。把 modules/ai 下**全部 34 处** `http.Error(...)`（actions.go/ai.go/chat.go/llm.go 等，grep 确认）替换为 jsonError，状态码与错误文案逐字不变。msg 含动态内容的（fmt.Sprintf(`{"error":%q}`,...) 两处）改为 jsonError(w, err.Error(), code) 之类，转义交给 helper（用 json.Marshal 生成体，别再手拼 %q）。
3. **单测**（config_test.go 或 ai_test.go 追加）：
   - 存真 key → 再 POST 空 api_key → LoadConfig 的 key 仍是真 key，且 Verified 未被清（base_url/model 也没变时）
   - 存真 key → POST 掩码回显值（MaskAPIKey(真key)）→ key 保留
   - POST 新真 key → key 更新且 Verified 重置为 false
   - jsonError 的响应 Content-Type 断言 application/json、体是合法 JSON
   - 既有测试全部保持通过（若有断言 text/plain 的用例同步改）
### B. web/frontend/app.js
testAIConnection 补失败分支（对齐 pitfall 2c 守卫模式）：
```js
const data = await this.api('/ai/test-connection', { method: 'POST' });
if (data && typeof data === 'object' && data.ok) {
    this.showToast(this.t('ai.test_ok'), 'success');
    await this.loadAIStatus();
} else if (typeof data === 'string' && data.trim()) {
    let msg = data;
    try { const j = JSON.parse(data); if (j && j.error) msg = j.error; } catch (e) {}
    this.showToast(msg, 'error');
}
```
（后端改 JSON content-type 后 api() 会自动 toast 并返回 null，此分支是 text/plain 兜底，不会双弹。）
不动 saveAIConfig 的成功清空逻辑（后端已把空值视为未修改）。

## 硬约束
1. 禁止读取/执行仓库外文件。
2. 不新增 i18n 键（错误文案都是后端中文串直出）；如动了 i18n 文件必须 python3 scripts/check-i18n.py 通过。
3. 前端 embed 进二进制：改完必须 `cd web && go build ./...` 成功。
4. 全部测试绿：`cd web && go test ./modules/ai/ -count=1` + `go vet ./modules/ai/`。
5. 单次提交，信息：`fix(ai): 配置保存空值/掩码回显不再覆盖真key + 错误响应改JSON content-type + 测试连通性失败不再静默(用户实报:保存成功后点测试无反应)`。不 git add 本 brief。

## 报告
写 /home/jacky/soft/nas/OPENCODE-REPORT-AI-KEYFIX.md：替换的 http.Error 处数、新增测试清单、go test 输出摘要、任何偏离。
