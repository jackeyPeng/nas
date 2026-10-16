# NAS 项目变更日志

## 版本变更一览

| 版本 | 日期 | 主要变化 |
|------|------|---------|
| v1.4.0-beta.21 | 2026-10-15 | 文件管理页新增「上传目录」— webkitdirectory 整目录树保留子路径上传（后端 Content-Disposition 原始 filename 逐段消毒、缺失父目录逐段 0755 创建、既有目录权限一律不动、409 覆盖重试不拍平、并发 EEXIST 加固）；穿越用例（`../pwn`）由 stdlib 剥路径后巧合放行改为显式 400 拒绝；修复仪表盘把未安装的可选服务（FileBrowser）计入异常导致全新安装恒显「1 个异常」假告警；两台 Debian 13 测试机部署 + Chrome 实机 E2E 全过 |
| v1.4.0-beta.20 | 2026-10-15 | beta.19 后续修复 — 回收站合并式恢复（原位已有同名目录时逐子项合并回原位，替代整树 .restored 旁落）+ folders.db 读取加固（坏行跳过+日志，不再静默半填充）+ install.sh FileBrowser 条件化补漏（全新安装不再报假「验证未通过」）+ 服务管理页状态徽章四态修复；两台 Debian 13 测试机重置后全新安装完整验收（各 34 项断言全 PASS） |
| v1.4.0-beta.19 | 2026-10-13 | 文件管理器 M3 权限管理全量落地 — stat/chmod/chown/fix-home-perms 四 API + 前端 v3（属性对话框八进制输入+符号位实时预览+预设、批量改权限串行进度、用户页一键修复 home 权限）；递归 Walk 免 sudo（符号链接 chmod 跳过/chown Lchown）；mode 白名单拒 setuid/setgid/sticky；三场景 type-to-confirm；FileBrowser 存量机器可经 cleanup.sh 卸载 |
| v1.4.0-beta.18 | 2026-10-13 | 文件管理器 M2 写操作全量落地 — 上传(XHR 进度)/新建/重命名/移动/复制/删除(进 .recycle 与回收站页联动) 六 API + 前端多选工具栏；setup.sh 新装默认不装 FileBrowser(--with-filebrowser 保留一版)；注册表 FileBrowser 4 项按安装态条件化(保 46/46) |
| v1.4.0-beta.17 | 2026-10-12 | 面板文件管理器 M1 v1 只读基线 — 内置「文件」页取代 FileBrowser 外链（浏览/面包屑/下载/zip 打包/预览/用量）+ resolvePoolPath 路径安全核心（穿越/符号链接逃逸 fail-closed）+ checklist 新增模块 17（166 项/18 模块，修正存量统计口径）+ 产品手册 7.8 章节 |
| v1.4.0-beta.16 | 2026-10-08 | 文档快照 — 新增《协议服务配置详解》(docs/protocol-service-configs.md，6 协议 + 横切配置代码逐项核对)+ 修正 GenerateNFSConfig 过期注释(与 no_root_squash 默认行为对齐)。文件管理器开发前的回滚基线，不含二进制变更 |
| v1.4.0-beta.15 | 2026-09-24 | 家用可用性优先 — NFS 默认 no_root_squash+insecure（挂载即可写、NAT 后可挂）+ public 共享 guest ok + fail2ban 放宽（ban 10 分钟/10 次）+ S3 短用户名 access key 修复 + 审计日志全覆盖加固 + rclone 同步实时进度 |
| v1.4.0-beta.14 | 2026-09-19 | 在线升级加固 — 二进制原子替换 + CLI 升级验签 + 升级后配置幂等迁移（setup.sh --config-only）+ FileBrowser 版本单一事实源 |
| v1.4.0-beta.13 | 2026-09-19 | NFS 导出对所有网段开放（跨网段客户端可挂载） |
| v1.4.0-beta.12 | 2026-09-18 | 创建向导按已选磁盘实时预估容量（选目标后预览面板 + 各方案可用容量/注意事项）+ 登录页品牌 slogan |
| v1.4.0-beta.11 | 2026-09-18 | 当前连接展示（监控页，SMB 会话 + 各协议端口归类）+ 回收站改 per-share `.recycle/`（删除=rename 零拷贝，恢复保留目录树，旧 `#recycle` 兼容）+ 存储页 tab 切换跳动修复 |

---

## [2026-10-15] - 目录上传 + 仪表盘假告警修复

