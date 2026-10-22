# NAS 项目变更日志

## 版本变更一览

| 版本 | 日期 | 主要变化 |
|------|------|---------|
| v1.4.0-beta.30 | 2026-10-22 | AI 配置保存与错误反馈修复（用户实报：保存成功后点「测试连通性」无反应）——① 配置保存的 key 覆盖根修：保存后前端清空 key、「测试连通性」先保存再测试，空 key 被 POST 回后端直接覆盖已存真 key（掩码回显 `****xxxx` 原样保存同理会存进掩码串）；现在空值/掩码回显一律视为「未修改」保留原 key，仅非空新值才更新 ② ai 模块 34 处错误响应从 text/plain 改 JSON content-type——此前前端 api() 不识别、失败完全静默，现在错误原因（如 DeepSeek 返回的「api key is invalid」）直接弹 toast ③ 前端 testAIConnection 补失败兜底分支；补 5 个单测（空值保留/掩码保留/新值更新+Verified 重置/JSON content-type）。实机验证：真实 DeepSeek 端点测试连通、开关开启、对话与三工具链全通过 |
| v1.4.0-beta.29 | 2026-10-21 | AI 助手入口可发现性修复 — 关闭态侧边栏此前无任何「AI」字样（仅剩工具箱组头部一个 27px 小 emoji，用户实测找不到模块）：导航项改为常驻可见并带「未启用」灰徽标（开启后自动消失），点击进入引导卡+开启向导；移除冗余小入口；引导文案同步更新（删除「开启后才出现在工具箱」过时表述，补「关闭态零足迹」说明，双语） |
| v1.4.0-beta.28 | 2026-10-21 | AI 助手模块（M1+M2）— 可开关内置功能模块（默认关闭零足迹）：OpenAI 兼容端点接入（DeepSeek/Ollama 预设+连通性测试+Key 0600 存储掩码回显）、NAS 域 15 工具（9 只读+6 写，ToolDomain 抽象+schema 校验，直调既有模块内核）、SSE 流式对话+SQLite 会话历史、写操作 type-to-confirm 确认门（参数服务端留存/一次性 Take/TTL 5 分钟）、create_user 密码前端生成直注（永不进模型上下文/落库/审计）、对抗性安全测试集（注入/schema 攻击/Key 泄漏面/开关守卫）。附验收修复集：删用户补 WebDAV 凭据撤销（被删用户仍能过 rclone 认证）、shell 管道调用点清零、AI 页 Alpine 响应式修复（气泡/工具轨迹不随流式更新）、控制面守卫豁免（关闭态无法存配置）、建池文案按模式报实际用盘数；双机重置用户级全流程验收（RAID1+LVM merge 各 38 项套件+31 项 AI E2E 全 PASS+本机 Chrome UI 实测） |
| v1.4.0-beta.27 | 2026-10-21 | 运维脚本审计修复 + 收尾 — cleanup.sh 存储拆除段前置到卸载软件包与删 /data 之前（修复 set -e 下 rm 挂载点 busy 即中止、RAID/LVM/软链接全残留的实踩事故；数据目录删除加"仍有挂载即跳过并警告"守卫）；配置备份/恢复补面板数据库整目录（folders.db/vault.db/registry.db/2fa.json/rclone tasks 等）、rclone.conf（root+用户）、/etc/projects+projid——重装恢复不再丢保险箱/云同步/共享元数据/2FA；rclone 升级下载按架构取 deb（arm64 不再必败，R2 已补 arm64 制品）；sudoers 白名单按实测调用面补全并收敛为具体子命令/路径粒度（115 条，拒绝 bash -c/cat */rm -rf * 等 root-equivalent 通配，visudo 校验通过）；仪表盘存储池卡片类型标签不再写死「单盘」（RAID 显示级别、LVM 按盘数区分单盘/合并，双语 i18n） |
| v1.4.0-beta.26 | 2026-10-20 | 安全审计全量修复 — P0：命令/sed 注入根除（证书 domain 校验+openssl 直传参、chpasswd/smbpasswd 走 stdin、.env 与 rclone-s3 unit 改纯 Go 行级重写、htpasswd 直传参、密码统一拒绝换行/控制字符）、/api/system/check 免认证暴露收口（loopback+JWT）、登录/TOTP IP 限流（5 次失败锁 15 分钟）、服务开关白名单、Samba/SSH 配置注入防护、备份路径穿越防护；P1：面板 HTTPS 真正生效（TLS_CERT/TLS_KEY→ListenAndServeTLS）、XFS 配额根修（建池挂载+fstab 加 prjquota、/etc/projects 改标准 id:path、建文件夹配额真正落地）、防火墙端口范围/备注回显/sudoers 补齐、xfs_growfs 改挂载点（扩容静默失败根除）、NVMe 分区名+mkfs 参数、删池/重置清脏数据、诊断三 bug；P2：磁盘操作全局锁补漏、rclone 状态事务锁、SQLite 统一 WAL（顺带根治 logger/eventbus 旧 DSN 参数被驱动静默忽略）、monitor 后台采样（1.5s→<1ms）+du/smartctl 缓存（不再反复唤醒休眠盘）、RAID 注册表区分初始重建与降级；双机重置验收全 PASS（用户一键安装 RAID5 机 + 分支构建 LVM 机，套件 33 项+五协议+配额 E2E+限流/注入实测） |
| v1.4.0-beta.25 | 2026-10-20 | 零知识凭证保险箱 v2（浏览器端 PBKDF2-600k+AES-256-GCM，服务器只存密文；独立口令/5 错锁 30 分钟/自动锁定/查看自动遮罩/旧版一键迁移）+ 独立密码生成器页 + 侧边栏「🧰 工具箱」分组；附发布前审查与体检修复集：迁移失败即停防明文丢失、解锁失败计数原子化、改口令实时拉取+覆盖校验、输错保险箱口令误踢登录修复、PWA sw.js 网络优先+旧缓存清理（根治升级后界面陈旧）、rclone 状态真实版本显示、配置一致性不再误报池根用户目录、staticcheck 清零；第八轮双机重置用户级全流程验收（merge/RAID1 各 33 项全 PASS + Chrome 实测） |
| v1.4.0-beta.24 | 2026-10-19 | 彻底移除 FileBrowser — 面板文件管理已完全取代；装机链/服务页/注册表(46→41)/HTTPS/密码同步/监控/备份/i18n/文档全链路清理；setup.sh 升级迁移自动卸载存量并备份 /etc/filebrowser；R2 安装包同步清理。附验收修复集：升级机注册表幽灵项剪枝、用户服务开关静默失败×2、x-for 重复 key 崩溃（'after' 错误真身，导航失效根因）、关于页 hardware null 守卫；第七轮双机重置完整验收全 PASS + Chrome 实测 |
| v1.4.0-beta.23 | 2026-10-17 | UI 巡检修复集 — 共享影子身份（建共享时的 nologin 占位用户）不再泄漏为用户页幻影用户；仪表盘/用户页 null 数组崩溃修复；面板停止的服务不再误标「失败」（is-active exit3 四态归一 + rclone unit SuccessExitStatus=143，已停止显黄色徽章）；健康中心口径对齐（not-installed 不计 warn、新增「N 个已停止」warn）；权限报错文案与校验对齐；第六轮双机重置完整验收（merge/RAID1 各 34 项全 PASS）+ Chrome 实测 |
| v1.4.0-beta.22 | 2026-10-16 | rclone 安全升级 v1.75.2（修 serve webdav 路径遍历 GHSA-44pm-2q5p-fm63 等命中我方暴露面的高危项 + Go 1.26.9 HTTP/2 CVE；新装必落 pin 版本；服务页重装路径 serve 根对齐 /data/nas1/public 消除越权隐患）+ 系统健康注册表存储层 6 项按池模式感知（RAID 池机器不再恒 fail，46/46 可达）；双机重置重装完整验收（merge/RAID1 各 35 项断言全 PASS） |
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

