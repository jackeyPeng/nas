#!/usr/bin/env python3
"""market-upload.py — 手工部署市场索引与包到 Cloudflare R2（get.z1.sale）。

市场是「v1 自托管」（APP-SPEC §7）：索引与包托管在自己的 R2 bucket 上，
本脚本是唯一的发布通道（与 install.sh 的 upload-r2.sh 同一凭据源）。

用法：
  # 上传一个包并合入/创建索引（--pubkey 必填：验签公钥 base64，见下）
  python3 scripts/market-upload.py \
      --id hello --name "Hello 演示应用" \
      --tarball /tmp/hello-1.0.0.tar.gz --sig /tmp/hello-1.0.0.tar.gz.sig \
      --pubkey <市场正式公钥 base64> \
      --summary "一句话简介" --changelog "本版变更" \
      [--min-core 1.5.0] [--index /path/to/index.json] [--allow-overwrite] \
      [--trusted-pubkey <信任锚 base64，缺省读 ~/.hermes/.env 的 APP_TRUSTED_PUBKEY>]

--pubkey / --trusted-pubkey（信任锚绑定，§1.1）：
  --pubkey 是用于验证 tarball.sig 的 ed25519 公钥（base64 标准编码）。它
  必须与**信任锚**逐字节相等，信任锚即核心 `web/modules/apps.
  TrustedPublicKeyB64` 正式注入的那枚公钥（docs/APP-SPEC.md §1.1），同时
  也是 `scripts/release.sh` 从 `~/.hermes/.env` 的 `APP_TRUSTED_PUBKEY`
  读出去注入 Go 构建的同一个值——构建侧信任谁签的包、发布侧校验签的是谁，
  必须是同一枚公钥，否则发布脚本自检"通过"没有任何意义（任何人自己生成
  一对 ed25519 key 签名、自己传 --pubkey 验证，永远能验证通过，但配置了
  正式信任锚的面板不会认）。

  信任锚来源：显式传 --trusted-pubkey 则直接用它；否则从
  `~/.hermes/.env` 读 `APP_TRUSTED_PUBKEY`（与 load_env() 读取 R2 凭据
  同一文件，与 scripts/release.sh 同一变量名）。两者都没有，或信任锚本身
  不是合法 32 字节 ed25519 公钥 base64 → fail-closed 直接中止，不允许
  "没配信任锚就放行任意 --pubkey"这种退化路径。

  本脚本不持有任何私钥，不代签，只负责"上传前自检 --pubkey 确实是信任锚、
  且签名确实有效"。额外显式拒绝入仓的开发公钥（`web/modules/apps/
  sign.go` 的 `DevPublicKeyB64`，其私钥已公开已知）：即便信任锚检查本身
  已经能把开发公钥挡在外面（开发公钥≠信任锚），仍单独判断一次是为了在
  报错信息里给出更具体的原因（"开发公钥"而不是笼统的"与信任锚不一致"）。

--allow-overwrite / 不可变对象 key（§7）：
  资源对象 key 内嵌内容 sha256（`apps/<id>/<version>/<sha256>/<basename>`），
  不同内容天然落到不同 key，已发布对象永不会被后续发布改写一个字节。
  同一 id+version 已存在于线上索引时默认直接中止；显式传
  --allow-overwrite 才允许"同版本号发新内容"——效果是新内容上传到一个
  新 key，索引条目改为指向新 key，**旧 key 对应的旧对象原样保留在
  bucket 里，不被触碰**。覆盖不是"原地改"，是"发新对象 + 切换索引引用"。

行为（§1.1/§7 对齐）：
  1. 验签：--pubkey 必须与信任锚逐字节相等（含开发公钥显式拒绝）；用
     --pubkey 校验 tarball 与 sig 匹配；解析 tarball 字节内的
     manifest.json 并与 --id/文件名版本做一致性校验（§1 包布局 + §2
     manifest 契约，规则对齐核心 web/modules/apps/package.go 的
     UnpackAppPackage）：
       - basename 必须严格是 <id>-<version>.tar.gz
       - 根目录必须单一且等于 <id>-<version>
       - 恰好一个 manifest.json（根目录正下方），多个/缺失/位置不对都拒绝
       - 拒绝路径穿越（绝对路径/".."片段）与符号链接/硬链接条目
       - manifest.json 不接受未知字段
     任一失败 → 不上传，报错退出。
  2. 获取发布锁（见下）：锁已被占用 → 直接中止，不产生任何上传副作用。
  3. 读现有线上索引：读失败（网络错误、权限错误等，区别于"真的不存在"）
     视为不安全操作，直接中止，绝不用空索引覆盖——避免把一次网络抖动
     变成"线上索引被清空"的事故。
  4. 同一 id+version 已存在于索引时，默认拒绝（AbortUpload）；只有显式
     传 --allow-overwrite 才继续。即便允许覆盖，也只替换 versions 数组里
     那一条，不触碰同一应用的其它版本或其它应用的条目，且不改写任何已
     存在的对象字节（见上「不可变对象 key」）。
  5. 先上传 tarball + sig（各自落在以本次 tarball sha256 为目录层的新
     key 下），再各自从 S3 读回字节做 sha256 校验，确认线上对象与本地
     文件完全一致后才写索引——避免"R2 返回 200 但内容被截断/损坏"这类
     边缘情况下，索引已经指向了一个坏对象。任一步失败都不写索引，已
     上传的坏对象也不会被索引引用到（坏对象 key 含它自己的 sha256，天然
     与"好对象"的 key 不同，不会被复用/覆盖）。
  6. 写索引前重新读一次线上索引（而不是沿用步骤 3 缓存的版本），且仅当
     本次要写入的 app 条目在重读结果里仍处于预期状态时才提交——这是配合
     发布锁的辅助检测，不是独立的 CAS 机制：它只能发现"本次目标
     id+version 条目"被并发改写，不能发现索引里其它 app/version 条目的
     并发写入（那部分互斥完全依赖步骤 2 的发布锁）。
  7. 全部对象 CacheControl: no-cache（面板侧另有 1h 索引缓存）。
  8. finally 释放发布锁（即便中途任何一步 AbortUpload）。

并发发布保护（非真正分布式 CAS，务实折衷，见上）：
  本脚本是手工单操作员工具，不依赖/不假设 R2 支持条件写
  （If-Match/If-None-Match，boto3 对 Cloudflare R2 的支持不保证）。改为
  用一个固定 key 的"发布锁"对象（apps/.lock）模拟互斥：获取锁 = 确认锁
  对象不存在（或已过期）后写入本次发布标识，写入后立即回读确认内容确实
  是自己刚写的；释放锁 = 删除该对象。这不是原子操作，两个进程几乎同时
  "看到锁不存在"仍有极小概率的竞态窗口，但相比"完全没有锁、只比较一个
  entry"是质的改善，且脚本明确不声称这是 CAS。锁默认 TTL 10 分钟，超过
  视为陈旧锁（比如上次发布进程被杀、没走到 finally）、允许后续发布自动
  抢占，避免"一次异常退出永久锁死发布通道"。

注意（2026-11-25 实锤）：
  - get.z1sale 绑定的是 **nas** bucket；www-z1-sale 是官网站 bucket，别传错
  - Cloudflare 默认对 404 做约 3 分钟边缘负缓存：上传后立刻 GET 可能仍 404
    （HEAD/带 query 参数可绕过验证），等 3 分钟即真值
"""
import argparse, base64, hashlib, io, json, os, re, sys, tarfile, time, uuid

