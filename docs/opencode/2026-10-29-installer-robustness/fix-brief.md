# 任务：安装脚本修复 — apt 瞬时失败加固（F-1）+ install.sh 失败路径证据保全（F-2）

你在 ~/soft/nas 仓库工作。基线 = HEAD (f4bc9f7)。两个缺陷均为真机实锤（Debian 13 测试机，2026-10-29 用户级全流程部署）。只改 scripts/install.sh 与 scripts/setup.sh；先通读两脚本相关段落；与本文不符在报告申报。

## 硬约束（违反 = 返工）

1. 禁读/改仓库外文件；禁止在本机真实执行 install.sh/setup.sh/apt（验证只用 `bash -n` 语法检查 + 逐段自查 + 有 shellcheck 就跑）。
2. 只改 scripts/install.sh、scripts/setup.sh；不动 web/、frontend/、其他脚本。
3. **install.sh 禁止任何交互式输入（read/read -s）**——`curl | bash` 时 stdin 是脚本流本身（既有红线，改动不得引入）。
4. 保持两脚本既有输出风格（步骤计时器/✓✗ 文案/中文提示）与退出码语义；不改 sudoers 需求面（不新增需要白名单的命令形态——apt-get 已在白名单：`/usr/bin/apt-get update`、`/usr/bin/apt-get install -y -qq *`、`/usr/bin/apt update`、`/usr/bin/apt upgrade *`；新参数必须落在这些既有通配形态内，尤其 install.sh 是 fm 用户经 sudo 逐条跑的）。
5. TASK-REPORT.md 覆盖写、不 git add；git add 只加两个脚本；单 commit，message 原样文末。

## F-1 根因与修法

**实锤**：install.sh 包阶段完成后调 setup.sh，其 [1/10] 第一步 `apt-get update`（setup.sh:282）恰与 systemd `apt-daily-upgrade.service`（journal 11:22:24 触发）撞锁失败，set -e 静默退出，整个安装中止；4 分钟后单独重跑全过——纯瞬时冲突，无重试无锁等待。

修法（两脚本所有 apt-get update / apt-get install 调用点统一加固）：
1. 所有 `apt-get update` 与 `apt-get install` 调用加 `-o DPkg::Lock::Timeout=60`（apt ≥1.9.10 支持，Debian 13 apt 2.9 满足；等锁 60s 而非立即失败）。注意该形态仍匹配 sudoers 既有通配（`/usr/bin/apt-get install -y -qq *` 前缀不变——把 -o 参数放在既有参数之后、包名之前会破坏通配吗？不会：通配 `*` 在尾部覆盖所有后续参数；但 `apt-get update` 白名单是**无参精确形态** `/usr/bin/apt-get update`——加参数后不再匹配！所以 update 的锁等待不能用 -o 参数，改用修法 2 的重试包裹；install 的 -o 参数可以加）。**先核对 scripts 里 sudoers 白名单生成段（install.sh/setup.sh 内嵌的 sudoers 内容）确认实际形态再动手，报告里写明核对结论。**
2. 重试包裹：新增 shell 函数（两脚本各自定义，不引入外部文件依赖）：
   `apt_retry() { local n=0; until [ $n -ge 3 ]; do "$@" && return 0; n=$((n+1)); echo "  ⚠ apt 命令失败（第 $n 次），10s 后重试: $*" >&2; sleep 10; done; return 1; }`
   所有 apt-get update/install 调用改 `apt_retry apt-get ...`。失败重试输出走 stderr（进 setup 日志）。
3. 若 sudoers 白名单需要为 `-o DPkg::Lock::Timeout=60` 形态补条目（install 的尾部通配已覆盖则不用），同步改两脚本内嵌 sudoers 段并在报告说明。

## F-2 根因与修法

**实锤**：install.sh:258-275 —— setup.sh 失败路径 `rm -f "$SETUP_LOG"` 销毁证据，提示文案却称"完整日志已保留: 重新运行可查看"（谎言）；且成功/失败都只用 `grep -E '^\[|✓|✗|⚠|...'` 过滤回显，apt/dpkg 的真实错误行（`E: ...`）不匹配过滤器，用户在 install.log 里只见"[1/10] 安装基础软件包..."后直接"执行失败"，零线索。

修法（install.sh）：
1. 失败路径：**不删** SETUP_LOG，把它移动到固定路径 `/tmp/nas-install-setup-$(date +%s).log`（或 mktemp 保留原名），提示文案改为如实的"完整日志: <路径>"；成功路径维持删除。
2. 失败时在过滤回显之后追加原始日志尾部：`echo "  ── setup.sh 日志尾部（未过滤）──"; tail -30 "$SETUP_LOG" | sed 's/^/  /'`。
3. grep 过滤模式补 `E:` 前缀行（apt/dpkg 错误行常态），成功路径同样受益。

## 验证（必须执行，输出贴报告）

```
bash -n scripts/install.sh && bash -n scripts/setup.sh && echo SYNTAX_OK
command -v shellcheck >/dev/null && shellcheck -S error scripts/install.sh scripts/setup.sh || echo "shellcheck 不可用或无 error 级问题"
grep -n "apt_retry\|DPkg::Lock::Timeout" scripts/install.sh scripts/setup.sh | head -20
grep -n "read -\?[^l]" scripts/install.sh | grep -vE "read line|read -r line|while read" || echo "NO_INTERACTIVE_READ"
```

## 提交（原样 message）

```
fix(scripts): apt 瞬时失败加固（锁等待+3次重试）+ install.sh 失败路径保全日志与真实错误回显 (F-1/F-2 真机实锤)
```

## 报告（TASK-REPORT.md，不 git add）

sudoers 形态核对结论、apt_retry 落点清单（每个调用点行号）、失败路径新行为说明、验证输出、偏差申报。