## [2026-10-22] - v1.4.0-beta.30 — AI 配置保存 key 覆盖根修 + 错误反馈不再静默

用户实报：AI 设置页保存 DeepSeek 配置成功后，点「测试连通性」毫无反应。排查为两个 bug 叠加，另暴露一个用户侧配置问题：

- **fix(ai)·key 覆盖根修**：`saveAIConfig` 成功后前端清空表单 api_key，而 `testAIConnection` 是「先保存再测试」——空 key 被 POST 回 `/api/ai/config`，后端无条件 `APIKey: req.APIKey` 把已存真 key 覆盖成空，随后的连通性测试必然 401；页面刷新后掩码回显（`****`+后4位）原样保存，还会把掩码串本身存成真 key。修复：`handleConfigSave` 把「空值或等于当前掩码」一律视为未修改、保留 `prev.APIKey`，仅非空新值才更新（Verified 三字段比对逻辑不变）。
- **fix(ai)·错误响应 content-type**：ai 模块 34 处 `http.Error`（text/plain 却写 JSON 形状体）全部改为 `jsonError` helper（application/json + json.Marshal 生成体，状态码与文案逐字不变）——此前前端 `api()` 只对 JSON 错误自动 toast，text/plain 被当字符串静默返回，`testAIConnection` 又无失败分支，用户看到的就是「点了没反应」。前端同时补字符串兜底分支（双保险不双弹）。
- **test(ai)**：新增 5 个单测——空 key 保存保留原值且 Verified 不误清、掩码回显保留、新 key 更新且 Verified 重置、jsonError 的 content-type/合法 JSON 断言；模块 91/91 绿。
- **实机验证（测试机 57，真实 DeepSeek 端点）**：test-connection `ok:true` → 开关开启 → 非流式对话正常 → 工具链对话真实调用 get_health_report/get_storage_overview/get_smart_status 并汇总「41/41 自检通过」。同时验证用户侧根因确为 key 无效（DeepSeek 返回 "Your api key is invalid"，修复后该错误可见）与 base_url 缺 `/v1` 后缀（预设按钮自动填对）。

---

## [2026-10-21] - v1.4.0-beta.29 — AI 助手入口可发现性修复

- **fix(ai)**：AI 导航入口常驻可见——beta.28 的入口设计（完整导航项仅在开启后显示，关闭态只剩工具箱组头部一个无文字的小 🤖 emoji）导致用户在面板上完全找不到 AI 模块（用户实测反馈）。改为：工具箱内「🤖 AI 助手」导航项始终显示，未启用时带灰色「未启用」徽标（开启后自动消失），点击进入未启用引导卡+开启向导；移除组头部冗余小入口。enabled 守卫语义不变：只约束功能面 API，不约束入口展示。
- **docs(ai)**：引导卡文案更新——删除「开启后才会出现在工具箱入口」的过时表述，补充「未开启时零足迹：不发起任何模型请求，对话与工具接口全部关闭」说明（中英双语）。
- 验证：本机 Chrome 实测关闭态徽标显示/开启态徽标消失/开关循环/引导卡新文案/页面渲染，双测试机部署 rc2 复核版本锚点。

---

## [2026-10-21] - v1.4.0-beta.28 — AI 助手模块（M1+M2）+ 发布前验收修复集

