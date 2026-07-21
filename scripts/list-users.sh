#!/bin/bash
# list-users.sh — NAS 用户及配额一览
# 列出所有 NAS 用户（Samba/FTP）、磁盘配额、共享文件夹权限
# 用法: ./list-users.sh

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'
BOLD='\033[1m'

section() { echo -e "\n${BOLD}${BLUE}═══ $1 ═══${NC}"; }

# ─── Samba 用户 ───
section "Samba 用户"
if command -v pdbedit &>/dev/null; then
    SAMBA_USERS=$(pdbedit -L 2>/dev/null || echo "")
    if [[ -n "$SAMBA_USERS" ]]; then
        echo "$SAMBA_USERS" | while IFS=: read -r user _; do
            # Get home dir size
            HOME_DIR=$(getent passwd "$user" 2>/dev/null | cut -d: -f6 || echo "")
            if [[ -n "$HOME_DIR" && -d "$HOME_DIR" ]]; then
                HOME_SIZE=$(du -sh "$HOME_DIR" 2>/dev/null | awk '{print $1}' || echo "?")
            else
                HOME_SIZE="无"
            fi
            echo -e "  ${GREEN}●${NC} $user (home: $HOME_SIZE)"
        done
    else
        echo "  无 Samba 用户"
    fi
else
    echo "  Samba 未安装"
fi

# ─── FTP 用户 ───
section "FTP 用户"
if [[ -f /etc/vsftpd.userlist ]]; then
    FTP_COUNT=0
    while IFS= read -r user; do
        user=$(echo "$user" | xargs)
        [[ -z "$user" ]] && continue
        echo -e "  ${GREEN}●${NC} $user"
        FTP_COUNT=$((FTP_COUNT+1))
    done < /etc/vsftpd.userlist
    [[ $FTP_COUNT -eq 0 ]] && echo "  无 FTP 用户"
else
    echo "  FTP 未配置"
fi

# ─── 系统用户（有数据目录的） ───
section "系统用户（数据目录）"
for mp in /data/nas*; do
    if [[ -d "$mp" ]]; then
        for dir in "$mp"/*/; do
            [[ -d "$dir" ]] || continue
            dirname=$(basename "$dir")
            # Skip system folders
            [[ "$dirname" == "lost+found" || "$dirname" == "#recycle" ]] && continue
            OWNER=$(stat -c '%U' "$dir" 2>/dev/null || echo "?")
            SIZE=$(du -sh "$dir" 2>/dev/null | awk '{print $1}' || echo "?")
            echo -e "  $mp/$dirname  所有者:${BOLD}$OWNER${NC}  大小:$SIZE"
        done
    fi
done

# ─── 存储配额 ───
section "存储配额 (XFS Project Quota)"
QUOTA_FOUND=false
for mp in /data/nas*; do
    if mountpoint -q "$mp" 2>/dev/null && mount | grep "$mp" | grep -q prjquota; then
        REPORT=$(xfs_quota -x -c "report -p" "$mp" 2>/dev/null || echo "")
        if [[ -n "$REPORT" ]]; then
            # Skip header lines, show projects with limits
            echo "$REPORT" | tail -n +5 | while read -r line; do
                [[ -z "$line" ]] && continue
                PROJ=$(echo "$line" | awk '{print $1}')
                [[ "$PROJ" == "#0" ]] && continue
                USED=$(echo "$line" | awk '{print $2}')
                SOFT=$(echo "$line" | awk '{print $3}')
                HARD=$(echo "$line" | awk '{print $4}')
                
                if [[ "$HARD" != "0" && -n "$HARD" ]]; then
                    # Convert KB to human readable
                    USED_H=$(echo "$USED" | awk '{if($1>=1048576) printf "%.1fG", $1/1048576; else if($1>=1024) printf "%.1fM", $1/1024; else printf "%dK", $1}')
                    HARD_H=$(echo "$HARD" | awk '{if($1>=1048576) printf "%.0fG", $1/1048576; else if($1>=1024) printf "%.0fM", $1/1024; else printf "%dK", $1}')
                    
                    # Calculate percentage
                    if [[ "$HARD" -gt 0 ]]; then
                        PCT=$((USED * 100 / HARD))
                    else
                        PCT=0
                    fi
                    
                    # Color based on usage
                    if [[ $PCT -gt 90 ]]; then
                        COLOR="$RED"
                    elif [[ $PCT -gt 80 ]]; then
                        COLOR="$YELLOW"
                    else
                        COLOR="$GREEN"
                    fi
                    
                    echo -e "  $mp  ${BOLD}$PROJ${NC}  ${COLOR}${USED_H} / ${HARD_H} (${PCT}%)${NC}"
                    QUOTA_FOUND=true
                fi
            done
        fi
    fi
done
if [[ "$QUOTA_FOUND" == "false" ]]; then
    echo -e "  ${CYAN}无配额配置${NC}"
fi

# ─── 共享文件夹权限 ───
section "共享文件夹权限"
if [[ -f /etc/samba/smb.conf ]]; then
    SHARE_COUNT=0
    in_share=false
    share_name=""
    share_path=""
    share_users=""
    share_rw=""
    
    while IFS= read -r line; do
        trimmed=$(echo "$line" | xargs)
        if [[ "$trimmed" =~ ^\[(.+)\]$ ]]; then
            # Print previous share
            if [[ -n "$share_name" && "$share_name" != "global" ]]; then
                RW_TEXT="读写"
                [[ "$share_rw" == "yes" ]] && RW_TEXT="只读"
                echo -e "  ${GREEN}[$share_name]${NC}  $share_path  $RW_TEXT  用户:$share_users"
                SHARE_COUNT=$((SHARE_COUNT+1))
            fi
            share_name="${BASH_REMATCH[1]}"
            share_path=""
            share_users=""
            share_rw=""
            in_share=true
        elif [[ "$in_share" == "true" ]]; then
            if [[ "$trimmed" =~ ^path[[:space:]]*=[[:space:]]*(.+) ]]; then
                share_path="${BASH_REMATCH[1]}"
            elif [[ "$trimmed" =~ ^valid\ users[[:space:]]*=[[:space:]]*(.+) ]]; then
                share_users="${BASH_REMATCH[1]}"
            elif [[ "$trimmed" =~ ^read\ only[[:space:]]*=[[:space:]]*(.+) ]]; then
                share_rw="${BASH_REMATCH[1]}"
            fi
        fi
    done < /etc/samba/smb.conf
    
    # Print last share
    if [[ -n "$share_name" && "$share_name" != "global" ]]; then
        RW_TEXT="读写"
        [[ "$share_rw" == "yes" ]] && RW_TEXT="只读"
        echo -e "  ${GREEN}[$share_name]${NC}  $share_path  $RW_TEXT  用户:$share_users"
        SHARE_COUNT=$((SHARE_COUNT+1))
    fi
    
    [[ $SHARE_COUNT -eq 0 ]] && echo "  无共享配置"
else
    echo "  Samba 未配置"
fi

echo ""
