package apps

import (
	"archive/tar"
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// setupLifecycleTest 打开一个全新的 apps.db（t.TempDir()），注入 fakeExecutor，
// 把包级单例 reg 指向它；返回 mux（裸路由，不经 AdminMiddleware）+ fakeExecutor
// 供断言调用形状。每个测试独立临时目录，互不污染。
func setupLifecycleTest(t *testing.T) (*http.ServeMux, *fakeExecutor) {
	t.Helper()
	db, err := openAppsDB(t.TempDir())
	if err != nil {
		t.Fatalf("openAppsDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	fe := newFakeExecutor()
	// fsRoot 把 MkdirAllChown/RemoveAll/Rename 落到真实磁盘前映射到此临时
	// 目录下，调用记录（fe.calls）里仍保留 /var/lib/nas/apps/...、
	// /data/nas1/... 等原始生产路径供断言，不需要为测试放宽 manifest 的
	// "/data/ 前缀" 校验，也不触达真实 /var/lib/nas 或 /data。
	fe.fsRoot = t.TempDir()
	reg = &registry{db: db, dataDir: t.TempDir(), executor: fe}

	mux := http.NewServeMux()
	RegisterRoutesBare(mux)
	return mux, fe
}

// devPrivateKey 读取入仓的开发私钥，供测试给包签名。
func devPrivateKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	b, err := os.ReadFile("testdata/devkey/dev.key")
	if err != nil {
		t.Fatalf("读取开发私钥失败: %v", err)
	}
	return ed25519.PrivateKey(b)
}

// buildSignedPackage 用给定条目构造 tar.gz 并用开发私钥签名，返回
// (tarball, base64签名)。
func buildSignedPackage(t *testing.T, entries []tarEntry) ([]byte, string) {
	t.Helper()
	tb := buildTarGz(t, entries)
	sig := ed25519.Sign(devPrivateKey(t), tb)
	return tb, base64.StdEncoding.EncodeToString(sig)
}

// multipartInstallBody 构造 install/upgrade 接口所需的 multipart 表单
// （package 文件字段 + signature 字段）。
func multipartInstallBody(t *testing.T, tarball []byte, sigB64 string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("package", "app.tar.gz")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(tarball); err != nil {
		t.Fatalf("write package: %v", err)
	}
	if err := w.WriteField("signature", sigB64); err != nil {
		t.Fatalf("WriteField signature: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &buf, w.FormDataContentType()
}

// appEntriesWithID 基于 validAppEntries() 产出一套合法包条目，替换 id/version
// 为给定值（manifest.json 内容 + tar 根目录前缀同步改名）。manifest 里
// storage 路径固定写死的 "/data/nas1/photos" 保持不变（校验要求必须以
// /data/ 开头），真实落盘前由 fakeExecutor.fsRoot 重定向到临时目录。
func appEntriesWithID(id, version string) []tarEntry {
	root := id + "-" + version + "/"
	manifest := strings.Replace(validManifestJSON, `"id": "photos",`, fmt.Sprintf(`"id": %q,`, id), 1)
	manifest = strings.Replace(manifest, `"version": "1.0.0",`, fmt.Sprintf(`"version": %q,`, version), 1)
	return []tarEntry{
		{name: root, typeflag: tar.TypeDir},
		{name: root + "manifest.json", content: []byte(manifest)},
		{name: root + "icon.png", content: validPNG},
		{name: root + "unit.tpl", content: []byte("[Unit]\nDescription=" + id + "\n\n[Service]\nExecStart=/app/main\n")},
		{name: root + "app/", typeflag: tar.TypeDir},
		{name: root + "app/main", content: []byte("#!/bin/sh\necho hi\n")},
		{name: root + "README.md", content: []byte("# " + id + "\n")},
	}
}

// setUpgradeHealthzTimeoutForTest 临时把包级 upgradeHealthzTimeout 调小，
// 让"healthz 永不通过"的回滚场景无需真的等 90s；返回的函数用于恢复原值。
func setUpgradeHealthzTimeoutForTest(t *testing.T, d time.Duration) func() {
	t.Helper()
	prev := upgradeHealthzTimeout
	upgradeHealthzTimeout = d
	return func() { upgradeHealthzTimeout = prev }
}

func doRequest(mux *http.ServeMux, method, path string, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, path, body)
		r.Header.Set("Content-Type", contentType)
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec
}

func TestInstallTwoStepFlow_PendingThenConfirmed(t *testing.T) {
	mux, fe := setupLifecycleTest(t)

	tb, sig := buildSignedPackage(t, appEntriesWithID("photos", "1.0.0"))
	body, ct := multipartInstallBody(t, tb, sig)

	rec := doRequest(mux, http.MethodPost, "/api/apps/install", body, ct)
	if rec.Code != http.StatusOK {
		t.Fatalf("install step1: %d body=%s", rec.Code, rec.Body.String())
	}
	var pending pendingInstallResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &pending); err != nil {
		t.Fatalf("decode pending response: %v", err)
	}
	if pending.ID != "photos" || pending.Status != statusPending {
		t.Fatalf("pending response mismatch: %+v", pending)
	}
	if pending.Port < minPort || pending.Port > maxPort {
		t.Fatalf("allocated port out of range: %d", pending.Port)
	}

	// 未 confirm 前不应已经建用户/建 unit。
	if len(fe.calls) != 0 {
		t.Fatalf("install step1 不应产生任何 Executor 副作用调用，got: %v", fe.calls)
	}

	rec = doRequest(mux, http.MethodPost, "/api/apps/install/photos/confirm", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("install confirm: %d body=%s", rec.Code, rec.Body.String())
	}
	var detail appDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Status != statusInstalled {
		t.Fatalf("confirm 后台账状态应为 installed，got %q", detail.Status)
	}
	if detail.ActiveState != "active" {
		t.Fatalf("confirm 后真实运行态应为 active，got %q", detail.ActiveState)
	}

	wantCalls := []string{
		"useradd -r -s /usr/sbin/nologin nasapp-photos",
		"nas-app-provision.sh provision photos fm",
		"nas-app-provision.sh chown-app photos /data/nas1/photos",
		"systemctl daemon-reload",
		"systemctl enable nas-app-photos.service",
		"systemctl start nas-app-photos.service",
	}
	for _, want := range wantCalls {
		found := false
		for _, c := range fe.calls {
			if c == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("confirm 流程应包含调用 %q，got: %v", want, fe.calls)
		}
	}
}

func TestInstall_RejectsBadSignature(t *testing.T) {
	mux, fe := setupLifecycleTest(t)
	tb := buildTarGz(t, appEntriesWithID("photos", "1.0.0"))
	body, ct := multipartInstallBody(t, tb, base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)))

	rec := doRequest(mux, http.MethodPost, "/api/apps/install", body, ct)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("签名错误应 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(fe.calls) != 0 {
		t.Fatalf("签名校验失败不应产生任何副作用调用，got: %v", fe.calls)
	}
}

