// Package controlplane holds the corpus-agnostic machinery shared by the
// control planes in this service: publication of immutable snapshots under
// monotonic generations, and convergence of a derived projection to the
// published generation.
//
// It deliberately holds no corpus state. Each corpus owns its own snapshot type,
// its own generation, its own persistence namespace and its own reconciler; only
// the mechanism is shared, which is what ADR-014 requires: mechanisms may match,
// state may never be shared. Document and chat generations therefore advance
// independently even though both are published through this type.
package controlplane

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// versioned couples a snapshot with the generation it was loaded at, so readers
// can never observe a value and a generation from different commits.
type versioned[S any] struct {
	value      *S
	generation uint64
}

// State is one corpus's published control-plane snapshot.
//
// Publication is monotonic in generation: a snapshot that was loaded before a
// concurrent commit can never overwrite the newer one. Publication is also the
// only write: readers load the pointer and never take a lock.
type State[S any] struct {
	current atomic.Pointer[versioned[S]]
}

// NewState publishes the initial snapshot at its generation.
func NewState[S any](initial *S, generation uint64) *State[S] {
	state := &State[S]{}
	state.current.Store(&versioned[S]{value: initial, generation: generation})
	return state
}

// Load returns the current snapshot. The caller must treat it as immutable.
func (s *State[S]) Load() *S {
	return s.current.Load().value
}

// Generation returns the generation of the current snapshot.
func (s *State[S]) Generation() uint64 {
	return s.current.Load().generation
}

// Publish installs value at generation and reports whether it became current.
// An older generation is refused rather than stored, because it was read before
// a commit that already won.
func (s *State[S]) Publish(value *S, generation uint64) bool {
	for {
		current := s.current.Load()
		if generation < current.generation {
			return false
		}
		if s.current.CompareAndSwap(current, &versioned[S]{value: value, generation: generation}) {
			return true
		}
	}
}

// Projection tracks how far a derived store has been brought up to a corpus's
// published generation, and runs that convergence in the background.
//
// The derived store is what lets candidate selection push lifecycle and
// authorization filtering down. Syncing it inside a request is what once made
// reads hold a global lock across a network write, so convergence happens here
// instead: a request only checks whether the projection has caught up with the
// snapshot it is about to serve.
type Projection struct {
	mu     sync.Mutex
	synced uint64
	err    error

	// syncMu serializes the actual projection writes across callers.
	syncMu sync.Mutex

	wake chan struct{}
}

// NewProjection builds an unconverged projection. Synced starts at zero on
// purpose: a restarted process must converge before it can serve, because the
// derived store may have been rebuilt or lost.
func NewProjection() *Projection {
	return &Projection{wake: make(chan struct{}, 1)}
}

// Synced returns the generation the derived store has been converged to.
func (p *Projection) Synced() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.synced
}

// LastError returns the error of the most recent failed convergence, if any.
func (p *Projection) LastError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// Signal asks the reconciler to converge without blocking: one pending wake-up
// already covers any number of published snapshots, because the reconciler
// always converges to the newest published generation.
func (p *Projection) Signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Converge brings the derived store up to generation, at most once even when
// several callers ask at the same time, and reports the error that made it
// impossible. sync runs without holding the state lock, so a slow projection
// write never blocks other readers.
func (p *Projection) Converge(ctx context.Context, generation uint64, sync func(context.Context) error) error {
	p.syncMu.Lock()
	defer p.syncMu.Unlock()

	if p.Synced() >= generation {
		return nil
	}
	err := sync(ctx)
	p.mu.Lock()
	if err == nil {
		p.synced = generation
		p.err = nil
	} else {
		p.err = err
	}
	p.mu.Unlock()
	return err
}

// ReconcilerConfig configures StartReconciler.
type ReconcilerConfig struct {
	// Interval bounds how long an unnoticed change can stay unconverged.
	Interval time.Duration
	// Timeout bounds one background convergence.
	Timeout time.Duration
	// Target reports the generation the projection must reach, usually the
	// corpus state's published generation.
	Target func() uint64
	// Sync performs one convergence to the given generation.
	Sync func(context.Context, uint64) error
}

// StartReconciler keeps the projection converged in the background until ctx is
// cancelled. It converges on every published change and on a fallback interval.
func (p *Projection) StartReconciler(ctx context.Context, config ReconcilerConfig) {
	if config.Sync == nil || config.Target == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(config.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-p.wake:
			case <-ticker.C:
			}
			generation := config.Target()
			if p.Synced() >= generation {
				continue
			}
			syncCtx := ctx
			cancel := func() {}
			if config.Timeout > 0 {
				syncCtx, cancel = context.WithTimeout(ctx, config.Timeout)
			}
			// A background failure is recorded, not raised: the next request that
			// needs a current projection fails closed with the recorded error.
			_ = p.Converge(syncCtx, generation, func(inner context.Context) error {
				return config.Sync(inner, generation)
			})
			cancel()
		}
	}()
}
