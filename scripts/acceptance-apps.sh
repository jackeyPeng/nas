#!/bin/bash
# acceptance-apps.sh — 托管应用平台（APP-SPEC v0.1）真机验收脚本
#
# 固化本轮真机验证过的 apps API 全链路断言：登录 → 清残留 → 安装 →
# 确认 → 健康检查 → SSO 头剥离 → 停/启 → 卸载保留/不保留数据。
#
# 用法:
#   bash scripts/acceptance-apps.sh <面板地址> <用户名> <密码文件> <hello包.tar.gz> <hello包.sig>
#
# 参数:
#   <面板地址>      如 http://127.0.0.1:8090（不带尾部斜杠）
#   <用户名>        面板登录用户名
#   <密码文件>      存登录密码的文件路径（避免密码进 shell 命令历史/ps）
#   <hello包.tar.gz> scripts/mkapp.sh 打包的 hello-app 应用包
#   <hello包.sig>    对应的 ed25519 detached 签名文件
#
# 依赖: curl、python3；本脚本设计为在面板所在机器本机运行（systemctl/
# 文件系统断言直接查本机状态，不经 ssh）。
#
# 退出码: 全部断言通过 → 0；任意 [FAIL] → 1。
set -uo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

PASS=0
FAIL=0

ok() {
    PASS=$((PASS + 1))
    echo -e "  ${GREEN}[ok]${NC} $*"
}
fail() {
    FAIL=$((FAIL + 1))
    echo -e "  ${RED}[FAIL]${NC} $*"
}
info() { echo -e "${BLUE}[INFO]${NC} $*"; }

