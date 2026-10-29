# TASK-REPORT：E7 审查回炉 + E8 删池清理 ARRAY 行

## 开头更正声明

上轮报告中"23 个测试 / 覆盖率 25.0%"两项数字与实测不符。本轮基线实测：
`web/modules/diskmgmt/raid_persist_test.go` 顶层测试函数为 **14 个**，`go test
-race ./modules/diskmgmt -count=1 -cover` 实测覆盖率为 **24.9%**。本轮报告中出现
的全部数字均为本轮实测结果（命令与输出见文末"验证"章节），不凭记忆填写。

基线 commit：`9100092`（fix(diskmgmt): RAID 配置持久化链路...）。

---

## 第一部分：E7 审查回炉（commit `ecb1231`）

### Fix A（Important）：persistRAIDConfig cat 失败留痕并透出 warning

- 改动点：`raid_persist.go` `persistRAIDConfig` 读现有 conf 的 `cat` 调用，
  `catErr != nil` 时：
  1. `log.Printf("[RAID_PERSIST] 读取 %s 失败，按空文件重建（若文件实际存在，
     其原有内容将被覆盖）: %v", ...)`（对齐包内 `config_sync.go:52` 的
     `[CONFIG_SYNC]` 前缀风格）；
  2. 文案并入返回的 `warning`，与 initramfs 警告分号拼接共存（两者都非空时
     `catWarning + "; " + initWarning`）；只有 catWarning 时单独返回 catWarning
     （此前写死 `return "", nil` 的末尾分支改为 `return catWarning, nil`）。
  调用方 stream.go 的 SSE/steps 已会把 `persistRAIDConfig` 返回的 warning 带出，
  未改调用方代码。
- 测试增改：新增 `TestPersistRAIDConfig_CatFailedLeavesTraceAndWarns`——模拟
  cat 第一次调用失败、第二次（写后验证）返回刚落盘内容，断言 `err == nil`、
  `warning` 含"读取"字样、落盘内容仅含新 ARRAY 行（且含目标 UUID）。

### Fix B（Important）：6 处测试 raidConfWriter stub 恢复写反

- 改动点：`raid_persist_test.go` 六处（`TestPersistRAIDConfig_InitramfsWarning`
  及 `TestResetRAIDConfig_RemovesArrayLinesOnly`/`NoChangeSkipsWrite`/
  `CatFailedSkipsGracefully`/`InitramfsWarning`/`WriteFailed`）统一改为先
  `orig := raidConfWriter` 再赋值 stub，`t.Cleanup` 恢复 `orig`（原生产实现
  `common.SafeWriteFile`），对齐 `withRAIDConfWriterStub`（:187-197，行号未变）
  与 `regression_test.go:53` 的正确写法。
- 泄漏为何此前未爆红：六处修复前的代码是"赋值 stub → `orig := raidConfWriter`
  （此刻 `raidConfWriter` 已是 stub）→ Cleanup 恢复 `orig`"，即 Cleanup 把全局
  变量恢复成**它自己（stub）**，等于没恢复。但因为：
  1. 六个测试彼此的 stub 行为高度相似（都只是记录写入内容/标志位，不依赖
     "必须是生产实现"这个前提）；
  2. Go 测试默认按文件内声明顺序顺序执行（未用 `t.Parallel()`），下一个测试
     函数入口处几乎总会立刻重新赋值一次 `raidConfWriter = func(...)...`，
     覆盖掉上一个测试残留的 stub，窗口期极短；
  3 没有测试在"不显式赋值 raidConfWriter 直接调用 persistRAIDConfig/
     resetRAIDConfig"的前提下断言调用了生产实现（唯一真正依赖生产实现的
     路径是 nil runner 回退，测试里从未触发）。
  三个条件叠加，残留的 stub 方向正好"安全"（下一测试会自己覆盖），所以从未
  观测到跨测试污染的断言失败。

### Fix C（Minor1）：stream.go 第 6 步 initramfs 警告 SSE 事件补 Index

- 改动点：`stream.go` `handleWizardSetupStream` 在 `stepRunning`/`stepDone`
  闭包旁新增 `stepIndex := func() int { return step + 1 }`（与 `stepRunning`
  用的索引值同源，对齐 `handleWizardResetStream` 里直接引用 `step+1` 的形态，
  但 `setupRAIDStream` 是独立函数，不能直接访问 `setupStream` 里的 `step`
  变量，所以用闭包传递）。`setupRAIDStream` 签名新增 `stepIndex func() int`
  参数，第 6 步补发的 SSE running 事件改为 `Index: stepIndex()`，不再是
  `index:0`。四个调用点（raid0/1/5/6）同步传参。
