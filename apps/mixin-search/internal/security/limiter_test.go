package security

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestDisabledLimiterAllowsEverything(t *testing.T) {
	t.Parallel()

	var disabled *RateLimiter
	if disabled.Enabled() {
		t.Fatal("nil limiter reports enabled")
	}
	for i := 0; i < 100; i++ {
		if !disabled.Allow("caller") {
			t.Fatal("nil limiter rejected a call")
		}
	}
	if NewRateLimiter(0, 10) != nil {
		t.Fatal("NewRateLimiter(0, 10) is enabled, want nil")
	}
	if NewRateLimiter(10, 0) != nil {
		t.Fatal("NewRateLimiter(10, 0) is enabled, want nil")
	}
}

func TestLimiterEnforcesBurstThenRefills(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_760_000_000, 0).UTC()
	limiter := NewRateLimiter(10, 3)
	limiter.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if !limiter.Allow("caller-a") {
			t.Fatalf("call %d rejected inside the burst budget", i+1)
		}
	}
	if limiter.Allow("caller-a") {
		t.Fatal("fourth call was allowed, want the burst budget enforced")
	}
	// Another caller has an independent budget: one noisy consumer must not
	// consume the capacity of the others.
	if !limiter.Allow("caller-b") {
		t.Fatal("second caller was throttled by the first caller's usage")
	}

	now = now.Add(100 * time.Millisecond)
	if !limiter.Allow("caller-a") {
		t.Fatal("call after refill was rejected, want one token refilled")
	}
}

func TestLimiterRefillIsCappedAtBurst(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_760_000_000, 0).UTC()
	limiter := NewRateLimiter(1000, 2)
	limiter.now = func() time.Time { return now }

	if !limiter.Allow("caller") || !limiter.Allow("caller") {
		t.Fatal("burst budget was not available")
	}
	// A long idle period must not accumulate an unbounded budget.
	now = now.Add(time.Hour)
	allowed := 0
	for i := 0; i < 10; i++ {
		if limiter.Allow("caller") {
			allowed++
		}
	}
	if allowed != 2 {
		t.Fatalf("allowed %d calls after a long idle period, want the burst cap of 2", allowed)
	}
}

func TestLimiterForgetsIdleCallers(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_760_000_000, 0).UTC()
	limiter := NewRateLimiter(10, 1)
	limiter.now = func() time.Time { return now }

	for i := 0; i < maxLimiterBuckets+16; i++ {
		limiter.Allow(fmt.Sprintf("caller-%04d", i))
	}
	now = now.Add(idleTimeout + time.Minute)
	limiter.Allow("fresh-caller")

	limiter.mu.Lock()
	size := len(limiter.buckets)
	limiter.mu.Unlock()
	if size > 2 {
		t.Fatalf("limiter retained %d buckets after an idle sweep, want it bounded", size)
	}
}

func TestLimiterRejectsNewCallerAtHardLimit(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_760_000_000, 0).UTC()
	limiter := NewRateLimiter(10, 2)
	limiter.now = func() time.Time { return now }

	for i := 0; i < maxLimiterBuckets; i++ {
		if !limiter.Allow(fmt.Sprintf("caller-%04d", i)) {
			t.Fatalf("caller %d was rejected before the hard limit", i)
		}
	}
	if limiter.Allow("overflow-caller") {
		t.Fatal("new caller was allowed after the hard bucket limit")
	}
	if !limiter.Allow("caller-0000") {
		t.Fatal("existing caller lost its remaining budget at the hard limit")
	}

	limiter.mu.Lock()
	size := len(limiter.buckets)
	limiter.mu.Unlock()
	if size != maxLimiterBuckets {
		t.Fatalf("limiter holds %d buckets, want hard limit %d", size, maxLimiterBuckets)
	}
}

func TestDecideSeparatesTheTwoRejectionReasons(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_760_000_000, 0).UTC()
	limiter := NewRateLimiter(10, 1)
	limiter.now = func() time.Time { return now }

	if decision := limiter.Decide("caller-a"); decision != DecisionAllowed {
		t.Fatalf("first call decision = %v, want allowed", decision)
	}
	// The caller spent its own budget: a caller-attributable rejection.
	if decision := limiter.Decide("caller-a"); decision != DecisionOverBudget {
		t.Fatalf("second call decision = %v, want over budget", decision)
	}

	for i := 1; i < maxLimiterBuckets; i++ {
		limiter.Decide(fmt.Sprintf("caller-%04d", i))
	}
	// The table is full and every bucket is active: a service-attributable
	// rejection that must not be reported as the caller's own overuse.
	if decision := limiter.Decide("overflow-caller"); decision != DecisionCallerTableFull {
		t.Fatalf("unseen caller decision = %v, want caller table full", decision)
	}

	var disabled *RateLimiter
	if decision := disabled.Decide("any"); decision != DecisionAllowed {
		t.Fatalf("nil limiter decision = %v, want allowed", decision)
	}
}

func TestLimiterIsConcurrencySafe(t *testing.T) {
	t.Parallel()

	limiter := NewRateLimiter(1e9, 1)
	var group sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for i := 0; i < 200; i++ {
				limiter.Allow("shared-caller")
			}
		}()
	}
	group.Wait()
}
