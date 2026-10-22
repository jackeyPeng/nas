package tasks

import (
	"path/filepath"
	"testing"
	"time"

	"nas-panel/common"
)

// resetForTest 清掉包级单例，让每个测试用全新的 t.TempDir() 数据目录初始化。
func resetForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	closeDBForTest()
	if err := InitTasks(dir); err != nil {
		t.Fatalf("InitTasks: %v", err)
	}
	t.Cleanup(closeDBForTest)
	return dir
}

func samplePlan() []Step {
	return []Step{
		{Key: "mv:/data/nas1/public/a.jpg", Op: "move_to_recycle", Args: map[string]string{"src": "public/a.jpg"}},
		{Key: "mv:/data/nas1/public/b.jpg", Op: "move_to_recycle", Args: map[string]string{"src": "public/b.jpg"}},
	}
}

func sampleImpact() Impact {
	return Impact{Groups: 1, Files: 2, Bytes: 2048}
}

func TestCreateJobGetJobRoundTrip(t *testing.T) {
	resetForTest(t)
	job, err := CreateJob("photo_dedupe", "public", samplePlan(), sampleImpact())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if job.Status != StatusPendingConfirm {
		t.Fatalf("CreateJob 初始状态 = %q, want pending_confirm", job.Status)
	}
	if job.ID == "" {
		t.Fatalf("CreateJob 未生成 ID")
	}

	got, ok := GetJob(job.ID)
	if !ok {
		t.Fatalf("GetJob(%q) 未找到", job.ID)
	}
	if got.Kind != "photo_dedupe" || got.ScopeRel != "public" {
		t.Fatalf("GetJob 基础字段不符: %+v", got)
	}
	if len(got.Plan) != 2 || got.Plan[0].Key != samplePlan()[0].Key || got.Plan[0].Args["src"] != "public/a.jpg" {
		t.Fatalf("GetJob plan 往返不一致: %+v", got.Plan)
	}
	if got.Impact != sampleImpact() {
		t.Fatalf("GetJob impact 往返不一致: %+v", got.Impact)
	}
}

func TestStateMachineLegalTransitions(t *testing.T) {
	resetForTest(t)

	legal := []struct {
		from Status
		to   Status
	}{
		{StatusPendingConfirm, StatusRunning},
		{StatusPendingConfirm, StatusCancelled},
		{StatusRunning, StatusPaused},
		{StatusRunning, StatusDone},
		{StatusRunning, StatusFailed},
		{StatusRunning, StatusCancelled},
		{StatusPaused, StatusRunning},
		{StatusPaused, StatusCancelled},
	}

	for _, tc := range legal {
		job, err := CreateJob("photo_dedupe", "", samplePlan(), sampleImpact())
		if err != nil {
			t.Fatalf("CreateJob: %v", err)
		}
		// 把状态沿合法路径摆到 tc.from（pending_confirm 是初始态）。
		for _, step := range pathTo(tc.from) {
			if err := UpdateJobStatus(job.ID, step, ""); err != nil {
				t.Fatalf("预置状态 %s 的中间迁移 -> %s 失败: %v", tc.from, step, err)
			}
		}
		if err := UpdateJobStatus(job.ID, tc.to, ""); err != nil {
			t.Fatalf("合法迁移 %s -> %s 被拒绝: %v", tc.from, tc.to, err)
		}
		got, _ := GetJob(job.ID)
		if got.Status != tc.to {
			t.Fatalf("迁移后状态 = %q, want %q", got.Status, tc.to)
		}
	}
}

// pathTo 返回从 pending_confirm 到达目标状态需要依次经过的合法迁移序列
// （不含 pending_confirm 本身）。
func pathTo(target Status) []Status {
	switch target {
	case StatusPendingConfirm:
		return nil
	case StatusRunning:
		return []Status{StatusRunning}
	case StatusPaused:
		return []Status{StatusRunning, StatusPaused}
	case StatusDone, StatusFailed, StatusCancelled:
		return []Status{StatusRunning, target}
	default:
		return nil
	}
}

