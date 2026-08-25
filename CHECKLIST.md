# NAS 系统配置清单

## 一、系统服务 (9项)

| # | 服务 | systemd unit | 端口 | 安装后预期 | 重置后预期 |
|---|------|-------------|------|-----------|-----------|
| 1 | Samba 文件共享 | smbd | 139,445 | active | active (默认配置) |
| 2 | NetBIOS 名称 | nmbd | 137,138 | active | active |
| 3 | NFS 服务 | nfs-kernel-server | 2049 | active | active (默认配置) |
| 4 | FTP 服务 | vsftpd | 21 | active | active (默认配置) |
| 5 | WebDAV | rclone-webdav | 8080 | active | active |
| 6 | 网页文件管理 | filebrowser | 8081 | active | active |
| 7 | S3 对象存储 | rclone-s3 | 9000 | active | active |
| 8 | 入侵防护 | fail2ban | - | active | active |
| 9 | 管理面板 | nas-panel | 8090 | active | active |

## 二、配置文件 (10项)

| # | 文件 | 用途 | 安装后预期 | 重置后预期 |
|---|------|------|-----------|-----------|
| 10 | /etc/samba/smb.conf | Samba 共享配置 | 含默认共享 + Z1 托管段 | 仅默认共享，无 Z1 托管段 |
| 11 | /etc/exports | NFS 导出配置 | 含默认导出 + Z1 托管段 | 仅默认导出，无 Z1 托管段 |
| 12 | /etc/nfs.conf | NFS 固定端口 | 含 lockd/mountd/statd 端口 | 含 lockd/mountd/statd 端口 |
| 13 | /etc/vsftpd.conf | FTP 配置 | 本地用户启用，被动端口 30000-31000 | 同左 |
| 14 | /etc/vsftpd.userlist | FTP 允许用户 | 含 NAS_USER | 含 NAS_USER |
| 15 | /etc/fail2ban/jail.local | Fail2ban 规则 | sshd + vsftpd jails | sshd + vsftpd jails |
| 16 | /etc/rclone-htpasswd | WebDAV 认证 | 含 NAS_USER 的 bcrypt hash | 含 NAS_USER 的 bcrypt hash |
| 17 | /etc/rclone/s3-env | S3 认证密钥 | 含 ACCESS_KEY + SECRET_KEY | 含 ACCESS_KEY + SECRET_KEY |
| 18 | /etc/sudoers.d/nas-panel | sudo 免密权限 | 含 nas-panel 命令白名单 | 含 nas-panel 命令白名单 |
| 19 | /etc/fstab | 文件系统挂载 | 无 /data 相关条目 (除非有数据盘) | 无 /data 相关条目 |

## 三、systemd 服务文件 (4项)

| # | 文件 | 安装后预期 | 重置后预期 |
|---|------|-----------|-----------|
| 20 | /etc/systemd/system/rclone-webdav.service | 存在 | 存在 |
| 21 | /etc/systemd/system/filebrowser.service | 存在 | 存在 |
| 22 | /etc/systemd/system/rclone-s3.service | 存在 | 存在 |
| 23 | /etc/systemd/system/nas-panel.service | 存在 | 存在 |

## 四、二进制文件 (2项)

| # | 文件 | 安装后预期 | 重置后预期 |
|---|------|-----------|-----------|
| 24 | /usr/local/bin/filebrowser | 存在且可执行 | 存在且可执行 |
| 25 | /usr/local/bin/nas-panel | 存在且可执行 | 存在且可执行 |

## 五、系统用户/密码 (4项)

| # | 项目 | 安装后预期 | 重置后预期 |
|---|------|-----------|-----------|
| 26 | 系统用户密码 | NAS_PASS 已设置 | NAS_PASS 已设置 (不变) |
| 27 | Samba 用户密码 | smbpasswd 已设置 | smbpasswd 已设置 |
| 28 | WebDAV 认证 | htpasswd 已设置 | htpasswd 已设置 |
| 29 | FileBrowser 用户 | 用户已创建，权限 admin | 用户已创建，权限 admin |

## 六、面板状态文件 (4项)

| # | 文件/目录 | 用途 | 安装后预期 | 重置后预期 |
|---|----------|------|-----------|-----------|
| 30 | /opt/nas/data/folders.db | 共享文件夹元数据 | 存在 (可能为空) | 不存在 (已删除) |
| 31 | /opt/nas/data/.last_reload | 配置重载时间戳 | 存在 | 不存在 (已删除) |
| 32 | /opt/nas/data/operations.db | 操作日志 (pending_ops) | 存在 | 不存在 (已删除) |
| 33 | /etc/filebrowser/filebrowser.db | FileBrowser 数据库 | 存在 | 存在 (保留) |

## 七、存储层 (6项)

| # | 项目 | 检查命令 | 安装后预期 | 重置后预期 |
|---|------|---------|-----------|-----------|
| 34 | LVM 卷组 | vgs --noheadings | 无 (或用户配置的) | 无 (全部删除) |
| 35 | LVM 逻辑卷 | lvs --noheadings | 无 | 无 |
| 36 | LVM 物理卷 | pvs --noheadings | 无 | 无 |
| 37 | RAID 阵列 | ls /dev/md* | 无 | 无 |
| 38 | 磁盘签名 | wipefs /dev/sdX | 无 LVM/RAID 签名 | 无任何签名 |
| 39 | /data 挂载 | df -h /data | 仅系统盘目录 | 仅系统盘目录 |

## 八、防火墙 (1项)

| # | 项目 | 检查命令 | 安装后预期 | 重置后预期 |
|---|------|---------|-----------|-----------|
| 40 | UFW 规则 | ufw status | 默认 deny + 9 服务端口开放 | 默认 deny + 9 服务端口开放 |

## 九、定时任务 (2项)

| # | 项目 | 安装后预期 | 重置后预期 |
|---|------|-----------|-----------|
| 41 | 监控 cron (每5分钟) | crontab 含 monitor.sh | crontab 含 monitor.sh |
| 42 | 备份 cron (每周日) | crontab 含 backup-config.sh | crontab 含 backup-config.sh |

## 十、目录结构 (3项)

| # | 路径 | 安装后预期 | 重置后预期 |
|---|------|-----------|-----------|
| 43 | /data 目录 | 存在，owner=NAS_USER | 存在，owner=NAS_USER |
| 44 | /opt/nas 软链接 | → ~/soft/nas | → ~/soft/nas |
| 45 | /var/lib/nas-monitor | 存在 | 存在 (保留) |

## 十一、软件包 (1项)

| # | 项目 | 安装后预期 | 重置后预期 |
|---|------|-----------|-----------|
| 46 | 核心软件包 | samba, nfs-*, vsftpd, rclone, fail2ban, ufw, smartmontools, xfsprogs, mdadm, lvm2, apache2-utils 已安装 | 同上 (不卸载) |

---

> 总计 **46 项**。安装后全部应为 ✅，重置后 #30-32, #34-38 应为"已清除"状态。