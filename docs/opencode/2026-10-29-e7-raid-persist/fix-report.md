# E7 — RAID 配置持久化链路修复 任务报告

## 根因

真机实锤（Debian 13 测试机，RAID1 池，2026-10-28/29）：流式建池
（`setup-stream?mode=raid1`，面板唯一实际入口）走 `stream.go`
`setupRAIDStream` 第 6 步 "保存 RAID 配置"：

1. `mdadm --detail --scan` 与 `cat /etc/mdadm/mdadm.conf` 的 error 都被 `_`
   吞掉——scan 失败时 `mdadmConf` 为空，仍 `SafeWriteFile` + `stepDone`，
   对外报成功（假成功）。
2. 没有 `update-initramfs` 调用——conf 文件本身写对了，但 initramfs 里打
   包的仍是装机时的旧版本。重启时内核早期装配阶段读的是 initramfs 里的旧
   conf，无视本次写入的 ARRAY 行，阵列最终以 `md127` 装配而不是 conf 里写
   的 `md0`（设备名漂移实锤；fstab 按 UUID 挂载不受影响，数据安全，但
   `mdadm.conf` 与运行时现实不一致）。
3. 没有写后验证——`SafeWriteFile` 失败（权限/磁盘满）同样被当作成功。
4. 没有 UUID 去重——同一阵列重复跑建池流程会在 conf 里叠加多条 ARRAY 行。

`wizard.go`（非流式旧接口）的 `setupRaidN` 函数（441-446 行附近）有同源但
形态略有差异的问题：`err == nil && mdadmConf != ""` 才写（失败静默跳
过，steps 仍报"保存 RAID 配置"成功）；`update-initramfs` 结果被完全忽
略；同样无写后验证、无 UUID 去重。

`reset.go:124` 与 `stream.go` 出厂重置路径把 `/etc/mdadm/mdadm.conf`
**整文件清空**，连 Debian 默认的 `HOMEHOST`/`MAILADDR` 等行都一起抹掉；
清完也不跟进 `update-initramfs`，旧 initramfs 里残留已删阵列的 ARRAY
行，可能导致下次开机时内核尝试装配一个已被拆除的阵列。

## 修法

### 新文件 `web/modules/diskmgmt/raid_persist.go`

选择新建独立文件而不是并入 `raid_sysfs.go`：`raid_sysfs.go` 的职责是
"读取阵列运行时状态"（sysfs 标量文件 → RAIDStatus），本次改动的职责是
"持久化阵列配置到 mdadm.conf + initramfs"，两者是读/写两条完全不同的关
注点，合并到同一文件会让"状态采集"与"配置落盘"的代码混在一起，不利于
后续各自演进。保持独立文件，命名 `raid_persist.go` 与职责对齐。

核心导出函数：

- `persistRAIDConfig(runner cmdRunner, mdDev string) (warning string, err error)`
  —— stream.go/wizard.go 建池路径调用。语义严格按任务书 a~f 六步实现：
  1. `mdadm --detail --scan <mdDev>` 取该设备自己的 ARRAY 行；失败或输出
     中找不到 UUID → `error`，调用方必须报 `stepFailed`，不得假成功。
  2. 读现有 `/etc/mdadm/mdadm.conf`；cat 失败按空文件处理（但不是静默
     "空白覆盖"——持久化会走正常的"追加"分支，只是读不到旧内容这件事
     本身已经通过 cat 返回的 error 留了痕，调用方若要看历史失败可查日
     志/err chain）。
  3. UUID 去重：`upsertArrayLine` 纯函数——已有同 UUID 的 ARRAY 行 → 原
     地替换该行（覆盖 md0→md127 设备名漂移场景）；无 → 追加到末尾。
  4. `common.SafeWriteFile` 落盘（通过下文 `raidConfWriter` 注入点）。
  5. 写后验证：再 cat 一次，确认目标 UUID 的 ARRAY 行真的在文件里，不在
     → `error`。
  6. `update-initramfs -u`：失败不 fatal，返回非空 `warning`（调用方必
     须把它带进 step 文案/SSE Detail，不得静默吞掉）。