if [ $# -ne 5 ]; then
    echo "用法: bash scripts/acceptance-apps.sh <面板地址> <用户名> <密码文件> <hello包.tar.gz> <hello包.sig>"
    exit 1
fi

PANEL="$1"
NAS_USERNAME="$2"
PASS_FILE="$3"
PKG_TARBALL="$4"
PKG_SIG="$5"

[ -f "$PASS_FILE" ] || {
    echo "密码文件不存在: $PASS_FILE"
    exit 1
}
[ -f "$PKG_TARBALL" ] || {
    echo "应用包不存在: $PKG_TARBALL"
    exit 1
}
[ -f "$PKG_SIG" ] || {
    echo "签名文件不存在: $PKG_SIG"
    exit 1
}

command -v curl >/dev/null 2>&1 || {
    echo "需要 curl"
    exit 1
}
command -v python3 >/dev/null 2>&1 || {
    echo "需要 python3"
    exit 1
}

NAS_PASSWORD="$(cat "$PASS_FILE")"
APP_ID="hello"
DATA_DIR="/var/lib/nas/apps-data/$APP_ID"
UNIT="nas-app-$APP_ID.service"

# ─────────────────────────────────────────────────────────────
# 1. 登录拿 token（失败即终止：后续全部断言依赖有效会话）
# ─────────────────────────────────────────────────────────────
info "1. 登录 $PANEL ..."
LOGIN_RESP="$(curl -sS -X POST "$PANEL/api/login" \
    --data-urlencode "username=$NAS_USERNAME" \
    --data-urlencode "password=$NAS_PASSWORD")"
TOKEN="$(python3 -c "import json,sys; print(json.loads(sys.argv[1]).get('token',''))" "$LOGIN_RESP" 2>/dev/null)"
if [ -z "$TOKEN" ]; then
    fail "登录失败，终止验收。响应: $LOGIN_RESP"
    echo ""
    echo "PASS=$PASS FAIL=$FAIL"
    exit 1
fi
ok "登录成功，拿到 token"
AUTH_HEADER="Authorization: Bearer $TOKEN"

# ─────────────────────────────────────────────────────────────
# 2. DELETE /api/apps/hello 清残留（幂等：404 或删除成功都算过）
# ─────────────────────────────────────────────────────────────
info "2. 清理残留的 $APP_ID 安装 ..."
DEL_CODE="$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "$PANEL/api/apps/$APP_ID" \
    -H "$AUTH_HEADER" -H 'Content-Type: application/json' -d '{"keepData":false}')"
if [ "$DEL_CODE" = "200" ] || [ "$DEL_CODE" = "404" ]; then
    ok "清残留幂等通过（HTTP $DEL_CODE）"
else
    fail "清残留返回意外状态码: $DEL_CODE"
fi

# install_app: 上传包+base64 signature，返回 install 响应体（pending 阶段）
install_app() {
    local sig_b64
    sig_b64="$(base64 -w0 "$PKG_SIG" 2>/dev/null || base64 "$PKG_SIG")"
    curl -sS -X POST "$PANEL/api/apps/install" \
        -H "$AUTH_HEADER" \
        -F "package=@$PKG_TARBALL;type=application/gzip" \
        -F "signature=$sig_b64"
}

json_get() {
    # json_get <json> <field>
    python3 -c "import json,sys; v=json.loads(sys.argv[1]).get(sys.argv[2]); print(v if v is not None else '')" "$1" "$2" 2>/dev/null
}

# ─────────────────────────────────────────────────────────────
# 3. install（multipart 包+base64 signature）
#    断言：返回 id=hello + status=pending + dataDir 权限清单字段
# ─────────────────────────────────────────────────────────────
info "3. 安装 $APP_ID（上传包）..."
INSTALL_RESP="$(install_app)"
INSTALL_ID="$(json_get "$INSTALL_RESP" id)"
INSTALL_STATUS="$(json_get "$INSTALL_RESP" status)"
if [ "$INSTALL_ID" = "$APP_ID" ]; then
    ok "install 返回 id=$APP_ID"
else
    fail "install 返回 id 不对，got '$INSTALL_ID'，响应: $INSTALL_RESP"
fi
if [ "$INSTALL_STATUS" = "pending" ]; then
    ok "install 返回 status=pending"
else
    fail "install 返回 status 不对，got '$INSTALL_STATUS'"
fi
# dataDir 权限清单字段：storagePaths/db/dataDir 三个字段必须出现在响应体里
HAS_DATADIR_FIELDS="$(python3 -c "
import json, sys
d = json.loads(sys.argv[1])
print('yes' if all(k in d for k in ('storagePaths', 'db', 'dataDir')) else 'no')
" "$INSTALL_RESP" 2>/dev/null)"
if [ "$HAS_DATADIR_FIELDS" = "yes" ]; then
    ok "install 返回体含 dataDir 权限清单字段（storagePaths/db/dataDir）"
else
    fail "install 返回体缺少 dataDir 权限清单字段，响应: $INSTALL_RESP"
fi

# ─────────────────────────────────────────────────────────────
# 4. confirm → 断言 status=installed
# ─────────────────────────────────────────────────────────────
info "4. 确认安装 $APP_ID ..."
CONFIRM_RESP="$(curl -sS -X POST "$PANEL/api/apps/install/$APP_ID/confirm" -H "$AUTH_HEADER")"
CONFIRM_STATUS="$(json_get "$CONFIRM_RESP" status)"
if [ "$CONFIRM_STATUS" = "installed" ]; then
    ok "confirm 返回 status=installed"
else
    fail "confirm 返回 status 不对，got '$CONFIRM_STATUS'，响应: $CONFIRM_RESP"
fi

# ─────────────────────────────────────────────────────────────
# 5. sleep 5 → systemctl is-active nas-app-hello == active
#    （非致命：真机本地 systemctl 检查失败不中止，仅计 FAIL）
# ─────────────────────────────────────────────────────────────
info "5. 等待服务启动 ..."
sleep 5
if command -v systemctl >/dev/null 2>&1; then
    ACTIVE_STATE="$(systemctl is-active "$UNIT" 2>/dev/null || true)"
    if [ "$ACTIVE_STATE" = "active" ]; then
        ok "systemctl is-active $UNIT == active"
    else
        fail "systemctl is-active $UNIT == $ACTIVE_STATE（非致命，脚本继续）"
    fi
else
    fail "本机无 systemctl，跳过服务状态断言（非致命）"
fi

# ─────────────────────────────────────────────────────────────
# 6. /apps/hello/healthz 带 Bearer → 200 body ok
# ─────────────────────────────────────────────────────────────
info "6. healthz ..."
HEALTHZ_BODY_FILE="$(mktemp)"
HEALTHZ_CODE="$(curl -sS -o "$HEALTHZ_BODY_FILE" -w '%{http_code}' "$PANEL/apps/$APP_ID/healthz" -H "$AUTH_HEADER")"
HEALTHZ_BODY="$(cat "$HEALTHZ_BODY_FILE")"
rm -f "$HEALTHZ_BODY_FILE"
if [ "$HEALTHZ_CODE" = "200" ] && [ "$HEALTHZ_BODY" = "ok" ]; then
    ok "healthz 200 body=ok"
else
    fail "healthz 期望 200/ok，got $HEALTHZ_CODE/'$HEALTHZ_BODY'"
fi

# ─────────────────────────────────────────────────────────────
# 7. /apps/hello/api/whoami 带 Bearer → JSON user==登录用户 且 valid==true
# ─────────────────────────────────────────────────────────────
info "7. whoami（真实会话）..."
WHOAMI_RESP="$(curl -sS "$PANEL/apps/$APP_ID/api/whoami" -H "$AUTH_HEADER")"
WHOAMI_USER="$(json_get "$WHOAMI_RESP" user)"
WHOAMI_VALID="$(python3 -c "import json,sys; print(json.loads(sys.argv[1]).get('valid'))" "$WHOAMI_RESP" 2>/dev/null)"
if [ "$WHOAMI_USER" = "$NAS_USERNAME" ] && [ "$WHOAMI_VALID" = "True" ]; then
    ok "whoami user==$NAS_USERNAME 且 valid==true"
else
    fail "whoami 期望 user=$NAS_USERNAME valid=true，响应: $WHOAMI_RESP"
fi

# ─────────────────────────────────────────────────────────────
# 8. 伪造头（X-NAS-User: root + x-nas-auth: fake）仍返回 user==登录用户
# ─────────────────────────────────────────────────────────────
info "8. 伪造 SSO 头剥离验证 ..."
FORGED_RESP="$(curl -sS "$PANEL/apps/$APP_ID/api/whoami" \
    -H "$AUTH_HEADER" \
    -H "X-NAS-User: root" \
    -H "x-nas-auth: fake")"
FORGED_USER="$(json_get "$FORGED_RESP" user)"
if [ "$FORGED_USER" = "$NAS_USERNAME" ]; then
    ok "伪造头被剥离，whoami 仍返回真实登录用户 $NAS_USERNAME"
else
    fail "伪造头剥离失败！whoami 返回 user='$FORGED_USER'，期望 $NAS_USERNAME（红线：剥离缺失=返工）"
fi

# ─────────────────────────────────────────────────────────────
# 9. 未登录 whoami → 401
# ─────────────────────────────────────────────────────────────
info "9. 未登录访问 → 401 ..."
UNAUTH_CODE="$(curl -sS -o /dev/null -w '%{http_code}' "$PANEL/apps/$APP_ID/api/whoami")"
if [ "$UNAUTH_CODE" = "401" ]; then
    ok "未登录 whoami 返回 401"
else
    fail "未登录 whoami 期望 401，got $UNAUTH_CODE"
fi

# ─────────────────────────────────────────────────────────────
# 10. stop 单元 → /apps/hello/api/whoami 带 Bearer → 503
# ─────────────────────────────────────────────────────────────
info "10. 停止应用 ..."
STOP_RESP="$(curl -sS -X POST "$PANEL/api/apps/$APP_ID/stop" -H "$AUTH_HEADER")"
STOP_STATUS="$(json_get "$STOP_RESP" status)"
if [ "$STOP_STATUS" = "stopped" ]; then
    ok "stop 返回 status=stopped"
else
    fail "stop 返回 status 不对，got '$STOP_STATUS'，响应: $STOP_RESP"
fi
sleep 1
STOPPED_CODE="$(curl -sS -o /dev/null -w '%{http_code}' "$PANEL/apps/$APP_ID/api/whoami" -H "$AUTH_HEADER")"
if [ "$STOPPED_CODE" = "503" ]; then
    ok "停用后 whoami 返回 503"
else
    fail "停用后 whoami 期望 503，got $STOPPED_CODE"
fi

# ─────────────────────────────────────────────────────────────
# 11. start → healthz 恢复 200
# ─────────────────────────────────────────────────────────────
info "11. 重新启动应用 ..."
curl -sS -X POST "$PANEL/api/apps/$APP_ID/start" -H "$AUTH_HEADER" >/dev/null
sleep 3
RESTART_HEALTHZ_CODE="$(curl -sS -o /dev/null -w '%{http_code}' "$PANEL/apps/$APP_ID/healthz" -H "$AUTH_HEADER")"
if [ "$RESTART_HEALTHZ_CODE" = "200" ]; then
    ok "start 后 healthz 恢复 200"
else
    fail "start 后 healthz 期望 200，got $RESTART_HEALTHZ_CODE"
fi

# ─────────────────────────────────────────────────────────────
# 12. 卸载 keepData=true → apps-data 目录存在
# ─────────────────────────────────────────────────────────────
info "12. 卸载（保留数据）..."
curl -sS -X DELETE "$PANEL/api/apps/$APP_ID" \
    -H "$AUTH_HEADER" -H 'Content-Type: application/json' -d '{"keepData":true}' >/dev/null
if [ -d "$DATA_DIR" ]; then
    ok "keepData=true 卸载后 $DATA_DIR 仍存在"
else
    fail "keepData=true 卸载后 $DATA_DIR 不应被删除（非致命，本机无权限读取时也会误报，脚本继续）"
fi

# ─────────────────────────────────────────────────────────────
# 13. 再装+confirm → data_dir_files==1（数据保留验证，whoami 返回）
# ─────────────────────────────────────────────────────────────
info "13. 重新安装验证数据保留 ..."
REINSTALL_RESP="$(install_app)"
REINSTALL_ID="$(json_get "$REINSTALL_RESP" id)"
if [ "$REINSTALL_ID" = "$APP_ID" ]; then
    ok "重新安装 install 返回 id=$APP_ID"
else
    fail "重新安装 install 返回 id 不对，got '$REINSTALL_ID'"
fi
curl -sS -X POST "$PANEL/api/apps/install/$APP_ID/confirm" -H "$AUTH_HEADER" >/dev/null
sleep 2
REINSTALL_WHOAMI="$(curl -sS "$PANEL/apps/$APP_ID/api/whoami" -H "$AUTH_HEADER")"
DATA_DIR_FILES="$(python3 -c "import json,sys; print(json.loads(sys.argv[1]).get('data_dir_files'))" "$REINSTALL_WHOAMI" 2>/dev/null)"
if [ "$DATA_DIR_FILES" = "1" ]; then
    ok "数据保留验证通过：data_dir_files==1"
else
    fail "数据保留验证失败：期望 data_dir_files==1，got '$DATA_DIR_FILES'，响应: $REINSTALL_WHOAMI"
fi

# ─────────────────────────────────────────────────────────────
# 14. 卸载 keepData=false → apps-data 目录不存在
# ─────────────────────────────────────────────────────────────
info "14. 卸载（不保留数据）..."
curl -sS -X DELETE "$PANEL/api/apps/$APP_ID" \
    -H "$AUTH_HEADER" -H 'Content-Type: application/json' -d '{"keepData":false}' >/dev/null
sleep 1
if [ ! -d "$DATA_DIR" ]; then
    ok "keepData=false 卸载后 $DATA_DIR 已删除"
else
    fail "keepData=false 卸载后 $DATA_DIR 仍存在，应已删除"
fi

# ─────────────────────────────────────────────────────────────
# 汇总
# ─────────────────────────────────────────────────────────────
echo ""
echo "─────────────────────────────────────"
echo "PASS=$PASS FAIL=$FAIL"
if [ "$FAIL" -eq 0 ]; then
    echo -e "${GREEN}全部通过${NC}"
    exit 0
else
    echo -e "${RED}存在失败项${NC}"
    exit 1
fi
