# 贡献指南

感谢你对 NAS 家用存储系统的关注！欢迎任何形式的贡献。

## 行为准则

- 尊重所有贡献者，保持友善和专业的交流
- 建设性提出问题和建议，对事不对人
- 帮助新手融入项目

## 如何贡献

### 报告 Bug

1. 先在 [Issues](https://gitee.com/gitdogcat/nas/issues) 中搜索，确认是否已有相同问题
2. 如果没有，创建新 Issue，包含以下信息：
   - **环境**：Debian 版本、硬件配置（CPU/内存/磁盘）
   - **复现步骤**：详细描述如何触发 Bug
   - **预期行为** vs **实际行为**
   - **日志**：`journalctl -u nas-panel -n 100` 的输出

### 功能建议

1. 先在 Issues 中搜索是否已有类似建议
2. 创建 Issue 时说明：
   - 使用场景：你会在什么情况下用到这个功能
   - 期望效果：功能应该如何工作
   - 是否有替代方案

### 提交代码

#### 开发环境搭建

```bash
# 要求：Go 1.25+

# 克隆仓库
git clone https://gitee.com/gitdogcat/nas.git
cd nas/web

# 安装依赖
go mod download

# 本地开发（注意：部分功能依赖系统命令，需要在 Debian 环境测试）
go run .

# 构建
go build -o nas-panel .
```

> 提示：建议在 Debian 13 虚拟机或物理机上进行完整功能测试，因为 Web 面板依赖多个系统命令（`systemctl`、`ufw`、`df`、`smartctl` 等）。

#### 代码规范

- **Go**：遵循标准 Go 代码风格，使用 `gofmt` 格式化
  ```bash
  gofmt -w .
  ```
- **文件命名**：小写 + 下划线（`backup_config.go`）
- **包命名**：小写单个单词，避免下划线
- **注释**：导出的函数/类型必须有注释，使用中文或英文均可，但要统一
- **错误处理**：不要用 `_` 忽略错误，必须显式处理
- **变量命名**：驼峰命名，缩写全大写（`nasUser` 而非 `nasuser`）

#### 模块架构

```
web/
├── main.go          # 入口，路由注册
├── common/          # 共享工具
│   ├── auth.go      # JWT 认证
│   ├── common.go    # JSON 响应、.env 操作
│   ├── module.go    # Module 接口定义
│   └── sudo.go      # sudo 命令封装
├── modules/         # 功能模块（每个模块独立注册路由）
│   ├── dashboard/   # 仪表盘
│   ├── services/    # 服务管理
│   ├── users/       # 用户管理
│   ├── storage/     # 存储信息
│   ├── firewall/    # 防火墙
│   ├── monitor/     # 监控告警
│   ├── config/      # 配置管理
│   ├── diskmgmt/    # 磁盘管理
│   ├── system/      # 系统设置
│   └── backup/      # 备份恢复
└── frontend/        # 前端 (Alpine.js SPA)
    ├── index.html
    ├── app.js
    └── style.css
```

添加新模块的方法：
1. 在 `modules/` 下创建新目录和文件
2. 实现路由注册函数 `RegisterRoutes(mux *http.ServeMux)`
3. 在 `main.go` 中注册新模块

#### PR 流程

1. Fork 本仓库
2. 创建功能分支：`git checkout -b feature/你的功能名`
3. 编写代码并确保通过 `go build`
4. 提交：`git commit -m "feat: 功能描述"`
5. 推送：`git push origin feature/你的功能名`
6. 创建 Pull Request

**Commit 信息规范**（参考 [Conventional Commits](https://www.conventionalcommits.org/)）：

| 前缀 | 用途 |
|------|------|
| `feat:` | 新功能 |
| `fix:` | Bug 修复 |
| `docs:` | 文档变更 |
| `refactor:` | 重构（不改变功能） |
| `perf:` | 性能优化 |
| `test:` | 测试相关 |
| `chore:` | 构建/工具链变更 |

#### 测试

```bash
# 运行所有测试（功能开发中）
go test ./...

# 运行特定模块测试
go test ./modules/dashboard/
```

> 测试框架正在完善中，欢迎贡献测试用例。

## 项目方向

本项目有两条主线：

1. **开源软件** — 打造社区驱动的 NAS 操作系统
2. **软硬一体产品** — 提供开箱即用的 NAS 硬件方案

请在贡献时考虑兼容性：代码应能在标准 Debian 13 上运行，不依赖特定硬件。

## 获取帮助

- **Issue**：[Gitee Issues](https://gitee.com/gitdogcat/nas/issues)
- **讨论**：欢迎在 Issue 区进行技术讨论

## 许可证

本项目采用 [GNU Affero General Public License v3.0](LICENSE)。贡献代码即表示你同意在此许可证下发布你的代码。
