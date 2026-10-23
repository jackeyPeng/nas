# 家用 NAS 系统 — 产品技术手册

> 版本: v1.4（AI 助手章节随 feat/ai-assistant 分支先行补充）  
> 最后更新: 2026-10-21  
> 适用系统: Debian GNU/Linux 13 (trixie), 内核 6.12.94+

> **v1.4 变更**: 监控告警模块重设计（SVG 仪表盘、分区使用表、多渠道告警）、RAID 扩容、存储配额、系统设置重构、自动化运维脚本。v1.4.0 起 FileBrowser 已彻底移除，文件管理请使用面板内置文件管理器（见 7.7 节）。
>
> **AI 助手（开发中，见 11.6 节）**: 新增自然语言 NAS 助手，默认关闭，开启需配置模型端点并通过连通性测试；写操作经服务端确认门二次确认后才会执行。

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
   - 7.5 [rclone — S3 对象存储](#75-rclone--s3-对象存储)
   - 7.6 [rclone — 远端同步（Web 面板）](#76-rclone--远端同步web-面板)
   - 7.7 [面板文件管理 — 内置 Web 文件页](#77-面板文件管理--内置-web-文件页)
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
| S3 对象存储 | rclone serve s3  | S3 兼容对象存储，bucket 自动映射目录      | 9000             |

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
| rclone              | 1.75.2 (.deb)           | WebDAV + S3 服务 (serve 模式) | ~30 MB |
| rpcbind             | 1.2.7-1                 | RPC 端口映射 (NFS 依赖)| ~0.5 MB        |
| xfsprogs            | 6.15.0-1                | XFS 文件系统工具        | ~5 MB          |
| mdadm               | 4.3+20250117-1          | RAID 管理工具           | ~2 MB          |
| lvm2                | 2.03.31-1               | LVM 逻辑卷管理          | ~5 MB          |

### 安全与运维包

| 包名                | 版本         | 用途                       | 大小    |
|---------------------|-------------|---------------------------|---------|
| fail2ban            | 1.1.0-8     | 登录暴力破解防护            | ~5 MB   |
| ufw                 | 0.36.2-9    | 简易防火墙管理              | ~2 MB   |
| iptables            | 1.8.11-2    | 底层包过滤 (ufw 后端)       | ~4 MB   |
| smartmontools       | 7.4-3       | 磁盘 SMART 健康监控         | ~6 MB   |
| unattended-upgrades | 2.12        | 自动安全补丁更新            | ~1 MB   |

### 安装命令

**一行命令安装（推荐）：**

```bash
# 交互式（提示输入密码）
curl -fsSL https://get.z1.sale/install.sh | bash

# 无人值守（环境变量传入密码）
NAS_PASS=mySecurePass123 curl -fsSL https://get.z1.sale/install.sh | bash
```

安装脚本自动检测网络（GitHub/Gitee），克隆仓库，交互式设密码，生成 .env，执行 setup.sh，完成后显示访问地址。

**手动安装：**

```bash
sudo apt-get update
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y \
    samba nfs-kernel-server vsftpd \
    fail2ban ufw smartmontools unattended-upgrades \
    smbclient nfs-common xfsprogs mdadm lvm2
```

**总计安装量：** 约 50 MB 下载，230 MB 磁盘占用（含全部依赖；rclone 通过 .deb 包单独安装）。

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
│   └── rclone-s3.service        ← rclone S3 systemd 单元文件
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
| /opt/nas/configs/rclone-s3.service     | /etc/systemd/system/rclone-s3.service     |
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
    samba nfs-kernel-server vsftpd \
    fail2ban ufw smartmontools unattended-upgrades \
    smbclient nfs-common xfsprogs mdadm lvm2
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
ExecStart=/usr/bin/rclone serve webdav /data/nas1/public --addr :8080 --htpasswd /etc/rclone-htpasswd
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

### 步骤 8：安装 rclone S3 服务

```bash
# 下载并安装 rclone .deb 包（推荐，速度快）
cd /tmp
curl -fsSL -o rclone.deb https://get.z1.sale/releases/rclone-v1.75.2-linux-amd64.deb
sudo dpkg -i rclone.deb
rm rclone.deb

# 验证安装
rclone version
# 期望输出: rclone v1.75.2

# 创建 S3 服务 unit（模板见 7.5 节，写入 /etc/systemd/system/rclone-s3.service；
# 产品一键安装/面板「服务管理」页安装会自动生成，无需手工创建）

# 启动服务
sudo systemctl daemon-reload
sudo systemctl enable rclone-s3
sudo systemctl start rclone-s3
```

### 步骤 9：配置防火墙

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
sudo ufw allow 9000/tcp               # rclone S3 API

# 启用防火墙
sudo ufw --force enable
sudo ufw status verbose
```

### 步骤 10：配置 Fail2ban

```bash
# 写入配置（详见 8.2 节）
sudo cp /opt/nas/configs/jail.local /etc/fail2ban/jail.local

# 启动服务
sudo systemctl enable fail2ban
sudo systemctl restart fail2ban
```

### 步骤 11：验证部署

```bash
# 检查所有服务状态
for svc in smbd nmbd nfs-kernel-server vsftpd rclone-webdav rclone-s3 fail2ban; do
    echo "$svc: $(systemctl is-active $svc)"
done

# 检查端口监听
sudo ss -tlnp | grep -E '22|445|139|2049|21|8080'

# 测试 Samba
smbclient -L localhost -U <NAS_USER>%<NAS_PASS>

# 测试 NFS
showmount -e localhost

# 测试 WebDAV
curl -u <NAS_USER>:<NAS_PASS> http://localhost:8080/

# 测试 rclone S3 API
curl -s http://localhost:9000/
echo  # 添加换行

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
#   root_squash     - 远程 root 降权为 nobody（更安全，需自行改 /etc/exports 开启）
#   no_root_squash  - 允许远程 root 保持 root 权限（默认，挂载即可写）
#   insecure        - 允许客户端使用 >=1024 源端口（NAT 后的客户端必需，见下方说明）

/data/shared    *(rw,sync,no_subtree_check,no_root_squash,insecure)
/data/media     *(ro,sync,no_subtree_check,insecure)
/data/documents *(rw,sync,no_subtree_check,no_root_squash,insecure)
/data/photos    *(rw,sync,no_subtree_check,no_root_squash,insecure)
/data/backups   *(rw,sync,no_subtree_check,no_root_squash,insecure)
```

**注意事项：**

- 导出网段为 `*`（对所有网段开放），跨网段/多网卡客户端可挂载；如需限制网段自行收紧
- **insecure（2026-09-24 起默认）**：NFS 传统 `secure` 选项要求客户端源端口 < 1024，但经过 NAT
  的客户端（家庭/办公网络跨网段访问的常态）源端口会被重映射为高位端口，mountd 会以
  `illegal port` 拒绝挂载（客户端表现为 `access denied by server`）。`insecure` 仅放开源端口限制，
  身份校验仍走客户端 IP，对家用局域网场景风险可控
- `/data/media` 设为只读，防止客户端误删影音文件
- **squash 策略（2026-09-24 起放宽为 no_root_squash）**：产品定位家用/小型办公，可用性优先——
  默认 `no_root_squash`，客户端 root 挂载后直接可写，不会出现"挂上了却 Permission denied"的困惑。
  需要收紧的部署可自行在面板将文件夹改为只读，或手工编辑 `/etc/exports` 加回 `root_squash`
  （注意：面板配置同步会以 folders.db 元数据重新生成托管段，手工改动会被覆盖）

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
ExecStart=/usr/bin/rclone serve webdav /data/nas1/public \
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
| `/data/nas1/public` | WebDAV 根目录＝public 共享（产品默认，与 setup.sh 一致；不暴露用户 home） |
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

### 7.5 rclone — S3 对象存储

**简介：** rclone serve s3 是 rclone 内置的 S3 兼容对象存储服务模式，产品默认以 `/data/nas1/public`（public 共享）为根目录，其下每个子目录自动映射为一个 S3 bucket。无需额外 Web 管理控制台，适合对接云存储、应用开发和备份场景。

**安装位置：** `/usr/bin/rclone`（通过 .deb 包安装）
**版本：** rclone v1.75.2
**数据目录：** `/data/nas1/public`（每个子目录即一个 bucket；不暴露用户 home）
**许可证：** MIT

**Systemd 服务文件：** `/etc/systemd/system/rclone-s3.service`

**rclone S3 环境配置文件：** `/etc/rclone/s3-env`

```bash
RCLONE_S3_AUTH_KEY=<NAS_USER>,<NAS_PASS>
```

**rclone S3 Systemd 服务文件：** `/etc/systemd/system/rclone-s3.service`

```ini
[Unit]
Description=Rclone S3 Object Storage Server
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=<NAS_USER>
Group=<NAS_USER>
EnvironmentFile=/etc/rclone/s3-env
ExecStart=/usr/bin/rclone serve s3 /data/nas1/public --addr :9000 --auth-key ${RCLONE_S3_AUTH_KEY}
Restart=always
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

**配置文件说明：**

- 凭证存储在 `/etc/rclone/s3-env` 文件中，通过 `EnvironmentFile` 加载
- 文件权限：`chmod 640`（允许 <NAS_USER> 组读取）
- 文件属主：`chown <NAS_USER>:<NAS_USER>`
- 使用统一的 NAS 密码：`$NAS_PASS`（在 setup.sh 中定义）
- **S3 access key（`<S3_ACCESS_KEY>`）**：默认等于 `NAS_USER`，但 rclone serve s3 内嵌的
  gofakes3 库会拒绝短于 3 字符的 access key（一律返回 `InvalidAccessKeyId`），
  因此用户名不足 3 字符时（如 `fm`）自动加 `z1-` 前缀（→ `z1-fm`）。
  产品安装的密钥直接写在 rclone-s3.service 的 `--auth-key` 参数里，
  面板改密时自动 sed 改写该 unit 并重启服务；手动部署变体用
  `/etc/rclone/s3-env` 的 `RCLONE_S3_ACCESS_KEY`（EnvironmentFile 注入）
- rclone serve s3 以 `/data/nas1/public` 为根目录（产品默认，setup.sh/面板安装一致），public 下的每个子目录自动成为 S3 bucket（如 `shared`、`backups`、`media` 等）；手动部署变体如以其他目录为根，请自行评估暴露面
- 内网环境下也可使用匿名模式（不带 `--auth-key`），允许无认证访问

**端口说明：**

| 端口 | 用途                  |
|------|----------------------|
| 9000 | S3 API 端点           |

> 注意：rclone serve s3 无 Web 管理控制台，不需要额外端口。

**安装步骤：**

```bash
# 1. 下载并安装 rclone .deb 包
# 方式 1：自有文件服务器（推荐，速度快）
cd /tmp
curl -fsSL -o rclone.deb https://get.z1.sale/releases/rclone-v1.75.2-linux-amd64.deb
sudo dpkg -i rclone.deb
rm rclone.deb

# 方式 2：官方地址（需要能访问 rclone.org）
# curl -fsSL -o rclone.deb https://rclone.org/downloads/rclone-v1.75.2-linux-amd64.deb
# sudo dpkg -i rclone.deb

# 验证
rclone version

# 2. 创建环境配置文件
sudo mkdir -p /etc/rclone
echo 'RCLONE_S3_AUTH_KEY=<NAS_USER>,<NAS_PASS>' | sudo tee /etc/rclone/s3-env
sudo chmod 640 /etc/rclone/s3-env
sudo chown <NAS_USER>:<NAS_USER> /etc/rclone/s3-env

# 3. 部署 systemd 服务（见上方配置）
sudo cp /opt/nas/configs/rclone-s3.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable rclone-s3
sudo systemctl start rclone-s3

# 4. 开放防火墙端口
sudo ufw allow 9000/tcp   # S3 API
```

**访问方式：**

- S3 API: `http://192.168.1.100:9000`
- Access Key: `<NAS_USER>`
- Secret Key: `<NAS_PASS>`
- Bucket 映射：`/data/backups` → bucket `backups`，`/data/media` → bucket `media`，以此类推

**使用 s3cmd 客户端：**

```bash
# 安装 s3cmd
sudo apt-get install -y s3cmd

# 配置 s3cmd（交互式或直接编辑配置文件）
s3cmd --configure
# 或直接创建配置文件
mkdir -p ~/.s3cfg
cat > ~/.s3cfg << EOF
[default]
access_key = <S3_ACCESS_KEY>
secret_key = <NAS_PASS>
host_base = 192.168.1.100:9000
host_bucket = 192.168.1.100:9000
use_https = False
EOF

# 常用操作
s3cmd ls                          # 列出存储桶
s3cmd mb s3://backups             # 创建存储桶
s3cmd put localfile.txt s3://backups/  # 上传文件
s3cmd get s3://backups/remotefile.txt ./  # 下载文件
s3cmd sync s3://backups/ ./restore/      # 同步到本地
s3cmd ls s3://backups             # 列出 bucket 内容
```

**S3 SDK 对接示例（Python boto3）：**

```python
import boto3

s3 = boto3.client(
    's3',
    endpoint_url='http://192.168.1.100:9000',
    aws_access_key_id='<S3_ACCESS_KEY>',
    aws_secret_access_key='<NAS_PASS>',
    region_name='us-east-1'
)

# 列出存储桶
s3.list_buckets()

# 创建存储桶（对应在 /data 下创建同名子目录）
s3.create_bucket(Bucket='backups')

# 上传文件
s3.upload_file('localfile.txt', 'backups', 'remotefile.txt')
```

**安全建议：**
- 生产环境建议启用 HTTPS（可通过反向代理如 Nginx 实现 TLS 终结）
- 内网环境下可使用匿名模式（省略 `--auth-key`），简化内网访问
- 可通过 bucket policy 或文件系统权限限制访问

**资源占用：** 内存 ~30MB，二进制 ~30MB（.deb 包）

---

### 7.6 rclone — 远端同步（Web 面板）

面板"远端同步"页把 rclone 的 remote 配置和同步任务全部可视化，用于数据备份上云、从云恢复、多机同步。

**远端存储管理：**

- 支持 5 种类型：S3 兼容（AWS/MinIO/阿里云OSS/腾讯云COS）、SFTP、WebDAV、FTP、本地路径
- 创建后可随时**编辑**：配置自动回填，密钥/密码等敏感字段脱敏显示，留空即保持不变
- 一键测试连接（rclone lsd 只列目录，不传输数据）

**同步任务：**

- **同步方向**：上传（本地 → 远端，备份到云）/ 下载（远端 → 本地，从云恢复）
- 三种模式：sync（单向，目标多余文件删除）/ copy（仅复制）/ bisync（双向）
- 本地路径限制在已配置共享目录内（白名单硬校验），可在共享下创建子目录作为同步路径
- 带宽限制、并发数、定时（cron 表达式，调度器规划中）
- 任务可随时编辑全部参数；手动运行；同步日志保留最近 200 条

**安全设计：**

- 远端名称白名单校验；敏感字段不回传明文
- 本地路径防目录穿越（拒绝 `..`、绝对路径）
- 下载 + sync 组合显示删除警告

---

### 7.7 面板文件管理 — 内置 Web 文件页

面板内置的整池文件管理器（v3 权限管理已落地）。无需额外服务/端口，随 nas-panel 本体运行，使用面板统一登录与审计。

**功能（v3）：**

- **目录浏览**：存储池全目录树，面包屑导航；列表展示名称/大小/修改时间/属主/权限位，符号链接带标注；单目录超 2000 条截断并提示
- **下载**：单文件下载支持断点续传（HTTP Range）；目录一键 zip 流式打包下载（符号链接不跟随，防止打包池外内容）
- **预览**：文本文件（≤ 1 MB）与图片（≤ 10 MB）在线预览；SVG 一律拒绝（可内联脚本，防存储型 XSS）
- **用量展示**：目录/存储池用量独立异步计算（du），超时自动降级为「计算中/不可用」，不阻塞浏览
- **上传**：多文件流式上传（不占面板内存），实时进度条；同名冲突提示后可选择覆盖重试；落盘文件自动按目标共享对齐权限（public 0664+nasusers / 用户共享 0600+属主）；**上传目录**（「上传目录」按钮，webkitdirectory）：整目录树保留子路径结构上传，缺失父目录自动逐段创建（恒 0755），已存在目录权限一律不动（合并式上传，不覆盖既有数据；子文件同名冲突走同款 409 覆盖确认，重试保留完整子路径不拍平）；浏览器限制：空子目录不会随目录上传重建
- **新建目录 / 重命名**：文件名白名单校验（拒斜杠、`..`、控制字符、超长）；重命名内核原子防并发覆盖
- **移动 / 复制**：目录选择器逐级浏览选定目标；同文件系统移动零拷贝；跨文件系统移动先复制、字节校验、权限对齐全部通过后才删源（任何失败源保留）；目录递归复制符号链接不跟随
- **删除（进回收站）**：删除=零拷贝移入所属共享 `.recycle/<用户>/<原相对路径>`（keeptree），与存储页回收站 Tab 及 SMB 回收站同源——回收站页可直接恢复到原位置（原位已有同名内容时：目录做**合并式恢复**逐子项回原位，文件碰撞加 `.restored` 后缀，均不覆盖既有数据）；共享未开启回收站时回退池根 `.recycle`，经回收站页「(池根 xxx)」条目同样可见、可原位恢复；同名冲突自动加时间戳后缀；删除目录需输入目录名确认（type-to-confirm）；共享文件夹本身、池根、回收站内容均不可经此删除
- **属性 / 改权限 / 改属主**：文件行内「属性」对话框回显属主/属组/权限（八进制+符号位）；权限八进制输入带实时符号位预览与常用预设（0644/0755/0700），仅接受 ≤0777 的常规权限位（setuid/setgid/sticky 拒绝）；目录可勾选递归（递归中符号链接自动跳过，属主修改对链接用 Lchown 不影响目标）；属主/属组下拉选择，未改动项不提交；目录递归修改需输入确认词（type-to-confirm）
- **批量改权限**：多选后工具栏「改权限」，前端串行逐条提交并显示进度 N/M，单条失败不中断，结束汇总成功/失败与原因；批量操作必须输入确认词
- **修复 home 权限（一键模板）**：用户管理页每行「修复 home 权限」按钮，将该用户家目录（存储池根下同名目录）递归修复为 0700 + 属主属组归还该用户（与建户基准同源）；需输入用户名确认；不创建缺失的子目录（只修复权限，不做目录初始化）

**安全设计：**

- 所有文件 API 入参一律为池根相对路径，服务端 `resolvePoolPath` 统一锚定：拒绝绝对路径、`..` 穿越、符号链接逃逸（对最深已存在祖先做真实路径解析），fail-closed；写操作双重锚定（父目录 + 拼接后完整路径各校验一次）
- 移动/复制拒绝目标位于源内部（含符号链接解析后），防止递归自我嵌套写满磁盘
- 权限修改仅接受常规权限位（≤0777，setuid/setgid/sticky 拒绝）；池根本身不可改权限/属主；「修复 home 权限」的目标目录若是符号链接一律拒绝（防止递归操作被引到池外）
- 上传/重命名/移动/复制/删除/改权限/改属主/修复 home 全部纳入审计日志（操作者/参数/结果）
- 访问经面板 JWT 认证

**FileBrowser 移除说明：** v1.4.0 起 FileBrowser 已彻底移除（安装链、服务管理页、系统注册表、HTTPS 证书应用、密码同步、监控端口映射、备份/恢复脚本、i18n 文案全链路清理）。存量旧机器升级时 `setup.sh` 会自动检测并卸载残留的 filebrowser 服务与 `/etc/filebrowser`（卸载前备份到 `/var/backups/filebrowser-<日期>.tar.gz`）；`scripts/cleanup.sh` 保留手动清理残留的能力。

---

### 7.8 工具箱 — 凭证保险箱与密码生成器

侧边栏新增「🧰 工具箱」分组，收纳两个与用户个人数据管理相关的独立工具：🔐 凭证保险箱、🔑 密码生成器。

#### 凭证保险箱（v2，零知识模型）

v1.4.0 起凭证保险箱全面升级为**零知识加密**模型，核心原则：**服务器永远看不到明文，管理员也看不到别人的密码。**

**加密架构：**

```
浏览器端（用户 A）                          服务器端
保险箱口令（用户记忆）                       salt（公开参数）
   ↓ PBKDF2-SHA256（600,000 次迭代）         verify_hash（校验值，不可逆推口令）
MasterKey（32 字节，仅存于浏览器内存）        encrypted（AES-256-GCM 密文）
   ↓ SHA-256(MasterKey) → verify_hash  ──→   比对 verify_hash，一致才返回密文
   ↓ AES-256-GCM 解密密文              ←──
明文显示给用户（仅本地内存，不落盘）
```

- **密钥派生**：PBKDF2-SHA256，600,000 次迭代，salt 16 字节随机数（32 位十六进制）
- **加密**：AES-256-GCM，IV 每次加密随机生成 12 字节，密文格式 `base64(IV(12B) + ciphertext + tag(16B))`
- **验证**：`SHA-256(MasterKey)` 的十六进制与服务器保存的 `verify_hash` 比对，服务器全程不接触口令和派生密钥本身
- **加密内核**：浏览器端 `zkvault.min.js`（基于 `@noble/hashes` + `@noble/ciphers`，MIT 协议，纯 JS 预构建，无需联网/无 CDN 依赖），通过 `window.ZkVault.deriveKey/verificationHash/encryptJSON/decryptJSON` 等接口调用

**数据模型：**

| 表 | 说明 |
|----|------|
| `vault_meta` | 每用户一条：`owner`（主键）、`salt`、`verify_hash`、`iterations`、`failed`（连续失败次数）、`locked_until` |
| `vault_items` | 条目：`owner`、`name`/`category`/`url`/`username`/`notes_preview`（明文元数据，便于列表展示）、`encrypted`（AES-256-GCM 密文，内含真正的密码/备注）、时间戳 |

所有查询均按 `WHERE owner = ?` 行级隔离，管理员账号同样不例外——数据库里只有密文，谁也解不开别人的保险箱。

**API（均需面板登录鉴权）：**

| 端点 | 说明 |
|------|------|
| `GET /api/vault/status` | 查询初始化/锁定状态、条目数、salt、iterations |
| `POST /api/vault/setup` | 首次设置口令（提交 salt + verify_hash + iterations） |
| `POST /api/vault/unlock` | 解锁（提交 verify_hash，服务端常数时间比较） |
| `GET /api/vault/items` | 条目列表（仅返回密文与明文元数据，不解密） |
| `POST /api/vault/create` / `update` / `delete` | 条目增删改（服务端只做校验与存取，不接触明文） |
| `POST /api/vault/change-passphrase` | 修改口令：浏览器端逐条用新密钥重新加密后，与新 salt/verify_hash 一起在单个数据库事务中提交，任一条目重加密失败整体回滚 |
| `POST /api/vault/legacy`、`/legacy/reveal`、`/legacy/purge` | 旧版（v1 服务端加密模型）迁移兼容：仅在新保险箱已设置口令后才可用，用于导出旧条目明文、本地重新加密为新条目、清空旧表 |

**安全措施：**

- 🔒 零知识：加密/解密全部在浏览器完成，服务器只存密文
- 🚫 行级隔离：所有查询 `WHERE owner = ?`，管理员不例外
- 🔑 独立口令：保险箱口令与登录密码完全独立
- ⏱️ 自动锁定：**15 分钟无操作**或切换到其他页面自动锁定，主密钥从浏览器内存清除
- 🛑 暴力破解防护：**连续 5 次解锁失败 → 锁定 30 分钟**（服务端按用户记录失败次数与锁定截止时间，解锁用常数时间比较防时序侧信道）
- 👁️ 查看自动遮罩：点击「查看」显示明文，**15 秒后自动重新遮罩**
- 🧹 剪贴板自动清理：复制密码后 **30 秒自动清空剪贴板**
- 🚫 无恢复机制：忘记口令 = 数据永久丢失，首次设置时明确告知用户

**旧版数据迁移：** 升级后旧版（服务端 PBKDF2+AES-GCM，密钥由管理密码派生）保险箱数据仍可见（`legacy_count`），但处于迁移兼容状态。用户设置新版口令后，可在页面内一键迁移：服务端临时解密旧条目明文返回浏览器（迁移专用的唯一服务端解密路径），浏览器立即用新保险箱密钥重新加密写入新表，迁移完成后旧表被清空，之后旧版明文不再有任何服务端解密入口。

#### 密码生成器

独立工具页面，**不依赖凭证保险箱解锁状态**，任何已登录用户均可直接使用。使用 `crypto.getRandomValues()` 生成安全随机密码，纯浏览器端运行，零外部依赖。

- 长度可调（8-64 位，默认 16），字符类型可选大写/小写/数字/特殊符号（至少选一种）
- 算法：按已选字符类型分别保证至少出现 1 次，剩余位从全字符池随机填充，再做 Fisher-Yates 洗牌打乱顺序
- **六档强度指示器**：综合长度与字符种类数评估（很弱 / 弱 / 一般 / 较强 / 强 / 极强）
- **生成历史**：最近 20 条，仅保存在当前页面内存中，刷新页面即清空，可随时复制
- 新增/编辑凭证表单内同样提供「生成密码」快捷按钮，一键填入密码字段

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

**Web 面板管理（推荐）：**

面板"防火墙"页提供可视化规则管理，无需命令行：

- 状态卡片：运行状态、默认传入策略、规则数量，一键启用/禁用
- 添加规则：端口（支持范围）、协议、动作、来源 IP、备注；常用服务（SSH/Samba/FTP/NFS/WebDAV）一键填表
- 规则表格：动作徽标、IPv6 标记、逐条删除
- 安全保护：通过面板启用防火墙时会自动放行 SSH(22) 和面板(8090)，不会把自己锁在门外

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

### Web 面板监控

NAS 管理面板（端口 8090）提供实时监控页面，无需命令行即可查看系统状态。

#### 核心指标仪表盘

监控页顶部为三个 SVG 环形仪表盘，直观显示关键指标：

| 仪表盘 | 显示内容 | 颜色规则 |
|--------|----------|----------|
| CPU 使用率 | 精确百分比（/proc/stat 采样） | 绿<50% / 黄50-70% / 橙70-90% / 红>90% |
| 内存使用率 | 已用/总量 + Swap | 同上 |
| 磁盘使用率 | /data 分区已用/总量 | 同上 |

CPU 卡片还显示：型号、负载 1/5/15 分钟、核数。

#### 网络流量

实时显示各网卡的上下行速率和累计流量，自动格式化单位（B/s → KB/s → MB/s → GB/s）。

#### 服务状态

显示 7 个核心服务的运行状态（smbd、nmbd、nfs-kernel-server、vsftpd、rclone-webdav、rclone-s3、fail2ban），绿色=运行中，红色=已停止。

#### 详细信息（可折叠）

点击"详细信息"展开更多监控数据：

- **Top 10 内存进程**：PID、用户、CPU%、内存%、RSS、命令行
- **登录用户**：当前登录的用户列表（用户名、终端、来源、时间）
- **分区使用情况**：所有挂载点的磁盘使用表（设备、挂载点、总大小、已用、可用、颜色进度条）
- **系统错误日志**：最近 24 小时的 journalctl 错误日志

#### 高级信息（可折叠）

- **物理磁盘**：lsblk 输出的磁盘拓扑（型号、大小、分区、挂载点）
- **Inode 使用**：df -i 输出的 inode 使用情况
- **LVM 信息**：卷组和逻辑卷状态

#### 告警配置

监控页底部可配置告警渠道，支持 4 种通知方式：

| 渠道 | 配置项 | 说明 |
|------|--------|------|
| 钉钉机器人 | Webhook URL + 加签密钥 | 钉钉群机器人推送 |
| Telegram Bot | Bot Token + Chat ID | Telegram 消息推送 |
| Bark (iOS) | Bark Key + Server | iOS 推送通知 |
| Email (SMTP) | SMTP 服务器/端口/用户名/密码/收件人 | 邮件通知 |

告警阈值可配置：磁盘告警百分比（默认 80%）、内存告警百分比（默认 90%）、负载告警（默认 4）。

告警由 cron 每 5 分钟自动检查，1 小时内不重复发送同一告警。

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

**面板审计日志**：面板自身的全部写操作（含登录成功/失败）自动记录到审计库
（`/opt/nas/data/audit.db`，SQLite WAL 异步写入，默认保留 90 天，
可用 `NAS_DATA_DIR` 改数据目录），在面板「日志」页查询，字段含时间/用户/操作/方法/路径/详情/结果/IP。
失败请求同样入审计（result=failed），可用于追溯误操作与暴力破解。

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
\\192.168.1.100\shared
\\192.168.1.100\<NAS_USER>
```

输入用户名 `<NAS_USER>`，密码 `<NAS_PASS>`。

**方法 2 — 映射网络驱动器：**

1. 打开"此电脑" → 右键 → "映射网络驱动器"
2. 输入路径: `\\192.168.1.100\shared`
3. 勾选"使用其他凭据连接"
4. 输入用户名和密码

**方法 3 — 命令行：**

```cmd
net use Z: \\192.168.1.100\shared /user:<NAS_USER> <NAS_PASS> /persistent:yes
```

### macOS 访问 Samba

1. Finder → 前往 → 连接服务器 (⌘K)
2. 输入: `smb://192.168.1.100/shared`
3. 输入用户名 `<NAS_USER>`，密码 `<NAS_PASS>`

### Linux 访问 Samba

```bash
# 安装客户端
sudo apt-get install cifs-utils

# 挂载
sudo mount -t cifs //192.168.1.100/shared /mnt/nas \
    -o username=<NAS_USER>,password=<NAS_PASS>

# 永久挂载（加入 /etc/fstab）
echo '//192.168.1.100/shared /mnt/nas cifs credentials=/etc/samba/credentials,uid=1000,gid=1000 0 0' | sudo tee -a /etc/fstab
echo 'username=<NAS_USER>' | sudo tee /etc/samba/credentials
echo 'password=<NAS_PASS>' | sudo tee -a /etc/samba/credentials
sudo chmod 600 /etc/samba/credentials
```

### Linux 访问 NFS

```bash
# 安装 NFS 客户端
sudo apt-get install nfs-common

# 挂载
sudo mount -t nfs 192.168.1.100:/data/shared /mnt/nas

# 永久挂载（加入 /etc/fstab）
echo '192.168.1.100:/data/shared /mnt/nas nfs defaults 0 0' | sudo tee -a /etc/fstab
```

### FTP 访问

**命令行：**
```bash
ftp 192.168.1.100
# 用户名: <NAS_USER>
# 密码: <NAS_PASS>
```

**GUI 工具（FileZilla 等）：**
- 主机: `192.168.1.100`
- 端口: `21`
- 用户名: `<NAS_USER>`
- 密码: `<NAS_PASS>`

### WebDAV 访问

**浏览器直接访问：**
```
http://192.168.1.100:8080/
```
输入用户名 `<NAS_USER>`，密码 `<NAS_PASS>`。

**Linux 挂载 WebDAV：**
```bash
sudo apt-get install davfs2
sudo mount -t davfs http://192.168.1.100:8080/ /mnt/webdav
```

**macOS 挂载 WebDAV：**
Finder → 前往 → 连接服务器 → `http://192.168.1.100:8080/`

**Windows 映射 WebDAV：**
```cmd
net use W: http://192.168.1.100:8080/ /user:<NAS_USER> <NAS_PASS>
```

### 面板内置文件管理访问

FileBrowser 已彻底移除，浏览器/移动端文件管理请直接使用面板内置的 Web 文件页（见 7.7 节），登录面板后点击左侧导航「文件」即可浏览、上传、下载、删除、改权限，无需额外端口。

---

## 11.5 存储管理（Web 面板）

**简介：** 存储管理是 NAS 面板的核心功能，采用四层抽象模型（Physical Disk → Storage Pool → Volume → Shared Folder），参考群晖/TrueNAS 设计，屏蔽底层路径，用户只看到"存储池1"等友好名称。

### 访问方式

浏览器打开 `http://<NAS IP>:8090`，登录后点击左侧导航"💽 存储管理"。

### 存储总览

页面顶部显示全局概览：
- 总容量 / 已用 / 可用 / 使用率进度条
- 存储池数 / 系统盘数 / 空闲盘数
- 健康状态（✓ 一切正常 / ⚠️ 有异常）

### 盘位图

存储总览下方显示 NAS 机箱式盘位图：
- 固定 4 个槽位（机器预留 4 盘位）
- 每个槽位显示设备路径（/dev/sdb）+ 容量 + 接口类型
- 四色状态：
  - **深蓝** — 已安装（SMART 正常）
  - **黄色** — 待配置（有空闲盘未加入存储池）
  - **深灰** — 空闲（无盘）
  - **红色** — 故障（SMART 失败）
- 槽位号 1-4 紫色显示在槽位顶部

### 存储池

每个存储池是一张展开的卡片：

- **头部**：存储池名（存储池1）+ RAID 级别标签 + 健康状态 + 容量进度条
- **成员磁盘**：属于该池的物理盘列表（设备路径/容量/接口/型号/温度/SMART）
- **卷（Volume）**：池上的逻辑卷，显示挂载点/文件系统/容量，每个卷下可包含多个共享文件夹
- **共享文件夹**：卷下的用户目录（名称/大小/权限徽标/回收站标记/配额）
- **操作按钮**：扩容（LVM 加盘 / RAID 加盘）/ 重置

### 存储配置向导（目标导向）

检测到空闲磁盘时自动显示。向导分两步：

**第一步：选择目标**（不出现 RAID 字样）

| 目标 | 说明 | 图标 |
|------|------|------|
| 数据安全 | 数据不丢最重要 | 🛡️ |
| 最大容量 | 一点空间都不浪费 | 📦 |
| 更高性能 | 读写速度优先 | ⚡ |
| 平衡 | 容量和容错兼顾 | ⚖️ |

**第二步：推荐方案**

根据选择的目标，展示匹配的方案：

| 磁盘数 | 可用方案 | 推荐方案 |
|--------|---------|---------|
| 1 块 | LVM 单盘 | ★ LVM 单盘（方便后期扩容） |
| 2 块 | LVM 合并 / RAID0 / RAID1 / 独立 | ★ RAID1 |
| 3 块 | + RAID5 | ★ RAID5 |
| 4 块 | + RAID6 | ★ RAID5 |

每个方案卡片显示：安全等级 / 可用容量 / 利用率 / 注意事项。配置过程 SSE 流式推送进度。

配置过程实时显示进度（SSE 流式推送）：
- 清除磁盘签名 → 创建物理卷 → 创建卷组 → 创建逻辑卷 → 格式化 → 挂载 → Samba 共享

### 扩容

LVM 类型的存储空间支持在线扩容：
1. 点击存储空间卡片内的"扩容"按钮
2. 选择空闲磁盘
3. 确认（含风险提示：合并后任一盘故障全丢）
4. 实时进度：清除签名 → pvcreate → vgextend → lvextend → xfs_growfs

### 共享文件夹管理

在存储空间内新建共享文件夹：
- 选择所属存储空间
- 输入文件夹名称
- 选择权限：读写 / 只读 / 不共享
- 可选开启回收站（Samba vfs objects = recycle，SMB 端删除文件进入共享内 `.recycle/<用户>/` 目录，对 SMB 客户端隐藏；旧版 `#recycle` 布局仍兼容显示）

### 其它磁盘

- **系统盘**：灰色标签，显示分区信息（如 nvme0n1p1 /boot/efi、nvme0n1p2 /、nvme0n1p3 swap）
- **待配置盘**：红色标签，点击"配置"按钮跳转到配置向导

### 系统盘共享目录（无数据盘场景）

当机器只有系统盘、没有数据盘时，用户仍可创建共享文件夹：

- 共享目录位于 `/data/system`，数据存储在系统盘上
- 管理界面与存储空间内的共享文件夹一致（权限/回收站）
- 后续添加数据盘后，可迁移到新的存储空间
- 页面位置：其它磁盘区域下方，灰色边框卡片

### 盘位图（无数据盘场景）

- 盘位图始终显示（有系统盘即显示）
- 无数据盘时4个槽位全灰色（空），用户可直观看到还能插4块盘

---

## 11.6 AI 助手

**简介：** 面板内置的自然语言 NAS 助手，用自己的话问"我的 NAS 正常吗""为什么占用了 4G""给 alice 配额加到 3G"，助手会调用面板既有功能查数据、给结论，涉及修改配置的操作会先出一张确认卡，用户手动确认后才真正执行。**默认关闭**，不配置、不开启时对系统零影响（不发起任何网络请求，不占用任何后台资源）。

### 定位

AI 助手不是新增一套独立的操作入口，而是给已有功能包一层自然语言界面——它能看到的信息（系统概览、监控快照、磁盘健康、存储池、用户列表、审计日志）和能做的操作（改配额、建用户、加防火墙规则、重启服务、跑磁盘自检、建共享文件夹）都是面板原有能力的子集，没有任何"助手专属"的后门操作。

### 开关与配置

1. 左侧导航"🤖 AI 助手" → 设置 tab。
2. 选择预设端点：
   - **DeepSeek（云端）** — 填入 API Key，对话内容会发送到云端服务商。
   - **Ollama（本地）** — 填本机或局域网内的 Ollama 服务地址，对话内容不出本地网络。
3. 填写模型名称，点击"测试连通性"，通过后才能开启。
4. 点击"开启"。关闭时不会清空已保存的端点/Key 配置，重新打开即恢复。

> 开关状态只影响对话/工具调用两类端点（403 拒绝未启用访问），配置相关的查看/保存/测连通性接口不受开关限制——否则第一次配置时就会陷入"没开不能配、不配不能开"的死循环。

### 隐私权衡

云端端点（如 DeepSeek）会收到对话原文，以及工具调用过程中查询到的上下文（文件名、用户名、健康摘要等系统信息片段）。涉及敏感信息的场景，建议改用局域网内的 Ollama 本地端点，对话内容不会离开本地网络。面板设置页会常驻这条隐私提示，不依赖用户记住。

无论选择哪种端点，**API Key 只会出现在发往模型服务商的 HTTP 请求头里**，不会被写入对话历史存储、不会出现在审计日志详情里，也不会出现在页面上除掩码（如 `sk-***abcd`）之外的任何地方。

### 确认门语义（写操作为什么要二次确认）

助手能触达的"写操作"（改配额、建用户、加防火墙规则、重启服务、跑磁盘自检、建共享文件夹）**永远不会由模型直接执行**。流程是：

1. 模型判断需要执行某个写操作，助手回复里会出现一张黄色操作卡，写明要做什么。
2. 操作卡弹出确认框，要求手动输入指定的确认词（如"执行"）才能点亮确认按钮——按钮本身也有禁用态，不是单纯界面好看，背后的确认逻辑在点击处理函数里会再检查一次，防止被绕过。
3. 确认后，服务端才会真正调用对应功能的既有执行逻辑（与在各自功能页面点按钮触发的是同一段代码），并写入审计日志。
4. 这张待确认的操作卡 5 分钟内有效，过期或取消都不会执行，且只能被确认一次——同一张卡不可能被执行两次。

**创建用户是特例**：初始密码不会经过模型（模型的工具定义里压根没有"密码"这个参数，给不出来），而是在确认框里点"生成密码"由浏览器本地生成，确认时随请求单独发送给服务端，全程不进入对话上下文、不落入对话历史数据库、不出现在审计日志里。密码只在确认框内显示一次，关闭后不会再显示，请在确认前复制保存。

### 对话历史

每次对话会落库保存（会话列表 + 消息），可在"对话"tab 左侧切换历史会话。历史记录只保存用户问的话和助手最终回复的文字内容，不包含待确认操作卡的临时状态（下一次打开不会残留未处理完的确认框）。

### 任务：让助手替你干活（不只是回答问题）

**什么是任务：** 前面讲的都是"一问一答"——助手查信息、改一次配置就结束了。但有些事（比如"把 NAS 里的重复照片清理掉"）不是一次性操作，而是要先弄清楚"要动哪些文件、动多少"，再真正执行，执行过程还可能要花一段时间、中途可以暂停。这类"有计划、有进度、可以暂停继续"的活，面板里叫"任务"。目前支持的任务类型只有一种：**清理重复照片/视频**。

**对话怎么发起：** 直接在对话里说你想做的事，比如"帮我清理重复照片"。助手不会立刻动文件，而是先去扫描一遍（只读，不改任何东西），找出内容完全相同的照片/视频分成一组一组，每组里留一份、标记其余的准备移走，然后把结果汇总成一张"确认卡"展示给你：大概有几组重复、一共多少个文件、能腾出多少空间。

**确认卡的影响面说的是什么：** 确认卡上的数字是"如果你同意，将会发生什么"的预估——组数（重复了几批）、文件数（会被移动的文件有多少个）、释放空间（这些文件占了多大）。这一步纯粹只是算了一遍，没有任何文件被移动过。只有你点了确认，助手才会真正开始动文件。如果扫描的文件特别多，确认卡可能会提示"扫描未完整"，这只是说还没扫完全部文件就先给你一个部分结果，不影响已经扫到的这部分是准确的。

**任务页的六种状态：** 左侧导航 AI 助手页面里有一个"任务"tab，能看到所有任务卡片，每张卡片有一个状态：

- **待确认** —— 刚生成计划，还没开始动文件，等你在对话里确认启动。
- **运行中** —— 正在真正移动文件，卡片上有进度条（已处理 N / 总数 M）。
- **已暂停** —— 执行过程中被暂停了（你自己点的，或者面板重启导致的，见下文）。
- **已完成** —— 全部执行完，展开"完成报告"能看到实际移动了多少个文件、释放了多少空间、跳过了多少个（比如你中途手动动过某个文件，助手发现它已经不在了就跳过，不会报错中断）。
- **失败** —— 执行过程中出了问题，卡片上会显示错误原因。
- **已取消** —— 被你主动取消，不会继续执行。

**暂停 / 继续 / 取消是什么意思：**

- **暂停** 不是破坏性操作（不会丢失任何进度），所以点一下就直接生效，不需要额外确认。暂停的时机是"当前正在处理的这一个文件处理完之后"，不会把一个文件处理到一半就中断。
- **继续** 会让任务从暂停的地方接着往下走——已经处理过的文件不会重复处理（判重靠任务自己记的进度），所以放心点继续，不会把同一批文件移动两次。因为继续等于"接着动文件"，所以这一步同样需要在对话里确认一次。
- **取消** 会让任务彻底停下来，不再继续处理剩余的文件（已经处理完的那部分不会撤回，比如已经移到回收站的文件还在回收站里，需要的话自己去回收站恢复）。取消也需要确认。

任务页上的暂停/继续/取消按钮和在对话里让助手帮你点，效果是一样的——按钮更直接，对话更适合你本来就在和助手聊这件事的场景。

**回收站联动：删除的文件去哪了、怎么恢复：** 任务"清理"的文件**不会被直接删除**，而是走面板统一的回收站机制——移动到对应共享文件夹下的回收站目录，和你在文件管理页手动删除文件、或者 SMB 客户端删除文件，进的是同一个回收站，同一套恢复方式。也就是说：

1. 打开存储管理页的"回收站"Tab，能看到被任务移走的文件，和手动删除的文件混在一起没有区别。
2. 选中想要的文件点恢复，会回到它原来的位置。如果原位置已经有同名文件了，面板会自动加后缀避免覆盖，不会让你丢数据。
3. 回收站里的内容永远不会被任务本身二次处理或清空——任务只负责"移进去"，"从回收站彻底删除"是你自己在回收站页的独立操作。

**为什么面板重启后任务会变成"已暂停"（这是故意的，不是故障）：** 如果面板所在的进程重启了（升级、断电、手动重启服务等），本来正在执行的任务不会在后台偷偷继续跑，也不会假装什么都没发生——面板重新启动后会把它找到的"重启前还在运行中"的任务统一标记为"已暂停"，并在进度说明里注明"面板重启已暂停，可继续"。

这么设计是出于安全考虑：任务会真正移动你的文件，如果面板重启后没人盯着就自动接着跑，一旦跑错了（比如重启前的扫描结果和重启后的文件现状已经不一致）没人能及时发现、及时停下。改成"重启后一律暂停，等你确认继续"，相当于让你在任务重新动文件之前多一次把关的机会——多一步确认的代价，换来的是不会在无人看管的情况下批量移动文件。确认没问题后，点"继续"就会从暂停的地方接着走，不会重新处理已经处理过的文件。

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
showmount -e 192.168.1.100

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
| rclone S3 服务       | `systemctl is-active rclone-s3`                   | active         |
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
| 6 | `/etc/fail2ban/jail.local`                  | fail2ban   | 入侵防护规则               |
| 7 | `/var/log/vsftpd.log`                       | vsftpd     | FTP 日志文件（需预创建）   |

**部署包文件清单 (`/opt/nas/`)：**

| # | 文件路径                              | 说明                     |
|---|---------------------------------------|--------------------------|
| 1 | `configs/smb.conf`                    | Samba 配置模板           |
| 2 | `configs/exports`                     | NFS 导出配置模板         |
| 3 | `configs/vsftpd.conf`                 | FTP 配置模板             |
| 4 | `configs/jail.local`                  | Fail2ban 配置模板        |
| 5 | `configs/rclone-webdav.service`       | WebDAV 服务单元模板      |
| 6 | `configs/rclone-s3.service`          | rclone S3 服务单元模板   |
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
| 9000         | TCP   | rclone S3   | 入站  | S3 API 端点                |

## 附录 B：Systemd 服务清单

| 服务名              | 类型     | 开机启动 | 说明                          |
|--------------------|---------|---------|------------------------------|
| smbd               | 系统服务 | enabled | Samba 文件共享主进程           |
| nmbd               | 系统服务 | enabled | Samba NetBIOS 名称服务         |
| nfs-kernel-server  | 系统服务 | enabled | NFS 内核服务器                 |
| rpcbind            | 系统服务 | enabled | RPC 端口映射（NFS 依赖）       |
| vsftpd             | 系统服务 | enabled | FTP 服务器                     |
| rclone-webdav      | 自定义   | enabled | WebDAV 服务（rclone serve）    |
| rclone-s3          | 自定义   | enabled | rclone S3 对象存储服务        |
| fail2ban           | 系统服务 | enabled | 入侵检测与自动封禁            |
| ufw                | 系统服务 | enabled | 防火墙                         |
| unattended-upgrades| 系统服务 | enabled | 自动安全更新                   |
| smartmontools      | 系统服务 | enabled | 磁盘 SMART 监控               |

---

> **文档维护说明：** 本文档应随系统版本更新同步修改。每次配置变更后，请更新对应章节
> 并修改文档顶部的版本号和日期。建议将本文档与 `/opt/nas/` 部署包一起纳入 git 版本管理。
