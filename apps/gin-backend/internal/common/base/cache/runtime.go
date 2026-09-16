package cache

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	PartitionEntities  = "entities"
	PartitionDocuments = "documents"
)

type RuntimeOptions struct {
	Partitions map[string]MemCacheOptions
}

type Runtime struct {
	primary    Store
	flight     singleflight.Group
	options    map[string]MemCacheOptions
	mu         sync.Mutex
	partitions map[string]*MemCache
	counters   sync.Map
}

type EntityCacheStats struct {
	PrimaryHits    uint64 `json:"primaryHits"`
	PrimaryMisses  uint64 `json:"primaryMisses"`
	PrimaryErrors  uint64 `json:"primaryErrors"`
	FallbackHits   uint64 `json:"fallbackHits"`
	FallbackMisses uint64 `json:"fallbackMisses"`
	FallbackErrors uint64 `json:"fallbackErrors"`
	Loads          uint64 `json:"loads"`
	Shared         uint64 `json:"shared"`
	FillErrors     uint64 `json:"fillErrors"`
	EvictErrors    uint64 `json:"evictErrors"`
}

type RuntimeStats struct {
	Entities map[string]EntityCacheStats `json:"entities"`
	Memory   map[string]MemCacheStats    `json:"memory"`
}

type entityCounters struct {
	primaryHits    atomic.Uint64
	primaryMisses  atomic.Uint64
	primaryErrors  atomic.Uint64
	fallbackHits   atomic.Uint64
	fallbackMisses atomic.Uint64
	fallbackErrors atomic.Uint64
	loads          atomic.Uint64
	shared         atomic.Uint64
	fillErrors     atomic.Uint64
	evictErrors    atomic.Uint64
}

func NewRuntime(primary Store, options RuntimeOptions) *Runtime {
	if primary == nil {
		panic("cache runtime: primary store is required")
	}
	normalized := defaultPartitionOptions()
	for name, option := range options.Partitions {
		base := normalized[name]
		if option.MaxEntries > 0 {
			base.MaxEntries = option.MaxEntries
		}
		if option.MaxBytes > 0 {
			base.MaxBytes = option.MaxBytes
		}
		if option.SweepInterval > 0 {
			base.SweepInterval = option.SweepInterval
		}
		normalized[name] = base
	}
	return &Runtime{
		primary: primary, options: normalized, partitions: make(map[string]*MemCache),
	}
}

var processRuntime = NewRuntime(NewRedisCache(), RuntimeOptions{})

func DefaultRuntime() *Runtime {
	return processRuntime
}

func NewEntityFromRuntime[T any](
	runtime *Runtime,
	namespace string,
	partition string,
	ttl time.Duration,
) *EntityCache[T] {
	if runtime == nil {
		panic("cache runtime: runtime is required")
	}
	if namespace == "" {
		panic("cache runtime: namespace is required")
	}
	return newEntityCache[T](
		runtime.primary,
		runtime.partition(partition),
		&runtime.flight,
		namespace,
		runtime.entityCounters(namespace),
		ttl,
	)
}

func (runtime *Runtime) Snapshot() RuntimeStats {
	snapshot := RuntimeStats{
		Entities: make(map[string]EntityCacheStats),
		Memory:   make(map[string]MemCacheStats),
	}
	runtime.counters.Range(func(key, value any) bool {
		snapshot.Entities[key.(string)] = value.(*entityCounters).snapshot()
		return true
	})
	runtime.mu.Lock()
	for name, partition := range runtime.partitions {
		snapshot.Memory[name] = partition.Stats()
	}
	runtime.mu.Unlock()
	return snapshot
}

// StartStatsLogger 定期输出不含业务 key 的缓存统计，同时触发过期项清理。
func (runtime *Runtime) StartStatsLogger(interval time.Duration) func() {
	if interval <= 0 {
		interval = time.Minute
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				slog.Info("entity_cache_runtime_stats", slog.Any("snapshot", runtime.Snapshot()))
			case <-stop:
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done
		})
	}
}

func (runtime *Runtime) partition(name string) *MemCache {
	if name == "" {
		name = PartitionEntities
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if existing := runtime.partitions[name]; existing != nil {
		return existing
	}
	options, exists := runtime.options[name]
	if !exists {
		options = MemCacheOptions{MaxEntries: 1024, MaxBytes: 16 << 20, SweepInterval: time.Minute}
	}
	partition := newMemCache(options)
	runtime.partitions[name] = partition
	return partition
}

func (runtime *Runtime) entityCounters(namespace string) *entityCounters {
	value, _ := runtime.counters.LoadOrStore(namespace, &entityCounters{})
	return value.(*entityCounters)
}

func (counters *entityCounters) snapshot() EntityCacheStats {
	return EntityCacheStats{
		PrimaryHits: counters.primaryHits.Load(), PrimaryMisses: counters.primaryMisses.Load(),
		PrimaryErrors: counters.primaryErrors.Load(), FallbackHits: counters.fallbackHits.Load(),
		FallbackMisses: counters.fallbackMisses.Load(), FallbackErrors: counters.fallbackErrors.Load(),
		Loads: counters.loads.Load(), Shared: counters.shared.Load(), FillErrors: counters.fillErrors.Load(),
		EvictErrors: counters.evictErrors.Load(),
	}
}

func defaultPartitionOptions() map[string]MemCacheOptions {
	return map[string]MemCacheOptions{
		PartitionEntities:  {MaxEntries: 10000, MaxBytes: 32 << 20, SweepInterval: time.Minute},
		PartitionDocuments: {MaxEntries: 2048, MaxBytes: 64 << 20, SweepInterval: time.Minute},
	}
}
