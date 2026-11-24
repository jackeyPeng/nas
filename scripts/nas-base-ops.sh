#!/bin/bash
# NAS 基座服务（M2）- PostgreSQL / Mosquitto 特权操作收口脚本
# 用法: sudo nas-base-ops.sh pg-create-db <dbname> <rolename>
#       sudo nas-base-ops.sh pg-set-password <rolename>   (stdin: password)
#       sudo nas-base-ops.sh pg-drop-db <dbname> <rolename>
#       sudo nas-base-ops.sh pg-isready
#       sudo nas-base-ops.sh mqtt-set-password <username>  (stdin: password)
#       sudo nas-base-ops.sh mqtt-delete-user <username>
#       sudo nas-base-ops.sh mqtt-acl-add <username> <topic_prefix>
#       sudo nas-base-ops.sh mqtt-acl-remove <username>
#
# 收口动机（与 nas-app-provision.sh/set-user-pass.sh 同一模式）：面板
# 需要以 postgres 系统用户身份执行 psql（sudo -u postgres psql -c "..."）
# 与改写 /etc/mosquitto/passwd、/etc/mosquitto/nas-acl.conf 两类特权
# 操作。裸 "sudo -u postgres psql -c *" 通配 = 任意 SQL（可执行
# DROP TABLE pg_shadow 等），裸 mosquitto_passwd/改写 acl 文件同样不能
# 以无约束通配进 sudoers。本脚本整体进白名单，内部只接受固定形状的
# 子命令 + 严格校验过的标识符参数，拼出的 SQL 只有 CREATE DATABASE/
# CREATE ROLE/ALTER ROLE PASSWORD/GRANT/DROP DATABASE/DROP ROLE 六种
# 固定模板，不接受调用方传入任意 SQL 片段。
#
# 标识符不变式：dbname/rolename/username 恒须匹配 ^nasapp_[a-z0-9_]{1,32}$
# （与 base 模块 appPgsqlNames/appMQTTUsername 的 "nasapp_<id>" 命名
# 固定前缀一致，<id> 中的 "-" 已被面板侧替换为 "_"），不满足立即 exit 1，
# 不执行任何操作——防止注入 SQL 关键字/路径穿越字符。密码经 stdin 传递
# 并用单引号转义（替换 ' 为 ''，Postgres 标准转义），不经 shell 参数/
# 环境变量，避免密码出现在 ps/日志里。

set -euo pipefail

IDENT_RE='^nasapp_[a-z0-9_]{1,32}$'
# 应用主题前缀：nas/apps/<id>/；面板事件桥专用：nas/events/（APP-SPEC §5.6
# 核心系统事件命名空间，面板只发布不订阅；桥账号用户名 nasapp_panel_events
# 由 check_ident 覆盖，前缀白名单在此收紧）。
TOPIC_PREFIX_RE='^(nas/apps/[a-z0-9-]{1,32}/|nas/events/)$'
MOSQUITTO_PASSWD_FILE="/etc/mosquitto/nas-passwd"
MOSQUITTO_ACL_FILE="/etc/mosquitto/nas-acl.conf"

usage() {
    echo "用法: sudo $0 pg-create-db <dbname> <rolename>" >&2
    echo "      sudo $0 pg-set-password <rolename>   (stdin: password)" >&2
    echo "      sudo $0 pg-drop-db <dbname> <rolename>" >&2
    echo "      sudo $0 pg-isready" >&2
    echo "      sudo $0 mqtt-set-password <username>  (stdin: password)" >&2
    echo "      sudo $0 mqtt-delete-user <username>" >&2
    echo "      sudo $0 mqtt-acl-add <username> <topic_prefix>" >&2
    echo "      sudo $0 mqtt-acl-remove <username>" >&2
    exit 1
}

