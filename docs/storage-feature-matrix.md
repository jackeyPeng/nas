# Z1 存储管理 — 功能清单与测试矩阵

> 版本: 2026-08-17  
> 测试环境: 192.168.1.100 (fm / <NAS_PASS>) 或 192.168.1.100 (jacky / <NAS_PASS>)  
> 面板地址: http://<ip>:8090  
> 测试前: 硬刷新浏览器 (Ctrl+Shift+R) 清除缓存

---

## 一、功能清单

### 1.1 创建模式 (7种)

| 模式 | API mode | 最少盘 | 容量公式 | 特点 |
|------|----------|--------|---------|------|
| LVM 单盘 | `single` | 1 | 1×盘大小 | 单盘 LVM，后期可扩容 |
| LVM 合并 | `merge` | 2 | N×盘大小 | 多盘合并为一个空间，可扩容 |
| RAID0 | `raid0` | 2 | N×盘大小 | 条带，速度最快，无冗余 |
| RAID1 | `raid1` | 2 | 1×盘大小 | 镜像，坏1盘不丢数据 |
| RAID5 | `raid5` | 3 | (N-1)×盘大小 | 分布式校验，坏1盘不丢 |
| RAID6 | `raid6` | 4 | (N-2)×盘大小 | 双重校验，坏2盘不丢 |
| 独立模式 | `separate` | 2 | N×盘大小 | 每盘独立一个存储空间 |

### 1.2 创建向导 (5步)

| 步骤 | 说明 |
|------|------|
| 1. 选磁盘 | 点击磁盘卡片选中/取消，显示已选数量 |
| 2. 定目标 | 4个目标: 数据安全/最大容量/读写性能/平衡 |
| 3. 选方案 | 动态展示匹配的方案卡片(含推荐标记、容量、安全性、最少盘数) |
| 4. 确认创建 | 显示方案摘要，点击"确认并创建" |
| 5. SSE进度 | 实时显示每一步进度(清除→创建→格式化→挂载→Samba) |

### 1.3 存储池操作 (4项)

| 操作 | 适用池类型 | 说明 |
|------|-----------|------|
| 扩容 | LVM / RAID | 加入新磁盘，自动扩展文件系统 |
| 替换盘 | RAID | 标记故障→移除→加新盘→自动重建 |
| 清理 | RAID | mdadm --action=check 扫描静默数据损坏 |
| 删除 | 全部 | 卸载→清理 LVM/RAID→擦除签名→释放磁盘 |

### 1.4 维护操作 (4项)

| 操作 | 说明 |
|------|------|
| 替换故障盘 | 弹出替换盘弹窗 |
| 扩容存储池 | 跳转到存储池 Tab |
| 数据清理 | 对所有 RAID 设备执行 scrub |
| SMART 检测 | 批量启动 smartctl -t short |

### 1.5 页面/Tab (5个)

| Tab | 内容 |
|-----|------|
| 存储总览 | 统计卡片 + 告警栏 + 拓扑树(Pool→Volume→Folder) |
| 物理磁盘 | 磁盘卡片网格(系统盘绿色/数据盘蓝色/空闲盘黄色虚线)，点击展开详情 |
| 存储池 | 池卡片列表(类型/健康/容量/进度条) + 扩容/替换盘/清理/删除按钮 |
| 创建向导 | 5步向导流程 |
| 维护 | 4个操作卡片 + 操作日志 |

### 1.6 其他

| 功能 | 说明 |
|------|------|
| 仪表盘盘位图 | 4槽位，颜色编码(蓝=已安装/黄=待配置/灰=空/红=故障) |
| 共享文件夹 | 创建/删除/权限/配额 |
| 操作日志 | 时间/状态/内容 |

---

## 二、功能组合矩阵

### 2.1 创建 × 操作 组合

| 创建\操作 | 扩容 | 替换盘 | 清理 | 删除 |
|-----------|------|--------|------|------|
| LVM 单盘 | ✅ | - | - | ✅ |
| LVM 合并 | ✅ | - | - | ✅ |
| RAID0 | ✅ | ✅ | ✅ | ✅ |
| RAID1 | ✅ | ✅ | ✅ | ✅ |
| RAID5 | ✅ | ✅ | ✅ | ✅ |
| RAID6 | ✅ | ✅ | ✅ | ✅ |
| 独立模式 | - | - | - | ✅ |

> ✅ = 可用, - = 不适用

### 2.2 目标 × 方案 组合

| 目标\盘数 | 1盘 | 2盘 | 3盘 | 4盘 |
|-----------|-----|-----|-----|-----|
| 数据安全 | - | RAID1 | RAID1,RAID5 | RAID1,RAID6 |
| 最大容量 | single | merge,separate | merge,separate | merge,separate |
| 读写性能 | - | RAID0 | RAID0 | RAID0 |
| 平衡 | - | separate | RAID5,separate | RAID5,separate |

---

## 三、测试场景

### 场景 A: 7种模式创建+删除 (基础)

```
for each mode in [single, merge, raid0, raid1, raid5, raid6, separate]:
    重置 → 创建 → 验证 overview → 删除 → 验证磁盘干净
```

**验证点:**
- 创建后 overview.pools 数量正确
- 池类型正确
- 容量公式正确 (merge=200G, raid1=50G, raid5=150G, raid6=100G)
- 删除后 pools=0, free_disks=4
- lsblk 显示所有磁盘干净(无 FSTYPE)

### 场景 B: 创建+扩容 (LVM组合)

```
1. 重置 → 创建 LVM merge (2盘)
2. 验证: 池容量=100G
3. 扩容加入第3盘 → 验证: 池容量变大
4. 扩容加入第4盘 → 验证: 池容量=200G
5. 删除
```

