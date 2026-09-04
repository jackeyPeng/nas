# Z1 权限模型 + 版本更新 落地实施手册

> 版本: v1.0  
> 日期: 2026-09-04  
> 适用: 1 人副业，每天 2-4 小时投入  
> 总工期: 权限模型 5 天 + 版本更新 2 天 = 7 天

---

## 第一部分：权限模型重构（5 天）

### Day 1：数据模型 + 迁移（2-3 小时）

**目标**：改 schema，加字段，确保旧数据兼容

#### 1.1 改数据库模型

文件：`web/modules/diskmgmt/model.go`（或你存 Folder 结构体的地方）

```go
type Folder struct {
    ID          string            `json:"id"`
    Name        string            `json:"name"`
    Path        string            `json:"path"`
    PoolID      string            `json:"pool_id"`
    VolumeID    string            `json:"volume_id"`
    Permission  string            `json:"permission"`   // rw/ro/no — 兼容旧数据
    ValidUsers  []string          `json:"valid_users"`
    ReadUsers   []string          `json:"read_users"`    // 🆕
    WriteUsers  []string          `json:"write_users"`   // 🆕
    Protocols   map[string]bool   `json:"protocols"`
    Quota       uint64            `json:"quota"`
    RecycleBin  bool              `json:"recycle_bin"`
    CreatedAt   time.Time         `json:"created_at"`
}
```

#### 1.2 写迁移脚本

文件：`web/scripts/migrate-002-permission.sql`

```sql
-- 检查是否已迁移
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 本次迁移
INSERT OR IGNORE INTO schema_migrations (version) VALUES (2);

-- 加字段（SQLite 不支持 ALTER TABLE ADD COLUMN 带默认值，需要重建表）
-- 但 SQLite 3.35.0+ 支持，Debian 13 的 sqlite3 应该够新
ALTER TABLE folders ADD COLUMN read_users TEXT DEFAULT '[]';
ALTER TABLE folders ADD COLUMN write_users TEXT DEFAULT '[]';
```

**注意**：如果 SQLite 版本太老不支持 `ALTER TABLE ADD COLUMN`，用重建表方案：

```sql
-- 老版本 SQLite 兼容方案
BEGIN TRANSACTION;
CREATE TABLE folders_new (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    path TEXT NOT NULL,
    pool_id TEXT,
    volume_id TEXT,
    permission TEXT DEFAULT 'rw',
    valid_users TEXT DEFAULT '[]',
    read_users TEXT DEFAULT '[]',
    write_users TEXT DEFAULT '[]',
    protocols TEXT DEFAULT '{}',
    quota INTEGER DEFAULT 0,
    recycle_bin INTEGER DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO folders_new SELECT id, name, path, pool_id, volume_id, permission, valid_users, '[]', '[]', protocols, quota, recycle_bin, created_at FROM folders;
DROP TABLE folders;
ALTER TABLE folders_new RENAME TO folders;
COMMIT;
```

#### 1.3 在应用启动时自动执行迁移

文件：`web/main.go` 或 `web/common/db.go`

```go
func initDB(dbPath string) (*sql.DB, error) {
    db, err := sql.Open("sqlite3", dbPath)
    if err != nil { return nil, err }

    // 执行迁移
    if err := runMigrations(db); err != nil {
        return nil, fmt.Errorf("migration failed: %w", err)
    }

    return db, nil
}

func runMigrations(db *sql.DB) error {
    // 读取 migrations/ 目录下所有 .sql 文件，按版本号顺序执行
    // 已执行的版本记录在 schema_migrations 表中
    // 简单实现：直接执行 migrate-002-permission.sql
    _, err := db.Exec(`ALTER TABLE folders ADD COLUMN read_users TEXT DEFAULT '[]'`)
    if err != nil && !strings.Contains(err.Error(), "duplicate column") {
        return err
    }
    _, err = db.Exec(`ALTER TABLE folders ADD COLUMN write_users TEXT DEFAULT '[]'`)
    if err != nil && !strings.Contains(err.Error(), "duplicate column") {
        return err
    }
    return nil
}
```

**Day 1 验收标准**：
- [ ] `read_users` / `write_users` 字段已加到数据库
- [ ] 旧数据启动不报错
- [ ] 新字段默认 `[]`，不影响现有功能

---

### Day 2：SMB 配置生成逻辑（3-4 小时）

**目标**：让 smb.conf 能根据 read_users / write_users 生成 `read list` / `write list`

#### 2.1 找到现有 SMB 配置生成代码

根据项目结构，应该在 `web/modules/config/` 或 `web/modules/diskmgmt/` 下。找到生成 smb.conf 托管段的函数（可能是 `GenerateSMBConfig` 或类似名字）。

#### 2.2 修改生成逻辑