### 版本 v1.4.0-beta.21

### 新增
- **文件管理页「上传目录」**（TODO #35，749402d）：webkitdirectory input 携带 webkitRelativePath，前端 FormData filename 带完整子路径（409 覆盖重试同样保留，不拍平）；后端自解析 Content-Disposition 取原始 filename（stdlib `Part.FileName()` 会 `filepath.Base` 剥路径，且并无 `FullFileName` 方法——已核实），`sanitizeUploadRelPath` 逐段过 validateFileName（拒 `..`/`.`/绝对路径/空段/控制字符/>255B），`ensureUploadParents` 缺失父目录逐段 Mkdir 0755+显式 chmod 消 umask，**既有目录权限一律不动**，中间段撞非目录 400；双重锚定/409 冲突 body/审计与单文件上传同款语义；穿越用例（`../pwn`）由旧「stdlib 剥路径后巧合 200」改为显式 400 拒绝；浏览器限制：空子目录不随 webkitdirectory 上传。新增/改写 9 个后端测试全绿
- 审查加固（854ac3e）：ensureUploadParents 并发 Mkdir EEXIST 重查视同已存在（目录 continue 不 chmod/非目录 400，对齐 handleMkdir 先例）；umask 测试改 0077 真正锁住显式 chmod；消毒拒绝表驱动测试补全树零残留断言

### 修复
- **仪表盘「N 个服务 · M 个异常」假告警**（87a772c）：serviceSummary 把 `not-installed` 的可选服务（FileBrowser，beta.19 起默认不装）计入异常，全新安装恒显「1 个异常」；现过滤 not-installed（不计异常也不计总数），failed/inactive/unknown 仍如实计入——与 beta.20 install.sh 条件化、服务页徽章四态同族收尾

### 验收
- 两台 Debian 13 测试机重置后 install.sh 纯用户路径直装 control 渠道 beta.20 → 替换本段二进制（854ac3e）；57 建池 merge 4×50G；Chrome 实机 E2E：仪表盘「7 个服务 · 全部正常」、目录上传全链路（嵌套 3 文件树/内容一致/新目录 0755）、409 冲突提示含完整相对路径 + 覆盖重试子路径不拍平（无散落文件）

---

## [2026-10-15] - 回收站合并式恢复 + 读取加固

### 版本 v1.4.0-beta.20

### 修复（tag v1.4.0-beta.19 之后）

- **回收站合并式恢复**（29f775d，D1 复审 Important 跟进）：restore 时目标位置已存在同名目录 → 不再整树旁落 `.restored`，改为逐子项合并恢复回原位（子项碰撞才落 `.restored` 后缀不覆盖既有数据；符号链接 Lstat 语义；只删空的回收站源目录）。默认配置下「删除嵌套文件 → 回收站页 UI 恢复」现在精确回原位。独立审查「可合入」，两台测试机实机 5 项断言全过
- **folders.db 读取加固**（d1f3a9d）：GetAllFolderMeta 检查 rows.Scan/rows.Err——NULL 列等坏行跳过+日志，不再静默半填充零值（曾致删除误走池根回退、回收站页 shares 为空，本地手工种库 E2E 因此误判产品 bug）
- **install.sh FileBrowser 条件化补漏**（d78cbce，双机部署抓出）：beta.18 只改了 setup.sh——install.sh 的 banner、服务状态循环（未安装的 filebrowser 打 ✗ 并误置 VERIFY_OK=false）、访问方式 URL 三处补齐与 setup.sh 相同的安装态条件化，全新安装不再报「验证未通过」假警告
- **服务管理页状态徽章恒显示 Running**（b134808）：徽章文字此前写死「运行中」只有颜色随状态变（pitfall 37 同型），未安装服务也显示 Running；改为按 active 四态映射文字+颜色（运行中/未安装/失败/已停止），复用既有 i18n 键

### 验收

- 两台 Debian 13 测试机重置后全新安装（install.sh 一条命令 + 本版二进制 0125d54）→ 建池 merge 4×50G → 各 34 项断言（27 API/文件系统 + 7 SMB）全 PASS 零 FAIL；注册表 46/46；nas-release「两台全过」口径满足

---

## [2026-10-13] - 文件管理器 M3 权限管理

### 版本 v1.4.0-beta.19

### 新增（feat/file-manager 分支，16 Task 计划的 Task 13-15，每 Task 均经独立代码审查+修复轮）

