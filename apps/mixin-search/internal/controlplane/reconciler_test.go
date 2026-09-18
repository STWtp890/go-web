package controlplane

import (
	"context"
	"testing"
	"time"
)

// TestStartReconcilerWithoutAnIntervalConvergesOnSignal pins the meaning of a
// non-positive interval: there is no fallback tick, but a publisher's signal
// still converges the projection. The alternative - building a zero-duration
// ticker - panics the whole process, which is a bad way to learn that a caller
// left the interval unset.
func TestStartReconcilerWithoutAnIntervalConvergesOnSignal(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	projection := NewProjection()
	converged := make(chan uint64, 1)
	projection.StartReconciler(ctx, ReconcilerConfig{
		Interval: 0,
		Target:   func() uint64 { return 1 },
		Sync: func(_ context.Context, generation uint64) error {
			select {
			case converged <- generation:
			default:
			}
			return nil
		},
	})

	projection.Signal()
	select {
	case generation := <-converged:
		if generation != 1 {
			t.Fatalf("converged to generation %d, want 1", generation)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a non-positive interval disabled the wake-up path")
	}
}
