#!/bin/bash
# NAS 一键部署脚本
# 用于在新机器上部署完整的 NAS 系统
# 用法: sudo ./setup.sh
#
# 包含服务: Samba, NFS, FTP, WebDAV, S3, NAS Web Panel
# 安全: Fail2ban, UFW 防火墙, unattended-upgrades
# 10步部署, 自动检测当前用户
# 开关: --config-only 仅同步配置

set -e

# ==================== 模式解析 ====================
# --config-only: 只重生成托管配置（Samba/NFS/FTP/WebDAV/S3/防火墙），
# 跳过 apt 安装、目录创建、nas-panel 安装。
# 用于在线升级后同步新版配置模板（幂等，用户自加段保留）。
CONFIG_ONLY=false
for arg in "$@"; do
    case "$arg" in
        --config-only) CONFIG_ONLY=true ;;
        *) echo "未知参数: $arg（支持: --config-only）"; exit 1 ;;
    esac
done

# ==================== 架构检测 ====================
# 将 uname -m 映射为 rclone 使用的架构名
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
# 自动获取当前用户（执行 sudo 的用户）。
# 显式导出的 NAS_USER 优先——升级流程（apply-upgrade.sh 经 systemd-run 以 root 跑，
# 无 SUDO_USER）从 nas-panel.service 的 Environment 读出后传入。
NAS_USER="${NAS_USER:-${SUDO_USER:-$USER}}"
if [ -z "$NAS_USER" ] || [ "$NAS_USER" = "root" ]; then
    echo "错误: 无法自动检测用户名，请使用 sudo 运行（而非直接以 root 身份）"
    exit 1
fi
DATA_DIR="/data"
NAS_DIR="/opt/nas"
BUNDLE_VERSION="v1.0.0"

# 获取脚本所在目录的绝对路径（仓库根目录）
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# ==================== 离线包检测 ====================
# 离线包包含 nas-panel 二进制及所有配置文件
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
    echo "   将使用离线包安装 nas-panel"
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
    echo "错误: NAS_PASS 至少需要 12 位（面板密码策略）"
    exit 1
fi
# 加固：.env 含 NAS_PASS/JWT_SECRET，收紧权限（install.sh 一键路径已 chmod 600，
# 此处覆盖手工 cp .env.example 创建的场景，默认 umask 可能留下 644）
chmod 600 "$ENV_FILE" 2>/dev/null || true

# ==================== 确保 /opt/nas 软链接存在 ====================

if [ ! -e "$NAS_DIR" ]; then
    echo "创建软链接: $NAS_DIR -> $SCRIPT_DIR"
    mkdir -p /opt
    ln -sfn "$SCRIPT_DIR" "$NAS_DIR"
fi

