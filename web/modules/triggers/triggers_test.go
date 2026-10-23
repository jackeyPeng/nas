package triggers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nas-panel/common"
)

func resetForTest(t *testing.T) {
	t.Helper()
	closeDBForTest()
	if err := InitTriggers(t.TempDir()); err != nil {
		t.Fatalf("InitTriggers: %v", err)
	}
	SetEnabledGuard(nil)
	t.Cleanup(closeDBForTest)
	t.Cleanup(func() { SetEnabledGuard(nil) })
}

func alwaysEnabled() bool  { return true }
func alwaysDisabled() bool { return false }

const taskPlanActions = `[{"type":"task_plan","kind":"photo_dedupe","scope":""}]`
const navigateActions = `[{"type":"navigate","page":"health"}]`

// ── 2. CreateSuggestion 成功→State=new+事件发出；冷却；上限；ttl→expire_at；过期惰性 ──

func TestCreateSuggestionSuccessEmitsEvent(t *testing.T) {
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)

	ch := common.Subscribe()
	defer common.Unsubscribe(ch)

	sg, ok, err := CreateSuggestion("watermark_high", "trig.watermark_title", "trig.watermark_body", taskPlanActions, time.Hour)
	if err != nil {
		t.Fatalf("CreateSuggestion: %v", err)
	}
	if !ok {
		t.Fatalf("want ok=true")
	}
	if sg.State != StateNew {
		t.Fatalf("want State=new, got %s", sg.State)
	}
	common.DrainEventsForTest()

	select {
	case ev := <-ch:
		if ev.Code != "suggestion.created" {
			t.Fatalf("want code=suggestion.created, got %s", ev.Code)
		}
		var params map[string]string
		json.Unmarshal([]byte(ev.Params), &params)
		if params["suggestion_id"] != sg.ID || params["trigger_key"] != "watermark_high" {
			t.Fatalf("事件参数不符: %+v", params)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("未收到 suggestion.created 事件")
	}
}

func TestCreateSuggestionCooldown24h(t *testing.T) {
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)

	_, ok, err := CreateSuggestion("sync_failed", "t", "b", navigateActions, time.Hour)
	if err != nil || !ok {
		t.Fatalf("首次创建应成功: ok=%v err=%v", ok, err)
	}

	// 同 key 立即再创建 → 冷却拒绝
	_, ok2, err := CreateSuggestion("sync_failed", "t", "b", navigateActions, time.Hour)
	if err != nil {
		t.Fatalf("CreateSuggestion: %v", err)
	}
	if ok2 {
		t.Fatalf("24h 冷却内同 key 应拒绝")
	}

	// 把记录的 created_at 改到 25h 前，模拟冷却窗口已过
	dbMu.Lock()
	_, execErr := db.Exec(`UPDATE suggestions SET created_at = ? WHERE trigger_key = ?`,
		time.Now().UTC().Add(-25*time.Hour).Format(time.RFC3339Nano), "sync_failed")
	dbMu.Unlock()
	if execErr != nil {
		t.Fatalf("模拟过期失败: %v", execErr)
	}

	_, ok3, err := CreateSuggestion("sync_failed", "t", "b", navigateActions, time.Hour)
	if err != nil {
		t.Fatalf("CreateSuggestion: %v", err)
	}
	if !ok3 {
		t.Fatalf("25h 前记录应已出冷却，允许新建")
	}
}

