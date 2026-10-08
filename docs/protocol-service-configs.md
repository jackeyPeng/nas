# 协议服务配置详解 — SMB / NFS / FTP / WebDAV / S3 / FileBrowser

> 更新: 2026-10-08 · 依据: configs/ 模板 + scripts/setup.sh + web/modules/diskmgmt/config_sync.go + web/modules/users/(代码逐项核对,非文档转述)
> 权威源: 共享文件夹元数据在 folders.db(SQLite,/opt/nas/data/folders.db),面板把 DB 投影进各服务配置的「Z1 MANAGED SHARES START/END」托管段。
> 配套阅读: docs/storage-permission-model.md(权限模型与已知问题)、AGENTS 侧 skill nas-storage-architecture(改配置生成代码前必读)。

---

## 0. 总览

| 协议 | 服务 | 服务根 | 认证 | 按用户隔离 | 配置生成 | 重载方式 | 端口 |
|------|------|--------|------|-----------|---------|---------|------|
| SMB | smbd | [public] + [用户名](托管段) | Samba 账号(pdbedit/smbpasswd) | ✅ valid users + write list | GenerateSambaConfig | `systemctl reload smbd`(无断连),失败回退 restart | 139/445 |
| NFS | nfs-kernel-server | 托管段导出的文件夹(默认仅 public) | 无(SYS_UID 信任) | ❌ 网段级是上限 | GenerateNFSConfig | `exportfs -a`(生成时即调),服务不重启 | 2049 t/u, 111, 20048, 32768-32769 |
| FTP | vsftpd | /data/nas1(chroot) | 系统账号 + userlist 白名单 | ⚠️ 靠文件系统权限(home 0700) | 静态 configs/vsftpd.conf | `systemctl reload vsftpd`(active 时) | 21, 30000-31000 |
| WebDAV | rclone serve webdav | /data/nas1/public | htpasswd(多账号) | ❌ 所有账号同一棵 public 树 | 静态 systemd unit | restart(debounce 合并) | 8080 |
| S3 | rclone serve s3 | /data/nas1/public | access key/secret 单一全局凭据 | ❌ | systemd unit + /etc/rclone/s3-env | restart(改密联动) | 9000 |
| FileBrowser | filebrowser | /data/nas1/public | BoltDB 独立账号库(单管理员) | ❌(过渡期,将被面板文件管理取代) | `filebrowser config set` | 重启 | 8081 |
| 面板 | nas-panel | — | JWT(可选 TOTP) | — | — | — | 8090 |

---

## 1. SMB — smbd(Samba)

### 1.1 配置源

- `configs/smb.conf` 模板提供 [global] 段;setup.sh 安装时替换 `__NAS_USER__` 后写入 /etc/samba/smb.conf,并保留旧托管段。
- 托管段由 `GenerateSambaConfig()`(config_sync.go)按 folders.db 全量重生成,`replaceManagedBlock` 只替换 START/END 之间的内容,[global] 与手工段不动。

### 1.2 [global] 关键项

```ini
security = user
map to guest = Bad User          # 配合 public 的 guest ok,坏用户名落到 guest
min protocol = SMB2
vfs objects = catia fruit streams_xattr   # macOS 兼容全套
fruit:metadata = stream / fruit:model = MacSamba / fruit:posix_rename = yes ...
unix password sync = yes + pam password change   # 改密走 passwd program
```

### 1.3 托管段每共享的生成规则

| 文件夹类型 | force user / group | mask | 写模式 |
|-----------|-------------------|------|--------|
| public(未按用户编辑) | nasUser / nasusers | 0775 | `writable = yes` + `guest ok = yes`(开放共享,局域网设备免账号) |
| public(被按用户编辑过) | nasUser / nasusers | 0775 | `valid users` + `read only = yes` + `write list`(见 1.4) |
| home / 普通共享 | 文件夹名 / 文件夹名 | 0700 | `valid users`(空则兜底文件夹名)+ smbShareParams |

- 非 public 共享的 `force user = <文件夹名>` 要求**存在同名系统用户/组**(executeCreateFolder 里 ensureFolderUser 落地),否则整共享报 NT_STATUS_NO_SUCH_USER。文件夹名必须过 isValidFolderName(小写字母开头、2-32 位、[a-z0-9_-])。
- 开启回收站的共享追加:

```ini
vfs objects = recycle
recycle:repository = .recycle/%U     # per-share 回收站,删除=rename 零拷贝
recycle:keeptree = yes
recycle:versions = yes
recycle:touch = yes
recycle:directory_mode = 0700
hide files = /.recycle/
```

### 1.4 按用户读写语义(write list)

- 只需 `write_users` 一列(逗号分隔),Samba 语义 write list 覆盖 read only。
- 判定:在 write_users=读写;在 valid_users 不在 write_users=只读;不在 valid_users=禁止。不变式 write_users ⊆ valid_users。
- write_users 空时回退文件夹级 permission(旧数据兼容)。
- 首次按用户编辑触发两级物化(applyPermissionChange):valid_users 空→展开为全部现有用户;write_users 空且 permission=readwrite→write_users=valid_users。
- home 初始 valid_users=owner、write_users 空(owner 走 writable=yes)。

### 1.5 已知问题

- ⚠️ **回收站共享会覆盖 [global] 的 vfs objects**:Samba 的 `vfs objects` 是 share 级参数,共享段写 `vfs objects = recycle` 后,**不再继承** [global] 的 `catia fruit streams_xattr`。开了回收站的共享,macOS 客户端失去 fruit 兼容(资源分叉/扩展属性/posix rename 行为变化)。候选修法:`vfs objects = catia fruit streams_xattr recycle`(把 global 模块显式带上)。待验证 Samba 版本行为后处理。

---

## 2. NFS — nfs-kernel-server

### 2.1 配置源

- `configs/nfs.conf`:锁定辅助端口 — `[mountd] port=20048`、`[lockd] port=32768 + udp-port=32768`、`[statd] port=32769`,与 ufw 放行段一一对应。
- `/etc/exports` 托管段由 `GenerateNFSConfig()` 生成,每次写完即 `exportfs -a`,服务无需重启。

### 2.2 导出规则

- 客户端固定 `*`(所有网段开放)。detectLANSubnet 网段检测已删:跨网段/多网卡客户端走路由访问时源地址不在本机网段,按网段导出会被 mountd 拒绝。setup.sh 保留旧托管导出时也统一归一为 `*`,并过滤路径已不存在的行(防 exportfs 非零退出中断安装)。
- 导出选项由 `nfsExportOpts()` 纯函数生成(单测 nfs_squash_test.go):

| permission | 导出选项 |
|-----------|---------|
| readonly | `ro,sync,no_subtree_check,insecure` |
| 其他(rw) | `rw,sync,no_subtree_check,no_root_squash,insecure` |

- **产品决策(2026-09-24,家用可用性优先)**:默认 no_root_squash + insecure,反转 09-21 的 root_squash 安全基线。insecure 是 NAT 客户端的硬性要求(secure 要求源端口 <1024,NAT 重映射后被 mountd 以 illegal port 拒绝,客户端只见 access denied 而 showmount 正常,极易误判)。改这些默认值前必须过用户确认。
- `nfs_no_root_squash` DB 列保留(兼容)但不参与生成。
- 默认只导 public(EnsurePoolStructure 只给 public 置 NFSExport=true);其他文件夹靠创建/编辑时勾选 nfs。

### 2.3 排障速查

`journalctl -u nfs-mountd | grep refused` — illegal port → 加 insecure;unmatched host → 查客户端 IP/网段。

### 2.4 已知边界

- NFS 协议无按用户授权,网段级是上限。
- 非 public 共享目录 mode 0700 + force user:NFS 非 root 客户端对 rw 导出实际写不进去(写权限只在 SMB 层映射)。§八 P3。

---

## 3. FTP — vsftpd

### 3.1 配置源

