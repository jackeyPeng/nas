# NAS 系统配置清单

> 最后更新: 2026-08-26
> 总计 46 项，覆盖 11 大类
>
> **一键安装**: `NAS_PASS=*** wget -qO- https://gitee.com/gitdogcat/nas/raw/master/scripts/install.sh | sudo bash`

## 使用方式

- **API**: `GET http://<NAS_IP>:8090/api/system/check` — 读取缓存
- **刷新**: `GET http://<NAS_IP>:8090/api/system/check?action=refresh` — 全量重新检查
- **存储**: `/opt/nas/data/registry.db` (SQLite)，面板启动时自动 seed
- **认证**: 公开接口，无需登录

## 完整清单

### 一、系统服务 (9项)

| # | 服务 | 检查命令 | 通过条件 |
|---|------|---------|---------|
| 1 | smbd — Samba 文件共享 | systemctl is-active/is-enabled | active + enabled |
| 2 | nmbd — NetBIOS 名称解析 | systemctl is-active/is-enabled | active + enabled |
| 3 | nfs-kernel-server — NFS 服务 | systemctl is-active/is-enabled | active + enabled/alias |
| 4 | vsftpd — FTP 服务 | systemctl is-active/is-enabled | active + enabled |
| 5 | rclone-webdav — WebDAV | systemctl is-active/is-enabled | active + enabled |
| 6 | filebrowser — 网页文件管理 | systemctl is-active/is-enabled | active + enabled |
| 7 | rclone-s3 — S3 对象存储 | systemctl is-active/is-enabled | active + enabled |
| 8 | fail2ban — 入侵防护 | systemctl is-active/is-enabled | active + enabled |
| 9 | nas-panel — 管理面板 | systemctl is-active/is-enabled | active + enabled |

### 二、配置文件 (10项)

| # | 文件 | 重置后预期 |
|---|------|-----------|
| 10 | /etc/samba/smb.conf | 不含 Z1 托管段 |
| 11 | /etc/exports | 不含 Z1 托管段 |
| 12 | /etc/nfs.conf | 存在 |
| 13 | /etc/vsftpd.conf | 存在 |
| 14 | /etc/vsftpd.userlist | 存在 |
| 15 | /etc/fail2ban/jail.local | 存在 |
| 16 | /etc/rclone-htpasswd | 存在 |
| 17 | /etc/rclone/s3-env | 存在 |
| 18 | /etc/sudoers.d/nas-panel | 存在 |
| 19 | /etc/fstab | 无 /data 数据盘挂载条目 |

### 三、systemd 服务文件 (4项)

| # | 文件 |
|---|------|
| 20 | /etc/systemd/system/rclone-webdav.service |
| 21 | /etc/systemd/system/filebrowser.service |
| 22 | /etc/systemd/system/rclone-s3.service |
| 23 | /etc/systemd/system/nas-panel.service |

### 四、二进制文件 (2项)

| # | 文件 |
|---|------|
| 24 | /usr/local/bin/filebrowser |
| 25 | /usr/local/bin/nas-panel |

### 五、系统用户/密码 (4项)

| # | 项目 | 检查方式 |
|---|------|---------|
| 26 | 系统用户密码 | passwd -S |
| 27 | Samba 用户 | pdbedit -L |
| 28 | WebDAV 认证 | /etc/rclone-htpasswd |
| 29 | FileBrowser 用户 | 数据库存在 + 服务运行 |

### 六、面板状态文件 (4项)

| # | 文件 | 重置后预期 |
|---|------|-----------|
| 30 | /opt/nas/data/folders.db | 存在但为空 (0 条记录) |
| 31 | /opt/nas/data/.last_reload | 不存在 |
| 32 | /opt/nas/data/operations.db | 不存在 |
| 33 | /etc/filebrowser/filebrowser.db | 存在 |

### 七、存储层 (6项)

| # | 项目 | 检查命令 | 重置后预期 |
|---|------|---------|-----------|
| 34 | LVM 卷组 | vgs --noheadings | 空 |
| 35 | LVM 逻辑卷 | lvs --noheadings | 空 |
| 36 | LVM 物理卷 | pvs --noheadings | 空 |
| 37 | RAID 阵列 | ls /dev/md* | 空 |
| 38 | 磁盘签名 | wipefs | 无残留 |
| 39 | /data 挂载 | df -h | 无数据盘挂载 |

### 八、防火墙 (1项)

| # | 项目 | 检查命令 |
|---|------|---------|
| 40 | UFW 防火墙 | ufw status |

### 九、定时任务 (2项)

| # | 项目 | 检查命令 |
|---|------|---------|
| 41 | 监控 cron (每5分钟) | crontab -u NAS_USER -l |
| 42 | 备份 cron (每周日) | crontab -u NAS_USER -l |

### 十、目录结构 (3项)

| # | 路径 |
|---|------|
| 43 | /data 目录 |
| 44 | /opt/nas 软链接 |
| 45 | /var/lib/nas-monitor |

### 十一、软件包 (1项)

| # | 项目 |
|---|------|
| 46 | 10 个核心软件包 (samba, nfs-*, vsftpd, rclone, fail2ban, ufw, smartmontools, xfsprogs, mdadm, lvm2) |

## 恢复出厂设置流程

1. 点击「恢复出厂设置」→ API 立即返回 `{"status":"running"}`
2. 后台执行 10 步：备份→卸载→清fstab→删LVM→停RAID→wipefs→删db→恢复Samba→恢复NFS→重建/data
3. 完成后自动调用 `ResetRegistryAfterFactoryReset()` 更新注册表
4. 前端轮询 `/api/disk/overview` 等待 Pools=0，完成后自动刷新页面

## 安装后自检

setup.sh 末尾自动调用 `/api/system/check?action=refresh`，展示 46 项注册表结果。