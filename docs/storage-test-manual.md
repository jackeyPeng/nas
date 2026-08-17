# Z1 存储管理测试手册

> 版本: v1.0  
> 日期: 2026-08-17  
> 适用: Z1 NAS Web 管理面板 — 存储管理模块  
> 目标读者: 测试人员 / AI Agent  
> 测试环境: 10.216.10.52 (fm / nas1234567890) 或 192.168.213.85 (jacky / nas1234567890)

---

## 一、测试环境

### 1.1 目标机器

| 机器 | IP | 用户 | 面板端口 | 登录 |
|------|-----|------|---------|------|
| 52 | 10.216.10.52 | fm | 8090 | fm / nas1234567890 |
| 85 | 192.168.213.85 | jacky | 8090 | jacky / nas1234567890 |

### 1.2 测试前置条件

测试前必须确保磁盘处于干净状态。执行重置：

```bash
# 方式 A: 通过面板 API
ssh <user>@<ip> 'TOKEN=$(curl -s -X POST http://localhost:8090/api/login \
  -d "username=<user>&password=nas1234567890" | python3 -c "import sys,json;print(json.load(sys.stdin)[\"token\"])") && \
curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:8090/api/disk/wizard/reset-stream?confirm=yes'
```

重置完成后验证磁盘状态：
```bash
ssh <user>@<ip> 'lsblk -o NAME,SIZE,TYPE,MOUNTPOINT,FSTYPE | grep -v sda | grep -v sr0'
# 预期: sdb, sdc, sdd, sde 全部为空（无 FSTYPE，无 MOUNTPOINT）
```

### 1.3 测试用 API 速查

所有 API 都需要 Bearer token 认证。先登录获取 token：

```bash
TOKEN=$(curl -s -X POST http://<ip>:8090/api/login \
  -d "username=<user>&password=nas1234567890" | \
  python3 -c "import sys,json;print(json.load(sys.stdin)['token'])")
```

| 方法 | 路径 | 用途 |
|------|------|------|
| GET | /api/disk/overview | 存储总览（四层数据） |
| GET | /api/disk/status | 磁盘状态 JSON |
| GET | /api/disk/wizard/status | 向导状态（空闲盘+方案） |
| GET | /api/disk/wizard/setup-stream?mode=X&confirm=yes | SSE 流式创建存储 |
| GET | /api/disk/wizard/reset-stream?confirm=yes | SSE 流式重置 |
| GET | /api/disk/pool/status | LVM 池状态 |
| POST | /api/disk/pool/create | 创建存储池 |
| GET | /api/disk/pool/extend-stream | SSE 扩容 |
| POST | /api/disk/replace | 替换 RAID 故障盘 |
| POST | /api/disk/scrub | 启动 RAID 清理 |
| GET | /api/disk/scrub/status?md_device=/dev/md0 | 清理进度 |
| POST | /api/disk/smart-scan | 批量 SMART 检测 |
| GET | /api/disk/operations?limit=20 | 操作日志 |
| GET | /api/disk/folders | 共享文件夹列表 |
| POST | /api/disk/folders/create | 创建共享文件夹 |
| GET | /api/disk/raid/reshape-status | RAID 重构进度 |

---

## 二、测试场景

### 场景 1: 仪表盘 — 盘位图与统计卡片

**前置**: 磁盘干净状态（4 块数据盘，无存储池）

**步骤**:
1. 浏览器打开 `http://<ip>:8090`，登录
2. 观察仪表盘左侧"盘位图"

**预期结果**:
- 4 个槽位全部显示黄色（待配置），显示设备名 `/dev/sdb` ~ `/dev/sde`
- 无"空"槽位
- 统计卡片显示与 overview API 一致

**API 验证**:
```bash
curl -s -H "Authorization: Bearer $TOKEN" http://<ip>:8090/api/disk/overview | \
  python3 -c "import sys,json;d=json.load(sys.stdin)['overview'];print(f'free_disks={len(d[\"free_disks\"])} stats={d[\"stats\"]}')"
# 预期: free_disks=4, stats={'total_disks':5, 'total_pools':0, 'total_volumes':0, 'total_shares':0}
```

**创建存储后验证**:
1. 执行 LVM 合并创建（见场景 3）
2. 刷新仪表盘
3. 盘位图 4 槽应全部变蓝色（已安装），显示设备名和容量
4. 统计卡片: 存储池=1, 逻辑卷=1

---

### 场景 2: 存储管理 — 物理磁盘页

