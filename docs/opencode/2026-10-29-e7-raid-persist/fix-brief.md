# 任务：E7 — RAID 配置持久化链路修复（stream 路径吞错/无 initramfs 更新/无写后验证）

你在 ~/soft/nas 仓库工作。基线 = HEAD (fa6c1cb)。缺陷已真机实锤（见下），修法明确。先读涉及文件；与本文不符在报告申报，不得静默绕过。

## 硬约束（违反 = 返工）

1. 禁读/改仓库外文件（含 ~/go/pkg/mod、/tmp）；验证只用 web/ 内 go build/vet/test。
2. 只改 web/modules/diskmgmt/ 下代码与测试。
3. 禁止真实磁盘/sudo/mdadm/update-initramfs 命令；逻辑用可注入执行器测试（沿用包内既有 stub 模式，如 config_sync 测试的做法）。
4. TASK-REPORT.md 覆盖写、不 git add；git add 只加改动文件；单 commit，message 原样文末。
5. 既有测试全过；覆盖率基线 23.6% 不降。

## 真机实锤（Debian 13 测试机，RAID1 池，2026-10-28/29）

流式建池（setup-stream?mode=raid1，面板唯一实际入口）后重启：
- /etc/mdadm/mdadm.conf 的 ARRAY 行**有**写入（stream.go:461-470 工作了）
- 但 **initramfs 未更新**（stream.go 无 update-initramfs 调用；wizard.go:445 有）→ initramfs 内还是装机时的旧 conf → 早期装配无视 ARRAY 行 → **阵列以 md127 装配而非 md0**（名称漂移实锤）。fstab 按 UUID 挂载所以数据安全、面板 sysfs 链也跟上了（pool 显示 /dev/md127 healthy），但 conf 与现实不一致、md127 语义上是"外来/无名阵列"档位。

## 现状代码问题（已核实）

1. **stream.go setupRAIDStream 第 6 步（461-470）**：
   - `mdadmConf, _ := common.SudoOutput(...)` 与 `existingConf, _ := common.SudoOutput("cat", ...)` 双吞错——scan 失败时 mdadmConf 为空，仍 SafeWriteFile + stepDone("保存 RAID 配置")，假成功（R1 已灭族的模式，这里是漏网）；
   - 无 update-initramfs；
   - 无写后验证；
   - 无 UUID 去重——同阵列重复跑会叠 ARRAY 行。
2. **wizard.go setupRAID（441-446）**：err==nil && mdadmConf != "" 才写（失败静默跳过，steps 仍报"保存 RAID 配置"成功）；update-initramfs 结果忽略；无写后验证。
3. **reset.go:124 / stream.go:617**：出厂重置把 /etc/mdadm/mdadm.conf **整文件清空**——连 Debian 默认的 HOMEHOST/MAILADDR 一起抹掉；且改完 conf 不更新 initramfs（旧 initramfs 里残留已删阵列的 ARRAY 行）。

## 修法

1. 新文件（或并入 raid_sysfs.go，你定，报告说明）共享 helper：
   `persistRAIDConfig(runner, mdDev string) error` —— 语义：
   a. `mdadm --detail --scan` 取 ARRAY 行；失败或输出无该 mdDev 对应 UUID 的行 → 返回 error（调用方报 stepFailed，不得假成功）；
   b. 读现有 /etc/mdadm/mdadm.conf（cat 失败按空文件处理但要留痕）；
   c. **UUID 去重**：已有同 UUID 的 ARRAY 行 → 替换该行（设备名可能漂移，md0→md127 场景）；无 → 追加；
   d. SafeWriteFile 落盘；
   e. **写后验证**：再 cat 一次，确认目标 UUID 的 ARRAY 行在文件中，不在 → error；
   f. `update-initramfs -u`：失败不 fatal，但返回 warning 信息（调用方 stepWarning/step 文案带出），不得静默。
   执行器注入：包内目前**无**现成 runner 注入模式（既有测试均为纯函数表驱动）——本轮引入最小函数型 runner 参数，如 `type cmdRunner func(name string, args ...string) (string, error)`，生产路径传 common.SudoOutput/SudoExec 的包装，测试传 stub；纯逻辑（ARRAY 行去重/替换/验证谓词/stripArrayLines）另抽独立纯函数表驱动测试。
2. stream.go 第 6 步与 wizard.go RAID 分支都改调 persistRAIDConfig：stream 路径 error → stepFailed + return err；wizard 路径 error → steps 追加失败文案 + return（与既有失败路径形态一致）。
3. reset.go:124 / stream.go:617 改为**只删 ARRAY 行、保留其余内容**（HOMEHOST/MAILADDR 等默认行不动；文件不存在跳过），删完若有变化则 update-initramfs -u（失败留警告不阻断重置）。抽纯函数 `stripArrayLines(conf string) string` 表驱动测试（多 ARRAY/无 ARRAY/空文件/CRLF 容错）。
4. 测试：persistRAIDConfig 的成功/scan 失败/scan 空/写后验证失败/initramfs 警告五路径（注入 stub）；stripArrayLines 表驱动；ARRAY 行替换（同 UUID 异设备名）用例。覆盖率不降。

## 明确不做

- 不动 detectPoolDevice/overview（重启后名称漂移面板已自愈，实证过）；
- 不引入 pools 注册表/执行内核（阶段 2 另轮）；
- 不改 fstab 逻辑（UUID 挂载已验证正确）。

## 验证（必须执行，输出贴报告）

```
cd web && go build ./... && go vet ./modules/diskmgmt && go test -race ./modules/diskmgmt -count=1 -cover
```

## 提交（原样 message）

```
fix(diskmgmt): RAID 配置持久化链路 — stream 路径灭假成功/UUID 去重替换/写后验证/initramfs 更新,重置只删 ARRAY 行 (E7 重启装配实锤)
```

## 报告（TASK-REPORT.md，不 git add）

根因/修法、helper 放置位置与注入模式说明、新增测试清单、覆盖率、偏差申报。
