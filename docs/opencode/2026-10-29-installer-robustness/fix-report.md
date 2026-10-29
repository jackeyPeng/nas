# TASK-REPORT: apt 瞬时失败加固（F-1）+ install.sh 失败路径证据保全（F-2）

基线: HEAD f4bc9f7。只改 `scripts/install.sh`、`scripts/setup.sh`。未执行任何真实 install.sh/setup.sh/apt 命令，验证仅用 `bash -n` 语法检查 + shellcheck + 逐段自查。

## 1. sudoers 白名单形态核对结论（关键偏差申报）

任务描述假设 "apt-get 已在白名单：`/usr/bin/apt-get update`、`/usr/bin/apt-get install -y -qq *`、`/usr/bin/apt update`、`/usr/bin/apt upgrade *`"，并要求 install.sh 的改动不得突破这些通配形态，因为"install.sh 是 fm 用户经 sudo 逐条跑的"。

**实际核对**（`scripts/setup.sh` 内嵌 sudoers 段，`SUDOERS_FILE="/etc/sudoers.d/nas-panel"` 写出的那一行）确认：

- 该白名单文本里确实原样含有 `/usr/bin/apt-get update`（裸、无参精确形态）、`/usr/bin/apt-get upgrade *`、`/usr/bin/apt-get install -y -qq *`、`/usr/bin/apt update`、`/usr/bin/apt upgrade *` 这几条。
- 但这个白名单文件是给 **nas-panel 服务进程**（systemd 以 fm 运行）在运行期需要免密 sudo 执行系统管理命令用的，由 `setup.sh` 的 `[10/10]` 步骤 `echo "... >" "$SUDOERS_FILE"` 写出，与 **install.sh 自身的 apt-get 调用是两个完全不同的信任边界**：
  - `install.sh` 对特权命令的处理是 `SUDO="sudo"`（EUID≠0 时）+ 开头一次性 `sudo -v` 缓存凭据（第 104 行），这依赖 fm 用户**本身拥有常规 sudo 权限**（通常在 `sudo` 组），在凭据缓存有效期内任意 `$SUDO <cmd>` 都会被允许执行，**不经过** `/etc/sudoers.d/nas-panel` 这个 NOPASSWD 白名单的逐条匹配。
  - `/etc/sudoers.d/nas-panel` 这个文件在 install.sh 执行阶段**尚未生成**（它是 setup.sh 第 [10/10] 步才写出的），install.sh 调 `setup.sh` 都在这之前，逻辑上也不可能依赖它。
- 结论：**install.sh 里的 `$SUDO apt-get ...` 调用不受该白名单约束**，可以自由加 `-o DPkg::Lock::Timeout=60` 等参数而不影响任何 sudoers 匹配。
- `setup.sh` 自身的 4 处 apt-get 调用（见下方清单）全部是裸命令（无 `sudo` 前缀），因为 setup.sh 整体已经是以 root 身份运行（`install.sh` 用 `$SUDO env NAS_USER=... bash setup.sh` 调起，或用户直接 `sudo ./setup.sh`），同样不经过该白名单。
- 唯一可能受该白名单约束的 apt-get 调用是 **nas-panel Go 代码**里如果存在的 `SudoExec("apt-get", ...)` 调用（面板运行期），但本任务范围是 `scripts/install.sh` 与 `scripts/setup.sh`，未检索/改动 `web/` 下的 Go 代码，故此次改动对该白名单的实际匹配面没有任何影响，**未改动 setup.sh 内嵌的 sudoers 文本**（F-1 修法第 3 点"若需要补条目"——核对后判定不需要）。

因此本次两脚本的 `-o DPkg::Lock::Timeout=60` 参数插入，虽然确认不受白名单约束，仍按任务指示做了保守处理：**`apt-get update` 一律不加 `-o` 参数**（改用 `apt_retry` 重试包裹承担锁等待语义），**只在 `apt-get install` 上加 `-o DPkg::Lock::Timeout=60`**，与任务文中"update 的锁等待不能用 -o 参数，改用重试包裹"的判断保持一致，不依赖"不受白名单约束"这个结论本身（纵深防御：即使日后面板代码或白名单形态发生变化，当前改法也不会意外突破）。

## 2. apt_retry 落点清单

两脚本各自定义 `apt_retry` 函数（不跨文件共享，不引入外部依赖），函数体与任务指定一致（3 次重试、间隔 10s、失败提示走 stderr）。

### scripts/install.sh

