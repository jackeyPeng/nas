# 面板文件管理器 — 设计规格（方案 A · 自建 filemgmt 模块）

> 日期: 2026-10-08 · 状态: 待用户评审 · 基线: v1.4.0-beta.16（开发前回滚点已打 tag）
> 取代对象: FileBrowser（8081，只盖 public、单账号、root 进程无权限校验）
> 关联: docs/storage-permission-model.md（权限模型）、docs/protocol-service-configs.md（协议配置）、skill nas-storage-architecture（改存储代码前必读）

---

## 一、背景与目标

管理员（家长/机主）现在对池内文件是**盲的**：面板没有文件管理，FileBrowser 只暴露 public 且单账号，管理员看不到也动不了任何用户的 home 内容。帮家人恢复误删文件、整理照片、清理空间、修复权限错乱——全得 SSH 上机或另找电脑挂 SMB。

目标：在面板内提供**管理员视角的整池文件管理**，一个 root 视角的文件浏览器覆盖 public + 每个用户 home + 所有纳管共享，支持浏览/上传/下载/移动/复制/删除/重命名/新建目录/改属主权限。删除与既有回收站页联动。

非目标（本期不做）：
- 用户自助登录面板管自己的 home（需要面板多用户认证体系，工程量大，另立项）
- 在线编辑文本/Office 文档（只做预览）
- 跨池（独立模式多池）间的文件操作 UI 优化（同池优先，跨 fs 走 copy+delete 兜底）

---

## 二、范围（v1 已拍板）

- **使用范围**：仅管理员视角。面板单管理员登录不变，管理员可浏览/管理整池所有目录（含每个用户 home）。
- **文件操作全集**（用户多选确认全要）：
  1. 浏览 + 下载（只读基线）
  2. 上传 + 新建目录 + 重命名
  3. 移动 / 复制 / 删除（删除走 .recycle 回收站）
  4. 改属主 / 权限（chown/chmod，管理员修复权限错乱用）

分三期落地，每期独立可发版：

| 期 | 内容 | 交付标志 |
|----|------|---------|
| **v1 只读基线** | 列目录、面包屑、下载（单文件 + 目录 zip 流）、文本/图片预览、磁盘用量 | 管理员能"看见"整池 |
| **v2 写操作** | 上传、新建目录、重命名、移动、复制、删除（进 .recycle） | 管理员能"整理" |
| **v3 权限修复** | chown/chmod 对话框、"修复 home 权限"一键模板、批量选择操作 | 管理员能"修权限" |

v1 完成即宣布 FileBrowser 进入淘汰倒计时（见 §十一）。

---

## 三、架构与组件

新增模块 `web/modules/filemgmt/`，与 diskmgmt 平级，main.go 加一行 `filemgmt.RegisterRoutes(mux)`。前端新增 `files` 导航页。

```
web/modules/filemgmt/
├── filemgmt.go        # RegisterRoutes + handler 分发（r.Method 分支）
├── path.go            # 路径锚定与穿越防护（核心安全件）+ 单测
├── browse.go          # 列目录 / 面包屑 / 用量
├── transfer.go        # 上传（流式）/ 下载（ServeContent + zip 流）
├── mutate.go          # 新建目录 / 重命名 / 移动 / 复制 / 删除（进 .recycle）
├── perms.go           # chown / chmod / 权限模板
├── preview.go         # 文本 / 图片预览（只读，限大小）
└── *_test.go          # 纯函数单测（path/perms/size 格式化）
```

复用既有件（不重造）：
- **路径穿越防护**：参照 `diskmgmt/recycle.go` 的 `resolveRecyclePath`（拒绝空/绝对/含 `..`，且校验 `strings.HasPrefix(full, filepath.Clean(root)+sep)`）。抽成 filemgmt 自己的 `resolvePoolPath`，逻辑同构。
- **共享归属判定**：`diskmgmt` 的 folders.db 读取（GetAllFolderMeta / enrichSharedFolder），用于"文件属于哪个 share"→ 删除时定位对应 .recycle。
- **回收站语义**：删除落 `<share>/.recycle/<admin>/`，与回收站页（diskmgmt/recycle.go 的 list/restore/delete/clear）天然联动，不另起回收站。
- **前端框架**：index.html 导航 `<li @click="navigate('files')">` + `<div x-show="page==='files'">`；app.js 加 files 页状态与方法；i18n 走 zh-CN.json（真相源）+ en-US.json 同步。

