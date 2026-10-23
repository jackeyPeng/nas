// accept_test.go 覆盖建议接受链（F2 Task 5）：AcceptSuggestion 内核 +
// POST /api/triggers/suggestions/{id}/accept 端点 + 对抗/边界用例。
package triggers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nas-panel/common"
	"nas-panel/modules/ai"
	"nas-panel/modules/filemgmt"
	"nas-panel/modules/tasks"
)

// setupAcceptTest 初始化 triggers（resetForTest 已做）+ tasks 内核 + 池
// fixture，供 accept 链全部用例复用。真实重复文件种子由各用例自行写入
// poolRoot。NAS_DATA_DIR 指向本用例独立临时目录——ai.LoadConfig/
// ai.SaveConfig/ai 的会话库（aiConfigPath/aiDBPath 未显式注入时）都会
// 落在这个目录下，不污染其它用例或真实 /opt/nas/data。
func setupAcceptTest(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	if err := tasks.InitTasks(dataDir); err != nil {
		t.Fatalf("tasks.InitTasks: %v", err)
	}
	t.Setenv("NAS_DATA_DIR", t.TempDir())
	poolRoot := t.TempDir()
	filemgmt.SetPoolRootOverrideForTest(poolRoot)
	t.Cleanup(func() { filemgmt.SetPoolRootOverrideForTest("") })
	return poolRoot
}

func mustWriteDupFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func createTaskPlanSuggestion(t *testing.T) Suggestion {
	t.Helper()
	sg, ok, err := CreateSuggestion("watermark_high", "trig.watermark_title", "trig.watermark_body", taskPlanActions, time.Hour)
	if err != nil || !ok {
		t.Fatalf("CreateSuggestion: ok=%v err=%v", ok, err)
	}
	return sg
}

// ── 对抗清单 1：accept 成功链 ─────────────────────────────────────

