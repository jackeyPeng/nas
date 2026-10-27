# opencode 工作产物归档

opencode 编码代理各轮次的工作文件，从仓库根目录归档至此（2026-10-27）。这些文件被提交信息、
CHANGELOG 和 docs/plans/ 中的实施计划以轮次代号引用（如「S1 R3」「映射 opencode R1-R5」），
保留作验收证据与过程追溯，不再更新。

命名规则：

- `TASK-<线>-R<轮次>.md` — 派发给 opencode 的任务书
- `OPENCODE-REPORT-<线>-R<轮次>.md` — opencode 回传的执行报告

各线对应：

| 代号 | 工作线 | 相关计划 |
|------|--------|----------|
| AI / AI-KEYFIX | AI 助手模块（beta.28-30）+ key 覆盖根修 | `docs/plans/2026-10-21-ai-assistant-plan.md` |
| F1 | 家庭管家底座 — 任务引擎垂直切片 | `docs/plans/2026-10-22-agent-foundation-f1-plan.md` |
| F2 | 家庭管家底座 — 主动管家（beta.32） | `docs/plans/2026-10-23-agent-foundation-f2-plan.md` |
| S1 | 最小权限（面板降权 fm / 安装 fm 化 / sudoers 白名单） | `docs/plans/2026-10-23-least-privilege-s1-plan.md` |