| 行号（改后） | 原调用 | 改后调用 | 说明 |
|---|---|---|---|
| 71-83 | — | 新增 `apt_retry()` 函数定义 | 放在 `step_fail()` 之后、Banner 之前 |
| 230 | `$SUDO apt-get update -qq 2>/dev/null` | `apt_retry bash -c "$SUDO apt-get update -qq 2>/dev/null"` | 用 `bash -c` 包一层，使 `2>/dev/null` 只抑制 apt-get 自身输出，不影响 `apt_retry` 自己的重试警告（走 stderr，需可见） |
| 246 | `$SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \ ...` | `apt_retry $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq -o DPkg::Lock::Timeout=60 \ ...` | 加锁等待参数；整条命令含重试警告一起被外层 `> "$APT_LOG" 2>&1` 捕获进日志，诊断信息更完整 |

### scripts/setup.sh

| 行号（改后） | 原调用 | 改后调用 | 说明 |
|---|---|---|---|
| 59-71 | — | 新增 `apt_retry()` 函数定义 | 放在 root 权限检查之后、Banner 之前，确保先于 `migrate_ownership_to_nas_user()` 定义/调用 |
| 253 | `apt-get install -y acl >/dev/null 2>&1 \|\| true` | `apt_retry apt-get install -y acl >/dev/null 2>&1 \|\| true` | 保持原有"完全静默、最佳努力"语义不变（原本就全量吞掉输出），重试警告同样被吞，与原设计意图一致（该行本就容忍失败） |
| 296 | `apt-get update` | `apt_retry apt-get update` | [1/10] 主流程，真机实锤撞锁点 |
| 297 | `DEBIAN_FRONTEND=noninteractive apt-get install -y \ ...` | `DEBIAN_FRONTEND=noninteractive apt_retry apt-get install -y -o DPkg::Lock::Timeout=60 \ ...` | 验证过 `VAR=val apt_retry cmd...` 形式下 bash 会把环境变量赋值导出给函数调用期间（已用 `bash -c` 单测确认），`DEBIAN_FRONTEND` 正确透传到内部 `apt-get` |
| 477 | `apt-get install -y apache2-utils 2>/dev/null \|\| true` | `apt_retry apt-get install -y apache2-utils 2>/dev/null \|\| true` | 同 253 行，最佳努力语义不变 |

**未改动**：`scripts/setup.sh:559`（`sudo apt-get -f install -y >/dev/null 2>&1 || true`，rclone dpkg 残局修复的一次性尝试，已 `|| true` 容忍失败，且是 `-f install` 修复态而非常规 update/install，不在 F-1 范围内）。

## 3. 失败路径新行为说明（F-2）

`scripts/install.sh` "── 5-10. 运行 setup.sh ──" 段：

1. **grep 过滤模式补 `^E:`**：原 `grep -E '^\[|✓|✗|⚠|服务状态|部署完成|通过|失败|警告'` 改为追加 `|^E:`，apt/dpkg 典型错误行（`E: Unable to ...` 等）现在会被回显捕获，成功/失败路径均受益（grep 调用点在分支判断之前，对两条路径一视同仁）。
2. **失败路径不再删除日志**：原 `rm -f "$SETUP_LOG"` 替换为 `mv -f "$SETUP_LOG" "$FAILED_LOG"`（`FAILED_LOG="/tmp/nas-install-setup-$(date +%s).log"`，`mv` 失败兜底 `cp`），日志改名落盘而非销毁。
3. **提示文案改为如实描述**：原"完整日志已保留: 重新运行可查看"（实际已被 `rm -f` 删除，谎言）改为"完整日志: `<FAILED_LOG 真实路径>`"。
4. **追加原始日志尾部**：失败时在过滤回显之后，额外输出 `echo "  ── setup.sh 日志尾部（未过滤）──"; tail -30 "$FAILED_LOG" | sed 's/^/  /'`，即使 grep 过滤器仍有遗漏的错误行格式，用户也能直接看到原始尾部 30 行。
5. **成功路径不变**：`SETUP_OK = true` 分支仍然 `rm -f "$SETUP_LOG"`（未改动该行），不额外占用 `/tmp` 空间。

## 4. 验证输出