**前置**: 磁盘干净状态

**步骤**:
1. 进入"存储管理" → "💾 物理磁盘" Tab
2. 观察磁盘卡片网格

**预期结果**:
- 显示 5 块磁盘: sda(系统) + sdb/sdc/sdd/sde(数据)
- 每块盘显示: 设备名、容量、接口类型、温度（如有）、SMART 状态
- 系统盘(sda)为绿色，数据盘(sdb-sde)为黄色虚线边框
- 点击磁盘卡片可展开详情面板（分区列表、序列号等）

**API 验证**:
```bash
curl -s -H "Authorization: Bearer $TOKEN" http://<ip>:8090/api/disk/overview | \
  python3 -c "
import sys,json
d=json.load(sys.stdin)['overview']
print('system:', len(d['system_disks']))
print('free:', len(d['free_disks']))
print('total stats:', d['stats']['total_disks'])
"
# 预期: system=1, free=4, total=5
```

---

### 场景 3: 创建向导 — 7 种存储模式

**前置**: 磁盘干净状态（4 块数据盘）

#### 3.1 向导入口

**步骤**:
1. 进入"存储管理" → "🧙 创建向导"
2. 观察页面内容

**预期结果**:
- 显示 5 步指示器（选磁盘→定目标→确认方案→创建卷→完成）
- 第 1 步显示 4 块可选磁盘卡片
- 磁盘可点击选中/取消（蓝色边框=选中）
- 底部显示"已选 N 块盘"

**常见 Bug**: 如果向导页空白，检查 API 返回的 `unused_disks` 是否正确映射到 `wizardStatus.available_disks`

#### 3.2 LVM 单盘 (single)

| 步骤 | 操作 | 预期 |
|------|------|------|
| 1 | 选中 1 块盘 | 已选 1 块 |
| 2 | 下一步 → 选目标 | 显示 3 个目标卡片 |
| 3 | 选"最大容量" | 高亮 |
| 4 | 下一步 → 确认方案 | 显示"LVM 单盘"方案 |
| 5 | 确认并创建 | SSE 进度条 6 步 |
| 6 | 完成 | 显示 ✅，返回总览 |

**API 验证**:
```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://<ip>:8090/api/disk/wizard/setup-stream?mode=single&confirm=yes"
```
**预期**: 创建 1 个 LVM 池，挂载到 /data/nas1，约 50G

#### 3.3 LVM 合并 (merge)

**操作**: 重置后，选中所有 4 块盘，目标选"最大容量"

**API**:
```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://<ip>:8090/api/disk/wizard/setup-stream?mode=merge&confirm=yes"
```

**预期结果**:
- SSE 步骤: 清除初始化×4 → 创建卷组 → 创建逻辑卷 → 格式化 → 挂载配置
- 1 个 LVM 池，总容量约 200G（4×50G）
- 挂载到 /data/nas1
- 自动创建 Samba 共享 nas1

**验证**:
```bash
ssh <user>@<ip> 'df -h /data/nas1 && sudo vgs && sudo lvs'
```

#### 3.4 RAID1 镜像

**前置**: 重置后，至少 2 块空闲盘

**API**:
```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://<ip>:8090/api/disk/wizard/setup-stream?mode=raid1&confirm=yes"
```

**预期结果**:
- SSE 步骤: 清除签名 → 创建 RAID1 → 等待就绪 → 格式化 → 挂载 → 保存配置 → Samba
- 1 个 RAID1 池，容量约 50G（镜像，容量=1 块盘）
- 设备 /dev/md0，挂载 /data/nas1
- `/proc/mdstat` 显示 `[UU]`（两块盘都在线）

**验证**:
```bash
ssh <user>@<ip> 'cat /proc/mdstat && sudo mdadm --detail /dev/md0 | grep -E "State|Active|Working"'
```

#### 3.5 RAID0 条带

**前置**: 重置后，至少 2 块空闲盘

**API**:
```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://<ip>:8090/api/disk/wizard/setup-stream?mode=raid0&confirm=yes"
```

**预期**: 容量约 100G（2×50G），无冗余

#### 3.6 RAID5 / RAID6

**前置**: 重置后，RAID5 需 ≥3 块，RAID6 需 ≥4 块

**API**:
```bash
# RAID5
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://<ip>:8090/api/disk/wizard/setup-stream?mode=raid5&confirm=yes"

# RAID6
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://<ip>:8090/api/disk/wizard/setup-stream?mode=raid6&confirm=yes"
```