import boto3  # pip3 install boto3
from cryptography.exceptions import InvalidSignature
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey

ENV_KEYS = ("CLOUDFLARE_S3_API", "CLOUDFLARE_KEY_ID", "CLOUDFLARE_SECRET")
BUCKET = "nas"  # get.z1.sale 绑定的 bucket（www-z1-sale 是官网）
INDEX_KEY = "apps/index.json"
LOCK_KEY = "apps/.lock"
LOCK_TTL_SECONDS = 10 * 60  # 超过视为陈旧锁，允许抢占（见模块 docstring）

# 入仓开发公钥（web/modules/apps/sign.go 的 DevPublicKeyB64），base64 标准
# 编码。它的私钥已公开入仓（web/modules/apps/testdata/devkey/dev.key），
# 仅供本地开发/测试使用，绝不能作为市场正式发布的验签公钥——用它签的包
# 对任何配置了正式 TrustedPublicKeyB64 的面板都验签失败。硬编码在这里是
# 为了让 --pubkey 的拒绝判断脱离 Go 侧常量存在与否；该值本身就是公开材料，
# 不是秘密，若 Go 侧常量将来变更需同步更新（当前未变更，也不打算变更）。
DEV_PUBLIC_KEY_B64 = "5mxVAYf9wWNaqdRHwG9c63EsrpGJRrT0SWAmwtjW/NM="

