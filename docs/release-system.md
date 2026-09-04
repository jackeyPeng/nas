# Z1 NAS 发版体系

## 架构

```
┌─────────────┐    ┌──────────────┐    ┌──────────────┐
│  git tag     │───▶│ release.sh   │───▶│ Gitee Release│
│ v1.3.0-beta │    │ build+pack   │    │ + R2 上传     │
└─────────────┘    └──────────────┘    └──────┬───────┘
                                              │
                    ┌──────────────┐          │
                    │  install.sh  │◀─────────┘
                    │ curl|bash   │  下载预编译包
                    └──────────────┘
```

## 版本号规范

| 通道 | Tag 格式 | 示例 | 说明 |
|------|---------|------|------|
| **beta** | `v1.4.0-beta.1` | 开发中，测试用 | 每次 beta 发版递增 `-beta.N` |
| **stable** | `v1.4.0` | 验证通过后发布 | beta 验证通过后去掉后缀打 stable tag |

## 发布包内容

```
nas-v1.4.0-beta.1-linux-amd64.tar.gz
├── nas-panel              ← 预编译二进制 (strip后 ~7.5M)
├── frontend/              ← 前端静态文件 (从 go:embed 导出)
│   ├── index.html
│   ├── style.css
│   ├── app.js
│   └── alpinejs.min.js
├── scripts/               ← 部署脚本
│   ├── setup.sh
│   ├── cleanup.sh
│   ├── deploy-nas-panel.sh
│   ├── add-user.sh
│   ├── remove-user.sh
│   └── monitor.sh
├── configs/               ← 服务配置模板
│   ├── smb.conf
│   ├── vsftpd.conf
│   ├── nfs.conf
│   └── nas-panel.service
├── .env.example           ← 配置模板
└── VERSION                ← 版本元数据
```

## 存储位置

| 位置 | 用途 | URL |
|------|------|-----|
| Gitee Release | git 托管，版本历史 | `https://gitee.com/gitdogcat/nas/releases/tag/v1.3.0` |
| R2 (beta) | 快速下载 | `https://get.z1.sale/releases/beta/nas-v1.4.0-beta.1-linux-amd64.tar.gz` |
| R2 (stable) | 快速下载 | `https://get.z1.sale/releases/stable/nas-v1.3.0-linux-amd64.tar.gz` |
| R2 (latest) | install.sh 默认 | `https://get.z1.sale/releases/stable/latest-linux-amd64.tar.gz` |

## install.sh 安装方式

```bash
# 稳定版（默认 — 走预编译包）
curl -fsSL https://get.z1.sale/install.sh | bash

# Beta 版
NAS_CHANNEL=beta curl -fsSL https://get.z1.sale/install.sh | bash

# 指定版本
NAS_VERSION=v1.3.0 curl -fsSL https://get.z1.sale/install.sh | bash

# 强制源码模式
NAS_CHANNEL=source curl -fsSL https://get.z1.sale/install.sh | bash

# 密码预置 + beta
NAS_PASS=*** NAS_CHANNEL=beta curl -fsSL https://get.z1.sale/install.sh | bash
```

install.sh 流程：先尝试下载预编译包 → 可用则直接解压跑 setup.sh → 不可用则回退到 git clone + 源码构建。

## release.sh 脚本

`scripts/release.sh` 负责完整发版流程：

1. **build** — 交叉编译 linux/amd64 + linux/arm64，注入 ldflags 版本号
2. **pack** — 打包 tar.gz（二进制 + 前端 + 脚本 + 配置 + VERSION）
3. **upload** — 上传到 Cloudflare R2（beta/stable 不同目录）
4. **latest** — stable 发版时更新 `latest-linux-{arch}.tar.gz` 指针
5. **Gitee Release** — stable 发版时上传附件到 Gitee Release

用法：
```bash
bash scripts/release.sh beta     # 构建 beta 包，上传 R2
bash scripts/release.sh stable   # 构建 stable 包，上传 R2 + Gitee Release
bash scripts/release.sh build    # 仅构建，不上传
```

## 版本 API

`GET /api/version` 返回当前运行版本：

```json
{
  "version": "v1.3.0",
  "build_time": "2026-08-13T15:00:00Z",
  "git_commit": "abc1234",
  "go_version": "go1.25.0",
  "os": "linux",
  "arch": "amd64"
}
```

版本号通过 Go ldflags 在编译时注入：
```bash
go build -ldflags "\
  -X nas-panel/modules/version.Version=v1.3.0 \
  -X nas-panel/modules/version.BuildTime=2026-08-13T15:00:00Z \
  -X nas-panel/modules/version.GitCommit=abc1234" .
```

## 发版流程

```bash
# Beta 发版
git tag v1.4.0-beta.1
git push origin v1.4.0-beta.1
bash scripts/release.sh beta

# 验证通过后打 stable
git tag v1.4.0
git push origin v1.4.0
bash scripts/release.sh stable
```

## 前端版本显示

在系统设置页底部显示当前版本号，点击可检查更新（后续版本更新功能实现）。

## 发布安全红线（禁止内网 IP / 明文密码）

> 2026-09-04 确立。任何对外发布的产品文件都不得包含本地测试/生产机器的内网 IP 和明文密码。

### 规则

1. **deploy-nas-panel.sh**：目标机器一律通过命令行参数传入（`[user@]host`）。禁止写死 SERVERS 数组、IP→用户映射表、以及 `/home/jacky` 等本机绝对路径（二进制路径用 `$SCRIPT_DIR/../web/nas-panel` 自定位）。
2. **install-services.sh**：账号密码从环境变量 `NAS_USER`/`NAS_PASS` 或 `/opt/nas/.env` 读取，禁止硬编码。
3. **一次性内网脚本**（如 deploy-115.sh）不得进仓库。
4. **文档示例 IP**：统一用 `192.168.1.100` / `192.168.1.0/24` 等通用示例。
5. **发版前检查**：`grep -rnE "10\.216\.|10\.187\.|192\.168\.213\.|nas123456" scripts/ docs/ configs/` 必须为空。

### 历史教训

- 2026-09-04：已发布的 beta.2 release tarball 和 R2 附件里 setup.sh 注释仍残留内网 IP。发布产物（release tarball / R2 附件）也是产品的一部分，只改 git 源码不够，必须重发。

## 文件清单

| 文件 | 作用 |
|------|------|
| `scripts/release.sh` | 发版脚本：build → pack → upload |
| `scripts/install.sh` | 安装器：支持 channel/version/源码模式 |
| `web/modules/version/version.go` | `/api/version` 端点（ldflags 注入版本号） |
| `web/main.go` | 注册 version 模块路由 |
| `DEVELOPMENT.md` | 开发者文档（发版章节已更新） |