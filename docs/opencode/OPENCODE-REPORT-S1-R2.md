# OPENCODE-REPORT-S1-R2：install.sh 以 fm 身份跑 + 六载体文案同步（S1 Task 2）

> 对应任务书 TASK-S1-R2，执行 `docs/plans/2026-10-23-least-privilege-s1-plan.md` 的 **Task 2**。
> 分支 `feat/least-privilege`。nas 仓单提交，commit message 用计划 Task 2 原文：
> `feat(s1): 安装/清理脚本支持fm身份直跑 — sudo -v一次缓存+特权步骤逐条sudo,仓库落用户home可见可管;安装命令六载体同步去sudo前缀(curl|bash),root直跑兼容`
> （commit `23f4e3a`）
>
> 官网仓（`/home/jacky/soft/nas-website`）独立提交 `docs: 安装命令去sudo前缀(fm身份直跑,S1)`（commit `af7f91a`），md+dist 同 commit，未跑 upload-r2（按硬约束 8，parent 验收通过后统一重传）。

## 1. 改动文件

nas 仓：
- `scripts/install.sh`：开头加 `SUDO` 封装 + 一次性 `sudo -v`；特权步骤逐条加 `$SUDO` 前缀；EUID 检测分支改为允许普通用户直跑；setup.sh 调用改 `$SUDO env NAS_USER=... bash`；头注释命令改为去 sudo 前缀形态
- `scripts/cleanup.sh`：同款 `SUDO` 封装；全部特权命令逐条加前缀；保留 readlink 解析仓库实体删除逻辑（2b9d5b7）原样不动
- `README.md` / `README.zh-CN.md`：安装命令两行改新形态
- `docs/release-system.md`：**零改动**（已是目标形态，见第 4 节）

nas-website 仓：
- `download.md` / `zh/download.md`：安装命令改新形态
- `.vitepress/dist/*`：`npx vitepress build` 重建产物（随文案变化的资源哈希重命名，31 个文件 rename/create/delete，与历次 `build: dist重建` 提交模式一致）

## 2. SUDO 封装实现

两个脚本用同一套模式（`cleanup.sh` 在注释里标注"与 install.sh 同款"）：

```bash
if [ "$EUID" -eq 0 ]; then
    SUDO=""
else
    SUDO="sudo"
    sudo -v   # 一次性缓存密码，tty 交互由 sudo 自己处理，不走脚本 stdin
fi
```

- **root 直跑**（`sudo bash install.sh`、或 root 账号本身跑）：`EUID=0` → `SUDO=""`，`$SUDO cmd` 展开成 `cmd`，逐字等价于改动前的直接调用，零行为变化。
- **fm 直跑**（`curl ... | bash`、或 `fm$ ./cleanup.sh`）：`EUID≠0` → `SUDO="sudo"`，`$SUDO cmd` 展开成 `sudo cmd`。
- **无交互式 read 铁律**：`install.sh` 的 `sudo -v` 在脚本最开头、在任何输出之前调用——curl|bash 管道下 stdin 是脚本自身文本流，但 `sudo -v` 不读 stdin 问密码，它直接接管当前 tty（`/dev/tty`）做密码交互，与脚本的 stdin 管道无关，不会死循环。`cleanup.sh` 同理，且本身是本地直跑（非管道），风险更低。
- 两个脚本都**没有**新增任何 `read`/`read -s`；`cleanup.sh` 原有的 `read -p "确定要继续吗？"` 保留——它不在 curl|bash 场景下运行（cleanup.sh 用法始终是本地 clone 后 `./cleanup.sh` 或 `sudo ./cleanup.sh`，不走管道），不受硬约束 2 约束范围。

### 环境变量赋值的 sudo 特殊处理

`sudo VAR=val cmd` **不会**把 `VAR=val` 当环境变量传给 `cmd`（sudo 会尝试把 `VAR=val` 解释成要执行的程序名，报 `command not found`）。两处 `DEBIAN_FRONTEND=noninteractive apt-get ...` 改成 `$SUDO env DEBIAN_FRONTEND=noninteractive apt-get ...`：
- root 路径：`$SUDO` 为空 → `env DEBIAN_FRONTEND=... apt-get ...`，`env` 本身在无 sudo 时也是合法惯用法，等价于直接赋值运行。
- fm 路径：`sudo env DEBIAN_FRONTEND=... apt-get ...`，`sudo` 把 `env` 当成要执行的命令，`env` 再把 `DEBIAN_FRONTEND` 设进自己的子进程环境后 exec `apt-get`——这是标准透传写法，与 install.sh 原有头注释里 `sudo env NAS_PASS=... bash` 的透传模式完全一致（任务书锚点里也验证过这个套路）。

