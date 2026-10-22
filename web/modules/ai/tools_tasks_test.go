package ai

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nas-panel/modules/filemgmt"
	"nas-panel/modules/tasks"
)

// F1 Task 5 Step 1：tools_tasks_test.go 失败测试。
//
// setupTasksToolTest 建一个独立的任务库 + 池根 fixture，供本文件全部用例
// 复用（tasks.InitTasks 内部会先关闭既有单例连接再重开，天然幂等，不需要
// 跨包导出额外的 close 函数）。
func setupTasksToolTest(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	if err := tasks.InitTasks(dataDir); err != nil {
		t.Fatalf("tasks.InitTasks: %v", err)
	}
	poolRoot := t.TempDir()
	filemgmt.SetPoolRootOverrideForTest(poolRoot)
	t.Cleanup(func() { filemgmt.SetPoolRootOverrideForTest("") })
	return poolRoot
}

func findTasksTool(name string) Tool {
	for _, t := range nasTasksTools() {
		if t.Name == name {
			return t
		}
	}
	return Tool{}
}

// ─── schema 校验 ───

func TestPlanTaskRejectsUnknownKind(t *testing.T) {
	setupTasksToolTest(t)
	tool := findTasksTool("plan_task")
	_, err := ValidateArgs(tool, `{"kind":"rm_everything"}`)
	if err == nil {
		t.Fatal(`kind="rm_everything" 必须被 Enum 校验拒绝`)
	}
}

func TestPlanTaskScopeOutsidePoolReturnsErrorJSONNotPanic(t *testing.T) {
	setupTasksToolTest(t)
	tool := findTasksTool("plan_task")
	args, err := ValidateArgs(tool, `{"kind":"photo_dedupe","scope":"../etc"}`)
	if err != nil {
		t.Fatalf("ValidateArgs 不应拒绝合法 schema 输入: %v", err)
	}
	out, execErr := tool.Exec(context.Background(), args)
	if execErr != nil {
		return // Exec 返回 Go error 也可接受（取决于 Write 分支约定），但本工具约定返回 JSON 字符串
	}
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("plan_task 输出非法 JSON: %v, raw=%s", err, out)
	}
	if _, ok := resp["error"]; !ok {
		t.Fatalf("scope 池外路径应返回 {\"error\":...}, got %s", out)
	}
}

// ─── plan → start 全链 ───

func TestPlanTaskThenStartTaskFullChain(t *testing.T) {
	poolRoot := setupTasksToolTest(t)
	tasks.StartWorker()
	t.Cleanup(tasks.StopWorkerForTest)

	content := []byte("tool-chain-duplicate-content")
	mustWriteFileAI(t, filepath.Join(poolRoot, "a.jpg"), content)
	mustWriteFileAI(t, filepath.Join(poolRoot, "sub", "b.jpg"), content)

	planTool := findTasksTool("plan_task")
	args, err := ValidateArgs(planTool, `{"kind":"photo_dedupe"}`)
	if err != nil {
		t.Fatalf("ValidateArgs: %v", err)
	}
	out, err := planTool.Exec(context.Background(), args)
	if err != nil {
		t.Fatalf("plan_task Exec: %v", err)
	}
	var planResp struct {
		JobID  string `json:"job_id"`
		Impact struct {
			Groups int   `json:"groups"`
			Files  int   `json:"files"`
			Bytes  int64 `json:"bytes"`
		} `json:"impact"`
		Partial bool `json:"partial"`
	}
	if err := json.Unmarshal([]byte(out), &planResp); err != nil {
		t.Fatalf("plan_task 输出非法 JSON: %v, raw=%s", err, out)
	}
	if planResp.JobID == "" {
		t.Fatalf("plan_task 应返回 job_id, raw=%s", out)
	}
	if planResp.Impact.Groups != 1 || planResp.Impact.Files != 1 {
		t.Fatalf("Impact 不符: %+v", planResp.Impact)
	}

	startTool := findTasksTool("start_task")
	if !startTool.Write {
		t.Fatal("start_task 必须是 Write=true（走确认门）")
	}
	startArgsJSON, _ := json.Marshal(map[string]string{"job_id": planResp.JobID})
	startArgs, err := ValidateArgs(startTool, string(startArgsJSON))
	if err != nil {
		t.Fatalf("ValidateArgs(start_task): %v", err)
	}

	// Write=true 工具不应被 agent loop 直接执行；这里模拟确认门之后
	// （actions.go executeConfirmedAction）直调 Exec。
	startOut, err := startTool.Exec(context.Background(), startArgs)
	if err != nil {
		t.Fatalf("start_task Exec（确认后）: %v", err)
	}
	var startResp map[string]interface{}
	if err := json.Unmarshal([]byte(startOut), &startResp); err != nil {
		t.Fatalf("start_task 输出非法 JSON: %v, raw=%s", err, startOut)
	}
	if _, isErr := startResp["error"]; isErr {
		t.Fatalf("start_task 确认后执行不应报错: %s", startOut)
	}

	job, ok := tasks.GetJob(planResp.JobID)
	if !ok {
		t.Fatalf("job 应存在: %s", planResp.JobID)
	}
	if job.Status != tasks.StatusRunning {
		t.Fatalf("StartJob 后状态应为 running, got %q", job.Status)
	}
}

