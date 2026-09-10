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
chmod +x "$NEW"
mv "$NEW" "$PANEL"
systemctl restart nas-panel
log "已替换二进制并重启"

# 3. 健康检查（最多 30 秒）
for i in $(seq 1 15); do
    sleep 2
    if systemctl is-active --quiet nas-panel && \
       curl -sf --max-time 5 http://localhost:8090/api/version >/dev/null 2>&1; then
        log "升级成功，面板健康"
        exit 0
    fi
done

# 4. 回滚
log "新版本启动失败，回滚到旧版本"
mv "$BACKUP" "$PANEL"
chmod +x "$PANEL"
systemctl restart nas-panel
log "已回滚"
exit 1