# tarball 文件名严格形如 <id>-<version>.tar.gz：id 对应 §2 id 规则
# [a-z0-9-]{2,24}，version 为合法 semver（允许 -prerelease/+build，与 Go
# 侧 web/modules/apps/manifest.go 的 semverPattern 对齐）。一次性正则解析
# 而不是"先按最后一个'-'切分再分别校验"，避免 id 本身含多个'-'时取错 version。
TARBALL_NAME_PATTERN = re.compile(
    r"^(?P<id>[a-z0-9-]{2,24})-(?P<version>\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)\.tar\.gz$"
)

# manifest.json 已知字段白名单（APP-SPEC §2）。本脚本只做"未知字段拒绝" +
# 必需字段存在性这一层最小校验，不复刻核心 ParseManifest 的全部语义规则
# （端口范围/权限枚举等），核心在安装时还会再校验一次；这里的目的是在
# 发布前就拦住"包里塞了规范外字段"这类明显问题，而不是取代核心校验。
MANIFEST_KNOWN_FIELDS = {
    "id", "name", "icon", "version", "spec", "minCoreVersion", "port",
    "entry", "healthz", "permissions", "dataDir", "upgrade",
}
MANIFEST_REQUIRED_FIELDS = {"id", "version"}


class AbortUpload(SystemExit):
    """用于"不能安全完成操作"时的显式中止：带明确原因退出，不生成任何
    上传副作用，调用方（人/CI）能从输出直接看到为什么没发布成功。"""

    def __init__(self, reason: str):
        super().__init__(f"ABORT: {reason}")


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


def make_s3_client(env):
    return boto3.client(
        "s3",
        endpoint_url=env["CLOUDFLARE_S3_API"],
        aws_access_key_id=env["CLOUDFLARE_KEY_ID"],
        aws_secret_access_key=env["CLOUDFLARE_SECRET"],
        region_name="auto",
    )


def _decode_pubkey_b64(label: str, value: str) -> bytes:
    """解码一枚 base64 公钥，统一校验格式与长度（32 字节 ed25519 公钥）。
    label 用于错误信息（区分是 --pubkey 还是信任锚配错）。"""
    try:
        raw = base64.b64decode(value, validate=True)
    except Exception as e:
        raise AbortUpload(f"{label} 不是合法 base64: {e}")
    if len(raw) != 32:
        raise AbortUpload(f"{label} 长度非法: 期望 32 字节 ed25519 公钥，实际 {len(raw)} 字节")
    return raw


def resolve_trusted_pubkey_b64(trusted_pubkey_arg: str, env: dict) -> str:
    """解析信任锚来源（§1.1 对齐）：显式 --trusted-pubkey 优先，否则读
    env 的 APP_TRUSTED_PUBKEY（scripts/release.sh 注入 Go 构建用的同一个
    变量名/同一文件）。两者皆无 → fail-closed 中止，不允许"没配信任锚
    就放行任意 --pubkey"这种退化路径。"""
    value = trusted_pubkey_arg or env.get("APP_TRUSTED_PUBKEY")
    if not value:
        raise AbortUpload(
            "未配置信任锚：既未传 --trusted-pubkey，~/.hermes/.env 也没有 "
            "APP_TRUSTED_PUBKEY（与 scripts/release.sh 注入 Go 构建的同一变量）。"
            "fail-closed：拒绝在没有唯一信任锚的情况下放行任何 --pubkey"
        )
    return value


