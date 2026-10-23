# 家庭管家底座 F2 实施计划（主动管家：triggers + notifications + 通知中心 + 周报）

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development / executing-plans，按 Task 逐个执行，checkbox 跟踪。

**Goal:** 面板从「被动等命令」升级为「主动发现、提出建议、经确认后干活」：事件/水位触发建议卡，通知中心统一收件，接受建议走既有确认门转任务执行，每周自动生成家庭周报。

**Architecture:** 新增 `web/modules/notifications/`（轻量收件箱，eventbus 订阅式叶子模块，零上游依赖）与 `web/modules/triggers/`（建议卡生成/冷却/上限/cron/周报）。依赖单向：`triggers → tasks + ai + diskmgmt + notifications`（建议接受转 pending action / 任务计划；周报读存储统计并落收件箱）；`notifications` 只 import `common`——其余生产方（tasks/health…）一律经 eventbus 解耦，不 import notifications。接受链复用 ai 确认门（新增 `ai.CreatePendingAction` 导出）与既有 `POST /api/ai/action/confirm`，前端复用 aiConfirm 弹窗模式。

**Tech Stack:** Go stdlib + 既有 common 基建（OpenSQLite WAL / eventbus / AuthMiddleware / LogAuditRequest）；cron 复用 rclone 调度器的 5 段匹配器（抽到 common 共享）；前端 Alpine + 既有 i18n。新增 Go 依赖 0。

**Spec:** docs/plans/2026-10-22-agent-foundation-design.md §五（主动管家）、§三/三b（架构与边界）、§七 F2、§八验收口径。

## Global Constraints（每个 Task 隐含遵守）

- 硬约束继承 spec §二全部 + v4 红线 9/10：建议与任务计划**永远只是建议**，执行必过确认门；同 trigger_key 冷却 24h、活跃建议上限 5 条——管家不许变骚扰。
- 零 CGO 单二进制；新增 Go 依赖 0；`notifications`/`triggers` 各自独立 sqlite 文件（notifications.db / triggers.db，common.OpenSQLite WAL，与 tasks.db 同 dataDir 模式）。
- 架构边界测试同步更新：notifications 允许 import=common；triggers 允许=common/tasks/ai/diskmgmt/notifications；**禁止** tasks/notifications/ai import triggers；禁止 notifications import tasks/ai/triggers。违规即红。
- 关断零足迹：两模块 REST 全部走 `RegisterRoutes(mux, ai.IsEnabled)` enabledGuard（关断 403）；后台 goroutine（eventbus 订阅循环、cron ticker）常驻但**每次动作前查 guard**，关断时不产生任何建议/通知/周报（对齐 tasks worker 的 SetEnabledGuard 模式，main.go 同款接线）。
- 写操作审计：accept/dismiss/report-generate/clear 走 common.LogAuditRequest 既有模式。
- 错误响应一律 JSON（对齐 ai jsonError / tasks 模式）；参数化 SQL，无字符串拼接。
- 人话层：notifications 的 title/body **存 i18n 键 + 参数**（data_json 带插值参数），前端 t() 扩展 {param} 插值渲染——双语与「家庭小白可懂」都在键值文案里解决，服务端不产自然语言。术语翻译表第一批键见 Task 3。
- TDD：每 Task 先红后绿；提交信息用各 Task 给定原文；`go test ./... -race` 常绿；不 git add 任务书/报告文件。
- 验证只用仓库内 go build/vet/test + scripts/check-i18n.py；禁止读取/执行仓库外文件。

## 代码现实锚点（写码前先读，以实际为准）