### AI 助手（新功能模块，默认关闭）

- **定位**：家庭助手基座——NAS 域为首个交付，后续健康/生活秘书类能力以独立工具域（Domain Pack）接入；安康系统按同一功能模块模式规划。
- **模块形态**：可开关内置功能模块，默认关闭且零足迹；控制面端点（status/config/toggle/test-connection）豁免开关守卫，功能面（chat/conversations/action）关闭态一律 403；开启前提=已配置端点且连通性测试通过。
- **模型接入**：OpenAI-compatible 客户端（流式/非流式），DeepSeek（云端）/Ollama（本地）双预设联动，API Key 0600 权限存储+掩码回显，独立连通性测试端点。
- **工具框架**：ToolDomain 抽象+参数 schema 校验（未知参数拒绝、Enum/白名单约束）；NAS 域 15 工具——9 只读（健康报告/服务状态/存储总览/共享文件夹/用户列表/防火墙/诊断/日志/系统信息）+ 6 写（建用户/改配额/加防火墙规则/重启服务/跑诊断/建共享文件夹）；只读工具统一 JSON 输出+8K 截断+参数 clamp；写工具全部直调既有模块内核（缺导出的模块补 export.go 薄 wrapper，不复制业务逻辑）。
- **对话**：SSE 流式（delta/tool_call/tool_result/pending_action/done/error 事件协议）+ SQLite 会话存储+会话列表/历史重载。
- **确认门（写操作最终防线）**：写操作一律转待确认卡——参数全程服务端留存（PendingStore，TTL 5 分钟、一次性 Take、不经客户端回传防篡改），前端 type-to-confirm 弹窗（函数级守卫），确认词错误拒绝、重放 410，执行成败均入审计日志。
- **create_user 密码红线**：工具参数表无 password 字段（模型给不出密码）；初始密码由确认弹窗一次性生成器生成（20 位四类字符），经专用 client_payload 通道随确认请求直发服务端，校验后注入执行——永不进对话上下文、不落消息库、不出现在审计 detail。
- **对抗性安全测试集**：恶意工具结果注入（"忽略之前指令"字样数据不触发直接执行）、schema 攻击面（shell 元字符/路径穿越被拒）、API Key 泄漏面（落库消息/审计 detail/SSE 全文三处断言无 Key）、开关守卫——全部进常驻测试。
- **文档**：功能操作清单新增「AI 助手」模块节（统计重算 64/104/27，合计 195 项/20 模块）；产品手册新增 11.6 节（定位/开关/隐私权衡/确认门语义）。

### 验收与修复（本轮双机重置·用户级全流程）

- **fix(users)**：删用户补 WebDAV 凭据撤销——remove-user.sh 清了 smbpasswd/userlist 却漏 /etc/rclone-htpasswd，被删用户仍能通过 rclone serve webdav 认证（实测抓到：删除后 PROPFIND 仍 207）；补 htpasswd -D+sed 兜底后实测 401。
- **refactor(security)**：残留 shell 管道调用点清零——services unit heredoc/users sed+grep/diagnostics sysfs echo/rclone chown --reference 全部直传参或纯 Go 化，为切换非 root 最小权限运行扫清障碍。
- **fix(ai)**：/api/ai/config 豁免 enabled 守卫——关闭态 403 导致无法保存配置、永远无法开启（前端联调发现的设计缺口）；守卫收窄到功能面。
- **fix(ai)**：SSE 事件改写 push 后的 proxy 引用而非裸对象——修复助手气泡/工具轨迹/pending 卡不随流式更新的 Alpine 响应式 bug（数据层正确、视图层空白；本机 Chrome 实测发现）。
- **fix(ai)**：确认执行后 pending 卡转绿色已执行态且不可再点（防重复确认撞 410）；新增 i18n 键 ai.executed_hint 双语。
- **fix(users)**：CreateUserWithPassword 内核补用户名白名单校验——此前只有 HTTP 向导 handler 校验，AI 写工具直调内核会绕过（对抗测试发现）。
- **fix(diskmgmt)**：建池完成文案按模式报实际用盘数——raid1/single 不再误报全部空闲盘数（4 盘机做 raid1 明示「2 块加入存储池，其余 2 块空闲盘未使用」）。
- **验收**：两台测试机重置干净后走纯用户路径（一键安装→首登强制改密→建池 RAID1 与 LVM merge 各一台→注册表 41/41→验收套件各 38 项全 PASS→AI E2E 各 31 项全 PASS（桩 LLM 驱动全工具链）→本机 Chrome UI 全链实测：流式渲染/工具轨迹/确认弹窗/密码生成器/已执行态）；测试用户与临时端点全部清理复位。

---

## [2026-10-21] - 运维脚本审计修复 + 收尾

### 版本 v1.4.0-beta.27

beta.26 安全审计修复的收尾轮：审计 P3（架构与运维脚本一致性）+ 验收过程新发现的脚本事故 + UI 观察项。

### 变更

