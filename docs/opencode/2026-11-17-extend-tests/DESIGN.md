# diskmgmt 流式写路径单测方案：handlePoolExtendStream / handleRAIDExpandStream

> 子代理（deleg_7c589974，2026-11-17）完整设计方案。调研基础：pool_extend.go / raid_expand.go /
> growfs.go / storage.go / raid_persist.go、6 个既有 seam 惯例（raid_persist 的 cmdRunner/raidConfWriter、
> config_sync 的 rcloneRestartCmd、filemgmt 的 poolRootOverride/getFolderMetaFn）、waitForResync 现测试
> 的真实时间坑（无可注入时钟导致现测试只能跑真实 8s）、LogAudit 单测环境 no-op 特性。
> 实施分轮：R2 = seam + 桩 + 全绿用例（P1-P6/R1-R5）；R3 = stepTracker/waitForResyncCore 抽取 +
> TDD-red 转绿（P7/P8/R6/R7/R8）。

## 1. 可测性分析

### 1.1 依赖清单（两 handler 全量盘点）

| 依赖 | 类型 | 现状 | 处置 |
|---|---|---|---|
| `common.SudoExec`（wipefs/pvcreate/vgextend/lvextend/mdadm --add/--grow/xfs_growfs/resize2fs） | 真实 sudo 命令 | 无注入点 | **新 seam**：`sudoExecFn` |
| `common.SudoOutput`（mdadm --detail） | 真实 sudo 命令 | 无注入点 | **新 seam**：`sudoOutputFn` |
| `common.ExecOutput`（findmnt/pvs/lsblk/cat /proc/mdstat） | 真实命令，被 `isSystemDisk`/`isDiskInUse` 间接调用 | 无注入点 | **新 seam**：`execOutputFn`（同时让两个安全检查函数免单独 seam） |
| `growFilesystemByDevice` | 内部调 SudoOutput/SudoExec | 无注入点 | 经上面两个 seam 即可控制，无需单独 seam |
| `getDiskStatus()` | lsblk 挂链 | 无注入点 | **新 seam**：`getDiskStatusFn`（filemgmt `getFolderMetaFn` 同款包级变量） |
| `os.Stat(mdDev)` | 真实文件系统 | 无注入点 | **不需要 seam**：测试用 `t.TempDir()` 造一个普通文件当 `md_device`，路径存在即过检，mdadm 后续走 stub |
| `readRAIDSysfsValues` | 硬编码 `/sys/block/<md>/md` | 无注入点 | **新 seam**：`sysfsRoot` 包级变量（默认 `"/sys/block"`，测试指到 TempDir 造的假 sysfs） |
| `os.ReadFile("/proc/mdstat")`（waitForResync 与 isDiskInUse 的 cat 回退） | 硬编码路径 | 无注入点 | **新 seam**：`mdstatPath` 包级变量 |
| `time.Sleep`（stepDone/stepRunning 各 100ms；waitForResync 前置 2s + 轮询 2s×600） | 真实睡眠 | 无注入点；raid_expand_test 注释明说“无可注入的时钟”导致现测试只能跑真实 8s | **新 seam**：`sleepFn` |
| `common.LockDiskOp/UnlockDiskOp` | 进程内 sync.Mutex | 测试环境无竞争 | 不需要 seam；可选加一条“锁持有期间命令序列不交错”的串行断言（低优先级） |
| `common.LogAuditRequest` → `LogAudit` | 非阻塞 channel，`auditChan` 未初始化时是 no-op | 单测环境 auditChan 为 nil | **不需要 seam**，方案中注明即可 |
| SSE 写出（`fmt.Fprintf` + `Flusher.Flush`） | `httptest.ResponseRecorder` 实现 `http.Flusher`，现有 waitForResync 测试已实证可行 | 可直接录制响应体后按 `data: {...}\n\n` 行解析 | 无需 seam；**但 `Fprintf` 返回值被丢弃** → 见 1.3 kill 中断缺口 |
| `parseMdDetailLevelAndDevices`、`parseMdstatBlocks`、`resyncProgressDetail` 等纯函数 | 纯函数 | 已有测试 | 复用 |

