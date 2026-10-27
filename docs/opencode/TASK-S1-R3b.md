# TASK-S1-R3b: 白名单实证修复轮（强化验收抓到的拒绝面 + root-equivalent 收口）

仓库：/home/jacky/soft/nas（分支 feat/least-privilege）。前置：R1-R4 已落地；48 真机强化验收（收紧 %sudo NOPASSWD 后面板仅剩白名单）抓到以下**实证问题**，本轮修复。

## 实证证据（journal 拒绝记录 + API 响应，2026-10-26 采集）
1. `fm : command not allowed ; COMMAND=/usr/sbin/chpasswd` —— 白名单写 `/usr/bin/chpasswd`，Debian 13 实际解析 `/usr/sbin/chpasswd`。后果链：POST /api/users 建户在设密码步炸（`设置密码失败: sudo: 需要密码`）、PUT password 同炸 → 套件 user-home/share 回收站/chown/fix-home-perms/SMB 12 项连锁 FAIL。
2. `gpasswd -a fm sudo` 在白名单下**被放行**（`/usr/bin/gpasswd -a * *` 组名无约束）——fm 可自加 sudo 组 = 完整 root 提权，root-equivalent 后门。
3. `gpasswd -M "" <group>`（groups.go:227 实际调用形状）白名单**无此条目**——用户组功能面在强化态必拒。
4. 裸 `/usr/bin/chpasswd` 条目本身 root-equivalent：`echo "root:x" | sudo chpasswd` 可设 root 密码接管整机——与已铲除的 sed -i * 同类，必须收口。

## 修复设计（拍板，按此实现）
### A. 路径系统性对帐（防同类潜伏）
白名单每个二进制：在 Debian 13 上 `command -v` 的解析路径 vs 条目路径逐一核对（chpasswd 是 sbin 类工具写成了 bin；同类嫌疑：chpasswd/smbpasswd/htpasswd/passwd/useradd/usermod/groupadd/groupdel/gpasswd/pdbedit 等 sbin/usr-bin 归属逐个查）。不一致的修条目。产出对帐表进报告（二进制/条目路径/实际解析/动作）。注意 sudo 匹配的是解析后绝对路径。

### B. 密码设置收口成脚本（消灭裸 chpasswd）
- 新建 `scripts/set-user-pass.sh`：stdin 读 `username:password` 行；校验 username 形状 `^[a-z][-a-z0-9_]{0,31}$`、拒绝 root、拒绝 uid<1000 的既有系统用户（用户不存在时允许=建户场景，但名字形状校验先行）；然后 exec chpasswd。整脚本进白名单（替代裸 chpasswd 条目），面板 users.go:376 与 create.go:144 的 SudoExecStdin(…,"chpasswd") 改调该脚本（路径 /opt/nas/scripts/set-user-pass.sh）。add-user.sh 内部 chpasswd 不动（脚本已整体白名单、以 root 跑）。
- 单测：校验函数的用户名形状/拒绝 root 逻辑（Go 侧调用形状改动跑既有 users 测试全绿；脚本本体 bash -n + 桌面用例写进报告）。

### C. 组成员管理收口成脚本（消灭裸 gpasswd）
- 新建 `scripts/nas-group-member.sh`：参数 `set|add|del <group> [user...]`；拒绝特权组黑名单 {root,sudo,wheel,admin,shadow,disk,adm}；set→gpasswd -M（空成员列表支持）、add→gpasswd -a、del→gpasswd -d。整脚本进白名单（替代三条裸 gpasswd 条目），groups.go 调用点改造。
- 单测同上（黑名单/形状校验纯函数化可测部分）。

### D. 白名单条目增删
- 删：裸 chpasswd、gpasswd -a/-d 三条
- 增：/opt/nas/scripts/set-user-pass.sh、/opt/nas/scripts/nas-group-member.sh（照 add-user.sh 条目形状）
- 修：A 对帐出的全部路径
- useradd 第二形状 `-g * *`（pending_ops.go:262 实际 -g <name> <name>）核对是否可收窄到实际调用（共享占位用户组名形状），不能则残留风险表记录
- visudo -cf 校验；条目总数前后对比进报告

## 硬约束
1. 禁止读取/执行仓库外文件；验证 go build/vet/test + bash -n（有 shellcheck 则跑）。
2. 新增 Go 依赖 0；Go 改动最小化（只动调用形状，不改功能语义）；相关既有测试全绿。
3. 脚本风格对照 add-user.sh（set -euo pipefail、输入校验先行、错误信息中文）。
4. **不许为过测试放宽白名单**——收窄是方向，放宽必须给证据（journal 拒绝记录或调用点 grep）。
5. 单提交，信息：`security(s1): 白名单实证修复 — chpasswd路径错配(sbin)根修+密码/组成员管理收口成校验脚本(灭裸chpasswd与gpasswd提权后门)+全二进制路径对帐(强化验收journal实证驱动)`
6. 不 git add 任务书与 OPENCODE-REPORT-S1-R3b.md。

## 验证与提交
1. cd web && go build ./... && go vet ./... && go test ./... -count=1 全绿
2. bash -n 全部脚本；sudoers 生成段 visudo -cf 过（本地可用 visudo -cf -f <(...) 或临时文件校验）
3. 报告写 OPENCODE-REPORT-S1-R3b.md：A 对帐表全量、B/C 脚本原文、调用点 diff 清单、白名单前后 diff、残留风险表更新、桌面推演（建户/改密/组管理三条链在强化态的完整 sudo 匹配路径）、偏离与疑虑。真机强化复验 parent 负责。
