# NAS 家用存储系统

基于 Debian 13 (trixie) 的轻量级家用 NAS 解决方案。

## 功能

- **Samba** — Windows/Mac/Linux 文件共享
- **NFS** — Linux 设备高速访问
- **FTP** — vsftpd 传统文件传输
- **WebDAV** — rclone serve WebDAV 服务
- **FileBrowser** — Web 文件管理界面

## 目录结构

```
nas/
├── configs/          # 服务配置文件
│   ├── smb.conf      # Samba 配置
│   ├── exports       # NFS 导出配置
│   ├── vsftpd.conf   # FTP 配置
│   ├── nfs.conf      # NFS 主配置
│   ├── jail.local    # Fail2ban 规则
│   └── *.service     # systemd 服务单元
├── scripts/          # 管理脚本
│   ├── setup.sh      # 一键部署
│   ├── add-user.sh   # 添加用户
│   └── remove-user.sh # 删除用户
└── docs/             # 文档
    └── nas-product-manual.md # 产品技术手册
```

## 快速部署

```bash
sudo ./scripts/setup.sh
```

详细部署步骤请参阅 `docs/nas-product-manual.md`
