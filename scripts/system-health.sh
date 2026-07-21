#!/bin/bash
# system-health.sh — NAS 系统健康检查
# 一键查看 CPU、内存、磁盘、服务、RAID、SMART 状态
# 用法: sudo ./system-health.sh [--json]

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color
BOLD='\033[1m'

JSON_MODE=false
if [[ "${1:-}" == "--json" ]]; then
    JSON_MODE=true
fi

# Counters
PASS=0
WARN=0
FAIL=0

log_pass() { PASS=$((PASS+1)); echo -e "  ${GREEN}✓${NC} $1"; }
log_warn() { WARN=$((WARN+1)); echo -e "  ${YELLOW}⚠${NC} $1"; }
log_fail() { FAIL=$((FAIL+1)); echo -e "  ${RED}✗${NC} $1"; }

section() {
    echo ""
    echo -e "${BOLD}${BLUE}═══ $1 ═══${NC}"
}

# ─── Hostname & Uptime ───
section "系统信息"
HOSTNAME=$(hostname)
UPTIME=$(uptime -p 2>/dev/null || uptime)
KERNEL=$(uname -r)
OS=$(grep PRETTY_NAME /etc/os-release 2>/dev/null | cut -d'"' -f2 || echo "Unknown")
echo -e "  主机名: ${BOLD}$HOSTNAME${NC}"
echo -e "  系统:   $OS"
echo -e "  内核:   $KERNEL"
echo -e "  运行:   $UPTIME"

# ─── CPU ───
section "CPU"
CPU_MODEL=$(grep "model name" /proc/cpuinfo 2>/dev/null | head -1 | cut -d: -f2 | xargs || echo "Unknown")
CPU_CORES=$(nproc)
LOAD=$(cat /proc/loadavg | awk '{print $1, $2, $3}')
LOAD_1MIN=$(cat /proc/loadavg | awk '{print $1}')
CPU_USAGE=$(top -bn1 | grep "Cpu(s)" | awk '{print $2}' | cut -d'%' -f1 || echo "?")
echo -e "  型号:   $CPU_MODEL"
echo -e "  核心:   $CPU_CORES"
echo -e "  负载:   $LOAD"

# Load check: warn if 1min load > cores
LOAD_HIGH=$(awk -v load="$LOAD_1MIN" -v cores="$CPU_CORES" 'BEGIN{print (load > cores) ? 1 : 0}')
LOAD_MED=$(awk -v load="$LOAD_1MIN" -v cores="$CPU_CORES" 'BEGIN{print (load > cores * 0.8) ? 1 : 0}')
if [[ "$LOAD_HIGH" == "1" ]]; then
    log_fail "1分钟负载 ($LOAD_1MIN) 超过核心数 ($CPU_CORES)"
elif [[ "$LOAD_MED" == "1" ]]; then
    log_warn "1分钟负载 ($LOAD_1MIN) 接近核心数 ($CPU_CORES)"
else
    log_pass "负载正常 ($LOAD_1MIN / $CPU_CORES 核心)"
fi

# ─── Memory ───
section "内存"
# Support both English and Chinese locale (Mem:/内存：)
MEM_LINE=$(free -h | grep -E '^Mem:|^内存：')
MEM_TOTAL=$(echo "$MEM_LINE" | awk '{print $2}')
MEM_USED=$(echo "$MEM_LINE" | awk '{print $3}')
MEM_AVAIL=$(echo "$MEM_LINE" | awk '{print $7}')
MEM_PCT=$(free | grep -E '^Mem:|^内存：' | awk '{printf "%.0f", $3/$2*100}')
SWAP_LINE=$(free -h | grep -E '^Swap:|^交换：')
SWAP_TOTAL=$(echo "$SWAP_LINE" | awk '{print $2}')
SWAP_USED=$(echo "$SWAP_LINE" | awk '{print $3}')
echo -e "  总计:   $MEM_TOTAL"
echo -e "  已用:   $MEM_USED ($MEM_PCT%)"
echo -e "  可用:   $MEM_AVAIL"
echo -e "  Swap:   $SWAP_USED / $SWAP_TOTAL"

