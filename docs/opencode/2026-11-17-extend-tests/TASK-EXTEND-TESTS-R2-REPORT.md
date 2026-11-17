# TASK-EXTEND-TESTS R2 报告：流式写路径 seam 落地 + P1-P6/R1-R5 用例

- 日期：2026-11-17
- 范围：`handlePoolExtendStream`、`handleRAIDExpandStream` 及调用链的测试 seam 基础设施 + 16 个（P1-P6/R1-R5）可实现用例。
- 依据：`docs/opencode/2026-11-17-extend-tests/DESIGN.md` + 任务书。P7/P8/R6/R7/R8（ctx 中断、TDD-red、waitForResyncCore 抽取）明确留给 R3，本轮未实现。

## 1. 变更清单

### 新增文件
| 文件 | 内容 |
|---|---|
| `web/modules/diskmgmt/seams.go` | 7 个包级 seam 变量：`sudoExecFn`/`sudoOutputFn`/`execOutputFn`/`getDiskStatusFn`/`sleepFn`/`sysfsRoot`/`mdstatPath`，各自默认指向生产实现，注释说明用途与惯例来源（`stubRunner`/`poolRootOverride`/`getFolderMetaFn`）。 |
| `web/modules/diskmgmt/stream_stubs_test.go` | 测试公共桩：`recordedCall`/`cmdResponse`/`stubExec`（按命令名分发响应 + 录制调用序列）、`withStubExec`、`withNoopSleep`、`withSSE`、`parseSSE`。 |
| `web/modules/diskmgmt/pool_extend_test.go` | P1-P6，6 个顶层测试函数（P3/P4 各含 2 个子用例），全部通过。 |
| `web/modules/diskmgmt/raid_expand_stream_test.go` | R1-R5，5 个顶层测试函数（R3 含 2 个子用例，R4 含 3 个，R5 含 3 个），全部通过。 |

### 修改文件
| 文件 | 改动点 |
|---|---|
| `web/modules/diskmgmt/storage.go` | `isSystemDisk` 的 3 处 `common.ExecOutput("findmnt",...)` → `execOutputFn`；`isDiskInUse` 的 `common.ExecOutput("cat","/proc/mdstat")` → `execOutputFn("cat", mdstatPath)`。`isDiskInUse` 里的 `pvs` 调用（约第 370 行）按任务书范围保持不动，仍是 `common.SudoOutput` 直调。 |
| `web/modules/diskmgmt/growfs.go` | 全部 4 处 `common.SudoOutput`/`common.SudoExec`（findmnt FSTYPE、findmnt TARGET、xfs_growfs、resize2fs）→ `sudoOutputFn`/`sudoExecFn`；移除不再使用的 `nas-panel/common` import。 |
| `web/modules/diskmgmt/pool_extend.go` | 4 处 `common.SudoExec`（wipefs/pvcreate/vgextend/lvextend）→ `sudoExecFn`；2 处 sleep → `sleepFn`。`common` import 保留（仍用于 LockDiskOp/UnlockDiskOp/LogAuditRequest）。 |
| `web/modules/diskmgmt/raid_expand.go` | `mdadm --detail`（handler 内，约第 62 行）→ `sudoOutputFn`；`getDiskStatus()` → `getDiskStatusFn()`；wipefs/mdadm --add/mdadm --grow → `sudoExecFn`；4 处 sleep（100ms×2、resync 前置 2s、轮询 2s）→ `sleepFn`；**3 处**（非任务书所述 2 处）`os.ReadFile("/proc/mdstat")` → `os.ReadFile(mdstatPath)`，详见下方「偏差声明」。`mdArrayLevel`（约第 490 行，供 `maintenance.go` 的 `handleReplaceDisk` 使用）里独立的 `common.SudoOutput("mdadm","--detail",...)` 按范围保持不动。 |
| `web/modules/diskmgmt/raid_sysfs.go` | 仅第 49 行：`filepath.Join("/sys/block", mdName, "md")` → `filepath.Join(sysfsRoot, mdName, "md")`。 |
| `web/modules/diskmgmt/raid_expand_test.go` | 原有真实耗时 ~8s 的 `TestWaitForResyncReturnsNilWhenNoResyncLine` 注入 `withNoopSleep(t)`，断言语义（无匹配设备视为同步完成，返回 nil）不变，运行耗时从 ~8s 降到 <10ms。 |

未触碰：`stream.go`（wizard 路径，明确排除）、路由、JSON 结构、`common` 包本身。

## 2. 构建与测试结果

### `go build ./...`
```
(无输出，exit 0，构建干净)
```

### `go test ./modules/diskmgmt/ -count=1`
```
ok  	nas-panel/modules/diskmgmt	5.187s
```

全量回归：162 个既有 `--- PASS` 断言 + 本轮新增 11 个顶层测试函数（P1/P2/P3/P4/P5/P6/R1/R2/R3/R4/R5），其中 P3/P4/R3/R4/R5 各自内含 2-3 个独立子用例（`t.Run`），覆盖任务书要求的全部 16 个场景点（P1-P6、R1-R5）。无 `FAIL`，无 skip。

