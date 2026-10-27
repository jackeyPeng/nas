# OPENCODE-REPORT-F1-R4: 前端任务 tab（F1 Task 6）

## 结论
已完成 ai 页第三 tab「任务」+ app.js 状态/方法 + 双语 i18n。`cd web && go build ./...`（embed 重编译）与 `go test ./... -count=1` 全绿，`python3 scripts/check-i18n.py` 通过。单提交 `8a3c221`，只改 4 个前端文件，未碰任何 Go 逻辑文件。

## 新增 i18n 键数
共 **31** 个新键（check-i18n.py 校验 zh-CN/en-US 两文件均为 1347 keys，一致）：

- `ai.*`（19 个）：`tab_tasks`、`task_kind_photo_dedupe`、`task_status_pending_confirm/running/paused/done/failed/cancelled`（6 个）、`task_progress`、`task_impact`、`task_report_title/moved/freed/skipped`（4 个）、`task_empty`、`task_hint_confirm`、`task_pause/resume/cancel`、`task_error_title`
- `msg.*`（4 个）：`task_cancel_confirm`、`task_action_pause_ok`、`task_action_resume_ok`、`task_action_cancel_ok`

计划 Step 3 给的"约 12 个"是最小集估算，实际按六态徽章全展开 + pause/resume/cancel 按钮文案 + confirm/toast 文案落地为 31 个（六态徽章本身就占 6 个键，加上报告三行、按钮三个、confirm/toast 四个，比最小集估算略多，均为计划枚举范围内的必需文案，没有超纲新增功能性文案）。

## app.js 新增函数清单
全部挂在 Alpine 根组件 `methods`/`getters` 里，位置在 AI 域方法块内（`aiActionSummaryKey` 之后、`checkPanelUpdate` 之前）：

| 名称 | 类型 | 作用 |
|---|---|---|
| `loadAITasks()` | async method | GET `/api/tasks` → `taskJobs`；字符串守卫 `!data \|\| typeof data !== 'object' \|\| !Array.isArray(data.jobs)` |
| `taskRunningOrPaused` | getter | 过滤 `taskJobs` 中 status 为 running/paused 的任务 |
| `syncTaskJobsPolling()` | method | 根据 `taskRunningOrPaused` 是否非空决定启停轮询，`loadAITasks()` 每次加载后自动调用校准 |
| `startTaskJobsPolling()` | method | 2s `setInterval`，tick 内先查 `this.page !== 'ai'` 则自停（对照 rclone `startRcloneProgressPolling` 模式） |
| `stopTaskJobsPolling()` | method | `clearInterval` + 置空 `taskJobsTimer` |
| `taskAction(id, action)` | async method | POST `/api/tasks/{id}/{action}`（pause/resume/cancel）；cancel 前 `confirm(this.t('msg.task_cancel_confirm'))`；守卫 `!data.ok`；成功 toast `msg.task_action_<action>_ok` + `loadAITasks()` |
| `taskKindLabel(kind)` | method | `ai.task_kind_<kind>` 查不到兜底原值 |
| `taskStatusLabel(status)` | method | `ai.task_status_<status>` 查不到兜底原值 |
| `taskStatusBadgeStyle(status)` | method | 六态 → 六色 CSS 变量字符串（供 `:style` 绑定） |
| `taskResult(job)` | method | 解析 `job.result` JSON（moved/freed/skipped），解析失败兜底 `null` |

状态新增：`taskJobs: []`、`taskJobsTimer: null`。

`navigate('ai')` case 追加 `this.loadAITasks()`；`navigate(page)` 入口追加 `离开 ai 页 → stopTaskJobsPolling()`；`init()` 的 `beforeunload` 监听里追加 `stopTaskJobsPolling()` 作为兜底清理（本应用是单页无 Alpine 组件销毁生命周期，真正的"组件销毁"等价物是 tick 内的 `this.page !== 'ai'` 自停检查，这与 rclone 既有轮询实现模式完全对齐）。

## 六态徽章样式对照表

| status | i18n key | 色语义 | `:style` 值 |
|---|---|---|---|
| pending_confirm | task_status_pending_confirm | 黄 | `background: var(--warn-light);color: var(--warn)` |
| running | task_status_running | 蓝 | `background: var(--primary-light);color: var(--primary)` |
| paused | task_status_paused | 灰 | `background: var(--card-bg-alt);color: var(--text-secondary)` |
| done | task_status_done | 绿 | `background: var(--success-light);color: var(--success)` |
| failed | task_status_failed | 红 | `background: var(--danger-light);color: var(--danger)` |
| cancelled | task_status_cancelled | 灰 | `background: var(--card-bg-alt);color: var(--text-muted)` |

