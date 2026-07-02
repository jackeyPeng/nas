# NAS 项目变更日志

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
| Samba | //192.168.213.85/shared | jacky | [REDACTED] |
| NFS | mount 192.168.213.85:/data/shared | - | - |
| FTP | ftp://192.168.213.85 | jacky | [REDACTED] |
| WebDAV | http://192.168.213.85:8080 | jacky | [REDACTED] |
| FileBrowser | http://192.168.213.85:8081 | jacky | [REDACTED] |
| MinIO Console | http://192.168.213.85:9002 | admin | [REDACTED] |
| MinIO API | http://192.168.213.85:9000 | - | - |

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
