package identity

import (
	"sort"
	"sync"
	"time"
)

// Limiter is a sliding-window login limiter with bounded memory: the key
// map is pruned on every call and hard-capped, so an attacker spraying
// source addresses cannot grow server memory (02-architecture: "bounded
// memory"). Not shared across processes; v1 runs a single API process.
type Limiter struct {
	mu         sync.Mutex
	hits       map[string][]time.Time
	limit      int
	window     time.Duration
	maxEntries int
	now        func() time.Time
}

// NewLoginLimiter: limit attempts per 15 minutes per key (config ships 10;
// callers pass both "ip:<addr>" and "account" keys and require Allow on both).
func NewLoginLimiter(limit int) *Limiter {
	return &Limiter{
		hits:       map[string][]time.Time{},
		limit:      limit,
		window:     15 * time.Minute,
		maxEntries: 10_000,
		now:        time.Now,
	}
}

func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-l.window)

	recent := l.hits[key]
	kept := recent[:0]
	for _, t := range recent {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)

	if len(l.hits) > l.maxEntries {
		l.pruneLocked(cutoff)
	}
	return true
}

func (l *Limiter) pruneLocked(cutoff time.Time) {
	for k, ts := range l.hits {
		alive := false
		for _, t := range ts {
			if t.After(cutoff) {
				alive = true
				break
			}
		}
		if !alive {
			delete(l.hits, k)
		}
	}
	// still over the cap (active key spray): evict least-recently-active
	// keys until back under the cap, breaking ties toward keys with fewer
	// recorded hits, so the victim's rate-limited counter survives while
	// the attacker's single-hit keys are dropped.
	if len(l.hits) > l.maxEntries {
		type entry struct {
			key  string
			last time.Time
			hits int
		}
		ents := make([]entry, 0, len(l.hits))
		for k, ts := range l.hits {
			ents = append(ents, entry{k, ts[len(ts)-1], len(ts)})
		}
		sort.Slice(ents, func(i, j int) bool {
			if !ents[i].last.Equal(ents[j].last) {
				return ents[i].last.Before(ents[j].last)
			}
			return ents[i].hits < ents[j].hits
		})
		for _, e := range ents {
			if len(l.hits) <= l.maxEntries {
				break
			}
			delete(l.hits, e.key)
		}
	}
}
