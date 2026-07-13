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

## 注意事项

### 跨平台开发

1. **路径分隔符**：始终使用 `/`，不要用 `\`
2. **系统命令**：`sudo`、`systemctl`、`ufw` 只在 Linux 上存在，相关代码加平台判断或 build tag
3. **文件权限**：`os.Chmod` 在 Windows 上行为不同，测试时注意
4. **行结束符**：统一 LF（.editorconfig 已配置）
5. **缩进**：Go 用 Tab，其他用空格（.editorconfig 已配置）

### 安全

1. **绝不硬编码密码**：始终从 `.env` 或环境变量读取
2. **sudoers 白名单**：新增需要 root 的命令时，同步更新 sudoers 配置
3. **JWT secret**：生产环境必须通过 `JWT_SECRET` 环境变量覆盖默认值
4. **路径注入**：所有用户输入的路径参数必须校验（参照 `backup.go` 的做法）

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

## 获取帮助

- 在 Issue 区提问，标签 `question`
- 技术讨论用 `discussion` 标签
- 紧急问题直接找项目维护者
