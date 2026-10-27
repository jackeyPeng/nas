# TASK-F1-R4: 前端任务 tab（F1 计划 Task 6）

仓库：/home/jacky/soft/nas（分支 feat/agent-foundation）。Go module 在 web/。
前置：R1-R3 已落地任务引擎+photo_dedupe+AI 工具。先读 web/modules/tasks/api.go（REST 响应形状逐字核对）与 web/modules/ai/tools_tasks.go（工具名/kind 映射），以代码现实为准。

## 必读
docs/plans/2026-10-22-agent-foundation-f1-plan.md 的 **Task 6**。

## 硬约束（前端红线，违反=返工）
1. 禁止读取/执行仓库外文件。
2. 依赖本地化：不引任何 CDN/外部资源；不新增 JS 库。
3. CSP：无内联 script；Alpine 绑定用既有模式。
4. 新文案全部 i18n 双语（计划 Task 6 Step 3 的键清单为最小集），完成后 `python3 scripts/check-i18n.py` 必须通过。
5. 弹窗 class 用既有 `modal`/`modal-box`；不因点击外部关闭；`:style` 绑定元素不写布局类 inline style；空状态用 `.empty-state` 组件；徽章用 `.badge-xs`/`.badge-sm` 密度类不写裸 padding。
6. api() 字符串守卫模式消费所有任务端点（`!data || typeof data !== 'object' || !data.<必需字段>` 判定，字符串错误 toast）。
7. 前端 embed 进二进制：改完必须 `cd web && go build ./...` 重编译验证；不碰任何 Go 逻辑文件。
8. 单提交，信息用计划原文。不 git add 本 brief 与 OPENCODE-REPORT-F1-R4.md。

## 交付明细
1. **ai 页第三 tab「任务」**：现有 aiTab（'chat'|'settings'）扩 'tasks'；tab 按钮行加项（i18n `ai.tab_tasks`），x-show 分支对齐既有两 tab 结构。
2. **app.js**：
   - 状态：`taskJobs: []`
   - `loadAITasks()`：GET /api/tasks → taskJobs（api() 字符串守卫）
   - `taskAction(id, action)`：POST /api/tasks/{id}/{action}，action ∈ pause/resume/cancel；cancel 前 `confirm()`（i18n 文案）；成功 toast + loadAITasks()
   - `taskProgressPolling()`：存在 running/paused 任务时 2s setInterval 轮询 loadAITasks，无则清定时器；navigate 离开 ai 页与组件销毁时清理（对照既有轮询实现模式，如监控页）
   - `navigate('ai')` case 追加 `this.loadAITasks()`
   - done 任务 Result JSON 解析出 moved/freed/skipped 渲染报告行（freed 走既有大小格式化 helper，找到并复用，不重写）
3. **index.html 任务列表卡片**：kind 中文名（`ai.task_kind_photo_dedupe`）、六态徽章（pending_confirm 黄/running 蓝+进度条/paused 灰/done 绿/failed 红/cancelled 灰，色用既有 badge 语义类）、进度条（cur/total+note，复用既有向导进度条样式类）、影响面行（N 组/大小）、按钮按状态显隐（running→暂停+取消；paused→继续+取消；pending_confirm→只读提示"请在对话中确认启动"键 `ai.task_hint_confirm`）、done 可展开报告、failed 显示 error。空状态 `.empty-state`（键 `ai.task_empty`）。
4. **聊天联动（轻）**：聊天里 start_task 确认卡已由既有确认门渲染（summary 含影响面），无需新组件；仅在工具轨迹条目里 plan_task/start_task 正常显示（既有机制自动覆盖，验证即可）。

## 验证与提交
1. python3 scripts/check-i18n.py 通过；cd web && go build ./...（embed 重编译）；go test ./... -count=1 无回归（本轮不动 Go 逻辑）。
2. 提交信息：`feat(ai): 前端任务tab — 六态徽章/进度条轮询/暂停恢复取消/完成报告+双语i18n (F1 T6)`
3. 报告写 /home/jacky/soft/nas/OPENCODE-REPORT-F1-R4.md：新增 i18n 键数、app.js 新增函数清单、六态徽章样式对照表、轮询清理点清单、与 REST 响应形状的差异发现（如有）、偏离与疑虑。浏览器实测不在本轮（R5 后 parent 用本机 Chrome 统一做）。
