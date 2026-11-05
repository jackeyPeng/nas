# 任务：修复存储总览 RAID 告警把「初始重建中」误报为「已降级，数据处于风险中」

工作目录：/home/jacky/soft/nas（git 仓库，master，工作区干净）。纯后端 Go 改动 + 单测。

## 背景与根因（parent 已核实，直接采用，勿重新考证）

用户新建 RAID5 后处于初始重建（mdadm 以 2 活跃+1 spare 起阵，后台 recovery）。此时：
- sysfs 路径 `web/modules/diskmgmt/raid_sysfs.go` 的 `buildRAIDStatusFromSysfs`：switch 里 `Health=="degraded"` 分支优先，`State` 被置为 `"degraded"`（即使 `sync_action=recovery` 正在重建），同时 `SyncPercent="92.0%"` 有值。
- mdstat 路径 `buildRAIDStatusFromMdstat`（overview.go ~965-1034）：有进度行时 `State="resyncing"`，1027 行跳过 degraded 覆盖——两条路径对同一真实状态给出不同 State。
- 告警生成 `web/modules/diskmgmt/overview.go` 531-543 行：先按 `SyncPercent != ""` 组「正在重建/同步 x%」warning，随后 `if ra.State == "degraded" || ra.State == "FAILED"` 无条件覆盖成 critical「RAID %s 已降级，数据处于风险中，建议尽快处理」→ 用户在初始重建这个正常暂态看到最严重告警。

正确语义参照 `web/modules/system/system_registry.go` 的 `classifyMdArray`（254-266 行）：降级 + 有重建进度 → warn「初始重建 x%」；降级 + 无进度 → fail「阵列降级」。

## 修改要求

1. **只改告警判定，不改两条解析路径的 State/Health 推导**（前端徽章等消费方依赖现有 State 语义，动它超出本轮范围）。
2. 在 `web/modules/diskmgmt/overview.go` 中把 531-543 行的 RAID 告警判定抽成**纯函数**（如 `raidAlertFromStatus(ra RAIDStatus) *StorageAlert`，返回 nil 表示无需告警），原处调用。判定规则：
   - `State` 为 "active" 或 "clean" → nil（不告警，与现状一致）
   - `State == "degraded"` 或 `"FAILED"`：
     - `SyncPercent != ""` → level=warning，消息 `RAID %s 初始重建/同步中 %s，期间冗余降低，完成后自动恢复`
     - `SyncPercent == ""` → level=critical，消息保持现状 `RAID %s 已降级，数据处于风险中，建议尽快处理`
   - 其他非 active/clean 状态：`SyncPercent != ""` → warning `RAID %s 正在重建/同步 %s`；否则 warning `RAID %s 状态异常: %s`（与现状一致）
   - Health 字段可用作辅助判定但以上述 State/SyncPercent 规则为准（两条解析路径都能命中）。
3. 466-475 行 Issues 块逻辑保持现状不动（它已正确显示「正在同步 x%」）。
4. 消息为后端硬编码中文（现有模式），不加 i18n。
5. **单测**：在 diskmgmt 包现有测试文件旁新增表驱动测试（如 `raid_alert_test.go`），覆盖至少 6 场景：degraded+sync→warning 初始重建文案；degraded 无 sync→critical；FAILED→critical；resyncing+sync→warning 正在重建；其他异常 state 无 sync→warning 状态异常；active/clean→nil。fixture 直接用构造的 RAIDStatus 值（纯函数无系统调用）。
6. 验证：`cd web && GOPROXY=https://goproxy.cn,direct go build ./... && go vet ./... && go test ./modules/diskmgmt/ -run TestRaidAlert -v`（测试函数名按你实际命名调整），全部通过。已知既有 flaky：根包 TestLoggingMiddlewareAuditDedup 与本任务无关，勿动。

## 硬约束

- 只允许修改/新增：`web/modules/diskmgmt/overview.go`、新测试文件。禁止改 raid_sysfs.go、system_registry.go、前端文件。
- 读取仓库外任何路径（/tmp、~/go/pkg/mod 等）会被自动拒绝并中止整个 run——不要尝试；只通过 go test 验证。
- 发现本任务书与代码现实矛盾时：在报告中标记，不要静默绕过。
- 不要 git add 报告文件。

## 交付

1. 自审 diff 后提交，commit message 精确用：`fix(diskmgmt): 初始重建中的 RAID 不再误报「已降级数据风险」critical — 告警判定对齐注册表语义（降级+有进度=warning 重建中），抽纯函数 raidAlertFromStatus + 表驱动单测`
2. 写报告到 /home/jacky/soft/nas/TASK-RAID-ALERT-REPORT.md（不 add）：改动摘要、测试结果原文（go test -v 输出关键行）、偏离/疑虑。
