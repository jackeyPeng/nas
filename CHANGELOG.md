# NAS 项目变更日志

> 本文件记录项目的所有重要变更，按时间倒序排列。

---

## 2026-07-02 — 重装验证 & 部署脚本完善

### 背景

[REDACTED] 重装 Debian 13 (trixie) 后，从零开始验证 setup.sh 一键部署脚本的完整性和可复现性。

### 完成的工作

#### 1. NFS 防火墙端口问题修复
重装后发现 NFS 从外部无法访问（`showmount -e` 超时）。排查发现 NFS 除了主端口 2049，还需要以下辅助端口：

| 端口 | 服务 | 用途 |
|------|------|------|
| 111/tcp+udp | rpcbind | RPC 端口映射 |
| 20048/tcp+udp | rpc.mountd | NFS 挂载守护进程（需固定端口） |
| 32768/tcp+udp | nlockmgr | NFS 文件锁服务 |
| 32769/tcp+udp | rpc.statd | NFS 状态监控 |

**修复方案：**
- 在 `/etc/nfs.conf` 中固定 mountd=20048、lockd=32768、statd=32769
- 在 UFW 防火墙中开放对应端口
- 重启后验证 NFS 挂载正常

#### 2. 重装验证部署流程
在干净的 Debian 13 系统上，完整验证了部署流程：

```
步骤:
  1. git clone 仓库到 ~/soft/nas
  2. 安装 git（新系统默认没有）
  3. 创建 /opt/nas 软链接指向 ~/soft/nas
  4. 执行 sudo bash /opt/nas/scripts/setup.sh
  5. 手动复制配置文件到系统位置
  6. 重启各服务
```

**发现的问题：**
- 新系统没有 `curl`、`git` 等基础工具，setup.sh 依赖 curl 但没预装
- setup.sh 中 NFS 配置文件路径查找逻辑需要 `/opt/nas` 软链接
- FileBrowser 和 MinIO 的 systemd 服务文件没有包含在 setup.sh 中
- MinIO systemd service 文件中 heredoc 导致 ExecStart 行与 Environment 行合并

#### 3. setup.sh 脚本升级 (7步 → 9步)
重大升级，新增 FileBrowser 和 MinIO 自动安装：

```
原流程 (7步):
  [1/7] 安装基础软件包
  [2/7] 创建数据目录
  [3/7] 配置 Samba
  [4/7] 配置 NFS
  [5/7] 配置 FTP
  [6/7] 配置 WebDAV
  [7/7] 配置防火墙

新流程 (9步):
  [1/9] 安装基础软件包（新增 curl）
  [2/9] 创建数据目录（新增 /data/minio）
  [3/9] 配置 Samba
  [4/9] 配置 NFS（新增 nfs.conf 固定端口配置）
  [5/9] 配置 FTP
  [6/9] 配置 WebDAV
  [7/9] 安装 FileBrowser（新增）
  [8/9] 安装 MinIO（新增）
  [9/9] 配置防火墙和安全（新增 FileBrowser/MinIO/NFS 端口）
```

**新增 download_file 函数：**
- 通用多源下载函数，支持自动回退
- FileBrowser 下载源：GitHub → ghfast.top → mirror.ghproxy.com → gh-proxy.com
- MinIO 下载源：dl.min.io → ghfast.top → mirror.ghproxy.com
- 自动验证下载文件完整性（非空、非 HTML 错误页）

**MinIO systemd 服务文件修复：**
- heredoc 在 bash 中生成 systemd 文件时，长行会被合并
- 改用 `write_minio_service()` 函数，逐行 echo 写入，避免合并问题

#### 4. 服务器最终状态确认

所有 8 个服务全部正常运行：

| 服务 | 状态 | 端口 |
|------|------|------|
| smbd | active | 139, 445 |
| nmbd | active | - |
| nfs-kernel-server | active | 2049 |
| vsftpd | active | 21 |
| rclone-webdav | active | 8080 |
| filebrowser | active | 8081 |
| minio | active | 9000, 9002 |
| fail2ban | active | - |

### 文件变更

