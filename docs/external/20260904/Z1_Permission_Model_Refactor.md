# Z1 权限模型重构方案：SMB 做精，其他诚实

> 版本: v1.0  
> 日期: 2026-09-04  
> 状态: 待评审  
> 关联: docs/permission-model-discussion.md, TODO #30, TODO #29

---

## 一、设计原则

### 1.1 核心结论

> **SMB 做精，其他诚实。**

家用 NAS 90% 的文件访问流量走 SMB。与其让所有协议勉强对齐一个做不到的三维矩阵，不如：
- **SMB**：真正实现「用户 × 文件夹 × 读写粒度」
- **NFS**：网段级导出，诚实标注
- **FTP**：只做私有目录，从共享文件夹协议开关中移除
- **WebDAV / S3**：全局服务，诚实标注「所有有凭据用户均可访问」

### 1.2 不做的事（明确边界）

| 不做的事 | 原因 |
|---------|------|
| 让 NFS 支持按用户权限 | NFS 协议本身不支持，改不了 |
| 让 FTP 支持共享文件夹矩阵 | vsftpd 架构限制，换 proftpd 是另一个项目 |
| 让 WebDAV/S3 支持按文件夹隔离 | rclone serve 单实例限制，多实例是 TODO #29 |
| 大改 schema 建关联表 | 1 人副业，最小改动优先 |

---

## 二、数据模型变更

### 2.1 最小改动方案

在现有 `folders` 表上加两个 JSON 字段，不改表结构、不改现有逻辑。

```sql
-- 迁移脚本: web/scripts/migrate-001-permission.sql
ALTER TABLE folders ADD COLUMN read_users TEXT DEFAULT '[]';
ALTER TABLE folders ADD COLUMN write_users TEXT DEFAULT '[]';
```

**字段语义**：

| 字段 | 类型 | 默认值 | 语义 |
|------|------|--------|------|
| `read_users` | JSON 数组 | `[]` | 对该文件夹有**只读**权限的用户列表 |
| `write_users` | JSON 数组 | `[]` | 对该文件夹有**读写**权限的用户列表 |

**权限优先级**：
```
write_users 包含该用户 → 读写
read_users  包含该用户 → 只读
都不包含，但 valid_users 包含 → 按文件夹级 permission 走（兼容旧逻辑）
都不包含，且 valid_users 不包含 → 禁止
```

### 2.2 数据模型示例

```json
{
  "id": "folder-001",
  "name": "shared",
  "path": "/data/shared",
  "permission": "rw",
  "valid_users": ["alice", "bob", "charlie"],
  "read_users": ["bob"],
  "write_users": ["alice", "charlie"],
  "protocols": {
    "smb": true,
    "nfs": true,
    "webdav": true,
    "s3": false
  }
}
```

**解读**：
- `alice` → 读写
- `bob` → 只读（因为他在 `read_users` 里，不在 `write_users` 里）
- `charlie` → 读写
- 如果 `permission = "ro"`，且没有 `read_users`/`write_users` → 所有 valid_users 都只读（兼容旧数据）

### 2.3 兼容旧数据

```sql
-- 迁移时：旧数据的 read_users/write_users 保持 []，走兼容逻辑
-- 前端展示时：如果 read_users/write_users 为空，按 permission 字段统一渲染
```

---

## 三、SMB 配置生成逻辑（核心）

### 3.1 配置模板

```ini
# configs/smb.conf 托管段模板
[{{.Name}}]
path = {{.Path}}
valid users = {{.ValidUsers}}
{{if .ReadList}}read list = {{.ReadList}}{{end}}
{{if .WriteList}}write list = {{.WriteList}}{{end}}
{{if .IsReadOnly}}read only = yes{{else}}read only = no{{end}}
force user = nasUser
force group = nasGroup
create mask = 0664
directory mask = 0775
browseable = yes
```

### 3.2 生成算法

