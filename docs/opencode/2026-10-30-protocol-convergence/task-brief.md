# 任务：协议服务默认收敛（新装只开 SMB+面板）+ 服务页人话卡片化

产品决策（用户已拍板）：新装机默认只开 SMB + 面板 Web，WebDAV/FTP/NFS/S3 四个协议默认关闭、
收进服务页一键开启（人话卡片）；**升级机现有开关状态一律不动**。目标是"用户用起来方便"——
默认路径越少越清楚，关掉的东西必须一键能开、开启后立即给出访问地址和凭据位置。

## 硬约束

- 允许改动：`scripts/setup.sh`、`scripts/install.sh`、`web/modules/services/`、
  `web/modules/dashboard/`、`web/modules/system/system_registry.go`、
  `web/frontend/{app.js,index.html,i18n/zh-CN.json,i18n/en-US.json}`、相关 `*_test.go`。
- 验证只通过 `web/` 内 `go build ./... && go vet ./... && go test ./... -count=1`（至少跑
  services/dashboard/system 三包 + 全量编译）与 `bash -n scripts/*.sh`、shellcheck（如可用）。
  **禁止真实执行安装脚本/systemctl/磁盘命令**；禁止读取仓库外文件。
- install.sh/setup.sh 禁止引入交互式输入（read）；不得新增超出既有 sudoers 白名单形态的命令
  （白名单已有 `/bin/systemctl enable *`、`disable *`、`start *`、`stop *`、`is-active *`、
  `list-unit-files *`，`systemctl enable --now x` 落在 `enable *` 通配内，可用）。
- 不动 ufw 防火墙规则、不动 folders.db/config_sync 投影逻辑（协议关闭时配置文件照常生成，
  保证开启即可用）。
- 每个逻辑单元独立 commit（建议：①scripts ②后端 services/dashboard/registry ③前端+i18n）。
- TASK-REPORT.md 写仓库根，不 git add。

## Item A：setup.sh 新装默认关闭四协议

现状：setup.sh 对 nfs-kernel-server(431/433)、vsftpd(468/470)、rclone-webdav(499/501)、
rclone-s3(608/610) 无条件 `systemctl enable` + `restart`。

改法：
1. 在 setup.sh 早期（读 .env 前后）判定 `FRESH_INSTALL`：**/opt/nas/.env 在 setup 开始时不存在
   = 全新安装**（与"升级保留已改密码"用同一信号；install.sh 升级路径会先有 .env）。
   注意 setup.sh 自身后续会创建 .env，判定必须发生在创建之前，存变量传递。
2. 四个协议服务的配置/单元文件**照常全部生成**（vsftpd.conf、userlist、/etc/exports、
   rclone 两个 unit、htpasswd——保持现有幂等逻辑不变，这是"开启即可用"的前提）。
3. enable/start 段改为：
   - `FRESH_INSTALL=true`：`systemctl disable --now <svc> 2>/dev/null || true`
     （apt 装 nfs-kernel-server/vsftpd 时 Debian preset 会自动 enable，必须显式关；
     rclone 两个 unit 是新写的，disable --now 幂等无害）。步骤日志改为
     「已安装并生成配置（默认关闭，可在面板"服务管理"一键开启）」。
   - `FRESH_INSTALL=false`（升级）：**完全沿用现状**——enable + restart，不动用户已有状态。
     若升级机上服务本来就是 disabled+inactive（用户此前手动关的），维持关闭：
     升级路径的规则 = 「enabled 或 active 才 enable+restart，否则只刷配置不启动」。
4. 注册表在 setup.sh 结尾的自检输出：四协议关闭时不应显示 ✗ 失败（见 Item B 的同款逻辑，
   setup.sh 里如有服务状态循环也要按"默认关闭"口径输出 ○ 而非 ✗）。

## Item B：install.sh 横幅与验证口径

1. 结尾「其他访问方式」段（388-392 行附近）：
   - Samba 行保留（注意保留现有 8+4 反斜杠 UNC 形态，别改坏）。
   - WebDAV/FTP/S3 行改为仅当对应服务 active 才打印。
   - 增加一行提示：「更多协议（FTP/WebDAV/NFS/S3）默认关闭，可在面板"服务管理"页一键开启」。
2. 「服务状态」验证循环：vsftpd/rclone-webdav/rclone-s3/nfs-kernel-server 四个服务
   inactive 且 disabled 时显示「○ 默认关闭」且**不得置 VERIFY_OK=false**（参考此前
   FileBrowser 退役时的条件化先例）；active 或 enabled 但 failed 仍如实 ✗。
   smbd/nmbd/fail2ban/nas-panel 的判定不变。

## Item C：后端 services 模块

1. `dashboard.NasServices` 的 ServiceDef 增加 `Optional bool` 字段：
   nfs-kernel-server、vsftpd、rclone-webdav、rclone-s3 = true；smbd/nmbd/fail2ban/nas-panel
   （及如有的其他核心项）= false。GetServices 返回的 JSON 带上 optional 字段。