文件：`web/modules/config/smb_generator.go`（假设路径）

```go
package config

import (
    "fmt"
    "strings"
)

// SMBShare 单个共享配置
type SMBShare struct {
    Name       string
    Path       string
    ValidUsers string  // 空格分隔
    ReadList   string  // 空格分隔
    WriteList  string  // 空格分隔
    ReadOnly   string  // "yes" or "no"
}

// BuildSMBShare 从 Folder 生成 SMB 共享配置
func BuildSMBShare(folder Folder) SMBShare {
    validUsers := strings.Join(folder.ValidUsers, " ")

    var readList, writeList []string

    // 如果新字段有值，用新字段
    if len(folder.ReadUsers) > 0 || len(folder.WriteUsers) > 0 {
        readList = folder.ReadUsers
        writeList = folder.WriteUsers
    } else {
        // 兼容旧数据：按 permission 字段统一设置
        if folder.Permission == "ro" {
            readList = folder.ValidUsers
        } else if folder.Permission == "rw" {
            writeList = folder.ValidUsers
        }
    }

    // 去重：write list 里的人不需要在 read list 里
    writeSet := make(map[string]bool)
    for _, u := range writeList { writeSet[u] = true }

    var dedupedRead []string
    for _, u := range readList {
        if !writeSet[u] {
            dedupedRead = append(dedupedRead, u)
        }
    }

    // 确定 read only
    // 只要有 write list 的人，就设 read only = no，用 write list 控制
    readOnly := "no"
    if len(writeList) == 0 && len(dedupedRead) > 0 {
        readOnly = "yes"
    }

    return SMBShare{
        Name:       folder.Name,
        Path:       folder.Path,
        ValidUsers: validUsers,
        ReadList:   strings.Join(dedupedRead, " "),
        WriteList:  strings.Join(writeList, " "),
        ReadOnly:   readOnly,
    }
}

// Render 渲染为 smb.conf 段落
func (s SMBShare) Render() string {
    var b strings.Builder
    b.WriteString(fmt.Sprintf("[%s]
", s.Name))
    b.WriteString(fmt.Sprintf("path = %s
", s.Path))
    if s.ValidUsers != "" {
        b.WriteString(fmt.Sprintf("valid users = %s
", s.ValidUsers))
    }
    if s.ReadList != "" {
        b.WriteString(fmt.Sprintf("read list = %s
", s.ReadList))
    }
    if s.WriteList != "" {
        b.WriteString(fmt.Sprintf("write list = %s
", s.WriteList))
    }
    b.WriteString(fmt.Sprintf("read only = %s
", s.ReadOnly))
    b.WriteString("force user = nasUser
")
    b.WriteString("force group = nasGroup
")
    b.WriteString("create mask = 0664
")
    b.WriteString("directory mask = 0775
")
    b.WriteString("browseable = yes
")
    if s.Name != "global" {
        b.WriteString("vfs objects = recycle
")
        b.WriteString("recycle:repository = .recycle/%U
")
        b.WriteString("recycle:keeptree = yes
")
        b.WriteString("recycle:versions = yes
")
    }
    b.WriteString("
")
    return b.String()
}
```

#### 2.3 关键边界测试

在 `smb_generator_test.go` 里写三个场景：

```go
package config

import "testing"

func TestBuildSMBShare(t *testing.T) {
    // 场景 A：Alice 读写，Bob 只读，Charlie 禁止
    folderA := Folder{
        Name:       "shared",
        Path:       "/data/shared",
        ValidUsers: []string{"alice", "bob"},
        ReadUsers:  []string{"bob"},
        WriteUsers: []string{"alice"},
        Permission: "rw",
    }
    shareA := BuildSMBShare(folderA)
    if shareA.ReadList != "bob" { t.Errorf("read list = %s, want bob", shareA.ReadList) }
    if shareA.WriteList != "alice" { t.Errorf("write list = %s, want alice", shareA.WriteList) }
    if shareA.ReadOnly != "no" { t.Errorf("read only = %s, want no", shareA.ReadOnly) }

    // 场景 B：全员只读（旧数据兼容）
    folderB := Folder{
        Name:       "photos",
        Path:       "/data/photos",
        ValidUsers: []string{"alice", "bob"},
        Permission: "ro",
    }
    shareB := BuildSMBShare(folderB)
    if shareB.ReadList != "alice bob" { t.Errorf("read list = %s", shareB.ReadList) }
    if shareB.WriteList != "" { t.Errorf("write list should be empty") }
    if shareB.ReadOnly != "yes" { t.Errorf("read only = %s, want yes", shareB.ReadOnly) }

    // 场景 C：全员读写（旧数据兼容）
    folderC := Folder{
        Name:       "backup",
        Path:       "/data/backup",
        ValidUsers: []string{"alice", "bob"},
        Permission: "rw",
    }
    shareC := BuildSMBShare(folderC)
    if shareC.ReadList != "" { t.Errorf("read list should be empty") }
    if shareC.WriteList != "alice bob" { t.Errorf("write list = %s", shareC.WriteList) }
    if shareC.ReadOnly != "no" { t.Errorf("read only = %s, want no", shareC.ReadOnly) }
}
```

