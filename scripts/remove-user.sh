#!/bin/bash
# NAS 用户管理脚本 - 删除用户
# 用法: sudo ./remove-user.sh <用户名> [--delete-data]

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
SMB_CONF="/etc/samba/smb.conf"
DATA_DIR="/data"

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

# 1. 从 Samba 配置中移除 share
echo "[1/3] 更新 Samba 配置..."
if grep -q "^\[$USERNAME\]" "$SMB_CONF"; then
    # 使用 sed 删除用户 share 段（从 [$USERNAME] 到下一个 [] 或文件结尾）
    sed -i "/^\[$USERNAME\]/,/^\[/{/^\[$USERNAME\]/d;/^$/d;/^   /d;}" "$SMB_CONF"
    systemctl restart smbd
    echo "  ✓ Samba 配置已更新"
else
    echo "  - Samba 配置中无 [$USERNAME] 共享"
fi

# 2. 删除 Samba 用户
echo "[2/3] 删除 Samba 用户..."
if pdbedit -L | grep -q "^$USERNAME:"; then
    smbpasswd -x "$USERNAME"
    echo "  ✓ Samba 用户已删除"
else
    echo "  - Samba 用户不存在"
fi

# 3. 处理用户数据
echo "[3/3] 处理用户数据..."
if [ "$DELETE_DATA" = "--delete-data" ]; then
    echo "  删除用户目录: $DATA_DIR/private/$USERNAME"
    rm -rf "$DATA_DIR/private/$USERNAME"
    echo "  ✓ 用户数据已删除"
else
    echo "  保留用户目录: $DATA_DIR/private/$USERNAME"
    echo "  如需删除数据，请手动运行: rm -rf $DATA_DIR/private/$USERNAME"
fi

# 4. 删除系统用户
echo "删除系统用户..."
userdel "$USERNAME"
echo "  ✓ 系统用户已删除"

echo ""
echo "========================================="
echo "用户 $USERNAME 已删除!"
echo "========================================="
