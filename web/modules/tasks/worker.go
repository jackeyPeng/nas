package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"nas-panel/common"
)

// OpExec 是一种步骤操作（Step.Op）对应的执行函数。F1 只有
// "move_to_recycle"（Task 4 注册真实实现），测试用 RegisterOp 注入 fake。
type OpExec func(ctx context.Context, step Step) error

// ctxJobIDKeyType / ctxJobIDKey：worker 把当前任务 ID 塞进执行步骤的
// ctx（F1 Task 4 接线：kinds_dedupe.go 的 move_to_recycle 执行体要按
// jobID 累计 moved/freed/skipped 统计供 ResultBuilder 汇总，不新增
// OpExec 签名参数——签名由 Task 2 定义，Task 4 不得改动）。用具体类型
// 而非内建 string 做 key，规避 vet 的 context key 冲突告警。
type ctxJobIDKeyType struct{}

var ctxJobIDKey = ctxJobIDKeyType{}

var (
	opMu  sync.Mutex
	opMap = map[string]OpExec{}
)

// RegisterOp 注册一种 op 的执行函数，幂等覆盖（重复注册直接替换，便于
// 测试在不同用例里换用不同 fake）。
func RegisterOp(op string, fn OpExec) {
	opMu.Lock()
	defer opMu.Unlock()
	opMap[op] = fn
}

func lookupOp(op string) (OpExec, bool) {
	opMu.Lock()
	defer opMu.Unlock()
	fn, ok := opMap[op]
	return fn, ok
}

// ResultBuilder 由 kind 实现方（如 kinds_dedupe.go）注册，在任务跑完全部
// 步骤后把 kind 专属统计（如 moved/freed/skipped）序列化进 Job.Result。
// doneCount 是本次执行成功的步骤数（含断点续传场景只统计本次新完成的），
// 供未注册 ResultBuilder 的 kind（测试用 fake op）兜底使用。
type ResultBuilder func(jobID string, doneCount int) string

var (
	resultBuilderMu sync.Mutex
	resultBuilders  = map[string]ResultBuilder{}
)

// RegisterResultBuilder 注册一种 kind 的结果汇总函数，幂等覆盖。
func RegisterResultBuilder(kind string, fn ResultBuilder) {
	resultBuilderMu.Lock()
	defer resultBuilderMu.Unlock()
	resultBuilders[kind] = fn
}

func lookupResultBuilder(kind string) (ResultBuilder, bool) {
	resultBuilderMu.Lock()
	defer resultBuilderMu.Unlock()
	fn, ok := resultBuilders[kind]
	return fn, ok
}

// buildJobResult 按 kind 查 resultBuilders；未注册时落回 Task 2 的占位
// 汇总 {"moved":doneCount,"freed":0,"skipped":0}（既有 worker 测试用
// fake op，不依赖 kind 专属统计，保持契约不变）。
func buildJobResult(jobID, kind string, doneCount int) string {
	if fn, ok := lookupResultBuilder(kind); ok {
		return fn(jobID, doneCount)
	}
	result, _ := json.Marshal(map[string]interface{}{"moved": doneCount, "freed": 0, "skipped": 0})
	return string(result)
}

// queueCapacity 是任务队列容量：单 worker 串行消费，家庭盒子磁盘友好，
// 不追求并发；16 足够覆盖"排队等待执行"的任务数（F1 场景任务量很小）。
const queueCapacity = 16

// controlSignal 是运行中任务的控制信号。
type controlSignal int

const (
	signalNone controlSignal = iota
	signalPause
	signalCancel
)

var (
	workerMu      sync.Mutex
	queue         chan string
	workerStarted bool
	workerDone    chan struct{}
	signalMu      sync.Mutex
	signals       = map[string]controlSignal{}
)

// StartWorker 启动单 goroutine 消费队列；InitTasks 成功后由 main 调用。
// 幂等：已启动时重复调用是空操作。
func StartWorker() {
	workerMu.Lock()
	defer workerMu.Unlock()
	if workerStarted {
		return
	}
	q := make(chan string, queueCapacity)
	queue = q
	done := make(chan struct{})
	workerDone = done
	workerStarted = true
	go func() {
		workerLoop(q) // 用局部变量 q，不读包级 queue（避免与 StopWorkerForTest 的 queue=nil 写入竞争）
		close(done)
	}()
}

