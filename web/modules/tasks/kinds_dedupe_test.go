package tasks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nas-panel/common"
	"nas-panel/modules/filemgmt"
)

// F1 Task 4 Step 1：kinds_dedupe_test.go 失败测试（t.TempDir 池 fixture）。

// setupDedupePool 建一个真实池根（filemgmt.SetPoolRootOverrideForTest
// 跨包注入，对照 filemgmt/path_test.go 的 setupFakePool 模式），并建
// 本用例共用的目录骨架。
func setupDedupePool(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	filemgmt.SetPoolRootOverrideForTest(root)
	t.Cleanup(func() { filemgmt.SetPoolRootOverrideForTest("") })
	return root
}

func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestPlanPhotoDedupeGroupsExactDuplicatesAcrossDirs(t *testing.T) {
	root := resetForTest(t)
	_ = root
	poolRoot := setupDedupePool(t)

	content := []byte("same-bytes-across-three-copies")
	mustWriteFile(t, filepath.Join(poolRoot, "a", "1.jpg"), content)
	mustWriteFile(t, filepath.Join(poolRoot, "b", "2.jpg"), content)
	mustWriteFile(t, filepath.Join(poolRoot, "c", "deep", "3.jpg"), content)

	job, partial, err := PlanPhotoDedupe("", time.Minute)
	if err != nil {
		t.Fatalf("PlanPhotoDedupe: %v", err)
	}
	if partial {
		t.Fatalf("预算充足不应 partial")
	}
	if job.Impact.Groups != 1 {
		t.Fatalf("Groups = %d, want 1", job.Impact.Groups)
	}
	if job.Impact.Files != 2 {
		t.Fatalf("Files（将移动数）= %d, want 2（保留 1 份）", job.Impact.Files)
	}
	if job.Impact.Bytes != int64(len(content))*2 {
		t.Fatalf("Bytes = %d, want %d", job.Impact.Bytes, int64(len(content))*2)
	}
	if len(job.Plan) != 2 {
		t.Fatalf("plan 步骤数 = %d, want 2", len(job.Plan))
	}
	// 保留者=路径最短 → "a/1.jpg" 最短，应被保留（不出现在 plan 里）。
	for _, step := range job.Plan {
		if step.Op != "move_to_recycle" {
			t.Fatalf("plan 步骤应只含 move_to_recycle, got %q", step.Op)
		}
		if step.Args["src"] == filepath.ToSlash(filepath.Join("a", "1.jpg")) {
			t.Fatalf("最短路径的副本应被保留，不应出现在 plan 里: %+v", step)
		}
		if step.Key == "" {
			t.Fatalf("step.Key 不应为空")
		}
	}
}

// 同 size 不同内容（头哈希相同、全量不同）——两级哈希分流断言：不应被
// 误判为一组。
func TestPlanPhotoDedupeTwoLevelHashSeparatesSameSizeDifferentContent(t *testing.T) {
	poolRoot := setupDedupePool(t)
	resetForTest(t)

	// 构造两个同 size 但内容不同的 png：前 64KB 相同，之后不同。
	head := make([]byte, 70*1024)
	for i := range head {
		head[i] = byte(i % 251)
	}
	fileA := append([]byte{}, head...)
	fileA = append(fileA, []byte("TAIL-A")...)
	fileB := append([]byte{}, head...)
	fileB = append(fileB, []byte("TAIL-B")...)
	if len(fileA) != len(fileB) {
		t.Fatalf("两个 fixture 文件长度应一致: %d vs %d", len(fileA), len(fileB))
	}

	mustWriteFile(t, filepath.Join(poolRoot, "x.png"), fileA)
	mustWriteFile(t, filepath.Join(poolRoot, "y.png"), fileB)

	job, partial, err := PlanPhotoDedupe("", time.Minute)
	if err != nil {
		t.Fatalf("PlanPhotoDedupe: %v", err)
	}
	if partial {
		t.Fatalf("不应 partial")
	}
	if job.Impact.Groups != 0 || len(job.Plan) != 0 {
		t.Fatalf("头哈希相同全量不同不应成组: groups=%d plan=%+v", job.Impact.Groups, job.Plan)
	}
}