- **权限四 API（web/modules/filemgmt/perms.go）**：
  - 属性 `GET /api/files/stat?path=`：owner/group/mode（字符串八进制 "0644"）/mode_text/recursive_possible；Lstat 语义（符号链接报自身）；池根放行（只读）
  - 改权限 `POST /api/files/chmod` {path,mode,recursive}：mode 白名单（^[0-7]{3,4}$ 且 ≤0777，setuid/setgid/sticky 拒绝）；非递归符号链接拒 400（chmod 跟随链接会误改目标）；递归 filepath.Walk 符号链接跳过；池根拒 400；失败即停 500 带路径
  - 改属主 `POST /api/files/chown` {path,owner,group,recursive}：owner/group 均可选（空=不变传 -1，至少一项）；user.Lookup/LookupGroup 校验失败 400 含具体项；符号链接 os.Lchown 改链接本身（与递归 Walk 语义一致）；池根拒 400
  - 修复 home `POST /api/files/fix-home-perms` {username}：home=池根/<username> 递归 0700 + chown username:同名组（LookupGroup 失败回退主组）；校验链 405→username 含 `/`或`..` 400→用户不存在 400→home 不存在/非目录/符号链接 400（Lstat fail-closed）；不创建缺失子目录（修复不做 provisioning）；权限基准与 EnsureUserHome 同源
  - 面板 root 进程直接 os.Chmod/os.Chown/os.Lchown 免 sudo（sudoers 无 chmod -R 白名单，刻意不扩）；chmod/chown/fix-home-perms 全部纳入审计日志（高危操作富 detail）
- **前端 v3（Task 15）**：文件行内「属性」按钮 → 对话框回显 stat + mode 八进制输入（实时符号位预览 modeToSymbolic）+ 预设按钮（0644/0755/0700）+ recursive 勾选（仅目录）+ owner/group 下拉（users/user-groups 懒加载）；单条保存 chmod/chown 独立失败各自收集错误；多选工具栏「改权限」批量串行逐条 POST + 进度 N/M + 汇总 toast；用户页每行「修复 home 权限」；三场景 type-to-confirm（单条目录递归/批量=固定确认词、修复 home=输用户名），批量确认有函数级守卫（E2E 抓到的 UI disabled 绕过）；21 个 files.* 双语键（check-i18n parity 1213 键）

### 安全（独立审查驱动的关键修复，全部有回归测试）

- **D1（同构机验收抓出）：默认配置下删除文件在回收站页不可见不可恢复**——向导/建户创建的共享默认 recycle_bin=0，面板删除走池根回退且旧布局仅存 basename（原位信息丢失），而回收站页只枚举 RecycleBin=true 共享。修复：回退布局改池根相对 keeptree（`.recycle/<user>/<池根相对原路径>`，可精确还原原位）+ recycleRoots() 纳入「(池根 xxx)」伪共享（真实同名共享优先）+ restore/delete/clear 统一走「真实共享优先、伪名兜底」解析；伪共享 clear 的 confirm_name 服务端校验同款。同构机复跑：删除→伪共享可见→原位 restore→穿越/非法 user 拒绝→RecycleBin=true share 级路径回归，全过
- fix-home-perms home 根 Lstat 拒符号链接（防池根下链接把递归 chmod/chown 引到池外目标）
- 非递归 chmod 拒符号链接（防跟随链接误改池内其他目标）；递归 Walk 链接跳过/Lchown 语义全程一致
- mode 白名单双重校验（正则+数值 ≤0777）前后端对齐：前端 ^0?[0-7]{3}$ 实时预览即拒 1755，后端独立再校验
- stat 失败路径守卫：api() 对纯文本错误返回真值字符串，调用方按「含 mode 字段对象」判定，错误 toast 不开框（复审 I1）
- 保存前 mode 合法性闸门（单条+批量），杜绝空/非法 mode 直发注定 400 的请求（复审 I2）

### FileBrowser 退役进展

- beta.18 起新装默认不装；存量已装机器升级不受影响（整段不动）。**卸载路径**：跑 `scripts/cleanup.sh`（服务停止+unit/数据库/二进制全清）或手动 `systemctl disable --now filebrowser && rm -f /usr/local/bin/filebrowser /etc/systemd/system/filebrowser.service && rm -rf /etc/filebrowser`；注册表 FileBrowser 5 项（ID 6/21/24/29/33）按二进制存在性条件化，卸载后自动记「未安装」保持 46/46 口径。beta.19 后评估彻底移除 setup.sh 的 --with-filebrowser 开关（spec §十一）

