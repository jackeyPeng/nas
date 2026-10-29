# 任务报告：SyncAllConfigs 全局互斥 + SSE running 事件 Detail 保全

## 改动文件清单

### Commit 1（Item A：互斥）
- `web/modules/diskmgmt/config_sync.go`：加包级 `syncAllMu sync.Mutex`，`SyncAllConfigs()` 函数体首行 `Lock/defer Unlock`，把生成 Samba/NFS 配置 + reloadServices + 错误聚合整体纳入临界区。`sync` 包在该文件中已被导入（用于 `configDBOnce`、`rcloneReloadMu`），未新增 import。
- `web/modules/diskmgmt/config_sync_mutex_test.go`（新增）：并发 Lock/Unlock 冒烟测试，验证不死锁、临界区计数正确。**未**直接调用 `SyncAllConfigs()`——它内部会 shell 出 `sudo`/`systemctl`，测试环境不可控且任务书禁止真实执行，详见下方"偏离说明"。

### Commit 2（Item B：前端 Detail 保全）
- `web/frontend/app.js`：5 处 SSE 解析器（`ev.status === 'running'` 所在块）统一改法：
  - 行 2116（`startWizardSetup`，无 index/total 变体）
  - 行 2171（`wizardSetup`，带 index/total 变体）
  - 行 2331（pool extend-stream，无 index/total 变体）
  - 行 2406（RAID expand-stream，此前把 detail 拼进 name 的那处，改为独立 detail 字段并删除 done 分支里冲掉 name 的 `last.name = ev.step`）
  - 行 2466（reset-stream，带 index/total 变体，无 error 分支）

  （注：任务书里给的参考行号 2116/2171/2331/2406/2467 是改动前的；改动后因增删字符不换行，行号基本未变，第 5 处因上一处少了一行而从 2467 变为 2466。）

- `web/frontend/index.html`：约 1782 行 `progressSteps` 渲染模板，原来的单行 `<div style="display:flex...">` 包进外层纵向 `<div>`，下方加 `x-show="step.detail"` 的 detail 子行，用 `--warn` 警告色、`--fs-xs` 字号、左侧缩进对齐图标宽度（`calc(1em + var(--sp-2))`），不设 `white-space:nowrap`（允许换行）。

## 验收命令输出摘要

```
$ cd web && go build ./...
(无输出，成功)

$ go vet ./...
(无输出，成功)

$ go test ./modules/diskmgmt/ -count=1
ok  	nas-panel/modules/diskmgmt	12.7~12.8s

$ node --check web/frontend/app.js
(无输出，成功)
```

5 处 SSE 解析器 grep 校验（`ev.status === 'running'` 后一行均含 `detail` 字段）：
```
2116:  if (ev.status === 'running') {
2117:      this.progressSteps.push({ name: ev.step, status: 'running', detail: ev.detail || '' });
2171:  if (ev.status === 'running') {
2172:      this.progressSteps.push({ name: ev.step, status: 'running', index: ev.index, total: ev.total, detail: ev.detail || '' });
2331:  if (ev.status === 'running') {
2332:      this.progressSteps.push({ name: ev.step, status: 'running', detail: ev.detail || '' });
2406:  if (ev.status === 'running') {
2407:      this.progressSteps.push({ name: ev.step, status: 'running', detail: ev.detail || '' });
2466:  if (ev.status === 'running') {
2467:      this.progressSteps.push({ name: ev.step, status: 'running', index: ev.index, total: ev.total, detail: ev.detail || '' });
```

## 偏离任务书的决定及理由

1. **Item A 的"可选测试"未直接调用 `SyncAllConfigs()`**：任务书允许的测试方案是"并发调用不死锁/不 panic 的冒烟测试"。但 `SyncAllConfigs()` 内部链路（`GenerateSambaConfig`/`GenerateNFSConfig` → `common.SudoOutput("cat", "/etc/samba/smb.conf")` / `common.SafeWriteFile` → `reloadServices()` → `common.SudoExec("systemctl", "reload", "smbd")` 等）会真实 fork `sudo`/`systemctl` 子进程。任务书硬约束明确"禁止真实执行 sudo/systemctl/磁盘命令"。为了同时满足"要测并发安全"与"不许真跑 sudo/systemctl"两条要求，测试改为直接对 `syncAllMu` 做并发 Lock/Unlock 验证（20 个 goroutine 竞争同一把锁，验证不死锁、临界区内计数不被打乱），不经过 `SyncAllConfigs()` 这层业务逻辑。这与任务书"若现有测试基建允许（generator 走真实 /etc 路径则跳过）"的指导一致——这里就是 generator 会走真实 `/etc` 路径的情形，因此跳过对业务函数本身的调用，只测锁的并发语义。

2. **第 5 处 SSE 解析器实际行号from 2467变为2466**：任务书给出的是改动前的参考行号，属预期内的自然漂移（Item B 第 4 处改动减少了一行代码），已在上方逐处标注改动后的准确行号，不影响改法本身。

其余均严格按任务书执行，未改动 `GenerateSambaConfig`/`GenerateNFSConfig`/`reloadServices` 内部逻辑，未改动 error/complete/reshaping 分支行为，`:key="step.name"` 维持不变。
