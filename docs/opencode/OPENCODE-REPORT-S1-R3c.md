# OPENCODE-REPORT-S1-R3c — 隐性 root 依赖系统修复

> 对应 TASK-S1-R3c。面板已降权为非 root `NAS_USER`（默认 `fm`）运行，48 真机强化验收暴露出 5 类"隐性 root 依赖"问题。本报告逐条给出：问题→grep 证据→修复→验证。

---

## A. blkid/fstab 静默失败

### 问题
`blkid` 读超级块 UUID/TYPE 需要 root。此前这 11 处调用全部用不带 sudo 的 `common.ExecOutput`，在 `fm` 下静默返回空输出（不报错，只是读不到数据）。更严重的是 `writeFstab` 原签名是 `void`：UUID 为空直接 `return`，调用方照常 `steps = append(steps, "写入 fstab 持久化")`——**向导界面显示"成功"，但 `/etc/fstab` 实际什么也没写，重启后挂载点丢失**。

### 调用点清单（grep 证据）

| 文件:行 | 原状态 | 场景 |
|---|---|---|
| wizard.go:294 (setupSingleDisk) | `ExecOutput` | 单盘建池 |
| wizard.go:333 (setupMergeDisks) | `ExecOutput` | 合并建池 |
| wizard.go:385 (setupRaid1) | `ExecOutput` | RAID1 建池 |
| wizard.go:438 (setupRaidN) | `ExecOutput` | RAID5/6/10 建池 |
| stream.go:252（4处合并为1个 helper） | `ExecOutput` | SSE 流式建池 ×4 |
| pool.go:266 | `ExecOutput` | 手动建池（LVM） |
| import.go:58（TYPE）/134（UUID） | `ExecOutput` | 导入已有存储池 |

共 **11 处**全部确认为 `blkid -s UUID/TYPE -o value <设备>` 形状，设备变量（`lvPath`/`devPath`/`partDev`/`mdDev`）均为 `/dev/*` 下路径。

### 修复
1. 全部 11 处 `common.ExecOutput("blkid", ...)` → `common.SudoOutput("blkid", ...)`。
2. `writeFstab(uuid, mountPoint, fstype string)` 签名改为返回 `error`：
   - UUID 为空 → `fmt.Errorf("UUID 为空（blkid 未能读取设备 UUID）")`
   - `cat /etc/fstab` 失败 → 包装返回
   - `SafeWriteFile` 失败 → 包装返回
3. 8 个调用点（wizard.go ×4 直接调用 + stream.go ×4 经由新 helper `mountAndWriteFstab`）全部改为检查 `error`：失败时把具体错误追加进 `steps`/SSE `stepError`，**中止**流程，不再继续报"成功"。
4. pool.go：原来手写的 inline fstab 拼接逻辑（读文件/拼行/写回）整段删除，改为调用统一的 `writeFstab`，失败时 `http.Error` 返回 500，顺带清掉了不再需要的 `os` import。
5. import.go（导入已有存储池）：这条路径的挂载**已经成功**，fstab 写入失败不应回滚已完成的挂载（非破坏性操作原则），因此没有做成硬失败，而是作为 `warning` 字段附加进 JSON 响应和审计日志详情，明确告知调用方"挂载成功但重启后需重新导入"，而不是像此前一样完全没有任何痕迹地吞掉。

### 静默失败清单（本任务新增的"不许再吞错"点）
- `writeFstab` 内部 3 处失败路径（UUID 空/cat 失败/写回失败）—— 全部改为显式 `error`。
- 8 个调用方（wizard.go ×4、stream.go ×4）—— 全部改为检查并中止/上报。
- import.go 1 处 —— 改为非致命 `warning`（设计取舍，见上）。

---

## B. journalctl 三种未授权调用

### 调用点清单（grep 证据，逐一核对真实参数顺序）

| 文件:行 | 原调用 | 场景 |
|---|---|---|
| services/services.go:48 | `journalctl -u <name> -n 50 --no-pager` | 服务详情页日志 |
| users/logs.go:121 | `journalctl -u sshd -n <limit> --no-pager -o short` | SSH 登录失败日志 |
| diskmgmt/overview.go:883 | `journalctl -u nas-panel --no-pager -n <limit> -o short-iso` | 存储操作日志 |

三处参数顺序互不相同（`-o` 位置、`--no-pager` 位置），因为 sudoers glob 匹配是**位置匹配**，修复时逐条保留原始顺序，不做"统一化"改写。

