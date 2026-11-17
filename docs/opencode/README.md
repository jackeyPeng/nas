# opencode 工作产物归档

opencode 编码代理各轮次的工作文件。第一批从仓库根目录归档至此（2026-10-27），
2026-11-17 整理轮把仍平铺在本目录根下的单文件按轮次收进日期子目录，此后**每个轮次一个目录**：
`<首提交日期>-<主题>/`，目录内放该轮 `TASK-*.md`（任务书）与 `OPENCODE-REPORT-*.md`（执行报告）。

这些文件被提交信息、CHANGELOG 和 docs/plans/ 中的实施计划以轮次代号引用（如「S1 R3」「映射 opencode R1-R5」），
保留作验收证据与过程追溯，**不再更新**。

命名规则：

- `TASK-<线>-R<轮次>.md` — 派发给 opencode 的任务书
- `OPENCODE-REPORT-<线>-R<轮次>.md` — opencode 回传的执行报告
- 后期轮次改用 `<任务名>-brief.md` / `<任务名>-REPORT.md` 成对命名（如 `TASK-PASSWD-8-brief.md` + `TASK-PASSWD-8-REPORT.md`）

## 目录索引

| 目录 | 线 / 主题 | 相关计划或发布 |
|------|----------|----------------|
| `2026-10-21-ai-assistant/` | AI 助手模块 M1+M2 + key 覆盖根修（AI / AI-KEYFIX 线，R1-R5） | `docs/plans/2026-10-21-ai-assistant-plan.md`（beta.28-30） |
| `2026-10-22-agent-f1/` | 家庭管家底座 F1 — 任务引擎垂直切片（R1-R5） | `docs/plans/2026-10-22-agent-foundation-f1-plan.md`（beta.31） |
| `2026-10-23-agent-f2/` | 家庭管家底座 F2 — 主动管家（R1-R5） | `docs/plans/2026-10-23-agent-foundation-f2-plan.md`（beta.32） |
| `2026-10-23-least-privilege-s1/` | 最小权限 S1 — 面板降权 fm / 安装 fm 化 / sudoers 白名单（R1-R4 + R3b/R3c） | `docs/plans/2026-10-23-least-privilege-s1-plan.md`（beta.33） |
| `2026-10-28-consensus/` | 三模型共识审查报告（diskmgmt 加固依据） | `docs/opencode/consensus-report.md` 前身（beta.34） |
| `2026-10-28-diskmgmt-hardening/` | diskmgmt 五轮加固 | beta.34 |
| `2026-10-28-e6-raidstatus/` | RAID 状态采集 sysfs 优先重写 | beta.34 |
| `2026-10-28-userdelete-sync/` | 删用户即时重投影 SMB/NFS | beta.34 |
| `2026-10-29-e7-raid-persist/` | RAID 持久化（重启后阵列名漂移根修） | beta.35 |
| `2026-10-29-installer-robustness/` | 安装脚本健壮性 | beta.35 |
| `2026-10-29-sync-mutex-sse-detail/` | SyncAllConfigs 互斥 + SSE 警告可见性 | beta.36 |
| `2026-10-30-protocol-convergence/` | 协议默认收敛 + 服务页卡片化 | beta.37 |
| `2026-11-05-badge-alert-docs/` | RAID 初始重建误报修复（RAID-ALERT）、池徽章三态化（POOL-BADGE）、#22 notice 归属修正（DOCS） | beta.35 之后修补轮 |
| `2026-11-12-gate-modal/` | 默认密码门禁误弹改密框修复 | beta.39 |
| `2026-11-16-passwd-8/` | 密码长度下限 12 → 8 位 | beta.40 |

新增轮次时：先 `mkdir <YYYY-MM-DD>-<主题>/`，任务书与报告直接写进去，不要再平铺在本目录根下。
