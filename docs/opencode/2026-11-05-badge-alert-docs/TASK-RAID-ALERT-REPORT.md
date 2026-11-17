# 任务报告：修复 RAID 初始重建误报 critical「已降级数据风险」

## 改动摘要

- 文件：`web/modules/diskmgmt/overview.go`
  - 抽出纯函数 `raidAlertFromStatus(ra RAIDStatus) *StorageAlert`，放在 `buildStorageOverview` 之后、`detectPoolDevice` 之前。
  - 原 531-543 行内联判定逻辑替换为调用该函数，调用处只剩：
    ```go
    for _, ra := range raidStatuses {
        if alert := raidAlertFromStatus(ra); alert != nil {
            overview.Alerts = append(overview.Alerts, *alert)
        }
    }
    ```
  - 判定规则（按任务书实现）：
    - `State` 为 `active`/`clean` → `nil`
    - `State` 为 `degraded`/`FAILED`：
      - `SyncPercent != ""` → `warning`，`"RAID %s 初始重建/同步中 %s，期间冗余降低，完成后自动恢复"`
      - `SyncPercent == ""` → `critical`，`"RAID %s 已降级，数据处于风险中，建议尽快处理"`（文案不变）
    - 其他非 active/clean 状态：
      - `SyncPercent != ""` → `warning`，`"RAID %s 正在重建/同步 %s"`（文案不变）
      - 否则 → `warning`，`"RAID %s 状态异常: %s"`（文案不变）
  - 466-475 行 Issues 块未改动。
  - raid_sysfs.go、system_registry.go、前端文件均未改动。
- 新增文件：`web/modules/diskmgmt/raid_alert_test.go`
  - `TestRaidAlertFromStatus` 表驱动测试，7 个子场景（覆盖任务书要求的全部 6 类 + clean 单独验证）：
    1. degraded + SyncPercent → warning 初始重建文案
    2. degraded + 无 SyncPercent → critical 数据风险
    3. FAILED → critical 数据风险
    4. resyncing + SyncPercent → warning 正在重建/同步
    5. 其他异常 state（inactive）+ 无 SyncPercent → warning 状态异常
    6. active → nil
    7. clean → nil

## 测试结果原文

```
$ cd web && GOPROXY=https://goproxy.cn,direct go build ./... && go vet ./... && go test ./modules/diskmgmt/ -run TestRaidAlertFromStatus -v

=== RUN   TestRaidAlertFromStatus
=== RUN   TestRaidAlertFromStatus/degraded且有重建进度→warning初始重建文案
=== RUN   TestRaidAlertFromStatus/degraded且无重建进度→critical数据风险
=== RUN   TestRaidAlertFromStatus/FAILED状态→critical数据风险
=== RUN   TestRaidAlertFromStatus/resyncing且有进度→warning正在重建同步
=== RUN   TestRaidAlertFromStatus/其他异常state且无进度→warning状态异常
=== RUN   TestRaidAlertFromStatus/active→不告警
=== RUN   TestRaidAlertFromStatus/clean→不告警
--- PASS: TestRaidAlertFromStatus (0.00s)
    --- PASS: TestRaidAlertFromStatus/degraded且有重建进度→warning初始重建文案 (0.00s)
    --- PASS: TestRaidAlertFromStatus/degraded且无重建进度→critical数据风险 (0.00s)
    --- PASS: TestRaidAlertFromStatus/FAILED状态→critical数据风险 (0.00s)
    --- PASS: TestRaidAlertFromStatus/resyncing且有进度→warning正在重建同步 (0.00s)
    --- PASS: TestRaidAlertFromStatus/其他异常state且无进度→warning状态异常 (0.00s)
    --- PASS: TestRaidAlertFromStatus/active→不告警 (0.00s)
    --- PASS: TestRaidAlertFromStatus/clean→不告警 (0.00s)
PASS
ok  	nas-panel/modules/diskmgmt	0.007s
```

额外验证：整包 `go test ./modules/diskmgmt/...` 全绿（12.69s，含既有 raid_sysfs_test.go、mdstat_parser_test.go 等未受影响）。未触碰根包，未验证 `TestLoggingMiddlewareAuditDedup`（按任务书要求不动，本次改动也不涉及根包）。

## 偏离 / 疑虑

- 测试函数命名为 `TestRaidAlertFromStatus`（而非任务书示例中的 `TestRaidAlert`），与新增纯函数名 `raidAlertFromStatus` 对齐，任务书已注明"按实际命名调整"，故运行命令中的 `-run` 也相应改为 `TestRaidAlertFromStatus`。
- 无其他与任务书矛盾之处：读取的代码现实（531-543 行原逻辑、466-475 行 Issues 块、RAIDStatus 结构体字段）与任务书描述一致，未发现需要标记的矛盾。

## 提交

Commit: `fix(diskmgmt): 初始重建中的 RAID 不再误报「已降级数据风险」critical — 告警判定对齐注册表语义（降级+有进度=warning 重建中），抽纯函数 raidAlertFromStatus + 表驱动单测`

改动文件：
- `web/modules/diskmgmt/overview.go`（修改）
- `web/modules/diskmgmt/raid_alert_test.go`（新增）