**Day 2 验收标准**：
- [ ] `go test ./...` 三个场景全部通过
- [ ] 手动验证：创建共享 → 改权限 → 看 smb.conf 是否正确生成

---

### Day 3：后端 API（3-4 小时）

**目标**：提供权限读写接口，保存时触发 smb.conf 重生成

#### 3.1 权限更新 API

文件：`web/modules/diskmgmt/folder_permission.go`（新建）

```go
package diskmgmt

import (
    "encoding/json"
    "net/http"
    "strings"

    "nas/web/common"
)

// PermissionRequest 更新请求
type PermissionRequest struct {
    UserID string `json:"user_id"`
    Access string `json:"access"` // "read" / "write" / "deny"
}

// UpdateFolderPermission 更新单个用户的权限
func (h *Handler) UpdateFolderPermission(w http.ResponseWriter, r *http.Request) {
    folderID := r.URL.Path[len("/api/disk/folders/"):]
    folderID = strings.TrimSuffix(folderID, "/permissions")

    var req PermissionRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        common.JSONError(w, "invalid request", http.StatusBadRequest)
        return
    }

    folder, err := h.db.GetFolder(folderID)
    if err != nil {
        common.JSONError(w, "folder not found", http.StatusNotFound)
        return
    }

    // 校验 user_id 是否在 valid_users 中
    valid := false
    for _, u := range folder.ValidUsers {
        if u == req.UserID { valid = true; break }
    }
    if !valid {
        common.JSONError(w, "user not in valid_users", http.StatusBadRequest)
        return
    }

    // 更新 read_users / write_users
    switch req.Access {
    case "write":
        folder.WriteUsers = addUnique(folder.WriteUsers, req.UserID)
        folder.ReadUsers = removeFrom(folder.ReadUsers, req.UserID)
    case "read":
        folder.ReadUsers = addUnique(folder.ReadUsers, req.UserID)
        folder.WriteUsers = removeFrom(folder.WriteUsers, req.UserID)
    case "deny":
        folder.ReadUsers = removeFrom(folder.ReadUsers, req.UserID)
        folder.WriteUsers = removeFrom(folder.WriteUsers, req.UserID)
    default:
        common.JSONError(w, "invalid access", http.StatusBadRequest)
        return
    }

    // 保存到数据库
    if err := h.db.UpdateFolder(folder); err != nil {
        common.JSONError(w, err.Error(), http.StatusInternalServerError)
        return
    }

    // 触发 smb.conf 重生成
    if err := h.regenerateSMBConfig(); err != nil {
        common.JSONError(w, "failed to regenerate smb config", http.StatusInternalServerError)
        return
    }

    // 重载 smbd
    if err := common.SudoExec("systemctl", "reload", "smbd"); err != nil {
        // 记录日志但不返回错误，配置已保存
        log.Printf("reload smbd failed: %v", err)
    }

    common.JSONSuccess(w, "permission updated")
}

// BatchUpdatePermissions 批量更新（权限矩阵保存用）
func (h *Handler) BatchUpdatePermissions(w http.ResponseWriter, r *http.Request) {
    folderID := r.URL.Path[len("/api/disk/folders/"):]
    folderID = strings.TrimSuffix(folderID, "/permissions/batch")

    var req struct {
        Permissions map[string]string `json:"permissions"` // user_id -> access
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        common.JSONError(w, "invalid request", http.StatusBadRequest)
        return
    }

    folder, err := h.db.GetFolder(folderID)
    if err != nil {
        common.JSONError(w, "folder not found", http.StatusNotFound)
        return
    }

    // 清空旧权限
    folder.ReadUsers = []string{}
    folder.WriteUsers = []string{}

    for userID, access := range req.Permissions {
        switch access {
        case "write":
            folder.WriteUsers = append(folder.WriteUsers, userID)
        case "read":
            folder.ReadUsers = append(folder.ReadUsers, userID)
        }
    }

    if err := h.db.UpdateFolder(folder); err != nil {
        common.JSONError(w, err.Error(), http.StatusInternalServerError)
        return
    }

    if err := h.regenerateSMBConfig(); err != nil {
        common.JSONError(w, "failed to regenerate smb config", http.StatusInternalServerError)
        return
    }

    common.SudoExec("systemctl", "reload", "smbd")
    common.JSONSuccess(w, "permissions updated")
}

// 辅助函数
func addUnique(slice []string, item string) []string {
    for _, s := range slice {
        if s == item { return slice }
    }
    return append(slice, item)
}

func removeFrom(slice []string, item string) []string {
    var result []string
    for _, s := range slice {
        if s != item { result = append(result, s) }
    }
    return result
}
```

