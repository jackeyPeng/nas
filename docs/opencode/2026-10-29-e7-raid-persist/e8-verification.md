# E8 真机验证纪要（parent 独立验证，2026-10-29，测试机57）

二进制：a18ba02 构建（v1.4.0-beta.34-3-ga18ba02，md5 a76d627e），.57/.48 双机部署。

## 删池清理 ARRAY 行（E8 本体）

- 删池响应 steps 从 7 步变 8 步，新增"清理 mdadm.conf ARRAY 行"（位于释放成员盘之后、清理 fstab 之前）
- 删后断言：mdadm.conf `ARRAY` 行数 = 0 ✓；`HOMEHOST <system>`/`MAILADDR root` 默认行保留 ✓；initramfs mtime 更新 = YES ✓
- 重建 raid1（流式，走 E7 persistRAIDConfig）：complete ✓，mdadm.conf 恰一条 ARRAY 行，UUID 与 `mdadm --detail --scan` 实际输出逐字一致（2269ff04:…）✓，/dev/md0 挂载 /data/nas1 ✓

## 重启装配（E7+E8 联合终验，resync 中途重启加验）

- resync 14.8% 时重启 → 开机装配为 **md0**（非 md127，ARRAY 行 + initramfs 生效）✓
- **resync 跨重启自动续跑**：14.8% → 重启后 55.4% 继续（bitmap 续同步）✓
- 面板重启后如实显示："RAID /dev/md0 正在同步 68.4%"、healthy=False、reshape-status progress/speed/eta 与内核一致 ✓ —— consensus 阶段 1 验收"重启后继续显示真实进度"实证
- 同步完成：[2/2] [UU]、pool healthy=True、注册表 41/41 fail 0 warn 0 ✓

## .48 回归（LVM 机，同二进制）

- pool /dev/vg_nas healthy=True、注册表 41/41 ✓（E7/E8 只动 RAID 路径，LVM 无回归）

## 结论

E7（建池持久化+initramfs）与 E8（删池清理）真机全闭环：建池→重启→删池→重建→resync 中途重启→续跑→完成，
mdadm.conf/initramfs/内核/面板四方全程一致，无名称漂移、无陈旧行、无假成功。
