package identity

import (
	"strconv"
	"testing"
	"time"
)

func withFakeClock(l *Limiter, start time.Time) *time.Time {
	now := start
	l.now = func() time.Time { return now }
	return &now
}

func TestLimiterWindow(t *testing.T) {
	l := NewLoginLimiter(10)
	now := withFakeClock(l, time.Unix(0, 0).UTC())
	for i := range l.limit {
		if !l.Allow("ip:1.2.3.4") {
			t.Fatalf("attempt %d denied inside limit", i+1)
		}
	}
	if l.Allow("ip:1.2.3.4") {
		t.Fatal("attempt over limit allowed")
	}
	if !l.Allow("ip:9.9.9.9") {
		t.Fatal("limit leaked across keys")
	}
	*now = now.Add(l.window + time.Second)
	if !l.Allow("ip:1.2.3.4") {
		t.Fatal("window did not slide")
	}
}

func TestLimiterBoundedMemory(t *testing.T) {
	l := NewLoginLimiter(10)
	l.maxEntries = 50
	withFakeClock(l, time.Unix(0, 0).UTC())
	for i := range l.maxEntries + 100 {
		l.Allow("ip:" + strconv.Itoa(i))
	}
	l.mu.Lock()
	size := len(l.hits)
	l.mu.Unlock()
	if size > l.maxEntries {
		t.Fatalf("map grew to %d over cap %d", size, l.maxEntries)
	}
}

func TestLimiterSprayKeepsFreshKeys(t *testing.T) {
	l := NewLoginLimiter(10)
	l.maxEntries = 20
	now := withFakeClock(l, time.Unix(0, 0).UTC())
	// attacker sprays stale keys first
	for i := range 10 {
		l.Allow("ip:stale" + strconv.Itoa(i))
	}
	*now = now.Add(time.Minute)
	// victim accumulates hits recently...
	for range 5 {
		l.Allow("ip:victim")
	}
	// ...then the attacker floods fresh keys at the same instant
	for i := range 100 {
		l.Allow("ip:fresh" + strconv.Itoa(i))
	}
	l.mu.Lock()
	_, victimAlive := l.hits["ip:victim"]
	size := len(l.hits)
	l.mu.Unlock()
	if !victimAlive {
		t.Fatal("spray evicted the victim's active counter")
	}
	if size > l.maxEntries {
		t.Fatalf("cap exceeded: %d", size)
	}
}