**scripts**
- cleanup.sh：「拆除存储池 + 清磁盘签名」段从文件末尾前置为 [5/8]（在 purge 软件包与删 /data 之前——lvremove/mdadm/wipefs 依赖 lvm2/mdadm 包，必须在 purge 前跑）。修复实踩事故：set -e 下 /data/nas* 仍挂载时 `rm -rf /data` EBUSY 非零退出直接中止脚本，拆除段与后续清理全部不执行（残留 md0 挂载、/opt/nas 软链接）。删 /data 前增加挂载守卫：仍有 /data/nas* 挂载则跳过 rm 并警告，绝不带着挂载点递归删除。
- backup-config.sh / restore-config.sh：备份范围补 /opt/nas/data/ 整目录（folders.db、vault.db、registry.db、diagnostics.db、audit.db、events.db、2fa.json、rclone/tasks.json，sqlite -wal/-shm 随目录同拷）、/root 与部署用户的 rclone.conf、/etc/projects + /etc/projid；恢复侧镜像回位（全部存在性判断，旧备份包兼容），db 恢复先于面板/rclone 服务重启。修复「重装恢复后保险箱凭据/云同步任务/共享元数据/2FA 全丢」。
- setup.sh：rclone 升级下载三个 URL 全部按 ${ARCH} 取 deb（原硬编码 amd64，arm64 机器必失败）；get.z1.sale 已补传 rclone-v1.75.2-linux-arm64.deb（GitHub 官方制品，md5 校验一致）。
- setup.sh sudoers：按 web/ 全量 SudoExec/SudoOutput/SudoExecStdin 调用面实测补全（systemctl reload/is-active/daemon-reload/list-unit-files、vgchange、useradd/userdel/usermod/groupadd/gpasswd、id、passwd -S、openssl req/x509/rsa、timedatectl/hostnamectl、sysctl -w、rclone、crontab -l、update-initramfs、apply-upgrade.sh/backup-data.sh、cat/rm/mkdir 具体路径），共 115 条，visudo -cf 通过。**安全不变式（写入脚本注释）**：白名单禁止 bash -c */sh -c */cat */cp */mv */rm -rf */find */apt install/systemd-run 等 root-equivalent 通配条目；代码残留的 shell 管道调用点（services.go unit heredoc、users/create.go 与 groups.go sed/grep、diagnostics.go echo>sysfs、rclone.go chown --reference）刻意不入白名单——面板当前以 root 运行（sudoers 旁路），若未来切非 root 最小权限，必须先改造这些调用点为直传参数。

**web/frontend**
- 仪表盘存储池卡片类型副标题不再写死「单盘」：新增 poolTypeLabel（RAID 显示级别大写、LVM 按盘数区分「LVM 单盘/LVM 合并」、single 显示「单盘」），i18n 中英各加两键（key 数对齐校验通过）。

### 验收
- 脚本轮：bash -n 全过、visudo -cf 通过；cleanup.sh 拆除顺序在测试机 E2E 复验（拆池→重装闭环）。
- go build ./... / go test ./... 全绿（前端为嵌入资源，无 Go 逻辑变更）。

---

## [2026-10-20] - 安全审计全量修复（P0/P1/P2）

### 版本 v1.4.0-beta.26

外部逐行代码审计报告（P0 安全漏洞 / P1 功能失效 / P2 并发与性能，共 20 项）经逐项核实后全量修复；每项修复经独立审查子代理复核，两轮审查发现（密码换行注入回归、服务白名单误伤、Glob 伪修复、采样器 panic 防护等）均已回修。

### 变更

**P0 安全**
- 命令/sed 注入根除：自签证书 domain 白名单校验 + openssl 直传参（废 bash -c 拼接）；建用户/改密的 chpasswd、smbpasswd 改 stdin 传参；管理员改密的 .env NAS_PASS、rclone-s3.service --auth-key、s3-env 三处 sed 拼接改为纯 Go 行级重写 + SafeWriteFile；WebDAV htpasswd 直传参。
- 密码入口统一拒绝换行/控制字符（防 chpasswd 多行注入改任意用户密码、防 .env 注入 KEY=VALUE、防 systemd unit 拆行损坏）。
- /api/system/check 免认证暴露收口：loopback 放行（安装脚本 localhost 调用不受影响）+ 远程要求有效 JWT；不信任 X-Forwarded-For。
- 面板登录 + TOTP 增加按 IP 限流：连续 5 次失败锁 15 分钟（429 + retry_after），成功清零，假时钟单测覆盖。
- 服务自启动开关白名单（8 核心服务 + rclone-webdav/rclone-s3），任意 systemd 服务启停通道关闭。
- Samba 共享 path/comment/valid_users 与 SSH 配置 port/permit_root_login 等参数拒绝换行与控制字符（防 smb.conf 注入 root preexec、防 sshd_config 注入）。
- 备份恢复/删除路径 filepath.Clean 归一 + 前缀后缀校验（杜绝 `../../` 穿越后 sudo rm）。

**P1 功能**
- 面板 HTTPS 打通：main.go 读取 TLS_CERT/TLS_KEY 走 ListenAndServeTLS（此前 applyPanelTLS 写入的环境变量从未被读取，HTTPS 是死代码）。
- XFS 配额根修：建池 mount（7 处）与 fstab（3 处写入点）加 prjquota；/etc/projects 修正为标准 `id:path` 两段格式（兼容归一存量三段脏数据）；**建文件夹带 quota_gb 时真正落地 project 配额**（此前只存元数据从不生效，实机 dd 验证 5G 硬限精确 EDQUOT）。
- 防火墙三修：来源 IP 规则的端口范围不再截断（30000:31000 原样传 ufw）；规则备注解析回显；sudoers 补齐 ufw delete/enable/disable/status numbered/verbose + xfs_quota。
- xfs_growfs 改传 findmnt 解析的挂载点（Debian 12/13 xfsprogs 传块设备必报错），扩容静默失败根除；resize2fs 兜底仅限实为 ext4 时。
- NVMe 分区名（nvme0n1→p1）+ mkfs 按文件系统选参数（xfs -f / ext4 -F）。
- 存储重置/单独删池对齐出厂重置：清 folders.db 行 + .last_reload + SyncAllConfigs 收敛托管段（不再残留幽灵共享）。
- 诊断三修：保存配置不再清空 last_run/last_result/last_detail（防改计划即误触发）；时间窗口按分钟解析；SMART 多盘进度统一 + 轮询超时不再误报通过。

