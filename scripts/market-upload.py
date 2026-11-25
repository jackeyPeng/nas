#!/usr/bin/env python3
"""market-upload.py — 手工部署市场索引与包到 Cloudflare R2（get.z1.sale）。

市场是「v1 自托管」（APP-SPEC §7）：索引与包托管在自己的 R2 bucket 上，
本脚本是唯一的发布通道（与 install.sh 的 upload-r2.sh 同一凭据源）。

用法：
  # 上传一个包并合入/创建索引
  python3 scripts/market-upload.py \
      --id hello --name "Hello 演示应用" \
      --tarball /tmp/hello-1.0.0.tar.gz --sig /tmp/hello-1.0.0.tar.gz.sig \
      --summary "一句话简介" --changelog "本版变更" \
      [--min-core 1.5.0] [--index /path/to/index.json]

行为：
  1. 读现有线上索引（无则新建），按 id+version 合入条目（同版本覆盖）
  2. 上传 tarball/sig 到 apps/<id>/<version>/ 下，索引写 apps/index.json
  3. 全部对象 CacheControl: no-cache（面板侧另有 1h 索引缓存）

注意（2026-11-25 实锤）：
  - get.z1sale 绑定的是 **nas** bucket；www-z1-sale 是官网站 bucket，别传错
  - Cloudflare 默认对 404 做约 3 分钟边缘负缓存：上传后立刻 GET 可能仍 404
    （HEAD/带 query 参数可绕过验证），等 3 分钟即真值
"""
import argparse, hashlib, json, os, sys

import boto3  # pip3 install boto3

ENV_KEYS = ("CLOUDFLARE_S3_API", "CLOUDFLARE_KEY_ID", "CLOUDFLARE_SECRET")
BUCKET = "nas"  # get.z1.sale 绑定的 bucket（www-z1-sale 是官网）
INDEX_KEY = "apps/index.json"


def load_env():
    env = {}
    for p in (os.path.expanduser("~/.hermes/.env"),):
        if os.path.exists(p):
            for line in open(p):
                line = line.strip()
                if "=" in line and not line.startswith("#"):
                    k, v = line.split("=", 1)
                    env[k] = v.strip().strip('"')
    missing = [k for k in ENV_KEYS if not env.get(k)]
    if missing:
        sys.exit(f"ERROR: .env 缺少 {missing}（找 ~/.hermes/.env）")
    return env


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--id", required=True)
    ap.add_argument("--name", required=True)
    ap.add_argument("--tarball", required=True)
    ap.add_argument("--sig", required=True)
    ap.add_argument("--summary", default="")
    ap.add_argument("--changelog", default="")
    ap.add_argument("--icon", default="")
    ap.add_argument("--min-core", default="1.5.0")
    ap.add_argument("--index", help="可选：本地预建索引路径（仍会合入本条目）")
    args = ap.parse_args()

    env = load_env()
    s3 = boto3.client(
        "s3",
        endpoint_url=env["CLOUDFLARE_S3_API"],
        aws_access_key_id=env["CLOUDFLARE_KEY_ID"],
        aws_secret_access_key=env["CLOUDFLARE_SECRET"],
        region_name="auto",
    )

    data = open(args.tarball, "rb").read()
    sig = open(args.sig, "rb").read()
    version = os.path.basename(args.tarball).split("-")[-1].removesuffix(".tar.gz")

    index = {"spec": "1", "apps": []}
    try:
        body = s3.get_object(Bucket=BUCKET, Key=INDEX_KEY)["Body"].read()
        index = json.loads(body)
        index.setdefault("apps", [])
    except Exception:
        pass  # 不存在 → 新建
    if args.index and os.path.exists(args.index):
        idx = json.load(open(args.index))
        index.setdefault("spec", idx.get("spec", "1"))
        for app in idx.get("apps", []):
            if app["id"] != args.id:
                index["apps"].append(app)

    base = f"https://get.z1.sale/apps/{args.id}/{version}"
    entry = {
        "version": version,
        "minCoreVersion": args.min_core,
        "tarball": f"{base}/{os.path.basename(args.tarball)}",
        "sha256": hashlib.sha256(data).hexdigest(),
        "sig": f"{base}/{os.path.basename(args.sig)}",
        "changelog": args.changelog,
        "size": len(data),
    }
    app = next((a for a in index["apps"] if a["id"] == args.id), None)
    if app is None:
        app = {"id": args.id, "name": args.name, "icon": args.icon,
               "summary": args.summary, "latest": version, "versions": []}
        index["apps"].append(app)
    app.update({"name": args.name, "icon": args.icon or app.get("icon", ""),
                "summary": args.summary or app.get("summary", "")})
    app["versions"] = [v for v in app.get("versions", []) if v["version"] != version]
    app["versions"].append(entry)
    app["latest"] = max([v["version"] for v in app["versions"]], key=lambda s: [int(x) for x in s.split(".")])

    objs = [
        (INDEX_KEY, json.dumps(index, ensure_ascii=False, indent=2).encode(), "application/json"),
        (f"apps/{args.id}/{version}/{os.path.basename(args.tarball)}", data, "application/gzip"),
        (f"apps/{args.id}/{version}/{os.path.basename(args.sig)}", sig, "text/plain"),
    ]
    for key, body, ct in objs:
        s3.put_object(Bucket=BUCKET, Key=key, Body=body, ContentType=ct, CacheControl="no-cache")
        print("[OK]", key, len(body))
    print("sha256:", entry["sha256"])
    print("提示：Cloudflare 对 404 有 ~3 分钟负缓存，上传后稍等再验证。")


if __name__ == "__main__":
    main()
