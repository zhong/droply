package server

import (
	"maps"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ipLimiter owns one login surface's independent per-IP buckets. At capacity,
// accounts evict the oldest entry; visitors reject new IPs until idle cleanup
// frees space. Retaining visitor buckets prevents churn from resetting quotas.
type ipLimiter struct {
	mu             sync.Mutex
	entries        map[string]*ipLimiterEntry
	capacity       int
	rejectWhenFull bool
	idleTTL        time.Duration
	lastCleanup    time.Time
}

type ipLimiterEntry struct {
	limiter *rate.Limiter
	seen    time.Time
}

func (l *ipLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.entries == nil {
		l.entries = make(map[string]*ipLimiterEntry)
	}
	if l.idleTTL > 0 && now.Sub(l.lastCleanup) > 5*time.Minute {
		maps.DeleteFunc(l.entries, func(_ string, entry *ipLimiterEntry) bool {
			return now.Sub(entry.seen) > l.idleTTL
		})
		l.lastCleanup = now
	}
	entry := l.entries[ip]
	if entry == nil {
		if l.capacity > 0 && len(l.entries) >= l.capacity {
			if l.rejectWhenFull {
				return false
			}
			var oldestKey string
			var oldest *ipLimiterEntry
			for key, value := range l.entries {
				if oldest == nil || value.seen.Before(oldest.seen) {
					oldestKey, oldest = key, value
				}
			}
			delete(l.entries, oldestKey)
		}
		entry = &ipLimiterEntry{limiter: rate.NewLimiter(rate.Every(6*time.Second), 10)}
		l.entries[ip] = entry
	}
	entry.seen = now
	return entry.limiter.Allow()
}