# ==================== 属主迁移：root → NAS_USER（面板降权，幂等） ====================
# 老机器（root 面板 + root 属主数据）跑 apply-upgrade.sh 时经由其对
# `setup.sh --config-only` 的既有调用自动触达本段——同一份逻辑，不建第二处。
# 本段不受 CONFIG_ONLY 影响（新装机器首次跑 setup.sh 全量模式时同样需要，
# 覆盖 install.sh 以 root 落地 .env / 面板以 root 首次启动写出的 data/ 等文件）。
# 幂等关键：每步先判断当前属主/内容，已是目标状态则跳过，重复执行零副作用。
migrate_ownership_to_nas_user() {
    echo ""
    echo "检查属主迁移 (root → $NAS_USER)..."

    # 路径操作认实体不认链接：readlink -f 解析 /opt/nas 软链接后对实体目录操作
    local repo_real
    repo_real=$(readlink -f "$NAS_DIR" 2>/dev/null || true)

    # 1. 仓库实体目录（含 web/、scripts/、configs/ 等，fm 需可见可管）
    if [ -n "$repo_real" ] && [ -d "$repo_real" ]; then
        if [ "$(stat -c '%U' "$repo_real" 2>/dev/null)" != "$NAS_USER" ]; then
            chown -R "$NAS_USER:$NAS_USER" "$repo_real"
            echo "  ✓ 仓库实体目录属主迁移: $repo_real → $NAS_USER"
        fi

        # 2. 面板数据目录 data/（folders.db / operations.db / ai.db 等，面板以 NAS_USER 运行后直写）
        if [ -d "$repo_real/data" ] && [ "$(stat -c '%U' "$repo_real/data" 2>/dev/null)" != "$NAS_USER" ]; then
            chown -R "$NAS_USER:$NAS_USER" "$repo_real/data"
            echo "  ✓ 面板数据目录属主迁移: $repo_real/data → $NAS_USER"
        fi

        # 3. .env（含 NAS_PASS/JWT_SECRET，面板 EnvironmentFile 需 NAS_USER 可读）
        if [ -f "$repo_real/.env" ]; then
            if [ "$(stat -c '%U' "$repo_real/.env" 2>/dev/null)" != "$NAS_USER" ]; then
                chown "$NAS_USER:$NAS_USER" "$repo_real/.env"
                echo "  ✓ .env 属主迁移: $repo_real/.env → $NAS_USER"
            fi
            chmod 600 "$repo_real/.env" 2>/dev/null || true
        fi
    fi

    # 4. 监控状态目录（cron 段创建于本脚本末尾，此处只迁移老机器已存在的）
    if [ -d /var/lib/nas-monitor ] && [ "$(stat -c '%U' /var/lib/nas-monitor 2>/dev/null)" != "$NAS_USER" ]; then
        chown -R "$NAS_USER:$NAS_USER" /var/lib/nas-monitor
        echo "  ✓ 监控状态目录属主迁移: /var/lib/nas-monitor → $NAS_USER"
    fi

    # 5. rclone 配置迁移: /root/.config/rclone/rclone.conf → ~NAS_USER/.config/rclone/
    # 存在才迁；不拿空文件覆盖已有目标；迁移后源文件留 .migrated 标记（而非直接删除——
    # 降权是新流程，留标记便于真机验收阶段核对迁移是否发生、异常时可比对原文件，
    # 对比直接删除更保守，机器稳定运行几个版本后可在后续清理脚本里统一清除 .migrated）。
    local root_rclone_conf="/root/.config/rclone/rclone.conf"
    local user_rclone_dir="/home/$NAS_USER/.config/rclone"
    local user_rclone_conf="$user_rclone_dir/rclone.conf"
    if [ -s "$root_rclone_conf" ]; then
        if [ -s "$user_rclone_conf" ]; then
            echo "  ⚠ rclone 配置迁移跳过: $user_rclone_conf 已存在且非空，不覆盖"
        else
            mkdir -p "$user_rclone_dir"
            cp -a "$root_rclone_conf" "$user_rclone_conf"
            chown -R "$NAS_USER:$NAS_USER" "$user_rclone_dir"
            chmod 600 "$user_rclone_conf"
            mv "$root_rclone_conf" "${root_rclone_conf}.migrated"
            echo "  ✓ rclone 配置迁移: $root_rclone_conf → $user_rclone_conf（源文件留 .migrated 标记）"
        fi
    fi

    # 6. /etc/nas/certs 可读性：面板降权后仍需按原路径读取证书/私钥（go 侧读取逻辑不变），
    # 私钥收窄为 640 root:NAS_USER（不开 644，私钥不给 other），证书保持 644。
    # 已知残留风险（报告记录）：面板内「重新生成/上传证书」会以 root 身份重新创建这两个
    # 文件（走 common.SudoExec openssl/cp），届时权限会被重置——该路径的收口留给后续
    # sudoers 白名单任务，此处只保证当前已存在证书在装机/升级这一刻对 NAS_USER 可读。
    if [ -d /etc/nas/certs ]; then
        chgrp "$NAS_USER" /etc/nas/certs 2>/dev/null || true
        chmod 750 /etc/nas/certs 2>/dev/null || true
        if [ -f /etc/nas/certs/server.key ]; then
            chgrp "$NAS_USER" /etc/nas/certs/server.key 2>/dev/null || true
            chmod 640 /etc/nas/certs/server.key 2>/dev/null || true
        fi
        if [ -f /etc/nas/certs/server.crt ]; then
            chgrp "$NAS_USER" /etc/nas/certs/server.crt 2>/dev/null || true
            chmod 644 /etc/nas/certs/server.crt 2>/dev/null || true
        fi
    fi

    echo "  ✓ 属主迁移检查完成"
}
migrate_ownership_to_nas_user

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

