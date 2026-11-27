#!/usr/bin/env python3
"""market_upload_test.py — scripts/market-upload.py 的离线回归测试。

全部用例用 FakeS3（内存字典模拟 S3 get/put，不建立任何网络连接）与随机生成
的测试专用 ed25519 密钥对，不连真实 R2、不读写 ~/.hermes/.env、不使用任何
正式私钥（本仓库也不持有正式私钥）。

运行：
  python3 scripts/market_upload_test.py -v

覆盖点对应任务要求：
  - --pubkey 必须与信任锚（--trusted-pubkey/APP_TRUSTED_PUBKEY）逐字节相等；
    任何其它合法 ed25519 公钥（即便签名本身有效）一律拒绝；信任锚缺失/非法
    时 fail-closed
  - 拒绝开发公钥（即便签名本身是有效的 ed25519 签名）
  - tarball/manifest 布局强校验：basename 必须是 <id>-<version>.tar.gz、
    单一根目录、恰好一个 manifest.json、manifest 不接受未知字段
  - 先上传后读回校验（读回内容/哈希不一致时中止，且不写索引）
  - 不可变对象 key（含内容 sha256）：同版本覆盖不改写旧对象，只切换索引引用
  - 发布锁：锁已被占用时直接中止、不产生副作用；释放后可重新获取
  - 验签失败、manifest 不一致等既有校验维持可用
"""
import base64
import hashlib
import importlib.util
import io
import json
import os
import sys
import tarfile
import tempfile
import time
import unittest

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives.serialization import Encoding, PublicFormat

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))

# market-upload.py 带短横线，不是合法 Python 模块名，用 importlib 按路径加载。
_spec = importlib.util.spec_from_file_location("market_upload", os.path.join(SCRIPT_DIR, "market-upload.py"))
mu = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(mu)


class FakeS3Exceptions:
    class NoSuchKey(Exception):
        pass


class FakeS3:
    """内存字典模拟的 S3 客户端，仅实现 market-upload.py 用到的
    get_object/put_object/delete_object 接口形状。不建立任何网络连接。"""

    def __init__(self, initial_objects=None):
        self.objects = dict(initial_objects or {})
        self.put_calls = []
        self.exceptions = FakeS3Exceptions
        # 测试用钩子：在第 N 次 get_object(INDEX_KEY) 命中前执行一次回调，
        # 用于模拟"读索引之后、写索引之前，索引被并发改写"的时间窗口竞态。
        # run() 里 INDEX_KEY 被读两次：步骤3（初次合并）与步骤7（写索引前
        # 的辅助并发检查），hook 要在第 2 次命中前触发才对得上时间窗口。
        self._get_hook = None
        self._get_hook_before_nth_index_read = 2
        self._index_read_count = 0

    def get_object(self, Bucket, Key):
        if Key == mu.INDEX_KEY:
            self._index_read_count += 1
            if self._get_hook is not None and self._index_read_count == self._get_hook_before_nth_index_read:
                hook, self._get_hook = self._get_hook, None
                hook()
        if Key not in self.objects:
            raise self.exceptions.NoSuchKey(f"no such key: {Key}")
        return {"Body": io.BytesIO(self.objects[Key])}

    def put_object(self, Bucket, Key, Body, ContentType, CacheControl):
        if hasattr(Body, "read"):
            Body = Body.read()
        self.objects[Key] = bytes(Body)
        self.put_calls.append(Key)

    def delete_object(self, Bucket, Key):
        self.objects.pop(Key, None)


def make_tarball(app_id: str, version: str, manifest_extra: dict = None, root_override: str = None) -> bytes:
    manifest = {"id": app_id, "version": version}
    if manifest_extra:
        manifest.update(manifest_extra)
    manifest_bytes = json.dumps(manifest).encode()
    root = root_override if root_override is not None else f"{app_id}-{version}"
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tf:
        info = tarfile.TarInfo(name=f"{root}/manifest.json" if root else "manifest.json")
        info.size = len(manifest_bytes)
        tf.addfile(info, io.BytesIO(manifest_bytes))
    return buf.getvalue()


def sign(priv: Ed25519PrivateKey, data: bytes) -> bytes:
    return priv.sign(data)


