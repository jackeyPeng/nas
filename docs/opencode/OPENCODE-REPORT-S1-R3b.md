# OPENCODE-REPORT-S1-R3b: 白名单实证修复 — chpasswd 路径错配 + 密码/组成员管理收口

仓库：/home/jacky/soft/nas，分支 `feat/least-privilege`。对应 TASK-S1-R3b.md，由 48 真机强化验收 journal 拒绝记录（2026-10-26 采集）驱动。

## 结论摘要

- **根因确认**：`/usr/bin/chpasswd` 白名单条目路径错配——Debian 13 实际把 `chpasswd` 解析到 `/usr/sbin/chpasswd`（shadow-utils 包，admin 工具按 FHS 惯例装 sbin），sudo 按解析后绝对路径匹配 Cmnd 规则，条目从未命中，建户第二步/改密 API 全炸（`设置密码失败: sudo: 需要密码`），级联到依赖改密成功的 12 项套件测试。
- **root-equivalent 后门确认两处**：裸 `/usr/bin/chpasswd`（`echo "root:x" | sudo chpasswd` 可接管整机）+ `/usr/bin/gpasswd -a * *`（`sudo gpasswd -a fm sudo` 可自加 sudo 组完整提权）。均已收口为校验脚本。
- 新建 `scripts/set-user-pass.sh`（替代裸 `chpasswd`）、`scripts/nas-group-member.sh`（替代三条裸 `gpasswd -a/-d/-M`），整脚本进白名单。
- Go 侧 3 处调用点改造（`users.go:changePassword`、`create.go:CreateUserWithPassword`、`groups.go:setGroupMembers`），调用形状改动，功能语义不变。
- 白名单条目数：**145 → 143**（删 4 条裸 chpasswd/gpasswd，增 2 条收口脚本）。
- `go build ./... && go vet ./... && go test ./... -count=1` 全绿；`bash -n`/`shellcheck` 全部改动脚本零告警；`visudo -cf` 通过。
- 路径系统性对帐（任务 A）：9 个嫌疑二进制中仅 `chpasswd` 一处错配，其余均正确，详见第 1 节。

## 1. 路径系统性对帐表（Debian FHS 惯例 + 本机 Fedora 交叉验证）

验证方法：`command -v <bin>` 在本机（Fedora 44，FHS 惯例与 Debian 对这批工具一致——均来自同一上游 `shadow-utils`/`samba`/`apache2-utils` 系）执行，交叉核对 `scripts/setup.sh` 当前白名单条目路径；Debian 专属二进制（`ufw`/`apt`/`update-initramfs`，Fedora 无对应包）按已知打包惯例判断，不虚构本机不存在的验证结果。真机环境未接入本次会话（webfetch/websearch 工具在本次会话中不可用），结论基于：① R3/R4 两轮已对本白名单做过路径收窄与死条目清理（含"原来 `/sbin/*` 与 `/usr/sbin/*` 重复挂载"一类历史纠正），② 任务书给出的 journal 实证直接定位到 `chpasswd`，③ FHS 对 admin-only 工具（改密、建户、删户类）归 sbin、用户可触达工具（gpasswd 虽改组但是非特权用户也能通过 setgid 位间接用、smbpasswd/htpasswd/passwd 面向最终用户）归 bin 的惯例在 Debian/Fedora 间是上游一致的（同一份源码打包）。