# ==================== [1/10] 安装基础软件包 ====================
if [ "$CONFIG_ONLY" = true ]; then
    echo "[1/10] 安装基础软件包...（--config-only 跳过）"
else
    echo "[1/10] 安装基础软件包..."
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y \
        curl samba nfs-kernel-server vsftpd rclone \
        fail2ban ufw smartmontools unattended-upgrades \
        smbclient nfs-common xfsprogs mdadm lvm2 rsync
    echo "  ✓ 软件包安装完成"
fi

# ==================== [2/10] 创建数据目录结构 ====================
echo ""
if [ "$CONFIG_ONLY" = true ]; then
    echo "[2/10] 创建数据目录结构...（--config-only 跳过，避免对已有数据树做递归 chown）"
else
    echo "[2/10] 创建数据目录结构..."
    mkdir -p "$DATA_DIR"
    chown -R "$NAS_USER:$NAS_USER" "$DATA_DIR"
    chmod 755 "$DATA_DIR"
    # 公共组 nasusers：public 目录组级读写用，所有 NAS 用户都加入
    if ! getent group nasusers >/dev/null 2>&1; then
        groupadd nasusers
    fi
    usermod -a -G nasusers "$NAS_USER" 2>/dev/null || true
    echo "  ✓ 目录结构创建完成（存储池挂载 /data/nas1，public 与用户 home 由存储向导创建）"
fi

# ==================== [3/10] 配置 Samba ====================
echo ""
echo "[3/10] 配置 Samba..."

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
systemctl reset-failed smbd nmbd 2>/dev/null
timeout 60 systemctl restart smbd nmbd || echo "  ⚠ smbd nmbd 重启失败（结尾注册表会标记，可稍后手动 restart）"
echo "  ✓ Samba 配置完成"

# ==================== [4/10] 配置 NFS ====================
echo ""
echo "[4/10] 配置 NFS..."
if [ -f "$NAS_DIR/configs/nfs.conf" ]; then
    cp "$NAS_DIR/configs/nfs.conf" /etc/nfs.conf
fi

# NFS 对所有网段开放（不再检测/限制本机网段）
# 提取旧的 Z1 托管 NFS 导出（如果存在）
Z1_NFS_EXPORTS=""
if [ -f /etc/exports ]; then
    Z1_NFS_EXPORTS=$(sed -n '/# === Z1 MANAGED SHARES START ===/,/# === Z1 MANAGED SHARES END ===/p' /etc/exports 2>/dev/null)
fi

if [ -f "$NAS_DIR/configs/exports" ]; then
    cp "$NAS_DIR/configs/exports" /etc/exports
else
    cat > /etc/exports << EXPEOF
# NFS 导出由面板托管段生成（仅 /data/nas1/public），此处保持空
EXPEOF
fi

# 追加 Z1 托管 NFS 导出（过滤掉路径已不存在的旧导出：reset/换机后 /data 可能还没建，
# 留着会让 exportfs -a 非零退出，set -e 下整个安装中断在 [4/10]；
# 旧版按网段导出的行统一归一为 *，与面板新生成的托管段一致）
if [ -n "$Z1_NFS_EXPORTS" ]; then
    KEPT=""
    while IFS= read -r line; do
        case "$line" in
            "#"*|"") continue ;;
        esac
        exp_path=$(echo "$line" | awk '{print $1}')
        if [ -d "$exp_path" ]; then
            exp_opts=$(echo "$line" | awk '{print $3}')
            [ -z "$exp_opts" ] && exp_opts="(rw,sync,no_subtree_check,no_root_squash,insecure)"
            KEPT="${KEPT}${exp_path} *${exp_opts}
"
        else
            echo "   跳过不存在的旧 NFS 导出: $exp_path"
        fi
    done << EXPEOF
$Z1_NFS_EXPORTS
EXPEOF
    if [ -n "$KEPT" ]; then
        echo "" >> /etc/exports
        printf '%s' "$KEPT" >> /etc/exports
        echo "  ✓ 已保留 Z1 托管 NFS 导出"
    fi
fi
exportfs -a 2>/dev/null || echo "  ⚠ exportfs -a 有告警（存储池未建时属正常，建池后面板会重生成导出）"
systemctl enable nfs-kernel-server
systemctl reset-failed nfs-kernel-server 2>/dev/null
timeout 60 systemctl restart nfs-kernel-server || echo "  ⚠ nfs-kernel-server 重启超时/失败（结尾注册表会标记，可稍后 systemctl restart）"
echo "  ✓ NFS 配置完成"