#### 3.2 路由注册

在 `web/modules/diskmgmt/` 的路由注册处加：

```go
mux.HandleFunc("/api/disk/folders/", h.handleFolders) // 现有
// 新增：
mux.HandleFunc("/api/disk/folders/", h.handleFolderPermissions) // 需要更精确匹配
// 或者用前缀路由
```

**注意**：Go 1.22+ 支持路径参数和 HTTP 方法路由，如果项目用的 Go 版本支持，可以：

```go
mux.HandleFunc("PUT /api/disk/folders/{id}/permissions", h.UpdateFolderPermission)
mux.HandleFunc("PUT /api/disk/folders/{id}/permissions/batch", h.BatchUpdatePermissions)
```

如果不支持，用字符串解析：

```go
func (h *Handler) handleFolderPermissions(w http.ResponseWriter, r *http.Request) {
    if r.Method != "PUT" {
        common.JSONError(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }
    path := r.URL.Path
    if strings.HasSuffix(path, "/permissions/batch") {
        h.BatchUpdatePermissions(w, r)
    } else if strings.HasSuffix(path, "/permissions") {
        h.UpdateFolderPermission(w, r)
    }
}
```

**Day 3 验收标准**：
- [ ] `PUT /api/disk/folders/:id/permissions` 可用 curl 测试通过
- [ ] 保存后 smb.conf 正确更新
- [ ] `systemctl reload smbd` 成功

---

### Day 4：前端权限矩阵（3-4 小时）

**目标**：把用户管理页的权限矩阵从"摆设"变成"真能用"

#### 4.1 找到现有权限矩阵代码

应该在 `web/frontend/app.js` 或用户管理相关的模块里。找到渲染权限矩阵的 Alpine.js 组件。

#### 4.2 修改数据流

```javascript
// 权限矩阵组件（假设在 app.js 的用户管理部分）
permissionMatrix() {
    return {
        users: [],
        folders: [],
        matrix: {}, // { userId: { folderId: 'write'|'read'|'deny' } }
        loading: false,

        async init() {
            // 加载用户和文件夹
            const [usersRes, foldersRes] = await Promise.all([
                fetch('/api/users').then(r => r.json()),
                fetch('/api/disk/folders').then(r => r.json())
            ]);
            this.users = usersRes.data;
            this.folders = foldersRes.data;

            // 初始化矩阵：从 folder.read_users / write_users 解析
            this.folders.forEach(folder => {
                this.users.forEach(user => {
                    let access = 'deny';
                    if (folder.write_users && folder.write_users.includes(user.id)) {
                        access = 'write';
                    } else if (folder.read_users && folder.read_users.includes(user.id)) {
                        access = 'read';
                    } else if (folder.valid_users && folder.valid_users.includes(user.id)) {
                        // 兼容旧数据
                        access = folder.permission === 'rw' ? 'write' : 'read';
                    }
                    if (!this.matrix[user.id]) this.matrix[user.id] = {};
                    this.matrix[user.id][folder.id] = access;
                });
            });
        },

        getAccessIcon(access) {
            return {
                'write': '✏️',
                'read': '👁️',
                'deny': '🚫'
            }[access] || '?';
        },

        getAccessLabel(access) {
            return {
                'write': this.t('permission_write') || '读写',
                'read': this.t('permission_read') || '只读',
                'deny': this.t('permission_deny') || '禁止'
            }[access];
        },

        async updatePermission(userId, folderId, access) {
            this.loading = true;
            try {
                const res = await fetch(`/api/disk/folders/${folderId}/permissions`, {
                    method: 'PUT',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ user_id: userId, access })
                });
                const data = await res.json();
                if (data.success) {
                    this.matrix[userId][folderId] = access;
                    this.toast(this.t('permission_updated') || '权限已更新');
                } else {
                    this.toast(data.message || '更新失败', 'error');
                }
            } finally {
                this.loading = false;
            }
        },

        cycleAccess(userId, folderId) {
            const current = this.matrix[userId][folderId];
            const next = { 'deny': 'read', 'read': 'write', 'write': 'deny' }[current] || 'deny';
            this.updatePermission(userId, folderId, next);
        }
    }
}
```

#### 4.3 修改矩阵 UI

