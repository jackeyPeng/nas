# 在线升级方案（简化版）— 设计文档

> 日期: 2026-09-19
> 状态: 已确认（用户拍板：不建 migration 引擎/组件注册表，复用幂等 setup.sh）

## 目标

升级平滑、文件安全、可回退。机制越少越可靠——全部落在已验证的现有代码路径上。

## 核心洞察

`setup.sh` 已经是「模板重生成托管配置 + 保留用户段」的幂等脚本（smb.conf Z1 托管段、
/etc/exports、vsftpd.conf、rclone-webdav.service 均此模式）。因此**不需要 migration 框架**：

```
升级 = 1. 换二进制（原子 rename + 健康检查 + .bak 回滚）
       2. setup.sh --config-only（幂等重生成全部托管配置 → 天然完成配置迁移）
```

回退 = 装回 .bak 二进制 + 重跑旧版配置（backup-config.sh tarball 兜底）。

文件安全：面板升级不碰 /data；SMB/NFS 文件服务进程独立于面板，升级期间文件访问不中断
（`--config-only` 会 restart samba/nfs，秒级中断，与 setup.sh 安装时行为一致）。

## 改动清单（4 项）

### 1. 原子替换（upgrade.sh + apply-upgrade.sh）

现状 bug：`mv /tmp/xxx /usr/local/bin/nas-panel` 跨文件系统 = copy+delete，
中途断电留半截二进制且旧文件已删。
修复：先 `install -m755 $TMP /usr/local/bin/nas-panel.new`，再 `mv` 同目录 rename（同 fs，原子）。

### 2. CLI 补签名校验（upgrade.sh）

现状 bug：CLI 只查 ELF 魔数+大小；OTA（update.go）有 SHA256+ed25519。
修复：upgrade.sh 下载 `control/latest-${ARCH}.json` manifest，
`sha256sum` 对比 + `openssl pkeyutl -verify -pubin`（内嵌同一公钥 hex）验 ed25519 签名。
manifest 拉取失败时降级为现行 ELF+大小检查并 warn（保持离线容错，与 OTA 的强校验差异明示）。

### 3. setup.sh 拆 --config-only

`setup.sh --config-only`：跳过 apt 安装/目录创建/面板安装（步骤 1/2/10），
只执行配置段（3 Samba / 4 NFS / 5 FTP / 6 WebDAV / 8 S3 / 9 防火墙），
FileBrowser（7）只在版本不匹配时重装。
upgrade.sh 与 apply-upgrade.sh 在健康检查通过后自动执行一次
`bash /opt/nas/scripts/setup.sh --config-only`。
失败不回滚二进制（面板已健康），仅提示手动重跑。

### 4. filebrowser 版本收敛 + bucket 拼写修正

现状 bug：版本三处不一致（services.go 写死 v2.63.17 ×3 处 URL、setup.sh
FILEBROWSER_VERSION=v2.63.17、upload-filebrowser.sh v2.32.0）；R2 路径拼错
`filebroswer/`（bucket key 与 Go 代码一致地错着，能跑但必须一起改）。
修复：
- 单一事实源：`web/common/versions.go` 导出 `FileBrowserVersion` 常量；
  services.go 全部 URL 引用它；setup.sh 从二进制读取
  （`strings /usr/local/bin/nas-panel | grep -m1 'filebrowser.version=v'` 或经 API），
  失败降级为脚本内变量（与常量同步维护，注释标注）。
- R2 key 修正为 `filebrowser/`（拼写正确），Go/setup.sh/upload 脚本三处同步；
  旧 key 保留一份（已装机器的 services.go 还会请求旧路径，直到升级）。

## 明确不做（砍掉）

migration 引擎、state.json、看门狗、指定版本下载、组件注册表 UI、
filebrowser 独立 OTA 通道、通道切换 UI（.env 变量即可）。

apt 组件升级（samba/nfs/vsftpd/rclone）本期不做面板集成——用户手动
`apt-get update && apt-get upgrade` + 升级前跑 backup-config.sh 即可；
后续若有需求再加「检查组件更新」按钮。

## 测试

- shellcheck 全绿（make lint-all）
- go test 全绿（versions 常量、manifest 解析若有单测）
- 测试机（.57/.48）实测：CLI 升级全流程（含 --check）、OTA 面板升级、
  --config-only 幂等重跑、人为注入坏二进制验证回滚路径
- 断电模拟不做（原子 rename 语义由 POSIX 保证）

## 实测记录（2026-09-19，.48）

- CLI：--check 验签通过（SHA256+ed25519）；完整升级 dev → v1.4.0-beta.13 成功，
  备份/替换/健康检查/git pull/--config-only 全链路 OK，7 个服务全 active，
  SMB 托管段与 NFS 导出完好
- 回滚：`.bak` mv 回滚 → dev，再升级 → beta.13，双向通过。
  注意回滚必须 `mv` 不能 `cp`（运行中二进制「文本文件忙」），脚本提示已更新
- OTA：面板内升级派发成功，apply-upgrade.sh 独立 unit 存活过面板重启，
  健康检查+配置同步完成
- 实测抓到的坑（已修，a07b511）：root 经 systemd-run 跑 `git pull` 撞
  safe.directory 所有权检查 → `git -c safe.directory=$(readlink -f /opt/nas)` 放行
- 目标机无 xxd：hex→binary 改用纯 bash printf 实现，验签在 .57/.48 均可用