- `common/eventbus.go`：`EmitEvent(source, typ, code string, params map[string]interface{}, detail string)` / `Subscribe() chan Event` / `Unsubscribe(ch)`；Event 字段自查。
- 既有事件生产方（Task 3 订阅清单的基础，写码时 `grep -rn 'EmitEvent(' modules/` 重新盘点）：`health.<checkID>`（**只在状态迁移时发**，health.go:102 附近）、`disk.replaced`、`rclone.sync_failed`/`rclone.sync_ok`、`backup.data_done`（**失败事件目前缺失**，见 Task 3）、`storage.*`、`recycle.*`、tasks 的 `task.progress/done/failed`（worker.go emitTaskEvent，params 带 job_id/kind）。
- `modules/tasks`：导出面 `PlanPhotoDedupe(scopeRel string, budget time.Duration) (Job, bool, error)`（返回 job=pending_confirm，bool=partial）、`StartJob(id)`、`GetJob`、`ListJobs(limit)`、`AddCounters/LoadCounters`、`RegisterRoutes(mux, enabledGuard)`、`SetEnabledGuard(fn)`（main.go:127-134 接线模式照抄）。
- `modules/ai/actions.go`：`PendingAction{ID,ToolName,ArgsJSON,Summary,ConfirmWord,CreatedAt}`、`defaultPendingStore`（进程级单例）、`pendingActionConfirmWord = "执行"`、TTL 5 分钟；确认端点 `POST /api/ai/action/confirm {action_id, confirm_word}` → `executeConfirmedAction` 按 ToolName 分发（start_task 已注册，triggers 造的卡直接可用）。
- `modules/rclone/scheduler.go`：`cronMatch(expr, t)` 5 段匹配器（Task 2 抽到 common）+ 30s ticker 扫描模式。
- `modules/diskmgmt`：池状态内核（handlePoolStatus 背后的统计函数）——周报/水位需要的用量数据源；无导出面则按 filemgmt export.go 模式新增。
- 前端：`aiConfirm` 弹窗对象（app.js:354 附近：show/action/confirmWordInput/submitting）、任务 tab（aiTab='tasks'）、`.badge-*` 密度类、`.empty-state` 组件、`modal/modal-box` 弹窗类；sidebar-header（index.html:85）。

---

### Task 1: notifications 模块（收件箱内核 + REST + eventbus 订阅）

**Files:**
- Create: `web/modules/notifications/notifications.go`（表 + 内核 + Init + RegisterRoutes）
- Create: `web/modules/notifications/subscriber.go`（eventbus 订阅循环）
- Create: `web/modules/notifications/notifications_test.go`
- Modify: `web/main.go`（Init + RegisterRoutes 接线，照 tasks 模式）
- Modify: `web/arch_boundary_test.go`（新模块条目：notifications 只准 common；任何模块不准 import notifications——triggers 例外见 Task 4 拍板）

**Interfaces:**
- Produces（后续 Task 与前端依赖，签名逐字）:
  - `type Notification struct { ID, Kind, Title, Body, DataJSON string; Read bool; CreatedAt string }`（Kind ∈ task/suggestion/report/system）
  - `func InitNotifications(dataDir string) error`
  - `func AddNotification(kind, title, body, dataJSON string) (string, error)`（title/body 存 i18n 键）
  - `func ListNotifications(limit int, unreadOnly bool) []Notification`
  - `func MarkRead(id string) error` / `func MarkAllRead() error` / `func ClearRead() (int, error)` / `func UnreadCount() int`
  - `func StartSubscriber()` / `func SetEnabledGuard(fn func() bool)` / `func RegisterRoutes(mux *http.ServeMux, enabledGuard func() bool)`
- REST: `GET /api/notifications?limit=&unread=1`、`GET /api/notifications/unread-count`、`POST /api/notifications/{id}/read`、`POST /api/notifications/read-all`、`POST /api/notifications/clear`（清已读）。
- Consumes: eventbus 事件（订阅 code ∈ `task.done`/`task.failed`/`health.*`/`rclone.sync_failed`/`suggestion.created`/`report.generated`；`task.progress` **不**入收件箱防刷屏）。事件→通知映射（i18n 键）：task.done→`notif.task_done`（params 已带 job_id/kind，明细留给前端跳转任务页）、task.failed→`notif.task_failed`、health.*→`notif.health_warn`/`notif.health_error`（参数 check_id）、rclone.sync_failed→`notif.sync_failed`、suggestion.created→`notif.suggestion`（参数 suggestion_id）、report.generated→`notif.report`。

