# S1 最小权限实施计划（面板降权 fm + 安装以 fm 跑 + 白名单收紧）

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development / executing-plans，按 Task 逐个执行。

**Goal:** 把 7×24 常驻的 nas-panel 从 root 降为 NAS_USER(fm)，安装流程改为「用户身份跑 + 特权步骤逐条 sudo」，sudoers 白名单铲掉 root-equivalent 条目——面板 RCE 不再等于整机 root，仓库/数据对用户可见可管。

**Architecture:** 代码面侦察结论（2026-10-23 实测）：**Go 侧已 fm-ready**——模块内零直写系统路径（/etc、/opt、/var 全无 os.WriteFile 直呼），特权操作全走 common.SudoExec/SudoOutput（sudo 前缀对 root 是 no-op、对 fm 走白名单，身份无关）；非 sudo 的 exec.Command 仅 6 处且全为只读（ps/dpkg -s/sysctl -n/df/rclone）；rclone conf 已有 /root→$HOME fallback（rclone.go:170-180）；unit 模板已有 __NAS_USER__ 注入点；setup.sh 本就拒绝裸 root 运行。因此 S1 主战场在**脚本与属主迁移**，Go 代码只动 rclone conf 迁移辅助（如需）。

**Spec:** 用户拍板（2026-10-23）：「是不是可以直接用 fm 账号部署，需要 root 的地方才用 root」+ 我方两步走建议（先铲 sed -i 后门【已完成 2e0ab8c】，再 S1 降权，排 F3 之前）。

## Global Constraints

- 新增 Go 依赖 0；单二进制形态不变；`go build/vet/test ./... -race` 常绿；架构边界测试不破。
- **白名单原则**：禁止 root-equivalent 通配（能用它改写 /etc/shadow、sudoers、任意系统文件的就是后门——sed -i * 已铲，本轮同标准审余下条目：chown -R *、mount *、openssl -out 任意路径、apt-get upgrade（postinst 任意码）等逐条判定：可收窄的收窄到具体路径/参数，功能必需的在计划里**明示残留风险并记录拍板**，不静默放过）。
- 属主迁移必须**幂等**且覆盖升级路径：老机器（root 面板 + root 属主数据）跑 apply-upgrade.sh / setup.sh 后自动迁移到 fm 属主，不丢数据、不重置密码。
- 安装命令文案变更是用户可见面：`curl -fsSL https://get.z1.sale/install.sh | bash`（去 sudo 前缀）必须六载体同步（README 中英 / docs/release-system.md / install.sh 头注释 / 官网 download.md 中英），中英命令逐字一致。
- install.sh 铁律不破：无交互式 read（curl|bash 下 stdin 是脚本自身）；sudo 提权用开头 `sudo -v` 缓存 + 逐命令 sudo 前缀；NAS_PASS 环境变量路径保留。
- 改 install.sh/setup.sh 后必须跑 scripts/upload-r2.sh 重传线上（脚本分发独立于 release，今天刚实踩过陈旧 install.sh 的坑）。
- TDD 适用面：Go 改动先红后绿；shell 改动以「bash -n + shellcheck（如可用）+ 真机验收」为准；每 Task 单独提交。
- 验证只用仓库内工具与测试机；不 git add 任务书/报告。

## 代码现实锚点

