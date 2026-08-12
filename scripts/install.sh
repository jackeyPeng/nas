#!/bin/bash
# ═══════════════════════════════════════════════════════════════
# Z1 NAS — One-line Installer
#
# Usage:
#   curl -fsSL https://get.z1.sale/install.sh | bash
#   NAS_PASS=myPass123 curl -fsSL https://get.z1.sale/install.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/jackeyPeng/nas/master/scripts/install.sh | bash
# ═══════════════════════════════════════════════════════════════
set -euo pipefail

# ── 配置 ─────────────────────────────────────────────────────────
GITHUB_RAW="https://raw.githubusercontent.com/jackeyPeng/nas/master/scripts/install.sh"
GITEE_RAW="https://gitee.com/gitdogcat/nas/raw/master/scripts/install.sh"
GITHUB_CLONE="https://github.com/jackeyPeng/nas.git"
GITEE_CLONE="https://gitee.com/gitdogcat/nas.git"
INSTALL_DIR="$HOME/soft/nas"
MIN_PASS_LEN=12

# ── 颜色输出 ──────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
BOLD='\033[1m'
NC='\033[0m'

info()  { echo -e "${BLUE}[INFO]${NC} $*"; }
ok()    { echo -e "${GREEN}[OK]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

# ── 横幅 ──────────────────────────────────────────────────────────
echo ""
echo -e "${BOLD}╔══════════════════════════════════════════╗${NC}"
echo -e "${BOLD}║          Z1 NAS — Installer              ║${NC}"
echo -e "${BOLD}║          Open-source NAS OS              ║${NC}"
echo -e "${BOLD}╚══════════════════════════════════════════╝${NC}"
echo ""

# ── 检测网络 & 选择源 ────────────────────────────────────────────
info "Detecting network..."
SRC=""
CLONE_URL=""

if curl -sSf --connect-timeout 3 -o /dev/null "$GITHUB_RAW" 2>/dev/null; then
    SRC="github"
    CLONE_URL="$GITHUB_CLONE"
    ok "Using GitHub (international)"
elif curl -sSf --connect-timeout 5 -o /dev/null "$GITEE_RAW" 2>/dev/null; then
    SRC="gitee"
    CLONE_URL="$GITEE_CLONE"
    ok "Using Gitee (China mirror)"
else
    error "Cannot reach GitHub or Gitee. Please check your network connection."
fi

# ── 检测 git ─────────────────────────────────────────────────────
HAS_GIT=0
if command -v git &>/dev/null; then
    HAS_GIT=1
fi

# ── 下载仓库 ─────────────────────────────────────────────────────
if [ -d "$INSTALL_DIR/.git" ]; then
    warn "Found existing clone at $INSTALL_DIR, pulling updates..."
    cd "$INSTALL_DIR"
    git pull --rebase 2>/dev/null || true
    cd - >/dev/null
    ok "Repository updated"
elif [ -d "$INSTALL_DIR" ]; then
    warn "Directory $INSTALL_DIR exists but is not a git repo, backing up..."
    mv "$INSTALL_DIR" "${INSTALL_DIR}.backup.$(date +%Y%m%d%H%M%S)"
    info "Backed up to ${INSTALL_DIR}.backup.*"
    NEED_CLONE=1
else
    NEED_CLONE=1
fi

if [ "${NEED_CLONE:-0}" = "1" ] || [ ! -d "$INSTALL_DIR/.git" ]; then
    info "Cloning Z1 NAS repository to $INSTALL_DIR ..."
    mkdir -p "$(dirname "$INSTALL_DIR")"

    if [ "$HAS_GIT" = "1" ]; then
        git clone --depth 1 "$CLONE_URL" "$INSTALL_DIR"
        ok "Repository cloned (git)"
    else
        warn "git not found, falling back to tar download..."
        # 没 git 时下载 tar 包
        if [ "$SRC" = "github" ]; then
            TAR_URL="https://github.com/jackeyPeng/nas/archive/refs/heads/master.tar.gz"
        else
            TAR_URL="https://gitee.com/gitdogcat/nas/repository/archive/master.tar.gz"
        fi

        if command -v wget &>/dev/null; then
            wget -qO /tmp/z1-nas.tar.gz "$TAR_URL"
        elif command -v curl &>/dev/null; then
            curl -fsSL -o /tmp/z1-nas.tar.gz "$TAR_URL"
        else
            error "Neither wget nor curl found. Please install git or wget/curl."
        fi

        mkdir -p "$INSTALL_DIR"
        tar xzf /tmp/z1-nas.tar.gz -C "$INSTALL_DIR" --strip-components=1
        rm -f /tmp/z1-nas.tar.gz
        ok "Repository downloaded (tar)"
    fi
fi

cd "$INSTALL_DIR"

# ── 交互式设密码 ─────────────────────────────────────────────────
if [ -f ".env" ]; then
    ok ".env already exists, skipping password setup"
else
    if [ -n "${NAS_PASS:-}" ]; then
        # 环境变量传入密码
        if [ "${#NAS_PASS}" -lt "$MIN_PASS_LEN" ]; then
            error "NAS_PASS is too short (min $MIN_PASS_LEN chars)"
        fi
        info "Using NAS_PASS from environment variable"
    else
        # 交互式输入密码
        echo ""
        echo -e "${BOLD}Set your NAS password (min $MIN_PASS_LEN chars):${NC}"
        echo -e "This password is used for: Samba, FTP, WebDAV, FileBrowser, S3, Web Panel"
        echo ""

        while true; do
            read -s -p "Password: " NAS_PASS
            echo ""
            if [ "${#NAS_PASS}" -lt "$MIN_PASS_LEN" ]; then
                warn "Password too short, minimum $MIN_PASS_LEN characters"
                continue
            fi
            read -s -p "Confirm:   " NAS_PASS2
            echo ""
            if [ "$NAS_PASS" != "$NAS_PASS2" ]; then
                warn "Passwords do not match, try again"
                continue
            fi
            break
        done
    fi

    # 自动检测用户名
    NAS_USER="${NAS_USER:-$(whoami)}"

    # 生成 .env
    cp .env.example .env
    sed -i "s/^NAS_PASS=.*/NAS_PASS=$NAS_PASS/" .env 2>/dev/null || echo "NAS_PASS=$NAS_PASS" >> .env
    sed -i "s/^NAS_USER=.*/NAS_USER=$NAS_USER/" .env 2>/dev/null || echo "NAS_USER=$NAS_USER" >> .env

    ok ".env configured (user: $NAS_USER)"
fi

# ── 运行 setup.sh ────────────────────────────────────────────────
info "Starting deployment (this takes 5-10 minutes)..."
echo ""

if [ ! -f "scripts/setup.sh" ]; then
    error "scripts/setup.sh not found. Repository may be incomplete."
fi

sudo bash scripts/setup.sh

# ── 完成提示 ─────────────────────────────────────────────────────
echo ""
echo -e "${GREEN}${BOLD}══════════════════════════════════════════════${NC}"
echo -e "${GREEN}${BOLD}  Z1 NAS installation complete!            ${NC}"
echo -e "${GREEN}${BOLD}══════════════════════════════════════════════${NC}"
echo ""

# 获取 IP
NAS_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
if [ -z "$NAS_IP" ]; then
    NAS_IP="<NAS_IP>"
fi

NAS_USER="${NAS_USER:-$(whoami)}"
NAS_PASS_DISPLAY="${NAS_PASS:-(see .env file)}"

echo -e "  Web Panel:  ${BOLD}http://$NAS_IP:8090${NC}"
echo -e "  FileBrowser: ${BOLD}http://$NAS_IP:8081${NC}"
echo -e "  Samba:       ${BOLD}\\\\$NAS_IP\\shared${NC}"
echo -e "  WebDAV:      ${BOLD}http://$NAS_IP:8080${NC}"
echo ""
echo -e "  Username:    ${BOLD}$NAS_USER${NC}"
echo -e "  Password:    ${BOLD}(set during install)${NC}"
echo ""
echo -e "  Config file: ${BOLD}$INSTALL_DIR/.env${NC}"
echo -e "  Documentation: ${BOLD}https://www.z1.sale${NC}"
echo -e "  GitHub:      ${BOLD}https://github.com/jackeyPeng/nas${NC}"
echo -e "  Gitee:       ${BOLD}https://gitee.com/gitdogcat/nas${NC}"
echo ""

# 安全提醒
if [ -z "${NAS_PASS:-}" ]; then
    info "Password was entered interactively and is not shown here."
    info "To change password: edit $INSTALL_DIR/.env and run 'sudo systemctl restart nas-panel'"
fi

echo ""
echo -e "${BOLD}Happy NAS-ing!${NC}"
echo ""
