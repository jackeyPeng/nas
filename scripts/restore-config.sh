#!/bin/bash
# NAS 配置恢复脚本
# 用法: sudo bash restore-config.sh <备份文件路径>
# 从指定备份恢复所有 NAS 配置

set -e

if [ "$EUID" -ne 0 ]; then
    echo "错误: 请使用 sudo 运行"
    exit 1
fi

BACKUP_FILE="$1"

if [ -z "$BACKUP_FILE" ]; then
    # 交互式选择备份
    BACKUP_DIR="/data/backups"
    echo "可用备份列表:"
    echo ""
    ls -lht "${BACKUP_DIR}"/config-*.tar.gz 2>/dev/null | head -10
    echo ""
    read -p "输入要恢复的备份文件名（如 config-20260710-120000.tar.gz）: " FILENAME
    BACKUP_FILE="${BACKUP_DIR}/${FILENAME}"
fi

if [ ! -f "$BACKUP_FILE" ]; then
    echo "错误: 备份文件不存在: $BACKUP_FILE"
    exit 1
fi

WORK_DIR=$(mktemp -d)
trap "rm -rf $WORK_DIR" EXIT

echo "========================================="
echo "NAS 配置恢复"
echo "========================================="
echo "备份文件: $BACKUP_FILE"
echo "恢复时间: $(date)"
echo ""

# 确认
read -p "⚠️ 此操作将覆盖当前所有 NAS 配置，确定要继续吗？(输入 yes 确认): " confirm
if [ "$confirm" != "yes" ]; then
    echo "已取消"
    exit 0
fi

echo ""

# ═══════════════════════════════════════
# 1. 解压备份
# ═══════════════════════════════════════
echo "[1/7] 解压备份..."
tar xzf "$BACKUP_FILE" -C "$WORK_DIR"
BACKUP_ROOT=$(find "$WORK_DIR" -maxdepth 1 -type d | tail -1)
echo "  ✓ 解压到: $BACKUP_ROOT"

# 显示备份信息
if [ -f "$BACKUP_ROOT/system-snapshot.txt" ]; then
    echo ""
    echo "=== 备份信息 ==="
    head -6 "$BACKUP_ROOT/system-snapshot.txt"
    echo ""
fi

# ═══════════════════════════════════════
# 2. 停止所有服务
# ═══════════════════════════════════════
echo "[2/7] 停止所有 NAS 服务..."
SERVICES="nas-panel smbd nmbd nfs-kernel-server vsftpd rclone-webdav filebrowser minio fail2ban"
for svc in $SERVICES; do
    if systemctl is-active --quiet "$svc" 2>/dev/null; then
        systemctl stop "$svc" 2>/dev/null || true
        echo "  停止 $svc"
    fi
done
echo "  ✓ 服务已停止"

# ═══════════════════════════════════════
# 3. 恢复系统配置文件
# ═══════════════════════════════════════
echo ""
echo "[3/7] 恢复系统配置文件..."

if [ -d "$BACKUP_ROOT/etc" ]; then
    cd "$BACKUP_ROOT"
    find etc -type f | while read file; do
        target="/$file"
        target_dir=$(dirname "$target")
        mkdir -p "$target_dir"
        cp -a "$file" "$target"
        echo "  ✓ $target"
    done
    echo "  ✓ 系统配置文件已恢复"
else
    echo "  ⚠ 无系统配置文件"
fi

# ═══════════════════════════════════════
# 4. 恢复 NAS 项目配置
# ═══════════════════════════════════════
echo ""
echo "[4/7] 恢复 NAS 项目配置..."

if [ -f "$BACKUP_ROOT/opt/nas/.env" ]; then
    mkdir -p /opt/nas
    cp -a "$BACKUP_ROOT/opt/nas/.env" /opt/nas/
    echo "  ✓ /opt/nas/.env"
fi

if [ -d "$BACKUP_ROOT/opt/nas/configs" ]; then
    cp -a "$BACKUP_ROOT/opt/nas/configs" /opt/nas/
    echo "  ✓ /opt/nas/configs/"
fi

echo "  ✓ NAS 项目配置已恢复"

# ═══════════════════════════════════════
# 5. 恢复 Samba 用户数据库
# ═══════════════════════════════════════
echo ""
echo "[5/7] 恢复 Samba 用户数据库..."

if [ -d "$BACKUP_ROOT/var/lib/samba/private" ]; then
    mkdir -p /var/lib/samba
    cp -a "$BACKUP_ROOT/var/lib/samba/private" /var/lib/samba/
    echo "  ✓ Samba 用户数据库已恢复"
else
    echo "  ⚠ 无 Samba 用户数据库"
fi

# ═══════════════════════════════════════
# 6. 恢复 crontab
# ═══════════════════════════════════════
echo ""
echo "[6/7] 恢复 crontab..."

NAS_USER="${SUDO_USER:-$USER}"
if [ -f "$BACKUP_ROOT/crontab.txt" ] && [ -n "$NAS_USER" ] && [ "$NAS_USER" != "root" ]; then
    crontab -u "$NAS_USER" "$BACKUP_ROOT/crontab.txt" 2>/dev/null || true
    echo "  ✓ crontab 已恢复 (${NAS_USER})"
else
    echo "  ⚠ 无 crontab 备份"
fi

# ═══════════════════════════════════════
# 7. 重启服务
# ═══════════════════════════════════════
echo ""
echo "[7/7] 重启服务..."

systemctl daemon-reload

for svc in smbd nmbd nfs-kernel-server vsftpd rclone-webdav filebrowser minio fail2ban nas-panel; do
    systemctl enable "$svc" 2>/dev/null || true
    systemctl start "$svc" 2>/dev/null || true
    status=$(systemctl is-active "$svc" 2>/dev/null)
    printf "  %-22s %s\n" "$svc:" "$status"
done

echo "  ✓ 服务已重启"

# ═══════════════════════════════════════
# 完成
# ═══════════════════════════════════════
echo ""
echo "========================================="
echo "配置恢复完成!"
echo "========================================="
echo "备份来源: $BACKUP_FILE"
echo ""
echo "建议: 检查各服务状态和配置是否正确"
echo "========================================="
