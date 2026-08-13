#!/bin/bash
# ═══════════════════════════════════════════════════════════════
# Z1 NAS — One-line Installer
#
# Usage:
#   curl -fsSL https://get.z1.sale/install.sh | bash
#   NAS_PASS=myPass123 curl -fsSL https://get.z1.sale/install.sh | bash
#   NAS_CHANNEL=beta curl -fsSL https://get.z1.sale/install.sh | bash
#   NAS_VERSION=v1.3.0 curl -fsSL https://get.z1.sale/install.sh | bash
# ═══════════════════════════════════════════════════════════════
set -euo pipefail

# ── 配置 ─────────────────────────────────────────────────────────
GITHUB_RAW="https://raw.githubusercontent.com/jackeyPeng/nas/master/scripts/install.sh"
GITEE_RAW="https://gitee.com/gitdogcat/nas/raw/master/scripts/install.sh"
GITHUB_CLONE="https://github.com/jackeyPeng/nas.git"
GITEE_CLONE="https://gitee.com/gitdogcat/nas.git"
INSTALL_DIR="$HOME/soft/nas"
MIN_PASS_LEN=12

# 发行版下载配置
# 环境变量可选: NAS_CHANNEL=beta|stable (默认stable), NAS_VERSION=v1.3.0 (默认latest)
NAS_CHANNEL="${NAS_CHANNEL:-stable}"
RELEASE_BASE="https://get.z1.sale/releases/${NAS_CHANNEL}"

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

# ── 网络检测 ─────────────────────────────────────────────────────
info "Checking network..."
SRC=""
if curl -sSf --connect-timeout 3 -o /dev/null "$GITHUB_RAW" 2>/dev/null; then
    SRC="github"
    ok "Using GitHub (international)"
elif curl -sSf --connect-timeout 5 -o /dev/null "$GITEE_RAW" 2>/dev/null; then
    SRC="gitee"
    ok "Using Gitee (China mirror)"
else
    warn "Cannot reach GitHub or Gitee — will try R2 release download"
fi

# ── 检测架构 ─────────────────────────────────────────────────────
ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64)  ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) warn "Architecture $ARCH may not have pre-built binaries" ;;
esac

# ── 尝试下载预编译包 ──────────────────────────────────────────────
BINARY_MODE=false
if [ -n "${NAS_VERSION:-}" ]; then
    TARBALL="nas-${NAS_VERSION}-linux-${ARCH}.tar.gz"
    TAR_URL="${RELEASE_BASE}/${TARBALL}"
elif [ -n "${NAS_CHANNEL:-}" ] && [ "$NAS_CHANNEL" != "source" ]; then
    # 走 latest 或 channel 下具体版本
    if [ "$NAS_CHANNEL" = "stable" ]; then
        TAR_URL="${RELEASE_BASE}/latest-linux-${ARCH}.tar.gz"
    else
        # beta 通道走具体版本探测 – 先试试 latest 指针
        TAR_URL="${RELEASE_BASE}/latest-linux-${ARCH}.tar.gz"
    fi
fi

if [ -n "${TAR_URL:-}" ]; then
    info "Trying pre-built release: $TAR_URL"
    TMP_DIR="/tmp/z1-install-$$"
    mkdir -p "$TMP_DIR"

    if curl -fsSL --connect-timeout 10 --max-time 60 -o "$TMP_DIR/release.tar.gz" "$TAR_URL" 2>/dev/null; then
        tar xzf "$TMP_DIR/release.tar.gz" -C "$TMP_DIR" --strip-components=1 2>/dev/null || true
        # 找到解压出来的目录
        EXTRACTED_DIR=$(find "$TMP_DIR" -maxdepth 1 -type d -name "nas-*" | head -1)
        if [ -n "$EXTRACTED_DIR" ] && [ -f "$EXTRACTED_DIR/scripts/setup.sh" ]; then
            ok "Pre-built release downloaded"
            INSTALL_SRC="$EXTRACTED_DIR"
            BINARY_MODE=true
        else
            # 直接解压到根
            if [ -f "$TMP_DIR/scripts/setup.sh" ]; then
                ok "Pre-built release downloaded"
                INSTALL_SRC="$TMP_DIR"
                BINARY_MODE=true
            else
                warn "Downloaded release is incomplete, falling back to source"
            fi
        fi
    else
        warn "Pre-built release not available for ${ARCH}, falling back to source"
    fi
fi

