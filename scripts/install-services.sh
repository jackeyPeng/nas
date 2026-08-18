#!/bin/bash
# 115 服务安装脚本
# 在 10.216.10.115 上运行: sudo bash install-services.sh
set -e

echo "=== 安装 NAS 服务 ==="

# 1. Samba
echo "[1/7] 安装 Samba..."
apt-get install -y samba 2>&1 | tail -1
systemctl enable smbd nmbd
systemctl start smbd nmbd

# 2. NFS
echo "[2/7] 安装 NFS..."
apt-get install -y nfs-kernel-server 2>&1 | tail -1
systemctl enable nfs-kernel-server
systemctl start nfs-kernel-server

# 3. FTP
echo "[3/7] 安装 FTP..."
apt-get install -y vsftpd 2>&1 | tail -1
systemctl enable vsftpd
systemctl start vsftpd

# 4. WebDAV (rclone)
echo "[4/7] 安装 WebDAV..."
apt-get install -y rclone 2>&1 | tail -1
cat > /etc/systemd/system/rclone-webdav.service << 'EOF'
[Unit]
Description=Rclone WebDAV Server
After=network.target
[Service]
Type=simple
ExecStart=/usr/bin/rclone serve webdav /data --addr :8080 --user fm --pass nas1234567890
Restart=on-failure
[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable rclone-webdav
systemctl start rclone-webdav

# 5. FileBrowser
echo "[5/7] 安装 FileBrowser..."
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
FB_VER="v2.32.0"
curl -fsSL "https://get.z1.sale/filebrowser_${FB_VER}_linux_${ARCH}.tar.gz" -o /tmp/fb.tar.gz 2>/dev/null || \
curl -fsSL "https://file.abwen.com/control/filebrowser_${FB_VER}_linux_${ARCH}.tar.gz" -o /tmp/fb.tar.gz 2>/dev/null || \
curl -fsSL "https://github.com/filebrowser/filebrowser/releases/download/${FB_VER}/linux-${ARCH}-filebrowser.tar.gz" -o /tmp/fb.tar.gz 2>/dev/null
if [ -f /tmp/fb.tar.gz ]; then
    tar xzf /tmp/fb.tar.gz -C /usr/local/bin filebrowser
    chmod +x /usr/local/bin/filebrowser
fi
cat > /etc/systemd/system/filebrowser.service << EOF
[Unit]
Description=FileBrowser
After=network.target
[Service]
Type=simple
ExecStart=/usr/local/bin/filebrowser -a :8081 -r /data --username fm --password nas1234567890
Restart=on-failure
[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable filebrowser
systemctl start filebrowser

# 6. S3 (rclone)
echo "[6/7] 安装 S3..."
cat > /etc/systemd/system/rclone-s3.service << 'EOF'
[Unit]
Description=Rclone S3 Server
After=network.target
[Service]
Type=simple
ExecStart=/usr/bin/rclone serve s3 /data --addr :9000
Restart=on-failure
[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable rclone-s3
systemctl start rclone-s3

# 7. Fail2ban
echo "[7/7] 安装 Fail2ban..."
apt-get install -y fail2ban 2>&1 | tail -1
systemctl enable fail2ban
systemctl start fail2ban

echo ""
echo "=== 全部完成 ==="
systemctl status smbd nmbd nfs-kernel-server vsftpd rclone-webdav filebrowser rclone-s3 fail2ban --no-pager -l | grep -E "Loaded|Active"