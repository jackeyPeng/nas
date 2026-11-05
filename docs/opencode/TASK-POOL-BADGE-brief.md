# 任务：存储池健康徽章三态化（健康 / 重建中 / 已降级），重建中不再显示红色「健康」

工作目录：/home/jacky/soft/nas（git 仓库，master，HEAD 0c527fe，工作区干净）。后端 Go + 前端 Alpine.js + i18n。

## 背景与根因（parent 已核实，直接采用）

RAID5 初始重建期间 `PoolSummary.Healthy=false`（overview.go 214-223：`healthy = ra.Health=="normal" && ra.Activity=="idle"`），前端徽章只有二态：
- 文案恒为 `$t('common.healthy')`（「健康」），只切颜色 → 重建/降级期间显示**红色的「健康」**，自相矛盾；
- 重建中（正常暂态）与真降级（需处理）观感无区分，都是红。

徽章渲染点（frontend/index.html，行号以当前 HEAD 为准，编辑前先 grep 复核）：
- 411：池卡片背景渐变 `pool.healthy ? primary 渐变 : danger-light 渐变`
- 415：总览卡片徽章
- 1405：拓扑树徽章
- 1699：存储池页徽章
- 1716：raid_info 详情里的「状态」行（同样恒「健康」只切色）

已有可复用件（勿重复造）：
- i18n：`msg.rebuilding` = 「重建进行中」/「Rebuilding」；`common.healthy` = 「健康」/英文对应；`dashboard.fault` = 「故障」。**缺「已降级」key，需新增**（建议 `common.degraded`，zh「已降级」en「Degraded」，两个语言包同位置加）。
- CSS：`.badge-warn { background: var(--warn-light); color: var(--warn); }`（style.css:192）、`.badge-err`（191 附近）。
- 后端：`RAIDStatus` 已有 `Health`（normal/degraded/failed）、`Activity`（idle/recover/resync/reshape/check/repair）、`SyncPercent`（"N.N%"）字段；上一轮 `raidAlertFromStatus`（overview.go）的判定语义可参考但**不要改它**。

## 修改要求

### 后端（web/modules/diskmgmt/overview.go）

1. `PoolSummary` 增加两个字段：`HealthState string \`json:"health_state"\``（取值 `healthy`/`rebuilding`/`degraded`/`failed`）和 `SyncPercent string \`json:"sync_percent,omitempty"\``。`VolumeSummary` 同步继承这两个字段（现在 264 行 Volume 继承 Healthy，一样继承新字段）。
2. 判定抽成**纯函数**（如 `poolHealthState(isRaidPool bool, ra *RAIDStatus, healthy bool) (state string, syncPercent string)`），在 214-223 处调用：
   - RAID 池且 raidMap 命中：
     - `ra.Health=="normal" && ra.Activity=="idle"` → `healthy`
     - `ra.Health=="degraded"` 且 `ra.SyncPercent != ""`（或 Activity 为 recover/resync/reshape）→ `rebuilding` + SyncPercent
     - `ra.Health=="degraded"` 无同步 → `degraded`
     - `ra.Health=="failed"` → `failed`
     - `ra.Health=="normal"` 但 Activity 非 idle（如 check/repair，无 SyncPercent 的边缘态）→ `rebuilding`（有 SyncPercent 就带上）
   - 非 RAID 池或 raidMap 未命中：`healthy ? "healthy" : "degraded"`（保持现有 bool 语义兜底）。
3. `Healthy` 字段保留不动（前端旧引用与 API 兼容）；`healthy` 与 `health_state=="healthy"` 必须一致。
4. **单测**：新增表驱动测试覆盖纯函数全部分支（至少 7 场景：normal+idle / degraded+sync / degraded 无 sync / failed / normal+check 非 idle / 非 RAID healthy / 非 RAID unhealthy）。
5. 验证：`cd web && GOPROXY=https://goproxy.cn,direct go build ./... && go vet ./... && go test ./modules/diskmgmt/ -v -run TestPoolHealthState`（函数名按实际调整），再整包 `go test ./modules/diskmgmt/...` 确认无回归。已知既有 flaky：根包 TestLoggingMiddlewareAuditDedup，勿动。

### 前端（web/frontend/index.html + app.js + i18n）

6. app.js 加两个 helper 方法（Alpine 数据对象内）：
   - `poolHealthState(pool)`：返回 `pool.health_state || (pool.healthy ? 'healthy' : 'degraded')`（容错兜底）。
   - `poolHealthLabel(pool)`：healthy → `this.t('common.healthy')`；rebuilding → `this.t('msg.rebuilding') + (pool.sync_percent ? ' ' + pool.sync_percent : '')`；degraded → `this.t('common.degraded')`；failed → `this.t('dashboard.fault')`。
7. 徽章三处（415/1405/1699）改为：文案 `x-text="poolHealthLabel(pool)"`；样式按状态四分支——healthy 绿（success-light/success）、rebuilding 琥珀（warn-light/warn）、degraded/failed 红（danger-light/danger）。用现有 token，可继续 `:style` 或改挂 `.badge-warn`/`.badge-err` 类，二选一但三处一致。**布局属性（宽高/flex）不要放进 :style**（Alpine 会覆盖内联布局——项目已知坑）。
8. 411 行池卡片背景：rebuilding 时用琥珀系渐变（`linear-gradient(135deg,var(--warn-light),var(--card-bg))`），degraded/failed 维持 danger 渐变，healthy 维持现状。
9. 1716 行「状态」行：文案同样用 `poolHealthLabel(pool)`，颜色四分支同上。
10. i18n：zh-CN.json 与 en-US.json 各加 `common.degraded`（「已降级」/「Degraded」），加在 `common.healthy` 附近保持两文件 key 顺序一致；跑 `python3 scripts/check-i18n.py` 确认双语同步无缺 key。

## 硬约束

- 允许修改：`web/modules/diskmgmt/overview.go`、新测试文件、`web/frontend/index.html`、`web/frontend/app.js`、`web/frontend/i18n/zh-CN.json`、`web/frontend/i18n/en-US.json`。禁止改：raid_sysfs.go、mdstat_parser.go、system_registry.go、style.css（现有 token/类够用）、scripts/。
- 不要改 `raidAlertFromStatus` 及其测试（上一轮刚定版）。
- 读取仓库外任何路径（/tmp、~/go/pkg/mod 等）会被自动拒绝并中止整个 run——只通过 go test / check-i18n.py 验证，不做浏览器验证（parent 负责真机 E2E）。
- 行号可能因编辑漂移，动手前先 grep 定位复核；发现任务书与代码现实矛盾时在报告中标记，不要静默绕过。
- 不要 git add 报告文件。

## 交付

1. 自审 diff（确认没碰禁区文件、i18n 两文件 key 对齐）后提交，commit message 精确用：`feat(diskmgmt): 存储池健康徽章三态化 — 重建中琥珀(带进度)/已降级红/健康绿，修复降级期红色「健康」矛盾文案；health_state+sync_percent 字段 + 纯函数判定 + 表驱动单测 + i18n 双语`
2. 写报告到 /home/jacky/soft/nas/TASK-POOL-BADGE-REPORT.md（不 add）：改动摘要（逐文件）、测试结果原文关键行、i18n 检查结果、偏离/疑虑。