### 验收

- 单测：filemgmt 131 测试全绿（chown 生效断言 root-gated 3 例 skip，生产同构机验收必测）；diskmgmt 回归绿
- 浏览器 E2E（本地 dev + /data/nas1 种池）：单条 stat 回显/预设/预览/保存落盘、批量守卫拦截与成功串行进度、递归目录 chmod 落盘+符号链接目标不变、非法 mode 400 文本展示、fix-home-perms 0777→0700 落盘、EN 确认词、审计落库、弹窗隔离——全过
- Debian 13 生产同构机全链路验收（含 smbclient force user 映射下 0700/0775 实测）见 Task 16 记录

---

## [2026-10-13] - 文件管理器 M2 写操作 + FileBrowser 默认退役

### 版本 v1.4.0-beta.18

### 新增（feat/file-manager 分支，16 Task 计划的 Task 7-12，每 Task 均经独立代码审查+修复轮）

- **写操作六 API（web/modules/filemgmt）**：
  - 上传 `POST /api/files/upload?path=[&overwrite=yes]`（upload.go）：MultipartReader 流式写盘不撑内存（32KB buffer，禁 ParseMultipartForm）；同名 409（带 conflict/written 列表）+ overwrite 覆盖；磁盘满 507 清半成品；O_EXCL 并发碰撞不误删（EEXIST→409）；落盘按目标共享权限对齐（public 0664+nasusers / 其他共享 0600+同名属主 / 无所属 0644，alignPermsDecision 纯函数 + chown 失败降级日志）
  - 新建目录 `POST /api/files/mkdir`（mutate.go）：validateFileName 白名单（拒 `/`、`..` 子串、控制字符、>255 字节）+ 双重路径锚定断言；mode 恒 0755（显式 chmod 消 umask 影响）
  - 重命名 `POST /api/files/rename`：renameat2(RENAME_NOREPLACE) 内核原子防 TOCTOU 覆盖；同名 no-op 200；池根拒绝 400
  - 移动 `POST /api/files/move` {src,dst_dir}：同 fs renameat2 零拷贝；跨 fs copyTree → 字节校验 → 权限对齐 → 全部通过才删源（任何失败点源保留）；目标在源内部（含 EvalSymlinks 解析后）400——堵无限递归写满磁盘
  - 复制 `POST /api/files/copy`：递归 copyTree（符号链接拷链接本身不跟随，目录/文件 mode 保源后经权限对齐）；失败半成品清理（EEXIST 例外不误删并发方文件）
  - 删除 `POST /api/files/delete` {path}：os.Rename 零拷贝进所属共享 `.recycle/<user>/<原相对路径>`（keeptree，与 diskmgmt 回收站页/SMB vfs recycle 同源，restore 直接恢复原位）；同名冲突加 `.<unixnano>` 后缀；无所属共享或共享未启用回收站回退池根 `.recycle`；共享根/池根/`.recycle` 段全拒（防套娃基于解析后真实路径，堵前导空格与池内符号链接两处绕过）
- **前端文件页 v2（Task 11）**：复选框多选（全选/单选，切目录清空）+ 工具栏（上传/新建目录/刷新 + 选中后移动/复制/删除）；上传走 XMLHttpRequest 带真实进度条（percent + loaded/total），409 confirm 后带 overwrite=yes 重试；移动/复制共用目录选择器（逐级浏览，过滤 .recycle）；删除确认展示条目列表（>10 折叠），含目录时 type-to-confirm 输入目录名；rename 后 selected 同步剔除旧名；29 个 files.* 双语键（check-i18n parity 1192 键）
- **setup.sh FileBrowser 默认不装（spec §十一 M2 节点）**：新增 `--with-filebrowser` 开关（保留一个版本周期，beta.19 评估彻底移除）；新装默认整段跳过 [7/10]；已装机器整段不动（不重装不重配不重启）；尾部访问方式提示条件打印
- **注册表 FileBrowser 4 项条件化**（ID 6 服务/21 systemd unit/24 二进制/33 数据库）：以 `/usr/local/bin/filebrowser` 存在性为门，未安装记 pass「未安装(面板文件管理已取代)」——全新机器保持 46/46 验收口径，--with-filebrowser 机器全真检

### 安全（独立审查驱动的关键修复，全部有回归测试）

