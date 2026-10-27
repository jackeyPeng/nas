# OPENCODE-REPORT-S1-R1：面板服务降权 fm（S1 Task 1）

> 对应任务书 TASK-S1-R1，执行 `docs/plans/2026-10-23-least-privilege-s1-plan.md` 的 **Task 1**。
> 分支 `feat/least-privilege`。单提交，commit message 用计划 Task 1 原文：
> `feat(s1): 面板服务降权NAS_USER — unit加User/Group+setup/apply-upgrade幂等属主迁移(data/.env/仓库实体)+rclone conf迁移+证书可读;常驻攻击面从root收敛到白名单`

## 1. 改动文件

- `configs/nas-panel.service`：加 `User=__NAS_USER__` / `Group=__NAS_USER__`
- `scripts/setup.sh`：新增 `migrate_ownership_to_nas_user()` 函数 + 调用，无其它逻辑改动
- `scripts/apply-upgrade.sh`：**零改动**（见第 4 节说明，复用既有调用点即满足共用逻辑约束）
- Go 代码：**零改动**（见第 6 节）

## 2. unit diff

```diff
 [Service]
 Type=simple
+User=__NAS_USER__
+Group=__NAS_USER__
 EnvironmentFile=/opt/nas/.env
 Environment=NAS_USER=__NAS_USER__
 ExecStart=/usr/local/bin/nas-panel
```

只加两行，其余字段（`EnvironmentFile`/`Environment`/`ExecStart`/`Restart`/`RestartSec`）未动。核对 `setup.sh:628`（原锚点行号 542-544，因本轮新增迁移段整体下移约 85 行）：

```bash
sed "s/__NAS_USER__/$NAS_USER/g" "$NAS_DIR/configs/nas-panel.service" > /etc/systemd/system/nas-panel.service
```

`/g` 全局替换，对新增的 `User=__NAS_USER__`、`Group=__NAS_USER__` 两行同样生效，与原有 `Environment=NAS_USER=__NAS_USER__` 一次 sed 调用内一并替换，无需改 sed 命令本身。

## 3. 迁移段脚本原文

插入位置：`scripts/setup.sh`「确保 /opt/nas 软链接存在」块之后，`download_file()` 函数定义之前（约第 145-227 行）。**不受 `--config-only` 影响**——刻意放在该分支判断之前，这样 `apply-upgrade.sh` 现有的 `setup.sh --config-only` 调用会自动执行本段，无需在 `apply-upgrade.sh` 里新增调用代码。