**P2 稳定性/性能**
- 破坏性磁盘操作全局锁补漏（建池/RAID 扩容/换盘/格式化/挂载/卸载/出厂重置互通一把锁）；廉价参数校验前置到锁外（错参请求不排队数小时）。
- rclone 任务状态"读-改-写"整体事务锁（后台调度与前端操作不再互相覆盖）；路径校验移出锁。
- SQLite 统一 common.OpenSQLite：WAL + busy_timeout 5s + MaxOpenConns(1)；**发现并根治 logger/eventbus 旧 DSN 参数写法被 modernc 驱动静默忽略**（并发 database is locked 隐患的真实根因）。
- 性能：/api/monitor 后台采样器（响应 1.5s→<1ms，panic 防护）；du 目录大小 60s TTL 缓存 + 60s 超时 + stale-while-revalidate；smartctl 30s 缓存（不再每次轮询唤醒休眠盘，超时错误不缓存）；存储页目录列表 Glob("/data/*/") 恒空伪展开修复（Glob+IsDir）。
- RAID 注册表项区分"初始重建中（warn + 进度/ETA）"与"真降级（fail）"——建池后重建窗口不再显示故障。
- 并入 beta.25 后未发布项：拆池 umount 竞态加固（00c3932）。

### 验收
- 双机重置验收：A 机走真实用户一条命令安装（RAID5 4 盘池，注册表 41/41，验收套件 33 项全 PASS）；B 机注入本分支构建（LVM merge 4 盘池，套件 33 项全 PASS）。
- 修复项实机实测：配额 E2E（id:path 落盘 + bhard 生效 + dd 精确停在 5GiB）、登录限流（5×401→429，锁定 IP 正确密码亦拒，loopback 不受影响）、单引号密码建户+SMB 登录、换行密码 400、备份穿越 400、防火墙范围规则+备注回显、system/check 远程 401/loopback 200、monitor <1ms。
- 本机 Chrome UI 实测：登录/仪表盘/用户页（配额回显）/防火墙页渲染正常。
- go build ./... / go test ./... / go vet / go test -race 全绿；新增单测 60+。

---

## [2026-10-20] - 零知识凭证保险箱 v2 + 密码生成器 + 工具箱菜单组

### 版本 v1.4.0-beta.25

### 变更
- **凭证保险箱升级为零知识加密模型**（`web/modules/vault/vault.go` 全量重写）：
  - 新增 `vault_meta`（每用户 salt/verify_hash/iterations/失败次数/锁定截止）与 `vault_items`（owner 行级隔离，`encrypted` 字段为浏览器端 AES-256-GCM 密文，服务器全程不接触明文/口令/派生密钥）两张表，所有查询按 `WHERE owner = ?` 隔离，管理员不例外
  - 新端点：`GET /api/vault/status`、`POST /api/vault/setup`、`POST /api/vault/unlock`（常数时间比较，连续 5 次失败锁定 30 分钟，423 响应带剩余秒数）、`GET /api/vault/items`、`POST /api/vault/create`/`update`/`delete`、`POST /api/vault/change-passphrase`（单事务重加密全部条目，任一条目不属于当前用户整体回滚）
  - 旧版（v1 服务端加密）保留只读迁移兼容：`GET /api/vault/legacy`、`POST /api/vault/legacy/reveal`、`POST /api/vault/legacy/purge`（需新保险箱已设置口令才可用，迁移完成后清空旧表）
  - 浏览器端加密内核 `web/frontend/zkvault.min.js`（基于 `@noble/hashes` + `@noble/ciphers` 预构建打包，MIT 协议，零 CDN 依赖）：PBKDF2-SHA256 600,000 次迭代派生密钥 + AES-256-GCM 加解密 + SHA-256 校验哈希
  - 前端新交互：三态页面（未初始化/已锁定/已解锁，`x-if` 惰性渲染）、查看明文 15 秒自动遮罩、复制剪贴板 30 秒自动清空、15 分钟空闲或切换页面自动锁定、修改口令、旧版数据一键迁移
- **新增独立密码生成器页面**（不依赖保险箱解锁）：`crypto.getRandomValues()` 浏览器端生成，长度 8-64 可调，字符类型可选，六档强度指示器，20 条内存生成历史
- **侧边栏新增「🧰 工具箱」菜单分组**（置于「维护」分组之后），收纳「🔐 凭证保险箱」与「🔑 密码生成器」；原「访问控制」分组移除凭证保险箱入口
- **i18n**：zh-CN/en-US 新增 `vault.*`、`pwgen.*`、`nav.toolbox`/`nav.pwgen`、`events.vault.*` 键，中英文严格对齐
- **Service Worker**：`web/frontend/sw.js` SHELL 清单加入 `/zkvault.min.js`（缓存策略与版本在下方修复集升级为 `z1-nas-v4`）
- **文档**：`docs/nas-product-manual.md` 新增 7.8 节（零知识保险箱架构图 + 密码生成器说明）；`docs/feature-operation-checklist.md` 第 9 节改写为零知识模型，新增第 10 节密码生成器，后续章节顺延编号（10-18 → 11-19），汇总统计重新复算
- **合规文档**：`NOTICE.md` / `web/modules/version/NOTICE.md`（镜像同步）/ `THIRD_PARTY_LICENSES.md` / `third_party/manifest.json` 新增 `@noble/hashes` 2.4.0 与 `@noble/ciphers` 2.4.0（MIT）条目；`web/modules/version/version.go` 的 `buildNoticeSections()` 开源许可证列表与关于页组件列表新增「ZkVault 加密内核 · MIT」
- 新增 `web/modules/vault/vault_test.go`：覆盖设置冲突/校验、解锁成功/失败/锁定/过期、条目 CRUD owner 隔离、修改口令事务性、旧版迁移前置条件等场景

