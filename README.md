# NAS 家用存储系统

[![License](https://img.shields.io/badge/license-AGPLv3-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8.svg)](https://go.dev/)

基于 Debian 13 (trixie) 的轻量级家用/小型办公 NAS 解决方案。全部原生 systemd 服务（不使用 Docker），配合 Go 单二进制 Web 管理面板，追求稳定、高性能、易维护。

## 项目愿景

本项目有两条并行主线：

| 方向 | 目标 | 说明 |
|------|------|------|
| 🔓 **开源软件** | 社区驱动的 NAS 操作系统 | 代码完全开放 (AGPLv3)，欢迎贡献 |
| 📦 **软硬一体** | 开箱即用的 NAS 产品 | 预装系统的硬件方案，降低使用门槛 |

> 两条线相互促进：开源社区贡献代码 → 产品更稳定 → 产品收入反哺开源开发。

### 📋 项目文档

| 文档 | 内容 |
|------|------|
| [DEVELOPMENT.md](DEVELOPMENT.md) | 团队开发手册：环境搭建、模块模板、跨平台注意事项 |
| [CONTRIBUTING.md](CONTRIBUTING.md) | 外部贡献指南：代码规范、PR 流程、Commit 格式 |
| [HARDWARE_SPEC.md](HARDWARE_SPEC.md) | 硬件规格推荐书：CPU 选型、BOM 清单、机箱设计 |
| [PURCHASE_LIST.md](PURCHASE_LIST.md) | 🛒 Phase 0 采购清单：主板、物料、淘宝关键词、预算 |

## 功能

- **Samba** — Windows/Mac/Linux 文件共享（端口 139/445）
- **NFS** — Linux 设备高速访问（端口 2049）
- **FTP** — vsftpd 传统文件传输（端口 21）
- **WebDAV** — rclone serve WebDAV 服务（端口 8080）
- **FileBrowser** — Web 文件管理界面（端口 8081）
- **S3 API (rclone)** — S3 兼容对象存储，bucket 自动映射目录（端口 9000）
- **NAS Web Panel** — Web 管理面板（端口 8090）

### Web 管理面板功能

| 模块 | 功能 |
|------|------|
| 仪表盘 | 系统信息、CPU/内存/磁盘使用率、服务一览 |
| 服务管理 | 8 个服务启动/停止/重启、查看日志 |
| 用户管理 | 添加/删除用户、修改密码（联动 Samba/系统/htpasswd） |
| 存储信息 | 磁盘使用、目录大小、Samba 配置、NFS 导出、SMART 状态 |
| 防火墙 | UFW 状态查看、端口允许/拒绝 |
| 监控告警 | 实时状态、网络流量、Top 进程、错误日志、告警配置（4 通道） |
| 配置管理 | Samba 共享增删、FTP 白名单、配置文件在线编辑、服务自启开关 |
| 存储管理 | 向导式配置：单盘/合并(LVM)/RAID1/独立模式，实时进度，一键重置 |
| 系统设置 | 网络配置、时间时区、主机名修改、SSH 配置、内核参数、系统更新 |
| 备份恢复 | 手动/自动备份、备份列表、一键恢复、删除备份 |

## 安全

- UFW 防火墙（默认 deny，仅开放必要端口）
- Fail2ban（SSH/FTP 暴力破解防护）
- unattended-upgrades（自动安全更新）
- smartmontools（磁盘健康监控）
- xfsprogs（xfs 文件系统工具）
- mdadm（RAID 管理）
- lvm2（LVM 逻辑卷管理）
- 监控告警（monitor.sh + cron 每5分钟检查，多通道通知）
- 密码通过 .env 文件管理，不硬编码在脚本中
- sudoers 精确限定 nas-panel 可执行的 root 命令白名单

## 目录结构

```
nas/
├── configs/            # 服务配置文件（__NAS_USER__ 占位符）
│   ├── smb.conf        # Samba 配置
│   ├── exports         # NFS 导出配置
│   ├── nfs.conf        # NFS 主配置（固定端口）
│   ├── vsftpd.conf     # FTP 配置
│   ├── vsftpd.userlist # FTP 用户白名单
│   ├── jail.local      # Fail2ban 规则
│   ├── rclone-webdav.service
│   ├── filebrowser.service
│   ├── nas-panel.service
│   └── rclone-s3.service
├── scripts/            # 管理脚本
│   ├── setup.sh        # 一键部署（10步）
│   ├── cleanup.sh      # 清理恢复（--keep-data 保留数据）
│   ├── add-user.sh     # 添加用户
│   ├── remove-user.sh  # 删除用户
│   ├── monitor.sh      # 监控告警（cron 每5分钟）
│   ├── backup-config.sh   # 配置备份（手动/升级前/定期）
│   └── restore-config.sh # 配置恢复
├── web/                 # Web 管理面板源码（Go）
│   ├── main.go          # 入口：路由注册 + 启动
│   ├── common/          # 共享工具包
│   │   ├── common.go    # JSON 响应、.env 读写
│   │   ├── auth.go      # JWT 认证
│   │   ├── sudo.go      # sudo exec 封装
│   │   └── module.go    # Module 接口
│   ├── modules/         # 功能模块（每个模块独立）
│   │   ├── dashboard/   # 仪表盘
│   │   ├── services/    # 服务管理
│   │   ├── users/       # 用户管理
│   │   ├── storage/     # 存储信息
│   │   ├── firewall/    # 防火墙
│   │   ├── monitor/     # 监控告警
│   │   ├── config/      # 配置管理
│   │   ├── diskmgmt/    # 磁盘管理
│   │   ├── system/      # 系统设置
│   │   └── backup/      # 备份恢复
│   ├── go.mod
│   └── frontend/        # 前端 (Alpine.js + 原生 CSS)
│       ├── index.html
│       ├── app.js
│       └── style.css
├── docs/               # 文档
│   ├── nas-product-manual.md   # 产品技术手册
│   └── nas-product-manual.pdf
├── .env.example        # 环境变量模板（复制为 .env 填入密码）
├── CHANGELOG.md        # 变更日志
├── TODO.md             # 优化路线图
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

## 从源码编译 nas-panel

Web 管理面板是 Go 单二进制程序。setup.sh 会优先从下载源获取二进制，如果需要自行编译：

```bash
cd ~/soft/nas/web

# 设置 Go 代理（国内用户推荐）
export GOPROXY=https://goproxy.cn,direct

# 编译
go build -o nas-panel .

# 去除调试信息（减小体积）
strip nas-panel

# 放到正确位置，setup.sh 会自动检测并使用
cp nas-panel ~/soft/nas/web/nas-panel
```

编译好的二进制放在 `web/nas-panel` 后，setup.sh 会优先使用本地文件，不再从网络下载。

## 恢复环境

```bash
# 清除所有 NAS 配置和数据，恢复到安装前状态
sudo bash /opt/nas/scripts/cleanup.sh

# 清除但保留 /data 数据目录
sudo bash /opt/nas/scripts/cleanup.sh --keep-data
```

## 配置备份与恢复

NAS 支持自动和手动配置备份，确保升级或修改配置后可以快速回滚。

### 自动备份

- **升级前备份**：运行 `setup.sh` 时自动检测已有配置并备份
- **定期备份**：cron 每周日凌晨 3 点自动执行
- **保留策略**：保留最近 5 个备份，自动清理旧的

### 手动操作

```bash
# 创建备份
sudo bash /opt/nas/scripts/backup-config.sh

# 从备份恢复（交互式选择）
sudo bash /opt/nas/scripts/restore-config.sh

# 从指定备份恢复
sudo bash /opt/nas/scripts/restore-config.sh /data/backups/config-20260710-163352.tar.gz
```

### Web 面板操作

在 Web 面板"备份恢复"页面可以：
- 查看所有备份列表（时间、大小、文件名）
- 一键创建备份
- 从指定备份恢复配置
- 删除不需要的备份

### 备份内容

| 类别 | 内容 |
|------|------|
| 系统配置 | smb.conf / exports / nfs.conf / vsftpd.conf / jail.local / rclone-htpasswd / rclone s3-env / sudoers |
| 服务文件 | rclone-webdav / filebrowser / rclone-s3 / nas-panel .service |
| 项目配置 | .env / configs/ 目录 |
| 用户数据 | Samba passdb.tdb / FTP 白名单 / 系统用户列表 |
| 定时任务 | crontab（监控告警 + 定期备份） |
| 状态快照 | 磁盘使用 / 服务状态 / 挂载点 / UFW 规则 |

## 服务访问

| 服务 | 地址 | 凭证 |
|------|------|------|
| Samba | //NAS_IP/shared | <NAS_USER> / <NAS_PASS> |
| NFS | mount -t nfs NAS_IP:/data/shared /mnt/nas | 按 IP 控制 |
| FTP | ftp://NAS_IP/ | <NAS_USER> / <NAS_PASS> |
| WebDAV | http://NAS_IP:8080/ | <NAS_USER> / <NAS_PASS> |
| FileBrowser | http://NAS_IP:8081/ | <NAS_USER> / <NAS_PASS> |
| S3 API | http://NAS_IP:9000 | <NAS_USER> / <NAS_PASS> |
| Web 面板 | http://NAS_IP:8090 | <NAS_USER> / <NAS_PASS> |

## 告警通知配置

在 .env 文件中配置告警通道，配哪个启用哪个，支持多通道同时通知：

| 通道 | 环境变量 | 获取方式 |
|------|----------|----------|
| 钉钉机器人 | ALERT_DINGTALK_WEBHOOK | 钉钉群 → 群设置 → 智能群助手 → 自定义机器人 |
| Telegram | ALERT_TELEGRAM_TOKEN | @BotFather → /newbot |
| Bark (iOS) | ALERT_BARK_KEY | App Store 下载 Bark → 复制 key |
| Email | ALERT_SMTP_HOST | 任意 SMTP 服务（QQ/Gmail等） |

告警阈值可在 .env 或 Web 面板"监控告警"页面自定义。

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

## 技术栈

| 层 | 技术 | 说明 |
|----|------|------|
| NAS 服务 | Samba / NFS / vsftpd / rclone / FileBrowser | 全部原生 systemd 服务，不使用 Docker |
| Web 面板后端 | Go 1.25 + go:embed | 单二进制，内嵌前端，内存占用 <3MB |
| Web 面板前端 | Alpine.js + 原生 CSS | 无构建工具，浅色主题 |
| 认证 | JWT | 24 小时有效期 |
| 监控告警 | Shell + cron | 零额外服务，每5分钟检查 |
| 部署 | Shell 脚本 | 10 步一键部署，自动检测用户 |
| 密码管理 | .env 文件 | .gitignore 排除，不提交到仓库 |

## 开发与贡献

欢迎贡献代码、文档、测试用例！

| 文档 | 面向人群 | 内容 |
|------|---------|------|
| [CONTRIBUTING.md](CONTRIBUTING.md) | 外部贡献者 | 贡献流程、代码规范、PR 指南 |
| [DEVELOPMENT.md](DEVELOPMENT.md) | 团队成员 | 环境搭建、项目结构、日常开发、测试、注意事项 |

### 快速开始

```bash
# 编译
make build

# 本地运行
make dev

# 交叉编译全部平台
make build-all
```

### 模块架构

Web 面板采用模块化架构，添加新功能只需：

1. 在 `web/modules/` 下新建目录
2. 实现 `RegisterRoutes(mux *http.ServeMux)` 函数
3. 在 `main.go` 添加 import 和一行路由注册
4. 编译部署

每个模块独立，互不依赖，共用 `common/` 包的认证、JSON、sudo 等工具函数。

## 许可证

- **本项目代码**: GNU AGPLv3 (见 [LICENSE](LICENSE))
- **第三方组件**: 见 [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md)
  - rclone (MIT) — WebDAV + S3 服务
  - FileBrowser (Apache 2.0) — Web 文件管理
  - Alpine.js (MIT) — 前端响应式框架
  - Go JWT (MIT) — 认证
  - Samba/NFS/vsftpd/Fail2ban/UFW 等 — 各自开源许可证

- 你可以自由使用、修改、分发本软件
- 如果你修改了代码并通过网络提供服务，必须同时公开修改后的源代码
- 详见 [LICENSE](LICENSE) 文件
