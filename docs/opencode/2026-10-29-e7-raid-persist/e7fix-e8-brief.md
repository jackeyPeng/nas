# 任务：E7 审查回炉 + E8 删池清理 ARRAY 行（两个独立 commit）

你在 ~/soft/nas 仓库工作。基线 = HEAD (9100092)。第一部分修 E7 独立审查裁决（request_changes，2 important + 2 minor 采纳），第二部分修真机实锤新缺陷 E8。先读 raid_persist.go / raid_persist_test.go / pool.go raid 分支 / stream.go；与本文不符在报告申报。

## 硬约束（违反 = 返工）

1. 禁读/改仓库外文件（含 ~/go/pkg/mod、/tmp）；验证只用 web/ 内 go build/vet/test。
2. 只改 web/modules/diskmgmt/ 下代码与测试（第一部分明确不改 frontend/）。
3. 禁止真实磁盘/sudo/mdadm/update-initramfs 命令；测试用既有 cmdRunner stub + raidConfWriter 注入。
4. TASK-REPORT.md 覆盖写、不 git add；git add 只加改动文件；**两个 commit**，message 各自原样。
5. 既有测试全过；覆盖率基线 24.9% 不降。
6. 报告中的数字（测试数/覆盖率）必须实测后填写，不得凭记忆——上轮报告"23 个测试/覆盖率 25.0%"两项与实测不符（实为 14 个顶层测试函数/24.9%），本轮报告开头先更正声明。

## 第一部分：E7 审查回炉（commit 1）

### Fix A（Important）：persistRAIDConfig cat 失败留痕并透出 warning

raid_persist.go:202-205 现状：`existingConf, catErr := runner("cat", ...); if catErr != nil { existingConf = "" }` —— catErr 静默丢弃。现有 conf 存在但 cat 瞬时失败时会用仅含新 ARRAY 行的内容整文件覆盖（HOMEHOST/MAILADDR/其他阵列行全丢），写后验证只查目标 UUID 必过——正是要灭族的静默模式。

修法：catErr != nil 时——
1. `log.Printf("[RAID_PERSIST] 读取 %s 失败，按空文件重建（若文件实际存在，其原有内容将被覆盖）: %v", ...)`（包内先例 config_sync.go:52 的 [CONFIG_SYNC] 风格）；
2. 该文案并入返回的 warning（与 initramfs 警告可共存，分号拼接），调用方 SSE/steps 已会带出 warning，无需改调用方。
测试：cat 失败路径断言 warning 含"读取"字样且写入内容仅新 ARRAY 行（现有 ScanFailed 等测试形态参照）。

### Fix B（Important）：6 处测试 raidConfWriter stub 恢复写反

raid_persist_test.go 六处（TestPersistRAIDConfig_InitramfsWarning 及 5 个 TestResetRAIDConfig_*，约 :302/:354/:388/:413/:434/:457）：先赋值 stub 再 `orig := raidConfWriter`，Cleanup 恢复的是 stub 自身——全局状态泄漏。修法：六处统一改为先 `orig := raidConfWriter` 再赋值 stub（对照同文件 withRAIDConfWriterStub :187-197 与 regression_test.go:53 的正确写法；能直接复用 helper 的就复用）。修完在报告说明泄漏为何此前未爆红（残留 stub 方向恰好安全）。

### Fix C（Minor1）：stream.go 第 6 步 initramfs 警告 SSE 事件补 Index

stream.go:473 的 ProgressEvent 缺 Index（序列化 index:0），与同流其他事件（如 :553 Index: step+1）形态不齐。setupRAIDStream 内若有 step 计数闭包（stepRunning/stepDone 的实现机制），用同一机制取当前 index；确实取不到则给该函数补传/内建计数，不得留 index:0。

### Fix D（Minor4）：stripArrayLines 删除条件放宽为 ARRAY 前缀

现以 extractArrayUUID 命中为删除条件——不带 UUID= 的手写 ARRAY 行（仅 name=/devices/...）出厂重置后残留。修法：stripArrayLines 的删除判据改为大小写不敏感 `^ARRAY\b` 前缀（抽纯函数 isArrayLine(line) 或等价），upsert/写后验证等按 UUID 匹配的逻辑不动。补表驱动用例：无 UUID 的 ARRAY 行被删、非 ARRAY 行（含 "ARRAYX" 之类前缀陷阱）不误删。

