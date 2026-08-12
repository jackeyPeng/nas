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
for p in "$HOME/.hermes/.env" "$PROJECT_DIR/.env" "$HOME/soft/nas/.env"; do
    if [ -f "$p" ]; then
        ENV_FILE="$p"
        break
    fi
done

if [ -z "$ENV_FILE" ]; then
    echo "ERROR: .env file not found"
    echo "Checked: ~/.hermes/.env, $PROJECT_DIR/.env, ~/soft/nas/.env"
    echo "Create ~/.hermes/.env with CLOUDFLARE_ID, CLOUDFLARE_KEY_ID, CLOUDFLARE_SECRET, CLOUDFLARE_S3_API"
    exit 1
fi

echo "[INFO] Loading credentials from $ENV_FILE"
set -a
source "$ENV_FILE"
set +a

# ── 校验 ──────────────────────────────────────────────────────────
BUCKET="nas"
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

# ── 上传（boto3，最兼容 R2）─────────────────────────────────────
UPLOAD_FILE="$INSTALL_SH" BUCKET="$BUCKET" REMOTE_KEY="install.sh" python3 -c "
import boto3, sys, os

s3 = boto3.client('s3',
    endpoint_url=os.environ['CLOUDFLARE_S3_API'],
    aws_access_key_id=os.environ['CLOUDFLARE_KEY_ID'],
    aws_secret_access_key=os.environ['CLOUDFLARE_SECRET'],
    region_name='auto'
)

upload_file = os.environ.get('UPLOAD_FILE', '')
bucket = os.environ.get('BUCKET', '')
remote_key = os.environ.get('REMOTE_KEY', '')

with open(upload_file, 'rb') as f:
    s3.put_object(
        Bucket=bucket,
        Key=remote_key,
        Body=f,
        ContentType='text/x-shellscript',
        CacheControl='no-cache'
    )
print('[OK] Uploaded', remote_key, 'to R2 bucket:', bucket)
" 2>&1 || {
    echo "ERROR: Upload failed (boto3 not installed?)"
    echo "Install: pip3 install boto3"
    exit 1
}

echo ""
echo "[DONE] install.sh uploaded to R2 bucket: $BUCKET"
echo ""
Public URL: https://file.abwen.com/install.sh
Test:        curl -fsSL https://file.abwen.com/install.sh | bash

(To use https://get.z1.sale/install.sh, configure DNS redirect to file.abwen.com/install.sh)