```go
// web/modules/config/smb_generator.go

type SMBShareConfig struct {
    Name        string
    Path        string
    ValidUsers  string   // 空格分隔的用户名
    ReadList    string   // 空格分隔的用户名
    WriteList   string   // 空格分隔的用户名
    IsReadOnly  bool     // 当 ReadList == ValidUsers 且 WriteList 为空时
}

func GenerateSMBShare(folder Folder, allUsers []User) SMBShareConfig {
    validUsers := folder.ValidUsers

    // 1. 确定 write list
    var writeList []string
    if len(folder.WriteUsers) > 0 {
        writeList = folder.WriteUsers
    } else if folder.Permission == "rw" {
        // 兼容旧数据：没有 write_users 时，所有 valid_users 可写
        writeList = validUsers
    }

    // 2. 确定 read list
    var readList []string
    if len(folder.ReadUsers) > 0 {
        readList = folder.ReadUsers
    } else if folder.Permission == "ro" {
        // 兼容旧数据：没有 read_users 时，所有 valid_users 只读
        readList = validUsers
    }

    // 3. 去重：write list 里的人不需要再出现在 read list
    //    Samba 语义：write list 优先级高于 read list
    readList = subtract(readList, writeList)

    // 4. 确定 read only
    //    如果 write list 为空，或 write list 不等于 valid users，
    //    不能简单设 read only = yes，因为 Samba 的 read only 是共享级开关
    //    正确做法：read only = no + write list 限制
    isReadOnly := len(writeList) == 0

    return SMBShareConfig{
        Name:       folder.Name,
        Path:       folder.Path,
        ValidUsers: strings.Join(validUsers, " "),
        ReadList:   strings.Join(readList, " "),
        WriteList:  strings.Join(writeList, " "),
        IsReadOnly: isReadOnly,
    }
}

// 辅助：从 a 中移除 b 的元素
func subtract(a, b []string) []string {
    set := make(map[string]bool)
    for _, x := range b { set[x] = true }
    var result []string
    for _, x := range a {
        if !set[x] { result = append(result, x) }
    }
    return result
}
```

### 3.3 配置生成示例

**场景 A：Alice 读写，Bob 只读，Charlie 禁止**

```json
{
  "valid_users": ["alice", "bob"],
  "read_users": ["bob"],
  "write_users": ["alice"],
  "permission": "rw"
}
```

生成：
```ini
[shared]
path = /data/shared
valid users = alice bob
read list = bob
write list = alice
read only = no
force user = nasUser
force group = nasGroup
create mask = 0664
directory mask = 0775
browseable = yes
```

**Samba 语义验证**：
- `alice`：在 valid users + write list → 读写 ✅
- `bob`：在 valid users + read list → 只读 ✅
- `charlie`：不在 valid users → 禁止 ✅

---

**场景 B：全员只读（兼容旧数据）**

```json
{
  "valid_users": ["alice", "bob"],
  "read_users": [],
  "write_users": [],
  "permission": "ro"
}
```

生成：
```ini
[shared]
path = /data/shared
valid users = alice bob
read list = alice bob
write list =
read only = yes
...
```

---

**场景 C：全员读写（兼容旧数据）**

```json
{
  "valid_users": ["alice", "bob"],
  "read_users": [],
  "write_users": [],
  "permission": "rw"
}
```

生成：
```ini
[shared]
path = /data/shared
valid users = alice bob
read list =
write list = alice bob
read only = no
...
```

---

### 3.4 关键边界：Samba read only 与 write list 的交互

Samba 有一个容易踩的坑：`read only = yes` 会覆盖 `write list`。

**正确做法**：
- 只要有写用户，就设 `read only = no`，用 `write list` 控制谁能写
- 没有写用户时，才设 `read only = yes`

```go
// 修正后的 isReadOnly 逻辑
isReadOnly := len(writeList) == 0 && len(readList) > 0
// 或更精确：
isReadOnly := len(writeList) == 0
```

---

## 四、前端权限矩阵交互

### 4.1 矩阵布局

```
                    shared     photos     backup
                    ─────      ──────     ──────
alice               [读写▼]    [读写▼]    [禁止▼]
bob                 [只读▼]    [禁止▼]    [只读▼]
charlie             [读写▼]    [读写▼]    [读写▼]
```

**下拉选项**：禁止 / 只读 / 读写

### 4.2 前端数据流

```
用户修改单元格 (bob, shared) → 只读
    │
    ▼
前端计算: bob 在 shared 的 read_users 中，不在 write_users 中
    │
    ▼
PUT /api/folders/folder-001/permissions
Body: {
    "user_id": "bob",
    "access": "read"   // read / write / deny
}
    │
    ▼
后端更新 folders.read_users / write_users
    │
    ▼
触发 smb.conf 重生成
    │
    ▼
systemctl reload smbd
```

### 4.3 前端实现（Alpine.js）

