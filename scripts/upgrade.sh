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
#   2. 升级前自动跑 backup-config.sh 备份配置到 /opt/nas/backups
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

# ── 校验签名（与 OTA update.go 同一套: SHA256 + ed25519）────────
# manifest 由 release.sh 签名生成。公钥与 web/modules/update/update.go 内嵌的完全一致。
OTA_PUBKEY_HEX="af048a1224405d29d06a5e97d795f525a78ef239b9bcf6c051c9cea633f5457e"
MANIFEST_URL="${DOWNLOAD_BASE}/latest-${ARCH}.json"
TMP_MANIFEST=$(mktemp)
trap 'rm -f "$TMP_BIN" "$TMP_MANIFEST"' EXIT
SIG_VERIFIED=false
if curl -fsSL --connect-timeout 15 --max-time 30 -o "$TMP_MANIFEST" "$MANIFEST_URL" 2>/dev/null \
   && command -v openssl >/dev/null 2>&1; then
    M_SHA=$(sed -n 's/.*"sha256"[[:space:]]*:[[:space:]]*"\([0-9a-f]*\)".*/\1/p' "$TMP_MANIFEST" | head -1)
    M_SIG=$(sed -n 's/.*"sig"[[:space:]]*:[[:space:]]*"\([0-9a-f]*\)".*/\1/p' "$TMP_MANIFEST" | head -1)
    LOCAL_SHA=$(sha256sum "$TMP_BIN" | awk '{print $1}')
    if [ -n "$M_SHA" ] && [ "$LOCAL_SHA" != "$M_SHA" ]; then
        fail "SHA256 校验失败（本地 ${LOCAL_SHA} ≠ manifest ${M_SHA}），拒绝安装"
        exit 1
    fi
    if [ -n "$M_SIG" ]; then
        PUB_PEM=$(mktemp); SIG_BIN=$(mktemp)
        # shellcheck disable=SC2064
        trap "rm -f '$TMP_BIN' '$TMP_MANIFEST' '$PUB_PEM' '$SIG_BIN'" EXIT
        # hex→binary 用纯 bash printf（目标机不一定有 xxd）
        hex2bin() {
            local h="$1" i
            for ((i=0; i<${#h}; i+=2)); do printf "\\x${h:i:2}"; done
        }
        { printf "\\x30\\x2a\\x30\\x05\\x06\\x03\\x2b\\x65\\x70\\x03\\x21\\x00"; hex2bin "$OTA_PUBKEY_HEX"; } \
            | openssl pkey -pubin -inform DER -out "$PUB_PEM" 2>/dev/null
        hex2bin "$M_SIG" > "$SIG_BIN"
        if openssl pkeyutl -verify -pubin -inkey "$PUB_PEM" -rawin \
               -in "$TMP_BIN" -sigfile "$SIG_BIN" >/dev/null 2>&1; then
            SIG_VERIFIED=true
            ok "SHA256 + ed25519 签名校验通过"
        else
            fail "ed25519 签名校验失败，拒绝安装（可能被篡改或下载损坏）"
            exit 1
        fi
    fi
else
    warn "无法获取 manifest 或缺少 openssl，跳过签名校验（仅 ELF+大小检查）"
fi
[ "$SIG_VERIFIED" = true ] || warn "本次升级未经过签名校验，请确认下载源可信"

# 从二进制里提取版本号。ldflags 注入的 buildinfo 里存有
# `version.Version=vX.Y.Z` 字符串，以此为锚点最可靠。
# （不能用裸 grep 第一个 vX.Y.Z —— 二进制里混有依赖的版本串，会抓到 v1.0.0 之类）
NEW_VERSION=$(grep -a -o -m1 'version\.Version=v[0-9][0-9.]*[-0-9A-Za-z.]*' "$TMP_BIN" \
    | sed 's/^version\.Version=//' || echo "unknown")
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
# 注意: 不能直接 mv $TMP_BIN（/tmp 与 /usr/local/bin 跨文件系统时 mv=copy+delete，
# 中途断电会留下半截二进制且旧文件已删）。先 install 到同目录 .new，再同 fs rename（原子）。
info "替换二进制并重启 nas-panel..."
install -m 0755 "$TMP_BIN" "${PANEL_BIN}.new"
mv "${PANEL_BIN}.new" "$PANEL_BIN"
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

# ── 9. 配置迁移（幂等重生成托管配置）─────────────────────────
# 先更新 /opt/nas 仓库拿新版配置模板（失败不阻塞），再跑 setup.sh --config-only。
# NAS_USER 从 nas-panel.service 的 Environment 读（root 环境下无 SUDO_USER）。
sync_configs() {
    local repo="/opt/nas"
    [ -d "$repo/scripts" ] || { warn "未找到 $repo/scripts，跳过配置同步"; return 0; }
    if [ -d "$repo/.git" ]; then
        info "更新配置模板（git pull）..."
        # root 跑非 root 仓库会撞 git safe.directory 检查，-c 显式放行本仓库
        local repo_real
        repo_real=$(readlink -f "$repo")
        git -c safe.directory="$repo_real" -C "$repo" pull --ff-only 2>/dev/null || warn "git pull 失败（本地有改动？），用现有模板继续"
    fi
    local nas_user
    nas_user=$(awk -F= '/^Environment=NAS_USER=/{print $3; exit}' /etc/systemd/system/nas-panel.service 2>/dev/null | tr -d '"')
    info "同步托管配置（setup.sh --config-only，NAS_USER=${nas_user:-未检测到}）..."
    if NAS_USER="$nas_user" bash "$repo/scripts/setup.sh" --config-only >/tmp/nas-config-sync.log 2>&1; then
        ok "托管配置已同步到新版模板"
    else
        warn "配置同步失败（不影响二进制升级），日志: /tmp/nas-config-sync.log，可手动重跑: sudo bash /opt/nas/scripts/setup.sh --config-only"
    fi
}
sync_configs

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
echo "    sudo mv ${BACKUP_BIN} ${PANEL_BIN} && sudo systemctl restart nas-panel"
echo "    （必须用 mv 原子替换，cp 会因「文本文件忙」失败）"
echo ""