class MarketUploadTestBase(unittest.TestCase):
    def setUp(self):
        self.tmpdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmpdir.cleanup)
        self.priv = Ed25519PrivateKey.generate()
        self.pub_b64 = base64.b64encode(
            self.priv.public_key().public_bytes(encoding=Encoding.Raw, format=PublicFormat.Raw)
        ).decode()
        # 测试专用信任锚：默认让 --pubkey 直接等于信任锚，模拟"配置正确"的
        # 正常路径；需要测"不一致"场景的用例自行覆盖 trusted_pubkey 参数。
        self.trusted_pub_b64 = self.pub_b64

    def write_package(self, app_id="photos", version="1.0.0", priv=None, tarball_bytes=None):
        priv = priv or self.priv
        tb = tarball_bytes if tarball_bytes is not None else make_tarball(app_id, version)
        sig = sign(priv, tb)
        tb_path = os.path.join(self.tmpdir.name, f"{app_id}-{version}.tar.gz")
        sig_path = tb_path + ".sig"
        with open(tb_path, "wb") as f:
            f.write(tb)
        with open(sig_path, "wb") as f:
            f.write(sig)
        return tb_path, sig_path, tb, sig

    def base_argv(self, tb_path, sig_path, app_id="photos", version="1.0.0", pubkey=None,
                   trusted_pubkey=None, extra=None):
        argv = [
            "--id", app_id, "--name", "照片",
            "--tarball", tb_path, "--sig", sig_path,
            "--pubkey", pubkey if pubkey is not None else self.pub_b64,
            "--trusted-pubkey", trusted_pubkey if trusted_pubkey is not None else self.trusted_pub_b64,
            "--summary", "测试", "--changelog", "测试版本",
        ]
        if extra:
            argv += extra
        return argv

    def run_upload(self, s3, argv, env=None):
        env = env or {"CLOUDFLARE_S3_API": "https://fake", "CLOUDFLARE_KEY_ID": "k", "CLOUDFLARE_SECRET": "s"}
        return mu.run(argv, s3_factory=lambda _env: s3, env_loader=lambda: env)

    def object_key_for(self, s3, app_id, version, basename):
        """从索引里找到 basename 对应的不可变 key（含 sha256 目录层），
        不在测试里重复硬编码 sha256 计算逻辑。"""
        index = json.loads(s3.objects[mu.INDEX_KEY])
        app = next(a for a in index["apps"] if a["id"] == app_id)
        entry = next(v for v in app["versions"] if v["version"] == version)
        url = entry["tarball"] if basename.endswith(".tar.gz") else entry["sig"]
        prefix = "https://get.z1.sale/"
        self.assertTrue(url.startswith(prefix))
        return url[len(prefix):]


class TestHappyPath(MarketUploadTestBase):
    def test_first_publish_creates_index_and_objects(self):
        s3 = FakeS3()
        tb_path, sig_path, tb, sig = self.write_package()
        self.run_upload(s3, self.base_argv(tb_path, sig_path))

        sha = hashlib.sha256(tb).hexdigest()
        tb_key = f"apps/photos/1.0.0/{sha}/{os.path.basename(tb_path)}"
        sig_key = f"apps/photos/1.0.0/{sha}/{os.path.basename(sig_path)}"
        self.assertIn(tb_key, s3.objects)
        self.assertIn(sig_key, s3.objects)
        self.assertEqual(s3.objects[tb_key], tb)
        self.assertEqual(s3.objects[sig_key], sig)

        index = json.loads(s3.objects[mu.INDEX_KEY])
        app = next(a for a in index["apps"] if a["id"] == "photos")
        self.assertEqual(app["latest"], "1.0.0")
        v = app["versions"][0]
        self.assertEqual(v["version"], "1.0.0")
        self.assertEqual(v["sha256"], sha)
        self.assertEqual(v["tarball"], f"https://get.z1.sale/{tb_key}")
        self.assertEqual(v["sig"], f"https://get.z1.sale/{sig_key}")

        # 发布锁必须在成功发布后被释放（删除或置空），不留下悬挂锁。
        self.assertNotIn(mu.LOCK_KEY, s3.objects, "发布成功后发布锁应被释放")

    def test_second_app_does_not_clobber_first(self):
        s3 = FakeS3()
        tb1, sig1, *_ = self.write_package(app_id="photos", version="1.0.0")
        self.run_upload(s3, self.base_argv(tb1, sig1, app_id="photos", version="1.0.0"))

        tb2, sig2, *_ = self.write_package(app_id="hello", version="1.0.0")
        self.run_upload(s3, self.base_argv(tb2, sig2, app_id="hello", version="1.0.0"))

        index = json.loads(s3.objects[mu.INDEX_KEY])
        ids = sorted(a["id"] for a in index["apps"])
        self.assertEqual(ids, ["hello", "photos"])


