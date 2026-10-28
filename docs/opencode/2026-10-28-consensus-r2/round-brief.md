# 任务：consensus R2 — RAID 状态链 sysfs 化 + extractVGName mapper 形态 + inactive 阵列纳管

你在 ~/soft/nas 仓库工作。基线 = HEAD (7d0c417)。先读每条列出的源文件——代码现实优先；与本文不符在报告申报，不得静默绕过。

## 硬约束（违反 = 返工）

1. 禁止读取/修改仓库外任何文件（含 ~/go/pkg/mod、/tmp）——auto-reject 终止 run。验证只用 web/ 内 go build / go vet / go test。
2. 只修改 web/modules/diskmgmt/ 下代码与测试（可新增文件）。不碰 frontend/、scripts/、其他模块。
3. 禁止执行真实磁盘/sudo/mdadm/lvm 命令；sysfs/mdstat 数据形态用下方真机 fixture，不要试图读本机 /sys。
4. 报告写仓库根 TASK-REPORT.md（覆盖，不 git add）。
5. git add 只加你改动的文件；三个 Item 各自独立 commit，message 原样。
6. 既有全部测试必须过；覆盖率基线 21.6% 不得下降。

## Item A（主项）：RAID 状态采集 sysfs 优先，mdstat 降为回退

现状：getRAIDStatus（overview.go，上轮 2773a3c 已改为遍历 parseMdstatBlocks）与 raid_expand.go 的
handleRAIDExpandFS / handleRAIDReshapeStatus / waitForResync 全部依赖 /proc/mdstat 文本解析。
共识报告阶段 1 要求：优先读内核结构化 sysfs，mdstat 仅作兼容回退；健康与活动状态分离。

### 真机 fixture（10.216.10.57，md0 raid1 2×50G，Debian 13 内核，2026-10-28 实采）

sysfs 目录 /sys/block/md0/md/ 含：level array_state degraded raid_disks sync_action sync_completed
sync_speed reshape_position last_sync_action metadata_version uuid mismatch_cnt chunk_size
consistency_policy rd0/ rd1/（每个含 state）等。**注意没有 raid_level 文件，层级在 `level`。**

```
[干净态]      level=raid1 array_state=clean   degraded=0 sync_action=idle    sync_completed=none sync_speed=none reshape_position=none rd0=rd1=in_sync
[降级态]      level=raid1 array_state=clean   degraded=1 sync_action=idle    sync_completed=none sync_speed=none last_sync_action=recover  仅 rd0 存在(in_sync)——failed 成员的 rd* 目录消失！mdstat 同刻=[2/1] [U_]
[重建态]      level=raid1 array_state=clean   degraded=1 sync_action=recover sync_completed=0 / 104790016 sync_speed=200213 rd0=in_sync rd1=spare  mdstat 同刻=recovery = 2.2%
[恢复完成]    degraded=0 sync_action=idle array_state=clean
```

语义陷阱（全部实测）：
- **降级时 array_state 仍是 clean**——健康判定必须看 degraded 计数，不能信 array_state（共识报告"降级阵列仍显示 active"的 sysfs 版）。
- **sysfs sync_action 值是 `recover`，mdstat 文本是 `recovery`**——映射别搞混。sync_action 可能值：idle/resync/recover/reshape/check/repair。
- sync_completed 格式 `<done> / <total>`（KB），或 `none`；百分比=done/total，与 mdstat 的百分比单位不同但各自内部一致。
- sync_speed 单位 K/sec，或 `none`。

### 修法

1. 新文件 raid_sysfs.go：
   - `readRAIDSysfsValues(mdName string) (map[string]string, map[string]string, bool)`：读 /sys/block/<md>/md/ 下标量文件（level/array_state/degraded/raid_disks/sync_action/sync_completed/sync_speed/reshape_position/last_sync_action）与 rd*/state 成员表；目录不存在返回 ok=false。
   - 纯函数 `buildRAIDStatusFromSysfs(mdName string, vals, members map[string]string) RAIDStatus`：供表驱动单测注入上方 fixture。
   - 枚举：`filepath.Glob("/sys/block/md*/md")` 提取 md 名（匹配 ^md\d+$）。
2. RAIDStatus 新增字段（既有 json 字段名与语义全部保留，向后兼容）：
   - `Health string json:"health"`：degraded>0 或 FailedDisks>0 → "degraded"；array_state=="inactive" 或 degraded==raid_disks → "failed"；否则 "normal"。
   - `Activity string json:"activity"`：sync_action 原值（idle/resync/recover/reshape/check/repair）。
   - State 兼容派生：degraded/failed → "degraded"；sync_action != idle → "resyncing"；否则沿用 array_state。SyncPercent 从 sync_completed 计算，保持现有 "N.N%" 字符串形态。