// StopWorkerForTest 关闭当前 worker goroutine 并**阻塞等待它真正退出**后
// 才清空信号状态，仅供单测在每个用例末尾调用（对齐
// common.InitEventBusForTest/CloseEventBusForTest 的"测试专用生命周期
// 控制"模式）：生产路径（main.go）从不调用它，StartWorker 仍是进程生命
// 周期内只执行一次的单例启动。
//
// 必须等待 goroutine 真正退出，不能只 close(queue) 就返回——否则调用方
// （测试）紧接着 CloseEventBusForTest 时，worker 可能还在执行最后一个
// 已出队任务的 emitTaskEvent/EmitEvent，与 eventbus 清理产生数据竞争
// （-race 下必现，R1 阶段实测踩过）。
func StopWorkerForTest() {
	workerMu.Lock()
	var done chan struct{}
	if workerStarted {
		close(queue)
		done = workerDone
		queue = nil
		workerDone = nil
		workerStarted = false
	}
	workerMu.Unlock()

	if done != nil {
		<-done
	}

	signalMu.Lock()
	signals = map[string]controlSignal{}
	signalMu.Unlock()
}

func getQueue() chan string {
	workerMu.Lock()
	defer workerMu.Unlock()
	return queue
}

// EnqueueJob 把任务 ID 送入待执行队列（StartJob/ResumeJob 内部调用）。
// AI 模块关断时拒绝新任务入队（零足迹语义）：调用方需在业务层先过
// enabledGuard，这里只负责物理入队，不感知 ai 包（架构边界：tasks 不得
// import ai）——真正的 enabled 守卫在 api.go 的 REST 层和 StartJob 的
// 调用点（kinds_dedupe.go/tools_tasks.go）做,不在引擎内核重复判断。
func EnqueueJob(id string) error {
	q := getQueue()
	if q == nil {
		return fmt.Errorf("worker 未启动")
	}
	select {
	case q <- id:
		return nil
	default:
		return fmt.Errorf("任务队列已满，请稍后重试")
	}
}

// StartJob 校验 status==pending_confirm，迁到 running，然后入队。
func StartJob(id string) error {
	if err := UpdateJobStatus(id, StatusRunning, ""); err != nil {
		return err
	}
	clearSignal(id)
	return EnqueueJob(id)
}

// PauseJob 请求暂停：worker 在当前步骤执行完、下一步开始前检查该信号，
// 落 paused 并退出该任务的处理循环（不强行中断正在执行的单步操作，避免
// 文件操作半途而断）。
func PauseJob(id string) error {
	job, ok := GetJob(id)
	if !ok {
		return fmt.Errorf("任务不存在: %s", id)
	}
	if job.Status != StatusRunning {
		return fmt.Errorf("非法状态迁移: %s -> %s", job.Status, StatusPaused)
	}
	setSignal(id, signalPause)
	return nil
}

// ResumeJob 把 paused 任务迁回 running 并重新入队；worker 执行时会先查
// checkpoint 跳过已完成步骤。
func ResumeJob(id string) error {
	if err := UpdateJobStatus(id, StatusRunning, ""); err != nil {
		return err
	}
	clearSignal(id)
	return EnqueueJob(id)
}

// CancelJob 请求取消：pending_confirm 直接落终态；running/paused 的任务
// 由 worker 在步骤边界感知信号后落终态（running 时信号中断步骤循环）。
func CancelJob(id string) error {
	job, ok := GetJob(id)
	if !ok {
		return fmt.Errorf("任务不存在: %s", id)
	}
	switch job.Status {
	case StatusPendingConfirm, StatusPaused:
		return UpdateJobStatus(id, StatusCancelled, "")
	case StatusRunning:
		setSignal(id, signalCancel)
		return nil
	default:
		return fmt.Errorf("非法状态迁移: %s -> %s", job.Status, StatusCancelled)
	}
}

func setSignal(id string, sig controlSignal) {
	signalMu.Lock()
	defer signalMu.Unlock()
	signals[id] = sig
}

func takeSignal(id string) controlSignal {
	signalMu.Lock()
	defer signalMu.Unlock()
	sig := signals[id]
	return sig
}

func clearSignal(id string) {
	signalMu.Lock()
	defer signalMu.Unlock()
	delete(signals, id)
}

// milestones 是进度事件的里程碑百分比表（10/25/50/75%，各至多发一次，
// 防刷屏；done/failed 终态另外必发，不受本表约束）。
var milestones = []int{10, 25, 50, 75}

// workerLoop 是唯一的 worker goroutine：串行消费队列，逐任务执行。
// 生命周期挂 ai enabled 守卫：executeJob 内部每步检查，关断时运行中任务
// 在当前步骤边界暂停落库，不继续消费新任务（守卫函数由 main.go 经
// SetEnabledGuard 注入，默认 nil 时视为"始终允许"，不影响单测——单测
// 不会调用 SetEnabledGuard，保持零依赖）。
func workerLoop(q chan string) {
	for id := range q {
		executeJob(id)
	}
}

// enabledGuardFn 由 main.go 通过 SetEnabledGuard 注入（通常是
// ai.IsEnabled），worker 执行每一步前检查；nil 表示不设限（测试默认态）。
var enabledGuardFn func() bool