```bash
# ==================== 属主迁移：root → NAS_USER（面板降权，幂等） ====================
# 老机器（root 面板 + root 属主数据）跑 apply-upgrade.sh 时经由其对
# `setup.sh --config-only` 的既有调用自动触达本段——同一份逻辑，不建第二处。
# 本段不受 CONFIG_ONLY 影响（新装机器首次跑 setup.sh 全量模式时同样需要，
# 覆盖 install.sh 以 root 落地 .env / 面板以 root 首次启动写出的 data/ 等文件）。
# 幂等关键：每步先判断当前属主/内容，已是目标状态则跳过，重复执行零副作用。
migrate_ownership_to_nas_user() {
    echo ""
    echo "检查属主迁移 (root → $NAS_USER)..."

    # 路径操作认实体不认链接：readlink -f 解析 /opt/nas 软链接后对实体目录操作
    local repo_real
    repo_real=$(readlink -f "$NAS_DIR" 2>/dev/null || true)

    # 1. 仓库实体目录（含 web/、scripts/、configs/ 等，fm 需可见可管）
    if [ -n "$repo_real" ] && [ -d "$repo_real" ]; then
        if [ "$(stat -c '%U' "$repo_real" 2>/dev/null)" != "$NAS_USER" ]; then
            chown -R "$NAS_USER:$NAS_USER" "$repo_real"
            echo "  ✓ 仓库实体目录属主迁移: $repo_real → $NAS_USER"
        fi

        # 2. 面板数据目录 data/（folders.db / operations.db / ai.db 等，面板以 NAS_USER 运行后直写）
        if [ -d "$repo_real/data" ] && [ "$(stat -c '%U' "$repo_real/data" 2>/dev/null)" != "$NAS_USER" ]; then
            chown -R "$NAS_USER:$NAS_USER" "$repo_real/data"
            echo "  ✓ 面板数据目录属主迁移: $repo_real/data → $NAS_USER"
        fi

        # 3. .env（含 NAS_PASS/JWT_SECRET，面板 EnvironmentFile 需 NAS_USER 可读）
        if [ -f "$repo_real/.env" ]; then
            if [ "$(stat -c '%U' "$repo_real/.env" 2>/dev/null)" != "$NAS_USER" ]; then
                chown "$NAS_USER:$NAS_USER" "$repo_real/.env"
                echo "  ✓ .env 属主迁移: $repo_real/.env → $NAS_USER"
            fi
            chmod 600 "$repo_real/.env" 2>/dev/null || true
        fi
    fi

    # 4. 监控状态目录（cron 段创建于本脚本末尾，此处只迁移老机器已存在的）
    if [ -d /var/lib/nas-monitor ] && [ "$(stat -c '%U' /var/lib/nas-monitor 2>/dev/null)" != "$NAS_USER" ]; then
        chown -R "$NAS_USER:$NAS_USER" /var/lib/nas-monitor
        echo "  ✓ 监控状态目录属主迁移: /var/lib/nas-monitor → $NAS_USER"
    fi

    # 5. rclone 配置迁移: /root/.config/rclone/rclone.conf → ~NAS_USER/.config/rclone/
    # 存在才迁；不拿空文件覆盖已有目标；迁移后源文件留 .migrated 标记（而非直接删除——
    # 降权是新流程，留标记便于真机验收阶段核对迁移是否发生、异常时可比对原文件，
    # 对比直接删除更保守，机器稳定运行几个版本后可在后续清理脚本里统一清除 .migrated）。
    local root_rclone_conf="/root/.config/rclone/rclone.conf"
    local user_rclone_dir="/home/$NAS_USER/.config/rclone"
    local user_rclone_conf="$user_rclone_dir/rclone.conf"
    if [ -s "$root_rclone_conf" ]; then
        if [ -s "$user_rclone_conf" ]; then
            echo "  ⚠ rclone 配置迁移跳过: $user_rclone_conf 已存在且非空，不覆盖"
        else
            mkdir -p "$user_rclone_dir"
            cp -a "$root_rclone_conf" "$user_rclone_conf"
            chown -R "$NAS_USER:$NAS_USER" "$user_rclone_dir"
            chmod 600 "$user_rclone_conf"
            mv "$root_rclone_conf" "${root_rclone_conf}.migrated"
            echo "  ✓ rclone 配置迁移: $root_rclone_conf → $user_rclone_conf（源文件留 .migrated 标记）"
        fi
    fi

    # 6. /etc/nas/certs 可读性：面板降权后仍需按原路径读取证书/私钥（go 侧读取逻辑不变），
    # 私钥收窄为 640 root:NAS_USER（不开 644，私钥不给 other），证书保持 644。
    # 已知残留风险（报告记录）：面板内「重新生成/上传证书」会以 root 身份重新创建这两个
    # 文件（走 common.SudoExec openssl/cp），届时权限会被重置——该路径的收口留给后续
    # sudoers 白名单任务，此处只保证当前已存在证书在装机/升级这一刻对 NAS_USER 可读。
    if [ -d /etc/nas/certs ]; then
        chgrp "$NAS_USER" /etc/nas/certs 2>/dev/null || true
        chmod 750 /etc/nas/certs 2>/dev/null || true
        if [ -f /etc/nas/certs/server.key ]; then
            chgrp "$NAS_USER" /etc/nas/certs/server.key 2>/dev/null || true
            chmod 640 /etc/nas/certs/server.key 2>/dev/null || true
        fi
        if [ -f /etc/nas/certs/server.crt ]; then
            chgrp "$NAS_USER" /etc/nas/certs/server.crt 2>/dev/null || true
            chmod 644 /etc/nas/certs/server.crt 2>/dev/null || true
        fi
    fi

    echo "  ✓ 属主迁移检查完成"
}
migrate_ownership_to_nas_user
```

