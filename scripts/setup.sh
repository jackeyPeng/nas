#!/bin/bash
# NAS 一键部署脚本
# 用于在新机器上部署完整的 NAS 系统
# 用法: sudo ./setup.sh
#
# 包含服务: Samba, NFS, FTP, WebDAV, FileBrowser, MinIO, NAS Web Panel
# 安全: Fail2ban, UFW 防火墙, unattended-upgrades
# 10步部署, 自动检测当前用户

set -e

# 检查 root 权限
if [ "$EUID" -ne 0 ]; then
    echo "错误: 请使用 sudo 运行此脚本"
    exit 1
fi

echo "========================================="
echo "家用 NAS 一键部署脚本"
echo "========================================="
echo ""

# ==================== 配置变量 ====================
# 自动获取当前用户（执行 sudo 的用户）
NAS_USER="${SUDO_USER:-$USER}"
if [ -z "$NAS_USER" ] || [ "$NAS_USER" = "root" ]; then
    echo "错误: 无法自动检测用户名，请使用 sudo 运行（而非直接以 root 身份）"
    exit 1
fi
DATA_DIR="/data"
NAS_DIR="/opt/nas"
FILEBROWSER_VERSION="v2.63.17"

# 获取脚本所在目录的绝对路径（仓库根目录）
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# ==================== 升级前自动备份 ====================
if [ -d "$DATA_DIR" ] && [ -f "/etc/samba/smb.conf" ]; then
    echo "检测到已有 NAS 配置，执行升级前备份..."
    bash "$SCRIPT_DIR/scripts/backup-config.sh" 2>/dev/null || echo "  ⚠ 备份失败，继续部署"
    echo ""
fi

# 从 .env 文件读取密码
ENV_FILE="$SCRIPT_DIR/.env"
if [ ! -f "$ENV_FILE" ]; then
    echo "错误: 未找到 $ENV_FILE"
    echo ""
    echo "请先创建配置文件："
    echo "  cp $SCRIPT_DIR/.env.example $ENV_FILE"
    echo "  nano $ENV_FILE"
    echo ""
    echo "必填项："
    echo "  NAS_PASS=你的密码（至少12位，用于所有服务）"
    echo ""
    echo "可选项："
    echo "  GITEE_TOKEN=你的Gitee token（仅创建Release时需要）"
    exit 1
fi
NAS_PASS=$(grep '^NAS_PASS=' "$ENV_FILE" | cut -d'=' -f2-)
if [ -z "$NAS_PASS" ]; then
    echo "错误: .env 文件中未设置 NAS_PASS"
    exit 1