# ── 源码模式（git clone 或 tar 下载） ──────────────────────────────
if [ "$BINARY_MODE" = false ]; then
    info "Using source mode (git clone or tar download)"

    if [ -d "$INSTALL_DIR/.git" ]; then
        warn "Found existing clone at $INSTALL_DIR, pulling updates..."
        cd "$INSTALL_DIR"
        git pull --rebase 2>/dev/null || true
        cd - >/dev/null
        ok "Repository updated"
        INSTALL_SRC="$INSTALL_DIR"
    elif [ -d "$INSTALL_DIR" ]; then
        warn "Directory $INSTALL_DIR exists but not a git repo, backing up..."
        mv "$INSTALL_DIR" "${INSTALL_DIR}.backup.$(date +%Y%m%d%H%M%S)"
        info "Backed up to ${INSTALL_DIR}.backup.*"
        NEED_CLONE=1
    else
        NEED_CLONE=1
    fi

    if [ "${NEED_CLONE:-0}" = "1" ] || [ ! -d "$INSTALL_DIR/.git" ]; then
        info "Cloning Z1 NAS repository to $INSTALL_DIR ..."
        mkdir -p "$(dirname "$INSTALL_DIR")"

        if command -v git &>/dev/null; then
            CLONE_URL="$GITEE_CLONE"
            [ "$SRC" = "github" ] && CLONE_URL="$GITHUB_CLONE"
            git clone --depth 1 "$CLONE_URL" "$INSTALL_DIR"
            ok "Repository cloned (git)"
        else
            warn "git not found, falling back to tar download..."
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
        INSTALL_SRC="$INSTALL_DIR"
    fi
fi

cd "$INSTALL_SRC"

# ── 交互式设密码 ─────────────────────────────────────────────────
if [ -f ".env" ]; then
    ok ".env already exists, skipping password setup"
else
    if [ -n "${NAS_PASS:-}" ]; then
        if [ "${#NAS_PASS}" -lt "$MIN_PASS_LEN" ]; then
            error "NAS_PASS is too short (min $MIN_PASS_LEN chars)"
        fi
        info "Using NAS_PASS from environment variable"
    else
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

    NAS_USER="${NAS_USER:-$(whoami)}"

    cp .env.example .env
    sed -i "s/^NAS_PASS=.*/NAS_PASS=$NAS_PASS/" .env 2>/dev/null || echo "NAS_PASS=$NAS_PASS" >> .env
    sed -i "s/^NAS_USER=.*/NAS_USER=$NAS_USER/" .env 2>/dev/null || echo "NAS_USER=$NAS_USER" >> .env

    ok ".env configured (user: $NAS_USER)"
fi

# ── 运行 setup.sh ────────────────────────────────────────────────
info "Starting deployment (this takes 5-10 minutes)..."
echo ""

if [ ! -f "scripts/setup.sh" ]; then
    error "scripts/setup.sh not found. Release may be incomplete."
fi

sudo bash scripts/setup.sh

# ── 完成提示 ─────────────────────────────────────────────────────
echo ""
echo -e "${GREEN}${BOLD}══════════════════════════════════════════════${NC}"
echo -e "${GREEN}${BOLD}  Z1 NAS installation complete!            ${NC}"
echo -e "${GREEN}${BOLD}══════════════════════════════════════════════${NC}"
echo ""

NAS_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
[ -z "$NAS_IP" ] && NAS_IP="<NAS_IP>"
NAS_USER="${NAS_USER:-$(whoami)}"

echo -e "  Web Panel:  ${BOLD}http://$NAS_IP:8090${NC}"
echo -e "  FileBrowser: ${BOLD}http://$NAS_IP:8081${NC}"
echo -e "  Samba:       ${BOLD}\\\\\\\\$NAS_IP\\\\shared${NC}"
echo -e "  WebDAV:      ${BOLD}http://$NAS_IP:8080${NC}"
echo ""

if [ "$BINARY_MODE" = true ]; then
    echo -e "  Install mode: ${BOLD}Pre-built binary (${NAS_CHANNEL})${NC}"
else
    echo -e "  Install mode: ${BOLD}Source build${NC}"
fi
echo -e "  Username:    ${BOLD}$NAS_USER${NC}"
echo -e "  Config file: ${BOLD}$INSTALL_DIR/.env${NC}"
echo -e "  Documentation: ${BOLD}https://www.z1.sale${NC}"
echo ""

if [ -z "${NAS_PASS:-}" ]; then
    info "Password was entered interactively and is not shown here."
fi

echo ""
echo -e "${BOLD}Happy NAS-ing!${NC}"
echo ""