### 发布前独立审查 + E2E 修复集（3c87c7f / a8f884a / cbcfd9e）
- **Critical×2**：①旧版迁移失败即停——此前单条 reveal/create 失败会静默跳过且无条件清空旧表，可致明文凭据永久丢失；改为失败即中断、如实报告、绝不 purge。②`vaultFetch` 把解锁接口的 401（口令错误）误判为会话过期直接登出——输错一次保险箱口令即被踢回登录页；改为按响应体区分业务错误与会话过期，错误提示内联显示剩余尝试次数
- **Important×4**：解锁失败计数改 SQL 原子自增（防并发稀释 5 次锁定阈值）；密码生成 randInt 改 rejection sampling（消模偏差）；保险箱口令下限 8→12 位对齐全站密码策略；修改口令改为实时拉取全部条目+逐条旧钥解密（失败即中止）+服务端事务内覆盖数校验（防旧标签页缓存漏加密其它设备新增条目）
- **Minor×7**：生成按钮字符类全关时禁用、强度改熵估算（bits=len×log2(pool) 六档）、isLocked 坏数据 fail-safe、锁定时清倒计时 interval、断网捕获、setup 并发 TOCTOU 归一 409、改口令对话框补 `vault.old_passphrase` 缺失 i18n 键（原显示裸键名）
- **PWA 陈旧根治**：`sw.js` 重写为网络优先+写穿缓存+activate 清理旧缓存（此前 cache-first 且无清理，`caches.match` 按创建顺序恒命中最老副本，升级后界面长期陈旧、离线缺 app.js）；SHELL 补 `/app.js`，缓存版本 → `z1-nas-v4`
- **staticcheck 清零**：删 8 个死函数（含 TODO 挂账的 ownerShareOf 去重项、被服务开关重构废弃的 enableWebDAVUser）+ 死变量/死赋值/测试恒真断言等 10 处卫生项
- **配置一致性检查移除池根未纳管目录扫描**：文件管理器时代池根直属目录是合法用户数据（web 上传目录曾被误报「存在但未在面板中管理」），真实配置漂移仍由 smb.conf/exports 托管段对账覆盖
- **rclone 状态显示真实版本**：此前前端硬编码「未知版本」未绑定 API 返回的 version 字段
- **UI 文案与对齐**（77a5a1f）：「池根」全链改「根目录」（面包屑/回收站伪共享 (根目录 nasN)/EN Root directory）；文件管理表补 `table` 基类修表头与内容对齐 + 单元格 `data-label`（移动端卡片有字段名）
- **加固与根治**：setup.sh 手工路径补 `chmod 600 .env`（含 NAS_PASS/JWT_SECRET）；`TestEventBusSubscribe` flaky 根治（goroutine Drain 与 TempDir 清理竞态，done 通道同步）
- **验收**：第八轮双机重置用户级全流程（48 merge / 57 RAID1）：一键安装零错误、8 服务 active、注册表 41/41、acceptance-suite 33/33、NFS/WebDAV/S3/FTP 协议断言全过、Chrome UI 实测（保险箱全链/工具箱/生成器/rclone 版本/健康页）0 JS 错误

---

## [2026-10-19] - 彻底移除 FileBrowser + 验收修复集

### 版本 v1.4.0-beta.24

### 变更
- **彻底移除 FileBrowser**（7a5990a + a0f1cf9，面板内置文件管理器已完全取代）：

- **装机链**：`scripts/setup.sh` 移除 `--with-filebrowser` 安装开关、`FILEBROWSER_VERSION` 变量与离线安装包文案；原「安装 FileBrowser」步骤改为升级迁移逻辑——检测存量机器上的 filebrowser 服务/二进制，自动 `systemctl stop/disable`，将 `/etc/filebrowser` 备份为 `/var/backups/filebrowser-$(date +%F).tar.gz`，再清理残留文件并 `daemon-reload`；同步移除 ufw 8081 放行规则与 sudoers 中 filebrowser 的 NOPASSWD 项
- **服务管理页**：`web/modules/services/services.go` 移除「安装全部服务」中的 FileBrowser 安装块与 `installSingleService` 的 `filebrowser` 特判分支
- **系统注册表**：`web/modules/system/system_registry.go` 移除 5 个 FileBrowser 相关检查项（原 ID 6/21/24/29/33，不重编号）及 `fileBrowserInstalled()` 辅助函数与全部调用点；总项数 46→41
- **HTTPS**：`web/modules/system/https.go` 移除 FileBrowser 证书应用分支与 `applyFileBrowserTLS()`
- **密码同步**：`web/modules/users/users.go` 移除修改密码时同步 FileBrowser 密码的逻辑
- **监控**：`web/modules/dashboard/dashboard.go`、`web/modules/monitor/connections.go` 移除 FileBrowser 服务定义与端口映射
- **版本信息**：`web/modules/version/version.go` 移除 FileBrowser 组件与许可证条目；`web/common/versions.go` 移除 `FileBrowserVersion`
- **运维脚本**：`scripts/install.sh`、`scripts/monitor.sh`、`scripts/backup-config.sh`、`scripts/restore-config.sh` 移除全部 filebrowser 服务/配置引用；`scripts/upload-filebrowser.sh` 删除
- **前端 + i18n**：`web/frontend/app.js`、`web/frontend/i18n/{en-US,zh-CN}.json` 移除全部 FileBrowser 相关文案与映射，中英文 key 保持对齐
- **文档**：`docs/nas-product-manual.md`、`docs/feature-operation-checklist.md`、`docs/protocol-service-configs.md`、`docs/storage-permission-model.md` 移除全部 FileBrowser 章节/表格/端口说明
- **升级迁移**：存量旧机器执行新版 `setup.sh` 时会自动卸载残留 FileBrowser 并备份其配置目录，不影响已有数据
- **`scripts/cleanup.sh` 保持不变**：仍保留全部 FileBrowser 卸载条目，作为唯一允许引用 filebrowser 的脚本，供手动清理残留使用
- **R2 上的 FileBrowser 安装包已随本版发布清理**（filebrowser/ 与 legacy 拼写 filebroswer/ 两组 key 删除）；旧版面板的「安装 FileBrowser」按钮自本版发布起不再可用，升级即移除
- third_party SBOM 重生成（20 组件，无 FileBrowser），NOTICE/THIRD_PARTY_LICENSES/gen-sbom.py 同步清理

