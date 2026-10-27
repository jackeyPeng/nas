# TASK-S1-R2: install.sh 以 fm 身份跑 + 六载体文案同步（S1 计划 Task 2）

仓库：/home/jacky/soft/nas（分支 feat/least-privilege）。前置：R1 已落地面板降权（unit User=/属主迁移）。先读 scripts/install.sh、scripts/cleanup.sh 全文与计划 Task 2 + Global Constraints 的安装铁律。

## 硬约束
1. 禁止读取/执行仓库外文件。
2. **无交互式 read 铁律**：curl|bash 下 stdin 是脚本自身文本，任何 read/read -s 都会死循环——sudo 密码走开头一次性 `sudo -v`（tty 交互由 sudo 自己完成），后续特权命令统一走封装函数 `SUDO`（EUID=0 时 SUDO 为空串，root 直跑完全兼容；EUID≠0 时 SUDO=sudo）。
3. REPO_DIR="$HOME/soft/nas" 语义保持——fm 跑时自然落 /home/fm/soft/nas；脚本内一切引用 REPO_DIR 的路径不得写死 /root。
4. 特权步骤盘点后逐个加 $SUDO 前缀：apt/dpkg、二进制安装到 /usr/local/bin、systemctl、useradd/组、ufw、mkdir/chown 系统目录、调用 setup.sh（`$SUDO bash setup.sh` 且确保 SUDO_USER 语义正确传递 NAS_USER——fm 用 sudo 跑 setup.sh 时 SUDO_USER=fm 天然成立；root 直跑时 SUDO_USER 为空，setup.sh 68 行已有 ${NAS_USER:-${SUDO_USER:-$USER}} 兜底链，核对 install.sh 传入的 NAS_USER env 路径不破）。
5. NAS_PASS 环境变量路径保留：fm 跑时 `curl … | env NAS_PASS=*** bash`（无 sudo 前缀，env 直接可见）；文档同步该写法。缺省=出厂默认密码+首登改密路径语义不变。
6. cleanup.sh 同款 SUDO 封装改造（fm 可跑，root 兼容），保留「仓库实体 readlink 解析删除」逻辑（2b9d5b7 的修复不许回退）。
7. **六载体安装命令逐字一致**：README.md / README.zh-CN.md / docs/release-system.md / scripts/install.sh 头注释 / 官网 download.md / 官网 zh/download.md——新命令 `curl -fsSL https://get.z1.sale/install.sh | bash`，带密码写法 `curl -fsSL https://get.z1.sale/install.sh | env NAS_PASS=*** bash`（占位符形态，中英文页命令部分逐字相同）。旧 `sudo bash`/`sudo env` 写法全部更新，grep 验证零残留。
8. 官网仓（/home/jacky/soft/nas-website）两个 download md 改动后：`npx vitepress build` 必须过 + dist grep 新命令 + md 与 dist 同 commit 单独提交（信息 `docs: 安装命令去sudo前缀(fm身份直跑,S1)`）。**不跑 upload-r2**（parent 验收通过后统一重传线上）。
9. install.sh 改完自测：`bash -n` + 桌面推演（报告里写 fm 路径/root 路径两条执行序列）；能 shellcheck 则跑。
10. 单提交（nas 仓），信息用计划 Task 2 原文。不 git add 任务书与 OPENCODE-REPORT-S1-R2.md。

## 验证与提交
1. bash -n scripts/install.sh scripts/cleanup.sh；grep 六载体命令一致性输出对照表进报告
2. cd web && go build ./... && go test ./... -count=1 无回归（本轮理论上零 Go 改动）
3. 报告写 OPENCODE-REPORT-S1-R2.md：SUDO 封装实现、特权步骤盘点清单（逐个标注加没加前缀）、NAS_USER 传递链推演、六载体 grep 对照表、官网 build 验证输出、偏离与疑虑。真机双路径安装验收 parent 负责。
