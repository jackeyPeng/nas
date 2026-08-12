#!/bin/bash
# ═══════════════════════════════════════════════════════════════
# upload-r2.sh — Upload install.sh to Cloudflare R2
#
# Reads credentials from .env:
#   CLOUDFLARE_ID         — Cloudflare Account ID
#   CLOUDFLARE_KEY        — (unused, kept for compat)
#   CLOUDFLARE_KEY_ID     — R2 Access Key ID
#   CLOUDFLARE_SECRET     — R2 Secret Access Key
#   CLOUDFLARE_S3_API    — R2 S3 API endpoint
#
# Bucket: NAS
# Uploads: scripts/install.sh → install.sh
# ═══════════════════════════════════════════════════════════════
set -euo pipefail

# ── 找 .env ──────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

ENV_FILE=""
for p in "$PROJECT_DIR/.env" "$HOME/soft/nas/.env" "$HOME/.env"; do
    if [ -f "$p" ]; then
        ENV_FILE="$p"
        break
    fi
done

if [ -z "$ENV_FILE" ]; then
    echo "ERROR: .env file not found"
    echo "Create ~/soft/nas/.env with CLOUDFLARE_ID, CLOUDFLARE_KEY_ID, CLOUDFLARE_SECRET, CLOUDFLARE_S3_API"
    exit 1
fi

echo "[INFO] Loading credentials from $ENV_FILE"
set -a
source "$ENV_FILE"
set +a

# ── 校验 ──────────────────────────────────────────────────────────
BUCKET="NAS"
INSTALL_SH="$SCRIPT_DIR/install.sh"

if [ ! -f "$INSTALL_SH" ]; then
    echo "ERROR: $INSTALL_SH not found"
    exit 1
fi

for var in CLOUDFLARE_ID CLOUDFLARE_KEY_ID CLOUDFLARE_SECRET CLOUDFLARE_S3_API; do
    val=$(eval echo "\$$var")
    if [ -z "$val" ]; then
        echo "ERROR: $var not set in $ENV_FILE"
        exit 1
    fi
done

echo "[INFO] Bucket: $BUCKET"
echo "[INFO] Endpoint: $CLOUDFLARE_S3_API"
echo "[INFO] Uploading install.sh ..."

# ── 上传 ──────────────────────────────────────────────────────────
export AWS_ACCESS_KEY_ID="$CLOUDFLARE_KEY_ID"
export AWS_SECRET_ACCESS_KEY="$CLOUDFLARE_SECRET"
export AWS_DEFAULT_REGION="auto"

# 用 aws-cli 或 rclone 上传
if command -v aws &>/dev/null; then
    aws s3 cp "$INSTALL_SH" "s3://$BUCKET/install.sh" \
        --endpoint-url "$CLOUDFLARE_S3_API" \
        --content-type "text/x-shellscript" \
        --cache-control "no-cache"
    echo "[OK] Uploaded via aws-cli"
elif command -v rclone &>/dev/null; then
    # rclone 需要预配置 remote，这里用环境变量方式
    rclone copy "$INSTALL_SH" ":s3,provider=Cloudflare,endpoint=$CLOUDFLARE_S3_API,access_key_id=$CLOUDFLARE_KEY_ID,secret_access_key=$CLOUDFLARE_SECRET:$BUCKET/" \
        --no-check-certificate
    echo "[OK] Uploaded via rclone"
else
    echo "ERROR: Neither aws-cli nor rclone found. Install one of them:"
    echo "  sudo apt install awscli   # or"
    echo "  curl https://rclone.org/install.sh | sudo bash"
    exit 1
fi

echo ""
echo "[DONE] install.sh uploaded to R2 bucket: $BUCKET"
echo ""
echo "  Public URL (via file.abwen.com): https://file.abwen.com/nas/install.sh"
echo "  Short URL (if configured):       https://get.z1.sale"
echo ""
echo "  Test: curl -fsSL https://file.abwen.com/nas/install.sh | bash"
