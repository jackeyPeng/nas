# .48 用户级全流程部署验收发现（2026-10-28）

机器 10.216.10.48（副节点测试机，5 盘：sda 100G 系统 + sdb-sde 4×50G），master f28b189，本地编译二进制注入。

## 部署结果：成功

- 干净机实查通过（无 /data、/opt/nas、/root/soft/nas、盘无签名、/tmp 权限正常 root:root 1777）
- fm 身份 clone master + 预置新编译二进制 + install.sh（未提供 NAS_PASS → 出厂默认 + 首登强制改密路径）
- 8 服务全 active（smbd/nmbd/nfs-kernel-server/vsftpd/rclone-webdav/rclone-s3/fail2ban/nas-panel）
- 注册表 41/41 pass，0 fail 0 warn（建池后）
- /api/services 返回 7 项，全 active
- 依赖齐全：lvm2/mdadm/parted/smartmontools/xfsprogs 均 ii（parted 已在 2d58fbc 补进依赖清单）
- 首登改密四件套全过：出厂密码登入 must_change=true → 改密 200 → 旧密码 401 → 新密码登入 must_change=false
- 存储向导 merge 建池成功：vg_nas 4×50G→200G LVM+XFS 挂 /data/nas1
- acceptance-suite.sh：38 PASS / 0 FAIL
- AI 模块 E2E（ai-e2e.py）：ALL PASS（含配置 mask / 确认门 / 一次性 Take replay 410 / create_user 密码旁路 / 三处泄漏面 / 关断守卫）
- F2 主动管家 E2E（f2-e2e.py）：ALL PASS（含 kill -9 重启后通知/建议不丢不重、关断态全端点 403）
- Chrome 本机实测：仪表盘/存储页渲染正常

## F1（真实缺陷，本轮已修 190695e）：删用户不立即重投影 SMB/NFS 托管段

现象：AI E2E 清理调 remove-user.sh 删 aitest01 后，`/etc/samba/smb.conf` 托管段残留 `[aitest01]` share 定义，
今天新加的配置漂移检测（config_sync.go managedSectionDrifted）立即在仪表盘报"Samba/NFS 托管段与 folders.db
不一致（配置漂移）"，直到手动 `POST /api/disk/config/sync` 才清除。

根因：`handleUserAction` DELETE 分支（users.go:358-369）调 `removeUser()`→remove-user.sh，脚本删了 folders.db
home 条目 + samba/ftp/webdav 凭据 + 系统用户，但不重新生成 smb.conf/exports 托管段（脚本收尾提示自己写明
"SMB 共享会在面板下次配置同步后消失，或手动触发"，remove-user.sh:102）。create 路径经 EnsureUserHome→SyncAllConfigs
早已即时重投影，delete 是这条模式里唯一的缺口。

真机实证：删 aitest01 后 smb.conf 残留 [aitest01]、config/check has_issues=true；调 config/sync 后段消失、
has_issues=false。即真阳性，非误报。

修法（190695e）：DELETE 分支 removeUser 成功后调 diskmgmt.SyncAllConfigs() 立即重投影；sync 失败不阻断删除
（账号已删成功），记审计 + 响应透出 config_sync_error。import 无环（diskmgmt 不引 users，users/create.go 本就引 diskmgmt）。

## F2（非缺陷，记录防误判）：/api/version git_commit 显示 "Jacky.P"

因本地用 `go build -buildvcs=false` 且未注入 ldflags，version.go 的兜底默认值（BuildTime/GitCommit="Jacky.P"）生效。
release.sh/build.sh 发布构建会 -X 注入真实 commit/tag。部署验证改用 build.sh 注入版本戳后消除。

## O1（设计观察，未授权不改）：/api/system/check 公开无鉴权

system.go:29 有意注释"公开接口，无需认证"。41 项系统检查明细可 LAN 无 token 读取。既有设计决策，仅记录。

## 非问题：aitest01 残留目录显示为兜底共享

folders.db 已无 aitest01（remove-user.sh 删对），面板存储页仍显示是"未纳管目录兜底"扫描到 orphan 目录
/data/nas1/aitest01（uid 1005 已删用户遗留），因 ai-e2e 清理用 remove-user.sh 未带 --delete-data 保留了数据目录。
属既有设计（folders.db 权威 + 目录兜底），非回归。