def verify_signature(pubkey_b64: str, trusted_pubkey_b64: str, data: bytes, sig: bytes):
    """验签入口，顺序固定：
      1. 开发公钥检查（独立判断，报错信息更具体）
      2. --pubkey 必须与信任锚逐字节相等（§1.1 信任锚绑定，核心要求）
      3. ed25519 签名校验
    任一环节失败直接 AbortUpload（不吞异常、不降级为警告）。"""
    dev_bytes = base64.b64decode(DEV_PUBLIC_KEY_B64, validate=True)
    pub_bytes = _decode_pubkey_b64("--pubkey", pubkey_b64)
    if pub_bytes == dev_bytes:
        raise AbortUpload(
            "--pubkey 等于入仓开发公钥（web/modules/apps/sign.go 的 "
            "DevPublicKeyB64，私钥已公开已知），拒绝用它发布市场正式包"
        )

    trusted_bytes = _decode_pubkey_b64("信任锚（--trusted-pubkey/APP_TRUSTED_PUBKEY）", trusted_pubkey_b64)
    if pub_bytes != trusted_bytes:
        raise AbortUpload(
            "--pubkey 与信任锚（--trusted-pubkey/APP_TRUSTED_PUBKEY，对应核心 "
            "TrustedPublicKeyB64）不一致，拒绝发布——市场正式包必须用与核心构建"
            "同一枚信任锚公钥验证通过的签名，不接受任何其它合法 ed25519 公钥"
        )

    try:
        Ed25519PublicKey.from_public_bytes(pub_bytes).verify(sig, data)
    except InvalidSignature:
        raise AbortUpload("签名校验失败：tarball 与 sig/--pubkey 不匹配，拒绝上传")
    except Exception as e:
        raise AbortUpload(f"签名校验过程出错: {e}")