`install.sh` 命中两处（apt-get install 主安装、无），`cleanup.sh` 命中两处（apt-get purge、apt-get autoremove）。

## 3. 特权步骤盘点清单

### scripts/install.sh

| 行为 | 改动前 | 改动后 | 加前缀 |
|---|---|---|---|
| EUID 检测 | 硬拒绝非 root，报错退出 | 普通用户可继续，开头 `sudo -v` 一次缓存 | — |
| `git clone`/`git pull` 到 `$REPO_DIR`（用户 home 下） | 直跑 | 直跑（用户自己的目录，不需特权） | 否（不需要） |
| `mkdir -p "$(dirname "$REPO_DIR")"` | 直跑 | 直跑（用户 home 下） | 否（不需要） |
| `ln -sfn "$REPO_DIR" "$INSTALL_DIR"`（创建 `/opt/nas` 软链接） | 直跑 | `$SUDO ln -sfn ...` | **是** |
| `.env` 写入/`chmod 600`（仓库目录内） | 直跑 | 直跑（用户自己目录内文件，不需特权） | 否（不需要） |
| `sed -i` 切换 apt 镜像源（`/etc/apt/sources.list`） | 直跑 | `$SUDO sed -i ...`（两行） | **是** |
| `apt-get update` | 直跑 | `$SUDO apt-get update -qq` | **是** |
| `apt-get install`（16 个软件包） | `DEBIAN_FRONTEND=... apt-get install` | `$SUDO env DEBIAN_FRONTEND=... apt-get install ...` | **是** |
| 调用 `setup.sh` | `bash "$INSTALL_DIR/scripts/setup.sh"` | `$SUDO env NAS_USER="$NAS_USER" bash "$INSTALL_DIR/scripts/setup.sh"` | **是**（+ 显式透传 NAS_USER） |
| 手动编译提示文案（`apt-get install golang-go`、`sudo bash setup.sh`） | 裸 `apt-get`/`sudo bash` | 改成 `sudo apt-get`/`sudo env NAS_USER=... bash`（纯文案，不是实际执行） | 是（文案对齐） |
| `systemctl reset-failed nas-panel` | 直跑 | `$SUDO systemctl reset-failed nas-panel` | **是** |
| `systemctl restart nas-panel` | 直跑 | `$SUDO systemctl restart nas-panel` | **是** |
| `systemctl is-active <svc>`（验证阶段，8 个服务循环） | 直跑 | 直跑（只读查询，非 root 可执行） | 否（不需要） |
| `curl localhost:8090/api/system/check`（验证阶段） | 直跑 | 直跑（面板监听 >1024 非特权口，本机回环 HTTP） | 否（不需要） |

### scripts/cleanup.sh