- rename/move 用 renameat2(RENAME_NOREPLACE)：Lstat 预检后的 TOCTOU 窗口不再静默覆盖目标（EEXIST/ENOTEMPTY→409）
- 上传 O_EXCL 碰撞不误删：清理责任收敛到 writePartToFile 内部（仅 copy 失败清自己建的半成品），handler 按错误类型分流 409/507
- copy/move 目标在源内部 → 400（GNU cp 同款），containment 用 EvalSymlinks 真实路径比较——堵字面前缀与池内符号链接两种无限递归构造
- 删除防套娃基于解析后路径判定（resolvePoolPath 归一 + EvalSymlinks），前导空格/池内链接指向 .recycle 两处实证绕过均堵死
- 跨 fs move 字节校验通过 + 权限对齐成功后才删源；失败清目标保源
- filemgmt 包为 Linux-only（renameat2/Stat_t），发布仅交叉编译 linux amd64/arm64 不受影响

### 验收

- 单测：filemgmt 96 个测试全绿（TDD，全部调真函数断言真实文件系统）；diskmgmt 回归绿
- 浏览器全流程 E2E（本地 dev 面板 + 真实 Chrome，2026-10-13）：mkdir/上传(8MB md5 一致+进度条 DOM 实测渲染)/rename/move/copy(选择器真实点击+落盘验证)/删除文件+目录(type-to-confirm)/回收站 keeptree 落位与 API 条目可见/409 覆盖重试/审计页富 detail/i18n EN 切换全通过
- 遗留（留 M3/同构机验收）：跨 fs 真实多盘路径、回收站页 UI restore 按钮联动、root 环境 chown 严格断言

### 文档

- feature-operation-checklist.md：模块 17 增至 14 项（+6 用户操作），汇总 172 项/18 模块（awk 实算）
- nas-product-manual.md：7.8 更新 v2 能力与 FileBrowser 退役说明
- TODO.md：#34 M2 完成勾选，M3 待办
- 遗留加固项（记录待评估）：diskmgmt GetAllFolderMeta 的 rows.Scan 错误未检查（NULL 列会静默半填充 meta）；审计 detail 用户输入转义（M2 收尾轮）

### 说明

- 面板服务页仍可手动安装 FileBrowser（opt-in），已装机器升级不受影响
- M3（Task 13-16）：stat/chmod/chown、fix-home-perms 一键模板、前端 v3 权限对话框、生产同构机全链路验收 + FileBrowser 卸载提示

---

## [2026-10-12] - 面板文件管理器 M1 v1 只读基线

### 版本 v1.4.0-beta.17

### 新增（feat/file-manager 分支，docs/plans/2026-10-08-file-manager-{design,plan}.md 16 Task 计划的 Task 1-6）

- **web/modules/filemgmt 模块（整池文件管理，取代 FileBrowser）**：
  - 路径安全核心 `resolvePoolPath`（path.go）：所有 handler 的唯一路径入口，入参一律池根相对路径；拒绝绝对路径/`..` 子串/池外路径；符号链接逃逸校验对**最深已存在祖先** EvalSymlinks（堵住「叶子不存在但中间目录是逃逸链接」的穿越）；池根 normalizedRoot 归一后比较（生产 /data/nas1 为符号链接时不误拒）
  - 列目录 `GET /api/files/list`（browse.go）：排序/属主/权限位/符号链接标注，2000 条截断 + truncated 标志
  - 下载 `GET /api/files/download` + 目录 `GET /api/files/zip`（transfer.go）：ServeContent+Range 断点续传；zip 流式写 ResponseWriter，符号链接存 stub 不跟随，zip 根为符号链接目录时先 EvalSymlinks 再 Walk（防空壳包）
  - 预览 `GET /api/files/preview` + 用量 `GET /api/files/usage`（preview.go）：文本 ≤1MB JSON 传输 + 前端 x-text 转义（无效 UTF-8→U+FFFD）；图片 ≤10MB；SVG 一律 415（可内联脚本，预览即存储型 XSS）；du 独立异步端点，du_status 三态 ok/timeout/error，前端非 ok 一律降级
- **前端「文件」页 v1**：目录浏览 + 面包屑导航 + 下载 + 预览 + 用量展示；导航「文件」入口由 FileBrowser 外链替换为面板内页面；blob URL closePreview 防泄漏；预览截断文案 i18n 化

### 安全