def validate_tarball_layout(tarball_basename: str, data: bytes, expected_id: str) -> dict:
    """在已读入内存的 tarball 字节上做布局+manifest 校验，规则对齐核心
    web/modules/apps/package.go 的 UnpackAppPackage 与 manifest.go 的
    ParseManifest（§1 包格式 / §2 manifest 契约的基本约束）：
      - basename 严格为 <id>-<version>.tar.gz，--id 必须与解析出的 id 一致
      - tar 内拒绝绝对路径/".."片段/符号链接/硬链接/非常规文件条目
      - 根目录必须单一且等于 <id>-<version>
      - 恰好一个 manifest.json（根目录正下方，不接受子目录或多个）
      - manifest.json 必须是合法 JSON 对象，不接受未知字段，必需字段齐全
      - manifest 内的 id/version 与文件名解析结果一致
    返回解析后的 manifest dict；任何不满足直接 AbortUpload。不在本地磁盘
    上重新按路径打开文件（只用已读取的 data 字节），避免 TOCTOU。
    """
    m = TARBALL_NAME_PATTERN.match(tarball_basename)
    if not m:
        raise AbortUpload(
            f"tarball 文件名 {tarball_basename!r} 不符合 <id>-<version>.tar.gz 格式"
            f"（§1 包格式），拒绝上传"
        )
    name_id, name_version = m.group("id"), m.group("version")
    if name_id != expected_id:
        raise AbortUpload(
            f"tarball 文件名解析出的 id ({name_id!r}) 与 --id ({expected_id!r}) 不一致，拒绝上传"
        )
    expected_root = f"{name_id}-{name_version}"

    try:
        tf = tarfile.open(fileobj=io.BytesIO(data), mode="r:gz")
    except Exception as e:
        raise AbortUpload(f"tarball 不是合法 gzip/tar 数据: {e}")

    root_prefix = None
    manifest_data = None
    manifest_count = 0
    try:
        with tf:
            for member in tf.getmembers():
                name = member.name
                if name in (".", "") :
                    continue
                if os.path.isabs(name) or name.startswith("/"):
                    raise AbortUpload(f"拒绝绝对路径条目: {name}")
                segments = name.split("/")
                if any(seg == ".." for seg in segments):
                    raise AbortUpload(f"拒绝包含 .. 的条目: {name}")
                if member.issym() or member.islnk():
                    raise AbortUpload(f"拒绝符号链接/硬链接条目: {name}")

                cleaned = name.rstrip("/")
                if cleaned == "":
                    continue
                parts = cleaned.split("/", 1)
                this_root = parts[0]
                if root_prefix is None:
                    root_prefix = this_root
                elif this_root != root_prefix:
                    raise AbortUpload(
                        f"根目录不一致: 期望单一前缀 {root_prefix!r}，发现 {this_root!r}"
                    )

                if member.isdir():
                    continue
                if not member.isfile():
                    raise AbortUpload(f"拒绝非常规文件条目: {name}（类型 {member.type!r}）")

                if len(parts) < 2:
                    continue  # 根目录自身作为文件条目，交给下方必需文件校验报错
                rel_path = parts[1]
                if rel_path == "manifest.json":
                    manifest_count += 1
                    if manifest_count > 1:
                        raise AbortUpload("tarball 内发现多个 manifest.json，拒绝上传（只允许恰好一个）")
                    extracted = tf.extractfile(member)
                    if extracted is None:
                        raise AbortUpload("manifest.json 不是常规文件")
                    manifest_data = extracted.read()
    except AbortUpload:
        raise
    except tarfile.TarError as e:
        raise AbortUpload(f"tar 格式错误: {e}")

    if root_prefix is None:
        raise AbortUpload("tarball 为空或不含任何条目")
    if root_prefix != expected_root:
        raise AbortUpload(
            f"tarball 根目录 {root_prefix!r} 与文件名推断的 {expected_root!r} 不一致，拒绝上传"
        )
    if manifest_count == 0:
        raise AbortUpload(f"tarball 内未找到 {expected_root}/manifest.json，拒绝上传")

    try:
        manifest = json.loads(manifest_data.decode("utf-8"))
    except Exception as e:
        raise AbortUpload(f"解析 tarball 内 manifest.json 失败: {e}")
    if not isinstance(manifest, dict):
        raise AbortUpload("manifest.json 顶层必须是 JSON 对象")

    unknown = set(manifest.keys()) - MANIFEST_KNOWN_FIELDS
    if unknown:
        raise AbortUpload(f"manifest.json 含未知字段（§2 不允许规范外字段）: {sorted(unknown)}")
    missing = MANIFEST_REQUIRED_FIELDS - set(manifest.keys())
    if missing:
        raise AbortUpload(f"manifest.json 缺少必需字段: {sorted(missing)}")

    manifest_id = manifest.get("id")
    manifest_version = manifest.get("version")
    if manifest_id != name_id:
        raise AbortUpload(
            f"manifest.json 的 id ({manifest_id!r}) 与 --id/文件名 ({name_id!r}) 不一致，拒绝上传"
        )
    if manifest_version != name_version:
        raise AbortUpload(
            f"manifest.json 的 version ({manifest_version!r}) 与文件名版本 "
            f"({name_version!r}) 不一致，拒绝上传（§1 文件名须为 <id>-<version>.tar.gz）"
        )
    return manifest


def _is_not_found_error(e) -> bool:
    """区分"确实不存在"（S3 404/NoSuchKey）与其它读失败。botocore 对 404
    可能抛具体的 NoSuchKey，也可能抛通用 ClientError，按错误码兜底判断。"""
    code = None
    resp = getattr(e, "response", None)
    if isinstance(resp, dict):
        code = resp.get("Error", {}).get("Code")
    return code in ("NoSuchKey", "404")


def _get_object_if_exists(s3, key: str):
    """读取对象，返回 bytes；确认不存在（404/NoSuchKey）时返回 None；其它
    读失败原样抛出，由调用方决定如何处理（不同场景对"读失败"的容错策略
    不一样，不在这里统一吞掉）。"""
    try:
        return s3.get_object(Bucket=BUCKET, Key=key)["Body"].read()
    except Exception as e:
        no_such_key = getattr(getattr(s3, "exceptions", None), "NoSuchKey", None)
        if no_such_key is not None and isinstance(e, no_such_key):
            return None
        if _is_not_found_error(e):
            return None
        raise