---

## 四、API 设计

全部挂 `common.AuthMiddleware`。路径参数统一用 query `?path=<相对池根的 POSIX 路径>`（如 `public/photos`、`fm/documents`），**不接受绝对路径**，服务端锚定到池根。写操作一律非 GET → 审计中间件自动记录（见 §六）。

### v1 只读

| 方法 | 端点 | 说明 |
|------|------|------|
| GET | `/api/files/list?path=` | 列目录：返回 entries[]（name/type/size/mode/owner/group/mtime）+ 当前路径用量 + 面包屑。按类型再名称排序，目录在前 |
| GET | `/api/files/download?path=` | 单文件下载：`http.ServeContent`（自动支持 Range/断点续传、Content-Type 嗅探） |
| GET | `/api/files/zip?path=` | 目录打包下载：`archive/zip` 流式写 ResponseWriter，不落地临时文件 |
| GET | `/api/files/preview?path=` | 文本/图片预览：文本限 1MB（超出截断提示），图片直接 ServeContent；非可预览类型返回 415 |
| GET | `/api/files/usage?path=` | 磁盘用量（df 池挂载点 + 当前目录 du，du 异步/限深防卡） |

### v2 写操作

| 方法 | 端点 | 说明 |
|------|------|------|
| POST | `/api/files/upload?path=` | 上传：`r.MultipartReader()` **流式**写盘（不用 ParseMultipartForm，避免大文件撑爆内存）；同名冲突返回 409 + 可选 `?overwrite=yes` |
| POST | `/api/files/mkdir` | 新建目录：`{path, name}`；name 过 isValidFolderName 类校验（防穿越/非法字符） |
| POST | `/api/files/rename` | 重命名：`{path, newName}`；同目录 rename |
| POST | `/api/files/move` | 移动：`{src, dstDir}`；同 fs → `os.Rename` 零拷贝；跨 fs → copy+delete（见 §五） |
| POST | `/api/files/copy` | 复制：`{src, dstDir}`；同 fs 也走 copy（复制语义）；目录递归 |
| POST | `/api/files/delete` | 删除：`{path}`；rename 进所属 share 的 `.recycle/<admin>/`（保留目录树），与回收站页联动；无所属 share（池根散落文件）→ 直接进 `<pool>/.recycle/<admin>/` |

### v3 权限

| 方法 | 端点 | 说明 |
|------|------|------|
| GET | `/api/files/stat?path=` | 返回当前 owner/group/mode（八进制 + 符号），供对话框回显 |
| POST | `/api/files/chmod` | `{path, mode, recursive}`；mode 白名单校验（0000-0777） |
| POST | `/api/files/chown` | `{path, owner, group, recursive}`；owner/group 必须是已存在系统用户/组 |
| POST | `/api/files/fix-home-perms` | 一键模板：`{username}` → home 递归 0700 owner:owner；public 递归 2775 nasUser:nasusers（对齐 EnsurePoolStructure/EnsureUserHome 的既定权限） |

> 批量操作：v3 前端支持多选，后端逐条调用上述端点（不另设批量端点，保持 API 面小、审计粒度细）。

---

## 五、关键数据流

### 5.1 列目录（v1 核心）
```
GET /api/files/list?path=public/photos
  → resolvePoolPath("public/photos")  # 锚定 /data/nas1，防穿越
  → os.ReadDir + 逐项 os.Lstat（取 owner/group/mode/mtime/size）
  → 符号链接标注 target（不跟随，防逃逸）
  → 返回 entries[] + 面包屑（池根/public/photos）
```
- 大目录分页：entries 超 2000 项返回前 2000 + `truncated:true`（前端提示"目录过大，请用 SMB/搜索"）。
- du 用量异步：list 不阻塞等 du，用量由 `/api/files/usage` 单独取（前端懒加载）。

