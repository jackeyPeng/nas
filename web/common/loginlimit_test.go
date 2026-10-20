package common

import (
	"testing"
	"time"
)

// fakeClock 提供可手动推进的测试时钟
type fakeClock struct {
	t time.Time
}

func (c *fakeClock) now() time.Time { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestLoginLimiter_LocksAfterMaxFailures(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	l := NewLoginLimiter(5, 15*time.Minute, clock.now)

	ip := "1.2.3.4"
	for i := 0; i < 4; i++ {
		if allowed, _ := l.Allow(ip); !allowed {
			t.Fatalf("第 %d 次失败前应仍允许尝试", i+1)
		}
		l.Fail(ip)
	}
	// 第 5 次失败触发锁定
	l.Fail(ip)

	allowed, retry := l.Allow(ip)
	if allowed {
		t.Error("达到 5 次失败后应被锁定")
	}
	if retry <= 0 {
		t.Errorf("锁定期间 retryAfterSec 应为正数, got %d", retry)
	}
}

func TestLoginLimiter_UnlocksAfterWindowExpires(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	l := NewLoginLimiter(5, 15*time.Minute, clock.now)

	ip := "1.2.3.4"
	for i := 0; i < 5; i++ {
		l.Fail(ip)
	}
	if allowed, _ := l.Allow(ip); allowed {
		t.Fatal("应处于锁定状态")
	}

	clock.advance(15*time.Minute + time.Second)

	allowed, retry := l.Allow(ip)
	if !allowed {
		t.Errorf("锁定窗口过期后应恢复允许, retry=%d", retry)
	}
}

func TestLoginLimiter_SuccessClearsFailures(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	l := NewLoginLimiter(5, 15*time.Minute, clock.now)

	ip := "1.2.3.4"
	for i := 0; i < 4; i++ {
		l.Fail(ip)
	}
	l.Success(ip)

	// 清零后再失败 4 次不应锁定（计数已重置）
	for i := 0; i < 4; i++ {
		l.Fail(ip)
	}
	if allowed, _ := l.Allow(ip); !allowed {
		t.Error("Success 后计数应重置，4 次失败不应触发锁定")
	}
}

func TestLoginLimiter_IndependentPerIP(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	l := NewLoginLimiter(5, 15*time.Minute, clock.now)

	ipA, ipB := "1.2.3.4", "5.6.7.8"
	for i := 0; i < 5; i++ {
		l.Fail(ipA)
	}

	if allowed, _ := l.Allow(ipA); allowed {
		t.Error("ipA 应已锁定")
	}
	if allowed, _ := l.Allow(ipB); !allowed {
		t.Error("ipB 不应受 ipA 锁定影响")
	}
}

func TestLoginLimiter_LockoutResetsCounterAfterExpiry(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	l := NewLoginLimiter(5, 15*time.Minute, clock.now)

	ip := "1.2.3.4"
	for i := 0; i < 5; i++ {
		l.Fail(ip)
	}
	clock.advance(15*time.Minute + time.Second)

	// 过期后的一次新失败不应立即重新锁定（计数应从 1 重新开始）
	l.Fail(ip)
	allowed, _ := l.Allow(ip)
	if !allowed {
		t.Error("锁定过期后重新计数，单次失败不应立即再次锁定")
	}
}
