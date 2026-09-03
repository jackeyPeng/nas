#!/bin/bash
# ═══════════════════════════════════════════════════════════════
# Z1 NAS — 面板一键升级脚本（二进制级，不碰 /data）
#
# 用法:
#   sudo bash upgrade.sh            # 升级到 control 通道最新版
#   sudo bash upgrade.sh --check    # 仅检查是否有新版本，不升级
#
# 数据安全保障:
#   1. 只替换 /usr/local/bin/nas-panel 二进制 + 重启 nas-panel，不碰 /data
#   2. 升级前自动跑 backup-config.sh 备份配置到 /data/backups
#   3. 升级前备份旧二进制 nas-panel.bak.<时间戳>，失败自动回滚
#   4. 下载后校验 ELF + 大小，替换后健康检查，起不来自动回滚
#
# 下载源（control 通道，与 setup.sh 安装流程一致）:
#   https://get.z1.sale/control/nas-panel-${ARCH}.latest
#   https://get.z1.sale/control/nas-panel.latest
# ═══════════════════════════════════════════════════════════════
set -euo pipefail

PANEL_BIN="/usr/local/bin/nas-panel"
PANEL_PORT=8090
BACKUP_SCRIPT="/opt/nas/scripts/backup-config.sh"
DOWNLOAD_BASE="https://get.z1.sale/control"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'
BLUE='\033[0;34m'; CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'
CHECKMARK="✓"; CROSSMARK="✗"

info()  { echo -e "${BLUE}[INFO]${NC} $*"; }
ok()    { echo -e "${GREEN}[${CHECKMARK}]${NC} $*"; }
warn()  { echo -e "${YELLOW}[⚠]${NC} $*"; }
fail()  { echo -e "${RED}[${CROSSMARK}]${NC} $*"; }

CHECK_ONLY=false
if [ "${1:-}" = "--check" ]; then
    CHECK_ONLY=true
fi

# ── 1. 权限检查 ──────────────────────────────────────────────
if [ "$EUID" -ne 0 ]; then
    echo "错误: 请使用 sudo 运行: sudo bash upgrade.sh"
    exit 1
fi

# ── 2. 架构检测 ──────────────────────────────────────────────
detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)  ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *) echo "错误: 不支持的 CPU 架构: $(uname -m)"; exit 1 ;;
    esac
}
detect_arch

echo -e "${BOLD}══════════════════════════════════════════════${NC}"
echo -e "${BOLD}  Z1 NAS 面板一键升级${NC}"
echo -e "${BOLD}══════════════════════════════════════════════${NC}"
echo "  架构: ${ARCH}"
echo ""

# ── 3. 前置检查 ──────────────────────────────────────────────
if [ ! -f "$PANEL_BIN" ]; then
    fail "未找到 $PANEL_BIN，此机器可能尚未安装 NAS 面板"
    exit 1
fi
for cmd in curl systemctl; do
    command -v "$cmd" >/dev/null 2>&1 || { fail "缺少命令: $cmd"; exit 1; }
done

# ── 4. 记录当前版本 ──────────────────────────────────────────
get_running_version() {
    curl -s --max-time 5 "http://localhost:${PANEL_PORT}/api/version" 2>/dev/null \
        | grep -oE '"version"\s*:\s*"[^"]*"' | head -1 | sed 's/.*"\([^"]*\)"$/\1/' \
        || echo "unknown"
}
OLD_VERSION=$(get_running_version)
info "当前版本: ${OLD_VERSION}"

# ── 下载新二进制到临时文件 ──────────────────────────────────
TMP_BIN=$(mktemp)
trap 'rm -f "$TMP_BIN"' EXIT

info "下载新二进制（${ARCH}）..."
DOWNLOAD_OK=false
for url in \
    "${DOWNLOAD_BASE}/nas-panel-${ARCH}.latest" \
    "${DOWNLOAD_BASE}/nas-panel.latest"; do
    echo "  尝试: $url"
    if curl -fsSL --connect-timeout 15 --max-time 180 -o "$TMP_BIN" "$url" 2>/dev/null; then
        if [ -s "$TMP_BIN" ]; then
            DOWNLOAD_OK=true
            break
        fi
    fi
done

if [ "$DOWNLOAD_OK" != "true" ]; then
    fail "下载失败，所有源均不可用"
    exit 1
fi