def acquire_publish_lock(s3) -> str:
    """获取发布锁（apps/.lock），模拟互斥，细节与局限见模块 docstring
    「并发发布保护」一节：不是原子 CAS，是"读不存在/陈旧 → 写入 → 回读
    确认是自己刚写的"这一务实折衷。返回本次持有的锁 token（release 时用
    于确认自己仍是持有者，避免释放掉别人后来抢占到的锁）。

    锁已被其它未过期的发布占用 → AbortUpload，不产生任何上传副作用
    （调用点在本地校验之后、读索引之前，见 run() 注释）。
    """
    existing = _get_object_if_exists(s3, LOCK_KEY)
    if existing is not None:
        try:
            info = json.loads(existing)
            held_at = float(info.get("ts", 0))
        except Exception:
            held_at = 0.0
        age = time.time() - held_at
        if age < LOCK_TTL_SECONDS:
            raise AbortUpload(
                f"发布锁（{LOCK_KEY}）已被占用（{age:.0f}s 前获取，TTL {LOCK_TTL_SECONDS}s），"
                f"可能有另一次发布正在进行，拒绝并发发布。若确认对方已异常退出，"
                f"等待锁过期或手工清理该对象后重试"
            )
        print(f"[WARN] 发布锁已过期（{age:.0f}s 前获取），视为陈旧锁，允许抢占")

    token = uuid.uuid4().hex
    payload = json.dumps({"token": token, "ts": time.time()}).encode()
    s3.put_object(Bucket=BUCKET, Key=LOCK_KEY, Body=payload, ContentType="application/json", CacheControl="no-cache")
    readback = _get_object_if_exists(s3, LOCK_KEY)
    if readback is None:
        raise AbortUpload(f"获取发布锁失败：写入后读回 {LOCK_KEY} 不存在")
    try:
        readback_token = json.loads(readback).get("token")
    except Exception:
        readback_token = None
    if readback_token != token:
        # 几乎同时的另一次 acquire 在本次写入之后又覆盖了锁对象——务实折衷
        # 的已知竞态窗口（非原子 CAS，见模块 docstring），诚实报告为锁获取
        # 失败而不是假装成功继续。
        raise AbortUpload(
            f"获取发布锁失败：写入后读回内容不是本次 token（{LOCK_KEY} 可能被并发发布抢先改写），"
            f"拒绝继续，重跑本命令"
        )
    print(f"[OK] 已获取发布锁 {LOCK_KEY} token={token}")
    return token


def release_publish_lock(s3, token: str):
    """释放发布锁：仅当锁对象仍是本次持有的 token 时才删除，避免释放掉
    陈旧锁过期后被别的发布抢占到的锁。读取/删除本身失败只打印警告，不
    再次抛出——此时主流程已经完成或已经在中止路径上，不应该让"释放锁"
    这个收尾动作掩盖真正的结果。"""
    try:
        current = _get_object_if_exists(s3, LOCK_KEY)
        if current is None:
            return
        current_token = json.loads(current).get("token")
        if current_token != token:
            print(f"[WARN] 发布锁已不是本次持有（已被抢占为陈旧锁后的新发布），跳过释放")
            return
        if hasattr(s3, "delete_object"):
            s3.delete_object(Bucket=BUCKET, Key=LOCK_KEY)
        else:
            # 最简 FakeS3 测试替身可能未实现 delete_object：退化为覆盖写一个
            # 空载荷，effect 等价于"锁已释放"（下次 acquire 读到的 token 不匹配）。
            s3.put_object(Bucket=BUCKET, Key=LOCK_KEY, Body=b"{}", ContentType="application/json", CacheControl="no-cache")
        print("[OK] 已释放发布锁")
    except Exception as e:
        print(f"[WARN] 释放发布锁时出错（不影响本次发布结果）: {e}")


