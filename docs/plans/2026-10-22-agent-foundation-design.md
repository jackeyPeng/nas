# Z1 NAS 家庭管家底座设计（spec v4 — 底座框架，已拍板方向，待用户审阅）

日期：2026-10-22
状态：**方向已拍板（§十），本 spec 覆盖「底座」范围；微信网关/MCP 插座/安康联邦各自另出 spec**
前序：docs/plans/2026-10-21-ai-assistant-design.md（spec v3，AI 助手 M1+M2 已发版 beta.28-30）

## 一、定位升级（2026-10-22 用户拍板）

**从"面板里的聊天助手"升级为"家庭管家"——AI 是产品的主交互层，面板降级为专家模式。**

- **主用户（B）：纯家庭小白**。不懂 SMB/RAID/配额，只为"照片视频不丢、找得到、家人能用"。跟系统打交道的方式：对话 + 手机推送 + 回复确认，永远不主动打开专家页面。
- **辅用户（A）：轻度技术 DIY 玩家**。面板专家模式保留全部现有能力；后续通过 MCP 插座自带 agent 接入（另出 spec）。
- **产品叙事不变**：NAS 是家庭数据的底座，AI 助手是家庭数据的门面——v3 把门面做小了（只读问答+运维写操作），v4 把门面做成管家：**主动发现问题、提出建议、经确认后自己把活干完**。

安康（健康管理/生活秘书）**明确缓接**：底座框架先做好做稳，联邦插座保留（v3 §七不变），启动时机等用户发令。

## 二、硬约束（继承 v3 §二，全部不变）

零 CGO 单 Go 二进制、前端本地化/CSP/i18n 双语、写操作全链审计、type-to-confirm 确认门（函数级守卫）、key 0600+脱敏、sudoers 不新增特权面、模块可关断零足迹、安康代码零合并红线。

**v4 新增红线**：
9. 任务引擎的文件操作只允许在池根（/data/nas*）内，路径复用 filemgmt 的 resolvePoolPath（穿越/符号链接 fail-closed）；**任务删除一律进 .recycle（回收站既有链路），永不硬删**。
10. 触发器建议与任务计划**永远只是建议**——执行前必过确认门；主动推送频率受限（同 key 冷却 + 每日上限），管家不许变骚扰。
11. 微信云中继（后续 spec）只经手密文：E2E 加密，中继无明文、无业务逻辑。

## 三、架构：单二进制、分层分包（方案三，已拍板）

```
web/modules/ai/        ← agent 内核（现有，M1/M2 产物）：LLM 客户端、agent loop、
                          工具域注册表、确认门(PendingStore)、会话存储、SSE chat
                          【目录不更名：ai == agent 内核，91 个测试与 /api/ai/* 路由零扰动；
                            是否改名 agent 属"后面再评估"项】
web/modules/tasks/     ← 任务引擎（本 spec 新增）：jobs 表、步骤化执行器、
                          进度事件、断点续传、结果报告
web/modules/triggers/  ← 主动管家（本 spec 新增）：eventbus 订阅 + cron 调度 +
                          建议卡生成/冷却/上限
web/modules/gateway/   ← 通道抽象（微信 spec 时落地；本 spec 只定义它将来消费的接口）
web/modules/mcp/       ← MCP 插座（另 spec）
```

- 否决独立管家进程（破单服务家电形态）；否决在 ai 包内继续堆（网关/任务/触发器与聊天搅在一起会成泥球）。
- 包间依赖单向：`ai → tasks`（AI 规划任务、查询/取消任务走 tasks 导出的内核 API）；`triggers → tasks + ai`（建议被接受后转 pending action / 任务计划）；tasks/triggers 不 import ai 的 LLM 部分（引擎无模型依赖，可独立测试）。
- **通道中立**：任务进度、建议卡、结果报告统一走 eventbus 事件 + 新增 `notifications` 轻量收件箱（面板通知中心数据源）；微信网关将来只是 notifications 的另一个消费者——底座不依赖任何云服务即可闭环验收。
- **架构边界测试（CI 强制）**：新增一个 Go 测试扫描 import 图并锁定分层——① 禁止跨模块 import 其他模块的内部实现（跨模块只准走 `export.go` 导出的内核 API，AI 工具域接入既有模式）；② tasks/triggers 不得 import ai 的 LLM/agent 部分（引擎无模型依赖）；③ ai→tasks、triggers→tasks+ai 之外出现反向/横向依赖即测试红。边界靠机器守，不靠 review 记忆。

### 三b、单二进制底座 + sidecar 生态（归位规则，2026-10-22 拍板）

产品架构叙事对齐群晖模式：**面板 = 单二进制底座（轻、常驻、核心闭环），生态 = sidecar 独立服务（经 TODO #37 外部应用注册机制统一管理：服务页启停/健康徽章/门户入口）**。后续任何新功能立项时按此归位：