**测试清单（最小集，先红后绿）:**
1. AddNotification→List 返回、unread 过滤、MarkRead/MarkAllRead/ClearRead 语义、UnreadCount 准确
2. limit 截断按 created_at 倒序
3. 订阅者：EmitEvent("tasks",…,"task.done",params,…) → 轮询（≤2s）收件箱出现对应 i18n 键通知；task.progress 不出现；enabledGuard=false 时事件被丢弃不入箱
4. REST：无 token 401、guard false 403、正常 200 JSON 形状断言
5. 边界测试红灯自检：临时让 notifications import tasks → 边界测试红 → 还原

**Commit:** `feat(notifications): 轻量收件箱 — notes表/AddNotification/List/MarkRead内核+REST(enabled守卫)+eventbus订阅(task.done/failed,health.*,sync_failed,suggestion.created→i18n键通知) (F2 T1)`

---

### Task 2: triggers 模块骨架（suggestions 表 + 冷却/上限内核 + cron 基建）+ common.CronMatch 抽取

**Files:**
- Create: `web/modules/triggers/triggers.go`（表 + 内核 + Init + RegisterRoutes）
- Create: `web/modules/triggers/loops.go`（eventbus 订阅循环 + cron ticker，Task 3 填规则）
- Create: `web/modules/triggers/triggers_test.go`
- Create: `web/common/cron.go`（从 rclone 迁移 cronMatch）+ `web/common/cron_test.go`
- Modify: `web/modules/rclone/scheduler.go`（改调 common.CronMatch，行为逐字不变）
- Modify: `web/main.go`、`web/arch_boundary_test.go`（triggers 允许 common/tasks/ai/diskmgmt/notifications；禁止反向）

**Interfaces:**
- Produces:
  - `type Suggestion struct { ID, TriggerKey, Title, Body, ActionsJSON string; State string; ExpireAt, CreatedAt string }`（State ∈ new/accepted/dismissed/expired）
  - `func InitTriggers(dataDir string) error`
  - `func CreateSuggestion(triggerKey, title, body, actionsJSON string, ttl time.Duration) (Suggestion, bool, error)`——**冷却/上限在这里守**：同 triggerKey 24h 内已有任意状态记录 → (zero,false,nil)；state=new 的活跃数 ≥5 → (zero,false,nil)；创建成功时 `EmitEvent("triggers", EventInfo, "suggestion.created", {suggestion_id, trigger_key}, "")`（notifications 订阅入收件箱）
  - `func ListSuggestions(includeInactive bool) []Suggestion` / `func GetSuggestion(id) (Suggestion, bool)`
  - `func DismissSuggestion(id string) error`（new→dismissed，其余 409 语义返回 error）
  - `func SetEnabledGuard(fn func() bool)` / `func StartLoops()` / `func RegisterRoutes(mux *http.ServeMux, enabledGuard func() bool)`
  - `common.CronMatch(expr string, t time.Time) bool`（5 段：分 时 日 月 周；`*`、单值、逗号列表、`a-b`、`*/n`——以 rclone 现实现能力为准，迁移不改语义）
- REST: `GET /api/triggers/suggestions?all=1`、`POST /api/triggers/suggestions/{id}/dismiss`。（accept 端点在 Task 5。）
- actions_json schema（**逐字**，Task 5 消费）：`[{"type":"task_plan","kind":"photo_dedupe","scope":""}]` 或 `[{"type":"navigate","page":"health"}]`；未知 type 创建时即拒绝（校验函数 validateActions）。

**测试清单:**
1. common.CronMatch 迁移等价：按 5 段语法 ≥8 例（`* * * * *`/`0 3 * * *`/`*/15`/`1,5`/`1-5`/周日 `0 9 * * 0`）全绿；rclone 包编译过且既有测试不变绿→绿
2. CreateSuggestion 成功→State=new+事件发出（订阅侧收到）；同 key 24h 冷却拒绝（改 created_at 模拟 25h 前记录→允许）；活跃 5 条上限拒绝（第 6 条 false；dismiss 一条后允许）；ttl→expire_at 正确；过期行 List 默认不返回且状态惰性置 expired
3. validateActions：合法两种 type 通过；未知 type/坏 JSON/空数组拒绝
4. Dismiss 语义：new→dismissed 成功；accepted/dismissed 再 dismiss 报错
5. guard=false 时 CreateSuggestion 返回错误（不产生记录、不发事件）
6. REST 401/403/200 形状
7. 边界红灯自检：临时让 notifications import triggers → 红 → 还原