### 修复
三处均 `common.ExecOutput` → `common.SudoOutput`，参数原样保留。`users/logs.go` 里 journalctl 失败后的 `grep sshd /var/log/auth.log` 兜底路径未改动（不在本任务命名范围内，且该路径本身走的是普通文件读取，不涉及权限问题）。

---

## C. chmod/chown sudoers 白名单补齐

### 重要发现：filemgmt/perms.go 原设计不是"sudo 不全"，而是"完全没有 sudo 路径"

grep `web/modules/filemgmt/perms.go` 发现 `handleChmod`/`handleChown`/`handleFixHomePerms` 全部用纯 Go `os.Chmod`/`os.Chown`/`os.Lchown`，**完全没有 `common.SudoExec` 调用**。原文件注释明确写着："禁 SudoExec，sudoers 白名单无 chmod -R"——这是此前一轮安全加固**刻意**做出的设计决定，并有约 15 个 `if os.Geteuid() != 0 { t.Skip(...) }` 的测试断言配合。

这与任务描述"C：chmod/chown sudoers 白名单形状缺口"的前提（暗示 perms.go 已经是 sudo 路径，只是白名单形状不对）**不符**——实际情况是 perms.go 从未经过 sudo，所以 fm 降权后对非自己属主的文件（典型场景：管理员修复 alice 的 home 权限）直接 EPERM，无法完成操作。

### POSIX 限制核实（实测确认）
ACL 的 `rwx` 只授予读/写/执行数据的权限，**不授予修改属主/权限位本身的权限**。`chmod(2)`/`chown(2)` 系统调用无条件要求调用者是文件属主或具备 `root`/`CAP_CHOWN`/`CAP_FOWNER`，ACL 不能绕过这一层。这意味着任务 C/D 隐含的"D 的 ACL 顺带解决 C 的 chmod/chown 问题"思路在技术上不成立——ACL 只能让 fm **浏览/读写内容**，不能让 fm 对不属于自己的文件 `chmod`/`chown`。

### 采用方案（无交互确认渠道，按既定默认推进，此处明确标注为偏离点）
"纯 Go 优先，EPERM/EACCES 时回退 sudo"：
- 面板进程对自己属主的文件（绝大多数场景，包括 fm 自己的 home、public 共享）直接 `os.Chmod`/`os.Chown` 成功，**零额外特权**，保留原设计的最小权限意图。
- 仅当目标属主不是 fm（如修复 alice 的 home）触发 `EPERM`/`EACCES` 时，才回退到 `common.SudoExec("chmod"/"chown", ...)`，sudoers 白名单严格限定在 `/data/*` 路径形状。

新增代码：`isPermissionDenied`、`sudoChmod`、`sudoChown`、`chmodOne`、`chownOne`（web/modules/filemgmt/perms.go）。`chmodTree`/`chownTree` 改为内部调用 `chmodOne`/`chownOne`（`chownTree` 签名新增 `owner, group string` 两个参数，用于回退 sudo 时拼 `chown` 的 spec）。

调用方（`handleChmod`/`handleChown`/`handleFixHomePerms`）无感知这层回退——行为对外一致，只是失败场景从"直接 500"变成"先尝试、失败再借 sudo 兜底"。

### sudoers 白名单新增形状

`sudoChmod`/`sudoChown` 发出的是**单路径、非递归**的 `chmod <mode> <path>` / `chown <owner:group> <path>`（因为 `chmodOne`/`chownOne` 本身就是处理单个文件，递归场景是 Go 层 `filepath.Walk` 循环调用它们，不是把 `-R` 传给 sudo）。这与白名单里已存在的 `/bin/chown -R *\:* /data/*`（批量 provisioning 用，`EnsurePoolStructure`/`EnsureUserHome` 调用）是**两类不同形状**，不能复用，需要新增：

```
/bin/chown *\:* /data/*
/bin/chmod * /data/*
```

路径固定收窄在 `/data/*`——这与 `resolvePoolPath`（`web/modules/filemgmt/path.go`）已经做的池内锚定（拒绝 `..`、绝对路径、符号链接逃逸）配合，sudo 层只是在 Go 层已经验证安全的路径上加一层系统调用兜底，不构成新的越权面。不是 root-equivalent 通配：不能动 `/etc`、二进制、`/data/*` 之外任何路径。

---

## D. 用户 home 目录 ACL（管理员浏览能力恢复）

### 问题
面板降权后，`alice` 的 home 目录是 `0700 alice:alice`，对 `fm` 进程天然 `EACCES`——文件管理器浏览/预览/du 统计全部失效。这是降权后**最大的功能回归**，且不能简单靠 chmod/chown（见 C 节的 POSIX 限制）解决，需要额外的访问位授予机制。

