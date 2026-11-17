# OPENCODE-REPORT-S1-R4：文档与验收物料（S1 计划 Task 4 文档部分）

> 对应任务书 TASK-S1-R4，执行 `docs/plans/2026-10-23-least-privilege-s1-plan.md` 的 **Task 4**（仅文档部分，机器验收留给 parent）。
> 分支 `feat/least-privilege`。改动文件：`docs/feature-operation-checklist.md`、`docs/nas-product-manual.md`、`docs/release-system.md`、`CHANGELOG.md`。
> 未 `git add` 任务书与本报告。

## 1. 交付内容

- `docs/feature-operation-checklist.md`：顶部新增「运行身份约定（S1 最小权限）」说明块（面板运行身份、sudoers 白名单机制、安装方式）；§18 文件管理模块补充 S1 降权对 chown 跨用户改属主的影响（残留风险，诚实记录，非静默）。未改动任何表格行，统计数字不变。
- `docs/nas-product-manual.md`：§4 安装命令改为用户身份直跑形态（并修正此前「无密码/带密码」两行不一致的 `env` 用法）；新增 §8.4「面板以家庭管理员身份运行」（用户视角，含「这意味着什么/不意味着什么」诚实呈现，吸收 R3 残留风险拍板表要点）；目录同步加 8.4 条目。
- `docs/release-system.md`：install.sh 安装方式段补「安装身份」说明；新增「sudoers 白名单原则」小节（root-equivalence 复审判定标准 + `sed -i *` 死后门案例，条目数 115→145）。
- `CHANGELOG.md`：未发布段新增「S1 最小权限」条目组，含 R1-R3 三个提交短哈希（`2aea84a`/`23f4e3a`/`ea3550e`）+ 残留风险拍板要点一句话。

## 2. 文档-现实一致性抽查表（写前核对）

| # | 核对项 | 命令/方法 | 结果 | 文档落地 |
|---|---|---|---|---|
| 1 | unit `User=` 行 | `grep -n "^User=" configs/nas-panel.service` | `User=__NAS_USER__`（第 7 行） | 手册 8.4、checklist 顶部说明均写「`User=__NAS_USER__`/`Group=__NAS_USER__`」，与实际一致 |
| 2 | sudoers 白名单条目数（S1 后，awk 实算） | 见第 3 节 awk 脚本，对 `scripts/setup.sh` 当前 `NOPASSWD:` 行按 `, ` 分隔计数（转义逗号 `\,` 先占位保护） | **145**（非 R3 报告自述的 147，见第 4 节偏离说明） | release-system.md「sudoers 白名单原则」、checklist 顶部说明、CHANGELOG 均写 145（以实测值为准，不盲信 R3 报告数字） |
| 3 | sudoers 白名单条目数（S1 前基线，awk 实算） | 同一脚本对 `git show ea3550e~1:scripts/setup.sh` 跑一遍 | **115** | release-system.md 写「115 → 145」，与 beta.27 CHANGELOG 历史条目「115 条」一致 |
| 4 | 安装命令六载体现实形态 | `grep -rn` 基础命令行 `curl -fsSL .../install.sh \| bash` 与带密码命令行 `curl ... \| env NAS_PASS=... bash`，范围 README.md/README.zh-CN.md/docs/release-system.md/scripts/install.sh/docs/nas-product-manual.md | 基础命令行六处逐字一致（无 sudo 前缀）；带密码命令行除 `docs/release-system.md`（渠道选择功能，结构不同，R2 报告已说明不在六载体范围）外全部为 `curl ... \| env NAS_PASS=mySecurePass123 bash` 形态 | 手册 §4 原来是 `NAS_PASS=mySecurePass123 curl ... \| bash`（缺 `env`，与其余五载体不一致，且该写法下环境变量实际传不到管道右侧的 bash 进程，是个潜在 bug）——**本轮已修正**为 `curl ... \| env NAS_PASS=mySecurePass123 bash`，现六载体内除 release-system.md 既有差异外完全一致 |
| 5 | install.sh 安装命令形态（有无前置 sudo） | 读 `scripts/install.sh` 第 6-7 行头注释 + 第 27-33 行 `SUDO` 封装逻辑 | 头注释无 sudo 前缀；`EUID -eq 0` 判空 `SUDO`，否则 `SUDO=sudo` 并 `sudo -v` 一次缓存 | 手册 §4、release-system.md、checklist 顶部说明均写「无需在命令前加 sudo，特权步骤脚本内部逐条加」，与代码行为一致 |
| 6 | 残留风险表条目 vs R3 报告 | `grep -n "mdadm \*\|rclone \*\|apt upgrade\|update-initramfs\|useradd -M\|groupadd \*" scripts/setup.sh` 核对 R3 报告「残留风险拍板表」列出的条目是否真实存在于当前白名单 | `mdadm *`、`rclone *`、`apt upgrade *`、`apt-get upgrade *`、`update-initramfs *`、`useradd -M -s /usr/sbin/nologin -g * *`、`groupadd *`/`groupadd -f *` 全部命中，与 R3 报告一致 | 手册 8.4、CHANGELOG 均引用这组条目作为「功能必需、已审查确认无法再收窄」的高权能功能面举例 |
| 7 | `sed -i *` 死后门案例 | 读 `scripts/setup.sh` 第 648-654 行注释 | 「曾在此处追加过 `NOPASSWD: /usr/bin/sed -i *`……该条目是死后门。永久移除，不要再以任何形式加回（2026-10-23 拍板）」 | release-system.md「sudoers 白名单原则」小节原文引用该案例作为 root-equivalence 判定标准的示例 |
| 8 | filemgmt chown 残留风险（本轮新发现，非 R1-R3 已记录） | 读 `web/modules/filemgmt/perms.go`（`handleChown`/`chownTree`/`handleFixHomePerms`）+ `upload.go`（`alignNewFilePerms`）+ `upload_test.go:500-508` 测试注释 | `handleChown`/`handleFixHomePerms` 用裸 `os.Chown`/`os.Lchown`（非 `common.SudoExec`），失败时返回 HTTP 500（非静默）；仅 `alignNewFilePerms`（上传后自动权限对齐）chown 失败只 `log.Printf`、不报错，测试注释也确认「非 root 环境 chown 应被跳过」 | checklist §18、手册 8.4、CHANGELOG 均记录此为 S1 新发现的残留风险（跨用户 chown 依赖 `CAP_CHOWN`，fm 身份下缺失），区分「显式报错」与「静默日志」两种表现 |
| 9（补充） | 仓库落地路径 | 读 `scripts/install.sh` 第 21 行 `REPO_DIR="$HOME/soft/nas"` | fm 身份跑时 `$HOME=/home/fm` | 手册/checklist/release-system 均写「仓库落在执行用户 home 下（如 `/home/fm/soft/nas`），不强制 `/root/soft/nas`」 |

