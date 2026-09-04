# Z1 项目全面评审报告

> 评审日期: 2026-09-04  
> 项目状态: 31 项 TODO 中 19 完成(61.3%)，1 进行中，11 待办  
> 评审维度: 架构设计 / 代码工程 / 用户体验 / 安全 / 商业化

---

## 一、总体评价

**一句话：项目进展远超预期，核心骨架已搭好，现在进入"打磨 + 填坑"阶段。**

作为 1 人副业项目，能在几个月内完成：
- 11 个 Web 面板模块
- 4 层存储模型 + RAID 向导 + LVM/RAID 在线扩容
- 多语言 i18n（970 keys）
- HTTPS 证书管理
- 一键安装脚本（9/9 服务 + 46/46 注册表验证通过）
- 系统诊断 + 监控告警 + 备份恢复

这个完成度和执行力，在开源 NAS 领域已经非常有竞争力。

---

## 二、🔴 P0：必须现在修（不修会积累技术债或影响口碑）

### 2.1 权限模型（TODO #30）— 最大技术债

**现状**：UI 画了一个「用户 × 文件夹 × 协议」的三维权限矩阵，但后端数据模型是「文件夹级」的——一个文件夹只有一个 `permission` 字段。这导致：
- 权限矩阵里设「Bob 只读」会落地成整个共享的 `read only = yes`，Alice 也跟着变只读
- 各协议真实能力和 UI 徽标不匹配（WebDAV/S3 是全局服务，却打 ✓ 冒充按用户隔离）
- 这是用户最可能遇到的"功能不可用"问题

**建议**：按 `docs/permission-model-discussion.md` + 之前讨论的《Z1 权限模型重构方案》执行：
1. **诚实标注**：SMB 显示「按用户」蓝标，WebDAV/S3/NFS 显示「全局」黄标，FTP 从共享文件夹移除
2. **SMB 做精**：`folders` 表加 `read_users` / `write_users` JSON 字段，生成 smb.conf 时用 `read list` / `write list`
3. **工期**：5-7 天，这是目前最值得投入的 TODO 项

### 2.2 存储管理 Tab 切换跳动（已知 UI 问题）

**现象**：点击 Tab 时内容从下方跳上来，不够平滑。

**根因**：Alpine.js 的 `x-transition` 在兄弟元素间切换时，旧元素淡出期间仍占位，新元素出现在下方，旧元素消失后新元素"跳"上来。

**建议**：不用改 DOM 结构，用 CSS 解决：
```css
/* 让所有 tab 内容绝对定位在同一位置 */
.tab-content {
    position: absolute;
    width: 100%;
    opacity: 0;
    pointer-events: none;
    transition: opacity 0.25s ease;
}
.tab-content.active {
    opacity: 1;
    pointer-events: auto;
    position: relative;
}
```
或者更简单：**去掉过渡动画**，直接 `x-show` 无动画切换。NAS 管理面板不是营销页，功能稳定 > 动画精美。

**工期**：10 分钟

### 2.3 Go 版本号

README 写的 Go 1.25，但截至目前 Go 最新稳定版应该是 1.23/1.24。如果 `go.mod` 里实际用的是 1.23，文档需要同步修正，避免用户困惑。

**工期**：5 分钟

---

## 三、🟡 P1：接下来 1-2 个月做（提升产品完整度）

### 3.1 版本更新功能（TODO #19）— 用户体验分水岭

**现状**：用户现在升级需要 SSH 登录 + 手动替换二进制。对家用 NAS 用户来说，这门槛太高。

**建议**：
```go
// 版本号嵌入编译
// Makefile
go build -ldflags "-X main.Version=$(VERSION)" -o nas-panel .

// 检查更新 API
GET /api/system/version
→ { "current": "1.2.3", "latest": "1.3.0", "url": "..." }

// 一键升级流程
1. 下载新二进制到 /tmp/nas-panel.new
2. 备份旧版到 /usr/local/bin/nas-panel.bak
3. systemctl stop nas-panel
4. cp /tmp/nas-panel.new /usr/local/bin/nas-panel
5. chmod +x
6. systemctl start nas-panel
7.  health check（访问 /api/health）
8. 失败则自动回滚
```

