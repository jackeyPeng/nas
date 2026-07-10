#!/bin/bash
# NAS 配置备份脚本
# 用法: sudo bash backup-config.sh
# 备份所有 NAS 配置到 /data/backups/config-YYYYMMDD-HHMMSS.tar.gz
# 保留最近 5 个备份，自动清理旧的

set -e

if [ "$EUID" -ne 0 ]; then
    echo "错误: 请使用 sudo 运行"
    exit 1
fi

TIMESTAMP=$(date +%Y%m%d-%H%M%S)
BACKUP_DIR="/data/backups"
BACKUP_NAME="config-${TIMESTAMP}"
BACKUP_PATH="${BACKUP_DIR}/${BACKUP_NAME}"
TARBALL="${BACKUP_DIR}/${BACKUP_NAME}.tar.gz"

# 临时工作目录
WORK_DIR=$(mktemp -d)
trap "rm -rf $WORK_DIR" EXIT

mkdir -p "${BACKUP_PATH}"

echo "========================================="
echo "NAS 配置备份"
echo "========================================="
echo "备份时间: $(date)"
echo "备份路径: ${TARBALL}"
echo ""

# ═══════════════════════════════════════
# 1. 系统配置文件
# ═══════════════════════════════════════
echo "[1/6] 备份系统配置文件..."

CONFIG_FILES="
/etc/samba/smb.conf
/etc/exports
/etc/nfs.conf
/etc/vsftpd.conf
/etc/vsftpd.userlist
/etc/fail2ban/jail.local
/etc/rclone-htpasswd
/etc/default/minio
/etc/sudoers.d/nas-panel
/etc/filebrowser/filebrowser.db
"

mkdir -p "${BACKUP_PATH}/etc"
for file in $CONFIG_FILES; do
    if [ -f "$file" ]; then
        # 保持目录结构
        dir=$(dirname "$file")
        mkdir -p "${BACKUP_PATH}${dir}"
        cp -a "$file" "${BACKUP_PATH}${dir}/"
        echo "  ✓ $file"
    fi
done

echo "  ✓ 系统配置文件备份完成"

# ═══════════════════════════════════════
# 2. systemd 服务文件
# ═══════════════════════════════════════
echo ""
echo "[2/6] 备份 systemd 服务文件..."

SERVICE_FILES="
/etc/systemd/system/rclone-webdav.service
/etc/systemd/system/filebrowser.service
/etc/systemd/system/minio.service
/etc/systemd/system/nas-panel.service
"

mkdir -p "${BACKUP_PATH}/etc/systemd/system"
for file in $SERVICE_FILES; do
    if [ -f "$file" ]; then
        cp -a "$file" "${BACKUP_PATH}/etc/systemd/system/"
        echo "  ✓ $file"
    fi
done

echo "  ✓ systemd 服务文件备份完成"

# ═══════════════════════════════════════
# 3. NAS 项目配置
# ═══════════════════════════════════════
echo ""
echo "[3/6] 备份 NAS 项目配置..."

# .env 文件
if [ -f "/opt/nas/.env" ]; then
    mkdir -p "${BACKUP_PATH}/opt/nas"
    cp -a /opt/nas/.env "${BACKUP_PATH}/opt/nas/"
    echo "  ✓ /opt/nas/.env"
fi

# configs 目录（仓库模板）
if [ -d "/opt/nas/configs" ]; then
    mkdir -p "${BACKUP_PATH}/opt/nas"
    cp -a /opt/nas/configs "${BACKUP_PATH}/opt/nas/"
    echo "  ✓ /opt/nas/configs/"
fi

# nas-panel 二进制版本信息
if [ -f "/usr/local/bin/nas-panel" ]; then
    /usr/local/bin/nas-panel --version 2>/dev/null > "${BACKUP_PATH}/nas-panel-version.txt" || \
        echo "unknown" > "${BACKUP_PATH}/nas-panel-version.txt"
    echo "  ✓ nas-panel 版本信息"
fi

echo "  ✓ NAS 项目配置备份完成"

# ═══════════════════════════════════════
# 4. 用户数据
# ═══════════════════════════════════════
echo ""
echo "[4/6] 备份用户数据..."

