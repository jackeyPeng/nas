# docs/ 文档索引

> 本目录文档分类导航。仓库级开发文档（AGENTS.md / DEVELOPMENT.md / CONTRIBUTING.md / CHANGELOG.md / TODO.md / RELEASE.md 等）在仓库根目录。

## 产品与用户文档

| 文档 | 说明 |
|------|------|
| `nas-product-manual.md` / `.pdf` | 产品技术手册（用户/运维视角，随版本更新） |
| `nas-product-brochure.html` / `.pdf` | 产品宣传册 |
| `protocol-service-configs.md` | 协议服务配置详解 — SMB / NFS / FTP / WebDAV / S3 |

## 架构与设计（现状）

| 文档 | 说明 |
|------|------|
| `architecture-v1.0.md` | 系统架构 v1.0 |
| `storage-permission-model.md` | 存储与权限模型 — **实际实现**梳理 |
| `storage-feature-matrix.md` | 存储管理功能清单与测试矩阵 |
| `凭证保险箱与密码生成器设计参考.md` | 零知识保险箱 v2 / 密码生成器设计参考 |

## 测试与验收

| 文档 | 说明 |
|------|------|
| `storage-test-manual.md` | 存储管理测试手册 |
| `feature-operation-checklist.md` | 面板功能清单 + 操作项分类（测试验证清单） |

## 流程与协作

| 文档 | 说明 |
|------|------|
| `COLLAB.md` | 跨 Agent 协作协议（PR 流程、冲突仲裁、红线）— 与 AGENTS.md 冲突时以此为准 |
| `release-system.md` | 发版体系（版本节奏、载体同步、sudoers 复审原则） |
| `plans/` | 按日期归档的设计稿与实施计划（web-panel → permission → file-manager → ai-assistant → agent-foundation F1/F2 → least-privilege S1） |

## 硬件

| 位置 | 说明 |
|------|------|
| `design/z1-4bay/` | Z1 4 盘位机箱设计（BOM / CAD / 渲染图） |
| 根目录 `HARDWARE_SPEC.md`、`PURCHASE_LIST.md` | 硬件规格与采购清单 |

## 历史存档（不再更新，仅供追溯）

| 位置 | 说明 |
|------|------|
| `permission-model-discussion.md` / `.en.md` | 权限模型早期讨论稿（结论已落进 storage-permission-model.md） |
| `security-audit-2026-08-27.md` | 2026-08 外部安全审计报告（修复已完成，见 beta.26/27） |
| `recent-changes-2026-10-23.md` | beta.23→beta.30+F1 变更 digest 快照 |
| `external/` | 外部咨询建议与评审报告（20260904 / 20260909 / 存储界面） |
| `opencode/` | opencode 编码代理的派发任务书（TASK-*）与回传报告（OPENCODE-REPORT-*），按轮次归档 |
