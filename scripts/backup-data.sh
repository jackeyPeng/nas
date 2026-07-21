#!/bin/bash
# backup-data.sh — NAS 数据备份脚本
# 用 rsync 将数据目录备份到指定目标（本地目录/远程主机/外部硬盘）
# 用法:
#   sudo ./backup-data.sh /mnt/backup                    # 备份到本地目录
#   sudo ./backup-data.sh user@remote:/backup            # 备份到远程
#   sudo ./backup-data.sh --dry-run /mnt/backup          # 试运行（不实际写入）

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'
BOLD='\033[1m'

# ─── 配置 ───
BACKUP_SOURCES=()
EXCLUDES=(
    "#recycle"
    ".Trash*"
    "lost+found"
    "*.tmp"
    ".DS_Store"
    "Thumbs.db"
)

DRY_RUN=false
TARGET=""

# ─── 参数解析 ───
while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        --source)
            BACKUP_SOURCES+=("$2")
            shift 2
            ;;
        -h|--help)
            echo "用法: $0 [选项] <目标路径>"
            echo ""
            echo "选项:"
            echo "  --dry-run          试运行，不实际写入"
            echo "  --source <路径>    指定备份源（可多次使用，默认所有 /data/nas*）"
            echo "  -h, --help         显示帮助"
            echo ""
            echo "示例:"
            echo "  $0 /mnt/usb-backup                  # 备份到 USB 硬盘"
            echo "  $0 user@192.168.1.100:/backup       # 备份到远程主机"
            echo "  $0 --source /data/nas1 /mnt/backup  # 只备份 nas1"
            echo "  $0 --dry-run /mnt/backup            # 先看看会备份什么"
            exit 0
            ;;
        *)
            TARGET="$1"
            shift
            ;;
    esac
done

if [[ -z "$TARGET" ]]; then
    echo -e "${RED}错误: 请指定备份目标${NC}"
    echo "用法: $0 [--dry-run] [--source <路径>] <目标路径>"
    echo "帮助: $0 --help"
    exit 1
fi

# Default sources: all /data/nas* mounts
if [[ ${#BACKUP_SOURCES[@]} -eq 0 ]]; then
    for mp in /data/nas*; do
        if mountpoint -q "$mp" 2>/dev/null; then
            BACKUP_SOURCES+=("$mp")
        fi
    done
fi

if [[ ${#BACKUP_SOURCES[@]} -eq 0 ]]; then
    echo -e "${RED}错误: 没有找到数据目录 (/data/nas*)${NC}"
    exit 1
fi

# ─── 显示备份计划 ───
echo -e "${BOLD}═══ NAS 数据备份 ═══${NC}"
echo ""
echo "备份源:"
for src in "${BACKUP_SOURCES[@]}"; do
    SIZE=$(du -sh "$src" 2>/dev/null | awk '{print $1}' || echo "?")
    echo "  $src ($SIZE)"
done
echo ""
echo "目标: $TARGET"
if [[ "$DRY_RUN" == "true" ]]; then
    echo -e "${YELLOW}模式: 试运行（不会实际写入）${NC}"
fi
echo ""

# ─── 构建 rsync 排除参数 ───
EXCLUDE_ARGS=()
for ex in "${EXCLUDES[@]}"; do
    EXCLUDE_ARGS+=("--exclude=$ex")
done

# ─── 执行备份 ───
TOTAL_DIRS=${#BACKUP_SOURCES[@]}
CURRENT=0
FAILED=0

for src in "${BACKUP_SOURCES[@]}"; do
    CURRENT=$((CURRENT+1))
    DIR_NAME=$(basename "$src")
    DEST="$TARGET/$DIR_NAME"
    
    echo -e "${BOLD}[$CURRENT/$TOTAL_DIRS] 备份 $src → $DEST${NC}"
    
    RSYNC_OPTS=(
        -avh
        --progress
        --delete          # 删除目标中多余的文件（镜像）
        --timeout=300
    )
    
    if [[ "$DRY_RUN" == "true" ]]; then
        RSYNC_OPTS+=(--dry-run)
    fi
    
    if rsync "${RSYNC_OPTS[@]}" "${EXCLUDE_ARGS[@]}" "$src/" "$DEST/" 2>&1; then
        echo -e "  ${GREEN}✓ $DIR_NAME 备份完成${NC}"
    else
        echo -e "  ${RED}✗ $DIR_NAME 备份失败${NC}"
        FAILED=$((FAILED+1))
    fi
    echo ""
done

# ─── 结果 ───
echo -e "${BOLD}═══ 备份结果 ═══${NC}"
if [[ $FAILED -eq 0 ]]; then
    echo -e "${GREEN}全部成功！$TOTAL_DIRS 个目录已备份到 $TARGET${NC}"
    if [[ "$DRY_RUN" == "true" ]]; then
        echo -e "${YELLOW}(试运行模式，未实际写入)${NC}"
    fi
else
    echo -e "${RED}$FAILED / $TOTAL_DIRS 个目录备份失败${NC}"
    exit 1
fi