if [[ $MEM_PCT -gt 90 ]]; then
    log_fail "内存使用率 ${MEM_PCT}% (>90%)"
elif [[ $MEM_PCT -gt 80 ]]; then
    log_warn "内存使用率 ${MEM_PCT}% (>80%)"
else
    log_pass "内存使用率 ${MEM_PCT}%"
fi

# ─── Disk Usage ───
section "磁盘使用"
# System disk
SYS_USAGE=$(df -h / | awk 'NR==2{print $5}' | tr -d '%')
SYS_AVAIL=$(df -h / | awk 'NR==2{print $4}')
echo -e "  系统盘: ${SYS_USAGE}% 已用, ${SYS_AVAIL} 可用"
if [[ $SYS_USAGE -gt 90 ]]; then
    log_fail "系统盘使用率 ${SYS_USAGE}% (>90%)"
elif [[ $SYS_USAGE -gt 80 ]]; then
    log_warn "系统盘使用率 ${SYS_USAGE}% (>80%)"
else
    log_pass "系统盘使用率 ${SYS_USAGE}%"
fi

# Data mounts
for mp in /data/nas*; do
    if mountpoint -q "$mp" 2>/dev/null; then
        USAGE=$(df -h "$mp" | awk 'NR==2{print $5}' | tr -d '%')
        AVAIL=$(df -h "$mp" | awk 'NR==2{print $4}')
        SIZE=$(df -h "$mp" | awk 'NR==2{print $2}')
        echo -e "  $mp: ${USAGE}% of ${SIZE}, ${AVAIL} 可用"
        if [[ $USAGE -gt 90 ]]; then
            log_fail "$mp 使用率 ${USAGE}% (>90%)"
        elif [[ $USAGE -gt 80 ]]; then
            log_warn "$mp 使用率 ${USAGE}% (>80%)"
        else
            log_pass "$mp 使用率 ${USAGE}%"
        fi
    fi
done

# ─── RAID Status ───
section "RAID 阵列"
if ls /dev/md* &>/dev/null; then
    for md in /dev/md[0-9]*; do
        MD_NAME=$(basename "$md")
        MDSTAT_LINE=$(grep "^$MD_NAME" /proc/mdstat 2>/dev/null || echo "")
        if [[ -n "$MDSTAT_LINE" ]]; then
            STATE=$(echo "$MDSTAT_LINE" | awk '{print $3}')
            LEVEL=$(echo "$MDSTAT_LINE" | grep -oP 'raid\d+' || echo "?")
            DISKS=$(echo "$MDSTAT_LINE" | grep -oP '\[\d+\]' | wc -l || true)
            DISKS=${DISKS:-0}
            FAILED=$(echo "$MDSTAT_LINE" | grep -oP '\(F\)' | wc -l || true)
            FAILED=${FAILED:-0}
            echo -e "  $md: $LEVEL, $DISKS 盘, 状态 $STATE"
            
            if [[ $FAILED -gt 0 ]]; then
                log_fail "$md 有 $FAILED 块盘故障"
            elif echo "$MDSTAT_LINE" | grep -qE 'resync|recovery|reshape'; then
                # Extract progress
                PROG=$(grep -A1 "^$MD_NAME" /proc/mdstat | tail -1 | grep -oP '\d+\.?\d*%' || echo "?")
                log_warn "$md 正在同步/重构 ($PROG)"
            else
                log_pass "$md 正常 ($LEVEL, $DISKS 盘)"
            fi
        fi
    done
else
    echo -e "  ${CYAN}无 RAID 阵列${NC}"
fi

# ─── SMART ───
section "磁盘健康 (SMART)"
SMARTCTL=$(which smartctl 2>/dev/null || echo "/usr/sbin/smartctl")
if [[ ! -x "$SMARTCTL" ]]; then
    echo -e "  ${CYAN}smartctl 未安装，跳过 SMART 检查${NC}"
