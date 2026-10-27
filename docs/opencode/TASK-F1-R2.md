# TASK-F1-R2: filemgmt 内核抽取 + photo_dedupe（F1 计划 Task 3 + Task 4）

仓库：/home/jacky/soft/nas（分支 feat/agent-foundation）。Go module 在 web/。
前置：R1 已落地 tasks 骨架+worker（jobs 表/状态机/checkpoint/RegisterOp/StartJob 等，签名见计划 Task 1/2 的 Interfaces——以代码现实为准，先读 web/modules/tasks/）。

## 必读
1. docs/plans/2026-10-22-agent-foundation-f1-plan.md 的 **Task 3 与 Task 4**（接口签名/测试清单/步骤逐字执行）。
2. spec §四（photo_dedupe 语义）与 §二红线 9（池根内/永不硬删）。

## 硬约束
1. 禁止读取/执行仓库外文件；验证只用 go build / go vet / go test。
2. 新增 Go 依赖 0（哈希用 crypto/sha256，EXIF 本轮不涉）。
3. TDD：计划里 Task 3/4 的测试清单是最小集，先红后绿。
4. **Task 3 是机械抽取**：handleDelete 的守卫顺序/文案/HTTP 码逐字保留（前端与验收套件依赖这些文案），内核 MoveToRecycle 用哨兵错误（ErrNotInPool/ErrIsRecycleContent/ErrIsShareRoot/ErrNotFound/ErrBadActor），handleDelete 映射回原文案。抽取前后 filemgmt 全量测试必须同绿。
5. **Task 4 扫描器安全语义**：路径一律先 filemgmt.ResolvePoolPath；跳过 .recycle/#recycle 段、隐藏文件、符号链接（Lstat）、非普通文件；保留策略=组内路径最短（并列取 mtime 最早）；move 步骤幂等（源已不存在且 checkpoint 已记→skip；未记→skipped 计数继续，不整任务失败）。
6. 扫描预算：PlanPhotoDedupe(scopeRel, budget)，超时返回已扫部分+partial=true；二次调用命中 FindPendingJob 直接返回不重扫。
7. 边界测试白名单：tasks→filemgmt 应已在 R1 允许表（若没有，本轮补进 arch_boundary_test.go 的 allowedModuleDeps 并在报告注明）；filemgmt 不得反向 import tasks。
8. 每 Task 单独提交，信息用计划原文。不 git add 本 brief 与 OPENCODE-REPORT-F1-R2.md。

## 落点提醒（已核实）
- handleDelete 在 modules/filemgmt/mutate.go:623 起；守卫链：resolvePoolPath→池根判定→.recycle 字面段+EvalSymlinks 双判定→共享根判定（ownerShareOfVia）→Lstat 404→actor 白名单→resolveRecycleTarget→recycleConflictFree→MkdirAll 0700→Rename
- filemgmt 无 export.go，本轮新建（模式对照 modules/users/export.go）
- 池根 override：filemgmt 若已有测试注入模式则复用；没有则加 poolRootOverrideForTest 包变量（生产恒 /data/nas1），并在报告注明
- RegisterOp("move_to_recycle", ...) 在 kinds_dedupe.go 的 init() 注册；执行链=Args["src"]→ResolvePoolPath→MoveToRecycle(src, "")
- 扩展名白名单：jpg/jpeg/png/heic/gif/webp/bmp/mp4/mov/avi/mkv/webm（小写比较）
- 两级哈希：size 分桶→头 64KB sha256→全量 sha256；读文件用 32KB buffer

## 验证与提交
1. cd web && go build ./... && go vet ./modules/filemgmt/ ./modules/tasks/ 全绿
2. go test ./modules/filemgmt/ ./modules/tasks/ -count=1 -race 绿；go test ./... -count=1 全量无回归；go test -run TestArchBoundaries . 绿
3. 提交信息（两条）：
   - `refactor(filemgmt): 抽取MoveToRecycle/ResolvePoolPath内核导出(export.go模式) — handleDelete行为不变,任务引擎复用回收站链路 (F1 T3)`
   - `feat(tasks): photo_dedupe — 两级哈希扫描/最短路径保留策略/预算partial/回收站执行接线(幂等skip) (F1 T4)`
4. 报告写 /home/jacky/soft/nas/OPENCODE-REPORT-F1-R2.md：抽取守卫链对照表（原 handleDelete 行为 vs 内核+映射）、dedupe 测试矩阵（含两级哈希分流/符号链接/预算 partial/幂等 skip）、边界白名单变更、测试计数、偏离与疑虑。
