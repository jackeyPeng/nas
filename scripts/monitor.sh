#!/bin/bash
# NAS 监控告警脚本
# 由 cron 每 5 分钟调用: */5 * * * * /opt/nas/scripts/monitor.sh
# 告警通道在 .env 中配置，配哪个启用哪个

set -e

# 加载配置
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="$SCRIPT_DIR/.env"

if [ ! -f "$ENV_FILE" ]; then
    exit 0  # 没有配置文件，静默退出
fi

# 加载 .env 变量
set -a
source "$ENV_FILE"
set +a

# 告警状态文件（防止同一告警反复发送）
ALERT_STATE="/var/lib/nas-monitor/alerts.state"
mkdir -p /var/lib/nas-monitor

# ═══════════════════════════════════════
# 告警发送函数
# ═══════════════════════════════════════

send_alert() {
    local title="$1"
    local msg="$2"

    # 钉钉
    if [ -n "$ALERT_DINGTALK_WEBHOOK" ]; then
        local timestamp=$(date +%s%3N)
        local sign=""
        if [ -n "$ALERT_DINGTALK_SECRET" ]; then
            sign=$(echo -n "$timestamp\n$ALERT_DINGTALK_SECRET" | openssl dgst -sha256 -hmac "$ALERT_DINGTALK_SECRET" -binary | base64 | tr -d '\n')
            sign=$(python3 -c "import urllib.parse; print(urllib.parse.quote('$sign'))" 2>/dev/null || echo "$sign")
        fi
        local url="$ALERT_DINGTALK_WEBHOOK"
        if [ -n "$sign" ]; then
            url="${url}&timestamp=${timestamp}&sign=${sign}"
        fi
        curl -s -X POST "$url" \
            -H "Content-Type: application/json" \
            -d "{\"msgtype\":\"text\",\"text\":{\"content\":\"[NAS] $title\n$msg\"}}" \
            > /dev/null 2>&1 || true
    fi

    # Telegram
    if [ -n "$ALERT_TELEGRAM_TOKEN" ] && [ -n "$ALERT_TELEGRAM_CHAT_ID" ]; then
        curl -s -X POST "https://api.telegram.org/bot${ALERT_TELEGRAM_TOKEN}/sendMessage" \
            -d "chat_id=${ALERT_TELEGRAM_CHAT_ID}" \
            -d "text=[NAS] $title
$msg" \
            > /dev/null 2>&1 || true
    fi

    # Bark
    if [ -n "$ALERT_BARK_KEY" ]; then
        local bark_server="${ALERT_BARK_SERVER:-https://api.day.app}"
        curl -s "${bark_server}/${ALERT_BARK_KEY}/NAS%20告警/$title?body=$(echo "$msg" | python3 -c "import sys,urllib.parse; print(urllib.parse.quote(sys.stdin.read()))" 2>/dev/null || echo "$msg")" \
            > /dev/null 2>&1 || true
    fi

    # Email
    if [ -n "$ALERT_SMTP_HOST" ] && [ -n "$ALERT_SMTP_USER" ]; then
        local subject="[NAS 告警] $title"
        echo "$msg" | python3 -c "
import smtplib, ssl, sys, os
host = os.environ.get('ALERT_SMTP_HOST', '')
port = int(os.environ.get('ALERT_SMTP_PORT', '465'))
user = os.environ.get('ALERT_SMTP_USER', '')
pwd = os.environ.get('ALERT_SMTP_PASS', '')
frm = os.environ.get('ALERT_SMTP_FROM', user)
to = os.environ.get('ALERT_SMTP_TO', user)
body = sys.stdin.read()
msg = f'Subject: {sys.argv[1]}\n\n{body}'
ctx = ssl.create_default_context()
with smtplib.SMTP_SSL(host, port, context=ctx) as s:
    s.login(user, pwd)
    s.sendmail(frm, to, msg.encode('utf-8'))
" "$subject" 2>/dev/null || true
    fi
}

# ═══════════════════════════════════════
# 去重逻辑：同一告警 1 小时内不重复发
# ═══════════════════════════════════════