### 修复（第七轮双机验收 + Chrome 巡检发现）
- **升级机注册表幽灵项**（977d4ce）：从旧版本升级后 registry.db 残留已删除目录项（FileBrowser 5 项），报告恒显 46 项——`syncRegistryMeta` 现按当前目录剪除退役行（ID 来自代码内常量，无注入面），报告口径恒等于目录（41）；TDD 内存库模拟旧库剪枝+状态保留
- **用户服务开关两个静默失败**（98bdf6f）：①`PUT /api/users/{name}/services` 把请求中缺失的字段当 false——前端快捷开关一次只发一个字段，导致「开 FTP 顺手关掉 Samba/WebDAV」；现只作用明确给出的字段（parseServiceToggles + 6 用例）②给不在 Samba 密码库/htpasswd 的用户开启服务返回 200 但登录被拒——现前置检查 pdbedit/htpasswd，缺失时 400 并指引「先修改一次密码（自动同步）再开启」
- **x-for 重复 key 崩溃根因**（4db9f8e）：操作日志 `:key="op.time"`、登录日志 `:key="log.time+log.user"` 同秒多条即重复，触发 Alpine keyed x-for 重排分支 `O.after` TypeError（即 beta.23 巡检观察项「reading 'after'」的真身），effect 调度器停摆致整页导航失效；改索引复合 key，双机 boot 错误清零
- **关于页 hardware null 守卫**（5d0990c）：`hardware` 初始 null 时 boot 期 8 条 null TypeError（profile/arch/cpu/memory_human/network），x-show 不阻止子绑定求值——hardware 区块改 `template x-if` 惰性渲染

### 验收
- 第七轮双机重置部署（install.sh 直装 → 替换 rc 二进制 → 57 merge 4×50G(LVM) / 48 向导 RAID1）：**API/文件系统 27 项 + SMB 7 项 + WebDAV(207)/FTP 白名单全 PASS**（目录上传建树含中文名、穿越拒、409 覆盖守卫、回收站合并式恢复 x+y+z 无旁落、chmod/chown/fix-home、setuid 拒、共享文件夹创建+影子身份过滤、pending/apply 队列删净、服务停止→inactive）；注册表 **41/41** 双机（升级机剪枝实证 fb_ghost=0）；Chrome 实测（窗口可见态）：boot 0 错误 0 警告、服务页 7 项无 FileBrowser、健康页 7/7 全绿、用户页 alice/bob/fm 无影子、关于页 hardware 渲染+组件无 fb、文件页正常、停止服务→黄色「已停止」徽章

---

## [2026-10-17] - UI 巡检修复集 + 共享影子身份过滤

### 版本 v1.4.0-beta.23

### 修复
- **共享影子身份不再泄漏到用户页/用户组页**（cc7c53a，Chrome 巡检发现）：建共享文件夹时 pending_ops 会 `useradd -M -s nologin -g 同名` 建占位用户（目录属主/force group 用，设计如此），但它此前出现在用户列表里成为幻影用户（如验收共享 rbtest 显示为可管理用户）。现按「nologin 类 shell + 同名主组」特征过滤（isShareIdentity 纯函数 + 8 用例；面板真实用户一律 /bin/bash 不受影响），users/user-groups 两 API 同步过滤；passwd 中身份保留（共享属主语义不变）
- **Chrome 深度巡检 5 项修复**（9aae591 + 8ee333e，第五轮双机重置部署 + 全 UI 走查发现）：①仪表盘 `configIssues.issues` 为 null 时模板 `.length` 崩溃（后端 config/check nil slice→`[]` + 前端赋值归一化双保险）②用户页空成员组 `g.members` null 连环 TypeError（后端 groups.go 初始化 `[]string{}` + 模板 `|| []` 守卫）③**面板停止的服务显示「失败」红色徽章 + 健康检查假告警**——双层根因：rclone serve 收 SIGTERM 退 143 被 systemd 标 failed（两 unit 加 `SuccessExitStatus=143`，setup.sh/services.go 重装路径/在役机原地三处同步）+ `GetServices` 把 is-active exit 3（inactive 已停止）一律映射 failed（抽纯函数 `classifyServiceActive` 四态归一，8 用例；健康检查新增「N 个已停止」warn 分支修「2/3 全部运行中」自相矛盾文案，3 用例；已停止徽章红→黄 badge-warn）④权限报错文案「3~4 位八进制」与实际校验（4 位 setuid 本就拒绝）矛盾，中英对齐。浏览器复测：登录 0 console 错误、UI 停止→黄色「已停止」+ 健康页「6/7 服务运行中，1 个已停止」、UI 合并式恢复/目录删除 type-to-confirm/批量守卫/递归 chmod 落盘/真实 webkitdirectory 目录上传全树落盘（含中文名）全过
- **健康中心系统服务检查口径与仪表盘对齐**（95160da）：`checkSystem` 不再把 not-installed（FileBrowser 等 beta.18 起默认不装的可选服务）计为 warn——此前默认安装的机器健康中心恒显「7/8 服务运行中，1 个未安装」警告、仪表盘「最近告警」常驻该事件（beta.21 只修了仪表盘统计侧，健康中心侧漏网）；已安装服务数为 0 时仍 warn「未检测到已安装的 NAS 服务」；抽纯函数 `systemCheckFrom` + 表驱动单测，`CGO_ENABLED=0 go test ./...` 全绿