func TestAcceptSuggestionSuccessChainPendingConfirmToConfirmToDone(t *testing.T) {
	resetForTest(t)
	poolRoot := setupAcceptTest(t)
	tasks.StartWorker()
	t.Cleanup(tasks.StopWorkerForTest)

	content := []byte("duplicate-content-accept-chain")
	mustWriteDupFile(t, filepath.Join(poolRoot, "a.jpg"), content)
	mustWriteDupFile(t, filepath.Join(poolRoot, "b.jpg"), content)

	sg := createTaskPlanSuggestion(t)

	result, err := AcceptSuggestion(sg.ID)
	if err != nil {
		t.Fatalf("AcceptSuggestion: %v", err)
	}
	if result.ActionID == "" || result.ConfirmWord == "" || result.JobID == "" {
		t.Fatalf("AcceptResult 字段不完整: %+v", result)
	}

	job, ok := tasks.GetJob(result.JobID)
	if !ok {
		t.Fatalf("job 应存在: %s", result.JobID)
	}
	if job.Status != tasks.StatusPendingConfirm {
		t.Fatalf("job 应处于 pending_confirm, got %s", job.Status)
	}
	if job.Impact.Groups != 1 {
		t.Fatalf("应扫出 1 组重复, got groups=%d", job.Impact.Groups)
	}

	// 建议应已迁到 accepted。
	after, ok := GetSuggestion(sg.ID)
	if !ok || after.State != StateAccepted {
		t.Fatalf("建议应迁到 accepted, got ok=%v state=%s", ok, after.State)
	}

	// action_id 可以 confirm（走既有 POST /api/ai/action/confirm，零新确认语义）。
	mux := setupAIActionConfirmMux(t)
	rec := postJSONAccept(t, mux, "/api/ai/action/confirm", map[string]interface{}{
		"action_id": result.ActionID, "confirm_word": result.ConfirmWord,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(5 * time.Second)
	var done tasks.Job
	for time.Now().Before(deadline) {
		done, ok = tasks.GetJob(result.JobID)
		if ok && (done.Status == tasks.StatusDone || done.Status == tasks.StatusFailed) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if done.Status != tasks.StatusDone {
		t.Fatalf("confirm 后 job 应 running→done, got %s err=%s", done.Status, done.Error)
	}
}

// ── 对抗清单 2：重放/非 new 状态拒绝 ─────────────────────────────

func TestAcceptSuggestionReplaySecondAcceptRejected(t *testing.T) {
	resetForTest(t)
	setupAcceptTest(t)

	sg := createTaskPlanSuggestion(t)

	if _, err := AcceptSuggestion(sg.ID); err != nil {
		t.Fatalf("首次 accept 应成功: %v", err)
	}
	_, err := AcceptSuggestion(sg.ID)
	if err == nil {
		t.Fatal("二次 accept 同一建议（已是 accepted）必须拒绝——409 语义")
	}
	if !strings.Contains(err.Error(), "非法状态迁移") {
		t.Fatalf("二次 accept 的错误应是状态迁移类（REST 层映射 409）, got %q", err.Error())
	}
}

func TestAcceptSuggestionDismissedRejected(t *testing.T) {
	resetForTest(t)
	setupAcceptTest(t)

	sg := createTaskPlanSuggestion(t)
	if err := DismissSuggestion(sg.ID); err != nil {
		t.Fatalf("DismissSuggestion: %v", err)
	}
	_, err := AcceptSuggestion(sg.ID)
	if err == nil {
		t.Fatal("dismissed 建议 accept 必须拒绝")
	}
}

func TestAcceptSuggestionExpiredRejected(t *testing.T) {
	resetForTest(t)
	setupAcceptTest(t)

	sg := createTaskPlanSuggestion(t)
	dbMu.Lock()
	_, execErr := db.Exec(`UPDATE suggestions SET state = ? WHERE id = ?`, StateExpired, sg.ID)
	dbMu.Unlock()
	if execErr != nil {
		t.Fatalf("模拟过期失败: %v", execErr)
	}

	_, err := AcceptSuggestion(sg.ID)
	if err == nil {
		t.Fatal("expired 建议 accept 必须拒绝")
	}
}

func TestAcceptSuggestionNotFoundRejected(t *testing.T) {
	resetForTest(t)
	setupAcceptTest(t)

	_, err := AcceptSuggestion("sg-does-not-exist")
	if err == nil {
		t.Fatal("不存在的建议 accept 必须拒绝")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("错误应含'不存在'（REST 层映射 404）, got %q", err.Error())
	}
}

// ── 对抗清单 3：action TTL/一次性 confirm 仍是既有 410 语义不变 ──────

// TestAcceptSuggestionActionIsOneTimeConfirmSecondCallGone 断言 accept 链
// 产出的 action_id 走的是既有 PendingStore.Take 一次性协议（TTL 惰性过期
// 与"已被 Take 一次"是同一个底层机制的两种触发方式，ai/actions_test.go
// 的 TestConfirmActionExpiredReturns410/TestConfirmActionSecondTakeFails
// 已经覆盖 TTL 本身；这里确认 accept 链没有绕开这套既有协议另起一套）：
// 第一次 confirm 成功后，第二次用同一 action_id 必须 410——与 chat.go
// 产出的 action 完全一致的"一次性即过期"语义，零新规则。
func TestAcceptSuggestionActionIsOneTimeConfirmSecondCallGone(t *testing.T) {
	resetForTest(t)
	setupAcceptTest(t)

	sg := createTaskPlanSuggestion(t)
	result, err := AcceptSuggestion(sg.ID)
	if err != nil {
		t.Fatalf("AcceptSuggestion: %v", err)
	}

	mux := setupAIActionConfirmMux(t)
	rec1 := postJSONAccept(t, mux, "/api/ai/action/confirm", map[string]interface{}{
		"action_id": result.ActionID, "confirm_word": result.ConfirmWord,
	})
	if rec1.Code == http.StatusGone {
		t.Fatalf("第一次 confirm 不应命中 410: %s", rec1.Body.String())
	}

	rec2 := postJSONAccept(t, mux, "/api/ai/action/confirm", map[string]interface{}{
		"action_id": result.ActionID, "confirm_word": result.ConfirmWord,
	})
	if rec2.Code != http.StatusGone {
		t.Fatalf("二次 confirm 同一 action_id 必须 410（既有一次性协议）, got %d body=%s", rec2.Code, rec2.Body.String())
	}

	// 不存在的 action_id 同样必须 410（对照 ai 既有语义）。
	rec3 := postJSONAccept(t, mux, "/api/ai/action/confirm", map[string]interface{}{
		"action_id": "does-not-exist", "confirm_word": result.ConfirmWord,
	})
	if rec3.Code != http.StatusGone {
		t.Fatalf("不存在的 action_id 必须 410, got %d body=%s", rec3.Code, rec3.Body.String())
	}
}

// ── 对抗清单 4：CreatePendingAction 未注册工具名拒绝；chat.go 既有确认门测试不受影响 ──

func TestAcceptSuggestionUnknownKindRejectedNoStateChange(t *testing.T) {
	resetForTest(t)
	setupAcceptTest(t)

	// kind≠photo_dedupe（F2 只支持这一种，F4 扩）→ 明确错误，state 不变。
	sg, ok, err := CreateSuggestion("custom_key", "t", "b",
		`[{"type":"task_plan","kind":"not_supported_kind","scope":""}]`, time.Hour)
	if err != nil || !ok {
		t.Fatalf("CreateSuggestion: ok=%v err=%v", ok, err)
	}

	_, err = AcceptSuggestion(sg.ID)
	if err == nil {
		t.Fatal("未支持的 kind 必须拒绝")
	}

	after, ok := GetSuggestion(sg.ID)
	if !ok || after.State != StateNew {
		t.Fatalf("失败路径不应改变建议 state, got state=%s", after.State)
	}
}

func TestAcceptSuggestionNavigateOnlyRejectedNoStateChange(t *testing.T) {
	resetForTest(t)
	setupAcceptTest(t)

	sg, ok, err := CreateSuggestion("sync_failed", "t", "b", navigateActions, time.Hour)
	if err != nil || !ok {
		t.Fatalf("CreateSuggestion: ok=%v err=%v", ok, err)
	}

	_, err = AcceptSuggestion(sg.ID)
	if err == nil {
		t.Fatal("navigate 类建议不应能走 accept（纯前端跳转+dismiss）")
	}

	after, ok := GetSuggestion(sg.ID)
	if !ok || after.State != StateNew {
		t.Fatalf("失败路径不应改变建议 state, got state=%s", after.State)
	}
}

// ── 对抗清单 5：无可清理文件时 PlanPhotoDedupe 0 组行为（F1 实现为准） ──

// TestAcceptSuggestionNoDuplicatesBuildsZeroStepJobWithNoteSummary 断言：
// 池内没有任何重复文件时，tasks.PlanPhotoDedupe 仍会建一个 0 步骤
// pending_confirm job（F1 CreateJob 不拒绝空 plan——读代码确认的实际
// 语义，见 OPENCODE-REPORT-F2-R3.md）。AcceptSuggestion 据此在 Summary
// 里明示"没有发现重复文件"，但仍正常走完 CreatePendingAction+accepted
// 流程（不是一条独立的"拒绝"分支）。
func TestAcceptSuggestionNoDuplicatesBuildsZeroStepJobWithNoteSummary(t *testing.T) {
	resetForTest(t)
	setupAcceptTest(t) // 空池根，没有任何文件

	sg := createTaskPlanSuggestion(t)
	result, err := AcceptSuggestion(sg.ID)
	if err != nil {
		t.Fatalf("0 组时 AcceptSuggestion 不应报错: %v", err)
	}
	if !strings.Contains(result.Summary, "没有发现重复文件") {
		t.Fatalf("0 组时 Summary 应明示'没有发现重复文件', got %q", result.Summary)
	}

	job, ok := tasks.GetJob(result.JobID)
	if !ok {
		t.Fatalf("job 应存在: %s", result.JobID)
	}
	if job.Impact.Groups != 0 || len(job.Plan) != 0 {
		t.Fatalf("0 组场景 job 应是 0 步骤计划, got groups=%d steps=%d", job.Impact.Groups, len(job.Plan))
	}

	after, ok := GetSuggestion(sg.ID)
	if !ok || after.State != StateAccepted {
		t.Fatalf("0 组场景仍应正常迁到 accepted, got state=%s", after.State)
	}
}

// ── 对抗清单 6：泄漏面——accept 响应/审计 detail 不含 api_key/密码形状 ──

const acceptLeakTestSecret = "sk-leaktest-accept-chain-should-never-appear-1234567890"

func TestAcceptSuggestionResponseAndAuditDoNotLeakSecretShapes(t *testing.T) {
	resetForTest(t)
	poolRoot := setupAcceptTest(t)

	// 文件名本身夹带一个"看起来像密钥"的字符串，验证即便数据层出现这种
	// 形状，accept 响应/审计 detail 也不会把它整段原样搬进去（对照
	// ai/security_test.go 的 key 泄漏面断言模式）。
	content := []byte("dup-content-leak-test")
	mustWriteDupFile(t, filepath.Join(poolRoot, acceptLeakTestSecret+"-a.jpg"), content)
	mustWriteDupFile(t, filepath.Join(poolRoot, acceptLeakTestSecret+"-b.jpg"), content)

	sg := createTaskPlanSuggestion(t)

	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)

	mux := registerAcceptTestMux(t)
	rec := postJSONAccept(t, mux, "/api/triggers/suggestions/"+sg.ID+"/accept", map[string]interface{}{})
	if rec.Code != http.StatusOK {
		t.Fatalf("accept 应 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), acceptLeakTestSecret) {
		t.Fatalf("accept 响应不应原样包含文件名里的敏感形状片段: %s", rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非法 JSON: %v", err)
	}
	for _, key := range []string{"action_id", "confirm_word", "summary", "job_id"} {
		if _, ok := resp[key]; !ok {
			t.Fatalf("accept 响应缺字段 %q: %v", key, resp)
		}
	}
}

// ── 测试内 helpers：ai action confirm mux / triggers accept mux ────

// setupAIActionConfirmMux 启用 ai 模块（NAS_DATA_DIR 已由 setupAcceptTest
// 指到独立临时目录，ai.SaveConfig/LoadConfig 落盘互不干扰其它用例）并
// 注册它的真实路由——accept 链产出的 action_id 要能走既有
// POST /api/ai/action/confirm，这里必须是 ai 包的真实 handler，不是 mock。
func setupAIActionConfirmMux(t *testing.T) *http.ServeMux {
	t.Helper()
	if err := ai.SaveConfig(ai.Config{
		Enabled: true, BaseURL: "http://127.0.0.1:1", Model: "test-model", MaxToolCalls: 12, Verified: true,
	}); err != nil {
		t.Fatalf("ai.SaveConfig: %v", err)
	}
	mux := http.NewServeMux()
	ai.RegisterRoutes(mux)
	return mux
}

// authedActionConfirmRequest 构造带合法 JWT 的 /api/ai/action/confirm 请求
// （ai.RegisterRoutes 的真实路由走 common.AuthMiddleware，不能像
// triggers 自己的 registerRoutesBare 一样跳过鉴权）。
func authedActionConfirmRequest(t *testing.T, path string, body map[string]interface{}) *http.Request {
	t.Helper()
	token, err := common.CreateToken("fm")
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("序列化请求体失败: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func registerAcceptTestMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	registerRoutesBare(mux, alwaysEnabled)
	return mux
}

// postJSONAccept 对 triggers 自身的 registerRoutesBare 路由（不鉴权）发
// POST 请求；对 ai.RegisterRoutes 的真实路由（鉴权）发请求时改用
// authedActionConfirmRequest 构造请求对象。
func postJSONAccept(t *testing.T, mux *http.ServeMux, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("序列化请求体失败: %v", err)
	}
	rec := httptest.NewRecorder()

	var req *http.Request
	if strings.HasPrefix(path, "/api/ai/") {
		m, ok := body.(map[string]interface{})
		if !ok {
			t.Fatalf("/api/ai/ 路径请求体必须是 map[string]interface{}")
		}
		req = authedActionConfirmRequest(t, path, m)
	} else {
		req = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
	}
	mux.ServeHTTP(rec, req)
	return rec
}
