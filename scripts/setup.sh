#!/bin/bash
# NAS 一键部署脚本
# 用于在新机器上部署完整的 NAS 系统
# 用法: sudo ./setup.sh

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

# 配置变量
NAS_USER="jacky"
NAS_PASS="[REDACTED]"
DATA_DIR="/data"
NAS_DIR="/opt/nas"

echo "[1/7] 安装基础软件包..."
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y \
    samba nfs-kernel-server vsftpd rclone \
    fail2ban ufw smartmontools unattended-upgrades \
    smbclient nfs-common
echo "  ✓ 软件包安装完成"

echo ""
echo "[2/7] 创建数据目录结构..."
mkdir -p "$DATA_DIR"/{shared,media/{movies,tv,music},documents,photos,backups,downloads,private/$NAS_USER}
chown -R "$NAS_USER:$NAS_USER" "$DATA_DIR"
chmod 755 "$DATA_DIR"
chmod 775 "$DATA_DIR"/{shared,media,documents,photos}
echo "  ✓ 目录结构创建完成"

echo ""
echo "[3/7] 配置 Samba..."
# 复制配置文件（假设当前目录有 configs/smb.conf）
if [ -f "$NAS_DIR/configs/smb.conf" ]; then
    cp "$NAS_DIR/configs/smb.conf" /etc/samba/smb.conf
else
    echo "  警告: 未找到 $NAS_DIR/configs/smb.conf，请手动配置"
fi
(echo "$NAS_PASS"; echo "$NAS_PASS") | smbpasswd -a "$NAS_USER" -s
smbpasswd -e "$NAS_USER"
systemctl enable smbd nmbd
systemctl restart smbd nmbd
echo "  ✓ Samba 配置完成"

echo ""
echo "[4/7] 配置 NFS..."
if [ -f "$NAS_DIR/configs/exports" ]; then
    cp "$NAS_DIR/configs/exports" /etc/exports
else
    cat > /etc/exports << EOF
$DATA_DIR/shared    192.168.0.0/16(rw,sync,no_subtree_check,no_root_squash)
$DATA_DIR/media     192.168.0.0/16(ro,sync,no_subtree_check)
$DATA_DIR/documents 192.168.0.0/16(rw,sync,no_subtree_check,no_root_squash)
$DATA_DIR/photos    192.168.0.0/16(rw,sync,no_subtree_check,no_root_squash)
$DATA_DIR/backups   192.168.0.0/16(rw,sync,no_subtree_check,no_root_squash)
EOF
fi
exportfs -a
systemctl enable nfs-kernel-server
systemctl restart nfs-kernel-server
echo "  ✓ NFS 配置完成"

echo ""
echo "[5/7] 配置 FTP (vsftpd)..."
if [ -f "$NAS_DIR/configs/vsftpd.conf" ]; then
    cp "$NAS_DIR/configs/vsftpd.conf" /etc/vsftpd.conf
else
    cat > /etc/vsftpd.conf << EOF
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
EOF
fi
echo "$NAS_USER" > /etc/vsftpd.userlist
systemctl enable vsftpd
systemctl restart vsftpd
echo "  ✓ FTP 配置完成"

echo ""
echo "[6/7] 配置 WebDAV (rclone)..."
# 生成密码哈希
PASS_HASH=$(rclone obscure "$NAS_PASS")
cat > /etc/systemd/system/rclone-webdav.service << EOF
[Unit]
Description=Rclone WebDAV Server
After=network.target

[Service]
Type=simple
User=$NAS_USER
ExecStart=/usr/bin/rclone serve webdav $DATA_DIR --addr :8080 --user $NAS_USER --pass $PASS_HASH
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable rclone-webdav
systemctl restart rclone-webdav
echo "  ✓ WebDAV 配置完成"

echo ""
echo "[7/7] 配置防火墙和安全..."
# 创建 vsftpd 日志文件
touch /var/log/vsftpd.log
chmod 640 /var/log/vsftpd.log

# 配置 fail2ban
if [ -f "$NAS_DIR/configs/jail.local" ]; then
    cp "$NAS_DIR/configs/jail.local" /etc/fail2ban/jail.local
else
    cat > /etc/fail2ban/jail.local << EOF
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
EOF
fi
systemctl enable fail2ban
systemctl restart fail2ban

# 配置防火墙
ufw --force reset
ufw default deny incoming
ufw default allow outgoing
ufw allow ssh
ufw allow 139/tcp    # Samba
ufw allow 445/tcp    # Samba
ufw allow 2049/tcp   # NFS
ufw allow 2049/udp   # NFS
ufw allow 21/tcp     # FTP
ufw allow 30000:31000/tcp  # FTP passive
ufw allow 8080/tcp   # WebDAV
ufw --force enable
echo "  ✓ 安全配置完成"

echo ""
echo "========================================="
echo "NAS 部署完成!"
echo "========================================="
echo ""
echo "服务状态:"
for svc in smbd nmbd nfs-kernel-server vsftpd rclone-webdav fail2ban; do
    echo "  $svc: $(systemctl is-active $svc)"
done

echo ""
echo "访问信息:"
echo "  用户名: $NAS_USER"
echo "  密码: $NAS_PASS"
echo ""
echo "访问方式:"
echo "  - Samba: //NAS_IP/shared (公共共享)"
echo "  - Samba: //NAS_IP/$NAS_USER (私有目录)"
echo "  - NFS: mount -t nfs NAS_IP:/data/shared /mnt/nas"
echo "  - FTP: ftp://NAS_IP/ (用户名: $NAS_USER)"
echo "  - WebDAV: http://NAS_IP:8080/ (用户名: $NAS_USER)"
echo ""
echo "管理脚本:"
echo "  添加用户: sudo $NAS_DIR/scripts/add-user.sh <用户名> <密码>"
echo "  删除用户: sudo $NAS_DIR/scripts/remove-user.sh <用户名> [--delete-data]"
echo "========================================="
