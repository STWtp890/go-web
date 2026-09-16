package cache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEntityCacheTreatsWrappedErrMissAsMiss(t *testing.T) {
	primary := newTestStore()
	primary.getErr = fmt.Errorf("primary: %w", ErrMiss)
	fallback := newTestStore()
	fallback.getErr = fmt.Errorf("fallback: %w", ErrMiss)
	runtime := NewRuntime(primary, RuntimeOptions{Partitions: map[string]MemCacheOptions{
		"test": {MaxEntries: 8, MaxBytes: 1024, SweepInterval: time.Hour},
	}})
	cache := newEntityCache[string](primary, fallback, &runtime.flight, "wrapped-miss", runtime.entityCounters("wrapped-miss"), time.Minute)
	loads := 0
	value, err := cache.Get(context.Background(), "key", func(context.Context) (string, error) {
		loads++
		return "loaded", nil
	})
	if err != nil || value != "loaded" || loads != 1 {
		t.Fatalf("value=%q loads=%d err=%v", value, loads, err)
	}
	stats := runtime.Snapshot().Entities["wrapped-miss"]
	if stats.PrimaryMisses != 1 || stats.FallbackMisses != 1 || stats.PrimaryErrors != 0 || stats.FallbackErrors != 0 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestRuntimeSharesSingleflightAcrossEntityCacheInstances(t *testing.T) {
	primary := newTestStore()
	runtime := NewRuntime(primary, RuntimeOptions{Partitions: map[string]MemCacheOptions{
		"test": {MaxEntries: 8, MaxBytes: 1024, SweepInterval: time.Hour},
	}})
	left := NewEntityFromRuntime[string](runtime, "shared", "test", time.Minute)
	right := NewEntityFromRuntime[string](runtime, "shared", "test", time.Minute)
	start := make(chan struct{})
	release := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(2)
	var loads atomic.Int32
	loader := func(context.Context) (string, error) {
		loads.Add(1)
		<-release
		return "value", nil
	}
	results := make(chan string, 2)
	for _, entity := range []*EntityCache[string]{left, right} {
		go func(cache *EntityCache[string]) {
			ready.Done()
			<-start
			value, err := cache.Get(context.Background(), "same-key", loader)
			if err != nil {
				results <- "error: " + err.Error()
				return
			}
			results <- value
		}(entity)
	}
	ready.Wait()
	close(start)
	time.Sleep(20 * time.Millisecond)
	close(release)
	for range 2 {
		if result := <-results; result != "value" {
			t.Fatalf("result = %q", result)
		}
	}
	if got := loads.Load(); got != 1 {
		t.Fatalf("loads = %d, want 1", got)
	}
	if shared := runtime.Snapshot().Entities["shared"].Shared; shared == 0 {
		t.Fatal("singleflight sharing was not observed")
	}
}

func TestEntityCacheEvictReturnsBothStoreErrors(t *testing.T) {
	primaryErr := errors.New("primary delete failed")
	fallbackErr := errors.New("fallback delete failed")
	primary := newTestStore()
	primary.delErr = primaryErr
	fallback := newTestStore()
	fallback.delErr = fallbackErr
	runtime := NewRuntime(primary, RuntimeOptions{})
	cache := newEntityCache[string](primary, fallback, &runtime.flight, "evict", runtime.entityCounters("evict"), time.Minute)
	err := cache.Evict(context.Background(), "key")
	if !errors.Is(err, primaryErr) || !errors.Is(err, fallbackErr) {
		t.Fatalf("evict error = %v", err)
	}
	if got := runtime.Snapshot().Entities["evict"].EvictErrors; got != 1 {
		t.Fatalf("evict errors = %d", got)
	}
}

func TestRevisionedKeyIsolatesLateStaleFill(t *testing.T) {
	primary := newTestStore()
	runtime := NewRuntime(primary, RuntimeOptions{Partitions: map[string]MemCacheOptions{
		"test": {MaxEntries: 8, MaxBytes: 4096, SweepInterval: time.Hour},
	}})
	cache := NewEntityFromRuntime[string](runtime, "versioned", "test", time.Minute)
	loaderStarted := make(chan struct{})
	releaseOldLoad := make(chan struct{})
	oldResult := make(chan error, 1)

	go func() {
		value, err := cache.Get(context.Background(), "entity:1:revision:1", func(context.Context) (string, error) {
			close(loaderStarted)
			<-releaseOldLoad
			return "v1", nil
		})
		if err == nil && value != "v1" {
			err = fmt.Errorf("old value = %q", value)
		}
		oldResult <- err
	}()

	select {
	case <-loaderStarted:
	case <-time.After(time.Second):
		t.Fatal("old loader did not start")
	}
	if err := cache.Evict(context.Background(), "entity:1:revision:1"); err != nil {
		t.Fatal(err)
	}
	close(releaseOldLoad)
	if err := <-oldResult; err != nil {
		t.Fatal(err)
	}

	newValue, err := cache.Get(context.Background(), "entity:1:revision:2", func(context.Context) (string, error) {
		return "v2", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if newValue != "v2" {
		t.Fatalf("new revision value = %q, want v2", newValue)
	}
	if raw, err := primary.Get(context.Background(), "entity:1:revision:1"); err != nil || raw != `"v1"` {
		t.Fatalf("late old fill = %q, err=%v", raw, err)
	}
	if raw, err := primary.Get(context.Background(), "entity:1:revision:2"); err != nil || raw != `"v2"` {
		t.Fatalf("new fill = %q, err=%v", raw, err)
	}
}

func TestRuntimeMergesPartialPartitionOptionsWithDefaults(t *testing.T) {
	runtime := NewRuntime(newTestStore(), RuntimeOptions{Partitions: map[string]MemCacheOptions{
		PartitionEntities: {MaxBytes: 2048},
	}})
	partition := runtime.partition(PartitionEntities)
	if partition.maxEntries != 10000 || partition.maxBytes != 2048 || partition.sweepInterval != time.Minute {
		t.Fatalf("partition options = entries:%d bytes:%d sweep:%s",
			partition.maxEntries, partition.maxBytes, partition.sweepInterval)
	}
}

type testStore struct {
	mu     sync.Mutex
	data   map[string]string
	getErr error
	setErr error
	delErr error
}

func newTestStore() *testStore {
	return &testStore{data: make(map[string]string)}
}

func (store *testStore) Get(_ context.Context, key string) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.getErr != nil {
		return "", store.getErr
	}
	value, exists := store.data[key]
	if !exists {
		return "", ErrMiss
	}
	return value, nil
}

func (store *testStore) Set(_ context.Context, key, value string, _ time.Duration) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.setErr != nil {
		return store.setErr
	}
	store.data[key] = value
	return nil
}

func (store *testStore) Del(_ context.Context, key string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.data, key)
	return store.delErr
}
