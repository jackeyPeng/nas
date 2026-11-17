# TASK-S1-R3: sudoers 白名单收紧 + 提权脚本审计（S1 计划 Task 3）

仓库：/home/jacky/soft/nas（分支 feat/least-privilege）。前置：R1 面板降权、R2 安装 fm 化已落地。先读 scripts/setup.sh 白名单段全文 + 计划 Task 3 收紧清单 + Global Constraints 白名单原则。

## 硬约束
1. 禁止读取/执行仓库外文件。
2. **调用面对帐先行**：grep 面板 Go 代码全部 SudoExec/SudoOutput/SudoExecStdin 调用点（含脚本内部 sudo 调用），产出「实际调用形状清单」；白名单条目必须与调用形状一一对帐——删掉的条目要证明无调用方，收窄的条目要证明覆盖所有实际参数形状。**宁可保守：拿不准的条目保留并记入残留风险表，不许拍脑袋删导致面板功能面静默炸**（套件会抓，但返工成本高）。
3. 每条判定用统一问题：「fm 能否借这条改写 /etc/shadow、/etc/sudoers.d、或获得等价无约束 root？」能→收窄到具体路径/参数形状；功能必需无法收窄→残留风险表记录理由（计划拍板：apt upgrade/update-initramfs/useradd 类属高权能功能面，明示不静默）。
4. 重点对象（计划清单逐条落实）：chown -R *（按调用形状收窄到 /data、/opt/nas、/var/lib/nas-monitor 等实际目标）；mount/umount（对照调用点）；openssl req/x509/rsa（-out 任意路径=可覆写系统文件：收窄或改专用脚本固定路径）；gpasswd（收窄 -a/-d 到 nasusers 组）；usermod（禁 sudo 组形状）；rm -f 类（已有路径限定的复核限定是否够窄）。
5. 提权脚本审计：add-user.sh / remove-user.sh / backup-config.sh / restore-config.sh / apply-upgrade.sh / backup-data.sh——逐个检查：变量引号包裹、无 eval、外部输入（用户名/路径参数）进命令前的校验（用户名形状白名单/路径前缀断言）、rm 通配范围。发现问题即修（最小 diff），审计结论逐脚本写进报告。
6. 白名单生成后 visudo -cf 语法校验必须过（setup.sh 内已有机制则复用，没有则加）。
7. 面板 Go 代码如因收窄需要调整调用形状（如 chown 目标参数化），改动最小化 + 相关单测跟进；go test ./... 全绿。
8. 单提交，信息用计划 Task 3 原文。不 git add 任务书与 OPENCODE-REPORT-S1-R3.md。

## 验证与提交
1. bash -n 全部改动脚本；cd web && go build ./... && go vet ./... && go test ./... -count=1 全绿
2. 报告写 OPENCODE-REPORT-S1-R3.md：调用面-白名单对帐表（全量）、逐条判定结论（收窄前后对照/保留理由）、残留风险拍板表、六脚本审计结论、visudo 校验输出、条目数前后对比、偏离与疑虑。真机负面测试（sudo cat /etc/shadow 被拒等）parent 验收负责。
