#!/bin/bash
# NAS 用户管理脚本 - 添加用户
# 用法: sudo ./add-user.sh <用户名> <密码>

set -e

# 检查参数
if [ $# -ne 2 ]; then
    echo "用法: sudo $0 <用户名> <密码>"
    echo "示例: sudo $0 alice mypassword123"
    exit 1
fi

USERNAME="$1"
PASSWORD="$2"
SMB_CONF="/etc/samba/smb.conf"
DATA_DIR="/data"

# 检查 root 权限
if [ "$EUID" -ne 0 ]; then
    echo "错误: 请使用 sudo 运行此脚本"
    exit 1
fi

# 检查用户名是否已存在
if id "$USERNAME" &>/dev/null; then
    echo "错误: 用户 $USERNAME 已存在"
    exit 1
fi

# 检查 Samba 配置中是否已有该用户的 share
if grep -q "^\[$USERNAME\]" "$SMB_CONF"; then
    echo "错误: Samba 配置中已存在 [$USERNAME] 共享"
    exit 1
fi

echo "正在添加用户: $USERNAME"

# 1. 创建系统用户
echo "[1/4] 创建系统用户..."
useradd -m -s /bin/bash "$USERNAME"
echo "$USERNAME:$PASSWORD" | chpasswd
echo "  ✓ 系统用户创建完成"

# 2. 添加 Samba 用户
echo "[2/4] 添加 Samba 用户..."
(echo "$PASSWORD"; echo "$PASSWORD") | smbpasswd -a "$USERNAME" -s
smbpasswd -e "$USERNAME"
echo "  ✓ Samba 用户添加完成"

# 3. 创建私有目录
echo "[3/4] 创建私有目录..."
mkdir -p "$DATA_DIR/private/$USERNAME"
chown "$USERNAME:$USERNAME" "$DATA_DIR/private/$USERNAME"
chmod 700 "$DATA_DIR/private/$USERNAME"
echo "  ✓ 私有目录创建完成: $DATA_DIR/private/$USERNAME"

# 4. 在 Samba 配置中添加用户 share
echo "[4/4] 更新 Samba 配置..."
cat >> "$SMB_CONF" << EOF

[$USERNAME]
   comment = $USERNAME 私有目录
   path = $DATA_DIR/private/$USERNAME
   browseable = no
   read only = no
   create mask = 0700
   directory mask = 0700
   valid users = $USERNAME
EOF

# 重启 Samba 服务
systemctl restart smbd
echo "  ✓ Samba 配置更新完成"

echo ""
echo "========================================="
echo "用户添加完成!"
echo "========================================="
echo "用户名: $USERNAME"
echo "密码: $PASSWORD"
echo ""
echo "访问方式:"
echo "  - Samba: //NAS_IP/$USERNAME (私有，需登录)"
echo "  - Samba: //NAS_IP/shared (公共共享)"
echo "  - NFS: mount -t nfs NAS_IP:/data/shared /mnt/nas"
echo "  - FTP: ftp://NAS_IP/ (登录用户名: $USERNAME)"
echo "  - WebDAV: http://NAS_IP:8080/ (登录用户名: $USERNAME)"
echo "========================================="
