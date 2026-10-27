# TASK-S1-R3c: 隐性 root 依赖系统修复（48 真机强化验收第二轮实证驱动）

仓库：/home/jacky/soft/nas（分支 feat/least-privilege）。前置：R1-R3b 已落地。48 真机以 fm 面板跑用户级全流程，抓到一批「root 面板时代隐性成立、fm 面板下静默失败/功能回归」的问题。本轮全部修复。**所有修改先 grep 调用点核实形状，以代码现实为准。**

## 实证问题清单（parent 真机采集，journal + fstab + API 响应为证）

### A. fstab 静默不写（已确认根因）
- 现象：建池向导报「写入 fstab 持久化」步骤成功，/etc/fstab 实际无条目，注册表 warn；journal 无 sudo 拒绝（因为根本没走 sudo）。
- 根因：wizard.go:293 / stream.go(4处) `common.ExecOutput("blkid","-s","UUID","-o","value",dev)` 不带 sudo——blkid 读超级块需 root，fm 下返回空 → writeFstab(uuid=="") 静默 return（wizard.go:451-453），步骤照样 append「写入 fstab」。
- 修复：① 全部 blkid 调用（含 `-s TYPE` 形状，grep 全量盘点约 11 处）改 SudoOutput；② 白名单补 `/usr/bin/blkid -s UUID -o value *` 之外的实际形状（TYPE 变体等，对照盘点）；③ writeFstab 改为返回 error，uuid 为空或写入失败时向导步骤记「失败: …」而不是假装成功（stream.go 各调用点同步）。

### B. journalctl 三处无权限空转
- services.go:47（-u <name> -n 50 --no-pager）、users/logs.go:121（-u sshd -n N --no-pager -o short）、diskmgmt/overview.go:882（-u nas-panel --no-pager -n N -o short-iso）——fm 不在 adm 组，全部返回空/报错。
- 修复：三处改 SudoOutput + 白名单按**实际参数顺序**加三条精确形状（sudoers 通配是位置敏感的，`-u * -n * --no-pager` 与 `-u * -n * --no-pager -o short` 是两条）。只读日志，安全。

### C. 文件权限 API 白名单形状缺失（强化态套件 12 FAIL 的主因之二）
- 现象：套件「chown生效 want=[alice] got=[fm]」「fix-home-perms FAIL」。
- 根因：白名单 chown 只有 `-R` 形状（`/bin/chown -R *\:* /data` 与 `/data/*`），filemgmt 权限 API 的单文件 chown（无 -R）被拒；chmod 对 /data 路径**零条目**（只有 /etc/nas 证书三条），文件权限修改/fix-home-perms 的 chmod 700 全被拒。
- 修复：grep filemgmt/perms、users/perms（fix-home-perms）实际调用形状，白名单补：非 -R chown 限定 `/data/*` 目标；chmod 限定 `/data/*` 目标（模式参数放开——池内管理语义，路径已限死）。全部形状以调用点为准。

### D. 面板读不了用户 home（fm 面板最大功能回归）——ACL 方案（parent 拍板）
- 现象：home 是 700 alice:alice，root 面板天然可读全部；fm 面板下管理员文件管理器浏览用户主目录/预览/du 统计全部 EACCES。
- **拍板方案：POSIX ACL**——每个用户 home 授予 `u:<NAS_USER>:rwx` + 默认 ACL `d:u:<NAS_USER>:rwx`（新文件自动继承），管理员恢复全 home 管理能力，其他用户之间隔离不变（SMB valid users + 无 ACL 授权），du/preview 原生可用无需 sudo 热路径。
- 实现：
  1. 新建 `scripts/nas-home-acl.sh <home_path>`：校验路径必须是 /data/nas*/<单段用户名> 且目录存在；`setfacl -m u:$NAS_USER:rwx`、`setfacl -d -m u:$NAS_USER:rwx`、存量内容 `setfacl -R -m u:$NAS_USER:rx`（目录另加 x：用 -R -m u:$NAS_USER:rX 大写 X 只给目录/已可执行文件 x）；NAS_USER 从 /opt/nas/.env 读或入参。整脚本进白名单（一条）。
  2. 挂钩点：add-user.sh 建 home 后调用；wizard 建 home 处调用；fix-home-perms handler 调用（修权限同时补 ACL）；setup.sh 属主迁移段对既有 home 幂等补挂（已迁移机器升级路径）。
  3. setup.sh apt 包列表加 `acl`（Debian 最小化可能无 setfacl）；面板代码不直接调 setfacl（全走脚本，白名单面最小）。
  4. Go 侧 fix-home-perms/perms handler 如直接 SudoExec setfacl 则改调脚本。
- 测试：脚本 bash -n + shellcheck；Go 调用点改动跑既有测试；桌面推演（建户→home ACL→fm 可浏览→alice SMB 隔离不变）写进报告。

### E. 遗留裸条目收窄（R3b 记录在案的残留）
- `htpasswd`（可写任意路径=可毁 /etc/shadow）：grep 调用点（应全是 /etc/rclone-htpasswd 的 -b/-D 形状），白名单收窄到 `/usr/bin/htpasswd -b /etc/rclone-htpasswd *`、`/usr/bin/htpasswd -D /etc/rclone-htpasswd *`（以实际形状为准）。
- `smbpasswd`：grep 调用点（-a/-e/-x/-s 组合），收窄到实际形状 + 用户名参数位。
- `pdbedit`：同上（-L/-x/-u 等实际形状）。
- 收窄后强化态功能面由 parent 套件复验兜底；拿不准的形状宁可保留并记残留风险表（原则：不为过测试放宽，也不凭想象收窄到功能炸）。

## 硬约束
1. 禁止读取/执行仓库外文件；验证 go build/vet/test + bash -n + shellcheck + visudo -cf。
2. 新增 Go 依赖 0；apt 新增仅 acl（D 项拍板）。
3. 白名单改动逐条给证据（调用点 grep 输出或 journal 拒绝记录）；条目数前后对比 + 全量 diff 进报告。
4. 静默失败零容忍：本轮所有「错误被吞」点（writeFstab 类）改为显式失败传播——同类 grep 盘点（wizard/stream 里 steps append 前不看 err 的模式）顺手修，修了哪些列报告。
5. 单提交，信息：`fix(s1): 隐性root依赖系统修复 — blkid/fstab静默失败根修+journalctl白名单三形状+chown/chmod池内形状补齐+用户home ACL方案(nas-home-acl.sh,管理员恢复全home能力,用户间隔离不变)+htpasswd/smbpasswd/pdbedit裸条目收窄(48真机强化验收实证驱动)`
6. 不 git add 任务书与 OPENCODE-REPORT-S1-R3c.md。

## 验证与提交
1. cd web && go build ./... && go vet ./... && go test ./... -count=1 全绿
2. bash -n 全部改动脚本 + 新脚本；visudo -cf 校验生成的 sudoers 行
3. 报告 OPENCODE-REPORT-S1-R3c.md：A-E 逐项修复对照（调用点盘点表/白名单 diff/ACL 挂钩点清单/静默失败盘点结果）、条目数对比、桌面推演、偏离与疑虑。真机强化复验 parent 负责。
