#!/bin/bash
# ═══════════════════════════════════════════════════════════════
# Z1 NAS — One-line Installer (Panel Only)
#
# Installs the Z1 web management panel. All NAS services
# (Samba, NFS, FTP, WebDAV, etc.) are installed from the web UI.
#
# Usage:
#   curl -fsSL https://get.z1.sale/install.sh | bash
#   NAS_PASS=myPass123 curl -fsSL https://get.z1.sale/install.sh | bash
# ═══════════════════════════════════════════════════════════════
set -euo pipefail

# ── Config ──────────────────────────────────────────────────
INSTALL_DIR="/opt/nas"
PANEL_BIN="/usr/local/bin/nas-panel"
RELEASE_URL="https://get.z1.sale/nas-panel-latest-linux-amd64"
MIN_PASS_LEN=12

# ── Colors ──────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'
BLUE='\033[0;34m'; BOLD='\033[1m'; NC='\033[0m'
info()  { echo -e "${BLUE}[INFO]${NC} $*"; }
ok()    { echo -e "${GREEN}[OK]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

# ── Banner ──────────────────────────────────────────────────
echo ""
echo -e "${BOLD}╔══════════════════════════════════════════╗${NC}"
echo -e "${BOLD}║          Z1 NAS — Panel Installer        ║${NC}"
echo -e "${BOLD}║          Web Panel + On-demand Services  ║${NC}"
echo -e "${BOLD}╚══════════════════════════════════════════╝${NC}"
echo ""

# ── Root check ──────────────────────────────────────────────
if [ "$EUID" -ne 0 ]; then
    error "Please run with sudo: curl ... | sudo bash"
fi

# ── Detect user ─────────────────────────────────────────────
NAS_USER="${SUDO_USER:-$USER}"
if [ -z "$NAS_USER" ] || [ "$NAS_USER" = "root" ]; then
    error "Cannot detect user. Please run with sudo (not as root directly)"
fi
info "User: $NAS_USER"

# ── Password ────────────────────────────────────────────────
if [ -n "${NAS_PASS:-}" ]; then
    if [ ${#NAS_PASS} -lt $MIN_PASS_LEN ]; then
        error "NAS_PASS must be at least $MIN_PASS_LEN characters"
    fi
    info "Using password from NAS_PASS environment variable"
else
    echo ""
    echo -e "${BOLD}Set your NAS admin password (min $MIN_PASS_LEN chars)${NC}"
    while true; do
        read -s -p "Password: " NAS_PASS
        echo ""
        if [ ${#NAS_PASS} -lt $MIN_PASS_LEN ]; then
            warn "Password too short (min $MIN_PASS_LEN)"
            continue
        fi
        read -s -p "Confirm: " CONFIRM
        echo ""
        if [ "$NAS_PASS" != "$CONFIRM" ]; then
            warn "Passwords do not match"
            continue
        fi
        break
    done
fi

# ── Step 1: Install dependencies ────────────────────────────
info "[1/5] Installing dependencies..."
apt-get update -qq 2>/dev/null
apt-get install -y -qq xfsprogs parted lvm2 mdadm smartmontools curl 2>&1 | tail -1
ok "Dependencies installed"

# ── Step 2: Download panel binary ───────────────────────────
info "[2/5] Downloading Z1 panel..."
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
BINARY_URL="https://get.z1.sale/nas-panel-latest-linux-${ARCH}"

if curl -fsSL --connect-timeout 10 --max-time 120 -o "$PANEL_BIN" "$BINARY_URL" 2>/dev/null; then
    chmod +x "$PANEL_BIN"
    ok "Panel binary installed ($(stat -c%s "$PANEL_BIN" | numfmt --to=iec))"
else
    warn "Cannot download from get.z1.sale, trying fallback..."
    # Fallback: try to find nas-panel in the repo if cloned
    if [ -f "$HOME/soft/nas/web/nas-panel" ]; then
        cp "$HOME/soft/nas/web/nas-panel" "$PANEL_BIN"
        chmod +x "$PANEL_BIN"
        ok "Panel binary installed from local repo"
    else
        error "Cannot download panel binary. Check network or install manually."
    fi
fi

# ── Step 3: Create config ───────────────────────────────────
info "[3/5] Creating configuration..."
mkdir -p "$INSTALL_DIR/data"
TOKEN=$(openssl rand -hex 32 2>/dev/null || python3 -c "import secrets;print(secrets.token_hex(32))")
cat > "$INSTALL_DIR/.env" << EOF
NAS_USER=$NAS_USER
NAS_PASS=$NAS_PASS
NAS_TOKEN=$TOKEN
NAS_DATA_DIR=$INSTALL_DIR/data
EOF
chmod 600 "$INSTALL_DIR/.env"
ok "Configuration created ($INSTALL_DIR/.env)"

# ── Step 4: Setup systemd service ───────────────────────────
info "[4/5] Setting up systemd service..."
cat > /etc/systemd/system/nas-panel.service << EOF
[Unit]
Description=Z1 NAS Web Management Panel
After=network.target

[Service]
Type=simple
EnvironmentFile=$INSTALL_DIR/.env
Environment=NAS_USER=$NAS_USER
ExecStart=$PANEL_BIN
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable nas-panel
ok "Systemd service created"

# ── Step 5: Start panel ─────────────────────────────────────
info "[5/5] Starting Z1 panel..."
systemctl restart nas-panel
sleep 2
if systemctl is-active --quiet nas-panel; then
    ok "Panel is running"
else
    warn "Panel may not have started. Check: journalctl -u nas-panel"
fi

# ── Done ────────────────────────────────────────────────────
IP=$(ip route get 1 2>/dev/null | awk '{print $7;exit}' || echo "YOUR_IP")
echo ""
echo -e "${BOLD}╔══════════════════════════════════════════╗${NC}"
echo -e "${BOLD}║          Z1 NAS Panel Ready!             ║${NC}"
echo -e "${BOLD}╚══════════════════════════════════════════╝${NC}"
echo ""
echo -e "  Panel URL:  ${GREEN}http://${IP}:8090${NC}"
echo -e "  Username:   ${GREEN}${NAS_USER}${NC}"
echo -e "  Password:   ${GREEN}(as entered)${NC}"
echo ""
echo -e "  ${YELLOW}Next: Open the panel, go to \"服务管理\","
echo -e "  and click \"一键安装所有服务\" to install"
echo -e "  Samba, NFS, FTP, WebDAV, and more.${NC}"
echo ""