**Commit:** `feat(triggers): 主动管家骨架 — suggestions表/冷却24h/活跃上限5/actions校验/REST + cronMatch抽common共享(rclone行为不变) (F2 T2)`

---

### Task 3: 事件源接线（订阅规则 + 水位 cron + 人话翻译表第一批）

**Files:**
- Create: `web/modules/triggers/rules.go`（事件→建议规则表 + 水位检查）
- Modify: `web/modules/triggers/loops.go`（订阅循环挂规则）
- Modify: `web/modules/backup/*.go`（**生产方补丁**：备份失败路径补 `EmitEvent("backup", common.EventError, "backup.data_failed", …)`，成功路径已有 data_done 不动；先 grep 确认失败路径位置，若已有失败事件则订阅之并不改产方）
- Modify: `web/frontend/i18n/zh-CN.json`、`en-US.json`（人话翻译表第一批键）
- Modify: `web/modules/diskmgmt/`（如池用量统计无导出面：按 filemgmt export.go 模式新增 `PoolUsage() (totalGB, usedGB float64, usePercent int, err error)`——只读，不改既有行为）
- Modify: `web/arch_boundary_test.go`（如新增 triggers→diskmgmt 边）
- Test: `web/modules/triggers/rules_test.go`

**规则表（trigger_key → 条件 → 动作 → 人话键）:**

| trigger_key | 事件源 | 建议条件 | actions_json | title/body i18n 键 |
|---|---|---|---|---|
| `watermark_high` | cron `0 */6 * * *` | 池 use_percent ≥ 85 | task_plan photo_dedupe（scope=""） | `trig.watermark_title` / `trig.watermark_body`（参数 {pct} {free}；zh 示例：「存储空间快满了」/「已用 {pct}%，剩 {free}。我可以帮你找出重复的照片视频，清理前会先给你过目」） |
| `health.raid` | eventbus `health.raid*` 迁移到 warn/error | 迁移即触发 | navigate health | `trig.raid_warn_title`/`trig.raid_warn_body`（zh：「一块硬盘出了问题」/「数据目前还有一份完整副本，但请尽快处理。点开查看哪块盘、怎么办」） |
| `health.disk_smart` | `health.smart*`/`health.disk*` warn/error | 迁移即触发 | navigate health | `trig.smart_title`/`trig.smart_body` |
| `backup_failed` | `backup.data_failed`（或既有失败事件） | 事件即触发 | navigate backup | `trig.backup_failed_title`/`trig.backup_failed_body` |
| `sync_failed` | `rclone.sync_failed` | 事件即触发 | navigate backup | `trig.sync_failed_title`/`trig.sync_failed_body` |

- 事件源实际 code 以 `grep -rn 'EmitEvent(' modules/` 盘点为准（health 的 checkID 命名先读 health.go 注册表再定通配前缀）；盘点结果与上表出入时**以代码现实修正规则表并在报告记录**。
- `task.done` 不在 triggers 建建议（F2 无「下一件事」可荐，F4 扩 kind 后加——spec §五③ 留钩：rules.go 里预留 task.done 分支注释锚点）；task.done 通知由 notifications 直接订阅（Task 1 已做）。
- 水位 cron：ticker 每 30s 扫（照 rclone scheduler 模式），命中 `0 */6 * * *` 且本 6h 窗口未跑过（内存 lastRun 记录）→ PoolUsage → ≥85 → CreateSuggestion（冷却/上限由内核守）。池不存在/未建 → 静默跳过。
- **人话红线**：body 键文案必须「家庭小白可懂 + 给出下一步动作」，禁止裸术语（RAID/SMART/inode 等词必须翻译成人话，术语只在「点开查看」后的专家页出现）；双语同标准。

**测试清单:**
1. 规则单测（注入假事件到内部 handler，不起真循环）：health raid warn → CreateSuggestion 被调且 trigger_key/actions/键名正确；同 key 连发两次，第二次被冷却拒
2. 水位：假 PoolUsage（注入函数变量）84%→不建议、85%→建议、池错误→不建议不 panic
3. cron 窗口去重：同窗口第二次 tick 不重复检查（lastRun 断言）
4. guard=false：事件到达不产生建议
5. backup 失败事件：产方补丁后触发路径 EmitEvent 被调（对照既有 backup 测试模式）
6. check-i18n.py 通过；新键 zh/en 全齐