| 文件 | 变更 |
|------|------|
| scripts/setup.sh | 重大升级：7步→9步，新增 FileBrowser + MinIO 自动安装 |
| configs/nfs.conf | 新增 NFS 固定端口配置 |
| configs/minio.service | 新增 MinIO systemd 服务文件 |
| configs/filebrowser.service | 新增 FileBrowser systemd 服务文件 |
| configs/exports | 更新 NFS 导出配置 |
| configs/vsftpd.conf | 更新 FTP 配置 |
| configs/jail.local | 更新 Fail2ban 规则 |
| OPTIMIZATION_CHECKLIST.md | 新增待优化清单 |
| docs/nas-product-manual.md | 升级到 v1.2，新增 MinIO 章节 |
| docs/nas-product-manual.pdf | 同步更新 PDF 版本 |

---

## 2026-07-01 — MinIO 对象存储部署 & Git 仓库初始化

### 完成的工作

#### 1. MinIO S3 兼容对象存储部署
- 评估了 5 种对象存储方案（MinIO、Garage、SeaweedFS、Ceph RGW、rclone serve s3）
- 选择 MinIO 作为最佳方案
- 部署 MinIO vRELEASE.2025-09-07
- 配置 systemd 服务，端口 9000（API）+ 9002（Web Console）
- 默认凭证：admin / [REDACTED]

#### 2. Git 仓库初始化
- 本地仓库：~/soft/nas
- 远程仓库：https://gitee.com/gitdogcat/nas.git
- 认证方式：GITEE_TOKEN（OAuth2）
- 首次提交包含：configs/、scripts/、docs/ 全部文件

#### 3. 产品手册更新 (v1.1 → v1.2)
- 新增 7.5 节 MinIO 配置详解
- 原 7.5 FileBrowser 改为 7.6
- 更新端口表、服务清单、包列表
- 更新防火墙规则和部署步骤

### 文件变更

| 文件 | 变更 |
|------|------|
| configs/minio.service | 新增 MinIO systemd 服务文件 |
| TODO.md | 新增项目优化路线图 |
| docs/nas-product-manual.md | v1.1→v1.2，新增 MinIO 章节 |
| README.md | 首次创建 |
| .gitignore | 首次创建 |

---

## 2026-06-30 — 项目初始化 & 基础 NAS 部署

### 完成的工作

#### 1. 系统基础配置
- 操作系统：Debian 13 (trixie)，内核 6.12.94
- 硬件：4核 CPU / 7.6 GiB 内存 / 238.5G NVMe
- 网络：[REDACTED]/24

#### 2. 核心服务部署
- Samba (smbd/nmbd) — Windows/Mac 文件共享
- NFS (nfs-kernel-server) — Linux 高速访问
- FTP (vsftpd) — 传统文件传输
- WebDAV (rclone serve) — 浏览器文件访问
- FileBrowser v2.63.17 — Web 文件管理器
- Fail2ban — 登录暴力破解防护
- UFW — 防火墙
- unattended-upgrades — 自动安全更新
- smartmontools — 磁盘健康监控

#### 3. 管理脚本
- setup.sh — 一键部署脚本（初始版本 7 步）
- add-user.sh — 添加用户脚本
- remove-user.sh — 删除用户脚本

#### 4. 产品技术手册 (v1.0 → v1.1)
- 14 个章节 + 2 个附录
- 包含完整的服务配置详解、客户端访问指南、故障排查

### 文件变更

| 文件 | 变更 |
|------|------|
| configs/smb.conf | Samba 配置 |
| configs/exports | NFS 导出配置 |
| configs/vsftpd.conf | FTP 配置 |
| configs/jail.local | Fail2ban 规则 |
| configs/rclone-webdav.service | WebDAV systemd 服务 |
| configs/filebrowser.service | FileBrowser systemd 服务 |
| scripts/setup.sh | 一键部署脚本 |
| scripts/add-user.sh | 添加用户脚本 |
| scripts/remove-user.sh | 删除用户脚本 |
| docs/nas-product-manual.md | 产品技术手册 v1.0→v1.1 |
| docs/nas-product-manual.pdf | PDF 版本 |