func TestCreateSuggestionActiveLimit5(t *testing.T) {
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)

	var ids []string
	for i := 0; i < 5; i++ {
		key := "key" + string(rune('a'+i))
		sg, ok, err := CreateSuggestion(key, "t", "b", navigateActions, time.Hour)
		if err != nil || !ok {
			t.Fatalf("第 %d 条应成功: ok=%v err=%v", i+1, ok, err)
		}
		ids = append(ids, sg.ID)
	}

	// 第 6 条（不同 key，避开冷却）应因活跃上限被拒
	_, ok6, err := CreateSuggestion("keyf", "t", "b", navigateActions, time.Hour)
	if err != nil {
		t.Fatalf("CreateSuggestion: %v", err)
	}
	if ok6 {
		t.Fatalf("第 6 条活跃建议应被上限拒绝")
	}

	// dismiss 一条后，第 6 条应被允许
	if err := DismissSuggestion(ids[0]); err != nil {
		t.Fatalf("DismissSuggestion: %v", err)
	}
	_, ok6b, err := CreateSuggestion("keyf", "t", "b", navigateActions, time.Hour)
	if err != nil {
		t.Fatalf("CreateSuggestion: %v", err)
	}
	if !ok6b {
		t.Fatalf("dismiss 一条后应允许新建")
	}
}

