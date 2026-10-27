# OPENCODE-REPORT-AI-R2：AI 助手 M1 第二轮（Task 4-5）

分支：`feat/ai-assistant`　提交：
- `d7a71ee` refactor(modules): 为AI工具域导出内核wrapper(...) (ai M1 T4)
- `959b811` feat(ai): nas域9个只读工具(...) (ai M1 T5)

## 一、衔接 R1 代码现实

落点确认（读代码而非凭计划假设）：
- `Tool{Name,Description,Write,Params,Exec}` / `ParamSpec{Name,Type,Description,Required,Enum}` / `ValidateArgs(Tool, raw string)` 与计划签名完全一致，直接复用。
- `Config`/`LoadConfig`/`SaveConfig`/`MaskAPIKey` 与计划一致，未碰。
- **偏离点**：计划设想 `logs.AuditEntry` 是 logs 包自定义类型、`logs.QueryAudit` 自己跑 SQL。实际读代码发现 `common/logger.go` 已有 `common.AuditEntry{ID,Timestamp,Username,Action,Method,Path,Detail,Result,IP}` 和 `common.QueryAuditLog(username,action,result string, days,limit,offset int) ([]AuditEntry,int,error)`，字段形状与计划表格描述（去掉 ID）完全吃得上。`logs.QueryAudit` 因此改为直接委托 `common.QueryAuditLog`，返回类型是 `[]common.AuditEntry`（没有重新定义 `logs.AuditEntry`）——这样零逻辑复制，且是最薄的导出。`get_audit_logs` 工具对这个返回类型直接 `marshalToolResult`，字段名随 `common.AuditEntry` 的 json tag（`time` 而不是 `timestamp`，与既有 `/api/logs` 接口保持一致）。

## 二、Task 4：导出 wrapper 清单

| 导出函数 | 内核 | 文件 |
|---|---|---|
| `monitor.Snapshot() map[string]interface{}` | `getMonitorSnapshot()`（sampler.go:83） | modules/monitor/export.go |
| `users.ListUsers() []NASUser` | `getUsers()`（users.go:71） | modules/users/export.go |
| `diskmgmt.OverviewData() StorageOverview` | `buildStorageOverview()`（overview.go，本轮从 handleStorageOverview 机械抽出） | modules/diskmgmt/export.go |
| `diskmgmt.SmartSnapshot() []DiskStatus` | `getDiskStatus()`（storage.go:161，走 smartctlCacheTTL 缓存） | modules/diskmgmt/export.go |
| `firewall.Status() FirewallStatus` | `getStatus()`（firewall.go:47） | modules/firewall/export.go |
| `diagnostics.StatusSnapshot() map[string]interface{}` | `buildStatusSnapshot()`（diagnostics.go，本轮从 handleStatus 机械抽出） | modules/diagnostics/export.go |
| `logs.QueryAudit(limit, sinceHours int) ([]common.AuditEntry, error)` | `common.QueryAuditLog(...)`（common/logger.go:108，= handleQueryLogs 用的同一条路径） | modules/logs/export.go |
| `system.GetRegistry()` / `system.RefreshRegistry()` | 已有导出（system_registry.go:441/486），未改动 | — |

每个 wrapper 都配 `export_test.go`：断言调用不 panic，依赖系统命令的路径在本开发机上均以优雅的空值/错误兜底返回（验证过：`which ufw` 缺失 → `firewall.Status().Installed=false`；`lsblk` 解析失败 → `diskmgmt.SmartSnapshot()` 返回 nil；审计 db 未初始化 → `logs.QueryAudit` 返回 error 不 panic）。

### 机械抽取前后测试对照

| 包 | 抽取前 | 抽取后 |
|---|---|---|
| diskmgmt | `go test ./modules/diskmgmt/... -count=1` → ok 0.138s | 同套 → ok 0.749s（含新增 export_test） |
| diagnostics | `go test ./modules/diagnostics/... -count=1` → ok 0.008s | 同套 → ok 0.009s（含新增 export_test） |

两次都是全绿，抽取只是把 handler 内联构造体包进 `buildStorageOverview()`/`buildStatusSnapshot()` 纯函数，handler 本体改成"调它 + 序列化"两行，没有改动任何判断逻辑、字段赋值顺序或数据来源。

## 三、Task 5：nas 域 9 个只读工具最终形态

文件：`web/modules/ai/tools_nas_read.go` + `tools_nas_read_test.go`。

| 工具 | 参数 | 数据源 | 输出裁剪 |
|---|---|---|---|
| get_health_report | refresh:boolean(可选) | system.RefreshRegistry()/GetRegistry() | items 只留 id/name/status/detail |
| get_smart_status | 无 | diskmgmt.SmartSnapshot() | device/health/temp/serial |
| get_monitor_snapshot | 无 | monitor.Snapshot() | 原样 |
| get_storage_overview | 无 | diskmgmt.OverviewData() | pools(name/display_name/type/size/used/healthy)+issues |
| get_user_list | 无 | users.ListUsers() | username/services/quota_gb |
| get_audit_logs | hours:integer(默认24/上限168), limit:integer(默认50/上限200) | logs.QueryAudit | 原样（common.AuditEntry 全字段） |
| get_firewall_rules | 无 | firewall.Status() | active+rules(num/port/proto/action/from/comment) |
| get_diagnostics_status | 无 | diagnostics.StatusSnapshot() | 原样 |
| search_files_by_name | pattern:string(required) | 占位 | `{"note":"文件搜索将在后续版本提供"}` |