**预期**: RAID5 容量约 150G(3×50G, n-1), RAID6 容量约 100G(4×50G, n-2)

#### 3.7 独立模式 (separate)

**前置**: 重置后，至少 2 块空闲盘

**API**:
```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://<ip>:8090/api/disk/wizard/setup-stream?mode=separate&confirm=yes"
```

**预期结果**:
- 每块盘独立: /data/nas1, /data/nas2, /data/nas3, /data/nas4
- 每个 50G XFS，各自独立
- overview 显示 4 个 single 类型池

**验证**:
```bash
ssh <user>@<ip> 'df -h | grep /data/nas'
```

---

### 场景 4: 存储池操作

**前置**: 已创建存储池（建议 LVM 合并，方便扩容测试）

#### 4.1 扩容 LVM 池

**前置**: 有 LVM 池 + 至少 1 块空闲盘

**步骤**:
1. 进入"存储池" Tab
2. 点击池卡片右侧"扩容"按钮
3. 弹窗输入新磁盘设备路径（如 /dev/sdd）
4. 点击"开始扩容"

**预期结果**:
- SSE 进度: 清除签名 → pvcreate → vgextend → lvextend → xfs_growfs
- 完成后池容量增大

**API**:
```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://<ip>:8090/api/disk/pool/extend-stream?vg_name=vg_nas&device=/dev/sdd&lv_name=data&confirm=yes"
```

**验证**:
```bash
ssh <user>@<ip> 'df -h /data/nas1 && sudo vgs'
```

#### 4.2 扩容 RAID 池

**前置**: 有 RAID 池 + 至少 1 块空闲盘

**API**:
```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://<ip>:8090/api/disk/raid/expand-stream?md_device=/dev/md0&device=/dev/sdd&confirm=yes"
```

**预期**: RAID 从 2 盘扩到 3 盘，触发 reshape

**验证**:
```bash
ssh <user>@<ip> 'cat /proc/mdstat'
# 预期看到 reshape 进度
```

---

### 场景 5: 维护操作

**前置**: 已创建 RAID 池

#### 5.1 数据清理 (Scrub)

**步骤**:
1. 进入"维护" Tab，点击"数据清理"
2. 或者进入"存储池" Tab，点击池卡片的"清理"按钮

**预期结果**:
- 弹出确认对话框
- 确认后 mdadm --action=check 启动
- 操作日志显示"清理已启动"

**API**:
```bash
# 指定设备
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  "http://<ip>:8090/api/disk/scrub" \
  -d "md_device=/dev/md0&confirm=yes"

# 所有 RAID 设备
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  "http://<ip>:8090/api/disk/scrub" \
  -d "confirm=yes"
```

**进度查询**:
```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://<ip>:8090/api/disk/scrub/status?md_device=/dev/md0" | python3 -m json.tool
```

**预期**: 返回 `progress`, `speed`, `eta`, `state` 字段。与 `/proc/mdstat` 一致。

**注意**: RAID 初始同步期间无法启动 scrub（返回 500，预期行为）

#### 5.2 替换故障盘

**前置**: 有 RAID 池，模拟故障盘

**步骤**:
1. 进入"维护" Tab，点击"替换故障盘"
2. 或存储池 → 池卡片 → "替换盘"
3. 弹窗填写: RAID 设备(/dev/md0)、故障盘(/dev/sdb)、新盘(/dev/sdd)

**API**:
```bash
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  "http://<ip>:8090/api/disk/replace" \
  -d "md_device=/dev/md0&old_device=/dev/sdb&new_device=/dev/sdd&confirm=yes"
```

**预期**: 标记故障 → 移除 → 清除新盘 → 添加 → 自动重建

**验证**:
```bash
ssh <user>@<ip> 'cat /proc/mdstat'
# 预期看到 recovery/rebuild 进度
```

#### 5.3 SMART 批量检测

**步骤**:
1. 进入"维护" Tab，点击"SMART 检测"

**API**:
```bash
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  "http://<ip>:8090/api/disk/smart-scan" \
  -d "type=short&confirm=yes"
```

**预期**: 对所有非系统盘启动 short self-test，约 2 分钟完成

#### 5.4 操作日志

**API**:
```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://<ip>:8090/api/disk/operations?limit=20"
```

**预期**: 返回最近操作记录，包含时间、状态(success/warning/error)、内容

---

### 场景 6: 存储总览 — 拓扑树

**前置**: 已创建存储池（建议独立模式，有多个池和卷）

