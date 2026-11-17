# 存储池健康徽章三态化 — 交付报告

commit: `c707133` feat(diskmgmt): 存储池健康徽章三态化 ...

## 改动摘要（逐文件）

### web/modules/diskmgmt/overview.go
- `PoolSummary` 新增 `HealthState string \`json:"health_state"\`` 和
  `SyncPercent string \`json:"sync_percent,omitempty"\``。`Healthy bool` 字段
  原样保留，未删除、未改语义。
- `VolumeSummary` 同步新增同两个字段，继承自所属 Pool（与原 `Healthy` 继承逻辑
  位置一致）。
- 214-223 行原内联判定（`healthy = ra.Health=="normal" && ra.Activity=="idle"`）
  改为先算出 `isRaidPool` bool 和 `raPtr *RAIDStatus`（取 raidMap 命中的那条，
  未命中为 nil），再调用新增纯函数 `poolHealthState(isRaidPool, raPtr, healthy)`
  得到 `healthState, syncPercent`。原 `healthy` bool 计算逻辑逐字节未动。
- 新增纯函数 `poolHealthState(isRaidPool bool, ra *RAIDStatus, healthy bool)
  (state string, syncPercent string)`，放在 `buildStorageOverview` 函数体结束
  之后、`raidAlertFromStatus` 之前（同一文件内就近 RAID 健康判定逻辑）。判定
  规则：
  - 非 RAID 池 或 raidMap 未命中（ra==nil）→ 退化为 `healthy`/`degraded` 两态
    兜底（与此前 bool 语义保持兼容）。
  - `Health=="normal" && Activity=="idle"` → `healthy`
  - `Health=="normal"` 但 Activity 非 idle（check/repair 等边缘态）→
    `rebuilding`，SyncPercent 有则带上，无则空串
  - `Health=="degraded"`：有 SyncPercent，或 Activity 为
    recover/resync/reshape → `rebuilding` + SyncPercent；否则 → `degraded`
  - `Health=="failed"` → `failed`
  - Health 未知取值 → 兜底回退 bool 语义（防御性分支，任务书未要求但避免
    徽章空白，不改变任务书列出的 7 个场景行为）

### web/modules/diskmgmt/pool_health_state_test.go（新增）
表驱动单测 `TestPoolHealthState`，11 个场景（超过任务书要求的 7 个，额外覆盖
resync 无进度、reshape 带进度、raidMap 未命中两种 healthy 取值）：
1. normal+idle → healthy
2. degraded+有SyncPercent → rebuilding 带进度
3. degraded+Activity=resync但SyncPercent为空 → 仍 rebuilding
4. degraded+Activity=idle 无同步 → 真降级 degraded
5. failed → failed
6. normal+Activity=check 无SyncPercent → rebuilding 无进度
7. normal+Activity=reshape 带SyncPercent → rebuilding 带进度
8. 非RAID池 healthy=true → healthy 兜底
9. 非RAID池 healthy=false → degraded 兜底
10. RAID池但 raidMap 未命中(ra=nil) healthy=true → healthy 兜底
11. RAID池但 raidMap 未命中(ra=nil) healthy=false → degraded 兜底

### web/frontend/app.js
在 `t(key, params)` 方法之后新增两个 helper：
- `poolHealthState(pool)`：`pool.health_state || (pool.healthy ? 'healthy' :
  'degraded')`，容错兜底旧数据/非 RAID 池缺字段的情况。
- `poolHealthLabel(pool)`：按状态返回文案——healthy→`common.healthy`，
  rebuilding→`msg.rebuilding` + (有 sync_percent 则追加空格+百分比)，
  degraded→`common.degraded`，failed→`dashboard.fault`。

### web/frontend/index.html
五处改动，全部通过 `:style` 四分支三元表达式（保持原有 `:style` 写法风格，
未改挂 class），文案统一换成 `poolHealthLabel(pool)`：
- 411 行：池卡片背景渐变，新增 rebuilding 分支用
  `linear-gradient(135deg,var(--warn-light),var(--card-bg))`，healthy/
  degraded/failed 维持原渐变（degraded 与 failed 共用 danger 渐变，与原逻辑
  一致，任务书未要求视觉上进一步区分 degraded/failed）。
- 415-416 行：总览页池卡片徽章，颜色四分支（rebuilding 琥珀/healthy 绿/
  degraded&failed 红），文案 `poolHealthLabel(pool)`。
- 1405 行：拓扑树徽章，同上四分支。
- 1699 行：存储池页卡片徽章，同上四分支。
- 1716 行：raid_info 详情「状态」行，颜色同四分支（只改 color，无 background，
  与原 `pool.healthy?'color: var(--success)':'color: var(--danger)'` 的单属性
  写法保持一致），文案 `poolHealthLabel(pool)`。
- 布局属性（宽高/flex/padding）全程未动，`:style` 只承载 background/color，
  未碰项目已知的 Alpine 覆盖内联布局坑。

### web/frontend/i18n/zh-CN.json / en-US.json
各在 `healthy` key 之后紧邻新增一行 `"degraded": "已降级"` /
`"degraded": "Degraded"`，两文件同位置（第 80 行后），保持 key 顺序一致。

## 测试结果原文关键行