# ── 校验下载产物：必须是非空 ELF 二进制 ──────────────────────
MAGIC=$(head -c 4 "$TMP_BIN" | od -An -tx1 | tr -d ' \n')
if [ "$MAGIC" != "7f454c46" ]; then
    fail "下载的不是有效 ELF 二进制（文件头 ${MAGIC}，可能被拦截或返回 HTML）"
    exit 1
fi
NEW_SIZE=$(stat -c%s "$TMP_BIN")
if [ "$NEW_SIZE" -lt 5000000 ]; then
    fail "下载的二进制过小（${NEW_SIZE} 字节），疑似损坏"
    exit 1
fi

# 从二进制里提取版本号（ldflags 注入的 Version 字符串，用 grep -a 读二进制，
# 不依赖 binutils 的 strings 命令——老机器可能没装）
NEW_VERSION=$(grep -a -o -m1 'v[0-9][0-9]*\.[0-9][0-9]*[-0-9A-Za-z.]*' "$TMP_BIN" | head -1 || echo "unknown")
ok "下载完成: ${NEW_SIZE} 字节, 版本 ${NEW_VERSION}"

# ── --check 模式到此结束 ─────────────────────────────────────
if [ "$CHECK_ONLY" = true ]; then
    echo ""
    OLD_MD5=$(md5sum "$PANEL_BIN" 2>/dev/null | awk '{print $1}')
    NEW_MD5=$(md5sum "$TMP_BIN" 2>/dev/null | awk '{print $1}')
    if [ "$OLD_MD5" = "$NEW_MD5" ]; then
        info "已是最新版本（当前 ${OLD_VERSION}）"
    else
        ok "有新版本可用: ${OLD_VERSION} → ${NEW_VERSION}"
    fi
    exit 0
fi

# ── 5. 备份配置 ──────────────────────────────────────────────
echo ""
info "升级前备份配置..."
if [ -f "$BACKUP_SCRIPT" ]; then
    bash "$BACKUP_SCRIPT" || warn "配置备份失败，继续升级（可手动重试 backup-config.sh）"
else
    warn "未找到 $BACKUP_SCRIPT，跳过配置备份"
fi

# ── 6. 备份旧二进制 ──────────────────────────────────────────
BACKUP_BIN="${PANEL_BIN}.bak.$(date +%Y%m%d%H%M%S)"
info "备份旧二进制 → ${BACKUP_BIN}"
cp -a "$PANEL_BIN" "$BACKUP_BIN"

# ── 7. 原子替换 + 重启 ───────────────────────────────────────
info "替换二进制并重启 nas-panel..."
chmod +x "$TMP_BIN"
mv "$TMP_BIN" "$PANEL_BIN"
systemctl restart nas-panel

# ── 8. 健康检查 + 自动回滚 ───────────────────────────────────
info "健康检查..."
HEALTHY=false
for i in $(seq 1 15); do
    sleep 2
    if systemctl is-active --quiet nas-panel; then
        if curl -s --max-time 5 "http://localhost:${PANEL_PORT}/api/version" >/dev/null 2>&1; then
            HEALTHY=true
            break
        fi
    fi
done

if [ "$HEALTHY" != "true" ]; then
    echo ""
    fail "新版本启动失败或面板无响应，执行自动回滚..."
    mv "$BACKUP_BIN" "$PANEL_BIN"
    chmod +x "$PANEL_BIN"
    systemctl restart nas-panel
    sleep 3
    if systemctl is-active --quiet nas-panel; then
        ok "已回滚到旧版本 ${OLD_VERSION}"
        echo "  日志: journalctl -u nas-panel --no-pager -n 50"
    else
        fail "回滚后面板仍未启动，请检查: journalctl -u nas-panel --no-pager -n 50"
    fi
    exit 1
fi

# ── 完成 ──────────────────────────────────────────────────────
RUNNING_VERSION=$(get_running_version)
echo ""
echo -e "${BOLD}${GREEN}══════════════════════════════════════════════${NC}"
echo -e "${BOLD}${GREEN}  升级完成！${NC}"
echo -e "${BOLD}${GREEN}══════════════════════════════════════════════${NC}"
echo "  升级前版本: ${OLD_VERSION}"
echo "  升级后版本: ${RUNNING_VERSION}"
echo "  旧二进制备份: ${BACKUP_BIN}"
echo ""
echo "  回滚方法（如需要）:"
echo "    sudo cp ${BACKUP_BIN} ${PANEL_BIN} && sudo systemctl restart nas-panel"
echo ""
