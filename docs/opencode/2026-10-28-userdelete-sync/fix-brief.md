# 任务：删用户后立即重投影 SMB/NFS 托管段（消除配置漂移残留）

你在 ~/soft/nas 仓库工作。基线 = HEAD (f28b189)。这是 .48 真机全流程部署验收抓到的一个真实缺陷，已完整诊断，修法明确。先读涉及文件确认代码现实；与本文不符在报告申报，不得静默绕过或扩大范围。

## 硬约束（违反 = 返工）

1. 禁止读取/修改仓库外任何文件（含 ~/go/pkg/mod、/tmp）——auto-reject 会终止整个 run。验证只用 web/ 内 go build / go vet / go test。
2. 只修改 web/modules/users/ 下代码与测试（若需在 diskmgmt 暴露一个更合适的导出符号，先在报告申报理由，parent 裁决后再动，不得直接改 diskmgmt）。不碰 frontend/、scripts/。
3. 禁止执行真实磁盘/sudo/systemctl/samba 命令。
4. 报告写仓库根 TASK-REPORT.md（覆盖，不 git add）。
5. `git add` 只加你改动的源码/测试文件；单个 commit，message 原样用文末给出的。

## 缺陷（真机实锤）

面板删用户链路：`handleUserAction` 的 DELETE 分支（web/modules/users/users.go:358-369）→ `removeUser()`（users.go:489）→ `sudo /opt/nas/scripts/remove-user.sh`。该脚本删除了 folders.db 的 home 条目、Samba 用户(smbpasswd -x)、FTP 白名单、WebDAV htpasswd、系统用户，但**不重新生成 smb.conf/exports 的 Z1 托管段**——脚本自己的收尾提示也写明"SMB 共享会在面板下次「配置同步」后消失，或手动触发"（remove-user.sh:102）。

后果：删用户后 `/etc/samba/smb.conf` 托管段残留已删用户的 `[username]` share 定义（虽然 samba 账号已删、无人能认证，但定义悬空），且 diskmgmt 的配置一致性检查（config_sync.go 的 CheckConfigConsistency，最近新增了托管段真比对 managedSectionDrifted）会立即把它判为"Samba/NFS 托管段与 folders.db 不一致（配置漂移）"，仪表盘弹"检测到配置不一致"告警，直到用户手动点"修复配置"（POST /api/disk/config/sync）才消除。

真机验证：删 aitest01 后 smb.conf 残留 [aitest01] 段、config/check 报 has_issues=true；调 /api/disk/config/sync 后段消失、has_issues=false。即"删用户"与"配置重投影"之间缺一次自动同步。

## 修法

在 `handleUserAction` 的 DELETE 分支，`removeUser()` 成功返回之后、`common.JSONResponse(... "已删除")` 之前，调用 `diskmgmt.SyncAllConfigs()`（web/modules/diskmgmt/config_sync.go:439 已导出；diskmgmt 不 import users，加 `nas-panel/modules/diskmgmt` 到 users.go 的 import 不会成环——务必自己 `go build ./...` 验证无环）。

要求：
1. SyncAllConfigs 返回的 error **不能让删用户整体失败**：用户/凭据/系统账号此时已删除成功，配置重投影是收尾。sync 失败时记一条审计/日志（用本文件既有的 common.LogAuditRequest 或 log 模式，注明"用户已删除但配置同步失败，需手动配置同步"），仍向客户端返回删除成功（可在响应里附一个 `config_sync_error` 字段透出，不改变 message 语义）。
2. 只在 DELETE 成功路径追加这一次 sync；create/其他分支不动。
3. 对称性核查（报告里说明，不一定要改）：create 用户路径（add-user.sh / handleUserAction POST）当前是否也会立即重投影托管段？若 create 走的是不同的即时生效机制（如直接写 smb.conf 或 pending 队列），在报告里对比说明删/建两条路径的一致性现状；本轮只修 delete 路径，create 若也有同类缺口只申报不改。

## 测试

受限于本仓库无 exec 注入点（既有约定：系统命令包装层不 mock），handler 级"删用户触发 sync"难以在单测里直接断言真实 SyncAllConfigs 效果。按可行范围补测并如实说明覆盖边界：
- 若能在不引入真实系统调用的前提下验证 DELETE 分支在 removeUser 成功后确实调用了 SyncAllConfigs（例如把 sync 调用点抽成一个包级函数变量 `var syncConfigsAfterUserDelete = diskmgmt.SyncAllConfigs`，测试里替换为计数器替身、断言被调用一次；removeUser 因走 SudoExec 无法在单测执行，可只测"替换点存在且被 DELETE 成功路径引用"这一层的可达性，或退而在报告说明为何 handler 级不可测）——二选一，报告说明选择与理由，不得虚报覆盖了跑不到的路径。
- 不破坏既有 users 包测试；`go test ./modules/users/... -count=1` 全过。
- 明确禁止为"让测试通过"而放宽生产逻辑（如把 sync 调用挪到测试能碰到但生产碰不到的位置）。

## 验证（必须执行，输出贴报告）

```
cd web && go build ./... && go vet ./modules/users ./modules/diskmgmt && go test ./modules/users/... ./modules/diskmgmt -count=1
```

全部通过、无 import 环、无 vet 警告。

## 提交（原样 message）

```
fix(users): 删用户后立即重投影 SMB/NFS 托管段 — 消除已删用户 share 残留与配置漂移告警

原因: removeUser→remove-user.sh 删了 folders.db 条目与各类凭据,但不重新生成 smb.conf/exports 托管段,已删用户的 [username] share 定义悬空残留,并被配置一致性检查(托管段真比对)判为漂移,仪表盘告警直到手动配置同步
修复:
- handleUserAction DELETE 分支在 removeUser 成功后调 diskmgmt.SyncAllConfigs() 立即重投影托管段
- sync 失败不阻断删除(账号此时已删成功),记审计日志并在响应透出 config_sync_error,仍返回删除成功
影响范围: 面板/API 删用户流程;create 路径一致性现状仅核查申报不改
```

## 报告（TASK-REPORT.md，不 git add）

根因（文件:行）、修法、import 无环的 build 证据、create/delete 对称性核查结论、测试方式与覆盖边界（如实）、验证命令完整输出、偏差与申报。