| 二进制 | 白名单条目路径（修复前） | `command -v` 实际解析（本机，Fedora） | 判定 | 动作 |
|---|---|---|---|---|
| `chpasswd` | `/usr/bin/chpasswd` | `/usr/sbin/chpasswd` | **错配**——与任务书 journal 证据（`fm : command not allowed ; COMMAND=/usr/sbin/chpasswd`）完全吻合 | **根修**：裸条目删除，改收口脚本 `/opt/nas/scripts/set-user-pass.sh`（整脚本白名单，路径问题随之消失——sudo 只需匹配脚本自身绝对路径，脚本内部以 root 身份跑，`chpasswd` 走脚本自身 `$PATH` 解析，不再经 sudoers Cmnd 匹配） |
| `smbpasswd` | `/usr/bin/smbpasswd` | 本机未装 samba-common-bin，无法直接验证；Debian `samba-common-bin` 包历史上一贯装 `/usr/bin/smbpasswd`（非 sbin——面向最终用户改自己 SMB 密码的场景，非纯 admin 工具） | 维持 | 不改 |
| `htpasswd` | `/usr/bin/htpasswd` | `/usr/bin/htpasswd` | 一致 | 不改 |
| `passwd` | `/usr/bin/passwd` | `/usr/bin/passwd` | 一致（白名单里是 `passwd -S *` 查询形状，非改密，但路径本身一致） | 不改 |
| `useradd` | `/usr/sbin/useradd` | `/usr/sbin/useradd` | 一致 | 不改 |
| `usermod` | `/usr/sbin/usermod` | `/usr/sbin/usermod` | 一致 | 不改 |
| `groupadd` | `/usr/sbin/groupadd` | `/usr/sbin/groupadd` | 一致 | 不改 |
| `groupdel` | `/usr/sbin/groupdel` | `/usr/sbin/groupdel` | 一致 | 不改 |
| `gpasswd` | `/usr/bin/gpasswd` | `/usr/bin/gpasswd` | 路径一致（本轮问题不是路径，是**参数形状无约束**，见下节） | 路径不改，收口参数形状 |
| `pdbedit` | `/usr/bin/pdbedit` | 本机未装，`remove-user.sh` 内部裸调用（非 sudoers 独立条目，整脚本已白名单） | 维持（白名单里的 `/usr/bin/pdbedit` 条目本身是 Go 侧是否有独立调用点的问题，grep 确认零独立调用，继续保留不影响） | 不改 |
| `userdel` | 无独立条目（R3 已删，`remove-user.sh` 内部裸调用） | `/usr/sbin/userdel` | 一致，且本就不在白名单独立挂载 | 不改 |

**结论**：9 个嫌疑二进制逐一核对，仅 `chpasswd` 一处路径错配，与任务书证据 1 完全对应。其余 8 个路径均正确，不存在"同类嫌疑全面爆雷"的情况——这次真机 journal 抓到的是唯一一个真实的路径问题。

## 2. B：密码设置收口脚本

### `scripts/set-user-pass.sh`（新建，替代裸 `chpasswd`）

```bash
#!/bin/bash
# NAS 用户管理脚本 - 设置用户密码
# 用法: sudo ./set-user-pass.sh   (stdin 逐行读取 "username:password")
#
# 收口动机（S1-R3b 实证修复）：裸 /usr/bin/chpasswd 在白名单里是
# root-equivalent 后门——任何持有 fm shell 的人可以
#   echo "root:x" | sudo chpasswd
# 直接改掉 root 密码接管整机。本脚本整体进白名单替代裸 chpasswd 条目，
# 内部对每一行的 username 做形状+身份校验后才 exec 真正的 chpasswd，
# 拒绝目标是 root 或 uid<1000 的既有系统用户（新建户场景用户尚不存在，
# 放行——面板建户流程先 useradd 再设密码）。
#
# 面板侧调用点：web/modules/users/users.go:changePassword、
# web/modules/users/create.go:CreateUserWithPassword，均经
# common.SudoExecStdin(..., "/opt/nas/scripts/set-user-pass.sh") 传入
# "username:password\n" 行，与原 chpasswd stdin 协议形状不变。

set -euo pipefail

USERNAME_RE='^[a-z][-a-z0-9_]{0,31}$'

# 逐行读取 stdin，校验后攒成合法行再统一喂给 chpasswd（避免多次调用
# chpasswd 产生多条审计日志，也避免部分行通过部分行失败时系统状态不一致）
valid_lines=()
line_no=0
while IFS= read -r line || [ -n "$line" ]; do
    line_no=$((line_no + 1))
    [ -z "$line" ] && continue

    username="${line%%:*}"
    password="${line#*:}"

    if [ "$username" = "$line" ]; then
        echo "错误: 第 $line_no 行格式非法（缺少 ':' 分隔符）" >&2
        exit 1
    fi

    if ! [[ "$username" =~ $USERNAME_RE ]]; then
        echo "错误: 第 $line_no 行用户名 \"$username\" 格式非法（只能小写字母开头+小写字母数字下划线短横线，<=32 字符）" >&2
        exit 1
    fi

    if [ "$username" = "root" ]; then
        echo "错误: 禁止通过本脚本设置 root 密码" >&2
        exit 1
    fi

    # 用户已存在时，拒绝 uid<1000 的既有系统用户（daemon/www-data 等）。
    # 用户不存在是建户场景的正常前置（面板先 useradd 再调用本脚本设密码），
    # 此时放行——不存在的用户不可能是系统用户。
    if existing_uid="$(id -u "$username" 2>/dev/null)"; then
        if [ "$existing_uid" -lt 1000 ]; then
            echo "错误: 禁止设置系统用户 \"$username\"（uid=$existing_uid < 1000）的密码" >&2
            exit 1
        fi
    fi

    valid_lines+=("$username:$password")
done

if [ "${#valid_lines[@]}" -eq 0 ]; then
    echo "错误: 未读到任何有效的 username:password 行" >&2
    exit 1
fi

printf '%s\n' "${valid_lines[@]}" | exec chpasswd
```

