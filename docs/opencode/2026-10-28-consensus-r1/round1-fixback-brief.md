# 任务：consensus R1 审查修复轮（diskmgmt）

你在 ~/soft/nas 仓库工作。基线 = HEAD (5f9be5c)。上一轮四个提交（ad27508/5304c05/7f4005a/5f9be5c）经独立审查，裁决 request_changes：0 critical、2 important、若干 minor。本轮只做下列编号修复，不扩大范围。先读涉及文件——代码现实优先于本文本；发现矛盾在报告申报，不得静默绕过。

## 硬约束（违反 = 返工）

1. 禁止读取/修改仓库外任何文件（含 ~/go/pkg/mod、/tmp）——auto-reject 会终止整个 run。验证只用 web/ 内 go build / go vet / go test。
2. 只修改 web/modules/diskmgmt/ 下 Go 代码与测试，以及仓库根 TASK-REPORT.md（报告不 git add）。不碰 frontend/、scripts/、其他模块。
3. 禁止执行真实磁盘 / sudo / systemctl / mdadm / lvm 命令。
4. `git add` 只加你改动的源码/测试文件；单个 commit，message 原样用文末给出的。
5. 偏差/缺口 → 报告申报，由 parent 裁决。

## Fix 1（Important）：raid 删池分支 mdadm --detail 失败必须安全拒绝

现状（pool.go:607）：`detailOut, _ := common.SudoOutput("/usr/sbin/mdadm", "--detail", poolDevice)` 错误被吞。detail 失败 → memberDisks 为空 → anySystemDisk 闸门空转通过 → umount、mdadm --stop 照常执行，而成员盘的 --zero-superblock/wipefs 全部被跳过，留下带 RAID 超级块的幽灵成员盘（可能被开机自动重组）。与同函数 lvm 分支对 pvs 失败的安全拒绝（pool.go:557 附近）不对称。

修法（对齐 lvm 分支模式）：
1. `detailOut, detailErr := ...`；detailErr != nil → 审计日志记 rejected + jsonError(HTTP 500, "无法确认阵列 %s 的成员（mdadm --detail 执行失败），为安全起见拒绝删除: %s")，return，不执行任何 umount/--stop。
2. detailErr == nil 但 parseMdMemberDisks 结果为空 → 同样安全拒绝（jsonError 500，文案说明"未能解析出阵列成员"）——能被 overview 识别为 raid 池的阵列必然有成员，解析为空说明状态异常。
3. 拒绝必须发生在该分支第一条破坏性命令（umount）之前。
4. 测试：parseMdMemberDisks 的空输出/畸形输出用例补齐（若已有则确认覆盖）；handler 级拒绝路径受限于无 exec 注入点，无法直接测——在报告里如实说明，不得虚报。

## Fix 2（Important）：TASK-REPORT.md 声明修正（诚实性）

1. "共 11 个测试函数" → 实际 10 个，改正。
2. 上一轮报告声称"对 handlePoolDelete 三分支系统盘闸门的集成验证（通过可注入 isSystemDisk 替身）"不实：isSystemDisk 是普通包级函数不可注入，实际只有 helper 级 TestAnySystemDisk + 未匹配池/确认名两个 handler 拒绝测试。把该段改写为如实描述：闸门为 helper 级覆盖，handler 级覆盖限于非破坏性拒绝分支；并补充本轮 Fix 1 新增的安全拒绝逻辑说明。
3. 报告追加「本轮审查修复」小节：逐 Fix 记录改动与验证。报告仍不 git add。

## Fix 3（Minor）：stream.go 重置路径复用 wipeManagedTargets

现状：stream.go:615-624 内联复制了擦盘循环（isSystemDisk 跳过 + zero-superblock + wipefs），与 reset.go:117 使用的 wipeManagedTargets 平行实现，且跳过系统盘时无日志。

修法：stream.go 改为调用 wipeManagedTargets(managedTargets)，把返回的 steps 逐条（或合并）通过 sendProgress 以 Detail 呈现（含「跳过系统盘」条目）；删除内联循环。保持 SSE 事件结构与 step 计数不变（totalSteps 不改）。

## Fix 4（Minor）：重置采集阶段 pvs/mdstat 失败要出声

现状：reset.go:37-38 / stream.go:540-542 采集 pvs、cat /proc/mdstat 的错误被 `_` 忽略。pvs 失败时 managedTargets 近乎为空，重置照常"成功"但纳管盘不被擦除（幽灵盘），全程静默。

修法：两处采集都接收 error；失败**不中止**重置（无 LVM 的机器上 pvs 缺失是合法状态），但必须记警告：reset.go 追加 steps 条目「无法枚举纳管盘（pvs/mdstat 失败）: <err>，擦除集合可能不完整」；stream.go 用 sendProgress 发一条 running 状态、Detail 含同样警告的事件（不新增 step、不改 totalSteps）。采集失败的警告文案要能让用户/审计看出"擦除不完整"。

## 明确不做（本轮范围外，已记录）

- extractVGName 对 /dev/mapper 形态的 VG 名提取缺陷（既有边缘场景，另轮处理）。
- inactive MD 阵列成员不进纳管集合（符合任务书"活跃阵列"措辞，另轮评估）。
- 不引入 exec 注入层/大规模可测性重构。

## 验证（必须执行，输出贴报告）

```
cd web && go build ./... && go vet ./modules/diskmgmt && go test -race ./modules/diskmgmt -count=1 -cover
```

基线覆盖率 20.8%，不得下降；全部既有测试通过。

## 提交（原样 message）

```
fix(diskmgmt): 审查回炉 — raid删池成员查询失败安全拒绝/流式重置复用统一擦盘helper/采集失败警告 (consensus R1审查)
```