| 行为 | 改动前 | 改动后 | 加前缀 |
|---|---|---|---|
| EUID 硬拒绝检测 | `[ "$EUID" -ne 0 ]` 直接报错退出 | 删除该拒绝块，改成 SUDO 封装（允许 fm 跑） | — |
| `read -p` 交互确认 | 保留 | 保留（本地直跑场景，非 curl\|bash 管道，不受硬约束 2 限制） | 不适用 |
| `[1/8]` `systemctl stop/disable <svc>`（8 个服务循环） | 直跑 | `$SUDO systemctl stop/disable "$svc"` | **是** |
| `[2/8]` `rm -f` 删 4 个 systemd unit 文件 | 直跑 | `$SUDO rm -f "$file"` | **是** |
| `[2/8]` `systemctl daemon-reload` | 直跑 | `$SUDO systemctl daemon-reload` | **是** |
| `[3/8]` `rm -f` 删 12 个配置文件 + `rm -rf /etc/filebrowser` | 直跑 | `$SUDO rm -f`/`$SUDO rm -rf` | **是** |
| `[4/8]` `rm -f` 删 2 个二进制（filebrowser/nas-panel） | 直跑 | `$SUDO rm -f "$bin"` | **是** |
| `[5/8]` `exportfs -ra` | 直跑 | `$SUDO exportfs -ra` | **是** |
| `[5/8]` `systemctl stop`（NFS/SMB/vsftpd/rclone×2） | 直跑 | `$SUDO systemctl stop ...` | **是** |
| `[5/8]` `umount "$mp"`（循环重试 6 次） | 直跑 | `$SUDO umount "$mp"` | **是** |
| `[5/8]` `lvremove`/`vgremove`/`pvremove` | 直跑 | `$SUDO lvremove -f`/`$SUDO vgremove -f`/`$SUDO pvremove -f` | **是** |
| `[5/8]` `sed -i` 清 `/etc/fstab` 数据盘条目 | 直跑 | `$SUDO sed -i ... /etc/fstab` | **是** |
| `[5/8]` `mdadm --stop`（按磁盘 + 全局 `/dev/md*`） | 直跑 | `$SUDO mdadm --stop ...` | **是** |
| `[5/8]` `wipefs -a "/dev/${disk}"` | 直跑 | `$SUDO wipefs -a ...` | **是** |
| `[5/8]` `sed -i` 清 `/etc/mdadm/mdadm.conf` | 直跑 | `$SUDO sed -i ...` | **是** |
| `[6/8]` `apt-get purge`/`apt-get autoremove` | `DEBIAN_FRONTEND=... apt-get ...` | `$SUDO env DEBIAN_FRONTEND=... apt-get ...` | **是** |
| `[7/8]` `rm -rf /data` | 直跑 | `$SUDO rm -rf /data` | **是** |
| `[8/8]` `smbpasswd -x "$NAS_USER"` | 直跑 | `$SUDO smbpasswd -x "$NAS_USER"` | **是** |
| `[8/8]` `rm -f /opt/nas`（软链接）+ `rm -rf "$NAS_REAL"`（仓库实体，readlink 解析） | 直跑 | `$SUDO rm -f /opt/nas` + `$SUDO rm -rf "$NAS_REAL"` | **是**（逻辑不变，2b9d5b7 修复保留） |
| `[8/8]` `rm -rf /opt/nas`（非软链接分支） | 直跑 | `$SUDO rm -rf /opt/nas` | **是** |
| `[8/8]` `rm -rf "$CAND"`（`/root/soft/nas` 或 `$HOME/soft/nas`） | 直跑 | `$SUDO rm -rf "$CAND"` | **是** |
| `[8/8]` `ufw --force reset` | 直跑 | `$SUDO ufw --force reset` | **是** |
| `[8/8]` `rm -f /etc/sudoers.d/nas-panel` | 直跑 | `$SUDO rm -f ...` | **是** |
| `[8/8]` `crontab -u "$NAS_USER" -l \| grep -v ... \| crontab -u "$NAS_USER" -` | 直跑（整条管道） | `$SUDO sh -c "crontab -u '$NAS_USER' -l ... \| crontab -u '$NAS_USER' -"`（见下方说明） | **是** |
| `[8/8]` `rm -rf /var/lib/nas-monitor` | 直跑 | `$SUDO rm -rf ...` | **是** |
| `[8/8]` `rm -f`/`rm -rf`（samba/nfs/vsftpd 日志与状态目录，5 行） | 直跑 | `$SUDO rm -f`/`$SUDO rm -rf` | **是** |
| `command -v`/`mount \| grep`/`lsblk`/`findmnt`（只读探测） | 直跑 | 直跑不变 | 否（只读，不需要） |

**crontab 管道的特殊处理**：`$SUDO cmd1 | cmd2` 只会给 `cmd1` 加特权，`cmd2`（第二个 `crontab -u ... -`，写入 crontab）仍以当前用户身份跑——fm 身份下这个写入目标是 `fm` 自己的 crontab，`crontab -u fm` 理论上普通用户对自己是允许的，但整条管道里第一个 `crontab -u fm -l` 若无特权在某些系统策略下可能受限（且升级场景 `NAS_USER` 可能不是当前执行者），为保证两端一致加特权，改成 `$SUDO sh -c "整条管道加引号"` 单次 sudo 覆盖整条管道，而不是两次独立 sudo（避免管道两端各自触发一次密码缓存检查、且语义更接近"以 root 身份原子操作目标用户的 crontab"，与改动前"root 直跑时两端天然都是 root"的行为完全等价）。