### Go 调用点改造

- `web/modules/users/users.go:changePassword`：`SudoExecStdin(username+":"+password+"\n", "chpasswd")` → `SudoExecStdin(username+":"+password+"\n", "/opt/nas/scripts/set-user-pass.sh")`。stdin 协议形状不变（`"user:pass\n"`），只换了命令名。
- `web/modules/users/create.go:CreateUserWithPassword`：同样改法，第 144 行。

## 3. C：组成员管理收口脚本

### `scripts/nas-group-member.sh`（新建，替代 `gpasswd -a/-d/-M` 三条裸条目）

```bash
#!/bin/bash
# NAS 用户管理脚本 - 组成员管理
# 用法: sudo ./nas-group-member.sh set|add|del <组名> [成员...]
#   set  <组名> [成员...]   替换式设置组成员（成员列表可为空=清空组）
#   add  <组名> <成员>      添加单个成员
#   del  <组名> <成员>      删除单个成员
#
# 收口动机（S1-R3b 实证修复）：裸 gpasswd -a/-M 在白名单里组名无约束，
# fm 可以 `sudo gpasswd -a fm sudo` 把自己加进 sudo 组，完整 root 提权——
# root-equivalent 后门。本脚本整体进白名单替代三条裸 gpasswd 条目，内部
# 拒绝特权组黑名单后才 exec 真正的 gpasswd。
#
# 面板侧已有 Go 层 isManagedGroup（GID 1000-59999 过滤）作为第一道防线
# （web/modules/users/groups.go），本脚本是第二道（纵深防御——任何持有
# fm shell+sudo 的人可以跳过面板直接调用本脚本，脚本自身必须独立校验，
# 不能只依赖 Go 层）。
#
# 面板侧调用点：web/modules/users/groups.go:setGroupMembers，经
# common.SudoExec("/opt/nas/scripts/nas-group-member.sh", "set", groupName, 成员...)
# 和 .../"add"/groupName/成员 两种形状调用（原裸 gpasswd -M ""/-a 调用形状）。

set -euo pipefail

DENY_GROUPS=(root sudo wheel admin shadow disk adm)
GROUPNAME_RE='^[a-z][-a-z0-9_]{0,31}$'

if [ $# -lt 2 ]; then
    echo "用法: sudo $0 set|add|del <组名> [成员...]" >&2
    exit 1
fi

ACTION="$1"
GROUPNAME="$2"
shift 2
MEMBERS=("$@")

if ! [[ "$GROUPNAME" =~ $GROUPNAME_RE ]]; then
    echo "错误: 组名 \"$GROUPNAME\" 格式非法（只能小写字母开头+小写字母数字下划线短横线，<=32 字符）" >&2
    exit 1
fi

for deny in "${DENY_GROUPS[@]}"; do
    if [ "$GROUPNAME" = "$deny" ]; then
        echo "错误: 禁止操作特权组 \"$GROUPNAME\"" >&2
        exit 1
    fi
done

for m in "${MEMBERS[@]}"; do
    if ! [[ "$m" =~ $GROUPNAME_RE ]]; then
        echo "错误: 成员名 \"$m\" 格式非法" >&2
        exit 1
    fi
done

case "$ACTION" in
    set)
        # gpasswd -M 接受逗号分隔成员列表，空列表=清空组（与 groups.go
        # 原先 gpasswd -M "" groupName 的清空语义一致）
        members_csv=""
        for m in "${MEMBERS[@]}"; do
            if [ -z "$members_csv" ]; then
                members_csv="$m"
            else
                members_csv="$members_csv,$m"
            fi
        done
        exec gpasswd -M "$members_csv" "$GROUPNAME"
        ;;
    add)
        if [ "${#MEMBERS[@]}" -ne 1 ]; then
            echo "错误: add 操作需要且只需要一个成员参数" >&2
            exit 1
        fi
        exec gpasswd -a "${MEMBERS[0]}" "$GROUPNAME"
        ;;
    del)
        if [ "${#MEMBERS[@]}" -ne 1 ]; then
            echo "错误: del 操作需要且只需要一个成员参数" >&2
            exit 1
        fi
        exec gpasswd -d "${MEMBERS[0]}" "$GROUPNAME"
        ;;
    *)
        echo "错误: 未知操作 \"$ACTION\"（支持 set|add|del）" >&2
        exit 1
        ;;
esac
```

### Go 调用点改造

