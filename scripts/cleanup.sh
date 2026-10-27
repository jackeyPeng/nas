#!/bin/bash
# NAS 清除脚本
# 用于将系统恢复到安装前的状态
# 用法: ./cleanup.sh [--keep-data] [-y|--yes]（fm 等普通用户直跑，特权命令按需 sudo）
#       sudo ./cleanup.sh [--keep-data] [-y|--yes]（root 直跑同样兼容）
#
# 选项:
#   --keep-data    保留 /data 目录（默认会删除）
#   -y, --yes      跳过交互确认，直接执行（自动化/管道场景；也可用 ASSUME_YES=1 环境变量）

set -e

# ── 特权命令封装 ────────────────────────────────────────────
# 与 install.sh 同款：EUID=0 时 SUDO 为空串，root 直跑原样兼容；
# EUID≠0（fm 等普通用户）时 SUDO=sudo，逐条特权命令加前缀。
if [ "$EUID" -eq 0 ]; then
    SUDO=""
else
    SUDO="sudo"
    # 本脚本本地直跑（非 curl|bash 管道），一次性缓存 sudo 密码，
    # tty 交互由 sudo 自己完成，后续 $SUDO 调用在缓存有效期内不再提示。
    sudo -v
fi

KEEP_DATA=false
ASSUME_YES="${ASSUME_YES:-false}"
for arg in "$@"; do
    case "$arg" in
        --keep-data) KEEP_DATA=true ;;
        -y|--yes)    ASSUME_YES=true ;;
        *) echo "未知选项: $arg"; echo "用法: $0 [--keep-data] [-y|--yes]"; exit 2 ;;
    esac
done

echo "========================================="
echo "NAS 清除脚本"
echo "========================================="
echo ""

if [ "$KEEP_DATA" = true ]; then
    echo "⚠️  将保留 /data 目录"
else
    echo "⚠️  将删除 /data 目录及所有数据"
fi

echo ""
if [ "$ASSUME_YES" = true ]; then
    echo "（-y/--yes：跳过交互确认，直接执行）"
else
    read -p "确定要继续吗？(输入 yes 确认): " confirm
    if [ "$confirm" != "yes" ]; then
        echo "已取消"
        exit 0
    fi
fi

echo ""

# ==================== [1/8] 停止所有 NAS 服务 ====================
echo "[1/8] 停止所有 NAS 服务..."
SERVICES="smbd nmbd nfs-kernel-server vsftpd rclone-webdav filebrowser rclone-s3 fail2ban nas-panel"
for svc in $SERVICES; do
    if systemctl is-active --quiet "$svc" 2>/dev/null; then
        $SUDO systemctl stop "$svc"
        echo "  停止 $svc"
    fi
    if systemctl is-enabled --quiet "$svc" 2>/dev/null; then
        $SUDO systemctl disable "$svc" 2>/dev/null
        echo "  禁用 $svc"
    fi
done
echo "  ✓ 服务已停止"

# ==================== [2/8] 删除 systemd 服务文件 ====================
echo ""
echo "[2/8] 删除 systemd 服务文件..."
SERVICE_FILES="/etc/systemd/system/rclone-webdav.service /etc/systemd/system/filebrowser.service /etc/systemd/system/rclone-s3.service /etc/systemd/system/nas-panel.service"
for file in $SERVICE_FILES; do
    if [ -f "$file" ]; then
        $SUDO rm -f "$file"
        echo "  删除 $file"
    fi
done
$SUDO systemctl daemon-reload
echo "  ✓ 服务文件已删除"

# ==================== [3/8] 删除配置文件 ====================
echo ""
echo "[3/8] 删除配置文件..."
CONFIG_FILES="
/etc/samba/smb.conf
/etc/exports
/etc/nfs.conf
/etc/vsftpd.conf
/etc/vsftpd.userlist
/etc/fail2ban/jail.local
/etc/filebrowser/filebrowser.db
/etc/rclone-htpasswd
/etc/rclone/s3-env
/etc/sudoers.d/nas-panel
/var/log/vsftpd.log
/var/log/filebrowser.log
"
for file in $CONFIG_FILES; do
    if [ -f "$file" ]; then
        $SUDO rm -f "$file"
        echo "  删除 $file"
    fi
done
if [ -d "/etc/filebrowser" ]; then
    $SUDO rm -rf /etc/filebrowser
    echo "  删除 /etc/filebrowser/"
fi
echo "  ✓ 配置文件已删除"

# ==================== [4/8] 删除二进制文件 ====================
echo ""
echo "[4/8] 删除二进制文件..."
BINARIES="/usr/local/bin/filebrowser /usr/local/bin/nas-panel"
for bin in $BINARIES; do
    if [ -f "$bin" ]; then
        $SUDO rm -f "$bin"
        echo "  删除 $bin"
    fi
done
echo "  ✓ 二进制文件已删除"