fi
if [ ${#NAS_PASS} -lt 12 ]; then
    echo "错误: NAS_PASS 至少需要 12 位（FileBrowser 强制要求）"
    exit 1
fi

# ==================== 确保 /opt/nas 软链接存在 ====================

if [ ! -e "$NAS_DIR" ]; then
    echo "创建软链接: $NAS_DIR -> $SCRIPT_DIR"
    mkdir -p /opt
    ln -sfn "$SCRIPT_DIR" "$NAS_DIR"
fi

# 通用下载函数：尝试多个源，第一个成功就返回
download_file() {
    local dest="$1"
    shift
    for url in "$@"; do
        echo "    尝试下载: $url"
        if curl -fsSL --connect-timeout 15 --max-time 120 -o "$dest" "$url" 2>/dev/null; then
            if [ -s "$dest" ] && ! head -c 20 "$dest" | grep -q "<!DOCTYPE\|<html\|Not Found"; then
                echo "    ✓ 下载成功"
                return 0
            fi
        fi
        echo "    ✗ 失败，尝试下一个源..."
    done
    echo "    ✗ 所有下载源均失败"
    return 1
}

# ==================== [1/9] 安装基础软件包 ====================
echo "[1/9] 安装基础软件包..."
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y \
    curl samba nfs-kernel-server vsftpd rclone \
    fail2ban ufw smartmontools unattended-upgrades \
    smbclient nfs-common
echo "  ✓ 软件包安装完成"

# ==================== [2/9] 创建数据目录结构 ====================
echo ""
echo "[2/9] 创建数据目录结构..."
mkdir -p "$DATA_DIR"/{shared,media/{movies,tv,music},documents,photos,backups,downloads,private/$NAS_USER}
mkdir -p "$DATA_DIR"/minio
chown -R "$NAS_USER:$NAS_USER" "$DATA_DIR"
chmod 755 "$DATA_DIR"
chmod 775 "$DATA_DIR"/{shared,media,documents,photos}
echo "  ✓ 目录结构创建完成"

# ==================== [3/9] 配置 Samba ====================
echo ""
echo "[3/9] 配置 Samba..."
if [ -f "$NAS_DIR/configs/smb.conf" ]; then
    sed "s/__NAS_USER__/$NAS_USER/g" "$NAS_DIR/configs/smb.conf" > /etc/samba/smb.conf
else
    echo "  警告: 未找到 $NAS_DIR/configs/smb.conf，使用默认配置"
fi
# 设置系统用户密码
echo "$NAS_USER:$NAS_PASS" | chpasswd
(echo "$NAS_PASS"; echo "$NAS_PASS") | smbpasswd -a "$NAS_USER" -s
smbpasswd -e "$NAS_USER"
systemctl enable smbd nmbd
systemctl restart smbd nmbd
echo "  ✓ Samba 配置完成"

# ==================== [4/9] 配置 NFS ====================
echo ""
echo "[4/9] 配置 NFS..."
if [ -f "$NAS_DIR/configs/nfs.conf" ]; then
    cp "$NAS_DIR/configs/nfs.conf" /etc/nfs.conf
fi
if [ -f "$NAS_DIR/configs/exports" ]; then
    cp "$NAS_DIR/configs/exports" /etc/exports
else
    cat > /etc/exports << EXPEOF
$DATA_DIR/shared    192.168.0.0/16(rw,sync,no_subtree_check,no_root_squash)
$DATA_DIR/media     192.168.0.0/16(ro,sync,no_subtree_check)
$DATA_DIR/documents 192.168.0.0/16(rw,sync,no_subtree_check,no_root_squash)
$DATA_DIR/photos    192.168.0.0/16(rw,sync,no_subtree_check,no_root_squash)
$DATA_DIR/backups   192.168.0.0/16(rw,sync,no_subtree_check,no_root_squash)
EXPEOF
fi
exportfs -a
systemctl enable nfs-kernel-server
systemctl restart nfs-kernel-server
echo "  ✓ NFS 配置完成"

# ==================== [5/9] 配置 FTP ====================
echo ""
echo "[5/9] 配置 FTP (vsftpd)..."
if [ -f "$NAS_DIR/configs/vsftpd.conf" ]; then
    cp "$NAS_DIR/configs/vsftpd.conf" /etc/vsftpd.conf
else
    cat > /etc/vsftpd.conf << FTPEOF
listen=YES
listen_ipv6=NO
anonymous_enable=NO
local_enable=YES
write_enable=YES
local_umask=022
dirmessage_enable=YES
use_localtime=YES
xferlog_enable=YES
connect_from_port_20=YES
chroot_local_user=YES
allow_writeable_chroot=YES
user_sub_token=\$USER
local_root=$DATA_DIR/private/\$USER
pasv_enable=YES
pasv_min_port=30000
pasv_max_port=31000
userlist_enable=YES
userlist_file=/etc/vsftpd.userlist
userlist_deny=NO
FTPEOF
fi
echo "$NAS_USER" > /etc/vsftpd.userlist
touch /var/log/vsftpd.log
chmod 640 /var/log/vsftpd.log
systemctl enable vsftpd
systemctl restart vsftpd
echo "  ✓ FTP 配置完成"

# ==================== [6/9] 配置 WebDAV ====================
echo ""
echo "[6/9] 配置 WebDAV (rclone)..."
# 安装 htpasswd 工具（用于 WebDAV 认证）
apt-get install -y apache2-utils 2>/dev/null || true
# 生成 htpasswd 文件（使用 bcrypt 哈希，兼容性好）
htpasswd -cb /etc/rclone-htpasswd "$NAS_USER" "$NAS_PASS"
chown "$NAS_USER:$NAS_USER" /etc/rclone-htpasswd
# 创建 systemd 服务文件（使用 htpasswd 认证，避免 rclone obscure hash 兼容性问题）
cat > /etc/systemd/system/rclone-webdav.service << WDEOF
[Unit]
Description=Rclone WebDAV Server
After=network.target

[Service]
Type=simple
User=$NAS_USER
ExecStart=/usr/bin/rclone serve webdav $DATA_DIR --addr :8080 --htpasswd /etc/rclone-htpasswd
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
WDEOF
systemctl daemon-reload
systemctl enable rclone-webdav
systemctl restart rclone-webdav
echo "  ✓ WebDAV 配置完成"

# ==================== [7/9] 安装 FileBrowser ====================
echo ""
echo "[7/9] 安装 FileBrowser..."
if command -v filebrowser &>/dev/null; then
    echo "  FileBrowser 已安装，跳过"
else
    download_file /tmp/filebrowser.tar.gz \
        "https://file.abwen.com/filebroswer/linux-amd64-filebrowser.tar.gz" \
        "https://github.com/filebrowser/filebrowser/releases/download/${FILEBROWSER_VERSION}/linux-amd64-filebrowser.tar.gz" \
        "https://ghfast.top/https://github.com/filebrowser/filebrowser/releases/download/${FILEBROWSER_VERSION}/linux-amd64-filebrowser.tar.gz"
    cd /tmp && tar xzf filebrowser.tar.gz && mv filebrowser /usr/local/bin/ && cd -
    chmod +x /usr/local/bin/filebrowser
fi

mkdir -p /etc/filebrowser
if [ ! -f /etc/filebrowser/filebrowser.db ]; then
    filebrowser config init --database /etc/filebrowser/filebrowser.db 2>/dev/null || true
fi
filebrowser config set \
    --database /etc/filebrowser/filebrowser.db \
    --address 0.0.0.0 \
    --port 8081 \
    --root "$DATA_DIR" \
    --log /var/log/filebrowser.log 2>/dev/null || true
filebrowser users add "$NAS_USER" "$NAS_PASS" \
    --database /etc/filebrowser/filebrowser.db \
    --perm.admin 2>/dev/null || true

cat > /etc/systemd/system/filebrowser.service << 'FBEOF'
[Unit]
Description=File Browser
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/filebrowser --database /etc/filebrowser/filebrowser.db
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
FBEOF
systemctl daemon-reload
systemctl enable filebrowser
systemctl restart filebrowser
echo "  ✓ FileBrowser 配置完成"

# ==================== [8/9] 安装 MinIO ====================
echo ""
echo "[8/9] 安装 MinIO..."
if command -v minio &>/dev/null; then
    echo "  MinIO 已安装，跳过"
else
    download_file /usr/local/bin/minio \
        "https://file.abwen.com/minio/minio.linux-amd64.RELEASE.2025-09-07T16-13-09Z" \
        "https://dl.min.io/server/minio/release/linux-amd64/minio" \
        "https://ghfast.top/https://dl.min.io/server/minio/release/linux-amd64/minio"
    chmod +x /usr/local/bin/minio
fi

mkdir -p "$DATA_DIR/minio"
chown "$NAS_USER:$NAS_USER" "$DATA_DIR/minio"


# Write MinIO credentials to environment file
cat > /etc/default/minio << MINIOENV
MINIO_ROOT_USER=$NAS_USER
MINIO_ROOT_PASSWORD=$NAS_PASS
MINIOENV
chown "$NAS_USER:$NAS_USER" /etc/default/minio
chmod 640 /etc/default/minio

# Write MinIO service file using a function to avoid heredoc issues
write_minio_service() {
    local svc_file="/etc/systemd/system/minio.service"
    echo "[Unit]" > "$svc_file"
    echo "Description=MinIO Object Storage" >> "$svc_file"
    echo "Documentation=https://docs.min.io" >> "$svc_file"
    echo "Wants=network-online.target" >> "$svc_file"
    echo "After=network-online.target" >> "$svc_file"
    echo "" >> "$svc_file"
    echo "[Service]" >> "$svc_file"
    echo "User=$NAS_USER" >> "$svc_file"
    echo "Group=$NAS_USER" >> "$svc_file"

    echo "EnvironmentFile=/etc/default/minio" >> "$svc_file"
    echo "ExecStart=/usr/local/bin/minio server $DATA_DIR/minio --console-address :9002" >> "$svc_file"
    echo "Restart=always" >> "$svc_file"
    echo "RestartSec=5" >> "$svc_file"
    echo "LimitNOFILE=65536" >> "$svc_file"
    echo "" >> "$svc_file"
    echo "[Install]" >> "$svc_file"
    echo "WantedBy=multi-user.target" >> "$svc_file"
}
write_minio_service

systemctl daemon-reload
systemctl enable minio
systemctl restart minio
echo "  ✓ MinIO 配置完成"

# ==================== [9/9] 配置防火墙和安全 ====================
echo ""
echo "[9/9] 配置防火墙和安全..."

if [ -f "$NAS_DIR/configs/jail.local" ]; then
    cp "$NAS_DIR/configs/jail.local" /etc/fail2ban/jail.local
else
    cat > /etc/fail2ban/jail.local << 'JEOF'
[DEFAULT]
bantime = 3600
findtime = 600
maxretry = 5

[sshd]
enabled = true
port = ssh
filter = sshd
logpath = /var/log/auth.log

[vsftpd]
enabled = true
port = ftp,ftp-data,ftps,ftps-data
filter = vsftpd
logpath = /var/log/vsftpd.log
maxretry = 5
JEOF
fi
systemctl enable fail2ban
systemctl restart fail2ban

ufw --force reset
ufw default deny incoming
ufw default allow outgoing
ufw allow ssh                    # 22 - SSH
ufw allow 139/tcp                # Samba NetBIOS
ufw allow 445/tcp                # Samba SMB
ufw allow 2049/tcp               # NFS
ufw allow 2049/udp               # NFS
ufw allow 111/tcp                # NFS rpcbind
ufw allow 111/udp                # NFS rpcbind
ufw allow 20048/tcp              # NFS mountd
ufw allow 20048/udp              # NFS mountd
ufw allow 32768:32769/tcp        # NFS lockd + statd
ufw allow 32768:32769/udp        # NFS lockd + statd
ufw allow 21/tcp                 # FTP
ufw allow 30000:31000/tcp        # FTP passive
ufw allow 8080/tcp               # WebDAV
ufw allow 8081/tcp               # FileBrowser
ufw allow 9000/tcp               # MinIO S3 API
ufw allow 9002/tcp               # MinIO Console
ufw allow 8090/tcp               # NAS Web Panel
ufw --force enable
echo "  ✓ 安全配置完成"

# ==================== [10/10] 安装 NAS Web 管理面板 ====================
echo ""
echo "[10/10] 安装 NAS Web 管理面板..."
if [ -f "$NAS_DIR/web/nas-panel" ]; then
    cp "$NAS_DIR/web/nas-panel" /usr/local/bin/nas-panel
    chmod +x /usr/local/bin/nas-panel
elif download_file /usr/local/bin/nas-panel \
    "https://file.abwen.com/control/nas-panel.latest" \
    "https://ghfast.top/https://github.com/gitdogcat/nas/releases/download/v1.0.0/nas-panel"; then
    chmod +x /usr/local/bin/nas-panel
else
    echo "  警告: 无法获取 nas-panel 二进制文件，跳过 Web 面板安装"
    echo "  请手动编译: cd ~/soft/nas/web && go build -o nas-panel ."
    echo "  然后重新运行: sudo bash scripts/setup.sh"
fi

# Create systemd service
if [ -f "$NAS_DIR/configs/nas-panel.service" ]; then
    sed "s/__NAS_USER__/$NAS_USER/g" "$NAS_DIR/configs/nas-panel.service" > /etc/systemd/system/nas-panel.service
fi
systemctl daemon-reload
systemctl enable nas-panel
systemctl restart nas-panel

# 配置 sudo 免密权限（nas-panel 需要执行系统管理命令）
SUDOERS_FILE="/etc/sudoers.d/nas-panel"
echo "${NAS_USER} ALL=(ALL) NOPASSWD: /usr/bin/pdbedit, /opt/nas/scripts/add-user.sh, /opt/nas/scripts/remove-user.sh, /usr/sbin/smartctl, /usr/bin/chpasswd, /usr/bin/smbpasswd, /usr/bin/htpasswd, /bin/systemctl start *, /bin/systemctl stop *, /bin/systemctl restart *, /bin/systemctl enable *, /bin/systemctl disable *, /usr/sbin/ufw status, /usr/sbin/ufw allow *, /usr/sbin/ufw deny *, /usr/sbin/exportfs, /usr/sbin/smartctl -H *, /usr/sbin/smartctl -a *, /usr/bin/tee /opt/nas/.env, /usr/bin/tee -a /etc/samba/smb.conf, /usr/bin/tee /etc/samba/smb.conf, /usr/bin/tee /etc/vsftpd.userlist, /usr/bin/tee -a /etc/vsftpd.userlist, /usr/bin/tee /etc/exports, /usr/bin/tee /etc/nfs.conf, /usr/bin/tee /etc/fail2ban/jail.local, /usr/bin/tee /etc/rclone-htpasswd, /usr/bin/journalctl -p err -n * --no-pager --since *, /usr/sbin/pvs --noheadings *, /usr/sbin/vgs --noheadings *, /usr/sbin/lvs --noheadings *, /usr/sbin/fdisk -l, /usr/sbin/mkfs.ext4 -F *, /usr/sbin/mkfs.xfs -f *, /usr/sbin/mkfs.btrfs *, /bin/mount *, /bin/umount *, /bin/mkdir -p /data/*, /bin/mkdir -p /data, /bin/cat /etc/samba/smb.conf, /bin/cat /etc/vsftpd.userlist, /bin/cat /etc/exports, /bin/cat /etc/nfs.conf, /bin/cat /etc/fail2ban/jail.local, /bin/cat /etc/rclone-htpasswd, /opt/nas/scripts/backup-config.sh, /opt/nas/scripts/restore-config.sh, /bin/rm -f /data/backups/*" > "$SUDOERS_FILE"
chmod 440 "$SUDOERS_FILE"
visudo -cf "$SUDOERS_FILE" 2>/dev/null || { echo "  错误: sudoers 语法检查失败"; rm -f "$SUDOERS_FILE"; }
echo "  ✓ NAS Web 管理面板配置完成"

# ==================== 配置监控告警 cron ====================
CRON_LINE="*/5 * * * * $NAS_DIR/scripts/monitor.sh 2>/dev/null"
( crontab -l 2>/dev/null | grep -v "monitor.sh"; echo "$CRON_LINE" ) | crontab -u "$NAS_USER" - 2>/dev/null || \
( crontab -l 2>/dev/null | grep -v "monitor.sh"; echo "$CRON_LINE" ) | crontab -
# 监控状态目录
mkdir -p /var/lib/nas-monitor
chown "$NAS_USER:$NAS_USER" /var/lib/nas-monitor
echo "  ✓ 监控告警 cron 已配置（每5分钟检查）"

# 配置每周备份 cron（每周日凌晨3点）
BACKUP_CRON_LINE="0 3 * * 0 $NAS_DIR/scripts/backup-config.sh 2>/dev/null"
( crontab -l 2>/dev/null | grep -v "backup-config.sh"; echo "$BACKUP_CRON_LINE" ) | crontab - 2>/dev/null || true
echo "  ✓ 配置备份 cron 已配置（每周日凌晨3点）"

# ==================== 部署完成 ====================
echo ""
echo "========================================="
echo "NAS 部署完成!"
echo "========================================="
echo ""
echo "服务状态:"
for svc in smbd nmbd nfs-kernel-server vsftpd rclone-webdav filebrowser minio fail2ban nas-panel; do
    printf "  %-22s %s\n" "$svc:" "$(systemctl is-active $svc)"
done

echo ""
echo "访问信息:"
echo "  用户名: $NAS_USER"
echo "  密码: $NAS_PASS"
echo ""
echo "访问方式:"
echo "  - Samba:       //NAS_IP/shared (公共共享)"
echo "  - Samba:       //NAS_IP/$NAS_USER (私有目录)"
echo "  - NFS:         mount -t nfs NAS_IP:/data/shared /mnt/nas"
echo "  - FTP:         ftp://NAS_IP/ (用户名: $NAS_USER)"
echo "  - WebDAV:      http://NAS_IP:8080/ (用户名: $NAS_USER)"
echo "  - FileBrowser: http://NAS_IP:8081/ (用户名: $NAS_USER)"
echo "  - MinIO API:   http://NAS_IP:9000"
echo "  - MinIO Web:   http://NAS_IP:9002 (用户名: $NAS_USER)"
echo "  - Web 面板:    http://NAS_IP:8090 (用户名: $NAS_USER)"
echo ""
echo "管理脚本:"
echo "  添加用户: sudo $NAS_DIR/scripts/add-user.sh <用户名> <密码>"
echo "  删除用户: sudo $NAS_DIR/scripts/remove-user.sh <用户名> [--delete-data]"
echo "========================================="
