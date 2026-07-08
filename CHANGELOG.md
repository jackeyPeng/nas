# NAS 项目变更日志

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
| Samba | //[REDACTED]/shared | jacky | <NAS_PASS> |
| NFS | mount [REDACTED]:/data/shared | - | - |
| FTP | ftp://[REDACTED] | jacky | <NAS_PASS> |
| WebDAV | http://[REDACTED]:8080 | jacky | <NAS_PASS> |
| FileBrowser | http://[REDACTED]:8081 | jacky | <NAS_PASS> |
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
