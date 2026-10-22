package tasks

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"nas-panel/common"
)

// resetWorkerForTest 初始化任务库 + 事件总线（InitEventBusForTest，手动
// DrainEventsForTest 消费，不起后台 writer goroutine），并启动 worker。
func resetWorkerForTest(t *testing.T) {
	t.Helper()
	resetForTest(t)
	// worker 异步投递 task.* 事件给 common.Subscribe() 订阅者，必须有真实
	// 后台 writer goroutine 才会广播（InitEventBusForTest 不起 goroutine，
	// 那是配合 DrainEventsForTest 同步消费场景的，这里要的是真异步）。
	common.InitEventBus(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)
	StartWorker()
	t.Cleanup(StopWorkerForTest)
}

// fakeStepPlan 生成 n 步的 Step 计划，op 用传入的 opName（必须先 RegisterOp）。
func fakeStepPlan(opName string, n int) []Step {
	plan := make([]Step, n)
	for i := 0; i < n; i++ {
		plan[i] = Step{
			Key:  fmt.Sprintf("step:%d", i),
			Op:   opName,
			Args: map[string]string{"n": fmt.Sprintf("%d", i)},
		}
	}
	return plan
}

// waitForStatus 轮询直到 job 到达 want 之一或超时。
func waitForStatus(t *testing.T, id string, timeout time.Duration, want ...Status) Job {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		job, ok := GetJob(id)
		if ok {
			for _, w := range want {
				if job.Status == w {
					return job
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	job, _ := GetJob(id)
	t.Fatalf("等待状态 %v 超时，当前状态 %q", want, job.Status)
	return Job{}
}

func TestWorkerRunsStepsInOrderAndFinishes(t *testing.T) {
	resetWorkerForTest(t)

	var mu sync.Mutex
	var executed []string
	RegisterOp("t1_seq", func(ctx context.Context, step Step) error {
		mu.Lock()
		executed = append(executed, step.Key)
		mu.Unlock()
		return nil
	})

	job, err := CreateJob("photo_dedupe", "", fakeStepPlan("t1_seq", 3), Impact{Files: 3})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := StartJob(job.ID); err != nil {
		t.Fatalf("StartJob: %v", err)
	}

	done := waitForStatus(t, job.ID, 2*time.Second, StatusDone, StatusFailed)
	if done.Status != StatusDone {
		t.Fatalf("job 应 done, got %q err=%s", done.Status, done.Error)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(executed) != 3 {
		t.Fatalf("执行步骤数 = %d, want 3: %v", len(executed), executed)
	}
	for i, key := range executed {
		want := fmt.Sprintf("step:%d", i)
		if key != want {
			t.Fatalf("执行顺序错乱: executed[%d]=%s, want %s", i, key, want)
		}
	}
	if done.ProgressCur != 3 || done.ProgressTotal != 3 {
		t.Fatalf("完成后进度应为 3/3, got %d/%d", done.ProgressCur, done.ProgressTotal)
	}
}

func TestWorkerPauseAtStepBoundaryThenResumeSkipsCheckpointedSteps(t *testing.T) {
	resetWorkerForTest(t)

	var mu sync.Mutex
	var executed []string
	block := make(chan struct{})
	release := make(chan struct{})

	RegisterOp("t2_pause", func(ctx context.Context, step Step) error {
		mu.Lock()
		executed = append(executed, step.Key)
		n := len(executed)
		mu.Unlock()
		if n == 1 {
			close(block) // 通知测试主逻辑：第 1 步已开始执行
			<-release    // 等待测试放行，模拟"第 2 步前阻塞" 场景的时序控制点
		}
		return nil
	})

	job, err := CreateJob("photo_dedupe", "", fakeStepPlan("t2_pause", 3), Impact{Files: 3})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := StartJob(job.ID); err != nil {
		t.Fatalf("StartJob: %v", err)
	}

	<-block // 第 1 步已跑完，worker 正阻塞在 release 上（尚未进入第 2 步）
	if err := PauseJob(job.ID); err != nil {
		t.Fatalf("PauseJob: %v", err)
	}
	close(release)

	paused := waitForStatus(t, job.ID, 2*time.Second, StatusPaused)
	if paused.Status != StatusPaused {
		t.Fatalf("job 应 paused, got %q", paused.Status)
	}

	mu.Lock()
	ranBeforePause := len(executed)
	mu.Unlock()
	if ranBeforePause != 1 {
		t.Fatalf("暂停前应只跑完 1 步, got %d: %v", ranBeforePause, executed)
	}

	if err := ResumeJob(job.ID); err != nil {
		t.Fatalf("ResumeJob: %v", err)
	}
	done := waitForStatus(t, job.ID, 2*time.Second, StatusDone, StatusFailed)
	if done.Status != StatusDone {
		t.Fatalf("resume 后应 done, got %q err=%s", done.Status, done.Error)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(executed) != 3 {
		t.Fatalf("resume 后总执行步骤数 = %d, want 3（第1步不重跑）: %v", len(executed), executed)
	}
	if executed[0] != "step:0" {
		t.Fatalf("第 1 步不应重跑: %v", executed)
	}
}

func TestWorkerCancelStopsRemainingSteps(t *testing.T) {
	resetWorkerForTest(t)

	var mu sync.Mutex
	var executed []string
	block := make(chan struct{})
	release := make(chan struct{})

	RegisterOp("t3_cancel", func(ctx context.Context, step Step) error {
		mu.Lock()
		executed = append(executed, step.Key)
		n := len(executed)
		mu.Unlock()
		if n == 1 {
			close(block)
			<-release
		}
		return nil
	})

	job, err := CreateJob("photo_dedupe", "", fakeStepPlan("t3_cancel", 5), Impact{Files: 5})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := StartJob(job.ID); err != nil {
		t.Fatalf("StartJob: %v", err)
	}

	<-block
	if err := CancelJob(job.ID); err != nil {
		t.Fatalf("CancelJob: %v", err)
	}
	close(release)

	cancelled := waitForStatus(t, job.ID, 2*time.Second, StatusCancelled)
	if cancelled.Status != StatusCancelled {
		t.Fatalf("job 应 cancelled, got %q", cancelled.Status)
	}

	time.Sleep(50 * time.Millisecond) // 给 worker goroutine 一点时间，确认后续步骤确实不会再跑
	mu.Lock()
	defer mu.Unlock()
	if len(executed) != 1 {
		t.Fatalf("取消后不应执行后续步骤, got %d: %v", len(executed), executed)
	}
}

func TestWorkerStepErrorFailsJobAndEmitsEvent(t *testing.T) {
	resetWorkerForTest(t)

	sub := common.Subscribe()
	defer common.Unsubscribe(sub)

	RegisterOp("t4_fail", func(ctx context.Context, step Step) error {
		return errors.New("模拟步骤失败")
	})

	job, err := CreateJob("photo_dedupe", "", fakeStepPlan("t4_fail", 2), Impact{Files: 2})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := StartJob(job.ID); err != nil {
		t.Fatalf("StartJob: %v", err)
	}

	failed := waitForStatus(t, job.ID, 2*time.Second, StatusFailed, StatusDone)
	if failed.Status != StatusFailed {
		t.Fatalf("job 应 failed, got %q", failed.Status)
	}
	if failed.Error == "" {
		t.Fatalf("failed job 应记录 error 文本")
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-sub:
			if ev.Source == "tasks" && ev.Code == "task.failed" {
				return // 收到期望的事件，测试通过
			}
		case <-deadline:
			t.Fatalf("超时未收到 tasks/task.failed 事件")
		}
	}
}

func TestWorkerProgressMilestonesCapped(t *testing.T) {
	resetWorkerForTest(t)

	sub := common.Subscribe()
	defer common.Unsubscribe(sub)

	RegisterOp("t5_milestone", func(ctx context.Context, step Step) error {
		return nil
	})

	const totalSteps = 20
	job, err := CreateJob("photo_dedupe", "", fakeStepPlan("t5_milestone", totalSteps), Impact{Files: totalSteps})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := StartJob(job.ID); err != nil {
		t.Fatalf("StartJob: %v", err)
	}

	waitForStatus(t, job.ID, 2*time.Second, StatusDone, StatusFailed)

	progressEvents := 0
	doneEvents := 0
	drain := true
	for drain {
		select {
		case ev := <-sub:
			if ev.Source != "tasks" {
				continue
			}
			switch ev.Code {
			case "task.progress":
				progressEvents++
			case "task.done":
				doneEvents++
			}
		case <-time.After(200 * time.Millisecond):
			drain = false
		}
	}

	// 10/25/50/75% 四个里程碑，每个至多一次 -> progress 事件数 <= 4；
	// 加上终态 task.done 1 次，总数 <= 6（计划里程碑数的上限口径按 spec 写）。
	if progressEvents > 4 {
		t.Fatalf("task.progress 事件数 = %d, want <= 4（10/25/50/75%% 各至多一次）", progressEvents)
	}
	if doneEvents != 1 {
		t.Fatalf("task.done 事件数 = %d, want 1", doneEvents)
	}
	if progressEvents+doneEvents > 6 {
		t.Fatalf("总事件数 = %d, want <= 6", progressEvents+doneEvents)
	}
}

func TestWorkerConcurrentStartPauseCancelNoRace(t *testing.T) {
	// 本用例只关心 worker 并发操作（Start/Pause/Cancel）本身的线程安全，
	// 不断言事件投递，用 InitEventBusForTest（无后台 writer goroutine）
	// 避免触发 common/eventbus.go 既有的"关闭时机"竞争（该竞争与 tasks
	// 包无关，记录在 OPENCODE-REPORT-F1-R1.md 的偏离与疑虑一节）。
	resetForTest(t)
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)
	StartWorker()
	t.Cleanup(StopWorkerForTest)

	RegisterOp("t6_race", func(ctx context.Context, step Step) error {
		time.Sleep(time.Millisecond)
		return nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		job, err := CreateJob("photo_dedupe", "", fakeStepPlan("t6_race", 5), Impact{Files: 5})
		if err != nil {
			t.Fatalf("CreateJob: %v", err)
		}
		wg.Add(3)
		go func(id string) {
			defer wg.Done()
			StartJob(id)
		}(job.ID)
		go func(id string) {
			defer wg.Done()
			time.Sleep(time.Millisecond)
			PauseJob(id)
		}(job.ID)
		go func(id string) {
			defer wg.Done()
			time.Sleep(2 * time.Millisecond)
			CancelJob(id)
		}(job.ID)
	}
	wg.Wait()

	// 不要求最终状态一致（并发竞争下谁先谁后不保证），只要求不 panic/不死锁，
	// -race 不报数据竞争即测试目的达成；这里顺带等一下让所有 goroutine 收尾。
	time.Sleep(100 * time.Millisecond)
}
