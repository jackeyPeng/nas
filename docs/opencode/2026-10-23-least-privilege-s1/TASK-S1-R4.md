# TASK-S1-R4: 文档与验收物料（S1 计划 Task 4 文档部分）

仓库：/home/jacky/soft/nas（分支 feat/least-privilege）。前置：R1-R3 已落地（面板降权/安装 fm 化/白名单收紧）。机器验收 parent 负责，本轮只文档。

## 必读
docs/plans/2026-10-23-least-privilege-s1-plan.md 的 **Task 4** + R1-R3 报告（OPENCODE-REPORT-S1-R*.md 的残留风险拍板表要吸收进手册安全说明）。

## 交付
1. **docs/feature-operation-checklist.md**：受影响条目更新（安装方式=用户身份直跑、面板运行身份、sudoers 白名单条目数变化）；统计若变动用附录 awk 实算。
2. **docs/nas-product-manual.md**：新增/更新安全说明小节：「面板以家庭管理员身份运行」用户视角解释（不是 root 常驻、特权操作走受限白名单、这意味着什么/不意味着什么——诚实呈现残留风险表里的高权能功能面如系统更新）；安装命令更新为用户身份直跑形态。
3. **docs/release-system.md**：sudoers 原则段补「root-equivalence 复审判定标准」（能改 /etc/shadow 或 sudoers 即后门，sed -i * 案例记录）+ 安装命令形态更新。
4. **CHANGELOG.md**：未发布段 S1 条目组合并完善（R1-R3 的提交短哈希 + 残留风险拍板要点一句话）。
5. 红线：不出现真实测试机 IP；与代码/脚本现实一致（写前核对 unit 文件、setup.sh 白名单段、install.sh 命令形态）。
6. 不 git add 任务书与 OPENCODE-REPORT-S1-R4.md。

## 验证与提交
1. 文档-现实一致性抽查表（≥8 项：unit User= 行、白名单条目数、安装命令六载体、残留风险表条目 vs R3 报告）写进报告
2. 提交信息：`docs(s1): 运行身份/安装方式文档同步+sudoers复审原则入release-system+CHANGELOG未发布段S1条目组 (S1 T4-docs)`
3. 报告写 OPENCODE-REPORT-S1-R4.md：抽查表、awk 实算输出（若清单变动）、偏离。
