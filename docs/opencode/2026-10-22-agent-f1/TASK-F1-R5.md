# TASK-F1-R5: 文档三件套（F1 计划 Task 7 文档部分）

仓库：/home/jacky/soft/nas（分支 feat/agent-foundation）。前置：F1 T1-T6 已全部落地（tasks 引擎/photo_dedupe/AI 工具 7 枚/前端任务 tab）。机器验收由 parent 负责，本轮只做文档。

## 必读
1. docs/plans/2026-10-22-agent-foundation-f1-plan.md 的 **Task 7 Step 1**。
2. 实现现实：web/modules/tasks/（kind/状态机/API）、web/modules/ai/tools_tasks.go（7 工具）、frontend 任务 tab（i18n 键 ai.task_*）——文档描述必须与代码一致，写之前先读。

## 交付
1. **docs/feature-operation-checklist.md**：新增「任务引擎」模块节——操作项照现有分类规则（只显示/用户操作/系统自动）：任务列表查看(只显示)、任务详情/报告(只显示)、AI 对话规划任务(用户操作)、确认启动(用户操作)、暂停/继续/取消(用户操作)、worker 后台执行(系统自动)、崩溃恢复置 paused(系统自动)、进度事件上报(系统自动)、photo_dedupe 扫描(系统自动)。**汇总统计用文档附录 awk 命令实算**（别拿旧数字增量算），模块数+1。
2. **docs/nas-product-manual.md**：AI 章节（11.6）补「任务」小节：什么是任务、对话怎么发起（"帮我清理重复照片"）、确认卡影响面、任务页六态、暂停/继续/取消语义、回收站联动（删除的文件去哪了/怎么恢复）、崩溃重启后为什么是"已暂停"（安全设计说明）。用户视角行文，不出现内部函数名。
3. **CHANGELOG.md**：「未发布」段新增 F1 条目组（任务引擎/photo_dedupe/AI 工具/前端任务 tab/架构边界测试/filemgmt 内核抽取），带 commit 短哈希风格对齐既有条目；不建版本段（发版时转正）。
4. 红线：CHANGELOG/手册不出现真实测试机 IP（用「测试机」泛指）；不 git add 本 brief 与 OPENCODE-REPORT-F1-R5.md。

## 验证与提交
1. awk 实算输出贴进报告；grep 确认文档描述的工具名/状态名/API 路径与代码一致（抽查清单写进报告）。
2. 提交信息：`docs(tasks): 操作清单任务引擎节+手册任务小节+CHANGELOG未发布段 (F1 T7-docs)`
3. 报告写 /home/jacky/soft/nas/OPENCODE-REPORT-F1-R5.md：awk 实算输出、文档-代码一致性抽查表、偏离。
