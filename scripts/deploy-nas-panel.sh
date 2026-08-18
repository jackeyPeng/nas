#!/bin/bash
# NAS panel 部署脚本
# 用法: ./deploy-nas-panel.sh [目标IP]

set -euo pipefail

BINARY="/home/jacky/soft/nas/web/nas-panel"
SERVERS=("192.168.213.85" "10.216.10.52")

if [ ! -f "$BINARY" ]; then
    echo "错误: 二进制文件不存在: $BINARY"
    echo "请先执行: cd /home/jacky/soft/nas/web && go build -o nas-panel ."
    exit 1
fi

echo "=========================================="
echo "  NAS Panel 部署脚本"
echo "=========================================="
echo "二进制: $BINARY ($(stat -c%s "$BINARY" | numfmt --to=iec))"
echo ""

deploy_to() {
    local ip="$1"
    local user=""
    
    if [ "$ip" = "192.168.213.85" ]; then
        user="jacky"
    elif [ "$ip" = "10.216.10.52" ]; then
        user="fm"
    elif [ "$ip" = "10.216.10.115" ]; then
        user="fm"
    else
        echo "未知服务器: $ip"
        return 1
    fi
    
    echo ">>> 部署到 $user@$ip"
    
    # 0. 安装依赖（首次部署时）
    echo "  [0/5] 检查依赖..."
    ssh -o ConnectTimeout=10 "$user@$ip" '
        for pkg in xfsprogs parted smartmontools lvm2 mdadm curl; do
            dpkg -l $pkg 2>/dev/null | grep -q "^ii" || sudo apt-get install -y -qq $pkg 2>/dev/null
        done
        echo "    依赖已就绪"
    '
    
    # 1. 上传二进制
    echo "  [1/5] 上传二进制..."
    scp -o ConnectTimeout=10 "$BINARY" "$user@$ip:/tmp/nas-panel.new"
    
    # 2. 备份旧版本
    echo "  [2/5] 备份旧版本..."
    ssh -o ConnectTimeout=10 "$user@$ip" "
        if [ -f /usr/local/bin/nas-panel ]; then
            sudo cp /usr/local/bin/nas-panel /usr/local/bin/nas-panel.bak.\$(date +%Y%m%d%H%M%S)
            echo '    旧版本已备份'
        else
            echo '    无旧版本需要备份'
        fi
    "
    
    # 3. 替换二进制
    echo "  [3/5] 替换二进制..."
    ssh -o ConnectTimeout=10 "$user@$ip" "
        sudo mv /tmp/nas-panel.new /usr/local/bin/nas-panel
        sudo chmod +x /usr/local/bin/nas-panel
        echo '    新版本已安装'
    "
    
    # 4. 重启服务
    echo "  [4/5] 重启服务..."
    ssh -o ConnectTimeout=10 "$user@$ip" "
        sudo systemctl restart nas-panel
        sleep 2
        if systemctl is-active --quiet nas-panel; then
            echo '    ✓ 服务已启动'
            curl -s http://localhost:8090/api/dashboard > /dev/null && echo '    ✓ API 响应正常' || echo '    ⚠ API 无响应'
        else
            echo '    ✗ 服务启动失败'
            sudo journalctl -u nas-panel --no-pager -n 20
            return 1
        fi
    "
    
    echo "  ✓ $ip 部署完成"
    echo ""
}

# 部署到指定服务器或全部
if [ $# -eq 1 ]; then
    deploy_to "$1"
else
    for ip in "${SERVERS[@]}"; do
        deploy_to "$ip" || echo "  ✗ $ip 部署失败，继续下一台"
    done
fi

echo "=========================================="
echo "  部署完成"
echo "=========================================="