### 1.2 seam 形态与惯例对齐

全部采用包级变量注入（项目既有惯例），**不改 common 包、不改生产函数签名**：

```go
var (
    sudoExecFn    = common.SudoExec    // func(name string, args ...string) (string, error)
    sudoOutputFn  = common.SudoOutput
    execOutputFn  = common.ExecOutput
    getDiskStatusFn = getDiskStatus
    sleepFn       = time.Sleep         // func(time.Duration)
    sysfsRoot     = "/sys/block"       // readRAIDSysfsValues 改为 filepath.Join(sysfsRoot, mdName, "md")
    mdstatPath    = "/proc/mdstat"     // 两处 os.ReadFile(mdstatPath)
)
```

测试侧提供两个既有模式的公共桩（对齐 raid_persist_test 的 `stubRunner`）：

- `stubExec`：按命令名分发 canned 输出/error，并**录制调用序列** `(name, args)` —— 用例断言“命令注入失败后不再执行后续命令”全靠它。
- `withSSE(t, ...)`：`httptest.NewRequest` + `httptest.NewRecorder`，返回录制器与 `parseSSE(body)` 解析出的 `[]PoolExtendEvent / []RAIDExpandEvent`（按 `data: ` 前缀 + JSON 反序列化）。
- 每个 stub 替换必须 `t.Cleanup` 还原（raid_persist_test Fix B 教训：orig 在赋 stub 前捕获）。

### 1.3 关键缺口（设计时必须写进用例，标 TDD-red）

1. **kill/断连中断**：两 handler 均不检查 `r.Context().Done()`，且 `sendXxxProgress` 的写错误被 `Fprintf` 丢弃——客户端 kill 后命令链会继续跑到底。用例先定义目标行为（ctx 取消后不再执行后续 sudoExec），配合抽取面 §3 的 `stepTracker`/错误返回签名落地。
2. **pool extend 先拿锁后校验参数**（raid expand 已是锁外廉价校验）：错误参数会排队等长操作。可加一条行为锁定用例，暂不强制改。
3. `totalSteps := 3` 初始值是死代码（立刻被 5/4 覆盖）——抽取 `raidExpandStepTotals` 时顺手消掉。

## 2. 用例清单

### 2.1 handlePoolExtendStream（8 条）

| # | 用例 | 桩/注入 | 断言要点 |
|---|---|---|---|
| P1 ✅ | 全链路成功（wipefs→pvcreate→vgextend→lvextend→growFS xfs） | stubExec 全成功 + findmnt 返回挂载点；sleepFn no-op | SSE 序列：每步 running→done 且 **Step 名称逐字配对**（A3b 回归）、Index 单调 1..5、Total=5、末事件 `complete`；命令序列与参数（`lvextend -l +100%FREE /dev/vg_nas/data`、`xfs_growfs <挂载点>`）精确匹配；growFS 走 xfs 分支用挂载点而非设备 |
| P2 ✅ | 参数默认值 | vg_name/lv_name 留空 | 命令落到 `vg_nas`/`data` 默认值 |
| P3 ❌ | 缺 device / 缺 confirm=yes | 不需要 stub | error 事件且**未发生任何 sudoExec**（廉价拒绝不触碰磁盘） |
| P4 ❌ | 安全检查拒绝（系统盘 / isDiskInUse 命中 PV） | execOutputFn 使 findmnt 返回 `/`；或 pvs 输出含 device | error 事件（“不允许使用系统盘”/“已在存储池中使用”），**wipefs 未执行**（含数据盘不许隐式擦除——四总纲第 2 条） |
| P5 ❌ | **命令注入失败负向**：vgextend 失败 | stubExec 令 vgextend 返回 error+stderr 文本 | error 事件 Step=「加入卷组 vg_nas」且 Detail 含原始输出与错误；**lvextend / resize2fs 未被调用**（Fix 4 回归：失败即中止不吞错）；已执行命令序列止于 vgextend |
| P6 ❌ | growFS 失败（findmnt 空输出 = 未挂载） | execOutputFn 使 findmnt 输出空 | error 事件含“设备未挂载”；末步不置 done、无 complete 事件（A1 同族语义：扩 FS 失败不许静默吞） |
| P7 ❌ | kill/断连中断（TDD-red，目标行为） | 请求带 `context.WithCancel`，在第 2 个 SSE 事件后 cancel；响应头写错误注入 | 抽取后：cancel 后**不再有新 SSE 事件、不再执行后续命令**；现状 red（命令链继续），驱动 §3 的 sendXxx 返回 error + tracker 检查 ctx |
| P8 ❌ | 中断后重试恢复语义 | P7/P5 半途状态（device 已成 PV）后再次请求 | 第二次被 `isDiskInUse` 拒绝（“已被占用”），**不会**对半途盘二次 wipefs；锁定“恢复需人工清理”的现行为 |