class TestTrustedPubkeyBinding(MarketUploadTestBase):
    """要求1：--pubkey 必须与信任锚逐字节相等，任何其它合法公钥一律拒绝。"""

    def test_valid_but_wrong_pubkey_rejected(self):
        """--pubkey 是一枚完全合法、且确实能验证通过签名的 ed25519 公钥，
        但它不是配置的信任锚：必须拒绝。这正是"自己签自己验、绕过信任锚"
        的攻击场景，是本次修复要堵住的核心漏洞。"""
        s3 = FakeS3()
        tb_path, sig_path, tb, sig = self.write_package()  # 用 self.priv 签

        # 信任锚配的是另一把完全不同、同样合法的公钥。
        other_priv = Ed25519PrivateKey.generate()
        other_trusted_b64 = base64.b64encode(
            other_priv.public_key().public_bytes(encoding=Encoding.Raw, format=PublicFormat.Raw)
        ).decode()

        argv = self.base_argv(tb_path, sig_path, trusted_pubkey=other_trusted_b64)
        with self.assertRaises(SystemExit) as ctx:
            self.run_upload(s3, argv)
        self.assertIn("信任锚", str(ctx.exception))
        self.assertEqual(s3.put_calls, [], "--pubkey 与信任锚不一致时不应产生任何上传副作用")

    def test_pubkey_equal_to_trusted_anchor_passes(self):
        s3 = FakeS3()
        tb_path, sig_path, *_ = self.write_package()
        # self.trusted_pub_b64 默认等于 self.pub_b64，走正常成功路径。
        self.run_upload(s3, self.base_argv(tb_path, sig_path))
        self.assertIn(mu.INDEX_KEY, s3.objects)

    def test_missing_trust_anchor_fails_closed(self):
        """既不传 --trusted-pubkey，env 里也没有 APP_TRUSTED_PUBKEY：
        必须 fail-closed 中止，不允许退化为"放行任意 --pubkey"。"""
        s3 = FakeS3()
        tb_path, sig_path, *_ = self.write_package()
        argv = [
            "--id", "photos", "--name", "照片",
            "--tarball", tb_path, "--sig", sig_path,
            "--pubkey", self.pub_b64,
            "--summary", "测试", "--changelog", "测试版本",
        ]
        env = {"CLOUDFLARE_S3_API": "https://fake", "CLOUDFLARE_KEY_ID": "k", "CLOUDFLARE_SECRET": "s"}
        with self.assertRaises(SystemExit) as ctx:
            mu.run(argv, s3_factory=lambda _env: s3, env_loader=lambda: env)
        self.assertIn("信任锚", str(ctx.exception))
        self.assertEqual(s3.put_calls, [])

    def test_malformed_trust_anchor_fails_closed(self):
        s3 = FakeS3()
        tb_path, sig_path, *_ = self.write_package()
        argv = self.base_argv(tb_path, sig_path, trusted_pubkey="not-valid-base64-!!!")
        with self.assertRaises(SystemExit) as ctx:
            self.run_upload(s3, argv)
        self.assertIn("信任锚", str(ctx.exception))
        self.assertEqual(s3.put_calls, [])

    def test_trust_anchor_from_env_when_flag_omitted(self):
        """不传 --trusted-pubkey 时从 env 的 APP_TRUSTED_PUBKEY 读信任锚
        （对应 ~/.hermes/.env，与 scripts/release.sh 同一变量名）。"""
        s3 = FakeS3()
        tb_path, sig_path, *_ = self.write_package()
        argv = [
            "--id", "photos", "--name", "照片",
            "--tarball", tb_path, "--sig", sig_path,
            "--pubkey", self.pub_b64,
            "--summary", "测试", "--changelog", "测试版本",
        ]
        env = {
            "CLOUDFLARE_S3_API": "https://fake", "CLOUDFLARE_KEY_ID": "k", "CLOUDFLARE_SECRET": "s",
            "APP_TRUSTED_PUBKEY": self.pub_b64,
        }
        mu.run(argv, s3_factory=lambda _env: s3, env_loader=lambda: env)
        self.assertIn(mu.INDEX_KEY, s3.objects)


