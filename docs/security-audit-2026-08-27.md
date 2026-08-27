# 安全审计报告 — 2026-08-27

> 外部安全审查发现的问题与修复记录

---

## P0 — 严重（数据安全）

### 1. 池删除误伤无关磁盘

**文件**: `web/modules/diskmgmt/pool.go:372-404`

**问题**: 
- LVM 删除时 `pvs --noheadings` 遍历全系统所有 PV 并全部 `pvremove`，而非只删目标 VG 的成员
- RAID 删除时遍历所有非系统盘执行 `mdadm --zero-superblock` + `wipefs -a`，独立数据盘也会被清签名

**风险**: 用户有多个存储池或独立 ext4 数据盘时，删除一个池会破坏其他磁盘数据

**修复** (`20d64dc`):
- LVM: `pvs -S vg_name=<vgName>` 只过滤目标 VG 成员
- RAID: 新增 `parseMdMemberDisks()` 解析 `mdadm --detail` 输出，只清理本阵列成员

### 2. mdadm 配置保存命令无效

**文件**: `web/modules/diskmgmt/wizard.go:417`

**问题**: 
```go
common.SudoExec(mdadmPath, "--detail", "--scan", ">>", "/etc/mdadm/mdadm.conf")
```
`exec.Command` 不经过 shell，`>>` 被当作 mdadm 的字面参数，重定向不生效

**风险**: RAID 配置未持久化到 mdadm.conf，重启后阵列可能不自动组装

**修复**: 改用 `SudoOutput` 读取输出，再通过 `sh -c` 追加写入文件

---

## P1 — 高（功能正确性）

### 3. NVMe 设备分区名拼接错误

**文件**: `web/modules/diskmgmt/wizard.go:267`

**问题**: `partDev := dev + "1"` 对 NVMe 盘会拼出错误的 `nvme0n11`（应为 `nvme0n1p1`）

**风险**: NVMe 盘走向导单盘模式时分区后格式化失败

**修复**: 检测 `/dev/nvme` 前缀，使用 `p1` 后缀

### 4. 非流式向导无错误检查

**文件**: `web/modules/diskmgmt/wizard.go:261-285`

**问题**: `setupSingleDisk` 中 wipefs/parted/mkfs/mount 全部 fire-and-forget，失败后继续执行后续步骤

**风险**: parted 失败后仍尝试格式化错误的分区设备，步骤日志显示"成功"假象

**修复**: 改用 `SudoOutput` 检查返回值，失败立即返回错误步骤

---

## P2 — 中（并发安全）

### 5. 磁盘操作并发竞态

**文件**: `web/modules/diskmgmt/*.go`

**问题**: 无并发保护，两个管理员同时操作可能对同一块盘执行 wipefs/mdadm

**风险**: 破坏性操作竞态，可能导致数据损坏

**修复**: 新增 `diskOpMutex sync.Mutex`，在 7 个破坏性 handler 入口加锁：
- `handleWizardSetup` / `handleWizardSetupStream`
- `handleWizardReset` / `handleWizardResetStream`
- `handlePoolDelete`
- `handlePoolExtend` / `handlePoolExtendStream`

---

## P3 — 低（代码质量）

### 6. ApplyPendingOps 失败项静默丢弃

**文件**: `web/modules/diskmgmt/config_sync.go`

**问题**: 批量操作无论单项成败最后统一清空队列，失败项只在日志留痕

**建议**: 保留失败项或返回失败计数

### 7. handleFormat 允许任意 fstype

**文件**: `web/modules/diskmgmt/storage.go`

**问题**: 调试用接口允许任意 `mkfs.<fstype>` 和任意 `mkdir -p`，无白名单

**建议**: 下线或加白名单校验

### 8. 测试覆盖不足

**现状**: 仅 `common/` 包有 4 个测试文件，核心磁盘操作模块无测试

**建议**: 补充 `parseSambaShares`、`extractPercent`、`parseMdMemberDisks` 等解析函数的表驱动测试

---

*审查日期: 2026-08-27 · 修复日期: 2026-08-27*