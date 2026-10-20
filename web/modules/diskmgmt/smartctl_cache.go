package diskmgmt

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ═══ smartctl 调用缓存（治理每请求 3×N 盘同步唤醒休眠盘）═══
//
// getDiskStatus() 对每块真实磁盘串行跑 getSmartStatus + getDiskTemp +
// getDiskSerial 三次 smartctl，/api/disk/status、/api/disk/overview 等
// 只读接口每次被前端轮询都会重新跑一遍——休眠的机械盘会被 smartctl
// 唤醒，多盘机器上一次页面刷新就能让所有盘醒过来。cachedSmartctl 给
// 这些"只读展示类"调用加 TTL 缓存（30s）+ 5s 超时保护：缓存键是完整
// 参数（命令+所有 args 拼接），同一组参数在 TTL 内只真正执行一次。
//
// 不走这个缓存的调用点（diagnostics 模块的自检 smartctl）：长自检
// -t short/-t long 触发命令和 -l selftest 轮询状态命令，缓存了就会在
// 自检进行中读到陈旧的"尚未开始"状态，必须每次真实查询；这两类调用
// 保持原样，不改。

type smartctlCacheEntry struct {
	value     string
	err       error
	fetchedAt time.Time
}

var (
	smartctlCache sync.Map // cacheKey -> *smartctlCacheEntry

	// smartctlExecFunc 实际执行一次 smartctl 的函数，默认指向 execSmartctlReal。
	// 定义成变量方便单测注入假命令计数，不依赖真实磁盘/smartctl 二进制。
	smartctlExecFunc = execSmartctlReal
)

// cachedSmartctl 执行 "smartctl <args...>" 并缓存 TTL 秒；缓存键是完整
// 参数拼接（strings.Join(args, "\x00")，用不可见分隔符避免参数边界歧义）。
// 行为与直接调用 common.SudoOutput("smartctl", args...) 完全一致
// （返回同样的 (output, error) 组合），只是 TTL 内重复调用不会真的
// 再次落盘执行 smartctl。
func cachedSmartctl(ttl time.Duration, args ...string) (string, error) {
	key := strings.Join(args, "\x00")
	if v, ok := smartctlCache.Load(key); ok {
		entry := v.(*smartctlCacheEntry)
		if time.Since(entry.fetchedAt) < ttl {
			return entry.value, entry.err
		}
	}
	out, err := smartctlExecFunc(args)
	smartctlCache.Store(key, &smartctlCacheEntry{value: out, err: err, fetchedAt: time.Now()})
	return out, err
}

// execSmartctlReal 实际执行一次 "sudo smartctl <args...>"，5s 超时保护。
func execSmartctlReal(args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fullArgs := append([]string{"smartctl"}, args...)
	out, err := exec.CommandContext(ctx, "sudo", fullArgs...).Output()
	return string(out), err
}
