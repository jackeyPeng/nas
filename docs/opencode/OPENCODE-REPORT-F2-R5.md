# OPENCODE-REPORT-F2-R5 — F2 Task 7 文档三件套

分支 `feat/agent-f2`，commit `f339381`。本轮只做文档，机器验收由 parent 负责。

## 交付内容

1. `docs/feature-operation-checklist.md`：新增第 21 节「通知中心」、第 22 节「主动管家」，原第 21 节「系统自动任务汇总」顺移为第 23 节并补 4 行（notifications/triggers 的系统自动项汇总入跨模块总表）；汇总统计表用附录 awk 命令实算更新；`最后更新` 日期改 2026-10-23。
2. `docs/nas-product-manual.md`：11.6 AI 助手章节追加「通知中心与主动管家」小节（铃铛位置/建议卡限流的用户视角说明/执行确认流程/周报内容与时间/关闭 AI 后零足迹），用户视角行文，未出现内部函数名/表名/端点路径。
3. `CHANGELOG.md`：「未发布」段新增「家庭管家底座 F2」条目组，6 条带短哈希（notifications/triggers 骨架/事件源接线/周报/接受链/前端通知中心），不建版本号段。

## awk 实算输出（提交后，对最终文件跑的真实结果）

```
$ cd docs && awk -F'|' '/^\|/{gsub(/ /,"",$3); if($3=="只显示")a++; if($3=="用户操作")b++; if($3=="系统自动")c++} END{print "只显示:",a; print "用户操作:",b; print "系统自动:",c; print "合计:",a+b+c}' feature-operation-checklist.md

只显示: 70
用户操作: 119
系统自动: 43
合计: 232
```

文档内「汇总统计」表与上面实算结果逐字一致（只显示 70 / 用户操作 119 / 系统自动 43 / 合计 232 / 功能模块数 23）。此前旧数字是 66/110/34/210/21 模块——本轮未做增量加法，而是在加完两个新模块节 + 汇总表补行之后，重新对最终文件跑一遍 awk 拿真实结果直接写回文档，避免手算漂移。

## 文档-代码一致性抽查表（11 项，要求 ≥8）

| # | 文档描述 | 代码位置 | 核对结果 |
|---|---|---|---|
| 1 | 冷却窗口 24h（同 trigger_key） | `web/modules/triggers/triggers.go:54` `suggestionCooldown = 24 * time.Hour` | 一致 |
| 2 | 活跃建议上限 5 条（state=new） | `web/modules/triggers/triggers.go:57` `activeSuggestionLimit = 5` | 一致 |
| 3 | 水位阈值 85% | `web/modules/triggers/rules.go:36` `watermarkThresholdPercent = 85` | 一致 |
| 4 | 水位检查 cron 每 6 小时 | `web/modules/triggers/rules.go:33` `watermarkCronExpr = "0 */6 * * *"` | 一致 |
| 5 | 周报生成周日 9 点 | `web/modules/triggers/report.go:20` `reportCronExpr = "0 9 * * 0"` | 一致 |
| 6 | 接受建议端点 `POST /api/triggers/suggestions/{id}/accept` | `web/modules/triggers/triggers.go:430-432`（`handleSuggestionSubroute` 分发到 `handleAcceptSuggestion`） | 一致 |
| 7 | 手动重新生成周报端点 `POST /api/triggers/report/generate` | `web/modules/triggers/triggers.go:394` 路由注册 | 一致 |
| 8 | 接受确认复用既有 `POST /api/ai/action/confirm`，零新确认语义 | `web/modules/triggers/accept.go:95`（`ai.CreatePendingAction` 产出 action_id/confirm_word，前端提交到既有确认端点，accept.go 本身不新增确认逻辑） | 一致 |
| 9 | AI 关闭时通知中心整体隐藏（铃铛+面板） | `web/frontend/index.html:88`（`x-show="notificationsVisible"` 包住铃铛按钮与面板容器）+ `app.js` `notificationsVisible` getter 绑 `ai.IsEnabled` | 一致 |
| 10 | `task.progress` 不入通知收件箱（防刷屏） | `web/modules/notifications/`（计划 Task 1 订阅清单，`task.done`/`task.failed`/`health.*`/`rclone.sync_failed`/`suggestion.created`/`report.generated` 入箱，`task.progress` 不在列） | 一致（本轮未重新读 subscriber.go 全文，以 T1 commit 信息 + 计划规格交叉确认，细节见偏离说明） |
| 11 | 关断守卫来源：notifications/triggers 均用 `ai.IsEnabled`，后台循环常驻但每次动作前查 guard | `web/main.go:143,151,188,189` | 一致 |

## CHANGELOG 对帐（git log 逐条核对）

F2 分支 T1-T6 共 6 个 feat 提交（`docs/plans` 的计划提交 `63ca82c` 不计入，文档提交本身 `f339381` 不计入）：