# Samba 用户数据库
if [ -d "/var/lib/samba/private" ]; then
    mkdir -p "${BACKUP_PATH}/var/lib/samba"
    cp -a /var/lib/samba/private "${BACKUP_PATH}/var/lib/samba/"
    echo "  ✓ Samba 用户数据库 (passdb.tdb)"
fi

# Samba 用户列表（文本格式，方便查看）
if command -v pdbedit &>/dev/null; then
    pdbedit -L > "${BACKUP_PATH}/samba-users.txt" 2>/dev/null || true
    echo "  ✓ Samba 用户列表"
fi

# vsftpd 用户列表
if [ -f "/etc/vsftpd.userlist" ]; then
    cp -a /etc/vsftpd.userlist "${BACKUP_PATH}/"
    echo "  ✓ FTP 用户白名单"
fi

# 系统用户列表（UID >= 1000 的非系统用户）
grep -E "^(.+:.*):/bin/(bash|sh)" /etc/passwd | awk -F: '$3 >= 1000 {print}' > "${BACKUP_PATH}/system-users.txt"
echo "  ✓ 系统用户列表"

# crontab
NAS_USER="${SUDO_USER:-$USER}"
if [ -n "$NAS_USER" ] && [ "$NAS_USER" != "root" ]; then
    crontab -u "$NAS_USER" -l > "${BACKUP_PATH}/crontab.txt" 2>/dev/null || true
    echo "  ✓ crontab (${NAS_USER})"
fi

# 组信息
getent group > "${BACKUP_PATH}/groups.txt"
echo "  ✓ 组信息"

echo "  ✓ 用户数据备份完成"

# ═══════════════════════════════════════
# 5. 系统状态快照
# ═══════════════════════════════════════
echo ""
echo "[5/6] 记录系统状态快照..."

{
    echo "NAS 配置备份快照"
    echo "备份时间: $(date)"
    echo "主机名: $(hostname)"
    echo "系统: $(cat /etc/debian_version 2>/dev/null || echo unknown)"
    echo "内核: $(uname -r)"
    echo "用户: ${NAS_USER}"
    echo ""
    echo "=== 磁盘使用 ==="
    df -h
    echo ""
    echo "=== 服务状态 ==="
    for svc in smbd nmbd nfs-kernel-server vsftpd rclone-webdav filebrowser minio fail2ban nas-panel; do
        printf "  %-22s %s\n" "$svc:" "$(systemctl is-active $svc 2>/dev/null)"
    done
    echo ""
    echo "=== 挂载点 ==="
    mount | grep -E "/data|/dev/sd|/dev/nvme"
    echo ""
    echo "=== UFW 状态 ==="
    ufw status 2>/dev/null | head -20
} > "${BACKUP_PATH}/system-snapshot.txt"

echo "  ✓ 系统状态快照已记录"

# ═══════════════════════════════════════
# 6. 打包 + 清理旧备份
# ═══════════════════════════════════════
echo ""
echo "[6/6] 打包备份..."

cd "${BACKUP_DIR}"
tar czf "${BACKUP_NAME}.tar.gz" "${BACKUP_NAME}"
rm -rf "${BACKUP_NAME}"

BACKUP_SIZE=$(ls -lh "${TARBALL}" | awk '{print $5}')
echo "  ✓ 备份已打包: ${TARBALL} (${BACKUP_SIZE})"

# 清理旧备份，保留最近 5 个
BACKUP_COUNT=$(ls -1 "${BACKUP_DIR}"/config-*.tar.gz 2>/dev/null | wc -l)
if [ "$BACKUP_COUNT" -gt 5 ]; then
    echo ""
    echo "  清理旧备份 (保留最近 5 个)..."
    ls -1t "${BACKUP_DIR}"/config-*.tar.gz | tail -n +6 | while read old_file; do
        rm -f "$old_file"
        echo "  删除: $(basename $old_file)"
    done
fi

echo ""
echo "========================================="
echo "备份完成!"
echo "========================================="
echo "备份文件: ${TARBALL}"
echo "备份大小: ${BACKUP_SIZE}"
echo "现有备份数: $(ls -1 "${BACKUP_DIR}"/config-*.tar.gz 2>/dev/null | wc -l)"
echo ""
echo "恢复命令: sudo bash /opt/nas/scripts/restore-config.sh ${TARBALL}"
echo "========================================="