func TestStateMachineIllegalTransitionsRejected(t *testing.T) {
	resetForTest(t)

	illegal := []struct {
		from Status
		to   Status
	}{
		{StatusDone, StatusRunning},
		{StatusFailed, StatusRunning},
		{StatusCancelled, StatusRunning},
		{StatusPendingConfirm, StatusPaused},
		{StatusPendingConfirm, StatusDone},
		{StatusDone, StatusCancelled},
		{StatusPaused, StatusDone},
	}

	for _, tc := range illegal {
		job, err := CreateJob("photo_dedupe", "", samplePlan(), sampleImpact())
		if err != nil {
			t.Fatalf("CreateJob: %v", err)
		}
		for _, step := range pathTo(tc.from) {
			if err := UpdateJobStatus(job.ID, step, ""); err != nil {
				t.Fatalf("预置状态 %s 的中间迁移 -> %s 失败: %v", tc.from, step, err)
			}
		}
		if err := UpdateJobStatus(job.ID, tc.to, ""); err == nil {
			t.Fatalf("非法迁移 %s -> %s 应被拒绝，却成功了", tc.from, tc.to)
		}
		got, _ := GetJob(job.ID)
		if got.Status != tc.from {
			t.Fatalf("非法迁移后状态被篡改: got %q, want 仍为 %q", got.Status, tc.from)
		}
	}
}

func TestListJobsNewestFirst(t *testing.T) {
	resetForTest(t)
	var ids []string
	for i := 0; i < 3; i++ {
		job, err := CreateJob("photo_dedupe", "", samplePlan(), sampleImpact())
		if err != nil {
			t.Fatalf("CreateJob: %v", err)
		}
		ids = append(ids, job.ID)
		time.Sleep(2 * time.Millisecond) // 确保 created_at 有序区分
	}

	jobs := ListJobs(10)
	if len(jobs) != 3 {
		t.Fatalf("ListJobs 返回 %d 条, want 3", len(jobs))
	}
	if jobs[0].ID != ids[2] || jobs[2].ID != ids[0] {
		t.Fatalf("ListJobs 未按新->旧排序: %v", jobs)
	}

	limited := ListJobs(1)
	if len(limited) != 1 || limited[0].ID != ids[2] {
		t.Fatalf("ListJobs(1) = %+v, want 只含最新一条", limited)
	}
}

func TestFindPendingJobHitAndMiss(t *testing.T) {
	resetForTest(t)
	job, err := CreateJob("photo_dedupe", "public", samplePlan(), sampleImpact())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	found, ok := FindPendingJob("photo_dedupe", "public")
	if !ok || found.ID != job.ID {
		t.Fatalf("FindPendingJob 应命中刚创建的 pending_confirm job, got ok=%v found=%+v", ok, found)
	}

	if _, ok := FindPendingJob("photo_dedupe", "private"); ok {
		t.Fatalf("FindPendingJob 对不同 scope 不应命中")
	}
	if _, ok := FindPendingJob("bulk_rename", "public"); ok {
		t.Fatalf("FindPendingJob 对不同 kind 不应命中")
	}

	// 状态迁出 pending_confirm 后不应再被找到
	if err := UpdateJobStatus(job.ID, StatusRunning, ""); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}
	if _, ok := FindPendingJob("photo_dedupe", "public"); ok {
		t.Fatalf("job 已非 pending_confirm，FindPendingJob 不应再命中")
	}
}

func TestCheckpointSaveLoadIdempotent(t *testing.T) {
	resetForTest(t)
	job, err := CreateJob("photo_dedupe", "", samplePlan(), sampleImpact())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	empty := LoadCheckpoint(job.ID)
	if len(empty) != 0 {
		t.Fatalf("初始 checkpoint 应为空, got %v", empty)
	}

	if err := SaveCheckpoint(job.ID, []string{"mv:/a"}); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}
	cp := LoadCheckpoint(job.ID)
	if !cp["mv:/a"] || len(cp) != 1 {
		t.Fatalf("checkpoint 后 = %v, want {mv:/a}", cp)
	}

	// 追加合并：第二次 SaveCheckpoint 带新 key，旧 key 应保留（不是覆盖）
	if err := SaveCheckpoint(job.ID, []string{"mv:/b"}); err != nil {
		t.Fatalf("SaveCheckpoint 追加: %v", err)
	}
	cp = LoadCheckpoint(job.ID)
	if !cp["mv:/a"] || !cp["mv:/b"] || len(cp) != 2 {
		t.Fatalf("checkpoint 追加合并后 = %v, want {mv:/a, mv:/b}", cp)
	}

	// 幂等：重复保存同一个 key 不应产生重复或出错
	if err := SaveCheckpoint(job.ID, []string{"mv:/a"}); err != nil {
		t.Fatalf("SaveCheckpoint 重复 key: %v", err)
	}
	cp = LoadCheckpoint(job.ID)
	if len(cp) != 2 {
		t.Fatalf("重复保存同一 key 后 checkpoint 应仍为 2 项, got %v", cp)
	}
}