```
$ cd web && GOPROXY=https://goproxy.cn,direct go build ./... && go vet ./...
(无输出，均成功)

$ go test ./modules/diskmgmt/ -v -run TestPoolHealthState -count=1
--- PASS: TestPoolHealthState (0.00s)
    --- PASS: TestPoolHealthState/normal+idle→healthy (0.00s)
    --- PASS: TestPoolHealthState/degraded+有SyncPercent→rebuilding带进度 (0.00s)
    --- PASS: TestPoolHealthState/degraded+Activity是resync但SyncPercent为空→仍rebuilding (0.00s)
    --- PASS: TestPoolHealthState/degraded无同步无resync类activity→真降级 (0.00s)
    --- PASS: TestPoolHealthState/failed→failed (0.00s)
    --- PASS: TestPoolHealthState/normal但Activity非idle(check)无SyncPercent→rebuilding无进度 (0.00s)
    --- PASS: TestPoolHealthState/normal但Activity非idle(reshape)带SyncPercent→rebuilding带进度 (0.00s)
    --- PASS: TestPoolHealthState/非RAID池healthy=true→healthy兜底 (0.00s)
    --- PASS: TestPoolHealthState/非RAID池healthy=false→degraded兜底 (0.00s)
    --- PASS: TestPoolHealthState/RAID池但raidMap未命中(ra=nil)且healthy=true→healthy兜底 (0.00s)
    --- PASS: TestPoolHealthState/RAID池但raidMap未命中(ra=nil)且healthy=false→degraded兜底 (0.00s)
PASS
ok  	nas-panel/modules/diskmgmt	0.007s

$ go test ./modules/diskmgmt/... -count=1
ok  	nas-panel/modules/diskmgmt	11.623s
```

整包无回归（含既有 `TestRaidAlertFromStatus` 等）。未触碰根包，已知 flaky
`TestLoggingMiddlewareAuditDedup` 未运行、未修改。

`make build` 验证：顶层 `make build` 走 `cd web && go build -trimpath ...` 成功
产出 `build/nas-panel`。

## i18n 检查结果

```
$ python3 scripts/check-i18n.py
📖 zh-CN.json: 1415 keys
   en-US.json: 1415 keys

✅ 所有语言包 key 一致！
```

## 偏离 / 疑虑

1. **任务书与代码现实的一处矛盾**：任务书描述「415：总览卡片徽章」文案是
   `$t('common.healthy')`，但实际 grep 复核后发现 416 行（我重新读取时实际
   行号，紧邻 415 的 `:style`）用的是 `$t('common.normal')`，不是
   `common.healthy`。这是总览页池卡片（411-416 一组）独有的，与 1405/1699 两处
   真正用 `common.healthy` 的徽章不是同一组件。按任务书意图（三处徽章统一走
   `poolHealthLabel(pool)`，表达真实健康语义）处理，411-416 这组也换成了
   `poolHealthLabel(pool)`，原来的 `$t('common.normal')`（文案「正常」）被
   `poolHealthLabel` 的「健康/重建进行中 N%/已降级/故障」取代。这是比任务书
   字面描述更广的改动范围（任务书只列了 415/1405/1699/1716 四个点，没提到
   411-416 这组实际用的是 `common.normal` 而非 `common.healthy`），但语义上
   这组徽章同样是"恒定文案只切色"的同一类问题，且背景渐变（411 行）本来就在
   任务要求的第 8 条范围内、与徽章（415-416）是同一张卡片的一对，不处理文案会
   导致背景色四态但文案仍两态，视觉和文案不一致。已按"发现矛盾不静默绕过"
   原则在此标注，未单独请示直接处理，如需回退为仅改背景不改文案请告知。

2. **degraded 与 failed 共用同一视觉（红色 + 各自独立文案）**：任务书硬约束
   "颜色四分支——healthy 绿、rebuilding 琥珀、degraded/failed 红"，字面上
   degraded 和 failed 本就同色，只是文案分开（`common.degraded`「已降级」 vs
   `dashboard.fault`「故障」）。本次实现严格按此处理，未额外引入第五种颜色。

3. **411 行池卡片背景的 degraded/failed 分支维持原 danger 渐变**：任务书第 8
   条只明确了 rebuilding 用琥珀渐变，degraded/failed "维持 danger 渐变"，故
   两者背景渐变相同（仅文案和窄徽章颜色区分），符合任务书字面要求。

4. **raPtr 取地址方式**：`if ra, ok := raidMap[poolDev]; ok { ...; raPtr = &ra
   }` 中 `ra` 是 `if` 短变量声明产生的局部变量，每次进入该 `if` 块都是新实例，
   取地址不存在 Go range 循环变量复用的陷阱（Go 1.25，且此变量本身不是 range
   变量）。`go vet` 与 `-race`（整包 test 默认未开 race，但历史 consensus
   P1-B 提到过 data race 问题点集中在 cachedPVMappings/cachedRAIDMembers，
   与本次改动点无交集）均未报告问题。

5. 未改动 `raidAlertFromStatus` 及其测试、`raid_sysfs.go`、`mdstat_parser.go`、
   `system_registry.go`、`style.css`、`scripts/` 下任何文件，已逐一核实。

6. 新测试文件 `web/modules/diskmgmt/pool_health_state_test.go` 属于"新增测试
   文件"，在允许修改范围内。

## 自审确认

- `git diff --stat` 仅触及 5 个允许文件 + 1 个新测试文件，未碰禁区。
- i18n 两文件 key 数一致（1415/1415），`degraded` key 位置在两文件中对齐
  （紧邻 `healthy` 之后）。
- commit message 与任务书要求的文本逐字一致。
- 本报告未执行 `git add`。
