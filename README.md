# NAS 离线安装包

本分支存放 NAS 部署所需的离线安装包，包含二进制文件和配置文件。

## 包结构

```
nas-bundle-{version}-{arch}.tar.gz
├── bin/
│   ├── filebrowser    # FileBrowser 二进制
│   └── nas-panel      # NAS Web 管理面板二进制
├── configs/           # 所有服务配置文件
│   ├── smb.conf
│   ├── exports
│   ├── nfs.conf
│   ├── vsftpd.conf
│   ├── vsftpd.userlist
│   ├── jail.local
│   ├── rclone-webdav.service
│   ├── filebrowser.service
│   ├── rclone-s3.service
│   ├── nas-panel.service
│   ├── minio.service
│   └── alert.conf.example
├── checksums.txt      # SHA256 校验
└── VERSION
```

## 使用方式

### 方式一：自动检测（推荐）

将离线包放在仓库根目录或 `/tmp` 下，setup.sh 会自动检测并使用：

```bash
# 下载离线包到仓库根目录
wget https://github.com/jackeyPeng/nas/raw/releases/nas-bundle-v1.0.0-amd64.tar.gz

# 运行 setup.sh，自动检测离线包
sudo bash scripts/setup.sh
```

### 方式二：手动安装

```bash
tar xzf nas-bundle-v1.0.0-amd64.tar.gz
sudo cp bin/filebrowser /usr/local/bin/
sudo cp bin/nas-panel /usr/local/bin/
sudo chmod +x /usr/local/bin/{filebrowser,nas-panel}
```

## 版本说明

- `v1.0.0` — 初始版本，FileBrowser v2.31.2
- 未来版本号与 master 分支的 tag 对齐

## 注意事项

- rclone 二进制不包含在离线包中（体积较大，~55MB），由 setup.sh 单独下载
- 离线包包含所有配置文件模板，但 `.env` 中的密码仍需用户自行填写
