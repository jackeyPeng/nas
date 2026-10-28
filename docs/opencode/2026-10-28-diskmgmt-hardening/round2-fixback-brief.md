# 任务：第二轮独立审查修复（编号回炉，逐条修，勿扩大范围）

你在 ~/soft/nas 仓库工作，Go module 在 `web/`。基线 = HEAD (602f1f8)。以下每条 finding 由独立审查代理产出、parent 已核实。硬约束同前轮：禁读仓库外文件（一次 external_directory 拒绝即整轮作废）、零新依赖、不全仓 gofmt、缺口申报不静默绕过。完成后构建+测试全绿，**一个提交**，message 原样：

```
fix(diskmgmt): 审查回炉 — 恢复被误删回归测试/错误响应JSON化灭假成功toast/resync首轮竞态/拒绝路径审计/separate非流式旁路封堵
```

报告写仓库根 TASK-REPORT.md（覆盖，不 git add）。

## 构建与测试

```
cd web && GOPROXY=https://goproxy.cn,direct go build ./... && go vet ./modules/diskmgmt/ && go test ./modules/diskmgmt/ -count=1 && cd .. && python3 scripts/check-i18n.py
```

## Finding 1（Important）— 恢复被误删的 TestFilterSelectedDisks

commit 602f1f8 重写 `web/modules/diskmgmt/stream_test.go` 时把 8609240 引入的 `TestFilterSelectedDisks`（5 个子用例：正常交集/交集为空/sel空串/sel含空白项/sel全空白）整体删除，生产函数 `filterSelectedDisks`（stream.go）现在零测试覆盖。修法：`git show 8609240:web/modules/diskmgmt/stream_test.go` 取回原测试函数原样并入现在的 stream_test.go（保留本轮已有的 3 个 separate 下线测试），跑通。

## Finding 2（Important）— 错误响应 JSON content-type：灭「pvs 失败仍弹删除成功」假 toast

证据链（已核实）：前端 app.js `api()` 对非 application/json 响应 `return res.text()`（字符串 truthy），deletePool 的 `if (data)` 判定通过 → pvs 失败 500 时用户看到「删除成功」toast 但池未删。仓库约定（pitfall 2d）：错误响应一律 JSON content-type，参照 `web/modules/ai/ai.go` 的 jsonError 模式。

修法：
- 在 diskmgmt 包内加 `jsonError(w http.ResponseWriter, msg string, code int)` helper（w.Header().Set("Content-Type","application/json") + w.WriteHeader(code) + json.Marshal 生成 `{"error":msg}` 体——用 Marshal 而非 Sprintf 拼接，杜绝 err 字符串含引号/反斜杠产出非法 JSON）。
- 迁移调用点（本轮两提交引入的 + 同函数存量的顺手一并迁，审查已授权）：
  - pool.go pvs 失败 500（c89fa1c 引入）；
  - pool.go 同函数 handlePoolDelete 内其余存量 `http.Error(w, `{"error":...}`, ...)`（confirm 缺失/确认名不匹配/未知池类型）；
  - maintenance.go handleReplaceDisk 的 raid0 400（c89fa1c 引入）及同函数其他 http.Error JSON 体；
  - diskmgmt.go debugDiskGate 的 403。
- 前端 deletePool 侧无需改动（api() 对 JSON error 已有 toast 路径）；如你核实后发现 deletePool 的 `if (data)` 对 `{error:...}` JSON 仍会走成功分支（data.error 存在时 api() 的行为自己读 app.js api() 实现确认），在报告中说明并给出最小前端守卫修法（`typeof data === 'object' && data.error` 不算成功），需要改就一起改。
- 单测：jsonError 输出 Content-Type 与 body 合法性（httptest），含 msg 带引号/反斜杠用例。

## Finding 3（Minor，安全相关，修）— waitForResync 首轮竞态

raid_expand.go waitForResync：mdadm --add/--grow 后内核把 resync 行写进 /proc/mdstat 有延迟，首轮轮询查不到 resync 行会立即判完成返回 nil → 提前 growFilesystem（阵列实际在重构）。修法：循环前先 `time.Sleep(2 * time.Second)`，并引入 cleanStreak 计数——连续 3 轮（约 6s）无 resync/recovery 行才判完成返回 nil；观察到过 resync 行后 cleanStreak 重新累计。600 轮总预算不变。更新/新增对应单测说明（无法真跑 mdstat 的部分在报告说明验证方式）。

## Finding 4（Minor，修）— 锁前拒绝路径补审计

raid_expand.go handleRAIDExpandStream 的 raid0 拒绝与「级别不支持」拒绝分支（锁前、直接 return 的两处）各补一条 `common.LogAuditRequest(r, "STORAGE", "扩展RAID阵列(流式)", fmt.Sprintf("md=%s device=%s rejected=%s", mdDev, device, <原因>), "rejected")`——被拒的破坏性请求不留痕是审计缺口。maintenance.go handleReplaceDisk 的 raid0 400 分支同样补一条。

## Finding 5（Minor，修）— separate 非流式旁路封堵

wizard.go 非流式 handleWizardSetup 仍保留 `case "separate"` 完整建池分支（约 :201/:243），直接 API 调用可绕过 B2 下线创建 separate 多池。修法：在 handleWizardSetup 入口加与 stream 版相同的拦截——mode=="separate" 时 JSON 400（用 Finding 2 的 jsonError）「separate 多池模式已下线，单池模型请使用 merge/RAID」；删除其 switch 里的 separate 分支与 setupSeparateDisks 函数（grep 确认无其他调用方后删；有则报告申报改拦截不删）。

## Finding 6（Minor，修）— 陈旧注释

options.go:9 RaidOption.ID 注释仍列 separate 为合法值，改为实际合法集合（single, merge, raid0, raid1, raid5, raid6）。

## 明确不修（本轮范围外，勿动）

- TOCTOU 锁外校验窗口（审查判 minor、mdadm 二次兜底）——S2 处理；
- diskMounts/loadDiskMounts 死代码与 i18n 死键——留给专门清理轮；
- waitForResync 以外的 raid_expand 结构。

## 报告要求

逐 finding：改动摘要、文件:行、Finding 2 的 api()/deletePool 核实结论与是否动了前端、Finding 3 的验证方式说明、测试清单与 `go test -v` 顶层 PASS 实际计数（如实报，本轮基线 66，恢复 TestFilterSelectedDisks 后应为 67+新增）、check-i18n 输出。
