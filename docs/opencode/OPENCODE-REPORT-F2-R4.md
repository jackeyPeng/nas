# OPENCODE-REPORT-F2-R4：前端通知中心（F2 Task 6）

## 范围确认

只改了 `web/frontend/app.js`、`web/frontend/index.html`、`web/frontend/i18n/zh-CN.json`、`web/frontend/i18n/en-US.json` 四个文件。没有碰任何 `web/*.go`、`web/modules/**/*.go`、`web/common/*.go`。消费的全部是 R1-R5 已落地的 REST：
- `GET /api/notifications/unread-count`
- `GET /api/notifications?limit=`
- `POST /api/notifications/{id}/read`
- `POST /api/notifications/read-all`
- `POST /api/notifications/clear`
- `GET /api/triggers/suggestions`
- `POST /api/triggers/suggestions/{id}/dismiss`
- `POST /api/triggers/suggestions/{id}/accept`
- `POST /api/triggers/report/generate`
- `POST /api/ai/action/confirm`（既有，零新确认语义）

## 新增 i18n 键数

`notif.*` 下新增 **26 个键**（zh-CN/en-US 各 26，check-i18n.py 校验通过）：

```
bell_label, panel_title, suggestions_title, report_section_title, list_title,
empty_suggestions, empty_notifications, empty_report,
btn_execute, btn_dismiss, btn_navigate, btn_mark_all_read, btn_clear_read, btn_regenerate,
report_regen_ok,
report_storage_label, report_storage_fmt,
report_tasks_label, report_tasks_fmt,
report_health_label, report_health_fmt,
confirm_title, confirm_word_label, confirm_execute, cancel
```

`trig.*`/`notif.task_done_*`/`notif.health_*`/`notif.suggestion_*`/`notif.report_title`/`notif.report_body` 等后端产的 i18n 键在 R1-R3 已存在，本轮零修改，直接消费。

## app.js 新增函数/状态清单

**状态：**
- `notifPanel`：`{ open, unreadCount, notifications, suggestions, report, loading }`
- `notifPollTimer`：铃铛 30s 轮询句柄
- `notifConfirm`：`{ show, action, confirmWordInput, submitting }`（独立于 `aiConfirm`）

**Getter：**
- `notificationsVisible`：`!!aiStatus.enabled`，驱动铃铛入口 `x-show`

**轮询生命周期：**
- `syncNotifPolling()` / `startNotifPolling()` / `stopNotifPolling()` / `loadNotifUnreadCount()`

**面板数据与交互：**
- `toggleNotifPanel()` / `closeNotifPanel()` / `loadNotifPanelData()`
- `notifParseData(jsonStr)`：JSON.parse 容错包装
- `notifTitle(item)` / `notifBody(item)`：t() 插值渲染包装
- `notifSuggestionAction(sg)`：解析 `actions_json[0]`
- `notifDismissSuggestion(id)` / `notifSuggestionNavigate(sg)` / `notifAcceptSuggestion(sg)`
- `notifReportData(report)`

**通知列表：**
- `notifClickNotification(n)` / `notifMarkRead(id)` / `notifMarkAllRead()` / `notifClearRead()`

**周报：**
- `notifGenerateReport()`

**接受确认弹窗：**
- `closeNotifConfirm()` / `notifConfirmDisabled()` / `confirmNotifAction()`

## t() 插值实现与既有调用面兼容说明

`window.t`/`i18n.js` 的 `translateWith` **已经**支持命名对象参数 `{pct}` 风格插值（`for (const [k,v] of Object.entries(params)) val = val.split('{'+k+'}').join(v)`），和既有的数组索引 `{0}` 插值并存，互不冲突——这是 R1-R3 遗留的基建，不是本轮新写的。本轮没有改 `i18n.js` 一行代码，只是在 `app.js` 新函数里按对象形式调用 `this.t(key, {pct: ..., used: ...})`。

兼容性自测：`grep -oE "\$t\('[^']+'(, ?\[[^]]*\])?\)" index.html` 统计全部 833 个唯一 key 调用点，抽查确认 `ai.confirm_word_label`（数组参数）、`ai.task_impact`（数组参数）等既有调用点字面量未改动；`window.t`/`$t` 函数签名未动，无参调用（占绝大多数）行为不变。

坏 JSON 容错：`notifParseData` 对 `data_json`/`params_json`/`actions_json`（经 `notifSuggestionAction`）解析失败统一 catch 返回 `{}`/`{}`（类型等价空对象），`t(key, {})` 时所有 `{xxx}` 占位符找不到对应 key，`val.split('{'+k+'}')` 循环体压根不执行，原始 `{xxx}` 字面量原样显示——不是最优雅的用户提示，但符合"坏 JSON 不炸渲染，显示原文"的字面要求，且不需要改 i18n.js。键缺失（如拼错 key）由 `i18n.js` 已有逻辑兜底，返回键名最后一段，不额外处理。

## 轮询清理点清单

对照任务 tab 的 `taskJobsTimer` 模式：

| 场景 | 清理动作 |
|---|---|
| `init()`：AI 未启用 | 不启动（`syncNotifPolling` 内判断） |
| `visibilitychange` → `document.hidden=true` | `stopNotifPolling()` |
| `visibilitychange` → 恢复可见 | `syncNotifPolling()`（立即补一次 unread-count + 重启定时器） |
| `toggleAI()` 关断 | `syncNotifPolling()` → 内部 `stopNotifPolling()` + `notifPanel.open=false` |
| `logout()` | `stopNotifPolling()` |
| `beforeunload` | `stopNotifPolling()`（与 `stopTaskJobsPolling()` 并列） |
| 定时器回调内部 | 每次 tick 二次校验 `document.hidden`，双重保险 |

