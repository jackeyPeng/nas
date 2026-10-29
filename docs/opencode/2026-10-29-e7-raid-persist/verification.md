# E7 真机验证纪要（parent 独立验证，2026-10-29）

## 缺陷实锤（修复前）

测试机57（Debian 13，RAID1 池，beta.34 发布物一键安装）重启后：

- `/proc/mdstat`：`md127 : active raid1 sdd[0] sda[2]` —— 阵列装配成 **md127** 而非建池时的 md0
- `/etc/mdadm/mdadm.conf`：ARRAY 行**存在**（流式建池第 6 步写入成功）
- initramfs 内 `etc/mdadm/mdadm.conf`：**无 ARRAY 行**（stream.go 从不调 update-initramfs，装机时的旧 initramfs 沿用）→ 早期装配无视根 conf → 名称漂移
- 数据安全无损：fstab 按 UUID 挂载正常、面板 sysfs 链自愈（pool 显示 /dev/md127 healthy=True）、注册表 41/41
- 盘符同时漂移（系统盘 sda→sde，成员盘重排）——面板 free/pool 判定全部正确跟随

## 修复后验证（9100092 二进制，a0071a03）

Phase A（删池→流式重建 raid1，全部走新代码路径）：

- 删池 7 步成功（卸载/停止/释放×2/fstab/folders.db/配置重投影）
- 重建后 mdadm.conf：新 UUID ARRAY 行写入 ✓（旧阵列陈旧行残留 → 立项 E8）
- initramfs mtime 更新 ✓，initramfs 内 mdadm.conf 含新 ARRAY 行 ✓（persistRAIDConfig 的 update-initramfs 生效）
- 手工清除陈旧行 + update-initramfs 后重启

Phase B（重启装配验证）：

- `/proc/mdstat`：`md0 : active raid1 sdc[0] sde[1] [2/2] [UU]` —— **装配为 md0，漂移消除** ✓
- `/dev/md0` 挂载 `/data/nas1` ✓（fstab UUID）
- 面板：pool nas1 raid1 /dev/md0 healthy=True、issues 无 ✓
- 注册表 41/41 fail 0 warn 0 ✓
- 盘符再次漂移（sdc/sde 成员）——面板全部正确跟随 ✓

## 结论

E7 修复端到端生效：建池路径写 ARRAY 行 + 更新 initramfs → 重启后阵列按 conf 命名装配（md0），
配置/initramfs/内核三方一致。遗留：删池不清 ARRAY 行（E8 立项，真机实锤两条同指 /dev/md0 的
ARRAY 行共存）。