- `configs/nas-panel.service`：模板含 `Environment=NAS_USER=__NAS_USER__`，**无 User= 行**；setup.sh:542-544 sed 替换后装到 /etc/systemd/system。
- `scripts/setup.sh`：NAS_USER=SUDO_USER 检测（68-72 行拒绝裸 root）；sudoers 全量重写（531 行 `> "$SUDOERS_FILE"`）+ 564 行 sed -i 条目已铲（2e0ab8c）；333/440 行 rclone-webdav/s3 已是 User=$NAS_USER；`chown $NAS_USER /var/lib/nas-monitor` 已有。
- `scripts/install.sh`：REPO_DIR="$HOME/soft/nas"（root 下跑=/root/soft/nas，fm 用户看不到）；146 行 sed 注入 NAS_PASS 到 $REPO_DIR/.env；二进制装 /usr/local/bin/nas-panel。
- `web/modules/rclone/rclone.go:170-195`：rcloneConfPath=/root/.config/rclone/rclone.conf，getRcloneConf() Stat 失败 fallback 到 os.UserHomeDir()；rcloneCmdSudo 已存在。
- `web/common/sudo.go`：SudoExec/SudoOutput/SudoExecStdin 统一 sudo 前缀。
- 数据面：/opt/nas/data/*.db 现 root 属主；池文件/回收站经 SMB force user 实际全 fm 属主（降权后文件操作语义反而更顺）。
- `/etc/nas/certs`（HTTPS 证书）：面板启动要**读**——降权后需 fm 可读（644 或 chown fm，私钥 640 root:fm 亦可，计划里定）。
- 测试机现状：fm 有 `(ALL:ALL) NOPASSWD: ALL`（装机镜像带的，非本仓脚本）——验收前必须收掉，否则测不出白名单真实约束。

---

### Task 1: 面板服务降权 fm（unit + 属主迁移 + rclone conf 迁移）

**Files:**
- Modify: `configs/nas-panel.service`（加 `User=__NAS_USER__` / `Group=__NAS_USER__`）
- Modify: `scripts/setup.sh`（属主迁移段：/opt/nas 实体（readlink 解析）+ data/ + .env + /var/lib/nas-monitor 幂等 chown fm；rclone conf 迁移 /root/.config/rclone/rclone.conf → ~NAS_USER/.config/rclone/（存在才迁，chown fm，600）；/etc/nas/certs 可读性处理）
- Modify: `scripts/apply-upgrade.sh`（同一迁移逻辑复用/调用，老机器升级路径自动降权）
- Test: 真机（parent 验收轮）；仓库内 `bash -n` + go 全套回归

**要点:**
1. unit 加 User/Group 后 `systemctl daemon-reload && systemctl restart nas-panel`；面板监听 :8090（>1024 非特权口，无 CAP 需求）。
2. 属主迁移幂等：`chown -R NAS_USER:NAS_USER "$(readlink -f /opt/nas)"` 级别操作前先判断当前属主，已是 fm 则跳过（升级重跑零副作用）。
3. .env 属主 fm + 600（含 NAS_PASS；面板 EnvironmentFile 以 fm 读）。
4. rclone conf 迁移后 getRcloneConf() 的 Stat(/root/...) 对 fm 恒 EACCES→fallback fm home，行为正确；迁移段把旧 conf 内容原样搬走（保留远端凭据）。
5. Go 侧如需感知运行身份（日志/健康页显示运行用户），最多加只读展示，不改行为。

**Commit:** `feat(s1): 面板服务降权NAS_USER — unit加User/Group+setup/apply-upgrade幂等属主迁移(data/.env/仓库实体)+rclone conf迁移+证书可读;常驻攻击面从root收敛到白名单`

---

### Task 2: install.sh 以 fm 身份跑（特权步骤逐条 sudo）+ 六载体文案同步

**Files:**
- Modify: `scripts/install.sh`（开头：检测 EUID≠0 时 `sudo -v`（一次性密码缓存）+ 后续特权命令统一 `sudo` 前缀封装函数 `SUDO`；EUID=0 直跑仍兼容（SUDO=空）；REPO_DIR 落在真实用户 home；二进制 install 到 /usr/local/bin 走 sudo；调 setup.sh 用 `sudo bash`（SUDO_USER 语义天然正确）；结尾服务状态/注册表检查的 curl 不需特权原样）
- Modify: `scripts/cleanup.sh`（同款 SUDO 封装，fm 身份可跑；root 直跑兼容）
- Modify: `README.md` / `README.zh-CN.md` / `docs/release-system.md` / install.sh 头注释 / 官网 `download.md` / `zh/download.md`（安装命令改 `curl -fsSL https://get.z1.sale/install.sh | bash`，自定义密码写法改 `curl … | env NAS_PASS=*** bash`——注意不再需要 sudo 前缀，六处逐字一致；官网两语言页同改）
- 注意：install.sh 里既有的 `sudo env NAS_PASS` 文档提示、NAS_PASS 缺省=出厂密码+首登改密路径全部保持语义不变。

**验证:**
1. `bash -n` 两脚本；root 直跑与 fm 跑双路径逻辑自查（EUID 分支）
2. **跑完必须 `bash scripts/upload-r2.sh` 重传线上 install.sh** 并 `curl -sL https://get.z1.sale/install.sh | md5sum` 对比本地一致（铁律）
3. 官网 md 改动：npx vitepress build 过 + dist 同 commit（nas-website 仓单独提交）

**Commit:** `feat(s1): 安装/清理脚本支持fm身份直跑 — sudo -v一次缓存+特权步骤逐条sudo,仓库落用户home可见可管;安装命令六载体同步去sudo前缀(curl|bash),root直跑兼容`

---

### Task 3: sudoers 白名单收紧 + 提权脚本审计

**Files:**
- Modify: `scripts/setup.sh`（白名单段重写）
- Audit: `scripts/add-user.sh`、`scripts/remove-user.sh`、`scripts/backup-config.sh`、`scripts/restore-config.sh`、`scripts/apply-upgrade.sh`、`scripts/backup-data.sh`（这些整脚本在白名单里，内部特权操作随脚本 root 化——审计注入面：变量是否都引号包裹、有无 eval/通配 rm、外部输入是否进命令拼接）
- Doc: 计划内「残留风险拍板表」

**收紧清单（逐条判定，以 grep 面板实际调用面为准）:**
1. `/bin/chown -R *` → 收窄为 `/bin/chown -R NAS_USER\:NAS_USER /data/*`、`/bin/chown -R * /opt/nas*`、`/bin/chown * /var/lib/nas-monitor*` 等实际用到的形状（先 grep SudoExec("chown"…) 全调用点定形状，再照形状写条目）
2. `/bin/mount *` / `/bin/umount *` → 对照调用点收窄（mount 仅 /data 目标/ fstab 源；无法安全收窄的条目记录残留风险）
3. `/usr/bin/openssl req|x509|rsa *` → -out 任意路径可覆写系统文件：收窄为面板证书实际命令形状（grep 调用点），或改走专用脚本（脚本整条入白名单，内部固定路径）
4. `apt-get upgrade *` / `update-initramfs` → postinst 任意码执行=准 root：属「系统更新」功能必需，记录残留风险拍板（家庭场景面板更新功能本就是高权能）
5. `useradd/usermod/gpasswd/passwd` → 用户管理功能必需；gpasswd 收窄到 `-a/-d 用户 nasusers` 形状（grep 实际调用）；usermod 禁 `-aG sudo` 形状（收窄组参数到 nasusers）
6. `/usr/bin/sed -i *` 已铲（2e0ab8c）——本 Task 复查其余条目同标准：「能否借此改写 /etc/shadow 或 /etc/sudoers.d？」能=收窄或删除
7. 白名单生成后 `visudo -cf` 校验语法；条目数与调用面 grep 对帐表进报告

**Commit:** `security(s1): sudoers白名单root-equivalence复审 — chown/mount/openssl/gpasswd/usermod按实际调用面收窄,提权脚本注入审计,残留风险拍板表入计划`

---

### Task 4: 文档与验收物料（parent 主导真机验收）

**Files:** `docs/feature-operation-checklist.md`（运行身份/安装方式条目更新）、`docs/nas-product-manual.md`（「面板以家庭管理员身份运行」用户视角说明+安全含义）、`CHANGELOG.md` 未发布段 S1 条目组、`docs/release-system.md`（安装命令+sudoers 原则）

**验收口径（parent 执行，双机）:**
1. **先收掉测试机 fm 的 `(ALL:ALL) NOPASSWD: ALL`**（临时 sudoers drop-in 删除，验收全程 fm 仅靠白名单+sudo 密码缓存）——这是 S1 验收的灵魂前置，不收等于没测
2. 纯 fm 一键安装：`curl … | bash`（无 sudo 前缀）走完 → `ps -o user= -C nas-panel` == fm → 仓库在 /home/fm/soft/nas 且 fm 可 git status → data 属主 fm → 首登改密 → 建池 → 注册表 41/41 → 套件 38 → AI/F2 E2E 全绿
3. **升级迁移路径**：另一台先装旧版（root 面板+root 属主）→ 跑新版 apply-upgrade/install → 断言进程身份变 fm、属主迁移完整、数据/密码/池零丢失、回收站与配额功能正常
4. 白名单负面测试：fm 面板下 `sudo cat /etc/shadow` 被拒、`sudo sed -i` 被拒、`sudo chown -R root:root /etc` 被拒（收窄生效）；正面：面板全部功能面过套件即证白名单够用
5. rclone 迁移：旧机 /root/.config/rclone/rclone.conf 有远端配置 → 升级后面板 rclone 页远端列表不丢
6. 线上 install.sh md5 == 仓库（upload-r2 后核验）

**Commit:** `docs(s1): 运行身份/安装方式文档同步+CHANGELOG未发布段S1条目组+验收口径固化`

---

## Self-Review 记录

- **侦察支撑**：Go 零直写系统路径/6 处只读 exec/rclone fallback/unit 注入点/setup 拒裸 root——全部 2026-10-23 实测于 master，锚点行号随文。
- **风险明示**：apt upgrade/update-initramfs/useradd 类条目功能必需但准 root（残留风险拍板表 T3 产出）；家庭 NAS 管理员面板本就是高权能产品，S1 目标是「消灭无谓 root 面 + 消灭死后门」，不是零特权。
- **回滚路径**：unit 去掉 User= 行 + 重跑 setup 即回 root 面板（数据 fm 属主对 root 面板无碍）；白名单旧版在 git 历史。
- **与 F3 关系**：S1 先行（用户已拍板）——F3 开机引导新增的特权调用面（建池/建号）从第一天就按白名单形状写，不欠新债。