- `resetRAIDConfig(runner cmdRunner) (warning string, err error)` —— 出厂
  重置路径调用。只删 ARRAY 行（`stripArrayLines` 纯函数），保留其余内容；
  cat 失败（文件不存在）视为无需处理，不是失败；内容有变化才落盘并跟进
  `update-initramfs -u`（失败同样只给 warning）。

### 执行器注入模式

包内此前**没有**命令执行器注入模式——既有测试（如 `config_sync_test.go`、
`raid_sysfs_test.go`）全部是纯函数表驱动，直接喂 map/string，不涉及任何
"替换生产执行路径"的需求。唯一近似的先例是 `regression_test.go` 里的
`rcloneRestartCmd`（包级变量存函数，测试直接替换）。

本轮引入两个注入点，均沿用这个包级变量/参数传函数的最小模式，不改动
`common` 包：

1. `type cmdRunner func(name string, args ...string) (string, error)`——
   作为 `persistRAIDConfig`/`resetRAIDConfig` 的**参数**传入（不是包级
   变量，因为这两个函数本身是新加的、调用方唯一，参数注入更直接）。
   `nil` 时回退到 `sudoRunner`（转发给 `common.SudoOutput`）。覆盖
   `mdadm --detail --scan` / `cat` / `update-initramfs` 三类命令。
2. `var raidConfWriter = common.SafeWriteFile`——包级变量，与
   `rcloneRestartCmd` 同款模式。单独开这个注入点的原因：
   `common.SafeWriteFile` 不是 `SudoOutput`/`SudoExec` 的薄包装（内部有
   "直写优先 → 目录可写则删除重建 → 不行再 sudo cp" 三层兜底逻辑和不同
   的函数签名 `(string, string) error`），塞不进 `cmdRunner` 的
   `(string, ...string) (string, error)` 签名，也不适合强行拗成同一个
   类型；单独一个包级变量测试替换，生产路径零改动。

### stream.go / wizard.go 改动

- `stream.go` `setupRAIDStream` 第 6 步改调 `persistRAIDConfig(nil, mdDev)`：
  `error` → `stepError` + `return err`（与函数其余步骤失败分支形态一
  致）；`warning` 非空 → 补发一条 `running` 状态的 SSE 事件把警告带给前
  端（不新增 step、不改 totalSteps，形态参考既有的"采集纳管盘"警告事
  件写法）。
- `wizard.go` `setupRaidN`（原 441-446 行）改调同一个 `persistRAIDConfig`：
  `error` → steps 追加失败文案 + `return steps`（与本函数 `writeFstab`
  失败分支形态一致）；`warning` 非空 → steps 文案带出警告，不覆盖掉"保
  存 RAID 配置"这条步骤名。
- `reset.go:124` 与 `stream.go` 出厂重置路径（原 `common.SafeWriteFile(...,
  "")` 两处）都改调 `resetRAIDConfig(nil)`：`error` → 步骤/SSE 带出失败
  文案（不中止整个重置，与该函数"尽力拆除"的既有语义一致）；`warning`
  非空 → 同样带出。

### 未改动范围（明确遵守"不做"清单）

- `setupRaid1`（`wizard.go` 348-386 行，`handleWizardSetup` 的 raid1 专
  用分支）本身从未调用过任何 mdadm.conf 持久化逻辑——这是一个与本任务
  描述的两处代码问题（`stream.go setupRAIDStream` / `wizard.go
  setupRaidN`）平行但更早就存在的缺口，任务书引用的行号（441-446）精确
  对应 `setupRaidN`，不含 `setupRaid1`。按"不扩大范围"原则未触碰，作为
  偏差在此申报：`handleWizardSetup`（非流式旧接口）走 `raid1` 模式时，
  建池后完全不写 mdadm.conf，持久化问题比 `setupRaidN` 更严重（不是"写
  了但有缺陷"而是"完全没写"）。该旧接口本身已被标注
  "deprecated non-stream path"，面板实际入口是 `stream.go` 的流式版本，
  故未在本轮处理，留给后续轮次或明确的另一个任务单。