**前端**：系统设置页加「版本信息」卡片，显示当前版本 + 检查更新按钮 + 升级进度（SSE 流式）。

**工期**：2-3 天

### 3.2 显示当前连接设备（TODO #31）— 监控页的自然延伸

**现状**：监控告警页已有 SSH 登录用户 + 网络流量，但缺少 SMB/NFS/FTP/WebDAV 的活跃会话。

**建议**：
```bash
# 数据源
SMB:  smbstatus -b          # 用户名/客户端IP/连接时长
NFS:  showmount -a          # 客户端IP/导出路径
SSH:  who                     # 已有
FTP:  netstat -tnp | grep :21  # 或 vsftpd 日志
Web:  netstat -tnp | grep :8080/9000
```

后端聚合为 `/api/monitor/connections`，前端在监控页加「活跃连接」表格。

**工期**：1-2 天

### 3.3 关于页面（TODO #22）— 简单但重要

**现状**：Web 面板没有「关于」页，用户看不到版本号、开源地址、许可证信息。

**建议**：左侧导航加「关于」项，展示：
- 项目 Logo + 简介
- 当前版本号
- 开源地址（Gitee/GitHub 链接）
- 许可证（AGPLv3）
- 第三方组件列表
- 更新日志摘要（读 CHANGELOG.md 前 10 行）

**工期**：半天

### 3.4 性能调优（TODO #9）— 有方案但需验证

**现状**：TODO 里列了很详细的优化方向（TCP BBR、I/O 调度器、缓存策略），但都是"待规划"。

**建议**：不要一次性做所有优化，先选**影响最大、风险最低**的：

| 优化项 | 影响 | 风险 | 命令 |
|--------|------|------|------|
| 启用 TCP BBR | 网络吞吐 ↑ 10-30% | 极低 | `sysctl net.ipv4.tcp_congestion_control=bbr` |
| noatime 挂载 | 减少写放大 | 极低 | `mount -o remount,noatime /data` |
| Samba oplocks | 多客户端并发 ↑ | 低 | `smb.conf: oplocks = yes` |
| NFS rsize/wsize | 大文件传输 ↑ | 低 | `exports: rsize=1048576,wsize=1048576` |

**工期**：1 天（写脚本 + 测试）

---

## 四、🟢 P2：可以推迟或不做（1 人副业资源有限）

### 4.1 插件系统（TODO #21）— 建议推迟到 v2.0

**现状**：规划了插件注册、插件市场、沙箱隔离等，但依赖 HTTP 微服务或 Go plugin 动态加载。

**问题**：
- Go plugin（.so）跨平台兼容性差，且 Go 1.20+ 后维护成本上升
- HTTP 微服务 = 每个插件一个进程，家用 NAS 内存吃不消
- 插件市场需要后端基础设施（审核、签名、分发），1 个人维护不过来

**建议**：
- **v1.x 阶段**：不做插件系统。BT 下载用 aria2 命令行，多媒体用 Jellyfin Docker（用户自行安装），面板只提供入口链接
- **v2.0 阶段**：如果用户量到 1000+，再考虑插件架构

### 4.2 AI 功能（TODO #20）— 噱头大于实用

**现状**：规划了智能分类、内容搜索、异常检测、人脸识别等。

**问题**：
- 本地模型（如 Ollama）对 N100/N150 这种低功耗 CPU 压力太大
- 云端 API 涉及隐私（用户照片、文档上传到第三方）
- "AI 找文件"的准确率在家用场景下不如直接按文件夹浏览

**建议**：完全不做。家用 NAS 的核心价值是"存储 + 共享 + 备份"，AI 是锦上添花且成本极高。如果将来要做，先做最简单的：
- 基于文件扩展名的自动分类（照片/视频/文档/其他）
- 零 AI 成本，纯规则引擎

### 4.3 多实例端口可配置（TODO #29）— 需求不明确

**现状**：规划支持同一协议多实例（如两个 WebDAV 不同端口）。

**问题**：
- 家用场景真的需要多个 WebDAV 实例吗？
- 每实例一个 systemd 服务 + 独立端口 + 独立防火墙规则，维护复杂度指数上升
- rclone serve 的稳定性在大规模实例下未知