class TestRejectsDevPublicKey(MarketUploadTestBase):
    def test_dev_pubkey_rejected_even_with_valid_signature(self):
        """用开发私钥（testdata/devkey/dev.key）签出有效签名，但 --pubkey
        传开发公钥：必须在上传任何对象之前就被拒绝。"""
        dev_key_path = os.path.join(SCRIPT_DIR, "..", "web", "modules", "apps", "testdata", "devkey", "dev.key")
        with open(dev_key_path, "rb") as f:
            dev_priv_bytes = f.read()
        # Go 的 ed25519.PrivateKey 是 64 字节（32 字节 seed + 32 字节公钥），
        # cryptography 库只要前 32 字节的 seed。
        self.assertEqual(len(dev_priv_bytes), 64, "开发私钥文件格式应为 Go ed25519.PrivateKey 64 字节")
        dev_priv = Ed25519PrivateKey.from_private_bytes(dev_priv_bytes[:32])

        tb_path, sig_path, tb, sig = self.write_package(priv=dev_priv)
        s3 = FakeS3()
        # 信任锚也配成开发公钥，确保走到的是"开发公钥"判断而不是"与信任锚不一致"。
        argv = self.base_argv(tb_path, sig_path, pubkey=mu.DEV_PUBLIC_KEY_B64,
                               trusted_pubkey=mu.DEV_PUBLIC_KEY_B64)

        with self.assertRaises(SystemExit) as ctx:
            self.run_upload(s3, argv)
        self.assertIn("开发公钥", str(ctx.exception))
        self.assertEqual(s3.put_calls, [], "拒绝开发公钥时不应产生任何上传副作用")

    def test_dev_pubkey_rejected_independent_of_signature_validity(self):
        """--pubkey 等于开发公钥时直接拒绝，不管签名是否恰好能验证通过——
        即便签名其实是无效的，报错原因也应指向"开发公钥"而不是"签名无效"，
        因为开发公钥检查先于验签执行。"""
        tb_path, sig_path, tb, sig = self.write_package()  # 用测试私钥签，不是开发私钥
        s3 = FakeS3()
        argv = self.base_argv(tb_path, sig_path, pubkey=mu.DEV_PUBLIC_KEY_B64,
                               trusted_pubkey=mu.DEV_PUBLIC_KEY_B64)

        with self.assertRaises(SystemExit) as ctx:
            self.run_upload(s3, argv)
        self.assertIn("开发公钥", str(ctx.exception))


