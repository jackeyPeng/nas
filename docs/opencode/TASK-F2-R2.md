# TASK-F2-R2: 事件源接线 + 家庭周报（F2 计划 Task 3+4）

仓库：/home/jacky/soft/nas（分支 feat/agent-f2）。Go module 在 web/。
前置：R1 已落地 notifications（AddNotification/订阅者）与 triggers 骨架（CreateSuggestion 冷却24h/上限5/validateActions）+ common.CronMatch。先读这两个模块与计划的「代码现实锚点」节，以代码现实为准。

## 必读
docs/plans/2026-10-23-agent-foundation-f2-plan.md 的 **Task 3 与 Task 4**（规则表/人话键/周报 JSON 形状逐字）。

## 硬约束
1. 禁止读取/执行仓库外文件；验证只用 go build / go vet / go test / scripts/check-i18n.py。
2. 新增 Go 依赖 0；TDD 先红后绿；每 Task 单独提交（信息用计划原文，两条）。
3. 事件源盘点先行：`grep -rn "EmitEvent(" modules/` 全量盘点，health 的 checkID 命名读 health.go 注册表后定通配前缀；与计划规则表出入时以代码现实修正并记录。
4. backup 失败事件：先确认 backup 模块是否已有失败 EmitEvent；没有才补 `backup.data_failed` 生产点（成功路径不动，行为零变化）。
5. diskmgmt.PoolUsage / health.Summary 导出按 filemgmt export.go 模式：只读、不改既有行为、export_test 覆盖。
6. **人话红线**：trig.*/notif.*/周报文案键必须家庭小白可懂+给下一步动作，禁止裸术语（RAID/SMART 等要翻译成人话）；zh/en 双语同标准；check-i18n.py 过。
7. triggers→notifications 直接 import（计划 T4 拍板）；边界允许表若 R1 未开这条边，本轮补开并在报告记录。
8. 水位 cron：ticker 30s 扫描 + `0 */6 * * *` 命中 + 6h 窗口去重（lastRun）；池不存在静默跳过；guard=false 全停摆。
9. 周报 JSON 形状逐字按计划（week_start/week_end/storage/tasks/health 三块）；本周过滤边界（恰好 7 天前）要有测试。
10. 不 git add 任务书与 OPENCODE-REPORT-F2-R2.md。

## 验证与提交
1. cd web && go build ./... && go vet ./modules/triggers/ ./modules/notifications/ ./modules/diskmgmt/ ./modules/health/ ./modules/backup/ 全绿
2. go test ./modules/triggers/ ./modules/notifications/ -count=1 -race 绿；go test ./... -count=1 全量无回归（已知沙箱 flake：tasks 并行 -race 偶发 SQLite OOM，复跑或 -p 1 确认非本轮引入）
3. python3 scripts/check-i18n.py 通过
4. 报告写 OPENCODE-REPORT-F2-R2.md：事件盘点结果 vs 计划规则表差异、backup 产方是否补点、PoolUsage/Summary 导出签名、人话键全清单（zh 文案原文）、周报 JSON 样例、测试计数、偏离与疑虑。