```html
<!-- 权限矩阵表格 -->
<div x-data="permissionMatrix()" x-init="init()">
    <table class="permission-matrix">
        <thead>
            <tr>
                <th x-text="t('user')">用户</th>
                <template x-for="folder in folders" :key="folder.id">
                    <th x-text="folder.name"></th>
                </template>
            </tr>
        </thead>
        <tbody>
            <template x-for="user in users" :key="user.id">
                <tr>
                    <td x-text="user.name"></td>
                    <template x-for="folder in folders" :key="folder.id">
                        <td>
                            <button 
                                @click="cycleAccess(user.id, folder.id)"
                                :disabled="loading"
                                :class="{
                                    'perm-write': matrix[user.id][folder.id] === 'write',
                                    'perm-read': matrix[user.id][folder.id] === 'read',
                                    'perm-deny': matrix[user.id][folder.id] === 'deny'
                                }"
                                class="perm-btn">
                                <span x-text="getAccessIcon(matrix[user.id][folder.id])"></span>
                                <span x-text="getAccessLabel(matrix[user.id][folder.id])"></span>
                            </button>
                        </td>
                    </template>
                </tr>
            </template>
        </tbody>
    </table>
</div>
```

```css
/* 权限按钮样式 */
.perm-btn {
    padding: 4px 10px;
    border-radius: 6px;
    border: 1px solid #e5e7eb;
    font-size: 12px;
    cursor: pointer;
    transition: all 0.2s;
    min-width: 70px;
}
.perm-btn:hover { transform: scale(1.05); }
.perm-write { background: #dcfce7; color: #166534; border-color: #86efac; }
.perm-read  { background: #dbeafe; color: #1e40af; border-color: #93c5fd; }
.perm-deny  { background: #fee2e2; color: #991b1b; border-color: #fca5a5; }
```

**Day 4 验收标准**：
- [ ] 权限矩阵正确显示当前权限状态
- [ ] 点击单元格循环切换（禁止→只读→读写→禁止）
- [ ] 切换后调用 API，smb.conf 更新
- [ ] 有 loading 状态防止重复点击

---

### Day 5：诚实标注 + 集成测试（2-3 小时）

**目标**：修复协议徽标，做端到端验证

#### 5.1 修改共享文件夹列表页的协议 tag

找到渲染共享文件夹列表的前端代码，修改协议 tag 逻辑：

```javascript
// 协议 tag 渲染
getProtocolTags(folder) {
    const tags = [];

    // SMB：按用户
    if (folder.protocols.smb) {
        tags.push({ name: 'SMB', type: 'per-user', tooltip: '按用户权限控制' });
    }

    // NFS：网段级
    if (folder.protocols.nfs) {
        tags.push({ name: 'NFS', type: 'network', tooltip: '网段级访问，无用户隔离' });
    }

    // WebDAV：全局
    if (folder.protocols.webdav) {
        tags.push({ name: 'WebDAV', type: 'global', tooltip: '全局服务，所有有凭据用户可访问' });
    }

    // S3：全局
    if (folder.protocols.s3) {
        tags.push({ name: 'S3', type: 'global', tooltip: '全局服务，所有有凭据用户可访问' });
    }

    // FTP：不在共享文件夹显示

    return tags;
}
```

```css
.tag-per-user { background: #dbeafe; color: #1e40af; }
.tag-network  { background: #f3f4f6; color: #6b7280; }
.tag-global   { background: #fef3c7; color: #92400e; }
```

#### 5.2 从共享文件夹协议开关移除 FTP

找到创建/编辑共享文件夹的表单，把 FTP 选项移除。FTP 私有目录的开关放到用户管理页。

#### 5.3 端到端测试清单

```bash
# 1. 创建测试用户
sudo /opt/nas/scripts/add-user.sh alice Alice123456
sudo /opt/nas/scripts/add-user.sh bob Bob123456

# 2. 创建共享文件夹
# 通过 Web 面板创建 /data/test-perm，valid_users = alice, bob

# 3. 设置权限：alice 读写，bob 只读
# 通过权限矩阵点击设置

# 4. 验证 smb.conf
cat /etc/samba/smb.conf | grep -A 20 "\[test-perm\]"
# 应该看到：
# [test-perm]
# path = /data/test-perm
# valid users = alice bob
# read list = bob
# write list = alice
# read only = no

# 5. 验证 Samba 行为
# 从 Windows/Mac 用 alice 登录 → 能创建文件
# 从 Windows/Mac 用 bob 登录 → 能看不能写

# 6. 验证 WebDAV 仍为全局
# 访问 http://NAS_IP:8080/ → 用 alice 或 bob 都能访问 test-perm
```

**Day 5 验收标准**：
- [ ] SMB 按用户权限工作正常
- [ ] WebDAV/S3 徽标显示为「全局」
- [ ] FTP 从共享文件夹协议开关移除
- [ ] 旧数据升级后行为不变

---

