# OPENCODE-REPORT-F1-R5: 文档三件套（F1 Task 7 文档部分）

分支：`feat/agent-foundation`（docs-only，未触碰任何 Go/前端源码）。

## 交付清单

1. `docs/feature-operation-checklist.md`：新增「19. 任务引擎（Tasks）」模块节（10 操作项），AI 助手节追加「任务工具调用（7 项）」一行，第 21 节系统自动汇总追加 3 行任务引擎自动项，模块总数 20→21，顶部「最后更新」改 2026-10-22。
2. `docs/nas-product-manual.md`：11.6 AI 助手章节追加「任务：让助手替你干活」小节（用户视角，不含函数名/表名/字段名）。
3. `CHANGELOG.md`：顶部新增 `## [未发布]` 段，F1 六个条目组（任务引擎骨架/架构边界测试/filemgmt 内核抽取/photo_dedupe/AI 任务工具 7 枚/前端任务 tab）+ 文档条目，带 commit 短哈希，未建版本号段。

## awk 实算输出（docs/feature-operation-checklist.md）

```
$ cd /home/jacky/soft/nas/docs && awk -F'|' '/^\|/{gsub(/ /,"",$3); if($3=="只显示")a++; if($3=="用户操作")b++; if($3=="系统自动")c++} END{print "只显示:",a; print "用户操作:",b; print "系统自动:",c; print "合计:",a+b+c}' feature-operation-checklist.md

只显示: 66
用户操作: 110
系统自动: 34
合计: 210
```

汇总统计表已按此结果写入（66/110/34/210），模块数用 `grep -c '^## [0-9]'` 实数验证为 21（含第 21 节跨模块汇总，延续既有计数口径——此前 20 节口径下该汇总节同样被计入模块数）。

```
$ grep -c '^## [0-9]' feature-operation-checklist.md
21
```

## 文档-代码一致性抽查表

| 文档描述 | 代码出处 | 核实结果 |
|---|---|---|
| 六态：pending_confirm/running/paused/done/failed/cancelled | `web/modules/tasks/tasks.go:22-27` `Status` 常量 | 一致 |
| 状态机合法迁移（pending_confirm→running/cancelled；running→paused/done/failed/cancelled；paused→running/cancelled） | `web/modules/tasks/tasks.go:68-73` `legalTransitions` | 一致 |
| AI 工具 7 枚名称：plan_task/start_task/cancel_task/pause_task/resume_task/get_task_list/get_task_detail | `web/modules/ai/tools_tasks.go:25-87` `nasTasksTools()` | 一致（grep 逐一核对 `Name:` 字段） |
| plan_task 只读、start_task/cancel_task/resume_task 写（确认门）、pause_task 只读 | `web/modules/ai/tools_tasks.go` 各 `Write:` 字段 | 一致 |
| REST 路径：`GET /api/tasks`、`GET /api/tasks/{id}`、POST pause/resume/cancel、`GET /api/tasks/{id}/stream` | `web/modules/tasks/api.go:38-41,66-95` | 一致 |
| AI 关闭时 `/api/tasks/*` 全部 403 | `web/modules/tasks/api.go:27-35` `requireTasksEnabled` + `web/main.go:169` `tasks.RegisterRoutes(mux, ai.IsEnabled)` | 一致 |
| 崩溃恢复：重启后 running→paused，不自动续传 | `web/modules/tasks/tasks.go:62-65,128-169` `crashRecoveryNote`/`recoverCrashedJobs` | 一致 |
| 文件清理走回收站（MoveToRecycle），永不硬删 | `web/modules/tasks/kinds_dedupe.go:311-340` `execMoveToRecycle` → `filemgmt.MoveToRecycle` | 一致 |
| 两级哈希：按大小分桶→头 64KB 哈希→全量哈希成组；保留路径最短者 | `web/modules/tasks/kinds_dedupe.go:154-263` `groupDedupeCandidates`/`pickKeepIndex` | 一致 |
| 扩展名白名单（jpg/jpeg/png/heic/gif/webp/bmp/mp4/mov/avi/mkv/webm） | `web/modules/tasks/kinds_dedupe.go:33-37` | 一致 |
| 进度事件里程碑 10/25/50/75% + 终态上报 task.progress/done/failed | `web/modules/tasks/worker.go:249-251,392-407` | 一致 |
| 源文件已不存在时记 skipped 不整任务失败 | `web/modules/tasks/kinds_dedupe.go:314-339` | 一致 |
| 前端六态徽章/进度条轮询/暂停继续取消按钮/完成报告/i18n 双语键 | `web/frontend/index.html:2855-2906`、`web/frontend/i18n/zh-CN.json:1393-1411` | 一致（i18n 键 ai.tab_tasks/task_kind_photo_dedupe/task_status_*6/task_progress/task_impact/task_report_*3/task_empty/task_hint_confirm/task_pause/task_resume/task_cancel/task_error_title 均存在） |
| 回收站恢复：原位冲突加后缀/合并式恢复，不覆盖 | `docs/nas-product-manual.md:956`（既有 7.x 节回收站描述，复用措辞口径，未杜撰新机制） | 一致 |

## 红线核对

- CHANGELOG/手册未出现真实测试机 IP（统一用"测试机"/"面板"措辞，未提具体 57/48 等机器编号）。
- 未 `git add` 本 brief（`TASK-F1-R5.md`）与 `OPENCODE-REPORT-F1-R5.md`（二者当前为未跟踪文件，提交时只 add 三份文档）。

## 偏离说明

无实现偏离。文档内容严格对照已落地代码撰写；唯一的措辞取舍是产品手册"任务"小节完全不出现 Go 类型名/表名/API 路径（按 brief 要求"用户视角行文，不出现内部函数名"），内部一致性抽查表里的代码引用仅用于本报告核实，不进手册正文。
