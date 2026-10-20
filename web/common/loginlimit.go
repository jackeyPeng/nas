package common

import (
	"sync"
	"time"
)

// LoginLimiter 内存型登录失败锁定器：同一 IP 连续失败达到阈值后锁定一段时间，
// 用于缓解 /api/login（含 TOTP）的暴力破解。
//
// 时钟通过 now 字段注入，方便单测用假时钟推进时间而不必真实 sleep。
type LoginLimiter struct {
	mu            sync.Mutex
	entries       map[string]*loginEntry
	maxFailures   int
	lockoutWindow time.Duration
	now           func() time.Time
}

type loginEntry struct {
	failures    int
	lockedUntil time.Time // 零值表示未锁定
	lastSeen    time.Time
}

// NewLoginLimiter 创建锁定器。maxFailures 为锁定前允许的连续失败次数，
// lockoutWindow 为锁定时长。now 为时钟源，传 nil 时默认用 time.Now。
func NewLoginLimiter(maxFailures int, lockoutWindow time.Duration, now func() time.Time) *LoginLimiter {
	if now == nil {
		now = time.Now
	}
	return &LoginLimiter{
		entries:       map[string]*loginEntry{},
		maxFailures:   maxFailures,
		lockoutWindow: lockoutWindow,
		now:           now,
	}
}

// staleAfter 条目超过多久未访问就视为陈旧可清理（锁定窗口的 4 倍，留足观察余量）
func (l *LoginLimiter) staleAfter() time.Duration {
	return l.lockoutWindow * 4
}

// Allow 判断该 IP 当前是否允许尝试登录。锁定中返回 false 与剩余锁定秒数。
// 顺手清理过期条目，避免 map 无限增长。
func (l *LoginLimiter) Allow(ip string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.evictLocked(now)

	e, ok := l.entries[ip]
	if !ok {
		return true, 0
	}
	if !e.lockedUntil.IsZero() && now.Before(e.lockedUntil) {
		remaining := int(e.lockedUntil.Sub(now).Seconds())
		if remaining < 1 {
			remaining = 1
		}
		return false, remaining
	}
	return true, 0
}

// Fail 记录一次失败尝试；达到阈值后锁定该 IP。
func (l *LoginLimiter) Fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	e, ok := l.entries[ip]
	if !ok {
		e = &loginEntry{}
		l.entries[ip] = e
	}
	e.lastSeen = now

	// 锁定已过期则重新计数
	if !e.lockedUntil.IsZero() && !now.Before(e.lockedUntil) {
		e.failures = 0
		e.lockedUntil = time.Time{}
	}

	e.failures++
	if e.failures >= l.maxFailures {
		e.lockedUntil = now.Add(l.lockoutWindow)
	}
}

// Success 清零该 IP 的失败计数与锁定状态。
func (l *LoginLimiter) Success(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, ip)
}

// evictLocked 清理长期未访问的陈旧条目；调用方必须已持有 l.mu。
func (l *LoginLimiter) evictLocked(now time.Time) {
	stale := l.staleAfter()
	for ip, e := range l.entries {
		if now.Sub(e.lastSeen) > stale {
			delete(l.entries, ip)
		}
	}
}
