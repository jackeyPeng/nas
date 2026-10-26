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
│   ├── install.sh
│   ├── add-user.sh
│   ├── remove-user.sh
│   ├── backup-config.sh
│   ├── restore-config.sh
│   └── monitor.sh
├── configs/               ← 服务配置模板
│   ├── smb.conf
│   ├── vsftpd.conf
│   ├── vsftpd.userlist
│   ├── nfs.conf
│   ├── exports
│   ├── jail.local
│   └── nas-panel.service
├── .env.example           ← 配置模板
└── VERSION                ← 版本元数据
```

## 存储位置

| 位置 | 用途 | URL |
|------|------|-----|
| Gitee Release | git 托管，版本历史 | `https://gitee.com/gitdogcat/nas/releases/tag/v1.3.0` |
| GitHub 镜像 | git 镜像仓库 | `https://github.com/jackeyPeng/nas` |
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

**安装身份（S1 最小权限，2026-10-23 起）**：上述命令不再要求整条命令前置 `sudo`——`install.sh`
开头检测 `EUID`，普通用户（如 `fm`）直跑时先 `sudo -v` 缓存一次密码，特权步骤（装包/建软链接/调
`setup.sh`/重启服务）逐条加 `sudo` 前缀；`EUID=0`（如 `sudo bash install.sh`）时特权前缀原样退化
为空，兼容旧用法。仓库落在执行用户的 home 下（如 `/home/fm/soft/nas`），不再强制落 `/root/soft/nas`。
`cleanup.sh` 同款封装。

## sudoers 白名单原则

`nas-panel` 常驻进程以 `NAS_USER`（装机时探测的管理员账号，如 `fm`）运行，不再是 root（S1 最小权限，
2026-10-23 起，`configs/nas-panel.service` 带 `User=__NAS_USER__`/`Group=__NAS_USER__`）。面板需要
的系统级特权操作统一经 `web/common/sudo.go` 的 `SudoExec`/`SudoOutput`/`SudoExecStdin` 走 `sudo`，
受 `/etc/sudoers.d/nas-panel`（`scripts/setup.sh` 生成，`visudo -cf` 校验）白名单约束——白名单只收
面板代码实际调用面用到的精确命令形状，当前 145 条（2026-10-23 sudoers 收紧后的实测值，`awk` 对
`scripts/setup.sh` 的 `NOPASSWD:` 行按 `, ` 分隔计数可复算；收紧前为 115 条）。

**root-equivalence 复审判定标准**：新增/修改白名单条目前，问一句——「这条规则能不能被用来改写
`/etc/shadow` 或 `/etc/sudoers.d/*` 本身，或者等价于拿到无约束 root？」能，就是后门，必须收窄到
具体路径/参数或整条拒绝，不能因为「功能需要」就放行通配。

- **死后门案例（已铲，不得以任何形式加回）**：`/usr/bin/sed -i *`。`sed -i` 对任意文件做原地替换，
  参数通配意味着可以直接 `sudo sed -i 's/root:.*/root::.../' /etc/shadow` 或改写 `sudoers.d` 自身，
  等价无约束 root。该条目在面板 Go 代码仍有 `sed`/`sh -c` 管道调用点时曾短暂存在，beta.26 起面板
  改用纯 Go 行级重写（`updateNasPassLine`/`removeExactLine` 等）替代 shell 管道后，`sed -i *` 条目
  已于 2026-10-23 永久移除（`scripts/setup.sh` 内有拍板注释），不再有任何功能依赖它。
- **判定方法**：对白名单里的通配条目（含 `*` 的参数位），逐条问「`*` 位置能不能填一个指向系统敏感
  文件的路径/用户名/组名，从而达成上面的效果」。能且无法通过缩小 `*` 的匹配范围消除风险的，才允许
  以「功能必需 + 明示残留风险」的方式保留（例如 `apt-get upgrade *`、`mdadm *`、`rclone *`——这些本
  身就是系统更新/磁盘管理/文件同步的高权能功能面，家用 NAS 管理员面板的产品定位决定了它们无法被
  收窄到零特权，残留风险表与缓解措施见 `docs/plans/2026-10-23-least-privilege-s1-plan.md` Task 3）。
  能收窄的（如 `chown -R *` → 限定到 `/data` 树、`openssl ... -out *` 任意路径 → 固化成证书实际
  路径三个常量、`usermod *` 任意组 → 仅允许 `-aG nasusers`），一律收窄，不保留"大概率没人会这么用"
  的侥幸通配。

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

1. **安装/部署脚本**：账号密码从环境变量 `NAS_USER`/`NAS_PASS` 或 `/opt/nas/.env` 读取，禁止硬编码真实密码。
2. **一次性内网脚本**（如 deploy-115.sh）不得进仓库。
3. **文档示例 IP**：统一用 `192.168.1.100` / `192.168.1.0/24` 等通用示例。
4. **发版前检查**：`grep -rnE "10\.216\.|10\.187\.|192\.168\.213\.|nas123456" scripts/ docs/ configs/` 必须为空。
5. **出厂默认密码豁免**：`Nas-Test-2026` 是出厂默认密码（账号 fm，首次登录面板后强制修改），允许出现在代码/脚本中。但它是「出厂初始值」而非任何真实生产机的实际密码——发版前必须确认所有生产机密码都已改掉、不是该默认值。

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