#!/bin/bash
# mkapp.sh — 托管应用打包签名工具（APP-SPEC v0.1 §1 发布物三件套）
#
# 用法:
#   bash scripts/mkapp.sh <app-dir> <out.tar.gz> [key-dir]
#
# 参数:
#   <app-dir>   应用源目录（如 examples/hello-app），须含 manifest.json/
#               icon.png（或 tools/genicon.go 可生成）/main.go/unit.tpl/README.md
#   <out.tar.gz> 输出的应用包路径，同目录下还会生成 <out.tar.gz>.sha256 和 .sig
#   [key-dir]   ed25519 签名私钥所在目录（须含 dev.key），默认
#               web/modules/apps/testdata/devkey（M1 期开发密钥，§1「验签公钥
#               编译进核心」——生产签发走正式密钥时覆盖此参数）
#
# 环境变量:
#   GOARCH      交叉编译目标架构，默认 amd64（纯 Go 无 CGO，可交叉编译；
#               本脚本按当前架构单包，不在一个包里混多架构二进制）
#
# 步骤: 编译 → 组装 <id>-<version>/ 目录 → tar.gz → sha256 → ed25519 签名
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'
info() { echo -e "${BLUE}[INFO]${NC} $*"; }
ok() { echo -e "${GREEN}[OK]${NC} $*"; }
error() {
    echo -e "${RED}[ERROR]${NC} $*" >&2
    exit 1
}

if [ $# -lt 2 ] || [ $# -gt 3 ]; then
    echo "用法: bash scripts/mkapp.sh <app-dir> <out.tar.gz> [key-dir]"
    exit 1
fi

APP_DIR="$1"
OUT_TARBALL="$2"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KEY_DIR="${3:-$REPO_ROOT/web/modules/apps/testdata/devkey}"
GOARCH="${GOARCH:-amd64}"

[ -d "$APP_DIR" ] || error "应用目录不存在: $APP_DIR"
APP_DIR="$(cd "$APP_DIR" && pwd)"

MANIFEST="$APP_DIR/manifest.json"
[ -f "$MANIFEST" ] || error "缺少 manifest.json: $MANIFEST"

KEY_FILE="$KEY_DIR/dev.key"
[ -f "$KEY_FILE" ] || error "缺少签名私钥: $KEY_FILE"

command -v go >/dev/null 2>&1 || error "需要 go 命令（未在 PATH 中找到）"

# ── 从 manifest.json 取 id/version（纯 shell，不依赖 jq：按 Go 项目约定本仓库其它
#    脚本也不强制要求 jq；用 python3 解析，python3 是 scripts/gen-sbom.py 等既有依赖） ──
command -v python3 >/dev/null 2>&1 || error "需要 python3（用于解析 manifest.json）"
APP_ID="$(python3 -c "import json; print(json.load(open('$MANIFEST'))['id'])")"
APP_VERSION="$(python3 -c "import json; print(json.load(open('$MANIFEST'))['version'])")"
[ -n "$APP_ID" ] || error "manifest.json 缺少 id 字段"
[ -n "$APP_VERSION" ] || error "manifest.json 缺少 version 字段"

info "打包应用: $APP_ID v$APP_VERSION（GOARCH=$GOARCH）"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

PKG_NAME="${APP_ID}-${APP_VERSION}"
PKG_DIR="$WORK_DIR/$PKG_NAME"
mkdir -p "$PKG_DIR/app"

# ── 编译 ─────────────────────────────────────────────────────────
info "交叉编译 linux/$GOARCH ..."
(
    cd "$APP_DIR"
    CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" go build -trimpath -ldflags "-s -w" -o "$PKG_DIR/app/$APP_ID" .
)
ok "二进制: app/$APP_ID ($(du -h "$PKG_DIR/app/$APP_ID" | cut -f1))"

# ── manifest / unit.tpl / README ────────────────────────────────
cp "$MANIFEST" "$PKG_DIR/manifest.json"

[ -f "$APP_DIR/unit.tpl" ] || error "缺少 unit.tpl: $APP_DIR/unit.tpl"
cp "$APP_DIR/unit.tpl" "$PKG_DIR/unit.tpl"

if [ -f "$APP_DIR/README.md" ]; then
    cp "$APP_DIR/README.md" "$PKG_DIR/README.md"
fi

# ── icon.png：应用目录自带则直接用，否则用 tools/genicon.go 生成 ──
if [ -f "$APP_DIR/icon.png" ]; then
    cp "$APP_DIR/icon.png" "$PKG_DIR/icon.png"
elif [ -f "$APP_DIR/tools/genicon.go" ]; then
    info "生成 icon.png（tools/genicon.go）..."
    (cd "$APP_DIR" && go run tools/genicon.go "$PKG_DIR/icon.png")
else
    error "缺少 icon.png，且无 tools/genicon.go 可生成"
fi
ok "icon.png ($(du -h "$PKG_DIR/icon.png" | cut -f1))"

# ── tar.gz（§1 包布局：根目录固定为 <id>-<version>/） ──────────
mkdir -p "$(dirname "$OUT_TARBALL")"
OUT_TARBALL="$(cd "$(dirname "$OUT_TARBALL")" && pwd)/$(basename "$OUT_TARBALL")"
tar czf "$OUT_TARBALL" -C "$WORK_DIR" "$PKG_NAME"
ok "打包: $OUT_TARBALL ($(du -h "$OUT_TARBALL" | cut -f1))"

# ── sha256 ───────────────────────────────────────────────────────
SHA_FILE="${OUT_TARBALL}.sha256"
sha256sum "$OUT_TARBALL" | awk '{print $1}' >"$SHA_FILE"
ok "摘要: $SHA_FILE ($(cat "$SHA_FILE"))"

# ── ed25519 签名（scripts/appsign，§1「ed25519 签名（对 tar.gz 的 detached
#    signature；验签公钥编译进核心）」） ──────────────────────────
SIG_FILE="${OUT_TARBALL}.sig"
(cd "$REPO_ROOT" && go run scripts/appsign/main.go sign "$OUT_TARBALL" "$KEY_FILE" "$SIG_FILE")
ok "签名: $SIG_FILE"

echo ""
ok "三件套就绪: $OUT_TARBALL / $SHA_FILE / $SIG_FILE"