// 视频重复同样走扩展名白名单 + 两级哈希。
func TestPlanPhotoDedupeGroupsDuplicateVideos(t *testing.T) {
	poolRoot := setupDedupePool(t)
	resetForTest(t)

	content := []byte("video-bytes-duplicate")
	mustWriteFile(t, filepath.Join(poolRoot, "clip1.mp4"), content)
	mustWriteFile(t, filepath.Join(poolRoot, "sub", "clip2.mp4"), content)

	job, _, err := PlanPhotoDedupe("", time.Minute)
	if err != nil {
		t.Fatalf("PlanPhotoDedupe: %v", err)
	}
	if job.Impact.Groups != 1 || job.Impact.Files != 1 {
		t.Fatalf("视频重复组应识别: groups=%d files=%d", job.Impact.Groups, job.Impact.Files)
	}
}

// .recycle 内重复必须跳过。
func TestPlanPhotoDedupeSkipsRecycleSegment(t *testing.T) {
	poolRoot := setupDedupePool(t)
	resetForTest(t)

	content := []byte("recycle-skip-content")
	mustWriteFile(t, filepath.Join(poolRoot, "keep.jpg"), content)
	mustWriteFile(t, filepath.Join(poolRoot, ".recycle", "fm", "keep.jpg"), content)
	mustWriteFile(t, filepath.Join(poolRoot, "#recycle", "keep.jpg"), content)

	job, _, err := PlanPhotoDedupe("", time.Minute)
	if err != nil {
		t.Fatalf("PlanPhotoDedupe: %v", err)
	}
	if job.Impact.Groups != 0 {
		t.Fatalf(".recycle/#recycle 段内容不应参与成组: groups=%d plan=%+v", job.Impact.Groups, job.Plan)
	}
}

// 隐藏文件跳过。
func TestPlanPhotoDedupeSkipsHiddenFiles(t *testing.T) {
	poolRoot := setupDedupePool(t)
	resetForTest(t)

	content := []byte("hidden-skip-content")
	mustWriteFile(t, filepath.Join(poolRoot, "visible.jpg"), content)
	mustWriteFile(t, filepath.Join(poolRoot, ".hidden.jpg"), content)

	job, _, err := PlanPhotoDedupe("", time.Minute)
	if err != nil {
		t.Fatalf("PlanPhotoDedupe: %v", err)
	}
	if job.Impact.Groups != 0 {
		t.Fatalf("隐藏文件不应参与成组: groups=%d", job.Impact.Groups)
	}
}

