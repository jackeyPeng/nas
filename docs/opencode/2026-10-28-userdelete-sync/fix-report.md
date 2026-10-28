# TASK-REPORT：删用户后立即重投影 SMB/NFS 托管段

基线：HEAD f28b189

## 根因（文件:行）

- `web/modules/users/users.go:358-369`（修改前）：`handleUserAction` 的 DELETE 分支调用 `removeUser()` 成功后直接 `common.JSONResponse(...)` 返回，没有任何配置重投影动作。
- `web/modules/users/users.go:489`（修改前行号，现为 509 附近）：`removeUser()` 只调用 `sudo /opt/nas/scripts/remove-user.sh`，该脚本删 folders.db home 条目、smbpasswd -x、FTP 白名单、WebDAV htpasswd、系统用户，但脚本自身收尾提示就写明"SMB 共享会在面板下次「配置同步」后消失，或手动触发"（remove-user.sh:102，脚本本身不在改动范围内，未修改）。
- `web/modules/diskmgmt/config_sync.go:585-623`（`CheckConfigConsistency`）：最近新增的托管段真比对（`managedSectionDrifted`，603/620 行）会把"smb.conf/exports 里残留已删用户的 `[username]` 托管段，但 folders.db 已经没有这条记录"的情况判为"Samba/NFS 托管段与 folders.db 不一致（配置漂移）"，直到手动调用 `POST /api/disk/config/sync`（`handleConfigSync` → `SyncAllConfigs`，config_sync.go:845-860）才消除。

结论与任务描述一致：删用户与配置重投影之间缺一次自动同步，真机现象（aitest01 残留 `[aitest01]` 段、`has_issues=true`，手动 sync 后消失）与代码逻辑完全对得上。

## 修法

`web/modules/users/users.go`：

1. import 新增 `log` 和 `nas-panel/modules/diskmgmt`。
2. 新增包级可替换函数变量：
   ```go
   var syncConfigsAfterUserDelete = diskmgmt.SyncAllConfigs
   ```
   生产默认值直接绑定 `diskmgmt.SyncAllConfigs`；单测可整体替换为计数替身，不触发真实系统调用。
3. `handleUserAction` 的 DELETE 分支，`removeUser()` 成功返回之后、`common.JSONResponse` 之前：
   - 调用 `syncConfigsAfterUserDelete()` 立即重投影 Samba/NFS 托管段。
   - 若返回 error：`log.Printf` 记一条 `[USERS] 用户 %s 已删除但配置同步失败，需手动配置同步` 日志；同时调用 `common.LogAuditRequest(r, "USERS", "删除用户", "username=... config_sync_error=...", "success")` 补一条审计明细（`result` 仍为 `"success"`，因为用户删除这件事本身确实成功了，只是收尾同步失败，与任务要求"记审计/日志，仍返回删除成功"一致）。
   - 响应体在 `message` 不变的前提下，失败时附加 `config_sync_error` 字段透出原因；`SyncAllConfigs` 成功或未调用失败分支时响应体里没有这个字段。
   - 只对 DELETE 这一条路径生效，create/password/services/quota 分支未改动。

## import 无环证据

`diskmgmt` 包未 import `users`（已用 grep 核实 `web/modules/diskmgmt/*.go` 无 `nas-panel/modules/users` 引用）。`users` 包内 `create.go` 本来就 import 了 `nas-panel/modules/diskmgmt`（`EnsureUserHome`/`GetAllFolderMeta`/`SyncFolderMeta`/`SyncAllConfigs` 早已在用），`users.go` 加同一个 import 只是同包内第二处引用，编译期可直接验证：

```
$ cd web && go build ./...
(无输出，构建成功，无环)
```

## create / delete 对称性核查（仅申报，未改动 create 路径）

- Create 路径：`handleCreateUser`（create.go:23 起）步骤 1.5（create.go:152-157）调用 `diskmgmt.EnsureUserHome(username)`。该函数（config_sync.go:906-929）内部：创建主目录 → `SyncFolderMeta` 写 folders.db → **`return SyncAllConfigs()`**。即 create 路径在"写用户主目录元数据"这一步之后立即重投影托管段，失败时在向导步骤列表里追加一条 `⚠️ 创建主目录失败: %v` 提示（create.go:154），**不阻断**整条创建流程（向导其余步骤继续跑），这与本次 delete 修法的"失败不阻断、仅提示"语义一致。
- 另外 `handleUpdatePermissions`（users.go 原文件内，实际在同文件 ~458-490 行区域，matrix 权限编辑路径）也会 `diskmgmt.SyncFolderMeta` + `return diskmgmt.SyncAllConfigs()`，同样是"改完元数据立即重投影"的模式。
- Delete 路径修改前是这条模式里唯一的缺口：`removeUser` 只收口到 shell 脚本删凭据，没有对应的 `SyncAllConfigs()` 收尾。本次修复后，create（经 `EnsureUserHome`）与 delete（经新增的 `syncConfigsAfterUserDelete`）两条路径现在都在各自的"凭据/元数据变更成功"之后立即调用 `SyncAllConfigs()`，一致。
- 结论：**create 路径没有同类缺口**——它从一开始就是"元数据变更后立即同步"的正确模式，是本次 delete 修复参照的标杆，不是另一个需要修的对称缺口。本轮未改动 create 任何代码。