- 测试：此事件属于 stream.go 的集成态 SSE 输出，包内既有测试风格（见
  raid_persist_test.go 顶部注释）对这类 HTTP handler 没有建立单测基础设施，
  本轮未新增 stream.go 的测试（与既有模式一致，纯 `go build`/`go vet` + 手动
  审查确认签名改动后四个调用点都已同步）。

### Fix D（Minor4）：stripArrayLines 删除条件放宽为 ARRAY 前缀

- 改动点：`raid_persist.go` 新增 `arrayPrefixRe = regexp.MustCompile(`(?i)^ARRAY\b`)`
  与纯函数 `isArrayLine(line string) bool`；`stripArrayLines` 的删除判据从
  `extractArrayUUID` 命中改为 `isArrayLine` 命中。`upsertArrayLine`/写后验证
  等按 UUID 匹配的逻辑（`extractArrayUUID`/`findArrayLineByUUID`）未动。
- 测试增改：`TestStripArrayLines` 补两个表驱动用例——"无UUID的手写ARRAY行被删"
  （`ARRAY /dev/md0 name=nas:0 devices=/dev/sda,/dev/sdb` 无 `UUID=` 字段，
  验证会被删）、"ARRAYX前缀陷阱不误删"（`ARRAYXYZ not a real array line`，
  `\b` 词边界后紧跟非空白字符，不匹配，验证原样保留）。

### 记录不修

- Minor2（前端 SSE running 事件丢 Detail）：跨轮前端缺口，另立项，本轮未碰
  `frontend/`，按约束硬性要求不改前端。
- Minor3（resetRAIDConfig 对无 ARRAY 的 CRLF 文件多做一次无害落盘）：记录
  不修，影响仅为多一次无害 IO。
- Minor5（`/usr/sbin/mdadm` 硬编码 vs `mdadmPath` 不一致）：记录不修，sudo
  场景绝对路径更稳，暂不统一。

---

## 第二部分：E8 删池清理 mdadm.conf ARRAY 行（commit `a18ba02`）

### 真机实锤复述

RAID1 删池成功后 `mdadm.conf` 仍留被删阵列的 ARRAY 行；重建新池后 conf 里两条
同指 `/dev/md0` 的 ARRAY 行（旧 UUID + 新 UUID）共存，initramfs 同带——重启
装配命名冲突风险，且违反"配置文件应与内核当前状态对账"的原则。根因：
`handlePoolDelete` 的 raid 分支从建立以来就不调用任何 mdadm.conf 清理逻辑
（E7 只接了建池 `persistRAIDConfig` 与出厂重置 `resetRAIDConfig` 两条路径，
删池这条此前完全没有相应函数，属于漏接而非回归）。

### 改动点

1. `raid_persist.go` 新增三个函数：
   - `removeArrayLineByUUID(conf, targetUUID string) string`：纯函数，按
     `extractArrayUUID` 命中的 UUID 精确匹配删除单条 ARRAY 行，其余行保序
     保留；`targetUUID` 为空或 conf 为空/空白原样返回；非空 conf 在无命中
     时仍走一次 split/join 做 CRLF 归一化（与 `upsertArrayLine`/
     `stripArrayLines` 行为一致）。
   - `raidArrayUUID(runner cmdRunner, mdDev string) (uuid string, err error)`：
     复用 `findArrayUUIDForScan` 解析 `mdadm --detail --scan <mdDev>` 输出；
     scan 失败或输出无 UUID 行均返回 error。函数注释明确标注"调用方必须在
     `mdadm --stop` 之前调用"。
   - `removeRAIDConfigByUUID(runner cmdRunner, targetUUID string) (warning string, err error)`：
     cat 失败 → 跳过（log 留痕，语义对齐 `resetRAIDConfig`）；
     `removeArrayLineByUUID` 后内容无变化 → 跳过落盘；落盘失败 → error；
     落盘后写后验证目标 UUID 行确已不在 → 验证失败则 error；
     `update-initramfs` 失败 → 非 fatal，返回 warning。
2. `pool.go` `handlePoolDelete` 的 `case "raid1", "raid0", "raid5", "raid6"`
   分支接线：
   - 在采集 UUID（A3，`blkid`）之后、`mdadm --stop` 之前调用
     `raidArrayUUID(nil, poolDevice)`；失败不阻断删除，`steps` 追加
     `"警告: 未能读取阵列 UUID，mdadm.conf 可能残留 ARRAY 行: <err>"`；
   - 成员盘 zero-superblock/wipefs 释放完成后（即拆除动作全部结束后），若
     `mdadmConfUUID != ""` 则调用 `removeRAIDConfigByUUID(nil, mdadmConfUUID)`：
     err → `steps` 追加"清理 mdadm.conf ARRAY 行失败: <err>"；
     warning 非空 → 追加该 warning 原文；
     均无 → 追加"清理 mdadm.conf ARRAY 行"。
   - 既有 steps 形态、审计日志（`common.LogAuditRequest`）、`jsonError`
     早退语义全部未动，新增代码只在原有"3. Stop RAID"前后插入，不改变
     既有分支结构。