全部复用既有 CSS 变量（`style.css` 里已定义，未新增任何颜色常量），徽章容器类用 `.badge-xs`，`:style` 只写 color/background，不写布局属性（符合硬约束 5）。进度条复用 `.progress-bar`/`.progress-fill`，`:style="'width:'+pct+'%'"` 与 rclone 任务区块的既有写法一致。

## 轮询清理点清单
1. `startTaskJobsPolling()` tick 内：`if (this.page !== 'ai') { stopTaskJobsPolling(); return; }` — 离开 ai 页的下一轮 tick 自停。
2. `navigate(page)`：离开 'ai' 页时立即同步调用 `stopTaskJobsPolling()`（不等下一轮 tick，比 rclone 模式更及时——rclone 原模式只在 tick 内检查，这里额外加了 navigate 出口即时清理，属于更保守的实现，无副作用）。
3. `window.addEventListener('beforeunload', …)`：页面整体卸载前兜底清理（与 vault 锁定清理同一个监听器内追加）。
4. `loadAITasks()` 每次调用末尾 `syncTaskJobsPolling()`：任务全部进终态后自动 `stopTaskJobsPolling()`，不需要等待下一轮才发现"没有活跃任务了"。

## 与 REST 响应形状的差异发现
逐字核对 `web/modules/tasks/api.go` 和 `web/modules/ai/tools_tasks.go` 后，未发现偏离计划文档的地方：
- `GET /api/tasks` → `{"jobs":[...]}`，`Job` 字段（`id/kind/status/scope_rel/plan/impact/progress_cur/progress_total/progress_note/result/error/created_at/started_at/finished_at`）与 Task 1 接口定义逐字一致，前端只消费 `kind/status/impact.groups/impact.bytes/progress_cur/progress_total/progress_note/result/error`，未用到 `plan`/`scope_rel`/时间戳字段（UI 不需要）。
- `POST /api/tasks/{id}/pause|resume|cancel` → `{"ok":true}`，错误时 `jsonError` 返回 `{"error":"..."}` 且非 2xx —— `app.api()` 对非 2xx JSON 响应里有 `error` 字段的情况已经统一 toast 并返回 `null`（见 app.js 553 行 `api()` 实现），所以 `taskAction` 里的 `!data.ok` 守卫主要兜底"2xx 但形状不对"的边界情况，和 api() 已有的错误 toast 不重复触发（api() 返回 null 时 `taskAction` 的守卫分支里 `typeof data === 'string'` 为假，直接静默 return，不会二次 toast——这是既有 `openPermsDialog` 守卫同款写法的直接复用）。
- `Result` 字段是字符串化 JSON（`"result":"{\"moved\":N,...}"`），不是嵌套对象，前端 `taskResult(job)` 做了 `JSON.parse` 容错处理，与 Task 4 实现（`kinds_dedupe.go` 里 `FinishJob(resultJSON, ...)`）吻合。

## 偏离与疑虑
- **徽章色语义顺序**：计划 Step 2 原文把 pending_confirm 标"黄"、running 标"蓝+进度条"，本实现 running 的进度条渲染条件是 `status === 'running' || status === 'paused'`（paused 也显示进度条，只是不再推进），因为 paused 任务的 `progress_cur/total` 仍是有意义的中断点数据，隐藏掉反而丢信息；计划原文没有明确禁止 paused 显示进度条，判断为合理扩展，非偏离核心约束。
- **cancel 按钮显隐范围**：按钮显隐表只写了 running→暂停+取消、paused→继续+取消，未提及 pending_confirm/done/failed/cancelled 态的按钮——这些态本身不显示任何操作按钮（pending_confirm 只显示只读提示文案，done/failed/cancelled 是终态，无需要操作按钮），与计划"按钮按状态显隐"的意图一致。
- **聊天联动**：按 brief 要求，start_task/plan_task 的确认卡与工具轨迹条目复用既有机制（`m.pendingAction`/`m.toolTrace` 渲染块，见 index.html 2817-2832 行），本轮未新增任何组件，只做了代码层核对（读了 chat.go 的 pendingActionSummary 分支已在 Task 5 落地），未做浏览器交互验证——按 brief 第 4 点要求，此项验证留给 R5 之后 parent 用本机 Chrome 统一实测。
- **浏览器实测**：本轮未做（brief 明确说明不在本轮范围），只验证了 `go build`/`go test`/`check-i18n.py` 三项。

## 验证记录
```
python3 scripts/check-i18n.py   → ✅ 所有语言包 key 一致（zh-CN/en-US 均 1347 keys）
cd web && go build ./...        → 无输出（成功，embed 已重打包前端资源）
cd web && go vet ./...          → 无输出
cd web && go test ./... -count=1 → 全部 ok（ai/tasks/filemgmt/... 无回归）
```
