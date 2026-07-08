# NAS 家用存储系统

基于 Debian 13 (trixie) 的轻量级家用 NAS 解决方案。

## 功能

- **Samba** — Windows/Mac/Linux 文件共享（端口 139/445）
- **NFS** — Linux 设备高速访问（端口 2049）
- **FTP** — vsftpd 传统文件传输（端口 21）
- **WebDAV** — rclone serve WebDAV 服务（端口 8080）
- **FileBrowser** — Web 文件管理界面（端口 8081）
- **MinIO** — S3 兼容对象存储（端口 9000/9002）

## 安全

- UFW 防火墙（默认 deny，仅开放必要端口）
- Fail2ban（SSH/FTP 暴力破解防护）
- unattended-upgrades（自动安全更新）
- smartmontools（磁盘健康监控）

## 目录结构

```
nas/
├── configs/            # 服务配置文件
│   ├── smb.conf        # Samba 配置
│   ├── exports         # NFS 导出配置
│   ├── nfs.conf        # NFS 主配置（固定端口）
│   ├── vsftpd.conf     # FTP 配置
│   ├── jail.local      # Fail2ban 规则
│   ├── rclone-webdav.service
│   ├── filebrowser.service
│   └── minio.service
├── scripts/            # 管理脚本
│   ├── setup.sh        # 一键部署（9步）
│   ├── add-user.sh     # 添加用户
│   └── remove-user.sh  # 删除用户
├── docs/               # 文档
│   ├── nas-product-manual.md   # 产品技术手册
│   └── nas-product-manual.pdf
├── .env.example        # 环境变量模板（复制为 .env 填入密码）
├── CHANGELOG.md        # 变更日志
├── OPTIMIZATION_CHECKLIST.md  # 待优化清单
└── README.md           # 本文件
```

## 快速部署

```bash
# 1. 克隆仓库
git clone https://gitee.com/gitdogcat/nas.git ~/soft/nas

# 2. 创建软链接
sudo ln -sfn ~/soft/nas /opt/nas

# 3. 配置密码
cp /opt/nas/.env.example /opt/nas/.env
# 编辑 .env 填入实际密码（至少12位）

# 4. 执行部署（自动检测当前用户作为 NAS 用户）
sudo bash /opt/nas/scripts/setup.sh
```

部署脚本会自动识别执行 sudo 的用户名作为 NAS 管理用户，无需手动指定。

详细部署步骤请参阅 `docs/nas-product-manual.md`

## 恢复环境

```bash
# 清除所有 NAS 配置和数据，恢复到安装前状态
sudo bash /opt/nas/scripts/cleanup.sh

# 清除但保留 /data 数据目录
sudo bash /opt/nas/scripts/cleanup.sh --keep-data
```

## 服务访问

| 服务 | 地址 | 凭证 |
|------|------|------|
| Samba | //NAS_IP/shared | <NAS_USER> / <NAS_PASS> |
| NFS | mount -t nfs NAS_IP:/data/shared /mnt/nas | 按 IP 控制 |
| FTP | ftp://NAS_IP/ | <NAS_USER> / <NAS_PASS> |
| WebDAV | http://NAS_IP:8080/ | <NAS_USER> / <NAS_PASS> |
| FileBrowser | http://NAS_IP:8081/ | <NAS_USER> / <NAS_PASS> |
| MinIO API | http://NAS_IP:9000 | <NAS_USER> / <NAS_PASS> |
| MinIO Web | http://NAS_IP:9002 | <NAS_USER> / <NAS_PASS> |

## 管理脚本

```bash
# 添加用户
sudo /opt/nas/scripts/add-user.sh <用户名> <密码>

# 删除用户
sudo /opt/nas/scripts/remove-user.sh <用户名> [--delete-data]
```

## 系统要求

- Debian 13 (trixie)
- 2 核 CPU / 2 GiB 内存（推荐 4 核 / 8 GiB）
- 32 GB+ 系统盘
- 独立数据盘（推荐）
- 千兆以太网

## 许可证

GNU AGPLv3（MinIO 部分）
