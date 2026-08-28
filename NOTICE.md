# Z1 NAS 隐私声明与法律声明 / Privacy & Legal Statement

> Z1 NAS · 版本随面板发布 · 完整声明可在面板「关于与隐私」页面查看
> Z1 NAS · Versioned with panel releases · Full statement available in the panel's "About & Privacy" page

---

## 1. 隐私声明 / Privacy Statement

### 数据归属 / Data Ownership

Z1 NAS 是私有部署的存储系统。你的全部数据（文件、账号、配置）仅存储在你自己的机器上，不会上传到任何外部服务器。

Z1 NAS is a privately-deployed storage system. All of your data (files, accounts, configurations) is stored exclusively on your own machine and is never uploaded to any external server.

### 数据不出网 / No Data Leaves Your Machine

- 面板与全部文件服务（Samba/NFS/FTP/WebDAV/S3）均为本地服务，仅监听局域网
- 系统不含任何遥测、用户行为统计或崩溃上报代码
- 升级检查仅获取版本号，不发送任何机器信息或用户数据

- The panel and all file services (Samba/NFS/FTP/WebDAV/S3) run locally and listen on your LAN only
- The system contains no telemetry, user-behavior analytics, or crash-reporting code
- Update checks fetch version numbers only; no machine or user data is ever transmitted

### 凭据存储 / Credential Storage

- 管理密码以环境变量形式存储于本机（权限 0600）
- WebDAV 凭据使用 bcrypt 哈希存储
- 各服务凭据存储于对应系统配置文件，不外传

- The admin password is stored locally as an environment variable (permission 0600)
- WebDAV credentials are stored with bcrypt hashing
- Service credentials remain in their respective system configuration files and are never transmitted

### 可选外联功能（默认关闭）/ Optional Outbound Features (Disabled by Default)

| 功能 Feature | 外联目标 Destination | 说明 Notes |
|---|---|---|
| 远程同步 Remote Sync | 用户配置的远端 User-configured remote | 仅同步用户指定目录 Syncs user-designated directories only |
| 告警通知 Alerts | 钉钉/Telegram/Bark/SMTP | 仅推送告警消息 Alert messages only |
| 时间同步 Time Sync | 系统 NTP 服务器 System NTP | Debian 系统行为 Debian system behavior |

---

## 2. 免责声明 / Disclaimer

### 用户数据 / User Data

Z1 NAS 仅提供系统软件。用户数据的存储、备份与安全由用户自行负责。对于因硬件故障、误操作、断电、软件缺陷或其他任何原因导致的用户数据丢失或损坏，本软件不承担责任。建议用户对重要数据建立独立备份。

Z1 NAS provides system software only. The storage, backup, and security of user data is the user's own responsibility. We assume no liability for any loss or corruption of user data caused by hardware failure, misoperation, power loss, software defects, or any other reason. Independent backups of important data are strongly recommended.

### 开源组件 / Open-Source Components

本软件基于多个开源组件构建（见第三方清单），遵循各组件的开源许可协议。开源组件按"现状"提供，原作者不对本软件的使用行为承担责任。

This software is built upon multiple open-source components (see the third-party list) and complies with their respective open-source licenses. Open-source components are provided "as is"; their original authors bear no responsibility for the use of this software.

### 不上传、不修改、不传播 / No Upload, No Modification, No Distribution

我们不上传、不修改、不主动传播任何用户数据。数据仅在用户明确配置的功能（如远程同步、告警通知）范围内，按用户设定进行处理。

We do not upload, modify, or actively distribute any user data. Data is processed only within user-configured features (such as remote sync and alerts) and only as the user specifies.

### 责任限制 / Limitation of Liability

在适用法律允许的最大范围内，本软件按"现状"和"现有"提供，不提供任何明示或默示的保证，包括但不限于适销性、特定用途适用性的保证。对于任何直接、间接、附带或后果性损失，本软件不承担责任。

To the maximum extent permitted by applicable law, this software is provided "as is" without warranty of any kind, express or implied, including but not limited to warranties of merchantability or fitness for a particular purpose. We shall not be liable for any direct, indirect, incidental, or consequential damages.

---

## 3. 版权 / Copyright

Z1 NAS 系统软件（含面板 nas-panel、安装与部署脚本）版权归本项目所有，以 AGPL-3.0 许可证发布，详见随附 LICENSE 文件。

The Z1 NAS system software (including the nas-panel binary and installation scripts) is copyrighted by this project and licensed under AGPL-3.0. See the accompanying LICENSE file.

### 商标 / Trademarks

Debian 是 Software in the Public Interest, Inc. 的注册商标。文中其他产品名称归各自所有者所有，仅用于表明兼容性或用途，不代表关联或背书。

Debian is a registered trademark of Software in the Public Interest, Inc. Other product names mentioned are the property of their respective owners and are used solely to indicate compatibility or purpose, implying no affiliation or endorsement.

---

## 4. 第三方开源软件清单 / Third-Party Open-Source Software

### 核心服务 / Core Services

| 组件 Component | 用途 Purpose | 许可证 License |
|---|---|---|
| Samba | SMB/CIFS 文件共享 File sharing | GPL-3.0 |
| NFS (nfs-kernel-server) | NFS 文件共享 File sharing | GPL-2.0 |
| vsftpd | FTP 服务 FTP server | GPL-2.0 |
| rclone | WebDAV/S3 服务端与远程同步 WebDAV/S3 server & sync | MIT |
| FileBrowser | Web 文件管理器 Web file manager | Apache-2.0 |
| Fail2ban | 防暴力破解 Brute-force protection | GPL-2.0 |
| UFW | 防火墙前端 Firewall frontend | GPL-3.0 |

### 存储与系统工具 / Storage & System Tools

| 组件 Component | 用途 Purpose | 许可证 License |
|---|---|---|
| LVM2 | 逻辑卷管理 Logical volume management | GPL-2.0 / LGPL-2.1 |
| mdadm | 软件 RAID 管理 Software RAID | GPL-2.0 |
| xfsprogs | XFS 文件系统工具 XFS tools | GPL-2.0 / LGPL-2.1 |
| smartmontools | SMART 磁盘健康监测 Disk health monitoring | GPL-2.0 |
| apache2-utils | htpasswd 凭据工具 Credential utility | Apache-2.0 |
| parted | 分区工具 Partitioning | GPL-3.0 |
| unattended-upgrades | 自动安全更新 Automatic security updates | GPL-2.0 |

### 面板内嵌库 / Embedded Libraries

| 组件 Component | 用途 Purpose | 许可证 License |
|---|---|---|
| Alpine.js | 前端响应式框架（本地内嵌） Frontend framework (embedded) | MIT |
| golang-jwt, modernc SQLite 等 | 面板运行库 Panel runtime libraries | MIT |

---

*Z1 NAS · https://www.z1.sale · 基于 Debian 13 / Built on Debian 13*

*最后更新 / Last updated: 2026-08-28*