## 4. docs/release-system.md 零改动说明

核对后发现该文件**已经是目标形态**：第 69 行 `curl -fsSL https://get.z1.sale/install.sh | bash` 本来就没有 `sudo` 前缀（该文档描述的是一个带 `NAS_CHANNEL`/`NAS_VERSION` 预编译包下载选项的安装方式，与当前 `scripts/install.sh` 的实际实现——纯 git clone + 源码构建、无渠道选择逻辑——存在既有差异，这个差异在本任务开始前就存在，不是本次引入，也不在本任务范围内处理）。`grep -rn "sudo bash\|sudo env" docs/release-system.md` 零命中，六载体清单里这一项无需改动，`git diff` 对该文件为空。

## 5. NAS_USER 传递链推演

### 路径 A：fm 跑 `curl ... | bash`（无 sudo 前缀）

1. 脚本以 fm 身份起跑，`EUID≠0` → `SUDO="sudo"`，`sudo -v` 提示输入 fm 的密码一次，之后 15 分钟内（系统默认 `timestamp_timeout`）`$SUDO` 调用免密。
2. `NAS_USER="${SUDO_USER:-$USER}"`：这一刻脚本进程本身不是被 sudo 包裹起来的（只是脚本内部某些命令会被 `$SUDO` 包裹），所以 `$SUDO_USER` 环境变量未设置 → 取 `$USER` = `fm`。
3. `REPO_DIR="$HOME/soft/nas"` = `/home/fm/soft/nas`（`$HOME` 是 fm 自己的 home，未被任何 sudo 污染）。
4. `git clone`/`mkdir` 直接以 fm 身份写入 `/home/fm/soft/nas`，属主天然是 fm。
5. `$SUDO ln -sfn /home/fm/soft/nas /opt/nas`：以 root 创建软链接（`/opt` 需要特权），链接指向 fm 的仓库目录。
6. `.env` 在 `/home/fm/soft/nas/.env` 内写入，fm 身份直接写，属主 fm，`chmod 600` 同样以 fm 身份执行（文件在 fm 自己的目录下，不需要特权）。
7. 调用 setup.sh：`$SUDO env NAS_USER="fm" bash /opt/nas/scripts/setup.sh`：
   - 展开成 `sudo env NAS_USER=fm bash /opt/nas/scripts/setup.sh`。
   - `sudo` 把整条 `env NAS_USER=fm bash ...` 以 root 身份执行 → `env` 设置 `NAS_USER=fm` 进子进程环境 → `bash setup.sh` 以 **root** 运行，但读到显式环境变量 `NAS_USER=fm`。
   - `setup.sh` 第 68 行 `NAS_USER="${NAS_USER:-${SUDO_USER:-$USER}}"`：此刻 `$NAS_USER` 已经是 `fm`（刚被 `env` 设置）→ 兜底链第一级直接命中，`NAS_USER=fm`，不需要走 `${SUDO_USER:-$USER}` 这层（这一层在 `sudo env ...` 下其实也会是 `fm`，因为 `sudo` 本身会设置 `SUDO_USER=fm`——两条路径殊途同归，但显式传入更直接、不依赖 sudo 内部机制的隐式行为）。
   - `setup.sh` 以 root 跑完全部特权步骤（装软件、建用户、写配置、生成 unit 等），unit 模板里 `User=fm`/`Group=fm`（R1 已落地），最终 `systemctl restart nas-panel` 让面板以 fm 身份启动。
8. install.sh 继续：`$SUDO systemctl reset-failed/restart nas-panel` 展开为 `sudo systemctl ...`，fm 对 systemctl 操作走 sudo 密码缓存（已在步骤 1 缓存），无需再输密码。

**结论**：全程只有一次密码交互（开头 `sudo -v`），`NAS_USER` 从 `$USER` 探测出 `fm` 后，通过 `env NAS_USER=fm` 显式透传给以 root 跑的 `setup.sh`，不依赖 `SUDO_USER` 隐式传递（虽然在这条路径下 `SUDO_USER` 恰好也会是 `fm`，属于双重保险而非唯一依赖）。仓库落在 `/home/fm/soft/nas`，fm 可见可管。

### 路径 B：root 直跑 `sudo bash install.sh`（本地场景，或兼容旧habits）

