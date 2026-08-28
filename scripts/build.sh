#!/bin/bash
# ═══════════════════════════════════════════════════════════════
# nas-panel 统一构建脚本 — 版本号自动从 git 生成
#
# 用法:
#   bash scripts/build.sh              # 正式构建（读 RELEASE.md 或 git 自动版本）
#   bash scripts/build.sh dev          # 开发构建（版本号带 -dev 后缀）
#   VERSION=v1.4.0 bash scripts/build.sh   # 环境变量手动指定（优先级最高）
#
# 版本号优先级:
#   环境变量 VERSION > RELEASE.md 的 VERSION > git describe > short-commit
#
# 正式包检查清单（脚本自动执行）:
#   1. 工作区是否干净（不干净 → 版本号带 -dirty 并黄色警告）
#   2. RELEASE.md 填了 VERSION 时，与 git tag 核对提示
# ═══════════════════════════════════════════════════════════════
set -euo pipefail

cd "$(dirname "$0")/../web"

# ── 版本号决定 ────────────────────────────────────────────
if [ -z "${VERSION:-}" ]; then
    # 2) RELEASE.md
    if [ -f ../RELEASE.md ]; then
        RELEASE_VERSION=$(grep -E "^VERSION=" ../RELEASE.md | head -1 | cut -d= -f2- | tr -d '[:space:]')
        if [ -n "$RELEASE_VERSION" ]; then
            VERSION="$RELEASE_VERSION"
            echo "📌 从 RELEASE.md 读取版本: ${VERSION}"
        fi
    fi
fi
if [ -z "${VERSION:-}" ]; then
    # 3) git 自动
    if GIT_TAG=$(git describe --tags --exact-match 2>/dev/null); then
        VERSION="$GIT_TAG"
    elif GIT_DESC=$(git describe --tags --dirty 2>/dev/null); then
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

# ── 打包前确认信息 ────────────────────────────────────────
echo "──────────────────────────────────────"
echo "  nas-panel 构建"
echo "  版本:   ${VERSION}"
echo "  提交:   ${GIT_COMMIT}"
echo "  时间:   ${BUILD_TIME}"

# 工作区干净度检查（正式版必须是干净的）
if [ -n "$(git status --porcelain 2>/dev/null)" ] && [[ "$VERSION" != *dev* ]]; then
    echo ""
    echo "  ⚠️  警告: 工作区有未提交改动"
    if [[ "$VERSION" != *dirty* ]]; then
        VERSION="${VERSION}-dirty"
        echo "     版本号已自动追加 -dirty: ${VERSION}"
        LDFLAGS="-s -w \
  -X nas-panel/modules/version.Version=${VERSION} \
  -X nas-panel/modules/version.BuildTime=${BUILD_TIME} \
  -X nas-panel/modules/version.GitCommit=${GIT_COMMIT}"
    fi
    echo "     正式发版请先 commit 所有改动!"
fi

# RELEASE.md 版本与 git tag 一致性提示
if [ -f ../RELEASE.md ]; then
    RELEASE_VERSION=$(grep -E "^VERSION=" ../RELEASE.md | head -1 | cut -d= -f2- | tr -d '[:space:]')
    if [ -n "$RELEASE_VERSION" ]; then
        if git rev-parse "refs/tags/${RELEASE_VERSION}" >/dev/null 2>&1; then
            echo "  ✅ git tag ${RELEASE_VERSION} 已存在"
        else
            echo "  ℹ️  git tag ${RELEASE_VERSION} 尚未创建（打包后记得: git tag ${RELEASE_VERSION}）"
        fi
    fi
fi
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
echo "  版本验证: curl localhost:8090/api/version | grep version"