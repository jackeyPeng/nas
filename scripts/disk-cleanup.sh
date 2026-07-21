#!/bin/bash
# disk-cleanup.sh — NAS 磁盘清理脚本
# 清理系统日志、临时文件、回收站，释放磁盘空间
# 用法: sudo ./disk-cleanup.sh [--dry-run] [--aggressive]

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'
BOLD='\033[1m'

DRY_RUN=false
AGGRESSIVE=false

for arg in "$@"; do
    case "$arg" in
        --dry-run) DRY_RUN=true ;;
        --aggressive) AGGRESSIVE=true ;;
        -h|--help)
            echo "用法: $0 [--dry-run] [--aggressive]"
            echo ""
            echo "  --dry-run      只显示会清理什么，不实际删除"
            echo "  --aggressive   深度清理（包括旧内核、包缓存、回收站）"
            echo ""
            echo "默认清理: 系统日志(7天前)、临时文件、缩略图缓存"
            exit 0
            ;;
    esac
done

TOTAL_FREED=0

section() { echo -e "\n${BOLD}═══ $1 ═══${NC}"; }

report() {
    local desc="$1" size="$2"
    if [[ "$DRY_RUN" == "true" ]]; then
        echo -e "  ${CYAN}[预览]${NC} $desc: ${YELLOW}$size${NC}"
    else
        echo -e "  ${GREEN}✓${NC} $desc: ${GREEN}$size${NC}"
    fi
}

get_size() {
    du -sb "$1" 2>/dev/null | awk '{print $1}' || echo 0
}

human_size() {
    local bytes=$1
    awk -v b="$bytes" 'BEGIN{
        if (b >= 1073741824) printf "%.1fG", b/1073741824
        else if (b >= 1048576) printf "%.1fM", b/1048576
        else if (b >= 1024) printf "%.1fK", b/1024
        else printf "%dB", b
    }'
}

# ─── 清理前磁盘状态 ───
section "磁盘状态（清理前）"
df -h / /data/nas* 2>/dev/null | grep -v "^Filesystem" || df -h /

# ─── 1. 系统日志 ───
section "系统日志"
JOURNAL_SIZE=$(journalctl --disk-usage 2>/dev/null | grep -oP '[\d.]+[KMGT]?' || echo "?")
echo "  当前日志占用: $JOURNAL_SIZE"

if [[ "$DRY_RUN" == "false" ]]; then
    journalctl --vacuum-time=7d 2>/dev/null || true
    AFTER=$(journalctl --disk-usage 2>/dev/null | grep -oP '[\d.]+[KMGT]?' || echo "?")
    report "清理7天前日志" "$JOURNAL_SIZE → $AFTER"
else
    report "将清理7天前日志" "$JOURNAL_SIZE"
fi

# ─── 2. 临时文件 ───
section "临时文件"
TMP_SIZE=$(get_size /tmp)
TMP_COUNT=$(find /tmp -type f -mtime +7 2>/dev/null | wc -l)
if [[ $TMP_COUNT -gt 0 ]]; then
    if [[ "$DRY_RUN" == "false" ]]; then
        find /tmp -type f -mtime +7 -delete 2>/dev/null || true
    fi
    report "清理 /tmp 中7天前的 $TMP_COUNT 个文件" "$(human_size $TMP_SIZE)"
else
    echo "  /tmp 无需清理"
fi

# ─── 3. 回收站 ───
section "回收站"
RECYCLE_FOUND=false
for mp in /data/nas*; do
    if [[ -d "$mp" ]]; then
        for recycle_dir in "$mp"/*/\#recycle "$mp"/\#recycle; do
            if [[ -d "$recycle_dir" ]]; then
                SIZE=$(get_size "$recycle_dir")
                if [[ $SIZE -gt 0 ]]; then
                    RECYCLE_FOUND=true
                    if [[ "$DRY_RUN" == "false" && "$AGGRESSIVE" == "true" ]]; then
                        rm -rf "$recycle_dir"/* 2>/dev/null || true
                        report "清空回收站 $recycle_dir" "$(human_size $SIZE)"
                    else
                        report "回收站 $recycle_dir" "$(human_size $SIZE) (需 --aggressive 清理)"
                    fi
                fi
            fi
        done
    fi
done
if [[ "$RECYCLE_FOUND" == "false" ]]; then
    echo "  无回收站数据"
fi

# ─── 4. APT 缓存 ───
section "APT 缓存"
if command -v apt-get &>/dev/null; then
    APT_CACHE=$(get_size /var/cache/apt/archives)
    if [[ $APT_CACHE -gt 1048576 ]]; then  # > 1MB
        if [[ "$DRY_RUN" == "false" ]]; then
            apt-get clean 2>/dev/null || true
        fi
        report "清理 APT 缓存" "$(human_size $APT_CACHE)"
    else
        echo "  APT 缓存很小，无需清理"
    fi
fi

# ─── 5. 旧内核（仅 aggressive） ───
if [[ "$AGGRESSIVE" == "true" ]]; then
    section "旧内核"
    if command -v apt-get &>/dev/null; then
        CURRENT_KERNEL=$(uname -r)
        OLD_KERNELS=$(dpkg -l 'linux-image-*' 2>/dev/null | grep ^ii | awk '{print $2}' | grep -v "$CURRENT_KERNEL" | wc -l || echo 0)
        if [[ $OLD_KERNELS -gt 0 ]]; then
            if [[ "$DRY_RUN" == "false" ]]; then
                apt-get autoremove --purge -y 2>/dev/null || true
            fi
            report "清理 $OLD_KERNELS 个旧内核" "(apt autoremove)"
        else
            echo "  无旧内核"
        fi
    fi
fi

# ─── 6. 缩略图缓存 ───
section "缩略图缓存"
THUMB_DIRS=(
    /home/*/.cache/thumbnails
    /root/.cache/thumbnails
)
THUMB_TOTAL=0
for dir in "${THUMB_DIRS[@]}"; do
    if [[ -d "$dir" ]]; then
        SIZE=$(get_size "$dir")
        THUMB_TOTAL=$((THUMB_TOTAL + SIZE))
        if [[ "$DRY_RUN" == "false" ]]; then
            rm -rf "$dir"/* 2>/dev/null || true
        fi
    fi
done
if [[ $THUMB_TOTAL -gt 0 ]]; then
    report "清理缩略图缓存" "$(human_size $THUMB_TOTAL)"
else
    echo "  无缩略图缓存"
fi

# ─── 清理后磁盘状态 ───
section "磁盘状态（清理后）"
df -h / /data/nas* 2>/dev/null | grep -v "^Filesystem" || df -h /

if [[ "$DRY_RUN" == "true" ]]; then
    echo -e "\n${YELLOW}这是预览模式，未实际清理。去掉 --dry-run 执行实际清理。${NC}"
fi