## 第二部分：版本更新功能（2 天）

### Day 6：后端升级逻辑（3-4 小时）

**目标**：实现一键检查更新 + 下载 + 替换 + 重启 + 回滚

#### 6.1 版本号嵌入

修改 `Makefile`：

```makefile
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS := -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)

build:
	cd web && go build -ldflags "$(LDFLAGS)" -o nas-panel .

dev:
	cd web && go run -ldflags "$(LDFLAGS)" .
```

修改 `web/main.go`：

```go
package main

import (
    "fmt"
    "net/http"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "time"
)

var (
    Version   = "dev"
    BuildTime = "unknown"
)

type VersionInfo struct {
    Current   string `json:"current"`
    BuildTime string `json:"build_time"`
    Latest    string `json:"latest"`
    HasUpdate bool   `json:"has_update"`
    DownloadURL string `json:"download_url,omitempty"`
}

func versionHandler(w http.ResponseWriter, r *http.Request) {
    latest, url, err := checkLatestVersion()
    if err != nil {
        // 检查失败不影响返回当前版本
        latest = "unknown"
    }

    common.JSONResponse(w, VersionInfo{
        Current:     Version,
        BuildTime:   BuildTime,
        Latest:      latest,
        HasUpdate:   latest != "unknown" && latest != Version && !isDevVersion(Version),
        DownloadURL: url,
    })
}

func isDevVersion(v string) bool {
    return v == "dev" || v == "" || len(v) < 5
}

func checkLatestVersion() (string, string, error) {
    // 从 file.abwen.com 或 Gitee Release 获取最新版本
    // 简单实现：读取一个文本文件的版本号
    resp, err := http.Get("https://file.abwen.com/control/nas-panel.version")
    if err != nil {
        return "", "", err
    }
    defer resp.Body.Close()

    var version string
    fmt.Fscanf(resp.Body, "%s", &version)

    url := fmt.Sprintf("https://file.abwen.com/control/nas-panel-%s-%s",
        runtime.GOOS, runtime.GOARCH)
    return version, url, nil
}
```

#### 6.2 升级 API

```go
// 升级任务状态
type UpgradeTask struct {
    ID        string    `json:"id"`
    Status    string    `json:"status"`   // pending/downloading/verifying/replacing/restarting/done/failed/rolledback
    Progress  int       `json:"progress"` // 0-100
    Message   string    `json:"message"`
    NewVersion string   `json:"new_version"`
    StartedAt time.Time `json:"started_at"`
}

var upgradeTask *UpgradeTask

func upgradeHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != "POST" {
        common.JSONError(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }

    // 防止重复升级
    if upgradeTask != nil && upgradeTask.Status == "downloading" {
        common.JSONError(w, "upgrade in progress", http.StatusConflict)
        return
    }

    // 异步执行升级
    go doUpgrade()

    common.JSONResponse(w, map[string]string{"status": "started"})
}

func doUpgrade() {
    task := &UpgradeTask{
        ID:        fmt.Sprintf("upgrade-%d", time.Now().Unix()),
        Status:    "downloading",
        Progress:  0,
        StartedAt: time.Now(),
    }
    upgradeTask = task

    binPath := "/usr/local/bin/nas-panel"
    backupPath := binPath + ".bak"
    tmpPath := "/tmp/nas-panel.new"

    // Step 1: 下载（30%）
    task.Message = "正在下载新版本..."
    _, url, _ := checkLatestVersion()
    if err := downloadFile(url, tmpPath); err != nil {
        task.Status = "failed"
        task.Message = "下载失败: " + err.Error()
        return
    }
    task.Progress = 30

    // Step 2: 校验（50%）
    task.Message = "校验文件..."
    if err := verifyBinary(tmpPath); err != nil {
        task.Status = "failed"
        task.Message = "校验失败: " + err.Error()
        os.Remove(tmpPath)
        return
    }
    task.Progress = 50

    // Step 3: 备份旧版（60%）
    task.Message = "备份当前版本..."
    if err := os.Rename(binPath, backupPath); err != nil {
        task.Status = "failed"
        task.Message = "备份失败: " + err.Error()
        return
    }
    task.Progress = 60

    // Step 4: 替换（70%）
    task.Message = "替换二进制..."
    if err := os.Rename(tmpPath, binPath); err != nil {
        // 回滚
        os.Rename(backupPath, binPath)
        task.Status = "rolledback"
        task.Message = "替换失败，已回滚"
        return
    }
    os.Chmod(binPath, 0755)
    task.Progress = 70

    // Step 5: 重启服务（90%）
    task.Message = "重启服务..."
    if err := exec.Command("systemctl", "restart", "nas-panel").Run(); err != nil {
        // 回滚
        os.Rename(binPath, tmpPath) // 把新版的移走
        os.Rename(backupPath, binPath) // 恢复旧版
        exec.Command("systemctl", "restart", "nas-panel").Run()
        task.Status = "rolledback"
        task.Message = "重启失败，已回滚到旧版本"
        return
    }
    task.Progress = 90

    // Step 6: 健康检查（100%）
    task.Message = "验证新版本..."
    time.Sleep(2 * time.Second) // 等服务启动
    if _, err := http.Get("http://localhost:8090/api/health"); err != nil {
        // 回滚
        exec.Command("systemctl", "stop", "nas-panel").Run()
        os.Rename(binPath, tmpPath)
        os.Rename(backupPath, binPath)
        exec.Command("systemctl", "start", "nas-panel").Run()
        task.Status = "rolledback"
        task.Message = "健康检查失败，已回滚"
        return
    }

    task.Status = "done"
    task.Progress = 100
    task.Message = "升级成功"

    // 保留备份 7 天，然后删除（用 cron 或下次升级时清理）
}

func downloadFile(url, path string) error {
    resp, err := http.Get(url)
    if err != nil { return err }
    defer resp.Body.Close()

    if resp.StatusCode != 200 {
        return fmt.Errorf("HTTP %d", resp.StatusCode)
    }

    f, err := os.Create(path)
    if err != nil { return err }
    defer f.Close()

    _, err = io.Copy(f, resp.Body)
    return err
}

func verifyBinary(path string) error {
    // 简单校验：文件大小 > 1MB，且是可执行文件
    info, err := os.Stat(path)
    if err != nil { return err }
    if info.Size() < 1024*1024 {
        return fmt.Errorf("file too small")
    }
    // 也可以做 SHA256 校验，如果发布时提供 checksum
    return nil
}
```

