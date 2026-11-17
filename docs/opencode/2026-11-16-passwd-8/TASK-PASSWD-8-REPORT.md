# 任务报告：NAS 密码策略放宽 — 长度下限 12 位 → 8 位

commit: `e96a6ae` — `feat(passwd): 密码长度下限 12 → 8 位，不引入复杂度要求（家庭用户输入成本）`

## 逐文件改动摘要

### A. Go 后端（3 处）

| 文件 | 改动 |
|---|---|
| `web/modules/users/create.go` | L42 `len(req.Password) < 12` → `< 8`；错误文案 `密码至少12位` → `密码至少8位` |
| `web/modules/users/users.go` | L305 `len(password) < 12` → `< 8`；错误文案同上 |
| `web/modules/ai/actions.go` | L208 `len(password) < 12` → `< 8`；错误文案同上；函数上方注释 `长度下限对齐 handleCreateUser 现有策略（>=12 位）` → `（>=8 位）` |

`common.ValidSecret`（拒绝换行/控制字符）未改动，字符校验保持原样。

### B. 前端（3 处 JS + i18n 双语）

| 文件 | 改动 |
|---|---|
| `web/frontend/app.js` | `addUserForm.password.length < 12`（原 L942）、`pwdForm.password.length < 12`（原 L965）、`userWizardForm.password.length < 12`（原 L1034）三处 → `< 8` |
| `web/frontend/i18n/zh-CN.json` | `password_min`: `密码（至少12位）`→`密码（至少8位）`；`new_password_12`: `新密码（至少12位）`→`新密码（至少8位）`；`pwd_min_12`: `密码至少12位`→`密码至少8位`（键名不变） |
| `web/frontend/i18n/en-US.json` | 同三个键，英文文案同步改为 `min 8 chars` |

`scripts/check-i18n.py` 校验：zh-CN/en-US 均 1415 keys，一致。

**index.html 未改动，附偏离说明**：任务书第 6 条称 `web/frontend/index.html` 的 L741、L847、L3897 附近有 NAS 用户/改密输入框的 `minlength="12"` 需要改成 8。实际核查（`grep -n 'minlength' web/frontend/index.html`）该文件中**唯一**出现的 4 处 `minlength="12"` 全部在 L2739、L2743、L2838、L2839，且全部是 vault passphrase 相关输入框（`vaultSetupForm.passphrase`/`.confirm`、`changePassphraseForm.newPassphrase`/`.confirm`）。L741/L847 分别是 `userWizardForm.password`、`pwdForm.password` 的 `<input>`，均**不带任何 `minlength` 属性**，长度校验纯靠 app.js 里的 JS 判断（已在 B 中改好）；L3897 附近未发现相关输入框。这是任务书描述与代码现实的语义矛盾（不是单纯行号漂移——声称存在的属性实际不存在），按指令要求申报、不臆造改动、未动该文件。

### C. 安装/部署脚本（2 处）

| 文件 | 改动 |
|---|---|
| `scripts/install.sh` | L22 `MIN_PASS_LEN=12` → `MIN_PASS_LEN=8`（L147-148 引用该变量，未改） |
| `scripts/setup.sh` | L185 提示文案 `至少12位` → `至少8位`；L196 `[ ${#NAS_PASS} -lt 12 ]` → `-lt 8`；L197 错误文案 `至少需要 12 位` → `至少需要 8 位` |

### D. 文档（4 个文件，共 5 处）

| 文件 | 改动 |
|---|---|
| `README.md` | L112 `# Edit .env (min 12 chars)` → `(min 8 chars)` |
| `README.zh-CN.md` | L156 `至少12位` → `至少8位` |
| `.env.example` | L10 表格行 `至少 12 位` → `至少 8 位`（L21 示例密码 `NAS_PASS=changeme123456` 为 13 位，新旧下限均满足，未改） |
| `docs/nas-product-brochure.html` | L257 `12 位长度强制校验` → `8 位长度强制校验`；L277 注释 `编辑填入密码(12位+)` → `(8位+)` |

CHANGELOG.md、TODO.md、docs/opencode/ 均未触碰。

### E. 单测（2 个文件各新增 2 条用例）