should_alert() {
    local key="$1"
    local now=$(date +%s)
    local state_file="$ALERT_STATE"

    # 检查上次告警时间
    if [ -f "$state_file" ]; then
        local last=$(grep "^${key}=" "$state_file" 2>/dev/null | cut -d'=' -f2)
        if [ -n "$last" ]; then
            local diff=$((now - last))
            if [ $diff -lt 3600 ]; then
                return 1  # 1 小时内已告警，跳过
            fi
        fi
    fi

    # 记录本次告警
    sed -i "/^${key}=/d" "$state_file" 2>/dev/null || true
    echo "${key}=${now}" >> "$state_file"
    return 0
}

# ═══════════════════════════════════════
# 监控项
# ═══════════════════════════════════════

# 1. 磁盘空间
check_disk() {
    local threshold="${ALERT_DISK_THRESHOLD:-80}"
    local usage=$(df /data 2>/dev/null | awk 'NR==2{gsub(/%/,""); print $5}')
    if [ -z "$usage" ]; then
        usage=$(df / | awk 'NR==2{gsub(/%/,""); print $5}')
    fi
    if [ "$usage" -ge "$threshold" ] 2>/dev/null; then
        if should_alert "disk"; then
            local total=$(df -h /data 2>/dev/null | awk 'NR==2{print $2}' || df -h / | awk 'NR==2{print $2}')
            local used=$(df -h /data 2>/dev/null | awk 'NR==2{print $3}' || df -h / | awk 'NR==2{print $3}')
            send_alert "磁盘空间不足" "使用率 ${usage}% (阈值 ${threshold}%)\n已用: ${used} / 总量: ${total}\n路径: /data"
        fi
    fi
}

# 2. 服务状态
check_services() {
    local services="smbd nmbd nfs-kernel-server vsftpd rclone-webdav filebrowser minio fail2ban"
    local down=""
    for svc in $services; do
        if [ "$(systemctl is-active "$svc" 2>/dev/null)" != "active" ]; then
            down="${down} ${svc}"
        fi
    done
    if [ -n "$down" ]; then
        if should_alert "services"; then
            send_alert "服务异常" "以下服务未运行:${down}"
        fi
    fi
}

# 3. 内存
check_memory() {
    local threshold="${ALERT_MEM_THRESHOLD:-90}"
    local total=$(awk '/MemTotal/{print $2}' /proc/meminfo)
    local avail=$(awk '/MemAvailable/{print $2}' /proc/meminfo)
    local used_pct=$(( (total - avail) * 100 / total ))
    if [ "$used_pct" -ge "$threshold" ] 2>/dev/null; then
        if should_alert "memory"; then
            send_alert "内存不足" "使用率 ${used_pct}% (阈值 ${threshold}%)"
        fi
    fi
}

# 4. CPU 负载
check_load() {
    local threshold="${ALERT_LOAD_THRESHOLD:-4}"
    local load1=$(awk '{print $1}' /proc/loadavg)
    local load_int=$(echo "$load1" | cut -d'.' -f1)
    if [ "$load_int" -ge "$threshold" ] 2>/dev/null; then
        if should_alert "load"; then
            local cores=$(nproc)
            send_alert "CPU 负载过高" "1分钟负载: ${load1} (阈值 ${threshold}, CPU核心: ${cores})"
        fi
    fi
}

# 5. SMART 磁盘健康
check_smart() {
    if ! command -v smartctl &>/dev/null; then
        return
    fi
    for disk in /dev/sd[a-z]; do
        [ -b "$disk" ] || continue
        local health=$(sudo smartctl -H "$disk" 2>/dev/null | grep -i "result" | awk -F: '{print $2}' | xargs)
        if [ -n "$health" ] && [ "$health" != "PASSED" ]; then
            local key="smart_$(basename $disk)"
            if should_alert "$key"; then
                send_alert "磁盘健康异常" "${disk} SMART: ${health}"
            fi
        fi
    done
}

# ═══════════════════════════════════════
# 执行
# ═══════════════════════════════════════

check_disk
check_services
check_memory
check_load
check_smart