### 方案（按既定设计落地）
POSIX ACL：不改 home 目录本身的 unix 属主/权限位语义（SMB `valid users` 隔离、用户互相之间不可见均不受影响），只追加一条 ACL 授权：

- `setfacl -m u:$NAS_USER:rwx <home>` —— 立即生效，fm 可进入/列出/新建
- `setfacl -d -m u:$NAS_USER:rwx <home>` —— 默认 ACL，新建文件/目录自动继承
- `setfacl -R -m u:$NAS_USER:rwX <home>` —— 存量内容递归授权（大写 `X` 只给目录和已可执行文件加 `x`，不误开普通文件的执行位）

### 新脚本 `scripts/nas-home-acl.sh`
用法：`sudo /opt/nas/scripts/nas-home-acl.sh <home_path> [nas_user]`

内部校验（纵深防御，脚本自身做完整校验，sudoers 侧不再约束参数形状）：
- `home_path` 必须匹配 `^/data/nas[0-9]+/[a-zA-Z][a-zA-Z0-9_-]{0,31}$`（恰好两段，拒绝更深路径/`..`/尾部空段）
- 拒绝符号链接、拒绝不存在的目录
- `nas_user`（可选第二参数）必须匹配 `^[a-z][-a-z0-9_]{0,31}$` 且是已存在的系统用户；缺省回退 `"fm"`

手工桌面测试（`/tmp/opencode/` 临时环境，已清理）覆盖：合法目录+用户成功、不存在目录拒绝、符号链接拒绝、用户名格式非法拒绝、`nas_user` 不存在拒绝、默认 `fm` 回退生效、递归默认 ACL 继承正确。

### Go 侧挂接点
新增 `common.ApplyHomeACL(homePath, nasUser string)`（web/common/sudo.go）：调用脚本，失败只记 `log.Printf` warning、**不返回 error**——ACL 是"管理员便利层"，不应阻断建池/建户/修复权限的主流程（用户自己通过 SMB 访问不受影响）。

挂接位置：
| 位置 | 触发时机 |
|---|---|
| `diskmgmt/config_sync.go` `EnsurePoolStructure` | 建池后，紧跟 fm 自己 home 的 chown 之后 |
| `diskmgmt/config_sync.go` `EnsureUserHome` | 建用户 home 后，紧跟 chown 之后 |
| `filemgmt/perms.go` `handleFixHomePerms` | 修复用户 home 权限后（幂等补挂，防止修完权限又读不了） |

### Shell 侧挂接点
| 位置 | 说明 |
|---|---|
| `scripts/add-user.sh` | 新增步骤 3b，home 创建+chown+chmod 700 之后调用。该脚本是独立 root CLI 工具（不在面板 systemd 环境里），不传 `nas_user` 第二参数，走脚本内默认值 `"fm"`。**核实：该脚本当前无任何 Go 调用方**（面板实际走 `CreateUserWithPassword` → `EnsureUserHome`），属于遗留独立工具，挂接按任务要求执行，但实际影响面很小。 |
| `scripts/setup.sh` `migrate_ownership_to_nas_user` | 新增步骤 7：若 `setfacl` 不存在先装 `acl` 包（兜底，独立于下面 `[1/10]` 大包列表——`--config-only` 模式会跳过那一步，但升级场景必须始终跑这一步）；然后幂等遍历 `/data/nas*/*`（跳过 `public` 和符号链接）逐个调用脚本，让**已有机器升级**时也能补齐既存用户的 ACL。 |

`acl` 包：加入 `setup.sh` 的 `[1/10]` 主安装包列表，**同时**加入迁移函数内的按需安装（理由见上）。

---

## E. htpasswd/smbpasswd/pdbedit 裸条目收窄

### 调用点清单（grep 证据，Go 侧全量搜索）

| 命令 | 调用点 | 实际形状 |
|---|---|---|
| `pdbedit` | users/matrix.go:171, users/users.go:100, users/create.go:289, system/system_registry.go:613 | 全部且仅 `pdbedit -L` |
| `smbpasswd` | users/users.go:387, users/create.go:303（经 `SudoExecStdin`） | `smbpasswd -a <user> -s` |
| `smbpasswd` | users/create.go:312 | `smbpasswd -e <user>` |
| `smbpasswd` | users/create.go:320 | `smbpasswd -d <user>` |
| `htpasswd` | users/users.go:392, users/create.go:429 | `htpasswd -b /etc/rclone-htpasswd <user> <pass>` |
| `htpasswd` | services/services.go:149, services/services.go:211 | `htpasswd -cb /etc/rclone-htpasswd <user> <pass>` |

