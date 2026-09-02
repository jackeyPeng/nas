#!/bin/bash
# ═══════════════════════════════════════════════════════════════
# Z1 NAS — 一键安装脚本
#
# 用法:
#   wget -qO- https://gitee.com/gitdogcat/nas/raw/master/scripts/install.sh | sudo bash
#   wget -qO- https://gitee.com/gitdogcat/nas/raw/master/scripts/install.sh | sudo env NAS_PASS=myPass123456 bash
#
# 或本地运行:
#   sudo bash install.sh
#
# 注意: sudo 默认清空环境变量，NAS_PASS 必须用 `sudo env NAS_PASS=... bash` 透传，
#       否则脚本读不到密码会进入交互分支（非 TTY 下报 stty ioctl 错误）。
# ═══════════════════════════════════════════════════════════════
set -euo pipefail

# ── 配置 ────────────────────────────────────────────────────
REPO_URL="https://gitee.com/gitdogcat/nas.git"
INSTALL_DIR="/opt/nas"
REPO_DIR="$HOME/soft/nas"
MIN_PASS_LEN=12
PANEL_BIN="/usr/local/bin/nas-panel"
PANEL_PORT=8090

# ── 颜色 ────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'
BLUE='\033[0;34m'; CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'
CHECKMARK="\033[0;32m✓\033[0m"
CROSSMARK="\033[0;31m✗\033[0m"
WARNMARK="\033[0;33m⚠\033[0m"

# ── 进度条 ──────────────────────────────────────────────────
STEP=0
TOTAL=10
STEP_START=0

step_begin() {
    STEP=$((STEP + 1))
    STEP_START=$(date +%s)
    printf "\n${BOLD}${CYAN}[%2d/%2d]${NC} %s ... " "$STEP" "$TOTAL" "$1"
}

step_ok() {
    local elapsed=$(($(date +%s) - STEP_START))
    printf "${GREEN}✓${NC} (%ds)\n" "$elapsed"
}

step_warn() {
    local elapsed=$(($(date +%s) - STEP_START))
    printf "${YELLOW}⚠ %s${NC} (%ds)\n" "$1" "$elapsed"
}

step_fail() {
    local elapsed=$(($(date +%s) - STEP_START))
    printf "${RED}✗ %s${NC} (%ds)\n" "$1" "$elapsed"
}

# ── Banner ──────────────────────────────────────────────────
echo ""
echo -e "${BOLD}${BLUE}╔══════════════════════════════════════════════╗${NC}"
echo -e "${BOLD}${BLUE}║                                              ║${NC}"
echo -e "${BOLD}${BLUE}║   🏠  Z1 NAS — 家用存储系统 一键安装         ║${NC}"
echo -e "${BOLD}${BLUE}║                                              ║${NC}"
echo -e "${BOLD}${BLUE}╚══════════════════════════════════════════════╝${NC}"
echo ""
echo -e "  ${CYAN}Samba · NFS · FTP · WebDAV · FileBrowser · S3 · 面板${NC}"
echo -e "  ${CYAN}防火墙 · 入侵防护 · 监控告警 · 自动备份${NC}"
echo ""

# ── 1. 环境检测 ────────────────────────────────────────────
step_begin "检测系统环境"

if [ "$EUID" -ne 0 ]; then
    echo ""
    echo -e "${RED}错误: 请使用 sudo 运行${NC}"
    echo ""
    echo "  wget -qO- https://gitee.com/gitdogcat/nas/raw/master/scripts/install.sh | sudo bash"
    echo ""
    exit 1
fi

NAS_USER="${SUDO_USER:-$USER}"
if [ -z "$NAS_USER" ] || [ "$NAS_USER" = "root" ]; then
    echo ""
    echo -e "${RED}错误: 无法检测用户名，请使用 sudo 运行（不要直接以 root 登录）${NC}"
    exit 1
fi