轮询本身是全局铃铛（不挂在某个 `page`），不随 `navigate()` 启停——与任务 tab「只在 ai 页跑」的语义不同，这里故意不对齐，因为通知中心要在任意页面显示未读数。

## 与后端响应形状的差异发现

逐字核对后没有发现响应形状偏差，记录两处需要前端特别处理的地方（不是 bug，是消费侧必须的适配）：

1. **周报不是独立端点返回的对象，是 `notifications` 列表里 `kind=report` 的最新一条**，`data_json` 字段里才是 `WeeklyReport` JSON（`storage.total_gb/used_gb/use_percent`、`tasks.done/failed/moved_files/freed_bytes`、`health.pass/warn/fail`）。`notif_storage_fmt` 用 `formatSize` 时把 GB 转回字节（`* 1024³`）喂给既有 `formatSize(bytes)`，没有重写格式化逻辑（硬约束 10）。
2. **`notif.suggestion` 事件通知** 的 `data_json` 只有 `{suggestion_id, trigger_key}`（EventBus Params），不含建议卡本身的 title/body——点击这类通知目前只做"标记已读"，不跳转到具体建议卡（建议卡本身已经在面板的"管家建议"区单独展示），与计划 Task 6 第 2 条"按 kind 跳转：task→任务 tab；health→健康页"的字面描述一致（suggestion 没写跳转目标，我没臆造）。
3. health 通知的 `kind` 字段实际落的是 `"system"`（`kindForEvent` 对 health.* 走 default 分支），不是直觉上的 `"health"`。前端 `notifClickNotification` 改用 `title` 前缀 `notif.health_` 判断是否跳健康页，而不是判断 `kind==='health'`（这个 kind 值在后端代码里从未出现过）。

## UI 实测说明

**没有自测通过浏览器**：当前环境没有可访问的 Chrome/浏览器自动化工具，也没有起本机服务器连调真实 AI 开启态（AI 开启需要配置真实 DeepSeek/Ollama 端点）。已完成的自测：

- `node -e "new Function(fs.readFileSync('app.js'))"` 确认 app.js 语法合法
- 手动逐行核对新增 HTML 块的开闭标签配对（sidebar-header 铃铛块、notifConfirm 弹窗块）
- `python3 scripts/check-i18n.py` 通过
- `cd web && go build ./... && go test ./... -count=1` 全绿，无回归（见下）

UI 实际渲染效果（弹窗位置、下拉面板遮挡、移动端铃铛可见性等）需要 parent 在本机 Chrome 验收轮里确认。

## 验证结果

```
$ python3 scripts/check-i18n.py
📖 zh-CN.json: 1396 keys
   en-US.json: 1396 keys
✅ 所有语言包 key 一致！

$ cd web && go build ./...
(no output, 构建成功)

$ cd web && go vet ./...
(no output)

$ cd web && go test ./... -count=1
ok  	nas-panel	0.160s
ok  	nas-panel/common	1.946s
ok  	nas-panel/modules/ai	8.668s
ok  	nas-panel/modules/backup	0.121s
...（全部 ok，含 notifications/triggers，无回归）
```

## 偏离与疑虑

1. **铃铛入口位置**：计划写"sidebar-header 或顶栏挂铃铛"。本仓库桌面布局没有顶栏（`main.content` 紧贴 sidebar），选了 `sidebar-header`（logo 右侧），移动端 `mobile-bar` 没加铃铛入口——计划交付明细没有明确要求覆盖移动端，且 mobile-bar 结构是硬编码汉堡菜单+标题+一个固定按钮，加铃铛需要额外改动 mobile-bar 布局。如果 parent 验收需要移动端铃铛，需要再补一轮小改动。
2. **通知面板的"周报卡"数据来源**：计划交付明细 2 说"周报卡（kind=report 最新一条）"，已按此实现（从 `GET /api/notifications` 返回列表里过滤取最新一条），没有新增端点，符合"不碰 Go 逻辑"的硬约束。
3. **health 通知跳转判断用 title 前缀而非 kind**：见上节差异发现 3，这是基于代码现实（`kindForEvent` 返回 `"system"`）做的必要适配，不是臆造行为。
4. **suggestion 通知点击不跳转**：见上节差异发现 2，计划原文"按 kind 跳转：task→任务 tab；health→健康页"没提 suggestion 要跳哪，保持不跳转（只标已读），没有画蛇添足加未经授权的交互。
5. **`notifConfirm` 弹窗不挂 `@click.self`**：这是硬约束 8"弹窗不因点击外部关闭"的直接实现——与 `aiConfirm` 弹窗（挂 `@click.self="cancelAIAction()"`）故意不同，这是计划明确要求的差异，不是疏漏。
6. 没有新增 Go 依赖、没有引入 CDN/新 JS 库，`alpinejs.min.js`/现有 i18n.js 原样复用。

## 提交

单提交，commit message 用计划 Task 6 原文：
```
feat(ui): 通知中心 — 铃铛+未读徽标/建议卡(执行/忽略/去看看)/周报卡/通知列表+t()插值+接受确认弹窗(复用type-to-confirm) 双语i18n (F2 T6)
```
未 `git add` 本报告文件与 `TASK-F2-R4.md`。
