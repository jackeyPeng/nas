# NAS 项目变更日志

## [2026-07-13] - v1.2.0 Release + 市场数据收集

### 版本发布
- 打 tag v1.2.0，创建 Gitee Release (ID: 744432)
- 旧 tag v1.1.0 已删除
- Release: https://gitee.com/gitdogcat/nas/releases/tag/v1.2.0

### 新增：nas-market-research skill
- research 类别 skill，用于 NAS 市场数据收集
- 10 个品牌搜索查询（群晖/威联通/绿联/极空间/海康/联想/铁威马/华硕/开源/N150迷你主机）
- 10 个数据字段（品牌/型号/盘位/CPU/内存/网口/M.2/价格/亮点/场景）
- 按盘位分组对比 + 价格区间分析 + 竞品对比

### 新增：市场数据 cron 定时任务
- 每周五下午 3:00 自动收集 NAS 市场数据
- 输出到 ~/soft/nasdata/nas-market-YYYY-MM-DD.md
- 加载 nas-market-research skill 执行
- 工具集: web + file

### 首次市场数据报告
- 生成 nas-market-2026-07-13.md (13KB)
- 覆盖 8 个成品品牌 + 3 个开源方案 + 4 个 DIY 主板方案
- 约 30+ 款产品
- 含与我们产品(N150标准版/RK3568经济版)的性价比对比
- 注: 首次因 Firecrawl API 未配置，基于已有数据整理，非实时价格

## [2026-07-12] - 架构决策敲定：x86 N150 路线

### 决策
- **确认 x86 路线**：Intel N150 标准版先行，ARM 经济版后续
- 6 项关键决策全部锁定（CPU/主板/盘位/机箱/屏幕/硬盘）
- 进入 Phase 0 原型验证阶段

### 采购清单
- 新增 `PURCHASE_LIST.md`：完整采购清单
  - 主板 2 块对比（畅网 ¥450-550 / 倍控 ¥598-698）
  - 配套物料 9 大类（内存/SSD/WiFi/电源/散热/线材/机箱/硬盘）
  - 预算汇总：精简方案 ~¥1,050-1,350，完整方案 ~¥1,510-1,855
  - 淘宝搜索关键词一键复制
  - 四阶段下单顺序建议

### Phase 0 行动清单
- [ ] 采购畅网 + 倍控 N150 主板各 1 块
- [ ] 采购配套物料：DDR4+DDR5 内存各 1、NVMe 128G ×2、MT7922 WiFi、电源 ×2
- [ ] 3D 打印「留白」机箱验证结构
- [ ] Debian 13 兼容性测试 + 功耗/温度基准测试

### 文档
- 更新 `HARDWARE_SPEC.md`：决策表全部标记为已定
- 新增 `PURCHASE_LIST.md`：Phase 0 采购清单
- 更新 `README.md`：文档导航加入采购清单

---

## [2026-07-12] - ARM 架构兼容性修复

### 部署脚本
- `scripts/setup.sh`：新增 `detect_arch()` 函数，自动检测 CPU 架构（amd64/arm64/armv7）
- FileBrowser 下载 URL 从硬编码 `linux-amd64` → 动态 `linux-${ARCH}`
- MinIO 下载 URL 从硬编码 → 动态，armv7 自动映射为 minio 使用的 `arm` 标识
- nas-panel 下载 URL 加入架构后缀 `nas-panel-${ARCH}.latest`
- 手动编译提示加入 `GOARCH=$GOARCH` 参数

### Makefile
- 修复 `build-all` 中 ARMv7 交叉编译 bug：`GOARCH=arm/v7` → 正确解析为 `GOARCH=arm GOARM=7`
- 输出文件名修正：`nas-panel-linux-arm` → `nas-panel-linux-armv7`

### 验证结论
- Go Web Panel：直接 `GOARCH=arm64 go build`，无代码修改
- systemd 服务/配置文件：无架构依赖
- Samba/NFS/FTP：Debian ARM APT 源原生支持
- 所有 systemd 服务文件引用 `/usr/local/bin/` 等通用路径，跨架构兼容

---

## [2026-07-12] - 硬件产品定义

### 硬件规格
- 新增 `HARDWARE_SPEC.md`：完整硬件规格推荐书
  - CPU 选型对比：ARM RK3568 vs x86 N150，**推荐 N150 先行**
  - 双版本 BOM 清单：标准版 ¥1063 / 经济版 ¥455
  - 三套机箱外观方案：「留白」极简 / 「透明探索版」赛博 / 「收音机」复古
  - 自研主板架构设计（芯片连接、背板、散热风道）
  - 四阶段开发路径：原型验证 → 工程样机 → 小批量试产 → 量产发布
  - 竞品对比表 + 定价推演（699-799 / 299-399）