- download/preview 响应带 `X-Content-Type-Options: nosniff`；1MB 截断边界测试
- resolvePoolPath fail-closed 设计 + 全覆盖单测（穿越/逃逸/归一化）

### 文档

- feature-operation-checklist.md：新增模块 17「文件管理（FileMgmt）」8 操作项；**修正存量统计口径**（旧汇总表写 158/用户操作 83，awk 实算 157/82，本次按实算基数更新为 166 项/18 模块）；「系统自动任务汇总」重编号为第 18 节并收录路径锚定校验项
- nas-product-manual.md：新增 7.8「面板文件管理 — 内置 Web 文件页」；7.6 FileBrowser 加过渡期说明（入口已替换，M2 后默认不再安装）
- TODO.md：新增 #34 面板文件管理（取代 FileBrowser），状态进行中（M1 完成，M2/M3 待办）

### 说明

- FileBrowser 服务本期仍随 setup.sh 安装、仍可独立访问（8081），仅面板导航入口替换；setup.sh 默认不装 FileBrowser 在 M2（beta.18）落地
- v1 为只读基线：无上传/新建/重命名/移动/删除/权限操作，M2/M3 按里程碑推进

---

## [2026-10-08] - 文档快照（文件管理器开发前基线）

### 版本 v1.4.0-beta.16

### 文档

- **新增 docs/protocol-service-configs.md《协议服务配置详解》**：SMB/NFS/FTP/WebDAV/S3/FileBrowser 六协议的服务配置逐项代码核对版（配置源、生成规则、认证与隔离粒度、重载方式、端口），加横切配置（fail2ban/ufw/sudoers 白名单/SyncAllConfigs 链路）与本次核对发现的 4 项问题清单（1 已修 3 待办：回收站共享 vfs objects 覆盖 fruit 模块、vsftpd 双模板漂移、全局协议旁路表述精确化）
- storage-permission-model.md 头部加交叉引用

### 修正

- **GenerateNFSConfig 过期注释**（config_sync.go）：注释仍写"默认 root_squash 安全基线"，与 nfsExportOpts 实际行为（默认 no_root_squash，2026-09-24 产品决策）相反；已更新为决策记录。纯注释改动，无行为变化

### 说明

- 本版为文件管理器（面板整池文件管理，取代 FileBrowser）开发前的回滚基线：仅 tag + CHANGELOG，不跑 release.sh、不更新对外 control 渠道，一键安装拿到的仍是 beta.15 二进制（内容等价，beta.15 后无代码行为变更）

---

## [2026-09-24] - 家用可用性优先 + S3 修复 + 审计加固

### 版本 v1.4.0-beta.15

### 变更（产品决策：家用可用性优先）

- **NFS 导出默认反转为 `no_root_squash,insecure`**：覆盖 09-21 的 root_squash 安全基线方案——客户端 root 挂载后直接可写（不再出现"挂上了却 Permission denied"）；`insecure` 允许 ≥1024 源端口，NAT 后的客户端（跨网段访问常态）不再被 mountd 以 illegal port 拒绝。readonly 文件夹一律 `ro,...,insecure`；`nfs_no_root_squash` 列保留（DB 兼容）但不再生效。需要收紧的部署可将文件夹改只读或手工编辑 /etc/exports（注意配置同步会覆盖托管段）
- **public 共享 `guest ok = yes`**：局域网设备（电视/手机/访客电脑）免账号直接访问，写入经 force user 统一映射到服务账号；管理员一旦按用户编辑过权限，自动恢复账号制
- **fail2ban 放宽**：bantime 3600→600 秒、maxretry 5→10 次——避免家用用户输错密码把自己锁在门外（jail.local + setup.sh 模板同步）

### 修复

- **S3 access key 短于 3 字符被 gofakes3 拒绝**：rclone serve s3 内嵌库强制 accessKeyMinLen=3，短用户名（如 fm）的签名请求一律 InvalidAccessKeyId（403），匿名探测正常故长期未暴露；新增 `common.S3AccessKey()` 派生——<3 字符加 `z1-` 前缀（fm → z1-fm），setup.sh/services.go 两处单元生成/安装提示同步；实际值以 /etc/rclone/s3-env 为准
- **改密后 S3 静默失效**（既有缺口）：changePassword 更新 NAS_USER 密码时从不重写 rclone-s3 的 auth-key；现同步更新 s3-env + service ExecStart 并重启服务
- **审计日志结果徽章永远绿色**：前端固定显示 success，failed 记录也标绿；日志筛选缺"诊断"等分类，补齐
- **GetAllFolderMeta 返回 null**：无文件夹时 JSON 输出 null 而非 []，前端需判空；显式 make 空切片