# ==================== [5/10] 配置 FTP ====================
echo ""
echo "[5/10] 配置 FTP (vsftpd)..."
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
local_root=$DATA_DIR/nas1
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
systemctl reset-failed vsftpd 2>/dev/null
timeout 60 systemctl restart vsftpd || echo "  ⚠ vsftpd 重启失败（结尾注册表会标记，可稍后手动 restart）"
echo "  ✓ FTP 配置完成"

# ==================== [6/10] 配置 WebDAV ====================
echo ""
echo "[6/10] 配置 WebDAV (rclone)..."
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
ExecStart=/usr/bin/rclone serve webdav $DATA_DIR/nas1/public --addr :8080 --htpasswd /etc/rclone-htpasswd
Restart=on-failure
RestartSec=10
SuccessExitStatus=143

[Install]
WantedBy=multi-user.target
WDEOF
systemctl daemon-reload
systemctl enable rclone-webdav
systemctl reset-failed rclone-webdav 2>/dev/null
timeout 60 systemctl restart rclone-webdav || echo "  ⚠ rclone-webdav 重启失败（结尾注册表会标记，可稍后手动 restart）"
echo "  ✓ WebDAV 配置完成"

# ==================== [7/10] 清理遗留 FileBrowser（升级迁移） ====================
echo ""
echo "[7/10] 清理遗留 FileBrowser..."
if command -v filebrowser &>/dev/null || [ -f /etc/systemd/system/filebrowser.service ]; then
    systemctl stop filebrowser 2>/dev/null || true
    systemctl disable filebrowser 2>/dev/null || true
    FB_BACKUP=""
    if [ -d /etc/filebrowser ]; then
        mkdir -p /var/backups
        FB_BACKUP="/var/backups/filebrowser-$(date +%F).tar.gz"
        tar czf "$FB_BACKUP" -C /etc filebrowser || { echo "  ⚠ 配置备份失败（磁盘空间？），继续移除"; FB_BACKUP=""; }
    fi
    rm -rf /etc/filebrowser /usr/local/bin/filebrowser /usr/bin/filebrowser /etc/systemd/system/filebrowser.service /var/log/filebrowser.log
    systemctl daemon-reload
    if [ -n "$FB_BACKUP" ]; then
        echo "  ✓ 已移除遗留 FileBrowser，配置备份在 $FB_BACKUP"
    else
        echo "  ✓ 已移除遗留 FileBrowser"
    fi
else
    echo "  未检测到遗留 FileBrowser，跳过"
fi