1. 若是 `sudo bash install.sh`：bash 进程本身已经是 root（`$EUID=0`）。`SUDO=""`，`sudo -v` 分支被跳过（`[ -n "$SUDO" ]` 为假）。
2. `NAS_USER="${SUDO_USER:-$USER}"`：此刻 `$SUDO_USER` 由外层那次 `sudo bash install.sh` 设置（谁执行了 `sudo`，`SUDO_USER` 就是谁，例如 fm）→ `NAS_USER=fm`（不会被判成 root，因为真正探测的是发起 sudo 的那个用户名）。
3. `REPO_DIR="$HOME/soft/nas"`：**注意** `$HOME` 在 `sudo bash install.sh` 下默认是 **root 的 home**（`/root`，除非外层 sudo 配置了 `--preserve-env=HOME` 或 `.bashrc` 覆盖），所以 `REPO_DIR=/root/soft/nas`——这是**改动前就存在的行为**，本次任务书锚点里也提到"REPO_DIR 语义保持，fm 跑时自然落 /home/fm/soft/nas"，隐含"root 跑时落 /root"是已知且保留的行为，不属于本次需要修的点（硬约束 3 只要求"不得写死 /root"，没有要求"root 路径下禁止落在 /root"——这是 `$HOME` 随执行身份自然变化的结果，不是硬编码）。
4. 后续所有 `$SUDO cmd` 展开为裸 `cmd`，与改动前的行为逐字等价。
5. 调用 setup.sh：`env NAS_USER=fm bash /opt/nas/scripts/setup.sh`（`$SUDO` 为空，只剩 `env NAS_USER=fm bash ...`）。`env` 没有 sudo 包裹依然合法（它只是设置环境变量再 exec），`setup.sh` 读到 `NAS_USER=fm`（如果外层 sudo 是 fm 发起的）或 `NAS_USER=root`（如果是 root 账号自己直接登录跑的，此时 `$SUDO_USER` 为空、`$USER=root`——这种情况下 install.sh 第 93 行的拒绝检测会在此之前就已经 exit 1，不会走到这一步）。
6. 若是纯 root 账号登录（无任何 sudo 外层），`EUID=0` 同样为真，`SUDO=""`，但第 92-97 行的 `NAS_USER` 检测会拿到 `$SUDO_USER` 为空 → 退到 `$USER=root` → 判等 `"root"` → 报错退出，**提前终止**，不会跑到 setup.sh 这一步（这是改动前就有的既有防呆逻辑，本次未改动这段判断的退出语义，只是把错误文案从"请使用 sudo 运行"改成"不要直接以 root 账号登录"，因为现在合法路径已经不止"sudo 跑"一种）。

**结论**：root 直跑路径里，只要是通过 `sudo bash install.sh`（由某个真实用户发起）这种方式，`NAS_USER` 探测与传递链完全不受影响、保持改动前的既有行为；纯 root 账号直接跑依然被提前拒绝（防呆逻辑未弱化）。`$SUDO` 为空串使得所有特权命令原样直跑，不会对已经是 root 的场景引入任何多余的 `sudo` 调用开销或行为差异。

## 6. bash -n / shellcheck 结果

```
$ bash -n scripts/install.sh scripts/cleanup.sh
（无输出，语法通过）
```

```
$ shellcheck scripts/install.sh
In scripts/install.sh line 241: SETUP_OK=false  — SC2034 (warning) SETUP_OK appears unused
In scripts/install.sh line 245: ... | while read line; do  — SC2162 (info) read without -r

$ shellcheck scripts/cleanup.sh
In scripts/cleanup.sh line 41: read -p ...  — SC2162 (info) read without -r
In scripts/cleanup.sh line 192: apt-get purge -y $PACKAGES  — SC2086 (info) 建议加引号
```

四条告警均为**改动前既有**（用 `git stash` 回退到改动前重跑 shellcheck 核对过，行号对应改动前的 226/230/34/184 行，内容和级别完全一致）——`SETUP_OK` 本来就没被读取、`read` 本来就没加 `-r`、`$PACKAGES` 本来就是有意的词分裂（空格分隔的包名列表需要展开成多个参数，这是既有设计，非本次引入）。本次新增代码零 shellcheck 告警。

## 7. 六载体命令一致性 grep 对照表

