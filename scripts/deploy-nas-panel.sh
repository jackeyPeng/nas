#!/bin/bash
# NAS panel 部署脚本
# 用法: ./deploy-nas-panel.sh [user@]host [[user@]host ...]
#
# 把本地编译好的 nas-panel 二进制部署到目标机器并重启服务。
# 目标用 [user@]host 指定（省略 user 时默认当前登录用户）。
# 注意: 此脚本是公开发布的产品，不要把任何真实 IP / 主机名写死在这里，
#       目标一律通过命令行参数传入。

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BINARY="${NAS_PANEL_BINARY:-$SCRIPT_DIR/../web/nas-panel}"

if [ $# -eq 0 ]; then
    echo "用法: $0 [user@]host [[user@]host ...]"
    echo ""
    echo "示例:"
    echo "  $0 fm@192.168.1.100"
    echo "  $0 jacky@192.168.1.101 fm@192.168.1.102"
    exit 1
fi

if [ ! -f "$BINARY" ]; then
    echo "错误: 二进制文件不存在: $BINARY"
    echo "请先执行: (cd web && go build -o nas-panel .)"
    exit 1
fi

echo "=========================================="
echo "  NAS Panel 部署脚本"
echo "=========================================="
echo "二进制: $BINARY ($(stat -c%s "$BINARY" | numfmt --to=iec))"
echo ""

deploy_to() {
    local target="$1"
    local user host

    if [[ "$target" == *@* ]]; then
        user="${target%%@*}"
        host="${target#*@}"
    else
        user="$(whoami)"
        host="$target"
    fi

    echo ">>> 部署到 $user@$host"

    # 0. 安装依赖（首次部署时）
    echo "  [0/5] 检查依赖..."
    ssh -o ConnectTimeout=10 "$user@$host" '
        for pkg in xfsprogs parted smartmontools lvm2 mdadm curl; do
            dpkg -l $pkg 2>/dev/null | grep -q "^ii" || sudo apt-get install -y -qq $pkg 2>/dev/null
        done
        echo "    依赖已就绪"
    '

    # 1. 上传二进制
    echo "  [1/5] 上传二进制..."
    scp -o ConnectTimeout=10 "$BINARY" "$user@$host:/tmp/nas-panel.new"

    # 2. 备份旧版本
    echo "  [2/5] 备份旧版本..."
    ssh -o ConnectTimeout=10 "$user@$host" "
        if [ -f /usr/local/bin/nas-panel ]; then
            sudo cp /usr/local/bin/nas-panel /usr/local/bin/nas-panel.bak.\$(date +%Y%m%d%H%M%S)
            echo '    旧版本已备份'
        else
            echo '    无旧版本需要备份'
        fi
    "

    # 3. 替换二进制
    echo "  [3/5] 替换二进制..."
    ssh -o ConnectTimeout=10 "$user@$host" "
        sudo mv /tmp/nas-panel.new /usr/local/bin/nas-panel
        sudo chmod +x /usr/local/bin/nas-panel
        echo '    新版本已安装'
    "

    # 4. 重启服务
    echo "  [4/5] 重启服务..."
    ssh -o ConnectTimeout=10 "$user@$host" "
        sudo systemctl restart nas-panel
        sleep 2
        if systemctl is-active --quiet nas-panel; then
            echo '    ✓ 服务已启动'
            curl -s http://localhost:8090/api/dashboard > /dev/null && echo '    ✓ API 响应正常' || echo '    ⚠ API 无响应'
        else
            echo '    ✗ 服务启动失败'
            sudo journalctl -u nas-panel --no-pager -n 20
            exit 1
        fi
    "

    echo "  ✓ $user@$host 部署完成"
    echo ""
}

FAILED=0
for target in "$@"; do
    deploy_to "$target" || { echo "  ✗ $target 部署失败"; FAILED=1; }
done

echo "=========================================="
if [ "$FAILED" -eq 0 ]; then
    echo "  部署完成"
else
    echo "  部分目标部署失败"
    exit 1
fi
echo "=========================================="
