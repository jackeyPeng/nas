# 任务：diskmgmt 外部审查五项高危修复

你在 ~/soft/nas 仓库工作。Go module 在 `web/`（go.mod 在 web/ 下，不在仓库根）。以下所有 file:line 已由 parent 对当前 HEAD (7243f4d) 核实过，代码现实优先于本文描述——动手前先通读列出的源文件。

## 硬约束（违反 = 返工）

- 禁止读取仓库外任何文件（包括 ~/go/pkg/mod、/tmp 实验、任何仓库根之外路径）——一次 external_directory 拒绝即整轮作废。测试只用 `go test`（可用 t.TempDir()）。
- 零 CGO、零新依赖。
- 只改 `web/modules/diskmgmt/` 下的文件 + 对应 `_test.go`。禁止碰 third_party/、frontend/、其他模块。
- 不要全仓 gofmt，只保证改动文件 gofmt 干净。
- 本任务书的缺口或自相矛盾之处，写进报告申报，不得静默绕过或自行扩大范围。
- 完成后：构建+测试全绿 → 用下面给定的 commit message 提交一次（不含报告文件）→ 把报告写到仓库根 `TASK-REPORT.md`（不要 git add 它）。

## 构建与测试命令

```
cd web && GOPROXY=https://goproxy.cn,direct go build ./... && go vet ./modules/diskmgmt/ && go test ./modules/diskmgmt/ -count=1
```

已知事实（勿再查证）：`common.SudoExec(name string, args ...string) (string, error)`、`common.SudoOutput` 同签名、`common.ExecOutput` 同签名（web/common/sudo.go）。单文件 vet 对同包兄弟文件符号报 undefined 是假阳性，以整模块 go build 为准。

## 修复项（5 项全做）

### Fix 1 — 空闲盘判定漏「未挂载但有数据的分区」（最高危，数据丢失路径）

现状：`storage.go` getDiskStatus 约 230-265 行，子分区有 FSType 但未挂载时（259-260 行）只置 childDS.Type="data"，父盘 ds.Type 不变；整盘无 FSTYPE 的盘保持 "unused"，经 overview.go 366 行进 FreeDisks，扩容/向导随后对其 wipefs。

改法：
1a. getDiskStatus 子分区循环里：任何 child 的 FSType 非空（含 swap）或 Mountpoint 非空时，若父盘 ds.Type 是 "unused"，改为 "data"。已是 "system" 的父盘不得被覆盖（保持 247-258 行现有语义）。
1b. `isDiskInUse`（storage.go:335）加第三查：目标设备的任一分区带文件系统/swap 签名即返回 true。用 `common.ExecOutput("lsblk", "-n", "-r", "-o", "NAME,FSTYPE", device)` 之类列设备及其子分区的 FSTYPE，任何非空 FSTYPE 行（包括设备自身行）即 in use。把「解析 lsblk 输出判断有无非空 FSTYPE」抽成纯函数（如 `hasFstypeSignature(out string) bool`）以便单测。此改动自动覆盖 pool_extend.go:63 和 maintenance.go:41（换盘）两个调用点，无需改调用方。

单测要求：1a 的分类逻辑若在现函数内不可测，允许把「child 类型推导 + 父盘类型联动」抽成纯函数再测；至少覆盖：整盘无 fs + 子分区有 xfs 未挂载 → 父盘 data；整盘无 fs + 子分区 swap → 父盘 data；系统盘（子分区挂 /）→ 保持 system 不被 data 覆盖；1b 纯函数覆盖：有 xfs 分区 → true、全空 → false、表头/空输出 → false。

### Fix 2 — 选盘参数失效时退化为「全部空闲盘」

现状：`stream.go` 82-91 行，`if len(filtered) > 0` 才收窄，filtered 为空时静默保留全部 unusedDevs，破坏范围扩大。

