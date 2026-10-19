package vault

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nas-panel/common"
)

// ─────────────────────────────────────────────────────────────
// 测试基础设施：每个测试用 t.TempDir() 独立数据库，不污染彼此；
// common.InitAuth 只需全局初始化一次（JWT secret 不涉及 NAS_DATA_DIR）。
// ─────────────────────────────────────────────────────────────

func setupTest(t *testing.T) *http.ServeMux {
	t.Helper()
	t.Setenv("NAS_DATA_DIR", t.TempDir())
	ResetForTest()
	t.Cleanup(ResetForTest)
	SetNowForTest(nil)
	t.Cleanup(func() { SetNowForTest(nil) })

	common.InitAuth("test-secret-vault")

	mux := http.NewServeMux()
	RegisterRoutes(mux)
	return mux
}

func authHeader(t *testing.T, username string) string {
	t.Helper()
	tok, err := common.CreateToken(username)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	return "Bearer " + tok
}

func doForm(t *testing.T, mux *http.ServeMux, method, path, bearer string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var body strings.Reader
	if form != nil {
		body = *strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, &body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if bearer != "" {
		req.Header.Set("Authorization", bearer)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func doGet(t *testing.T, mux *http.ServeMux, path, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", bearer)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func jsonBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("bad json: %v, body=%s", err, rec.Body.String())
	}
	return m
}

const validSalt = "0123456789abcdef0123456789abcdef"
const validHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func setupVault(t *testing.T, mux *http.ServeMux, bearer string) {
	t.Helper()
	rec := doForm(t, mux, http.MethodPost, "/api/vault/setup", bearer, url.Values{
		"salt":        {validSalt},
		"verify_hash": {validHash},
		"iterations":  {"600000"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("setup failed: %d %s", rec.Code, rec.Body.String())
	}
}

// ═══ setup ═══

func TestSetupSuccess(t *testing.T) {
	mux := setupTest(t)
	bearer := authHeader(t, "alice")
	rec := doForm(t, mux, http.MethodPost, "/api/vault/setup", bearer, url.Values{
		"salt":        {validSalt},
		"verify_hash": {validHash},
		"iterations":  {"600000"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSetupDuplicateReturns409(t *testing.T) {
	mux := setupTest(t)
	bearer := authHeader(t, "alice")
	setupVault(t, mux, bearer)

	rec := doForm(t, mux, http.MethodPost, "/api/vault/setup", bearer, url.Values{
		"salt":        {validSalt},
		"verify_hash": {validHash},
		"iterations":  {"600000"},
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSetupInvalidParams(t *testing.T) {
	mux := setupTest(t)
	bearer := authHeader(t, "alice")

	cases := []struct {
		name       string
		salt       string
		verifyHash string
		iterations string
	}{
		{"salt 太短", "0123abcd", validHash, "600000"},
		{"salt 非十六进制", strings.Repeat("g", 32), validHash, "600000"},
		{"verify_hash 太短", validSalt, "0123abcd", "600000"},
		{"verify_hash 非十六进制", validSalt, strings.Repeat("g", 64), "600000"},
		{"iterations 太小", validSalt, validHash, "1000"},
		{"iterations 太大", validSalt, validHash, "99999999"},
		{"iterations 非数字", validSalt, validHash, "abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doForm(t, mux, http.MethodPost, "/api/vault/setup", bearer, url.Values{
				"salt":        {tc.salt},
				"verify_hash": {tc.verifyHash},
				"iterations":  {tc.iterations},
			})
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s: want 400, got %d: %s", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestStatusReflectsSetup(t *testing.T) {
	mux := setupTest(t)
	bearer := authHeader(t, "alice")

	rec := doGet(t, mux, "/api/vault/status", bearer)
	body := jsonBody(t, rec)
	if body["setup"] != false {
		t.Fatalf("未 setup 时 setup 应为 false: %+v", body)
	}

	setupVault(t, mux, bearer)
	rec = doGet(t, mux, "/api/vault/status", bearer)
	body = jsonBody(t, rec)
	if body["setup"] != true {
		t.Fatalf("已 setup 时 setup 应为 true: %+v", body)
	}
	if body["iterations"].(float64) != 600000 {
		t.Fatalf("iterations 应回显: %+v", body)
	}
}

// ═══ unlock ═══

func TestUnlockCorrectHash(t *testing.T) {
	mux := setupTest(t)
	bearer := authHeader(t, "alice")
	setupVault(t, mux, bearer)

	rec := doForm(t, mux, http.MethodPost, "/api/vault/unlock", bearer, url.Values{"verify_hash": {validHash}})
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUnlockWrongHash(t *testing.T) {
	mux := setupTest(t)
	bearer := authHeader(t, "alice")
	setupVault(t, mux, bearer)

	rec := doForm(t, mux, http.MethodPost, "/api/vault/unlock", bearer, url.Values{"verify_hash": {strings.Repeat("f", 64)}})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d: %s", rec.Code, rec.Body.String())
	}
	body := jsonBody(t, rec)
	if body["attempts_left"].(float64) != 4 {
		t.Fatalf("第一次失败后应剩 4 次, got %+v", body)
	}
}

func TestUnlockFiveFailuresLocks(t *testing.T) {
	mux := setupTest(t)
	bearer := authHeader(t, "alice")
	setupVault(t, mux, bearer)

	wrong := strings.Repeat("f", 64)
	for i := 0; i < maxFailed-1; i++ {
		rec := doForm(t, mux, http.MethodPost, "/api/vault/unlock", bearer, url.Values{"verify_hash": {wrong}})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次失败应 401, got %d", i+1, rec.Code)
		}
	}
	// 第 5 次失败 → 锁定
	rec := doForm(t, mux, http.MethodPost, "/api/vault/unlock", bearer, url.Values{"verify_hash": {wrong}})
	if rec.Code != http.StatusLocked {
		t.Fatalf("第 5 次失败应 423, got %d: %s", rec.Code, rec.Body.String())
	}

	// 锁定期间，即使口令正确也拒绝
	rec = doForm(t, mux, http.MethodPost, "/api/vault/unlock", bearer, url.Values{"verify_hash": {validHash}})
	if rec.Code != http.StatusLocked {
		t.Fatalf("锁定期内正确口令也应 423, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUnlockLockExpiresAndRecovers(t *testing.T) {
	mux := setupTest(t)
	bearer := authHeader(t, "alice")
	setupVault(t, mux, bearer)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	SetNowForTest(func() time.Time { return base })

	wrong := strings.Repeat("f", 64)
	for i := 0; i < maxFailed; i++ {
		doForm(t, mux, http.MethodPost, "/api/vault/unlock", bearer, url.Values{"verify_hash": {wrong}})
	}
	// 确认已锁定
	rec := doForm(t, mux, http.MethodPost, "/api/vault/unlock", bearer, url.Values{"verify_hash": {validHash}})
	if rec.Code != http.StatusLocked {
		t.Fatalf("应处于锁定状态, got %d", rec.Code)
	}

	// 时间前进到锁定过期之后
	SetNowForTest(func() time.Time { return base.Add(lockDuration + time.Minute) })
	rec = doForm(t, mux, http.MethodPost, "/api/vault/unlock", bearer, url.Values{"verify_hash": {validHash}})
	if rec.Code != http.StatusOK {
		t.Fatalf("锁定过期后正确口令应放行, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUnlockSuccessClearsFailedCount(t *testing.T) {
	mux := setupTest(t)
	bearer := authHeader(t, "alice")
	setupVault(t, mux, bearer)

	wrong := strings.Repeat("f", 64)
	// 失败两次
	doForm(t, mux, http.MethodPost, "/api/vault/unlock", bearer, url.Values{"verify_hash": {wrong}})
	doForm(t, mux, http.MethodPost, "/api/vault/unlock", bearer, url.Values{"verify_hash": {wrong}})
	// 成功一次，清零
	rec := doForm(t, mux, http.MethodPost, "/api/vault/unlock", bearer, url.Values{"verify_hash": {validHash}})
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	// 再失败 4 次不应锁定（计数已清零，第 5 次才锁）
	for i := 0; i < maxFailed-1; i++ {
		rec = doForm(t, mux, http.MethodPost, "/api/vault/unlock", bearer, url.Values{"verify_hash": {wrong}})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次应仍是 401（未锁）, got %d", i+1, rec.Code)
		}
	}
}

func TestUnlockWithoutSetupReturns400(t *testing.T) {
	mux := setupTest(t)
	bearer := authHeader(t, "alice")
	rec := doForm(t, mux, http.MethodPost, "/api/vault/unlock", bearer, url.Values{"verify_hash": {validHash}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未 setup 时 unlock 应 400, got %d", rec.Code)
	}
}

// ═══ items CRUD + owner 隔离 ═══

func createItem(t *testing.T, mux *http.ServeMux, bearer, name, encrypted string) {
	t.Helper()
	rec := doForm(t, mux, http.MethodPost, "/api/vault/create", bearer, url.Values{
		"name":          {name},
		"category":      {"test"},
		"url":           {""},
		"username":      {"u1"},
		"notes_preview": {""},
		"encrypted":     {encrypted},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("createItem failed: %d %s", rec.Code, rec.Body.String())
	}
}

func TestItemsCreateRequiresSetup(t *testing.T) {
	mux := setupTest(t)
	bearer := authHeader(t, "alice")
	rec := doForm(t, mux, http.MethodPost, "/api/vault/create", bearer, url.Values{
		"name":      {"github"},
		"encrypted": {"ciphertext"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未 setup 时新增条目应 400, got %d", rec.Code)
	}
}

func TestItemsCreateValidation(t *testing.T) {
	mux := setupTest(t)
	bearer := authHeader(t, "alice")
	setupVault(t, mux, bearer)

	cases := []struct {
		name      string
		itemName  string
		encrypted string
	}{
		{"名称为空", "", "ciphertext"},
		{"名称超长", strings.Repeat("x", maxNameLen+1), "ciphertext"},
		{"加密内容为空", "github", ""},
		{"加密内容超长", "github", strings.Repeat("x", maxEncLen+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doForm(t, mux, http.MethodPost, "/api/vault/create", bearer, url.Values{
				"name":      {tc.itemName},
				"encrypted": {tc.encrypted},
			})
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s: want 400, got %d", tc.name, rec.Code)
			}
		})
	}
}

func TestItemsOwnerIsolationList(t *testing.T) {
	mux := setupTest(t)
	aliceBearer := authHeader(t, "alice")
	bobBearer := authHeader(t, "bob")
	setupVault(t, mux, aliceBearer)
	setupVault(t, mux, bobBearer)

	createItem(t, mux, aliceBearer, "alice-item", "enc-a")
	createItem(t, mux, bobBearer, "bob-item", "enc-b")

	rec := doGet(t, mux, "/api/vault/items", aliceBearer)
	body := jsonBody(t, rec)
	items := body["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("alice 应只看到自己的 1 条, got %d", len(items))
	}
	first := items[0].(map[string]interface{})
	if first["name"] != "alice-item" {
		t.Fatalf("alice 看到了错误条目: %+v", first)
	}

	rec = doGet(t, mux, "/api/vault/items", bobBearer)
	body = jsonBody(t, rec)
	items = body["items"].([]interface{})
	if len(items) != 1 || items[0].(map[string]interface{})["name"] != "bob-item" {
		t.Fatalf("bob 应只看到自己的条目: %+v", body)
	}
}

func TestItemsOwnerIsolationUpdateDelete(t *testing.T) {
	mux := setupTest(t)
	aliceBearer := authHeader(t, "alice")
	bobBearer := authHeader(t, "bob")
	setupVault(t, mux, aliceBearer)
	setupVault(t, mux, bobBearer)
	createItem(t, mux, aliceBearer, "alice-item", "enc-a")

	// 拿到 alice 条目的 id
	rec := doGet(t, mux, "/api/vault/items", aliceBearer)
	body := jsonBody(t, rec)
	items := body["items"].([]interface{})
	id := int(items[0].(map[string]interface{})["id"].(float64))

	// bob 尝试更新 alice 的条目 → 404（不存在于 bob 的范围内）
	rec = doForm(t, mux, http.MethodPost, "/api/vault/update", bobBearer, url.Values{
		"id":        {fmt.Sprintf("%d", id)},
		"name":      {"hacked"},
		"encrypted": {"enc-hacked"},
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bob 更新 alice 条目应 404, got %d: %s", rec.Code, rec.Body.String())
	}

	// bob 尝试删除 alice 的条目 → 404
	rec = doForm(t, mux, http.MethodPost, "/api/vault/delete", bobBearer, url.Values{"id": {fmt.Sprintf("%d", id)}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bob 删除 alice 条目应 404, got %d: %s", rec.Code, rec.Body.String())
	}

	// alice 自己更新成功
	rec = doForm(t, mux, http.MethodPost, "/api/vault/update", aliceBearer, url.Values{
		"id":        {fmt.Sprintf("%d", id)},
		"name":      {"renamed"},
		"encrypted": {"enc-renamed"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("alice 更新自己条目应成功, got %d: %s", rec.Code, rec.Body.String())
	}

	// alice 自己删除成功
	rec = doForm(t, mux, http.MethodPost, "/api/vault/delete", aliceBearer, url.Values{"id": {fmt.Sprintf("%d", id)}})
	if rec.Code != http.StatusOK {
		t.Fatalf("alice 删除自己条目应成功, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ═══ change-passphrase（事务性） ═══

func TestChangePassphraseSuccess(t *testing.T) {
	mux := setupTest(t)
	bearer := authHeader(t, "alice")
	setupVault(t, mux, bearer)
	createItem(t, mux, bearer, "item1", "old-enc-1")
	createItem(t, mux, bearer, "item2", "old-enc-2")

	rec := doGet(t, mux, "/api/vault/items", bearer)
	body := jsonBody(t, rec)
	items := body["items"].([]interface{})
	var payload []changePassItem
	for _, raw := range items {
		m := raw.(map[string]interface{})
		payload = append(payload, changePassItem{ID: int(m["id"].(float64)), Encrypted: "new-enc-" + m["name"].(string)})
	}
	payloadJSON, _ := json.Marshal(payload)

	newSalt := strings.Repeat("a", 32)
	newHash := strings.Repeat("b", 64)
	rec = doForm(t, mux, http.MethodPost, "/api/vault/change-passphrase", bearer, url.Values{
		"salt":        {newSalt},
		"verify_hash": {newHash},
		"iterations":  {"700000"},
		"items":       {string(payloadJSON)},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("change-passphrase 应成功, got %d: %s", rec.Code, rec.Body.String())
	}

	// 新口令能解锁
	rec = doForm(t, mux, http.MethodPost, "/api/vault/unlock", bearer, url.Values{"verify_hash": {newHash}})
	if rec.Code != http.StatusOK {
		t.Fatalf("新口令应能 unlock, got %d", rec.Code)
	}
	// 旧口令不再有效
	rec = doForm(t, mux, http.MethodPost, "/api/vault/unlock", bearer, url.Values{"verify_hash": {validHash}})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("旧口令应失效, got %d", rec.Code)
	}
}

// 一条 id 不属于 owner → 整体回滚：meta 不变、其它条目密文不变。
func TestChangePassphraseRollsBackOnForeignItem(t *testing.T) {
	mux := setupTest(t)
	aliceBearer := authHeader(t, "alice")
	bobBearer := authHeader(t, "bob")
	setupVault(t, mux, aliceBearer)
	setupVault(t, mux, bobBearer)
	createItem(t, mux, aliceBearer, "alice-item", "alice-enc")
	createItem(t, mux, bobBearer, "bob-item", "bob-enc")

	// 取得 bob 条目 id
	rec := doGet(t, mux, "/api/vault/items", bobBearer)
	body := jsonBody(t, rec)
	bobID := int(body["items"].([]interface{})[0].(map[string]interface{})["id"].(float64))

	// 取得 alice 条目 id
	rec = doGet(t, mux, "/api/vault/items", aliceBearer)
	body = jsonBody(t, rec)
	aliceID := int(body["items"].([]interface{})[0].(map[string]interface{})["id"].(float64))

	payload := []changePassItem{
		{ID: aliceID, Encrypted: "alice-new-enc"},
		{ID: bobID, Encrypted: "injected-enc"}, // 不属于 alice
	}
	payloadJSON, _ := json.Marshal(payload)

	newSalt := strings.Repeat("a", 32)
	newHash := strings.Repeat("b", 64)
	rec = doForm(t, mux, http.MethodPost, "/api/vault/change-passphrase", aliceBearer, url.Values{
		"salt":        {newSalt},
		"verify_hash": {newHash},
		"iterations":  {"700000"},
		"items":       {string(payloadJSON)},
	})
	if rec.Code == http.StatusOK {
		t.Fatalf("混入他人条目应失败回滚, got 200")
	}

	// alice 的旧口令应仍有效（未被改）
	rec = doForm(t, mux, http.MethodPost, "/api/vault/unlock", aliceBearer, url.Values{"verify_hash": {validHash}})
	if rec.Code != http.StatusOK {
		t.Fatalf("回滚后 alice 旧口令应仍有效, got %d", rec.Code)
	}

	// alice 条目密文应未被更新
	rec = doGet(t, mux, "/api/vault/items", aliceBearer)
	body = jsonBody(t, rec)
	enc := body["items"].([]interface{})[0].(map[string]interface{})["encrypted"].(string)
	if enc != "alice-enc" {
		t.Fatalf("回滚后 alice 条目密文应不变, got %q", enc)
	}

	// bob 的条目也应未被影响
	rec = doGet(t, mux, "/api/vault/items", bobBearer)
	body = jsonBody(t, rec)
	enc = body["items"].([]interface{})[0].(map[string]interface{})["encrypted"].(string)
	if enc != "bob-enc" {
		t.Fatalf("回滚后 bob 条目密文应不变, got %q", enc)
	}
}

// ═══ legacy purge 前置条件 ═══

func TestLegacyPurgeRequiresNewVaultSetup(t *testing.T) {
	mux := setupTest(t)
	bearer := authHeader(t, "alice")
	rec := doForm(t, mux, http.MethodPost, "/api/vault/legacy/purge", bearer, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("新保险箱未 setup 时 purge 应 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLegacyPurgeSucceedsAfterSetup(t *testing.T) {
	mux := setupTest(t)
	bearer := authHeader(t, "alice")
	setupVault(t, mux, bearer)
	rec := doForm(t, mux, http.MethodPost, "/api/vault/legacy/purge", bearer, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup 后 purge 应成功, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ═══ 纯函数单测 ═══

func TestIsLocked(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if isLocked("", now) {
		t.Error("空字符串不应算锁定")
	}
	if !isLocked("not-a-time", now) {
		t.Error("坏数据应视为锁定中（fail-safe，审查 M3：宁可多锁不可漏放）")
	}
	future := now.Add(time.Hour).Format(time.RFC3339)
	if !isLocked(future, now) {
		t.Error("未来时间应算锁定")
	}
	past := now.Add(-time.Hour).Format(time.RFC3339)
	if isLocked(past, now) {
		t.Error("过去时间不应算锁定")
	}
}

func TestValidSetupParams(t *testing.T) {
	if err := validSetupParams(validSalt, validHash, 600000); err != nil {
		t.Errorf("合法参数应通过: %v", err)
	}
	if err := validSetupParams(validSalt, validHash, minIterations); err != nil {
		t.Errorf("边界最小迭代次数应通过: %v", err)
	}
	if err := validSetupParams(validSalt, validHash, maxIterations); err != nil {
		t.Errorf("边界最大迭代次数应通过: %v", err)
	}
	if err := validSetupParams(validSalt, validHash, minIterations-1); err == nil {
		t.Error("低于最小迭代次数应拒绝")
	}
	if err := validSetupParams(validSalt, validHash, maxIterations+1); err == nil {
		t.Error("高于最大迭代次数应拒绝")
	}
}

func TestParseChangePassItems(t *testing.T) {
	items, err := parseChangePassItems(`[{"id":1,"encrypted":"a"},{"id":2,"encrypted":"b"}]`)
	if err != nil {
		t.Fatalf("合法 JSON 应解析成功: %v", err)
	}
	if len(items) != 2 || items[0].ID != 1 || items[1].Encrypted != "b" {
		t.Fatalf("解析结果不符: %+v", items)
	}
	if _, err := parseChangePassItems("not json"); err == nil {
		t.Error("非法 JSON 应报错")
	}
	empty, err := parseChangePassItems("")
	if err != nil || empty != nil {
		t.Errorf("空字符串应返回 nil, nil, got %v %v", empty, err)
	}
}

// 审查 I4：change-passphrase 提交的条目必须恰好覆盖 owner 全部条目——
// 少交（如旧标签页拿缓存只交一部分）应 400 拒绝，且 meta 与密文均不变。
func TestChangePassphraseCoverageMismatch(t *testing.T) {
	mux := setupTest(t)
	bearer := authHeader(t, "alice")
	setupVault(t, mux, bearer)
	createItem(t, mux, bearer, "item-1", "enc-1")
	createItem(t, mux, bearer, "item-2", "enc-2")

	rec := doGet(t, mux, "/api/vault/items", bearer)
	body := jsonBody(t, rec)
	first := body["items"].([]interface{})[0].(map[string]interface{})
	id1 := int(first["id"].(float64))
	enc0 := first["encrypted"].(string)

	payload, _ := json.Marshal([]changePassItem{{ID: id1, Encrypted: "new-enc"}})
	rec = doForm(t, mux, http.MethodPost, "/api/vault/change-passphrase", bearer, url.Values{
		"salt":        {strings.Repeat("c", 32)},
		"verify_hash": {strings.Repeat("d", 64)},
		"iterations":  {"700000"},
		"items":       {string(payload)},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("覆盖校验应 400 拒绝, got %d", rec.Code)
	}
	// 旧口令仍有效（meta 未动）
	rec = doForm(t, mux, http.MethodPost, "/api/vault/unlock", bearer, url.Values{"verify_hash": {validHash}})
	if rec.Code != http.StatusOK {
		t.Fatalf("拒绝后旧口令应仍有效, got %d", rec.Code)
	}
	// 密文未被覆盖
	rec = doGet(t, mux, "/api/vault/items", bearer)
	body = jsonBody(t, rec)
	enc := body["items"].([]interface{})[0].(map[string]interface{})["encrypted"].(string)
	if enc != enc0 {
		t.Fatalf("密文不应被改动: got %s, want %s", enc, enc0)
	}
}
