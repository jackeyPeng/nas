package diskmgmt

import (
	"context"
	"time"
)

// stepTracker 把 running/done/error 三类 SSE 事件的发送、ctx 取消检查、sleep
// 节奏收口到一处，供 handlePoolExtendStream 使用（设计 §3 第 4 项）。
//
// 语义（R3 目标行为）：每次 Running/Done/Error 调用前先查 ctx.Err()；ctx 已
// 取消时直接返回 error，不发任何事件（连接已死，写了也白发）。调用方（handler
// 的命令执行循环）据此中止后续 sudoExecFn 调用，不再发送任何新 SSE 事件，
// 最终正常返回（不 panic、不写 500）。
//
// emit 返回的写错误（SSE 发送失败，通常是客户端已断连）同样视为中止信号：
// 调用方把 Running/Done/Error 的返回值当"是否继续"的唯一依据，不需要额外
// 检查 ctx——stepTracker 内部已经把两种中止源（ctx 取消 / 写失败）统一成同
// 一个 error 返回值。
type stepTracker struct {
	total int
	step  int
	emit  func(PoolExtendEvent) error
	sleep func(time.Duration)
	ctx   context.Context
}

func newStepTracker(total int, emit func(PoolExtendEvent) error, sleep func(time.Duration), ctx context.Context) *stepTracker {
	return &stepTracker{total: total, emit: emit, sleep: sleep, ctx: ctx}
}

// Running 发送 running 事件（Index=当前即将执行的步数），随后 sleepFn(100ms)。
func (t *stepTracker) Running(name string) error {
	if err := t.ctx.Err(); err != nil {
		return err
	}
	if err := t.emit(PoolExtendEvent{Step: name, Status: "running", Index: t.step + 1, Total: t.total}); err != nil {
		return err
	}
	t.sleep(100 * time.Millisecond)
	return nil
}

// Done 发送 done 事件（Index 自增），随后 sleepFn(100ms)。
func (t *stepTracker) Done(name string) error {
	if err := t.ctx.Err(); err != nil {
		return err
	}
	t.step++
	if err := t.emit(PoolExtendEvent{Step: name, Status: "done", Index: t.step, Total: t.total}); err != nil {
		return err
	}
	t.sleep(100 * time.Millisecond)
	return nil
}

// Error 发送 error 事件；ctx 已取消时不发（连接已死）。
func (t *stepTracker) Error(step, detail string) error {
	if err := t.ctx.Err(); err != nil {
		return err
	}
	return t.emit(PoolExtendEvent{Step: step, Status: "error", Detail: detail})
}

// raidStepTracker 是 stepTracker 对 RAIDExpandEvent 的同构版本（事件类型不同，
// 字段形态一致），供 handleRAIDExpandStream 使用。两者不合并成泛型是因为
// 任务书给定的 newStepTracker 签名固定绑定 PoolExtendEvent，RAID 侧另起一个
// 同构类型以保持最小 diff 和调用点可读性。
type raidStepTracker struct {
	total int
	step  int
	emit  func(RAIDExpandEvent) error
	sleep func(time.Duration)
	ctx   context.Context
}

func newRAIDStepTracker(total int, emit func(RAIDExpandEvent) error, sleep func(time.Duration), ctx context.Context) *raidStepTracker {
	return &raidStepTracker{total: total, emit: emit, sleep: sleep, ctx: ctx}
}

func (t *raidStepTracker) Running(name string) error {
	if err := t.ctx.Err(); err != nil {
		return err
	}
	if err := t.emit(RAIDExpandEvent{Step: name, Status: "running", Index: t.step + 1, Total: t.total}); err != nil {
		return err
	}
	t.sleep(100 * time.Millisecond)
	return nil
}

func (t *raidStepTracker) Done(name string) error {
	if err := t.ctx.Err(); err != nil {
		return err
	}
	t.step++
	if err := t.emit(RAIDExpandEvent{Step: name, Status: "done", Index: t.step, Total: t.total}); err != nil {
		return err
	}
	t.sleep(100 * time.Millisecond)
	return nil
}

func (t *raidStepTracker) Error(step, detail string) error {
	if err := t.ctx.Err(); err != nil {
		return err
	}
	return t.emit(RAIDExpandEvent{Step: step, Status: "error", Detail: detail})
}

// CurrentStep 返回已完成的步数（Done 调用次数），供 waitForResync 等需要知道
// "当前在第几步"的调用点读取（取代原来 handler 内手写的 step 局部变量）。
func (t *raidStepTracker) CurrentStep() int {
	return t.step
}

// SkipStep 让内部计数前进一步但不发送任何事件——对应原 handler 里 resync
// 完成后手写的 step++（resync 轮询期间发的是 waitForResync 自己的 running
// 事件，不经过 tracker.Done，故 tracker 的计数需要手动补一步，否则随后
// "数据同步完成" done 事件的 Index 会比原行为少 1）。
func (t *raidStepTracker) SkipStep() {
	t.step++
}
