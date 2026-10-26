#!/bin/bash
# NAS 用户管理脚本 - 设置用户密码
# 用法: sudo ./set-user-pass.sh   (stdin 逐行读取 "username:password")
#
# 收口动机（S1-R3b 实证修复）：裸 /usr/bin/chpasswd 在白名单里是
# root-equivalent 后门——任何持有 fm shell 的人可以
#   echo "root:x" | sudo chpasswd
# 直接改掉 root 密码接管整机。本脚本整体进白名单替代裸 chpasswd 条目，
# 内部对每一行的 username 做形状+身份校验后才 exec 真正的 chpasswd，
# 拒绝目标是 root 或 uid<1000 的既有系统用户（新建户场景用户尚不存在，
# 放行——面板建户流程先 useradd 再设密码）。
#
# 面板侧调用点：web/modules/users/users.go:changePassword、
# web/modules/users/create.go:CreateUserWithPassword，均经
# common.SudoExecStdin(..., "/opt/nas/scripts/set-user-pass.sh") 传入
# "username:password\n" 行，与原 chpasswd stdin 协议形状不变。

set -euo pipefail

USERNAME_RE='^[a-z][-a-z0-9_]{0,31}$'

# 逐行读取 stdin，校验后攒成合法行再统一喂给 chpasswd（避免多次调用
# chpasswd 产生多条审计日志，也避免部分行通过部分行失败时系统状态不一致）
valid_lines=()
line_no=0
while IFS= read -r line || [ -n "$line" ]; do
    line_no=$((line_no + 1))
    [ -z "$line" ] && continue

    username="${line%%:*}"
    password="${line#*:}"

    if [ "$username" = "$line" ]; then
        echo "错误: 第 $line_no 行格式非法（缺少 ':' 分隔符）" >&2
        exit 1
    fi

    if ! [[ "$username" =~ $USERNAME_RE ]]; then
        echo "错误: 第 $line_no 行用户名 \"$username\" 格式非法（只能小写字母开头+小写字母数字下划线短横线，<=32 字符）" >&2
        exit 1
    fi

    if [ "$username" = "root" ]; then
        echo "错误: 禁止通过本脚本设置 root 密码" >&2
        exit 1
    fi

    # 用户已存在时，拒绝 uid<1000 的既有系统用户（daemon/www-data 等）。
    # 用户不存在是建户场景的正常前置（面板先 useradd 再调用本脚本设密码），
    # 此时放行——不存在的用户不可能是系统用户。
    if existing_uid="$(id -u "$username" 2>/dev/null)"; then
        if [ "$existing_uid" -lt 1000 ]; then
            echo "错误: 禁止设置系统用户 \"$username\"（uid=$existing_uid < 1000）的密码" >&2
            exit 1
        fi
    fi

    valid_lines+=("$username:$password")
done

if [ "${#valid_lines[@]}" -eq 0 ]; then
    echo "错误: 未读到任何有效的 username:password 行" >&2
    exit 1
fi

printf '%s\n' "${valid_lines[@]}" | exec chpasswd
