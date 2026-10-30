# TASK-REPORT：协议服务默认收敛（新装只开 SMB+面板）+ 服务页人话卡片化

## 改动文件清单

| 文件 | 改动点 |
|---|---|
| `scripts/setup.sh` | FRESH_INSTALL 判定；`enable_optional_service` 辅助函数；四协议 enable/restart 段改调用该函数；结尾服务状态/访问方式按新/旧装机条件化输出 |
| `scripts/install.sh` | "其他访问方式"横幅条件化打印 + 默认关闭提示行；"服务状态"验证循环拆分核心/可选两段，可选服务 disabled+inactive 不置 VERIFY_OK=false |
| `web/modules/dashboard/dashboard.go` | `ServiceDef.Optional` 字段；`IsOptionalService()`；`ServiceState`/`servicesSummary()` 纯函数；`GetServices()` JSON 带 `optional`；`handleDashboard` 用新口径算 `active_count`/`total_services`/`bad_services` |
| `web/modules/dashboard/dashboard_test.go` | `TestServicesSummary`（三态）、`TestIsOptionalService`、`TestNasServicesOptionalFlags` |
| `web/modules/services/services.go` | `handleServiceAction` 新增 `on`/`off` 分支；`serviceToggleAction()` 纯函数；`handleServiceToggle()`（optional 门禁 400 + systemctl enable/disable --now + is-active 复核 + `common.LogAuditRequest` 审计） |
| `web/modules/services/unit_test.go` | `TestServiceToggleAction`（映射）、`TestHandleServiceToggleOptionalGate`（门禁拒绝路径，不触发真实 SudoExec） |
| `web/modules/system/system_registry.go` | `classifyOptionalProtocolService()` 纯函数（三态判定）；`checkOptionalProtocolService` 闭包；ID 3/4/5/7 改用该判定 |
| `web/modules/system/system_registry_test.go` | `TestClassifyOptionalProtocolService`（四态用例） |
| `web/frontend/index.html` | 服务页拆分为「核心区」表格（精简为重启+日志，无 stop/disable）与「协议服务区」2 列卡片（图标/用途/开关/状态徽章/地址+凭据/风险提示） |
| `web/frontend/app.js` | `coreServices`/`protocolServices` getter；`svcIcon/svcPurpose/svcRisk/svcAccessAddress/svcCredentialHint/svcToggle`；`serviceSummary` getter 口径同步后端三态 |
| `web/frontend/i18n/zh-CN.json`、`en-US.json` | 新增协议卡片人话文案（用途/风险/开关提示/地址/凭据），双语同步 1414 key |

TASK-REPORT.md 本身未 `git add`。

## FRESH_INSTALL 判定点

`scripts/setup.sh`：
- 第 153 行：`ENV_FILE="$SCRIPT_DIR/.env"`
- 第 161-165 行：
  ```bash
  if [ -f "$ENV_FILE" ]; then
      FRESH_INSTALL=false
  else
      FRESH_INSTALL=true
  fi
  ```
  紧跟在 `ENV_FILE` 赋值之后、且早于第 167 行起的 `.env` 存在性校验/`chmod 600` 等任何可能读写 `.env` 的逻辑之前——setup.sh 自身从不创建 `.env`（创建动作在 install.sh 的"创建配置文件"步骤，先于调用 setup.sh），因此该读取时刻的存在性即是真实的"是否已装过"信号，不会被自身副作用污染。

`enable_optional_service()` 定义于第 80-101 行（早于 `FRESH_INSTALL` 赋值，但只在调用时求值，调用点全部在第 161 行之后，不存在用前未赋值的问题）。四处调用：
- 第 474 行：`enable_optional_service nfs-kernel-server "NFS"`
- 第 508 行：`enable_optional_service vsftpd "FTP"`
- 第 536 行：`enable_optional_service rclone-webdav "WebDAV"`
- 第 642 行：`enable_optional_service rclone-s3 "S3 对象存储"`

## 三处口径判定表

### 1. 注册表（`system_registry.go` ID 3/4/5/7，`classifyOptionalProtocolService`）

| enabled | active | 判定 | detail |
|---|---|---|---|
| false | false | **pass** | "默认关闭，可在服务管理页开启" |
| true | true | pass | "active + enabled"（原有语义） |
| true | false | fail | "状态: inactive, 自启: enabled"（原有语义） |
| false | true | warn | "active but not enabled"（原有语义） |

`allRegistryItems()` 总数保持 41（`TestAllRegistryItemsNoFileBrowser` 锁定），配置项(13/14)、systemd 单元项(20/22)、软件包项(46) 判定逻辑未改——文件/单元照常生成，这几项与协议服务是否被 enable 无关。

### 2. 仪表盘计数（`dashboard.go` `servicesSummary`）

