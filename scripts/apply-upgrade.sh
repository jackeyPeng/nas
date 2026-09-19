#!/bin/bash
# apply-upgrade.sh — 面板升级的应用阶段（独立进程，跨面板重启存活）
# 用法: bash apply-upgrade.sh <已校验的新二进制>
#
# 此脚本由面板在「下载 + 签名校验通过」后派发。
# 职责: 备份旧二进制 → 原子替换 → 重启 → 健康检查 → 失败自动回滚。
set -euo pipefail

NEW="$1"
PANEL="/usr/local/bin/nas-panel"
LOG="/tmp/nas-upgrade.log"

log() { echo "[$(date '+%H:%M:%S')] $*" >> "$LOG"; }

if [ ! -f "$NEW" ]; then
    log "错误: 找不到新二进制 $NEW"
    exit 1
fi

# 1. 备份旧二进制
BACKUP="${PANEL}.bak.$(date +%Y%m%d%H%M%S)"
cp -a "$PANEL" "$BACKUP"
log "已备份旧二进制 → $BACKUP"

# 2. 原子替换 + 重启
# 不能直接 mv $NEW（/tmp 与 /usr/local/bin 跨文件系统时 mv=copy+delete，
# 中途断电会留下半截二进制且旧文件已删）。同目录 .new + rename 才是原子的。
install -m 0755 "$NEW" "${PANEL}.new"
mv "${PANEL}.new" "$PANEL"
rm -f "$NEW"
systemctl restart nas-panel
log "已替换二进制并重启"

# 3. 健康检查（最多 30 秒）
for i in $(seq 1 15); do
    sleep 2
    if systemctl is-active --quiet nas-panel && \
       curl -sf --max-time 5 http://localhost:8090/api/version >/dev/null 2>&1; then
        log "升级成功，面板健康"
        # 4. 配置迁移：git pull 更新模板 + setup.sh --config-only 幂等重生成托管配置
        # 失败不回滚二进制（面板已健康），只记日志提示手动重跑
        if [ -d /opt/nas/.git ]; then
            git -C /opt/nas pull --ff-only >>"$LOG" 2>&1 || log "git pull 失败，用现有模板继续"
        fi
        NAS_USER_ENV=$(awk -F= '/^Environment=NAS_USER=/{print $3; exit}' /etc/systemd/system/nas-panel.service 2>/dev/null | tr -d '"')
        if [ -f /opt/nas/scripts/setup.sh ]; then
            if NAS_USER="$NAS_USER_ENV" bash /opt/nas/scripts/setup.sh --config-only >/tmp/nas-config-sync.log 2>&1; then
                log "托管配置已同步到新版模板"
            else
                log "配置同步失败（不影响升级），日志 /tmp/nas-config-sync.log，可手动重跑 setup.sh --config-only"
            fi
        fi
        exit 0
    fi
done

# 5. 回滚
log "新版本启动失败，回滚到旧版本"
mv "$BACKUP" "$PANEL"
chmod +x "$PANEL"
systemctl restart nas-panel
log "已回滚"
exit 1
