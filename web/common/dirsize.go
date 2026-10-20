package common

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ═══ 目录大小缓存（治理 du -sh 同步阻塞）═══
//
// 存储总览/文件夹列表等只读接口过去对每个共享文件夹同步跑一次
// "sudo du -sh <path>"，大目录下这条命令可能跑几秒到几十秒，没有
// 超时保护，且每次页面刷新都重新跑一遍。CachedDirSize 用 TTL 缓存
// + stale-while-revalidate：缓存命中且未过期直接返回；过期了先把
// 旧值立刻返回给当前请求，同时在后台异步刷新缓存（不阻塞当前请求）；
// 完全没有缓存过（冷启动）则同步跑一次、带超时保护，避免真的卡死
// 对 60s 还没返回的 du 调用。
//
// 返回值格式与现状完全一致：取 "du -sh" 输出第一列（如 "1.2G"），
// 调用方不需要改动自己的格式化逻辑。

type dirSizeEntry struct {
	value     string
	err       error
	fetchedAt time.Time
}

var (
	dirSizeCache     sync.Map // path -> *dirSizeEntry
	dirSizeRefreshMu sync.Map // path -> *sync.Mutex，防止同一路径并发触发多次后台刷新

	// dirSizeExecFunc 实际执行一次 "du -sh" 的函数，默认指向 execDirSizeReal。
	// 定义成变量是为了单测能注入假命令计数，不必真的 fork sudo/du 进程、
	// 也能测试超时路径（用一个会阻塞的假实现）。
	dirSizeExecFunc = execDirSizeReal
)

// CachedDirSize 返回 path 的人类可读大小（如 "1.2G"），TTL 秒内的重复
// 调用直接命中缓存，不会重新执行 du。
//
//   - 缓存命中且未过期：直接返回缓存值。
//   - 缓存命中但已过期：立刻返回旧值（可能稍微陈旧，但不阻塞请求），
//     同时在后台异步跑一次 du 刷新缓存（stale-while-revalidate）。
//     同一 path 的并发刷新只会有一个真正执行，不会堆积多个 du 进程。
//   - 完全没有缓存（冷启动/首次访问）：同步跑一次，带 60s 超时保护，
//     结果写入缓存后返回。
func CachedDirSize(path string, ttl time.Duration) (string, error) {
	if v, ok := dirSizeCache.Load(path); ok {
		entry := v.(*dirSizeEntry)
		if time.Since(entry.fetchedAt) < ttl {
			return entry.value, entry.err
		}
		// 过期：先把旧值返回，后台异步刷新。
		triggerAsyncDirSizeRefresh(path)
		return entry.value, entry.err
	}

	// 冷启动：没有任何旧值可以兜底，只能同步等待（带超时）。
	value, err := dirSizeExecFunc(path)
	dirSizeCache.Store(path, &dirSizeEntry{value: value, err: err, fetchedAt: time.Now()})
	return value, err
}

// triggerAsyncDirSizeRefresh 异步刷新 path 的缓存。同一 path 并发触发时，
// 只有第一个真正执行 du，其余直接返回（旧值已经被调用方读到了，不需要
// 它们也排队跑一遍 du）。
func triggerAsyncDirSizeRefresh(path string) {
	muAny, _ := dirSizeRefreshMu.LoadOrStore(path, &sync.Mutex{})
	refreshMu := muAny.(*sync.Mutex)
	if !refreshMu.TryLock() {
		// 已经有一个刷新在跑，不重复触发。
		return
	}
	go func() {
		defer refreshMu.Unlock()
		value, err := dirSizeExecFunc(path)
		dirSizeCache.Store(path, &dirSizeEntry{value: value, err: err, fetchedAt: time.Now()})
	}()
}

// execDirSizeReal 实际执行一次 "sudo du -sh <path>"，60s 超时保护。
func execDirSizeReal(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sudo", "du", "-sh", path).Output()
	if err != nil {
		return "-", err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "-", nil
	}
	return fields[0], nil
}
