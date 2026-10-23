# NAS 项目近期变更整理（2026-10-17 → 10-23）

> 范围：v1.4.0-beta.23 → beta.30（8 个版本，69 个提交，172 文件 +14356/-1814）+ 未发布的「家庭管家底座 F1」。
> 来源：仓库 CHANGELOG.md、git log、docs/plans/。整理日期 2026-10-23。

## 一、时间线速览

| 版本 | 日期 | 一句话 |
|------|------|--------|
| beta.23 | 10-17 | UI 巡检修复集 + 共享影子身份过滤 + 健康中心口径对齐 |
| beta.24 | 10-19 | 彻底移除 FileBrowser（面板文件管理完全取代）+ 验收修复集 |
| beta.25 | 10-20 | 零知识凭证保险箱 v2 + 密码生成器 + 工具箱菜单组 |
| beta.26 | 10-20 | 外部安全审计全量修复（P0/P1/P2 共 20 项） |
| beta.27 | 10-21 | 运维脚本审计修复收尾（cleanup/backup/sudoers/rclone arm64） |
| beta.28 | 10-21 | **AI 助手模块（M1+M2）** + 发布前验收修复集 |
| beta.29 | 10-21 | AI 入口可发现性修复（常驻导航项 + 未启用徽标） |
| beta.30 | 10-22 | AI 配置 key 覆盖根修 + 错误反馈不再静默 |
| （未发布） | 10-22~ | 家庭管家底座 spec v4 + F1 任务引擎垂直切片（分支 feat/agent-foundation） |

## 二、五条主线

### 1. AI 助手模块（beta.28-30，本轮最大新功能）
- **形态**：可开关内置模块，默认关闭零足迹；控制面（status/config/toggle/test）豁免开关守卫，功能面（chat/conversations/action）关闭态一律 403。
- **模型接入**：OpenAI 兼容端点（DeepSeek 云端 / Ollama 本地双预设），Key 0600 存储 + 掩码回显 + 连通性测试。
- **工具框架**：ToolDomain 抽象 + schema 校验；NAS 域 15 工具（9 只读 + 6 写），写工具直调既有模块内核（export.go 薄 wrapper，不复制业务逻辑）。
- **安全设计**：写操作 type-to-confirm 确认门（参数服务端留存、TTL 5 分钟、一次性 Take、重放 410）；create_user 密码前端生成直注，永不进模型上下文/落库/审计；对抗性安全测试集常驻（注入/schema 攻击/Key 泄漏面/守卫）。
- **两轮用户实测驱动的修复**：beta.29 关闭态找不到入口 → 导航项常驻 + 灰徽标；beta.30 保存配置后「测试连通性」无反应 → 空值/掩码视为未修改保留真 key + 34 处错误响应改 JSON content-type（此前前端静默吞错）。

### 2. 家庭管家底座 / 任务引擎（未发布，开发中）
- **spec v4 已拍板**（docs/plans/2026-10-22-agent-foundation-design.md）：单二进制分层分包（ai 内核 + tasks + triggers），否决拆多进程/多仓；F1-F4 里程碑零云依赖；主动管家建议卡 + 反骚扰。
- **F1 垂直切片已落地 7 任务**（docs/plans/2026-10-22-agent-foundation-f1-plan.md）：
  - jobs 表 + 六态状态机 + 单 goroutine worker（checkpoint 断点续传，暂停/取消在步骤边界响应）；崩溃恢复保守化——重启后 running 一律转 paused，需手动继续。
  - 架构边界测试：go/parser 解析 import 图，机器强制 tasks 不依赖 ai、核心模块不依赖 ai/tasks/triggers，违规过不了 go test。
  - filemgmt 内核抽取（MoveToRecycle/ResolvePoolPath + 哨兵错误），任务引擎复用回收站链路。
  - photo_dedupe：两级哈希（大小分桶→头 64KB→全量）+ 最短路径保留 + 预算 partial + 回收站执行接线。
  - AI 任务工具 7 枚（plan/start/cancel/pause/resume/list/detail）+ 恶意文件名/job_id 注入对抗用例。
  - 前端任务 tab：六态徽章、进度轮询、暂停/继续/取消、完成报告，双语 i18n。
  - 真机验收抓到并修复：任务计数器持久化（counters_json 落库，kill -9 续跑不再丢前段计数）。
- **状态**：分支 feat/agent-foundation，CHANGELOG 未发布段已记录，尚未合并 master、未发版。

