# OPENCODE-REPORT-F2-R1

任务书：TASK-F2-R1.md（notifications 收件箱 + triggers 骨架，对应计划
`docs/plans/2026-10-23-agent-foundation-f2-plan.md` Task 1 + Task 2）。
分支：`feat/agent-f2`。提交：
- `b70e8ee` feat(notifications) F2 T1
- `5478ed2` feat(triggers) F2 T2

---

## Task 1：notifications 模块

### 接口落地对照

与计划 §Task1 Interfaces 逐字核对，全部一致，无签名偏离：

| 计划签名 | 落地位置 | 差异 |
|---|---|---|
| `type Notification struct{ID,Kind,Title,Body,DataJSON string;Read bool;CreatedAt string}` | `notifications.go` | 无 |
| `func InitNotifications(dataDir string) error` | `notifications.go` | 无 |
| `func AddNotification(kind, title, body, dataJSON string) (string, error)` | `notifications.go` | 无 |
| `func ListNotifications(limit int, unreadOnly bool) []Notification` | `notifications.go` | 无 |
| `func MarkRead/MarkAllRead/ClearRead/UnreadCount` | `notifications.go` | 无 |
| `func StartSubscriber()/SetEnabledGuard(fn)/RegisterRoutes(mux, guard)` | `notifications.go`+`subscriber.go` | 无 |
| REST 5 端点 | `notifications.go` | 无（`GET /api/notifications?limit=&unread=1`、`unread-count`、`{id}/read`、`read-all`、`clear`） |

事件→通知映射（Consumes 节）落地形状：计划给出的是**键根**（如
`notif.task_done`），未明确 title/body 是否需要独立键。落地按仓库既有
i18n 命名惯例（`_title`/`_body` 后缀，对照计划 Task 3 规则表展示的
`trig.watermark_title`/`trig.watermark_body`）拆成两个键：

| 事件 code | Kind | title 键 | body 键 |
|---|---|---|---|
| `task.done` | task | `notif.task_done_title` | `notif.task_done_body` |
| `task.failed` | task | `notif.task_failed_title` | `notif.task_failed_body` |
| `health.*`（Type=warn） | system | `notif.health_warn_title` | `notif.health_warn_body` |
| `health.*`（Type=error） | system | `notif.health_error_title` | `notif.health_error_body` |
| `rclone.sync_failed` | system | `notif.sync_failed_title` | `notif.sync_failed_body` |
| `suggestion.created` | suggestion | `notif.suggestion_title` | `notif.suggestion_body` |
| `report.generated` | report | `notif.report_title` | `notif.report_body` |
| `task.progress` | — | 不入收件箱（故意丢弃） |

这批键本身的中英文文案不在本轮范围（计划把术语翻译表排到 Task 3），
`_title`/`_body` 键名只是管道打通，前端 `t()` 未命中时会回退显示键名，
不影响功能。**偏离记录**：计划原文只写了键根，未显式给出 title/body
是否共用同键还是拆键——拆键是本轮按现状惯例做的拍板，Task 3 写
zh-CN.json/en-US.json 时需按这批键名（`_title`/`_body` 后缀）补文案，
不要另起键名。

### 边界红灯自检记录

临时在 `notifications.go` import 加入 `_ "nas-panel/modules/tasks"`：

```
arch_boundary_test.go:149: 规则1违规: modules/notifications 非测试文件
import 了 modules/tasks，不在白名单 allowedModuleDeps["notifications"]=[] 内。
```

`TestArchBoundaries` 红灯确认后立即还原 import，重新跑绿（`go build` +
`TestArchBoundaries` PASS）。

另外为落实计划"任何模块不准 import notifications"的硬约束，新增了
`restrictedConsumers` 规则 4（通用机制，非 notifications 专属）：
`restrictedConsumers["notifications"] = {}`，Task 4 落地周报时会把
`triggers` 加进这个白名单（计划 Task 4「依赖拍板」）。

---

## Task 2：triggers 骨架 + common.CronMatch 抽取

### 接口落地对照