### 2.2 handleRAIDExpandStream（8 条）

| # | 用例 | 桩/注入 | 断言要点 |
|---|---|---|---|
| R1 ✅ | RAID1 全链路成功（add→grow→resync→growFS） | TempDir 文件当 mdDev；mdadm --detail 桩返回 raid1/2 设备（不含 device）；getDiskStatusFn 返回 unused；sysfsRoot 指 TempDir 假 sysfs（sync_action=idle、sync_completed=100%）；sleepFn no-op | totalSteps=5；resync 步 done 后 growFS 被调；complete 事件 Detail 含“容量不变（镜像）”与 2→3 |
| R2 ✅ | RAID5 路径：启动 reshape 即返回 | mdadm detail 返回 raid5 | 末事件 Status=**reshaping**（非 complete）；**waitForResync 未被轮询**（sleepFn 断言无 2s 调用、无 sysfs 读）；Detail 含“手动扩展文件系统”指引；growFS 未调用 |
| R3 ❌ | mdadm --detail 失败 / mdDev 不存在 | sudoOutputFn 返回 err；或不造 TempDir 文件 | 两分支各自 error 事件；**锁外拒绝**（拿锁前的拒绝不排队） |
| R4 ❌ | raid0 拒绝 + 不支持级别 + 设备数解析失败 | detail 桩分别返回 raid0 / raid10 / 无 Raid Devices 行 | 各自 error 文案；**均未拿锁、未执行 wipefs** |
| R5 ❌ | 目标盘非空闲：已在阵列（detail 输出含 device）/ getDiskStatus 非 unused / isDiskInUse（存量签名） | 三种注入各一条子用例 | 各自 error 事件且 wipefs 未执行（三层防御独立生效） |
| R6 ❌ | **resync 超时上限与后续单独扩 FS 语义** | `waitForResyncCore` 直测：poll 恒 not-done、maxRounds=3、sleepFn no-op | 返回非 nil；handler 层 error 事件 Detail 同时含“已中止、文件系统未扩展”与 expand-fs 入口指引——**文件系统绝不在 resync 未完成时被扩**；配套 `handleRAIDExpandFS` 闸门用例：sync_action=resync → 拒绝；idle+reshape_position=none → 放行 |
| R7 ❌ | resync streak 竞态（Finding 3 回归） | poll 序列 [busy,busy,clean,busy,clean,clean,clean]（TempDir sysfs 内容按轮次切换） | 仅在**连续 3 轮 clean** 后返回 nil；中途 clean 后再 busy 则 streak 归零；busy 轮发 running 事件且 Detail 为“同步进度: x%”或“同步中（<action>）” |
| R8 ❌ | 命令注入失败负向 + 断连 | mdadm --grow 返回 error；ctx cancel 同 P7 | error 事件含原始输出；后续步骤命令不再执行；断连后命令链停（TDD-red 同 P7） |

补充共通断言（并入各用例）：每个 done 步的 Step 字符串与前面 running 完全一致；error 事件后**不出现任何后续 done/complete 事件**；`Total` 与级别匹配（raid1=5，raid5/6=4）。

## 3. 纯函数抽取重构面（最小 diff 函数签名清单）

