#!/bin/bash
# ═══════════════════════════════════════════════════════════════
# Z1 NAS — 一键安装脚本
#
# 用法（以普通用户身份直跑，脚本按需逐条 sudo，交互式密码只问一次）:
#   curl -fsSL https://get.z1.sale/install.sh | bash
#   curl -fsSL https://get.z1.sale/install.sh | env NAS_PASS=mySecurePass123 bash
#
# 或本地运行:
#   bash install.sh
#
# 注意: NAS_PASS 走 `env NAS_PASS=... bash` 透传（curl|bash 管道下 env 直接可见，
#       不需要 sudo），否则脚本读不到密码会进入交互分支（非 TTY 下报 stty ioctl 错误）。
# 仍兼容 root 直跑（如 sudo bash install.sh）：特权命令自动降级为无 sudo 前缀直执行。
# ═══════════════════════════════════════════════════════════════
set -euo pipefail

# ── 配置 ────────────────────────────────────────────────────
REPO_URL="https://gitee.com/gitdogcat/nas.git"
INSTALL_DIR="/opt/nas"
REPO_DIR="$HOME/soft/nas"
MIN_PASS_LEN=12
PANEL_BIN="/usr/local/bin/nas-panel"
PANEL_PORT=8090

# ── 特权命令封装 ────────────────────────────────────────────
# EUID=0（root 直跑）时 SUDO 为空串，特权命令原样直跑，完全兼容旧用法；
# EUID≠0（fm 等普通用户跑）时 SUDO=sudo，逐条命令加前缀。
# curl|bash 管道下 stdin 是脚本自身文本，不能用 read/read -s 问密码——
# sudo 密码在开头一次性 `sudo -v` 缓存（tty 交互由 sudo 自己处理，不走脚本 stdin），
# 缓存有效期内（通常 15 分钟）后续 $SUDO 调用不会再次提示。
if [ "$EUID" -eq 0 ]; then
    SUDO=""
else
    SUDO="sudo"
fi

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

# ── apt 重试包裹 ────────────────────────────────────────────
# apt-daily-upgrade.service 等系统定时任务可能与本脚本的 apt-get 调用撞锁，
# 直接失败退出（F-1 真机实锤：撞锁 4 分钟后单独重跑即过，纯瞬时冲突）。
# 重试 3 次、每次间隔 10s，失败提示走 stderr。
apt_retry() {
    local n=0
    until [ $n -ge 3 ]; do
        "$@" && return 0
        n=$((n + 1))
        echo "  ⚠ apt 命令失败（第 $n 次），10s 后重试: $*" >&2
        sleep 10
    done
    return 1
}

# ── Banner ──────────────────────────────────────────────────
echo ""
echo -e "${BOLD}${BLUE}╔══════════════════════════════════════════════╗${NC}"
echo -e "${BOLD}${BLUE}║                                              ║${NC}"
echo -e "${BOLD}${BLUE}║   🏠  Z1 NAS — 家用存储系统 一键安装         ║${NC}"
echo -e "${BOLD}${BLUE}║                                              ║${NC}"
echo -e "${BOLD}${BLUE}╚══════════════════════════════════════════════╝${NC}"
echo ""
echo -e "  ${CYAN}Samba · NFS · FTP · WebDAV · S3 · 面板${NC}"
echo -e "  ${CYAN}防火墙 · 入侵防护 · 监控告警 · 自动备份${NC}"
echo ""

# ── 1. 环境检测 ────────────────────────────────────────────
step_begin "检测系统环境"

# 普通用户直跑时先一次性缓存 sudo 密码（tty 交互由 sudo 自己完成，不走本脚本 stdin），
# 后续所有 $SUDO 前缀命令在缓存有效期内不再提示密码。root 直跑（EUID=0）跳过，SUDO 为空串。
if [ -n "$SUDO" ]; then
    sudo -v
fi

NAS_USER="${SUDO_USER:-$USER}"
if [ -z "$NAS_USER" ] || [ "$NAS_USER" = "root" ]; then
    echo ""
    echo -e "${RED}错误: 无法检测用户名，请不要直接以 root 账号登录运行（可用 sudo 或普通用户身份）${NC}"
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
# 三级优先级：
#   1. 显式提供 NAS_PASS 环境变量 → 用它（用户明确意图，覆盖任何现值）
#   2. 未提供，但目标机已有 .env 且 NAS_PASS 非空 → 保留现值不动（升级场景，
#      防止重跑 install.sh 把用户已改的密码静默打回出厂默认）
#   3. 两者皆无（全新安装）→ 出厂默认 + 首次登录强制改密
EXISTING_NAS_PASS=""
if [ -f "$REPO_DIR/.env" ]; then
    EXISTING_NAS_PASS=$(grep '^NAS_PASS=' "$REPO_DIR/.env" 2>/dev/null | head -1 | cut -d'=' -f2-)
fi