#### 6.3 升级状态查询 API

```go
func upgradeStatusHandler(w http.ResponseWriter, r *http.Request) {
    if upgradeTask == nil {
        common.JSONResponse(w, map[string]interface{}{
            "status": "idle",
        })
        return
    }
    common.JSONResponse(w, upgradeTask)
}
```

**Day 6 验收标准**：
- [ ] `GET /api/system/version` 返回当前版本和最新版本
- [ ] `POST /api/system/upgrade` 触发升级
- [ ] 升级失败自动回滚
- [ ] `GET /api/system/upgrade-status` 返回进度

---

### Day 7：前端 + 集成（2-3 小时）

**目标**：系统设置页加版本卡片，显示升级按钮和进度

#### 7.1 版本信息卡片

```html
<!-- 系统设置页 -->
<div class="card" x-data="versionManager()" x-init="init()">
    <div class="header">
        <div class="title">版本信息</div>
        <span class="badge" :class="hasUpdate ? 'badge-yellow' : 'badge-green'" 
              x-text="hasUpdate ? '有更新' : '已是最新'"></span>
    </div>

    <div style="display:grid; grid-template-columns: 1fr 1fr; gap: 12px; font-size: 13px; margin-bottom: 16px;">
        <div><strong>当前版本:</strong> <span x-text="currentVersion"></span></div>
        <div><strong>构建时间:</strong> <span x-text="buildTime"></span></div>
        <div><strong>最新版本:</strong> <span x-text="latestVersion"></span></div>
        <div><strong>系统架构:</strong> <span x-text="arch"></span></div>
    </div>

    <!-- 有更新时显示 -->
    <div x-show="hasUpdate" style="margin-bottom: 16px;">
        <div class="alert-box" style="margin-bottom: 12px;">
            🔔 新版本 <span x-text="latestVersion"></span> 可用，建议升级
        </div>
        <button @click="startUpgrade()" :disabled="upgrading" class="btn btn-primary">
            <span x-show="!upgrading">一键升级</span>
            <span x-show="upgrading">升级中...</span>
        </button>
    </div>

    <!-- 升级进度 -->
    <div x-show="upgrading || upgradeDone" style="margin-top: 12px;">
        <div style="font-size: 12px; color: #6b7280; margin-bottom: 6px;">
            <span x-text="upgradeMessage"></span>
            <span x-text="upgradeProgress + '%'"></span>
        </div>
        <div class="disk-bar" style="height: 8px;">
            <div class="disk-bar-fill" :style="'width:' + upgradeProgress + '%'"
                 :class="upgradeFailed ? 'bg-red' : 'bg-blue'"></div>
        </div>
        <div x-show="upgradeDone && !upgradeFailed" style="color: #22c55e; font-size: 13px; margin-top: 8px;">
            ✅ 升级成功，页面将在 3 秒后刷新...
        </div>
        <div x-show="upgradeFailed" style="color: #ef4444; font-size: 13px; margin-top: 8px;">
            ❌ <span x-text="upgradeMessage"></span>
        </div>
    </div>
</div>
```