else
for dev in /dev/sd[a-z] /dev/nvme[0-9]n[0-9] /dev/vd[a-z]; do
    [[ -b "$dev" ]] || continue
    # Skip partitions
    [[ "$dev" =~ p[0-9]+$ ]] && continue
    
    MODEL=$($SMARTCTL -i "$dev" 2>/dev/null | grep "Device Model\|Model Number\|Vendor" | head -1 | cut -d: -f2 | xargs || echo "?")
    SMART=$($SMARTCTL -H "$dev" 2>/dev/null | grep -i "overall-health\|Health Status" | awk -F: '{print $2}' | xargs || echo "N/A")
    TEMP=$($SMARTCTL -A "$dev" 2>/dev/null | grep -i "^194\|Temperature" | head -1 | awk '{print $10}' || echo "?")
    
    if [[ "$SMART" == "PASSED" || "$SMART" == "OK" ]]; then
        log_pass "$dev ($MODEL) ${TEMP}°C"
    elif [[ "$SMART" == "N/A" || -z "$SMART" ]]; then
        echo -e "  ${CYAN}–${NC} $dev ($MODEL) SMART 不可用（虚拟盘？）"
    else
        log_fail "$dev ($MODEL) SMART: $SMART"
    fi
done
fi

# ─── Services ───
section "核心服务"
SERVICES=("smbd" "nmbd" "vsftpd" "nfs-server" "nas-panel" "fail2ban" "cron")
for svc in "${SERVICES[@]}"; do
    if systemctl is-active "$svc" &>/dev/null; then
        log_pass "$svc 运行中"
    elif systemctl is-enabled "$svc" &>/dev/null 2>&1; then
        log_fail "$svc 已启用但未运行"
    else
        echo -e "  ${CYAN}–${NC} $svc 未安装"
    fi
done

# ─── Quota Status ───
section "存储配额"
QUOTA_FOUND=false
for mp in /data/nas*; do
    if mountpoint -q "$mp" 2>/dev/null; then
        if mount | grep "$mp" | grep -q prjquota; then
            QUOTA_COUNT=$(xfs_quota -x -c "report -p -N" "$mp" 2>/dev/null | grep -cv "^#0" || echo 0)
            if [[ $QUOTA_COUNT -gt 0 ]]; then
                echo -e "  $mp: $QUOTA_COUNT 个配额生效"
                QUOTA_FOUND=true
            fi
        fi
    fi
done
if [[ "$QUOTA_FOUND" == "false" ]]; then
    echo -e "  ${CYAN}无配额配置${NC}"
fi

# ─── Network ───
section "网络"
IP_ADDR=$(ip -4 addr show | grep -oP 'inet \K[\d.]+' | grep -v '^127\.' | head -1 || echo "?")
GATEWAY=$(ip route | grep default | awk '{print $3}' || echo "?")
echo -e "  IP:     $IP_ADDR"
echo -e "  网关:   $GATEWAY"

# Check connectivity
if ping -c1 -W2 "$GATEWAY" &>/dev/null; then
    log_pass "网关连通"
else
    log_fail "网关不通"
fi

# ─── Summary ───
echo ""
echo -e "${BOLD}═══ 检查结果 ═══${NC}"
echo -e "  ${GREEN}通过: $PASS${NC}  ${YELLOW}警告: $WARN${NC}  ${RED}失败: $FAIL${NC}"

if [[ $FAIL -gt 0 ]]; then
    echo -e "  ${RED}系统存在 $FAIL 个问题，建议立即处理${NC}"
    exit 1
elif [[ $WARN -gt 0 ]]; then
    echo -e "  ${YELLOW}系统有 $WARN 个警告，建议关注${NC}"
    exit 0
else
    echo -e "  ${GREEN}系统运行正常 ✓${NC}"
    exit 0
fi