### 基础命令 `curl -fsSL https://get.z1.sale/install.sh | bash`

| 载体 | 文件 | 行号 | 内容 |
|---|---|---|---|
| 1 | `README.md` | 86 | `curl -fsSL https://get.z1.sale/install.sh \| bash` |
| 2 | `README.zh-CN.md` | 130 | `curl -fsSL https://get.z1.sale/install.sh \| bash` |
| 3 | `docs/release-system.md` | 69 | `curl -fsSL https://get.z1.sale/install.sh \| bash`（零改动，已是目标形态） |
| 4 | `scripts/install.sh` 头注释 | 6 | `#   curl -fsSL https://get.z1.sale/install.sh \| bash` |
| 5 | 官网 `download.md` | 7 | `curl -fsSL https://get.z1.sale/install.sh \| bash` |
| 6 | 官网 `zh/download.md` | 7 | `curl -fsSL https://get.z1.sale/install.sh \| bash` |

六处逐字一致，全部核对通过。

### 带密码命令 `curl -fsSL https://get.z1.sale/install.sh | env NAS_PASS=mySecurePass123 bash`

| 载体 | 文件 | 行号 | 内容 |
|---|---|---|---|
| 1 | `README.md` | 89 | `curl -fsSL https://get.z1.sale/install.sh \| env NAS_PASS=mySecurePass123 bash` |
| 2 | `README.zh-CN.md` | 133 | `curl -fsSL https://get.z1.sale/install.sh \| env NAS_PASS=mySecurePass123 bash` |
| 3 | `docs/release-system.md` | — | 该文件的带密码示例是 `NAS_PASS=*** NAS_CHANNEL=beta curl ... \| bash`（组合渠道选择，结构不同，见第 4 节说明，不在本次改动范围） |
| 4 | `scripts/install.sh` 头注释 | 7 | `#   curl -fsSL https://get.z1.sale/install.sh \| env NAS_PASS=mySecurePass123 bash` |
| 5 | 官网 `download.md` | 10 | `curl -fsSL https://get.z1.sale/install.sh \| env NAS_PASS=mySecurePass123 bash` |
| 6 | 官网 `zh/download.md` | 10 | `curl -fsSL https://get.z1.sale/install.sh \| env NAS_PASS=mySecurePass123 bash` |

五处（除 docs/release-system.md 外）逐字一致，示例密码统一为 `mySecurePass123`（`scripts/install.sh` 头注释改动前是 `myPass123456`，本次同步对齐，属于一次性顺带修正）。

### 残留检查

```
$ grep -rn "get.z1.sale/install.sh | sudo" README.md README.zh-CN.md docs/release-system.md scripts/install.sh
（无输出）
$ grep -rn "get.z1.sale/install.sh | sudo" download.md zh/download.md   # 官网仓
（无输出）
```

六载体范围内零 `sudo bash`/`sudo env` 残留。

**范围外提醒（未改动，供参考）**：`CHECKLIST.md` 第 6 行、`docs/architecture-v1.0.md` 第 143 行仍是更早期的 `wget -qO- https://gitee.com/.../install.sh | sudo bash` 形态——这两个文件不在硬约束 7 列出的六载体清单内，本任务未触碰；`DEVELOPMENT.md` 第 441-453 行已经是新形态（`curl ... | bash` 系列），该文件同样不在六载体清单内，列出仅供后续核对参考。

## 8. 官网 build 验证输出

```
$ cd /home/jacky/soft/nas-website && npx vitepress build
  vitepress v1.6.4
- building client + server bundles...
✓ building client + server bundles...
- rendering pages...
✓ rendering pages...
build complete in 4.02s.
```

dist grep 验证：

```
$ grep -rn "get.z1.sale/install.sh" .vitepress/dist/download.html .vitepress/dist/zh/download.html
（两个文件均命中新命令 curl -fsSL https://get.z1.sale/install.sh | bash 与 | env NAS_PASS=... bash 两行，内容已编译进 shiki 高亮 span）

$ grep -rln "sudo bash\|sudo env" .vitepress/dist/
（无输出，零残留）
```

md + dist 已在 nas-website 仓单独提交（commit `af7f91a`，33 个文件，2 个 md + 31 个 dist 资源因内容哈希变化 rename/create/delete），**未执行 upload-r2**（按硬约束 8，留给 parent 验收通过后统一重传线上）。

