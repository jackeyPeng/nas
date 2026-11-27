# 存储与权限模型 — 实现现状梳理

> 更新: 2026-11-27 · 对应代码: web/modules/diskmgmt + web/modules/users + web/modules/filemgmt
> 本文描述**实际实现**，非设计愿景。与 docs/permission-model-discussion.md（SMB 按用户模型讨论稿）互补。
> 各协议服务的配置细节（smb.conf/exports/vsftpd/rclone unit/横切配置）见 docs/protocol-service-configs.md（2026-10-08 代码核对版）。

## 一、四层存储链路

```
物理磁盘 DiskSummary            /dev/sdb, /dev/nvme0n1
   │ 被 mdadm(RAID) 或 LVM(pvcreate) 吸收
   ▼
存储池 PoolSummary              /dev/md0 或 /dev/vg_nas（UI 不暴露设备名）
   │ type: single/lvm/raid0/1/5/6 · MemberDisks[] · Healthy(mdadm state)
   ▼
逻辑卷 VolumeSummary            /dev/vg_nas/data → 挂载 /data/nas1
   │ 一个 Pool 可有多个 Volume · 预留 Quota/压缩/快照能力位
   ▼
共享文件夹 SharedFolder         /data/nas1/movies
     权限与协议的唯一落点
```

**归属是反向推导的**：overview 扫 `/data*` 挂载点 → `detectPoolDevice()` 归池 → 文件夹按 mountpoint 前缀匹配回 Volume。没有显式的"文件夹属于哪个池"配置。

## 二、真相源与配置生成

```
SQLite folders 表（唯一事实源，config_sync.go）
  name / path / pool / permission / valid_users / write_users
  / samba_share / nfs_export / recycle_bin / quota_gb
        │ SyncAllConfigs()
        ├──► GenerateSambaConfig() → /etc/smb.conf 托管段 → smbd reload
        └──► GenerateNFSConfig()   → /etc/exports 托管段 → exportfs -a
```

- 两份系统配置只重写 `Z1 managed` 标记段，段外手写内容不动。
- WebDAV / S3 **不走这条链路**：`rclone serve webdav /data`（htpasswd）与 `serve s3 /data`（auth-key）是两个独立 systemd 服务，整盘挂 /data，与 folders 表无关。
- 文件夹创建时会 `ensureFolderUser()` 建同名系统用户/组，目录 chown + `force user` + `create mask 0700`（public 例外：nasusers 组 + 0775）。

## 三、协议 × 权限粒度

| 协议 | 粒度 | 机制 | 按用户？ |
|---|---|---|---|
| SMB | 用户 × 文件夹 | valid_users + write list | ✅ 真实生效 |
| NFS | 网段级 | exports `<subnet>(rw\|ro)` | ❌ 协议上限 |
| WebDAV | 全局 | rclone-htpasswd，root=/data | ❌ 登录=全见 |
| S3 | 全局 | auth-key 单一凭据 | ❌ 同上 |
| FTP | 全局 | vsftpd 单 root chroot | ❌ 已移出文件夹协议开关 |

UI tag 诚实标注：SMB 蓝（按用户）、NFS 灰（网段）、DAV/S3 黄（全局）。

## 四、SMB 权限判定

单字段最小模型：`read only = yes` 为默认底，`write list` 提权覆盖，不设 read/write 双列表。

```
在 write_users        → 读写
在 valid_users 不在 write_users → 只读
不在 valid_users      → 禁止（tree connect failed）
不变式: write_users ⊆ valid_users
```

生成侧 `smbManagedLines()`（config_sync.go）：
- `public` 且 valid/write_users 双空 → 开放共享，不写 `valid users`，所有认证用户读写；
- 其余（含 public 被按用户编辑后）→ `valid users` + `smbShareParams()` 按用户粒度。

回显侧（users/matrix.go）：`FolderMetaUserPermission()` 直接读 folders.db 元数据，同语义，不再解析 smb.conf（生成物）——SyncAllConfigs 失败/滞后时矩阵与 DB 不再漂移（2026-09-21 修复，§八 #4）。

## 五、旧数据兼容与两级物化

`write_users` 为空时回退文件夹级 `permission`（旧数据行为不变，直到首次按用户编辑）。