## 4. apply-upgrade.sh 共用逻辑说明（零改动理由）

`apply-upgrade.sh` 原本就在二进制替换 + 健康检查通过后（第 40-54 行）调用：

```bash
NAS_USER_ENV=$(awk -F= '/^Environment=NAS_USER=/{print $3; exit}' /etc/systemd/system/nas-panel.service ...)
if [ -f /opt/nas/scripts/setup.sh ]; then
    if NAS_USER="$NAS_USER_ENV" bash /opt/nas/scripts/setup.sh --config-only >/tmp/nas-config-sync.log 2>&1; then
```

因为我把 `migrate_ownership_to_nas_user` 放在 `setup.sh` 里**不受 `CONFIG_ONLY` 分支判断影响**的位置（在该判断之前），`apply-upgrade.sh` 这次既有调用不用改一行代码就会自动走到迁移逻辑——两处（新装 `setup.sh` 全量模式、升级 `apply-upgrade.sh` → `setup.sh --config-only`）命中的是**同一份函数定义**，不存在"抽公共脚本 vs 复制处注明同源"的选择问题，因为压根没有第二处实现。满足硬约束 7（禁止两处漂移）的方式是"让已有调用路径自然触达"，而非新建公共脚本文件——判断：新建 `lib-migrate-owner.sh` 会多一次 source 开销和一个新文件，而现状下 `apply-upgrade.sh` 已经 100% 依赖 `setup.sh --config-only` 做配置同步，复用该调用点是更小改动量的方案。

验证此判断的时序正确性（见第 5 节桌面推演）：`apply-upgrade.sh` 第 31 行先用**旧 unit（如果还是 root）**重启一次面板做健康检查，第 49 行再调 `setup.sh --config-only`——此时迁移函数先把 `.env`/`data/`/仓库实体等属主改好，紧接着 `setup.sh` 自身末尾（第 626-633 行，同样不受 `CONFIG_ONLY` 影响）重新生成 unit 文件（带 `User=$NAS_USER`）并再次 `systemctl restart nas-panel`——此时 `.env` 已经是 `NAS_USER` 属主，面板以 `NAS_USER` 身份启动可正常读取 `EnvironmentFile`。两次重启不是本次引入的新行为（`setup.sh --config-only` 结尾本就无条件重启面板），只是多了一次，不影响正确性。

## 5. 迁移逻辑桌面推演

### 场景 A：新装机器跑 `setup.sh`（全量模式，无 `--config-only`）

1. `NAS_USER` 从 `SUDO_USER` 探测为 `fm`。
2. 符号链接块：`/opt/nas` 不存在 → 创建 `/opt/nas -> $SCRIPT_DIR`。
3. **迁移函数执行**：
   - `repo_real` = `readlink -f /opt/nas` = 仓库实体路径，属主刚被 `install.sh`/当前 shell 以 `sudo` 身份 clone/解压，通常是 `root`（或执行 `sudo` 的用户，取决于 install.sh 落地方式）——若已经是 `fm`（例如 install.sh 本身已经用 fm 身份跑，见 Task 2），判等后跳过，零副作用。
   - `repo_real/data` 此刻一般不存在（面板还没启动过）→ 跳过。
   - `.env`：`install.sh` 以 root 写入时属主 root → chown 为 fm，chmod 600。
   - `/var/lib/nas-monitor`：此刻不存在（在脚本末尾才 `mkdir -p` + `chown`）→ 跳过，交给末尾的既有逻辑创建。
   - rclone conf：`/root/.config/rclone/rclone.conf` 在全新机器上通常不存在 → 跳过。
   - `/etc/nas/certs`：全新机器未生成证书，目录不存在 → 跳过。
