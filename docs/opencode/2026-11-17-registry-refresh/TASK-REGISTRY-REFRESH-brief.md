# TASK: 注册表在建池/删池/重置/导入/共享配置变更后自动刷新（快修轮 R1）

## 背景与缺陷（已实锤）

`.48/.57` 真机验收发现：`GET /api/system/check` 默认路径（无 `action=refresh`）只回读 registry.db 上次快照（`GetRegistry`，modules/system/system_registry.go:462），磁盘状态变更（建池/删池/重置/导入）后没有刷新钩子，面板上出现「安装时 41/41、建池后回读仍 32 pass + 9 warn（『存储池未配置』）」的自相矛盾。只有显式 `?action=refresh` 或 AI 读工具才真重算。

## 目标

磁盘状态变更完成后，注册表自动重算，面板默认读取即最新。**同步阻塞调用不可接受**：`RefreshRegistry()`（modules/system/system_registry.go:507）要跑约 41 项检查（大量 systemctl 子进程），不能在请求热路径上同步跑。

## 实现设计（照此实现，偏离须在报告中申报）

### 1. 新增 `modules/system/registry_refresh.go`

```go
// ScheduleRegistryRefresh 安排一次延迟注册表重算（尾随去抖）。
// 磁盘/共享配置变更完成后调用；3 秒窗口内的多次调用合并为一次重算，
// 避免「建池 + 建共享夹 + 建用户」连发触发多轮 41 项检查。
// 重算在后台 goroutine 执行，绝不阻塞请求路径。
func ScheduleRegistryRefresh()
```

- 包级实现要点：
  - `var registryRefreshMu sync.Mutex`（防并发重算互斥）+ `atomic.Bool`（scheduled 标志，已排程则直接返回——本次排程的定时器触发时会看到最新状态，合并即正确）。
  - `time.AfterFunc`（或单 goroutine + timer reset，二选一，倾向 `AfterFunc` 简单形态）延迟 3s 执行：先 CAS 清 scheduled 标志，再 `registryRefreshMu.Lock()` 串行跑 `refreshNow()`。
  - `refreshNow()` 内部调用包级变量 `registryRefreshFn func() RegistryReport`（默认 = `RefreshRegistry`，测试 seam，仓库既有模式参考 filemgmt 的 `getFolderMetaFn`/`poolRootOverride` 包变量注入）。错误/耗时仅 `log.Printf("[REGISTRY] 后台刷新...")` 留痕，绝不 panic、不向调用方返回错误。
  - **并发安全事实（已核实）**：`initRegistryDB()` 是 `sync.Once`（registryDB 包级单例），`RefreshRegistry` 与 `GetRegistry` 并发读同一句柄安全（SQLite WAL 经 `common.OpenSQLite`）。
- 禁止：不要给 `GetRegistry` 加 TTL/自动重算（面板每读一次就跑 41 项检查不可接受）；不要改 `handleSystemCheck` 的 action=refresh 语义。

### 2. 接入点（已逐个核实文件:行号，按行号就近插入，行号漂移以函数体为准）

diskmgmt 包新增调用前先 `import "nas/web/modules/system"`（**已核实 modules/system 不 import diskmgmt，无循环**）：

| 接入点 | 位置 | 插入时机 |
|--------|------|----------|
| 向导建池完成 | modules/diskmgmt/stream.go:193 附近的 `sendProgress(...Status:"complete"...)`（handleWizardSetupStream 尾部）之后 | 发完 complete 事件后 |
| 出厂重置完成 | stream.go:687-693 `Detail:"存储已重置，磁盘已释放"` 的 sendProgress 之后（handleWizardResetStream 尾部） | 同上 |
| 删池成功 | modules/diskmgmt/pool.go:779 附近（handlePoolDelete 成功响应 `存储池已删除` json 前） | 响应发出前后皆可 |
| 池扩容完成 | pool.go:375 `"message": "存储池扩容完成"` 所在 handler 的成功响应前 | 同上 |
| 池创建（非流式旧路径） | pool.go:283 `"message": "存储池创建完成"` 成功响应前 | 同上 |
| 导入池完成 | modules/diskmgmt/import.go:152 附近（`storage.pool_imported` EmitEvent 前后） | 同上 |
| 共享配置投影变更 | modules/diskmgmt/config_sync.go `SyncAllConfigs()` 尾部（`return nil` 前，只在 `changed==true` 时调——Samba/NFS 生成成功即配置面变了） | 尾部统一接 |

