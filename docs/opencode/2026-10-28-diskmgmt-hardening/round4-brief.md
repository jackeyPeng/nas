# 任务：install.sh 升级路径静默重置面板密码修复（真机实锤）

你在 ~/soft/nas 仓库工作。基线 = HEAD (2d58fbc)。

## 缺陷（48 真机实证）

已装机（/opt/nas/.env 里 NAS_PASS 是用户改过的密码）重跑 `bash scripts/install.sh`（不带 NAS_PASS 环境变量）时，步骤 [3/10] 创建配置文件把 .env 的 NAS_PASS 重置回出厂默认密码（日志行：「未提供 NAS_PASS，使用出厂默认密码（首次登录后强制修改）」；.env mtime 落在升级窗口；用户密码 24-hex 被换成 13 位出厂密码，must_change_password 重新变 true）。升级把管理员密码静默打回出厂默认 = 知道默认密码的任何人在用户下次登录前都能进面板，安全回退。

## 期望语义（三级优先，修复目标）

1. 调用时显式提供 NAS_PASS 环境变量 → 用它（用户明确意图，覆盖任何现值）。
2. 未提供 NAS_PASS，但目标机已有 .env 且其中 NAS_PASS 非空 → **保留现值不动**（升级场景），日志打「检测到已有配置，保留现有面板密码」。
3. 两者皆无（全新安装）→ 出厂默认 + 首登强制改密（现状行为，保持）。

## 修法要求

- 通读 scripts/install.sh 的 .env 创建段（步骤 [3/10] 附近）与 scripts/setup.sh 所有写 /opt/nas/.env 的路径（sudoers 里有 `tee /opt/nas/.env` 条目，说明 setup.sh 也写 .env——逐个调用点核实是否会重置 NAS_PASS；setup.sh 被 install.sh 调用也会被面板升级流程调用，两条链都要满足上面三级语义）。
- .env 其他键（NAS_USER 等）的现有处理语义不得改变；只修 NAS_PASS 的覆盖行为。
- 保留现值的实现要幂等且不破坏 .env 其他行（读旧值→写新文件时回填，或干脆不触碰 .env；选最不易出错的方式，报告说明选择）。
- 出厂默认密码的判定逻辑（面板侧 must_change_password 依赖「密码==出厂默认」）不得被破坏：全新安装路径行为逐字节不变。
- `bash -n` 两个脚本过语法；如仓库有 shell 相关测试脚手架则跑之（没有不强求）。
- 禁读仓库外文件、只改 scripts/install.sh 与 scripts/setup.sh（如需改 web/ 侧说明理由先申报，不得直接改）。

## 提交（原样 message）

```
fix(scripts): 升级路径保留现有面板密码 — install.sh/setup.sh 不再把已改的 NAS_PASS 静默重置回出厂默认
```

报告写 TASK-REPORT.md（覆盖，不 git add）：缺陷根因（哪个函数/行重置的）、setup.sh 各 .env 写点的核实结论、三级语义实现方式、全新安装路径不变的论证、bash -n 输出。