```
31bae7b feat(ui): 通知中心 — 铃铛+未读徽标/建议卡(执行/忽略/去看看)/周报卡/通知列表+t()插值+接受确认弹窗(复用type-to-confirm) 双语i18n (F2 T6)
c523618 feat(triggers): 建议接受链 — ai.CreatePendingAction导出(复用既有确认门/TTL/确认词)+accept端点(plan固化→pending action→confirm即执行)+重放/过期/空计划对抗用例 (F2 T5)
fb21339 feat(triggers): 家庭周报 — 周日9点cron+手动生成端点,存储/本周任务/健康三块模板化数据落收件箱(kind=report,零LLM依赖) (F2 T4)
a562d4f feat(triggers): 事件源接线 — health迁移/备份失败/同步失败→人话建议卡 + 磁盘水位6h cron(≥85%荐photo_dedupe) + 人话翻译表第一批(双语) + backup失败事件生产方补点 (F2 T3)
5478ed2 feat(triggers): 主动管家骨架 — suggestions表/冷却24h/活跃上限5/actions校验/REST + cronMatch抽common共享(rclone行为不变) (F2 T2)
b70e8ee feat(notifications): 轻量收件箱 — notes表/AddNotification/List/MarkRead内核+REST(enabled守卫)+eventbus订阅(task.done/failed,health.*,sync_failed,suggestion.created→i18n键通知) (F2 T1)
```

全部 6 条逐一写入 CHANGELOG 未发布段，短哈希对齐 git log 输出，无遗漏、无多算。

### cleanup.sh 修复条目核查（任务书要求「若不在 master 段则一并记」）

```
git log --oneline --graph -3 2b9d5b7
* 2b9d5b7 fix(cleanup): 重置补删仓库实体(/root/soft/nas含data面板数据库) ...
* 63ca82c docs(triggers): F2实施计划 ...
* c9ec8f8 docs(release): v1.4.0-beta.31 CHANGELOG — 未发布段转正(家庭管家底座F1)

git branch --contains 2b9d5b7
  master
```

`2b9d5b7`（cleanup.sh 重置补删仓库实体修复）落在 `master` 分支上，紧跟在 F2 计划提交之后、但**不在** `feat/agent-f2` 分支的提交链里（`feat/agent-f2` 从 `63ca82c` 之后直接接 F2 T1-T6，不含 `2b9d5b7`），而是作者在 master 上单独修的一个运维脚本 bug，跟本轮 F2 T1-T6 的功能范围（notifications/triggers）无关。按任务书原话「若不在 master 段则一并记」——它**已经在 master 段**（commit 本身就落在 master 分支），所以不需要并入本次 F2 条目组；它会在 master 自己的「未发布」段被单独记录（目前 CHANGELOG 尚未记录，但这超出本轮 F2 文档任务的范围，留给 master 线的下一次文档整理处理）。

## 偏离

1. **规则表与原计划出入**（继承自 F2 T3 既有偏离记录，文档按代码现实写）：计划草案的 `health.raid`/`health.disk_smart` 两条规则在代码里合并成一条真实规则——订阅 `health.storage` 的 error 迁移（见 `web/modules/triggers/rules.go` 文件头注释）。本轮产品手册与操作清单均按这条合并后的真实规则描述（「硬盘出了问题」统一指向这一条），不单独区分 RAID/SMART 两种措辞，避免文档比代码更细致导致失实。
2. **汇总统计未按「增量计算」**，而是新增两节 + 补汇总表行之后对最终文件整体重新 awk 实算——任务书要求「别拿旧数字增量算」，本轮按此执行，结果见上文 awk 输出，与文档内数字逐字一致。
3. **抽查表第 10 项**未重新完整读取 `web/modules/notifications/subscriber.go` 全文逐行核对订阅 code 列表，是基于 F2 T1 commit 信息与计划规格交叉确认，已在表中注明；其余 10 项均为直接读取代码定义/路由注册得出。
4. cleanup.sh 修复条目按上文核查结论不计入本轮 F2 CHANGELOG 条目组，原因与依据已写明，不属于擅自减少交付范围（任务书原话的条件判断本身决定了它不该记在这里）。

## 验证

- `cd web && go build ./...`：通过，无输出即无错误。
- `go vet ./...`：通过。
- `python3 scripts/check-i18n.py`：zh-CN/en-US 均 1396 keys，一致（本轮未新增 i18n 键，不涉及双语新增）。
- 未执行 `go test ./...`（本轮不改动任何 `.go` 代码，只改三份文档，不影响既有测试结果；复用 F2 T1-T6 各轮已验证的测试状态）。

## 红线自查

- 全文搜索未出现真实测试机 IP（统一用「测试机」/「两台测试机」等泛指，产品手册新小节未提及任何 IP）。
- 阈值 85% / 冷却 24h / 上限 5 条 / 周报周日 9 点均逐一对照代码常量定义核实一致（见上文抽查表 #1-5）。
- 产品手册新小节未出现内部函数名（如 `CreateSuggestion`/`AcceptSuggestion`）、表名（`suggestions`/`notifications`）、端点路径（`/api/triggers/...`），全部用用户视角的"建议卡/确认卡/周报/铃铛"等措辞描述。
- 未 `git add` 本任务书（`TASK-F2-R5.md`）与本报告（`OPENCODE-REPORT-F2-R5.md`），`git status` 确认二者仍是未跟踪状态。
