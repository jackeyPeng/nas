# TASK-S1-R1: 面板服务降权 fm（S1 计划 Task 1）

仓库：/home/jacky/soft/nas（分支 feat/least-privilege）。Go module 在 web/。
执行方式：严格按 docs/plans/2026-10-23-least-privilege-s1-plan.md 的 **Task 1** 执行（含 Global Constraints 与「代码现实锚点」——锚点带文件行号，写码前逐个核对）。

## 硬约束
1. 禁止读取/执行仓库外文件；Go 验证只用 go build/vet/test；shell 验证 bash -n + （如有）shellcheck。
2. 属主迁移必须幂等：重复跑 setup.sh / apply-upgrade.sh 零副作用；迁移前判断当前属主。
3. readlink -f 解析 /opt/nas 软链接后对**实体目录**chown（cleanup.sh 穿越事故的镜像教训：一切路径操作认实体不认链接）。
4. rclone conf 迁移：/root/.config/rclone/rclone.conf 存在才迁；目标 ~NAS_USER/.config/rclone/；权限 600 属主 fm；迁移后源文件删除或留 .migrated 标记（二选一，报告说明理由）；不迁空文件覆盖已有目标。
5. /etc/nas/certs 处理：证书 fm 可读方案落地（私钥 640 root:fm 或等价），面板 HTTPS 启动路径回归（go 侧 cert 读取逻辑不改）。
6. unit 模板只加 User=/Group= 两行（__NAS_USER__ 占位符同款替换），其余字段不动；setup.sh 的 sed 替换逻辑核对能同时替换新行。
7. apply-upgrade.sh 的迁移调用与 setup.sh 共用同一段逻辑（抽公共脚本或函数复制处注明同源，禁止两处漂移）。
8. Go 代码原则上零改动；若 rclone.go 需要小调整（如日志提示 conf 实际路径），改动最小化并补测试。
9. 单提交，信息用计划 Task 1 原文。不 git add 任务书与 OPENCODE-REPORT-S1-R1.md。

## 验证与提交
1. bash -n scripts/setup.sh scripts/apply-upgrade.sh；cd web && go build ./... && go test ./... -count=1 全绿（Go 若有改动加 -race 针对性跑）
2. 迁移逻辑桌面推演写进报告：老机器（root 面板+root data）跑 apply-upgrade 后的状态序列；新装机器跑 setup 的序列；重复跑第二遍的序列（幂等证明）
3. 报告写 OPENCODE-REPORT-S1-R1.md：unit diff、迁移段脚本原文、rclone conf 迁移决策、证书权限方案、幂等推演、Go 改动清单（如有）、偏离与疑虑。真机验证 parent 负责。