**Commit:** `feat(triggers): 事件源接线 — health迁移/备份失败/同步失败→人话建议卡 + 磁盘水位6h cron(≥85%荐photo_dedupe) + 人话翻译表第一批(双语) + backup失败事件生产方补点 (F2 T3)`

---

### Task 4: 家庭周报（周日 cron + 手动生成端点 + 模板化数据）

**Files:**
- Create: `web/modules/triggers/report.go`
- Modify: `web/modules/triggers/loops.go`（周日 cron 挂 report）
- Modify: `web/frontend/i18n/*.json`（周报文案键）
- Test: `web/modules/triggers/report_test.go`

**Interfaces:**
- Produces:
  - `func GenerateWeeklyReport() (string, error)`——收集：① 存储（diskmgmt.PoolUsage）② 本周任务（tasks.ListJobs(50) 过滤 finished_at ∈ [7天前,现在]：done/failed 计数、Σ counters moved/freed）③ 健康（health 模块当前汇总——读注册表检查结果内核，无导出面则按 export.go 模式加只读导出 `Summary() (pass, warn, fail int)`）。产出报告 JSON：`{"week_start":…,"week_end":…,"storage":{"total_gb":…,"used_gb":…,"use_percent":…},"tasks":{"done":N,"failed":N,"moved_files":N,"freed_bytes":N},"health":{"pass":N,"warn":N,"fail":N}}`
  - 落地：`notifications.AddNotification("report", "trig.report_title", "trig.report_body", 报告JSON)` + `EmitEvent("triggers", EventInfo, "report.generated", {notification_id}, "")`；返回 notification id。
  - **依赖拍板**：triggers 直接 import notifications（AddNotification 是稳定内核 API；经 eventbus 绕行会把整个报告负载塞进事件参数，得不偿失）。边界测试允许表加 `triggers → notifications` 边；notifications 仍禁止 import 任何业务模块。
  - cron `0 9 * * 0`（周日 09:00）+ ticker 窗口去重（Task 3 同款 lastRun 模式）
- REST: `POST /api/triggers/report/generate`（enabledGuard + 审计；手动生成，E2E 与「重新生成」按钮用）

**测试清单:**
1. 种子数据（假 job 行 finished_at 本周内/外各若干 + 假 PoolUsage 注入）→ GenerateWeeklyReport JSON 字段值精确断言（本周过滤边界：恰好 7 天前时刻）
2. 报告落 notifications（kind=report、data_json 可解析）+ 事件发出
3. guard=false 报错不产生
4. REST：403/200、审计行写入断言（对照 tasks/ai 审计测试模式）
5. cron 表达式 `0 9 * * 0` 用 common.CronMatch 对固定周日/非周日时刻断言

**Commit:** `feat(triggers): 家庭周报 — 周日9点cron+手动生成端点,存储/本周任务/健康三块模板化数据落收件箱(kind=report,零LLM依赖) (F2 T4)`

---

### Task 5: 建议接受链（ai.CreatePendingAction 导出 + accept 端点 + 对抗用例）

**Files:**
- Create: `web/modules/ai/export.go`（若 ai 已有 export.go 则并入）
- Create: `web/modules/triggers/accept.go`
- Test: `web/modules/ai/export_test.go`、`web/modules/triggers/accept_test.go`