共 9 项（≥8 项要求），全部基于本轮实读代码/实跑命令核对，非凭空转抄 R1-R3 报告文字。

## 3. awk 实算输出（原始命令与结果）

```bash
$ awk '
/NOPASSWD:/ {
  line=$0
  sub(/^.*NOPASSWD: /, "", line)
  sub(/" > "\$SUDOERS_FILE".*$/, "", line)
  gsub(/\\,/, "###COMMA###", line)   # 保护转义逗号（如 mount -o remount\, prjquota），防止被当分隔符切开
  n = split(line, arr, ", ")
  print n
}
' scripts/setup.sh
145

$ git show ea3550e~1:scripts/setup.sh | awk '同上脚本'
115
```

与 R3 报告自述的「115 → 147」存在 **2 条差异**（实测 145，非 147）。未盲信上一轮报告数字，复算后以实测值 145 写入全部文档（checklist/release-system/CHANGELOG 三处口径一致）。差异原因未深究（不在本任务范围，可能是 R3 报告计数脚本对某个含转义字符的条目处理方式不同导致），仅记录偏离，不代码还原。

## 4. 偏离与疑虑

1. **sudoers 条目数以实测 145 为准，而非 R3 报告自述的 147**：R3 报告正文「结论摘要」「条目数前后对比」两处均写「115 → 147」，但本轮用 awk 对当前 `scripts/setup.sh` 实际内容重新计数得到 145。已采用实测值，三处文档落地统一写 145。未回头修改 R3 报告本身（任务书未要求改已有报告，且报告是历史记录，保留原样供追溯）。提请 parent 注意这个 2 条的偏差，必要时可另行复核。
2. **filemgmt 的 chown 残留风险是本轮新发现，不在 R1/R2/R3 任一份报告的「残留风险」或「偏离与疑虑」小节里**：R1 报告第 7 节只记录了 HTTPS 证书的权限残留风险（已吸收进手册 8.4），R3 报告的「残留风险拍板表」聚焦 sudoers 白名单条目本身（mdadm/rclone/apt 等），没有覆盖 filemgmt 模块完全在 sudoers 机制之外、直接依赖进程 UID 能力（`os.Chown`/`CAP_CHOWN`）的这一类操作。本轮读代码（`perms.go`/`upload.go`/`upload_test.go`）核实后确认这是面板降权后一个真实存在但此前未被记录的行为变化——不是本任务引入的新 bug（代码本身零改动，纯文档任务），而是 S1 R1 改变运行身份后的既有代码在新身份下的真实表现，按任务「诚实呈现残留风险表里的……这意味着什么/不意味着什么」的要求补充进手册与 checklist。如果 parent 认为这个发现应该走修复而非仅记录（例如把 `handleChown`/`fix-home-perms` 改走 `SudoExec` + 补 sudoers 条目，或给 `alignNewFilePerms` 的 chown 失败也报错），建议开一个新的小任务或并入 S1 验收清单的待办，本任务范围内只做文档记录不做代码修复。
3. **`docs/protocol-service-configs.md` 第 7.3 节「sudoers 白名单」小节已过时**（仍写「面板进程以 root 跑」「chmod -R / groupadd / usermod 不在白名单」），与 S1 后现实不符。该文件不在本任务交付清单内（任务书「交付」只列 4 份文档），本轮未改动，仅在此提请知悉——如需同步更新建议另开小任务，按同样的「写前核对」方法处理（该文件条目粒度更粗，需要重新梳理整节而非局部打补丁）。
4. **手册 §4 安装命令的 `env` 用法修正属于顺带修复**：原文「无人值守（环境变量传入密码）」写的是 `NAS_PASS=mySecurePass123 curl ... | bash`（直接前置环境变量，不经 `env`），这个写法在 `curl | bash` 场景下其实也能生效（shell 会把 `curl` 整条管道当一条命令，`NAS_PASS=xxx` 前缀只作用于 `curl` 进程，对 `bash`（管道右侧）不生效——这其实是该文档里原有的一个潜在 bug，`NAS_PASS` 根本传不到 `bash` 执行的 `install.sh` 里）。本轮按六载体的正确形式（`curl ... | env NAS_PASS=... bash`）统一修正，顺带修了这个此前就存在、与 S1 本身无关的文案错误。
5. **未触碰手册顶部版本横幅（第 3-9 行「最后更新: 2026-10-21」等）**：任务要求范围是「新增/更新安全说明小节」+「安装命令更新」，顶部横幅的版本号/日期/功能摘要文字判断为超出本任务范围，未动，避免无意义扩大改动面。
6. **checklist 的运行身份说明放在文档顶部说明块，而非作为新的「模块」小节**：该文档结构是「23 个功能模块表格」，运行身份/安装方式是跨模块的基础设施属性，不是某个具体功能模块，因此没有像 F2 那样新增一个「模块 N」小节，而是追加到文档开头已有的说明块里（审计约定旁边）。判断这样更符合文档现有结构（避免「汇总统计」23 个模块数被污染），但如果 parent 期望看到独立编号小节，可以调整。