`web/modules/users/groups.go:setGroupMembers`——原来两次裸调用（`gpasswd -M "" groupName` 清空 + 循环 `gpasswd -a m groupName` 逐个加），改为一次调用收口脚本的 `set` 子命令，把全部目标成员作为位置参数传入：

```go
func setGroupMembers(groupName, membersStr string) error {
	const script = "/opt/nas/scripts/nas-group-member.sh"

	members := []string{}
	if membersStr != "" {
		for _, m := range strings.Split(membersStr, ",") {
			m = strings.TrimSpace(m)
			if m != "" {
				members = append(members, m)
			}
		}
	}

	args := append([]string{"set", groupName}, members...)
	out, err := common.SudoExec(script, args...)
	if err != nil {
		return fmt.Errorf("设置组成员失败: %s: %v", out, err)
	}

	return nil
}
```

脚本的 `add`/`del` 子命令当前 Go 侧没有调用方（面板"设置组成员"功能走替换式 `set`，没有"单独加一个/删一个成员"的独立端点）；按任务书设计落地（"参数 set|add|del"）保留供未来调用方使用，不是死代码——是脚本整体作为一个"gpasswd 安全外壳"的完整能力面，`createGroup`/`deleteGroup`（`groupadd`/`groupdel`）本身不在本任务收口范围内（任务书证据只针对 chpasswd 和 gpasswd）。

## 4. 白名单条目增删（D）

```diff
- /usr/bin/chpasswd,
...
- /usr/bin/gpasswd -a * *, /usr/bin/gpasswd -d * *, /usr/bin/gpasswd -M * *,
+ /opt/nas/scripts/set-user-pass.sh, /opt/nas/scripts/nas-group-member.sh,
```

精确 diff（`scripts/setup.sh` 第 645 行单行白名单字符串）：
- 删除 `/usr/bin/chpasswd`（1 条）
- 删除 `/usr/bin/gpasswd -a * *`、`/usr/bin/gpasswd -d * *`、`/usr/bin/gpasswd -M * *`（3 条——`-d * *` 本身 Go 侧从无调用方，是 R3 遗留的"预留但未使用"条目，一并清除而非只清两条有调用方的）
- 新增 `/opt/nas/scripts/set-user-pass.sh`、`/opt/nas/scripts/nas-group-member.sh`（2 条，形状对照 `/opt/nas/scripts/add-user.sh` 整脚本白名单风格）

### 条目数前后对比

- 修复前：145（R4 报告 awk 实测值）
- 修复后：**143**（本轮 awk 同一脚本实测，见第 6 节验证记录）
- 净变化：-2（删 4 增 2），且删除的 4 条全部是本轮判定的 root-equivalent 风险项或路径错配死条目，不存在"为过测试放宽"的情况。

### useradd 第二形状 `-g * *` 复核（任务书 D 指定项）

`pending_ops.go:262`：`common.SudoExec("useradd", "-M", "-s", "/usr/sbin/nologin", "-g", name, name)`——`name` 是共享文件夹名，经 `folders.go:isValidFolderName` 校验（小写字母开头+小写字母数字下划线短横线，2-32 字符），但该校验发生在 `ensureFolderUser` 调用方的上游（文件夹创建流程），`-g` 的组名取值范围本质上是"任意合法文件夹名"，无法收窄到固定值（每个共享文件夹都需要一个同名占位用户+同名主组，这是功能设计本身决定的动态性，不是校验缺失）。**结论**：维持现状（不收窄），因为：
1. 组名/用户名同形，受 `isValidFolderName` 约束，不是任意字符串可达；
2. sudoers glob 无法表达"组名必须等于第二个位置参数"这种关联约束（`useradd -M -s /usr/sbin/nologin -g * *` 的两个 `*` 在 sudoers 层面是独立通配，不保证一致，但 Go 侧调用点本身保证了一致——这是"调用点固定代码路径生成参数"模式的典型案例，风险边界已经在 R3 报告的残留风险表里以这个条目记录过）；
3. 本任务范围是 chpasswd/gpasswd 两处证据驱动的修复，不包含 useradd 的新发现问题——复核后未发现超出 R3 既有残留风险表记录范围的新风险，不额外改动。

维持 R3 残留风险拍板表对该条目的既有记录（"`-g` 组参数通配……已审查确认无法再收窄"），本轮复核结论一致，不新增变更。

## 5. 单元测试