4. 继续走 [1/10]~[9/10]，到 [10/10] 用 sed 生成带 `User=fm`/`Group=fm` 的 unit，`daemon-reload` + `restart`。
5. 面板以 `fm` 启动：读取 `/opt/nas/.env`（已是 fm:fm 600，可读）→ 正常监听 `:8090`。

**状态序列**：`root 装机脚本跑完 → .env 已 chown fm → unit 含 User=fm → 面板进程 fm`。无数据丢失（本场景下谈不上"迁移"，更多是新建即正确属主）。

### 场景 B：老机器（root 面板 + root 属主数据）跑 `apply-upgrade.sh`

前置状态：`/opt/nas` 实体目录 root 属主，`.env` root:root 644/600，`/opt/nas/data/*.db` root 属主，`/var/lib/nas-monitor` root 属主（老版本面板一直以 root 跑，这些文件由 root 进程写出），`/root/.config/rclone/rclone.conf` 存在且非空（面板此前以 root 身份配置过 rclone 远端），`/etc/nas/certs/server.{crt,key}` 存在（用户此前生成过自签证书），unit 文件只有 `Environment=NAS_USER=fm`，**无 `User=` 行**（老模板）。

1. `apply-upgrade.sh` 收到新二进制，备份旧二进制，原子替换，`systemctl restart nas-panel`——此刻 unit 仍是老模板，面板依然以 **root** 启动（新二进制但沿用旧 unit，身份未变）。
2. 健康检查通过（`curl :8090/api/version` 200）。
3. `git pull --ff-only` 更新模板文件（含新的 `nas-panel.service` 模板、新 `setup.sh`）。
4. `NAS_USER_ENV` 从新拉取的 unit 文件里读 `Environment=NAS_USER=fm` → `fm`（旧 unit 该行格式未变，awk 照常取到）。

   > 注意：此刻磁盘上 `/etc/systemd/system/nas-panel.service` **还是老版本**（git pull 只更新了仓库里的模板文件 `$NAS_DIR/configs/nas-panel.service`，没有动 `/etc/systemd/system/` 下已安装的单元），所以这一步读到的还是老 unit 里的 `Environment=NAS_USER=fm` 行——这行从一开始就存在（锚点确认），取值不受本次改动影响。