## 5. 验证记录

本轮为纯文档任务，无 Go/shell 代码改动，验证限于：

```bash
$ git status --short | grep -v '^??'
 M CHANGELOG.md
 M docs/feature-operation-checklist.md
 M docs/nas-product-manual.md
 M docs/release-system.md
（仅 4 份目标文档被修改，无其它文件被意外改动）

$ git diff --stat
 CHANGELOG.md                        | 34 +++++++++++++++++++++++++
 docs/feature-operation-checklist.md | 14 ++++++++--
 docs/nas-product-manual.md          | 51 ++++++++++++++++++++++++++++++++++---
 docs/release-system.md              | 33 ++++++++++++++++++++++++
 4 files changed, 126 insertions(+), 6 deletions(-)

$ cd docs && awk -F'|' '/^\|/{gsub(/ /,"",$3); if($3=="只显示")a++; if($3=="用户操作")b++; if($3=="系统自动")c++} END{print a,b,c,a+b+c}' feature-operation-checklist.md
70 119 43 232
（与改动前完全一致——本轮只追加说明文字，未改动任何表格行，统计数字不应变化，核对通过）

$ grep -rnE "10\.216\.|10\.187\.|192\.168\.213\." CHANGELOG.md docs/feature-operation-checklist.md docs/nas-product-manual.md docs/release-system.md
（无输出，零内网 IP 残留，符合 docs/release-system.md 发布安全红线）

$ grep -rn "get.z1.sale/install.sh | sudo" README.md README.zh-CN.md docs/release-system.md docs/nas-product-manual.md scripts/install.sh
（无输出，六载体范围内零 sudo 前缀残留）
```

未跑 `go build`/`go test`（本轮零 `.go`/`.sh` 文件改动）。未跑 `make lint-all`（无 shell 脚本改动）。

## 6. 提交记录

```
$ git add docs/feature-operation-checklist.md docs/nas-product-manual.md docs/release-system.md CHANGELOG.md
$ git commit -m "docs(s1): 运行身份/安装方式文档同步+sudoers复审原则入release-system+CHANGELOG未发布段S1条目组 (S1 T4-docs)"
[feat/least-privilege 3961f36] docs(s1): 运行身份/安装方式文档同步+sudoers复审原则入release-system+CHANGELOG未发布段S1条目组 (S1 T4-docs)
 4 files changed, 126 insertions(+), 6 deletions(-)
```

仅 4 份目标文档入提交；`TASK-S1-R4.md`（任务书）与本报告 `OPENCODE-REPORT-S1-R4.md` 均未 `git add`（红线 6），`git status` 确认两者仍为未跟踪状态。