## 测试方式与覆盖边界（如实说明）

受限：`removeUser()` 对合法用户名会真正走 `common.SudoExec("/opt/nas/scripts/remove-user.sh", ...)`，这是真实系统命令，按仓库既有约定（系统命令包装层不 mock）和任务硬约束 #3（禁止执行真实磁盘/sudo/systemctl/samba 命令），单测不能用合法用户名把 DELETE 分支从 `removeUser` 成功一路跑到 `syncConfigsAfterUserDelete()`。因此采用任务给出的两个可行方案中的"替换点"方案，分三层测试覆盖能测到的部分，如实标注测不到的部分：

1. `TestHandleUserAction_Delete_InvalidUsername_SkipsConfigSync`（新增）：用非法用户名（`".."`）让 `removeUser` 在真正执行 `SudoExec` 之前就因 `isValidUsername` 短路失败，断言：
   - handler 返回 500（`removeUser` 失败分支，印证没有滑向真实系统调用）；
   - 替身 `syncConfigsAfterUserDelete` 调用计数为 0 —— 证明 sync 调用确实挂在"`removeUser` 成功"之后，不是无条件触发在 DELETE 分支入口。
   这一测试不能证明"成功路径会调用一次"，只能证明"失败路径不会误调用"。
2. `TestHandleUserAction_Delete_ConfigSyncFailure_DoesNotFailDelete`（新增）：不经过完整 `handleUserAction`（会触发真实 `removeUser`），直接复用 DELETE 分支收尾那段"组装响应 + 判断 sync 错误"的等价逻辑，断言 `syncConfigsAfterUserDelete` 返回 error 时 `message` 保持"已删除"语义不变、`config_sync_error` 字段被正确透出。这一测试覆盖"sync 失败不改变成功语义"这条产品要求，但不是走真实 handler 代码路径的端到端测试（因为端到端会触发真实 `removeUser`）。
3. `TestSyncConfigsAfterUserDelete_DefaultNotNil`（新增）：确认包初始化后 `syncConfigsAfterUserDelete` 不是 nil。Go 不支持比较两个非 nil 函数值是否相等，所以不能用单测断言它"就是" `diskmgmt.SyncAllConfigs` 本身——这一点只能从源码声明 `var syncConfigsAfterUserDelete = diskmgmt.SyncAllConfigs`（users.go）人工核验，已在报告和代码注释中如实说明，不虚报为"已单测覆盖"。

**明确测不到、只能真机验证的部分**：`removeUser` 成功之后，`syncConfigsAfterUserDelete()`（生产即 `diskmgmt.SyncAllConfigs`）被调用一次、且确实重新生成了不含已删用户 share 定义的 smb.conf/exports——这条完整链路因为依赖真实 `sudo useradd/smbpasswd/remove-user.sh` 和真实 `/etc/samba/smb.conf` 读写，只能在 Debian 真机按任务描述的方式复测（删 aitest01 后检查 `has_issues` 字段变化）。本次未新增任何"为了让测试过而把 sync 调用挪到生产不会走的位置"的代码——`syncConfigsAfterUserDelete()` 调用点严格在 `handleUserAction` 的 DELETE 成功分支内，生产和测试走的是同一行代码，只是测试环境把变量指向的函数实现换成了替身。

未破坏既有测试：`TestRemoveUser_RejectsInvalidUsername` 原样保留且仍通过。

## 验证命令完整输出

```
$ cd web && go build ./... && go vet ./modules/users ./modules/diskmgmt && go test ./modules/users/... ./modules/diskmgmt -count=1
ok  	nas-panel/modules/users	0.239s
ok  	nas-panel/modules/diskmgmt	11.496s
```

build 无输出（成功），`go vet` 无输出（无警告），两个包测试全部 `ok`。另外执行了 `gofmt -l modules/users/`，无输出（无需格式化）。

补充（非任务要求的验证范围，仅供参考）：跑过一次全仓库 `go test ./...`，`nas-panel/modules/tasks` 报 `unable to open database file: out of memory (14)`（SQLite 并发测试在本次运行环境下的资源问题），与本次改动的文件（仅 `web/modules/users/users.go`、`web/modules/users/users_remove_test.go`）无关，且不在任务要求的验证命令范围内，未做任何处理。

## 偏差与申报

- 未修改 `web/modules/diskmgmt` 任何代码：`SyncAllConfigs` 已经是导出函数，不需要新增导出符号，硬约束 #2 提到的"若需在 diskmgmt 暴露更合适符号先申报"这一条本次未触发。
- 未修改 `remove-user.sh` 脚本或任何 `scripts/`、`frontend/` 文件，符合硬约束 #2。
- 未执行任何真实磁盘/sudo/systemctl/samba 命令；测试全程通过函数变量替身规避真实系统调用，符合硬约束 #3。
- create 路径现状确认良好（见上方对称性核查），按任务要求仅申报不改。
- 响应体新增 `config_sync_error` 字段属于新增可选字段，不改变现有 `message` 字段语义，不认为是破坏性变更；前端未同步改动（任务范围禁止改 frontend/），该字段目前只是可供前端未来消费的透出信息，暂不会在 UI 上展示。