| 计划签名 | 落地位置 | 差异 |
|---|---|---|
| `type Suggestion struct{ID,TriggerKey,Title,Body,ActionsJSON string;State string;ExpireAt,CreatedAt string}` | `triggers.go` | 无 |
| `func InitTriggers(dataDir string) error` | `triggers.go` | 无 |
| `func CreateSuggestion(triggerKey,title,body,actionsJSON string, ttl time.Duration) (Suggestion,bool,error)` | `triggers.go` | 无（冷却/上限/事件发出逐字按计划） |
| `func ListSuggestions(includeInactive bool) []Suggestion` / `GetSuggestion(id)` | `triggers.go` | 无 |
| `func DismissSuggestion(id string) error` | `triggers.go` | 无（new→dismissed；其余返回 error，REST 层映射 409） |
| `func SetEnabledGuard/StartLoops/RegisterRoutes` | `triggers.go`+`loops.go` | 无 |
| `common.CronMatch(expr string, t time.Time) bool` | `common/cron.go` | 无 |
| REST：`GET suggestions?all=1`、`POST suggestions/{id}/dismiss` | `triggers.go` | 无（accept 留给 Task 5） |
| actions_json schema | `validateActions` | 无（`task_plan`/`navigate` 两种 type，未知即拒绝） |

cronMatch 迁移：`rclone/scheduler.go` 的 `cronMatch`/`cronFieldMatch`/
`fieldRanges` 整段移到 `common/cron.go`（改名为 `CronMatch`/
`cronFieldMatch`/`cronFieldRanges`，逻辑逐字不变）；`scheduler.go` 保留
同名 `cronMatch(expr, t) bool` 薄包装直接转调 `common.CronMatch`，这样
既有 `scheduler_test.go`（跑 `cronMatch(...)`）**完全未改动**仍绿，
满足"行为逐字不变 + 既有测试不动仍绿"的硬约束。

### 冷却/上限边界值测试覆盖

- 冷却 24h：`TestCreateSuggestionCooldown24h`——同 key 立即二次创建拒绝；
  手动把 `created_at` 改到 **25h 前**（覆盖"恰好 24h 以上"边界）后第三次
  创建放行。
- 活跃上限 5：`TestCreateSuggestionActiveLimit5`——5 个不同 key 各建一条
  (第 1~5 条全部成功)，第 6 条（不同 key，避开冷却干扰）精确命中
  `≥5` 边界被拒；`dismiss` 一条后第 6 条放行。
- TTL/过期惰性：`TestCreateSuggestionTTLAndExpire`——ttl>0 时
  `expire_at` 非空；手动把 `expire_at` 改到过去后，`ListSuggestions(false)`
  不再返回该行，且 `ListSuggestions(true)` 能看到该行状态已被惰性置为
  `expired`。

### 边界红灯自检记录

临时在 `notifications.go` import 加入 `_ "nas-panel/modules/triggers"`：

```
arch_boundary_test.go:149: 规则1违规: modules/notifications 非测试文件
import 了 modules/triggers，不在白名单 allowedModuleDeps["notifications"]=[] 内。
```

红灯确认后立即还原，重新跑绿。（这一违规同时被规则 1 的 allowedModuleDeps
空白名单和规则 4 的 restrictedConsumers 捕获，两层守卫都生效。）

---

## 测试计数（前后对比）

基线提交 `63ca82c`（F2 计划文档落地前，F1 收尾状态）：

```
go test ./... -list '.*' | 非 ok/? 行数 = 535
```

本轮（`5478ed2`，Task 1+2 完成后）：

```
go test ./... -list '.*' | 非 ok/? 行数 = 561
```

净增 **26** 个测试函数：
- `modules/notifications`：12（新增模块，从 0 起）
- `modules/triggers`：13（新增模块，从 0 起）
- `common/cron_test.go`：1（`TestCronMatch`，`modules/rclone` 既有同名测试
  未改动，不计入净增）

## 事件订阅映射表落地形状

