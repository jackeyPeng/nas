package tasks

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nas-panel/common"
)

// setupAPITestMux 建库 + 注册路由（绕过 AuthMiddleware，直接挂裸 handler，
// 对齐 events_test.go 的 setupTestMux 模式）；enabled 由传入的 guard 控制。
func setupAPITestMux(t *testing.T, guard func() bool) *http.ServeMux {
	t.Helper()
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)
	StartWorker()
	t.Cleanup(StopWorkerForTest)

	mux := http.NewServeMux()
	registerAPIRoutesBare(mux, guard)
	return mux
}

func alwaysEnabled() bool  { return true }
func alwaysDisabled() bool { return false }

func TestAPIEnabledGuardForbidsAllEndpoints(t *testing.T) {
	mux := setupAPITestMux(t, alwaysDisabled)

	job, err := CreateJob("photo_dedupe", "", samplePlan(), sampleImpact())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	cases := []struct {
		method string
		path   string
	}{
		{"GET", "/api/tasks"},
		{"GET", "/api/tasks/" + job.ID},
		{"POST", "/api/tasks/" + job.ID + "/pause"},
		{"POST", "/api/tasks/" + job.ID + "/resume"},
		{"POST", "/api/tasks/" + job.ID + "/cancel"},
		{"GET", "/api/tasks/" + job.ID + "/stream"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s: enabled=false 应 403, got %d body=%s", c.method, c.path, rec.Code, rec.Body.String())
		}
		ct := rec.Header().Get("Content-Type")
		if !strings.Contains(ct, "application/json") {
			t.Fatalf("%s %s: 403 响应 Content-Type 应为 json, got %q", c.method, c.path, ct)
		}
	}
}

func TestAPIListAndDetailHappyPath(t *testing.T) {
	mux := setupAPITestMux(t, alwaysEnabled)

	job, err := CreateJob("photo_dedupe", "public", samplePlan(), sampleImpact())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/tasks?limit=5", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/tasks: %d body=%s", rec.Code, rec.Body.String())
	}
	var listResp struct {
		Jobs []Job `json:"jobs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if len(listResp.Jobs) != 1 || listResp.Jobs[0].ID != job.ID {
		t.Fatalf("列表应含刚创建的任务: %+v", listResp)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/tasks/"+job.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/tasks/{id}: %d body=%s", rec.Code, rec.Body.String())
	}
	var got Job
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if got.ID != job.ID || got.ScopeRel != "public" {
		t.Fatalf("详情字段不符: %+v", got)
	}
}

func TestAPIDetailNotFound(t *testing.T) {
	mux := setupAPITestMux(t, alwaysEnabled)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/tasks/job-doesnotexist", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在的任务应 404, got %d", rec.Code)
	}
}

func TestAPIPauseResumeCancelHappyPath(t *testing.T) {
	mux := setupAPITestMux(t, alwaysEnabled)

	RegisterOp("api_happy_op", func(ctx context.Context, step Step) error {
		return nil
	})
	job, err := CreateJob("photo_dedupe", "", fakeStepPlan("api_happy_op", 1), Impact{Files: 1})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	// pending_confirm -> running 走 StartJob（内核调用，REST 没有单独
	// start 端点——start_task 是 AI 工具层概念，Task 5 接入；这里直接调用
	// 内核函数等价于"已确认启动"）。
	if err := StartJob(job.ID); err != nil {
		t.Fatalf("StartJob: %v", err)
	}
	waitForStatus(t, job.ID, 2*time.Second, StatusDone, StatusFailed)

	// done 状态下 cancel 应 409（非法迁移）
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/tasks/"+job.ID+"/cancel", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("done 任务 cancel 应 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("409 响应应为 json content-type, got %q", ct)
	}
}

func TestAPIPauseHappyPath(t *testing.T) {
	mux := setupAPITestMux(t, alwaysEnabled)

	block := make(chan struct{})
	release := make(chan struct{})
	RegisterOp("api_pause_op", func(ctx context.Context, step Step) error {
		select {
		case <-block:
		default:
			close(block)
			<-release
		}
		return nil
	})
	job, err := CreateJob("photo_dedupe", "", fakeStepPlan("api_pause_op", 3), Impact{Files: 3})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := StartJob(job.ID); err != nil {
		t.Fatalf("StartJob: %v", err)
	}
	<-block

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/tasks/"+job.ID+"/pause", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST pause: %d body=%s", rec.Code, rec.Body.String())
	}
	var okResp struct {
		OK bool `json:"ok"`
	}
	json.Unmarshal(rec.Body.Bytes(), &okResp)
	if !okResp.OK {
		t.Fatalf("pause 响应应 ok=true, got %s", rec.Body.String())
	}
	close(release)
	waitForStatus(t, job.ID, 2*time.Second, StatusPaused)

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/tasks/"+job.ID+"/resume", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST resume: %d body=%s", rec.Code, rec.Body.String())
	}
	waitForStatus(t, job.ID, 2*time.Second, StatusDone, StatusFailed)
}

func TestAPIStreamSendsTerminalEventForDoneJobImmediately(t *testing.T) {
	mux := setupAPITestMux(t, alwaysEnabled)

	job, err := CreateJob("photo_dedupe", "", samplePlan(), sampleImpact())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := UpdateJobStatus(job.ID, StatusRunning, ""); err != nil {
		t.Fatalf("UpdateJobStatus running: %v", err)
	}
	if err := FinishJob(job.ID, `{"moved":2,"freed":2048,"skipped":0}`, "", StatusDone); err != nil {
		t.Fatalf("FinishJob: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/tasks/"+job.ID+"/stream", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET stream: %d body=%s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("stream Content-Type 应为 text/event-stream, got %q", ct)
	}

	scanner := bufio.NewScanner(strings.NewReader(rec.Body.String()))
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var payload struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err == nil {
			if payload.Status == string(StatusDone) {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("done 任务的 SSE 应立即发送终态事件并关闭, body=%s", rec.Body.String())
	}
}
