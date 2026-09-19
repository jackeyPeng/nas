#!/bin/bash
# upload-filebrowser.sh — 上传 FileBrowser tarball 到 R2（nas bucket）
#
# 版本单一事实源: web/common/versions.go 的 FileBrowserVersion。
# 此脚本自动从该常量读取版本，从 GitHub 官方 release 下载对应 tarball，
# 上传到两个 key:
#   filebrowser/linux-${ARCH}-filebrowser.tar.gz   （拼写正确的新 key）
#   filebroswer/linux-${ARCH}-filebrowser.tar.gz   （旧拼写 key，兼容已装机器的旧面板/setup.sh）
#
# 凭证: ~/.hermes/.env 的 CLOUDFLARE_KEY_ID / CLOUDFLARE_SECRET / CLOUDFLARE_S3_API
# 用法: bash scripts/upload-filebrowser.sh [amd64|arm64|armv7]（默认 amd64）
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARCH="${1:-amd64}"
R2_ENV="$HOME/.hermes/.env"

# 从 Go 常量读版本（单一事实源），读不到就报错——不许静默用旧版本号
FB_VER=$(grep -oP 'FileBrowserVersion\s*=\s*"\K[^"]+' "$REPO_ROOT/web/common/versions.go")
if [ -z "$FB_VER" ]; then
    echo "错误: 无法从 web/common/versions.go 读取 FileBrowserVersion"
    exit 1
fi

TAR_NAME="linux-${ARCH}-filebrowser.tar.gz"
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

echo "FileBrowser ${FB_VER} (${ARCH})"

# 下载官方 release tarball（GitHub 原生结构: linux-${ARCH}-filebrowser.tar.gz）
DL_URL="https://github.com/filebrowser/filebrowser/releases/download/${FB_VER}/${TAR_NAME}"
echo "下载: $DL_URL"
if ! curl -fsSL --connect-timeout 15 --max-time 300 -o "$TMP_DIR/$TAR_NAME" "$DL_URL"; then
    echo "下载失败，尝试镜像..."
    curl -fsSL --connect-timeout 15 --max-time 300 -o "$TMP_DIR/$TAR_NAME" "https://ghfast.top/$DL_URL"
fi

# 校验是有效 gzip 且含 filebrowser 可执行文件
tar tzf "$TMP_DIR/$TAR_NAME" | grep -qx "filebrowser" || {
    echo "错误: tarball 内没有顶层 filebrowser 文件，结构异常"
    exit 1
}

CLOUDFLARE_KEY_ID=$(grep '^CLOUDFLARE_KEY_ID=' "$R2_ENV" | cut -d'=' -f2-)
CLOUDFLARE_SECRET=$(grep '^CLOUDFLARE_SECRET=' "$R2_ENV" | cut -d'=' -f2-)
CLOUDFLARE_S3_API=$(grep '^CLOUDFLARE_S3_API=' "$R2_ENV" | cut -d'=' -f2-)

for KEY in "filebrowser/${TAR_NAME}" "filebroswer/${TAR_NAME}"; do
    python3 -c "
import boto3
s3 = boto3.client('s3',
    endpoint_url='${CLOUDFLARE_S3_API}',
    aws_access_key_id='${CLOUDFLARE_KEY_ID}',
    aws_secret_access_key='${CLOUDFLARE_SECRET}',
    region_name='auto')
with open('${TMP_DIR}/${TAR_NAME}', 'rb') as f:
    s3.put_object(Bucket='nas', Key='${KEY}', Body=f,
        ContentType='application/gzip', CacheControl='no-cache')
print('  OK: ${KEY}')
"
done

echo "完成: https://get.z1.sale/filebrowser/${TAR_NAME}"