统一输出：`marshalToolResult` = `json.Marshal` + `truncateOutput`（8000 字符上限）。`hours`/`limit` 的 clamp 在 Exec 内用 `clampInt` 完成（ValidateArgs 只管类型/枚举，不管数值范围——与计划"clamp 逻辑在 Exec 内"一致）。

## 四、偏离与疑虑（需人工关注）

1. **截断实现与计划字面表述有出入，已验证是必要偏离**：计划原文"超出尾部追加 `...(截断)`"字面读是对 JSON 原文做字符位置截断。实测 `get_monitor_snapshot` 在本机真实环境下输出超 8000 字符（`top_procs` 里混进了一条巨长的 shell 命令行，含大量 `\uXXXX` 转义），按字符位置硬切会切断转义序列/字符串边界，产出的不再是合法 JSON（`json.Valid` 为 false，Task 5 测试步骤明确要求 `json.Valid`）。改为：超限时把原文前 8000 字符重新包进 `{"truncated":"...<前缀>...(截断)"}` 这个新的合法 JSON 对象里，而不是返回半截的原 JSON。效果上仍是"尾部追加截断标记"，只是外层多包了一层对象以保证合法性。认为这是计划意图的忠实实现（而不是走捷径），但**请人工确认该偏离可接受**，否则 M3/M4 继续往这个工具表加字段时可能复现同样的边界问题。

2. **`get_diagnostics_status` 的 nil db 兜底**：`diagnostics.StatusSnapshot()` 读包级 `db`，这个变量只在 `diagnostics.RegisterRoutes()` 里被 `initDB()` 赋值。main.go 里 `diagnostics.RegisterRoutes(mux)`（第 151 行）确实排在未来会加的 `ai.RegisterRoutes(mux)`（第 158 行，R1 已接好）之前，生产环境下 db 恒已初始化，不会触发。但 Task 5 要求"每个工具 Exec 在开发机不 panic"独立成立，不应该依赖"先跑了其他包的 RegisterRoutes 作为前提"——于是在 `execGetDiagnosticsStatus` 里加了一层 `recover()`，panic 时返回 `{"error":"诊断状态暂不可用"}` 而不是让整个 agent loop 因为工具调用顺序偏差崩掉。这不算碰 diagnostics 包内部逻辑（硬约束 3 只限制 diagnostics.go 本身的机械抽取范围），但**这是一个新增的防御行为，不在计划原文字面范围内**，一并标出来请确认是否要收窄为"仅测试环境需要"或者保留为生产兜底（我倾向保留：工具调用链比 HTTP handler 链更容易出现意外的调用顺序/生命周期组合，agent loop 本身在 M1 Task 6 也会是"错误回填继续"语义，这层 recover 和那个语义是一致的）。

3. **未对 `get_audit_logs` 字段名做任何改名**：直接用 `common.AuditEntry` 的 json tag（`time`/`username`/`action`/`method`/`path`/`detail`/`result`/`ip`，外加 `id`），与计划表格里的字段名描述（Timestamp,Username,Action,Method,Path,Detail,Result,IP）语义一致、tag 命名不同（`time` vs 期望的 `timestamp`）。因为是复用既有类型而不是新定义，没有自由改 tag 的空间；若后续前端/LLM 对字段名有强假设，需要在 Task 6+ 补一层字段名映射，目前没有这个需求信号就没加。

4. **没有改动 R1 任何既有文件**：`ai.go`/`config.go`/`domain.go`/`llm.go` 及对应 `_test.go` 零改动，`main.go` 也未改（`ai.RegisterRoutes(mux)` 接线是 R1 已完成的，本轮未涉及）。Registry 组装（把 `nasReadTools()` 接进 `NewRegistry`）本轮**没有做**——计划 Task 5 的 Files/Interfaces 只要求产出 `nasReadTools() []Tool`，真正组装进 Registry 是 Task 6（agent loop）范围，故未动 `ai.go`。

## 五、验证记录

```
cd web && go build ./... && go vet ./modules/...                     # 无输出，全绿
go test ./modules/ai/ ./modules/monitor/ ./modules/users/ \
  ./modules/diskmgmt/ ./modules/firewall/ ./modules/diagnostics/ \
  ./modules/logs/ ./modules/system/ -count=1                          # 8 个包全部 ok
go test -race ./modules/ai/ -count=1                                  # ok（6.0s）
```

无遗留临时文件（diagnostics export_test 验证过不会在 `/opt/nas/data` 不可写时在包目录留下 `diagnostics.db`）。