Go 侧既有测试全绿覆盖了本轮改动的调用形状（未新增 Go 测试文件，理由：本轮改动是"调用的命令名/路径从裸命令换成脚本"，不改变任何校验逻辑或业务分支——`changePassword`/`CreateUserWithPassword`/`setGroupMembers` 函数签名、返回值、调用方校验顺序均未变，既有测试（`TestHandleUserAction_ChangePassword_RejectsControlCharPassword`、`TestHandleCreateUser_RejectsControlCharPassword`、`TestValidateMemberNames`、`TestIsManagedGroup`、`TestHandleGroupAction_RejectsUnmanagedGroup`）继续覆盖"校验先于副作用短路"这条主线，且全部通过，证明调用形状改动没有破坏任何既有保护）。

脚本本体验证（桌面用例，真实执行验证逻辑，拦截最终 `exec` 调用观察是否到达）：

```bash
$ bash -n scripts/set-user-pass.sh && bash -n scripts/nas-group-member.sh
（无输出，语法通过）

$ shellcheck scripts/set-user-pass.sh scripts/nas-group-member.sh
（无输出，零告警）
```

`set-user-pass.sh` 桌面用例（替换脚本尾部 `exec chpasswd` 为 `cat`，观察是否放行到最终执行）：

| 输入 | 预期 | 实测 |
|---|---|---|
| `alice:mypassword123` | 放行（新用户名合法形状） | `alice:mypassword123` 原样输出，放行 ✓ |
| `root:x` | 拒绝（root 黑名单） | `错误: 禁止通过本脚本设置 root 密码`，exit=1 ✓ |
| `Alice:x` | 拒绝（大写字母，不匹配 `^[a-z]...`） | `错误: 第 1 行用户名 "Alice" 格式非法`，exit=1 ✓ |
| `daemon:x` | 拒绝（既有系统用户 uid=2 < 1000） | `错误: 禁止设置系统用户 "daemon"（uid=2 < 1000）的密码`，exit=1 ✓ |
| `aliceNOCOLON`（无冒号） | 拒绝（格式非法） | `错误: 第 1 行格式非法（缺少 ':' 分隔符）`，exit=1 ✓ |
| `alice:x\nroot:y`（两行，第二行非法） | 整体拒绝（不半途放行第一行） | `错误: 禁止通过本脚本设置 root 密码`，exit=1，且脚本设计上先收集全部合法行再统一 `chpasswd`，非法行出现时在到达 `printf \| exec chpasswd` 之前就 `exit 1`，alice 那行不会被单独执行 ✓ |
| 空 stdin | 拒绝（无有效行） | `错误: 未读到任何有效的 username:password 行`，exit=1 ✓ |

`nas-group-member.sh` 桌面用例（在不存在的测试组/用户上跑，真实 `gpasswd` 因组/用户不存在报错，但错误发生点证明前置校验已放行到达 `gpasswd` 层——即验证的是"校验逻辑是否正确拦截/放行"，不是"gpasswd 本身是否成功"）：

| 输入 | 预期 | 实测 |
|---|---|---|
| `set testgroup alice bob` | 放行到 `gpasswd -M alice,bob testgroup` | `gpasswd：用户"alice"不存在`（放行到达 gpasswd，报错来自 gpasswd 本身因测试环境无此用户，非本脚本拦截）✓ |
| `set testgroup`（空成员） | 放行到 `gpasswd -M "" testgroup`（清空语义） | `gpasswd：testgroup 组不存在于 /etc/group 中`（同上，放行到达 gpasswd）✓ |
| `add testgroup alice` | 放行到 `gpasswd -a alice testgroup` | 放行到达 gpasswd ✓ |
| `del testgroup alice` | 放行到 `gpasswd -d alice testgroup` | 放行到达 gpasswd ✓ |
| `add sudo fm` | **拒绝**（sudo 在黑名单） | `错误: 禁止操作特权组 "sudo"`，exit=1，未到达 gpasswd ✓（核心回归：验证 root-equivalent 后门已关闭） |
| `add ../etc fm` | 拒绝（组名格式非法，防路径穿越/注入） | `错误: 组名 "../etc" 格式非法`，exit=1 ✓ |
| `add testgroup 'fm;whoami'` | 拒绝（成员名含特殊字符） | `错误: 成员名 "fm;whoami" 格式非法`，exit=1 ✓ |

全部用例按预期行为执行，核心安全回归（`add sudo fm` 必须被拦截）验证通过。

## 6. 桌面推演：三条链在强化态的完整 sudo 匹配路径

### 建户链（`POST /api/users`）