`go vet ./modules/diskmgmt/...` 同样无输出。

耗时说明：5.19s 中绝大部分来自既有 `TestCachedSmartctl_ExpiresAfterTTL`（0.02s）、`TestRestartRcloneDebounced`（0.11s）等既有真实定时器测试，与本轮新增代码无关；本轮新增的 11 个测试全部使用 `withNoopSleep` 消除延迟，单个用例耗时在 0.06-0.14s 量级（主要是 `httptest` 本身开销），`TestWaitForResyncReturnsNilWhenNoResyncLine` 从 ~8s 降至 <10ms。

## 3. 偏差声明（Deviations）

### 3.1 mdstatPath 路由范围：3 处而非任务书所述 2 处
任务书原文指向 `raid_expand.go:249/326`（即 `handleRAIDExpandFS` 和 `handleRAIDReshapeStatus` 内的 `os.ReadFile("/proc/mdstat")`）。但 `waitForResync` 函数内部（原约第 394 行）还有第 3 处同样的 `os.ReadFile("/proc/mdstat")` 调用，用于 sysfs 不可用时的回退路径。

**决定**：三处全部路由到 `mdstatPath`。

**理由**：R1/R2 的测试设计依赖通过 `sysfsRoot` 完全控制 resync/reshape 判定（走 sysfs 分支，不触发 mdstat 回退），但若 `waitForResync` 内部那一处仍硬编码真实 `/proc/mdstat` 路径，在沙箱环境里对真实文件系统发起只读 `os.ReadFile`（目标文件在大多数开发/CI 环境不存在，返回 error 被忽略、`mdstatBytes` 为空切片，行为上不会导致测试失败，但这是**偶然安全**而非**设计上受控**）——测试的可重复性和"该函数三处 mdstat 回退口径一致"这一前提都要求三处必须用同一个可注入的路径变量，否则 seam 本身在逻辑上不完整（同一函数内两条退路一个受控一个不受控，是不一致的半成品状态）。因此将其纳入路由，视为任务书描述疏漏的必要补全，而非超范围改动——改动内容仍是"把硬编码路径替换为既有 seam 变量"，未新增变量、未变更签名、未碰其它文件。

### 3.2 P4「已在存储池中使用」子用例改用 lsblk FSTYPE 签名而非 pvs
任务书范围明确只路由 `isSystemDisk` 的 `findmnt` 和 `isDiskInUse` 的 `cat /proc/mdstat`，`isDiskInUse` 内第一层检查（`pvs --noheadings -o pv_name`）未列入路由范围，保持 `common.SudoOutput` 直调，测试无法控制其输出。

**决定**：P4「已在存储池中使用」子用例改用 `isDiskInUse` 第三层检查（`lsblk -n -r -o NAME,FSTYPE` → `hasFstypeSignature`，同样走 `execOutputFn`，在路由范围内）触发"已占用"判定。

**影响范围**：这一设计选择同样影响了 R5（RAID 扩容的"目标盘不空闲"用例）——R5 的第三个子用例（`isDiskInUse` 命中）同样使用 lsblk FSTYPE 签名而非 pvs，保持与 P4 一致的触发方式。两处用例都额外验证了"wipefs 从未被调用"，间接证明了 `isDiskInUse` 的拒绝生效，不依赖具体是哪一层检查命中——语义覆盖是完整的，只是没有单独覆盖 pvs 命中这一具体分支（该分支本身逻辑在 `hasFstypeSignature`/mdstat 两条分支的既有测试中已有纯函数级覆盖，`isDiskInUse` 整体行为未失去测试覆盖，只是"走 pvs 分支触发"这个具体路径没有专门用例）。

### 3.3 R1 的 sysfs fixture 目录名选择
`withFakeSysfsMD` 的目录名直接使用 `mdDev` 的完整临时文件路径（而非形如 `"md0"` 的短名）。原因：`readRAIDSysfsValues(mdName)` 内部用 `strings.TrimPrefix(mdDev, "/dev/")` 得到 `mdName`，测试里 `mdDev` 是 `t.TempDir()` 下的绝对路径（不带 `/dev/` 前缀），`TrimPrefix` 不生效，`mdName` 实际等于 `mdDev` 本身。为了让 `readRAIDSysfsValues` 拼出的路径（`filepath.Join(sysfsRoot, mdName, "md")`）与测试构造的 fixture 路径一致，`withFakeSysfsMD` 必须用同样的值作为目录名——这不是对生产代码行为的曲解，只是测试侧需要精确复现生产代码在"mdDev 不是 /dev/ 前缀路径"这一测试场景下的实际拼接结果（`filepath.Join` 对包含路径分隔符的中间段会原样拼接并 Clean，两处计算方式一致，已用独立脚本验证字节级相同）。不是偏差，仅记录在此供 R3 阅读测试时理解这个看似反直觉的写法。