**步骤**:
1. 进入"存储总览" Tab
2. 观察顶部统计卡片和告警栏
3. 观察拓扑树

**预期结果**:
- 统计卡片: 物理磁盘/存储池/逻辑卷/共享文件夹 数量正确
- 告警栏: 无异常时隐藏，有高温/SMART 失败时显示
- 拓扑树: Pool→Volume→Folder 三层缩进连线
  - Pool 行: 图标 + Badge + 进度条
  - Volume 卡片: 边框 + 协议 tag
  - Folder 行: 协议 tag

**API 验证**:
```bash
curl -s -H "Authorization: Bearer $TOKEN" http://<ip>:8090/api/disk/overview | \
  python3 -c "
import sys,json
ov=json.load(sys.stdin)['overview']
print('pools:', len(ov['pools']))
for p in ov['pools']:
    print(f'  {p[\"display_name\"]}: {p[\"type\"]} {p[\"size\"]} healthy={p[\"healthy\"]}')
    for v in p.get('volumes',[]):
        print(f'    {v[\"display_name\"]}: {v[\"mountpoint\"]} {v[\"fstype\"]} {v[\"size\"]} folders={len(v.get(\"folders\",[]))}')
"
```

---

## 三、完整回归测试流程

### 3.1 单轮测试脚本

以下 Python 脚本在目标机器上执行一轮完整测试（通过 SSH）：

```python
import urllib.request, json, subprocess, time

# ===== 配置 =====
HOST = "10.216.10.52"
USER = "fm"
PASS = "nas1234567890"
BASE = f"http://localhost:8090"

# ===== 登录 =====
def login():
    data = f"username={USER}&password={PASS}".encode()
    req = urllib.request.Request(f"{BASE}/api/login", data=data)
    resp = urllib.request.urlopen(req, timeout=10)
    return json.loads(resp.read())["token"]

TOKEN = login()

def api(path):
    req = urllib.request.Request(f"{BASE}{path}")
    req.add_header("Authorization", f"Bearer {TOKEN}")
    return json.loads(urllib.request.urlopen(req, timeout=10).read())

def sse(path):
    req = urllib.request.Request(f"{BASE}{path}")
    req.add_header("Authorization", f"Bearer {TOKEN}")
    resp = urllib.request.urlopen(req, timeout=120)
    buf = b""
    while True:
        try:
            chunk = resp.read(4096)
            if not chunk: break
            buf += chunk
            for line in buf.split(b"\n"):
                if line.startswith(b"data: "):
                    ev = json.loads(line[6:])
                    if ev["status"] == "error":
                        print(f"  ❌ {ev['step']}: {ev.get('detail','')}")
                        return False
            buf = buf.split(b"\n")[-1] if buf else b""
        except: break
    return True

# ===== 测试序列 =====
results = []

# 1. 重置
print("1. 重置存储...")
sse("/api/disk/wizard/reset-stream?confirm=yes")
r = api("/api/disk/overview")
assert len(r["overview"]["free_disks"]) == 4, f"Expected 4 free disks, got {len(r['overview']['free_disks'])}"
results.append(("重置", "PASS"))

# 2. LVM 合并
print("2. LVM 合并 4 盘...")
sse("/api/disk/wizard/setup-stream?mode=merge&confirm=yes")
r = api("/api/disk/overview")
assert len(r["overview"]["pools"]) == 1
assert r["overview"]["pools"][0]["type"] == "lvm"
assert r["overview"]["pools"][0]["healthy"] == True
results.append(("LVM合并", "PASS"))

# 3. 重置 → RAID1 → 清理
print("3. RAID1 + 清理...")
sse("/api/disk/wizard/reset-stream?confirm=yes")
sse("/api/disk/wizard/setup-stream?mode=raid1&confirm=yes")
r = api("/api/disk/overview")
assert r["overview"]["pools"][0]["type"] == "raid1"
# 等待初始同步
for i in range(60):
    time.sleep(5)
    r = api("/api/disk/scrub/status?md_device=/dev/md0")
    if r["scrub_status"].get("progress") == "100":
        break
# 清理
r = api("/api/disk/scrub", "POST", "md_device=/dev/md0&confirm=yes")
assert "清理已启动" in r.get("message", "")
results.append(("RAID1+清理", "PASS"))

# 4. 重置 → 独立模式
print("4. 独立模式...")
sse("/api/disk/wizard/reset-stream?confirm=yes")
sse("/api/disk/wizard/setup-stream?mode=separate&confirm=yes")
r = api("/api/disk/overview")
assert len(r["overview"]["pools"]) >= 2
results.append(("独立模式", "PASS"))

# ===== 报告 =====
print("\n" + "="*40)
for name, status in results:
    print(f"  {status}: {name}")
print("="*40)
```