**Interfaces:**
- Produces:
  - `ai.CreatePendingAction(toolName, argsJSON, summary string) (id, confirmWord string, err error)`——ID 生成/ConfirmWord/TTL/存储全部复用 chat.go 既有内部路径（抽取共享 helper，chat 行为逐字不变）；toolName 必须是已注册写工具（查域注册表，未注册拒绝）
  - `triggers.AcceptSuggestion(id string) (AcceptResult, error)`；`type AcceptResult struct { ActionID, ConfirmWord, Summary, JobID string }`——流程：GetSuggestion（非 new → 409 语义 error）→ 解析 actions_json 取第一个 `task_plan`（`navigate` 类建议前端直接跳转**不调 accept**；accept 收到 navigate-only 卡返回明确错误）→ `tasks.PlanPhotoDedupe(scope, 120*time.Second)`（kind≠photo_dedupe → 明确错误，F4 扩）→ partial 时 Summary 注明「部分扫描」→ `ai.CreatePendingAction("start_task", {"job_id":…}, 影响面摘要)`（摘要格式与 ai 工具 start_task 确认卡**同源**：复用/抽取 chat.go 的 summary 构造函数，禁止复制文案）→ 建议 state=accepted → 审计 → 返回 AcceptResult。失败路径（plan 错误/工具未注册）不改 state。
- REST: `POST /api/triggers/suggestions/{id}/accept` → 200 `{"action_id":…,"confirm_word":…,"summary":…,"job_id":…}`；前端拿它开确认弹窗 → 既有 `POST /api/ai/action/confirm` → StartJob（**零新确认语义**）。

**对抗/边界测试清单:**
1. accept 成功链：种重复文件 → CreateSuggestion → accept → 断言 job pending_confirm + action_id 可 confirm → confirm 后 job running→done（对照 F1 tasks 测试模式）
2. 重放：同建议二次 accept → 409 语义；dismissed/expired accept → 拒绝
3. action TTL：过期 action_id confirm → 既有 410/404 语义不变
4. CreatePendingAction 未注册工具名拒绝；chat.go 既有确认门测试全部不改动仍绿
5. 无可清理文件时：PlanPhotoDedupe 0 组的行为以 F1 实现为准——若不建 job，AcceptSuggestion 返回「无需清理」语义错误且建议保持 new；若建 0 步 job，Summary 明示「没有发现重复文件」
6. 泄漏面：accept 响应/审计 detail 不含 api_key/密码形状（对照 ai security_test 模式）

**Commit:** `feat(triggers): 建议接受链 — ai.CreatePendingAction导出(复用既有确认门/TTL/确认词)+accept端点(plan固化→pending action→confirm即执行)+重放/过期/空计划对抗用例 (F2 T5)`

---

### Task 6: 前端通知中心（铃铛 + 建议卡 + 周报 + 接受确认弹窗）

**Files:**
- Modify: `web/frontend/index.html`（sidebar-header 或顶栏挂铃铛+未读徽标；通知面板结构照既有 modal/下拉模式）
- Modify: `web/frontend/app.js`
- Modify: `web/frontend/i18n/zh-CN.json`、`en-US.json`
- **不碰任何 Go 逻辑文件**（消费 Task 1-5 的 REST）

**交付明细:**
1. **铃铛入口**：AI 关断时隐藏整个通知中心（triggers/notifications 属 AI 功能面，关断零足迹的 UI 表达）；未读数徽标（badge-xs 语义色）；30s 轮询 unread-count（document.hidden 时暂停，对照任务 tab 轮询清理模式；离开页面/组件销毁清定时器）。
2. **通知面板**（点击铃铛展开）三段：
   - 建议卡区（suggestions state=new）：人话 title/body（t(key, params) 插值）+ 按钮：task_plan 类→「执行」（接受链）；navigate 类→「去看看」（navigate(page) + dismiss）；「忽略」（dismiss）
   - 周报卡（kind=report 最新一条）：渲染 storage/tasks/health 三块数字（大小走既有格式化 helper），「重新生成」按钮
   - 通知列表（其余 kind 倒序，未读加点标）：点击→已读+按 kind 跳转（task→任务 tab；health→健康页）；「全部已读」「清空已读」
3. **接受确认弹窗**：「执行」→ POST accept → {action_id, confirm_word, summary} → 确认弹窗（**复用 aiConfirm 的模板结构与 type-to-confirm 交互**，独立状态对象 notifConfirm 避免与聊天确认互踩）→ POST /api/ai/action/confirm → 成功 toast + 跳任务 tab + loadAITasks()。弹窗不因点击外部关闭。
4. **t() 插值**：`t(key, {pct: 85})` → 文案内 `{pct}` 替换（不破坏既有无参调用）；参数从通知/建议 data_json JSON.parse 后传入。
5. api() 字符串守卫模式消费所有新端点；空状态 .empty-state；新文案全双语，check-i18n.py 过；:style 绑定元素不写布局类 inline style（前端红线全套）。

