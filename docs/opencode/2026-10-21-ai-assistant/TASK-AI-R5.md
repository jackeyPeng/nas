# TASK-AI-R5: AI 助手 M2 轮（计划 Task 10-12）

仓库：/home/jacky/soft/nas（分支 feat/ai-assistant）。Go module 在 web/。
前置：R1-R4 已落地（后端全链 + 前端 chat/设置页，pending_action 目前是只读占位卡）。先读 web/modules/ai/{actions,agent,chat,tools_nas_read,domain}.go 与 frontend app.js 的 ai 部分，以代码现实为准衔接。

## 必读
1. docs/plans/2026-10-21-ai-assistant-plan.md 的 **Task 10、Task 11、Task 12**。
2. docs/plans/2026-10-21-ai-assistant-design.md §五（确认门是最终防线）。

## 硬约束
1. 禁止读取/执行仓库外文件；验证只用仓库内 go build / go test / check-i18n.py。
2. **create_user 工具的参数 schema 里不得存在 password 字段**——密码由前端确认卡内生成器生成、随确认请求 client_payload.password 直发服务端，服务端 common.ValidSecret 校验后注入执行；密码永不进 LLM 上下文、永不进审计 detail、永不进消息落库。这条是红线，测试必须断言（schema 无 password + 落库/审计内容 grep 不到密码子串）。
3. pending action 参数**不经客户端回传**：确认端点按 action_id 从服务端 PendingStore 取参数执行；客户端只能附加 create_user 的 client_payload.password。
4. 确认门前端必须函数级守卫（`if (aiConfirmDisabled()) return;` 模式，对齐 filemgmt v3 教训——按钮 :disabled 不算）；确认词比对用 i18n 动态值（zh「执行」/en "EXECUTE"），服务端存下发时的同值。
5. TTL 5 分钟；Take 一次性（二次确认同一 id 必须失败）；cancel 端点丢弃。
6. 写工具 Exec 直调既有内核（各模块缺导出的按 R2 模式补 export.go 薄 wrapper，禁止复制逻辑）；restart_service 的 service 参数 Enum=common.AllowedManagedServices() 键集合（构造时动态生成）。
7. 前端红线同 R4（本地依赖/CSP/modal 规范/:style 坑/i18n 双语/check-i18n.py 过/embed 重编译）。
8. Task 12 的 checklist 更新必须用文档末尾附录 awk 命令**实算**汇总统计（别拿旧数字增量算）；CHANGELOG 只记「未发布」段。
9. 每 Task 单独提交，信息用计划原文。不 git add 本 brief 与 OPENCODE-REPORT-AI-R5.md。

## 落点提醒（已核实）
- 既有内核：users 创建用户链（create.go handleCreateUser 内核，需抽 CreateUserWithPassword(username, password string, svcSamba, svcFtp, svcWebdav bool) (steps []string, err error) 导出——机械抽取，handler 调它，行为不变，抽取前后跑 users 包测试证明）；users 配额（quota.go PUT 内核）；firewall.AddRule（handleAddRule 内核抽取，含 buildAddRuleArgs）；system 服务重启白名单路径（handleServices 的 allowed+systemctl）；diagnostics.RunItem（handleRun 内核）；diskmgmt 建文件夹（AddPendingOp+SyncAllConfigs 既有链）。
- 确认卡影响面一句话：i18n 键 ai.action_summary_{tool}×6。
- 密码生成器：frontend 已有（vault 工具箱页），复用其生成函数（读 app.js 找现有实现，抽公用函数或直接调用，不重写算法）。
- 审计：确认执行成功/失败都要 LogAuditRequest 富 detail（工具名+参数摘要，create_user 只记用户名与服务开关）。
- Task 11 安全测试集按计划 4 组场景全落（注入/schema 攻击/key 泄漏面/enabled 守卫），key 泄漏面断言三处：messages 落库、audit detail、SSE 事件全文。

## 验证与提交
1. cd web && go build ./... && go vet ./modules/... && go test ./... -count=1 全绿；go test -race ./modules/ai/ ./modules/users/ ./modules/firewall/ 绿；python3 scripts/check-i18n.py 过。
2. 提交信息：
   - `feat(ai): M2确认门全链(服务端pending存储+type-to-confirm+密码前端注入)+6写工具 (ai M2 T10)`
   - `test(ai): 对抗性注入/schema攻击/key泄漏面/开关守卫安全测试集 (ai M2 T11)`
   - `docs(ai): 操作清单+产品手册AI章节+CHANGELOG未发布段 (ai M2 T12)`
3. 报告写 /home/jacky/soft/nas/OPENCODE-REPORT-AI-R5.md：写工具→内核映射表、确认门时序（含 create_user 密码流）、安全测试矩阵结果、checklist awk 实算输出、偏离与疑虑。