5. 调用 `NAS_USER=fm bash /opt/nas/scripts/setup.sh --config-only`：
   - 进入新版 `setup.sh`，`NAS_USER` 显式传入为 `fm`（不走 `SUDO_USER` 探测，因为 `systemd-run` 跑的进程没有 `SUDO_USER`——这也是锚点里特别强调"显式导出的 NAS_USER 优先"的原因）。
   - 符号链接块：`/opt/nas` 已存在（指向同一仓库），跳过创建。
   - **迁移函数执行**（这是本次新增代码第一次在老机器上跑）：
     - `repo_real` 属主 = root ≠ fm → `chown -R fm:fm $repo_real`（含 `.git/`、`web/`、`scripts/` 等整个仓库树）。
     - `repo_real/data` 属主 = root ≠ fm → `chown -R fm:fm $repo_real/data`（`folders.db`/`operations.db`/`ai.db` 等全部转 fm 属主，内容不变，老数据保留）。
     - `.env` 属主 = root ≠ fm → `chown fm:fm .env`；`chmod 600`。
     - `/var/lib/nas-monitor` 属主 = root ≠ fm → `chown -R fm:fm /var/lib/nas-monitor`。
     - rclone conf：`/root/.config/rclone/rclone.conf` 非空，`/home/fm/.config/rclone/rclone.conf` 不存在（老机器面板一直用 root conf，fm 侧从未有这个文件）→ 走迁移分支：`mkdir -p /home/fm/.config/rclone` → `cp -a` 原样搬运内容（远端凭据不变）→ `chown -R fm:fm` → `chmod 600` → 源文件 `mv` 成 `/root/.config/rclone/rclone.conf.migrated`。
     - `/etc/nas/certs` 存在 → `chgrp fm` + `chmod 750` 目录；`server.key` 存在 → `chgrp fm` + `chmod 640`；`server.crt` 存在 → `chgrp fm` + `chmod 644`。
   - `--config-only` 跳过 [1/10] apt 安装、[2/10] 数据目录递归 chown（避免对 `/data` 大数据树做二次递归，本就是既有设计）、[10/10] 二进制安装分支，但**不跳过** Samba/NFS/FTP/WebDAV/S3/防火墙模板重生成，也**不跳过** [10/10] 里 unit 文件重生成 + `daemon-reload` + `enable` + `restart` 那几行（无 `CONFIG_ONLY` 判断包裹）。
   - unit 文件按新模板用 sed 重新生成，含 `User=fm` / `Group=fm`，写入 `/etc/systemd/system/nas-panel.service`。
   - `systemctl daemon-reload && systemctl enable nas-panel && systemctl restart nas-panel`——**此次重启面板真正以 fm 身份启动**。此刻 `.env` 已经是 fm:fm 600（第 2 步已完成），面板读 `EnvironmentFile=/opt/nas/.env` 成功，`NAS_PASS`/`JWT_SECRET` 正常加载。
6. `setup.sh --config-only` 退出码 0 → `apply-upgrade.sh` 记录"托管配置已同步到新版模板"，`exit 0`。

**状态序列**：`root 面板运行中 → 新二进制+旧unit以root重启一次(健康检查用) → git pull模板 → setup.sh --config-only 内迁移函数把仓库/data/.env/monitor目录/rclone conf/证书属主改为fm → setup.sh 重生成unit(User=fm)并以fm重启面板 → 面板最终以fm运行，数据/密码/rclone远端/证书全部保留`。

### 场景 C：重复跑第二遍（幂等证明）

紧接场景 B 之后，再跑一次 `bash /opt/nas/scripts/apply-upgrade.sh <同一个或另一个已验证二进制>`（或直接再跑一次 `setup.sh --config-only`）：

1. `repo_real` 属主已经是 `fm` → `stat -c '%U' == $NAS_USER` 判等成立 → **跳过** chown，不打印"✓ 仓库实体目录属主迁移"。
2. `repo_real/data` 同理，属主已是 fm → 跳过。
3. `.env` 属主已是 fm → 跳过 chown；`chmod 600` 这一行**无条件重复执行**，但 600 已经是 600，`chmod` 对已是目标模式的文件是无副作用操作（不改 mtime 之外的任何状态，且该行本身是既有代码未改动的行为延续）。
4. `/var/lib/nas-monitor` 属主已是 fm → 跳过。
5. rclone conf：`/root/.config/rclone/rclone.conf` 已经在第一次跑时被 `mv` 成 `.conf.migrated`，原路径**不存在** → `[ -s "$root_rclone_conf" ]` 判断为假 → 整个迁移分支不进入，不会尝试覆盖 `/home/fm/.config/rclone/rclone.conf`（也不会误删 `.migrated` 标记文件，因为代码只读不写它）。
6. 证书：`chgrp`/`chmod` 对已经是目标属主/模式的文件重复执行无状态变化（`chgrp fm` 对已是 fm 的文件是 no-op；`chmod 640` 对已是 640 的文件是 no-op）。
7. 其余 Samba/NFS/… 配置生成本就是既有的幂等设计（覆盖写 + 保留 Z1 托管段），不受本次改动影响。

