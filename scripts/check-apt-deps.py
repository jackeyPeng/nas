#!/usr/bin/env python3
"""
静态断言：setup.sh / install.sh 的 apt-get install 包列表必须包含
attr（getfattr/setfattr，覆盖写保留 user.* xattr 依赖，见
web/modules/filemgmt/upload.go captureACLXattr/restoreACLXattr）以及
acl（getfacl/setfacl，同一覆盖写流程保留 ACL 依赖）。

用法: python3 scripts/check-apt-deps.py

背景: Point 4 要求——这两个包一旦被误删，覆盖写的 ACL/xattr 保留功能在
全新安装的机器上会直接退化（getfacl/setfacl/getfattr/setfattr 命令缺失，
captureACLXattr/restoreACLXattr 的 runWithSudoFallback 两次尝试都失败），
且只有在真实覆盖写时才会暴露，不会在 `make build`/`go vet` 时报错。
这里用脚本级静态检查代替运行期发现，纳入 make lint-all。

实现：只扫描真正执行批量 `apt-get install` 的那一行（含续行），不依赖
人工维护的包名清单，避免脚本改了但检查脚本忘了同步更新的脱节风险。

两个脚本里都存在多条包含 "apt-get install" 字样的文本（注释、sudoers
白名单字符串、echo 出来的帮助提示、单包 fallback 安装等），这些都不是
"安装全部依赖"的主批量安装命令，不应被当成校验对象。主批量安装命令的
共同特征是带 `-o DPkg::Lock::Timeout=60` 选项（本仓库两个脚本里唯一
用到这个选项的就是各自的主批量安装行），用它来精确定位，而不是匹配
任何包含 "apt-get install" 子串的行。
"""
import re
import sys
import os

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
REQUIRED_PACKAGES = ["attr", "acl"]
TARGETS = ["scripts/setup.sh", "scripts/install.sh"]
# 主批量安装命令的锚点特征：真正装全部依赖的那一行都带这个 apt 选项，
# 注释/sudoers 白名单字符串/echo 帮助文本/单包 fallback 都不带。
ANCHOR = "DPkg::Lock::Timeout=60"


def extract_apt_install_blocks(text):
    """提取每一条主批量 `apt-get install ... \\\n继续 ...` 多行指令的完整文本。

    只匹配同时包含 `apt-get install` 和 ANCHOR 的起始行，排除注释、
    sudoers 白名单字符串、echo 帮助文本、单包 fallback 安装等噪音行。
    命中起始行后，把该行以及后续以反斜杠续行的所有行拼接起来，直到遇到
    不以反斜杠结尾的行为止（指令结束）。
    """
    lines = text.splitlines()
    blocks = []
    i = 0
    while i < len(lines):
        line = lines[i]
        stripped = line.strip()
        is_comment = stripped.startswith("#")
        if "apt-get install" in line and ANCHOR in line and not is_comment:
            block_lines = [line]
            j = i
            while block_lines[-1].rstrip().endswith("\\") and j + 1 < len(lines):
                j += 1
                block_lines.append(lines[j])
            blocks.append("\n".join(block_lines))
            i = j + 1
        else:
            i += 1
    return blocks


def block_package_names(block):
    """从一条 apt-get install 指令块里剥离出纯包名 token 集合。

    去掉命令本身、apt 的 -y/-o .../--xxx 选项、重定向、变量引用等，剩下的
    空格分隔 token 视为包名。足够应对本仓库两个脚本里固定的写法，不追求
    通用 shell 解析。
    """
    # 去掉续行符与多余空白，单行化。
    flat = block.replace("\\\n", " ")
    # 只取 apt-get install 之后的部分。
    idx = flat.find("apt-get install")
    if idx < 0:
        return set()
    tail = flat[idx + len("apt-get install"):]
    # 砍掉行内重定向/管道/逻辑运算之后的内容（包名清单之后可能有
    # `> log 2>&1 || ...` 这类尾巴）。
    tail = re.split(r">|\|\||;", tail)[0]
    tokens = tail.split()
    names = set()
    for tok in tokens:
        if tok.startswith("-"):
            continue
        if "=" in tok and tok.split("=", 1)[0].replace("-", "").isalpha() is False:
            # -o DPkg::Lock::Timeout=60 已被上面 "-" 分支挡掉；这里兜底跳过
            # 任何形如 key=val 的非包名 token（apt 选项值误拼进包名清单的
            # 防御性处理，当前两个脚本不触发，留作健壮性）。
            continue
        names.add(tok)
    return names


def check_file(rel_path):
    path = os.path.join(ROOT, rel_path)
    if not os.path.exists(path):
        return [f"{rel_path}: 文件不存在"]
    with open(path, "r", encoding="utf-8") as f:
        text = f.read()
    blocks = extract_apt_install_blocks(text)
    if not blocks:
        return [f"{rel_path}: 未找到任何 apt-get install 指令，无法校验 attr/acl 依赖"]
    errors = []
    for block in blocks:
        names = block_package_names(block)
        missing = [pkg for pkg in REQUIRED_PACKAGES if pkg not in names]
        if missing:
            first_line = block.splitlines()[0].strip()
            errors.append(
                f"{rel_path}: apt-get install 指令缺少必需包 {missing} "
                f"(起始行: {first_line!r})"
            )
    return errors


def main():
    all_errors = []
    for rel_path in TARGETS:
        all_errors.extend(check_file(rel_path))
    if all_errors:
        print("[check-apt-deps] 失败：")
        for e in all_errors:
            print(f"  - {e}")
        print(
            "\n覆盖写保留 ACL/xattr 功能 (web/modules/filemgmt/upload.go "
            "captureACLXattr/restoreACLXattr) 依赖 getfacl/setfacl(acl 包)与 "
            "getfattr/setfattr(attr 包)，全新安装的机器上缺这两个包会导致该功能"
            "静默退化（fail-closed 报错，但功能不可用）。请在对应脚本的 "
            "apt-get install 包列表里加回 attr acl。"
        )
        sys.exit(1)
    print("[check-apt-deps] 通过：setup.sh / install.sh 均包含 attr acl")


if __name__ == "__main__":
    main()