1. `handleCreateUser` → `isValidUsername(username)` 校验（Go 层，格式：字母开头+字母数字下划线短横线）
2. `CreateUserWithPassword` → `common.SudoExec("useradd", "-m", "-s", "/bin/bash", "-G", "nasusers", username)` → sudo 匹配 `/usr/sbin/useradd -m -s /bin/bash -G nasusers *`（路径正确，无变化）
3. `common.SudoExecStdin(username+":"+password+"\n", "/opt/nas/scripts/set-user-pass.sh")` → sudo 匹配 `/opt/nas/scripts/set-user-pass.sh`（整脚本白名单，命中）→ 脚本内 `username` 已经过 Go 层 `isValidUsername` 一次 + 脚本自身 `^[a-z][-a-z0-9_]{0,31}$` 二次校验（纵深防御，两道口径不完全相同——Go 层允许大写字母，脚本只收小写；因为 `isValidUsername` 允许的大写用户名若真的存在，会在脚本这一关被挡下，这是一个已知的口径差异，见"偏离与疑虑"）→ `id username` 检查（刚 `useradd` 过，用户已存在但 uid>=1000，放行）→ `exec chpasswd`（真实执行，root 身份）
4. 后续 Samba/FTP/WebDAV 启用步骤不变

**修复效果**：步骤 3 修复前会在 `sudo` 匹配阶段直接被拒绝（`command not allowed`），`设置密码失败: ...`，整条建户链在"设置密码"这一步失败，用户已创建但无密码，后续 Samba 等步骤仍会尝试执行（`addStep` continue 式流程），留下"系统用户存在但无密码、服务配置残缺"的半成品状态。修复后步骤 3 正常命中 sudoers 规则，链条完整。

### 改密链（`PUT /api/users/{name}/password`）

1. `handleUserAction` → `common.ValidSecret(password)` 校验（拒绝换行/控制字符）
2. `changePassword(username, password)` → `common.SudoExecStdin(..., "/opt/nas/scripts/set-user-pass.sh")` → sudo 匹配整脚本路径（命中）→ 脚本内校验（用户已存在，uid>=1000，放行）→ `exec chpasswd`
3. `common.SudoExecStdin(..., "smbpasswd", "-a", username, "-s")` → sudo 匹配 `/usr/bin/smbpasswd`（路径正确，无变化）
4. `common.SudoExec("htpasswd", "-b", "/etc/rclone-htpasswd", username, password)` → sudo 匹配 `/usr/bin/htpasswd`

**修复效果**：步骤 2 修复前同样卡在 sudo 拒绝，`chpasswd 失败: ...: sudo: 需要密码`（任务书证据 1 原文）。修复后正常。

### 组管理链（`PUT /api/user-groups/{name}/members`）

1. `handleGroupAction` → URL 路径段取 `groupName` → `isValidUsername(groupName) && isManagedGroup(groupName)`（Go 层第一道：格式 + GID 1000-59999 范围过滤，`sudo`/`root` 等系统组 GID<1000 天然被拒）→ 不满足直接 400，**不会调用到 SudoExec**
2. 通过后，`members` 表单值 → `validateMemberNames` 逐个过 `isValidUsername`
3. `setGroupMembers(groupName, membersStr)` → `common.SudoExec("/opt/nas/scripts/nas-group-member.sh", "set", groupName, 成员...)` → sudo 匹配整脚本路径（命中）→ 脚本内第二道校验：组名格式 + 黑名单 `{root,sudo,wheel,admin,shadow,disk,adm}` 命中即拒绝，成员名格式逐个校验 → 全部通过后 `exec gpasswd -M "成员CSV" groupName`

**修复效果（核心安全回归）**：`PUT /api/user-groups/sudo/members` 场景下，步骤 1 的 Go 层 `isManagedGroup` 已经拦截（`sudo` 组 GID=27 <1000），400 返回，根本不会走到步骤 3。即便假设 Go 层这道防线被绕过（例如面板存在未知的 RCE 或逻辑漏洞让 `groupName` 绕开 URL 路径解析），步骤 3 的脚本黑名单仍会在 `exec gpasswd` 之前二次拦截 `sudo`/`root` 等特权组——这是本任务新增的纵深防御第二道，修复前（裸 `gpasswd -a * *` 白名单通配）这道防线不存在，只要 Go 层防线被绕过就是直接 root 提权。

## 7. 验证记录