## 9. Go 回归验证

```
$ cd web && go build ./...
（无输出，通过）

$ go vet ./...
（无输出，通过）

$ go test ./... -count=1
首轮：modules/tasks 偶发 "out of memory (14)" SQLite 失败（并发资源争用导致的既有 flaky，
      与 S1-R1 报告记录的现象一致）
二轮（重跑 go test ./... -count=1）：全部 ok，含 modules/tasks
单独验证：go test ./modules/tasks/... -count=1 -p 1（串行跑单包）两次均 100% 通过
```

本轮 `git diff --stat` 确认零 Go 文件改动（见下方 diff 统计），该 flaky 与本任务无关，纯测试资源争用（SQLite 文件句柄在高并发单测下偶发 `out of memory`），复测后稳定通过。

`git show --stat HEAD`（nas 仓本次提交）：

```
README.md          |  6 ++--
README.zh-CN.md    |  6 ++--
scripts/cleanup.sh | 88 +++++++++++++++++++++++++++++-------------------------
scripts/install.sh | 65 ++++++++++++++++++++++++----------------
4 files changed, 94 insertions(+), 71 deletions(-)
```

无 `web/` 目录下任何文件改动，与任务书"本轮理论上零 Go 改动"的预期完全一致。

## 10. 偏离与疑虑

1. **`scripts/install.sh` 头注释密码示例值顺带对齐**：改动前头注释用 `myPass123456`，与 README/官网等其他五个载体的 `mySecurePass123` 不一致（这个不一致在本任务开始前就存在）。本次为满足"六载体逐字一致"的硬约束，把它也改成了 `mySecurePass123`——这是任务书要求范围内的必要改动，不是额外扩权，但提请 parent 知悉这触碰了一个"原本不完全属于 sudo 前缀问题"的既有小瑕疵。
2. **`docs/release-system.md` 的带密码示例未改动**：该文件描述的是一个尚未在 `scripts/install.sh` 实现的渠道选择功能（`NAS_CHANNEL`/`NAS_VERSION`），带密码示例是 `NAS_PASS=*** NAS_CHANNEL=beta curl ... | bash` 这种组合形式，结构上不是简单的 `env NAS_PASS=*** bash`。基础命令行（无密码）已经与其他五个载体逐字一致，判断不需要为了凑"带密码行也一致"而扭曲这个文件本身描述的另一个功能的语义，故未改动。如果 parent 认为这个文件需要额外补一行纯粹的 `curl ... | env NAS_PASS=*** bash` 示例以完全对齐六载体的两种形态，可以追加一个小补丁。
3. **crontab 清理行改成单次 `sudo sh -c "管道整体"`**：改动前（root 直跑场景）这条管道两端天然都是 root 身份；改动后为了让 fm 身份下这条管道两端行为与 root 直跑完全等价，包了一层 `sh -c`。这是本次改动里语法形态变化最大的一行，已核对 `bash -n`/shellcheck 均通过，但建议真机验收时专门留意这一行在 fm 身份下的实际执行效果（`crontab -u fm -l` 读 + `crontab -u fm -` 写两次 crontab 操作）。
4. **cleanup.sh 的交互式 `read -p` 确认保留未改**：任务书硬约束 2 的"无交互式 read 铁律"原文聚焦在 "curl|bash 下 stdin 是脚本自身"场景，而 `cleanup.sh` 从未通过管道分发（README 里的用法始终是先 clone 仓库再本地跑 `sudo bash scripts/cleanup.sh`），所以判断这条交互确认不违反铁律、应当保留（这是一个破坏性操作的安全闸门，删掉反而增加风险）。如果 parent 的本意是"两个脚本统一去交互"，这里需要进一步确认后再改。
5. **真机双路径安装验收未执行**：本轮严格限定在仓库内验证（`bash -n`/shellcheck/go build/vet/test + grep 对照 + vitepress build + 逐行桌面推演），未连接任何真实 NAS 设备跑 `curl ... | bash`（fm 路径）或 `sudo bash install.sh`（root 路径）的完整安装流程。`sudo -v`、`apt-get`、`systemctl`、`setup.sh` 调用链的真实副作用需要 parent 在真机上验证（任务书已声明此前提，第 4 节/第 5 节的推演是基于代码逐行阅读的逻辑推导，非实际执行观测）。