def load_existing_index(s3) -> dict:
    """读取线上现有索引。区分"确实不存在"（S3 404/NoSuchKey，返回空索引
    合情合理）与"读失败"（网络错误、权限错误、索引内容损坏等，必须中止，
    绝不能让这类失败悄悄表现成"索引是空的"进而被后续逻辑覆盖掉线上数据）。
    """
    try:
        body = _get_object_if_exists(s3, INDEX_KEY)
    except Exception as e:
        raise AbortUpload(
            f"读取线上索引失败（{e}）——这不等于索引不存在，为避免误用空索引"
            f"覆盖线上数据，中止本次上传。请排查 R2 凭据/网络后重试。"
        )
    if body is None:
        print("[INFO] 线上索引不存在，将新建")
        return {"spec": "1", "apps": []}

    try:
        index = json.loads(body)
    except Exception as e:
        raise AbortUpload(f"线上索引内容不是合法 JSON（{e}），可能已损坏，中止——需要人工确认后再处理")
    if not isinstance(index, dict) or "apps" not in index:
        raise AbortUpload("线上索引缺少 apps 字段或结构非法，中止——需要人工确认后再处理")
    index.setdefault("spec", "1")
    return index


def find_version_entry(index: dict, app_id: str, version: str):
    """在索引里找 id==app_id 的 app 条目，及其 versions 里 version 匹配的
    那一条（后者可能为 None，代表这个版本还不存在）。"""
    app = next((a for a in index.get("apps", []) if a.get("id") == app_id), None)
    if app is None:
        return None, None
    entry = next((v for v in app.get("versions", []) if v.get("version") == version), None)
    return app, entry


def put_and_verify_readback(s3, key: str, body: bytes, content_type: str):
    """上传一个对象，再从 S3 读回字节做 sha256 比较，确认线上内容与本地
    一致才算成功。任一环节异常都 AbortUpload，不吞异常继续——读回校验的
    意义就是不信任"put_object 没报错 = 内容一定对"这个假设。"""
    s3.put_object(Bucket=BUCKET, Key=key, Body=body, ContentType=content_type, CacheControl="no-cache")
    try:
        readback = s3.get_object(Bucket=BUCKET, Key=key)["Body"].read()
    except Exception as e:
        raise AbortUpload(f"上传后读回校验失败（{key}）：{e}——线上对象状态不确定，中止，不写索引")
    want = hashlib.sha256(body).hexdigest()
    got = hashlib.sha256(readback).hexdigest()
    if want != got:
        raise AbortUpload(
            f"上传后读回校验不一致（{key}）：本地 sha256={want} 线上 sha256={got}，"
            f"中止，不写索引（线上对象可能损坏，需要人工排查后重新上传）"
        )
    print(f"[OK] {key} {len(body)} bytes，读回校验通过 sha256={got}")


def build_run_parser() -> argparse.ArgumentParser:
    ap = argparse.ArgumentParser()
    ap.add_argument("--id", required=True)
    ap.add_argument("--name", required=True)
    ap.add_argument("--tarball", required=True)
    ap.add_argument("--sig", required=True)
    ap.add_argument("--pubkey", required=True,
                     help="验签公钥 base64；必须与信任锚（见 --trusted-pubkey）逐字节一致")
    ap.add_argument("--trusted-pubkey", default=None,
                     help="信任锚 base64（对应 TrustedPublicKeyB64，§1.1）；缺省读 "
                          "~/.hermes/.env 的 APP_TRUSTED_PUBKEY。--pubkey 必须与它逐字节相等")
    ap.add_argument("--summary", default="")
    ap.add_argument("--changelog", default="")
    ap.add_argument("--icon", default="")
    ap.add_argument("--min-core", default="1.5.0")
    ap.add_argument("--index", help="可选：本地预建索引路径（仍会合入本条目）")
    ap.add_argument("--allow-overwrite", action="store_true",
                     help="同一 id+version 已发布时默认拒绝；显式传此项才允许发布新内容"
                          "并切换索引引用（不改写已存在对象，见模块 docstring）")
    return ap


