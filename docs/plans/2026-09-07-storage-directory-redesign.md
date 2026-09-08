# NAS 存储与共享目录架构重构方案

> 日期: 2026-09-07
> 状态: 已确认待实施
> 范围: 目录结构收敛 + 协议配置 + 用户目录模型 + FileBrowser 处置

---

## 1. 问题现状

当前 /data 下存在「三套并行、互相漂移」的目录体系：

1. **种子目录**（setup.sh 安装时直接建在 /data 下）：shared / media / documents / downloads / photos / backups / system / private —— 通过 SMB 非托管段 + NFS exports 暴露，**不进 folders.db**，面板看不到也管不到。
2. **面板托管目录**（存储卷 /data/nas1 下）：team / photos —— 进 folders.db，走 Z1 托管段，是唯一真正按用户粒度的。
3. **用户私有目录**：/data/private/$USER —— SMB 私有 share + FTP chroot。

问题：
- 目录太多太乱（/data 下 8 个种子目录 + /data/nas1 下托管目录 + private 平铺）。
- 不同协议服务不同目录（NFS 导出 /data/photos，SMB 托管 /data/nas1/photos，是两个不同目录）。
- 双源真相：写侧走 folders.db，读侧（列表页/总览）还在扫文件系统 + smb.conf。
- 维护复杂：6 个脚本各自引用默认目录。

## 2. 目标模型

单存储池 + 单根 + 用户主目录为唯一粒度。

- 整个系统一个存储池，固定挂载 /data/nas1。
- 用户主目录 /data/nas1/$USER（创建用户时自动建 + 默认子目录）。
- 公共共享区 /data/nas1/public（存储池配置完成后自动创建，所有人可读可写）。

## 3. 目录结构（最终）

```
/data/nas1                          # 唯一存储池根
├── public                          # 公共区：所有人读写，全协议
│   ├── media/                      # 影音
│   ├── photos/                     # 照片
│   ├── documents/                  # 文档
│   └── downloads/                  # 下载
├── fm/                             # 用户 home（每个用户一个）
│   ├── media/
│   ├── photos/
│   ├── documents/
│   └── downloads/
├── alice/  (同上)
└── bob/    (同上)
```

## 4. 协议映射

| 协议/服务 | 服务根 | 用户隔离方式 |
|-----------|--------|-------------|
| SMB | [public] → /data/nas1/public + [用户名] → /data/nas1/$USER | 按用户（valid users + write list + force user=$USER） |
| FTP | 根 = /data/nas1（chroot_local_user=YES） | 文件系统权限（home 0700，public 2775 公共组） |
| NFS | 仅导出 /data/nas1/public | 网段级，无按用户 |
| WebDAV | rclone serve webdav /data/nas1/public | 全局凭据 |
| S3 | rclone serve s3 /data/nas1/public | 全局凭据 |
| FileBrowser | root = /data/nas1/public | 单一全局账号（过渡期） |

## 5. 关键决策记录

1. 删除全部种子目录（shared/media/documents/downloads/photos/backups/system/private）。
2. 单存储池固定挂载 /data/nas1。
3. 用户 home = /data/nas1/$USER，创建用户时自动建 + 默认子目录（media/photos/documents/downloads）。
4. public = /data/nas1/public，所有人可读可写，无按用户控制，全协议暴露。
5. FTP 选「方案 A」：根 = /data/nas1，隔离靠文件系统权限（home 0700、public 2775 公共组 nasusers）。
6. FileBrowser：本期收口到 public；第二期面板自写文件管理模块（安全边界参考 FileBrowser 源码，不 fork），做完后下线 FileBrowser。

## 6. 迁移清单（在测试机执行）

删除（当前内容已核实基本为空）：
- /data/shared /data/media /data/documents /data/downloads /data/photos /data/system（均空或近空）

移动：
- /data/private/{fm,alice,bob,charlie} → /data/nas1/{fm,alice,bob,charlie}（数据迁移）
- /data/nas1/team、/data/nas1/photos → 迁到 /data/nas1/public/ 下（team/photos 是公共共享）

处理：
- /data/backups → 备份脚本目标改为 /opt/nas/backups（含现有 2 个 tar.gz 迁移）
- 重建 folders.db：public（samba+nfs）+ 各用户 home（samba）

## 7. 脚本改动

- `setup.sh`：删种子目录创建（L158-163），改为创建 /data/nas1/public + nasusers 组；NFS exports 只导出 public；WebDAV/S3/FileBrowser root 从 /data 改 /data/nas1/public；FTP local_root 改 /data/nas1。
- `add-user.sh`：建 /data/nas1/$USER + 默认子目录，加入 nasusers 组，SMB share 指向 home，FTP userlist 加入。
- `backup-config.sh`：备份目标 /data/backups → /opt/nas/backups。
- `install.sh` / `restore-config.sh` / `upgrade.sh` / `cleanup.sh` / `install-services.sh`：同步引用路径。
- `sudoers`：/bin/mkdir -p /data/* 等规则已覆盖，补 /opt/nas/backups 相关。

## 8. 面板代码改动

- `config_sync.go`：FolderMeta 增加 force_user（home 专属）；GenerateSambaConfig 按文件夹 force_user（home=$USER，public=nasUser）；GenerateNFSConfig 只导出 nfs_export=1（仅 public）。
- `users/create.go`：创建用户时建 home + 默认子目录（不再 /data/private），写 folders.db（home 条目），SMB/FTP 开关逻辑不变。
- `diskmgmt/folders.go`、`overview.go`：读侧迁到 GetAllFolderMeta（消除双源真相），SharedFolder 增加 write_users。
- `services.go`：FileBrowser 安装/修复的 --root 改 /data/nas1/public。
- `version.go` / `system_registry.go`：FileBrowser 描述标注「过渡期 · 将被面板文件管理取代」。

## 9. FileBrowser 处置

- 本期：root 收口到 /data/nas1/public，单一账号，作为「网页文件管理」过渡入口。
- 第二期：面板自写文件管理模块（列目录/上传/下载/建删改/改名/移动 → 预览/视频流/分享），安全边界参考 FileBrowser 源码（scope/软链接/路径校验），前端用 Alpine 自绘。
- 完成后：下线 FileBrowser。

## 10. 分期与测试计划

- 第一期（本次）：目录收敛 + 协议配置 + 迁移 + FileBrowser 收口。
- 第二期（后续）：面板内置文件管理模块，下线 FileBrowser。

测试（在测试机多轮验证）：
1. SMB：public 全用户读写；各 home 仅本人（+授权者）可达；跨目录软链接行为回归。
2. FTP：登录落在 /data/nas1；本人 home 可读写、他人 home 0700 隔离；public 公共组可写。
3. NFS：仅 public 可挂载，home 不可达。
4. WebDAV / S3：仅 public 暴露。
5. FileBrowser：仅 public 可见。
6. 新建/删除用户：home + 默认子目录 + SMB share + FTP userlist 全链路。
7. 配置备份/恢复：目标 /opt/nas/backups 正常。
8. 一键安装（全新 Debian 13）：9 服务 + 46 项注册表全过。
