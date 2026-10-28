# 任务：consensus R2 审查回炉修复

你在 ~/soft/nas 仓库工作。基线 = HEAD (4ff79fd)。R2 三提交经独立审查裁决 approve_with_minor（1 important + 6 minor），本轮只修下列编号项，其余已裁决为记录在案不动。先读涉及文件；与本文不符在报告申报。

## 硬约束（违反 = 返工）

1. 禁读/改仓库外文件（含 ~/go/pkg/mod、/tmp）；验证只用 web/ 内 go build/vet/test。
2. 只改 web/modules/diskmgmt/ 下代码与测试；TASK-REPORT.md 覆盖写但不 git add。
3. 禁止真实磁盘/sudo/mdadm 命令；数据形态用 fixture。
4. git add 只加改动文件；单 commit，message 原样文末。
5. 既有测试全过，覆盖率基线 23.3% 不降。

## Fix 1（Important）：mdstat 回退路径用 StatusLine 补降级计数

现状（审查实锤）：`buildRAIDStatusFromMdstat`（overview.go ~980-1030）调
`raidHealthFromCounts(headerState, ra.FailedDisks, ra.Disks, ra.FailedDisks)`——degraded 入参用 (F) token 计数。
但"失败成员已被 mdadm --remove 摘除"的降级阵列，mdstat 头行只剩存活盘 token（如 `md0 : active raid1 sda[1]`，
无 (F)），降级信息只在 StatusLine `[2/1] [U_]` 里 → FailedDisks=0、Disks=1 → Health 误报 "normal"、
新增 json health 字段对外失真。代码注释（overview.go:999-1003"mdstat 的成员 token 在阵列失活前不会消失"）
与事实不符。

修法：
1. MdstatBlock.StatusLine 现有格式 `^[[ _U]+]$`（如 "[U_]"、"[UUU_]"）。在 buildRAIDStatusFromMdstat 中：
   - `statusDegraded := strings.Count(block.StatusLine, "_")`（StatusLine 为空则 0）
   - `designDisks := len(block.StatusLine) - 2`（StatusLine 非空时；即槽位数）
   - 传入 raidHealthFromCounts 的 degraded 实参改为 `max(ra.FailedDisks, statusDegraded)`，
     raidDisks 实参改为 `max(ra.Disks, designDisks)`（StatusLine 为空时维持现值）。
2. 修正 999-1003 注释：如实说明"(F) token 在成员被 --remove 摘除后消失，降级须以 StatusLine 下划线计数兜底"。
3. State 派生同步受益（Health=degraded → State="degraded"），确认既有 State 语义不回归。
4. 测试：fixture 补三例——`[2/1] [U_]` 且头行只剩 1 个存活 token（无 (F)）→ Health=degraded；
   `[2/2] [UU]` 干净 → normal；含 (F) token 且 StatusLine `[U_]` → degraded（两来源取 max 不重复计）。
   既有 TestBuildRAIDStatusFromMdstat 8 子用例断言如需更新 Health 期望值，逐个在报告说明。

## Fix 2（Minor3）：sysfs sync_action 缺键 fail-safe

现状：`sysfsResyncDone(syncAction)` 把 ""（含 vals 缺键的零值）判为完成——readRAIDSysfsValues 目录存在
即 ok=true，单个文件读失败被静默跳过时，waitForResync 计 cleanStreak、handleRAIDExpandFS 闸门放行（fail-open）。

修法：waitForResync / handleRAIDExpandFS / handleRAIDReshapeStatus 的 sysfs 分支改为
`syncAction, hasKey := vals["sync_action"]`；**缺键时视为该轮 sysfs 不可用，走 mdstat 回退判定**
（waitForResync 当轮按回退路径的块内进度行判定；expandfs 闸门按回退判据；reshape-status 按回退输出），
不得把缺键当 idle。`sysfsResyncDone` 保留现签名与语义（它只负责"拿到值后"的判定），调用侧负责 hasKey 分流。
纯函数测试： vals 缺键 → 分流正确（若需抽 helper 如 `sysfsSyncAction(vals) (string, bool)`，抽之并测）。

## Fix 3（Minor5）：waitForResync 空百分比文案

现状（raid_expand.go:363-371）：sysfs 分支 sync_completed 为 none/畸形时 percent="" 仍拼
`"同步进度: "`（悬挂冒号）。修法：percent 非空才用 `"同步进度: "+percent`，空则 Detail 用 `"同步中（"+syncAction+"）"`。
mdstat 分支不动。

## Fix 4（Minor1）：extractVGName 分支2注释修正

overview.go:643-644 注释举例错误（`/dev/vg_nas` 实际走 parentBase=="dev" 排除项兜底返回 base，
base==device 分支只命中完全无斜杠的裸名）。只改注释，行为不动（五用例已审查确认正确）。

## 明确不做（审查已裁决记录在案）

- Minor2（extractVGName 分支3非 /dev 两级路径行为面扩大）：现有调用点无影响，记录不修。
- Minor4（两路径展示桶计数差异）：展示字段无逻辑消费方，记录不修；但报告里"两条路径桶计数一致"的过强声明要在本轮报告更正。
- Minor6（getRAIDStatus 部分成功静默丢阵列）：竞态窗口可忽略，记录不修。

## 验证（必须执行，输出贴报告）

```
cd web && go build ./... && go vet ./modules/diskmgmt && go test -race ./modules/diskmgmt -count=1 -cover
```

## 提交（原样 message）

```
fix(diskmgmt): 审查回炉 — mdstat回退StatusLine下划线补降级计数/sysfs缺键fail-safe/空百分比文案/注释更正 (consensus R2审查)
```

## 报告（TASK-REPORT.md 覆盖，不 git add）

逐 Fix：改动点、测试增改清单、既有断言变化说明、验证输出、覆盖率、偏差申报。