// SetEnabledGuard 注入 worker 的运行态守卫：返回 false 时，运行中任务在
// 当前步骤边界暂停落库，不再消费后续步骤；队列里尚未开始的任务同样会在
// executeJob 入口被拦截直接 paused。zero-footprint 语义的一部分。
func SetEnabledGuard(fn func() bool) {
	enabledGuardFn = fn
}

func guardAllows() bool {
	if enabledGuardFn == nil {
		return true
	}
	return enabledGuardFn()
}

func executeJob(id string) {
	job, ok := GetJob(id)
	if !ok {
		return
	}
	if job.Status != StatusRunning {
		// 可能已被并发的 Cancel/Pause 在入队和出队之间改变状态，直接跳过。
		return
	}

	checkpoint := LoadCheckpoint(id)
	total := len(job.Plan)
	doneCount := 0
	for _, step := range job.Plan {
		if checkpoint[step.Key] {
			doneCount++
		}
	}
	reportedMilestones := map[int]bool{}
	// 已完成的步骤不重新触发里程碑事件（续传场景）：按已完成比例预标记。
	for _, m := range milestones {
		if total > 0 && doneCount*100/total >= m {
			reportedMilestones[m] = true
		}
	}

	ctx := context.WithValue(context.Background(), ctxJobIDKey, id)

	for _, step := range job.Plan {
		if checkpoint[step.Key] {
			continue // 幂等：已完成步骤跳过（断点续传核心）
		}

		if !guardAllows() {
			pauseAndNote(id, "AI 功能已关闭，任务已暂停")
			return
		}

		switch takeSignal(id) {
		case signalCancel:
			FinishJob(id, "", "", StatusCancelled)
			clearSignal(id)
			return
		case signalPause:
			pauseAndNote(id, "")
			clearSignal(id)
			return
		}

		fn, ok := lookupOp(step.Op)
		if !ok {
			FinishJob(id, "", fmt.Sprintf("未知操作类型: %s", step.Op), StatusFailed)
			emitTaskEvent(id, "task.failed", job.Kind, "未知操作类型: "+step.Op)
			return
		}

		if err := fn(ctx, step); err != nil {
			FinishJob(id, "", err.Error(), StatusFailed)
			emitTaskEvent(id, "task.failed", job.Kind, err.Error())
			return
		}

		SaveCheckpoint(id, []string{step.Key})
		doneCount++
		note := fmt.Sprintf("已处理 %d/%d", doneCount, total)
		SetProgress(id, doneCount, total, note)

		if total > 0 {
			pct := doneCount * 100 / total
			for _, m := range milestones {
				if pct >= m && !reportedMilestones[m] {
					reportedMilestones[m] = true
					emitTaskEvent(id, "task.progress", job.Kind, note)
				}
			}
		}
	}

	// 跑完全部步骤：汇总结果。F1 Task 4 接线：按 job.Kind 查
	// resultBuilders 取 kind 专属的 moved/freed/skipped 统计（kinds_dedupe.go
	// 的 init() 为 "photo_dedupe" 注册）；未注册的 kind（测试用的 fake op）
	// 落回 Task 2 的占位汇总，不影响既有 worker 测试契约。
	result := buildJobResult(id, job.Kind, doneCount)
	if err := FinishJob(id, result, "", StatusDone); err != nil {
		log.Printf("[tasks] FinishJob(%s) done 失败: %v", id, err)
		return
	}
	emitTaskEvent(id, "task.done", job.Kind, "任务完成")
}

func pauseAndNote(id, extraNote string) {
	job, ok := GetJob(id)
	if !ok {
		return
	}
	note := job.ProgressNote
	if extraNote != "" {
		if note != "" {
			note = note + "；" + extraNote
		} else {
			note = extraNote
		}
	}
	SetProgress(id, job.ProgressCur, job.ProgressTotal, note)
	UpdateJobStatus(id, StatusPaused, "")
}

// emitTaskEvent 统一 tasks 模块的 eventbus 上报：source 固定 "tasks"，
// code=kind（task.progress/task.done/task.failed），params 带 job_id 等
// 上下文供订阅者（将来 triggers/通知中心）插值。
func emitTaskEvent(jobID, kind, taskKind, detail string) {
	job, _ := GetJob(jobID)
	typ := common.EventInfo
	switch kind {
	case "task.done":
		typ = common.EventSuccess
	case "task.failed":
		typ = common.EventError
	}
	common.EmitEvent("tasks", typ, kind, map[string]interface{}{
		"job_id": jobID,
		"kind":   taskKind,
		"cur":    job.ProgressCur,
		"total":  job.ProgressTotal,
	}, detail)
}