### 3.2 多轮压力测试

```bash
# 在目标机器上循环执行 3 轮
for round in 1 2 3; do
    echo "=== Round $round ==="
    # 重置
    curl -s -H "Authorization: Bearer $TOKEN" \
        "http://localhost:8090/api/disk/wizard/reset-stream?confirm=yes" > /dev/null
    # LVM 合并
    curl -s -H "Authorization: Bearer $TOKEN" \
        "http://localhost:8090/api/disk/wizard/setup-stream?mode=merge&confirm=yes" > /dev/null
    # 验证
    POOLS=$(curl -s -H "Authorization: Bearer $TOKEN" \
        "http://localhost:8090/api/disk/overview" | \
        python3 -c "import sys,json;print(len(json.load(sys.stdin)['overview']['pools']))")
    echo "  Pools: $POOLS (expected: 1)"
done
```

---

## 四、已知问题与注意事项

### 4.1 环境依赖

| 依赖 | 用途 | 安装命令 |
|------|------|---------|
| parted | 独立模式创建 GPT 分区 | `apt-get install -y parted` |
| mdadm | RAID 创建/管理 | 通常已预装 |
| smartctl | SMART 磁盘检测 | 通常已预装 |
| lvm2 | LVM 卷管理 | 通常已预装 |

### 4.2 已知 Bug 清单（已修复）

以下 bug 已在 v1.4.0-dev 修复，测试时不应再出现：

| Bug | 表现 | 根因 |
|-----|------|------|
| 创建向导空白 | 向导 Tab 无内容 | `unused_disks` 未映射到 `wizardStatus.available_disks` |
| 按钮无响应 | 存储池/维护按钮点击无反应 | 缺少 `@click` 处理器 |
| 盘位图缺盘 | 仪表盘只显示部分磁盘 | `stopRAIDArrays` 用 `ls /dev/md*` 通配符不展开 |
| 统计数字错误 | 物理磁盘计数不对 | 同上，sdb/sdc 被 overview 丢弃 |
| scrub 进度不准 | 始终显示 100% | mdstat 多行格式解析错误 |
| lvcreate 失败 | 旧签名拦截 | 缺少 `-y` 参数 |
| scrub 通配失败 | POST /api/disk/scrub 500 | `ls /dev/md*` 通配符不展开 |
| parted 缺失 | 独立模式创建失败 | Debian 未预装 parted |

### 4.3 浏览器缓存

部署新版本后，浏览器可能缓存旧的前端代码。测试前务必 **硬刷新** (Ctrl+Shift+R) 或清除缓存。

---

## 五、测试检查清单

### 仪表盘
- [ ] 盘位图显示全部数据盘（4 槽，无"空"）
- [ ] 盘位图颜色正确（蓝=已安装, 黄=待配置, 灰=空, 红=故障）
- [ ] 统计数字与 API 返回一致

### 存储总览
- [ ] 统计卡片: 物理磁盘/存储池/逻辑卷/共享文件夹 数量正确
- [ ] 拓扑树: Pool→Volume→Folder 三层关系正确
- [ ] 告警栏: 有异常时显示（如高温），无异常时隐藏

### 物理磁盘
- [ ] 显示全部磁盘（含系统盘）
- [ ] 磁盘卡片颜色编码正确
- [ ] 点击展开详情面板正常

### 存储池
- [ ] 池列表正确显示
- [ ] 扩容/替换盘/清理 按钮功能正常
- [ ] 扩容弹窗可用
- [ ] 替换盘弹窗可用

### 创建向导
- [ ] 5 步指示器正常
- [ ] 磁盘选择可点击切换
- [ ] 目标选择卡片正常
- [ ] 方案推荐正确
- [ ] SSE 进度条正常
- [ ] 完成后可返回总览

### 维护
- [ ] 替换故障盘弹窗正常
- [ ] 扩容存储池跳转到池 Tab
- [ ] 数据清理确认+执行
- [ ] SMART 检测确认+执行
- [ ] 操作日志显示正确

### 后端 API
- [ ] 所有 GET 端点返回正确 JSON
- [ ] 所有 POST 端点功能正常
- [ ] SSE 流式端点进度正常推送
- [ ] 重置→创建→重置 循环无残留