### 3. 安全加固（beta.26 + beta.27）
- **P0 根除**：命令/sed 注入全线直传参或纯 Go 化（证书 domain 校验、chpasswd/smbpasswd 走 stdin、.env 与 unit 行级重写）；密码入口拒绝换行/控制字符；/api/system/check 免认证暴露收口（loopback+JWT）；登录/TOTP IP 限流（5 次失败锁 15 分钟）；服务开关白名单；Samba/SSH 配置注入防护；备份路径穿越防护。
- **P1 功能**：面板 HTTPS 真正生效（此前是死代码）；XFS 配额根修（prjquota + /etc/projects 标准格式 + 建文件夹配额真落地，dd 实测 5G 精确 EDQUOT）；xfs_growfs 改挂载点（扩容静默失败根除）。
- **P2 稳定性**：磁盘操作全局锁、rclone 状态事务锁、SQLite 统一 WAL（根治 logger/eventbus DSN 参数被驱动静默忽略）、monitor 后台采样 1.5s→<1ms、du/smartctl 缓存（不再反复唤醒休眠盘）。
- **运维脚本（beta.27）**：cleanup.sh 存储拆除段前置（修 set -e 下 EBUSY 中止致 RAID/LVM 全残留的实踩事故）；备份/恢复补面板数据库整目录 + rclone.conf + /etc/projects（重装不再丢保险箱/云同步/2FA）；rclone 升级按架构取 deb（arm64 修复）；sudoers 收敛为 115 条具体子命令粒度、拒绝 root-equivalent 通配。

### 4. 功能演进（beta.24 + beta.25）
- **FileBrowser 彻底移除**（beta.24）：装机链/服务页/注册表（46→41）/HTTPS/密码同步/监控/i18n/文档/R2 安装包全链路清理；存量机器升级时自动卸载并备份 /etc/filebrowser。
- **零知识凭证保险箱 v2**（beta.25）：浏览器端 PBKDF2-600k + AES-256-GCM，服务器只存密文、不接触明文/口令/派生密钥；owner 行级隔离（管理员不例外）；5 错锁 30 分钟、查看 15 秒自动遮罩、剪贴板 30 秒清空、空闲自动锁定；旧版一键迁移（失败即停防明文丢失）。附独立密码生成器页 + 「🧰 工具箱」菜单组；PWA sw.js 改网络优先（根治升级后界面陈旧）。

### 5. UI / 稳定性巡检（beta.23 + 各版验收修复集）
- 共享影子身份（nologin 占位用户）不再泄漏为幻影用户；仪表盘/用户页 null 数组崩溃修复；已停止服务不再误标红色「失败」（is-active 四态归一 + SuccessExitStatus=143）；健康中心 not-installed 不计 warn；x-for 重复 key 崩溃根因修复（整页导航失效的 'after' 错误真身）；删用户补 WebDAV 凭据撤销（被删用户此前仍能过 rclone 认证）；用户服务开关静默失败 ×2；建池文案按模式报实际用盘数。

## 三、验收方法论（贯穿全部版本）
每个版本发布前跑「双机重置·用户级全流程验收」：两台测试机（merge / RAID1 各一台）factory reset → 一键安装 → 首登改密 → 建池 → 注册表 41/41 → 验收套件 33-38 项 → 协议断言（SMB/NFS/WebDAV/S3/FTP）→ Chrome 实机 UI 走查。beta.28 起追加 AI E2E 31 项（桩 LLM 驱动全工具链）。本轮为第五~八轮+，多起真 bug（计数器丢失、WebDAV 凭据残留、x-for 崩溃）均由该流程抓到。

## 四、文档面同步
- 新增：`docs/plans/2026-10-22-agent-foundation-design.md`（spec v4）、`docs/plans/2026-10-22-agent-foundation-f1-plan.md`（F1 计划）、`docs/凭证保险箱与密码生成器设计参考.md`。
- 更新：产品手册（7.8 零知识保险箱、11.6 AI 助手+任务小节）、功能操作清单（21 模块 210 项）、协议服务配置详解、存储权限模型（FileBrowser 章节清理）。
- 官网（nas-website）：功能特性页新增 12 模块面板截图 × 中英双语（10-17，commit 9ee45c7）；beta.23-30 版本历史四文件已随各版同步。**注意：www.z1.sale 的 R2 线上部署按惯例由用户手推，git push 不更新线上。**

## 五、当前状态与待办
1. **feat/agent-foundation 分支未合并**：F1 七个任务已 commit（顶到 b532ef0），CHANGELOG 未发布段有未提交改动，仓内还有未跟踪的 OPENCODE-REPORT-AI-R*.md——并行会话仍在收尾，合并/发版待拍板。
2. **beta.30 之后的修复未发版**：control 渠道二进制仍是 beta.30，F1 任务引擎对外不可用（enabled 守卫 + 未发版双保险）。
3. **官网 R2 部署**：截图区块要等手推 dist 才在线上可见。
4. 长期积压：TODO.md 性能调优大块（TCP/IO/Samba 参数）仍未启动。
