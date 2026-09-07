#!/bin/bash
# NAS 用户管理脚本 - 添加用户
# 用法: sudo ./add-user.sh <用户名> <密码>
# 新模型：用户主目录 /data/nas1/<用户名>，默认子目录 media/photos/documents/downloads
#         主目录提供 SMB（按用户）+ FTP（落在 /data/nas1，靠文件系统权限隔离）

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
POOL_DIR="/data/nas1"

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

# 存储池必须已存在
if [ ! -d "$POOL_DIR" ]; then
    echo "错误: 存储池 $POOL_DIR 不存在，请先在面板完成存储配置"
    exit 1
fi

# 检查 Samba 配置中是否已有该用户的 share
if grep -q "^\[$USERNAME\]" "$SMB_CONF"; then
    echo "错误: Samba 配置中已存在 [$USERNAME] 共享"
    exit 1
fi

echo "正在添加用户: $USERNAME"

# 0. 确保公共组 nasusers 存在
if ! getent group nasusers >/dev/null 2>&1; then
    groupadd nasusers
fi

# 1. 创建系统用户（加入 nasusers 公共组）
echo "[1/5] 创建系统用户..."
useradd -m -s /bin/bash -G nasusers "$USERNAME"
echo "$USERNAME:$PASSWORD" | chpasswd
echo "  ✓ 系统用户创建完成"

# 2. 添加 Samba 用户
echo "[2/5] 添加 Samba 用户..."
(echo "$PASSWORD"; echo "$PASSWORD") | smbpasswd -a "$USERNAME" -s
smbpasswd -e "$USERNAME"
echo "  ✓ Samba 用户添加完成"

# 3. 创建用户主目录 + 默认子目录
echo "[3/5] 创建用户主目录..."
mkdir -p "$POOL_DIR/$USERNAME"/{media,photos,documents,downloads}
chown -R "$USERNAME:$USERNAME" "$POOL_DIR/$USERNAME"
chmod 700 "$POOL_DIR/$USERNAME"
echo "  ✓ 主目录创建完成: $POOL_DIR/$USERNAME"

# 4. 写入 folders.db（home 条目），由面板托管段统一生成 SMB 共享
echo "[4/5] 写入元数据..."
python3 - "$POOL_DIR" "$USERNAME" << 'PYEOF'
import sqlite3, sys, os
pool, username = sys.argv[1], sys.argv[2]
db_path = "/opt/nas/data/folders.db"
db = sqlite3.connect(db_path)
db.execute("""INSERT INTO folders (name, path, pool, permission, valid_users, write_users, recycle_bin, samba_share, nfs_export, quota_gb, updated_at)
              VALUES (?, ?, ?, 'readwrite', ?, '', 0, 1, 0, 0, datetime('now'))
              ON CONFLICT(path) DO UPDATE SET name=excluded.name, pool=excluded.pool, permission=excluded.permission,
              valid_users=excluded.valid_users, write_users=excluded.write_users, samba_share=excluded.samba_share,
              nfs_export=excluded.nfs_export, updated_at=datetime('now')""",
           (username, os.path.join(pool, username), pool, username))
db.commit()
print("  ✓ folders.db 已写入 home 条目")
PYEOF

# 5. 加入 FTP 白名单
echo "[5/5] 配置 FTP..."
if ! grep -qx "$USERNAME" /etc/vsftpd.userlist 2>/dev/null; then
    echo "$USERNAME" >> /etc/vsftpd.userlist
fi
systemctl reload vsftpd 2>/dev/null || true
echo "  ✓ FTP 已启用（白名单模式）"

echo ""
echo "========================================="
echo "用户添加完成!"
echo "========================================="
echo "用户名: $USERNAME"
echo "密码: $PASSWORD"
echo ""
echo "访问方式:"
echo "  - Samba: //NAS_IP/$USERNAME (主目录，需登录)"
echo "  - Samba: //NAS_IP/public (公共共享)"
echo "  - FTP:   ftp://NAS_IP/ (登录后落在 /data/nas1)"
echo "========================================="
echo ""
echo "提示: 若 SMB 共享未立即生效，请在面板点一次「配置同步」或重启 nas-panel。"