class TestManifestLayoutHardening(MarketUploadTestBase):
    """要求2：tarball/manifest 校验在已读入内存的 data 上执行，严格对齐
    核心 UnpackAppPackage/ParseManifest 的基本约束。"""

    def test_wrong_basename_rejected(self):
        s3 = FakeS3()
        tb = make_tarball("photos", "1.0.0")
        sig = sign(self.priv, tb)
        tb_path = os.path.join(self.tmpdir.name, "wrong-name.tar.gz")
        sig_path = tb_path + ".sig"
        with open(tb_path, "wb") as f:
            f.write(tb)
        with open(sig_path, "wb") as f:
            f.write(sig)
        with self.assertRaises(SystemExit) as ctx:
            self.run_upload(s3, self.base_argv(tb_path, sig_path))
        self.assertIn("tarball 文件名", str(ctx.exception))
        self.assertEqual(s3.put_calls, [])

    def test_wrong_root_dir_rejected(self):
        s3 = FakeS3()
        tb = make_tarball("photos", "1.0.0", root_override="not-the-right-root")
        tb_path, sig_path, tb, sig = self.write_package(tarball_bytes=tb)
        with self.assertRaises(SystemExit) as ctx:
            self.run_upload(s3, self.base_argv(tb_path, sig_path))
        self.assertIn("根目录", str(ctx.exception))
        self.assertEqual(s3.put_calls, [])

    def test_multiple_manifest_json_rejected(self):
        s3 = FakeS3()
        manifest = json.dumps({"id": "photos", "version": "1.0.0"}).encode()
        buf = io.BytesIO()
        with tarfile.open(fileobj=buf, mode="w:gz") as tf:
            for _ in range(2):
                info = tarfile.TarInfo(name="photos-1.0.0/manifest.json")
                info.size = len(manifest)
                tf.addfile(info, io.BytesIO(manifest))
        tb = buf.getvalue()
        tb_path, sig_path, tb, sig = self.write_package(tarball_bytes=tb)
        with self.assertRaises(SystemExit) as ctx:
            self.run_upload(s3, self.base_argv(tb_path, sig_path))
        self.assertIn("多个 manifest.json", str(ctx.exception))
        self.assertEqual(s3.put_calls, [])

    def test_unknown_manifest_field_rejected(self):
        s3 = FakeS3()
        tb = make_tarball("photos", "1.0.0", manifest_extra={"totally_unknown_field": 1})
        tb_path, sig_path, tb, sig = self.write_package(tarball_bytes=tb)
        with self.assertRaises(SystemExit) as ctx:
            self.run_upload(s3, self.base_argv(tb_path, sig_path))
        self.assertIn("未知字段", str(ctx.exception))
        self.assertEqual(s3.put_calls, [])

    def test_missing_manifest_json_rejected(self):
        s3 = FakeS3()
        buf = io.BytesIO()
        with tarfile.open(fileobj=buf, mode="w:gz") as tf:
            info = tarfile.TarInfo(name="photos-1.0.0/README.md")
            info.size = 0
            tf.addfile(info, io.BytesIO(b""))
        tb = buf.getvalue()
        tb_path, sig_path, tb, sig = self.write_package(tarball_bytes=tb)
        with self.assertRaises(SystemExit) as ctx:
            self.run_upload(s3, self.base_argv(tb_path, sig_path))
        self.assertIn("manifest.json", str(ctx.exception))
        self.assertEqual(s3.put_calls, [])

    def test_path_traversal_entry_rejected(self):
        s3 = FakeS3()
        manifest = json.dumps({"id": "photos", "version": "1.0.0"}).encode()
        buf = io.BytesIO()
        with tarfile.open(fileobj=buf, mode="w:gz") as tf:
            info = tarfile.TarInfo(name="photos-1.0.0/manifest.json")
            info.size = len(manifest)
            tf.addfile(info, io.BytesIO(manifest))
            evil = tarfile.TarInfo(name="photos-1.0.0/../../etc/passwd")
            evil.size = 0
            tf.addfile(evil, io.BytesIO(b""))
        tb = buf.getvalue()
        tb_path, sig_path, tb, sig = self.write_package(tarball_bytes=tb)
        with self.assertRaises(SystemExit) as ctx:
            self.run_upload(s3, self.base_argv(tb_path, sig_path))
        self.assertIn("..", str(ctx.exception))
        self.assertEqual(s3.put_calls, [])


class TestUploadReadbackVerification(MarketUploadTestBase):
    def test_corrupted_readback_aborts_without_writing_index(self):
        """模拟 put_object 之后 R2 返回的内容被截断/损坏：put_and_verify_readback
        必须在读回字节与本地不一致时中止，且不得写索引。"""

        class CorruptingS3(FakeS3):
            def get_object(self, Bucket, Key):
                result = super().get_object(Bucket, Key)
                if Key.endswith(".tar.gz"):
                    body = result["Body"].read()
                    return {"Body": io.BytesIO(body + b"\x00corrupted")}
                return result

        s3 = CorruptingS3()
        tb_path, sig_path, *_ = self.write_package()

        with self.assertRaises(SystemExit) as ctx:
            self.run_upload(s3, self.base_argv(tb_path, sig_path))
        self.assertIn("读回校验不一致", str(ctx.exception))
        self.assertNotIn(mu.INDEX_KEY, s3.objects, "读回校验失败时绝不能写索引")

    def test_readback_failure_aborts_without_writing_index(self):
        """读回这一步本身报错（比如网络中断）也必须中止，不写索引。"""

        class FlakyS3(FakeS3):
            def get_object(self, Bucket, Key):
                if Key.endswith(".tar.gz"):
                    raise RuntimeError("连接中断")
                return super().get_object(Bucket, Key)

        s3 = FlakyS3()
        tb_path, sig_path, *_ = self.write_package()

        with self.assertRaises(SystemExit) as ctx:
            self.run_upload(s3, self.base_argv(tb_path, sig_path))
        self.assertIn("读回校验失败", str(ctx.exception))
        self.assertNotIn(mu.INDEX_KEY, s3.objects)