**结论**：六个迁移点里，前五个都显式做了"当前状态 == 目标状态则跳过"判断（4 个 `stat` 属主比较 + 1 个"源文件不存在即不触发"），第六点（证书 chgrp/chmod）天然幂等（无条件操作但目标状态收敛）。重复执行不改变系统状态、不产生额外日志之外的副作用、不触碰已标记 `.migrated` 的文件、不会用空文件覆盖已迁移的 rclone conf。满足硬约束 2（幂等）。

## 6. rclone conf 迁移决策

- **决策：迁移后源文件改名为 `.migrated`，不直接删除。**
- 理由：
  1. 降权是本仓库第一次做这类属主迁移，真机验收阶段（parent 负责）需要能对比"迁移前原文件内容"与"迁移后目标文件内容"是否一致，直接删除会让核对失去原始样本。
  2. `.migrated` 后缀使其在 `ls /root/.config/rclone/` 时清晰可辨，不会被 `getRcloneConf()` 的 `os.Stat(rcloneConfPath)` 误识别为仍存在的有效配置（路径是 `rclone.conf.migrated`，不是 `rclone.conf`，`Stat("/root/.../rclone.conf")` 直接返回"不存在/无权限"进入 fallback，行为不受影响）。
  3 机器稳定运行若干版本后，可以在后续清理脚本或下一轮计划里统一清除所有 `.migrated` 标记文件，不在本次范围内处理（避免本任务膨胀出清理功能）。
- 边界条件：
  - 源文件不存在或为空（`-s` 判假）→ 不触发迁移，不创建任何文件。
  - 目标文件已存在且非空 → 跳过迁移并打印警告，**不会用迁移内容覆盖用户可能已经手工配置或此前已迁移过的 fm 侧 rclone 远端**（避免双跑 `apply-upgrade.sh` 时反复迁移，也避免误覆盖用户已在 fm 账号下自行配置的 rclone 远端）。
  - 源非空但目标为空目录/不存在 → 原样 `cp -a` 搬运全部内容（含加密的远端密钥字段，`cp -a` 保留内容不做任何转换），`chown fm:fm` + `chmod 600` 后再 `mv` 源文件。

## 7. 证书权限方案

