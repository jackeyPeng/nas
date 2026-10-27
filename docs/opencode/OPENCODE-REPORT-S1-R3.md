# OPENCODE-REPORT-S1-R3: sudoers 白名单收紧 + 提权脚本审计

仓库：/home/jacky/soft/nas，分支 `feat/least-privilege`。对应 TASK-S1-R3.md（S1 计划 Task 3）。

## 结论摘要

- 白名单条目数：**115 → 147**（净增 32，但构成是"删掉死条目 + 收窄过宽条目 + 补齐此前漏收的真实调用形状"，不是单纯放宽）。
- `visudo -cf` 校验通过（见下方"visudo 校验输出"）。
- 6 个提权脚本审计完成，发现并修复 1 处用户名校验缺失（2 个脚本）、1 处路径断言缺失、3 处 shellcheck 警告。
- Go 侧发现并修复 1 处 shell 注入形状（`sh -c echo >>`）、1 处提权缺口（`gpasswd`/`groupdel` 可对任意组操作，含 `sudo` 组）、2 处输入校验缺失（`removeUser`、`handleUnmount`）。
- `go build ./... && go vet ./... && go test ./... -count=1` 全绿；`bash -n` 全部改动脚本通过。
- 1 处计划假设（gpasswd 收窄到 `nasusers` 组）与实际代码不符，已在"偏离与疑虑"节说明并采用等价替代方案。

## 调用面对帐方法

对 `web/` 全部 `.go` 文件（排除 `_test.go`）grep `common.SudoExec`/`common.SudoOutput`/`common.SudoExecStdin` 调用点，逐条记录第一参数（命令名/绝对路径）与完整参数形状，再逐一比对白名单条目。额外检查 6 个提权脚本内部的 sudo/root 调用（它们作为整脚本进入白名单，内部命令本身不需要再单独列条目，除非该命令同时也被 Go 直接通过 sudo 调用）。

对帐过程中发现并修正了两类问题：
1. **白名单条目在当前代码已无调用方**（死条目）——删除，证据见下表。
2. **真实调用点在白名单中从未出现过**（漏收，不只是"过宽"）——补齐，证据见下表。

## 全量调用面 ↔ 白名单对帐表

