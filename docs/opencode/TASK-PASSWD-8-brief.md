# 任务：NAS 密码策略放宽 — 长度下限 12 位 → 8 位

仓库：/home/jacky/soft/nas（master，工作区干净）。Go 模块在 web/ 子目录。

## 背景（用户需求，明确拍板）

家庭用户反馈：12 位密码输入太困难（NAS 面板/家庭账号密码要频繁输入）。要求**下限降到 8 位，且不引入任何复杂度要求**（大小写/数字/符号组合规则一律不要）。

安全前提不变：`common.ValidSecret` 拒绝换行/控制字符（防 chpasswd stdin 协议注入）必须保留——只改长度下限，不动字符校验。

## 明确的改动范围（逐条核实过，按此实施）

### A. Go 后端（3 处长度校验，全部 12 → 8）

1. `web/modules/users/create.go` 约 L42：`if len(req.Password) < 12 {` → `< 8`，错误文案 `密码至少12位` → `密码至少8位`
2. `web/modules/users/users.go` 约 L305：`if password == "" || len(password) < 12 {` → `< 8`，文案同上改 8 位
3. `web/modules/ai/actions.go` 约 L208：`if len(password) < 12 {` → `< 8`，文案 `密码至少12位` → `密码至少8位`；同时更新该函数上方注释里「长度下限对齐 handleCreateUser 现有策略（>=12 位）」为 `>=8 位`

**不要动** `web/frontend/app.js` 里保险箱（vault）的 passphrase 长度校验（约 L3214/L3397 的 `< 12`）和 index.html 里 vault 的 `minlength="12"`——那是浏览器端本地加密主口令（PBKDF2 加密个人凭据库），不是 NAS 登录密码，属安全边界，本次明确不改。

### B. 前端（3 处 + i18n）

4. `web/frontend/app.js` 三处密码长度校验 12 → 8：
   - L942 附近（addUserForm）、L965 附近（pwdForm 改密）、L1034 附近（userWizardForm）
5. i18n 双语同步（`web/frontend/i18n/zh-CN.json` + `en-US.json`）：
   - `users.password_min`：`密码（至少12位）` → `密码（至少8位）` / `Password (min 12 chars)` → `Password (min 8 chars)`
   - `users.new_password_12`：`新密码（至少12位）` → `新密码（至少8位）` / `New password (min 12 chars)` → `New password (min 8 chars)`
   - `msg.pwd_min_12`：`密码至少12位` → `密码至少8位` / `Password min 12 chars` → `Password min 8 chars`
   键名保持 `new_password_12` / `pwd_min_12` 不变（改名要连 index.html/app.js 引用一起动，收益为零、风险更高；在报告里说明这个决定即可）。
6. `web/frontend/index.html` 两处引用 `users.new_password_12` 的 label 无需改动（走 i18n）；**新增用户 / 改密输入框若有 `minlength="12"` 一律改 8**（grep 确认：L741、L847、L3897 附近；vault 的 minlength 不动）。

### C. 安装/部署脚本（2 处）

7. `scripts/install.sh` L22：`MIN_PASS_LEN=12` → `MIN_PASS_LEN=8`（L147-148 引用该变量，无需改）
8. `scripts/setup.sh` L185 提示文案 `至少12位` → `至少8位`；L196 `if [ ${#NAS_PASS} -lt 12 ]; then` → `-lt 8`；L197 错误文案 `至少需要 12 位` → `至少需要 8 位`

### D. 文档（5 处）

9. `README.md` L112：`# Edit .env (min 12 chars)` → `(min 8 chars)`
10. `README.zh-CN.md` L156：`至少12位` → `至少8位`
11. `.env.example` L10 表格行：`至少 12 位` → `至少 8 位`
12. `docs/nas-product-brochure.html` L257 `12 位长度强制校验` → `8 位长度强制校验`；L277 注释 `编辑填入密码(12位+)` → `(8位+)`
13. 全仓兜底 grep：`grep -rn "至少12\|至少 12\|12位\|12 chars\|min 12" --include='*.go' --include='*.js' --include='*.json' --include='*.html' --include='*.md' --include='*.sh' web/ scripts/ README* .env.example docs/nas-product-*.md docs/nas-product-brochure.html` —— 除 vault/passphrase 相关（明确豁免）与 CHANGELOG.md（历史记录不可改）、docs/opencode/（历史任务报告，不可改）外，其余必须清零。**CHANGELOG.md 不动**（parent 补记）。

### E. 单测

14. 在 `web/modules/users/` 现有测试文件里补/改用例（不要新建大文件，能加就加到已有 `*_test.go`）：
    - 7 位密码 → 400 且错误含「至少8位」
    - 8 位密码 → 通过长度校验（若后续步骤因 fixture 缺失失败，断言到「不是长度错误」即可，不要造假通过）
    - 覆盖 handleCreateUser 与 changePassword 两条路径各至少一条
15. 若 `web/modules/ai/actions_test.go` 有长度相关既有用例断言 12 位语义，同步改到 8 位。

## 硬约束（违反 = 返工）

- 只改上述文件。不动 CHANGELOG.md / TODO.md / docs/opencode/ / vault 相关校验。
- 不放宽度以外的任何校验（不加复杂度规则、不加黑名单）。
- 前端禁止新增内联 `<script>`、不动 CSP。
- 完成后自测：`cd /home/jacky/soft/nas/web && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./... -count=1`，全绿。
- 禁止读取/访问仓库目录之外任何路径（包括 ~/go/pkg/mod、/tmp、任何绝对路径外部文件）——会被沙箱自动拒绝并中断整个 run。自测只用 go test 在仓库内跑。
- 发现任务书与代码现实矛盾（行号漂移正常按内容定位；语义矛盾才算）→ 在报告里申报，不得静默绕过或扩大改动面。

## 产出

1. 一次 git commit（只含上述文件），首行：
   `feat(passwd): 密码长度下限 12 → 8 位，不引入复杂度要求（家庭用户输入成本）`
   正文列出 A/B/C/D/E 五组改动与 vault 豁免理由。
2. 报告写到仓库根 TASK-REPORT.md（**不要 git add**）：逐文件改动摘要、go build/go test 结果、第 13 条兜底 grep 的实际输出（证明清零）、任何偏离与理由。
