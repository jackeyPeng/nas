#!/bin/bash
# NAS 清除脚本
# 用于将系统恢复到安装前的状态
# 用法: sudo ./cleanup.sh [--keep-data]
#
# 选项:
#   --keep-data    保留 /data 目录（默认会删除）

set -e

# 检查 root 权限
if [ "$EUID" -ne 0 ]; then
    echo "错误: 请使用 sudo 运行此脚本"
    exit 1
fi

KEEP_DATA=false
if [ "$1" = "--keep-data" ]; then
    KEEP_DATA=true
fi

echo "========================================="
echo "NAS 清除脚本"
echo "========================================="
echo ""

if [ "$KEEP_DATA" = true ]; then
    echo "⚠️  将保留 /data 目录"
else
    echo "⚠️  将删除 /data 目录及所有数据"
fi

echo ""
read -p "确定要继续吗？(输入 yes 确认): " confirm
if [ "$confirm" != "yes" ]; then
    echo "已取消"
    exit 0
fi

echo ""

# ==================== [1/7] 停止所有 NAS 服务 ====================
echo "[1/7] 停止所有 NAS 服务..."
SERVICES="smbd nmbd nfs-kernel-server vsftpd rclone-webdav filebrowser minio fail2ban nas-panel"
for svc in $SERVICES; do
    if systemctl is-active --quiet "$svc" 2>/dev/null; then
        systemctl stop "$svc"
        echo "  停止 $svc"
    fi
    if systemctl is-enabled --quiet "$svc" 2>/dev/null; then
        systemctl disable "$svc" 2>/dev/null
        echo "  禁用 $svc"
    fi
done
echo "  ✓ 服务已停止"

# ==================== [2/7] 删除 systemd 服务文件 ====================
echo ""
echo "[2/7] 删除 systemd 服务文件..."
SERVICE_FILES="/etc/systemd/system/rclone-webdav.service /etc/systemd/system/filebrowser.service /etc/systemd/system/minio.service /etc/systemd/system/nas-panel.service"
for file in $SERVICE_FILES; do
    if [ -f "$file" ]; then
        rm -f "$file"
        echo "  删除 $file"
    fi
done
systemctl daemon-reload
echo "  ✓ 服务文件已删除"

# ==================== [3/7] 删除配置文件 ====================
echo ""
echo "[3/7] 删除配置文件..."
CONFIG_FILES="
/etc/samba/smb.conf
/etc/exports
/etc/nfs.conf
/etc/vsftpd.conf
/etc/vsftpd.userlist
/etc/fail2ban/jail.local
/etc/filebrowser/filebrowser.db
/etc/rclone-htpasswd
/etc/default/minio
/var/log/vsftpd.log
/var/log/filebrowser.log
"
for file in $CONFIG_FILES; do
    if [ -f "$file" ]; then
        rm -f "$file"
        echo "  删除 $file"
    fi
done
if [ -d "/etc/filebrowser" ]; then
    rm -rf /etc/filebrowser
    echo "  删除 /etc/filebrowser/"
fi
echo "  ✓ 配置文件已删除"

# ==================== [4/7] 删除二进制文件 ====================
echo ""
echo "[4/7] 删除二进制文件..."
BINARIES="/usr/local/bin/filebrowser /usr/local/bin/minio /usr/local/bin/nas-panel"
for bin in $BINARIES; do
    if [ -f "$bin" ]; then
        rm -f "$bin"
        echo "  删除 $bin"
    fi
done
echo "  ✓ 二进制文件已删除"

# ==================== [5/7] 卸载软件包 ====================
echo ""
echo "[5/7] 卸载软件包..."
PACKAGES="samba smbclient nfs-kernel-server nfs-common vsftpd rclone fail2ban ufw smartmontools unattended-upgrades apache2-utils"
DEBIAN_FRONTEND=noninteractive apt-get purge -y $PACKAGES 2>/dev/null || true
DEBIAN_FRONTEND=noninteractive apt-get autoremove -y 2>/dev/null || true
echo "  ✓ 软件包已卸载"

# ==================== [6/7] 清理数据目录 ====================
echo ""
echo "[6/7] 清理数据目录..."
if [ "$KEEP_DATA" = false ]; then
    if [ -d "/data" ]; then
        rm -rf /data
        echo "  删除 /data/"
    fi
else
    echo "  保留 /data/ (--keep-data)"
fi
echo "  ✓ 数据目录已处理"

# ==================== [7/7] 清理其他文件 ====================
echo ""
echo "[7/7] 清理其他文件..."
# 清理 Samba 用户（自动获取部署时的用户）
NAS_USER="${SUDO_USER:-$USER}"
if [ -n "$NAS_USER" ] && [ "$NAS_USER" != "root" ]; then
    if command -v smbpasswd &>/dev/null; then
        smbpasswd -x "$NAS_USER" 2>/dev/null || true
        echo "  删除 Samba 用户 $NAS_USER"
    fi
fi

# 清理 /opt/nas 软链接
if [ -L "/opt/nas" ]; then
    rm -f /opt/nas
    echo "  删除 /opt/nas 软链接"
fi

# 清理 UFW（如果已重新安装）
if command -v ufw &>/dev/null; then
    ufw --force reset 2>/dev/null || true
    echo "  重置 UFW 规则"
fi

# 清理 sudoers
if [ -f /etc/sudoers.d/nas-panel ]; then
    rm -f /etc/sudoers.d/nas-panel
    echo "  删除 /etc/sudoers.d/nas-panel"
fi

# 清理监控 cron
NAS_USER="${SUDO_USER:-$USER}"
if [ -n "$NAS_USER" ] && [ "$NAS_USER" != "root" ]; then
    crontab -u "$NAS_USER" -l 2>/dev/null | grep -v "monitor.sh" | crontab -u "$NAS_USER" - 2>/dev/null || true
    echo "  删除监控 cron"
fi

# 清理告警状态
rm -rf /var/lib/nas-monitor 2>/dev/null || true

# 清理日志
rm -f /var/log/samba/log.* 2>/dev/null || true
rm -rf /var/log/samba 2>/dev/null || true
rm -rf /var/lib/samba 2>/dev/null || true
rm -rf /var/lib/nfs 2>/dev/null || true
rm -rf /var/lib/vsftpd 2>/dev/null || true

echo "  ✓ 其他文件已清理"

# ==================== 完成 ====================
echo ""
echo "========================================="
echo "NAS 清除完成!"
echo "========================================="
echo ""
echo "已清除的内容:"
echo "  - 所有 NAS 服务（Samba、NFS、FTP、WebDAV、FileBrowser、MinIO）"
echo "  - systemd 服务文件"
echo "  - 配置文件"
echo "  - 二进制文件（filebrowser、minio）"
echo "  - apt 软件包"
if [ "$KEEP_DATA" = false ]; then
    echo "  - /data 数据目录"
else
    echo "  - /data 数据目录已保留"
fi
echo ""
echo "系统已恢复到安装前的状态"
echo "========================================="