KEEP_EXISTING_PASS=false
if [ -n "${NAS_PASS:-}" ]; then
    if [ ${#NAS_PASS} -lt $MIN_PASS_LEN ]; then
        echo -e "\n${RED}错误: NAS_PASS 至少需要 ${MIN_PASS_LEN} 位${NC}"
        exit 1
    fi
elif [ -n "$EXISTING_NAS_PASS" ]; then
    # 升级场景：不提供 NAS_PASS 时绝不覆盖已有密码
    NAS_PASS="$EXISTING_NAS_PASS"
    KEEP_EXISTING_PASS=true
    echo -e "  ${GREEN}检测到已有配置，保留现有面板密码${NC}"
else
    # 全新安装，未提供密码 → 用出厂默认密码，首次登录面板后强制修改
    NAS_PASS="Nas-Test-2026"
    echo -e "  ${YELLOW}未提供 NAS_PASS，使用出厂默认密码（首次登录后强制修改）${NC}"
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

# 创建 /opt/nas 软链接（/opt 是系统目录，需要特权）
$SUDO ln -sfn "$REPO_DIR" "$INSTALL_DIR" 2>/dev/null || true

step_ok

# ── 3. 创建配置 ────────────────────────────────────────────
step_begin "创建配置文件"

if [ ! -f "$REPO_DIR/.env" ]; then
    cp "$REPO_DIR/.env.example" "$REPO_DIR/.env"
fi
# 保留现有密码分支下 .env 的 NAS_PASS 本来就是 $NAS_PASS，sed 本应是空操作；
# 但仍显式跳过写入，避免任何意外改写（如行尾空格、编码差异）影响已有 .env。
if [ "$KEEP_EXISTING_PASS" != true ]; then
    sed -i "s/^NAS_PASS=.*/NAS_PASS=${NAS_PASS}/" "$REPO_DIR/.env"
fi
# 确保 NAS_USER 在 .env 中（面板登录需要）
if ! grep -q "^NAS_USER=" "$REPO_DIR/.env"; then
    echo "NAS_USER=${NAS_USER}" >> "$REPO_DIR/.env"
fi

# 确保 .env 可被 sudo 读取
chmod 600 "$REPO_DIR/.env"

step_ok

# ── 4. 安装系统依赖 ────────────────────────────────────────
STEP=$((STEP + 1))
STEP_START=$(date +%s)
echo -e "\n${BOLD}${CYAN}[$STEP/$TOTAL]${NC} 安装系统软件包"

sub_step() { echo -e "  ${CYAN}→${NC} $1"; }
sub_done() { echo -e "  ${GREEN}✓${NC} $1 ($(($(date +%s) - SUB_START))s)"; }

# 检查是否需要更新 apt 源（速度测试：下载 < 100KB/s 则切换）
APT_SOURCES="/etc/apt/sources.list"
if [ -f "$APT_SOURCES" ] && grep -q "deb.debian.org" "$APT_SOURCES"; then
    sub_step "测试软件源速度 ..."
    SUB_START=$(date +%s)
    SPEED=$(curl -s --connect-timeout 5 --max-time 10 \
        -o /dev/null -w "%{speed_download}" \
        "http://deb.debian.org/debian/dists/trixie/main/binary-amd64/Packages.gz" 2>/dev/null || echo "0")
    SPEED_INT=$(echo "$SPEED" | cut -d. -f1)
    if [ -z "$SPEED_INT" ] || [ "$SPEED_INT" -lt 100000 ]; then
        $SUDO sed -i 's|http://deb\.debian\.org|http://mirrors.tuna.tsinghua.edu.cn|g' "$APT_SOURCES"
        $SUDO sed -i 's|http://security\.debian\.org|http://mirrors.tuna.tsinghua.edu.cn|g' "$APT_SOURCES"
        sub_done "官方源较慢，已切换到清华镜像"
    else
        sub_done "官方源速度正常"
    fi
fi

sub_step "更新软件包索引 (apt-get update) ..."
SUB_START=$(date +%s)
apt_retry bash -c "$SUDO apt-get update -qq 2>/dev/null"
sub_done "软件包索引已更新"

sub_step "下载安装 17 个软件包 (samba/nfs/vsftpd/rclone/fail2ban/ufw 等)"
echo -e "    ${YELLOW}此步骤视网络状况约需 2-10 分钟，请耐心等待（下方实时显示耗时）${NC}"
SUB_START=$(date +%s)
APT_LOG=$(mktemp)
# 后台计时器：每秒刷新已耗时，让用户知道安装仍在进行
( while true; do
    printf "\r    ⏱  已进行 %3ds ..." $(( $(date +%s) - SUB_START ))
    sleep 1
  done ) &
TIMER_PID=$!
APT_RC=0
# DEBIAN_FRONTEND 是环境变量赋值，`sudo VAR=val cmd` 不会按预期传递——
# 用 `sudo env VAR=val cmd` 显式透传（root 直跑时 $SUDO 为空，env 直接生效）。
apt_retry $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq -o DPkg::Lock::Timeout=60 \
    curl samba nfs-kernel-server vsftpd rclone fail2ban ufw \
    smartmontools unattended-upgrades smbclient nfs-common \
    xfsprogs parted mdadm lvm2 apache2-utils \
    > "$APT_LOG" 2>&1 || APT_RC=$?
kill $TIMER_PID 2>/dev/null || true; wait $TIMER_PID 2>/dev/null || true
printf "\r    %-40s\r" ""
if [ $APT_RC -ne 0 ]; then
    tail -10 "$APT_LOG"
    step_fail "软件包安装失败 (exit $APT_RC)"
    rm -f "$APT_LOG"
    exit 1
fi
rm -f "$APT_LOG"
sub_done "软件包安装完成"

step_ok

# ── 5-10. 运行 setup.sh ────────────────────────────────────
echo ""
echo -e "${BOLD}${BLUE}═══ 开始配置 NAS 服务 ═══${NC}"

# 用子 shell 跑 setup.sh，捕获输出但不影响主流程
# fm 用 sudo 跑时 SUDO_USER=fm 天然成立，setup.sh 自身的 ${NAS_USER:-${SUDO_USER:-$USER}}
# 兜底链能正确识别；root 直跑时 $SUDO 为空、$NAS_USER 显式透传（root 下 SUDO_USER 为空，
# 此处显式传入避免 setup.sh 兜底到 $USER=root 被拒）。
SETUP_LOG=$(mktemp)
if $SUDO env NAS_USER="$NAS_USER" bash "$INSTALL_DIR/scripts/setup.sh" > "$SETUP_LOG" 2>&1; then
    SETUP_OK=true
else
    SETUP_OK=false
fi

# 显示 setup.sh 的关键输出行（E: 前缀行是 apt/dpkg 的真实错误行，补进过滤器
# 让用户在回显里直接看到失败原因，而不是只看到"[1/10] 安装基础软件包..."）
grep -E '^\[|✓|✗|⚠|服务状态|部署完成|通过|失败|警告|^E:' "$SETUP_LOG" 2>/dev/null | while read line; do
    echo "  $line"
done
if [ "$SETUP_OK" != true ]; then
    echo ""
    echo -e "${RED}✗ setup.sh 执行失败(退出码非零),安装中止${NC}"
    # 失败路径证据保全：不删日志，移到固定路径供排查（此前直接 rm -f 销毁证据，
    # 提示却称"完整日志已保留"——谎言。现如实落盘并在提示中给出真实路径，
    # 并在过滤回显之后追加原始日志尾部，覆盖 E: 不匹配过滤器但确实是错误行的场景）
    FAILED_LOG="/tmp/nas-install-setup-$(date +%s).log"
    mv -f "$SETUP_LOG" "$FAILED_LOG" 2>/dev/null || cp -f "$SETUP_LOG" "$FAILED_LOG"
    echo -e "  完整日志: ${YELLOW}${FAILED_LOG}${NC};常见原因: 磁盘空间/网络/依赖冲突"
    echo "  ── setup.sh 日志尾部（未过滤）──"
    tail -30 "$FAILED_LOG" | sed 's/^/  /'
    exit 1
fi
rm -f "$SETUP_LOG"

# ── 检查 nas-panel 是否安装成功 ────────────────────────────
if [ ! -f "$PANEL_BIN" ]; then
    echo ""
    echo -e "${RED}✗ nas-panel 二进制下载失败${NC}"
    echo -e "  ${YELLOW}请手动编译后重新安装:${NC}"
    echo ""
    echo -e "  sudo apt-get install -y golang-go"
    echo -e "  cd $REPO_DIR/web"
    echo -e "  GOPROXY=https://goproxy.cn,direct go build -buildvcs=false -o $PANEL_BIN ."
    echo -e "  sudo env NAS_USER=$NAS_USER bash $REPO_DIR/scripts/setup.sh"
    echo ""
    exit 1
fi

# 确保面板服务正常运行
if [ -f "$PANEL_BIN" ]; then
    $SUDO systemctl reset-failed nas-panel 2>/dev/null || true
    $SUDO systemctl restart nas-panel 2>/dev/null || true
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
for svc in smbd nmbd nfs-kernel-server vsftpd rclone-webdav rclone-s3 fail2ban nas-panel; do
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
echo -e "  │ 密码:   ${GREEN}${NAS_PASS}${NC}"
echo -e "  └─────────────────────────────────────────┘"
echo ""
echo -e "  ${BOLD}其他访问方式${NC}"
echo -e "  WebDAV:       ${CYAN}http://${PRIMARY_IP}:8080${NC}"
echo -e "  Samba:        ${CYAN}\\\\\\\\${PRIMARY_IP}\\\\public${NC}"
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
echo -e "  ${CYAN}备份目录: /opt/nas/backups${NC}"
echo ""