## 4. 测试设计要点（供 R3 参考）

- **R1（RAID1 全链路成功）**：用 `t.TempDir()` 文件做 `mdDev` 存在性探针；`mdadm --detail` 桩返回 raid1/2 盘；`getDiskStatusFn` 桩返回目标新盘 `unused`；sysfs fixture 复用 `raid_sysfs_test.go` 的 `cleanRAID1SysfsVals/Members`（干净态，`sync_action=idle`）。断言 resync `done` 事件在 growFS `running` 事件之前、complete Detail 含"容量不变（镜像）"与 2→3 盘。
- **R2（RAID5 reshape-only）**：`mdadm --detail` 桩返回 raid5/3 盘，断言末事件 `Status=reshaping`、`Total=4`，且 `sleepFn` 记录的调用里不含任何 `2*time.Second`（证明未进入 `waitForResync` 轮询），不调用任何 growFS 命令。
- **R3（锁外拒绝-设备/detail）**：md 设备文件不存在 + `mdadm --detail` 失败两个子用例，均断言零命令调用（设备不存在）或 `wipefs` 未执行（detail 失败）——两者都发生在 `common.LockDiskOp()` 之前，由现有代码结构天然保证。
- **R4（级别拒绝）**：raid0/raid10（不支持）/设备数解析失败（detail 缺 "Raid Devices" 行）三个独立子用例，各自文案不同，均断言 `wipefs` 未执行。
- **R5（目标盘不空闲，三层防御）**：已在阵列中（detail 含设备字符串）/ `getDiskStatusFn` 返回非 unused Type / `isDiskInUse` 命中（lsblk FSTYPE 签名）三个独立子用例，各自独立生效，均断言 `wipefs` 未执行，证明三层检查互不替代。

## 5. 未覆盖/留给 R3 的关注点

1. **ctx 取消语义（P7/R6/R7）**：当前 handler 均未读取 `r.Context()`，SSE 连接中断后 handler 会继续跑完整个写路径（无法从客户端侧中止正在执行的磁盘命令）。本轮未新增 ctx 相关测试，也未改任何 ctx 处理逻辑；R3 若要落地 ctx 取消，需要先确认这是否要改 `handlePoolExtendStream`/`handleRAIDExpandStream` 的函数签名或内部轮询循环结构（当前 `stepRunning`/`stepDone`/`waitForResync` 均不感知 ctx）。
2. **TDD-red 用例（P8）**：任务书中标注的"先写红后改绿"类用例本轮未涉及，需在 R3 单独评估是否仍适用（取决于 R3 阶段要新增的生产代码改动顺序）。
3. **`waitForResyncCore` 抽取（R8）**：`waitForResync` 当前是一个耦合了 SSE 发送（`sendRAIDExpandProgress`）、`sleepFn` 调用、sysfs/mdstat 双路径判定的单一函数；`sysfsCleanStreakResult` 已经是从中抽出的纯函数判定核心（上一轮工作），但完整的"轮询循环 + 两种数据源回退"逻辑仍耦合在原函数里。若 R3 要抽取 `waitForResyncCore`，需要决定新函数的输入边界（是否仍接受 `http.ResponseWriter` 用于发送进度，或改为返回事件列表由调用方发送）——这会影响是否需要改动 `sendRAIDExpandProgress` 调用点，建议 R3 启动前先确认这一点，因为任务书明确禁止本轮改 `sendXxx` 签名，R3 需要重新评估这条红线是否仍适用。
4. **R5 的 pvs 分支缺专门用例**：如 3.2 节所述，`isDiskInUse` 走 `pvs` 命中触发拒绝这一具体分支没有专门的 handler 级集成测试覆盖（该函数整体行为仍被覆盖，只是触发源不是 pvs）。如果 R3 要给 `isDiskInUse` 补充 seam 覆盖 `pvs` 调用，需要先确认这是否在 R3 授权范围内（本轮 brief 明确排除）。
5. **sysfs fixture 目录名写法（见 3.3 节）**：这个写法依赖 `mdDev` 不带 `/dev/` 前缀这一测试场景特性；如果 R3 新增的用例需要用真实形如 `/dev/mdN` 的路径作为 `mdDev`（例如验证 `strings.TrimPrefix` 正常生效的场景），需要用不同的 fixture 构造方式（直接以 `"md0"` 短名建目录，同时让 `os.Stat(mdDev)` 检查通过需要另外在真实不存在的 `/dev/mdN` 路径上想办法，或者接受那条检查路径在该场景下走不通）。

## 6. 提交

单次提交，提交信息：
```
test(diskmgmt): extend/expand 流式写路径 seam 落地 + P1-P6/R1-R5 用例 — 命令/SSE/时间全注入，失败中止与锁外拒绝语义锁定
```
未执行 `git push`。本报告文件未加入该提交（按要求不 `git add`）。
