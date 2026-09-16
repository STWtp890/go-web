package security

import (
	"sync"
	"time"
)

// maxLimiterBuckets bounds per-caller state. A caller that stops calling is
// forgotten after idleTimeout so an attacker cannot grow the map without bound.
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

// Enabled reports whether throttling is active. It is nil-safe.
func (limiter *RateLimiter) Enabled() bool {
	return limiter != nil
}

// Allow consumes one token for key and reports whether the call may proceed.
// It is nil-safe and never blocks.
func (limiter *RateLimiter) Allow(key string) bool {
	if limiter == nil {
		return true
	}
	now := limiter.now()

	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	if len(limiter.buckets) > maxLimiterBuckets {
		for existing, bucket := range limiter.buckets {
			if now.Sub(bucket.last) > idleTimeout {
				delete(limiter.buckets, existing)
			}
		}
	}

	bucket, ok := limiter.buckets[key]
	if !ok {
		bucket = &tokenBucket{tokens: limiter.burst, last: now}
		limiter.buckets[key] = bucket
	}
	elapsed := now.Sub(bucket.last).Seconds()
	if elapsed > 0 {
		bucket.tokens = min(limiter.burst, bucket.tokens+elapsed*limiter.perSecond)
	}
	bucket.last = now
	if bucket.tokens < 1 {
		return false
	}
	bucket.tokens--
	return true
}