check_ident() {
    local ident="$1"
    if ! [[ "$ident" =~ $IDENT_RE ]]; then
        echo "错误: 标识符 \"$ident\" 格式非法（必须匹配 ^nasapp_[a-z0-9_]{1,32}\$）" >&2
        exit 1
    fi
}

check_topic_prefix() {
    local prefix="$1"
    if ! [[ "$prefix" =~ $TOPIC_PREFIX_RE ]]; then
        echo "错误: 主题前缀 \"$prefix\" 格式非法（必须匹配 ^nas/apps/[a-z0-9-]{1,32}/\$）" >&2
        exit 1
    fi
}

# pg_escape_literal 把单引号转义为两个单引号（Postgres 标准字符串字面量转义），
# 用于把 stdin 读到的密码安全地拼进 ALTER ROLE ... PASSWORD '<pw>' 语句。
# 标识符（库名/角色名）不走这个函数——标识符已经过 check_ident 的白名单
# 正则校验（只含 [a-z0-9_]），不需要也不应该转义。
pg_escape_literal() {
    printf '%s' "${1//\'/\'\'}"
}

if [ $# -lt 1 ]; then
    usage
fi

SUBCOMMAND="$1"
shift

case "$SUBCOMMAND" in
    pg-create-db)
        [ $# -eq 2 ] || usage
        DBNAME="$1"
        ROLENAME="$2"
        check_ident "$DBNAME"
        check_ident "$ROLENAME"
        # 固定形状 1/4：CREATE ROLE（NOLOGIN 占位，真实登录密码由
        # pg-set-password 紧接着设置，分两步是因为 CREATE ROLE 不接受
        # 从 stdin 读密码，必须内嵌在 -c 的 SQL 文本里——这里先建一个
        # 无密码（不可登录）的角色，第二步再设密码+启用登录）。
        sudo -u postgres psql -c "CREATE ROLE ${ROLENAME} WITH NOLOGIN;" >/dev/null 2>&1 || true
        # 固定形状 2/4：CREATE DATABASE ... OWNER ...
        sudo -u postgres psql -c "CREATE DATABASE ${DBNAME} OWNER ${ROLENAME};" >/dev/null 2>&1 || true
        # 固定形状：GRANT ALL PRIVILEGES（db 创建时一并授权，不单列子命令）
        sudo -u postgres psql -c "GRANT ALL PRIVILEGES ON DATABASE ${DBNAME} TO ${ROLENAME};"
        ;;

    pg-set-password)
        [ $# -eq 1 ] || usage
        ROLENAME="$1"
        check_ident "$ROLENAME"
        PASSWORD="$(cat)"
        if [ -z "$PASSWORD" ]; then
            echo "错误: 密码不能为空" >&2
            exit 1
        fi
        ESCAPED="$(pg_escape_literal "$PASSWORD")"
        # 固定形状 3/4：ALTER ROLE ... WITH LOGIN PASSWORD '...'
        sudo -u postgres psql -c "ALTER ROLE ${ROLENAME} WITH LOGIN PASSWORD '${ESCAPED}';"
        ;;

    pg-drop-db)
        [ $# -eq 2 ] || usage
        DBNAME="$1"
        ROLENAME="$2"
        check_ident "$DBNAME"
        check_ident "$ROLENAME"
        # 固定形状 4/4：DROP DATABASE / DROP ROLE（卸载清理用，忽略
        # "不存在"错误——幂等卸载，重复调用不应报错中止）。
        sudo -u postgres psql -c "DROP DATABASE IF EXISTS ${DBNAME};"
        sudo -u postgres psql -c "DROP ROLE IF EXISTS ${ROLENAME};"
        ;;

    pg-isready)
        [ $# -eq 0 ] || usage
        exec pg_isready -h 127.0.0.1 -p 5432
        ;;

    mqtt-set-password)
        [ $# -eq 1 ] || usage
        USERNAME="$1"
        check_ident "$USERNAME"
        PASSWORD="$(cat)"
        if [ -z "$PASSWORD" ]; then
            echo "错误: 密码不能为空" >&2
            exit 1
        fi
        touch "$MOSQUITTO_PASSWD_FILE"
        chmod 600 "$MOSQUITTO_PASSWD_FILE"
        if [ -s "$MOSQUITTO_PASSWD_FILE" ] && grep -q "^${USERNAME}:" "$MOSQUITTO_PASSWD_FILE"; then
            mosquitto_passwd -b "$MOSQUITTO_PASSWD_FILE" "$USERNAME" "$PASSWORD"
        else
            mosquitto_passwd -c -b "$MOSQUITTO_PASSWD_FILE" "$USERNAME" "$PASSWORD"
        fi
        # Mosquitto 不热重载 passwd/acl 文件——写入后必须 SIGHUP 重读，
        # 否则新凭据在 broker 重启前一直 not authorised（真机实锤：.57
        # 桥申领成功但连接被拒，直到手工 restart mosquitto 才通）。
        # broker 未运行时跳过（首次安装场景，启动时自然读到新文件）。
        if systemctl is-active --quiet mosquitto 2>/dev/null; then
            systemctl kill -s HUP mosquitto || systemctl restart mosquitto
        fi
        ;;

    mqtt-delete-user)
        [ $# -eq 1 ] || usage
        USERNAME="$1"
        check_ident "$USERNAME"
        if [ -f "$MOSQUITTO_PASSWD_FILE" ]; then
            mosquitto_passwd -D "$MOSQUITTO_PASSWD_FILE" "$USERNAME" || true
        fi
        ;;

    mqtt-acl-add)
        [ $# -eq 2 ] || usage
        USERNAME="$1"
        TOPIC_PREFIX="$2"
        check_ident "$USERNAME"
        check_topic_prefix "$TOPIC_PREFIX"
        touch "$MOSQUITTO_ACL_FILE"
        chmod 644 "$MOSQUITTO_ACL_FILE"
        # 幂等：先删掉该用户可能已存在的旧 acl 段，再追加新段。
        if grep -q "^user ${USERNAME}\$" "$MOSQUITTO_ACL_FILE" 2>/dev/null; then
            TMP="$(mktemp)"
            awk -v u="user ${USERNAME}" '
                $0==u {skip=1; next}
                skip && /^user / {skip=0}
                skip && /^topic / {next}
                {print}
            ' "$MOSQUITTO_ACL_FILE" > "$TMP"
            mv "$TMP" "$MOSQUITTO_ACL_FILE"
            chmod 644 "$MOSQUITTO_ACL_FILE"
        fi
        {
            echo "user ${USERNAME}"
            echo "topic readwrite ${TOPIC_PREFIX}#"
        } >> "$MOSQUITTO_ACL_FILE"
        # 同 mqtt-set-password：acl 文件同样不热重载，SIGHUP 重读。
        if systemctl is-active --quiet mosquitto 2>/dev/null; then
            systemctl kill -s HUP mosquitto || systemctl restart mosquitto
        fi
        ;;

    mqtt-acl-remove)
        [ $# -eq 1 ] || usage
        USERNAME="$1"
        check_ident "$USERNAME"
        if [ -f "$MOSQUITTO_ACL_FILE" ] && grep -q "^user ${USERNAME}\$" "$MOSQUITTO_ACL_FILE"; then
            TMP="$(mktemp)"
            awk -v u="user ${USERNAME}" '
                $0==u {skip=1; next}
                skip && /^user / {skip=0}
                skip && /^topic / {next}
                {print}
            ' "$MOSQUITTO_ACL_FILE" > "$TMP"
            mv "$TMP" "$MOSQUITTO_ACL_FILE"
            chmod 644 "$MOSQUITTO_ACL_FILE"
        fi
        ;;

    *)
        echo "错误: 未知子命令 \"$SUBCOMMAND\"" >&2
        usage
        ;;
esac