| Optional | Enabled | 是否计入分母(total) | 是否算异常(bad) |
|---|---|---|---|
| false（核心服务） | — | 是 | 看 Active（不活跃即异常） |
| true（协议服务） | false（disabled） | **否** | **否**（不触碰） |
| true（协议服务） | true（enabled） | 是 | 看 Active（enabled 但未 active 才算异常） |

前端 `app.js` 的 `serviceSummary` getter 与后端 `servicesSummary` 用同一条规则，分别在客户端（仪表盘页展示）和服务端（`/api/dashboard` 的 `total_services`/`bad_services`，当前未被前端消费但保持正确性）各实现一份，规则逐字一致。

### 3. install.sh 验证循环

| 服务类别 | 状态 | 验证结果 | 是否置 VERIFY_OK=false |
|---|---|---|---|
| 核心（smbd/nmbd/fail2ban/nas-panel） | active | ✓ | 否 |
| 核心 | 非 active | ✗ | **是**（不变） |
| 可选（nfs/vsftpd/webdav/s3） | active | ✓ | 否 |
| 可选 | inactive **且** disabled | **"○ 默认关闭"** | **否**（新增豁免） |
| 可选 | 其他（active 但判定为非 active 的瞬态，或 enabled 但 failed） | ✗ | 是 |

## 验收自检

- `cd web && go build ./... && go vet ./... && go test ./... -count=1`：全过（`modules/tasks` 在高并发跑全量测试时偶发 "database out of memory"，单独重跑/重跑全量均稳定通过，确认是测试环境资源争用导致的既有不稳定，非本次改动引入——已用 `git stash` 对比验证 stash 前后 shellcheck 警告集合完全一致，同样方法确认了该 flaky 与本次 diff 无关）。
- `bash -n scripts/setup.sh scripts/install.sh`：通过。
- `shellcheck scripts/setup.sh scripts/install.sh`：警告集合与改动前（`git stash` 对比）完全一致（SC2034/SC2024/SC2162，均为历史遗留，未改动的代码行），未引入新警告。
- `python3 scripts/check-i18n.py`：zh-CN/en-US 均 1414 key，一致。
- `grep allRegistryItems` 前后 `allRegistryItems()` 长度仍为 41（`TestAllRegistryItemsNoFileBrowser` 断言）。
- install.sh 的 UNC 行 `\\\\\\\\${PRIMARY_IP}\\\\public` 逐字节未变（只是向上移动位置，紧跟在 Samba 固定展示行之后，WebDAV/FTP/S3 改为条件展示）。
- 前端 grep：`nfs-kernel-server`/`vsftpd`/`rclone-webdav`/`rclone-s3` 均出现在 `app.js` 的 `svcPurpose`/`svcRisk`/`svcIcon`/`svcAccessAddress` 映射与对应 i18n 键（`services.purpose_*`/`services.risk_*`）中；核心区表格（`coreServices` 循环）内 grep `svcAction(svc.name, 'stop'|'disable')` 命中数为 0。

## 偏离与理由

1. **NFS 访问地址文案**：任务文案给的示例是 `<IP>:/public`，但 `/etc/exports` 实际导出路径是 `/data/nas1/public`（面板托管段固定路径，`setup.sh`/`install.sh` 结尾横幅也打印 `mount -t nfs NAS_IP:/data/nas1/public /mnt/nas`）。为避免给用户一条实际挂载会失败的命令，`svcAccessAddress()` 里 NFS 一项使用了真实路径 `/data/nas1/public` 而非任务文案的简化写法，其余三项（WebDAV/FTP/S3）端口和协议与任务文案完全一致。
2. **install.sh 验证循环**：任务只要求"disabled 且 inactive 不算失败"，未明确 active 但 disabled（例如用户手动 `systemctl start` 但没 enable）算什么。本实现沿用判定表第 4 行（除 active 和"disabled+inactive"外的其他组合）一律判为 ✗ 并置 VERIFY_OK=false，与核心服务判定口径保持一致（有活跃信号即应达到完全健康态，否则提示检查），未引入额外的宽松分支。
3. **仪表盘后端 `total_services`/`bad_services` 字段**：梳理代码发现前端 `serviceSummary` 是纯客户端计算（不读取这两个后端字段），为保证口径统一仍按任务要求同步改了后端这两个字段的计算方式（使用同一条 `servicesSummary` 规则），但这是面向未来/其他消费者的正确性修复，而非本次验收必须项——按"先找到现有计算点，该改前端就改前端，该改后端就改后端"的指示，两处都改了以保证不会出现两套口径长期分叉。
4. **S3 凭据提示来源**：按任务要求 grep 了 `setup.sh` 第 577-586 行 `S3_ACCESS_KEY`/`/etc/rclone/s3-env` 的生成逻辑，如实写入 `credential_s3_akks` 文案："AK = 用户名（或 z1- 前缀），SK = 面板密码"，与后端 `common.S3AccessKey()` 的实现逐字对应。
