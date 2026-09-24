# 存储与权限模型 — 实现现状梳理

> 更新: 2026-09-15 · 对应代码: web/modules/diskmgmt + web/modules/users
> 本文描述**实际实现**，非设计愿景。与 docs/permission-model-discussion.md（SMB 按用户模型讨论稿）互补。

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
- 面板登录密码 = `.env NAS_PASS`，仅 `username == NAS_USER` 的改密才联动 NAS_PASS/FileBrowser/内存密码。

## 七、已知能力边界

- NFS 按用户：协议不支持，网段级是上限。
- FTP 文件夹矩阵：vsftpd 单 root 架构限制（换 proftpd 是独立项目）。
- WebDAV/S3 按文件夹隔离：需多实例（TODO #29）。
- 回收站集中式：跨设备移动退化为 copy+delete（TODO #25）。

## 八、已知问题（待修，按风险排序）

1. ~~**P1 — 文件夹权限对话框会清空 write_users**~~ ✅ 已修（2026-09-15 commit 6ddb75b）：`executeUpdateFolder` 先读 DB 现有元数据，`mergeFolderUpdate` 合并保留 write_users（裁剪保持 write⊆valid），op.ValidUsers 空时继承现有值。
2. **P1 — 全局协议旁路 SMB 权限**：任何拿到 WebDAV/S3/FTP 凭据的用户可见/可写整个 /data（含他人 home 目录、noaccess 文件夹）。UI tag 已诚实标注，但产品文档未强调"开全局协议 ≈ 放弃文件夹权限"。
3. ~~**P2 — NFS `no_root_squash`**~~ ✅ 已处置（2026-09-24 产品决策反转）：产品定位家用/小白用户，可用性优先——NFS 导出默认 `no_root_squash,insecure`（挂载即可写、NAT 后客户端可挂），`nfs_no_root_squash` 列保留但不再影响生成结果；rw/ro 仍由文件夹 permission 控制。原 2026-09-21 的 root_squash 安全基线方案被产品决策覆盖。测试：nfs_squash_test.go。
4. ~~**P2 — 矩阵回显解析 smb.conf 而非事实源**~~ ✅ 已修（2026-09-21）：buildPermissionMatrix 改为读 folders.db（GetAllFolderMeta + FolderMetaUserPermission），smb.conf 不再参与回显；总览/列表页同步改为 folders.db 权威源 + enrichSharedFolder 统一富化（未纳管目录仍走 smb.conf 兜底）。
5. **P3 — 非 public 共享目录 mode 0700 + force user**：NFS 非 root 客户端对 rw 导出实际写不进去（写权限只在 SMB 层映射）；FTP/WebDAV 以 nasUser 身份走 force 映射不受影响。
6. **P3 — listAllUsers 只含 Samba+FTP 用户**：仅 WebDAV(htpasswd) 用户不在物化展开范围内。
7. **P3 — SyncAllConfigs 无全局互斥**：并发编辑可能交错生成配置（单管理员家庭场景风险低）。
8. **P3 — 创建/更新即时生效、删除走 pending 队列**：行为不一致，删除提示"已加入待应用队列"但改权限立即 reload smbd。
