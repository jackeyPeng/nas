# NAS 家用存储系统 — Agent 开发指南

> 给 AI agent 和开发者的仓库速览。协作规则（PR 流程、冲突仲裁、红线）见 docs/COLLAB.md，两者冲突时以 COLLAB.md 为准。人类视角的完整手册见 DEVELOPMENT.md 和 CONTRIBUTING.md。

## 项目是什么

面向家庭/小型办公的 NAS 管理系统：**Go 单二进制 Web 面板（nas-panel）+ Debian 13 部署运维脚本**。Go 源码在 `web/`，前端是 Alpine.js 单页（`web/frontend/`），embed 进二进制发布。

## 常用命令

```bash
make build        # 编译当前平台 → build/nas-panel
make build-all    # 交叉编译 linux/amd64、arm64、arm/v7
make test         # cd web && go test ./... -v -count=1
make lint         # golangci-lint（未装则降级 go vet）
make fmt          # gofmt
make lint-all     # Go + shellcheck 全面检查
make fmt-all      # Go + Shell + 前端格式化
make dev          # 本地开发运行
```

要求 Go 1.25+。日常开发可在任意平台，但面板大量依赖系统命令（systemctl/ufw/df/smartctl/mdadm 等），**最终集成验证必须在 Debian 13 上做**。

## 目录结构

```
nas/
├── web/                  # Go 模块（main.go + 全部后端代码）
│   ├── main.go
│   ├── common/           # 跨模块共享：auth、sudo、logger、module 注册机制（均有 _test.go）
│   ├── modules/          # 每个功能一个目录：storage、diskmgmt、backup、users、
│   │                     #   firewall、monitor、health、update、vault、rclone、services ...
│   └── frontend/         # Alpine.js SPA：index.html、style.css、app.js、alpinejs.min.js、i18n/
├── scripts/              # 部署运维：setup.sh、install.sh、upgrade.sh、release.sh、
│                         #   backup-data.sh、check-i18n.py、gen-sbom.py ...
├── configs/              # systemd unit 等配置模板
├── third_party/          # 第三方组件（filebrowser 等）+ THIRD_PARTY_LICENSES.md
└── docs/                 # 架构文档、产品手册、plans/、COLLAB.md
```

## 硬性约定

- **前端 embed 进二进制**：改 `web/frontend/` 后必须重新 `make build` 才生效，二进制和前端不分开部署。
- **前端依赖必须本地化**：alpinejs 等以本地文件形式放 frontend/，禁止引 CDN（内网环境 CDN = 白屏）。
- **新前端文案必须过 i18n**：中英文双语，`scripts/check-i18n.py` 可校验。
- **模块模式**：新功能作为 `web/modules/<name>/` 新目录，通过 common/module.go 的注册机制接入，不往 main.go 堆代码。
- **需要 root 的操作走 common/sudo.go**，不自己拼 sudo 命令。
- 提交信息遵循 Conventional Commits（feat/fix/docs/refactor/test/chore），详见 DEVELOPMENT.md。
- 版本号：`v1.4.0-beta.N` 开发通道，验证通过后打 `v1.4.0` 正式版；发布走 `scripts/release.sh` + RELEASE.md 流程，产物上传 Gitee release（attach_files）。

## 生产环境警示

面板部署在真实 NAS 物理机上，直通磁盘/防火墙/服务管理。**在生产机上执行任何 服务重启、磁盘操作（分区/RAID/格式化）、防火墙变更 之前，必须有对应 Gitee issue 的明确授权**（COLLAB.md 红线）。本地开发时 `make dev` 是安全的，但别把本地面板指向生产机的系统命令。

## 测试

`make test` 覆盖 common/ 与各 modules 的纯逻辑单元测试。涉及系统命令的路径只能在 Debian 环境验证；shell 脚本改动跑 `make lint-all`（含 shellcheck）。PR 合入前两者都要绿。
