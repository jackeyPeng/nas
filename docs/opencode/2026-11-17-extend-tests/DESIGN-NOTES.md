# diskmgmt 流式写路径单测方案（调研记录，子代理产出，transcript 截断仅存摘要）

> 子代理完成调研（pool_extend.go / raid_expand.go / growfs.go / storage.go / raid_persist.go /
> 既有 seam 模式 / common/sudo.go / common/disklock.go），以下为其方案的可还原部分。
> transcript 截断，完整用例表/重构签名清单需按下方可还原骨架由 R2 轮任务书重新展开。

## 可测性分析（依赖盘点）

| 依赖 | 类型 | 现状 | 处置 |
|---|---|---|---|
| common.SudoExec（wipefs/pvcreate/vgextend/lvextend/mdadm --add/--grow/xfs_growfs/resize2fs） | 真实 sudo 命令 | 无注入点 | 新 seam：sudoExecFn |
| common.SudoOutput（mdadm --detail） | 真实 sudo 命令 | 无注入点 | 新 seam：sudoOutputFn |
| common.ExecOutput（findmnt/pvs/…） | 真实命令 | 无注入点 | 新 seam：execOutputFn（案例：Fn 使 findmnt 返回 /；pvs 输出含 device） |
| SSE 写出（sendProgress） | 直接写 http.ResponseWriter | 无返回值 | 抽取后让 sendXxx 返回 error，支持 cancel 传播（P7 驱动） |
| 时间等待（resync 轮询） | 直接 sleep | 无注入点 | sleepFn 注入 + 轮数上限参数化 |

## 用例清单骨架（P1-P7，正/负向）

- P1-P4 正向链路（含系统盘/已在池中拒绝路径：error 事件且 wipefs 未执行——四总纲第 2 条：含数据盘不许隐式擦除）
- P5 命令注入失败负向：vgextend 失败 → error 事件 Step=「加入卷组 vg_nas」且 Detail 含原始输出；lvextend/resize2fs 未被调用（失败即中止不吞错）
- P6 growFS 失败（findmnt 空输出=未挂载）→ error 含「设备未挂载」；无 complete 事件
- P7 kill/断连中断（context.WithCancel）→ 抽取后 cancel 不再有新 SSE 事件、不再执行后续命令；现状 red（命令链继续），驱动重构

## 纯函数抽取重构面（函数签名，可还原部分）

// 8) 命令计划（纯数据）
func buildRAIDExpandSteps(mdDev, device, raidLevel string, newDevs int) []execStep
// 9) waitForResync 循环核（判据经 poll 注入，睡眠经 sleepFn 注入，轮数上限参数化）
func waitForResyncCore(poll func() (done bool, detail string), maxRounds, requiredCleanStreak int, sleepFn func(time.Duration)) error
// 10) 单轮判据（读 sysfs/mdstat，供 poll 闭包与 handleRAIDExpandFS/handleRAIDReshapeStatus 复用）
func resyncPollOnce(mdName string, readSysfs func(string) (map[string]string, bool), mdstatText string) (done bool, detail string)
// 7)（pool_extend 侧，签名头部被截断）
func buildExtendSteps(... (totalSteps int, isRAID1 bool)