func TestSetProgressAndFinishJob(t *testing.T) {
	resetForTest(t)
	job, err := CreateJob("photo_dedupe", "", samplePlan(), sampleImpact())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := UpdateJobStatus(job.ID, StatusRunning, ""); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}

	if err := SetProgress(job.ID, 1, 2, "处理中"); err != nil {
		t.Fatalf("SetProgress: %v", err)
	}
	got, _ := GetJob(job.ID)
	if got.ProgressCur != 1 || got.ProgressTotal != 2 || got.ProgressNote != "处理中" {
		t.Fatalf("SetProgress 后字段不符: %+v", got)
	}

	// total=0 防除零：不应 panic，字段按原样记录
	if err := SetProgress(job.ID, 0, 0, "空任务"); err != nil {
		t.Fatalf("SetProgress total=0: %v", err)
	}

	if err := FinishJob(job.ID, `{"moved":2,"freed":2048,"skipped":0}`, "", StatusDone); err != nil {
		t.Fatalf("FinishJob: %v", err)
	}
	got, _ = GetJob(job.ID)
	if got.Status != StatusDone {
		t.Fatalf("FinishJob 后状态 = %q, want done", got.Status)
	}
	if got.Result == "" {
		t.Fatalf("FinishJob 后 Result 应非空")
	}
	if got.FinishedAt == nil {
		t.Fatalf("FinishJob 后 FinishedAt 应被设置")
	}
}

func TestInitTasksIdempotentOnExistingDB(t *testing.T) {
	dir := t.TempDir()
	closeDBForTest()
	if err := InitTasks(dir); err != nil {
		t.Fatalf("第一次 InitTasks: %v", err)
	}
	job, err := CreateJob("photo_dedupe", "", samplePlan(), sampleImpact())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	closeDBForTest()

	// 重新 InitTasks 指向同一目录：应幂等，不丢数据、不报错
	if err := InitTasks(dir); err != nil {
		t.Fatalf("第二次 InitTasks（已存在库）: %v", err)
	}
	t.Cleanup(closeDBForTest)

	got, ok := GetJob(job.ID)
	if !ok {
		t.Fatalf("InitTasks 幂等重建后旧数据丢失")
	}
	if got.Kind != "photo_dedupe" {
		t.Fatalf("InitTasks 幂等重建后数据损坏: %+v", got)
	}
}