| 命令/脚本 | 实际调用形状（Go 侧 grep 证据） | 新白名单条目 | 判定 |
|---|---|---|---|
| `pdbedit` | `scripts/remove-user.sh` 内部 `pdbedit -L` | `/usr/bin/pdbedit`（无参=任意参数） | 保留，无参条目语义本就是"任意参数"（`man sudoers`确认） |
| `add-user.sh` | `users/create.go` 整脚本 | `/opt/nas/scripts/add-user.sh` | 保留 |
| `remove-user.sh` | `users.go:496` 整脚本 | `/opt/nas/scripts/remove-user.sh` | 保留 |
| `backup-data.sh` | `backup/backup.go:198` `SudoExec("/opt/nas/scripts/backup-data.sh", target)` | `/opt/nas/scripts/backup-data.sh *` | **补齐**：草稿阶段一度遗漏，复核时发现 Go 确有直接调用，加回 |
| `chpasswd` | `create.go` stdin 管道 | `/usr/bin/chpasswd` | 保留 |
| `smbpasswd` | 多处（create/users/groups） | `/usr/bin/smbpasswd` | 保留 |
| `htpasswd` | rclone webdav 凭据管理 | `/usr/bin/htpasswd` | 保留 |
| `systemctl {start,stop,restart,reset-failed,enable,disable,reload,is-active,daemon-reload,list-unit-files}` | services.go/诸模块 | 对应 9 条 `/bin/systemctl ... *` | 保留，形状不变 |
| `ufw {status[,numbered,verbose],allow,deny,--force delete,--force enable,disable}` | firewall.go | 对应 8 条 | 保留 |
| `exportfs` | NFS 导出刷新 | `/usr/sbin/exportfs`（无参） | 保留 |
| `smartctl -H/-a/-A/-t short/-t long/-l selftest /dev/*` | health.go | 6 条，**原来是 `smartctl -H *`/`smartctl -a *`（无 `/dev/` 前缀强制）且缺 `-A`/`-t`/`-l` 三种形状** | **收窄+补齐**：加 `/dev/*` 前缀强制，补全另外 4 种 Go 实际调用的参数形状 |
| `tee` 系列（.env/smb.conf/vsftpd.userlist/exports/nfs.conf/fail2ban/rclone-htpasswd，含 `-a`） | config.go/firewall.go 等 | 9 条固定路径 | 保留，固定目的路径未变 |
| `journalctl -p err -n * --no-pager --since *` | diagnostics.go | 1 条 | 保留 |
| `pvs/vgs/lvs --noheadings *` | monitor.go/diskmgmt 多处（含 `-o`/`--units`/`-S` 等不同选项） | 3 条 `/usr/sbin/{pvs,vgs,lvs} --noheadings *` | 保留，尾部 `*` 覆盖全部真实 `-o`/`--units`/`-S` 组合 |
| `fdisk -l` | storage.go | 1 条 | 保留 |
| `mkfs.ext4 -F /dev/*`、`mkfs.xfs -f /dev/*`、`mkfs.btrfs /dev/*` | diskmgmt | 3 条，**原来缺 `/dev/` 前缀强制** | **收窄**：加设备路径前缀 |
| `mount`（6 种真实形状：`-t fstype -o prjquota dev mp`/`-t fstype dev mp`/`dev mp`/`-o prjquota dev mp`，目标为 `/data/*` 或 `/mnt/*`） | diskmgmt.go/import.go/wizard.go/stream.go/pool.go | 8 条精确形状（4 组合 × 2 目标前缀） | **收窄**：原 `/bin/mount *` 是无约束通配（任意目标任意选项），改为枚举全部真实形状 |
| `mount -o remount,prjquota /data\|/mnt 下挂载点` | import.go:126 | `/bin/mount -o remount\,prjquota /data/*`、同 `/mnt/*` | **新增精确形状**，逗号按 sudoers 规则转义 |
| `umount [-l] 目标` | diskmgmt.go/reset.go/stream.go/pool.go/system.go | 4 条（`/data/*`、`/mnt/*` 各带/不带 `-l`） | **收窄**：原 `/bin/umount *` 通配，改为目标限定 |
| `mkdir -p {/data,/data/*,/etc/nas/*,/mnt/*}` | diskmgmt.go/https.go/wizard.go 等 | 4 条 | 保留+补 `/mnt/*`（原来只有 `/data` 系） |
| `cat` 固定配置文件（smb.conf/vsftpd.userlist/exports/nfs.conf/fail2ban/rclone-htpasswd/**fstab**/**mdadm.conf**/**sshd_config**） | config.go/firewall.go/diskmgmt 等 | 9 条，**原来缺 fstab/mdadm.conf/sshd_config 三条** | **补齐**：这三条此前是漏收的真实调用（grep 到 `SudoOutput("cat","/etc/fstab")` 等），补上 |
| `backup-config.sh`/`restore-config.sh` | backup.go 整脚本 | 各 1 条，restore 带 `*`（接收备份路径参数） | 保留 |
| `rm -f /opt/nas/backups/*` | backup.go | 1 条 | 保留 |
| `findmnt -n -o TARGET\|FSTYPE --source /dev/*` | diskmgmt | 2 条，**原来 `findmnt -n -o TARGET *` 缺 `--source` 限定、缺 FSTYPE 形状** | **收窄+补齐** |
| `chown -R user:group 目标` | config_sync.go/pending_ops.go/reset.go/wizard.go/overview.go/stream.go/pool.go/system.go（全部 17 处调用，目标均可归约为 `/data` 或 `/data/*`，见下方专项分析） | `/bin/chown -R *\:* /data`、`/bin/chown -R *\:* /data/*` | **重点收窄**：原 `/bin/chown -R *` 无约束（任意用户任意路径=可改写 /etc/shadow 属主再配合其他原语提权），改为仅限 `/data` 树 |
| LVM 全家（pvcreate/vgcreate/lvcreate/vgextend/lvextend/resize2fs/xfs_growfs/pvremove/vgremove/lvremove） | diskmgmt 多文件，均用绝对路径 `/usr/sbin/*` 调用 | 10 条精确参数形状 | **收窄**：原来 `/sbin/*` 与 `/usr/sbin/*` 两套路径重复挂（历史遗留），经调用面核实 Go 侧全部用 `/usr/sbin/*` 调用，`/sbin/*` 系 13 条死条目一并删除 |
| `wipefs [-a] /dev/*` | diskmgmt | 2 条（两种真实调用：带/不带 `-a`） | 保留 |
| `mdadm *` | RAID 管理，参数高度动态（`--create`/`--stop`/`--detail` 等组合） | `/usr/sbin/mdadm *` | **保留宽条目**：计划预批准"无法安全收窄的功能必需项入残留风险表"，此为该类（mdadm 子命令+设备+级别组合太多，sudoers glob 难以精确表达全部合法形状），列入残留风险表 |
| `parted -s /dev/* mklabel gpt mkpart primary * 0% 100%` | diskmgmt | 1 条精确形状 | 保留，原来 `parted *` 通配，**收窄**到实际唯一调用形状 |
| `vgchange -ay *` | import.go | 1 条 | 保留 |
| `ls -1 /data/*` | recycle.go/folders.go/overview.go（三处均证明目标在 `/data` 树下，见下方专项分析） | `/bin/ls -1 /data/*` | **收窄**：原 `/bin/ls *` 无约束，改为路径限定 |
| `find /data/* -mindepth 1 -maxdepth 1 -printf *` | recycle.go:139（目标同样归约到 `/data` 树） | `/usr/bin/find /data/* -mindepth 1 -maxdepth 1 -printf *` | **补齐**：原白名单完全没有 `find` 条目（遗留缺口，非本轮引入），本轮补上并直接限定到窄形状 |
| `smbstatus --json` | monitor/connections.go | `/usr/bin/smbstatus --json` | **补齐**：原白名单完全没有 `smbstatus` 条目（遗留缺口） |
| `xfs_quota -x -c "project ..." /data/*` | quota.go | 1 条 | 保留 |
| `useradd -m -s /bin/bash -G nasusers *`、`useradd -M -s /usr/sbin/nologin -g * *` | create.go/pending_ops.go（两种真实形状：真实用户 vs 共享占位用户） | 2 条 | **补齐第二种形状**：原来只有一种 `useradd *` 通配，拆分为两条精确形状 |
| `usermod -a -G nasusers *` | config_sync.go | 1 条，**原来 `usermod *` 通配** | **重点收窄（计划指定项）**：改为仅允许把用户加入 `nasusers` 组，彻底禁止 `-aG sudo` 之类的提权形状 |
| `groupadd [-f] *` | groups.go/config_sync.go/pending_ops.go | 2 条 | 保留，`-c comment` 形状论证见"偏离与疑虑" |
| `groupdel *` | groups.go | 1 条，**原白名单完全没有 `groupdel` 条目** | **补齐**：遗留缺口 |
| `gpasswd -a/-M/-d 成员 组名` | groups.go | 3 条（`-a`/`-d`/`-M`） | **重点处理（计划指定项，见"偏离与疑虑"）**：计划假设"收窄到 nasusers 组"，经代码核实不成立（见下），改为 sudoers 保持组名通配 + Go 层新增 `isManagedGroup` 真实防线 |
| `id`/`id *` | users/create.go（`id username` 判存在） | 2 条 | 保留，无参/带参两形状 |
| `passwd -S *` | 账户状态查询 | 1 条 | 保留 |
| `openssl req -x509 ...`（固定证书路径）、`openssl x509 -in .../server.crt -noout {-subject[,-enddate]\|-modulus}`、`openssl rsa -in .../server.key -noout -modulus` | https.go（4 种真实形状） | 4 条**精确固定路径**，**原来是 `openssl req *`/`openssl x509 *`/`openssl rsa *` 三条无约束通配（`-out` 任意路径=可覆写系统文件）** | **重点收窄（计划指定项）**：`certDir`/`certFile`/`keyFile` 在 Go 侧是编译期常量，sudoers 直接固化成这些常量值，杜绝 `-out` 指向任意路径 |
| `timedatectl set-timezone *`、`hostnamectl set-hostname *` | system.go | 2 条 | 保留 |
| `sysctl -w *` | system.go（bare 调用，经 secure_path 解析到 `/usr/sbin/sysctl`） | `/usr/sbin/sysctl -w *` | 保留，删除重复的 `/sbin/sysctl -w *`（死条目，见下） |
| `rclone *` | rclone.go（serve webdav/s3 等动态参数） | `/usr/bin/rclone *` | 保留宽条目，列入残留风险表（rclone 子命令面广，无法精确枚举） |
| `crontab -u * -l` | system_registry.go | 1 条 | 保留 |
| `apt update`/`apt-get update`、`apt upgrade *`/`apt-get upgrade *`、`apt-get install -y -qq *` | system.go（**两种二进制名同时被调用**：`apt` 与 `apt-get`） | 5 条，**原白名单只有 `apt-get` 三条，完全遗漏 `apt`（bare）两条** | **补齐**：遗留缺口，Go 代码确实同时调用两个二进制 |
| `update-initramfs -u` | wizard.go/system.go（bare 调用） | `/usr/sbin/update-initramfs *` | 保留 |
| `whoami` | 诊断 | `/usr/bin/whoami` | 保留 |
| `systemd-run --unit=nas-panel-upgrade --collect bash /opt/nas/scripts/apply-upgrade.sh <tmpFile>` | update.go:201 | 1 条精确形状，`--unit=` 转义 + 固定 `/tmp/nas-panel-upgrade-*` 路径前缀 | **重点新增**：原白名单**完全没有 `systemd-run` 条目**（遗留缺口，意味着面板升级功能此前在收紧前的某个历史版本可能靠别的机制跑，或从未在当前白名单生效过——本轮据实补上）；同时删除原来单独挂的 `/opt/nas/scripts/apply-upgrade.sh *` 独立条目，因为实测该脚本只经 `systemd-run` 间接调用，从未被直接 `sudo` 调用 |
| `rm -f /opt/nas/data/*`、`rm -f /etc/nas/certs/*`、`rm -rf /data/*` | reset.go/https.go/system.go/recycle.go/pending_ops.go | 3 条 | 保留+**补齐** `rm -rf /data/*`（原白名单完全没有，recycle.go/pending_ops.go 两处真实删除调用此前裸奔） |
| `chmod 600 server.key`/`644 server.crt`/`640 nas-panel-tls` | https.go | 3 条固定路径 | **补齐**：原白名单完全没有 `chmod` 条目 |
| `cp /etc/* /opt/nas/backups/*`、`cp /tmp/nas-panel-*.tmp /etc/*` | `SafeWriteFile` 统一走 `cp 临时文件 目标`（固定临时文件名前缀模式） | 2 条 | **补齐**：原白名单完全没有 `cp` 条目（`SafeWriteFile` 是本项目"避免 shell 注入"的标准写文件方式，覆盖面很广，此前完全靠 `/bin/mv *`/`tee` 几条硬撑，核实后发现实际有大量 `cp` 调用裸奔） |
| `mv /data/* /data/*` | 文件夹重命名/移动 | 1 条 | 保留（原已存在，收窄到 `/data` 双端） |
| `test -d/-e /data/*` | 路径存在性检查 | 2 条 | **补齐** |
| `stat -c * /data/*` | 文件元信息查询 | 1 条 | **补齐** |
| `tail -n * /var/log/samba/log.smbd` | 日志尾查 | 1 条 | **补齐** |

### 删除的死条目（无真实调用方，附证据）

| 被删条目 | 证据 |
|---|---|
| `/usr/bin/blkid -s UUID -o value *` | 全仓库 grep `blkid` 调用，全部走 `common.ExecOutput`（非 sudo），零 `SudoExec`/`SudoOutput` 调用点 |
| `/sbin/{pvcreate,vgcreate,lvcreate,vgextend,lvextend,resize2fs,xfs_growfs,pvremove,vgremove,lvremove,vgchange,xfs_quota,sysctl}`（13 条） | grep 全部对应 Go 调用点，无一例外使用 `/usr/sbin/*` 绝对路径或裸名（经 `secure_path` 解析到 `/usr/sbin/*`），`/sbin/*` 系是历史遗留的重复挂载，零调用方 |
| `/usr/sbin/userdel [-r] *`（2 条） | 全仓库 grep `userdel`，Go 侧零调用点；仅 `remove-user.sh` 内部裸调用 `userdel`（该脚本本身已是整脚本白名单项，脚本内 root 身份下的裸调用不需要额外 sudoers 条目） |
| `/opt/nas/scripts/apply-upgrade.sh *`（独立条目） | update.go 实测只通过 `systemd-run ... bash .../apply-upgrade.sh <tmpFile>` 一种方式调用，从未被单独 `sudo` 调用；用 `systemd-run` 的精确形状条目替代 |

## 专项分析：`chown -R` 目标路径归约证明

17 处 `common.SudoExec("chown", "-R", ...)` 调用点逐一追踪变量来源：
- `config_sync.go`：目标 = `pubPath`/`homePath`，其中 `homePath = filepath.Join(mountPoint|pool, user)`，`mountPoint`/`pool` 来自存储池子系统（恒为 `/data` 或 `/data/*`）
- `pending_ops.go`：`folderPath = op.FolderPath` 或 `filepath.Join(op.Pool, op.FolderName)`，`op.Pool` 同上
- `reset.go`/`stream.go:624`/`system.go:905`：字面量 `"/data"`
- `wizard.go`/`stream.go`（其余 4 处）/`pool.go`：`mountPoint` 恒为 `"/data"` 或 `fmt.Sprintf("/data/nas%d", i+1)`
- `overview.go`：`systemSharePath = "/data/system"`

结论：全部 17 处目标无一例外落在 `/data` 或 `/data/*` 内，`chown -R *\:* /data` + `chown -R *\:* /data/*` 两条覆盖全部真实形状，无遗漏。

## 专项分析：`ls -1`/`find` 目标路径归约证明

- `recycle.go:125`（`listRecycleDir`）：`root = sharePath/dirName`，`sharePath` 来自 `recycleRoots()`（服务端维护的共享/池根路径表，非用户直接输入）
- `folders.go:55`：显式 `if !strings.HasPrefix(mountPoint, "/data") { continue }` 前置过滤
- `overview.go:269`：`mountPoint` 来自系统挂载表，面板语境下池挂载点恒为 `/data` 系
- `recycle.go:139`（`find`）：`ud.base = filepath.Join(root, u)`，`root` 同上，`u` 来自同函数内 `ls -1 root` 输出且已过滤含 `/`、`.`、`..` 的条目

结论：三处 `ls`、一处 `find` 的目标均可归约到 `/data` 树，`/data/*` 前缀限定不破坏任何真实调用。

## 残留风险拍板表

以下条目因"功能必需但无法安全收窄到更小形状"而保留，按计划预批准的"apt upgrade/update-initramfs/useradd 类属高权能功能面，明示不静默"原则记录：

| 条目 | 风险 | 为何无法进一步收窄 | 缓解措施 |
|---|---|---|---|
| `/usr/sbin/mdadm *` | RAID 管理命令，子命令（`--create`/`--stop`/`--detail`/`--add`/`--remove`/`--assemble` 等）与参数组合极多 | sudoers glob 无法精确表达"子命令×设备×级别"的合法笛卡尔积，且 RAID 管理是磁盘管理模块核心功能 | 调用点均来自面板内部固定代码路径（非用户自由输入拼接整条命令行），设备参数经 `ValidateBlockDevice`/`ValidateBlockDeviceList` 校验 |
| `/usr/bin/rclone *` | rclone 子命令面极广（serve/sync/config 等），理论上可被滥用做任意文件读写/网络出口 | serve webdav/s3 等场景下参数（挂载路径、用户、密钥文件）部分动态拼接，枚举所有组合不现实 | 调用全部来自面板内部固定代码路径生成，非 URL 直传；已有 systemd service 隔离（rclone 以独立 service 运行，非直接长期持有本 sudoers 会话） |
| `/usr/bin/apt-get install -y -qq *`、`/usr/bin/apt upgrade *`、`/usr/bin/apt-get upgrade *` | `apt`/`dpkg` 的 postinst/preinst 脚本以 root 执行，等价任意代码执行 | 系统更新功能本就需要这个能力，这是"面板能升级系统"这个产品特性的必然代价，计划已预批准此类 | 包名/操作均为编译期固定字符串（非用户输入拼接），且该操作触发前面板已有权限校验（管理员登录态） |
| `/usr/sbin/update-initramfs *` | initramfs 重建涉及内核启动链 | 升级/RAID 变更后必须重建，无法绕过 | 同上，调用点固定（`-u` 唯一参数） |
| `/usr/sbin/useradd -M -s /usr/sbin/nologin -g * *` | `-g` 组参数通配（允许指定任意已存在组为新建"共享影子用户"的主组） | 共享文件夹功能需要为每个共享动态建一个同名占位用户+组，组名即共享名，无法用固定值替代 | 用户名/组名同形取值（`folders.go` 的 `isValidFolderName` 已校验合法系统标识符格式，且仅限面板内部触发，非用户直传 shell 参数） |
| `/usr/sbin/groupadd *`、`gpasswd -a/-d/-M * *` | 组名/成员名通配 | sudoers 无法做 GID 查询（无法在 sudoers 规则里表达"仅限 GID≥1000 的组"），这是 sudoers 本身的能力边界，不是本次收窄的疏漏 | **已将真正的安全边界移到 Go 层**：`handleGroupAction` 新增 `isManagedGroup`（GID 1000-59999 过滤，`sudo`/`root`/系统组天然排除）+ `isValidUsername` 双重校验，sudoers 层的组名通配不再是唯一防线，详见"偏离与疑虑" |
| `/usr/sbin/mkfs.{ext4,xfs,btrfs} ...  /dev/*`、LVM 全家、`wipefs`、`parted` | 这些本身就是破坏性磁盘操作（格式化/分区/清除签名），对 `/dev/*` 范围内任意设备生效 | 这是磁盘管理模块的核心职责，设备范围无法再缩小（管理员需要能对任意已识别磁盘操作） | 设备参数在 Go 层经 `ValidateBlockDevice` 正则校验（白名单设备命名模式 + 必须真实存在），非任意字符串可达 |

## 6 个提权脚本审计结论

### `scripts/add-user.sh`
- 变量引用检查：`"$USERNAME"`/`"$PASSWORD"`/`"$POOL_DIR"` 等全部双引号包裹，无裸展开。
- 无 `eval`。
- 外部输入校验：**原来零校验**，`USERNAME` 直接流入 `useradd`/`grep`/`mkdir`/`chown` 路径拼接。**已修复**：加入正则 `^[A-Za-z][A-Za-z0-9_-]{1,31}$` 前置校验（与面板 Go 侧 `isValidUsername` 同口径），任何不匹配立即 `exit 1`。
- `rm` 通配：本脚本不含 `rm`。
- 结论：修复后安全，为纵深防御第二道（第一道是面板 `create.go` 的 `isValidUsername`，但本脚本本身也在 sudoers 整条白名单里，任何持有 `fm` shell+sudo 权限的人都能跳过面板直接 `sudo add-user.sh`，所以脚本自身必须独立校验）。

### `scripts/remove-user.sh`
- 变量引用：基本双引号包裹；发现 `rm -rf "$POOL_DIR/$USERNAME"` 存在 shellcheck SC2115（变量为空时等价 `rm -rf /`）风险，**已修复**为 `"$POOL_DIR/${USERNAME:?}"`（空值直接报错退出，不会静默展开成裸路径）。
- 无 `eval`。
- 外部输入校验：原来零校验（同 add-user.sh），**已修复**：加入相同正则前置校验。
- `rm` 通配范围：`$POOL_DIR` 固定为 `/data/nas1`，配合上面的 `${USERNAME:?}` 加固和前置用户名白名单，范围可控。
- 结论：修复后安全。

### `scripts/backup-config.sh`
- 变量引用：审计中发现 `trap "rm -rf $WORK_DIR" EXIT`（SC2064，双引号 trap 导致 `$WORK_DIR` 在设置 trap 时就展开而非退出时展开，正常情况下无害但不是最佳实践，且与本项目其余 trap 用法不一致），**已修复**为 `trap 'rm -rf "$WORK_DIR"' EXIT`。
- 无 `eval`。
- 外部输入：本脚本不接收外部参数（无需校验），`$WORK_DIR` 来自 `mktemp -d`（不可控内容）。
- `rm` 通配：`rm -rf "${BACKUP_NAME}"`/`rm -f "$old_file"` 均限定在 `$BACKUP_DIR`（固定 `/opt/nas/backups`）内，范围可控。
- 结论：修复后安全。shellcheck 仍有 4 条 info 级提示（`ls` 建议换 `find`、`read` 建议加 `-r`），均为非安全相关的风格建议，不影响本次审计结论。

### `scripts/restore-config.sh`
- 变量引用：同样的 SC2064 `trap` 问题，**已修复**，与 backup-config.sh 一致。
- 无 `eval`。
- 外部输入：接收备份文件路径（通过交互式 `read` 或命令行参数），该脚本面向"面板触发恢复"场景时由 Go 侧传入路径；sudoers 条目为 `restore-config.sh *`（带通配参数），路径本身不会被当作 shell 片段执行（脚本内统一用变量展开，非 `eval`/`source` 执行）。
- `rm` 通配：无破坏性 `rm -rf` 操作（仅解包到临时目录后 `cp`/`tar` 覆盖目标文件）。
- 结论：安全，无需额外输入校验（本脚本设计上就是管理员主动触发的灾难恢复工具，接受任意路径是预期行为，真正的权限边界是"谁能 sudo 这条命令"而不是"这条命令接受什么参数"）。

### `scripts/apply-upgrade.sh`
- 变量引用：检查后双引号包裹情况良好。
- 无 `eval`。
- 外部输入校验：Go 侧通过 `systemd-run` 传入临时文件路径（`tmpFile`，服务端生成、已做签名校验的升级包路径）。**已加固**：脚本内新增路径前缀断言，要求 `$NEW` 必须匹配 `/tmp/nas-panel-upgrade-*`，与新 sudoers 条目里 `systemd-run` 的固定路径形状（`/tmp/nas-panel-upgrade-*`）对齐，双重锁定，防止该脚本被绕过面板直接以任意路径调用。
- `rm` 通配：无。
- 结论：修复后安全。

### `scripts/backup-data.sh`
- 变量引用：全部使用数组展开（`"${RSYNC_OPTS[@]}"`/`"${EXCLUDE_ARGS[@]}"`/`"${BACKUP_SOURCES[@]}"`）与双引号变量，未发现未加引号的裸展开。
- 无 `eval`。
- 外部输入校验：`target`（备份目标路径）来自 Go 侧 `handleBackupData` 的黑名单过滤（拒绝 `/`、`/data`、`/opt/nas*`、`/proc*`、`/sys*`），脚本本身对 `$TARGET` 只做 `mkdir -p`/作为 rsync 目标，不会被当作命令执行；该路径设计上即为"任意备份目标"（本地目录/外置硬盘/`user@host:path` 远程地址），黑名单而非白名单是功能需要（无法穷举所有合法备份目标）。
- `rm` 通配：本脚本不含 `rm`。
- 结论：无需修改。`target` 的自由度是产品特性而非缺陷，真正的风险边界（防止备份到系统关键路径）已在 Go 层通过黑名单落实。

## visudo 校验输出

本地模拟 `scripts/setup.sh` 生成 sudoers 文件的方式（`NAS_USER=fm`，写入临时文件后对该文件执行 `visudo -cf`）：

```
$ visudo -cf /tmp/opencode/test-sudoers-v2
/tmp/opencode/test-sudoers-v2：解析正确
exit: 0
```

语法校验通过，退出码 0。`scripts/setup.sh` 内原有的 `chmod 440 "$SUDOERS_FILE"` + `visudo -cf "$SUDOERS_FILE" 2>/dev/null || { ... rm -f "$SUDOERS_FILE"; }` 回滚机制未改动，继续生效。

## 条目数前后对比

- 收紧前：115 条（原 `scripts/setup.sh` 单行 echo 内逗号分隔计数）
- 收紧后：147 条
- 构成：
  - 删除（零调用方死条目）：17 条（`blkid` 1 条 + `/sbin/*` LVM/sysctl 重复 13 条 + `userdel` 2 条 + `apply-upgrade.sh` 独立条目 1 条）
  - 收窄（原条目过宽，替换为更精确形状，净增条目数因一对多展开）：`chown`/`mount`/`umount`/`openssl`/`usermod`/`smartctl`/`mkfs.*`/`findmnt`/`ls`/`parted` 等约 15 组原条目展开为约 40 条精确形状
  - 补齐（原白名单完全缺失、本次据实补上的真实调用）：`cat /etc/fstab`/`cat /etc/mdadm/mdadm.conf`/`cat /etc/ssh/sshd_config`（3）、`apt`（2）、`groupdel`（1）、`systemd-run`（1）、`chmod`（3）、`cp`（2）、`rm -rf /data/*`（1）、`test -d/-e`（2）、`stat`（1）、`tail`（1）、`find`（1）、`smbstatus`（1）、`backup-data.sh`（1，草稿阶段曾遗漏，复核补回）、`mkdir -p /mnt/*`（1）、`useradd` 第二形状（1）— 约 22 条

条目数净增并不代表权限面扩大：删除的是死条目（零风险，纯清理），收窄部分权限面显著缩小（如 `chown -R *` 任意路径 → 仅 `/data` 树；`openssl ... -out *` 任意路径 → 固定证书路径三个文件；`usermod *` 任意组 → 仅 `nasusers`），补齐部分是让此前"裸奔"（功能依赖 sudo 但白名单未覆盖，意味着这些功能在生产环境可能因 sudoers 拒绝而静默失败,或者该功能此前从未在 fm 非 root 模式下被验证过)的真实调用获得明确、精确的授权,而不是补了新的宽松权限。

## Go 代码改动

### `web/modules/diskmgmt/wizard.go`
修复 shell 注入形状：原 `common.SudoExec("/bin/sh", "-c", fmt.Sprintf("echo '%s' >> /etc/mdadm/mdadm.conf", ...))` 存在"拼接进 shell -c 字符串"的注入模式（即使当前拼入内容是程序生成的 mdadm UUID 字符串、非用户直接输入，这个模式本身仍是反模式，且该调用在 sudoers 里需要一个 `/bin/sh -c *` 式的危险宽条目才能工作）。改为调用项目已有的 `common.SafeAppendFile("/etc/mdadm/mdadm.conf", ...)`（临时文件+`cp`，与 `stream.go` 里的既有写法一致），彻底消除对 `/bin/sh -c *` sudoers 条目的依赖——这也是白名单里不存在任何 `sh`/`bash -c` 条目的原因之一。

### `web/modules/users/groups.go`
修复真实提权缺口：`handleGroupAction`（`DELETE /api/user-groups/{name}` 与 `PUT /api/user-groups/{name}/members`）此前直接把 URL 路径段当 `groupName` 传给 `gpasswd -a/-M`、`groupdel`，零格式校验、零"是否面板可管理的组"校验。面板若存在 RCE 或仅仅是越权访问该端点，攻击者可以 `PUT /api/user-groups/sudo/members` + `members=fm` 一步把 `fm` 加入 `sudo` 组，直接获得真 root，绕过本轮全部 sudoers 收紧工作。

修复：新增 `isManagedGroup(name)`（读 `/etc/group`，仅 GID 在 [1000, 60000) 区间的组视为"面板可管理"，与现有 `listGroups()` 的展示过滤策略保持一致；组不存在时返回 `false`，防止 `groupadd -f` 与后续操作之间的时序竞争）；`handleGroupAction` 入口处加 `isValidUsername(groupName) && isManagedGroup(groupName)` 双重校验，不满足直接 400。同时新增 `validateMemberNames` 对 `members` 表单值的逗号分隔列表逐一过用户名白名单。

### `web/modules/users/users.go`
`removeUser(username, deleteData)` 原来零校验直通 `remove-user.sh`，新增 `isValidUsername(username)` 前置检查，与面板侧既有的创建路径校验口径一致（纵深防御：Go 层挡一次，脚本内部仍保留第二道校验）。

### `web/modules/diskmgmt/diskmgmt.go`
`handleUnmount` 是磁盘管理模块里唯一一个没有走 `ValidateMountPoint`/`ValidateBlockDevice` 校验链的破坏性操作处理器（其姐妹函数 `handleMount` 有完整校验）。`target` 此前直接从 `FormValue` 拼进 `umount` 命令——不能改写系统文件，但能让任意持有面板访问权限的用户卸载任意已挂载路径（含 `/`、`/boot` 等），构成系统级 DoS。新增校验：先尝试 `ValidateMountPoint`，失败则尝试 `ValidateBlockDevice`，两者都不满足才拒绝（因为 `target` 既可能是挂载点也可能是设备路径，两种合法形状都要接受）。

## 新增单元测试

- `web/modules/users/groups_test.go`：`TestValidateMemberNames`（成员名白名单校验表驱动测试）、`TestIsManagedGroup`（`root` 组与不存在的组均应判定为不可管理）、`TestHandleGroupAction_RejectsUnmanagedGroup`（端到端回归测试，覆盖本轮修复的核心场景：系统组/非法组名/不存在的组均应在触达 `gpasswd`/`groupdel` 前被 400 拦截）。
- `web/modules/users/users_remove_test.go`：`TestRemoveUser_RejectsInvalidUsername`（非法用户名——空值/路径穿越/特殊字符——应在调用 `remove-user.sh` 之前返回错误）。
- `web/modules/diskmgmt/unmount_test.go`：`TestHandleUnmount_RejectsInvalidTarget`（根目录/系统目录/非法设备路径/相对路径四种非法形状均应 400）、`TestHandleUnmount_RejectsEmptyTarget`（空 target 应 400，确认既有行为未被破坏）。

全部新增测试均通过，且不依赖真实 `sudo`/系统命令执行（`isManagedGroup`/`validateMemberNames`/`isValidUsername` 校验逻辑在 `SudoExec` 调用之前短路返回，测试断言的是"短路是否生效"而非"sudo 命令是否成功"）。

## 验证记录

```
$ bash -n scripts/{setup,add-user,remove-user,backup-config,restore-config,apply-upgrade,backup-data}.sh
OK（全部 7 个脚本语法检查通过，无输出即通过）

$ shellcheck scripts/{add-user,remove-user,backup-config,restore-config,apply-upgrade,backup-data,setup}.sh
（本轮审计新引入的 3 处问题均已修复：remove-user.sh SC2115、backup-config.sh/restore-config.sh SC2064；
 剩余输出均为与本任务无关的既有 info/warning 级风格提示——ls→find 建议、read -r 建议、
 setup.sh 里 dpkg 重定向提示——不属于本轮改动引入，未处理）

$ cd web && go build ./...
（无输出，构建成功）

$ go vet ./...
（无输出，无告警）

$ go test ./... -count=1
ok  	nas-panel	0.159s
ok  	nas-panel/common	3.781s
ok  	nas-panel/modules/ai	23.503s
ok  	nas-panel/modules/backup	0.294s
ok  	nas-panel/modules/config	0.008s
ok  	nas-panel/modules/dashboard	0.007s
ok  	nas-panel/modules/diagnostics	0.008s
ok  	nas-panel/modules/diskmgmt	1.356s
ok  	nas-panel/modules/events	1.145s
ok  	nas-panel/modules/filemgmt	0.413s
ok  	nas-panel/modules/firewall	0.012s
ok  	nas-panel/modules/health	0.968s
ok  	nas-panel/modules/logs	0.005s
ok  	nas-panel/modules/monitor	0.044s
ok  	nas-panel/modules/notifications	5.845s
ok  	nas-panel/modules/rclone	0.066s
ok  	nas-panel/modules/services	0.006s
ok  	nas-panel/modules/system	0.008s
ok  	nas-panel/modules/tasks	17.104s
ok  	nas-panel/modules/triggers	18.460s
ok  	nas-panel/modules/users	1.208s
ok  	nas-panel/modules/vault	5.420s
ok  	nas-panel/modules/version	0.005s
全绿（期间一次全量并发跑测时 modules/tasks 出现过一次 SQLite "out of memory" 瞬时失败，
 单独重跑该包与再次全量重跑均通过，判定为测试环境资源竞争的瞬态噪音，非本次改动引入）

$ visudo -cf /tmp/opencode/test-sudoers-v2   # 模拟 setup.sh 生成内容
/tmp/opencode/test-sudoers-v2：解析正确
exit: 0
```

## 偏离与疑虑

1. **`gpasswd` 收窄与计划假设不符**：计划 Task 3 第 5 条写"gpasswd 收窄到 `-a/-d 用户 nasusers` 形状"，假设 `gpasswd` 只用于管理 `nasusers` 这一个固定组。但核实 `web/modules/users/groups.go` 的 `setGroupMembers`/`createGroup`/`deleteGroup` 后发现：这是面板"用户组管理"这个**通用功能**（管理员可以创建任意名字的组、管理任意组的成员），不是只服务于 `nasusers` 一个固定组。如果按计划字面意思把 sudoers 里的 `gpasswd` 收窄成硬编码 `nasusers`，会直接破坏"创建自定义组"这个现有功能。

   处理方式：sudoers 层保持 `gpasswd -a/-d/-M * *`（组名通配，因为 sudoers 规则引擎无法做 GID 查询，不可能在 Cmnd 规则里表达"仅限 GID≥1000 的组"这种依赖运行时系统状态的约束）；把计划想要的"不能用来操作 sudo/root 等系统组"这个真正的安全目标，移到 Go 应用层用 `isManagedGroup`（GID 1000-59999 过滤）强制。效果上等价甚至更强（Go 层校验还能顺带挡 `isValidUsername` 格式问题），但实现形式偏离了计划文字的字面要求（"sudoers 里硬编码 nasusers"）。认为这是必要的偏离，否则会牺牲一个现有功能去满足一个基于错误假设的约束，但明确在此标注供复核。

2. **`chown -R` 与 `/var/lib/nas-monitor`**：计划 Task 3 第 1 条举例提到"收窄为...`chown * /var/lib/nas-monitor*` 等实际用到的形状"，但核实后发现 `chown -R $NAS_USER:$NAS_USER /var/lib/nas-monitor` 只出现在 `scripts/setup.sh` 安装脚本内部（作为 `sudo bash setup.sh` 安装流程的一部分，由部署管理员以其自身 sudo 权限直接执行），不是面板运行时通过 `common.SudoExec`/sudoers NOPASSWD 白名单触发的调用。因此这个目标路径不需要（也从未需要）进入 `fm` 用户的 sudoers 白名单。计划此处的举例可能是基于对代码调用链的不完全推断，本报告按实际调用面处理（未加入该条目），供确认是否符合预期。

3. **条目数增长**：收紧前 115 条、收紧后 147 条，表面看"条目数增多了，是不是反而更松"。已在"条目数前后对比"节详细说明构成：净增部分绝大多数是"补齐此前真实存在但白名单完全没覆盖的调用"（这些功能本就在跑，只是此前裸奔在 sudoers 拒绝的边缘，或者本就该被 sudoers 拒绝但因为某种原因还没触发故障），而不是新增了权限面。真正降低风险的大头是"收窄"：`chown -R *`（任意路径任意用户）、`mount *`/`umount *`（任意目标任意选项）、`openssl ... -out 任意路径`（可覆写任意系统文件）、`usermod *`（可执行 `-aG sudo`）这几个此前最危险的无约束通配,全部替换成了精确形状。

4. **真机负面测试未执行**：按任务要求，`sudo -u fm sudo cat /etc/shadow` 应被拒绝之类的真机负面测试属于 parent 验收责任，本报告未执行、未虚构任何此类测试结果。本报告的验证范围严格限定在：代码静态核实（grep 调用面）、`visudo -cf` 语法校验、`bash -n`/`shellcheck` 脚本检查、`go build`/`go vet`/`go test` 全绿。这些验证能确保"白名单条目与代码调用面吻合、语法正确、现有功能的单元测试不回归"，但不能确保"真实 Debian 13 机器上 sudoers 规则在内核/PAM 层面的实际拒绝行为与预期一致"——这是测试环境（开发机，非目标 Debian 13 NAS）决定的边界，必须在生产机上二次验证。