3. getRAIDStatus：sysfs 枚举优先；sysfs 不可用（目录缺失/空且 mdstat 有阵列）时回退 buildRAIDStatusFromMdstat（保留，不删）。
4. buildStorageOverview 池健康（overview.go:203-209）：改为 healthy = Health=="normal" && Activity=="idle"（对回退路径：mdstat 构造的 RAIDStatus 同样填 Health/Activity，语义一致）。Issues/事件文案保持现有格式（"RAID %s 正在同步 %s" / "状态: degraded"）。
5. raid_expand.go 三处：
   - waitForResync：优先轮询 sysfs sync_action；完成判据=连续 3 轮（cleanStreak）sync_action==idle；**保留**首轮 sleep 2s、600 轮上限、超时返回 error 语义；sysfs 不可用回退现有 mdstat parser 路径。进度事件文案沿用现有格式（百分比从 sync_completed 算）。
   - handleRAIDReshapeStatus：reshaping = sync_action != idle；progress 用 sync_completed 百分比；speed = sync_speed+"K/sec"；eta 由 (total-done)/speed 估算（分钟、一位小数；speed 为 0/none 时置空）——mdstat 的 finish= 不再是唯一来源，报告里说明估算方式。sysfs 不可用回退 mdstat。
   - handleRAIDExpandFS 闸门：sync_action != idle 或 reshape_position != none/0 → 拒绝 growfs（保持现有错误响应形态）；回退路径沿用 parseMdstatBlocks 块内进度行判定。
6. 测试：buildRAIDStatusFromSysfs 表驱动（用上方四态 fixture 逐字构造 map）；sync_completed 解析纯函数（"none"/"0 / 104790016"/畸形）；Health/Activity 派生表；waitForResync 的 sysfs 轮询逻辑若无法脱离真实文件测试，抽"给定 sync_action 序列 → 完成判定/超时"的纯函数测（报告说明）。

commit message（原样）：

```
fix(diskmgmt): RAID 状态链 sysfs 优先采集 — 健康/活动分离,mdstat 降为回退 (consensus 阶段1)
```

## Item B：extractVGName mapper 形态 + 删池 lvm 分支空成员安全拒绝

现状（已核实）：overview.go:608 extractVGName 两个分支都返回 filepath.Base(device)——
`/dev/mapper/vg_nas-data` 会得出 "vg_nas-data"（错 VG 名），`pvs -S vg_name=` 查空但不报错，
删池 lvm 分支成员闸门空转。调用点：overview.go:216、pool.go:546、pool.go:699。

修法：
1. extractVGName 重写为纯函数并表驱动测试：
   - `/dev/vg_nas` → `vg_nas`（base，现状保持）
   - `/dev/vg_nas/data` → `vg_nas`（LV 直路径取父目录名）
   - `/dev/mapper/vg_nas-data` → `vg_nas`（LVM mapper 命名：VG 与 LV 间第一个"落单"的 `-` 分隔；VG/LV 名内的 `-` 被转义为 `--`，先还原）
   - `/dev/mapper/my--vg-data` → `my-vg`（转义还原用例）
   - `/dev/mapper/control` → `""`（control 设备特判）
2. handlePoolDelete lvm 分支：解析出 vgName 后，在**第一条破坏性命令（lvremove 循环）之前**：
   - `vgs --noheadings -o vg_name` 输出中无该 vgName → jsonError 409 "卷组 X 不存在或名称解析失败"，审计 rejected；
   - 既有 pvs 失败拒绝保留；**新增** pvs 成功但 memberPVs 为空 → jsonError 500 安全拒绝（真实存在的 VG 必有成员 PV，空集=名称/状态异常）。
3. 注意 overview.go:216（池显示名）与 pool.go:699（扩容路径）共用 extractVGName——行为变化对它们同样生效，报告说明各调用点影响。

commit message（原样）：

```
fix(diskmgmt): extractVGName 支持 mapper/LV 路径形态 + 删池 lvm 分支 VG 校验与空成员安全拒绝 (consensus minor3)
```

## Item C：inactive MD 阵列成员纳入重置擦盘集合

现状（已核实）：managed_wipe.go:26 mdstatMemberDisks 只认含 " : active" 的行——
inactive 阵列（成员盘仍带 RAID 超级块）不进纳管集合，重置后残留签名可能被开机自动重组（幽灵阵列）。

修法：mdstatMemberDisks 改走既有 parseMdstatBlocks（其块切分不区分 active/inactive，md 名匹配即可），
从块 MemberTokens 提取盘名加 /dev/ 前缀，保持去重与返回形态不变；若 parseMdstatBlocks 对 inactive
头行有过滤（核实其实现），一并修正。fixture 表驱动测试补：inactive 阵列、active+inactive 混合、
成员带 (F)/(S) 标志（标志不影响成员归集——重置要擦的就是所有成员盘）。

commit message（原样）：

```
fix(diskmgmt): 重置纳管集合覆盖 inactive MD 阵列成员 — 防幽灵阵列残留 (consensus minor4)
```

## 验证（必须执行，输出贴报告）

```
cd web && go build ./... && go vet ./modules/diskmgmt && go test -race ./modules/diskmgmt -count=1 -cover
```

## 报告（TASK-REPORT.md，不 git add）

逐 Item：根因/修法选择、新增与修改测试清单（名称+数量）、真机 fixture 与实现语义的对应说明
（尤其 array_state=clean+degraded=1、recover/recovery 映射、sync_completed 单位）、
waitForResync/reshape-status 回退路径保留说明、覆盖率、偏差与申报。