```html
<!-- 权限矩阵组件 -->
<div x-data="permissionMatrix()">
    <table class="permission-matrix">
        <thead>
            <tr>
                <th>用户 \ 文件夹</th>
                <template x-for="folder in folders">
                    <th x-text="folder.name"></th>
                </template>
            </tr>
        </thead>
        <tbody>
            <template x-for="user in users">
                <tr>
                    <td x-text="user.name"></td>
                    <template x-for="folder in folders">
                        <td>
                            <select 
                                x-model="matrix[user.id][folder.id]"
                                @change="updatePermission(user.id, folder.id, $event.target.value)">
                                <option value="deny">🚫 禁止</option>
                                <option value="read">👁️ 只读</option>
                                <option value="write">✏️ 读写</option>
                            </select>
                        </td>
                    </template>
                </tr>
            </template>
        </tbody>
    </table>
</div>

<script>
function permissionMatrix() {
    return {
        users: [],
        folders: [],
        matrix: {},

        init() {
            // 加载数据时，把 read_users/write_users 转成矩阵
            this.folders.forEach(folder => {
                this.users.forEach(user => {
                    let access = 'deny';
                    if (folder.write_users.includes(user.id)) {
                        access = 'write';
                    } else if (folder.read_users.includes(user.id)) {
                        access = 'read';
                    } else if (folder.valid_users.includes(user.id)) {
                        // 兼容旧数据：按 folder.permission
                        access = folder.permission === 'rw' ? 'write' : 'read';
                    }
                    this.matrix[user.id] = this.matrix[user.id] || {};
                    this.matrix[user.id][folder.id] = access;
                });
            });
        },

        async updatePermission(userId, folderId, access) {
            await fetch(`/api/folders/${folderId}/permissions`, {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ user_id: userId, access })
            });
            // 成功后提示，后台自动重载 smb
        }
    }
}
</script>
```

---

## 五、API 设计

### 5.1 文件夹权限 API

```
GET    /api/folders/:id/permissions
→ {
    "valid_users": ["alice", "bob", "charlie"],
    "matrix": {
        "alice":   { "access": "write", "source": "write_users" },
        "bob":     { "access": "read",  "source": "read_users" },
        "charlie": { "access": "deny",  "source": "not_in_list" }
    }
}

PUT    /api/folders/:id/permissions
Body: { "user_id": "bob", "access": "read" }
→ 更新 folders.read_users / write_users，触发 smb.conf 重生成

PUT    /api/folders/:id/valid-users
Body: { "users": ["alice", "bob"] }
→ 更新 valid_users，同时清理 read_users/write_users 中不在 valid_users 的用户
```

### 5.2 批量更新（权限矩阵保存）

```
PUT    /api/folders/:id/permissions/batch
Body: {
    "permissions": {
        "alice": "write",
        "bob": "read",
        "charlie": "deny"
    }
}
→ 后端事务：清空 read_users/write_users → 按权限分类写入 → 重生成 smb.conf → reload smbd
```

---

## 六、各协议真实能力与 UI 标注

### 6.1 协议能力矩阵

| 协议 | 按用户白名单 | 按用户读写粒度 | 按文件夹隔离 | 现状 |
|------|:---:|:---:|:---:|------|
| **SMB** | ✅ | ✅（本方案实现） | ✅ | **做精** |
| **NFS** | ❌ | ❌ | ✅（按导出路径） | 网段级，诚实标注 |
| **FTP** | ❌ | ❌ | ❌（一人一 chroot） | 仅私有目录 |
| **WebDAV** | ❌ | ❌ | ❌（全局） | 全局服务 |
| **S3** | ❌ | ❌ | ❌（全局） | 全局服务 |

### 6.2 UI 标注方案

**共享文件夹列表页**：

```
📁 shared
   SMB:  [✅ 按用户]     ← 蓝色，hover 显示具体权限
   NFS:  [🌐 网段级]     ← 灰色
   FTP:  [—]             ← 不显示（FTP 不参与共享）
   DAV:  [🌐 全局]       ← 灰色，tooltip: "所有有凭据用户可访问"
   S3:   [🌐 全局]       ← 灰色，tooltip: "所有有凭据用户可访问"
```

**协议 tag 颜色规范**：

| 类型 | 背景 | 文字 | 用途 |
|------|------|------|------|
| 按用户 | `#dbeafe` | `#1e40af` | SMB 已配置用户级权限 |
| 网段级 | `#f3f4f6` | `#6b7280` | NFS |
| 全局 | `#fef3c7` | `#92400e` | WebDAV/S3，所有用户统一暴露 |
| 关闭 | `#fee2e2` | `#991b1b` | 协议未启用 |

### 6.3 FTP 私有目录

从共享文件夹协议开关中移除 FTP，在用户管理页新增：

```
用户: bob
├── 基本信息
├── 密码
├── 共享文件夹权限 (SMB 矩阵)
└── FTP 私有目录 [✅ 启用]
    访问路径: ftp://NAS_IP/ → 进入 /data/private/bob
```

vsftpd 配置保持现状：
```ini
# configs/vsftpd.conf
local_root=/data/private/$USER
chroot_local_user=YES
```

---

## 七、文件属主统一策略

### 7.1 问题

如果 SMB 用 `force user = nasUser`，而 FTP 用真实用户写入，同一文件夹内文件属主会不一致，导致权限混乱。

### 7.2 解决方案

**所有协议统一用同一个 uid/gid 写入**：

```ini
# smb.conf 全局
force user = nasUser
force group = nasGroup
create mask = 0664
directory mask = 0775
```

```bash
# FTP 虚拟用户映射（如果将来做）
# /etc/vsftpd/virtual-user-map
# bob → nasUser

# 当前方案：FTP 只用于私有目录，不涉及共享文件夹，天然隔离
```

