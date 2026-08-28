#!/bin/bash
# NAS 一键部署脚本
# 用于在新机器上部署完整的 NAS 系统
# 用法: sudo ./setup.sh
#
# 包含服务: Samba, NFS, FTP, WebDAV, FileBrowser, MinIO, NAS Web Panel
# 安全: Fail2ban, UFW 防火墙, unattended-upgrades
# 10步部署, 自动检测当前用户

set -e

# ==================== 架构检测 ====================
# 将 uname -m 映射为 FileBrowser/MinIO 使用的架构名
detect_arch() {
    local arch
    arch=$(uname -m)
    case "$arch" in
        x86_64|amd64)
            ARCH="amd64"
            GOARCH="amd64"
            ;;
        aarch64|arm64)
            ARCH="arm64"
            GOARCH="arm64"
            ;;
        armv7l|armv7|arm)
            ARCH="armv7"
            GOARCH="arm"
            ;;
        *)
            echo "错误: 不支持的 CPU 架构: $arch"
            echo "支持的架构: x86_64 (amd64), aarch64 (arm64), armv7l (armv7)"
            exit 1
            ;;
    esac
    echo "检测到 CPU 架构: $arch → 下载标识: $ARCH"
}
detect_arch

# 检查 root 权限
if [ "$EUID" -ne 0 ]; then
    echo "错误: 请使用 sudo 运行此脚本"
    exit 1
fi

echo "========================================="
echo "家用 NAS 一键部署脚本"
echo "========================================="
echo ""

# ==================== 配置变量 ====================
# 自动获取当前用户（执行 sudo 的用户）
NAS_USER="${SUDO_USER:-$USER}"
if [ -z "$NAS_USER" ] || [ "$NAS_USER" = "root" ]; then
    echo "错误: 无法自动检测用户名，请使用 sudo 运行（而非直接以 root 身份）"
    exit 1
fi
DATA_DIR="/data"
NAS_DIR="/opt/nas"
FILEBROWSER_VERSION="v2.63.17"
BUNDLE_VERSION="v1.0.0"

# 获取脚本所在目录的绝对路径（仓库根目录）
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# ==================== 离线包检测 ====================
# 离线包包含 FileBrowser、nas-panel 二进制及所有配置文件
# 放在仓库根目录或 /tmp 下即可自动检测
OFFLINE_BUNDLE=""
for path in \
    "$SCRIPT_DIR/nas-bundle-${BUNDLE_VERSION}-${ARCH}.tar.gz" \
    "$SCRIPT_DIR/nas-bundle-latest-${ARCH}.tar.gz" \
    "/tmp/nas-bundle-${BUNDLE_VERSION}-${ARCH}.tar.gz" \
    "/tmp/nas-bundle-latest-${ARCH}.tar.gz"; do
    if [ -f "$path" ]; then
        OFFLINE_BUNDLE="$path"
        echo "📦 发现离线包: $OFFLINE_BUNDLE"
        break
    fi
done

if [ -n "$OFFLINE_BUNDLE" ]; then
    echo "   将使用离线包安装 FileBrowser 和 nas-panel"
    echo ""
fi

# ==================== 升级前自动备份 ====================
if [ -d "$DATA_DIR" ] && [ -f "/etc/samba/smb.conf" ]; then
    echo "检测到已有 NAS 配置，执行升级前备份..."
    bash "$SCRIPT_DIR/scripts/backup-config.sh" 2>/dev/null || echo "  ⚠ 备份失败，继续部署"
    echo ""
fi

# 从 .env 文件读取密码
ENV_FILE="$SCRIPT_DIR/.env"
if [ ! -f "$ENV_FILE" ]; then
    echo "错误: 未找到 $ENV_FILE"
    echo ""
    echo "请先创建配置文件："
    echo "  cp $SCRIPT_DIR/.env.example $ENV_FILE"
    echo "  nano $ENV_FILE"
    echo ""
    echo "必填项："
    echo "  NAS_PASS=你的密码（至少12位，用于所有服务）"
    echo ""
    echo "可选项："
    echo "  GITEE_TOKEN=你的Gitee token（仅创建Release时需要）"
    exit 1