class TestOverwriteProtection(MarketUploadTestBase):
    def test_same_version_rejected_by_default(self):
        s3 = FakeS3()
        tb_path, sig_path, tb, sig = self.write_package()
        self.run_upload(s3, self.base_argv(tb_path, sig_path))
        index_before = s3.objects[mu.INDEX_KEY]
        tb_key = self.object_key_for(s3, "photos", "1.0.0", os.path.basename(tb_path))
        tarball_before = s3.objects[tb_key]

        # 重跑同一条命令（同 id+version），不传 --allow-overwrite。
        with self.assertRaises(SystemExit) as ctx:
            self.run_upload(s3, self.base_argv(tb_path, sig_path))
        self.assertIn("默认拒绝覆盖", str(ctx.exception))
        # 索引与已上传对象必须保持原样，不受这次被拒绝的调用影响。
        self.assertEqual(s3.objects[mu.INDEX_KEY], index_before)
        self.assertEqual(s3.objects[tb_key], tarball_before)

    def test_same_version_overwrite_allowed_with_explicit_flag(self):
        s3 = FakeS3()
        tb_path, sig_path, tb1, sig1 = self.write_package()
        self.run_upload(s3, self.base_argv(tb_path, sig_path))

        # 构造内容不同的"新"1.0.0 包（比如修正了签名错误后重签），用
        # --allow-overwrite 重新发布同一版本号。文件名必须仍是
        # <id>-<version>.tar.gz 才能通过版本解析，内容不同所以落到新的
        # sha256 key 下。
        tb2 = make_tarball("photos", "1.0.0", manifest_extra={"icon": "icon.png"})
        sig2 = sign(self.priv, tb2)
        with open(tb_path, "wb") as f:
            f.write(tb2)
        with open(sig_path, "wb") as f:
            f.write(sig2)

        self.run_upload(s3, self.base_argv(tb_path, sig_path, extra=["--allow-overwrite"]))

        index = json.loads(s3.objects[mu.INDEX_KEY])
        app = next(a for a in index["apps"] if a["id"] == "photos")
        self.assertEqual(len(app["versions"]), 1, "覆盖同版本不应产生重复 versions 条目")
        self.assertEqual(app["versions"][0]["sha256"], hashlib.sha256(tb2).hexdigest())

    def test_overwrite_does_not_mutate_old_object_leaves_it_at_old_key(self):
        """要求3核心断言：覆盖发布之后，老版本内容对应的旧 key 必须原样
        保留在 bucket 里，没有被改写或删除；新 key 持有新内容；索引切到
        新 key。"""
        s3 = FakeS3()
        tb_path, sig_path, tb1, sig1 = self.write_package()
        self.run_upload(s3, self.base_argv(tb_path, sig_path))
        old_tb_key = self.object_key_for(s3, "photos", "1.0.0", os.path.basename(tb_path))
        old_sha = hashlib.sha256(tb1).hexdigest()
        self.assertIn(old_sha, old_tb_key)

        tb2 = make_tarball("photos", "1.0.0", manifest_extra={"icon": "icon.png"})
        sig2 = sign(self.priv, tb2)
        with open(tb_path, "wb") as f:
            f.write(tb2)
        with open(sig_path, "wb") as f:
            f.write(sig2)
        self.run_upload(s3, self.base_argv(tb_path, sig_path, extra=["--allow-overwrite"]))

        new_tb_key = self.object_key_for(s3, "photos", "1.0.0", os.path.basename(tb_path))
        self.assertNotEqual(old_tb_key, new_tb_key, "覆盖后对象 key 必须变化（内容 sha256 不同）")
        # 旧 key 对应的对象原样还在，字节未被触碰。
        self.assertIn(old_tb_key, s3.objects)
        self.assertEqual(s3.objects[old_tb_key], tb1)
        # 新 key 对应新内容。
        self.assertEqual(s3.objects[new_tb_key], tb2)
        # 索引指向新 key。
        index = json.loads(s3.objects[mu.INDEX_KEY])
        app = next(a for a in index["apps"] if a["id"] == "photos")
        self.assertEqual(len(app["versions"]), 1)
        self.assertIn(new_tb_key, app["versions"][0]["tarball"])

    def test_overwrite_does_not_affect_other_versions_or_apps(self):
        s3 = FakeS3()
        tb1, sig1, *_ = self.write_package(version="1.0.0")
        self.run_upload(s3, self.base_argv(tb1, sig1, version="1.0.0"))
        tb2, sig2, *_ = self.write_package(version="2.0.0")
        self.run_upload(s3, self.base_argv(tb2, sig2, version="2.0.0"))
        other_tb, other_sig, *_ = self.write_package(app_id="hello", version="1.0.0")
        self.run_upload(s3, self.base_argv(other_tb, other_sig, app_id="hello", version="1.0.0"))

        # 覆盖 photos 1.0.0（内容需不同才能落到新 key，否则沿用同一个 key 并非问题，
        # 但这里重点验证不影响其它条目）。
        tb1b = make_tarball("photos", "1.0.0", manifest_extra={"icon": "icon.png"})
        sig1b = sign(self.priv, tb1b)
        with open(tb1, "wb") as f:
            f.write(tb1b)
        with open(sig1, "wb") as f:
            f.write(sig1b)
        self.run_upload(s3, self.base_argv(tb1, sig1, version="1.0.0", extra=["--allow-overwrite"]))

        index = json.loads(s3.objects[mu.INDEX_KEY])
        photos = next(a for a in index["apps"] if a["id"] == "photos")
        versions = sorted(v["version"] for v in photos["versions"])
        self.assertEqual(versions, ["1.0.0", "2.0.0"], "覆盖 1.0.0 不应影响 2.0.0 条目")
        hello = next(a for a in index["apps"] if a["id"] == "hello")
        self.assertEqual(len(hello["versions"]), 1, "覆盖 photos 不应影响 hello 应用")