| 形态 | 判据 | 例子 |
|---|---|---|
| 进底座（web/modules/*） | 轻量、常驻内存小、属核心管理闭环、纯 Go 零重依赖 | ai/tasks/triggers/存储/用户/防火墙… |
| sidecar 独立二进制（可选安装件） | 重计算/重依赖/可选装/独立生命周期 | 照片 AI 分类、向量索引、媒体刮削 |
| 云服务（独立仓+独立部署） | 跑在厂商/第三方侧 | 微信 E2E 中继 |
| 生态应用（独立仓，零代码合并） | 独立产品、隐私域隔离 | 安康 |

**明确否决**：把底座按模块拆多进程/多仓库——当前体量（25k 行 Go、21 模块、13.6MB 二进制）远未到拆分阈值；多服务会新增服务间认证/网络半死/版本偏差三类恶性 bug，且家庭小白用户无法自救多进程故障；每模块一仓会摧毁"一个提交横跨后端+前端+i18n+文档"的原子开发速度。仓库切分只在发布节奏真正分叉时进行（现状：nas / nas-website / 安康 / 微信中继(将来)四个仓即终态格局）。压 bug 的手段是测试与验收（12k 行单测 + 38 项验收套件 + 双机重置流程），不是包结构。

## 四、任务引擎（tasks）

### 数据模型（SQLite，common.OpenSQLite，WAL）

```
jobs: id TEXT PK, kind TEXT, status TEXT,          -- pending_confirm|running|paused|done|failed|cancelled
      plan_json TEXT,                               -- 步骤序列（生成时固化，确认的就是它）
      progress_cur INT, progress_total INT, progress_note TEXT,
      checkpoint_json TEXT,                         -- 断点：已完成步骤游标 + 幂等标记
      result_json TEXT, error TEXT,
      created_at, started_at, finished_at
```

- **步骤幂等**：每步带稳定 key，执行前查 checkpoint 跳过已完成项——崩溃/重启后从 checkpoint 续传，不重做副作用。
- **两阶段**：AI（或触发器建议）生成 plan → `pending_confirm`（确认卡含影响面预估：N 个文件/M GB/涉及共享）→ 用户确认 → `running`。plan 固化后不可被模型改写（改计划=新任务）。
- **单 worker 串行队列**（磁盘友好，家庭盒子不追求并发）；暂停/恢复/取消 API + 前端进度条（SSE 复用向导既有模式）。
- 进度与终态发 eventbus（`task.progress` 只在 10%/25%/50%/75% 里程碑发，防刷屏；`task.done/failed` 必发）→ triggers/通知中心/将来微信通道消费。

### 第一批任务 kind（小白场景优先）

| kind | 内核 | 确认卡影响面 |
|---|---|---|
| photo_dedupe | 池内图片/视频哈希扫描（分块+全量两级），重复集移 .recycle 保留最优副本 | N 个重复文件 / 释放 M GB |
| photo_organize | EXIF 拍摄日期 → 年/月子目录归置（移动非复制，无 EXIF 用 mtime，冲突加后缀） | 涉及 N 个文件、目标结构预览 |
| bulk_rename | 规则模板（日期前缀/序号/替换），dry-run 出对照表 | 前 20 条改名对照 |
| big_file_inventory | 盘点 Top-N 大文件生成报告（只读任务，免确认，管家建议的数据源） | — |
| run_backup | 复用既有备份链（backup-data.sh / rclone 任务） | 备份目标与范围 |
| rclone_sync_setup | 复用 rclone 模块建同步任务（凭据仍走面板既有表单，AI 不碰密钥） | 远端/方向/目录 |

### AI 集成（ai 模块新增工具，走既有域注册表）

- 只读：`get_task_list`（含进度）、`get_task_detail`
- 写（确认门）：`plan_task`（参数=kind+范围，服务端生成 plan，模型不产步骤明细）、`cancel_task`、`pause_task`/`resume_task`
- **红线**：模型只能选 kind 和范围参数；plan 步骤由服务端模板生成——防注入构造任意文件操作。删除类只有"进回收站"一种语义。

## 五、主动管家（triggers）

- **事件源**：① eventbus 订阅（健康状态迁移 ok→warn/error、RAID 降级、备份失败——事件中心已有生产方）② cron（磁盘水位每 6h、big_file_inventory 每周、重复扫描每周低峰、家庭周报每周日）③ 任务完成回调（"清理完成，释放 30GB"顺势建议下一件）。
- **建议卡**：`suggestions: id, trigger_key, title, body(人话层), actions_json(接受后转 pending action 或 task plan), expire_at, state(new/accepted/dismissed/expired)`。
- **接受路径**：面板通知中心/仪表盘卡点「执行」→ 生成确认卡（走既有确认门，参数服务端固化）→ 任务引擎执行。将来微信通道 = 推送卡 + 回复确认词，同一 actions_json。
- **反骚扰**：同 trigger_key 冷却 24h；活跃建议上限 5 条；周报等固定节奏可单独关；模块关断（AI enabled=false）时 triggers 完全停摆（零足迹继承）。
- **人话层**：所有 title/body 过术语翻译表（i18n 键，如 `RAID1 降级`→`一块硬盘出了问题，数据目前还有一份完整副本`）；LLM 生成的推送文案 system prompt 强制"家庭小白可懂、给出下一步动作"。

## 六、对话式开机引导（B 模式装机体验）

- AI 模块首次启用（或面板首次登录检测到无池）时，聊天页进入**引导会话**：结构化问答（主要存什么/几个人用/要不要手机备份），选项以快捷回复按钮呈现——**意图→方案映射是服务端固定模板，不靠 LLM 自由发挥**（LLM 只做措辞润色，方案本身来自模板：如"照片+视频→RAID1 池 + 家庭相册共享 + 成员账号"）。
- 产出=一张组合确认卡（建池方案+共享+账号清单），确认后依次调用既有向导/用户内核（全部现有确认语义不变）。
- 专家模式入口保留（"我想自己配置"→ 跳现有存储向导）。

## 七、里程碑（底座内部）

- **F1 垂直切片**：ai→tasks 分层落位 + **架构边界测试** + 任务引擎核心（jobs 表/worker/checkpoint/SSE 进度/暂停取消）+ photo_dedupe 一个 kind 全链（AI 规划→确认卡→执行→回收站→报告）+ 前端"任务"区块（聊天页工具轨迹旁挂任务卡）
- **F2 主动管家**：triggers（eventbus 订阅+水位 cron+冷却/上限）+ notifications 收件箱 + 面板通知中心 + 建议卡接受链 + 家庭周报
- **F3 装机体验**：对话式开机引导 + 人话层术语表全量铺开（含既有推送文案改造）
- **F4 任务扩批**：photo_organize / bulk_rename / big_file_inventory / run_backup / rclone_sync_setup
- 每个 F 独立验收（双机实测+对抗用例），F1-F4 全部只依赖面板+现有网页聊天，**零云服务依赖**；微信网关 spec 落地后 F1-F4 的产出自动获得微信通道（消费同一 notifications/eventbus）。

## 八、验收口径

- 功能：F1 用种入的重复文件集实测（扫描→确认→回收站→报告→恢复）；断点续传实测（任务中 kill 面板重启，续传不重做副作用）；F2 人为触发水位/SMART 迁移验证建议卡与冷却；F3 全新机器走完引导建池建号。
- 安全：对抗用例扩展——恶意文件名注入不得改变已确认 plan；`plan_task` 参数 schema 拒绝未知 kind/池外路径；任务引擎路径逃逸测试（复用 filemgmt 穿越用例）；关断态 tasks/triggers API 全 403、无后台 goroutine。
- 回归：现有 ai 91 测试 + acceptance-suite 38 项 + go test ./... -race 全绿；**架构边界测试进 CI 常绿**（跨模块内部 import / 反向依赖即红）。

## 九、成本与依赖

- 新增 Go 依赖：0（任务引擎/触发器纯 stdlib + 既有 common 基建；EXIF 解析用纯 Go 轻量实现评估 rwcarlsen/goexif 或自写 TIFF 头解析——F4 前定，倾向自写最小子集）
- 无新增常驻负载：triggers cron 复用面板进程内调度（rclone 调度器同款模式），worker 空闲零开销
- 云服务依赖：无（中继属微信 spec）

## 十、拍板记录（2026-10-22 用户确认）

1. **用户分层**：B 为主（纯家庭小白，AI+推送即产品本体），A 为辅（面板专家模式保留）。
2. **微信通道**：走云中继路线（出站长连接、NAS 零公网暴露；85 机+火龙已是自家生产先例）；E2E 加密红线；**具体设计另出 spec**。
3. **agent 化路径**：自研轻 agent 内核不变 + MCP 插座（**另出 spec**）；不封装 OpenClaw/Hermes 类整壳框架（运行时/安全模型/通道三重不匹配，封装省不掉剩余工作）；库级借用开放（mcp-go、必要时 Eino）。
4. **架构**：方案三——单二进制、分层分包（ai 内核/tasks/triggers/gateway/mcp 五层）。
5. **安康**：缓接，插座保留，等用户发令。
6. **本 spec 范围**：底座（F1-F4），零云依赖闭环；"后面再评估"项：ai 目录更名、Eino 引入时机、微信中继形态细化。
7. **单体 vs 多服务（2026-10-22 讨论后拍板）**：单二进制底座 + 包级分层 + 架构边界测试（CI 强制）+ sidecar 生态归位规则（§三b）；否决按模块拆多进程/每模块一仓（体量未到阈值、多服务新增恶性 bug 类、多仓摧毁原子提交速度）；重计算可选件走 sidecar、云服务独立仓、生态应用零合并——"多服务"作为生态形态存在，不炸底座。