2. `handleServiceAction` 增加两个复合动作：
   - `on`：`systemctl enable --now <name>`，随后 is-active 复核，返回 {message, active}
   - `off`：`systemctl disable --now <name>`，返回 {message, active}
   仅限 Optional=true 的服务可用（非 optional 服务调 on/off 返回 400，防误关 smbd）。
   成功路径调 `common.LogAuditRequest` 富 detail 审计（协议开关是安全相关操作）。
3. 单测：on/off 动作的 optional 门禁（非 optional 拒绝）、动作到 systemctl 参数的映射
   （如现有测试基建允许注入 SudoExec 则注入，否则把决策逻辑抽纯函数测）。

## Item D：dashboard 服务计数口径

现状：i18n `services_ok: "{0} 个服务 · 全部正常"` / `services_bad: "{0} 个服务 · {1} 个异常"`。
新装 4 协议关闭后不能显示"4 个异常"。

改法：计数口径 = **核心服务 + 已启用(enabled)的可选服务**：
- 可选服务 disabled → 不进分母、不算异常；
- 可选服务 enabled 但 stopped/failed → 算异常（用户开了就要好好跑）；
- 抽纯函数（如 servicesSummary(list) (total, bad)）+ 单测覆盖上述三态。
前端仪表盘处使用新口径（如前端自己算的，改前端；如后端给的，改后端——先找到现有计算点）。

## Item E：注册表条件化（system_registry.go）

四个协议服务项（ID 3 nfs / 4 vsftpd / 5 rclone-webdav / 7 rclone-s3）：
- 单元 disabled 且 inactive → **pass**，detail 注明「默认关闭，可在服务管理页开启」；
- enabled 或 active → 沿用现有真实检查（active 判活、端口等）；
- enabled 但 failed/inactive → fail（现状语义）。
配置项（13/14 vsftpd 配置、20/22 rclone unit）与软件包项 46 不变（文件照常生成）。
总项数保持 41，新装建池后仍必须能到 41/41 全 pass。
把判定抽成可测纯函数（enabled/active 两布尔入参 → status/detail），补单测。

## Item F：前端服务页人话卡片 + i18n

现状 index.html 438 行起是密集表格（每服务 5 个技术按钮）。改为两区：

1. **核心区**（optional=false：smbd/nmbd/fail2ban/nas-panel）：保留现有表格形态即可，
   按钮可精简为 重启/日志（去掉对核心服务的 stop/disable 误操作入口——核心服务不提供
   stop/disable 按钮）。
2. **协议服务区**（optional=true 四个）：卡片网格（2 列，沿用现有 CSS 变量与卡片样式），
   每张卡片：
   - 图标 + 服务名 + 用途一句话（i18n，文案见下）
   - 大开关（toggle）：调新 on/off 动作；开启成功后卡片展开显示「访问地址 + 凭据提示」
   - 状态徽章：运行中 / 已关闭 / 异常
   - 次级小按钮：重启、日志（沿用 svcAction/svcLogs 现有函数）
   - 卡片底部小字风险提示（FTP 明文/NFS 全网段，见文案）
3. i18n 文案（zh-CN / en-US 两份都要）：
   - WebDAV 用途：「第三方文件 App（手机/平板挂载工具等）接入公共目录，仅能看到 public 共享」
   - FTP 用途：「监控摄像头、老式设备等只能用 FTP 上传的场景」；风险：「密码明文传输，建议仅局域网使用」
   - NFS 用途：「Linux/Unix 设备与部分媒体播放器挂载共享」；风险：「局域网内所有设备可挂载，无按用户权限」
   - S3 用途：「备份软件与开发工具对接的对象存储接口」
   - 开启后地址提示：WebDAV `http://<IP>:8080`、FTP `ftp://<IP>`、NFS 客户端挂载 `<IP>:/public`、
     S3 `http://<IP>:9000`（IP 用现有前端拿主机 IP 的方式；凭据提示：WebDAV/FTP=面板用户密码，
     S3=安装时生成的 AK/SK——具体来源 grep setup.sh 里 auth-key 的存放位置如实写）
   - 开关确认文案：关闭时 toast「已关闭 <服务>，依赖它的设备将断开」
4. 「install_all / install_service」按钮逻辑保留（not-installed 态仍走原路径）。

## 验收标准（parent 将逐项复核）

- go build/vet/test 全过；新增单测覆盖：optional 门禁、注册表三态、dashboard 计数三态。
- bash -n + shellcheck（如可用）过；grep 确认 setup.sh 的 FRESH_INSTALL 判定在 .env 创建之前。
- install.sh 的 UNC 行（`\\\\\\\\${PRIMARY_IP}\\\\public` 形态）逐字未变。
- 前端 grep：四个 optional 服务名出现在卡片区 i18n 键中；核心服务表格无 stop/disable 按钮。
- 注册表总项数仍为 41（grep allRegistryItems 计数）。
- TASK-REPORT.md：改动文件清单、FRESH_INSTALL 判定点行号、三处口径（注册表/仪表盘/install 验证）
  的判定表、偏离与理由。