### 场景 C: 创建+扩容 (RAID组合)

```
1. 重置 → 创建 RAID1 (2盘)
2. 验证: 池容量=50G, dev=/dev/md0
3. 扩容加入第3盘 → 验证: mdstat 显示 reshape
4. 删除
```

### 场景 D: 创建+清理 (RAID组合)

```
1. 重置 → 创建 RAID1 (2盘)
2. 等待初始同步完成 (约4分钟)
3. 触发清理 → 验证: scrub_status 返回进度>0
4. 删除
```

### 场景 E: 创建+删除 (快速连续)

```
for i in range(3):
    重置 → 创建 merge → 删除
    验证: 每轮后磁盘干净
```

### 场景 F: 同步中删除

```
1. 重置 → 创建 RAID1
2. 立即删除(不等同步完成)
3. 验证: 磁盘干净, md0 已停止
```

### 场景 G: 向导目标过滤

```
1. 选2盘 → 目标"数据安全" → 应显示 RAID1(推荐)
2. 选2盘 → 目标"最大容量" → 应显示 merge, separate
3. 选2盘 → 目标"读写性能" → 应显示 RAID0
4. 选2盘 → 目标"平衡" → 应显示 separate
5. 选4盘 → 目标"数据安全" → 应显示 RAID1, RAID6(推荐)
6. 选1盘 → 目标"最大容量" → 应显示 single(推荐)
```

### 场景 H: 向导SSE进度

```
1. 走完向导流程(选盘→目标→方案→确认)
2. 观察步骤5: 应实时显示每步进度
3. 每步应有状态图标(🔄运行中/✅完成)
4. 全部完成后显示✅存储池创建成功
5. 点击"返回总览"→ 应跳到总览页并刷新数据
```

### 场景 I: 仪表盘一致性

```
1. 干净状态: 盘位图4槽全黄(待配置), stats: disks=5,pools=0
2. 创建 merge: 盘位图4槽全蓝(已安装), stats: disks=5,pools=1,vols=1
3. 创建 separate: 盘位图4槽全蓝, stats: disks=5,pools=4,vols=4
4. 删除后: 盘位图4槽全黄, stats: disks=5,pools=0
```

### 场景 J: 维护操作

```
1. 替换故障盘弹窗: 弹出正确, 填写3个字段
2. 扩容存储池: 点击跳转到存储池Tab
3. 数据清理: 确认后启动 scrub
4. SMART检测: 确认后启动 smartctl
5. 操作日志: 显示最近操作
```

### 场景 K: 不存在的池删除

```
1. 调用删除API传 ghost 池名
2. 应返回错误(不是崩溃)
```

### 场景 L: 共享文件夹

```
1. 创建 merge 池 → 创建共享文件夹
2. 验证: folder列表显示, 磁盘目录存在
3. 删除文件夹 → 验证: 目录已删除
4. 删除池 → 验证: 文件夹随池删除
```

---

## 四、API 测试速查

```bash
# 登录
TOKEN=$(curl -s -X POST http://<ip>:8090/api/login \
  -d "username=<user>&password=<NAS_PASS>" | \
  python3 -c "import sys,json;print(json.load(sys.stdin)['token'])")

# 重置
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://<ip>:8090/api/disk/wizard/reset-stream?confirm=yes"

# 创建 LVM merge
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://<ip>:8090/api/disk/wizard/setup-stream?mode=merge&confirm=yes"

# 检查 overview
curl -s -H "Authorization: Bearer $TOKEN" http://<ip>:8090/api/disk/overview | python3 -m json.tool

# 删除池 (需要先获取 pool 信息)
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  "http://<ip>:8090/api/disk/pool/delete" \
  -d "pool_name=nas1&pool_type=lvm&pool_device=/dev/vg_nas&confirm=yes"
```

---

## 五、检查清单

### 创建向导
- [ ] 选1盘→目标"最大容量"→显示 single(推荐)
- [ ] 选2盘→目标"数据安全"→显示 RAID1(推荐)
- [ ] 选2盘→目标"最大容量"→显示 merge, separate
- [ ] 选2盘→目标"读写性能"→显示 RAID0
- [ ] 选2盘→目标"平衡"→显示 separate
- [ ] 选4盘→目标"数据安全"→显示 RAID1, RAID6
- [ ] 选4盘→目标"平衡"→显示 RAID5(推荐), separate
- [ ] SSE进度实时显示每步状态
- [ ] 完成后可返回总览

### 存储池
- [ ] 扩容按钮弹出弹窗
- [ ] 替换盘按钮弹出弹窗
- [ ] 清理按钮触发 scrub
- [ ] 删除按钮二次确认后删除

### 存储总览
- [ ] 统计卡片数字正确
- [ ] 拓扑树显示 Pool→Volume→Folder
- [ ] 告警栏无异常时隐藏

### 物理磁盘
- [ ] 显示全部磁盘(含系统盘)
- [ ] 颜色编码正确
- [ ] 点击展开详情面板

### 维护
- [ ] 4个卡片均可点击
- [ ] 操作日志显示历史记录

### 仪表盘
- [ ] 盘位图4槽位全部显示
- [ ] 颜色随存储状态变化
- [ ] 无"空"槽位(有盘时)

### 数据一致性
- [ ] merge: stats.disks=5, pools=1, vols=1
- [ ] separate: stats.disks=5, pools=4, vols=4
- [ ] 删除后: pools=0, free_disks=4
- [ ] 每轮创建+删除后磁盘干净(lsblk 无残留 FSTYPE)