### 验收
- 第六轮双机重置部署（install.sh 直装 control beta.22 + rclone 1.75.2 门槛自动升级 → 替换 rc 二进制 cc7c53a）：57 merge 4×50G / 48 向导 RAID1；**双机各 34 项断言全 PASS**（注册表 46/46 双布局、目录上传建树、穿越拒、回收站合并恢复、chmod/chown/fix-home、SMB 隔离全套）；Chrome 实测：仪表盘 0 console 错误、服务停止→黄色已停止徽章+健康 warn 文案、影子身份过滤后用户/组列表纯净（users=[alice,bob,fm]）

## [2026-10-16] - 注册表池模式感知 + rclone v1.75.2 安全升级

### 版本 v1.4.0-beta.22

### 变更
- **rclone v1.75.2 升级审计修复**（12ed66e）：setup.sh 升级门槛 1.62→1.75（此前发行版 rclone ≥1.62 的机器永远不会升到 pin 版本；现全新安装必落 v1.75.2，双机重装实证）；services.go 服务页重装路径的 WebDAV/S3 serve 根 `/data`→`/data/nas1/public`×4（对齐 setup.sh 产品语义，消除重装后越权暴露全部用户 home 的隐患）；手册 7.4/7.5 根目录/参数表/S3 密钥同步描述对齐产品实际（改密 sed unit 的 `--auth-key` 而非 s3-env），步骤 9 移除对不存在的 configs/rclone-s3.service 模板引用
- **rclone 升级 v1.75.2**（setup.sh 下载 pin 1.74.4→1.75.2 三 URL + 手册版本表/命令同步）：deb 已上传 R2（releases/rclone-v1.75.2-linux-amd64.deb，公网 200 验证）；两台 Debian 13 测试机实机升级验证——WebDAV PROPFIND 207/GET/PUT 落盘、S3 s3cmd 全链路（mb/put/ls/get/rm/rb）、面板 rclone 状态页正确上报 v1.75.2、服务重启后双 active；升级后双机完整验收各 34 项断言全 PASS（57 merge 池 / 48 向导建 RAID1 池——#36 池模式判定经真实建池流程复验 46/46）。已知 quirk（非本次引入，1.74.4 同款）：rclone 自身作 s3 客户端连 rclone serve s3 报 InvalidBucketName，标准客户端 s3cmd 正常
- **升级安全动机（1.74.4→1.75.2 跨三个版本，命中我方暴露面）**：serve webdav 越出服务目录的路径遍历（GHSA-44pm-2q5p-fm63，直接打在 rclone-webdav :8080 上，为本轮最高危项）；serve s3 presigned URL 可被变成复制任意对象（GHSA-w3h7-jxxq-vfrp）+ multipart 缓冲上限被超大 part size 绕过（GHSA-qjw8-8hmp-c3m6，DoS 向）+ 客户端声明 part size 内存耗尽（GHSA-2p48-j3qc-rx9f，1.75.1）；随 Go 1.26.9 消除 HTTP/2 服务端 CVE（HPACK 竞态崩溃 CVE-2026-97032 / Trailer 头内存耗尽 CVE-2026-78659 / Range 头无大小限制 CVE-2026-78667）。行为变化：OPTIONS 请求现要求认证（GHSA-gj73-fh6v-92fj），双机实测 WebDAV/s3cmd 客户端均不受影响；serve s3 新增 --multipart-max-uploads / --multipart-streaming-buffer-total / --metadata-max-objects 三个内存限制参数，默认值生效、无需改 unit。附带收益：s3 multipart 改流式直通后端（内存占用大降）+ 对象列表惰性分页（深层目录翻页变快）

### 修复
- **系统健康注册表存储层 6 项按池模式感知**（TODO #36，d359836 + 审查修复 96b02f6）：此前 34 VG/35 LV/36 PV/37 RAID/38 磁盘签名按 merge/LVM 模式硬编码——RAID 模式池（mdadm+XFS 直挂）机器上健康的 md0 恒 fail、LVM 三项恒 warn，46/46 永远达不成。新增纯函数 detectPoolMode（按 /data/nas1 挂载源判 raid/lvm/none/other）+ parseMdstat（阵列名/级别/槽位/降级/resync）+ storageItemStates 四模式判定表：RAID 池 34-36 pass「LVM 不适用」、37 查阵列健康（降级 fail/无阵列矛盾 fail/健康 pass 列出全部阵列含 resync 进度）、38 分模式文案；none 模式保留「LVM 存在但未挂载」诊断信号（pass+提示）；merge 模式语义不变（md 残留仍 fail）。39 恢复挂载计数文案。22+2 组表驱动测试；实机验证：RAID1 池测试机 46/46 全 pass（含 XFS 上目录上传/回收站合并恢复/SMB 隔离 34 项验收全过）、merge 池测试机 46/46 无回归

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