```javascript
function versionManager() {
    return {
        currentVersion: '',
        latestVersion: '',
        buildTime: '',
        arch: '',
        hasUpdate: false,
        upgrading: false,
        upgradeProgress: 0,
        upgradeMessage: '',
        upgradeDone: false,
        upgradeFailed: false,

        async init() {
            this.arch = navigator.userAgent.includes('x86_64') || navigator.platform.includes('64') 
                ? 'x86_64' : 'unknown';
            await this.checkVersion();
        },

        async checkVersion() {
            try {
                const res = await fetch('/api/system/version');
                const data = await res.json();
                this.currentVersion = data.current;
                this.latestVersion = data.latest;
                this.buildTime = data.build_time;
                this.hasUpdate = data.has_update;
            } catch (e) {
                console.error('check version failed', e);
            }
        },

        async startUpgrade() {
            if (!confirm('确定要升级吗？升级过程中面板会短暂不可用。')) return;

            this.upgrading = true;
            this.upgradeProgress = 0;
            this.upgradeMessage = '准备升级...';

            try {
                const res = await fetch('/api/system/upgrade', { method: 'POST' });
                if (!res.ok) {
                    const data = await res.json();
                    alert(data.message || '升级启动失败');
                    this.upgrading = false;
                    return;
                }

                // 轮询进度
                this.pollProgress();
            } catch (e) {
                this.upgradeFailed = true;
                this.upgradeMessage = '网络错误: ' + e.message;
                this.upgrading = false;
            }
        },

        async pollProgress() {
            const interval = setInterval(async () => {
                try {
                    const res = await fetch('/api/system/upgrade-status');
                    const data = await res.json();

                    if (data.status === 'idle') {
                        clearInterval(interval);
                        return;
                    }

                    this.upgradeProgress = data.progress || 0;
                    this.upgradeMessage = data.message || '升级中...';

                    if (data.status === 'done') {
                        clearInterval(interval);
                        this.upgradeDone = true;
                        this.upgrading = false;
                        setTimeout(() => location.reload(), 3000);
                    } else if (data.status === 'failed' || data.status === 'rolledback') {
                        clearInterval(interval);
                        this.upgradeFailed = true;
                        this.upgrading = false;
                    }
                } catch (e) {
                    // 服务重启期间请求会失败，这是正常的，继续轮询
                }
            }, 1000);

            // 超时保护：5 分钟后停止轮询
            setTimeout(() => {
                clearInterval(interval);
                if (this.upgrading) {
                    this.upgradeFailed = true;
                    this.upgradeMessage = '升级超时，请手动检查服务状态';
                    this.upgrading = false;
                }
            }, 5 * 60 * 1000);
        }
    }
}
```

#### 7.2 发布流程

每次发版时：

```bash
# 1. 打 tag
git tag -a v1.3.0 -m "Release v1.3.0"
git push origin v1.3.0

# 2. GitHub Actions 自动编译（需要配置）
# 或者手动编译多平台
make build-all

# 3. 上传二进制到 file.abwen.com
cp web/nas-panel file.abwen.com/control/nas-panel-linux-amd64
echo "v1.3.0" > file.abwen.com/control/nas-panel.version

# 4. 写 Release Notes
cat > CHANGELOG.md << 'EOF'
## v1.3.0 (2026-09-10)
- 新增：权限矩阵支持按用户粒度控制 SMB 读写权限
- 新增：Web 面板一键升级功能
- 修复：存储管理 Tab 切换跳动
- 改进：协议徽标按真实能力诚实标注
EOF
```

**Day 7 验收标准**：
- [ ] 系统设置页显示版本信息
- [ ] 有更新时显示「一键升级」按钮
- [ ] 升级过程有进度条
- [ ] 升级成功自动刷新页面
- [ ] 升级失败显示错误信息

---

## 附录：每日时间分配建议

| 天 | 事项 | 建议时段 | 预计时长 |
|---|------|---------|---------|
| Day 1 | 数据模型 + 迁移 | 晚上 2 小时 | 2-3h |
| Day 2 | SMB 生成逻辑 + 单元测试 | 晚上 2 小时 | 3-4h |
| Day 3 | 后端 API | 晚上 2 小时 | 3-4h |
| Day 4 | 前端权限矩阵 | 晚上 2 小时 | 3-4h |
| Day 5 | 诚实标注 + 集成测试 | 周末半天 | 2-3h |
| Day 6 | 版本更新后端 | 晚上 2 小时 | 3-4h |
| Day 7 | 版本更新前端 + 发布 | 周末半天 | 2-3h |

**总计**：约 20 小时，分散在 2 周内（每天 2 小时 + 两个周末半天）

---

*文档结束*
