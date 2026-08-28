# Z1 NAS 第三方软件与隐私声明

> 本文档是 Z1 NAS 的合规声明，涵盖隐私说明与全部第三方开源组件信息。
> 面板内置「关于」页面展示同源内容。最后更新: 2026-08-28

---

## 一、隐私声明

### 数据归属
Z1 NAS 是**私有部署**的存储系统。你的所有数据（文件、用户账号、配置）**仅存储在你自己的机器上**，不会上传到任何外部服务器。

### 数据不出网
- 面板、文件服务（Samba/NFS/FTP/WebDAV/S3）均为本地服务，只监听局域网
- 系统不含任何遥测、用户行为统计或崩溃上报代码
- 升级检查（如启用）仅访问 gitee.com 获取版本号，不发送任何机器/用户信息

### 凭据存储
- 管理密码以环境变量（.env，权限 0600）形式存储于本机
- WebDAV 凭据使用 bcrypt 哈希（/etc/rclone-htpasswd）
- 各服务凭据存储于对应系统配置（passdb.tdb / shadow 等），不外传

### 可选外联功能（默认关闭，需用户主动配置）
| 功能 | 外联目标 | 说明 |
|------|---------|------|
| 远程同步 | 用户配置的 S3/SFTP/WebDAV/FTP 远端 | rclone 任务，仅同步用户指定目录 |
| 告警通知 | 钉钉/Telegram/Bark/SMTP | 仅在用户配置后推送告警消息 |
| 时间同步 | 系统默认 NTP 服务器 | Debian 系统行为 |

---

## 二、第三方开源软件清单

### 核心服务组件

| 组件 | 用途 | 许可证 | 上游 |
|------|------|--------|------|
| **Samba** | SMB/CIFS 文件共享 | GPL-3.0 | samba.org |
| **NFS (nfs-kernel-server)** | NFS 文件共享 | GPL-2.0 | linux-nfs.org |
| **vsftpd** | FTP 服务 | GPL-2.0 | security.appspot.com/vsftpd.html |
| **rclone** | WebDAV 服务端 + S3 网关 + 远程同步 | MIT | rclone.org |
| **FileBrowser** | Web 文件管理器 | Apache-2.0 | filebrowser.org |
| **Fail2ban** | SSH/FTP 防暴力破解 | GPL-2.0 | fail2ban.org |
| **UFW** | 防火墙前端 | GPL-3.0 | launchpad.net/ufw |

### 存储与系统工具

| 组件 | 用途 | 许可证 |
|------|------|--------|
| **LVM2** | 逻辑卷管理 | GPL-2.0 / LGPL-2.1 |
| **mdadm** | 软件 RAID 管理 | GPL-2.0 |
| **xfsprogs** | XFS 文件系统工具 | GPL-2.0 / LGPL-2.1 |
| **smartmontools** | SMART 磁盘健康监测 | GPL-2.0 |
| **apache2-utils** | htpasswd 凭据工具 | Apache-2.0 |
| **parted** | 分区工具 | GPL-3.0 |
| **unattended-upgrades** | 自动安全更新 | GPL-2.0 |

### 面板内嵌库

| 组件 | 用途 | 许可证 |
|------|------|--------|
| **Alpine.js** | 前端响应式框架（本地内嵌，无 CDN） | MIT |
| **Go 标准库及第三方库** | JWT (golang-jwt/v5)、SQLite (modernc.org/sqlite) 等 | BSD-3/MIT |

### Z1 NAS 自身
- 面板（nas-panel）：**AGPL-3.0**（见 LICENSE）
- 安装/部署脚本：AGPL-3.0

---

## 三、商标说明

Debian 是 Software in the Public Interest, Inc. 的注册商标。
文中提到的其他产品名称归各自所有者所有，仅用于表明兼容性/用途，不代表关联。

---

*Z1 NAS · https://www.z1.sale · 基于 Debian 13*