# TASK-F2-R4: 前端通知中心（F2 计划 Task 6）

仓库：/home/jacky/soft/nas（分支 feat/agent-f2）。Go module 在 web/。
前置：R1-R3 已落地 notifications/triggers 后端与接受链。先读 web/modules/notifications/notifications.go 与 web/modules/triggers/（REST 路由/响应形状逐字核对）、web/modules/triggers/accept.go（accept 响应 JSON 键名）、frontend/app.js 的 aiConfirm 弹窗与任务 tab 轮询模式，以代码现实为准。

## 必读
docs/plans/2026-10-23-agent-foundation-f2-plan.md 的 **Task 6**（交付明细 5 条逐字）。

## 硬约束（前端红线全套）
1. 禁止读取/执行仓库外文件；不碰任何 Go 逻辑文件（消费既有 REST）。
2. 依赖本地化：不引 CDN/外部资源/新 JS 库；CSP：无内联 script。
3. 新文案全部 i18n 双语（含 t() 插值扩展后所有通知/建议/周报文案键的渲染回退——键缺失时显示键名不炸页面），完成后 python3 scripts/check-i18n.py 必须通过。
4. 弹窗用既有 modal/modal-box 类；不因点击外部关闭；:style 绑定元素不写布局类 inline style；空状态 .empty-state；徽章 .badge-xs/.badge-sm 密度类。
5. api() 字符串守卫模式消费所有通知/建议端点。
6. t() 插值：t(key, {pct: 85}) → 文案内 {pct} 替换；**不破坏既有全部无参调用**（改完 grep 既有 t( 调用面自测）；通知/建议的 data_json JSON.parse 失败容错（坏 JSON 不炸渲染，显示原文）。
7. 铃铛轮询 30s：document.hidden 暂停、navigate 离开清定时器（对照任务 tab 轮询清理模式）；AI 关断（aiStatus.enabled=false）时整个通知中心隐藏。
8. 接受确认弹窗独立状态对象 notifConfirm（不复用 aiConfirm 实例，避免与聊天确认互踩），交互同款 type-to-confirm；确认成功 toast + 跳任务 tab + loadAITasks()。
9. navigate 类建议卡「去看看」= navigate(page) + dismiss 一次完成；「执行」按钮只对 task_plan 类显示。
10. 周报卡渲染 storage/tasks/health 三块数字（大小走既有格式化 helper 复用不重写）；「重新生成」按钮 POST /api/triggers/report/generate 后刷新列表。
11. 改完 cd web && go build ./... 重编译（前端 embed 进二进制）验证。
12. 单提交，信息用计划 Task 6 原文。不 git add 任务书与 OPENCODE-REPORT-F2-R4.md。

## 验证与提交
1. python3 scripts/check-i18n.py 通过；cd web && go build ./... && go test ./... -count=1 无回归
2. UI 实测 parent 用本机 Chrome 验收（stub 环境）；你能自测则自测并在报告记录，做不了注明
3. 报告写 OPENCODE-REPORT-F2-R4.md：新增 i18n 键数、app.js 新增函数/状态清单、t() 插值实现与既有调用面兼容说明、轮询清理点清单、与后端响应形状的差异发现（如有）、偏离与疑虑。
