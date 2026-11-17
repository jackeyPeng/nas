# TASK: extend/expand 流式写路径单测 R2 — seam 落地 + 全绿用例（P1-P6 / R1-R5）

## 依据

完整设计方案在 `docs/opencode/2026-11-17-extend-tests/DESIGN.md`（先通读，代码现实优先于方案文本，冲突须申报）。本轮只做方案的第 1 阶段（seam + 桩 + 16 条可实现用例）；**P7/P8/R6/R7/R8（ctx 中断/TDD-red/waitForResyncCore 抽取）明确留给 R3，本轮不做**。

## 已核实事实（parent 逐一 grep 验证过，勿再查源码外部）

- `common.SudoExec` / `common.SudoOutput` / `common.ExecOutput` 签名均为 `func(name string, args ...string) (string, error)`（common/sudo.go:12/19/43）。
- `getDiskStatus()` 在 modules/diskmgmt/storage.go:208。
- `isSystemDisk`（storage.go:334）内部调 `common.ExecOutput("findmnt",...)` 三处（:335/:350/:356）；`isDiskInUse`（storage.go:368）调 `common.ExecOutput("cat", "/proc/mdstat")`（:377）。
- 直接调用计数：pool_extend.go 4 处、raid_expand.go 5 处、growfs.go 4 处（common.SudoExec/SudoOutput/ExecOutput）。
- `readRAIDSysfsValues`（raid_sysfs.go:48）硬编码 `filepath.Join("/sys/block", mdName, "md")`（:49）。
- `os.ReadFile("/proc/mdstat")` 在 raid_expand.go:249/326（本轮只改这两处；overview.go/maintenance.go 的同名调用不动）。
- sleep 点：pool_extend.go:74（100ms）+ raid_expand.go:148/152（100ms）/363（2s 前置）/421（2s 轮询）。**stream.go 的 sleep 一律不动**（向导路径不在本轮范围，避免回归面扩大）。
- 测试文件现状：无 pool_extend_test.go；raid_expand_test.go 已存在（含真实 8s 用例）；新建文件放用例避免冲突。

## 改动内容

### 1. 新建 modules/diskmgmt/seams.go（生产文件，包级变量 seam）

```go
var (
    sudoExecFn      = common.SudoExec
    sudoOutputFn    = common.SudoOutput
    execOutputFn    = common.ExecOutput
    getDiskStatusFn = getDiskStatus
    sleepFn         = time.Sleep
    sysfsRoot       = "/sys/block"
    mdstatPath      = "/proc/mdstat"
)
```

每个变量带注释（默认指向真实实现、测试注入替身，惯例同 raid_persist_test 的 stubRunner / filemgmt 的 poolRootOverride）。

### 2. 路由替换（行为中立：默认值即原函数）

- pool_extend.go / raid_expand.go / growfs.go：全部 `common.SudoExec/SudoOutput/ExecOutput` 直接调用 → 对应 `xxxFn`。
- storage.go：仅 isSystemDisk 的 3 处 findmnt 与 isDiskInUse 的 1 处 cat /proc/mdstat → `execOutputFn`（getDiskStatus 的 lsblk :209 不动）。
- raid_sysfs.go:49：`"/sys/block"` → `sysfsRoot` 变量。
- raid_expand.go:249/326：`"/proc/mdstat"` → `mdstatPath`。
- 上述 sleep 点：`time.Sleep(...)` → `sleepFn(...)`（只改列出的 6 处）。

### 3. 测试桩（新建 modules/diskmgmt/stream_stubs_test.go）

- `stubExec`：按命令名分发 canned 输出/错误 + 录制调用序列（name+args）；替换/还原必须 `t.Cleanup`（orig 在赋 stub 前捕获——raid_persist_test Fix B 教训）。
- `withSSE`：httptest.NewRequest + NewRecorder（实现 Flusher），返回录制器；`parseSSE(body)` 按 `data: ` 前缀解析出事件切片。

### 4. 用例（按 DESIGN.md §2.1 P1-P6、§2.2 R1-R5，共 11 条）

- 新建 pool_extend_test.go：P1-P6（含命令序列精确断言：`lvextend -l +100%FREE /dev/vg_nas/data`、`xfs_growfs <挂载点>`；P4/P5/P6 的「失败后不再执行后续命令」靠 stubExec 录制断言）。
- 新建 raid_expand_stream_test.go：R1-R5（R1 用 t.TempDir 文件当 mdDev + 假 sysfs；R2 断言 sleepFn 无 2s 调用、无 sysfs 读；R3/R4 断言锁外拒绝——不排队；R5 三层防御独立生效）。
- 共通断言并入：done 步 Step 与 running 逐字一致；error 后无后续 done/complete；Total 与级别匹配（raid1=5，raid5/6=4）。
- 既有 raid_expand_test.go 的真实 8s 用例：sleepFn 落地后改为注入以缩短（用 sleepFn 替换其真实等待，断言语义不变）。

## 硬约束（违反即返工）

- 只动：seams.go（新建）、pool_extend.go、raid_expand.go、growfs.go、storage.go（仅列出的 4 处）、raid_sysfs.go（仅 :49）、stream_stubs_test.go / pool_extend_test.go / raid_expand_stream_test.go（新建）、raid_expand_test.go（仅 8s 用例注入化）。stream.go 与其他文件一律不碰。
- 不改 sendXxx 签名、不做 stepTracker/waitForResyncCore 抽取、不写 P7/P8/R6/R7/R8 用例（R3 范围）。
- 不改路由、不改 JSON 形态、不碰 common 包。
- **禁止读取仓库外任何文件**（~/go/pkg/mod、/tmp、repo 外路径）——一次拒绝即中止整个 run；验证只走 `go build ./...` 与 `go test ./modules/diskmgmt/ -count=1`。
- 与 DESIGN.md 或本 brief 冲突处：报告中申报，不要静默绕过。
- 单笔 commit，信息精确用：`test(diskmgmt): extend/expand 流式写路径 seam 落地 + P1-P6/R1-R5 用例 — 命令/SSE/时间全注入，失败中止与锁外拒绝语义锁定`
- 禁 git push。

## 产出报告

写 `docs/opencode/TASK-EXTEND-TESTS-R2-REPORT.md`（**不要 git add**）：改动清单、go build 尾行、go test 真实输出尾行（含新增用例数）、偏离申报、遗留疑虑（含发现的 R3 需要注意的点）。
