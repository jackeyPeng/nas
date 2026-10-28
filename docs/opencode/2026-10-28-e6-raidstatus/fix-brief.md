# 任务：修复 getRAIDStatus 枚举失效 — ls glob 不经 shell 展开导致 RAID 健康监控全盲

你在 ~/soft/nas 仓库工作。基线 = HEAD (c751c3c)。缺陷已在真机实锤（10.216.10.57，md0 raid1 resync 57% 时面板池健康仍报 true），修法明确。先读涉及文件；与本文不符在报告申报，不得静默绕过。

## 硬约束（违反 = 返工）

1. 禁止读取/修改仓库外任何文件（含 ~/go/pkg/mod、/tmp）——auto-reject 终止 run。验证只用 web/ 内 go build / go vet / go test。
2. 只修改 web/modules/diskmgmt/ 下代码与测试。不碰 frontend/、scripts/、其他模块。
3. 禁止执行真实磁盘/sudo/mdadm 命令。
4. 报告写仓库根 TASK-REPORT.md（覆盖，不 git add）。
5. git add 只加你改动的文件；单个 commit，message 原样用文末给出的。

## 缺陷（真机实锤）

web/modules/diskmgmt/overview.go:848 `getRAIDStatus()` 第一步：

```go
out, err := common.ExecOutput("ls", "/dev/md*")
if err != nil {
    return arrays   // ← 永远走这里
}
```

`common.ExecOutput` 是 exec.Command 直调（common/sudo.go:43），**没有 shell，`/dev/md*` 通配不展开**。ls 收到字面参数报"无法访问 '/dev/md*': 没有那个文件或目录"，rc=2 → err != nil → 函数永远返回空切片。真机验证（python subprocess 等价复现）：rc=2。

后果链：getRAIDStatus 恒空 → buildStorageOverview（overview.go:181 唯一调用方）的 raidMap 恒空 → RAID 池健康判定 `if ra, ok := raidMap[poolDev]; ok`（overview.go:205-209）永不命中 → **RAID 池 healthy 恒为 true**：降级、缺盘、resync/recovery/reshape 中全部显示"正常"；overview.go:457 的"RAID 正在同步"Issues、overview.go:523 的重建事件同样永不触发。RAID 健康监控整条链路线上全盲。

历史注记：仓库曾有 e3c9b4b "fix: RAID pool member disks missing - Go exec does not expand glob" 修过同类问题，此处是漏网点。全仓 grep 核实：其余 md 设备枚举都正确用 filepath.Glob（maintenance.go:110/156、overview.go:769、reset.go:174），`ls /dev/md*` 形参 glob 直调仅此一处。

## 修法

1. **枚举源改为 mdstat 块本身**：getRAIDStatus 已经读 /proc/mdstat 并调 parseMdstatBlocks（mdstat_parser.go:50，Item D 产物）——删除 `ls /dev/md*` 调用与基于其输出的外层循环，改为直接遍历 blocks：凡 MdName 匹配 `^md\d+$` 的块各构建一个 RAIDStatus（Device = "/dev/"+MdName）。块内字段填充逻辑（HeaderFields 取 State/Level、MemberTokens 经 memberFlag 分桶 Disks/ActiveDisks/SpareDisks/FailedDisks、FailedDisks>0 置 degraded、进度行取 SyncPercent + State="resyncing"）**保持现有语义不变**，只换枚举来源。
2. **可测性**：把"mdstat 文本 → []RAIDStatus"抽成纯函数（如 `buildRAIDStatusFromMdstat(content string) []RAIDStatus`），getRAIDStatus 退化为读文件 + 调它（os.ReadFile 失败传空串 → 返回空切片，与旧行为一致）。
3. **注意**：mdstat 枚举会包含 inactive/未挂载设备节点的阵列（ls 时代靠设备节点存在性过滤）——这是可接受且更正确的行为（阵列在 mdstat 有块即有状态可报）；下游 raidMap 按 Device 字符串匹配挂载源，不受影响。在代码注释里说明这一点。
4. parseMdstatBlocks/findBlockByMdName/memberFlag 等 Item D 既有函数与 raid_expand.go 的四个调用点**不要动**（findBlockByMdName 若因此不再被 getRAIDStatus 使用，保留——raid_expand.go 仍在用）。

## 测试

`buildRAIDStatusFromMdstat` 表驱动测试（fixture 用真实 mdstat 文本形态），至少覆盖：
- 干净 raid1（[UU]，无进度行）→ State=active/clean 语义、ActiveDisks=2、SyncPercent 空
- 降级阵列（含 (F) 成员）→ FailedDisks=1、State="degraded"
- 含 (S) spare → SpareDisks=1
- resync 中（进度行在 blocks 行之后、百分比在 ] 之后）→ SyncPercent 非空、State="resyncing"
- 多数组并存 → 各自独立、互不污染
- 空输入 / 只有 Personalities+unused devices 尾行 → 返回空切片不 panic
- md126 之类多位数命名 → 正常识别
回归：既有 overview/raid 相关测试全过；如既有测试对 getRAIDStatus 的 ls 行为有隐含依赖，报告说明。

## 验证（必须执行，输出贴报告）

```
cd web && go build ./... && go vet ./modules/diskmgmt && go test -race ./modules/diskmgmt -count=1 -cover
```

基线覆盖率 21.0%，不得下降。

## 提交（原样 message）

```
fix(diskmgmt): getRAIDStatus 枚举改走 mdstat 块 — ls /dev/md* 无 shell 展开恒失败致 RAID 健康监控全盲

原因: ExecOutput 直调 exec.Command 无 shell,glob 字面量必报错,getRAIDStatus 恒返回空 → raidMap 恒空 → RAID 池 healthy 恒 true,降级/重建/同步全部显示正常,Issues/事件永不触发(真机实锤: md0 resync 57% 面板报健康)
修复:
- 删除 ls /dev/md* 枚举,直接遍历 parseMdstatBlocks 的块构建 RAIDStatus(mdstat 有块即有状态,含 inactive 阵列)
- 抽纯函数 buildRAIDStatusFromMdstat 供表驱动单测,块内字段填充语义不变
影响范围: 存储总览 RAID 池健康/告警/事件链;raid_expand.go 调用点不动
```

## 报告（TASK-REPORT.md，不 git add）

根因确认（含真机证据引用）、修法、新增/修改测试清单、覆盖率、既有测试回归情况、偏差与申报。
