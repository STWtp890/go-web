package security

import (
	"sync"
	"time"
)

// maxLimiterBuckets is a hard bound on per-caller state. Idle callers are
// forgotten first; when every bucket is active, a new caller is rejected.
const (
	maxLimiterBuckets = 1024
	idleTimeout       = 10 * time.Minute
)

// RateLimiter bounds how fast one caller may issue RPCs, so a single consumer
// cannot consume the capacity of the others sharing this service.
//
// A nil limiter allows everything, which is the documented behaviour when
// throttling is switched off by configuration.
type RateLimiter struct {
	perSecond float64
	burst     float64
	now       func() time.Time

	mu      sync.Mutex
	buckets map[string]*tokenBucket
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// NewRateLimiter builds a limiter, or returns nil when throttling is disabled.
// Disabled means either value is not positive: an unset rate must not silently
// become an effective rate.
func NewRateLimiter(perSecond float64, burst int) *RateLimiter {
	if perSecond <= 0 || burst <= 0 {
		return nil
	}
	return &RateLimiter{
		perSecond: perSecond,
		burst:     float64(burst),
		now:       time.Now,
		buckets:   make(map[string]*tokenBucket),
	}
}

// Decision is the outcome of one throttling check.
type Decision int

const (
	// DecisionAllowed means the caller may proceed.
	DecisionAllowed Decision = iota
	// DecisionOverBudget means the caller spent its own request budget.
	DecisionOverBudget
	// DecisionCallerTableFull means the caller table is at its hard limit and no
	// idle caller could be reclaimed, so an unseen caller cannot be tracked yet.
	DecisionCallerTableFull
)

// Enabled reports whether throttling is active. It is nil-safe.
func (limiter *RateLimiter) Enabled() bool {
	return limiter != nil
}

// Allow consumes one token for key and reports whether the call may proceed.
// It is nil-safe and never blocks.
func (limiter *RateLimiter) Allow(key string) bool {
	return limiter.Decide(key) == DecisionAllowed
}

// Decide consumes one token for key and reports why the call may proceed or not.
// The two rejection reasons are kept apart so the audit record states what
// actually happened: a caller that spent its budget is not the same event as a
// service that cannot track one more caller.
func (limiter *RateLimiter) Decide(key string) Decision {
	if limiter == nil {
		return DecisionAllowed
	}
	now := limiter.now()

	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	bucket, ok := limiter.buckets[key]
	if !ok {
		if len(limiter.buckets) >= maxLimiterBuckets {
			for existing, existingBucket := range limiter.buckets {
				if now.Sub(existingBucket.last) > idleTimeout {
					delete(limiter.buckets, existing)
				}
			}
		}
		if len(limiter.buckets) >= maxLimiterBuckets {
			return DecisionCallerTableFull
		}
		bucket = &tokenBucket{tokens: limiter.burst, last: now}
		limiter.buckets[key] = bucket
	}
	elapsed := now.Sub(bucket.last).Seconds()
	if elapsed > 0 {
		bucket.tokens = min(limiter.burst, bucket.tokens+elapsed*limiter.perSecond)
	}
	bucket.last = now
	if bucket.tokens < 1 {
		return DecisionOverBudget
	}
	bucket.tokens--
	return DecisionAllowed
}