- 更新 `README.md`：加入项目文档导航表

### 待决策
- [ ] CPU 路线：确认 x86 N150 先行
- [ ] 主板策略：确认先采购 ODM 方案
- [ ] 机箱风格：三选一或混合策略
- [ ] 首批数量：建议 100-200 台

---

## [2026-07-12] - 开源基础建设（阶段一）

### 许可证
- LICENSE 修正：GPLv3 → AGPLv3（与 README 声明一致）
- 理由：NAS 核心是 Web 服务，AGPLv3 堵住"改代码不公开"的 ASP 漏洞

### 工程化
- 新增 `Makefile`：12 个目标（build, test, lint, fmt, release 等）
- 新增 `.golangci.yml`：17 项 linter 配置，含启用/禁用理由注释
- 新增 `.editorconfig`：统一 Go/HTML/JS/CSS/Shell/Makefile 缩进与换行
- 新增 `.gitee-ci.yml`：Gitee CI 三阶段流水线（lint → test → build）
- 新增 `.github/workflows/ci.yml`：GitHub Actions 镜像

### 测试
- 新增 `web/common/auth_test.go`：JWT 创建/验证/中间件/过期/benchmark
- 新增 `web/common/common_test.go`：JSONResponse、ReadEnvFile、ReadAllEnv、压测
- 新增 `web/common/module_test.go`：Module 接口编译时检查 + 运行时验证
- 新增 `web/common/sudo_test.go`：sudo/exec 命令测试（仅 Linux 编译）
- 测试覆盖 common/ 包核心逻辑，跨平台可运行

### 文档
- 新增 `CONTRIBUTING.md`：外部贡献流程、代码规范、Commit 格式
- 新增 `DEVELOPMENT.md`：团队开发手册（环境搭建、模块模板、测试指南、跨平台注意事项）
- 更新 `README.md`：加入项目愿景（开源+产品化双目标）、文档导航表

---

## [2026-07-10] - 模块化重构 + 配置管理 + 备份恢复

### 重构：后端模块化架构
- 拆分 common/ 共享包 (auth/json/sudo/env/module)
- 引入 encoding/json 替代手写 toJSON
- 6 个现有模块拆分到 modules/ 目录
- main.go 精简为路由注册 + 启动
- 删除旧的 auth.go/handlers.go/services.go/system.go/monitor.go

### 新增：配置管理模块 (config)
- Samba 共享在线添加/删除（表单提交，自动重启 smbd）
- FTP 用户白名单在线增删（自动重启 vsftpd）
- 配置文件在线编辑器（smb.conf/vsftpd.conf/exports/nfs.conf/jail.local/.env）
- 服务开机自启管理（enable/disable）

### 新增：磁盘管理模块 (diskmgmt)
- 分区信息（fdisk -l）
- 创建目录（限 /data/ 下）
- 挂载/卸载分区
- 格式化分区（禁止系统盘，需二次确认，支持 ext4/xfs/btrfs）
- LVM/I/O/SMART 详情

### 新增：系统设置模块 (system)
- 网络配置（IP/路由/DNS）
- 时间与时区
- 主机名在线修改
- SSH 配置查看
- 内核参数（sysctl）
- 系统更新状态
- 开机自启服务列表

### 新增：备份恢复系统
- backup-config.sh: 备份所有 NAS 配置到 /data/backups/
  - 系统配置/服务文件/项目配置/Samba 用户数据库/crontab/状态快照
  - 打包 tar.gz，保留最近 5 个
- restore-config.sh: 从备份恢复配置
  - 交互式选择，7 步恢复流程（停止服务→恢复配置→重启服务）
- setup.sh 升级前自动备份
- cron 每周日凌晨 3 点定期备份
- Web 面板备份恢复模块（创建/列表/恢复/删除）

### 新增：Web 面板备份恢复模块 (backup)
- /api/backup/list: 列出所有备份
- /api/backup/create: 手动创建备份
- /api/backup/restore: 从指定备份恢复
- /api/backup/delete: 删除备份文件
- 前端: 备份列表表格 + 立即备份按钮 + 恢复/删除操作

### 更新：setup.sh sudoers 白名单
- 新增 systemctl enable/disable
- 新增 tee 读写各配置文件
- 新增 fdisk/mount/umount/mkdir/mkfs 磁盘操作
- 新增 journalctl/cat 配置读取
- 新增 backup-config.sh/restore-config.sh 备份恢复
- 新增 rm 备份文件清理

### 更新：README 开源完善
- Web 面板功能表（10 个模块）
- 从源码编译说明
- 告警通知配置表
- 技术栈表
- 扩展开发指南

## [2026-07-09] - Web 管理面板 + 监控告警系统