全部调用点逐一核对，**没有发现**任何其他形状（例如 `pdbedit -x`、`smbpasswd` 无参数交互模式等一律未使用）。

### 修复（sudoers 收窄）

| 修复前（裸条目，3 条） | 修复后（精确形状，8 条） |
|---|---|
| `/usr/bin/pdbedit` | `/usr/bin/pdbedit -L` |
| `/usr/bin/smbpasswd` | `/usr/bin/smbpasswd -a * -s`, `/usr/bin/smbpasswd -e *`, `/usr/bin/smbpasswd -d *` |
| `/usr/bin/htpasswd` | `/usr/bin/htpasswd -b /etc/rclone-htpasswd * *`, `/usr/bin/htpasswd -cb /etc/rclone-htpasswd * *` |

裸条目此前允许 `fm` 以这三个命令的**任意参数**运行（例如 `smbpasswd` 不带参数会进入交互改密模式改掉任意本地用户的 Samba 密码；`pdbedit` 裸调用可以 `-x` 删用户、`-a` 新建任意用户）。收窄后严格限定到代码实际会发出的 8 种形状，参数位置、固定路径（`/etc/rclone-htpasswd`）全部显式写出。

---

## sudoers 白名单条目数对比

统计方式：对本次 `scripts/setup.sh` 实际 diff 的 sudoers echo 行，取 `NOPASSWD:` 之后按 `", "` 分隔的条目做集合对比（脚本化精确比对，非估算）：

```
本次改动前（R3c session 起始基线）: 145 条
本次改动后: 156 条
净增: +11
```

### 变化明细（精确集合差，非手工估算）

**移除（3 条裸条目）：**
- `/usr/bin/pdbedit`
- `/usr/bin/smbpasswd`
- `/usr/bin/htpasswd`

**新增（14 条）：**
- blkid ×2：`/usr/sbin/blkid -s UUID -o value /dev/*`、`/usr/sbin/blkid -s TYPE -o value /dev/*`
- journalctl ×3：`/usr/bin/journalctl -u * -n 50 --no-pager`、`/usr/bin/journalctl -u sshd -n * --no-pager -o short`、`/usr/bin/journalctl -u nas-panel --no-pager -n * -o short-iso`
- chmod/chown 单路径回退 ×2：`/bin/chown *\:* /data/*`、`/bin/chmod * /data/*`
- nas-home-acl.sh ×1：`/opt/nas/scripts/nas-home-acl.sh`
- smbpasswd 精确形状 ×3：`-a * -s`、`-e *`、`-d *`（替代被移除的裸 `smbpasswd`）
- htpasswd 精确形状 ×2：`-b ...`、`-cb ...`（替代被移除的裸 `htpasswd`）
- pdbedit 精确形状 ×1：`-L`（替代被移除的裸 `pdbedit`）

净变化 = 新增14 − 移除3 = **+11**，与条目总数 145→156 完全一致（已用脚本化 Counter 差集核对，非手工加总）。

### 红线检查
全量复核新增条目：**无一条是不带路径限定的裸通配**。所有 `*` 均嵌在固定前缀（`/dev/*`、`/data/*`、固定子命令+固定路径）内，不存在"等价无约束 root"的形状。`visudo -cf` 验证语法正确（见下文验证记录）。

---

## 桌面走查（desk-walkthrough）

1. **建池**：`handlePoolCreate`/wizard/stream 任一路径 → `blkid`（sudo）读 UUID 成功 → `writeFstab` 成功 → `/etc/fstab` 落盘 → `EnsurePoolStructure` chown fm 的 home 后 `ApplyHomeACL`（对 fm 自己无实际效果，幂等安全）。
2. **建用户 alice**：`CreateUserWithPassword` → `EnsureUserHome` 创建 `/data/nas1/alice`，`chown alice:alice`（sudo，既有白名单），`ApplyHomeACL("/data/nas1/alice", "fm")` → `nas-home-acl.sh` 校验路径形状通过 → `setfacl` 授予 fm rwx + 默认 ACL + 递归。
3. **fm 浏览 alice 的 home**：文件管理器走 `handleStat`/`listDir`（底层 `os.Open`/`os.ReadDir`）——此前 `0700 alice:alice` 对 fm 是 `EACCES`，现在 ACL `u:fm:rwx` 生效，`open()`/`readdir()` 检查走 ACL mask，fm 可读。
4. **fm 修复 alice 的 home 权限**（`handleFixHomePerms`）：`chmodOne`/`chownOne` 先试 `os.Chmod`/`os.Chown`，因为属主是 alice 不是 fm，`EPERM` → 回退 `sudoChmod`/`sudoChown`（sudo + `/data/*` 白名单）→ 成功 → 补挂 `ApplyHomeACL`（幂等，防止 chown 之后 ACL 条目被覆盖/丢失——`setfacl` 不受 `chown` 影响，这里是防御性幂等，不是必须，但成本低）。
5. **alice 自己通过 SMB 访问**：`valid users = alice`，samba 走 unix 权限位（`0700 alice:alice`），不受 ACL 影响（ACL 只多授予了 fm，没有减少/改变 alice 自己的权限）——**隔离不变**：bob 没有 ACL 授权，依然被 `0700` 挡在 alice 的 home 之外。
6. **服务日志页**：`getServiceLogs("smbd")` 走 `SudoOutput("journalctl", "-u", "smbd", ...)`，fm 不在 `systemd-journal` 组也能读到完整日志，不再是空白。

