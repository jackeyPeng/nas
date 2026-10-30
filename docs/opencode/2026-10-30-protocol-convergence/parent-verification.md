# Parent 验证纪要 — 协议收敛轮（2026-10-30，第五轮双机验收）

## opencode 产出（4 commits）

c827db7 setup.sh 新装默认关 / a833922 install.sh 横幅与验证口径 / 8e29e7c 后端 on-off 开关+optional 门禁+计数与注册表三态 / c0e15cf 前端服务页卡片化。
go build/vet/test 全过，i18n 1414 key 对齐，注册表 41 项锁定。详见 opencode-report.md。

## Parent 复核抓出 4 个 bug（全部真机实锤后由 parent 直修）

1. **9fa8f37 — FRESH_INSTALL 判定在一键路径完全失效**（功能失效级）：install.sh 第 3 步先创建 .env 再调 setup.sh，setup.sh 按「.env 存在性」判定新装/升级恒得升级态 → 新装机仍会全量开启四协议。opencode 报告如实记录了「install.sh 先建 .env」的事实但推导出相反结论。修复：install.sh 用 REPO_ENV_EXISTED（与「保留已改密码」同源信号）判定后经 env 显式传入，setup.sh 未收到时回退本地判定。
2. **1d36b07 — set -e 下裸赋值 `state=$(systemctl is-active x)` 对 inactive 服务退出码 3 中止脚本**：新装机四协议全关必现，安装停在服务状态段（.48 首装实锤，靠 F-2 保全日志 /tmp/nas-install-setup-*.log 定位）。修复：`|| true`。
3. **f9bbafb — systemd alias 单元双坑**：nfs-kernel-server 是 nfs-server 的 alias，`is-enabled` 恒报 "alias" 且退出码 0 → 注册表把默认关闭判成 fail（40/41+1warn 实锤）、升级路径会把用户关着的 NFS 误重开；同时 install.sh 验证循环 `|| echo 兜底` 对非零退出码拼出双行值（"inactive\ninactive"）致 ○ 分支等值比较必假走 ✗。修复：common.ResolveServiceEnabled（show -p Id 解析规范单元再查），dashboard/registry/setup.sh/install.sh 四处统一。
4. **cb1c0b4 — `systemctl restart` 对 stopped 单元是「启动」语义**：首登改密路径无条件 restart rclone-s3 把默认关闭的 S3 偷偷拉起（.48 实锤，注册表如实报 active-but-not-enabled warn）。FTP 白名单增删、HTTPS 开关同款共 5 处。修复：common.RestartIfActive 守卫（unit/配置文件照常落盘，下次开启生效）。

教训沉淀：默认关闭态是一类新的产品状态，所有「无条件 restart/enable」的历史代码路径都变成潜在复活点；alias 单元与 systemctl 非零退出码是 shell 判定的两个通用坑。

## 第五轮双机验收（最终代码 cb1c0b4，错误账本：零）

**.48（LVM merge 4×50G→200G）**：一次装成零错误；新默认态正确（4 核心 active + 4 协议 ○ 默认关闭、横幅只印 Samba+提示、UNC `\\IP\public` 正确）；首登四件套 + **改密后 S3 不复活回归**；建池后注册表 41/41（默认关闭=pass 态）；开关往返（vsftpd on/off、NFS alias on 后规范单元 enabled、smbd off 门禁 400）；全开后仪表盘口径 7/7、套件 38/38、AI E2E 31 项 ALL PASS、F2 E2E ALL PASS、F1 双向零漂移；Chrome 服务页卡片视觉确认（2×2 网格、用途/开关/徽章/真实 IP 地址/凭据/橙色风险提示齐全）+ **UI 实点开关全链验证**（点击→原生 confirm→后端 vsftpd inactive/disabled→再点开恢复）。

**.57（RAID1 2×50G）**：一次装成零错误；四件套 + S3 回归过；E7 ARRAY 行落盘；**resync 42.6% 时重启 → md0 装配、续跑至 91.9%、面板进度实时跟随（90.5%）、协议默认关闭态跨重启持久化**；resync 完成后注册表 41/41（协议关/开两态均 41/41）；API 全开四协议 → 套件 38/38、AI E2E ALL PASS、F2 E2E ALL PASS、F1 零漂移（delete_data=true 版无孤儿 home）；Chrome 三页全绿（无漂移、RAID 健康、无 md127、协议卡片+7 运行中徽章+风险文案）。

## 遗留观察（非阻塞）

- svcToggle 关闭确认用原生 confirm()（阻塞式、样式不可控），后续可换面板内确认卡（低优先级）。
- acceptance-suite 尚无「默认关闭断言 + 开关往返」段（本轮为跑套件手动全开协议）；F1 脚本已改 delete_data=true 版（scratch，需回灌 skill 资产时一并考虑）。
- 存储页对孤儿 home 目录（delete 不带 delete_data 的遗留）按「未纳管目录兜底」显示——设计行为，验收 ghost 断言需区分。
