# TASK-F2-R5: 文档三件套（F2 计划 Task 7）

仓库：/home/jacky/soft/nas（分支 feat/agent-f2）。前置：F2 T1-T6 已全部落地（notifications 收件箱/triggers 建议卡+冷却上限/事件源接线+水位 cron/家庭周报/接受链/前端通知中心）。机器验收由 parent 负责，本轮只做文档。

## 必读
1. docs/plans/2026-10-23-agent-foundation-f2-plan.md 的 **Task 7**。
2. 实现现实：web/modules/notifications/、web/modules/triggers/（rules.go 规则表/report.go 周报/accept.go 接受链——端点/阈值/键名以代码为准）、frontend 通知中心（i18n 键 trig.*/notif.*）。文档描述写之前先读代码核对。

## 交付
1. **docs/feature-operation-checklist.md**：新增「通知中心」与「主动管家」两个模块节——操作项按计划 Task 7 的分类清单（只显示/用户操作/系统自动）逐项落；**汇总统计用文档附录 awk 命令实算**（别拿旧数字增量算），模块数 +2。
2. **docs/nas-product-manual.md**：AI 章节补「通知中心与主动管家」小节：铃铛位置、建议卡是什么/为什么同一件事 24 小时内只提醒一次且最多 5 条待处理（用户视角写「管家不会反复唠叨」）、执行建议的确认流程（点执行→确认卡→输入确认词→任务开跑）、周报内容与生成时间（周日 9 点+手动重新生成）、关闭 AI 后通知中心整体消失（零足迹）。不出现内部函数名/表名/端点路径。
3. **CHANGELOG.md**：「未发布」段新增 F2 条目组（notifications/triggers/事件源与水位/周报/接受链/前端通知中心/cleanup.sh 修复若不在 master 段则一并记——先 git log 对帐），带 commit 短哈希；不建版本段。
4. 红线：不出现真实测试机 IP（用「测试机」泛指）；阈值（85%）/冷却（24h）/上限（5 条）/周报时间（周日 9 点）写前与代码核对一致。
5. 不 git add 本任务书与 OPENCODE-REPORT-F2-R5.md。

## 验证与提交
1. awk 实算输出贴进报告；文档-代码一致性抽查表（端点/阈值/键名 ≥8 项）写进报告。
2. 提交信息：`docs(triggers): 操作清单通知中心+主动管家节(统计实算)+手册小节+CHANGELOG未发布段 (F2 T7-docs)`
3. 报告写 OPENCODE-REPORT-F2-R5.md：awk 输出、抽查表、CHANGELOG 对帐结果（git log 逐条）、偏离。
