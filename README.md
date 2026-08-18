# NAS 离线安装包

本分支存放 NAS 部署所需的二进制文件和离线安装包。

## 文件说明

| 文件 | 大小 | 说明 |
|------|------|------|
| `filebrowser-v2.31.2-amd64` | 17MB | FileBrowser 二进制 |
| `nas-panel-v1.0.0-amd64` | 11MB | NAS Web 管理面板 |
| `nas-bundle-v1.0.0-amd64.tar.gz` | 13MB | 离线安装包（含以上二进制+所有配置文件） |

## 使用方式

### 方式一：离线包（推荐）

```bash
# 下载离线包到仓库根目录
wget https://github.com/jackeyPeng/nas/raw/releases/nas-bundle-v1.0.0-amd64.tar.gz

# 运行 setup.sh，自动检测并使用
sudo bash scripts/setup.sh
```

### 方式二：单独下载

```bash
wget https://github.com/jackeyPeng/nas/raw/releases/filebrowser-v2.31.2-amd64
wget https://github.com/jackeyPeng/nas/raw/releases/nas-panel-v1.0.0-amd64
sudo cp filebrowser-v2.31.2-amd64 /usr/local/bin/filebrowser
sudo cp nas-panel-v1.0.0-amd64 /usr/local/bin/nas-panel
sudo chmod +x /usr/local/bin/{filebrowser,nas-panel}
```

## 版本对应关系

| Bundle 版本 | FileBrowser | nas-panel | 说明 |
|-------------|-------------|-----------|------|
| v1.0.0 | v2.31.2 | v1.0.0 | 初始版本 |