首次按用户编辑（matrix → setSharePermission）触发物化，防静默越权/锁人：
1. **valid_users 空 → 展开为全部现有用户**：防止给一人设权限把其他人踢出开放共享（如 public）。
2. **write_users 空 + permission=readwrite → write_users = valid_users**：防止"降为只读"在文件夹级数据上失效。

变更后 permission 反向同步（write_users 非空→readwrite；valid_users 空→noaccess；否则 readonly）；deny 同时清两列表。

## 六、用户与配额

- 用户 = Linux 系统用户，各协议独立启停：Samba(pdbedit/smbpasswd)、FTP(vsftpd.userlist)、WebDAV(htpasswd)。
- 配额挂 Volume 层（XFS project quota，fstab `prjquota`），按共享文件夹分配 project ID，删除自动清理。
- 面板登录密码 = `.env NAS_PASS`，仅 `username == NAS_USER` 的改密才联动 NAS_PASS/内存密码。

## 七、已知能力边界

- NFS 按用户：协议不支持，网段级是上限。
- FTP 文件夹矩阵：vsftpd 单 root 架构限制（换 proftpd 是独立项目）。
- WebDAV/S3 按文件夹隔离：需多实例（TODO #29）。
- 回收站条目粒度：回收站页按 `.recycle/<用户>/` **顶层条目**列举（keeptree 子树随条目整体恢复）；原位已有同名目录时走合并式恢复（29f775d，逐子项回原位、碰撞落 `.restored` 不覆盖）；共享未开回收站时面板删除回退池根 `.recycle`，经「(池根 xxx)」伪共享条目可见可恢复（D1 修复）。

## 八、已知问题（待修，按风险排序）