### 新增：NAS Web 管理面板 (nas-panel)
- Go 单二进制 Web 管理面板，端口 8090，内存占用 2.7MB
- 技术栈: Go + go:embed + Alpine.js + JWT 认证
- 前端浅色主题，大字体高对比度，左侧导航栏
- 二进制多源下载: file.abwen.com/control/nas-panel.latest (主) + GitHub (备)

#### 面板功能模块
- 仪表盘: 主机名/系统/运行时间/CPU/内存/磁盘使用率 + 服务一览
- 服务管理: 8个服务启动/停止/重启 + 日志查看 (journalctl)
- 用户管理: 添加/删除用户 (联动 Samba/系统/htpasswd) + 改密码
- 存储信息: 磁盘使用/目录大小/Samba 配置/NFS 导出/SMART 状态
- 防火墙: UFW 状态/规则查看 + 端口允许/拒绝
- 监控告警: 实时状态 + 网络流量 + 告警配置

#### 监控告警页面
- 当前状态卡片 (每 180 秒自动刷新):
  - 磁盘: 已用/总量 + 进度条 + 百分比
  - 内存: 已用/总量 + 进度条 + 百分比
  - CPU: 负载值 + 核心数
  - 服务: 异常数 + 总数
  - 进程数
- 物理磁盘: lsblk 显示型号/大小/类型/挂载
- Inode 使用: df -i
- LVM 卷管理: pvs/vgs/lvs
- 内存详情: free -h 含 Swap
- CPU 详情: /proc/cpuinfo 型号/频率/缓存/governor
- 内存占用 Top 10 进程: ps aux 按内存排序
- 登录用户: who 显示用户/终端/来源/时间
- 系统错误日志: journalctl -p err 最近 24 小时 10 条
- 网络流量: /proc/net/dev 双采样计算实时上下行速率 + 总流量
- 已配置通知渠道徽章: 钉钉/Telegram/Bark/Email

#### 告警配置 (Web 页面可编辑)
- 钉钉机器人: Webhook URL + 加签密钥
- Telegram Bot: Token + Chat ID
- Bark (iOS 推送): Key + Server (可自建)
- Email (SMTP): 服务器/端口/用户名/密码/收件人
- 告警阈值: 磁盘(80%)/内存(90%)/负载(4) 可自定义
- 配置保存到 .env，页面直接编辑无需 SSH

### 新增：监控告警脚本 (monitor.sh)
- Shell 脚本 + cron 每 5 分钟检查
- 零额外服务、零额外内存
- 监控项: 磁盘空间/服务状态/内存/CPU 负载/SMART 磁盘健康
- 多通道告警: 钉钉/Telegram/Bark/Email (配哪个启用哪个)
- 告警去重: 同一告警 1 小时内不重复发送
- setup.sh 自动配置 cron + sudoers
- cleanup.sh 自动清理

### 新增：setup.sh [10/10] 步骤
- 部署 nas-panel 二进制 (本地/下载/手动编译 三级回退)
- 配置 systemd 服务 (nas-panel.service)
- 配置 sudoers 免密白名单 (pdbedit/systemctl/smartctl/ufw/chpasswd/smbpasswd/htpasswd/journalctl/pvs/vgs/lvs/tee)
- 配置监控 cron (每 5 分钟)
- 创建监控状态目录 /var/lib/nas-monitor

### 修复
- nas-panel 所有系统命令加 sudo 前缀 (pdbedit/smartctl/ufw/systemctl 等)
- toJSON() 支持 map[string]string 类型 (修复用户列表不显示)
- .env.example 完善说明 (表格 + 必填/选填 + 示例)
- setup.sh .env 不存在时显示创建步骤和必填项

### 验证
- 2026-07-09 在 [REDACTED] (用户 dog) 部署验证通过
- 9/9 服务全部 active (含 nas-panel)
- Web 面板 API 全部正常: 登录/仪表盘/服务/用户/存储/防火墙/监控/告警配置
- 监控数据实时采集: 磁盘/内存/CPU/进程/网络流量/登录用户/错误日志
- 告警配置保存到 .env 正常

## [2026-07-08] - 安全清洗 & 用户名通用化 (v1.0.0)

### 安全
- git filter-repo 重写全部历史，清除所有明文密码
  - [REDACTED], [REDACTED], [REDACTED] 等密码从历史中彻底清除
  - Gitee OAuth token 确认从未进入代码库
- 密码改用 .env 文件读取（.env.example 作为模板）
  - setup.sh 从 $SCRIPT_DIR/.env 读取 NAS_PASS
  - 密码长度校验（至少 12 位，FileBrowser 强制要求）
  - .gitignore 已排除 .env