func TestInstall_IdempotentRejectAfterConfirm(t *testing.T) {
	mux, _ := setupLifecycleTest(t)
	tb, sig := buildSignedPackage(t, appEntriesWithID("photos", "1.0.0"))
	body, ct := multipartInstallBody(t, tb, sig)
	if rec := doRequest(mux, http.MethodPost, "/api/apps/install", body, ct); rec.Code != http.StatusOK {
		t.Fatalf("install: %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(mux, http.MethodPost, "/api/apps/install/photos/confirm", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("confirm: %d body=%s", rec.Code, rec.Body.String())
	}

	tb2, sig2 := buildSignedPackage(t, appEntriesWithID("photos", "1.0.0"))
	body2, ct2 := multipartInstallBody(t, tb2, sig2)
	rec := doRequest(mux, http.MethodPost, "/api/apps/install", body2, ct2)
	if rec.Code != http.StatusConflict {
		t.Fatalf("已安装应用重复 install 应 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestInstall_PendingReuploadOverwritesPreviousPending(t *testing.T) {
	mux, _ := setupLifecycleTest(t)
	tb, sig := buildSignedPackage(t, appEntriesWithID("photos", "1.0.0"))
	body, ct := multipartInstallBody(t, tb, sig)
	if rec := doRequest(mux, http.MethodPost, "/api/apps/install", body, ct); rec.Code != http.StatusOK {
		t.Fatalf("install #1: %d body=%s", rec.Code, rec.Body.String())
	}

	// 未 confirm，重新上传同一 id：应覆盖而不是 409。
	tb2, sig2 := buildSignedPackage(t, appEntriesWithID("photos", "1.0.0"))
	body2, ct2 := multipartInstallBody(t, tb2, sig2)
	rec := doRequest(mux, http.MethodPost, "/api/apps/install", body2, ct2)
	if rec.Code != http.StatusOK {
		t.Fatalf("pending 态重新上传应允许覆盖, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStartStopRestart_RealActiveStateReflected(t *testing.T) {
	mux, fe := setupLifecycleTest(t)
	tb, sig := buildSignedPackage(t, appEntriesWithID("photos", "1.0.0"))
	body, ct := multipartInstallBody(t, tb, sig)
	doRequest(mux, http.MethodPost, "/api/apps/install", body, ct)
	doRequest(mux, http.MethodPost, "/api/apps/install/photos/confirm", nil, "")

	rec := doRequest(mux, http.MethodPost, "/api/apps/photos/stop", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("stop: %d body=%s", rec.Code, rec.Body.String())
	}
	if fe.UnitActiveState(appUnitName("photos")) != "inactive" {
		t.Fatal("stop 后真实状态应为 inactive")
	}

	rec = doRequest(mux, http.MethodGet, "/api/apps/photos", nil, "")
	var detail appDetail
	json.Unmarshal(rec.Body.Bytes(), &detail)
	if detail.ActiveState != "inactive" {
		t.Fatalf("详情接口应反映真实运行态 inactive，got %q", detail.ActiveState)
	}

	rec = doRequest(mux, http.MethodPost, "/api/apps/photos/start", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("start: %d body=%s", rec.Code, rec.Body.String())
	}
	rec = doRequest(mux, http.MethodPost, "/api/apps/photos/restart", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("restart: %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDelete_RemovesUserUnitAndPortButKeepsStoragePaths(t *testing.T) {
	mux, fe := setupLifecycleTest(t)
	tb, sig := buildSignedPackage(t, appEntriesWithID("photos", "1.0.0"))
	body, ct := multipartInstallBody(t, tb, sig)
	doRequest(mux, http.MethodPost, "/api/apps/install", body, ct)
	doRequest(mux, http.MethodPost, "/api/apps/install/photos/confirm", nil, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/apps/photos", strings.NewReader(`{"keepData":true}`))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d body=%s", rec.Code, rec.Body.String())
	}

	found := false
	for _, c := range fe.calls {
		if c == "userdel nasapp-photos" {
			found = true
		}
	}
	if !found {
		t.Errorf("卸载应调用 userdel，got: %v", fe.calls)
	}

	rec = doRequest(mux, http.MethodGet, "/api/apps/photos", nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("卸载后应台账查不到: %d", rec.Code)
	}

	// 端口释放后可被新安装复用。
	tb2, sig2 := buildSignedPackage(t, appEntriesWithID("gallery", "1.0.0"))
	body2, ct2 := multipartInstallBody(t, tb2, sig2)
	rec = doRequest(mux, http.MethodPost, "/api/apps/install", body2, ct2)
	if rec.Code != http.StatusOK {
		t.Fatalf("install after delete: %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestDelete_PendingApp_CleansUpWithoutProvisioningSideEffects 覆盖 M1-6b
// 真机验收发现的死锁 bug：install 第一步（验签/暂存/分配端口）成功但
// confirm 未执行（或执行前用户放弃）时，应用台账停在 pending，此时没有
// 任何用户/目录/unit 被创建过。此前 DELETE 对 pending 应用走的是"已安装
// 应用"的完整清理路径（StopUnit/RemoveAppDirs/DelAppUser...），卡在
// 不存在的 unit/目录上导致无法通过面板删除，只能手工改数据库才能摆脱
// pending 态——本测试断言 DELETE 能正确清理且不产生任何 provision 类调用。
func TestDelete_PendingApp_CleansUpWithoutProvisioningSideEffects(t *testing.T) {
	mux, fe := setupLifecycleTest(t)
	tb, sig := buildSignedPackage(t, appEntriesWithID("photos", "1.0.0"))
	body, ct := multipartInstallBody(t, tb, sig)
	rec := doRequest(mux, http.MethodPost, "/api/apps/install", body, ct)
	if rec.Code != http.StatusOK {
		t.Fatalf("install step1: %d body=%s", rec.Code, rec.Body.String())
	}
	var pending pendingInstallResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &pending); err != nil {
		t.Fatalf("decode pending response: %v", err)
	}

	// install 第一步不应产生任何 Executor 副作用（与
	// TestInstallTwoStepFlow_PendingThenConfirmed 的断言一致）。
	if len(fe.calls) != 0 {
		t.Fatalf("install step1 不应产生任何 Executor 副作用调用，got: %v", fe.calls)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/apps/photos", strings.NewReader(`{"keepData":false}`))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete pending: %d body=%s", rec.Code, rec.Body.String())
	}

	// pending 清理不涉及任何 provision 相关的 Executor 调用（没建过东西，
	// 也没什么可删/可停）。
	if len(fe.calls) != 0 {
		t.Fatalf("pending 态 DELETE 不应产生任何 Executor 调用，got: %v", fe.calls)
	}

	// 台账行应已清除。
	rec = doRequest(mux, http.MethodGet, "/api/apps/photos", nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("pending 删除后应台账查不到: %d", rec.Code)
	}

	// 暂存的 tarball 应已清理。
	if _, err := os.Stat(stagedTarballPath(reg.dataDir, "photos")); !os.IsNotExist(err) {
		t.Fatalf("pending 删除后暂存 tarball 应已清理，stat err=%v", err)
	}

	// 端口释放后可被新安装复用。
	tb2, sig2 := buildSignedPackage(t, appEntriesWithID("gallery", "1.0.0"))
	body2, ct2 := multipartInstallBody(t, tb2, sig2)
	rec = doRequest(mux, http.MethodPost, "/api/apps/install", body2, ct2)
	if rec.Code != http.StatusOK {
		t.Fatalf("install after pending delete: %d body=%s", rec.Code, rec.Body.String())
	}
	var pending2 pendingInstallResponse
	json.Unmarshal(rec.Body.Bytes(), &pending2)
	if pending2.Port != pending.Port {
		t.Errorf("端口应被释放复用，原端口 %d，新分配 %d", pending.Port, pending2.Port)
	}
}

func TestUpgrade_SuccessPath(t *testing.T) {
	mux, fe := setupLifecycleTest(t)
	tb, sig := buildSignedPackage(t, appEntriesWithID("photos", "1.0.0"))
	body, ct := multipartInstallBody(t, tb, sig)
	doRequest(mux, http.MethodPost, "/api/apps/install", body, ct)
	doRequest(mux, http.MethodPost, "/api/apps/install/photos/confirm", nil, "")

	tb2, sig2 := buildSignedPackage(t, appEntriesWithID("photos", "1.1.0"))
	body2, ct2 := multipartInstallBody(t, tb2, sig2)
	rec := doRequest(mux, http.MethodPost, "/api/apps/photos/upgrade", body2, ct2)
	if rec.Code != http.StatusOK {
		t.Fatalf("upgrade: %d body=%s", rec.Code, rec.Body.String())
	}
	var detail appDetail
	json.Unmarshal(rec.Body.Bytes(), &detail)
	if detail.Version != "1.1.0" {
		t.Fatalf("升级后版本应为 1.1.0, got %q", detail.Version)
	}

	backupLeft := false
	for _, c := range fe.calls {
		if strings.HasPrefix(c, "rm -rf") && strings.Contains(c, ".bak-") {
			backupLeft = true
		}
	}
	if !backupLeft {
		t.Errorf("升级成功后应清理 .bak 备份目录，got: %v", fe.calls)
	}
}

func TestUpgrade_RollbackOnHealthzFailure(t *testing.T) {
	mux, fe := setupLifecycleTest(t)
	tb, sig := buildSignedPackage(t, appEntriesWithID("photos", "1.0.0"))
	body, ct := multipartInstallBody(t, tb, sig)
	doRequest(mux, http.MethodPost, "/api/apps/install", body, ct)
	doRequest(mux, http.MethodPost, "/api/apps/install/photos/confirm", nil, "")

	// 升级后 restart 看似成功，但新版本 healthz 永不通过：令 fakeExecutor
	// 的 CheckHealthz 恒失败，配合极短超时断言走到回滚分支，不必真的等 90s；
	// upgradeHealthzTimeout 是包内变量，测试通过临时覆盖验证行为。
	fe.failers["CheckHealthz"] = fmt.Errorf("模拟新版本 healthz 永不通过")
	restore := setUpgradeHealthzTimeoutForTest(t, 50*time.Millisecond)
	defer restore()

	tb2, sig2 := buildSignedPackage(t, appEntriesWithID("photos", "1.1.0"))
	body2, ct2 := multipartInstallBody(t, tb2, sig2)
	rec := doRequest(mux, http.MethodPost, "/api/apps/photos/upgrade", body2, ct2)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("healthz 失败应触发回滚并返回 500, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "已自动回滚") {
		t.Fatalf("错误信息应说明已自动回滚, got %s", rec.Body.String())
	}

	// 回滚后台账应仍是旧版本。
	rec = doRequest(mux, http.MethodGet, "/api/apps/photos", nil, "")
	var detail appDetail
	json.Unmarshal(rec.Body.Bytes(), &detail)
	if detail.Version != "1.0.0" {
		t.Fatalf("回滚后台账版本应保持 1.0.0, got %q", detail.Version)
	}

	restartedOld := false
	for _, c := range fe.calls {
		if c == "systemctl restart nas-app-photos.service" {
			restartedOld = true
		}
	}
	if !restartedOld {
		t.Errorf("回滚流程应重启旧版本 unit，got: %v", fe.calls)
	}
}

func TestUpgrade_RejectsBelowMinUpgradeFrom(t *testing.T) {
	mux, _ := setupLifecycleTest(t)
	tb, sig := buildSignedPackage(t, appEntriesWithID("photos", "0.5.0"))
	body, ct := multipartInstallBody(t, tb, sig)
	doRequest(mux, http.MethodPost, "/api/apps/install", body, ct)
	doRequest(mux, http.MethodPost, "/api/apps/install/photos/confirm", nil, "")

	// 新包要求 minUpgradeFrom=1.0.0（validManifestJSON 固定值），已装 0.5.0 低于它。
	tb2, sig2 := buildSignedPackage(t, appEntriesWithID("photos", "2.0.0"))
	body2, ct2 := multipartInstallBody(t, tb2, sig2)
	rec := doRequest(mux, http.MethodPost, "/api/apps/photos/upgrade", body2, ct2)
	if rec.Code != http.StatusConflict {
		t.Fatalf("低于 minUpgradeFrom 应 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}
