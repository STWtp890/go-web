package controlplane

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestStatePublishesOnlyNewerGenerations(t *testing.T) {
	t.Parallel()

	first := "first"
	second := "second"
	stale := "stale"
	state := NewState(&first, 3)

	if got := state.Load(); got != &first {
		t.Fatalf("initial value = %v", *got)
	}
	if got := state.Generation(); got != 3 {
		t.Fatalf("initial generation = %d", got)
	}

	if !state.Publish(&second, 4) {
		t.Fatal("publishing a newer generation was refused")
	}
	// Re-publishing at the same generation is allowed: a reload of an unchanged
	// control plane replaces the snapshot without advancing the generation.
	if !state.Publish(&stale, 4) {
		t.Fatal("publishing the same generation was refused")
	}
	// An older generation was read before a commit that already won; storing it
	// would silently roll the corpus back.
	if state.Publish(&first, 3) {
		t.Fatal("publishing an older generation was accepted")
	}
	if got := state.Load(); got != &stale {
		t.Fatalf("value = %v", *got)
	}
	if got := state.Generation(); got != 4 {
		t.Fatalf("generation = %d", got)
	}
}

func TestStateKeepsValueAndGenerationConsistent(t *testing.T) {
	t.Parallel()

	initial := 0
	state := NewState(&initial, 0)

	var group sync.WaitGroup
	stop := make(chan struct{})
	group.Add(1)
	go func() {
		defer group.Done()
		for generation := uint64(1); generation <= 200; generation++ {
			value := int(generation)
			state.Publish(&value, generation)
		}
		close(stop)
	}()

	for {
		value := state.Load()
		generation := state.Generation()
		// The pointer and the generation must never be observed from different
		// commits, which is why they are published as one value.
		if uint64(*value) > generation {
			t.Fatalf("value %d is newer than generation %d", *value, generation)
		}
		select {
		case <-stop:
			group.Wait()
			return
		default:
		}
	}
}

func TestProjectionConvergesOnceAndFailsClosed(t *testing.T) {
	t.Parallel()

	projection := NewProjection()
	if projection.Synced() != 0 {
		t.Fatal("a fresh projection must start unconverged")
	}

	var calls int
	var mu sync.Mutex
	converge := func(context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return nil
	}

	var group sync.WaitGroup
	for index := 0; index < 8; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := projection.Converge(context.Background(), 5, converge); err != nil {
				t.Errorf("converge: %v", err)
			}
		}()
	}
	group.Wait()

	mu.Lock()
	observed := calls
	mu.Unlock()
	if observed != 1 {
		t.Fatalf("projection writes = %d, want exactly one", observed)
	}
	if projection.Synced() != 5 {
		t.Fatalf("synced generation = %d", projection.Synced())
	}

	// An older request never rewrites a newer projection.
	if err := projection.Converge(context.Background(), 4, converge); err != nil {
		t.Fatalf("converge to an older generation: %v", err)
	}
	mu.Lock()
	observed = calls
	mu.Unlock()
	if observed != 1 {
		t.Fatalf("projection writes = %d, want the older request to be a no-op", observed)
	}

	wantErr := errors.New("projection unavailable")
	failing := NewProjection()
	if err := failing.Converge(context.Background(), 2, func(context.Context) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("failing converge error = %v", err)
	}
	if failing.Synced() != 0 {
		t.Fatal("a failed convergence must not advance the synced generation")
	}
	if !errors.Is(failing.LastError(), wantErr) {
		t.Fatalf("last error = %v", failing.LastError())
	}
}

func TestProjectionReconcilerConvergesOnSignalAndInterval(t *testing.T) {
	t.Parallel()

	projection := NewProjection()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	target := uint64(0)
	synced := make(chan uint64, 8)
	projection.StartReconciler(ctx, ReconcilerConfig{
		Interval: 100 * time.Millisecond,
		Timeout:  time.Second,
		Target: func() uint64 {
			mu.Lock()
			defer mu.Unlock()
			return target
		},
		Sync: func(_ context.Context, generation uint64) error {
			synced <- generation
			return nil
		},
	})

	mu.Lock()
	target = 1
	mu.Unlock()
	projection.Signal()

	select {
	case generation := <-synced:
		if generation != 1 {
			t.Fatalf("converged generation = %d, want 1", generation)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("signal did not trigger convergence")
	}

	// The fallback interval must converge changes nobody signalled.
	mu.Lock()
	target = 2
	mu.Unlock()
	select {
	case generation := <-synced:
		if generation != 2 {
			t.Fatalf("converged generation = %d, want 2", generation)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("interval did not trigger convergence")
	}
}
