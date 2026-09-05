package server

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestIPLimiterBurstRefillAndConcurrency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := &ipLimiter{}
		var accepted atomic.Int32
		var wg sync.WaitGroup
		for range 100 {
			wg.Go(func() {
				if limiter.allow("client") {
					accepted.Add(1)
				}
			})
		}
		wg.Wait()
		if accepted.Load() != 10 {
			t.Fatalf("accepted burst = %d", accepted.Load())
		}
		if !limiter.allow("other") {
			t.Fatal("different IP shared quota")
		}
		time.Sleep(6 * time.Second)
		if !limiter.allow("client") || limiter.allow("client") {
			t.Fatal("expected exactly one refilled token")
		}
	})
}

func TestIPLimiterPreservesSurfacePolicies(t *testing.T) {
	for _, policy := range []struct {
		name           string
		idleTTL        time.Duration
		limit          int
		capacity       int
		rejectWhenFull bool
	}{
		{name: "account", limit: 4096, capacity: 4096},
		{name: "visitor", idleTTL: 10 * time.Minute, limit: 4096, capacity: 4096, rejectWhenFull: true},
	} {
		t.Run(policy.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				limiter := &ipLimiter{capacity: policy.limit, idleTTL: policy.idleTTL, rejectWhenFull: policy.rejectWhenFull}
				for i := range 4097 {
					time.Sleep(time.Nanosecond)
					limiter.allow(strconv.Itoa(i))
				}
				if len(limiter.entries) != policy.capacity {
					t.Fatalf("entry count = %d, want %d", len(limiter.entries), policy.capacity)
				}
				if policy.name == "account" && limiter.entries["0"] != nil {
					t.Fatal("oldest account entry not evicted")
				}
				time.Sleep(11 * time.Minute)
				limiter.allow("fresh")
				want := 4096
				if policy.name == "visitor" {
					want = 1
				}
				if len(limiter.entries) != want {
					t.Fatalf("idle entry count = %d, want %d", len(limiter.entries), want)
				}
			})
		})
	}
}

func TestIPLimiterVisitorCleanupCadence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := &ipLimiter{idleTTL: 10 * time.Minute}
		limiter.allow("old")
		time.Sleep(10 * time.Minute)
		limiter.allow("current")
		if limiter.entries["old"] == nil {
			t.Fatal("entry expired at the exact TTL boundary")
		}
		time.Sleep(5 * time.Minute)
		limiter.allow("current")
		if limiter.entries["old"] == nil {
			t.Fatal("cleanup ran at the exact interval boundary")
		}
		time.Sleep(time.Nanosecond)
		limiter.allow("current")
		if limiter.entries["old"] != nil {
			t.Fatal("expired entry survived scheduled cleanup")
		}
	})
}

func TestIPLimiterAccountCapacityAtEqualTimestamps(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := &ipLimiter{capacity: 2}
		for _, ip := range []string{"first", "second", "third", "fourth"} {
			if !limiter.allow(ip) {
				t.Fatal("account stopped admitting new IPs")
			}
			if len(limiter.entries) > 2 {
				t.Fatal("equal timestamps allowed capacity overflow")
			}
		}
	})
}

func TestIPLimiterVisitorCapacityPreservesQuotas(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := &ipLimiter{capacity: 4096, rejectWhenFull: true, idleTTL: 10 * time.Minute}
		for range 10 {
			if !limiter.allow("blocked") {
				t.Fatal("initial burst rejected")
			}
		}
		var accepted atomic.Int32
		var wg sync.WaitGroup
		for i := range 8192 {
			wg.Go(func() {
				if limiter.allow(strconv.Itoa(i)) {
					accepted.Add(1)
				}
			})
		}
		wg.Wait()
		if accepted.Load() != 4095 || len(limiter.entries) != 4096 {
			t.Fatalf("accepted=%d entries=%d", accepted.Load(), len(limiter.entries))
		}
		if limiter.allow("blocked") || limiter.allow("new") {
			t.Fatal("high-cardinality churn bypassed quota or capacity")
		}
		time.Sleep(6 * time.Second)
		if !limiter.allow("blocked") || limiter.allow("blocked") {
			t.Fatal("existing IP refill changed at capacity")
		}
		time.Sleep(11 * time.Minute)
		if !limiter.allow("new") || len(limiter.entries) != 1 {
			t.Fatal("idle cleanup did not restore admission")
		}
	})
}
