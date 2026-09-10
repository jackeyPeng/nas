# Import Pool 恢复 — 设计记录

> 日期: 2026-09-09
> 触发: 商业化评审报告 §47/52/53（换机恢复 / Storage metadata 脱离系统盘恢复）
> 状态: v1 实现中（clarify 超时，按推荐默认值推进，可随时调整）

## 目标

换机 / 系统盘重装后，旧数据盘上的既有存储（LVM 卷组）能被识别并"导入"——重新挂载为 /data/nas1，重建共享文件夹元数据，恢复可访问状态。全程非破坏性。

## 现状缺口

- getDiskStatus 用 lsblk 分类：带 LVM 签名的盘归为 "data"（不是 unused），向导不拿它建池。
- overview 只识别"已挂载到 /data"的池；未激活/未挂载的 VG 不被识别。
- folders.db（共享文件夹元数据）在系统盘 /opt/nas/data，系统盘损坏即丢失。

## v1 范围（已选默认值）

1. 存储类型：LVM（覆盖 single 单盘 / merge 多盘两种向导模式，都是 VG）。
   RAID(mdadm)、独立分区(separate) 留扩展位，后续版本加。
2. 元数据恢复：目录扫描重建（public=所有人读写+NFS，其它目录=用户 home 且 valid_users=目录名）。
   「数据盘存元数据备份(.z1meta)」作为后续增强，v1 不做。
3. 触发方式：存储管理页检测到可导入池时显示横幅 + 手动「导入」按钮（非自动）。

## 实现

- 后端 `web/modules/diskmgmt/import.go`
  - detectImportablePools：pvs→VG，排除已挂载的 LV，返回可导入池列表。
  - handleImportList：GET /api/disk/import
  - handleImportPool：POST /api/disk/import/pool {vg_name, confirm=yes}
    ① vgchange -ay 激活 → ② mount LV 到 /data/nas1 → ③ 按 UUID 写 fstab → ④ rebuildFoldersFromDisk 扫目录重建 folders.db → ⑤ SyncAllConfigs 重生成 smb.conf/exports。
  - 非破坏性：不 wipefs / 不 mkfs / 不 lvremove。
- 前端：存储管理页横幅 + 导入按钮；navigate('diskmgmt') 时加载可导入列表。

## 安全

- 导入只做 assemble + mount + 读目录 + 写配置，任何误操作不丢数据。
- confirm=yes 门槛（非破坏性，无需输入确认词）。
- 已挂载的 LV 自动跳过，不会重复挂载。