fi
NAS_PASS=$(grep '^NAS_PASS=' "$ENV_FILE" | cut -d'=' -f2-)
if [ -z "$NAS_PASS" ]; then
    echo "错误: .env 文件中未设置 NAS_PASS"
    exit 1
fi
if [ ${#NAS_PASS} -lt 12 ]; then
    echo "错误: NAS_PASS 至少需要 12 位（FileBrowser 强制要求）"
    exit 1
fi

# ==================== 确保 /opt/nas 软链接存在 ====================

if [ ! -e "$NAS_DIR" ]; then
    echo "创建软链接: $NAS_DIR -> $SCRIPT_DIR"
    mkdir -p /opt
    ln -sfn "$SCRIPT_DIR" "$NAS_DIR"
fi

# 通用下载函数：尝试多个源，第一个成功就返回
download_file() {
    local dest="$1"
    shift
    for url in "$@"; do
        echo "    尝试下载: $url"
        if curl -fsSL --connect-timeout 15 --max-time 120 -o "$dest" "$url" 2>/dev/null; then
            if [ -s "$dest" ] && ! head -c 20 "$dest" | grep -q "<!DOCTYPE\|<html\|Not Found"; then
                echo "    ✓ 下载成功"
                return 0
            fi
        fi
        echo "    ✗ 失败，尝试下一个源..."
    done
    echo "    ✗ 所有下载源均失败"
    return 1
}

# ==================== [1/9] 安装基础软件包 ====================
echo "[1/9] 安装基础软件包..."
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y \
    curl samba nfs-kernel-server vsftpd rclone \
    fail2ban ufw smartmontools unattended-upgrades \
    smbclient nfs-common xfsprogs mdadm lvm2
echo "  ✓ 软件包安装完成"

# ==================== [2/9] 创建数据目录结构 ====================
echo ""
echo "[2/9] 创建数据目录结构..."
mkdir -p "$DATA_DIR"/system
mkdir -p "$DATA_DIR"/{shared,media/{movies,tv,music},documents,photos,backups,downloads,private/$NAS_USER}
chown -R "$NAS_USER:$NAS_USER" "$DATA_DIR"
chmod 755 "$DATA_DIR"
chmod 775 "$DATA_DIR"/{shared,media,documents,photos,system}
echo "  ✓ 目录结构创建完成（含 /data/system 系统盘共享目录）"

# ==================== [3/9] 配置 Samba ====================
echo ""
echo "[3/9] 配置 Samba..."

# 提取旧的 Z1 托管共享（如果存在）
Z1_SAMBA_SHARES=""
if [ -f /etc/samba/smb.conf ]; then
    Z1_SAMBA_SHARES=$(sed -n '/# === Z1 MANAGED SHARES START ===/,/# === Z1 MANAGED SHARES END ===/p' /etc/samba/smb.conf 2>/dev/null)
fi

if [ -f "$NAS_DIR/configs/smb.conf" ]; then
    sed "s/__NAS_USER__/$NAS_USER/g" "$NAS_DIR/configs/smb.conf" > /etc/samba/smb.conf
else
    echo "  警告: 未找到 $NAS_DIR/configs/smb.conf，使用默认配置"
fi

# 追加 Z1 托管共享
if [ -n "$Z1_SAMBA_SHARES" ]; then
    echo "" >> /etc/samba/smb.conf
    echo "$Z1_SAMBA_SHARES" >> /etc/samba/smb.conf
    echo "  ✓ 已保留 Z1 托管共享 ($(echo "$Z1_SAMBA_SHARES" | grep -c '^\[') 个共享)"
fi

# 设置系统用户密码
echo "$NAS_USER:$NAS_PASS" | chpasswd
(echo "$NAS_PASS"; echo "$NAS_PASS") | smbpasswd -a "$NAS_USER" -s
smbpasswd -e "$NAS_USER"
systemctl enable smbd nmbd
systemctl reset-failed smbd nmbd 2>/dev/null; systemctl restart smbd nmbd
echo "  ✓ Samba 配置完成"

# ==================== [4/9] 配置 NFS ====================
echo ""
echo "[4/9] 配置 NFS..."
if [ -f "$NAS_DIR/configs/nfs.conf" ]; then
    cp "$NAS_DIR/configs/nfs.conf" /etc/nfs.conf
fi

# 自动检测本机内网段
DETECT_SUBNET=""
PRIMARY_IP=$(ip -4 -o addr show scope global | grep -v 'docker\|virbr\|lo' | head -1 | awk '{print $4}')
if [ -n "$PRIMARY_IP" ]; then
    # 提取 /24 子网: [REDACTED]/24 → [REDACTED]/24
    DETECT_SUBNET=$(echo "$PRIMARY_IP" | sed -E 's/\.[0-9]+\/[0-9]+$/.0\/24/' 2>/dev/null)
    if [ -z "$DETECT_SUBNET" ] || [ "$DETECT_SUBNET" = "$PRIMARY_IP" ]; then
        DETECT_SUBNET="192.168.0.0/24"  # fallback
    fi
else
    DETECT_SUBNET="192.168.0.0/24"  # fallback
fi
echo "  检测到内网段: ${DETECT_SUBNET}"

# 提取旧的 Z1 托管 NFS 导出（如果存在）
Z1_NFS_EXPORTS=""
if [ -f /etc/exports ]; then
    Z1_NFS_EXPORTS=$(sed -n '/# === Z1 MANAGED SHARES START ===/,/# === Z1 MANAGED SHARES END ===/p' /etc/exports 2>/dev/null)
fi

if [ -f "$NAS_DIR/configs/exports" ]; then
    sed "s|__SUBNET__|${DETECT_SUBNET}|g" "$NAS_DIR/configs/exports" > /etc/exports
else
    cat > /etc/exports << EXPEOF
$DATA_DIR/shared    ${DETECT_SUBNET}(rw,sync,no_subtree_check,no_root_squash)
$DATA_DIR/media     ${DETECT_SUBNET}(ro,sync,no_subtree_check)
$DATA_DIR/documents ${DETECT_SUBNET}(rw,sync,no_subtree_check,no_root_squash)
$DATA_DIR/photos    ${DETECT_SUBNET}(rw,sync,no_subtree_check,no_root_squash)
$DATA_DIR/backups   ${DETECT_SUBNET}(rw,sync,no_subtree_check,no_root_squash)
EXPEOF
fi

# 追加 Z1 托管 NFS 导出
if [ -n "$Z1_NFS_EXPORTS" ]; then
    echo "" >> /etc/exports
    echo "$Z1_NFS_EXPORTS" >> /etc/exports
    echo "  ✓ 已保留 Z1 托管 NFS 导出"
fi
exportfs -a
systemctl enable nfs-kernel-server
systemctl reset-failed nfs-kernel-server 2>/dev/null; systemctl restart nfs-kernel-server
echo "  ✓ NFS 配置完成"

# ==================== [5/9] 配置 FTP ====================
echo ""
echo "[5/9] 配置 FTP (vsftpd)..."
if [ -f "$NAS_DIR/configs/vsftpd.conf" ]; then
    cp "$NAS_DIR/configs/vsftpd.conf" /etc/vsftpd.conf
else
    cat > /etc/vsftpd.conf << FTPEOF
listen=YES
listen_ipv6=NO
anonymous_enable=NO
local_enable=YES
write_enable=YES
local_umask=022
dirmessage_enable=YES
use_localtime=YES
xferlog_enable=YES
connect_from_port_20=YES
chroot_local_user=YES
allow_writeable_chroot=YES
user_sub_token=\$USER
local_root=$DATA_DIR/private/\$USER
pasv_enable=YES
pasv_min_port=30000
pasv_max_port=31000
userlist_enable=YES
userlist_file=/etc/vsftpd.userlist
userlist_deny=NO
FTPEOF
fi
echo "$NAS_USER" > /etc/vsftpd.userlist
touch /var/log/vsftpd.log
chmod 640 /var/log/vsftpd.log
systemctl enable vsftpd
systemctl reset-failed vsftpd 2>/dev/null; systemctl restart vsftpd
echo "  ✓ FTP 配置完成"

# ==================== [6/9] 配置 WebDAV ====================
echo ""
echo "[6/9] 配置 WebDAV (rclone)..."
# 安装 htpasswd 工具（用于 WebDAV 认证）
apt-get install -y apache2-utils 2>/dev/null || true
# 生成 htpasswd 文件（使用 bcrypt 哈希，兼容性好）
htpasswd -cb /etc/rclone-htpasswd "$NAS_USER" "$NAS_PASS"
chown "$NAS_USER:$NAS_USER" /etc/rclone-htpasswd
# 创建 systemd 服务文件（使用 htpasswd 认证，避免 rclone obscure hash 兼容性问题）
cat > /etc/systemd/system/rclone-webdav.service << WDEOF
[Unit]
Description=Rclone WebDAV Server
After=network.target

[Service]
Type=simple
User=$NAS_USER
ExecStart=/usr/bin/rclone serve webdav $DATA_DIR --addr :8080 --htpasswd /etc/rclone-htpasswd
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
WDEOF
systemctl daemon-reload
systemctl enable rclone-webdav
systemctl reset-failed rclone-webdav 2>/dev/null; systemctl restart rclone-webdav
echo "  ✓ WebDAV 配置完成"

# ==================== [7/9] 安装 FileBrowser ====================
echo ""
echo "[7/9] 安装 FileBrowser..."
if command -v filebrowser &>/dev/null; then
    echo "  FileBrowser 已安装，跳过"
elif [ -n "$OFFLINE_BUNDLE" ]; then
    echo "  从离线包安装 FileBrowser..."
    mkdir -p /tmp/nas-offline
    tar xzf "$OFFLINE_BUNDLE" -C /tmp/nas-offline
    cp /tmp/nas-offline/bin/filebrowser /usr/local/bin/
    chmod +x /usr/local/bin/filebrowser
    rm -rf /tmp/nas-offline
    echo "  ✓ 离线安装完成"
else
    # 尝试从 GitHub releases 分支下载离线包（含 filebrowser + nas-panel + 配置）
    set +e  # 下载失败不应中断整个脚本
    download_file /tmp/filebrowser.tar.gz \
        "https://github.com/jackeyPeng/nas/raw/releases/nas-bundle-${BUNDLE_VERSION}-${ARCH}.tar.gz" \
        "https://ghfast.top/https://github.com/jackeyPeng/nas/raw/releases/nas-bundle-${BUNDLE_VERSION}-${ARCH}.tar.gz" \
        "https://get.z1.sale/filebroswer/linux-${ARCH}-filebrowser.tar.gz" \
        "https://github.com/filebrowser/filebrowser/releases/download/${FILEBROWSER_VERSION}/linux-${ARCH}-filebrowser.tar.gz" \
        "https://ghfast.top/https://github.com/filebrowser/filebrowser/releases/download/${FILEBROWSER_VERSION}/linux-${ARCH}-filebrowser.tar.gz"
    FB_DOWNLOAD_OK=$?
    set -e
    if [ "$FB_DOWNLOAD_OK" -eq 0 ]; then
        cd /tmp && tar xzf filebrowser.tar.gz
        # 不同来源的 tar.gz 结构不同：可能直接是 filebrowser 或 linux-amd64-filebrowser/filebrowser
        if [ -f "filebrowser" ]; then
            mv filebrowser /usr/local/bin/
        elif [ -f "linux-amd64-filebrowser/filebrowser" ]; then
            mv linux-amd64-filebrowser/filebrowser /usr/local/bin/
        elif FB_BIN=$(find . -maxdepth 2 -name filebrowser -type f | head -1) && [ -n "$FB_BIN" ]; then
            mv "$FB_BIN" /usr/local/bin/
        else
            echo "  警告: 无法找到 filebrowser 二进制文件"
        fi
        rm -rf filebrowser.tar.gz linux-amd64-filebrowser filebrowser 2>/dev/null
        cd - > /dev/null
        chmod +x /usr/local/bin/filebrowser
    fi
fi

mkdir -p /etc/filebrowser
if [ ! -f /etc/filebrowser/filebrowser.db ]; then
    filebrowser config init --database /etc/filebrowser/filebrowser.db 2>/dev/null || true
fi
filebrowser config set \
    --database /etc/filebrowser/filebrowser.db \
    --address 0.0.0.0 \
    --port 8081 \
    --root "$DATA_DIR" \
    --log /var/log/filebrowser.log 2>/dev/null || true
filebrowser users add "$NAS_USER" "$NAS_PASS" \
    --database /etc/filebrowser/filebrowser.db \
    --perm.admin 2>/dev/null || true

cat > /etc/systemd/system/filebrowser.service << 'FBEOF'
[Unit]
Description=File Browser
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/filebrowser --database /etc/filebrowser/filebrowser.db
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
FBEOF
systemctl daemon-reload
systemctl enable filebrowser
systemctl reset-failed filebrowser 2>/dev/null; systemctl restart filebrowser
echo "  ✓ FileBrowser 配置完成"

# ==================== [8/10] 配置 S3 对象存储 (rclone serve s3) ====================
echo ""
echo "[8/10] 配置 S3 对象存储 (rclone serve s3)..."
# 安装/升级 rclone（需要 1.62+ 才有 serve s3 子命令）
RCLONE_VERSION=$(rclone version 2>/dev/null | head -1 | grep -oP 'v\K[0-9]+\.[0-9]+' | head -1)
if [ -z "$RCLONE_VERSION" ] || [ "$(echo "$RCLONE_VERSION < 1.62" | bc 2>/dev/null || echo 1)" = "1" ]; then
    echo "  升级 rclone..."
    if download_file /tmp/rclone-latest.deb \
        "https://get.z1.sale/minio/rclone-v1.74.4-linux-amd64.deb" \
        "https://github.com/rclone/rclone/releases/download/v1.74.4/rclone-v1.74.4-linux-amd64.deb" \
        "https://ghfast.top/https://github.com/rclone/rclone/releases/download/v1.74.4/rclone-v1.74.4-linux-amd64.deb"; then
        sudo dpkg -i /tmp/rclone-latest.deb 2>/dev/null || true
        echo "  ✓ rclone 已升级到 $(rclone version | head -1)"
    else
        echo "  ⚠ rclone 升级失败，S3 服务可能不可用（需要 rclone 1.62+）"
    fi
fi

# 创建 S3 服务配置（认证密钥）
mkdir -p /etc/rclone
cat > /etc/rclone/s3-env << S3EOF
RCLONE_S3_ACCESS_KEY=$NAS_USER
RCLONE_S3_SECRET_KEY=$NAS_PASS
S3EOF
chmod 640 /etc/rclone/s3-env

# 创建 systemd 服务文件
cat > /etc/systemd/system/rclone-s3.service << S3SVC
[Unit]
Description=Rclone S3 Server
After=network.target

[Service]
Type=simple
User=$NAS_USER
EnvironmentFile=/etc/rclone/s3-env
ExecStart=/usr/bin/rclone serve s3 $DATA_DIR --addr :9000 --auth-key $NAS_USER,$NAS_PASS
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
S3SVC

systemctl daemon-reload
systemctl enable rclone-s3
systemctl reset-failed rclone-s3 2>/dev/null; systemctl restart rclone-s3
echo "  ✓ S3 对象存储配置完成 (rclone serve s3, 端口 9000)"
echo "    bucket 列表: $DATA_DIR 下每个目录自动成为一个 bucket"
echo "    访问方式: s3cmd --no-ssl --host=NAS_IP:9000 ls s3://shared/"

# ==================== [9/9] 配置防火墙和安全 ====================
echo ""
echo "[9/9] 配置防火墙和安全..."

if [ -f "$NAS_DIR/configs/jail.local" ]; then
    cp "$NAS_DIR/configs/jail.local" /etc/fail2ban/jail.local
else
    cat > /etc/fail2ban/jail.local << 'JEOF'
[DEFAULT]
bantime = 3600
findtime = 600
maxretry = 5

[sshd]
enabled = true
port = ssh
filter = sshd
logpath = /var/log/auth.log

[vsftpd]
enabled = true
port = ftp,ftp-data,ftps,ftps-data
filter = vsftpd
logpath = /var/log/vsftpd.log
maxretry = 5
JEOF
fi
systemctl enable fail2ban
systemctl reset-failed fail2ban 2>/dev/null; systemctl restart fail2ban

ufw --force reset
ufw default deny incoming
ufw default allow outgoing
ufw allow ssh                    # 22 - SSH
ufw allow 139/tcp                # Samba NetBIOS
ufw allow 445/tcp                # Samba SMB
ufw allow 2049/tcp               # NFS
ufw allow 2049/udp               # NFS
ufw allow 111/tcp                # NFS rpcbind
ufw allow 111/udp                # NFS rpcbind
ufw allow 20048/tcp              # NFS mountd
ufw allow 20048/udp              # NFS mountd
ufw allow 32768:32769/tcp        # NFS lockd + statd
ufw allow 32768:32769/udp        # NFS lockd + statd
ufw allow 21/tcp                 # FTP
ufw allow 30000:31000/tcp        # FTP passive
ufw allow 8080/tcp               # WebDAV
ufw allow 8081/tcp               # FileBrowser
ufw allow 9000/tcp               # S3 API (rclone serve s3)
ufw allow 8090/tcp               # NAS Web Panel
ufw --force enable
echo "  ✓ 安全配置完成"

# ==================== [10/10] 安装 NAS Web 管理面板 ====================
echo ""
echo "[10/10] 安装 NAS Web 管理面板..."
if [ -f "$NAS_DIR/web/nas-panel" ]; then
    cp "$NAS_DIR/web/nas-panel" /usr/local/bin/nas-panel
    chmod +x /usr/local/bin/nas-panel
elif [ -n "$OFFLINE_BUNDLE" ] && tar tzf "$OFFLINE_BUNDLE" 2>/dev/null | grep -q "bin/nas-panel"; then
    echo "  从离线包安装 nas-panel..."
    mkdir -p /tmp/nas-offline
    tar xzf "$OFFLINE_BUNDLE" -C /tmp/nas-offline
    cp /tmp/nas-offline/bin/nas-panel /usr/local/bin/
    chmod +x /usr/local/bin/nas-panel
    rm -rf /tmp/nas-offline
    echo "  ✓ 离线安装完成"
elif download_file /usr/local/bin/nas-panel \
    "https://get.z1.sale/control/nas-panel-${ARCH}.latest" \
    "https://get.z1.sale/control/nas-panel.latest" \
    "https://ghfast.top/https://github.com/gitdogcat/nas/releases/download/v1.0.0/nas-panel-linux-${ARCH}"; then
    chmod +x /usr/local/bin/nas-panel
else
    echo "  警告: 无法获取 nas-panel 二进制文件，跳过 Web 面板安装"
    echo "  请手动编译: cd $NAS_DIR/web && GOPROXY=https://goproxy.cn,direct go build -buildvcs=false -o nas-panel ."
    echo "  然后重新运行: sudo bash scripts/setup.sh"
fi

# Create systemd service
if [ -f "$NAS_DIR/configs/nas-panel.service" ]; then
    sed "s/__NAS_USER__/$NAS_USER/g" "$NAS_DIR/configs/nas-panel.service" > /etc/systemd/system/nas-panel.service
fi
systemctl daemon-reload
systemctl enable nas-panel
systemctl reset-failed nas-panel 2>/dev/null; systemctl restart nas-panel

# 配置 sudo 免密权限（nas-panel 需要执行系统管理命令）
SUDOERS_FILE="/etc/sudoers.d/nas-panel"
echo "${NAS_USER} ALL=(ALL) NOPASSWD: /usr/bin/pdbedit, /opt/nas/scripts/add-user.sh, /opt/nas/scripts/remove-user.sh, /usr/sbin/smartctl, /usr/bin/chpasswd, /usr/bin/smbpasswd, /usr/bin/htpasswd, /bin/systemctl start *, /bin/systemctl stop *, /bin/systemctl restart *, /bin/systemctl reset-failed *, /bin/systemctl enable *, /bin/systemctl disable *, /usr/sbin/ufw status, /usr/sbin/ufw allow *, /usr/sbin/ufw deny *, /usr/sbin/exportfs, /usr/sbin/smartctl -H *, /usr/sbin/smartctl -a *, /usr/bin/tee /opt/nas/.env, /usr/bin/tee -a /etc/samba/smb.conf, /usr/bin/tee /etc/samba/smb.conf, /usr/bin/tee /etc/vsftpd.userlist, /usr/bin/tee -a /etc/vsftpd.userlist, /usr/bin/tee /etc/exports, /usr/bin/tee /etc/nfs.conf, /usr/bin/tee /etc/fail2ban/jail.local, /usr/bin/tee /etc/rclone-htpasswd, /usr/bin/journalctl -p err -n * --no-pager --since *, /usr/sbin/pvs --noheadings *, /usr/sbin/vgs --noheadings *, /usr/sbin/lvs --noheadings *, /usr/sbin/fdisk -l, /usr/sbin/mkfs.ext4 -F *, /usr/sbin/mkfs.xfs -f *, /usr/sbin/mkfs.btrfs *, /bin/mount *, /bin/umount *, /bin/mkdir -p /data/*, /bin/mkdir -p /data, /bin/cat /etc/samba/smb.conf, /bin/cat /etc/vsftpd.userlist, /bin/cat /etc/exports, /bin/cat /etc/nfs.conf, /bin/cat /etc/fail2ban/jail.local, /bin/cat /etc/rclone-htpasswd, /opt/nas/scripts/backup-config.sh, /opt/nas/scripts/restore-config.sh, /bin/rm -f /data/backups/*, /usr/bin/blkid -s UUID -o value *, /usr/bin/findmnt -n -o TARGET *, /usr/bin/tee /etc/fstab, /usr/bin/tee -a /etc/samba/smb.conf, /bin/chown -R *, /sbin/pvcreate -f *, /sbin/vgcreate -f *, /sbin/lvcreate *, /sbin/vgextend *, /sbin/lvextend *, /sbin/resize2fs *, /sbin/xfs_growfs *, /sbin/pvs --noheadings *, /sbin/vgs --noheadings *, /sbin/lvs --noheadings *, /sbin/pvremove -f *, /sbin/vgremove -f *, /sbin/lvremove -f *, /usr/sbin/wipefs *, /usr/sbin/mdadm *, /usr/sbin/parted *, /bin/ls *" > "$SUDOERS_FILE"
chmod 440 "$SUDOERS_FILE"
visudo -cf "$SUDOERS_FILE" 2>/dev/null || { echo "  错误: sudoers 语法检查失败"; rm -f "$SUDOERS_FILE"; }
# 追加改密码需要的命令（filebrowser + sed）
echo "${NAS_USER} ALL=(ALL) NOPASSWD: /usr/local/bin/filebrowser *, /usr/bin/sed -i *" >> "$SUDOERS_FILE"
visudo -cf "$SUDOERS_FILE" 2>/dev/null || true
echo "  ✓ NAS Web 管理面板配置完成"

# ==================== 配置监控告警 cron ====================
CRON_LINE="*/5 * * * * $NAS_DIR/scripts/monitor.sh 2>/dev/null"
( crontab -l 2>/dev/null | grep -v "monitor.sh"; echo "$CRON_LINE" ) | crontab -u "$NAS_USER" - 2>/dev/null || \
( crontab -l 2>/dev/null | grep -v "monitor.sh"; echo "$CRON_LINE" ) | crontab -
# 监控状态目录
mkdir -p /var/lib/nas-monitor
chown "$NAS_USER:$NAS_USER" /var/lib/nas-monitor
echo "  ✓ 监控告警 cron 已配置（每5分钟检查）"

# 配置每周备份 cron（每周日凌晨3点，保留最近 3 份）
BACKUP_CRON_LINE="0 3 * * 0 sudo $NAS_DIR/scripts/backup-config.sh 2>/dev/null"
( crontab -u "$NAS_USER" -l 2>/dev/null | grep -v "backup-config.sh"; echo "$BACKUP_CRON_LINE" ) | crontab -u "$NAS_USER" - 2>/dev/null || true
echo "  ✓ 配置备份 cron 已配置（每周日凌晨3点，保留3份）"

# 部署完成后立即做一次初始备份
echo "  → 执行初始备份..."
if [ -f "$NAS_DIR/scripts/backup-config.sh" ]; then
    sudo bash "$NAS_DIR/scripts/backup-config.sh" 2>/dev/null || echo "  ⚠ 初始备份失败，可稍后手动执行"
fi

# ==================== 部署完成 ====================
echo ""
echo "========================================="
echo "NAS 部署完成!"
echo "========================================="
echo ""
echo "服务状态:"
for svc in smbd nmbd nfs-kernel-server vsftpd rclone-webdav filebrowser rclone-s3 fail2ban nas-panel; do
    printf "  %-22s %s\n" "$svc:" "$(systemctl is-active $svc)"
done

# ==================== 系统注册表检查 ====================
echo ""
echo "========================================="
echo "系统注册表检查 (46 项)"
echo "========================================="
# 等待面板启动
sleep 3
echo "正在刷新注册表..."
RESULT=$(curl -s "http://localhost:8090/api/system/check?action=refresh" 2>/dev/null)
if [ -n "$RESULT" ]; then
    echo "$RESULT" | python3 -c "
import sys,json
d=json.load(sys.stdin)
total = d['total']
passed = d['passed']
failed = d['failed']
warn = d['warn']
print(f'  总计: {total}  通过: {passed}  失败: {failed}  警告: {warn}')
print(f'  {d[\"summary\"]}')
if failed > 0 or warn > 0:
    print()
    for item in d['items']:
        if item['status'] != 'pass':
            icon = '❌' if item['status']=='fail' else '⚠️'
            print(f'  {icon} [{item[\"id\"]:02d}] {item[\"name\"]}: {item[\"detail\"]}')
" 2>/dev/null
else
    echo "  ⚠ 面板未就绪，跳过注册表检查"
fi

echo ""
echo "访问信息:"
echo "  用户名: $NAS_USER"
echo "  密码: $NAS_PASS"
echo ""
echo "访问方式:"
echo "  - Samba:       //NAS_IP/shared (公共共享)"
echo "  - Samba:       //NAS_IP/$NAS_USER (私有目录)"
echo "  - NFS:         mount -t nfs NAS_IP:/data/shared /mnt/nas"
echo "  - FTP:         ftp://NAS_IP/ (用户名: $NAS_USER)"
echo "  - WebDAV:      http://NAS_IP:8080/ (用户名: $NAS_USER)"
echo "  - FileBrowser: http://NAS_IP:8081/ (用户名: $NAS_USER)"
echo "  - S3 API:       http://NAS_IP:9000 (s3cmd --no-ssl --host=NAS_IP:9000 ls s3://shared/)"
echo "  - Web 面板:    http://NAS_IP:8090 (用户名: $NAS_USER)"
echo ""
echo "管理脚本:"
echo "  添加用户: sudo $NAS_DIR/scripts/add-user.sh <用户名> <密码>"
echo "  删除用户: sudo $NAS_DIR/scripts/remove-user.sh <用户名> [--delete-data]"
echo "========================================="