- minio.service 改用 EnvironmentFile=/etc/default/minio 方式
- 文档中密码统一用 <NAS_PASS> 占位符

### 重构
- 用户名从硬编码 jacky 改为自动获取当前用户
  - setup.sh: NAS_USER="${SUDO_USER:-$USER}" 自动检测 sudo 执行者
  - 直接以 root 运行会报错提示
  - 配置文件用 __NAS_USER__ 占位符，setup.sh 部署时 sed 替换
  - configs/smb.conf, vsftpd.userlist, minio.service, rclone-webdav.service 全部改造
- cleanup.sh: 删除 Samba 用户改为动态获取
- remove-user.sh: 保护用户从硬编码 jacky 改为动态获取部署用户

### 新增
- .env.example 环境变量模板

### 验证
- 2026-07-08 在 [REDACTED] 全新系统上 cleanup -> setup 完整验证通过
- 8/8 服务全部 active
- Samba 列出 5 个共享, NFS 挂载成功, FTP 正常, WebDAV HTTP 200
- FileBrowser JWT 登录成功, MinIO Health 200, Console 200
- 用户名自动检测 (jacky)、.env 读取密码、占位符替换全部正常

## [2026-07-02] - WebDAV 认证优化 & 清理脚本完善

### 新增
- cleanup.sh 增加 `/etc/rclone-htpasswd` 文件清理
- cleanup.sh 增加 `apache2-utils` 包卸载（htpasswd 工具）

### 修复
- WebDAV 认证改用 htpasswd 文件方式（Apache 标准 bcrypt 哈希）
  - 原因：rclone obscure 生成的哈希在多次部署验证中出现兼容性问题
  - 方案：使用 `htpasswd -cb` 生成密码文件，rclone 通过 `--htpasswd` 参数读取
  - 影响：setup.sh、configs/rclone-webdav.service 同步更新
- setup.sh 步骤 [6/9] 增加 `apt-get install apache2-utils` 自动安装 htpasswd 工具

### 验证
- cleanup.sh → setup.sh 完整循环测试通过
- WebDAV HTTP 200 认证正常

## [2026-07-02] - 完整部署验证通过

### 新增
- MinIO S3 兼容对象存储服务（端口 9000 API，端口 9002 控制台）
- setup.sh 自动安装 MinIO（多源下载回退机制）
- 防火墙规则：开放 9000/9002 端口

### 修复
- setup.sh 第 238 行语法错误（MinIO service 文件生成时缺少 >> 重定向符）
- 根因：Python 字符串拼接中 `>>` 被解释为 shell 重定向

### 验证结果
所有 8 个服务在 Debian 13 全新系统上部署验证通过：
- ✓ Samba (smbd/nmbd) - 6 个共享目录
- ✓ NFS (nfs-kernel-server) - 5 个导出目录
- ✓ FTP (vsftpd) - 正常响应
- ✓ WebDAV (rclone serve) - 服务运行
- ✓ FileBrowser - Web 界面正常，JWT 认证工作
- ✓ MinIO API - 健康检查返回 200
- ✓ MinIO Console - Web 管理界面正常
- ✓ Fail2ban - sshd/vsftpd 防护已启用

### 访问信息
| 服务 | 地址 | 用户名 | 密码 |
|------|------|--------|------|
| Samba | //[REDACTED]/shared | <NAS_USER> | <NAS_PASS> |
| NFS | mount [REDACTED]:/data/shared | - | - |
| FTP | ftp://[REDACTED] | <NAS_USER> | <NAS_PASS> |
| WebDAV | http://[REDACTED]:8080 | <NAS_USER> | <NAS_PASS> |
| FileBrowser | http://[REDACTED]:8081 | <NAS_USER> | <NAS_PASS> |
| MinIO Console | http://[REDACTED]:9002 | admin | <NAS_PASS> |
| MinIO API | http://[REDACTED]:9000 | - | - |

## [2026-07-02] - NFS 端口固定

### 新增
- 在 `/etc/nfs.conf` 中固定 NFS 相关服务端口：
  - mountd: 20048
  - lockd: 32768
  - statd: 32769
- 防火墙规则：开放 111 (rpcbind), 20048 (mountd), 32768-32769 (lockd/statd)

### 修复
- NFS 从外部无法访问的问题（原因：mountd 使用随机端口）

## [2026-07-02] - 初始化项目

### 新增
- 基础 NAS 服务：Samba、NFS、FTP、WebDAV
- FileBrowser Web 文件管理器
- setup.sh 一键部署脚本（9 步流程）
- add-user.sh / remove-user.sh 用户管理脚本
- 产品技术手册 v1.0
- 完整的安全配置（UFW 防火墙 + Fail2ban）

### 首次部署
- 在 Debian 13 (trixie) 全新系统上完成部署验证
