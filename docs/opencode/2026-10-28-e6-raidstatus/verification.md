# E6 修复验证纪要（getRAIDStatus 枚举失效 → RAID 健康监控全盲）

修复提交：2773a3c。coder 自报报告（TASK-REPORT.md）在 parent 推送前被误删未归档（parent 操作失误，
过程完整保留在派发 run log），本纪要以 parent 独立验证 + 真机证据替代。

## 缺陷与真机实锤

- `getRAIDStatus()` 用 `common.ExecOutput("ls", "/dev/md*")` 枚举阵列——exec.Command 直调无 shell，
  glob 不展开，ls 收字面量必 rc=2 → 函数恒返回空 → raidMap 恒空 → RAID 池 healthy 恒 true，
  降级/重建/同步全显示"正常"，Issues/事件永不触发。
- 真机证据（.57，修复前）：md0 raid1 resync 57.1% 时 /api/disk/overview 报 pool healthy=true、无 issues；
  python subprocess 等价复现 `ls "/dev/md*"` rc=2。
- 历史注记：e3c9b4b 修过同类 glob 问题，此处为漏网点；全仓排查确认 `ls` 形参 glob 直调仅此一处
  （其余 md 枚举均正确用 filepath.Glob）。

## 修法（opencode，2773a3c）

- 删除 ls 枚举，直接遍历 parseMdstatBlocks 的块（MdName 匹配 ^md\d+$）构建 RAIDStatus，
  字段填充语义不变；抽纯函数 buildRAIDStatusFromMdstat 供表驱动单测（8 用例：干净/降级(F)/spare(S)/
  resync 百分比取 ] 后/多数组隔离/空输入/md126 多位数命名）。
- mdstat 枚举会含 inactive 阵列（ls 时代靠设备节点过滤）——更正确，下游 raidMap 按 Device 匹配挂载源不受影响。

## Parent 独立验证

- go build ./... / go vet 干净；go test -race 110 PASS / 0 FAIL；覆盖率 21.0% → 21.6%（不降）。
- diff 逐项核读：仅 overview.go + overview_test.go，+153/-35，语义与任务书一致。

## .57 真机全生命周期验证（部署 4193afd5 二进制后）

| 阶段 | 内核状态 | 面板表现 | 判定 |
|---|---|---|---|
| 基线 | md0 [UU] 干净 | healthy=True, issues=[] | ✓ |
| fail sdc | [2/1] [U_] 降级 | healthy=False, issues=["RAID /dev/md0 状态: degraded"] | ✓（修复前恒 True） |
| remove+add sdc | recovery 1.9% | healthy=False, issues=["RAID /dev/md0 正在同步 1.9%"] | ✓ 百分比正确（] 后提取，Item D+E6 联动） |
| recovery 98.5% | [U_] 恢复中 | healthy=False, "正在同步 98.5%"，注册表 40/41+1warn（RAID 项如实报） | ✓ 时序性 warn 非缺陷 |
| 恢复完成 | [2/2] [UU] | healthy=True, issues=None, 注册表 41/41 全 pass | ✓ 闭环 |

.48（LVM merge 池，无 RAID）回归：pool healthy=True、注册表 41/41——无 RAID 机器行为不受影响。

## 两台机器终态（2026-10-28）

- .48：merge LVM 200G /data/nas1，二进制 4193afd5（v1.4.0-beta.33-19-g2773a3c 语义），41/41
- .57：raid1 md0 50G /data/nas1（sdd/sde 空闲），同二进制，41/41
- 验收残留已清（aitest01 orphan 目录、stub 进程、临时脚本、测试用户 f1test）