# 检测 CPU 架构
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/;s/armv7l/armhf/')
OS_NAME=$(grep "^PRETTY_NAME=" /etc/os-release 2>/dev/null | cut -d'"' -f2 || echo "Unknown")
DISK_TOTAL=$(df -h / | awk 'NR==2 {print $2}')
MEM_TOTAL=$(free -h 2>/dev/null | awk 'NR==2 {print $2}' || free -h 2>/dev/null | awk '/Mem|内存/ {print $2}')
PRIMARY_IP=$(ip -4 addr show scope global | grep -oP 'inet \K[0-9.]+' | head -1)
if [ -z "$PRIMARY_IP" ]; then
    PRIMARY_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
fi

step_ok
echo -e "  用户:     ${GREEN}${NAS_USER}${NC}"
echo -e "  系统:     ${OS_NAME}"
echo -e "  架构:     ${ARCH}"
echo -e "  磁盘:     ${DISK_TOTAL}"
echo -e "  内存:     ${MEM_TOTAL}"
echo -e "  地址:     ${PRIMARY_IP:-未知}"

# ── 密码 ────────────────────────────────────────────────────
if [ -n "${NAS_PASS:-}" ]; then
    if [ ${#NAS_PASS} -lt $MIN_PASS_LEN ]; then
        echo -e "\n${RED}错误: NAS_PASS 至少需要 ${MIN_PASS_LEN} 位${NC}"
        exit 1
    fi
else
    # 交互式输入需要 TTY；管道/非 TTY 环境（如 CI、nohup）无法读密码，直接给出明确错误
    if [ ! -t 0 ]; then
        echo ""
        echo -e "${RED}错误: 非交互式环境无法输入密码${NC}"
        echo -e "  请通过环境变量传入（注意用 sudo env 透传）：${NC}"
        echo ""
        echo -e "  wget -qO- https://gitee.com/gitdogcat/nas/raw/master/scripts/install.sh | ${CYAN}sudo env NAS_PASS=你的密码${NC} bash"
        echo ""
        exit 1
    fi
    echo ""
    echo -e "${BOLD}设置管理密码（至少 ${MIN_PASS_LEN} 位，用于所有服务）${NC}"
    while true; do
        printf "密码: "
        stty -echo
        read NAS_PASS
        stty echo
        echo ""
        if [ ${#NAS_PASS} -lt $MIN_PASS_LEN ]; then
            echo -e "${YELLOW}密码太短，至少需要 ${MIN_PASS_LEN} 位${NC}"
            continue
        fi
        printf "确认: "
        stty -echo
        read CONFIRM
        stty echo
        echo ""
        if [ "$NAS_PASS" != "$CONFIRM" ]; then
            echo -e "${YELLOW}两次输入不一致，请重试${NC}"
            continue
        fi
        break
    done
fi

# ── 2. 克隆仓库 ────────────────────────────────────────────
step_begin "获取安装脚本"

if [ -d "$REPO_DIR/.git" ]; then
    echo -n "(更新) "
    cd "$REPO_DIR" && git pull --ff-only origin master 2>/dev/null || true
else
    mkdir -p "$(dirname "$REPO_DIR")"
    if ! git clone --depth 1 "$REPO_URL" "$REPO_DIR" 2>/dev/null; then
        step_fail "无法克隆仓库"
        echo -e "  ${YELLOW}请检查网络连接，或手动克隆:${NC}"
        echo "  git clone $REPO_URL $REPO_DIR"
        exit 1
    fi
fi

# 创建 /opt/nas 软链接
ln -sfn "$REPO_DIR" "$INSTALL_DIR" 2>/dev/null || true

step_ok

# ── 3. 创建配置 ────────────────────────────────────────────
step_begin "创建配置文件"

if [ ! -f "$REPO_DIR/.env" ]; then
    cp "$REPO_DIR/.env.example" "$REPO_DIR/.env"
fi
sed -i "s/^NAS_PASS=.*/NAS_PASS=${NAS_PASS}/" "$REPO_DIR/.env"
# 确保 NAS_USER 在 .env 中（面板登录需要）
if ! grep -q "^NAS_USER=" "$REPO_DIR/.env"; then
    echo "NAS_USER=${NAS_USER}" >> "$REPO_DIR/.env"
fi

# 确保 .env 可被 sudo 读取
chmod 600 "$REPO_DIR/.env"

step_ok

# ── 4. 安装系统依赖 ────────────────────────────────────────
step_begin "安装系统软件包"

# 检查是否需要更新 apt 源（速度测试：下载 < 100KB/s 则切换）
APT_SOURCES="/etc/apt/sources.list"
if [ -f "$APT_SOURCES" ] && grep -q "deb.debian.org" "$APT_SOURCES"; then
    echo -n "(测速) "
    SPEED=$(curl -s --connect-timeout 5 --max-time 10 \
        -o /dev/null -w "%{speed_download}" \
        "http://deb.debian.org/debian/dists/trixie/main/binary-amd64/Packages.gz" 2>/dev/null || echo "0")
    SPEED_INT=$(echo "$SPEED" | cut -d. -f1)
    if [ -z "$SPEED_INT" ] || [ "$SPEED_INT" -lt 100000 ]; then
        echo -n "(切换清华镜像) "
        sed -i 's|http://deb\.debian\.org|http://mirrors.tuna.tsinghua.edu.cn|g' "$APT_SOURCES"
        sed -i 's|http://security\.debian\.org|http://mirrors.tuna.tsinghua.edu.cn|g' "$APT_SOURCES"
    fi
fi

apt-get update -qq 2>/dev/null
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
    curl samba nfs-kernel-server vsftpd rclone fail2ban ufw \
    smartmontools unattended-upgrades smbclient nfs-common \
    xfsprogs mdadm lvm2 apache2-utils \
    2>&1 | tail -3

step_ok

# ── 5-10. 运行 setup.sh ────────────────────────────────────
echo ""
echo -e "${BOLD}${BLUE}═══ 开始配置 NAS 服务 ═══${NC}"

# 用子 shell 跑 setup.sh，捕获输出但不影响主流程
SETUP_LOG=$(mktemp)
if bash "$INSTALL_DIR/scripts/setup.sh" > "$SETUP_LOG" 2>&1; then
    SETUP_OK=true
else
    SETUP_OK=false
fi

# 显示 setup.sh 的关键输出行
grep -E '^\[|✓|✗|⚠|服务状态|部署完成|通过|失败|警告' "$SETUP_LOG" 2>/dev/null | while read line; do
    echo "  $line"
done
rm -f "$SETUP_LOG"

# ── 检查 nas-panel 是否安装成功 ────────────────────────────
if [ ! -f "$PANEL_BIN" ]; then
    echo ""
    echo -e "${RED}✗ nas-panel 二进制下载失败${NC}"
    echo -e "  ${YELLOW}请手动编译后重新安装:${NC}"
    echo ""
    echo -e "  apt-get install -y golang-go"
    echo -e "  cd $REPO_DIR/web"
    echo -e "  GOPROXY=https://goproxy.cn,direct go build -buildvcs=false -o $PANEL_BIN ."
    echo -e "  sudo bash $REPO_DIR/scripts/setup.sh"
    echo ""
    exit 1
fi

# 确保面板服务正常运行
if [ -f "$PANEL_BIN" ]; then
    systemctl reset-failed nas-panel 2>/dev/null || true
    systemctl restart nas-panel 2>/dev/null || true
fi

# ── 11. 验证安装 ────────────────────────────────────────────
echo ""
echo -e "${BOLD}${BLUE}═══ 验证安装结果 ═══${NC}"
echo ""

VERIFY_OK=true
VERIFY_PASS=0
VERIFY_FAIL=0
VERIFY_WARN=0

# 等待面板启动
sleep 2

# 检查所有服务
echo -e "${BOLD}服务状态:${NC}"
for svc in smbd nmbd nfs-kernel-server vsftpd rclone-webdav filebrowser rclone-s3 fail2ban nas-panel; do
    STATUS=$(systemctl is-active "$svc" 2>/dev/null || echo "inactive")
    case "$STATUS" in
        active)
            printf "  ${CHECKMARK} %-22s ${GREEN}%s${NC}\n" "$svc" "$STATUS"
            ;;
        *)
            printf "  ${CROSSMARK} %-22s ${RED}%s${NC}\n" "$svc" "$STATUS"
            VERIFY_OK=false
            ;;
    esac
done

# 系统注册表
echo ""
echo -e "${BOLD}系统注册表检查:${NC}"
if curl -s --max-time 10 "http://localhost:${PANEL_PORT}/api/system/check?action=refresh" > /tmp/nas-verify.json 2>/dev/null; then
    VERIFY_PASS=$(python3 -c "import json;d=json.load(open('/tmp/nas-verify.json'));print(d['passed'])" 2>/dev/null || echo "?")
    VERIFY_FAIL=$(python3 -c "import json;d=json.load(open('/tmp/nas-verify.json'));print(d['failed'])" 2>/dev/null || echo "?")
    VERIFY_WARN=$(python3 -c "import json;d=json.load(open('/tmp/nas-verify.json'));print(d['warn'])" 2>/dev/null || echo "?")
    
    if [ "$VERIFY_FAIL" = "0" ]; then
        echo -e "  ${CHECKMARK} 通过: ${GREEN}${VERIFY_PASS}${NC} / 失败: 0 / 警告: ${VERIFY_WARN}"
    else
        echo -e "  ${CROSSMARK} 通过: ${VERIFY_PASS} / 失败: ${RED}${VERIFY_FAIL}${NC} / 警告: ${VERIFY_WARN}"
        VERIFY_OK=false
    fi
    
    # 显示失败项
    python3 -c "
import json
d=json.load(open('/tmp/nas-verify.json'))
for i in d['items']:
    if i['status'] != 'pass':
        print(f\"    [{i['status']}] {i['name']}: {i['detail']}\")
" 2>/dev/null
else
    echo -e "  ${WARNMARK} 面板未响应，跳过注册表检查"
fi
rm -f /tmp/nas-verify.json

# ── 完成 ────────────────────────────────────────────────────
echo ""
echo -e "${BOLD}${GREEN}╔══════════════════════════════════════════════╗${NC}"
echo -e "${BOLD}${GREEN}║         🎉  Z1 NAS 安装完成！                ║${NC}"
echo -e "${BOLD}${GREEN}╚══════════════════════════════════════════════╝${NC}"
echo ""
echo -e "  ${BOLD}Web 管理面板${NC}"
echo -e "  ┌─────────────────────────────────────────┐"
echo -e "  │ 地址:   ${GREEN}http://${PRIMARY_IP}:${PANEL_PORT}${NC}"
echo -e "  │ 用户名: ${GREEN}${NAS_USER}${NC}"
echo -e "  │ 密码:   ${GREEN}(你设置的密码)${NC}"
echo -e "  └─────────────────────────────────────────┘"
echo ""
echo -e "  ${BOLD}其他访问方式${NC}"
echo -e "  FileBrowser:  ${CYAN}http://${PRIMARY_IP}:8081${NC}"
echo -e "  WebDAV:       ${CYAN}http://${PRIMARY_IP}:8080${NC}"
echo -e "  Samba:        ${CYAN}\\\\${PRIMARY_IP}\\shared${NC}"
echo -e "  FTP:          ${CYAN}ftp://${PRIMARY_IP}${NC}"
echo -e "  S3:           ${CYAN}http://${PRIMARY_IP}:9000${NC}"
echo ""

if [ "$VERIFY_OK" = true ]; then
    echo -e "  ${GREEN}所有服务正常运行，可以开始使用了！${NC}"
else
    echo -e "  ${YELLOW}部分服务可能未正常启动，请检查上方输出。${NC}"
    echo -e "  ${YELLOW}查看日志: journalctl -u <服务名>${NC}"
fi

echo ""
echo -e "  ${CYAN}数据目录: /data${NC}"
echo -e "  ${CYAN}配置目录: ${INSTALL_DIR}${NC}"
echo -e "  ${CYAN}备份目录: /data/backups${NC}"
echo ""