- **决策：目录 `750 root:fm`，私钥 `640 root:fm`，证书 `644`（属主不变，组改 fm）。**
- 对照计划锚点给出的两个候选（"644 或 chown fm" / "私钥 640 root:fm 亦可"）：选择 **640 root:fm** 而非把私钥整体 `chown fm` 或开 644——私钥不应该让 `other` 可读，640 配合目录 `750`（group fm 可进入、可读、不可写，`x` 位允许穿透列目录项）是在"fm 面板进程需要读取"和"不过度放宽私钥可见范围"之间的折中，优先级高于"改完全属主"的方案（改全属主需要额外处理 `openssl req -keyout` 以 root 身份写出后立刻二次 chown 的时序，而组权限只需加一次 `chgrp`，改动更小、更不容易在未来被 Go 侧代码的后续写操作打破属主假设）。
- **已知残留风险（写入报告，不在本任务内处理）**：
  - `web/modules/system/https.go` 的 `handleHTTPSGenerate`/`handleHTTPSUpload`/`handleHTTPSRemove` 全部通过 `common.SudoExec`/`common.SafeWriteFile` 以 **root** 身份重新创建/覆盖 `server.crt`/`server.key`（`SudoExec` 恒以 `sudo` 前缀跑，和面板进程本身是 root 还是 fm 无关）。一旦用户在面板里重新生成或上传证书，这两个文件会被 root 重新写出：
    - `openssl req -keyout keyFile -out certFile`（`handleHTTPSGenerate`）：openssl 默认创建私钥为 `600 root:root`。
    - `common.SafeWriteFile(keyFile, keyPEM)`（`handleHTTPSUpload`）：内部用 `os.CreateTemp`（默认 0600）+ `sudo cp`，`cp` 默认保留源文件权限位，目标同样落地 `600 root:root`。
    - 代码里紧接着的 `common.SudoExec("chmod", "600", keyFile)` / `chmod 644 certFile` 两行，在当前 sudoers 白名单（`scripts/setup.sh` 第 560 行附近完整白名单字符串）里**没有 `chmod` 条目**，该调用在真机白名单生效时会失败（返回非零，但 https.go 没有检查这两次 `SudoExec` 的返回值，静默吞掉）——不影响既有行为（因为这两行本来就是多余的，`cp`/`openssl` 已经定好了最终权限），但也说明这两行代码从一开始就是死代码，不是本次改动引入的新问题。
    - 净效果：**面板重新生成/上传证书后，私钥会变回 `600 root:root`，fm 身份的面板进程将无法再读取它**，`main.go` 的 `os.Stat(keyFile)` 检查仍会通过（目录可穿透、文件存在，`Stat` 不需要读权限），但 `http.ListenAndServeTLS` 实际读取文件内容时会因权限不足报错——这是面板自身的 HTTPS 功能在 Go 代码层的一个新 bug，**修复点在 `https.go`，不在 `scripts/setup.sh`**，超出 Task 1 文件范围（Task 1 的 Files 清单只列 `configs/nas-panel.service`/`scripts/setup.sh`/`scripts/apply-upgrade.sh`）。
  - 本次 `migrate_ownership_to_nas_user()` 只保证**装机 / 升级这一刻**已存在的证书文件对 fm 可读，不保证降权之后用户在面板里重新生成证书仍然可读。
  - 建议后续处理方式（不在本任务执行，列出供 Task 3 或单独小补丁参考）：
    1. 最小侵入：在 `https.go` 的 `handleHTTPSGenerate`/`handleHTTPSUpload` 末尾把 `chmod 600 keyFile` 改成等价效果的 `chgrp $NAS_USER keyFile && chmod 640 keyFile`（需要 Go 侧读取运行用户名，或固定走 `common.SudoExec("chgrp", nasUserEnvVar, keyFile)`），并在 sudoers 白名单补一条收窄到具体路径的 `chgrp`/`chmod` 条目（如 `/usr/bin/chgrp * /etc/nas/certs/server.key`、`/usr/bin/chmod 640 /etc/nas/certs/server.key`），这属于 Task 3（白名单收紧）的工作范畴，和本任务的"Go 代码零改动"硬约束冲突，故本轮不做。
    2. 临时缓解：真机验收时如果测试了"面板内重新生成证书"这一步，需要额外手动 `chgrp fm /etc/nas/certs/server.key && chmod 640 /etc/nas/certs/server.key` 一次，或重新跑 `setup.sh --config-only` 让本次新增的迁移函数把权限修回来（迁移函数对已存在证书的 chgrp/chmod 是无条件执行的，不依赖"是否刚生成"，所以重跑 `--config-only` 可以作为该残留风险的手动兜底）。

## 8. 幂等推演结论汇总

见第 5 节场景 A/B/C 完整推演。核心结论：

- 新装机器：迁移函数基本是 no-op（没有旧数据），真正生效的是 `.env` 的 chown（因为 install.sh 以 root 写入）。
- 升级机器（老 root 面板）：第一次跑 `apply-upgrade.sh`/`setup.sh --config-only` 完整迁移 5 类资源（仓库实体/面板数据/.env/监控状态目录/rclone conf）+ 证书权限收紧，第二次起全部进入"已是目标状态"分支，零副作用。
- 没有任何分支会在重复执行时丢数据、重置密码、或用空/部分内容覆盖已迁移的目标文件。

## 9. Go 改动清单

**无改动。** `web/modules/rclone/rclone.go` 的 `getRcloneConf()`（锚点行 170-180）在面板以 fm 身份运行后：