改法：带 disks 参数（sel 非空）但 filtered 为空时，`sendProgress` 一条 error 事件（Detail 说明「所选磁盘已不是空闲盘，请刷新页面重试」）并 return，绝不回退到全部空闲盘。sel 为空（未传参数）时保持现有行为（用全部 unusedDevs）不变。把过滤逻辑抽成纯函数（如 `filterSelectedDisks(unused []string, sel string) (filtered []string, hasSel bool)`）单测：正常交集、交集为空、sel 为空串、sel 含空白项。

### Fix 3 — 向导失败后仍发「完成」

现状：四个 `setup*Stream`（stream.go:261/317/374/465）签名为 void，内部错误只 sendProgress(error)+return；外层 handleWizardSetupStream 在 switch 之后（164-185 行）无条件发 Status:"complete"。

改法：四个 setup*Stream 签名改为返回 error——内部每个 sendProgress(error)+return 处改为返回非 nil error（错误信息同 Detail）；成功走完返回 nil。外层 switch 改为接收 error，非 nil 直接 return（错误事件已由内层发出，不要重复发），仅 nil 时才发 complete 事件。逐分支核对四个函数内所有 error 事件路径都改为 return err，不得漏。前端不用改。此项无独立单测，靠 go build + 你在报告里逐函数列出「error 路径数 = return err 数」的自查表。

### Fix 4 — LVM 流式扩容吞掉前四步错误

现状：`pool_extend.go` 81-100 行，wipefs/pvcreate/vgextend/lvextend 四步 SudoExec 返回值全丢弃、无条件 stepDone；只有第 5 步查错。

改法：四步各自接 `_, err := common.SudoExec(...)`；err != nil 时 sendPoolExtendProgress 发 error 事件（Step 用当前步骤名，Detail 含命令名和 err 输出摘要）并 return，不再 stepDone。第 5 步现状保留。

### Fix 5 — 删 LVM 池：PV 清理顺序 + fstab 误伤

5a. 现状：`pool.go` 503-514 行先 `vgremove -f`，再用 `pvs -S vg_name=<已删VG>` 查成员——必然查空，pvremove/wipefs 被跳过，磁盘留 LVM2_member 签名变幽灵盘。改法：在 vgremove 之前先执行 pvs 查询把成员盘列表存下来（照抄同函数 RAID 分支 525-527 行 "Get member disks BEFORE stopping" 的顺序意识），vgremove 之后对存下的列表逐个 pvremove -f + wipefs -a。

5b. 现状：pool.go 555-577 行 fstab 清理按前缀 `/data/nas` 删所有行，separate 模式多池机器删一个池会抹掉其他保留池的开机挂载。改法：只删属于被删池的行，判据二选一命中即删：(a) 挂载点（fields[1]）精确等于 deletedMountPoints 中某项；(b) 仅 LVM 分支——源设备（fields[0]）以 `/dev/mapper/<vgName>-` 或 `/dev/<vgName>/` 为前缀。其余行一律保留。RAID/single 分支只用判据 (a)。

单测要求：fstab 过滤逻辑抽成纯函数（如 `filterFstabForDeletedPool(fstab string, deletedMounts []string, lvmVgName string) string`，lvmVgName 为空表示非 LVM 分支只用判据 a），覆盖：多 nas* 挂载点只删目标、LVM mapper 行按 vgName 删除、其他池行保留、注释/空行原样保留、无匹配时输出与输入一致。

## Commit message（原样使用，一次提交）

```
fix(diskmgmt): 外部审查五项高危修复 — 空闲盘误判存量数据分区/选盘空集退化全盘/向导失败仍报完成/扩容吞错/删池PV顺序与fstab误伤
```

## 报告（TASK-REPORT.md，不入库）

内容：每项 fix 的改动摘要与涉及文件、Fix 3 的逐函数 error 路径自查表、测试清单与 `go test -v` 实际 PASS 计数、构建输出、任何偏离本任务书之处及理由、发现但未修的问题（flag 不动手）。