func TestCreateSuggestionTTLAndExpire(t *testing.T) {
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)

	sg, ok, err := CreateSuggestion("ttl_key", "t", "b", navigateActions, time.Hour)
	if err != nil || !ok {
		t.Fatalf("创建失败: ok=%v err=%v", ok, err)
	}
	if sg.ExpireAt == "" {
		t.Fatalf("ttl>0 时 expire_at 应非空")
	}

	// 手动把 expire_at 改到过去，模拟已过期
	dbMu.Lock()
	_, execErr := db.Exec(`UPDATE suggestions SET expire_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), sg.ID)
	dbMu.Unlock()
	if execErr != nil {
		t.Fatalf("模拟过期失败: %v", execErr)
	}

	active := ListSuggestions(false)
	for _, s := range active {
		if s.ID == sg.ID {
			t.Fatalf("过期建议不应出现在默认列表")
		}
	}

	all := ListSuggestions(true)
	found := false
	for _, s := range all {
		if s.ID == sg.ID {
			found = true
			if s.State != StateExpired {
				t.Fatalf("过期建议状态应惰性置为 expired, got %s", s.State)
			}
		}
	}
	if !found {
		t.Fatalf("includeInactive=true 时应仍能看到过期行")
	}
}

// ── 3. validateActions：合法两种 type 通过；未知 type/坏 JSON/空数组拒绝 ──

func TestValidateActions(t *testing.T) {
	cases := []struct {
		name    string
		actions string
		wantErr bool
	}{
		{"task_plan合法", taskPlanActions, false},
		{"navigate合法", navigateActions, false},
		{"未知type", `[{"type":"unknown_type"}]`, true},
		{"坏JSON", `not json`, true},
		{"空数组", `[]`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateActions(c.actions)
			if c.wantErr && err == nil {
				t.Fatalf("%s: 期望报错，未报错", c.name)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("%s: 期望通过，报错: %v", c.name, err)
			}
		})
	}
}

func TestCreateSuggestionRejectsInvalidActions(t *testing.T) {
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)

	_, _, err := CreateSuggestion("bad_key", "t", "b", `[{"type":"unknown_type"}]`, time.Hour)
	if err == nil {
		t.Fatalf("未知 action type 应在创建时拒绝")
	}
}

// ── 4. Dismiss 语义：new→dismissed 成功；accepted/dismissed 再 dismiss 报错 ──

func TestDismissSuggestionSemantics(t *testing.T) {
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)

	sg, ok, err := CreateSuggestion("dismiss_key", "t", "b", navigateActions, time.Hour)
	if err != nil || !ok {
		t.Fatalf("创建失败: ok=%v err=%v", ok, err)
	}

	if err := DismissSuggestion(sg.ID); err != nil {
		t.Fatalf("new->dismissed 应成功: %v", err)
	}

	if err := DismissSuggestion(sg.ID); err == nil {
		t.Fatalf("dismissed 再 dismiss 应报错")
	}

	if err := DismissSuggestion("sg-doesnotexist"); err == nil {
		t.Fatalf("不存在的建议 dismiss 应报错")
	}
}

// ── 5. guard=false 时 CreateSuggestion 返回错误（不产生记录、不发事件） ──

func TestCreateSuggestionGuardFalse(t *testing.T) {
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)
	SetEnabledGuard(alwaysDisabled)

	ch := common.Subscribe()
	defer common.Unsubscribe(ch)

	_, ok, err := CreateSuggestion("guard_key", "t", "b", navigateActions, time.Hour)
	if err == nil {
		t.Fatalf("guard=false 应返回 error")
	}
	if ok {
		t.Fatalf("guard=false 时 ok 应为 false")
	}
	common.DrainEventsForTest()

	select {
	case ev := <-ch:
		t.Fatalf("guard=false 时不应发事件, got %+v", ev)
	default:
	}

	if len(ListSuggestions(true)) != 0 {
		t.Fatalf("guard=false 时不应产生记录")
	}
}

// ── 6. REST 401/403/200 形状 ──

func setupAPITestMux(t *testing.T, guard func() bool) *http.ServeMux {
	t.Helper()
	resetForTest(t)
	mux := http.NewServeMux()
	registerRoutesBare(mux, guard)
	return mux
}

func TestAPIGuardFalseForbidsAllEndpoints(t *testing.T) {
	mux := setupAPITestMux(t, alwaysDisabled)

	cases := []struct {
		method string
		path   string
	}{
		{"GET", "/api/triggers/suggestions"},
		{"GET", "/api/triggers/suggestions?all=1"},
		{"POST", "/api/triggers/suggestions/sg-x/dismiss"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s: guard=false 应 403, got %d body=%s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}

func TestAPIListAndDismissHappyPath(t *testing.T) {
	mux := setupAPITestMux(t, alwaysEnabled)

	sg, ok, err := CreateSuggestion("api_key", "trig.t", "trig.b", navigateActions, time.Hour)
	if err != nil || !ok {
		t.Fatalf("创建失败: ok=%v err=%v", ok, err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/triggers/suggestions", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET suggestions: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Suggestions []Suggestion `json:"suggestions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if len(resp.Suggestions) != 1 || resp.Suggestions[0].ID != sg.ID {
		t.Fatalf("列表形状不符: %+v", resp)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/triggers/suggestions/"+sg.ID+"/dismiss", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST dismiss: %d body=%s", rec.Code, rec.Body.String())
	}

	// dismiss 后默认列表（all 未传）不应再含该条
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/triggers/suggestions", nil))
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Suggestions) != 0 {
		t.Fatalf("dismiss 后默认列表应为空: %+v", resp)
	}

	// all=1 应仍能看到（state=dismissed）
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/triggers/suggestions?all=1", nil))
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Suggestions) != 1 || resp.Suggestions[0].State != StateDismissed {
		t.Fatalf("all=1 应看到 dismissed 状态: %+v", resp)
	}
}

func TestAPIDismissNotFoundAndConflict(t *testing.T) {
	mux := setupAPITestMux(t, alwaysEnabled)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/triggers/suggestions/sg-doesnotexist/dismiss", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在的建议应 404, got %d", rec.Code)
	}

	sg, _, _ := CreateSuggestion("conflict_key", "t", "b", navigateActions, time.Hour)
	if err := DismissSuggestion(sg.ID); err != nil {
		t.Fatalf("DismissSuggestion: %v", err)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/triggers/suggestions/"+sg.ID+"/dismiss", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("已 dismissed 再 dismiss 应 409, got %d", rec.Code)
	}
}

func TestAPIAuthMiddlewareRequiresToken(t *testing.T) {
	resetForTest(t)
	mux := http.NewServeMux()
	RegisterRoutes(mux, alwaysEnabled)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/triggers/suggestions", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无 token 应 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ── StartLoops/StopLoopsForTest 幂等性（骨架测试） ──

func TestStartLoopsIdempotent(t *testing.T) {
	resetForTest(t)
	StartLoops()
	StartLoops() // 重复调用不应 panic 或起第二个循环
	StopLoopsForTest()
	t.Cleanup(StopLoopsForTest)
}