```bash
# WebDAV / S3 用 rclone 时，确保运行用户是 nasUser
# nas-panel.service 中:
User=nasUser
Group=nasGroup
```

**结果**：
- SMB 写的文件 → `nasUser:nasGroup 0664`
- WebDAV/S3 写的文件 → `nasUser:nasGroup 0664`
- FTP 私有目录 → 各用户自己的目录，互不干扰

---

## 八、迁移与兼容性

### 8.1 数据库迁移

```sql
-- web/scripts/migrate-001-permission.sql
-- 在 setup.sh 或面板启动时自动执行

-- 1. 加字段
ALTER TABLE folders ADD COLUMN read_users TEXT DEFAULT '[]';
ALTER TABLE folders ADD COLUMN write_users TEXT DEFAULT '[]';

-- 2. 初始化：旧数据保持 []，走兼容逻辑
--    不需要回填，GenerateSMBShare 会处理

-- 3. 记录迁移版本
INSERT INTO schema_migrations (version, applied_at) VALUES ('001', datetime('now'));
```

### 8.2 配置迁移

```bash
# setup.sh 或面板启动时
# 1. 检测是否有旧版 smb.conf（无 read list / write list）
# 2. 如果有，触发一次全量配置重生成
# 3. 重载 smbd
```

### 8.3 回滚策略

```sql
-- 如果出问题，回滚很简单：
ALTER TABLE folders DROP COLUMN read_users;
ALTER TABLE folders DROP COLUMN write_users;
-- 然后重生成 smb.conf，回到旧逻辑
```

---

## 九、实现 Checklist

### Phase 1: 诚实标注（1 天）

- [ ] 前端：共享文件夹列表页，FTP 协议 tag 移除
- [ ] 前端：NFS/WebDAV/S3 tag 改为「网段级」/「全局」样式
- [ ] 前端：hover tooltip 说明协议真实能力
- [ ] 前端：用户管理页新增「FTP 私有目录」开关
- [ ] 文档：更新用户手册，说明各协议权限边界

### Phase 2: 数据模型（1 天）

- [ ] 后端：数据库迁移脚本 `migrate-001-permission.sql`
- [ ] 后端：Folder 结构体加 `ReadUsers` / `WriteUsers` 字段
- [ ] 后端：API `PUT /api/folders/:id/permissions`
- [ ] 后端：API `PUT /api/folders/:id/permissions/batch`
- [ ] 后端：保存权限时自动清理（read/write 用户必须在 valid_users 中）

### Phase 3: SMB 生成逻辑（1-2 天）

- [ ] 后端：重写 `GenerateSMBShare`，接入 read list / write list
- [ ] 后端：smb.conf 模板更新
- [ ] 后端：单元测试（场景 A/B/C + 边界情况）
- [ ] 部署：setup.sh 加迁移执行步骤

### Phase 4: 前端矩阵（1-2 天）

- [ ] 前端：权限矩阵组件（Alpine.js）
- [ ] 前端：矩阵初始化（从 read_users/write_users 渲染）
- [ ] 前端：批量保存 + 加载状态
- [ ] 前端：保存后提示"SMB 配置已更新"

### Phase 5: 验证（1 天）

- [ ] 测试：Alice 读写、Bob 只读、Charlie 禁止，SMB 验证
- [ ] 测试：旧数据升级后行为不变
- [ ] 测试：FTP 私有目录不受影响
- [ ] 测试：WebDAV/S3 全局访问正常

**总工期：5-7 天**

---

## 十、FAQ

**Q: 为什么不做成关联表（user_folder_permissions）？**

A: 1 人副业，最小改动优先。JSON 字段足够表达「用户列表」这种简单关系，且 SQLite 的 JSON 支持很好。将来如果需要更复杂的权限（如用户组、继承），再迁移到关联表。

**Q: NFS 真的完全不能按用户吗？**

A: NFSv3/v4 的认证基于 UID/GID，不是用户名。可以配合 LDAP/NIS 做用户映射，但那是企业级方案，远超家用 NAS 范畴。诚实标注「网段级」是最务实的做法。

**Q: 如果用户强烈要求 FTP 共享文件夹怎么办？**

A: 建议用户用 SMB 替代。如果坚持，可以评估 proftpd + mod_auth_file，但这是另一个独立项目，不在本方案范围内。

**Q: WebDAV/S3 多实例（TODO #29）做了之后，权限模型怎么升级？**

A: 本方案为 WebDAV/S3 预留了扩展空间：
- 当前：全局服务，所有文件夹统一暴露
- 将来多实例：每个实例独立凭据，可以在文件夹上加 `webdav_users` / `s3_users` 字段，复用同一套 JSON 数组模式

---

*文档结束*