# ==================== [8/10] 配置 S3 对象存储 (rclone serve s3) ====================
echo ""
echo "[8/10] 配置 S3 对象存储 (rclone serve s3)..."
# 安装/升级 rclone（锁定 v1.75.2：低于 1.75 即升级到 pin 版本；serve s3 的功能下限是 1.62）
RCLONE_VERSION=$(rclone version 2>/dev/null | head -1 | grep -oP 'v\K[0-9]+\.[0-9]+' | head -1)
# bc 在最小化 Debian 上可能不存在（57 机实测 NO_BC）——版本比较一律走 awk
rclone_lt_175() {
    [ -z "$1" ] && { echo 1; return; }
    echo "$1" | awk -F. '{print ($1<1 || ($1==1 && $2<75)) ? 1 : 0}'
}
if [ "$(rclone_lt_175 "$RCLONE_VERSION")" = "1" ]; then
    echo "  升级 rclone..."
    if download_file /tmp/rclone-latest.deb \
        "https://get.z1.sale/releases/rclone-v1.75.2-linux-${ARCH}.deb" \
        "https://github.com/rclone/rclone/releases/download/v1.75.2/rclone-v1.75.2-linux-${ARCH}.deb" \
        "https://ghfast.top/https://github.com/rclone/rclone/releases/download/v1.75.2/rclone-v1.75.2-linux-${ARCH}.deb"; then
        # 等 dpkg 锁：apt 批量装完后 unattended-upgrades 常仍持锁，立即 dpkg -i 会
        # 失败——此前 `2>/dev/null || true` 把失败吞掉还照报"✓ 已升级"，机器上
        # 留下 apt 版 rclone 1.60（无 serve s3），rclone-s3 崩溃循环（48 用户级
        # 部署实抓）。等锁 + 保留错误输出 + 装后验证版本，三重防线。
        for _i in $(seq 1 60); do
            if ! fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1 && ! fuser /var/lib/apt/lists/lock >/dev/null 2>&1; then
                break
            fi
            sleep 2
        done
        if sudo dpkg -i /tmp/rclone-latest.deb >/tmp/rclone-dpkg.log 2>&1; then
            :
        else
            echo "  ⚠ dpkg 安装 rclone 失败，错误输出："
            tail -5 /tmp/rclone-dpkg.log | sed 's/^/    /'
            # 常见残局：dpkg 中断态，尝试修复一次
            sudo apt-get -f install -y >/dev/null 2>&1 || true
            sudo dpkg -i /tmp/rclone-latest.deb >/tmp/rclone-dpkg.log 2>&1 || true
        fi
        RCLONE_NOW=$(rclone version 2>/dev/null | head -1 | grep -oP 'v\K[0-9]+\.[0-9]+' | head -1)
        if [ "$(rclone_lt_175 "$RCLONE_NOW")" = "0" ]; then
            echo "  ✓ rclone 已升级到 $(rclone version | head -1)"
        else
            echo "  ✗ rclone 升级未生效（当前 $(rclone version 2>/dev/null | head -1)），S3 服务将不可用"
            echo "    手动修复: sudo dpkg -i /tmp/rclone-latest.deb && sudo systemctl restart rclone-s3"
        fi
    else
        echo "  ⚠ rclone 升级失败，S3 服务可能不可用（需要 rclone 1.62+，锁定版本 v1.75.2）"
    fi
fi