### 5.2 删除进回收站（v2，与回收站页联动）
```
POST /api/files/delete {path: "fm/documents/old.zip"}
  → resolvePoolPath → 绝对路径 /data/nas1/fm/documents/old.zip
  → 找所属 share：folders.db 里 path 前缀最长匹配（fm → /data/nas1/fm）
  → 目标 .recycle：<share>/.recycle/<admin>/<时间戳>/old.zip
  → os.Rename（同 fs 零拷贝，保留目录树上下文）
  → 回收站页（diskmgmt/recycle.go list）即可看到、可 restore/delete/clear
```
- 复用 recycle.go 的 `.recycle/<user>/` 约定与 keeptree 语义，删除= rename，与 SMB `vfs objects=recycle` 的回收站同源（同一份 .recycle，两处可见）。

### 5.3 移动/复制跨 fs 兜底
```
同 fs（同池内，/data/nas1 下都是同一挂载）：os.Rename，O(1)
跨 fs（独立模式多池，或池↔系统盘——本期 UI 限制只在池内，跨池罕见）：
  → 递归 copy（io.Copy 流式）→ 校验字节数 → 删源
  → 大目录返回任务 id + 进度（复用 rclone 进度面板的 SSE 模式，或轮询）
```
- 本期 UI 默认只在单池内操作，跨 fs 是兜底路径，进度展示可简化为"处理中"不阻塞。

### 5.4 上传流式（v2）
```
POST /api/files/upload?path=public/uploads  (multipart/form-data)
  → r.MultipartReader() 逐 part
  → 每个 part：resolvePoolPath(path + filename) → io.Copy 到目标（32KB buffer）
  → 同名存在且无 overwrite=yes → 409
  → 写盘后 chmod 对齐目标目录默认（public 0664/0775 setgid，home 0600/0700）
```
- 不用 `r.ParseMultipartForm`（默认 32MB 内存上限，大文件会落临时目录再搬，慢且占系统盘）。
- 上传大小上限：配置项（默认不限，靠磁盘空间兜底）；前端显示已传字节。

---

## 六、安全边界（所有期共用，硬约束）

1. **路径强制锚定池内**：`resolvePoolPath` 是唯一入口，拒绝：空路径、绝对路径、含 `..`、解析后逃出池根（`filepath.Clean` + `HasPrefix(root+sep)` 双重校验）、符号链接指向池外（`filepath.EvalSymlinks` 后仍须 HasPrefix 池根）。参照 resolveRecyclePath 加固。
2. **只读浏览不出池**：系统盘（/opt/nas、/etc、/root）一律不可见、不可达。池根 = folders.db 所在池挂载点（/data/nas1）。
3. **写操作全审计**：所有非 GET `/api/files/*` 由 main.go loggingMiddleware 自动 LogAudit（username 从 JWT、IP 从 X-Forwarded-For、HTTP≥400 记 failed）。chown/chmod/delete/fix-home-perms 属高危，handler 内补 `common.LogAuditRequest` 富 detail（目标路径 + 参数）。
4. **危险操作确认**：递归 chmod/chown、递归删除、fix-home-perms 前端弹 type-to-confirm（输入目录名/用户名），对齐既有"删池/删卷"确认模式。
5. **chown/chmod 执行方式**：nas-panel 进程以 root 跑（service 无 User=）。
   - chmod：`os.Chmod`/`os.Chown` 系统调用直接生效（root 有 CAP_CHOWN/CAP_FOWNER），**不走 sudo**。
   - 若实现时验证发现某些路径面板非 root（不应发生），回退 `SudoExec chown -R`（sudoers 白名单已含 chown -R；chmod -R 不在白名单，故 chmod 必须 os.Chmod）。
   - **实现首步**：验证 `nas-panel` 进程 uid==0，据此定 chown/chmod 走 os.* 还是 SudoExec。
6. **上传/新建名称校验**：文件名过白名单校验（禁 `/`、`..`、控制字符、超长），防借上传写池外或造非法 inode。
7. **预览限大小**：文本预览限 1MB，图片限 10MB，超出只下载不预览，防内存炸。

---

## 七、前端设计

新增 `files` 导航页（💾 图标，i18n key `nav.files`），布局：

```
┌─ 面包屑: 池根 / public / photos        [用量条 ████░ 1.2T/2T] ─┐
├─ 工具栏: [上传][新建目录][刷新] | 选中N项: [移动][复制][删除][改权限] ─┤
├─ 文件表: ☐ 名称        大小   属主   权限    修改时间   操作      ─┤
│   ☐ 📁 documents      —      fm     drwx------  10-08   ⋮        │
│   ☐ 📄 photo.jpg      2.1M   fm     -rw-------  10-07   ⋮        │
│   ☐ 🔗 link -> ../x   —      fm     lrwxrwxrwx  10-06   ⋮        │
├─ 行内 ⋮ 菜单: 下载/预览/重命名/移动/复制/删除/改权限            ─┤
└─ 上传进度条（v2）/ 大目录 truncated 提示（v1）                 ─┘
```

