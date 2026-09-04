# 权限模型落地方案：SMB 按用户粒度 + 其他协议诚实标注

> 版本: v1.0
> 日期: 2026-09-04
> 范围: TODO #30（用户与目录权限模型统一）
> 依据: 对方《Z1_Permission_Model_Refactor.md》方向 + 现有代码实测核对
> 状态: 待评审

---

## 一、方向确认

采用对方定调的核心思路，但细节按真实代码落地：

- **SMB 做精**：真正实现「用户 × 文件夹 × 读写粒度」。
- **NFS**：网段级导出，诚实标注「网段级」。
- **FTP**：只做私有目录，从共享文件夹协议开关中移除。
- **WebDAV / S3**：全局服务，诚实标注「全局，所有有凭据用户可访问」。

## 二、对对方方案的 3 处技术修正

这 3 处是落地前必须改的，否则做出来是坏的：

### 2.1 只用一个字段，不是两个

对方方案在 `folders` 表加 `read_users` + `write_users` 两个 JSON 字段。实测现有代码里 `valid_users` 就是逗号分隔的 TEXT（见 `config_sync.go` 的 `FolderMeta`），JSON 数组会引入新的序列化路径。更重要的是：

> Samba 官方手册（smb.conf man page）：
> - `write list`：列表内用户「no matter what the read only option is set to」都获得写权限。
> - `read list`：列表内用户「no matter what the read only option is set to」都不获得写权限。

所以正确的最小实现是：

```
read only = yes        # 默认只读
write list = <写者>     # 写者覆盖默认只读
valid users = <全部有权限者>
```

由此权限判定自洽：

| 用户状态 | 判定 |
|---------|------|
| 在 write_users | 读写 |
| 在 valid_users 但不在 write_users | 只读 |
| 不在 valid_users | 禁止 |

**结论：只需加一个 `write_users TEXT DEFAULT ''` 列，不需要 read_users。** 少一个字段 = 少一类「read/write 列表漂移」的 bug。

### 2.2 对方把「read only 覆盖 write list」写反了

对方方案 §3.4 声称「read only = yes 会覆盖 write list」。实际正相反：`write list` 覆盖 `read only`。正因为如此，上面的「read only = yes + write list」才能工作。对方最终配置碰巧对，但依据的心智模型错，照它写会踩坑。

### 2.3 deny 语义必须落到 valid_users

对方 API 的 deny 分支只从 read/write 列表移除、从不碰 valid_users。实测现有代码 `setSharePermission`（`users/create.go`）已经是「noaccess = 从 valid_users 移除」的正确语义，我们要保留它并继续修复，不要引入对方那个坏的 deny 分支。

## 三、数据模型（最小改动）

现有 `folders` 表（`web/modules/diskmgmt/config_sync.go:43`）：

```
id, name, path(UNIQUE), pool, permission, valid_users,
recycle_bin, samba_share, nfs_export, quota_gb, created_at, updated_at
```

其中：
- `permission` 取值是 `readwrite` / `readonly` / `noaccess`（注意：不是对方文档里的 rw/ro/no）
- `valid_users` 是逗号分隔字符串

**新增一列**：

```sql
ALTER TABLE folders ADD COLUMN write_users TEXT NOT NULL DEFAULT '';
```

不新建关联表、不引入 JSON。`write_users` 与 `valid_users` 保持同一逗号分隔风格。

### 迁移与兼容（幂等，面板启动时执行）

`write_users` 默认为空 = 走旧的文件夹级逻辑（行为完全不变）。迁移时无需回填。唯一的兼容点见 §五的生成逻辑。

## 四、SMB 配置生成逻辑（核心）

改写 `GenerateSambaConfig`（`web/modules/diskmgmt/config_sync.go:196`）。

每个 SambaShare 文件夹的生成规则：

```
valid users = <valid_users，空则兜底 nasUser>
write list  = <write_users，非空才写此行>
read only   = yes         # 有 write_users 时
```

判定逻辑（Go）：

```go
func smbShareParams(m FolderMeta) (writeMode, writeList string) {
    // 按用户粒度：write_users 非空则进入矩阵模式
    if strings.TrimSpace(m.WriteUsers) != "" {
        return "read only = yes", m.WriteUsers
    }
    // 兼容旧数据：文件夹级 permission
    if m.Permission == "readonly" {
        return "read only = yes", ""
    }
    return "writable = yes", "" // readwrite 或 noaccess 都不会到这里（noaccess 已跳过 SambaShare）
}
```

生成段落模板（在现有 force user/group、create mask 基础上加 write list 行）：

```ini
[name]
   path = /data/xxx
   browseable = yes
   read only = yes
   write list = alice charlie
   valid users = alice bob charlie
   create mask = 0775
   directory mask = 0775
   force user = nasUser
   force group = nasUser
```

Samba 语义验证（场景 A：alice 读写、bob 只读、charlie 禁止）：

- valid users = alice bob
- write list = alice
- read only = yes
- alice：在 write list → 写 ✓
- bob：在 valid users、不在 write list、read only=yes → 只读 ✓
- charlie：不在 valid users → 拒绝 ✓

## 五、权限矩阵 API 重写

现有矩阵有两个入口，都要改：

### 5.1 `setSharePermission`（web/modules/users/create.go:286）

当前实现只更新 valid_users（注释已写明「按用户的读写粒度见 TODO #30」）。重写为按 §二的判定更新 `valid_users` + `write_users`：

