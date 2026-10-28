# 任务：48/57 真机验收错误统一修复轮

你在 ~/soft/nas 仓库工作，Go module 在 `web/`。基线 = HEAD (fe21146)。背景：两轮 diskmgmt 修复（8609240/c89fa1c+602f1f8/17024dd+fe21146）已在 48（LVM merge）/57（RAID1+foreign 盘 fixture）双机用户级全流程验收，套件 38/38×2 全绿，本清单是真机验收记录的全部错误与遗留。硬约束同前：禁读仓库外文件、零新依赖、不全仓 gofmt、缺口申报不静默绕过。**一个提交**，message 原样：

```
fix(nas): 真机验收统一修复 — parted依赖缺失/RAID扩容isDiskInUse双保险/separate拦截前移/http.Error迁移/死代码死键清理
```

报告写 TASK-REPORT.md（覆盖，不 git add）。

## 构建与测试

```
cd web && GOPROXY=https://goproxy.cn,direct go build ./... && go vet ./modules/diskmgmt/ && go test ./modules/diskmgmt/ -count=1 && cd .. && python3 scripts/check-i18n.py && bash -n scripts/install.sh && bash -n scripts/setup.sh
```

## F1 — parted 依赖缺失（真机实锤：48/57 全新安装后 /usr/sbin/parted 不存在）

现状：`scripts/install.sh` 约 211-214 行 apt 安装列表与 `scripts/setup.sh` 约 285-286 行依赖列表都没有 parted；但 sudoers 白名单含 `/usr/sbin/parted -s /dev/* mklabel gpt mkpart primary * 0% 100%` 条目、`web/modules/version/version.go:280` 第三方声明含 parted、wizard.go 非流式 setupSingleDisk（deprecated 但仍注册）调用 parted——全新安装机器上该路径必然失败。
修法：两个脚本的 apt 列表各加 `parted`（install.sh 加在 xfsprogs 同行附近；setup.sh 加在 xfsprogs 旁）。改完 `bash -n` 语法校验。不改 sudoers、不改 version.go。

## F2 — handleRAIDExpandStream 缺 isDiskInUse 纵深防御

现状：`raid_expand.go` 对新盘只做 isSystemDisk + getDiskStatus 的 `Type=="unused"` 单层检查；换盘（maintenance.go:38）与 LVM 扩容（pool_extend.go:63）都是 isSystemDisk+isDiskInUse 双保险。真机负面测试确认单层检查当前能拦住 foreign 盘（Fix 1a 后 Type=data），但缺纵深。
修法：在 raid_expand.go 现有「不是空闲磁盘」检查之后追加 `isDiskInUse(device)` 检查，命中发 error 事件（Detail：device + " 已被占用（PV/RAID成员或含存量签名），不能用于扩容"）并 return。加锁后执行处如已有等价检查则报告说明。

## F3 — separate 拦截前移（流式）

现状：stream.go handleWizardSetupStream 的 `mode=="separate"` 拦截在磁盘扫描与「没有可用的空闲磁盘」检查之后——无空闲盘机器上（48 实测）用户看到的是「没有空闲磁盘」而非「separate 已下线」，文案误导。
修法：把 separate 拦截移到函数最前（拿锁后、getDiskStatus 之前即可，保持锁语义不变），确保 mode=separate 恒定返回下线文案。对应更新/新增单测断言（若拦截点不可纯函数测试，报告说明，靠既有 TestValidateModeRejectsSeparate 兜底也可）。

## F4 — diskMounts/loadDiskMounts 死代码删除

现状：app.js 的 `diskMounts` 状态与 `loadDiskMounts` 方法在 B1 删除四个低级别方法后已无任何调用方，index.html 零引用（上轮审查已核实）。
修法：删除两者。删前再跑一次 `grep -rn 'diskMounts\|loadDiskMounts' web/frontend/` 确认零引用，有引用则申报不删。

## F5 — diskmgmt 模块 http.Error JSON 体全量迁移 jsonError

现状：17024dd 只迁移了审查点名的调用点；`grep -rn 'http.Error' web/modules/diskmgmt/` 仍有约 10+ 处手拼 JSON 体走 text/plain（maintenance.go 的 method not allowed/confirm/type 校验/清理启动失败等、wizard.go 的 method not allowed/请确认操作/未知模式已迁但同文件或有残留、其他文件自行 grep 盘点）。前端 api() 对这些响应返回字符串，调用方 if(data) 误判成功的风险面与上轮 Important 完全同族。
修法：diskmgmt 包内所有 `http.Error(w, <JSON形状字符串>, code)` 一律换成 `jsonError(w, msg, code)`（helper 已存在于 diskmgmt.go）。逐个迁移前先 grep 全量列表写进报告；**非 JSON 形状的 http.Error（纯文本报错）一并迁移**（jsonError 对任何 msg 都产出合法 JSON）。迁移后 `grep -rn 'http.Error' web/modules/diskmgmt/` 应为 0 命中（或逐条说明保留理由）。

## F6 — 13 个死 i18n 键删除（先逐键复核）

上轮报告确认的死键清单：`msg.enter_path`、`msg.device_mount_required`、`msg.mount_success`、`msg.enter_mount_or_device`、`msg.unmount_success`、`msg.enter_device`、`msg.format_confirm`、`msg.format_success`、`msg.confirm_type_device`、`storage.raid_name_separate`、`storage.raid_desc_separate`、`storage.raid_warn_separate`、`storage.mode_separate`。
修法：**逐键**在 app.js/index.html/i18n.js 全量 grep（含动态拼接形态：`'storage.raid_name_'+`、`'msg.'+` 等前缀拼接消费方——raid_name_separate 一族就是动态拼接产物，必须确认拼接源已不再产生 separate id）确认零消费后，从 zh-CN.json 与 en-US.json **成对**删除；任何一键发现有消费即保留并申报。禁止 json.load→json.dump 整文件回写（用文本锚点删行，保持缩进），改完 json.load 验合法性 + check-i18n.py 验 parity + `git diff --stat` 确认只有删除行。

## 明确不修（勿动）

- TOCTOU 锁外校验窗口（S2 里程碑处理）；
- install.log 密码行 ANSI 色码（记录为脚本解析注意事项，不改产品）；
- wizard.go 非流式 deprecated 路径本体（F1 让它可用即可，整体下线是 S2 议题）。

## 报告要求

逐 F 项：改动摘要、文件:行、F5 的全量迁移清单（迁移前 grep 列表 → 迁移后 grep 结果）、F6 的逐键复核结论、测试与 check-i18n 输出、顶层 PASS 计数（当前基线 70）、偏离申报。