// 符号链接跳过（Lstat 判定，不跟随）。
func TestPlanPhotoDedupeSkipsSymlinks(t *testing.T) {
	poolRoot := setupDedupePool(t)
	resetForTest(t)

	content := []byte("symlink-skip-content")
	real := filepath.Join(poolRoot, "real.jpg")
	mustWriteFile(t, real, content)
	mustWriteFile(t, filepath.Join(poolRoot, "dup.jpg"), content)
	if err := os.Symlink(real, filepath.Join(poolRoot, "link.jpg")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	job, _, err := PlanPhotoDedupe("", time.Minute)
	if err != nil {
		t.Fatalf("PlanPhotoDedupe: %v", err)
	}
	if job.Impact.Groups != 1 || job.Impact.Files != 1 {
		t.Fatalf("应只有 real.jpg+dup.jpg 一组(link 跳过): groups=%d files=%d", job.Impact.Groups, job.Impact.Files)
	}
	for _, step := range job.Plan {
		if step.Args["src"] == "link.jpg" {
			t.Fatalf("符号链接不应出现在 plan 里: %+v", step)
		}
	}
}

// 超预算场景：budget 极小 → partial=true。
func TestPlanPhotoDedupeBudgetTimeoutReturnsPartial(t *testing.T) {
	poolRoot := setupDedupePool(t)
	resetForTest(t)

	for i := 0; i < 200; i++ {
		mustWriteFile(t, filepath.Join(poolRoot, "bulk", padName(i)+".jpg"), []byte(padName(i)))
	}

	_, partial, err := PlanPhotoDedupe("", time.Nanosecond)
	if err != nil {
		t.Fatalf("PlanPhotoDedupe: %v", err)
	}
	if !partial {
		t.Fatalf("预算 1ns 应 partial=true")
	}
}

func padName(i int) string {
	return "f" + string(rune('a'+i%26)) + string(rune('0'+i%10))
}

// PlanPhotoDedupe 二次调用命中 FindPendingJob，直接返回同一 job，不重扫。
func TestPlanPhotoDedupeSecondCallReturnsSamePendingJob(t *testing.T) {
	poolRoot := setupDedupePool(t)
	resetForTest(t)

	content := []byte("same-pending-job-content")
	mustWriteFile(t, filepath.Join(poolRoot, "a.jpg"), content)
	mustWriteFile(t, filepath.Join(poolRoot, "b.jpg"), content)

	job1, _, err := PlanPhotoDedupe("", time.Minute)
	if err != nil {
		t.Fatalf("PlanPhotoDedupe #1: %v", err)
	}

	// 扫描后再加一个新重复文件：第二次调用若真的重扫会把它纳入 plan；
	// 命中 FindPendingJob 直接返回同一 job 则不会。
	mustWriteFile(t, filepath.Join(poolRoot, "c.jpg"), content)

	job2, _, err := PlanPhotoDedupe("", time.Minute)
	if err != nil {
		t.Fatalf("PlanPhotoDedupe #2: %v", err)
	}
	if job2.ID != job1.ID {
		t.Fatalf("二次调用应返回同一 pending job, got %s vs %s", job2.ID, job1.ID)
	}
	if len(job2.Plan) != len(job1.Plan) {
		t.Fatalf("二次调用不应重扫（plan 应不变）: %d vs %d", len(job2.Plan), len(job1.Plan))
	}
}

// scopeRel="../etc" 拒绝。
func TestPlanPhotoDedupeRejectsTraversalScope(t *testing.T) {
	setupDedupePool(t)
	resetForTest(t)

	if _, _, err := PlanPhotoDedupe("../etc", time.Minute); err == nil {
		t.Fatalf("越界 scopeRel 应报错")
	}
}

// 非普通文件（设备/管道等）跳过——用目录模拟"非 jpg/视频扩展名"场景已
// 覆盖扩展名白名单；本用例确认目录本身不会被当成候选文件误处理。
// F1 Task 4 Step 3：执行链单测（真 op，不是 fake）—— 种重复文件 →
// PlanPhotoDedupe → StartJob → worker 跑完 → 断言重复文件在 .recycle 下、
// 保留者在原位、Result JSON {moved,freed,skipped} 数字对、eventbus
// task.done 收到。
func TestPhotoDedupeExecutionChainMovesToRecycleAndReportsResult(t *testing.T) {
	poolRoot := setupDedupePool(t)
	resetWorkerForTest(t)

	content := []byte("end-to-end-dedupe-content")
	// 保留者：最短路径 "a.jpg"；其余两份应进回收站。
	mustWriteFile(t, filepath.Join(poolRoot, "a.jpg"), content)
	mustWriteFile(t, filepath.Join(poolRoot, "bb", "b.jpg"), content)
	mustWriteFile(t, filepath.Join(poolRoot, "ccc", "c.jpg"), content)

	job, partial, err := PlanPhotoDedupe("", time.Minute)
	if err != nil {
		t.Fatalf("PlanPhotoDedupe: %v", err)
	}
	if partial {
		t.Fatalf("不应 partial")
	}
	if job.Impact.Files != 2 {
		t.Fatalf("Impact.Files = %d, want 2", job.Impact.Files)
	}

	sub := common.Subscribe()
	defer common.Unsubscribe(sub)

	if err := StartJob(job.ID); err != nil {
		t.Fatalf("StartJob: %v", err)
	}

	done := waitForStatus(t, job.ID, 5*time.Second, StatusDone, StatusFailed)
	if done.Status != StatusDone {
		t.Fatalf("job 应 done, got %q err=%s", done.Status, done.Error)
	}

	// 保留者原位。
	if _, err := os.Stat(filepath.Join(poolRoot, "a.jpg")); err != nil {
		t.Fatalf("保留者应仍在原位: %v", err)
	}
	// 其余两份应已不在原位（已移入各自所属的 .recycle）。
	if _, err := os.Lstat(filepath.Join(poolRoot, "bb", "b.jpg")); err == nil {
		t.Fatalf("重复文件应已移出原位: bb/b.jpg")
	}
	if _, err := os.Lstat(filepath.Join(poolRoot, "ccc", "c.jpg")); err == nil {
		t.Fatalf("重复文件应已移出原位: ccc/c.jpg")
	}

	var result struct {
		Moved   int   `json:"moved"`
		Freed   int64 `json:"freed"`
		Skipped int   `json:"skipped"`
	}
	if err := json.Unmarshal([]byte(done.Result), &result); err != nil {
		t.Fatalf("Result JSON 解析失败: %v, raw=%s", err, done.Result)
	}
	if result.Moved != 2 {
		t.Fatalf("Result.Moved = %d, want 2", result.Moved)
	}
	if result.Freed != int64(len(content))*2 {
		t.Fatalf("Result.Freed = %d, want %d", result.Freed, int64(len(content))*2)
	}
	if result.Skipped != 0 {
		t.Fatalf("Result.Skipped = %d, want 0", result.Skipped)
	}

	deadline := time.After(2 * time.Second)
	gotDone := false
	for !gotDone {
		select {
		case ev := <-sub:
			if ev.Source == "tasks" && ev.Code == "task.done" {
				gotDone = true
			}
		case <-deadline:
			t.Fatalf("超时未收到 tasks/task.done 事件")
		}
	}
}

// 幂等 skip：checkpoint 已记但源已不存在（模拟"文件被手动移走后任务续
// 跑"）——不应整任务失败，skipped 计数 +1。
//
// 本用例不断言 eventbus 事件投递，故用 InitEventBusForTest（无后台
// writer goroutine）而非 resetWorkerForTest 的真实 InitEventBus——
// 对照 TestWorkerConcurrentStartPauseCancelNoRace 的既有选择，规避
// common/eventbus.go 既有的 test-cleanup 竞争（记录在
// OPENCODE-REPORT-F1-R1.md 偏离与疑虑第 3 条，非本轮改动范围）。
func TestPhotoDedupeExecutionChainIdempotentSkipWhenSourceAlreadyGone(t *testing.T) {
	poolRoot := setupDedupePool(t)
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)
	StartWorker()
	t.Cleanup(StopWorkerForTest)

	content := []byte("idempotent-skip-content")
	mustWriteFile(t, filepath.Join(poolRoot, "keep.jpg"), content)
	victim := filepath.Join(poolRoot, "zz", "dup.jpg")
	mustWriteFile(t, victim, content)

	job, _, err := PlanPhotoDedupe("", time.Minute)
	if err != nil {
		t.Fatalf("PlanPhotoDedupe: %v", err)
	}
	if len(job.Plan) != 1 {
		t.Fatalf("plan 步骤数 = %d, want 1", len(job.Plan))
	}

	// 手动把源文件挪走，模拟"确认计划之后、任务真正执行之前，文件已被
	// 用户手动移动/删除"。
	if err := os.Remove(victim); err != nil {
		t.Fatalf("预先删除源失败: %v", err)
	}

	if err := StartJob(job.ID); err != nil {
		t.Fatalf("StartJob: %v", err)
	}
	done := waitForStatus(t, job.ID, 5*time.Second, StatusDone, StatusFailed)
	if done.Status != StatusDone {
		t.Fatalf("源已不存在不应整任务失败, got %q err=%s", done.Status, done.Error)
	}

	var result struct {
		Moved   int `json:"moved"`
		Skipped int `json:"skipped"`
	}
	if err := json.Unmarshal([]byte(done.Result), &result); err != nil {
		t.Fatalf("Result JSON 解析失败: %v, raw=%s", err, done.Result)
	}
	if result.Skipped != 1 {
		t.Fatalf("Result.Skipped = %d, want 1", result.Skipped)
	}
	if result.Moved != 0 {
		t.Fatalf("Result.Moved = %d, want 0", result.Moved)
	}
}

func TestPlanPhotoDedupeIgnoresNonWhitelistedExtensions(t *testing.T) {
	poolRoot := setupDedupePool(t)
	resetForTest(t)

	content := []byte("not-a-media-file")
	mustWriteFile(t, filepath.Join(poolRoot, "a.txt"), content)
	mustWriteFile(t, filepath.Join(poolRoot, "b.txt"), content)

	job, _, err := PlanPhotoDedupe("", time.Minute)
	if err != nil {
		t.Fatalf("PlanPhotoDedupe: %v", err)
	}
	if job.Impact.Groups != 0 {
		t.Fatalf(".txt 不在扩展名白名单，不应成组: groups=%d", job.Impact.Groups)
	}
}
