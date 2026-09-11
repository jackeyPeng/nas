#!/usr/bin/env python3
"""生成机器可读的第三方组件清单：third_party/manifest.json + SBOM.spdx.json。

数据来源：
  1. Go 依赖 —— `go list -m all`（直接 + 间接），许可证从模块缓存里的 LICENSE 文件自动识别
  2. 系统包 + 第三方二进制 —— 脚本内维护的清单（与 setup.sh 安装的组件一致）

目的：替代手工维护 THIRD_PARTY_LICENSES.md 造成的漂移（评审报告 §6/7/63/64）。
每次 release 前运行，产物提交进仓库并打进 tarball。
"""
import datetime
import json
import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
WEB = os.path.join(REPO, "web")
OUT_DIR = os.path.join(REPO, "third_party")

# (name, version, license_spdx, type, homepage)
SYSTEM_COMPONENTS = [
    ("samba", "4.x", "GPL-3.0-only", "system-package", "https://www.samba.org"),
    ("nfs-kernel-server", "Debian", "GPL-2.0-only", "system-package", "https://linux-nfs.org"),
    ("vsftpd", "Debian", "GPL-2.0-only", "system-package", "https://security.appspot.com/vsftpd.html"),
    ("fail2ban", "Debian", "GPL-2.0-or-later", "system-package", "https://www.fail2ban.org"),
    ("ufw", "Debian", "GPL-3.0-only", "system-package", "https://launchpad.net/ufw"),
    ("smartmontools", "Debian", "GPL-2.0-or-later", "system-package", "https://www.smartmontools.org"),
    ("unattended-upgrades", "Debian", "GPL-2.0-or-later", "system-package", "https://wiki.debian.org/UnattendedUpgrades"),
    ("apache2-utils", "Debian", "Apache-2.0", "system-package", "https://httpd.apache.org"),
    ("rclone", "1.74.4", "MIT", "third-party-binary", "https://rclone.org"),
    ("FileBrowser", "2.x", "Apache-2.0", "third-party-binary", "https://filebrowser.org"),
    ("Alpine.js", "3.x", "MIT", "third-party-binary", "https://alpinejs.dev"),
]


def get_go_modules():
    # 用 go list -deps（只列实际编译进二进制的模块），离线可用，不拉全量 module graph。
    r = subprocess.run(
        ["go", "list", "-deps", "-f", "{{if .Module}}{{.Module.Path}} {{.Module.Version}}{{end}}", "."],
        cwd=WEB, capture_output=True, text=True,
    )
    if r.returncode != 0:
        print("[WARN] go list 失败: " + r.stderr.strip(), file=sys.stderr)
        return []
    mods = []
    seen = set()
    for line in r.stdout.splitlines():
        parts = line.split()
        if len(parts) == 2 and parts[0] != "nas-panel":
            key = (parts[0], parts[1])
            if key not in seen:
                seen.add(key)
                mods.append(key)
    return mods


def gomod_cache_dir():
    r = subprocess.run(["go", "env", "GOMODCACHE"], capture_output=True, text=True)
    return r.stdout.strip() or os.path.expanduser("~/go/pkg/mod")


def escape_module_path(path):
    # Go 模块缓存把大写字母转义成 !小写
    return "".join("!" + c.lower() if c.isupper() else c for c in path)


def find_license_text(path, version, cache):
    d = os.path.join(cache, escape_module_path(path) + "@" + version)
    for name in ("LICENSE", "LICENSE.txt", "LICENSE.md", "COPYING", "COPYRIGHT", "UNLICENSE"):
        p = os.path.join(d, name)
        if os.path.isfile(p):
            try:
                with open(p, encoding="utf-8", errors="replace") as f:
                    return f.read()
            except OSError:
                return ""
    return ""


def detect_license(text):
    t = text.lower()
    if "apache license" in t:
        return "Apache-2.0"
    if "mit license" in t or ("permission is hereby granted" in t and "free of charge" in t):
        return "MIT"
    if "redistribution and use in source and binary forms" in t:
        if "neither the name" in t or "3-clause" in t:
            return "BSD-3-Clause"
        return "BSD-2-Clause"
    if "mozilla public license" in t:
        return "MPL-2.0"
    if "gnu general public license" in t and "version 3" in t:
        return "GPL-3.0-only"
    if "gnu general public license" in t and "version 2" in t:
        return "GPL-2.0-only"
    if "gnu lesser general public license" in t:
        return "LGPL-3.0-only"
    if "isc license" in t:
        return "ISC"
    if "unlicense" in t:
        return "Unlicense"
    return "NOASSERTION"


def main():
    os.makedirs(OUT_DIR, exist_ok=True)
    cache = gomod_cache_dir()

    components = []
    for path, version in get_go_modules():
        if path == "nas-panel":
            continue  # 主模块，不计入第三方
        text = find_license_text(path, version, cache)
        lic = detect_license(text) if text else "NOASSERTION"
        components.append({
            "name": path,
            "version": version,
            "license": lic,
            "type": "go-dependency",
            "homepage": "https://pkg.go.dev/" + path,
        })

    for name, version, lic, typ, homepage in SYSTEM_COMPONENTS:
        components.append({
            "name": name,
            "version": version,
            "license": lic,
            "type": typ,
            "homepage": homepage,
        })

    components.sort(key=lambda c: (c["type"], c["name"].lower()))

    now = datetime.datetime.now(datetime.timezone.utc)

    # --- manifest.json ---
    manifest = {
        "generated_at": now.isoformat(),
        "project": "Z1 / Abwen NAS (nas-panel)",
        "project_license": "AGPL-3.0-only",
        "components": components,
    }
    with open(os.path.join(OUT_DIR, "manifest.json"), "w", encoding="utf-8") as f:
        json.dump(manifest, f, indent=2, ensure_ascii=False)
        f.write("\n")

    # --- SBOM.spdx.json (SPDX 2.3) ---
    spdx = {
        "spdxVersion": "SPDX-2.3",
        "dataLicense": "CC0-1.0",
        "SPDXID": "SPDXRef-DOCUMENT",
        "name": "Z1-Abwen-NAS",
        "documentNamespace": "https://z1.sale/spdx/" + now.strftime("%Y%m%d-%H%M%S"),
        "creationInfo": {
            "created": now.strftime("%Y-%m-%dT%H:%M:%SZ"),
            "creators": ["Tool: nas-panel scripts/gen-sbom.py", "Organization: Abwen"],
        },
        "packages": [],
        "relationships": [],
    }
    for i, c in enumerate(components):
        spdx["packages"].append({
            "name": c["name"],
            "SPDXID": "SPDXRef-Package-%d" % i,
            "versionInfo": c["version"],
            "downloadLocation": c["homepage"],
            "filesAnalyzed": False,
            "licenseConcluded": c["license"],
            "licenseDeclared": c["license"],
            "copyrightText": "NOASSERTION",
        })
    with open(os.path.join(OUT_DIR, "SBOM.spdx.json"), "w", encoding="utf-8") as f:
        json.dump(spdx, f, indent=2, ensure_ascii=False)
        f.write("\n")

    print("[OK] 生成 %d 个组件 → third_party/manifest.json + SBOM.spdx.json" % len(components))


if __name__ == "__main__":
    main()