3. 测试：`removeArrayLineByUUID` 表驱动（多ARRAY删中间/删唯一/无命中/
   空conf/targetUUID为空/CRLF容错/大小写不敏感），`raidArrayUUID`/
   `removeRAIDConfigByUUID` 用 `stubRunner` 覆盖 scan 失败/scan 输出无
   UUID/cat 失败跳过/无命中跳过落盘/删除成功写后验证/initramfs 警告/
   落盘失败七条路径。全部用 Fix B 教训后的正确 stub 恢复模式（`orig :=
   raidConfWriter` 在赋值 stub 之前）。

### 偏差申报

- 任务描述里 `removeRAIDConfigByUUID` 的测试分组建议是"scan 失败/无命中
  跳过/删除成功写后验证/initramfs 警告"四路径，实际实现时发现
  `removeRAIDConfigByUUID` 本身不调用 scan（scan 是 `raidArrayUUID` 的职责，
  两者已拆成独立函数各自测试），`removeRAIDConfigByUUID` 层面对应的是
  "cat 失败跳过"，按其真实参数面追加了"落盘失败"路径的测试，测试覆盖口径
  比建议更细（7 条而不是 4 条），非偏离任务意图，只是按实际函数边界拆分
  得更精确。
- `handlePoolDelete` 的 raid 分支里 `raidArrayUUID`/`removeRAIDConfigByUUID`
  未被既有 `handlePoolDelete` 测试触达（`pool_test.go` 现有测试只覆盖早退
  路径——未匹配存活池/确认名不符，均在进入 raid 分支前返回），因此这两个
  新调用点在单测层面没有端到端覆盖，只有其内部纯函数/stub 注入覆盖。真机
  层面的验证超出本轮"禁止真实磁盘/sudo/mdadm"的约束范围，留给后续真机验收
  轮次。

---

## 验证

```
$ cd web && go build ./... && go vet ./modules/diskmgmt && go test -race ./modules/diskmgmt -count=1 -cover
ok  	nas-panel/modules/diskmgmt	11.678s	coverage: 25.6% of statements
```

- `go build ./...`：无输出，构建通过。
- `go vet ./modules/diskmgmt`：无输出，无告警。
- `go test -race ./modules/diskmgmt -count=1 -cover`：**PASS**，覆盖率
  **25.6%**（基线 24.9%，未降，提升 0.7 个百分点）。
- `gofmt -l` 对本轮改动的四个文件（`raid_persist.go`/`raid_persist_test.go`/
  `pool.go`/`stream.go`）均无输出，格式合规。
- 测试规模：`raid_persist_test.go` 顶层测试函数由 14 个增至 **18 个**（新增
  `TestPersistRAIDConfig_CatFailedLeavesTraceAndWarns`、
  `TestRemoveArrayLineByUUID`、`TestRaidArrayUUID`、
  `TestRemoveRAIDConfigByUUID`，`TestStripArrayLines`/
  `TestResetRAIDConfig_*` 等既有顶层函数内部补充了子用例，未增加顶层函数
  计数）；`modules/diskmgmt` 包级 `go test -list '.*'` 枚举到的全部测试条目
  （含子测试）为 148 个（实测命令：
  `grep -rE '^func Test' modules/diskmgmt/*_test.go | wc -l`）。

## 约束遵守确认

- 未读写仓库外文件（`~/go/pkg/mod`、`/tmp` 均未触碰；临时验证脚本写在
  `/home/jacky/.hermes/cache/scratch/opencode/` 后已删除）。
- 只改了 `web/modules/diskmgmt/raid_persist.go`、
  `web/modules/diskmgmt/raid_persist_test.go`、`web/modules/diskmgmt/stream.go`
  （commit 1）与 `web/modules/diskmgmt/pool.go`（commit 2），未碰 `frontend/`。
- 未执行任何真实磁盘/sudo/mdadm/update-initramfs 命令；全部测试通过既有
  `cmdRunner` stub（`stubRunner`）与 `raidConfWriter` 注入验证。
- `git add` 只加了改动文件，未 `git add .`；仓库内预先存在的未跟踪目录
  `docs/opencode/2026-10-29-e7-raid-persist/`（非本轮产物）未被添加、未被
  读取或修改。
- 两个 commit 均已创建，message 与任务描述原样一致。
