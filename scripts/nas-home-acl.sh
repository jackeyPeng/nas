#!/bin/bash
# NAS 用户管理脚本 - 用户 home 目录 ACL 授权
# 用法: sudo ./nas-home-acl.sh <home_path> [nas_user]
#
# 背景（S1-R3c 实证修复，48 真机强化验收）：面板降权为 NAS_USER（如 fm）运行后，
# 用户 home（0700 alice:alice）对 fm 天然不可读——管理员文件管理器浏览/预览/du
# 统计全部 EACCES，这是 fm 面板下最大的功能回归。
#
# 拍板方案（parent 拍板）：POSIX ACL，不改 home 的 unix 属主/权限位语义（SMB
# valid users 隔离、用户互相之间不可见均不受影响），只追加一条 ACL 授权：
#   - home 目录本身：u:$NAS_USER:rwx（即时生效，fm 可进入/列出/新建）
#   - 默认 ACL：d:u:$NAS_USER:rwx（新建文件/目录自动继承，无需每次重新授权）
#   - 存量内容：递归 -R -m u:$NAS_USER:rX（大写 X：目录与已经可执行的文件给 x，
#     普通文件只给 rw 对应的 r，不会把本来不可执行的文件误开执行位）
#
# 整脚本进 sudoers 白名单（一条，不含通配参数——命令名+位置参数本身已经由脚本
# 内部的路径/身份校验收紧，无需在 sudoers glob 层再约束参数形状）。
#
# 校验：
#   - home_path 必须形如 /data/nas<N>/<单段用户名>（无 "/" 子路径、无 ".."、
#     不能是池根本身），且目录必须已存在（provisioning 由 EnsureUserHome /
#     EnsurePoolStructure 负责，本脚本不创建目录，只补 ACL）
#   - nas_user 可选第二参数覆盖。本脚本以 root 身份跑，不在面板进程环境里，
#     读不到面板侧 common.GetNASUser() 依赖的 systemd Environment=NAS_USER，
#     因此调用方（Go/add-user.sh/setup.sh）必须把 NAS_USER 当第二参数显式
#     传入；未传入时兜底默认值 "fm"（main.go 的 defaultUser，与
#     common.GetNASUser() 的兜底值保持一致），避免裸调用缺参时直接报错退出。

set -euo pipefail

HOME_PATH="${1:-}"
NAS_USER="${2:-}"

if [ -z "$HOME_PATH" ]; then
    echo "用法: sudo $0 <home_path> [nas_user]" >&2
    exit 1
fi

# home_path 形状校验：/data/nas<数字>/<单段用户名>，恰好两段，不允许更深路径、
# 不允许 ".."、不允许以 "/" 结尾造成的空尾段。
if ! [[ "$HOME_PATH" =~ ^/data/nas[0-9]+/[a-zA-Z][a-zA-Z0-9_-]{0,31}$ ]]; then
    echo "错误: home_path \"$HOME_PATH\" 形状非法（必须是 /data/nas<N>/<单段用户名>）" >&2
    exit 1
fi

if [ ! -d "$HOME_PATH" ]; then
    echo "错误: 目录不存在: $HOME_PATH" >&2
    exit 1
fi

# 拒绝符号链接（防止 home 段是指向池外目录的链接，ACL 顺着链接作用到意料外的
# 目标——与 filemgmt/perms.go 对 home 的 Lstat 防御口径一致）。
if [ -L "$HOME_PATH" ]; then
    echo "错误: home_path 不能是符号链接: $HOME_PATH" >&2
    exit 1
fi

if [ -z "$NAS_USER" ]; then
    NAS_USER="fm"
fi

NAS_USER_RE='^[a-z][-a-z0-9_]{0,31}$'
if ! [[ "$NAS_USER" =~ $NAS_USER_RE ]]; then
    echo "错误: nas_user \"$NAS_USER\" 格式非法" >&2
    exit 1
fi

if ! id "$NAS_USER" >/dev/null 2>&1; then
    echo "错误: nas_user \"$NAS_USER\" 不是已存在的系统用户" >&2
    exit 1
fi

# 1. home 目录本身：立即授权 rwx（能进入目录、列目录、在目录内新建）
setfacl -m "u:${NAS_USER}:rwx" "$HOME_PATH"

# 2. 默认 ACL：新建的文件/子目录自动继承同一授权，不需要每次手动补
setfacl -d -m "u:${NAS_USER}:rwx" "$HOME_PATH"

# 3. 存量内容：递归授权。大写 X 只给目录和已经对某人可执行的文件加 x，
# 普通文件不会被误开执行位，同时仍能读写（r/w 由 ACL mode 字符串里的 r/w 决定）。
setfacl -R -m "u:${NAS_USER}:rwX" "$HOME_PATH"

echo "✓ 已为 $HOME_PATH 授予 ${NAS_USER} 的管理 ACL（含默认继承）"