```
$ cd web && go build ./...
（无输出，构建成功）

$ go vet ./...
（无输出，无告警）

$ go test ./... -count=1
ok  	nas-panel	0.131s
ok  	nas-panel/common	1.394s
ok  	nas-panel/modules/ai	8.714s
ok  	nas-panel/modules/backup	0.098s
ok  	nas-panel/modules/config	0.009s
ok  	nas-panel/modules/dashboard	0.010s
ok  	nas-panel/modules/diagnostics	0.013s
ok  	nas-panel/modules/diskmgmt	1.117s
ok  	nas-panel/modules/events	0.280s
ok  	nas-panel/modules/filemgmt	0.330s
ok  	nas-panel/modules/firewall	0.023s
?   	nas-panel/modules/hardware	[no test files]
ok  	nas-panel/modules/health	0.613s
ok  	nas-panel/modules/logs	0.007s
ok  	nas-panel/modules/monitor	0.036s
ok  	nas-panel/modules/notifications	1.095s
ok  	nas-panel/modules/rclone	0.069s
ok  	nas-panel/modules/services	0.008s
?   	nas-panel/modules/storage	[no test files]
ok  	nas-panel/modules/system	0.008s
ok  	nas-panel/modules/tasks	4.964s
ok  	nas-panel/modules/triggers	6.051s
?   	nas-panel/modules/update	[no test files]
ok  	nas-panel/modules/users	0.235s
ok  	nas-panel/modules/vault	1.888s
ok  	nas-panel/modules/version	0.005s
全绿

$ bash -n scripts/set-user-pass.sh scripts/nas-group-member.sh scripts/setup.sh
（无输出，全部语法通过）

$ shellcheck scripts/set-user-pass.sh scripts/nas-group-member.sh
（无输出，零告警）

$ shellcheck scripts/setup.sh
（仅 2 条既有 info/warning，均为本轮未改动区域——第 41 行 GOARCH 未用、
 482/489 行 sudo+重定向组合提示——不属于本轮引入）

$ gofmt -l web/modules/users/*.go web/common/*.go
web/common/loginlimit_test.go
web/common/secret_test.go
（这两个文件本轮零改动，git diff 确认无变化，gofmt 告警是既有状态，非本轮引入）

# 模拟 setup.sh 生成 sudoers 文件内容并校验（NAS_USER=fm）
$ visudo -cf /tmp/opencode/test-sudoers-r3b
/tmp/opencode/test-sudoers-r3b：解析正确
exit: 0

# 白名单条目数 awk 实算（与 R3/R4 同一方法）
$ awk '/NOPASSWD:/ { line=$0; sub(/^.*NOPASSWD: /,"",line); sub(/" > "\$SUDOERS_FILE".*$/,"",line); gsub(/\\,/,"###COMMA###",line); n=split(line,arr,", "); print n }' scripts/setup.sh
143

# 确认白名单内已无 chpasswd/裸 gpasswd，新脚本各恰好出现一次
$ grep -n "/usr/bin/chpasswd\|/usr/bin/gpasswd" scripts/setup.sh
（无输出，确认已清除）
$ cat /tmp/opencode/test-sudoers-r3b | tr ',' '\n' | grep -n "set-user-pass\|nas-group-member"
5: /opt/nas/scripts/set-user-pass.sh
6: /opt/nas/scripts/nas-group-member.sh

# 确认 Go 侧零残留裸调用
$ cd web && grep -rn 'SudoExec.*"chpasswd"\|SudoExecStdin.*"chpasswd"\|"gpasswd"' --include="*.go" .
（无输出，确认已清除）
```

## 8. 提交记录

```
$ git add scripts/set-user-pass.sh scripts/nas-group-member.sh scripts/setup.sh \
    web/modules/users/users.go web/modules/users/create.go web/modules/users/groups.go
$ git commit -m "security(s1): 白名单实证修复 — chpasswd路径错配(sbin)根修+密码/组成员管理收口成校验脚本(灭裸chpasswd与gpasswd提权后门)+全二进制路径对帐(强化验收journal实证驱动)"
```

`TASK-S1-R3b.md`（任务书）与本报告 `OPENCODE-REPORT-S1-R3b.md` 均未 `git add`（红线 6）。

## 9. 偏离与疑虑

1. **`set-user-pass.sh` 的用户名正则比 Go 层 `isValidUsername` 更严格**：任务书明确指定脚本内校验用 `^[a-z][-a-z0-9_]{0,31}$`（纯小写），而 Go 层 `isValidUsername`（`create.go:190`）允许首字符大写字母（`r >= 'A' && r <= 'Z'`）。这意味着如果有一个大写开头的用户名（如 `Alice`）通过了 Go 层校验走到 `changePassword`/`CreateUserWithPassword`，会在脚本这一关被拒绝（`设置密码失败`），即使这个用户名本身并不危险。核实现有前端/测试用例：`create_test.go`/`users_passwd_test.go` 的测试用例全部用小写用户名（`alice`/`charinjecttest`），未发现产品侧有意使用大写用户名的场景，Linux 系统用户名惯例本身也以小写为主（`useradd` 默认拒绝大写，`NAME_REGEX` 系统配置通常也是小写）。判断这个口径差异是**可接受的收紧**而非回归——按任务书字面要求实现脚本正则，不因为 Go 层更宽松就放宽脚本正则（红线 4 禁止为过测试放宽白名单/校验）。如果 parent 认为 Go 层 `isValidUsername` 的大写许可本身是个该收紧的口径不一致问题，建议另开任务统一两处正则，本任务不在范围内扩大改动。

