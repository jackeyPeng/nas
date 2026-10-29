# 任务：SyncAllConfigs 全局互斥 + SSE running 事件 Detail 保全（前端）

两个独立小改动，两个独立 commit。

## 硬约束

- 只允许改动：`web/modules/diskmgmt/config_sync.go`、`web/frontend/app.js`、`web/frontend/index.html`（如需测试可加 `web/modules/diskmgmt/*_test.go`）。
- 验证只通过 `web/` 内 `go build ./...`、`go vet ./...`、`go test ./modules/diskmgmt/ -count=1`，以及 `node --check web/frontend/app.js`（如环境有 node）。
- 禁止真实执行 sudo/systemctl/磁盘命令；禁止读取仓库外文件。
- 不动 GenerateSambaConfig / GenerateNFSConfig / reloadServices 的内部逻辑。
- TASK-REPORT.md 写在仓库根，不 git add。

## Item A：SyncAllConfigs 全局互斥（commit 1）

现状：`web/modules/diskmgmt/config_sync.go:439` 的 `SyncAllConfigs()` 有 12 个调用点（folders.go/import.go/pending_ops.go/pool.go/reset.go/stream.go/users/create.go），并发编辑时配置生成+服务重载可能交错（已知问题 §八 #7）。

改法：
1. config_sync.go 加包级 `var syncAllMu sync.Mutex`（import sync，确认该文件是否已 import）。
2. `SyncAllConfigs()` 函数体第一行 `syncAllMu.Lock(); defer syncAllMu.Unlock()`，把「生成 Samba/NFS 配置 + reloadServices + 错误聚合」整体纳入临界区。
3. 已核实无重入风险：applyPendingOps（pending_ops.go:202）单向调用 SyncAllConfigs，SyncAllConfigs 内部不调 applyPendingOps，也不调任何会回调自身的函数。不要改成 RWMutex，不要引入 TryLock 语义。
4. 注释说明：串行化「folders.db → smb.conf/exports 投影 → 服务重载」整链，防并发编辑交错生成配置；单管理员场景锁竞争可忽略。
5. 可选测试：若现有测试基建允许（generator 走真实 /etc 路径则跳过），加一个并发调用不死锁/不 panic 的冒烟测试即可，不强求。

## Item B：SSE running 事件 Detail 保全（commit 2）

背景：后端流式操作（建池/扩容/RAID 扩展/重置）在 `web/modules/diskmgmt/stream.go` 会对非致命警告发 `ProgressEvent{Status:"running", Detail:warning}`（如 482/562/636 行的 initramfs 失败警告、采集纳管盘警告）。前端 app.js 有 **5 处**结构几乎相同的 SSE 解析器（约 2116/2171/2331/2406/2467 行的 `if (ev.status === 'running')`），问题：

- 4 处（2116/2171/2331/2467）running 分支 push `{name: ev.step, status:'running'}` —— **detail 直接丢弃**。
- 1 处（2406）running 把 detail 拼进 name，但 done 分支 `last.name = ev.step` 又把它**冲掉**。
- 所有 5 处的 done 分支「更新最后一个 running 步骤」路径都不携带/不保留 detail。

改法（5 处统一同一模式，保持各处现有的 index/total 字段差异不动）：

1. running 分支：push `{ name: ev.step, status: 'running', detail: ev.detail || '' }`（2171 那处保留原有 `index: ev.index, total: ev.total`；2406 处把拼接 detail 的旧写法改为独立 detail 字段，name 只放 ev.step）。
2. done 分支「更新最后一个 running」路径：**只改 status，不动 name/detail**（删掉 2406 处的 `last.name = ev.step`）；push 新条目路径带 `detail: ev.detail || ''`。
3. error / complete / reshaping 分支保持现状不动（error 已把 detail 拼进 name，行为不变）。
4. 渲染 `web/frontend/index.html` 约 1782 行的 `<template x-for="step in progressSteps">`：在步骤名 span 下方（同一外层 div 内改为纵向排布，或紧随其后加兄弟 div）增加 detail 子行：
   - `x-show="step.detail"`，`x-text="step.detail"`；
   - 样式：字号继承 var(--fs-xs)、颜色用警告色（查现有 CSS 变量，应有 --warning 或类似；没有就用 #b45309 一类琥珀色，别硬编码进 style 属性以外）、左侧留出与图标等宽的缩进，让它看起来是步骤的附属信息；
   - detail 可能较长（如 initramfs 警告），允许换行（不要 white-space:nowrap）。
5. `:key="step.name"` 维持不变（步骤名在同一流程内唯一，现状已如此）。

## 验收标准

- `go build ./... && go vet ./... && go test ./modules/diskmgmt/ -count=1` 全过。
- `node --check web/frontend/app.js`（有 node 时）过；5 处 SSE 解析器 grep `ev.status === 'running'` 后一行都能看到 detail 字段。
- index.html 渲染处能看到 detail 子行模板。
- 两个 commit 分开：A=互斥，B=前端 detail 保全。commit message 用中文，说明问题与修法。
- TASK-REPORT.md：改动文件清单、5 处 SSE 行号、验收命令输出摘要、任何偏离本任务书的决定及理由。
