#!/bin/bash
# Upload FileBrowser binary to get.z1.sale (R2)
# Run on a machine that has the binary
set -e

FB_BIN="/usr/local/bin/filebrowser"
FB_VER="v2.32.0"
ARCH="amd64"
TAR_NAME="linux-${ARCH}-filebrowser.tar.gz"
URL="https://get.z1.sale/filebroswer/${TAR_NAME}"

if [ ! -f "$FB_BIN" ]; then
    echo "Error: $FB_BIN not found"
    echo "Install first: curl -fsSL https://github.com/filebrowser/filebrowser/releases/download/${FB_VER}/linux-${ARCH}-filebrowser.tar.gz | tar xz -C /usr/local/bin"
    exit 1
fi

# Create tar
cd /tmp
cp "$FB_BIN" filebrowser
tar czf "$TAR_NAME" filebrowser
echo "Created: $TAR_NAME ($(stat -c%s $TAR_NAME | numfmt --to=iec))"

# Upload to R2 (requires rclone or aws-cli configured)
if command -v rclone &>/dev/null; then
    echo "Uploading via rclone..."
    rclone copyto "/tmp/$TAR_NAME" "r2:nas/$TAR_NAME" && echo "Done!"
elif [ -f ~/soft/nas/scripts/upload-r2.sh ]; then
    echo "Uploading via upload-r2.sh..."
    cp "/tmp/$TAR_NAME" ~/soft/nas/
    cd ~/soft/nas && bash scripts/upload-r2.sh
else
    echo ""
    echo "Manual upload:"
    echo "  scp /tmp/$TAR_NAME <server>:~/"
    echo "  Then upload to https://get.z1.sale/$TAR_NAME"
fi