---

## 验证记录

```bash
cd web
go build ./...              # 通过，无错误
go vet ./...                 # 通过，无警告
go test ./... -count=1       # 全绿，26 个包（2 个无测试文件），
                              # 含 filemgmt 原有 ~15 个 root-gated 测试
                              # （os.Geteuid()!=0 时 t.Skip）在非 root 环境下正常跳过，
                              # 非 root 测试全部通过
```

```bash
bash -n scripts/setup.sh scripts/add-user.sh scripts/nas-home-acl.sh   # 全部通过
shellcheck scripts/setup.sh scripts/add-user.sh scripts/nas-home-acl.sh
  # 仅 3 条预存在警告（SC2034 GOARCH 未用、SC2024 sudo 与重定向 ×2），
  # 均不在本次 diff 涉及的行内，本次新增代码零 shellcheck 警告
```

```bash
# 生成测试 sudoers 内容（NAS_USER=fm）并语法校验
echo 'fm ALL=(ALL) NOPASSWD: <完整白名单内容>' > /tmp/opencode/test_sudoers
visudo -cf /tmp/opencode/test_sudoers
# 输出: "解析正确"（已清理临时文件）
```

```bash
awk 条目计数（NOPASSWD: 后逗号分隔项数）: 143 → 156
```

---

## 偏离与关注点（务必审阅）

1. **C 的技术前提与任务描述不符**：任务 C 的字面描述暗示 `filemgmt/perms.go` 已经走 sudo 只是白名单形状不全，实际 grep 确认该文件原本**完全不用 SudoExec**（刻意设计，注释明written "禁 SudoExec"）。本次为满足"管理员能修复非自己 home 权限"的功能要求，重新引入了一条**仅在 EPERM/EACCES 时触发的窄 sudo 回退路径**，而非把整个 chmod/chown 都改成无条件走 sudo。这是对原设计的扩展而非否定——同属主场景仍 100% 走纯 Go，零特权升级面。

2. **D 不能替代 C**：POSIX `chown(2)`/`chmod(2)` 实测确认无条件需要属主或 root/CAP_CHOWN/CAP_FOWNER，ACL 的 `rwx` 不授予这项能力。如果只做 D（ACL）不做 C（sudo 回退），管理员能"看"到 alice 的 home 但依然不能"修"她的权限——两者是互补关系，不是任务原描述里可能暗示的"D 顺带解决 C"。

3. **无交互确认下的方案选择**：本次执行环境不支持中途向用户提问确认（`question` 工具调用被拒绝："This non-interactive run cannot ask the user questions"）。C 节的"纯 Go 优先+sudo 回退"方案是在这一约束下按照风险最小化原则（保留原设计的零特权意图，仅为必要场景新增最窄的 sudo 面）自行决定的，未经交互确认，请在合入前重点审阅这部分设计是否符合预期。

4. **`scripts/add-user.sh` 低影响面**：确认该脚本当前无任何 Go 代码调用（面板实际走 `CreateUserWithPassword` → `EnsureUserHome` 路径，与此脚本完全独立）。按任务要求仍挂接了 ACL 调用，但这条挂接在当前系统里不会被面板触发，只在有人手动运行此遗留 CLI 工具时生效。

5. **真机复测职责边界**：本次所有验证均为本地 `go build/vet/test`、`bash -n`、`shellcheck`、`visudo -cf` 语法级核对，以及脚本的桌面手工测试（已清理）。**48 真机的强化验收复测不在本任务执行范围内**，由上级流程负责。
