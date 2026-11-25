#!/bin/bash
# NAS 应用平台 - 应用目录供给/属主/清理收口脚本
# 用法: sudo nas-app-provision.sh provision <id> <nas_user>
#       sudo nas-app-provision.sh chown-app <id> <path>
#       sudo nas-app-provision.sh remove <id> <keepdata true|false>
#
# 收口动机（M1-6b 真机实测缺陷修复，.48 集成验收）：面板以 fm（非 root）
# 运行后，/var/lib 下建目录（mkdir /var/lib/nas/apps/<id>）与把目录
# chown 给面板自身用户（程序目录）或应用系统用户 nasapp-<id>（数据目录）
# 两类操作 fm 均无权限直接执行（实测报 "mkdir /var/lib/nas: permission
# denied"）。与 set-user-pass.sh（S1-R3b）同一收口模式：脚本整体进
# sudoers 白名单，裸 mkdir -p/chown 不进白名单（裸 chown *:* 任意路径=
# root-equivalent，可把 /etc/shadow 等任意文件 chown 给攻击者可控用户）。
# 本脚本内部对 id/path/用户名做形状校验通过才执行真实特权操作。
#
# 路径不变式（本脚本只认这两个固定前缀，不接受任意路径）：
#   /var/lib/nas/apps/<id>       程序目录（provision 建，remove 删；
#                                 属主 <nas_user>:<nas_user> 0755 ——
#                                 面板进程本身以 <nas_user>（默认 fm）
#                                 身份运行，需要直接写程序文件（app.Files）
#                                 进这个目录，故不能是 root:root；运行时
#                                 应用用户对程序目录的访问由 unit 的
#                                 ProtectSystem=strict 硬化兜底，不依赖
#                                 静态目录属主隔离）
#   /var/lib/nas/apps-data/<id>  数据目录（provision 建，remove 按
#                                 keepdata 删；属主 nasapp-<id>:同名组 0750）
#   /data/*                      manifest 声明的 storage 路径（仅
#                                 chown-app 允许，mkdir 由面板侧
#                                 MkdirAllChown 走 storage 模块既有
#                                 /bin/mkdir -p /data/* 白名单条目完成，
#                                 本脚本只管后续 chown）
# <id> 恒须匹配 ^[a-z0-9-]{2,24}$（与 manifest.go 的 idPattern 一致），
# <nas_user> 恒须匹配 ^[a-z][-a-z0-9_]{0,31}$（与 set-user-pass.sh 的
# USERNAME_RE 一致），均不满足立即 exit 1，不执行任何操作——防止用
# ".."、"/" 等字符注入路径。

set -euo pipefail

ID_RE='^[a-z0-9-]{2,24}$'
USERNAME_RE='^[a-z][-a-z0-9_]{0,31}$'
APPS_BASE="/var/lib/nas/apps"
APPS_DATA_BASE="/var/lib/nas/apps-data"

usage() {
    echo "用法: sudo $0 provision <id> <nas_user>" >&2
    echo "      sudo $0 chown-app <id> <path>" >&2
    echo "      sudo $0 remove <id> <keepdata true|false>" >&2
    echo "      sudo $0 mvapp <from> <to>" >&2
    echo "      sudo $0 rmapp <name>" >&2
    exit 1
}

# APP_NAME_RE：mvapp/rmapp 的目录名约束。允许 a-z0-9 起头 + 字母数字点下划线
# 连字符（升级备份目录形如 nas-photos.bak-1732...-ab12）；首字符限 [a-z0-9]
# 天然排除 "." 与 ".."（穿越），且整名不含斜杠 → 路径恒在 APPS_BASE 内。
APP_NAME_RE='^[a-z0-9][a-z0-9._-]{0,80}$'

check_appname() {
    local name="$1"
    if ! [[ "$name" =~ $APP_NAME_RE ]]; then
        echo "错误: 应用目录名 \"$name\" 格式非法（必须匹配 $APP_NAME_RE）" >&2
        exit 1
    fi
}

check_id() {
    local id="$1"
    if ! [[ "$id" =~ $ID_RE ]]; then
        echo "错误: id \"$id\" 格式非法（必须匹配 ^[a-z0-9-]{2,24}\$）" >&2
        exit 1
    fi
}

check_username() {
    local username="$1"
    if ! [[ "$username" =~ $USERNAME_RE ]]; then
        echo "错误: 用户名 \"$username\" 格式非法（必须匹配 ^[a-z][-a-z0-9_]{0,31}\$）" >&2
        exit 1
    fi
}

