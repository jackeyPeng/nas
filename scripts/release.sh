#!/bin/bash
# ═══════════════════════════════════════════════════════════════
# Z1 NAS — Release Builder & Publisher
#
# 用法:
#   bash scripts/release.sh beta     # 构建 beta 包，上传 R2
#   bash scripts/release.sh stable   # 构建 stable 包，上传 R2 + Gitee Release
#   bash scripts/release.sh build    # 仅构建，不上传
#
# 前置条件:
#   - 当前在 git 仓库内，HEAD 有 tag
#   - 安装了 Python 3 + boto3 (pip install boto3)
#   - ~/.hermes/.env 有 Cloudflare R2 凭证
# ═══════════════════════════════════════════════════════════════
set -euo pipefail

CHANNEL="${1:-}"
if [ "$CHANNEL" != "beta" ] && [ "$CHANNEL" != "stable" ] && [ "$CHANNEL" != "build" ]; then
    echo "用法: bash scripts/release.sh <beta|stable|build>"
    echo ""
    echo "  beta    — 构建 beta 包，上传到 R2 beta 目录"
    echo "  stable  — 构建 stable 包，上传到 R2 stable 目录 + Gitee Release"
    echo "  build   — 仅构建，不上传"
    exit 1
fi

# ── 配置 ─────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$SCRIPT_DIR"

# 版本号：从 git tag 取
VERSION="${VERSION:-$(git describe --tags --abbrev=0 2>/dev/null || echo "unknown")}"
if [ "$VERSION" = "unknown" ]; then
    echo "错误: 未找到 git tag，请先打 tag: git tag v1.3.0 && git push origin v1.3.0"
    exit 1
fi
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
ARCH_LIST="amd64 arm64"

# R2 配置（从 ~/.hermes/.env 读取）
R2_ENV="$HOME/.hermes/.env"
if [ -f "$R2_ENV" ]; then
    CLOUDFLARE_ID=$(grep '^CLOUDFLARE_ID=' "$R2_ENV" | cut -d'=' -f2- || true)
    CLOUDFLARE_KEY_ID=$(grep '^CLOUDFLARE_KEY_ID=' "$R2_ENV" | cut -d'=' -f2- || true)
    CLOUDFLARE_SECRET=$(grep '^CLOUDFLARE_SECRET=' "$R2_ENV" | cut -d'=' -f2- || true)
    CLOUDFLARE_S3_API=$(grep '^CLOUDFLARE_S3_API=' "$R2_ENV" | cut -d'=' -f2- || true)
fi

BUCKET="nas"
RELEASE_DIR="/tmp/z1-release-$$"
VERSION_TAG="$VERSION"
PACK_NAME="nas-${VERSION_TAG}"