### 新增

- **审计日志全覆盖加固**（09-21）：loggingMiddleware 拦截所有非 GET `/api/*` 写操作（此前 54 个接口漏记，现 100%）；JWT 真实身份 + client IP；HTTP ≥400 记 failed；/api/login 成败均入档（失败含尝试用户名+IP，可追溯暴力破解）；4 个 SSE 流式接口 handler 内显式审计
- **权限矩阵/总览回显改读 folders.db**（唯一事实源），不再解析 smb.conf 生成物；SyncFolderMeta 签名改整结构体，杜绝写入路径漏字段（write_users 事故的结构性修复）
- **rclone 同步任务实时进度**：进度面板展示传输速率/已传字节，新增文件数统计（海量小文件任务可估算完成度）；事件中心模块补录操作清单（158 项/17 模块）

### 验收

- 两台测试机清空重装：9 服务 active、注册表 46 项 37 过 + 9 警告（存储池未建，建池后消除）；RAID1 建池 + testshare（回收站+NFS）创建，/etc/exports 与 smb.conf 托管段生成正确（no_root_squash,insecure / guest ok 均生效）
- 工作站实测全协议：NFS v4.2 挂载 root 写入成功、SMB guest 免密上传成功、FTP 登录+上传、WebDAV PROPFIND+PUT 201、FileBrowser JWT 登录+列表、S3（z1-fm）boto3 list/put/get/list 全过；账号制隔离验证（fm 连 testshare 被拒，符合预期）

---

## [2026-09-19] - 在线升级加固

### 版本 v1.4.0-beta.14

### 新增

- **升级后配置幂等迁移**：`setup.sh --config-only` 只重生成托管配置（Samba/NFS/FTP/WebDAV/S3/防火墙），跳过 apt 安装、目录创建、FileBrowser/面板安装；CLI（upgrade.sh）与 OTA（apply-upgrade.sh）在健康检查通过后自动 `git pull` 更新模板 + 跑一次 --config-only，新版配置格式变更天然完成迁移，失败不回滚二进制（面板已健康）只提示手动重跑；NAS_USER 支持显式传入（systemd-run 环境无 SUDO_USER）
- **CLI 升级验签**：upgrade.sh 下载后拉 `latest-${ARCH}.json` manifest 做 SHA256 + ed25519 校验（openssl，公钥与 OTA update.go 内嵌一致；hex→binary 用纯 bash printf，无 xxd 依赖），与面板 OTA 安全水位拉平；manifest 不可达时降级为 ELF+大小检查并明示警告

### 修复

- **二进制替换原子性**：upgrade.sh / apply-upgrade.sh 原先从 /tmp `mv` 到 /usr/local/bin——跨文件系统时 mv=copy+delete，中途断电会留下半截二进制且旧文件已删；改为同目录 `install .new` + rename（同 fs 原子操作）
- **root 跑 git pull 撞 safe.directory**：apply-upgrade.sh 经 systemd-run 以 root 执行，git 对非 root 属主仓库做所有权检查导致配置模板更新失败；`git -c safe.directory=$(readlink -f /opt/nas)` 显式放行
- **回滚提示误导**：升级完成后的回滚命令从 `cp` 改为 `mv`（运行中二进制 cp 会报「文本文件忙」）
- **FileBrowser 版本三处不一致**（services.go 写死 ×3、setup.sh 变量、upload 脚本 v2.32.0 陈旧）：收敛到 `web/common/versions.go` 单一常量；R2 key 拼写 `filebroswer/` → `filebrowser/`（新旧 key 双上传/双源兜底）；upload-filebrowser.sh 重写为从 Go 常量读版本 + GitHub 官方源下载 + tarball 结构校验

### 验收

- 测试机 .48 实测：CLI 全链路（--check 验签 → 备份 → 原子替换 → 健康检查 → git pull → --config-only），升级后 7 服务全 active、SMB 托管段/NFS 导出完好；回滚双向（.bak mv 回退 → 再升级）通过；OTA 面板内升级派发 → apply 独立 unit 存活过重启 → 配置同步完成
- 设计文档：docs/plans/2026-09-19-upgrade-simplification.md

---

## [2026-09-19] - NFS 导出对所有网段开放