- **复用既有 CSS 体系**：`.table-dense`（高行数表格，已用于登录日志/事件中心）、`.empty-state`（空目录）、`.clickable-card`、`.badge-*`、CSS 变量（--card-bg-alt 等），不引入新设计语言。
- **状态**：app.js 加 `filesPage` 状态对象（currentPath、entries、selected[]、breadcrumb、usage、uploadProgress）。
- **对话框**：改权限对话框（owner/group 下拉取系统用户/组 + mode 八进制输入 + recursive 勾选 + 预设模板按钮）；移动/复制对话框（目录树选择器，复用 list 端点逐级展开）。
- **i18n**：zh-CN.json 真相源，en-US.json 同步；所有文案走 `$t('files.*')`，不硬编码。
- **预览**：文本用 `<pre>` + 等宽字体；图片用 `<img>` + 懒加载；PDF/视频本期不内嵌预览，只下载。

---

## 八、错误处理

| 场景 | 处理 |
|------|------|
| 路径穿越/池外 | 400 + 审计记 failed，不泄露真实文件系统结构 |
| 文件不存在 | 404 |
| 权限不足（理论上 root 不会，兜底） | 403 + 提示 |
| 同名冲突（上传/移动/复制/重命名） | 409 + `?overwrite=yes` 重试选项 |
| 大目录 truncated | 200 + `truncated:true`，前端提示用 SMB/搜索 |
| 跨 fs 移动中途失败 | copy 成功 delete 失败 → 保留源 + 告警（不静默丢数据）；copy 失败 → 不动源 |
| 删除目标无所属 share | 落池根 .recycle（不报错） |
| 上传磁盘满 | 507 + 清理半成品文件 |
| du 超时（海量小文件） | 用量显示"计算中…"，不阻塞 list |

---

## 九、测试策略

1. **纯函数单测**（必须调真函数，不复刻 if/else）：
   - `resolvePoolPath`：穿越用例（`..`、绝对路径、符号链接逃逸、池外）全拒；合法路径全过。
   - 权限模板：fix-home-perms 生成的 mode/owner 与 EnsureUserHome 一致。
   - 大小/时间格式化、mode 八进制↔符号互转。
2. **API 层**：`httptest` + tmpdir 模拟池（建 public + 2 个 home），逐端点测：list 排序/分页、download Range、zip 流完整性、upload 流式 + 冲突、move 同 fs rename、delete 进 .recycle 且回收站页可见、chmod/chown 生效。
3. **审计验证**：写操作后查 audit_log 落库（包级持久目录 + TestMain 清理，不用 t.TempDir()，参照 skill 审计测试坑）。
4. **生产同构机实测**（Debian 13，单测绿只算一半）：建 2 用户 + home → 管理员视角浏览/上传/下载/移动/删除/改权限全链路 → chown 后 SMB 端 smbclient 实测读写行为正确 → 删除后回收站页 restore 成功 → 路径穿越攻击用例（`?path=../../etc/passwd`）被拒。
5. **提交前**：`go build ./...` + `go test ./modules/filemgmt/ ./modules/diskmgmt/ -count=1`。

---

## 十、分期里程碑

- **M1（v1 只读）**：path.go（安全件，先写 + 单测）→ browse.go → transfer.go（download/zip）→ preview.go → 前端 files 页骨架 + 列表 + 下载。验收：管理员能浏览整池、下载文件/目录。
- **M2（v2 写）**：mutate.go（mkdir/rename/move/copy/delete）→ 前端工具栏 + 上传进度 + 删除确认。验收：能整理文件，删除进回收站且回收站页联动。
- **M3（v3 权限）**：perms.go（stat/chmod/chown/fix-home-perms）→ 前端改权限对话框 + 批量选择。验收：能修复权限错乱，fix-home-perms 一键模板生效。

每期独立 commit + 可发版（beta.N+1）。M1 完成即可对外宣布"面板文件管理上线，FileBrowser 进入淘汰"。