def run(argv, s3_factory=make_s3_client, env_loader=load_env):
    """主流程，拆成可注入 s3_factory/env_loader 的函数，供离线测试用 fake S3
    替换真实网络调用；CLI 入口（main）用默认参数调用它。"""
    args = build_run_parser().parse_args(argv)

    with open(args.tarball, "rb") as f:
        data = f.read()
    with open(args.sig, "rb") as f:
        sig = f.read()
    tarball_basename = os.path.basename(args.tarball)

    env = env_loader()
    trusted_pubkey_b64 = resolve_trusted_pubkey_b64(args.trusted_pubkey, env)

    # ── 1. 验签（信任锚绑定 + 开发公钥拒绝）+ tarball/manifest 布局校验 ──
    verify_signature(args.pubkey, trusted_pubkey_b64, data, sig)
    print("[OK] 签名校验通过（信任锚匹配，非开发公钥）")

    manifest = validate_tarball_layout(tarball_basename, data, args.id)
    version = manifest["version"]
    print(f"[OK] tarball 布局与 manifest 一致性校验通过: id={manifest['id']} version={version}")

    s3 = s3_factory(env)

    # ── 2. 获取发布锁（见模块 docstring「并发发布保护」） ──────────────
    lock_token = acquire_publish_lock(s3)
    try:
        # ── 3. 读现有索引（失败即中止，不用空索引覆盖） ──────────────
        index = load_existing_index(s3)
        if args.index and os.path.exists(args.index):
            with open(args.index) as f:
                idx = json.load(f)
            index.setdefault("spec", idx.get("spec", "1"))
            for app in idx.get("apps", []):
                if app["id"] != args.id:
                    index["apps"].append(app)

        # ── 4. 同版本覆盖默认拒绝（显式 --allow-overwrite 才继续） ───
        existing_app, existing_entry = find_version_entry(index, args.id, version)
        if existing_entry is not None and not args.allow_overwrite:
            raise AbortUpload(
                f"{args.id} {version} 已存在于线上索引（tarball={existing_entry.get('tarball')!r}），"
                f"默认拒绝覆盖。确认要发布新内容替换已发布版本时显式传 --allow-overwrite 重跑"
            )
        if existing_entry is not None:
            print(f"[WARN] --allow-overwrite 已传入，将发布新内容并切换 {args.id} {version} 的索引引用"
                  f"（旧对象不会被改写，见模块 docstring）")

        # ── 5. 不可变对象 key：apps/<id>/<version>/<sha256>/<basename> ──
        sha256 = hashlib.sha256(data).hexdigest()
        base_key = f"apps/{args.id}/{version}/{sha256}"
        base_url = f"https://get.z1.sale/{base_key}"
        sig_basename = os.path.basename(args.sig)
        entry = {
            "version": version,
            "minCoreVersion": args.min_core,
            "tarball": f"{base_url}/{tarball_basename}",
            "sha256": sha256,
            "sig": f"{base_url}/{sig_basename}",
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

        # ── 6. 先上传 tarball + sig（新不可变 key），各自读回校验，最后才写索引 ──
        put_and_verify_readback(s3, f"{base_key}/{tarball_basename}", data, "application/gzip")
        put_and_verify_readback(s3, f"{base_key}/{sig_basename}", sig, "text/plain")

        # ── 7. 写索引前重新读一次线上索引，辅助检测（非 CAS，见模块 docstring） ──
        fresh_index = load_existing_index(s3)
        fresh_app, fresh_entry = find_version_entry(fresh_index, args.id, version)
        if fresh_entry != existing_entry:
            raise AbortUpload(
                f"写索引前检测到线上索引中 {args.id} {version} 的状态已被并发修改（从 "
                f"{existing_entry!r} 变为 {fresh_entry!r}），中止——避免用本次基于旧索引"
                f"算出的合并结果覆盖对方刚发布的内容。本检测仅覆盖目标 id+version 条目，"
                f"不是整份索引的 CAS，完整互斥依赖发布锁（见模块 docstring）。资源已上传"
                f"到不可变 key，重跑本命令可基于最新索引重新合并发布"
            )

        s3.put_object(
            Bucket=BUCKET, Key=INDEX_KEY,
            Body=json.dumps(index, ensure_ascii=False, indent=2).encode(),
            ContentType="application/json", CacheControl="no-cache",
        )
        print("[OK]", INDEX_KEY, "updated")

        print("sha256:", entry["sha256"])
        print("提示：Cloudflare 对 404 有 ~3 分钟负缓存，上传后稍等再验证。")
    finally:
        # ── 8. 无论成功还是中止，都释放发布锁，避免锁泄漏 ────────────
        release_publish_lock(s3, lock_token)


def main():
    run(sys.argv[1:])


if __name__ == "__main__":
    main()
