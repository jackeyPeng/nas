# F-1/F-2 真机验证纪要（parent 独立验证，2026-10-29）

## F-1 复现与根因（测试机48，修复前脚本）

- 用户级全流程部署首次尝试：install.sh 自身包阶段成功（journal 11:21:03 update、11:21:06 env install），
  随后 setup.sh [1/10] 静默死亡，安装中止
- journal 实锤：11:22:24 systemd 拉起 apt-daily-upgrade.service，11:22:55 apt-helper 报
  networkd-wait-online 错误——setup.sh 的 `apt-get update` 恰在此窗口撞锁，set -e 静默退出
- 4 分钟后单独重跑 setup.sh EXIT=0 全过 → 纯瞬时冲突定性成立
- 修复后（619ce19）：测试机57 全新安装一次通过，零 apt 重试触发（加固为保险性质，未再复现撞锁）

## F-2 复现（测试机48）

- 失败路径实际行为：install.log 只见 "[1/10] 安装基础软件包..." 后直接 "✗ setup.sh 执行失败"，
  apt 真实错误行被 grep 过滤器丢弃；`rm -f "$SETUP_LOG"` 销毁证据，提示却称"完整日志已保留"
- 修复后行为（代码审查确认）：失败时日志移至 /tmp/nas-install-setup-<ts>.log、提示给出真实路径、
  追加未过滤 tail -30、过滤器补 ^E: 行

## sudoers 形态核对（修复评审时确认）

- 机器上 fm 为家庭管理员（无限制 sudo），install/setup 的 apt 形态不受 nas-panel 白名单约束；
  白名单仅管面板进程的逐条 sudo 调用
- `/usr/bin/apt-get update` 白名单为精确无参形态、无 /usr/bin/env 条目——本次修复未改动面板
  调用面，install 路径形态与修复前一致（+尾部通配内的 -o 参数），无白名单回归

## .48/.57 双机验收结论（本轮）

- .48（LVM merge 4×50G→200G）：8 服务 active、首登四件套、注册表 41/41、套件 38/38、
  AI E2E + F2 E2E ALL PASS、F1 零漂移、Chrome 三页无幽灵无告警
- .57（RAID1 2×50G）：修复后脚本一次安装通过、8 服务、四件套、注册表 41/41、套件 38/38、
  AI E2E ALL PASS、F1 零漂移、Chrome 无 md127 无幽灵；**resync 40% 时重启 → md0 装配、
  续跑至 90.9%、面板实时显示 85.1% 同步进度**（E7 持久化 + sysfs 链联合实证）