### 记录不修（报告申报即可）

- Minor2（前端 SSE running 事件丢 Detail，警告到不了用户眼前）：跨轮前端缺口，另立项，本轮不碰 frontend/。
- Minor3（resetRAIDConfig 对无 ARRAY 的 CRLF 文件多做一次无害落盘）、Minor5（/usr/sbin/mdadm 硬编码 vs mdadmPath 不一致，sudo 场景绝对路径更稳）：记录不修。

### commit 1 message（原样）

```
fix(diskmgmt): E7审查回炉 — cat失败留痕透出warning/6处测试stub恢复写反修正/SSE警告补Index/重置删无UUID的ARRAY行 (E7审查)
```

## 第二部分：E8 删池清理 mdadm.conf ARRAY 行（commit 2）

### 真机实锤（2026-10-29）

RAID1 删池成功后 mdadm.conf 仍留被删阵列的 ARRAY 行；重建新池后 conf 里两条同指 /dev/md0 的 ARRAY 行（旧 UUID+新 UUID）共存，initramfs 同带——重启装配命名冲突风险 + 违反配置与内核对账原则。根因：handlePoolDelete raid 分支从不清理 mdadm.conf（E7 只接了建池与出厂重置）。

### 修法

1. raid_persist.go 新增：
   - 纯函数 `removeArrayLineByUUID(conf, targetUUID string) string`：删指定 UUID 的 ARRAY 行（大小写不敏感、CRLF 容错），其余行保序；targetUUID 空或无命中原样返回（CRLF 归一化）。表驱动测试（多 ARRAY 删中间/删唯一/无命中/空 conf/CRLF）。
   - `raidArrayUUID(runner cmdRunner, mdDev string) (uuid string, err error)`：`mdadm --detail --scan <mdDev>` 取 UUID（复用 findArrayUUIDForScan）；**调用方必须在 mdadm --stop 之前调**（stop 后 scan 不到）。
   - `removeRAIDConfigByUUID(runner cmdRunner, targetUUID string) (warning string, err error)`：cat 失败=跳过（同 resetRAIDConfig 语义）；removeArrayLineByUUID 后无变化直接返回；落盘 + 写后验证（目标 UUID 行已不在）；update-initramfs 失败 → warning 非 fatal。cat 失败同样按 Fix A 模式 log 留痕。
2. pool.go handlePoolDelete raid 分支接线：
   - **mdadm --stop 之前**（成员收集/安全拒绝闸门之后）调 raidArrayUUID；失败 → 不阻断删除，steps 追加"警告: 未能读取阵列 UUID，mdadm.conf 可能残留 ARRAY 行"（尽力而为但必须留痕）；
   - 拆除成功后调 removeRAIDConfigByUUID：err → steps 追加失败文案；warning → steps 追加警告文案；成功 → steps 追加"清理 mdadm.conf ARRAY 行"。
   - 既有 steps 形态/审计/jsonError 语义不动。
3. 测试：removeArrayLineByUUID 表驱动；raidArrayUUID/removeRAIDConfigByUUID stub 注入覆盖 scan 失败/无命中跳过/删除成功写后验证/initramfs 警告四路径；raidConfWriter stub 恢复用正确模式（Fix B 教训）。

### commit 2 message（原样）

```
fix(diskmgmt): 删池清理 mdadm.conf 被删阵列 ARRAY 行 + initramfs 跟进 — 灭重启装配命名冲突 (E8 真机实锤)
```

## 验证（必须执行，输出贴报告）

```
cd web && go build ./... && go vet ./modules/diskmgmt && go test -race ./modules/diskmgmt -count=1 -cover
```

## 报告（TASK-REPORT.md，不 git add）

开头更正声明（上轮 23 测试/25.0% 两项不实，本轮实测数字）；逐 Fix/E8 项：改动点、测试增改、覆盖率、偏差申报。