- `detectPoolDevice`/`overview` 未动——面板自愈链路按任务书确认正常。
- 未引入 pools 注册表/执行内核。
- 未改 fstab 逻辑。

## 新增测试清单（`raid_persist_test.go`）

纯函数表驱动：

- `TestStripArrayLines`：多 ARRAY 行+默认行保留、无 ARRAY 行原样返回、
  空文件、只有空白字符、CRLF 容错、只有 ARRAY 行删完为空、注释行与空行
  保留，共 7 个子用例。
- `TestUpsertArrayLine`：空文件追加、无同 UUID 行追加到末尾、**同 UUID
  异设备名原地替换**（md0→md127 核心场景）、重复调用不叠加行，共 4 个
  子用例。
- `TestExtractArrayUUID`：正常 ARRAY 行、大写 UUID 归一化小写、非 ARRAY
  行、ARRAY 行无 UUID 字段、空行、注释行，共 6 个子用例。

`persistRAIDConfig`（注入 `stubRunner` + `raidConfWriter` stub，覆盖任务
书要求的五路径）：

- `TestPersistRAIDConfig_Success`
- `TestPersistRAIDConfig_ScanFailed`（scan 命令本身报错）
- `TestPersistRAIDConfig_ScanEmpty`（scan 不报错但输出无 ARRAY/UUID 行）
- `TestPersistRAIDConfig_VerifyFailed`（落盘成功但写后验证的 cat 读不到
  目标 UUID）
- `TestPersistRAIDConfig_InitramfsWarning`（conf 落盘成功，
  update-initramfs 失败只带 warning 不报错）
- `TestPersistRAIDConfig_WriteFailed`（额外补充：`raidConfWriter` 落盘本
  身失败）

`resetRAIDConfig`：

- `TestResetRAIDConfig_RemovesArrayLinesOnly`（只删 ARRAY 行，保留
  HOMEHOST/MAILADDR）
- `TestResetRAIDConfig_NoChangeSkipsWrite`（无 ARRAY 行时内容无变化，不
  触发落盘）
- `TestResetRAIDConfig_CatFailedSkipsGracefully`（文件不存在不是失败）
- `TestResetRAIDConfig_InitramfsWarning`
- `TestResetRAIDConfig_WriteFailed`

## 覆盖率

基线 23.6% → 修改后 **25.0%**（`go test ./modules/diskmgmt -cover`），
无下降。

## 验证结果

```
cd web && go build ./... && go vet ./modules/diskmgmt && go test -race ./modules/diskmgmt -count=1 -cover
```

```
ok  	nas-panel/modules/diskmgmt	12.359s	coverage: 25.0% of statements
```

`go build ./...`、`go vet ./modules/diskmgmt` 均无输出（无错误/无警
告）。`gofmt -l` 对本轮新增/修改的文件（`raid_persist.go`、
`raid_persist_test.go`）无输出（已格式化）；`export_test.go` 存在既有的
格式问题，与本任务无关，未处理。

## 偏差申报

1. `setupRaid1`（非流式旧接口的 raid1 专用分支）完全不写 mdadm.conf 的
   缺口未处理——见上文"未改动范围"说明，任务书引用行号未覆盖该函数，
   按最小范围原则未扩大修复面。
2. `persistRAIDConfig`/`resetRAIDConfig` 放在新文件 `raid_persist.go`
   而非并入 `raid_sysfs.go`——任务书允许"你定，报告说明"，已在上文"新
   文件"一节说明理由（读/写关注点分离）。
3. `cmdRunner` 采用函数参数注入而非包级变量；`raidConfWriter` 采用包级
   变量注入——两种方式混用，已在"执行器注入模式"一节说明各自理由（新函
   数调用方单一用参数更直接；`SafeWriteFile` 签名特殊且需要在多个新函
   数间共享同一个 stub 点，用包级变量）。