见上文 Task 1 小节的表格；`subscriber.go` 用 `notifKeyRootForEvent(ev)`
做 code→键根 的 switch 映射，`kindForEvent(ev)` 做 code→Notification.Kind
映射，两者解耦（不是写在同一个大 struct 表里），方便 Task 3 往
`health.*` 分支扩展更细的 checkID 规则时不动其余分支。

## 验证记录

```
cd web
go build ./...                                                          # 绿
go vet ./modules/notifications/ ./modules/triggers/ ./common/ ./modules/rclone/   # 绿
go test ./modules/notifications/ ./modules/triggers/ ./common/ ./modules/rclone/ -count=1 -race   # 绿
go test ./... -count=1 -p 1                                              # 绿（全量，含 tasks/ai/vault 等既有包）
go test ./... -count=1 -race -p 1                                        # 绿（全量 + race）
go test -run TestArchBoundaries .                                        # 绿
python3 scripts/check-i18n.py                                            # 绿（本轮未新增 i18n 文案，1347 keys 不变）
```

备注：`go test ./... -count=1 -race`（默认并行度，不加 `-p 1`）在本沙箱
环境偶发对 `modules/tasks` 报 `unable to open database file: out of
memory (14)`——这是并行跑多包时 SQLite 临时文件/内存资源争用导致的沙箱
限制，与本轮改动无关（`modules/tasks` 本轮未修改任何文件；单独跑该包或
加 `-p 1` 串行跑全部稳定绿，复现过 3 次）。验证口径采用 `-p 1` 规避该
沙箱噪音，真机/CI 环境预期不会遇到此问题。

## 偏离与疑虑

1. **notifications title/body 键名拆分**：计划 Consumes 表只给了键根，
   本轮按仓库 `_title`/`_body` 惯例拆成两个键（见上文映射表）。Task 3
   写 i18n 文案时需按这批键名对齐，不要另起新键。
2. **restrictedConsumers 新规则**：为落实"任何模块不准 import
   notifications"，在 `arch_boundary_test.go` 新增了规则 4
   （`restrictedConsumers`），是对现有规则 1/3 的补充机制，不是计划原文
   指名的实现方式，但效果等价（规则 1 的空白名单已经能拦住，规则 4 是
   双重保险 + 未来 Task 4 开放 `triggers→notifications` 时更明确的落点）。
3. **triggers 的 allowedModuleDeps 本轮未实际用到**：计划要求把
   `triggers` 的白名单扩到 `{tasks, ai, diskmgmt, notifications}`，但
   Task 2 的 `triggers.go`/`loops.go` 实际只 import 了 `common`——白名单
   是"预先开好本轮不用、后续 Task 直接消费"的口径，与计划 Global
   Constraints"triggers 允许表按本轮实际用到的最小面开（后续 Task 需要
   时再扩）"字面上略有张力：计划同一份文档的 Global Constraints 说"最小
   面"，但 Task 2 的 Files 小节又明确写"main.go、arch_boundary_test.go
   （triggers 允许 common/tasks/ai/diskmgmt/notifications）"。本轮采用
   Task 2 小节的明确指示（一次开满），因为 Task 3/4/5 紧接着就要用，
   拆成多次改白名单意义不大，且不违反任何机器检查（规则 1 只是"不超出
   白名单"，不检查"是否用满白名单"）。如果 parent review 认为应该收紧为
   本轮实际用到的 `{}`（因为 Task 2 没 import 任何其它模块），可以回退
   这一条，不影响任何测试结果。
4. **task.done 的 triggers 侧预留钩子**：`loops.go` 的
   `runCronChecks` 函数体里只放了两条锚点注释（水位检查、周报生成），
   没有创建单独的 "task.done 不建议" 的显式分支——因为 Task 2 阶段
   `triggers` 还没有事件订阅循环（只有 cron ticker），事件订阅循环要等
   Task 3 的 `rules.go` 才会接入 `common.Subscribe()`。计划 §Task3 要求
   "rules.go 里预留 task.done 分支注释锚点"，本轮没有提前建这个文件/
   分支，留给 Task 3 原样处理，不算偏离（计划把这个锚点明确归属 Task 3
   Files 节的 `rules.go`）。