---

## 十一、FileBrowser 淘汰

- M1 发版：面板导航移除 FileBrowser（8081）入口链接；setup.sh 仍安装 FileBrowser（兼容期，老用户习惯）。
- M2 发版：setup.sh 默认不装 FileBrowser（新装用户无此服务）；已装的保留。
- M3 发版后一个版本周期：cleanup.sh / 升级脚本移除 FileBrowser 服务 + 卸载提示。
- FileBrowser 的 BoltDB 账号库与面板用户体系是两张皮，淘汰后消除一处权限盲区（§八 P1 全局协议旁路的 FileBrowser 部分）。

---

## 十二、复用点清单（实现时直接调，不重造）

| 需求 | 复用 |
|------|------|
| 路径穿越防护 | `diskmgmt/recycle.go` resolveRecyclePath 模式 |
| 共享归属判定 | `diskmgmt` GetAllFolderMeta / enrichSharedFolder（folders.db） |
| 回收站落地 | `diskmgmt/recycle.go` 的 .recycle/<user>/ + keeptree + list/restore |
| 路由注册 | `common.AuthMiddleware` + 模块 RegisterRoutes(mux) 模式 |
| 审计 | main.go loggingMiddleware（非 GET 自动）+ common.LogAuditRequest（富 detail） |
| 危险操作确认 | 既有 type-to-confirm 模式（删池/删卷） |
| 前端表格/空态/徽章 | .table-dense / .empty-state / .badge-* / CSS 变量 |
| i18n | zh-CN.json 真相源 + en-US.json 同步 + $t() |
| 权限模板基准 | EnsurePoolStructure / EnsureUserHome（public 2775 nasusers、home 0700 owner） |
| 进度展示（跨 fs 大目录） | rclone 进度面板 SSE 模式（可选） |

---

## 十三、验收标准（M1-M3 全过才算完）

- [ ] 管理员在面板内可浏览 public + 每个用户 home + 所有纳管共享
- [ ] 下载单文件（含断点续传）+ 目录 zip 流
- [ ] 上传（流式，大文件不撑爆内存）+ 新建目录 + 重命名
- [ ] 移动/复制（同 fs 零拷贝）+ 删除（进 .recycle，回收站页可见可 restore）
- [ ] chown/chmod + fix-home-perms 一键模板，改后 SMB 端行为正确
- [ ] 批量选择操作
- [ ] 所有路径穿越用例被拒（`..`/绝对路径/符号链接逃逸/池外）
- [ ] 所有写操作审计落库（audit_log 可查）
- [ ] 危险操作 type-to-confirm
- [ ] i18n 中英双语完整（check-i18n.py 过）
- [ ] go build ./... + 定向单测绿 + Debian 13 同构机实测全链路
- [ ] FileBrowser 入口按 §十一 节奏移除

---

## 十四、风险与开放问题

1. **海量小文件目录的 du/list 性能**：du 异步 + list 分页（2000 上限）缓解；极端目录（数万文件）仍可能慢 → 前端提示用 SMB。开放：是否需要服务端缓存目录树（本期不做，YAGNI）。
2. **跨 fs 移动进度展示**：本期单池内为主，跨 fs 兜底用"处理中"不阻塞；若独立模式多池用户多，再上 SSE 进度（复用 rclone 模式）。
3. **chown/chmod 的 sudo 依赖**：实现首步验证 nas-panel uid==0（应然），决定走 os.* 还是 SudoExec；若 sudoers 需补 chown（非 -R），同步改 setup.sh 白名单。
4. **上传大小/磁盘空间**：默认不限大小靠磁盘兜底；是否需要配置上限 + 配额联动（folders.db 有 quota_gb 列）→ 本期不做，留扩展位。
5. **预览安全**：文本/图片预览须防 XSS（文本转义、图片 Content-Type 校验 + 禁 SVG 内联脚本），对齐 KB 项目 XSS 防护栈经验。
6. **与 SMB 回收站同源**：文件管理器删除落 .recycle，SMB `vfs objects=recycle` 也落 .recycle —— 两处写同一份，restore 语义须一致（都按 keeptree rename 回）。实现时验证 SMB 删除的文件能在回收站页看到、文件管理器删除的文件 SMB 端也一致。