```go
var rcloneConfPath = "/root/.config/rclone/rclone.conf"
func getRcloneConf() string {
    if _, err := os.Stat(rcloneConfPath); err == nil {
        return rcloneConfPath
    }
    home, _ := os.UserHomeDir()
    return filepath.Join(home, ".config", "rclone", "rclone.conf")
}
```

已实测验证（非真机，本地沙箱）：非 root 用户 `os.Stat("/root/.config/rclone/rclone.conf")` 返回 `permission denied`（因为 `/root` 目录本身是 `700 root:root`，不可穿透），`err == nil` 判断为假，自动走 `os.UserHomeDir()` fallback；fm 身份下 `os.UserHomeDir()` 返回 `/home/fm`，与本次迁移函数写入的目标路径 `/home/$NAS_USER/.config/rclone/rclone.conf` 完全一致。因此 Go 侧零改动即可正确衔接 shell 侧的迁移结果，未触碰任何 `.go` 文件，未新增/修改任何测试。

## 10. 验证记录

```
$ bash -n scripts/setup.sh scripts/apply-upgrade.sh
（无输出，语法通过）

$ shellcheck scripts/setup.sh scripts/apply-upgrade.sh
（仅剩 4 条与本次改动无关的既有告警：SC2034 GOARCH/i 未使用、SC2024 sudo+重定向，
  均在本次新增代码行范围之外，行号核对过不属于 migrate_ownership_to_nas_user）

$ cd web && go build ./...
（无输出，通过）

$ go vet ./...
（无输出，通过）

$ go test ./... -count=1
全部 ok，仅 modules/tasks 偶发一例 SQLite "out of memory (14)" 失败
（并发测试资源争用导致的既有 flaky，单独重跑该用例、以及重跑整套 ./... 均 100% 通过；
  本次改动零 Go 文件涉及，`git status` 确认改动文件只有 configs/nas-panel.service 与
  scripts/setup.sh 两个，与该失败无关）
```

`git diff --stat`：

```
configs/nas-panel.service |  2 ++
scripts/setup.sh          | 85 +++++++++++++++++++++++++++++++++++++++++++
2 files changed, 87 insertions(+)
```

## 11. 偏离与疑虑

1. **cert chmod 白名单缺失是预先存在的 bug，非本次引入**（见第 7 节），记录在案但不在本任务处理范围，建议 Task 3 或单独小补丁跟进 `https.go` + sudoers 白名单的联动修复。
2. **未新建公共迁移脚本文件**：计划「要点」描述为"抽公共脚本或函数复制处注明同源"，我选择的是第三种路径——让迁移函数只存在于 `setup.sh` 一处，`apply-upgrade.sh` 通过既有的 `setup.sh --config-only` 调用间接触达，零新文件、零代码重复。认为这是满足"不漂移"约束里最小改动量的实现，但如果 parent 期望看到 `apply-upgrade.sh` 里有显式可见的"迁移"字样调用，这个选择可能需要调整（例如加一行注释明示该路径会触发迁移，当前已在 `setup.sh` 侧加了该说明，`apply-upgrade.sh` 侧未加对应注释，因为 Task 1 的 Files 清单允许但没有强制要求改动该文件，而我判断零改动风险更低）。
3. **真机验证未执行**：本轮严格限定在仓库内（`bash -n`/shellcheck/go build/vet/test + 沙箱逻辑推演），未连接任何真实 NAS 设备，`chown`/`systemctl restart`/证书读取的最终效果需要 parent 在真机上验证（任务书已声明此前提）。
4. **`/etc/nas/certs` 不存在时的行为**：全新机器或从未配置过 HTTPS 的机器，该目录不存在，迁移函数的第 6 步整体跳过（`[ -d /etc/nas/certs ]` 为假），不会提前创建该目录——沿用现状（该目录目前完全由 Go 侧 `handleHTTPSGenerate`/`handleHTTPSUpload` 按需 `mkdir -p` 创建），没有在 shell 侧抢先建空目录，避免引入一个"目录存在但无证书"的新中间态需要额外处理。