class TestPublishLock(MarketUploadTestBase):
    """要求4：并发发布必须被真实的发布锁拦住或安全拒绝，不是只比较一个
    索引条目就假装完成了 CAS。"""

    def test_lock_held_rejects_immediately_without_side_effects(self):
        s3 = FakeS3()
        # 模拟另一次发布正在进行：锁对象已存在且时间戳是"刚刚"。
        s3.objects[mu.LOCK_KEY] = json.dumps({"token": "someone-else", "ts": time.time()}).encode()

        tb_path, sig_path, *_ = self.write_package()
        with self.assertRaises(SystemExit) as ctx:
            self.run_upload(s3, self.base_argv(tb_path, sig_path))
        self.assertIn("发布锁", str(ctx.exception))
        # 锁被占用时必须在读索引/上传资源之前就拒绝，没有任何上传副作用，
        # 也不能动被占用的锁对象本身。
        self.assertEqual(s3.put_calls, [])
        self.assertNotIn(mu.INDEX_KEY, s3.objects)

    def test_lock_released_after_successful_publish_allows_next(self):
        s3 = FakeS3()
        tb_path, sig_path, *_ = self.write_package()
        self.run_upload(s3, self.base_argv(tb_path, sig_path))
        self.assertNotIn(mu.LOCK_KEY, s3.objects, "成功发布后应释放锁")

        # 锁已释放，第二次发布（不同应用）应能正常获取锁并成功。
        tb2, sig2, *_ = self.write_package(app_id="hello", version="1.0.0")
        self.run_upload(s3, self.base_argv(tb2, sig2, app_id="hello", version="1.0.0"))
        index = json.loads(s3.objects[mu.INDEX_KEY])
        ids = sorted(a["id"] for a in index["apps"])
        self.assertEqual(ids, ["hello", "photos"])

    def test_lock_released_after_aborted_publish(self):
        """中途中止（比如默认拒绝覆盖）也要在 finally 里释放锁，不留下
        悬挂锁锁死后续发布。"""
        s3 = FakeS3()
        tb_path, sig_path, *_ = self.write_package()
        self.run_upload(s3, self.base_argv(tb_path, sig_path))
        self.assertNotIn(mu.LOCK_KEY, s3.objects)

        with self.assertRaises(SystemExit):
            self.run_upload(s3, self.base_argv(tb_path, sig_path))  # 同版本，默认拒绝覆盖
        self.assertNotIn(mu.LOCK_KEY, s3.objects, "中止路径也必须释放锁，不能悬挂")

    def test_stale_lock_can_be_preempted(self):
        s3 = FakeS3()
        stale_ts = time.time() - (mu.LOCK_TTL_SECONDS + 60)
        s3.objects[mu.LOCK_KEY] = json.dumps({"token": "long-dead", "ts": stale_ts}).encode()

        tb_path, sig_path, *_ = self.write_package()
        self.run_upload(s3, self.base_argv(tb_path, sig_path))
        self.assertIn(mu.INDEX_KEY, s3.objects)
        self.assertNotIn(mu.LOCK_KEY, s3.objects, "抢占陈旧锁后发布成功应正常释放")

    def test_concurrent_index_change_between_read_and_write_aborts(self):
        """模拟"读索引之后、写索引之前，另一进程已经抢先发布了同一
        id+version"的竞态（发布锁本身无法覆盖的极端时序，比如锁 TTL
        内手工绕过锁直接改了索引）：辅助检测仍必须中止，不覆盖对方的发布。"""
        s3 = FakeS3()
        tb_path, sig_path, *_ = self.write_package(app_id="photos", version="1.0.0")

        def concurrent_writer():
            rival_index = {
                "spec": "1",
                "apps": [{
                    "id": "photos", "name": "照片(并发写入)", "icon": "",
                    "summary": "", "latest": "1.0.0",
                    "versions": [{"version": "1.0.0", "minCoreVersion": "1.5.0",
                                  "tarball": "https://rival/tb", "sha256": "rival",
                                  "sig": "https://rival/sig", "changelog": "", "size": 1}],
                }],
            }
            s3.objects[mu.INDEX_KEY] = json.dumps(rival_index).encode()

        s3._get_hook = concurrent_writer

        with self.assertRaises(SystemExit) as ctx:
            self.run_upload(s3, self.base_argv(tb_path, sig_path))
        self.assertIn("并发修改", str(ctx.exception))

        # 对方的发布必须原样保留，不被本次（已中止的）调用覆盖。
        index = json.loads(s3.objects[mu.INDEX_KEY])
        app = next(a for a in index["apps"] if a["id"] == "photos")
        self.assertEqual(app["versions"][0]["sha256"], "rival")

        # 中止路径也要释放锁。
        self.assertNotIn(mu.LOCK_KEY, s3.objects)


