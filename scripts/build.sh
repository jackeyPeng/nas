#!/bin/bash
# ═══════════════════════════════════════════════════════════════
# nas-panel 统一构建脚本 — 版本号自动从 git 生成
#
# 用法:
#   bash scripts/build.sh              # 正式构建（版本号 + strip）
#   bash scripts/build.sh dev          # 开发构建（版本号带 -dev 时间戳）
#   VERSION=v1.4.0 bash scripts/build.sh   # 手动指定版本
# ═══════════════════════════════════════════════════════════════
set -euo pipefail

cd "$(dirname "$0")/../web"

# ── 版本号：环境变量 > git tag > git describe > dev ────────
if [ -z "${VERSION:-}" ]; then
    if GIT_TAG=$(git describe --tags --exact-match 2>/dev/null); then
        VERSION="$GIT_TAG"
    elif GIT_DESC=$(git describe --tags --dirty 2>/dev/null); then
        # v1.3.0-5-g1a2b3c4 或 v1.3.0-dirty
        VERSION="$GIT_DESC"
    else
        VERSION="v0.0.0-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
    fi
    if [ "${1:-}" = "dev" ]; then
        VERSION="$VERSION-dev"
    fi
fi

GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS="-s -w \
  -X nas-panel/modules/version.Version=${VERSION} \
  -X nas-panel/modules/version.BuildTime=${BUILD_TIME} \
  -X nas-panel/modules/version.GitCommit=${GIT_COMMIT}"

echo "──────────────────────────────────────"
echo "  nas-panel 构建"
echo "  版本:   ${VERSION}"
echo "  提交:   ${GIT_COMMIT}"
echo "  时间:   ${BUILD_TIME}"
echo "──────────────────────────────────────"

GOPROXY=${GOPROXY:-https://goproxy.cn,direct} \
    go build -buildvcs=false -ldflags "${LDFLAGS}" -o nas-panel .
strip nas-panel 2>/dev/null || true

# 产物命名: nas-panel-<version>（软链 nas-panel 保持兼容）
cp nas-panel "nas-panel-${VERSION}"
ls -la "nas-panel-${VERSION}"

echo ""
echo "✓ 构建完成: web/nas-panel-${VERSION}"
echo "  兼容副本: web/nas-panel"
echo "  版本验证: ./nas-panel --version 2>/dev/null || curl localhost:8090/api/version"