```go
func setSharePermission(username, folder, perm string) error {
    metas := diskmgmt.GetAllFolderMeta()
    // ... 找到 target ...

    validUsers := splitSet(target.ValidUsers)
    writeUsers := splitSet(target.WriteUsers)

    switch perm {
    case "readwrite":
        validUsers[username] = true
        writeUsers[username] = true
    case "readonly":
        validUsers[username] = true
        delete(writeUsers, username)
    case "noaccess":
        delete(validUsers, username)
        delete(writeUsers, username)   // ← 关键：deny 必须同时从 write_users 移除
    }

    diskmgmt.SyncFolderMeta(target.Name, target.Path, target.Pool, target.Permission,
        joinSet(validUsers), joinSet(writeUsers), target.SambaShare, target.NFSExport,
        target.RecycleBin, target.QuotaGB)
    return diskmgmt.SyncAllConfigs()
}
```

不变式：**write_users ⊆ valid_users**（同步时兜底：join 前先过滤 write 中不在 valid 的用户）。

### 5.2 `getUserFolderPermission`（web/modules/users/matrix.go:134）

现在只解析 valid users + read only。新增解析 `write list`：

```go
// 1. 不在 valid users → noaccess
// 2. 在 write list → readwrite
// 3. 在 valid users 且 read only=yes → readonly
//    （兼容旧数据：read only=no 且无 write list → readwrite）
```

### 5.3 `buildPermissionMatrix`（web/modules/users/matrix.go:55）

保持不变，仍返回 user→folder→permission 三态。前端下拉框已支持 readwrite/readonly/noaccess，无需改交互。

## 六、协议诚实标注

### 6.1 后端：诚实探测 or 移除

`overview.go:293-306` 目前 `isFTPAccessible` / `isWebDAVServed` / `isS3Served` 只判断「服务在跑」就给每个文件夹打 ✓，造成夸大。改法：

- **FTP**：从共享文件夹协议开关移除。FTP 开关移到用户管理页（私有目录开关），`isFTPAccessible` 不再用于共享文件夹 tag。
- **WebDAV / S3**：保留探测结果，但语义改为「全局服务」。文件夹 tag 文案从「DAV✓/S3✓」改为「全局」。
- **NFS**：tag 文案改为「网段级」。

### 6.2 前端 tag（web/frontend/index.html:1243-1265）

协议 tag 颜色规范（对齐对方方案 §6.2）：

| 类型 | 背景 | 文字 | 用途 |
|------|------|------|------|
| 按用户 | #dbeafe | #1e40af | SMB 已配置用户级权限 |
| 网段级 | #f3f4f6 | #6b7280 | NFS |
| 全局 | #fef3c7 | #92400e | WebDAV/S3 |
| 关闭 | #fee2e2 | #991b1b | 协议未启用 |

具体改动：
- `index.html:1243` SMB tag → 保持「按用户」蓝色。
- `index.html:1244` NFS tag → 灰色「网段级」。
- `index.html:1263` DAV tag → 黄色「全局」。
- `index.html:1265` S3 tag → 黄色「全局」。
- `index.html:1264` FTP tag → 删除（FTP 不参与共享文件夹）。
- 协议开关 UI（创建/编辑文件夹表单）移除 FTP 复选框。

## 七、落地任务清单（按序）

### Phase 1：数据模型 + 迁移（后端，约 1 天）

1. `config_sync.go` `folders` 建表语句加 `write_users TEXT NOT NULL DEFAULT ''`。
2. `initConfigDB` 加幂等迁移：检测列不存在则 `ALTER TABLE folders ADD COLUMN write_users TEXT NOT NULL DEFAULT ''`。
3. `FolderMeta` 结构体加 `WriteUsers string`。
4. `GetAllFolderMeta` 的 SELECT 与 Scan 加 write_users。
5. `SyncFolderMeta` 签名加 writeUsers 参数，UPDATE 语句加列。

### Phase 2：SMB 生成逻辑（后端，约 0.5 天）

6. `GenerateSambaConfig` 按 §四重写：有 write_users → `read only = yes` + `write list`；无 → 兼容文件夹级 permission。
7. `parseSambaShares`（`folders.go:307`）加解析 `write list`，供列表页回显。

### Phase 3：矩阵 API（后端，约 0.5 天）

8. `setSharePermission` 按 §5.1 重写（含 deny 修复 + 不变式兜底）。
9. `getUserFolderPermission` 按 §5.2 重写。
10. `SyncFolderMeta` 所有调用点补 write_users 参数（`pending_ops.go:218/225`、`users/create.go:313`）。

### Phase 4：协议诚实标注（前后端，约 1 天）

11. `overview.go` FTP 从共享文件夹探测移除。
12. `index.html` 协议 tag 按 §6.2 改文案/颜色/移除 FTP。
13. 前端协议开关表单移除 FTP。

### Phase 5：测试 + 验证（约 1 天）

14. 单元测试：SMB 生成三场景（A：读写/只读/禁止；B：旧数据全员只读；C：旧数据全员读写）+ deny 不变式 + write_users 兼容。
15. 端到端：Alice 读写、Bob 只读、Charlie 禁止，用 `smbclient` 实测。
16. 验证旧数据升级后行为不变。

## 八、明确不做（本次范围外）

- NFS 按用户权限（协议不支持）。
- FTP 共享文件夹矩阵（vsftpd 架构限制，换 proftpd 是独立项目）。
- WebDAV/S3 按文件夹隔离（需多实例，即 TODO #29，对方也建议不做）。
- 文件属主统一策略（SMB 已用 force user=nasUser，FTP/WebDAV 各自私有/全局，无交叉，暂不处理）。