注意：`SyncAllConfigs` 在 12 个调用点被复用（含向导/建夹/用户变更），在它内部接一次即可覆盖共享 CRUD 面；磁盘六事件点再各接一次覆盖池生命周期。插入方式统一为 `system.ScheduleRegistryRefresh()` 一行（去抖保证合并）。

### 3. 测试（modules/system/registry_refresh_test.go）

用 seam 覆盖（不真跑 41 项检查）：
1. **合并**：连续 N 次（循环 10 次）`ScheduleRegistryRefresh()`，3s 去抖后 `registryRefreshFn`（换成计数 fake）只被调 1 次（测试里把延迟参数抽成包级 var `registryRefreshDelay`（默认 3s），测试改 50ms，避免真睡 3 秒）。
2. **串行**：fake 的 fn 里 sleep 制造重叠窗口，验证第二次排程等第一次完成后才跑（用 channel 收集开始/结束顺序断言不并行）。
3. **不阻塞调用方**：fn 里 sleep 200ms，断言 `ScheduleRegistryRefresh()` 调用本身 <50ms 返回。
4. **RefreshRegistry 真函数冒烟**：单测环境（无 systemd）调用默认 `RefreshRegistry()` 不 panic、返回的 Report 非空（既有 system_registry_test.go 已证明此环境可跑，沿用其环境假设）。

### 4. 文档（一并落地）

- CHANGELOG.md `[未发布]` 段新增一条（保持既有格式）：
  `- fix(registry): 磁盘状态变更（建池/删池/重置/导入/共享配置投影）后注册表自动后台重算（3s 尾随去抖，goroutine 执行不阻塞请求）——修「安装 41/41、建池后回读仍 9 条『存储池未配置』warn」的面板自相矛盾（真机实抓）；GetRegistry 快照语义不变，action=refresh 显式刷新保留`

## 硬约束（违反即返工）

- 只动这些文件：modules/system/registry_refresh.go（新建）、modules/system/registry_refresh_test.go（新建）、modules/diskmgmt/stream.go、modules/diskmgmt/pool.go、modules/diskmgmt/import.go、modules/diskmgmt/config_sync.go、CHANGELOG.md。其他一律不碰（前端零改动）。
- **禁止读取仓库外任何文件**（~/go/pkg/mod 模块缓存、/tmp 实验、repo 根之外的任何路径）——外部目录权限会被自动拒绝并中止整个 run。验证一律走 `go build ./...` 与 `go test ./modules/system/ ./modules/diskmgmt/ -count=1`（项目内测试框架）。
- 本 brief 里的符号/行号均已由 parent 核实过存在：`RefreshRegistry`/`GetRegistry`/`initRegistryDB`（system_registry.go:507/462/56）、`SyncAllConfigs`（config_sync.go）、六个接入点行号。若实际代码与 brief 冲突，**报告中申报矛盾，不要静默绕过**。
- 提交信息精确使用：
  `fix(registry): 磁盘状态变更后注册表自动后台重算（3s 去抖）——面板回读不再显示建池前的陈旧 warn`
- 单笔 commit。禁 git push。

## 产出报告

完成后把报告写到 `docs/opencode/TASK-REGISTRY-REFRESH-REPORT.md`（**不要 git add 此文件**，parent 审查后归档）：改动清单、`go build ./...` 输出尾行、两个包 `go test -count=1` 的真实输出尾行（含 PASS/FAIL 计数）、偏离申报（无则写「无偏离」）、遗留疑虑。