| 文件 | 新增用例 |
|---|---|
| `web/modules/users/create_test.go` | `TestHandleCreateUser_RejectsShortPassword`（7 位 → 400 + 文案含「密码至少8位」）；`TestHandleCreateUser_AcceptsEightCharPassword_PastLengthCheck`（8 位 → 断言不是长度校验的 400，不伪造对后续 useradd/chpasswd 系统调用成功的断言） |
| `web/modules/users/users_passwd_test.go` | `TestHandleUserAction_ChangePassword_RejectsShortPassword`（7 位 → 400 + 文案含「密码至少8位」）；`TestHandleUserAction_ChangePassword_AcceptsEightCharPassword_PastLengthCheck`（8 位 → 断言不是长度校验的 400） |

`web/modules/ai/actions_test.go` 核查后未发现任何长度语义相关的既有断言（仅含控制字符注入测试、`MaxToolCalls: 12` 等无关的 12），无需同步修改。

## go build / go test 结果

```
$ cd web && CGO_ENABLED=0 go build ./...
(无输出，成功)

$ cd web && CGO_ENABLED=0 go test ./... -count=1
ok  	nas-panel	0.089s
ok  	nas-panel/common	1.426s
ok  	nas-panel/modules/ai	14.786s
ok  	nas-panel/modules/backup	0.156s
ok  	nas-panel/modules/config	0.018s
ok  	nas-panel/modules/dashboard	0.013s
ok  	nas-panel/modules/diagnostics	0.015s
ok  	nas-panel/modules/diskmgmt	12.263s
ok  	nas-panel/modules/events	0.429s
ok  	nas-panel/modules/filemgmt	0.298s
ok  	nas-panel/modules/firewall	0.011s
?   	nas-panel/modules/hardware	[no test files]
ok  	nas-panel/modules/health	1.623s
ok  	nas-panel/modules/logs	0.009s
ok  	nas-panel/modules/monitor	0.036s
ok  	nas-panel/modules/notifications	1.308s
ok  	nas-panel/modules/rclone	0.068s
ok  	nas-panel/modules/services	0.009s
?   	nas-panel/modules/storage	[no test files]
ok  	nas-panel/modules/system	0.012s
ok  	nas-panel/modules/tasks	5.578s
ok  	nas-panel/modules/triggers	8.295s
?   	nas-panel/modules/update	[no test files]
ok  	nas-panel/modules/users	0.524s
ok  	nas-panel/modules/vault	1.937s
ok  	nas-panel/modules/version	0.005s
```

全绿。新增 4 条用例单独验证（`-run 'ShortPassword|EightCharPassword' -v`）均 PASS。

另附 `gofmt -l .` 核查：输出的 12 个文件均为本次改动范围外的既有文件，本次改动的 14 个文件无一出现在列表中，确认未引入格式问题。

## 第 13 条兜底 grep 实际输出

```
$ grep -rn "至少12\|至少 12\|12位\|12 chars\|min 12" --include='*.go' --include='*.js' --include='*.json' --include='*.html' --include='*.md' --include='*.sh' web/ scripts/ README* .env.example docs/nas-product-*.md docs/nas-product-brochure.html
web/frontend/i18n/zh-CN.json:955:    "passphrase_too_short": "口令至少 12 位",
web/common/secret_test.go:7:		"abcdefgh1234",        // 12位基本密码
```

两处均为明确豁免，非清零遗漏：

1. `i18n/zh-CN.json:955` 的 `vault.passphrase_too_short` 是 vault 保险箱本地加密主口令的提示文案，对应 app.js L3214/3397 的 `length < 12` 判断，任务书明确豁免、本次不动。
2. `web/common/secret_test.go:7` 的注释 `12位基本密码` 是 `TestValidSecret` 的测试数据说明——该测试验证的是 `common.ValidSecret` 的**字符合法性**校验（拒绝换行/控制字符等），不是长度下限策略，不在本次改动范围（A 部分只改 create.go/users.go/actions.go 三处长度判断调用点），样例密码本身是 12 位字符串但与「下限」语义无关，不改动。

两者外，兜底范围内未发现任何 12 位残留，清零确认。

## 偏离与理由汇总

1. **B 部分 index.html 未改动**：任务书声称存在的 L741/L847/L3897 处 `minlength="12"` 实际不存在，该文件里所有 `minlength="12"` 均属 vault（已在上方"index.html 未改动"小节详细说明）。按"语义矛盾才申报、不得静默绕过或扩大改动面"的硬约束，已据实申报，未做任何该文件内的改动。
2. **其余所有条目均按任务书原样落地**，无其他偏离。