- `configs/vsftpd.conf`(存在则 setup.sh 直接 cp);不存在时 setup.sh 内嵌 heredoc 兜底。
- ⚠️ **双模板漂移**:兜底 heredoc 多 `user_sub_token=*** 且 local_root 用 `$DATA_DIR/nas1` 变量,主模板硬编码 `/data/nas1`。当前 DATA_DIR=/data 行为等价,改配置时两处要同步。

### 3.2 关键项

```ini
anonymous_enable=NO
local_enable=YES  write_enable=YES  local_umask=022
chroot_local_user=YES          # 所有用户 chroot 到池根
allow_writeable_chroot=YES
local_root=/data/nas1
pasv_enable=YES  pasv_min_port=30000  pasv_max_port=31000
userlist_enable=YES  userlist_file=/etc/vsftpd.userlist  userlist_deny=NO   # 白名单制
```

### 3.3 权限模型

- 隔离完全靠文件系统权限:home 0700 owner、public 2775 setgid nasusers。
- userlist 白名单:setup 时只写 NAS_USER;建用户时由面板追加(tee 白名单内)。
- 无按文件夹权限矩阵(vsftpd 单 root 架构限制,换 proftpd 是独立项目,§七)。
- chroot 后跨 chroot 软链接断链。
- fail2ban [vsftpd] jail 盯 /var/log/vsftpd.log(见 §7)。

### 3.4 已知边界

- FTP 用户凭据可见**整个池的目录名**(chroot 在 /data/nas1,home 进不去但可枚举),这是 §八 P1「全局协议旁路」的主要暴露面。

---

## 4. WebDAV — rclone serve webdav

### 4.1 systemd unit(setup.sh 生成 /etc/systemd/system/rclone-webdav.service)

```ini
User=$NAS_USER
ExecStart=/usr/bin/rclone serve webdav /data/nas1/public --addr :8080 --htpasswd /etc/rclone-htpasswd
Restart=on-failure  RestartSec=10
```

### 4.2 认证与隔离

- htpasswd 多账号:每个面板建的用户都会 `htpasswd -b /etc/rclone-htpasswd <user> <pass>`(users/create.go addWebDAVUser;改密同步 users.go)。
- **但服务根只有 public**:多账号看到的是同一棵 public 树。htpasswd 多用户 ≠ 按用户隔离;按文件夹隔离需多实例(TODO #29,P2 暂缓)。
- 仅存在于 htpasswd 的 WebDAV-only 用户不在权限物化展开范围内(listAllUsers 只聚合 Samba+FTP)。§八 P3。

### 4.3 重载

- rclone 不支持 reload,只能 restart;经 `restartRcloneDebounced`(trailing debounce)合并,防短时间多次 SyncAllConfigs 触发 systemd start-limit(StartLimitBurst=5/10s)把服务打成 failed。

---

## 5. S3 — rclone serve s3

### 5.1 systemd unit + 环境文件

```ini
User=$NAS_USER
EnvironmentFile=/etc/rclone/s3-env        # RCLONE_S3_ACCESS_KEY / RCLONE_S3_SECRET_KEY,chmod 640
ExecStart=/usr/bin/rclone serve s3 /data/nas1/public --addr :9000 --auth-key $S3_ACCESS_KEY,$NAS_PASS
Restart=on-failure  RestartSec=10
```

- 需要 rclone ≥1.62(serve s3 子命令),setup.sh 会检测并升级。
- public 目录即 bucket;单一全局凭据,无按用户。
- 验证:`s3cmd --no-ssl --host=NAS_IP:9000 ls s3://public/`

### 5.2 access key 派生(common.S3AccessKey,有单测)

- 内嵌库 gofakes3 强制 accessKeyMinLen=3,短用户名(如 fm)的签名请求一律 InvalidAccessKeyId(403),匿名探测正常故长期未暴露。
- 规则:<3 字符加 `z1-` 前缀(fm → z1-fm)。实际值以 /etc/rclone/s3-env 为准,setup.sh 与面板两处生成逻辑同源。

### 5.3 改密联动(users.go changePassword)

- **仅 username == NAS_USER 时**:sed 更新 ExecStart 里 --auth-key 字面值 + s3-env 两变量 → daemon-reload → restart rclone-s3;同时更新 .env NAS_PASS、FileBrowser 密码、内存面板密码(UpdateNasPass)。
- 改普通用户密码绝不能碰这三处,否则管理员被顶下线。

---

## 6. FileBrowser(过渡期组件)