class TestExistingChecksKeepWorking(MarketUploadTestBase):
    def test_invalid_signature_rejected(self):
        s3 = FakeS3()
        tb_path, sig_path, tb, sig = self.write_package()
        other_priv = Ed25519PrivateKey.generate()
        other_pub_b64 = base64.b64encode(
            other_priv.public_key().public_bytes(encoding=Encoding.Raw, format=PublicFormat.Raw)
        ).decode()

        # --pubkey 与 --trusted-pubkey 都设为 other_pub_b64，这样能先通过
        # 信任锚校验、再在真正验签这一步失败（否则会先被信任锚不一致挡住，
        # 测不到"签名校验失败"这条路径）。
        with self.assertRaises(SystemExit) as ctx:
            self.run_upload(s3, self.base_argv(tb_path, sig_path, pubkey=other_pub_b64,
                                                trusted_pubkey=other_pub_b64))
        self.assertIn("签名校验失败", str(ctx.exception))
        self.assertEqual(s3.put_calls, [])

    def test_manifest_id_mismatch_rejected(self):
        s3 = FakeS3()
        tb_path, sig_path, tb, sig = self.write_package(app_id="photos")
        argv = self.base_argv(tb_path, sig_path, app_id="not-photos")
        with self.assertRaises(SystemExit) as ctx:
            self.run_upload(s3, argv)
        self.assertIn("不一致", str(ctx.exception))

    def test_read_index_failure_aborts_without_fallback_to_empty(self):
        class BrokenS3(FakeS3):
            def get_object(self, Bucket, Key):
                if Key == mu.LOCK_KEY:
                    return super().get_object(Bucket, Key)
                raise RuntimeError("权限错误")

        s3 = BrokenS3()
        tb_path, sig_path, *_ = self.write_package()
        with self.assertRaises(SystemExit) as ctx:
            self.run_upload(s3, self.base_argv(tb_path, sig_path))
        self.assertIn("读取线上索引失败", str(ctx.exception))
        # 发布锁在读索引之前获取，锁对象的 put 不算"上传副作用"；真正的
        # 资源对象（tarball/sig）与索引都不应被写入。
        self.assertEqual([k for k in s3.put_calls if k != mu.LOCK_KEY], [])


if __name__ == "__main__":
    unittest.main()
