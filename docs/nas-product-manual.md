# 家用 NAS 系统 — 产品技术手册

> 版本: v1.2  
> 最后更新: 2026-06-30  
> 适用系统: Debian GNU/Linux 13 (trixie), 内核 6.12.94+

---

## 目录

1. [系统概述](#1-系统概述)
2. [架构设计](#2-架构设计)
3. [硬件与系统要求](#3-硬件与系统要求)
4. [软件依赖包明细](#4-软件依赖包明细)
5. [目录结构规划](#5-目录结构规划)
6. [安装部署步骤](#6-安装部署步骤)
7. [服务配置详解](#7-服务配置详解)
   - 7.1 [Samba — Windows/Mac 文件共享](#71-samba--windowsmac-文件共享)
   - 7.2 [NFS — Linux 文件共享](#72-nfs--linux-文件共享)
   - 7.3 [vsftpd — FTP 服务](#73-vsftpd--ftp-服务)
   - 7.4 [rclone — WebDAV 服务](#74-rclone--webdav-服务)
   - 7.5 [FileBrowser — Web 文件管理器](#75-filebrowser--web-文件管理器)
8. [安全配置](#8-安全配置)
   - 8.1 [UFW 防火墙](#81-ufw-防火墙)
   - 8.2 [Fail2ban 入侵防护](#82-fail2ban-入侵防护)
   - 8.3 [自动安全更新](#83-自动安全更新)
9. [用户管理](#9-用户管理)
10. [运维管理](#10-运维管理)
11. [客户端访问指南](#11-客户端访问指南)
12. [故障排查](#12-故障排查)
13. [批量部署](#13-批量部署)
14. [配置文件清单](#14-配置文件清单)

---

## 1. 系统概述

本系统是一套轻量级家用/小型办公 NAS（网络附加存储）解决方案，基于 Debian 13 原生服务构建，不依赖 Docker 容器化，追求稳定、高性能、易维护。

**核心功能：**

| 协议    | 软件包              | 用途                          | 默认端口         |
|---------|---------------------|-------------------------------|------------------|
| SMB/CIFS | samba              | Windows/Mac/Linux 文件共享    | 139, 445         |
| NFS     | nfs-kernel-server  | Linux 设备高速文件访问        | 2049             |
| FTP     | vsftpd             | 传统文件传输                  | 21, 30000-31000  |
| WebDAV  | rclone serve       | 浏览器/移动端文件管理         | 8080             |
| Web UI  | FileBrowser        | Web 文件管理界面（浏览/上传/下载/编辑） | 8081    |
| S3 对象存储 | MinIO          | S3 兼容对象存储                   | 9000, 9002       |

**设计原则：**

- **稳定优先** — 全部使用系统原生服务，减少容器层故障点
- **精简功能** — 只做核心存储共享，不堆砌无用服务
- **安全加固** — 防火墙 + 入侵检测 + 自动安全补丁
- **可移植** — 所有配置集中在 `/opt/nas/`，支持一键部署到新机器

---

## 2. 架构设计

```
┌─────────────────────────────────────────────────────────┐
│                    Debian 13 (trixie)                    │
│                                                         │
│  ┌─────────┐  ┌─────┐  ┌────────┐  ┌───────────────┐  │
│  │  smbd   │  │ NFS │  │ vsftpd │  │ rclone WebDAV │  │
│  │  nmbd   │  │     │  │        │  │   (8080)      │  │
│  │(139/445)│  │(2049)│  │  (21)  │  │               │  │
│  └────┬────┘  └──┬──┘  └───┬────┘  └──────┬────────┘  │
│  ┌─────────────┐                                         │
│  │ FileBrowser │                                         │
│  │   (8081)    │                                         │
│  └──────┬──────┘                                         │
│       │          │         │               │           │
│       └──────────┴─────────┴───────────────┘           │
│                          │                              │
│                    ┌─────┴─────┐                        │
│                    │   /data/  │                        │
│                    │ (数据目录) │                        │
│                    └───────────┘                        │
│                                                         │
│  ┌──────────┐  ┌──────────┐  ┌───────────────────┐     │
│  │   ufw    │  │ fail2ban │  │ unattended-upgrades│     │
│  │ (防火墙)  │  │(入侵防护) │  │  (自动安全更新)   │     │
│  └──────────┘  └──────────┘  └───────────────────┘     │
│                                                         │
│  ┌─────────────────────────────────────────────┐        │
│  │  /opt/nas/ (部署配置 + 管理脚本, git 管理)   │        │
│  │  ├── configs/   (服务配置文件)               │        │
│  │  └── scripts/   (部署与管理脚本)             │        │
│  └─────────────────────────────────────────────┘        │
└─────────────────────────────────────────────────────────┘
```

**为什么不用 Docker？**

Samba、NFS 都是内核级服务（NFS 直接在内核空间运行），用 Docker 跑会带来：
- 额外的网络桥接层，增加延迟
- 内核模块加载问题（NFS 需要内核支持）
- 权限映射复杂（容器内外 UID/GID 对不上）
- 故障排查多一层，不够"透明"

原生部署 = 最少抽象层 = 最稳定。

---

## 3. 硬件与系统要求

### 最低配置

| 项目     | 要求                    |
|----------|-------------------------|
| CPU      | 2 核 x86_64            |
| 内存     | 2 GiB                   |
| 系统盘   | 32 GB SSD/NVMe         |
| 数据盘   | 按需（建议独立挂载）    |
| 网络     | 千兆以太网              |
| 系统     | Debian 13 (trixie)     |

### 推荐配置

| 项目     | 要求                    |
|----------|-------------------------|
| CPU      | 4 核 x86_64            |
| 内存     | 8 GiB                   |
| 系统盘   | 256 GB NVMe            |
| 数据盘   | 独立 HDD/SSD，挂载到 /data |
| 网络     | 2.5GbE 或更高          |

### 系统要求

- 全新安装的 Debian 13 (trixie) minimal
- 已配置好网络（静态 IP 推荐）
- 有一个具有 sudo 权限的管理用户
- apt 源可用（推荐配置国内镜像如 USTC）

---

## 4. 软件依赖包明细

### 核心服务包

| 包名                | 版本                    | 用途                   | 大小（含依赖） |
|---------------------|-------------------------|------------------------|----------------|
| samba               | 2:4.22.8+dfsg-0+deb13u2 | SMB/CIFS 文件共享服务  | ~25 MB         |
| smbclient           | 2:4.22.8+dfsg-0+deb13u2 | Samba 客户端工具       | ~5 MB          |
| nfs-kernel-server   | 1:2.8.3-1               | NFS 内核服务端         | ~3 MB          |
| nfs-common          | 1:2.8.3-1               | NFS 通用工具           | ~3 MB          |
| vsftpd              | 3.0.5-0.2               | FTP 服务器             | ~1.5 MB        |
| rclone              | 1.60.1+dfsg-4           | WebDAV 服务 (serve 模式)| ~15 MB         |
| minio               | RELEASE.2025-09-07 | MinIO 对象存储 (S3 兼容) | ~106 MB (单文件) |
| filebrowser         | v2.63.17                | Web 文件管理器          | ~15 MB (单文件)|
| rpcbind             | 1.2.7-1                 | RPC 端口映射 (NFS 依赖)| ~0.5 MB        |

### 安全与运维包

| 包名                | 版本         | 用途                       | 大小    |
|---------------------|-------------|---------------------------|---------|
| fail2ban            | 1.1.0-8     | 登录暴力破解防护            | ~5 MB   |
| ufw                 | 0.36.2-9    | 简易防火墙管理              | ~2 MB   |
| iptables            | 1.8.11-2    | 底层包过滤 (ufw 后端)       | ~4 MB   |
| smartmontools       | 7.4-3       | 磁盘 SMART 健康监控         | ~6 MB   |
| unattended-upgrades | 2.12        | 自动安全补丁更新            | ~1 MB   |

### 安装命令

```bash
sudo apt-get update
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y \
    samba nfs-kernel-server vsftpd rclone \
    fail2ban ufw smartmontools unattended-upgrades \
    smbclient nfs-common
```

**总计安装量：** 约 54 MB 下载，251 MB 磁盘占用（含 99 个包及全部依赖）。

---

## 5. 目录结构规划

### 数据目录 `/data/`

```
/data/                          ← 数据根目录（建议挂载独立磁盘）
├── shared/                     ← 公共共享（所有授权用户可读写）
├── media/                      ← 影音文件
│   ├── movies/                 ← 电影
│   ├── tv/                     ← 电视剧
│   └── music/                  ← 音乐
├── documents/                  ← 文档资料
├── photos/                     ← 照片
├── backups/                    ← 备份（手机/电脑）
├── downloads/                  ← 下载目录
└── private/                    ← 用户私有目录
    ├── <NAS_USER>/                  ← <NAS_USER> 的私有空间
    └── {username}/             ← 其他用户的私有空间
```

**权限规则：**

| 目录                | 权限  | 说明                          |
|---------------------|-------|-------------------------------|
| /data/              | 755   | 根目录，所有人可读            |
| /data/shared/       | 775   | 公共共享，组可写              |
| /data/media/        | 775   | 影音目录，组可写              |
| /data/documents/    | 775   | 文档目录，组可写              |
| /data/photos/       | 775   | 照片目录，组可写              |
| /data/backups/      | 755   | 备份目录                      |
| /data/downloads/    | 755   | 下载目录                      |
| /data/private/{user}/| 700  | 个人私有，仅用户本人可访问    |

### 部署配置目录 `/opt/nas/`

```
/opt/nas/                       ← 部署根目录（git 管理）
├── configs/                    ← 服务配置文件
│   ├── smb.conf                ← Samba 配置
│   ├── exports                 ← NFS 导出配置
│   ├── vsftpd.conf             ← FTP 配置
│   ├── jail.local              ← Fail2ban 配置
│   ├── rclone-webdav.service   ← WebDAV systemd 单元文件
│   ├── filebrowser.service     ← FileBrowser systemd 单元文件
│   └── minio.service           ← MinIO systemd 单元文件
└── scripts/                    ← 管理脚本
    ├── setup.sh                ← 一键部署脚本
    ├── add-user.sh             ← 添加用户
    └── remove-user.sh          ← 删除用户
```

### 配置文件部署位置映射

| 源文件 (git 仓库)                    | 目标位置 (系统)                          |
|--------------------------------------|------------------------------------------|
| /opt/nas/configs/smb.conf            | /etc/samba/smb.conf                      |
| /opt/nas/configs/exports             | /etc/exports                             |
| /opt/nas/configs/vsftpd.conf         | /etc/vsftpd.conf                         |
| /opt/nas/configs/jail.local          | /etc/fail2ban/jail.local                 |
| /opt/nas/configs/rclone-webdav.service | /etc/systemd/system/rclone-webdav.service |
| /opt/nas/configs/filebrowser.service  | /etc/systemd/system/filebrowser.service  |
| /opt/nas/configs/minio.service        | /etc/systemd/system/minio.service        |
| /opt/nas/scripts/*.sh                | 保留原位，直接执行                        |

---

## 6. 安装部署步骤

### 步骤 1：系统准备

```bash
# 更新系统
sudo apt-get update && sudo apt-get upgrade -y

# 设置主机名（可选）
sudo hostnamectl set-hostname nas

# 配置静态 IP（推荐，编辑 netplan 或 NetworkManager）
# 此处省略，根据实际网络环境配置

# 安装基础工具
sudo apt-get install -y curl ca-certificates gnupg
```

### 步骤 2：安装软件包

```bash
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y \
    samba nfs-kernel-server vsftpd rclone \
    fail2ban ufw smartmontools unattended-upgrades \
    smbclient nfs-common
```

### 步骤 3：创建数据目录结构

```bash
# 创建目录
sudo mkdir -p /data/{shared,media/{movies,tv,music},documents,photos,backups,downloads,private/<NAS_USER>}

# 设置所有权（假设管理用户为 <NAS_USER>）
sudo chown -R <NAS_USER>:<NAS_USER> /data

# 设置权限
sudo chmod 755 /data
sudo chmod 775 /data/{shared,media,documents,photos}
```

### 步骤 4：配置 Samba

```bash
# 备份原配置
sudo cp /etc/samba/smb.conf /etc/samba/smb.conf.bak

# 写入新配置（详见 7.1 节）
sudo cp /opt/nas/configs/smb.conf /etc/samba/smb.conf

# 设置 Samba 密码
(echo "<NAS_PASS>"; echo "<NAS_PASS>") | sudo smbpasswd -a <NAS_USER> -s
sudo smbpasswd -e <NAS_USER>

# 验证配置
sudo testparm -s

# 启动服务
sudo systemctl enable smbd nmbd
sudo systemctl restart smbd nmbd
```

### 步骤 5：配置 NFS

```bash
# 写入导出配置（详见 7.2 节）
sudo cp /opt/nas/configs/exports /etc/exports

# 使配置生效
sudo exportfs -a

# 启动服务
sudo systemctl enable nfs-kernel-server
sudo systemctl restart nfs-kernel-server
```

### 步骤 6：配置 FTP

```bash
# 写入 FTP 配置（详见 7.3 节）
sudo cp /opt/nas/configs/vsftpd.conf /etc/vsftpd.conf

# 创建用户白名单
echo "<NAS_USER>" | sudo tee /etc/vsftpd.userlist

# 创建 FTP 日志文件
sudo touch /var/log/vsftpd.log
sudo chmod 640 /var/log/vsftpd.log

# 启动服务
sudo systemctl enable vsftpd
sudo systemctl restart vsftpd
```

### 步骤 7：配置 WebDAV

```bash
# 安装 htpasswd 工具
sudo apt-get install -y apache2-utils

# 生成 htpasswd 文件（bcrypt 哈希）
sudo htpasswd -cb /etc/rclone-htpasswd <NAS_USER> "<NAS_PASS>"

# 创建 systemd 服务文件（详见 7.4 节）
sudo tee /etc/systemd/system/rclone-webdav.service > /dev/null << EOF
[Unit]
Description=Rclone WebDAV Server
After=network.target

[Service]
Type=simple
User=<NAS_USER>
ExecStart=/usr/bin/rclone serve webdav /data --addr :8080 --htpasswd /etc/rclone-htpasswd
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

# 启动服务
sudo systemctl daemon-reload
sudo systemctl enable rclone-webdav
sudo systemctl restart rclone-webdav
```

### 步骤 8：安装 FileBrowser

```bash
# 下载并安装 FileBrowser（单二进制文件）
# 方式 1：自有文件服务器（推荐，速度快）
cd /tmp
curl -fsSL -o filebrowser.tar.gz https://file.abwen.com/filebroswer/linux-amd64-filebrowser.tar.gz
tar xzf filebrowser.tar.gz
sudo mv filebrowser /usr/local/bin/

# 方式 2：官方脚本（需要能访问 GitHub）
# curl -fsSL https://raw.githubusercontent.com/filebrowser/get/master/get.sh | bash

# 方式 3：通过 GitHub 镜像（备用）
# cd /tmp
# curl -fsSL -o filebrowser.tar.gz https://ghfast.top/https://github.com/filebrowser/filebrowser/releases/download/v2.63.17/linux-amd64-filebrowser.tar.gz
# tar xzf filebrowser.tar.gz
# sudo mv filebrowser /usr/local/bin/

# 验证安装
filebrowser version
# 期望输出: File Browser v2.63.17/...

# 创建配置目录和数据库
sudo mkdir -p /etc/filebrowser
sudo filebrowser config init --database /etc/filebrowser/filebrowser.db

# 配置服务参数
sudo filebrowser config set \
    --database /etc/filebrowser/filebrowser.db \
    --address 0.0.0.0 \
    --port 8081 \
    --root /data \
    --log /var/log/filebrowser.log

# 创建管理员用户（密码至少 12 位，FileBrowser v2.63+ 强制要求）
sudo filebrowser users add <NAS_USER> <NAS_PASS> \
    --database /etc/filebrowser/filebrowser.db \
    --perm.admin

# 创建 systemd 服务文件（详见 7.5 节）
sudo tee /etc/systemd/system/filebrowser.service > /dev/null << 'EOF'
[Unit]
Description=File Browser
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/filebrowser --database /etc/filebrowser/filebrowser.db
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

# 启动服务
sudo systemctl daemon-reload
sudo systemctl enable filebrowser
sudo systemctl start filebrowser
```

### 步骤 9：安装 MinIO

```bash
# 下载并安装 MinIO
sudo curl -fsSL https://dl.min.io/server/minio/release/linux-amd64/minio -o /usr/local/bin/minio
sudo chmod +x /usr/local/bin/minio

# 创建数据目录
sudo mkdir -p /data/minio
sudo chown <NAS_USER>:<NAS_USER> /data/minio

# 复制 systemd 服务文件（详见 7.5 节）
sudo cp /opt/nas/configs/minio.service /etc/systemd/system/

# 启动服务
sudo systemctl daemon-reload
sudo systemctl enable minio
sudo systemctl start minio
```

### 步骤 10：配置防火墙

```bash
# 重置并配置 ufw
sudo ufw --force reset
sudo ufw default deny incoming
sudo ufw default allow outgoing

# 开放必要端口
sudo ufw allow ssh                    # 22/tcp - SSH
sudo ufw allow 139/tcp                # Samba NetBIOS
sudo ufw allow 445/tcp                # Samba SMB
sudo ufw allow 2049/tcp               # NFS TCP
sudo ufw allow 2049/udp               # NFS UDP
sudo ufw allow 21/tcp                 # FTP
sudo ufw allow 30000:31000/tcp        # FTP 被动模式
sudo ufw allow 8080/tcp               # WebDAV
sudo ufw allow 8081/tcp               # FileBrowser
sudo ufw allow 9000/tcp               # MinIO S3 API
sudo ufw allow 9002/tcp               # MinIO Web Console

# 启用防火墙
sudo ufw --force enable
sudo ufw status verbose
```

### 步骤 11：配置 Fail2ban

```bash
# 写入配置（详见 8.2 节）
sudo cp /opt/nas/configs/jail.local /etc/fail2ban/jail.local

# 启动服务
sudo systemctl enable fail2ban
sudo systemctl restart fail2ban
```

### 步骤 12：验证部署

```bash
# 检查所有服务状态
for svc in smbd nmbd nfs-kernel-server vsftpd rclone-webdav filebrowser minio fail2ban; do
    echo "$svc: $(systemctl is-active $svc)"
done

# 检查端口监听
sudo ss -tlnp | grep -E '22|445|139|2049|21|8080|8081'

# 测试 Samba
smbclient -L localhost -U <NAS_USER>%<NAS_PASS>

# 测试 NFS
showmount -e localhost

# 测试 WebDAV
curl -u <NAS_USER>:<NAS_PASS> http://localhost:8080/

# 测试 FileBrowser
curl -s http://localhost:8081/ | head -5

# 测试 MinIO API
curl -s http://localhost:9000/minio/health/live
echo  # 添加换行

# 测试 MinIO Console
curl -s http://localhost:9002/login | head -3

# 测试防火墙
sudo ufw status verbose
```

---

## 7. 服务配置详解

### 7.1 Samba — Windows/Mac 文件共享

**配置文件：** `/etc/samba/smb.conf`

```ini
[global]
   # 基本设置
   workgroup = WORKGROUP
   server string = NAS Server
   security = user
   map to guest = Bad User
   server role = standalone server

   # 日志
   log file = /var/log/samba/log.%m
   max log size = 1000
   logging = file
   panic action = /usr/share/samba/panic-action %d

   # 密码同步（Samba 密码修改时同步系统密码）
   obey pam restrictions = yes
   unix password sync = yes
   passwd program = /usr/bin/passwd %u
   passwd chat = *Enter\snew\s*\spassword:* %n\n *Retype\snew\s*\spassword:* %n\n *password\supdated\ssuccessfully* .
   pam password change = yes

   # 安全 — 最低 SMB2 协议（禁用不安全的 SMB1）
   min protocol = SMB2

   # macOS 兼容性（Time Machine、Finder 友好）
   ea support = yes
   vfs objects = catia fruit streams_xattr
   fruit:metadata = stream
   fruit:model = MacSamba
   fruit:posix_rename = yes
   fruit:veto_appledouble = no
   fruit:wipe_intentionally_left_blank_rfork = yes
   fruit:delete_empty_adfiles = yes

# ==================== 共享定义 ====================

[shared]
   comment = 公共共享
   path = /data/shared
   browseable = yes              # 在网络邻居中可见
   read only = no                # 可写
   create mask = 0775            # 新文件权限
   directory mask = 0775         # 新目录权限
   valid users = <NAS_USER>           # 允许访问的用户列表
   force user = <NAS_USER>            # 强制以 <NAS_USER> 身份操作
   force group = <NAS_USER>

[media]
   comment = 影音文件
   path = /data/media
   browseable = yes
   read only = yes               # 只读（防止误删）
   guest ok = no
   valid users = <NAS_USER>

[documents]
   comment = 文档资料
   path = /data/documents
   browseable = yes
   read only = no
   create mask = 0775
   directory mask = 0775
   valid users = <NAS_USER>

[photos]
   comment = 照片
   path = /data/photos
   browseable = yes
   read only = no
   create mask = 0775
   directory mask = 0775
   valid users = <NAS_USER>

[backups]
   comment = 备份
   path = /data/backups
   browseable = yes
   read only = no
   create mask = 0775
   directory mask = 0775
   valid users = <NAS_USER>

# 用户私有目录（每个用户一个独立 share）
# 注意：不使用 [homes] + %U 变量，因为 %U 在 path 中
# 解析不稳定，可能导致路径错误
[<NAS_USER>]
   comment = <NAS_USER> 私有目录
   path = /data/private/<NAS_USER>
   browseable = no               # 不在列表中显示，需直接访问
   read only = no
   create mask = 0700            # 仅用户本人可访问
   directory mask = 0700
   valid users = <NAS_USER>
```

**关键配置说明：**

| 参数                    | 说明                                                    |
|------------------------|--------------------------------------------------------|
| `security = user`      | 用户级认证，每个访问者需要用户名密码                    |
| `map to guest = Bad User` | 未知用户自动映射为 Guest（匿名访问）                  |
| `min protocol = SMB2`  | 禁用 SMB1（WannaCry 漏洞利用的协议）                    |
| `ea support = yes`     | 启用扩展属性，macOS 需要                               |
| `vfs objects = fruit`  | 加载 macOS 兼容 VFS 模块                               |
| `browseable = no`      | 私有目录不在网络邻居中显示                              |
| `force user/group`     | 忽略客户端身份，统一以指定用户操作，避免权限问题        |

**为什么不用 `[homes]` + `%U`：**

Samba 的 `path` 指令虽然文档声称支持 `%U` 变量，但 `%U` 表示的是 session username，
其值取决于连接建立的时序，在多用户环境中经常解析为字面量 `%U`，导致路径变成
`/data/private/%U` 而非预期路径。使用独立 share 段虽然配置稍多，但完全避免了
这个坑，是生产环境推荐的做法。

### 7.2 NFS — Linux 文件共享

**配置文件：** `/etc/exports`

```
# 格式: 目录  允许网段(选项)
#
# 选项说明:
#   rw              - 读写
#   ro              - 只读
#   sync            - 同步写入（数据立即落盘，安全但稍慢）
#   no_subtree_check - 不检查子目录（性能更好）
#   no_root_squash  - 允许远程 root 保持 root 权限（局域网内信任）

/data/shared    [REDACTED]/24(rw,sync,no_subtree_check,no_root_squash)
/data/media     [REDACTED]/24(ro,sync,no_subtree_check)
/data/documents [REDACTED]/24(rw,sync,no_subtree_check,no_root_squash)
/data/photos    [REDACTED]/24(rw,sync,no_subtree_check,no_root_squash)
/data/backups   [REDACTED]/24(rw,sync,no_subtree_check,no_root_squash)
```

**注意事项：**

- IP 段限制为 `[REDACTED]/24`，仅允许同局域网设备访问
- `/data/media` 设为只读，防止客户端误删影音文件
- 批量部署时，如果网段不同，需修改此文件中的 IP 段
- `no_root_squash` 允许远程 root 以 root 身份操作文件，适合家庭/办公局域网；
  如果部署在公网，应改为 `root_squash`（默认值）

**使配置生效：**

```bash
sudo exportfs -a          # 重新加载导出表
sudo exportfs -v           # 查看当前导出（验证）
sudo systemctl restart nfs-kernel-server  # 重启服务
```

### 7.3 vsftpd — FTP 服务

**配置文件：** `/etc/vsftpd.conf`

```ini
# 运行模式
listen=YES                      # 独立守护进程模式（非 xinetd）
listen_ipv6=NO                  # 仅监听 IPv4

# 访问控制
anonymous_enable=NO             # 禁止匿名访问
local_enable=YES                # 允许本地系统用户登录
write_enable=YES                # 允许写操作（上传/删除/改名）
local_umask=022                 # 新文件权限掩码（结果: 755/644）

# 用户隔离 — 锁定用户在自己的目录中
chroot_local_user=YES           # 所有用户锁定在家目录
allow_writeable_chroot=YES      # 允许家目录可写（默认不允许）
user_sub_token=$USER            # 变量替换 token
local_root=/data/private/$USER  # 用户登录后进入的根目录

# 被动模式（解决防火墙/NAT 环境下的连接问题）
pasv_enable=YES
pasv_min_port=30000             # 被动模式端口范围起始
pasv_max_port=31000             # 被动模式端口范围结束

# 白名单机制 — 只允许列表中的用户登录
userlist_enable=YES
userlist_file=/etc/vsftpd.userlist
userlist_deny=NO                # NO = 白名单模式（列表中的用户允许）

# 日志
dirmessage_enable=YES           # 进入目录时显示消息
use_localtime=YES               # 使用本地时间
xferlog_enable=YES              # 启用传输日志

# 数据连接
connect_from_port_20=YES        # 使用端口 20 进行主动模式数据连接
```

**关键安全设计：**

1. **禁止匿名** — `anonymous_enable=NO`，不暴露任何公共入口
2. **Chroot 隔离** — 用户登录后被锁定在 `/data/private/$USER` 中，无法浏览系统目录
3. **白名单模式** — 只有 `/etc/vsftpd.userlist` 中列出的用户才能 FTP 登录
4. **被动模式端口范围** — 限制在 30000-31000，便于防火墙精确放行

**用户白名单文件：** `/etc/vsftpd.userlist`

```
<NAS_USER>
```

每行一个用户名，只有列在此文件中的用户才能通过 FTP 登录。

### 7.4 rclone — WebDAV 服务

**Systemd 服务文件：** `/etc/systemd/system/rclone-webdav.service`

```ini
[Unit]
Description=Rclone WebDAV Server
After=network.target

[Service]
Type=simple
User=<NAS_USER>
ExecStart=/usr/bin/rclone serve webdav /data \
    --addr :8080 \
    --htpasswd /etc/rclone-htpasswd
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
```

**参数说明：**

| 参数              | 说明                                         |
|-------------------|----------------------------------------------|
| `serve webdav`    | rclone 的 WebDAV 服务模式                     |
| `/data`           | 共享根目录，暴露整个 /data 树                  |
| `--addr :8080`    | 监听 8080 端口                                |
| `--htpasswd`      | 使用 Apache htpasswd 文件进行认证（推荐）      |
| `Restart=on-failure` | 进程异常退出后自动重启                     |
| `RestartSec=10`   | 重启前等待 10 秒，避免频繁重启                 |

**认证配置（htpasswd 文件方式）：**

setup.sh 会自动安装 `apache2-utils` 包并使用 `htpasswd` 工具生成密码文件：

```bash
# 安装 htpasswd 工具（setup.sh 自动执行）
apt-get install -y apache2-utils

# 生成 htpasswd 密码文件（使用 bcrypt 哈希）
htpasswd -cb /etc/rclone-htpasswd <NAS_USER> "<NAS_PASS>"

# 设置文件权限
chown <NAS_USER>:<NAS_USER> /etc/rclone-htpasswd
```

**为什么选择 htpasswd 而非 rclone obscure：**

- `htpasswd` 生成的是 Apache 标准 bcrypt 哈希，兼容性更好
- `rclone obscure` 生成的哈希在多次部署验证中出现兼容性问题
- `htpasswd` 文件可以支持多用户，便于权限管理
- `htpasswd` 是业界标准工具，文档和调试支持更完善

**为什么选 rclone 而不是 Apache/Nginx + mod_dav：**

- rclone 是单二进制文件，无额外依赖
- 不需要配置 Apache/Nginx 的复杂虚拟主机
- 性能对家用场景完全足够
- 进程挂了 systemd 自动重启，运维成本极低

### 7.5 MinIO — S3 兼容对象存储

**简介：** MinIO 是一个高性能、S3 兼容的对象存储服务器，单二进制文件部署，提供 Web 管理控制台和完整的 S3 API。适合对接云存储、应用开发和备份场景。

**安装位置：** `/usr/local/bin/minio`
**版本：** RELEASE.2025-09-07T16-13-09Z (go1.24.6 linux/amd64)
**数据目录：** `/data/minio`
**许可证：** GNU AGPLv3

**Systemd 服务文件：** `/etc/systemd/system/minio.service`

**MinIO 环境配置文件：** `/etc/default/minio`

```bash
MINIO_ROOT_USER=<NAS_USER>
MINIO_ROOT_PASSWORD=***
```

**MinIO Systemd 服务文件：** `/etc/systemd/system/minio.service`

```ini
[Unit]
Description=MinIO Object Storage
Documentation=https://docs.min.io
Wants=network-online.target
After=network-online.target

[Service]
User=<NAS_USER>
Group=<NAS_USER>
EnvironmentFile=/etc/default/minio
ExecStart=/usr/local/bin/minio server /data/minio --console-address :9002
Restart=always
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

**配置文件说明：**

- 凭证存储在 `/etc/default/minio` 文件中，通过 `EnvironmentFile` 加载
- 文件权限：`chmod 640`（允许 <NAS_USER> 组读取）
- 文件属主：`chown <NAS_USER>:<NAS_USER>`
- 使用统一的 NAS 密码：`$NAS_PASS`（在 setup.sh 中定义）

**端口说明：**

| 端口 | 用途                  |
|------|----------------------|
| 9000 | S3 API 端点           |
| 9002 | Web 管理控制台         |

**安装步骤：**

```bash
# 1. 下载 MinIO 二进制文件
# 方式 1：自有文件服务器（推荐，速度快）
curl -fsSL https://file.abwen.com/minio/minio.linux-amd64.RELEASE.2025-09-07T16-13-09Z -o /usr/local/bin/minio

# 方式 2：官方地址（需要能访问 dl.min.io）
# curl -fsSL https://dl.min.io/server/minio/release/linux-amd64/minio -o /usr/local/bin/minio

# 方式 3：通过 GitHub 镜像（备用）
# curl -fsSL https://ghfast.top/https://dl.min.io/server/minio/release/linux-amd64/minio -o /usr/local/bin/minio

sudo chmod +x /usr/local/bin/minio

# 2. 创建数据目录
sudo mkdir -p /data/minio
sudo chown -R <NAS_USER>:<NAS_USER> /data/minio

# 3. 部署 systemd 服务（见上方配置）
sudo systemctl daemon-reload
sudo systemctl enable minio
sudo systemctl start minio

# 4. 开放防火墙端口
sudo ufw allow 9000/tcp   # S3 API
sudo ufw allow 9002/tcp   # Web Console
```

**访问方式：**

- Web 控制台: `http://[REDACTED]:9002`
- S3 API: `http://[REDACTED]:9000`
- 用户名: `<NAS_USER>`
- 密码: `<NAS_PASS>`

**使用 mc 客户端：**

```bash
# 下载 mc (MinIO Client)
curl -fsSL https://dl.min.io/client/mc/release/linux-amd64/mc -o mc
chmod +x mc

# 配置别名
./mc alias set mynas http://[REDACTED]:9000 <NAS_USER> <NAS_PASS>

# 常用操作
./mc ls mynas/                          # 列出存储桶
./mc mb mynas/backups                   # 创建存储桶
./mc cp localfile.txt mynas/backups/    # 上传文件
./mc cp -r mynas/backups/ ./restore/    # 下载文件
./mc mirror mynas/backups /local/backup # 双向同步
```

**S3 SDK 对接示例（Python boto3）：**

```python
import boto3

s3 = boto3.client(
    's3',
    endpoint_url='http://[REDACTED]:9000',
    aws_access_key_id='admin',
    aws_secret_access_key='***'
)

# 创建存储桶
s3.create_bucket(Bucket='backups')

# 上传文件
s3.upload_file('localfile.txt', 'backups', 'remotefile.txt')
```

**安全建议：**
- 部署后立即修改默认密码（在 Web 控制台中修改）
- 生产环境建议启用 HTTPS
- 可通过 bucket policy 限制访问权限

**资源占用：** 内存 ~140MB，二进制 ~106MB

---

### 7.6 FileBrowser — Web 文件管理器

**简介：** FileBrowser 是一个轻量级 Web 文件管理器，单二进制文件，内存占用极低（< 50 MB），提供浏览器端的文件浏览、上传、下载、编辑、分享等功能。

**安装位置：** `/usr/local/bin/filebrowser`
**配置文件：** `/etc/filebrowser/filebrowser.db`（SQLite 数据库）
**日志文件：** `/var/log/filebrowser.log`

**Systemd 服务文件：** `/etc/systemd/system/filebrowser.service`

```ini
[Unit]
Description=File Browser
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/filebrowser --database /etc/filebrowser/filebrowser.db
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
```

**核心配置说明：**

FileBrowser 的配置存储在 SQLite 数据库中，通过命令行管理：

```bash
# 初始化数据库
sudo filebrowser config init --database /etc/filebrowser/filebrowser.db

# 设置服务参数
sudo filebrowser config set \
    --database /etc/filebrowser/filebrowser.db \
    --address 0.0.0.0 \       # 监听所有网卡
    --port 8081 \              # Web 端口
    --root /data \             # 文件根目录
    --log /var/log/filebrowser.log  # 日志路径
```

| 参数              | 说明                                         |
|-------------------|----------------------------------------------|
| `--address`       | 监听地址，`0.0.0.0` 表示所有网卡             |
| `--port`          | Web 服务端口，默认 8080，此处用 8081 避免与 WebDAV 冲突 |
| `--root`          | 文件根目录，设为 `/data` 暴露整个数据树       |
| `--database`      | SQLite 数据库路径，存储用户和配置             |
| `--log`           | 日志文件路径                                  |

**用户管理：**

```bash
# 添加管理员用户（密码至少 12 位，v2.63+ 强制要求）
sudo filebrowser users add <NAS_USER> <NAS_PASS> \
    --database /etc/filebrowser/filebrowser.db \
    --perm.admin

# 添加普通用户
sudo filebrowser users add alice alicepassword123 \
    --database /etc/filebrowser/filebrowser.db

# 查看所有用户
sudo filebrowser users ls --database /etc/filebrowser/filebrowser.db

# 修改用户密码
sudo filebrowser users update <NAS_USER> --password newpassword123 \
    --database /etc/filebrowser/filebrowser.db

# 删除用户
sudo filebrowser users delete alice \
    --database /etc/filebrowser/filebrowser.db
```

**权限控制：**

FileBrowser 支持细粒度的权限管理，可以在 Web 界面或命令行设置：

| 权限          | 说明                           |
|--------------|-------------------------------|
| `--perm.admin` | 管理员权限（可管理用户和配置）|
| `--scope`      | 限制用户只能访问指定目录       |

示例：限制用户只能访问自己的私有目录：

```bash
sudo filebrowser users add bob bobpassword123 \
    --database /etc/filebrowser/filebrowser.db \
    --scope /data/private/bob
```

**功能特性：**

- **文件浏览** — 目录树导航，支持列表/网格视图
- **上传下载** — 拖拽上传，批量下载（ZIP 打包）
- **在线编辑** — 文本文件在线编辑（代码高亮）
- **文件分享** — 生成临时分享链接（可设过期时间）
- **多语言** — 支持中文界面（登录后在设置中切换）
- **用户管理** — 管理员可在 Web 界面添加/删除用户
- **Shell 集成** — 可配置执行自定义命令（默认禁用）

**与 WebDAV (rclone) 的区别：**

| 特性        | FileBrowser (8081)          | rclone WebDAV (8080)       |
|------------|-----------------------------|---------------------------|
| 界面        | 美观的 Web UI，有文件预览    | 原生 WebDAV 协议，无 UI    |
| 适用场景    | 浏览器直接访问，临时文件管理  | 挂载为网络驱动器，程序集成  |
| 认证方式    | Web 登录（JWT Token）        | HTTP Basic Auth            |
| 资源占用    | ~50 MB 内存                  | ~30 MB 内存                |
| 功能        | 浏览/上传/下载/编辑/分享     | 仅 WebDAV 协议访问         |

两者可以共存，FileBrowser 给用户用，WebDAV 给程序/挂载用。

---

## 8. 安全配置

### 8.1 UFW 防火墙

**策略：默认拒绝所有入站，仅开放必要端口。**

| 端口            | 协议  | 服务         | 说明                          |
|-----------------|-------|--------------|-------------------------------|
| 22/tcp          | TCP   | SSH          | 远程管理                      |
| 139/tcp         | TCP   | Samba        | NetBIOS Session（Windows 共享）|
| 445/tcp         | TCP   | Samba        | SMB over TCP（主要共享端口）   |
| 2049/tcp        | TCP   | NFS          | NFS 数据传输                  |
| 2049/udp        | UDP   | NFS          | NFS 数据传输（UDP 备用）      |
| 21/tcp          | TCP   | FTP          | FTP 控制连接                  |
| 30000:31000/tcp | TCP   | FTP Passive  | FTP 被动模式数据通道          |
| 8080/tcp        | TCP   | WebDAV       | WebDAV HTTP 服务              |

**配置命令：**

```bash
# 重置所有规则
sudo ufw --force reset

# 默认策略
sudo ufw default deny incoming
sudo ufw default allow outgoing

# 开放端口
sudo ufw allow ssh
sudo ufw allow 139/tcp
sudo ufw allow 445/tcp
sudo ufw allow 2049/tcp
sudo ufw allow 2049/udp
sudo ufw allow 21/tcp
sudo ufw allow 30000:31000/tcp
sudo ufw allow 8080/tcp

# 启用（--force 跳过确认提示）
sudo ufw --force enable
```

**验证：**

```bash
sudo ufw status verbose
```

### 8.2 Fail2ban 入侵防护

**配置文件：** `/etc/fail2ban/jail.local`

```ini
[DEFAULT]
# 封禁时间: 1 小时
bantime = 3600
# 检测时间窗口: 10 分钟
findtime = 600
# 最大失败次数
maxretry = 5

# SSH 防护
[sshd]
enabled = true
port = ssh
filter = sshd
logpath = /var/log/auth.log

# FTP 防护
[vsftpd]
enabled = true
port = ftp,ftp-data,ftps,ftps-data
filter = vsftpd
logpath = /var/log/vsftpd.log
maxretry = 5
```

**工作机制：**

- 监控日志文件中连续失败的认证记录
- 同一 IP 在 `findtime`（10分钟）内失败超过 `maxretry`（5次）次
- 自动将该 IP 加入 iptables 封禁规则，持续 `bantime`（1小时）
- 封禁到期后自动解除

**常用管理命令：**

```bash
# 查看所有 jail 状态
sudo fail2ban-client status

# 查看特定 jail 详情
sudo fail2ban-client status sshd
sudo fail2ban-client status vsftpd

# 手动解封 IP
sudo fail2ban-client set sshd unbanip 192.168.1.100

# 手动封禁 IP
sudo fail2ban-client set sshd banip 192.168.1.100
```

**关于 Samba 防护：**

Fail2ban 默认不包含 `samba-auth` filter，且 Samba 日志格式因版本而异。
本方案暂未启用 Samba jail，如需启用需自定义 filter 规则：

```bash
# 创建自定义 Samba filter（可选）
sudo tee /etc/fail2ban/filter.d/samba-auth.conf > /dev/null << 'EOF'
[Definition]
failregex = ^.*smbd.*Authentication for user .* FAILED.*$
ignoreregex =
EOF
```

### 8.3 自动安全更新

通过 `unattended-upgrades` 包实现自动安装安全补丁。

**默认配置文件：** `/etc/apt/apt.conf.d/50unattended-upgrades`

该包安装后自动启用 systemd 服务 `unattended-upgrades.service`，每天自动：
1. 检查 Debian 安全仓库中的新补丁
2. 下载并安装安全更新
3. 不自动重启系统（需手动重启或配置自动重启）

**验证状态：**

```bash
systemctl status unattended-upgrades
sudo unattended-upgrade --dry-run  # 模拟运行，查看会安装哪些更新
```

---

## 9. 用户管理

### 添加用户

```bash
sudo /opt/nas/scripts/add-user.sh <用户名> <密码>

# 示例
sudo /opt/nas/scripts/add-user.sh alice mypassword123
```

**脚本执行流程：**

1. 创建系统用户（`useradd -m -s /bin/bash`）
2. 设置系统密码
3. 添加 Samba 用户并启用（`smbpasswd -a` + `smbpasswd -e`）
4. 创建私有目录 `/data/private/<用户名>`，权限 700
5. 在 `/etc/samba/smb.conf` 中追加该用户的独立 share 段
6. 重启 smbd 使配置生效

**添加用户后还需要手动操作：**

```bash
# 将新用户加入 FTP 白名单
echo "alice" | sudo tee -a /etc/vsftpd.userlist

# 将新用户加入 NFS 共享访问（NFS 按 IP 控制，不需要用户级别配置）

# 如果需要让新用户也能访问公共共享，需要在 smb.conf 的
# [shared] [documents] [photos] [backups] 的 valid users 中添加
```

### 删除用户

```bash
# 删除用户但保留数据
sudo /opt/nas/scripts/remove-user.sh alice

# 删除用户并清除数据
sudo /opt/nas/scripts/remove-user.sh alice --delete-data
```

**脚本执行流程：**

1. 从 `smb.conf` 中移除该用户的 share 段
2. 删除 Samba 用户（`smbpasswd -x`）
3. 根据参数决定是否删除私有目录数据
4. 删除系统用户（`userdel`）

**安全限制：**
- 不允许删除 `root` 和 `<NAS_USER>`（系统保护用户）
- 不自动清理 FTP 白名单，需手动移除

### 修改用户密码

```bash
# 修改系统密码
sudo passwd <用户名>

# 修改 Samba 密码
sudo smbpasswd <用户名>
```

---

## 10. 运维管理

### 服务管理命令

```bash
# 查看所有 NAS 服务状态
for svc in smbd nmbd nfs-kernel-server vsftpd rclone-webdav fail2ban; do
    printf '%-22s %s\n' "$svc:" "$(systemctl is-active $svc)"
done

# 重启某个服务
sudo systemctl restart smbd
sudo systemctl restart nfs-kernel-server
sudo systemctl restart vsftpd
sudo systemctl restart rclone-webdav
sudo systemctl restart fail2ban

# 查看服务日志
sudo journalctl -u smbd -f           # 实时跟踪 Samba 日志
sudo journalctl -u rclone-webdav -f   # 实时跟踪 WebDAV 日志
sudo journalctl -u nfs-kernel-server  # NFS 日志
```

### 磁盘健康监控

```bash
# 查看 SMART 信息（需要指定磁盘设备）
sudo smartctl -a /dev/nvme0n1

# 快速健康检查
sudo smartctl -H /dev/nvme0n1

# 启用自动监控守护进程
sudo systemctl enable smartd
sudo systemctl start smartd
```

### 日志管理

| 日志文件                        | 服务       | 说明                    |
|--------------------------------|-----------|------------------------|
| /var/log/samba/log.%m          | Samba     | 按客户端 IP 分文件      |
| /var/log/vsftpd.log            | vsftpd    | FTP 传输日志            |
| /var/log/auth.log              | 系统      | SSH/系统认证日志        |
| /var/log/fail2ban.log          | Fail2ban  | 封禁/解封记录           |

### 配置备份

建议定期备份以下文件：

```bash
# 一键备份所有关键配置
tar czf /data/backups/nas-config-$(date +%Y%m%d).tar.gz \
    /etc/samba/smb.conf \
    /etc/exports \
    /etc/vsftpd.conf \
    /etc/vsftpd.userlist \
    /etc/fail2ban/jail.local \
    /etc/systemd/system/rclone-webdav.service \
    /opt/nas/
```

---

## 11. 客户端访问指南

### Windows 访问 Samba

**方法 1 — 资源管理器地址栏：**

```
\\[REDACTED]\shared
\\[REDACTED]\<NAS_USER>
```

输入用户名 `<NAS_USER>`，密码 `<NAS_PASS>`。

**方法 2 — 映射网络驱动器：**

1. 打开"此电脑" → 右键 → "映射网络驱动器"
2. 输入路径: `\\[REDACTED]\shared`
3. 勾选"使用其他凭据连接"
4. 输入用户名和密码

**方法 3 — 命令行：**

```cmd
net use Z: \\[REDACTED]\shared /user:<NAS_USER> <NAS_PASS> /persistent:yes
```

### macOS 访问 Samba

1. Finder → 前往 → 连接服务器 (⌘K)
2. 输入: `smb://[REDACTED]/shared`
3. 输入用户名 `<NAS_USER>`，密码 `<NAS_PASS>`

### Linux 访问 Samba

```bash
# 安装客户端
sudo apt-get install cifs-utils

# 挂载
sudo mount -t cifs //[REDACTED]/shared /mnt/nas \
    -o username=<NAS_USER>,password=<NAS_PASS>

# 永久挂载（加入 /etc/fstab）
echo '//[REDACTED]/shared /mnt/nas cifs credentials=/etc/samba/credentials,uid=1000,gid=1000 0 0' | sudo tee -a /etc/fstab
echo 'username=<NAS_USER>' | sudo tee /etc/samba/credentials
echo 'password=<NAS_PASS>' | sudo tee -a /etc/samba/credentials
sudo chmod 600 /etc/samba/credentials
```

### Linux 访问 NFS

```bash
# 安装 NFS 客户端
sudo apt-get install nfs-common

# 挂载
sudo mount -t nfs [REDACTED]:/data/shared /mnt/nas

# 永久挂载（加入 /etc/fstab）
echo '[REDACTED]:/data/shared /mnt/nas nfs defaults 0 0' | sudo tee -a /etc/fstab
```

### FTP 访问

**命令行：**
```bash
ftp [REDACTED]
# 用户名: <NAS_USER>
# 密码: <NAS_PASS>
```

**GUI 工具（FileZilla 等）：**
- 主机: `[REDACTED]`
- 端口: `21`
- 用户名: `<NAS_USER>`
- 密码: `<NAS_PASS>`

### WebDAV 访问

**浏览器直接访问：**
```
http://[REDACTED]:8080/
```
输入用户名 `<NAS_USER>`，密码 `<NAS_PASS>`。

**Linux 挂载 WebDAV：**
```bash
sudo apt-get install davfs2
sudo mount -t davfs http://[REDACTED]:8080/ /mnt/webdav
```

**macOS 挂载 WebDAV：**
Finder → 前往 → 连接服务器 → `http://[REDACTED]:8080/`

**Windows 映射 WebDAV：**
```cmd
net use W: http://[REDACTED]:8080/ /user:<NAS_USER> <NAS_PASS>
```

### FileBrowser 访问

**浏览器访问：**
```
http://[REDACTED]:8081/
```
输入用户名 `<NAS_USER>`，密码 `<NAS_PASS>`。

**功能使用：**
- **文件浏览** — 左侧目录树导航，右侧文件列表
- **上传文件** — 点击上传按钮或拖拽文件到页面
- **下载文件** — 点击文件名或右键选择下载
- **在线编辑** — 双击文本文件直接在浏览器中编辑
- **文件分享** — 右键选择"分享"生成临时链接
- **切换中文** — 登录后点击用户头像 → 设置 → 语言 → 简体中文

**移动端访问：**
FileBrowser 响应式设计，手机/平板浏览器直接访问，无需安装 App。

---

## 12. 故障排查

### 服务无法启动

```bash
# 查看详细错误日志
sudo journalctl -u <服务名> --no-pager -n 50

# 常见服务名: smbd, nmbd, nfs-kernel-server, vsftpd, rclone-webdav, fail2ban
```

### Samba 连接失败

```bash
# 1. 检查配置语法
sudo testparm -s

# 2. 检查服务状态
systemctl status smbd nmbd

# 3. 检查防火墙
sudo ufw status | grep 445

# 4. 本地测试
smbclient -L localhost -U <NAS_USER>%<NAS_PASS>

# 5. 查看 Samba 用户列表
sudo pdbedit -L

# 6. 检查日志
sudo tail -50 /var/log/samba/log.*
```

### NFS 无法挂载

```bash
# 1. 检查 NFS 服务
systemctl status nfs-kernel-server

# 2. 查看导出列表
showmount -e localhost
# 或
sudo exportfs -v

# 3. 检查 rpcbind
systemctl status rpcbind

# 4. 客户端测试
showmount -e [REDACTED]

# 5. 检查 exports 语法
cat /etc/exports
```

### FTP 连接超时

```bash
# 1. 确认 vsftpd 运行中
systemctl status vsftpd

# 2. 确认用户在白名单中
cat /etc/vsftpd.userlist

# 3. 检查被动模式端口
sudo ufw status | grep 30000

# 4. 本地测试
curl ftp://<NAS_USER>:<NAS_PASS>@localhost/

# 5. 检查日志
sudo tail -50 /var/log/vsftpd.log
```

### WebDAV 无法访问

```bash
# 1. 检查 rclone 进程
systemctl status rclone-webdav

# 2. 查看日志（常见错误: --pass 参数不是 obscure 哈希）
sudo journalctl -u rclone-webdav -n 20

# 3. 本地测试
curl -u <NAS_USER>:<NAS_PASS> http://localhost:8080/

# 4. 重新生成密码哈希并更新服务文件
NEW_HASH=$(rclone obscure "<NAS_PASS>")
# 编辑 /etc/systemd/system/rclone-webdav.service 中的 --pass 值
sudo systemctl daemon-reload
sudo systemctl restart rclone-webdav
```

### Fail2ban 启动失败

```bash
# 1. 查看详细错误
sudo journalctl -u fail2ban -n 20

# 2. 常见原因: filter 名称不存在
ls /etc/fail2ban/filter.d/ | grep <filter名>

# 3. 常见原因: 日志文件不存在
sudo touch /var/log/vsftpd.log

# 4. 验证配置后重启
sudo fail2ban-client -t  # 测试配置
sudo systemctl restart fail2ban
```

### FileBrowser 无法访问

```bash
# 1. 检查服务状态
systemctl status filebrowser

# 2. 查看日志
sudo journalctl -u filebrowser -n 50
sudo tail -50 /var/log/filebrowser.log

# 3. 检查端口监听
sudo ss -tlnp | grep 8081

# 4. 本地测试
curl http://localhost:8081/

# 5. 检查防火墙
sudo ufw status | grep 8081

# 6. 重置管理员密码（如果忘记密码）
sudo filebrowser users update <NAS_USER> --password newpassword123 \
    --database /etc/filebrowser/filebrowser.db

# 7. 检查数据库文件权限
ls -la /etc/filebrowser/filebrowser.db
sudo chown root:root /etc/filebrowser/filebrowser.db
```

---

## 13. 批量部署

### 方式 1：脚本部署（推荐简单场景）

**前提：** 新机器已安装 Debian 13，有 sudo 用户。

```bash
# 1. 从模板机器复制部署包
scp -r /opt/nas/ <NAS_USER>@<新机器IP>:/tmp/

# 2. 在新机器上执行
ssh <NAS_USER>@<新机器IP>
sudo mv /tmp/nas /opt/
sudo chown -R root:root /opt/nas/scripts/*.sh
sudo chmod +x /opt/nas/scripts/*.sh
sudo /opt/nas/scripts/setup.sh
```

### 方式 2：Git 仓库部署（推荐多机管理）

```bash
# 1. 将 /opt/nas/ 推送到 git 仓库
cd /opt/nas
git init
git add .
git commit -m "NAS deployment package"
# 推送到你的 git 服务器

# 2. 在新机器上克隆并部署
ssh <NAS_USER>@<新机器IP>
sudo mkdir -p /opt/nas
git clone <你的仓库地址> /opt/nas
sudo /opt/nas/scripts/setup.sh
```

### 部署前检查清单

| 检查项                         | 命令                                    |
|-------------------------------|----------------------------------------|
| 系统版本是否为 Debian 13       | `cat /etc/os-release`                  |
| 网络是否配置好                 | `ip addr show`                         |
| apt 源是否可用                 | `apt-get update`                       |
| sudo 用户是否存在              | `sudo -l`                              |
| /data 目录磁盘空间是否充足     | `df -h`                                |

### 部署后验证清单

| 验证项               | 命令                                             | 期望结果        |
|---------------------|--------------------------------------------------|----------------|
| Samba 服务          | `systemctl is-active smbd`                       | active         |
| NFS 服务            | `systemctl is-active nfs-kernel-server`          | active         |
| FTP 服务            | `systemctl is-active vsftpd`                     | active         |
| WebDAV 服务         | `systemctl is-active rclone-webdav`              | active         |
| FileBrowser 服务    | `systemctl is-active filebrowser`                | active         |
| MinIO 服务          | `systemctl is-active minio`                      | active         |
| Fail2ban            | `systemctl is-active fail2ban`                   | active         |
| UFW 防火墙          | `sudo ufw status`                                | active         |
| Samba 共享列表      | `smbclient -L localhost -U <NAS_USER>%<NAS_PASS>`      | 显示共享列表    |
| NFS 导出列表        | `showmount -e localhost`                         | 显示导出路径    |
| WebDAV 响应         | `curl -u <NAS_USER>:<NAS_PASS> http://localhost:8080/` | HTTP 200/207   |
| 端口监听            | `sudo ss -tlnp`                                  | 所有端口正常    |

---

## 14. 配置文件清单

以下为本系统涉及的所有配置文件，部署/维护时请对照此清单：

| # | 文件路径                                    | 所属服务    | 说明                       |
|---|---------------------------------------------|------------|---------------------------|
| 1 | `/etc/samba/smb.conf`                       | Samba      | SMB 共享定义与全局参数     |
| 2 | `/etc/exports`                              | NFS        | NFS 导出目录与权限         |
| 3 | `/etc/vsftpd.conf`                          | vsftpd     | FTP 服务配置               |
| 4 | `/etc/vsftpd.userlist`                      | vsftpd     | FTP 用户白名单             |
| 5 | `/etc/systemd/system/rclone-webdav.service`  | rclone     | WebDAV systemd 服务单元    |
| 6 | `/etc/systemd/system/filebrowser.service`    | filebrowser| FileBrowser systemd 服务单元|
| 7 | `/etc/filebrowser/filebrowser.db`            | filebrowser| FileBrowser SQLite 数据库  |
| 8 | `/etc/fail2ban/jail.local`                  | fail2ban   | 入侵防护规则               |
| 9 | `/var/log/vsftpd.log`                       | vsftpd     | FTP 日志文件（需预创建）   |

**部署包文件清单 (`/opt/nas/`)：**

| # | 文件路径                              | 说明                     |
|---|---------------------------------------|--------------------------|
| 1 | `configs/smb.conf`                    | Samba 配置模板           |
| 2 | `configs/exports`                     | NFS 导出配置模板         |
| 3 | `configs/vsftpd.conf`                 | FTP 配置模板             |
| 4 | `configs/jail.local`                  | Fail2ban 配置模板        |
| 5 | `configs/rclone-webdav.service`       | WebDAV 服务单元模板      |
| 6 | `configs/filebrowser.service`        | FileBrowser 服务单元模板 |
| 7 | `configs/minio.service`              | MinIO 服务单元模板       |
| 7 | `scripts/setup.sh`                    | 一键部署脚本             |
| 8 | `scripts/add-user.sh`                 | 添加用户脚本             |
| 9 | `scripts/remove-user.sh`              | 删除用户脚本             |

---

## 附录 A：端口速查表

| 端口          | 协议  | 服务         | 方向  | 说明                        |
|--------------|-------|-------------|-------|----------------------------|
| 22           | TCP   | SSH         | 入站  | 远程管理                    |
| 139          | TCP   | Samba       | 入站  | NetBIOS Session Service     |
| 445          | TCP   | Samba       | 入站  | SMB over TCP (Direct Host)  |
| 2049         | TCP   | NFS         | 入站  | NFS 数据                    |
| 2049         | UDP   | NFS         | 入站  | NFS 数据（UDP 备用）        |
| 21           | TCP   | FTP         | 入站  | FTP 控制连接                |
| 30000-31000  | TCP   | FTP         | 入站  | FTP 被动模式数据通道        |
| 8080         | TCP   | WebDAV      | 入站  | WebDAV HTTP 服务            |
| 8081         | TCP   | FileBrowser | 入站  | Web 文件管理器              |
| 9000         | TCP   | MinIO       | 入站  | S3 API 端点                |
| 9002         | TCP   | MinIO       | 入站  | Web 管理控制台              |

## 附录 B：Systemd 服务清单

| 服务名              | 类型     | 开机启动 | 说明                          |
|--------------------|---------|---------|------------------------------|
| smbd               | 系统服务 | enabled | Samba 文件共享主进程           |
| nmbd               | 系统服务 | enabled | Samba NetBIOS 名称服务         |
| nfs-kernel-server  | 系统服务 | enabled | NFS 内核服务器                 |
| rpcbind            | 系统服务 | enabled | RPC 端口映射（NFS 依赖）       |
| vsftpd             | 系统服务 | enabled | FTP 服务器                     |
| rclone-webdav      | 自定义   | enabled | WebDAV 服务（rclone serve）    |
| filebrowser        | 自定义   | enabled | Web 文件管理器                 |
| minio              | 自定义   | enabled | MinIO 对象存储 (S3 API + Web Console) |
| fail2ban           | 系统服务 | enabled | 入侵检测与自动封禁            |
| ufw                | 系统服务 | enabled | 防火墙                         |
| unattended-upgrades| 系统服务 | enabled | 自动安全更新                   |
| smartmontools      | 系统服务 | enabled | 磁盘 SMART 监控               |

---

> **文档维护说明：** 本文档应随系统版本更新同步修改。每次配置变更后，请更新对应章节
> 并修改文档顶部的版本号和日期。建议将本文档与 `/opt/nas/` 部署包一起纳入 git 版本管理。
