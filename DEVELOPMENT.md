# 团队开发手册

> 本文档写给 NAS 项目的所有开发者。无论你在哪里写代码（Windows/macOS/Linux），
> 都能找到对应的开发环境搭建方式和注意事项。

## 目录

- [项目概述](#项目概述)
- [开发环境搭建](#开发环境搭建)
- [项目结构](#项目结构)
- [日常开发流程](#日常开发流程)
- [测试指南](#测试指南)
- [提交规范](#提交规范)
- [注意事项](#注意事项)

## 项目概述

NAS 家用存储系统由两部分组成：

| 层 | 内容 | 语言 |
|----|------|------|
| **Web 管理面板** | Go 单二进制 + Alpine.js 前端 | Go / HTML / JS |
| **部署 & 运维脚本** | systemd 服务配置 + Shell 脚本 | Shell |

### 开发平台差异

| 任务 | Windows | macOS | Linux (Debian) |
|------|---------|-------|----------------|
| 写 Go 代码 | ✅ | ✅ | ✅ |
| 运行单元测试 | ✅ (纯逻辑部分) | ✅ | ✅ |
| 运行 golangci-lint | ✅ | ✅ | ✅ |
| 编译 nas-panel | ✅ 交叉编译 | ✅ | ✅ |
| 运行 Web 面板 | ⚠️ 无法访问系统服务 | ⚠️ 同左 | ✅ |
| 测试 sudo 功能 | ❌ 无 sudo 命令 | ⚠️ | ✅ |
| 测试部署脚本 | ❌ | ❌ | ✅ |
| 完整集成测试 | ❌ | ❌ | ✅ |

> **结论**：日常开发可以在任意平台进行，但最终必须在 Debian 13 上做集成验证。

## 开发环境搭建

### 第一步：安装 Go

```bash
# 要求 Go 1.25+
# 下载地址：https://go.dev/dl/
go version  # 确认版本
```

### 第二步：克隆仓库

```bash
git clone https://gitee.com/gitdogcat/nas.git
cd nas
```

### 第三步：安装开发工具（可选但推荐）

```bash
# golangci-lint（代码检查）
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# goimports（import 自动排序）
go install golang.org/x/tools/cmd/goimports@latest
```

### 第四步：验证环境

```bash
# 下载依赖
cd web && go mod download

# 运行测试（仅验证环境可用）
go test ./common/ -v

# 编译检查
go build -o nas-panel .
```

如果以上都通过，环境就搭好了。

## 项目结构

```
nas/
├── Makefile              # 统一构建入口（详见下方）
├── .golangci.yml         # 代码检查规则
├── .editorconfig         # 编辑器配置（自动生效）
├── .gitee-ci.yml         # Gitee CI 配置
├── .github/workflows/    # GitHub Actions 镜像
├── CONTRIBUTING.md       # 外部贡献指南
├── DEVELOPMENT.md        # 本文件 — 内部开发手册
│
├── web/                  # === Web 管理面板 ===
│   ├── main.go           # 入口：路由注册 + 服务启动
│   ├── go.mod / go.sum   # Go 依赖
│   ├── common/           # 共享工具包
│   │   ├── auth.go       #   JWT 认证（Init/Create/Verify/Middleware）
│   │   ├── common.go     #   JSON响应、.env读写
│   │   ├── module.go     #   Module接口、日志中间件
│   │   └── sudo.go       #   sudo命令封装（⚠️ Linux专用）
│   ├── modules/          # 功能模块（每个模块独立）
│   │   ├── dashboard/    #   仪表盘
│   │   ├── services/     #   服务管理（systemctl）
│   │   ├── users/        #   用户管理（useradd/smbpasswd）
│   │   ├── storage/      #   存储信息（df/smartctl）
│   │   ├── firewall/     #   防火墙（ufw）
│   │   ├── monitor/      #   监控告警
│   │   ├── config/       #   配置管理（smb.conf等）
│   │   ├── diskmgmt/     #   磁盘管理（分区/格式化/LVM）
│   │   ├── system/       #   系统设置（网络/时间/hostname）
│   │   └── backup/       #   备份恢复
│   └── frontend/         # 前端 (Alpine.js SPA)
│       ├── index.html    # 单页面入口（~750行）
│       ├── app.js        # 前端逻辑（~580行）
│       └── style.css     # 样式（~170行）
│
├── scripts/              # === Shell 运维脚本 ===
│   ├── setup.sh          #   一键部署（10步，幂等）
│   ├── cleanup.sh        #   清理恢复
│   ├── add-user.sh       #   添加用户
│   ├── remove-user.sh    #   删除用户
│   ├── monitor.sh        #   监控告警（cron每5分钟）
│   ├── backup-config.sh  #   配置备份
│   └── restore-config.sh #   配置恢复
│
├── configs/              # === 服务配置文件模板 ===
│   ├── smb.conf          # Samba
│   ├── exports           # NFS
│   ├── nfs.conf          # NFS 主配置
│   ├── vsftpd.conf       # FTP
│   ├── jail.local        # Fail2ban
│   ├── *.service         # systemd 服务文件（4个）
│   └── alert.conf.example
│
└── docs/                 # === 外部文档 ===
    ├── nas-product-manual.md
    └── nas-product-manual.pdf
```

## 日常开发流程

### 添加新功能

```bash
# 1. 创建分支
git checkout -b feature/my-new-feature

# 2. 编写代码（参考现有模块结构）
#    - 在 web/modules/ 下新建目录
#    - 实现 RegisterRoutes(mux *http.ServeMux) 函数
#    - 在 web/main.go 注册模块

# 3. 格式化 + 检查
make fmt
make lint

# 4. 运行测试
make test

# 5. 编译验证
make build

# 6. 提交
git add .
git commit -m "feat: 功能描述"
git push origin feature/my-new-feature
```

### Makefile 常用命令

```bash
make help        # 查看所有可用命令
make build       # 编译（当前平台）
make build-all   # 交叉编译（linux/amd64, arm64, armv7）
make test        # 运行所有测试
make lint        # 代码检查
make fmt         # 格式化代码
make dev         # 本地开发运行
make clean       # 清理构建产物
make release     # 构建发布版本（清理→编译→校验和）
```

### 模块开发模板

```go
// web/modules/myfeature/myfeature.go
package myfeature

import (
    "net/http"
    "nas-panel/common"
)

func RegisterRoutes(mux *http.ServeMux) {
    mux.HandleFunc("/api/myfeature", common.AuthMiddleware(handleMyFeature))
}

func handleMyFeature(w http.ResponseWriter, r *http.Request) {
    common.JSONResponse(w, map[string]string{"status": "ok"})
}
```

然后在 `web/main.go` 的 `main()` 中添加：
```go
import "nas-panel/modules/myfeature"
// ...
myfeature.RegisterRoutes(mux)
```

---

### 分支命名规范

```
<type>/<简短描述>
```

| 前缀 | 用途 | 从哪分支切 | 合到哪 |
|------|------|-----------|-------|
| `feature/` | 新功能开发 | `develop` | `develop` |
| `fix/` | Bug 修复 | `develop` | `develop` |
| `hotfix/` | 紧急生产修复 | `main` | `main` + `develop` |
| `refactor/` | 重构 | `develop` | `develop` |
| `docs/` | 文档变更 | `develop` | `develop` |
| `release/` | 发布版本准备 | `develop` | `main` + `develop` |
| `chore/` | CI/工具链/构建 | `develop` | `develop` |

> **分支保护规则**：`main` 分支只接受从 `release/` 或 `hotfix/` 来的 PR，且必须通过 CI 检查 + 至少 1 人 review。

### 版本号规范

遵循 [语义化版本 2.0.0](https://semver.org/)：

```
主版本.次版本.补丁  （如 v1.2.3）
```

| 版本位 | 触发条件 | 示例 |
|--------|---------|------|
| **主版本** | 不兼容的 API 改动、重大架构变更 | `v2.0.0` |
| **次版本** | 向下兼容的新功能、模块新增 | `v1.3.0` |
| **补丁** | 向下兼容的 Bug 修复、小优化 | `v1.2.4` |

> 版本号在 `CHANGELOG.md` 中记录，发布时通过 git tag 标记。

---

## 测试指南

### 测试分类

| 类别 | 目录 | 平台要求 | 说明 |
|------|------|---------|------|
| 单元测试 | `web/*/` | 任意平台 | 纯逻辑测试，不依赖系统命令 |
| Linux 测试 | `web/common/sudo_test.go` | Debian 13 | 加 `//go:build linux` 标签 |
| 集成测试 | 待建设 | Debian 13 | 需要完整 NAS 环境 |

### 运行测试

```bash
# 全部单元测试（任何平台）
cd web && go test ./... -v

# 仅 common 包
cd web && go test ./common/ -v

# 含覆盖率
cd web && go test ./... -cover -coverprofile=coverage.out
go tool cover -html=coverage.out  # 浏览器查看

# 性能基准
cd web && go test ./common/ -bench=. -benchmem

# Linux 平台专用测试（需在 Debian 上运行）
cd web && go test ./common/ -v -run TestSudo
```

### 测试文件命名

- `*_test.go` — 通用单元测试
- `*_linux_test.go` — Linux 专用（Go 自动识别 build tag）
- 或文件内加 `//go:build linux`

### 当前测试覆盖范围

| 文件 | 覆盖内容 | 状态 |
|------|---------|------|
| `common/auth_test.go` | JWT 创建/验证/中间件/过期 | ✅ |
| `common/common_test.go` | JSON响应/env文件读写 | ✅ |
| `common/module_test.go` | Module接口/日志中间件 | ✅ |
| `common/sudo_test.go` | sudo/exec 命令（仅Linux） | ✅ |
| `modules/*/` | 各功能模块 | 🔲 待建设 |

### 测试要求

| 项 | 要求 |
|----|------|
| 单元测试覆盖率 | 新增代码 ≥ 60%（CI 中通过 `go test -cover` 检查） |
| Shell 脚本 | 所有 `.sh` 文件通过 `shellcheck` 检查，CI 中强制执行 |
| Linux 专用测试 | 使用 `//go:build linux` 构建标签，非 Linux 平台自动跳过 |

```bash
# Shell 脚本检查
shellcheck scripts/*.sh
```

---

## 前端开发规范

### 技术选型

- **HTML/CSS**：原生，不引入框架
- **JS 逻辑**：Alpine.js（已内联在 `index.html` 中）
- **样式**：`style.css`，全小写连字符命名

### API 调用规范

所有后端 API 调用遵循统一模式：

```javascript
async function apiCall(url, options = {}) {
    const resp = await fetch(url, {
        headers: { 'Authorization': 'Bearer ' + getToken(), ...options.headers },
        ...options
    });
    if (resp.status === 401) { logout(); return; }
    if (!resp.ok) {
        const err = await resp.json().catch(() => ({ error: resp.statusText }));
        showError(err.error || '请求失败');
        return null;
    }
    return resp.json();
}
```

### 错误状态处理

- 每个 API 调用必须有 `.catch()` 或 try/catch 兜底
- 用户操作（保存/删除等）完成后显示反馈提示
- 全局 401 处理：自动跳转登录页

### CSS 命名约定

- 使用全小写连字符：`disk-card`、`service-status`、`progress-bar`
- 避免深层嵌套（最多 3 层选择器）
- 颜色变量统一在 `:root` 中定义

### 新增页面/组件

- 在 `index.html` 中新增 section，`x-data` 命名与模块对应
- 非 Alpine.js 逻辑写在 `app.js` 中，按模块分组注释
- 新增 CSS 写在 `style.css` 中，按页面区域注释分组

---

## 提交规范

遵循 [Conventional Commits](https://www.conventionalcommits.org/)：

```
<type>: <简短描述>

<详细说明（可选）>
```

| type | 用途 |
|------|------|
| `feat` | 新功能 |
| `fix` | Bug 修复 |
| `docs` | 文档变更 |
| `refactor` | 重构 |
| `test` | 测试 |
| `chore` | 工具链/构建 |

示例：
```
feat: 添加磁盘健康状态实时监控

- 新增 S.M.A.R.T. 定时检测（每30分钟）
- Web 面板新增"磁盘健康"页面
- 异常时通过钉钉/邮件告警

Closes #12
```

---

## 版本发布流程

### 版本号规范

遵循语义化版本，加 beta/stable 通道：

| 通道 | Tag 格式 | 示例 | 说明 |
|------|---------|------|------|
| **beta** | `v1.4.0-beta.1` | 开发中，测试用 | 递增 `-beta.N` |
| **stable** | `v1.4.0` | 验证通过后发布 | beta 验证通过后打 |

### 发布包内容

```
nas-v1.4.0-linux-amd64.tar.gz
├── nas-panel              ← 预编译二进制 (strip后 ~7.5M)
├── frontend/              ← 前端文件 (index.html/style.css/app.js/alpinejs.min.js)
├── scripts/               ← 部署脚本 (setup.sh/cleanup.sh/...)
├── configs/               ← 服务配置模板 (smb.conf/vsftpd.conf/...)
├── .env.example           ← 配置模板
└── VERSION                ← 版本元数据
```

### 存储位置

| 位置 | 用途 | URL |
|------|------|-----|
| Gitee Release | git 托管，版本历史 | `https://gitee.com/gitdogcat/nas/releases` |
| R2 (beta) | 快速下载 | `https://get.z1.sale/releases/beta/nas-v1.4.0-beta.1-linux-amd64.tar.gz` |
| R2 (stable) | 快速下载 | `https://get.z1.sale/releases/stable/nas-v1.3.0-linux-amd64.tar.gz` |
| R2 (latest) | install.sh 默认 | `https://get.z1.sale/releases/stable/latest-linux-amd64.tar.gz` |

### 发版步骤

```bash
# 1. Beta 发版
git tag v1.4.0-beta.1
git push origin v1.4.0-beta.1
bash scripts/release.sh beta

# 2. 验证通过后打 stable
git tag v1.4.0
git push origin v1.4.0
bash scripts/release.sh stable
```

### 安装方式

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

### release.sh 脚本

`scripts/release.sh` 负责完整发版流程：

1. **build** — 交叉编译 linux/amd64 + linux/arm64，注入 ldflags 版本号
2. **pack** — 打包 tar.gz（二进制 + 前端 + 脚本 + 配置 + VERSION）
3. **upload** — 上传到 Cloudflare R2（beta/stable 不同目录）
4. **latest** — stable 发版时更新 `latest-linux-{arch}.tar.gz` 指针
5. **Gitee Release** — stable 发版时上传附件到 Gitee Release

### 版本号 API

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

### CHANGELOG 格式

参照 [Keep a Changelog](https://keepachangelog.com/)：

```markdown
## [v1.2.0] - 2026-07-26

### Added
- 新功能 A
- 新功能 B

### Fixed
- Bug 修复 C
- Bug 修复 D

### Changed
- 已有功能变更 E
```

### PR 描述规范

每个 PR 必须包含：
1. **变更类型**（feat/fix/refactor 等）
2. **关联 Issue**（如有关联）
3. **测试情况**（fmt / lint / test / build 是否通过）
4. **额外说明**（需要 reviewer 特别关注的点）

> PR 模板见 `.github/PULL_REQUEST_TEMPLATE.md`，创建 PR 时自动加载。

### 文档更新约定

| 变更类型 | 需要更新的文档 |
|---------|--------------|
| 新增功能模块 | `README.md` 功能列表、`TODO.md` 进度 |
| API 变更 | `README.md` API 文档部分 |
| 配置变更 | `.env.example`、`configs/` 下对应模板 |
| 硬件/部署变更 | `HARDWARE_SPEC.md`、`setup.sh` |
| 开发流程变更 | `DEVELOPMENT.md`、`CONTRIBUTING.md` |

> **原则**：功能变更和文档变更在同一 PR 中提交，不单独提 doc PR。

---

## 注意事项

### 跨平台开发

1. **路径分隔符**：始终使用 `/`，不要用 `\`
2. **系统命令**：`sudo`、`systemctl`、`ufw` 只在 Linux 上存在，相关代码加平台判断或 build tag
3. **文件权限**：`os.Chmod` 在 Windows 上行为不同，测试时注意
4. **行结束符**：统一 LF（.editorconfig 已配置）
5. **缩进**：Go 用 Tab，其他用空格（.editorconfig 已配置）

### 模块架构规范

#### 模块间依赖

```
common/  ←  modules/*/    （√ 所有模块依赖 common）
modules/A/  ←  modules/B/  （✗ 禁止模块间直接 import）
```

- 所有公共工具代码放在 `common/` 包中
- 功能模块 **禁止直接 import 其他功能模块**（如 `diskmgmt` 不能 import `system`）
- 如果模块 A 需要模块 B 的能力，请将共享逻辑下沉到 `common/` 包
- 前端模块间通信通过 Alpine.js 的全局事件（`$dispatch` / `$watch`），禁止通过 DOM 耦合

#### API 响应格式

所有 API 遵循统一格式：

```json
// 成功
{ "status": "ok", "data": { ... } }

// 失败
{ "error": "错误描述" }
```

- HTTP 状态码使用标准语义：200 成功、400 参数错误、401 未认证、403 无权限、500 服务端错误
- 错误响应始终返回 JSON，不返回纯文本
- 路径全部小写，支持连字符：`/api/disk/overview`、`/api/storage/quota`
  > **例外**：Linux 标准术语保留原样（如 `LVM`、`SMART`、`UUID`）

#### Go 构建标签约定

| 场景 | 方式 | 示例 |
|------|------|------|
| 整个文件仅 Linux | 文件名 `_linux.go` | `sudo_linux.go` |
| 文件内部分代码仅 Linux | `//go:build linux` | 仅在需要系统命令的函数上加 |
| 跨平台兼容 | `runtime.GOOS` 判断 | 可选项、降级行为用 runtime |

> 优先使用文件名约定（Go 自动识别），只在同一文件内混用跨平台和 Linux 代码时才用 build tag。

### 安全

1. **绝不硬编码密码**：始终从 `.env` 或环境变量读取
2. **sudoers 白名单**：新增需要 root 的命令时，同步更新 sudoers 配置
3. **JWT secret**：生产环境必须通过 `JWT_SECRET` 环境变量覆盖默认值
4. **路径注入**：所有用户输入的路径参数必须校验（参照 `backup.go` 的做法）
5. **发布产品不写内网 IP / 明文密码**（2026-09-04 确立）：`deploy-nas-panel.sh` 目标机器一律命令行参数传入，禁止写死 SERVERS 数组、IP→用户映射、本机绝对路径；`install-services.sh` 账号密码从环境变量 / `/opt/nas/.env` 读取；一次性内网脚本（deploy-115.sh）不进仓库；文档示例 IP 用 192.168.1.100 这类通用示例。发版前 `grep -rnE "10\.216\.|10\.187\.|192\.168\.213\.|nas123456" scripts/ docs/ configs/` 必须为空。详见 `docs/release-system.md`。

### 不要在 Windows 上做的事

- 运行 `setup.sh`（必须在 Debian 上）
- 测试 sudo 相关功能（无 sudo 命令）
- 测试 systemd 服务管理（无 systemd）
- 测试 UFW 防火墙（无 ufw）
- 测试 S.M.A.R.T.（无 smartctl）

### 代码审查清单

- [ ] 所有导出函数有注释
- [ ] 没有用 `_` 忽略错误（特殊情况需注释说明）
- [ ] 新增命令已加入 sudoers 白名单
- [ ] 前端新增 API 调用有 loading/error 状态
- [ ] Shell 脚本变量加了引号
- [ ] 路径中没有硬编码 `/opt/nas`（用变量）
- [ ] API 路径遵循全小写连字符命名
- [ ] 新增模块没有直接 import 其他功能模块
- [ ] 前端 API 调用遵循统一的 `apiCall` 模式
- [ ] `go.sum` 已提交（不要加入 .gitignore）
- [ ] 相关文档已同步更新（README / TODO / docs 等）
- [ ] PR 描述填写完整（变更类型、测试情况、额外说明）

## 获取帮助

- 在 Issue 区提问，标签 `question`
- 技术讨论用 `discussion` 标签
- 紧急问题直接找项目维护者