2. **`nas-group-member.sh` 的 `add`/`del` 子命令当前零调用方**：任务书第 C 节明确要求脚本支持 `set|add|del` 三个子命令，但核实 `web/modules/users/groups.go` 后发现面板的"组成员管理"功能只有一个"替换式设置"端点（`PUT /api/user-groups/{name}/members`），没有"单独添加一个成员"或"单独删除一个成员"的独立 API。按任务书字面设计实现了完整的三子命令脚本（为未来可能的增量式成员管理 API 预留能力面），但 `add`/`del` 两个子命令目前是"规格完整但无调用方"的状态。这不是引入了新的攻击面（脚本整体仍在黑名单校验之后才能执行任何子命令，三个子命令的安全边界一致），但如实记录这个"规格先行于实现"的情况供 parent 知悉。

3. **路径系统性对帐（任务 A）未能在真实 Debian 13 机器上执行**：本次会话的 `webfetch`/`websearch` 工具均不可用（请求被取消），无法联网核实 Debian 官方包数据库（如 `packages.debian.org` 的 `dpkg -L` 等价信息）。对帐结论基于：① 本机 Fedora 44 的 `command -v` 交叉验证（对 shadow-utils 系工具如 `useradd`/`usermod`/`groupadd`/`groupdel`/`gpasswd`/`passwd` 有效，这些包在 Fedora/Debian 间走同一上游源码，FHS 位置惯例一致）；② 任务书给出的 journal 实证直接定位到 `chpasswd`，吻合本机交叉验证发现的唯一路径错配；③ `smbpasswd`/`htpasswd`/`pdbedit` 本机未装对应包，无法直接验证，按 Debian 对应包（`samba-common-bin`/`apache2-utils`）的历史打包惯例判断为正确，**未虚构验证结果**，如实记录"本机无法直接验证"这一限制。`ufw`/`apt`/`apt-get`/`update-initramfs` 等 Debian 专属二进制同样无法在本机验证，R3/R4 报告里这些条目此前已经过路径审查且未被标记为问题，本轮未发现新证据表明它们有误，未改动。**建议真机强化复验时把这几个"本机无法验证"的条目也纳入 journal 拒绝记录抓取范围**，如果强化态下这几个条目也有解析路径问题，会在真机复验阶段暴露，而不是本报告能提前保证的。

4. **删除了 `gpasswd -d * *` 这个 Go 侧从无调用方的死条目**：任务书证据 2/3 聚焦的是 `-a`/`-M` 两种真实被调用的形状，但白名单里同时存在一条 `gpasswd -d * *`（R3 轮加入，但复核 `groups.go` 发现从未被 Go 代码实际调用——面板没有"从组里删除单个成员"的独立操作，只有"替换式设置整个成员列表"）。本轮一并删除这条死条目（非任务书直接要求，但符合"白名单条目与代码调用面吻合"的既有原则，且该条目本身若保留也是同等的 root-equivalent 风险——组名无约束的 `gpasswd -d` 虽然是"删除"而非"添加"，但对 `sudo` 组执行 `gpasswd -d` 不会造成提权，风险等级低于 `-a`/`-M`，但仍判断为应当随 `-a`/`-M` 一起清理，保持白名单"只保留真实调用形状"的一致性）。

5. **真机强化复验未执行**：按任务书"真机强化复验 parent 负责"的分工，本报告的全部验证限于：代码静态核实、`visudo -cf` 语法校验、`bash -n`/`shellcheck` 脚本检查、`go build`/`go vet`/`go test` 全绿、脚本桌面用例（本机非 root 测试环境下真实执行脚本观察校验逻辑是否正确拦截/放行）。无法在本次会话里验证"真实 Debian 13 机器上，`fm` 用户通过 sudo 调用 `/opt/nas/scripts/set-user-pass.sh` 和 `/opt/nas/scripts/nas-group-member.sh` 的端到端行为是否与预期一致（含文件权限 755、脚本是否可执行、`/opt/nas` 软链接是否正确解析到仓库 `scripts/` 目录等部署细节）"，这些必须在目标机器上二次验证。