原则：**不改路由/不改外部 JSON 形态**；handler 退化为“校验 → 取桩 → 跑计划 → 发事件”的编排壳。

**pool_extend.go**

```go
// 1) 参数校验+默认值（纯）
func poolExtendValidate(device, confirm string) (rejStep, rejDetail string, ok bool)
// 2) 命令计划（纯数据，handler 循环执行）
type execStep struct { Name string; Cmd string; Args []string }
func buildPoolExtendSteps(vgName, lvName, device string) []execStep
// 3) SSE 发送改为可失败（kill 中断前提）
func sendPoolExtendProgress(w http.ResponseWriter, ev PoolExtendEvent) error
// 4) 步进器：纯编排，emit 与 sleep 注入
func newStepTracker(total int, emit func(PoolExtendEvent) error, sleep func(time.Duration), ctx context.Context) *stepTracker
//   方法：Running(name) error / Done(name) error / Error(step, detail) error
```

**raid_expand.go**

```go
// 5) 锁外廉价校验（纯）
func raidExpandValidateParams(mdDev, device, confirm string) (rejStep, rejDetail string, ok bool)
// 6) 目标阵列/磁盘校验（纯）
func raidExpandValidateTarget(detailOut, device string, disks []DiskStatus, diskInUse bool) (rejStep, rejDetail string, level string, currentDevs int, ok bool)
// 7) 步数（纯；顺带消掉 totalSteps:=3 死代码）
func raidExpandStepTotals(raidLevel string) (totalSteps int, isRAID1 bool)
// 8) 命令计划（纯数据）
func buildRAIDExpandSteps(mdDev, device, raidLevel string, newDevs int) []execStep
// 9) waitForResync 循环核
func waitForResyncCore(poll func() (done bool, detail string), maxRounds, requiredCleanStreak int, sleepFn func(time.Duration)) error
// 10) 单轮判据
func resyncPollOnce(mdName string, readSysfs func(string) (map[string]string, bool), mdstatText string) (done bool, detail string)
// 11) 同 3)
func sendRAIDExpandProgress(w http.ResponseWriter, ev RAIDExpandEvent) error
```

**落点改动量评估**：6 个包级变量 seam + readRAIDSysfsValues/mdstat 读点各改 1 行路径来源 + 两个 send 函数签名加 error（调用点 8 处，机械改）；`waitForResync` 外壳保留原签名，内部委托 9)+10)。不触碰 common 包与其它 handler。

**风险/顺序**：先落 seam（P3/P4/R3-R5 只需 seam 即可绿）→ 再抽 stepTracker（P7/R8 断连红转绿）→ 最后抽 waitForResyncCore（R6/R7）；每步跑 `go test ./modules/diskmgmt/` 全量防回归（现有 28 个测试文件，特别注意 raid_expand_test 的真实 8s 用例在 sleepFn 落地后应改注入以缩短）。

---

## 附：Chrome E2E 断连基线实测（2026-11-17 18:32，修复前二进制 0cef275）

- 方法：本机 Chrome 打开面板（token 存 nas_token 走 app 自身会话恢复），页面上下文发与 app.js 完全同款的 `fetch('/api/disk/raid/expand-stream?md_device=/dev/md0&device=/dev/sdd&confirm=yes')`，收到第 1 个 SSE 事件（`清除新磁盘签名 running index=1/5`）后 100ms `AbortController.abort()` 断连。
- **缺陷现场实锤**：断连后命令链照常跑完——journal 证据：`GET /api/disk/raid/expand-stream`（18:32:44）之后 `sudo mdadm --grow /dev/md0 --raid-devices=3`（18:32:45）；8 秒后 mdstat 显示 `sdd[2]` 已入阵、recovery 8.7% 进行中。即客户端已死、服务端继续执行破坏性磁盘命令——正是 R3 要修的语义。
- 现场恢复：`mdadm --fail/--remove sdd` → `mdadm --grow --raid-devices=2` 收缩 → `wipefs -a /dev/sdd`，回到 [2/2] clean 基线。
- R3 落地部署后用同款脚本复测：断连后 journal 不应再出现后续 sudo 命令、sdd 保持空白。
