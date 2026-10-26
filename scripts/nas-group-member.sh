#!/bin/bash
# NAS 用户管理脚本 - 组成员管理
# 用法: sudo ./nas-group-member.sh set|add|del <组名> [成员...]
#   set  <组名> [成员...]   替换式设置组成员（成员列表可为空=清空组）
#   add  <组名> <成员>      添加单个成员
#   del  <组名> <成员>      删除单个成员
#
# 收口动机（S1-R3b 实证修复）：裸 gpasswd -a/-M 在白名单里组名无约束，
# fm 可以 `sudo gpasswd -a fm sudo` 把自己加进 sudo 组，完整 root 提权——
# root-equivalent 后门。本脚本整体进白名单替代三条裸 gpasswd 条目，内部
# 拒绝特权组黑名单后才 exec 真正的 gpasswd。
#
# 面板侧已有 Go 层 isManagedGroup（GID 1000-59999 过滤）作为第一道防线
# （web/modules/users/groups.go），本脚本是第二道（纵深防御——任何持有
# fm shell+sudo 的人可以跳过面板直接调用本脚本，脚本自身必须独立校验，
# 不能只依赖 Go 层）。
#
# 面板侧调用点：web/modules/users/groups.go:setGroupMembers，经
# common.SudoExec("/opt/nas/scripts/nas-group-member.sh", "set", groupName, 成员...)
# 和 .../"add"/groupName/成员 两种形状调用（原裸 gpasswd -M ""/-a 调用形状）。

set -euo pipefail

DENY_GROUPS=(root sudo wheel admin shadow disk adm)
GROUPNAME_RE='^[a-z][-a-z0-9_]{0,31}$'

if [ $# -lt 2 ]; then
    echo "用法: sudo $0 set|add|del <组名> [成员...]" >&2
    exit 1
fi

ACTION="$1"
GROUPNAME="$2"
shift 2
MEMBERS=("$@")

if ! [[ "$GROUPNAME" =~ $GROUPNAME_RE ]]; then
    echo "错误: 组名 \"$GROUPNAME\" 格式非法（只能小写字母开头+小写字母数字下划线短横线，<=32 字符）" >&2
    exit 1
fi

for deny in "${DENY_GROUPS[@]}"; do
    if [ "$GROUPNAME" = "$deny" ]; then
        echo "错误: 禁止操作特权组 \"$GROUPNAME\"" >&2
        exit 1
    fi
done

for m in "${MEMBERS[@]}"; do
    if ! [[ "$m" =~ $GROUPNAME_RE ]]; then
        echo "错误: 成员名 \"$m\" 格式非法" >&2
        exit 1
    fi
done

case "$ACTION" in
    set)
        # gpasswd -M 接受逗号分隔成员列表，空列表=清空组（与 groups.go
        # 原先 gpasswd -M "" groupName 的清空语义一致）
        members_csv=""
        for m in "${MEMBERS[@]}"; do
            if [ -z "$members_csv" ]; then
                members_csv="$m"
            else
                members_csv="$members_csv,$m"
            fi
        done
        exec gpasswd -M "$members_csv" "$GROUPNAME"
        ;;
    add)
        if [ "${#MEMBERS[@]}" -ne 1 ]; then
            echo "错误: add 操作需要且只需要一个成员参数" >&2
            exit 1
        fi
        exec gpasswd -a "${MEMBERS[0]}" "$GROUPNAME"
        ;;
    del)
        if [ "${#MEMBERS[@]}" -ne 1 ]; then
            echo "错误: del 操作需要且只需要一个成员参数" >&2
            exit 1
        fi
        exec gpasswd -d "${MEMBERS[0]}" "$GROUPNAME"
        ;;
    *)
        echo "错误: 未知操作 \"$ACTION\"（支持 set|add|del）" >&2
        exit 1
        ;;
esac
