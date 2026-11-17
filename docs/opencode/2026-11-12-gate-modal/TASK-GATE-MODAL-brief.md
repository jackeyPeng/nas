# 任务：修复 beta.38「未登录访问登录页即弹出不可关闭的改密框」

仓库：/home/jacky/soft/nas（当前分支 master，工作区干净）。Go 模块在 web/ 子目录。

## 根因（已实锤核实，勿再花时间复查，直接按此修）

三层叠加：

1. **后端门禁跑在鉴权之前**：web/main.go 的 loggingMiddleware 里（约 L253-264），`common.IsDefaultPass() && !defaultPassGateAllowed(...)` 对所有请求生效——未登录（无 token 或无效 token）访问非白名单 /api/* 也得到 403 `{"error":"must_change_password","username":"fm"}`，而语义上未登录请求应由 AuthMiddleware 回 401。
2. **登录页上有三个不带守卫的 x-init 在启动时发 API**：web/frontend/index.html
   - L1342：`x-init="diskmgmtTab = diskmgmtTab || 'overview'; loadPendingOps()"`（diskmgmt 页 div，x-show 隐藏但 x-init 在 Alpine 初始化时照常执行）
   - L1726：`x-init="loadWizardStatus()"`
   - L1851：`x-init="loadOperationsLog()"`
   实测（Chrome performance entries）：全新无 token 打开登录页即发出 /api/disk/pending、/api/disk/wizard/status、/api/disk/operations 三个请求，全部撞门禁 403。
3. **前端 api() 无条件弹强制改密框**：web/frontend/app.js 约 L683-689，收到 `data.error === 'must_change_password'` 就置 `mustChangePassword = true; page = 'login'`，该模态（index.html L3892，z-index 10000）无关闭按钮 → 登录页被盖死。实测 Alpine 状态：`mustChangePassword:true, page:'login', token:''`。

已核实的安全前提：/api/disk/operations、/api/disk/pending、/api/disk/wizard/status 均包着 `common.AuthMiddleware`（modules/diskmgmt/diskmgmt.go L67/L86/L93），门禁放行未认证请求后会由 handler 层回 401，无安全回退。`common.ValidTokenFromRequest(r)` 已存在（web/common/auth.go L94），直接用。

## 修改清单（只许动这 4 个文件）

### 1. web/main.go — 门禁只对已认证请求生效

把门禁条件改为：

```go
if common.IsDefaultPass() && common.ValidTokenFromRequest(r) && !defaultPassGateAllowed(r.URL.Path, common.GetNASUser()) {
```

同步更新该处注释：说明门禁语义是「已登录会话不得在默认密码未改时使用系统」；未认证请求放给各路由的 AuthMiddleware 回 401（否则前端在未登录时收到 must_change_password 会误弹强制改密框）。

### 2. web/default_pass_gate_test.go — 补未认证放行用例

在现有中间件测试（TestDefaultPassGateMiddleware）基础上补：
- 无 Authorization 头访问非白名单路径 → 门禁不拦截（请求到达 next handler，或响应非 403 must_change_password）；
- 无效 token（`Bearer garbage`）→ 同上；
- 有效 token（用 `common.CreateToken(common.GetNASUser())` 生成；注意测试环境需已初始化，参考现有测试的 setup 方式）访问非白名单路径 → 仍 403 must_change_password。
保持现有白名单/纯函数用例语义不变。

### 3. web/frontend/app.js — api() 的 must_change_password 分支加登录态守卫（纵深防御）

约 L683-689：改为仅当 `this.token` 非空才走现有弹框逻辑；`this.token` 为空时执行 `this.logout(); return null;`（清掉可能的陈旧 token，静默留在登录页）。已登录分支行为一字不改。

### 4. web/frontend/index.html — 三个 x-init 加 page 守卫 + tab 改点击时加载

参照 files 页既有模式（L2024：`x-init="if(page==='files') loadFiles('')"`）：
- L1342 → `x-init="diskmgmtTab = diskmgmtTab || 'overview'; if(page==='diskmgmt') loadPendingOps()"`（diskmgmtTab 赋值是纯前端动作，保留在守卫外）
- L1726 → `x-init="if(page==='diskmgmt'&&diskmgmtTab==='wizard') loadWizardStatus()"`
- L1851 → `x-init="if(page==='diskmgmt'&&diskmgmtTab==='maintenance') loadOperationsLog()"`
- tab 按钮补点击加载（守卫后 x-init 不再兜底，切 tab 必须能拉到数据；参照 L1375 recycle 按钮的既有写法 `@click="diskmgmtTab='recycle';loadRecycleBin()"`）：
  - L1373 wizard 按钮：`@click="diskmgmtTab='wizard'"` → `@click="diskmgmtTab='wizard';loadWizardStatus()"`
  - L1374 maintenance 按钮：`@click="diskmgmtTab='maintenance'"` → `@click="diskmgmtTab='maintenance';loadOperationsLog()"`

注意：登录成功后 `navigate('diskmgmt')`（app.js L751）本来就会调 loadStorageOverview/loadWizardStatus/loadPendingOps 等，页面首次进入的数据加载不受守卫影响。

## 硬约束（违反 = 返工）

- 只改上面 4 个文件。不动 CHANGELOG.md / TODO.md / docs/（parent 负责补记）。
- 前端禁止新增内联 `<script>`、不动 CSP；沿用项目现有 Alpine 写法。
- Go 构建零 CGO。完成后必须自测：`cd /home/jacky/soft/nas/web && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./...`，全部通过。
- 禁止读取/访问仓库目录之外的任何路径（包括 ~/go/pkg/mod、/tmp 试验、任何绝对路径外部文件）——会被沙箱自动拒绝并中断整个 run。自测只通过 go test 在仓库内进行。
- 若发现任务书与代码现实矛盾（行号漂移属正常，按内容定位；语义矛盾才算），在报告里申报，不得静默绕过或自行扩大改动面。

## 产出

1. 一次 git commit（只含上述 4 个文件），commit message 首行：
   `fix(auth): 默认密码门禁对未认证访客误弹不可关闭改密框`
   正文简述：门禁先于鉴权对未登录请求回 403 must_change_password；登录页三个无守卫 x-init 触发；门禁收敛为仅对已认证请求生效，前端 api() 空 token 不弹框，x-init 加 page 守卫并改 tab 点击时加载。
2. 报告写到仓库根 TASK-REPORT.md（**不要 git add**）：改动摘要、go build/go test 结果（含测试数）、任何偏离与理由。