**验证:**
1. python3 scripts/check-i18n.py；cd web && go build ./...（embed 重编译）；go test ./... -count=1 无回归
2. UI 实测由 parent 本机 Chrome 验收轮做；执行者能自测则自测（stub 环境），做不了在报告注明

**Commit:** `feat(ui): 通知中心 — 铃铛+未读徽标/建议卡(执行/忽略/去看看)/周报卡/通知列表+t()插值+接受确认弹窗(复用type-to-confirm) 双语i18n (F2 T6)`

---

### Task 7: 文档三件套（验收由 parent 负责，本 Task 只文档）

**Files:** `docs/feature-operation-checklist.md`、`docs/nas-product-manual.md`、`CHANGELOG.md`

1. **checklist**：新增「通知中心」与「主动管家」模块节，按分类规则（只显示/用户操作/系统自动）：通知列表/未读徽标(只显示)、标记已读/清空(用户操作)、建议卡执行/忽略(用户操作)、接受确认(用户操作)、周报查看/重新生成(用户操作)、事件转通知(系统自动)、水位 cron(系统自动)、冷却/上限(系统自动)、周报生成(系统自动)。汇总统计用附录 awk 命令**实算**。
2. **手册**：AI 章节补「通知中心与主动管家」小节：铃铛在哪、建议卡是什么/为什么限流（用户视角：「管家不会反复唠叨同一件事」）、执行建议的确认流程、周报内容与时间（周日 9 点）、关断 AI 后通知中心消失。用户视角行文，不出现内部函数名/表名。
3. **CHANGELOG**：未发布段加 F2 条目组（带 commit 短哈希）。
4. 红线：不出现真实测试机 IP；文档与代码一致（端点/阈值 85%/冷却 24h/上限 5 写前核对实现）。

**Commit:** `docs(triggers): 操作清单通知中心+主动管家节(统计实算)+手册小节+CHANGELOG未发布段 (F2 T7-docs)`

---

## 验收口径（parent 执行，spec §八 F2 行）

- 真机 E2E（48）：① 真事件链——停 vsftpd → health 迁移事件 → 通知中心人话通知（键渲染正确）→ 恢复；② 建议接受链——fixture 建 watermark 建议 + 种重复文件 → 面板接受 → 确认弹窗 → 任务执行 → done → 报告数字对；③ 冷却/上限真机断言（同 key 连触两次只出一张卡）；④ 周报 generate 端点 → report 卡 → 前端渲染三块数字；⑤ 关断态：notifications/triggers 全端点 403、事件不再入箱；⑥ kill -9 重启后通知/建议在库不丢、无重复事件风暴。
- 回归：acceptance-suite 38 项、ai-e2e2、任务 E2E 18 项全绿；`go test ./... -race` 绿；边界测试含新模块常绿。
- 57：真实 DeepSeek 环境轻回归（通知中心 UI + 不动用户配置）。

## Self-Review 记录

- **spec 覆盖**：§五事件源①②③→T3（③ 留钩注明 F4）；建议卡表结构→T2；接受路径→T5+T6；反骚扰三条（冷却/上限/关断停摆）→T2/T3/Global；人话层→T3 键表+T6 插值；周报→T4；通知中心数据源+通道中立→T1 叶子设计；§八 F2 验收→上节。
- **偏离 spec 明示**：① task.done「顺势建议下一件」F2 不做（无第二种 kind 可荐，F4 补）；② LLM 润色推送文案不在 F2（模板键已满足人话标准，LLM 属 F3）；③ navigate 类建议不走 accept 端点（纯前端跳转+dismiss）；④ triggers→notifications 允许直接 import（T4 内拍板理由）。
- **类型一致性**：CreateSuggestion/AcceptSuggestion/GenerateWeeklyReport/AddNotification/CreatePendingAction 签名在定义处与消费处逐一核对；AcceptResult 字段与 REST 响应 JSON 键名一致（action_id/confirm_word/summary/job_id）。
