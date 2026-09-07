#!/bin/bash
# NAS 用户管理脚本 - 删除用户
# 用法: sudo ./remove-user.sh <用户名> [--delete-data]
# 新模型：用户主目录 /data/nas1/<用户名>，SMB 共享由 folders.db 托管段统一生成

set -e

# 检查参数
if [ $# -lt 1 ]; then
    echo "用法: sudo $0 <用户名> [--delete-data]"
    echo "示例: sudo $0 alice              # 删除用户但保留数据"
    echo "      sudo $0 alice --delete-data # 删除用户和数据"
    exit 1
fi

USERNAME="$1"
DELETE_DATA="$2"
POOL_DIR="/data/nas1"

# 检查 root 权限
if [ "$EUID" -ne 0 ]; then
    echo "错误: 请使用 sudo 运行此脚本"
    exit 1
fi

# 防止删除 root 和部署用户
DEPLOY_USER="${SUDO_USER:-$USER}"
if [ "$USERNAME" = "root" ] || [ "$USERNAME" = "$DEPLOY_USER" ]; then
    echo "错误: 不允许删除系统用户 $USERNAME"
    exit 1
fi

# 检查用户是否存在
if ! id "$USERNAME" &>/dev/null; then
    echo "错误: 用户 $USERNAME 不存在"
    exit 1
fi

echo "正在删除用户: $USERNAME"

# 1. 从 folders.db 移除 home 条目（SMB 共享由托管段统一生成）
echo "[1/5] 更新元数据..."
python3 - "$USERNAME" << 'PYEOF'
import sqlite3, sys
db = sqlite3.connect("/opt/nas/data/folders.db")
db.execute("DELETE FROM folders WHERE name = ?", (sys.argv[1],))
db.commit()
print("  ✓ folders.db 已移除 home 条目")
PYEOF

# 2. 删除 Samba 用户
echo "[2/5] 删除 Samba 用户..."
if pdbedit -L 2>/dev/null | grep -q "^$USERNAME:"; then
    smbpasswd -x "$USERNAME"
    echo "  ✓ Samba 用户已删除"
else
    echo "  - Samba 用户不存在"
fi

# 3. 从 FTP 白名单移除
echo "[3/5] 更新 FTP 白名单..."
sed -i "/^$USERNAME$/d" /etc/vsftpd.userlist
systemctl reload vsftpd 2>/dev/null || true
echo "  ✓ FTP 已移除"

# 4. 处理用户数据
echo "[4/5] 处理用户数据..."
if [ "$DELETE_DATA" = "--delete-data" ]; then
    echo "  删除用户目录: $POOL_DIR/$USERNAME"
    rm -rf "$POOL_DIR/$USERNAME"
    echo "  ✓ 用户数据已删除"
else
    echo "  保留用户目录: $POOL_DIR/$USERNAME"
    echo "  如需删除数据，请手动运行: rm -rf $POOL_DIR/$USERNAME"
fi

# 5. 删除系统用户
echo "[5/5] 删除系统用户..."
userdel "$USERNAME" 2>/dev/null || userdel -r "$USERNAME"
echo "  ✓ 系统用户已删除"

echo ""
echo "========================================="
echo "用户 $USERNAME 已删除!"
echo "提示: SMB 共享会在面板下次「配置同步」后消失，或手动触发。"
echo "========================================="