# ==================== [5/8] 拆除存储池 + 清理磁盘 RAID/LVM 签名 ====================
# （48 机实踩事故：本段原在文件末尾「[额外]」位置、purge 软件包与 rm -rf /data 之后。
#  set -e 下 /data/nas* 还挂着时原 [6/7] 的 rm -rf /data 遇 EBUSY 非零退出，脚本中止，
#  导致本段与后续清理全部不执行，残留 md0 仍挂载、/opt/nas 软链接仍在。
#  现移到 purge 软件包之前：lvremove/vgremove/pvremove/mdadm/wipefs 等命令依赖 lvm2/mdadm
#  软件包，必须在 [6/8] purge 之前执行，否则命令已被卸载。）
echo ""
echo "[5/8] 拆除存储池 + 清理磁盘 RAID/LVM 签名..."

# 卸载 /data/nas* 存储池
# （实踩：服务刚 stop 时 nfsd/smbd 收尾会短暂持有挂载点，单次 umount busy 被静默吞掉
#  → LVM/磁盘清理连锁跳过、/data 删不掉。先静默 NFS 导出并兜底 stop，再重试等待，失败如实警告）
$SUDO exportfs -ra 2>/dev/null || true
$SUDO systemctl stop nfs-kernel-server smbd nmbd vsftpd rclone-webdav rclone-s3 2>/dev/null || true
for mp in $(mount | grep -oE '/data/nas[0-9]+' | sort -u); do
    mp_ok=false
    for _try in 1 2 3 4 5 6; do
        if $SUDO umount "$mp" 2>/dev/null; then mp_ok=true; break; fi
        sleep 1
    done
    if [ "$mp_ok" = true ]; then
        echo "  卸载 $mp"
    else
        echo "  ⚠️  $mp 仍被占用无法卸载（fuser -vm $mp 排查后重跑），后续 LVM/磁盘/数据清理可能不完整"
    fi
done

# 拆除 LVM：先 LV → VG → PV（旧版本只做 mdadm，遇到 LVM 池会残留）
if command -v lvremove >/dev/null 2>&1; then
    for lv in $(lvs --noheadings -o lv_name,vg_name 2>/dev/null | awk '{print $2"/"$1}'); do
        $SUDO lvremove -f "$lv" 2>/dev/null && echo "  删除 LV $lv" || true
    done
fi
if command -v vgremove >/dev/null 2>&1; then
    for vg in $(vgs --noheadings -o vg_name 2>/dev/null); do
        $SUDO vgremove -f "$vg" 2>/dev/null && echo "  删除 VG $vg" || true
    done
fi
if command -v pvremove >/dev/null 2>&1; then
    for pv in $(pvs --noheadings -o pv_name 2>/dev/null); do
        $SUDO pvremove -f "$pv" 2>/dev/null && echo "  删除 PV $pv" || true
    done
fi

# 清除数据盘 fstab 条目（/data/nas*）
if [ -f /etc/fstab ]; then
    $SUDO sed -i '/\/data\/nas[0-9]*/d' /etc/fstab
    echo "  清除 fstab 数据盘条目"
fi

# 先停止所有 RAID 阵列：active 阵列会持有成员盘，若不先 stop，下面对成员盘的
# wipefs 会 EBUSY 失败并被 2>/dev/null 静默吞掉，残留 linux_raid_member 签名。
# （48 机实踩：旧顺序把 stop /dev/md* 放在 wipefs 循环之后，导致 sdb/sdc 签名残留）
for md in /dev/md*; do
    [ -e "$md" ] && $SUDO mdadm --stop "$md" 2>/dev/null && echo "  ✓ $md 已停止" || true
done
# 清理 mdadm.conf
$SUDO sed -i '/^ARRAY/d' /etc/mdadm/mdadm.conf 2>/dev/null || true

# 识别系统盘（根挂载所在磁盘），排除后得到数据盘
SYS_DISK=$(findmnt -n -o SOURCE / 2>/dev/null | sed 's|/dev/||; s|[0-9]*$||; s|p[0-9]*$||')
DATA_DISKS=$(lsblk -nd -o NAME,TYPE | awk '$2=="disk"{print $1}' | grep -v "^$SYS_DISK$")
for disk in $DATA_DISKS; do
    # 擦除签名（阵列已停，成员盘不再 busy，wipefs 可擦净 RAID/LVM 签名）
    if $SUDO wipefs -a "/dev/${disk}" 2>/dev/null | grep -q "erased"; then
        echo "  ✓ /dev/${disk} 签名已擦除"
    fi
done
echo "  ✓ 磁盘签名清理完成"

# ==================== [6/8] 卸载软件包 ====================
echo ""
echo "[6/8] 卸载软件包..."
PACKAGES="samba smbclient nfs-kernel-server nfs-common vsftpd rclone fail2ban ufw smartmontools unattended-upgrades apache2-utils"
# DEBIAN_FRONTEND 环境变量赋值需 `sudo env VAR=val cmd` 透传，见 install.sh 同款注释
$SUDO env DEBIAN_FRONTEND=noninteractive apt-get purge -y $PACKAGES 2>/dev/null || true
$SUDO env DEBIAN_FRONTEND=noninteractive apt-get autoremove -y 2>/dev/null || true
echo "  ✓ 软件包已卸载"