### 版本 v1.4.0-beta.13

### 新增

- **NFS 导出对所有网段开放**：导出客户端由「本机网段自动检测」改为 `*`，跨网段/多网卡客户端（如笔记本走路由访问 NAS）不再被 mountd 拒绝 `unmatched host`；setup.sh 移除内网段检测，旧托管导出行安装时统一归一为 `*`；面板 NFS 徽章提示文案同步更新

---

## [2026-09-18] - 创建向导容量预估 + 登录页 slogan

### 版本 v1.4.0-beta.12

### 新增

- **创建向导按已选磁盘实时预估容量**：第 2 步选择目标（安全性/容量/读写性能/平衡）后新增预览面板——「已选 N 块磁盘 · 总容量」摘要 + 该目标下每个方案的可用容量、安全性、注意事项；第 3 步方案卡片与第 4 步确认页的可用容量改为按第 1 步实际所选磁盘重算（后端按全部空闲盘计算，只选部分盘时数值不准）。混合容量盘按最小盘计算 RAID5/6，与 mdadm 实际行为一致（single=第一块盘、raid1=min(前两块)、raid5=min×(n-1)、raid6=min×(n-2)、merge/raid0/separate=总和）
- **登录页品牌 slogan**：「你的AI Nas / Your AI Nas」中英双语

### 修复

- 向导第 4 步确认页「可用容量:」「安全性:」双冒号
- `raidName`/`raidDesc`/`raidWarn`/`safetyLabel` 补 i18n 响应式依赖，语言切换后方案名称/注意事项正确刷新

### 验收

- 测试机真机验证：2/3/4 盘 × 四目标共 7 组容量断言全对（含 50G+2T 混合盘 RAID5=100G、独立模式=2.1T）；真实点击走完 4 步向导全流程；中英切换正常；i18n parity 1141/1141

---

## [2026-09-18] - 当前连接展示 + per-share 回收站 + tab 跳动修复

### 版本 v1.4.0-beta.11

### 新增

- **当前连接展示**（TODO #31）：监控告警页新增「🔗 当前连接」卡片——`GET /api/monitor/connections` 聚合各协议活跃会话：SMB 用 `smbstatus --json`（用户名/IP/共享列表/连接时间，Samba 4.22 schema，tcons 按 session_id 关联），SSH/FTP/NFS/WebDAV/S3/面板/FileBrowser 用 `ss` 按本地端口归类，`who` 补 SSH 用户名；回环过滤 + IPv4-mapped IPv6 归一；协议徽章 + 相对时长，随监控页轮询刷新
- **per-share 回收站**（TODO #25，Architecture v1.0 第十条）：Samba `vfs_recycle` 从集中 `#recycle` 改为每共享 `.recycle/%U`（按删除者隔离）+ keeptree 保留目录树 + `directory_mode 0700` + `hide files` 对 SMB 客户端隐藏。删除=rename（跨设备移动不再 copy+delete），恢复=rename 回原路径零拷贝。存储管理新增「🗑️ 回收站」tab：共享筛选/恢复/彻底删除/清空（confirm_name=共享名服务端校验，恢复冲突加 `.restored` 后缀不覆盖）；旧 `#recycle` 兼容只读展示（「旧版」徽章）；`resolveRecyclePath` 防目录穿越；事件总线接入 recycle.restored/deleted/cleared

### 修复

- 存储管理页 tab 切换跳动（2026-08-20 记录的已知问题）：全部 tab 内容包进 `.tab-stack` grid 层叠容器（`grid-area:1/1`），离开的 tab 淡出期间不再把新内容顶到下方；实测 6 tab 循环切换 stackTop 恒定零跳动
- 监控连接 IPv4-mapped IPv6（`::ffff:x.x.x.x`）显示归一为纯 IPv4
- 全仓回收站扫描排除点同步 `.recycle`：overview/import/config_sync/folders + backup-data/list-users/disk-cleanup 脚本

### 文档

- TODO.md 完成率 21/33 → 23/33（69.7%）；feature-operation-checklist.md 监控页 +1、存储页新增 5.6 回收站 Tab（6 项），汇总 141 → 148

### 验收

- 两台测试机全新安装 + 完整验收通过：注册表 46/46、SMB 真实删除→`.recycle/<user>/` 落盘→API 恢复整树回原路径、越权/穿越/无确认词全拒、事件落库、连接卡片实测显示 SMB 会话