**建议**：不做。如果用户有隔离需求，建议用 VLAN/防火墙规则限制访问，而不是起多实例。

### 4.4 回收站分散（TODO #25）— 可做但非紧急

**现状**：集中回收站，跨设备移动变 copy+delete。

**建议**：等权限模型（#30）做完后再做。因为回收站和文件夹权限强相关，先稳定权限再改回收站逻辑。

---

## 五、🔵 工程实践建议（长期受益）

### 5.1 加单元测试

目前没有看到测试相关文件。建议至少给以下模块加测试：

```
web/modules/config/smb_generator_test.go    # SMB 配置生成
web/modules/diskmgmt/raid_recommend_test.go   # RAID 推荐引擎
web/common/sudo_test.go                       # sudo 命令白名单
scripts/setup_test.sh                         # 部署脚本（在 Docker 中跑）
```

**工期**：每个模块 2-4 小时，总 1-2 天

### 5.2 GitHub Actions CI

```yaml
# .github/workflows/ci.yml
name: CI
on: [push, pull_request]
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.23' }
      - run: cd web && go test ./...
      - run: cd web && go build -o nas-panel .
      - run: shellcheck scripts/*.sh
```

**工期**：1 小时搭建，之后零维护

### 5.3 版本号嵌入 + Release 自动化

```makefile
# Makefile
VERSION := $(shell git describe --tags --always --dirty)
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')

build:
	cd web && go build -ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)" -o nas-panel .

release:
	# GitHub Actions 自动编译多平台二进制并发布到 Release
```

**工期**：2 小时

### 5.4 数据库迁移框架

目前权限模型改 schema 需要手动执行 SQL。建议加一个极简迁移框架：

```go
// web/common/migrate.go
var migrations = []Migration{
    {Version: 1, SQL: "CREATE TABLE folders (...)"},
    {Version: 2, SQL: "ALTER TABLE folders ADD COLUMN read_users TEXT DEFAULT '[]'"},
    // ...
}

func Migrate(db *sql.DB) error {
    // 读取当前版本，按顺序执行未执行的迁移
}
```

**工期**：半天

---

## 六、📊 优先级总表

| 优先级 | 事项 | 工期 | 影响 |
|--------|------|------|------|
| 🔴 P0 | 权限模型（#30） | 5-7 天 | 解决核心功能缺陷 |
| 🔴 P0 | Tab 切换跳动 | 10 分钟 | 修复已知 UI 问题 |
| 🔴 P0 | Go 版本号修正 | 5 分钟 | 文档准确性 |
| 🟡 P1 | 版本更新（#19） | 2-3 天 | 用户体验分水岭 |
| 🟡 P1 | 活跃连接（#31） | 1-2 天 | 监控页完整性 |
| 🟡 P1 | 关于页面（#22） | 半天 | 产品专业度 |
| 🟡 P1 | 性能调优（#9） | 1 天 | 传输速度提升 |
| 🟢 P2 | 回收站分散（#25） | 2-3 天 | 等权限做完再做 |
| 🟢 P2 | 插件系统（#21） | 不建议做 | 资源不够 |
| 🟢 P2 | AI 功能（#20） | 不建议做 | 噱头 |
| 🟢 P2 | 多实例端口（#29） | 不建议做 | 需求不明确 |
| 🔵 工程 | 单元测试 | 1-2 天 | 长期受益 |
| 🔵 工程 | GitHub Actions CI | 1 小时 | 长期受益 |
| 🔵 工程 | 版本号嵌入 + Release | 2 小时 | 长期受益 |
| 🔵 工程 | 数据库迁移框架 | 半天 | 长期受益 |

---

## 七、💡 一句话建议

> **先填权限模型的坑，再做版本更新，其他都可以等。**

权限模型是用户每天会碰到的功能，现在"UI 画得漂亮但后端做不到"的状态最伤口碑。版本更新是"装完就不用 SSH"的关键，做完这两个，Z1 就可以从"极客玩具"升级为"家用可用"的产品。

插件、AI、多实例这些，等你有 1000 个真实用户、有人愿意付费的时候，自然知道该做什么。

---

*评审结束*
