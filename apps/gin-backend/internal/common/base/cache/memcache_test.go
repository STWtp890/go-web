package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemCacheReturnsErrMissForMissingAndExpiredEntries(t *testing.T) {
	cache := newMemCache(MemCacheOptions{MaxEntries: 4, MaxBytes: 1024, SweepInterval: time.Millisecond})
	if _, err := cache.Get(context.Background(), "missing"); !errors.Is(err, ErrMiss) {
		t.Fatalf("missing error = %v, want ErrMiss", err)
	}
	if err := cache.Set(context.Background(), "expired", "value", time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := cache.Get(context.Background(), "expired"); !errors.Is(err, ErrMiss) {
		t.Fatalf("expired error = %v, want ErrMiss", err)
	}
	if stats := cache.Stats(); stats.Entries != 0 || stats.Misses != 2 || stats.Expired != 1 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestMemCacheEnforcesLRUAndByteLimits(t *testing.T) {
	cache := newMemCache(MemCacheOptions{MaxEntries: 2, MaxBytes: 64, SweepInterval: time.Hour})
	ctx := context.Background()
	for _, item := range []struct{ key, value string }{{"a", "one"}, {"b", "two"}} {
		if err := cache.Set(ctx, item.key, item.value, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cache.Get(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := cache.Set(ctx, "c", "three", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Get(ctx, "b"); !errors.Is(err, ErrMiss) {
		t.Fatalf("least recently used entry error = %v", err)
	}
	if stats := cache.Stats(); stats.Entries != 2 || stats.Evictions != 1 || stats.Bytes > 64 {
		t.Fatalf("stats = %#v", stats)
	}
	if err := cache.Set(ctx, "oversized", string(make([]byte, 128)), time.Hour); !errors.Is(err, ErrEntryTooLarge) {
		t.Fatalf("oversized error = %v", err)
	}
}