// ─── get_task_list / get_task_detail ───

func TestGetTaskListAndDetailOutputContainsExpectedFields(t *testing.T) {
	poolRoot := setupTasksToolTest(t)
	content := []byte("list-detail-content")
	mustWriteFileAI(t, filepath.Join(poolRoot, "1.jpg"), content)
	mustWriteFileAI(t, filepath.Join(poolRoot, "2.jpg"), content)

	job, _, err := tasks.PlanPhotoDedupe("", time.Minute)
	if err != nil {
		t.Fatalf("PlanPhotoDedupe: %v", err)
	}

	listTool := findTasksTool("get_task_list")
	listArgs, _ := ValidateArgs(listTool, `{}`)
	listOut, err := listTool.Exec(context.Background(), listArgs)
	if err != nil {
		t.Fatalf("get_task_list Exec: %v", err)
	}
	var listResp struct {
		Jobs []map[string]interface{} `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(listOut), &listResp); err != nil {
		t.Fatalf("get_task_list 输出非法 JSON: %v, raw=%s", err, listOut)
	}
	if len(listResp.Jobs) == 0 {
		t.Fatalf("get_task_list 应至少返回刚创建的 job, raw=%s", listOut)
	}

	detailTool := findTasksTool("get_task_detail")
	detailArgsJSON, _ := json.Marshal(map[string]string{"job_id": job.ID})
	detailArgs, err := ValidateArgs(detailTool, string(detailArgsJSON))
	if err != nil {
		t.Fatalf("ValidateArgs(get_task_detail): %v", err)
	}
	detailOut, err := detailTool.Exec(context.Background(), detailArgs)
	if err != nil {
		t.Fatalf("get_task_detail Exec: %v", err)
	}
	var detailResp map[string]interface{}
	if err := json.Unmarshal([]byte(detailOut), &detailResp); err != nil {
		t.Fatalf("get_task_detail 输出非法 JSON: %v, raw=%s", err, detailOut)
	}
	for _, field := range []string{"id", "kind", "status", "result", "error"} {
		if _, ok := detailResp[field]; !ok {
			t.Fatalf("get_task_detail 输出缺字段 %q: %s", field, detailOut)
		}
	}
}

func TestGetTaskDetailUnknownJobIDReturnsErrorJSON(t *testing.T) {
	setupTasksToolTest(t)
	detailTool := findTasksTool("get_task_detail")
	detailArgs, _ := ValidateArgs(detailTool, `{"job_id":"job-does-not-exist"}`)
	out, err := detailTool.Exec(context.Background(), detailArgs)
	if err != nil {
		t.Fatalf("get_task_detail Exec: %v", err)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("输出非法 JSON: %v, raw=%s", err, out)
	}
	if _, ok := resp["error"]; !ok {
		t.Fatalf("未知 job_id 应返回 {\"error\":...}, got %s", out)
	}
}

// ─── pause/resume/cancel 状态机错误透传 ───

func TestPauseTaskIllegalTransitionReturnsErrorJSONNotGoErr(t *testing.T) {
	poolRoot := setupTasksToolTest(t)
	content := []byte("pause-illegal-content")
	mustWriteFileAI(t, filepath.Join(poolRoot, "1.jpg"), content)
	mustWriteFileAI(t, filepath.Join(poolRoot, "2.jpg"), content)

	job, _, err := tasks.PlanPhotoDedupe("", time.Minute)
	if err != nil {
		t.Fatalf("PlanPhotoDedupe: %v", err)
	}
	// job 仍是 pending_confirm，pause 应该是非法迁移。
	pauseTool := findTasksTool("pause_task")
	if pauseTool.Write {
		t.Fatal("pause_task 应是只读工具（Write=false），暂停无破坏性")
	}
	pauseArgsJSON, _ := json.Marshal(map[string]string{"job_id": job.ID})
	pauseArgs, err := ValidateArgs(pauseTool, string(pauseArgsJSON))
	if err != nil {
		t.Fatalf("ValidateArgs(pause_task): %v", err)
	}
	out, execErr := pauseTool.Exec(context.Background(), pauseArgs)
	if execErr != nil {
		t.Fatalf("pause_task Exec 不应返回 Go error（应转 JSON 错误字符串）: %v", execErr)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("输出非法 JSON: %v, raw=%s", err, out)
	}
	if _, ok := resp["error"]; !ok {
		t.Fatalf("非法状态迁移应返回 {\"error\":...}, got %s", out)
	}
}

func TestCancelTaskAndResumeTaskAreWriteTools(t *testing.T) {
	cancelTool := findTasksTool("cancel_task")
	if !cancelTool.Write {
		t.Fatal("cancel_task 必须是 Write=true")
	}
	resumeTool := findTasksTool("resume_task")
	if !resumeTool.Write {
		t.Fatal("resume_task 必须是 Write=true")
	}
}

// mustWriteFileAI 种一个池内 fixture 文件（含建目录），对照
// tasks/kinds_dedupe_test.go 的 mustWriteFile 同款模式（本包独立实现，
// 两个包互不借用对方的测试辅助函数）。
// ─── pendingActionSummary 新分支（start/cancel/resume_task + job 不存在兜底） ───

func TestPendingActionSummaryStartTaskFormatsImpactAndSize(t *testing.T) {
	setupTasksToolTest(t)
	job, err := tasks.CreateJob("photo_dedupe", "", nil, tasks.Impact{Groups: 3, Files: 5, Bytes: 2*1024*1024 + 512*1024})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	argsJSON, _ := json.Marshal(map[string]string{"job_id": job.ID})
	summary := pendingActionSummary("start_task", string(argsJSON))
	want := "启动任务：清理重复照片（3 组文件，释放 2.5MB）"
	if summary != want {
		t.Fatalf("summary = %q, want %q", summary, want)
	}
}

func TestPendingActionSummaryCancelTaskFormatsProgress(t *testing.T) {
	setupTasksToolTest(t)
	job, err := tasks.CreateJob("photo_dedupe", "", nil, tasks.Impact{Groups: 1, Files: 4})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := tasks.UpdateJobStatus(job.ID, tasks.StatusRunning, ""); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}
	if err := tasks.SetProgress(job.ID, 2, 4, "已处理 2/4"); err != nil {
		t.Fatalf("SetProgress: %v", err)
	}
	argsJSON, _ := json.Marshal(map[string]string{"job_id": job.ID})
	summary := pendingActionSummary("cancel_task", string(argsJSON))
	want := "取消任务：清理重复照片（当前进度 2/4）"
	if summary != want {
		t.Fatalf("summary = %q, want %q", summary, want)
	}
}

func TestPendingActionSummaryResumeTaskFormatsKindName(t *testing.T) {
	setupTasksToolTest(t)
	job, err := tasks.CreateJob("photo_dedupe", "", nil, tasks.Impact{})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	argsJSON, _ := json.Marshal(map[string]string{"job_id": job.ID})
	summary := pendingActionSummary("resume_task", string(argsJSON))
	want := "继续任务：清理重复照片"
	if summary != want {
		t.Fatalf("summary = %q, want %q", summary, want)
	}
}

// job 不存在 → 三个分支统一兜底"任务不存在或已清理"。
func TestPendingActionSummaryUnknownJobFallsBackForAllThreeBranches(t *testing.T) {
	setupTasksToolTest(t)
	argsJSON, _ := json.Marshal(map[string]string{"job_id": "job-does-not-exist"})
	for _, toolName := range []string{"start_task", "cancel_task", "resume_task"} {
		got := pendingActionSummary(toolName, string(argsJSON))
		if got != "任务不存在或已清理" {
			t.Fatalf("%s: summary = %q, want 任务不存在或已清理", toolName, got)
		}
	}
}

func mustWriteFileAI(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
