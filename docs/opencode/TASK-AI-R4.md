# TASK-AI-R4: AI 助手 M1 第四轮（计划 Task 8-9，前端）

仓库：/home/jacky/soft/nas（分支 feat/ai-assistant）。Go module 在 web/。
前置：后端 R1-R3 已落地——先读 web/modules/ai/ai.go（路由与响应形状）、chat.go（SSE 事件协议）、config.go，以代码现实为准对接。

## 必读
1. docs/plans/2026-10-21-ai-assistant-plan.md 的 **Task 8、Task 9**。
2. docs/plans/2026-10-21-ai-assistant-design.md §三（开关语义）§五（确认门是最终防线——本轮只做渲染，确认交互在 R5）。

## 硬约束（前端红线，违反=返工）
1. 禁止读取/执行仓库外文件。
2. 依赖本地化：不引任何 CDN/外部资源；不新增 JS 库。
3. CSP：无内联 script； Alpine 绑定用既有模式。
4. 新文案全部走 i18n 双语（计划里列的键一个不落，可增不可减语义），完成后 `python3 scripts/check-i18n.py` 必须通过。
5. 弹窗 class 用既有 `modal`/`modal-box`；弹窗不得因点击外部关闭；checkbox 行用 `label:has(input[type="checkbox"])` flex 模式（.form-group input width:100% 坑）。
6. `:style` 绑定元素不写布局类 inline style（flex/width 用 class，Alpine 会整串覆盖）。
7. 后端返回原始值、前端 formatter 换算的契约不破；api() 对纯文本错误返回真值字符串——判响应必须按「含期望字段的对象」判定（typeof data === 'object' && data.xxx !== undefined）。
8. 前端 embed 进二进制：改完必须 `cd web && go build ./...` 重编译验证；不碰任何 Go 逻辑文件。
9. 每 Task 单独提交，信息用计划原文。不 git add 本 brief 与 OPENCODE-REPORT-AI-R4.md。

## 结构参照（先读再写）
- 页面骨架/导航：index.html 工具箱 nav-group（187-194 行附近，vault 入口同款）+ `<div x-show="page === 'ai'" class="page">`；navigate() 在 app.js:600。
- SSE 消费先例：app.js:2472 附近 wizard reset-stream 的 fetch+getReader 模式。
- 设置表单先例：vault 页（beta.25 最新页面，结构最规范）；系统设置页的条目化表单行模式。
- 密码生成器 UI（vault 模块的工具箱页）R5 确认卡要复用其生成逻辑——本轮不接。

## 交付明细
### Task 8（设置+开关+入口）
- 工具箱组内 AI 入口 li：`x-show="aiStatus.enabled || page==='ai'"`；组头部加「AI 助手」常驻小入口（未启用时点击进 ai 页显示未启用引导卡+开启向导：填端点→连通性测试→开启）。
- ai 页两 tab（对话/设置），设置 tab：开关（toggleAI，开启失败展示后端原因）、预设下拉（deepseek/ollama，applyAIPreset 联动 base_url/model/api_key，ollama 时 key 禁用）、base_url/api_key/model 表单、连通性测试按钮（转圈+toast）、保存、红色隐私提示块 ai.privacy_note。
- loadAIStatus() 进页自动 + 应用启动时拉一次（决定入口显隐）。
- i18n 键按计划 Task 8 清单（zh/en）。
### Task 9（聊天页）
- 对话 tab：消息列表（user/assistant 气泡）、aiMessages 渲染、delta 增量拼接、tool_call/tool_result 折叠小条目（🔧 名称+digest）、输入框+发送（Enter 发送/Shift+Enter 换行）、思考中状态、错误事件 toast+消息内红条。
- 会话侧栏：aiConversations 列表（GET /api/ai/conversations）、点击载历史（GET /api/ai/conversations/{id}）、新建对话按钮。
- pending_action 事件本轮先渲染为**只读信息卡**（"该操作需要确认——确认交互下个版本接入"占位文案键 ai.pending_hint），R5 替换为确认卡。
- i18n 键按计划 Task 9 清单。

## 验证与提交
1. python3 scripts/check-i18n.py 通过；cd web && go build ./...（embed 重编译）。
2. 提交信息：
   - `feat(ai): 前端设置页+模块开关+导航入口+i18n双语 (ai M1 T8)`
   - `feat(ai): 前端聊天页SSE消费+工具轨迹渲染+会话列表 (ai M1 T9)`
3. 报告写 /home/jacky/soft/nas/OPENCODE-REPORT-AI-R4.md：新增 i18n 键数、app.js 新增函数清单、SSE 事件处理对照表、与后端实际响应形状的差异发现（如有）、偏离与疑虑。浏览器实测不在本轮（R5 后 parent 统一做）。