# 创建 S3 服务配置（认证密钥）
# S3 access key：rclone serve s3 (gofakes3) 拒绝短于 3 字符的 access key
# （InvalidAccessKeyId），短用户名（如 fm）加 z1- 前缀
S3_ACCESS_KEY="$NAS_USER"
if [ ${#S3_ACCESS_KEY} -lt 3 ]; then
    S3_ACCESS_KEY="z1-$NAS_USER"
fi
mkdir -p /etc/rclone
cat > /etc/rclone/s3-env << S3EOF
RCLONE_S3_ACCESS_KEY=$S3_ACCESS_KEY
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
ExecStart=/usr/bin/rclone serve s3 $DATA_DIR/nas1/public --addr :9000 --auth-key $S3_ACCESS_KEY,$NAS_PASS
Restart=on-failure
RestartSec=10
SuccessExitStatus=143

[Install]
WantedBy=multi-user.target
S3SVC

systemctl daemon-reload
systemctl enable rclone-s3
systemctl reset-failed rclone-s3 2>/dev/null
timeout 60 systemctl restart rclone-s3 || echo "  ⚠ rclone-s3 重启失败（结尾注册表会标记，可稍后手动 restart）"
echo "  ✓ S3 对象存储配置完成 (rclone serve s3, 端口 9000)"
echo "    bucket 列表: public 目录自动成为一个 bucket"
echo "    访问方式: s3cmd --no-ssl --host=NAS_IP:9000 ls s3://public/"

# ==================== [9/10] 配置防火墙和安全 ====================
echo ""
echo "[9/10] 配置防火墙和安全..."

if [ -f "$NAS_DIR/configs/jail.local" ]; then
    cp "$NAS_DIR/configs/jail.local" /etc/fail2ban/jail.local
else
    cat > /etc/fail2ban/jail.local << 'JEOF'
[DEFAULT]
# 家用放宽策略：ban 10 分钟、允许 10 次尝试，避免用户输错密码把自己锁在门外
bantime = 600
findtime = 600
maxretry = 10

[sshd]
enabled = true
port = ssh
filter = sshd
logpath = /var/log/auth.log
maxretry = 10

[vsftpd]
enabled = true
port = ftp,ftp-data,ftps,ftps-data
filter = vsftpd
logpath = /var/log/vsftpd.log
maxretry = 10
JEOF
fi
systemctl enable fail2ban
systemctl reset-failed fail2ban 2>/dev/null
timeout 60 systemctl restart fail2ban || echo "  ⚠ fail2ban 重启失败（结尾注册表会标记，可稍后手动 restart）"

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
ufw allow 9000/tcp               # S3 API (rclone serve s3)
ufw allow 8090/tcp               # NAS Web Panel
ufw --force enable
echo "  ✓ 安全配置完成"

# ==================== [10/10] 安装 NAS Web 管理面板 ====================
echo ""
echo "[10/10] 安装 NAS Web 管理面板..."
if [ "$CONFIG_ONLY" = true ]; then
    echo "  --config-only 模式跳过面板安装（二进制由 upgrade.sh / OTA 负责）"
elif [ -f "$NAS_DIR/web/nas-panel" ]; then
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
systemctl reset-failed nas-panel 2>/dev/null
timeout 60 systemctl restart nas-panel || echo "  ⚠ nas-panel 重启失败（结尾注册表会标记，可稍后手动 restart）"

# 配置 sudo 免密权限（nas-panel 需要执行系统管理命令）
# ⚠️ 白名单不变式：禁止加入 root-equivalent 通配条目（bash -c */sh -c */cat */cp */mv */
# rm -rf */find */apt-get install */systemd-run */openssl enc 等——任一都等于无限制 root）。
# 代码中残留的 SudoExec("bash"/"sh","-c",…) 管道调用点已于本轮清零（services.go 的
# unit heredoc 改为纯 Go 构造 + SafeWriteFile、users/create.go 与 groups.go 的
# sed/grep 管道改为直传参数或纯 Go 行操作、diagnostics.go 的 echo>sysfs 改为
# os.WriteFile、rclone.go 的 chown --reference 改为 os.Stat+os.Chown）；切换到
# 非 root 最小权限运行的前置障碍已清除。本不变式（禁入 root-equivalent 通配条目）
# 继续保留，防止后续改动再引入等价风险。
SUDOERS_FILE="/etc/sudoers.d/nas-panel"
echo "${NAS_USER} ALL=(ALL) NOPASSWD: /usr/bin/pdbedit, /opt/nas/scripts/add-user.sh, /opt/nas/scripts/remove-user.sh, /usr/sbin/smartctl, /usr/bin/chpasswd, /usr/bin/smbpasswd, /usr/bin/htpasswd, /bin/systemctl start *, /bin/systemctl stop *, /bin/systemctl restart *, /bin/systemctl reset-failed *, /bin/systemctl enable *, /bin/systemctl disable *, /usr/sbin/ufw status, /usr/sbin/ufw status numbered, /usr/sbin/ufw status verbose, /usr/sbin/ufw allow *, /usr/sbin/ufw deny *, /usr/sbin/ufw --force delete *, /usr/sbin/ufw --force enable, /usr/sbin/ufw disable, /usr/sbin/exportfs, /usr/sbin/smartctl -H *, /usr/sbin/smartctl -a *, /usr/bin/tee /opt/nas/.env, /usr/bin/tee -a /etc/samba/smb.conf, /usr/bin/tee /etc/samba/smb.conf, /usr/bin/tee /etc/vsftpd.userlist, /usr/bin/tee -a /etc/vsftpd.userlist, /usr/bin/tee /etc/exports, /usr/bin/tee /etc/nfs.conf, /usr/bin/tee /etc/fail2ban/jail.local, /usr/bin/tee /etc/rclone-htpasswd, /usr/bin/journalctl -p err -n * --no-pager --since *, /usr/sbin/pvs --noheadings *, /usr/sbin/vgs --noheadings *, /usr/sbin/lvs --noheadings *, /usr/sbin/fdisk -l, /usr/sbin/mkfs.ext4 -F *, /usr/sbin/mkfs.xfs -f *, /usr/sbin/mkfs.btrfs *, /bin/mount *, /bin/umount *, /bin/mkdir -p /data/*, /bin/mkdir -p /data, /bin/cat /etc/samba/smb.conf, /bin/cat /etc/vsftpd.userlist, /bin/cat /etc/exports, /bin/cat /etc/nfs.conf, /bin/cat /etc/fail2ban/jail.local, /bin/cat /etc/rclone-htpasswd, /opt/nas/scripts/backup-config.sh, /opt/nas/scripts/restore-config.sh, /bin/rm -f /opt/nas/backups/*, /usr/bin/blkid -s UUID -o value *, /usr/bin/findmnt -n -o TARGET *, /usr/bin/tee /etc/fstab, /bin/chown -R *, /sbin/pvcreate -f *, /sbin/vgcreate -f *, /sbin/lvcreate *, /sbin/vgextend *, /sbin/lvextend *, /sbin/resize2fs *, /sbin/xfs_growfs *, /sbin/pvs --noheadings *, /sbin/vgs --noheadings *, /sbin/lvs --noheadings *, /sbin/pvremove -f *, /sbin/vgremove -f *, /sbin/lvremove -f *, /usr/sbin/wipefs *, /usr/sbin/mdadm *, /usr/sbin/parted *, /bin/ls *, /usr/sbin/xfs_quota *, /sbin/xfs_quota *, /bin/systemctl reload *, /bin/systemctl is-active *, /bin/systemctl daemon-reload, /bin/systemctl list-unit-files *, /usr/sbin/vgchange *, /sbin/vgchange *, /usr/sbin/useradd *, /usr/sbin/userdel *, /usr/sbin/usermod *, /usr/sbin/groupadd *, /usr/bin/gpasswd *, /usr/bin/id, /usr/bin/id *, /usr/bin/passwd -S *, /usr/bin/openssl req *, /usr/bin/openssl x509 *, /usr/bin/openssl rsa *, /usr/bin/timedatectl set-timezone *, /usr/bin/hostnamectl set-hostname *, /usr/sbin/sysctl -w *, /sbin/sysctl -w *, /usr/bin/rclone *, /usr/bin/crontab -u * -l, /usr/bin/apt-get update, /usr/bin/apt-get upgrade *, /usr/bin/apt update, /usr/bin/apt upgrade *, /usr/sbin/update-initramfs *, /usr/bin/whoami, /opt/nas/scripts/apply-upgrade.sh *, /opt/nas/scripts/backup-data.sh *, /bin/cat /etc/fstab, /bin/cat /etc/mdadm/mdadm.conf, /bin/cat /etc/ssh/sshd_config, /bin/rm -f /opt/nas/data/*, /bin/rm -f /etc/nas/certs/*, /bin/mkdir -p /etc/nas/*, /bin/mkdir -p /data/system" > "$SUDOERS_FILE"
chmod 440 "$SUDOERS_FILE"
visudo -cf "$SUDOERS_FILE" 2>/dev/null || { echo "  错误: sudoers 语法检查失败"; rm -f "$SUDOERS_FILE"; }
# 追加改密码需要的命令（sed）
# 安全红线（beta.27 自定原则）：白名单禁止 root-equivalent 通配。
# 曾在此处追加过 `NOPASSWD: /usr/bin/sed -i *`——sed -i 通配可改写
# /etc/shadow、sudoers 本身，等价无约束 root，且面板 Go 代码自 beta.26
# 起已全部改用纯 Go 行级重写（updateNasPassLine/removeExactLine 等），
# 该条目是死后门。永久移除，不要再以任何形式加回（2026-10-23 拍板）。
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
for svc in smbd nmbd nfs-kernel-server vsftpd rclone-webdav rclone-s3 fail2ban nas-panel; do
    printf "  %-22s %s\n" "$svc:" "$(systemctl is-active $svc)"
done

# ==================== 系统注册表检查 ====================
echo ""
echo "========================================="
echo "系统注册表检查 (41 项)"
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
echo "  - Samba:       //NAS_IP/public (公共共享)"
echo "  - Samba:       //NAS_IP/$NAS_USER (用户主目录)"
echo "  - NFS:         mount -t nfs NAS_IP:/data/nas1/public /mnt/nas"
echo "  - FTP:         ftp://NAS_IP/ (用户名: $NAS_USER)"
echo "  - WebDAV:      http://NAS_IP:8080/ (用户名: $NAS_USER)"
echo "  - S3 API:       http://NAS_IP:9000 (access key: $S3_ACCESS_KEY, s3cmd --no-ssl --host=NAS_IP:9000 ls s3://public/)"
echo "  - Web 面板:    http://NAS_IP:8090 (用户名: $NAS_USER)"
echo ""
echo "管理脚本:"
echo "  添加用户: sudo $NAS_DIR/scripts/add-user.sh <用户名> <密码>"
echo "  删除用户: sudo $NAS_DIR/scripts/remove-user.sh <用户名> [--delete-data]"
echo "========================================="