# ==================== [7/8] 清理数据目录 ====================
echo ""
echo "[7/8] 清理数据目录..."
if [ "$KEEP_DATA" = false ]; then
    if [ -d "/data" ]; then
        if mount | grep -q '/data/nas'; then
            echo "  ⚠️  /data 下仍有挂载点（/data/nas*），跳过 rm -rf /data 以避免误删/残留挂载点"
            echo "  ⚠️  请手动排查（mount | grep /data/nas、fuser -vm 对应挂载点）后重跑本脚本"
        else
            $SUDO rm -rf /data || echo "  ⚠️  删除 /data/ 失败，请手动检查"
            echo "  删除 /data/"
        fi
    fi
else
    echo "  保留 /data/ (--keep-data)"
fi
echo "  ✓ 数据目录已处理"

# ==================== [8/8] 清理其他文件 ====================
echo ""
echo "[8/8] 清理其他文件..."
# 清理 Samba 用户（自动获取部署时的用户）
NAS_USER="${SUDO_USER:-$USER}"
if [ -n "$NAS_USER" ] && [ "$NAS_USER" != "root" ]; then
    if command -v smbpasswd &>/dev/null; then
        $SUDO smbpasswd -x "$NAS_USER" 2>/dev/null || true
        echo "  删除 Samba 用户 $NAS_USER"
    fi
fi

# 清理 /opt/nas 软链接与仓库实体
# 坑（2026-10-23 实踩）：安装布局是 /opt/nas -> $HOME/soft/nas（sudo 下为
# /root/soft/nas）。只删软链接会把仓库实体连同 data/（面板数据库：ai.db/
# tasks.db/audit.db/registry.db…）留在机器上，下次 install.sh 检测到已有
# 仓库直接 git pull + 重建软链接，面板"继承"旧数据——所谓 factory reset
# 实际不是 factory（发布后复验时旧任务记录穿越重置复活，实锤此路径）。
if [ -L "/opt/nas" ]; then
    NAS_REAL="$(readlink -f /opt/nas)"
    $SUDO rm -f /opt/nas
    echo "  删除 /opt/nas 软链接"
    if [ -n "$NAS_REAL" ] && [ "$NAS_REAL" != "/" ] && [ -d "$NAS_REAL" ]; then
        $SUDO rm -rf "$NAS_REAL"
        echo "  删除仓库实体 $NAS_REAL（含 data/ 面板数据库）"
    fi
elif [ -d "/opt/nas" ]; then
    $SUDO rm -rf /opt/nas
    echo "  删除 /opt/nas 目录"
fi
for CAND in /root/soft/nas "${HOME}/soft/nas"; do
    if [ -d "$CAND" ]; then
        $SUDO rm -rf "$CAND"
        echo "  删除仓库目录 $CAND"
    fi
done

# 清理 UFW（如果已重新安装）
if command -v ufw &>/dev/null; then
    $SUDO ufw --force reset 2>/dev/null || true
    echo "  重置 UFW 规则"
fi

# 清理 sudoers
if [ -f /etc/sudoers.d/nas-panel ]; then
    $SUDO rm -f /etc/sudoers.d/nas-panel
    echo "  删除 /etc/sudoers.d/nas-panel"
fi

# 清理监控 cron
NAS_USER="${SUDO_USER:-$USER}"
if [ -n "$NAS_USER" ] && [ "$NAS_USER" != "root" ]; then
    $SUDO sh -c "crontab -u '$NAS_USER' -l 2>/dev/null | grep -v monitor.sh | crontab -u '$NAS_USER' -" 2>/dev/null || true
    echo "  删除监控 cron"
fi

# 清理告警状态
$SUDO rm -rf /var/lib/nas-monitor 2>/dev/null || true

# 清理日志
$SUDO rm -f /var/log/samba/log.* 2>/dev/null || true
$SUDO rm -rf /var/log/samba 2>/dev/null || true
$SUDO rm -rf /var/lib/samba 2>/dev/null || true
$SUDO rm -rf /var/lib/nfs 2>/dev/null || true
$SUDO rm -rf /var/lib/vsftpd 2>/dev/null || true

echo "  ✓ 其他文件已清理"

# ==================== 完成 ====================
echo ""
echo "========================================="
echo "NAS 清除完成!"
echo "========================================="
echo ""
echo "已清除的内容:"
echo "  - 所有 NAS 服务（Samba、NFS、FTP、WebDAV、FileBrowser、S3）"
echo "  - systemd 服务文件"
echo "  - 配置文件"
echo "  - 二进制文件（filebrowser、nas-panel）"
echo "  - apt 软件包"
if [ "$KEEP_DATA" = false ]; then
    echo "  - /data 数据目录"
else
    echo "  - /data 数据目录已保留"
fi
echo ""
echo "系统已恢复到安装前的状态"
echo "========================================="