// TestInitTasksCrashRecoveryPausesRunningJobs 覆盖崩溃恢复语义（收紧版，
// 比 spec 的"自动续传"保守）：面板重启时若 jobs 表里还有 status=running 的行，
// 说明上次是被 kill -9/断电中断的，而不是正常走到终态——这种行一律改成
// paused 并在 progress_note 追加提示，交给用户主动点"继续"才会再动文件。
//
// 理由：任务引擎的默认操作是移动文件进回收站，无人值守地自动续传意味着
// 面板一重启就可能在用户不知情的情况下继续挪文件——对纯家庭小白用户，
// "不自动做可能出错的事，等一个确认"比"尽快恢复进度"更安全。
func TestInitTasksCrashRecoveryPausesRunningJobs(t *testing.T) {
	dir := t.TempDir()
	closeDBForTest()
	if err := InitTasks(dir); err != nil {
		t.Fatalf("第一次 InitTasks: %v", err)
	}
	runningJob, err := CreateJob("photo_dedupe", "", samplePlan(), sampleImpact())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := UpdateJobStatus(runningJob.ID, StatusRunning, ""); err != nil {
		t.Fatalf("UpdateJobStatus running: %v", err)
	}
	if err := SetProgress(runningJob.ID, 1, 2, "处理中"); err != nil {
		t.Fatalf("SetProgress: %v", err)
	}

	// 对照组：pending_confirm 的 job 不该被崩溃恢复逻辑碰
	pendingJob, err := CreateJob("photo_dedupe", "other", samplePlan(), sampleImpact())
	if err != nil {
		t.Fatalf("CreateJob pending: %v", err)
	}

	closeDBForTest() // 模拟进程被 kill：不走任何优雅关闭/终态收尾

	if err := InitTasks(dir); err != nil {
		t.Fatalf("重启后 InitTasks: %v", err)
	}
	t.Cleanup(closeDBForTest)

	got, ok := GetJob(runningJob.ID)
	if !ok {
		t.Fatalf("崩溃恢复后 job 应仍存在")
	}
	if got.Status != StatusPaused {
		t.Fatalf("崩溃恢复后 running job 状态 = %q, want paused", got.Status)
	}
	if !containsSubstring(got.ProgressNote, "面板重启已暂停，可继续") {
		t.Fatalf("崩溃恢复后 progress_note 应追加提示, got %q", got.ProgressNote)
	}
	// 进度游标本身不应被清零/篡改（后续 resume 还要从这接着判断）
	if got.ProgressCur != 1 || got.ProgressTotal != 2 {
		t.Fatalf("崩溃恢复不应清除既有进度: %+v", got)
	}

	gotPending, ok := GetJob(pendingJob.ID)
	if !ok {
		t.Fatalf("pending_confirm job 应仍存在")
	}
	if gotPending.Status != StatusPendingConfirm {
		t.Fatalf("崩溃恢复不应影响 pending_confirm job，got %q", gotPending.Status)
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// TestCountersPersistAcrossReinit：计数器必须活过进程重启——kill -9 后
// 续跑段 Result 依赖库里的累计值（48 机 E2E 实测抓到的 moved=23/实为149 根修）。
func TestCountersPersistAcrossReinit(t *testing.T) {
	dir := resetForTest(t)
	job, err := CreateJob("photo_dedupe", "", samplePlan(), sampleImpact())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := AddCounters(job.ID, map[string]int64{"moved": 126, "freed": 126000000}); err != nil {
		t.Fatalf("AddCounters: %v", err)
	}
	// 模拟进程重启：关库后对同一目录重新 InitTasks
	closeDBForTest()
	if err := InitTasks(dir); err != nil {
		t.Fatalf("re-InitTasks: %v", err)
	}
	// 续跑段再累计
	if err := AddCounters(job.ID, map[string]int64{"moved": 23, "freed": 23000000, "skipped": 1}); err != nil {
		t.Fatalf("AddCounters 续: %v", err)
	}
	c := LoadCounters(job.ID)
	if c["moved"] != 149 || c["freed"] != 149000000 || c["skipped"] != 1 {
		t.Fatalf("重启后累计计数器 = %v, want moved=149 freed=149000000 skipped=1", c)
	}
}

// TestCountersMigrationFromOldSchema：F1-rc1 老库没有 counters_json 列，
// InitTasks 必须 ALTER 补列且不丢既有行。
func TestCountersMigrationFromOldSchema(t *testing.T) {
	dir := t.TempDir()
	closeDBForTest()
	// 手工建老 schema 库（无 counters_json）+ 一行数据
	old, err := common.OpenSQLite(filepath.Join(dir, "tasks.db"))
	if err != nil {
		t.Fatalf("open old db: %v", err)
	}
	if _, err := old.Exec(`CREATE TABLE jobs (
		id TEXT PRIMARY KEY, kind TEXT NOT NULL, status TEXT NOT NULL,
		scope_rel TEXT NOT NULL DEFAULT '', plan_json TEXT NOT NULL DEFAULT '[]',
		impact_json TEXT NOT NULL DEFAULT '{}', progress_cur INTEGER NOT NULL DEFAULT 0,
		progress_total INTEGER NOT NULL DEFAULT 0, progress_note TEXT NOT NULL DEFAULT '',
		checkpoint_json TEXT NOT NULL DEFAULT '{}', result_json TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, started_at TEXT, finished_at TEXT)`); err != nil {
		t.Fatalf("create old schema: %v", err)
	}
	if _, err := old.Exec(`INSERT INTO jobs (id,kind,status,created_at) VALUES ('job-old','photo_dedupe','done','2026-10-22T00:00:00Z')`); err != nil {
		t.Fatalf("insert old row: %v", err)
	}
	old.Close()
	// InitTasks 应迁移补列并保留旧行
	if err := InitTasks(dir); err != nil {
		t.Fatalf("InitTasks on old db: %v", err)
	}
	t.Cleanup(closeDBForTest)
	j, ok := GetJob("job-old")
	if !ok || j.Status != StatusDone {
		t.Fatalf("迁移后旧行丢失: %v %v", j, ok)
	}
	if err := AddCounters("job-old", map[string]int64{"moved": 1}); err != nil {
		t.Fatalf("迁移后 AddCounters: %v", err)
	}
	if c := LoadCounters("job-old"); c["moved"] != 1 {
		t.Fatalf("迁移后计数器 = %v, want moved=1", c)
	}
	// 幂等：再跑一次 InitTasks 不应报 duplicate column
	closeDBForTest()
	if err := InitTasks(dir); err != nil {
		t.Fatalf("二次 InitTasks: %v", err)
	}
}