```
$ bash -n scripts/install.sh && bash -n scripts/setup.sh && echo SYNTAX_OK
SYNTAX_OK

$ command -v shellcheck >/dev/null && shellcheck -S error scripts/install.sh scripts/setup.sh || echo "shellcheck 不可用或无 error 级问题"
（无输出，rc=0 → 零 error 级问题；shellcheck 0.11.0 可用）

$ grep -n "apt_retry\|DPkg::Lock::Timeout" scripts/install.sh scripts/setup.sh | head -20
scripts/install.sh:75:apt_retry() {
scripts/install.sh:230:apt_retry bash -c "$SUDO apt-get update -qq 2>/dev/null"
scripts/install.sh:246:apt_retry $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq -o DPkg::Lock::Timeout=60 \
scripts/setup.sh:62:apt_retry() {
scripts/setup.sh:253:        apt_retry apt-get install -y acl >/dev/null 2>&1 || true
scripts/setup.sh:296:    apt_retry apt-get update
scripts/setup.sh:297:    DEBIAN_FRONTEND=noninteractive apt_retry apt-get install -y -o DPkg::Lock::Timeout=60 \
scripts/setup.sh:477:apt_retry apt-get install -y apache2-utils 2>/dev/null || true

$ grep -n "read -\?[^l]" scripts/install.sh | grep -vE "read line|read -r line|while read" || echo "NO_INTERACTIVE_READ"
NO_INTERACTIVE_READ
```

（额外自查：`shellcheck`（不带 `-S error`）对两脚本报出 3 条已存在于基线、与本次改动无关的 info/warning —— install.sh:281 的 `SC2162 read without -r`（既有 `while read line` 结构，本次只是在同一 grep 的模式串里追加了 `|^E:`，未改变该结构）、setup.sh:41 的 `SC2034 GOARCH unused`、setup.sh:553/560 的 `SC2024 sudo doesn't affect redirects`，均未改动所在代码，非本次引入。）

## 5. 偏差申报

1. **install.sh:230 的 `apt-get update` 调用引入了 `bash -c` 包装**，而非任务文本里 `apt_retry apt-get ...` 的直接形式。原因：原代码 `$SUDO apt-get update -qq 2>/dev/null` 的 `2>/dev/null` 是用来抑制 apt-get 自身输出噪音的；如果直接写成 `apt_retry $SUDO apt-get update -qq 2>/dev/null`，这个重定向会作用于整条 `apt_retry` 调用（包括函数内部 `echo ... >&2` 的重试警告），导致重试警告被一起吞掉，违反任务要求的"失败重试输出走 stderr（进 setup 日志）"。改用 `apt_retry bash -c "$SUDO apt-get update -qq 2>/dev/null"` 使重定向只作用于内层命令。已用本地 bash 单测验证该包装行为正确（内层命令失败时外层重试警告仍可见，3 次耗尽后返回 1）。
2. **`APT_RC` 不再是 apt-get 本身的退出码**：install.sh 第二处调用点（17 个软件包安装）原先 `|| APT_RC=$?` 捕获的是 `apt-get` 的真实退出码（用于 `step_fail "软件包安装失败 (exit $APT_RC)"` 文案），加入 `apt_retry` 包裹后，`$?` 变成 `apt_retry` 函数自身的返回值（成功 0 / 三次耗尽后恒为 1），不再是 apt-get 的原始退出码（如 100）。影响范围仅是失败提示文案里的数字从"真实 exit code"变成"恒为 1"，诊断信息不丢失（`tail -10 "$APT_LOG"` 紧跟在前，仍打印 apt-get 的真实错误文本；日志现在还会包含重试过程的 `⚠` 行）。未在任务约束里找到关于保留原始退出码数值的硬性要求，视为可接受的次要行为变化，特此申报。
3. **setup.sh:253 与 477 两处"静默最佳努力"调用点未做 `bash -c` 包装**，即 `apt_retry` 的重试警告在这两处仍会被外层 `>/dev/null 2>&1` / `2>/dev/null` 吞掉。判断依据：这两处在改动前就是完全静默、`|| true` 容忍任何失败的设计意图（acl 包、apache2-utils 包的辅助安装，不影响主流程判定成功/失败），维持原有静默语义优先于让重试过程可见；且任务文本给出的 `apt_retry` 替换示例也是直接前缀替换，未要求对这类调用点额外加包装。

## 6. 文件改动范围确认

```
$ git status --short scripts/install.sh scripts/setup.sh
 M scripts/install.sh
 M scripts/setup.sh
```

未改动 `web/`、`frontend/`、仓库内其他脚本。本报告覆盖写入 `TASK-REPORT.md`，不执行 `git add`。