- BoltDB(不是 SQLite):/etc/filebrowser/filebrowser.db,独立账号库,单一管理员 NAS_USER。
- root=/data/nas1/public,监听 0.0.0.0:8081。
- 进程以 root 跑,不做文件系统权限校验;每用户 scope 是硬边界(当前只单账号)。
- 已知坑:`filebrowser config set` 前必须 `systemctl stop filebrowser`(否则锁库 timeout);setup.sh 已按此顺序执行。验证 root:停服务后 `filebrowser config cat --database ... | grep -i root`。
- 定位:将被面板文件管理取代。

---

## 7. 横切配置

### 7.1 fail2ban(configs/jail.local)

```ini
[DEFAULT] bantime=600  findtime=600  maxretry=10    # 家用放宽(2026-09-24):防用户输错密码把自己锁门外
[sshd]    enabled  logpath=/var/log/auth.log   maxretry=10
[vsftpd]  enabled  logpath=/var/log/vsftpd.log maxretry=10
```

- configs/jail.local 与 setup.sh 内嵌模板两处,改时同步。

### 7.2 ufw 端口(setup.sh [9/10])

22(ssh)、139/445(SMB)、2049 t/u + 111 t/u + 20048 t/u + 32768:32769 t/u(NFS)、21 + 30000:31000(FTP)、8080(WebDAV)、8081(FileBrowser)、9000(S3)、8090(面板)

### 7.3 sudoers 白名单(/etc/sudoers.d/nas-panel)

面板进程以 root 跑(nas-panel.service 无 User=),特权操作走 SudoExec/SudoOutput 且受白名单约束。要点:

- 用户/密码:pdbedit、smbpasswd、chpasswd、htpasswd、add-user.sh、remove-user.sh
- 服务:systemctl start/stop/restart/reset-failed/enable/disable(带 * 通配)
- 配置写入:tee /etc/samba/smb.conf(含 -a)、/etc/exports、/etc/nfs.conf、/etc/vsftpd.userlist、/etc/fail2ban/jail.local、/etc/rclone-htpasswd、/opt/nas/.env、/etc/fstab
- 存储:pvcreate/vgcreate/lvcreate/vgextend/lvextend/resize2fs/xfs_growfs/mdadm/parted/mkfs.ext4|xfs|btrfs/wipefs/mount/umount/blkid/findmnt/chown -R
- 网络:ufw allow/deny/status;诊断:smartctl、exportfs、journalctl -p err、cat 各配置
- 注意:chown -R 在白名单,chmod -R / groupadd / usermod 不在 → 面板用 os.Chmod(免 sudo)+ SudoExec chown -R 组合。

### 7.4 配置同步链路(SyncAllConfigs)

folders.db(唯一事实源)→ GenerateSambaConfig + GenerateNFSConfig(投影托管段)→ reloadServices:smbd reload(失败回退 restart)→ exportfs -a(NFS 生成时已调)→ vsftpd reload(active 时)→ rclone-webdav/s3 restart(debounced)→ markReloaded。

- GetAllFolderMeta 空表返回空 slice 而非 nil(nil 语义 = 读取失败)。
- SyncFolderMeta 收整个 FolderMeta 结构体(编译期强制携带全部字段,防 write_users 抹除类事故)。
- applyPendingOps 末尾不要再调 reloadServices(SyncAllConfigs 内部已含)。

---

## 8. 本次核对发现(2026-10-08)

| # | 级别 | 发现 | 状态 |
|---|------|------|------|
| 1 | 注释 | config_sync.go GenerateNFSConfig 注释仍写"默认 root_squash 安全基线",与 nfsExportOpts 实际行为(默认 no_root_squash)相反 | ✅ 已修(注释更新为 09-24 产品决策) |
| 2 | 功能 | 回收站共享的 `vfs objects = recycle` 覆盖 [global] 的 `catia fruit streams_xattr`,macOS 兼容丢失 | ⬜ 待修(候选:共享段写全 `catia fruit streams_xattr recycle`,先在 Debian 13 同构机验证) |
| 3 | 维护 | vsftpd 双模板漂移(configs/vsftpd.conf vs setup.sh heredoc:user_sub_token、$DATA_DIR) | ⬜ 待收敛(行为当前等价) |
| 4 | 文档 | §八 P1「全局协议旁路」实际暴露面:WebDAV/S3 根锁 public(进不了他人 home);真正能枚举整池目录树的是 FTP(chroot 在 /data/nas1);home 0700 兜底 | ⬜ storage-permission-model.md 表述可精确化 |