1. ~~**P1 — 文件夹权限对话框会清空 write_users**~~ ✅ 已修（2026-09-15 commit 6ddb75b）：`executeUpdateFolder` 先读 DB 现有元数据，`mergeFolderUpdate` 合并保留 write_users（裁剪保持 write⊆valid），op.ValidUsers 空时继承现有值。
2. ~~**P1 — 全局协议旁路 SMB 权限（文档强调缺失）**~~ ✅ 已处置（2026-11-17 文档轮）：产品手册 §8.5 新增「全局协议的能力边界」专节（WebDAV/S3 根锁 public；FTP 可枚举池目录树、他人 home 0700 兜底；NFS 默认仅导出 public），协议服务配置详解 §0.1 补「新装默认只开 SMB + 面板」口径与风险提示；beta.37 起四协议默认关闭、服务页卡片按需开启。协议自身的全局凭据设计保留为产品取舍（可用性优先，不按协议实现按用户隔离）——用户知情后显式开启。
3. ~~**P2 — NFS `no_root_squash`**~~ ✅ 已处置（2026-09-24 产品决策反转）：产品定位家用/小白用户，可用性优先——NFS 导出默认 `no_root_squash,insecure`（挂载即可写、NAT 后客户端可挂），`nfs_no_root_squash` 列保留但不再影响生成结果；rw/ro 仍由文件夹 permission 控制。原 2026-09-21 的 root_squash 安全基线方案被产品决策覆盖。测试：nfs_squash_test.go。
4. ~~**P2 — 矩阵回显解析 smb.conf 而非事实源**~~ ✅ 已修（2026-09-21）：buildPermissionMatrix 改为读 folders.db（GetAllFolderMeta + FolderMetaUserPermission），smb.conf 不再参与回显；总览/列表页同步改为 folders.db 权威源 + enrichSharedFolder 统一富化（未纳管目录仍走 smb.conf 兜底）。
5. **P3 — 非 public 共享目录 mode 0700 + force user**：NFS 非 root 客户端对 rw 导出实际写不进去（写权限只在 SMB 层映射）；FTP/WebDAV 以 nasUser 身份走 force 映射不受影响。
6. **P3 — listAllUsers 只含 Samba+FTP 用户**：仅 WebDAV(htpasswd) 用户不在物化展开范围内。
7. ~~**P3 — SyncAllConfigs 无全局互斥**~~ ✅ 已修（2026-10-29 commit 86aaf46）：包级 syncAllMu 把「folders.db → smb.conf/exports 投影 → 服务重载」整链纳入临界区，12 个调用点并发编辑不再交错生成配置（无重入：applyPendingOps 单向调用 SyncAllConfigs）。
8. **P3 — 创建/更新即时生效、删除走 pending 队列**：行为不一致，删除提示"已加入待应用队列"但改权限立即 reload smbd。
9. ~~**P2 — GetAllFolderMeta 静默半填充**~~ ✅ 已修（2026-10-14 commit d1f3a9d）：rows.Scan 错误此前被吞——手工种库/损坏库的 NULL 列会让行以零值半填充进入结果（Name/Path 正常而 RecycleBin 等后续字段静默丢失，删除误走池根回退、回收站页 shares 为空）。现在坏行跳过+日志、rows.Err 检查，fail-visible。
10. ~~**P1 — 重置向导旧入口全局枚举 VG/PV/md 且吞错**~~ ✅ 已修（Item 1 修复）：`handleWizardReset`（reset.go，无 confirm 的旧非流式入口）与 `handleWizardResetStream`（stream.go，前端实际调用的 SSE 入口）此前都存在"扫描全主机 VG/PV/md 设备并逐个销毁、单步失败继续下一步"的隐患——非本面板纳管的 VG/软 RAID 若恰好存在会被误杀，且错误不中断流程。现改为 `managed_wipe.go` 新增 `managedVGNames()`/`managedMDDevices()`，仅收敛到"本次操作范围内实际探测到、与目标磁盘关联"的对象；`stopRAIDArrays()` 改为接受显式设备列表而非 glob 全局 `/dev/md*`。测试：reset_test.go、reset_stream_test.go、managed_wipe_test.go。
11. ~~**P1 — 存储池创建旧入口绕过检查**~~ ✅ 已处置（Item 2 修复）：`handleWizardSetup`（wizard.go）为前端未调用的遗留 API 入口，缺少 setup-stream（stream.go，前端实际路径）具备的创建前校验链，保留会被绕过利用。现已直接下线返回 `410 Gone` 并给出明确文案（而非静默成功或 500），前端唯一实际调用路径 `/api/disk/wizard/setup-stream` 不受影响；关联死代码一并清理。测试：wizard_setup_test.go。
12. ~~**P1 — 目录增删改与权限/元数据队列吞错**~~ ✅ 已修（Item 3 修复）：`pending_ops.go` 的 `executeCreateFolder`/`executeUpdateFolder`/`executeDeleteFolder` 此前丢弃 `common.SudoExec`（mkdir/chown 等系统命令）与 `SyncFolderMeta`/`RemoveFolderMeta`（DB 落盘 + smb.conf/exports 同步）的全部错误，导致"系统命令或 DB 写入实际失败，但队列按成功结算、前端收到假成功"。现 `SyncFolderMeta`/`RemoveFolderMeta` 改为返回 `error`，三个 execute 函数均正确传播失败，前端能看到真实失败结果（队列仍按既有设计每轮无条件清空，非重试队列，这点行为不变，只修正了"结果汇报是否真实"）。测试：pending_ops_errors_test.go。
13. **（已确认现状正确，无需修复）P1 — 存储池删除失败恢复** Item 4 复核确认：`pool.go::handlePoolDelete` 已经在销毁动作前采集成员盘信息、对每一步销毁命令的错误都做检查并在失败处中止、维护"已完成步骤"轨迹，对系统盘有拦截闸门，并明确区分"完全成功"与"已完成但配置清理存在问题"（HTTP 207）。`pool_test.go` 中既有 8 个测试覆盖这些路径，本轮未发现需要修复的缺口，不做代码变更。
14. ~~**P2 — 覆盖上传不保留 ACL/xattr**~~ ✅ 已修（Item 5 修复）：覆盖已有文件时，原实现仅保留 owner/mode（临时文件+rename，失败不落地），但未保留 POSIX ACL 与扩展属性，覆盖后 ACL/xattr 会丢失为新文件的默认值。现 `upload.go` 新增 `captureACLXattr`/`restoreACLXattr`，走 `getfacl`/`setfacl`/`getfattr`/`setfattr`（含 sudo 兜底，镜像 perms.go 的 chmod/chown 模式）：写入前捕获原文件 ACL/xattr dump，临时文件权限对齐后、rename 前写回；捕获或写回任一步失败则中止覆盖、只清理临时文件、原文件保持不变（fail-closed，不留半成品）。测试：upload_acl_xattr_test.go。
15. ~~**P2 — 跨文件系统移动字节校验缺少并发修改场景的直接测试**~~ ✅ 已确认现状正确并补测（Item 6）：`mutate.go::moveCrossFS` 复核确认逻辑本身已经正确——`copyTree` 返回写入字节数，复制完成后用 `treeByteSize(srcAbs)` 重新统计源树并比对，不一致立即返回错误且绝不执行 `os.RemoveAll(src)`（fail-closed，源始终保留）。此前只有"源不存在导致 copyTree 必然失败"的间接测试覆盖该分支，缺少真正触发"复制期间源被并发修改"场景的测试。新增 `TestMoveCrossFSConcurrentModificationDuringCopyKeepsSource`：用真实后台 goroutine 在 copyTree 流式复制大文件期间并发追加已拷完的小文件，确定性触发字节不一致分支，断言返回错误且源文件完整保留。无生产代码改动。
16. ~~**P2 — 配额并发竞态（find-or-create project ID）**~~ ✅ 已修（Item 7 修复）：`quota.go::setFolderQuota` 的"查找或创建 project ID"流程此前对 `/etc/projid`/`/etc/projects` 两个文件的读-改-写是无锁的，并发调用（如两个文件夹几乎同时设置配额）存在 TOCTOU 竞态，可能分配冲突或覆盖彼此的写入。现增加包级 `quotaMu sync.Mutex` 包裹该临界区；`removeProjectEntry`/`updateProjectPath` 改为返回 `error` 并正确传播；文件路径与 `common.SafeWriteFile` 调用均改为可替换的 seam 变量，便于测试注入故障而不触碰真实 `/etc/projid`。测试：quota_test.go（含 20-goroutine 并发用例，`-race` 通过）。
17. ~~**P1 — Samba 配置生成前缺少用户名严格校验**~~ ✅ 已修（Item 8 修复）：`users/matrix.go::handleMatrix` 的 PUT/POST 处理器此前在写入 `valid_users`/`write_users` 前未对用户名做 `isValidUsername` 校验，恶意或畸形用户名（如含换行）理论上可注入到后续生成的 smb.conf 段。现已在进入 Samba 配置生成链路前统一校验，拒绝非法用户名并返回明确错误。测试：matrix_test.go::TestHandleMatrixRejectsInvalidUsername（5 个子用例）；config_sync_errors_test.go 确认全程走 seam 注入，不读真实配置文件。

