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
		name     string
		idleTTL  time.Duration
		limit    int
		capacity int
	}{
		{name: "account", limit: 4096, capacity: 4096},
		{name: "visitor", idleTTL: 10 * time.Minute, capacity: 4097},
	} {
		t.Run(policy.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				limiter := &ipLimiter{capacity: policy.limit, idleTTL: policy.idleTTL}
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