# 颜色
RED='\033[0;31m'; GREEN='\033[0;32m'; BLUE='\033[0;34m'; BOLD='\033[1m'; NC='\033[0m'
info()  { echo -e "${BLUE}[INFO]${NC} $*"; }
ok()    { echo -e "${GREEN}[OK]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

echo -e "${BOLD}══════════════════════════════════════════════${NC}"
echo -e "${BOLD}  Z1 NAS Release Builder — ${VERSION_TAG} (${CHANNEL})${NC}"
echo -e "${BOLD}══════════════════════════════════════════════${NC}"
echo ""

# ── 清理 ─────────────────────────────────────────────────────────
rm -rf "$RELEASE_DIR"
mkdir -p "$RELEASE_DIR"

# ── 构建所有架构 ─────────────────────────────────────────────────
for ARCH in $ARCH_LIST; do
    info "Building for linux/${ARCH} ..."
    GOOS=linux GOARCH=$ARCH CGO_ENABLED=0 \
        go build -ldflags "\
            -s -w \
            -X nas-panel/modules/version.Version=${VERSION_TAG} \
            -X nas-panel/modules/version.DisplayVersion=${VERSION_TAG} \
            -X nas-panel/modules/version.BuildTime=${BUILD_TIME} \
            -X nas-panel/modules/version.GitCommit=${COMMIT}" \
        -o "${RELEASE_DIR}/nas-panel-${ARCH}" \
        ./web/

    # strip 进一步减小体积
    strip "${RELEASE_DIR}/nas-panel-${ARCH}" 2>/dev/null || true
    ok "  nas-panel-${ARCH} ($(du -h "${RELEASE_DIR}/nas-panel-${ARCH}" | cut -f1))"
done

# ── 打包 ─────────────────────────────────────────────────────────
for ARCH in $ARCH_LIST; do
    PKG_DIR="${RELEASE_DIR}/${PACK_NAME}-linux-${ARCH}"
    mkdir -p "$PKG_DIR"

    # 二进制
    cp "${RELEASE_DIR}/nas-panel-${ARCH}" "$PKG_DIR/nas-panel"

    # 前端文件
    mkdir -p "$PKG_DIR/frontend"
    cp web/frontend/index.html "$PKG_DIR/frontend/"
    cp web/frontend/style.css "$PKG_DIR/frontend/"
    cp web/frontend/app.js "$PKG_DIR/frontend/"
    cp web/frontend/alpinejs.min.js "$PKG_DIR/frontend/"

    # 脚本
    mkdir -p "$PKG_DIR/scripts"
    cp scripts/setup.sh "$PKG_DIR/scripts/"
    cp scripts/cleanup.sh "$PKG_DIR/scripts/"
    cp scripts/add-user.sh "$PKG_DIR/scripts/"
    cp scripts/remove-user.sh "$PKG_DIR/scripts/"
    cp scripts/backup-config.sh "$PKG_DIR/scripts/"
    cp scripts/restore-config.sh "$PKG_DIR/scripts/"
    cp scripts/monitor.sh "$PKG_DIR/scripts/"
    cp scripts/install.sh "$PKG_DIR/scripts/"

    # 配置
    mkdir -p "$PKG_DIR/configs"
    cp configs/smb.conf "$PKG_DIR/configs/"
    cp configs/vsftpd.conf "$PKG_DIR/configs/"
    cp configs/vsftpd.userlist "$PKG_DIR/configs/"
    cp configs/nfs.conf "$PKG_DIR/configs/"
    cp configs/exports "$PKG_DIR/configs/"
    cp configs/jail.local "$PKG_DIR/configs/"
    cp configs/nas-panel.service "$PKG_DIR/configs/"

    # .env 模板
    cp .env.example "$PKG_DIR/.env.example"

    # VERSION 文件
    cat > "$PKG_DIR/VERSION" << EOF
version=${VERSION_TAG}
channel=${CHANNEL}
commit=${COMMIT}
build_time=${BUILD_TIME}
arch=${ARCH}
EOF

    # 打包
    TARBALL="${PACK_NAME}-linux-${ARCH}.tar.gz"
    tar czf "${RELEASE_DIR}/${TARBALL}" -C "$RELEASE_DIR" "${PACK_NAME}-linux-${ARCH}"
    ok "  ${TARBALL} ($(du -h "${RELEASE_DIR}/${TARBALL}" | cut -f1))"
done

# ── 仅构建模式：到此结束 ─────────────────────────────────────────
if [ "$CHANNEL" = "build" ]; then
    echo ""
    ok "构建完成，产物在: $RELEASE_DIR"
    exit 0
fi

# ── 上传到 R2 ────────────────────────────────────────────────────
if [ -z "${CLOUDFLARE_S3_API:-}" ]; then
    warn "未配置 Cloudflare R2 (CLOUDFLARE_S3_API)，跳过上传"
    echo "产物在: $RELEASE_DIR"
    exit 0
fi

info "Uploading to Cloudflare R2 (${CHANNEL})..."

for ARCH in $ARCH_LIST; do
    TARBALL="${PACK_NAME}-linux-${ARCH}.tar.gz"
    LOCAL_PATH="${RELEASE_DIR}/${TARBALL}"

    # R2 路径: releases/{channel}/{tarball}
    R2_KEY="releases/${CHANNEL}/${TARBALL}"

    python3 -c "
import boto3
s3 = boto3.client('s3',
    endpoint_url='${CLOUDFLARE_S3_API}',
    aws_access_key_id='${CLOUDFLARE_KEY_ID}',
    aws_secret_access_key='${CLOUDFLARE_SECRET}',
    region_name='auto')
with open('${LOCAL_PATH}', 'rb') as f:
    s3.put_object(Bucket='${BUCKET}', Key='${R2_KEY}', Body=f,
        ContentType='application/gzip', CacheControl='no-cache')
print('  OK: ${R2_KEY}')
" || error "R2 上传失败: ${TARBALL}"
done

# ── 更新 latest 指针 ──────────────────────────────────────────────
if [ "$CHANNEL" = "stable" ]; then
    for ARCH in $ARCH_LIST; do
        TARBALL="${PACK_NAME}-linux-${ARCH}.tar.gz"
        LATEST_KEY="releases/stable/latest-linux-${ARCH}.tar.gz"

        python3 -c "
import boto3
s3 = boto3.client('s3',
    endpoint_url='${CLOUDFLARE_S3_API}',
    aws_access_key_id='${CLOUDFLARE_KEY_ID}',
    aws_secret_access_key='${CLOUDFLARE_SECRET}',
    region_name='auto')
s3.copy_object(
    Bucket='${BUCKET}',
    CopySource={'Bucket': '${BUCKET}', 'Key': 'releases/stable/${TARBALL}'},
    Key='${LATEST_KEY}',
    ContentType='application/gzip',
    CacheControl='no-cache')
print('  OK: ${LATEST_KEY}')
" || warn "latest 指针更新失败"
    done
fi

ok "上传完成"

# ── Gitee Release（仅 stable） ────────────────────────────────────
if [ "$CHANNEL" = "stable" ]; then
    info "Creating Gitee Release..."
    TOKEN=$(grep '^GITEE_TOKEN=' "$SCRIPT_DIR/.env" 2>/dev/null | cut -d'=' -f2- || echo "")

    if [ -n "$TOKEN" ]; then
        # 取 CHANGELOG 中当前版本的变更
        CHANGELOG_BODY=""
        if [ -f "$SCRIPT_DIR/CHANGELOG.md" ]; then
            CHANGELOG_BODY=$(awk "/^## \\[${VERSION_TAG##v}\\]/{flag=1; next} /^## \\[/{flag=0} flag" "$SCRIPT_DIR/CHANGELOG.md" 2>/dev/null || echo "")
        fi
        if [ -z "$CHANGELOG_BODY" ]; then
            CHANGELOG_BODY="Release ${VERSION_TAG}"
        fi

        # 上传 release 附件
        for ARCH in $ARCH_LIST; do
            TARBALL="${PACK_NAME}-linux-${ARCH}.tar.gz"
            curl -fsSL -X POST \
                "https://gitee.com/api/v5/repos/gitdogcat/nas/releases/${VERSION_TAG}/attach_files" \
                -H "Authorization: Bearer $TOKEN" \
                -F "file=@${RELEASE_DIR}/${TARBALL}" \
                -o /dev/null -w "%{http_code}" 2>/dev/null || true
        done
        ok "Gitee Release 附件已上传"
    else
        warn "未配置 GITEE_TOKEN，跳过 Gitee Release"
        echo "手动创建: https://gitee.com/gitdogcat/nas/releases/new"
    fi
fi

# ── 完成 ─────────────────────────────────────────────────────────
echo ""
echo -e "${GREEN}${BOLD}══════════════════════════════════════════════${NC}"
echo -e "${GREEN}${BOLD}  Release ${VERSION_TAG} (${CHANNEL}) 构建完成${NC}"
echo -e "${GREEN}${BOLD}══════════════════════════════════════════════${NC}"
echo ""
echo "  下载地址:"
for ARCH in $ARCH_LIST; do
    echo "  https://get.z1.sale/releases/${CHANNEL}/${PACK_NAME}-linux-${ARCH}.tar.gz"
done
if [ "$CHANNEL" = "stable" ]; then
    echo ""
    echo "  install.sh 默认拉取:"
    for ARCH in $ARCH_LIST; do
        echo "  https://get.z1.sale/releases/stable/latest-linux-${ARCH}.tar.gz"
    done
fi
echo ""
echo "  本地产物: $RELEASE_DIR"
echo ""