## 九、协议隔离与恢复边界（本轮梳理，Item 9）

本轮（见本节上方 #10-#17）修复聚焦"失败路径是否诚实"：旧入口是否绕过检查、队列是否吞错、覆盖/移动是否在失败时保留原始数据。梳理如下：

- **协议隔离边界**：按用户隔离目前只有 SMB 真实生效（§三）。本轮未改变这一边界，但 #17 确保了"进入 SMB 配置生成链路的用户名"本身经过严格校验，避免配置注入扩大该边界之外的风险面。NFS/WebDAV/S3/FTP 仍是协议层面的全局/网段粒度上限，不是本面板的 bug，是协议能力边界（§七）。
- **恢复边界——系统命令/DB 失败后的状态**：
  - 目录增删改队列（#12）：失败会被诚实上报，但队列仍按既有设计无条件清空，不是"失败自动重试"队列——失败后需要用户/运维重新发起操作，不会静默悬挂。
  - 存储池删除（#13）：已确认对部分失败有"已完成步骤"轨迹和 HTTP 207 区分，不是简单的全有全无；但一旦销毁动作已执行到某一步，该步骤本身不可逆（如 mdadm --stop 后数组已停止），文档不承诺"回滚到销毁前状态"，只承诺"诚实汇报进展到哪一步、不假装完全成功"。
  - 覆盖上传（#14）、跨文件系统移动（#15）：均为 fail-closed 设计——任一步失败（ACL/xattr 读写、字节校验）都保留原始文件/源目录不动，不会出现"部分覆盖/部分移动"的中间态暴露给用户。
  - 配额设置（#16）：并发场景下现在靠锁避免分配冲突，但若 `/etc/projid`/`/etc/projects` 本身已被外部手工改坏（非本面板并发导致），仍需运维介入修复，不在本面板自动恢复范围内。
- **旧/遗留入口的处置原则**（#10、#11）：对于"前端已不使用、但 API 层仍可达"的旧入口，优先保留为"明确下线"（410 + 文案)或"收紧到安全范围"，而不是直接删除接口或保持原样不管——避免误删未知调用方，也避免旧入口成为绕过新校验的后门。