if [ $# -lt 1 ]; then
    usage
fi

SUBCOMMAND="$1"
shift

case "$SUBCOMMAND" in
    provision)
        [ $# -eq 2 ] || usage
        ID="$1"
        NAS_USER="$2"
        check_id "$ID"
        check_username "$NAS_USER"

        USERNAME="nasapp-$ID"
        if ! id -u "$USERNAME" >/dev/null 2>&1; then
            echo "错误: 应用系统用户 \"$USERNAME\" 不存在（安装顺序应先 useradd 再 provision）" >&2
            exit 1
        fi
        if ! id -u "$NAS_USER" >/dev/null 2>&1; then
            echo "错误: 面板用户 \"$NAS_USER\" 不存在" >&2
            exit 1
        fi

        PROGRAM_DIR="$APPS_BASE/$ID"
        DATA_DIR="$APPS_DATA_BASE/$ID"

        # 程序目录：<nas_user>:<nas_user> 0755——面板进程本身以
        # nas_user（默认 fm）身份直接写程序文件（app.Files），不能是
        # root:root（面板无法再写入自己不拥有的目录）。运行时应用用户
        # 对该目录的隔离由 unit 的 ProtectSystem=strict 硬化兜底。
        # mkdir -p 不带 -m（SC2174：-p 下 -m 只作用于最深一级，父链权限
        # 不可控），改为建完后显式 chmod 目标目录本身，语义明确不依赖 umask。
        mkdir -p "$PROGRAM_DIR"
        chown "$NAS_USER:$NAS_USER" "$PROGRAM_DIR"
        chmod 0755 "$PROGRAM_DIR"

        # 数据目录：chown 给应用用户，仅应用用户可写。
        mkdir -p "$DATA_DIR"
        chown "$USERNAME:$USERNAME" "$DATA_DIR"
        chmod 0750 "$DATA_DIR"
        ;;

    chown-app)
        [ $# -eq 2 ] || usage
        ID="$1"
        TARGET="$2"
        check_id "$ID"

        USERNAME="nasapp-$ID"
        if ! id -u "$USERNAME" >/dev/null 2>&1; then
            echo "错误: 应用系统用户 \"$USERNAME\" 不存在" >&2
            exit 1
        fi

        case "$TARGET" in
            "$APPS_DATA_BASE"/*|/data/*) ;;
            *)
                echo "错误: 路径 \"$TARGET\" 不在允许的 chown 前缀内（仅允许 $APPS_DATA_BASE/ 或 /data/）" >&2
                exit 1
                ;;
        esac
        if [ ! -d "$TARGET" ]; then
            echo "错误: 路径 \"$TARGET\" 不存在或不是目录" >&2
            exit 1
        fi

        chown "$USERNAME:$USERNAME" "$TARGET"
        ;;

    remove)
        [ $# -eq 2 ] || usage
        ID="$1"
        KEEPDATA="$2"
        check_id "$ID"
        case "$KEEPDATA" in
            true|false) ;;
            *)
                echo "错误: keepdata 参数必须是 true 或 false，got \"$KEEPDATA\"" >&2
                exit 1
                ;;
        esac

        PROGRAM_DIR="$APPS_BASE/$ID"
        DATA_DIR="$APPS_DATA_BASE/$ID"

        rm -rf "$PROGRAM_DIR"
        if [ "$KEEPDATA" = "false" ]; then
            rm -rf "$DATA_DIR"
        fi
        ;;

    # mvapp/rmapp：升级流程的程序目录搬移/删除（Go 侧 defaultExecutor.Rename/
    # RemoveAll 对 /var/lib/nas/apps/ 下路径自动路由到这里——APPS_BASE 属主是
    # root，面板进程（fm）对其内的目录无 rename/unlink 权限；remove 子命令的
    # rm -rf 同理只能由 root 做）。名字经 check_appname 白名单校验，路径恒在
    # APPS_BASE 内，无 root-equivalent 风险（sudoers 白名单形状不变）。
    mvapp)
        [ $# -eq 2 ] || usage
        FROM="$1"; TO="$2"
        check_appname "$FROM"; check_appname "$TO"
        if [ "$FROM" = "$TO" ]; then
            echo "错误: mvapp 源与目标相同" >&2
            exit 1
        fi
        if [ ! -d "$APPS_BASE/$FROM" ]; then
            echo "错误: 源目录 $APPS_BASE/$FROM 不存在" >&2
            exit 1
        fi
        if [ -e "$APPS_BASE/$TO" ]; then
            echo "错误: 目标 $APPS_BASE/$TO 已存在" >&2
            exit 1
        fi
        mv "$APPS_BASE/$FROM" "$APPS_BASE/$TO"
        ;;

    rmapp)
        [ $# -eq 1 ] || usage
        NAME="$1"
        check_appname "$NAME"
        rm -rf "$APPS_BASE/$NAME"
        ;;

    *)
        echo "错误: 未知子命令 \"$SUBCOMMAND\"" >&2
        usage
        ;;
esac
