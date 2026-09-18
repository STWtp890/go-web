package rag

import (
	"context"
	"time"

	"mixin-search/internal/controlplane"
)

// projectionReconcileInterval is how often the background reconciler checks
// whether the vector-store projection still matches the published generation.
// Convergence is also requested immediately whenever a snapshot is published,
// so this ticker only bounds how long an unnoticed change can stay unsynced.
const projectionReconcileInterval = 200 * time.Millisecond

// projectionSyncTimeout bounds one background projection write.
const projectionSyncTimeout = 10 * time.Second

// StartProjectionReconciler keeps the vector-store control projection converged
// in the background.
//
// The projection is what lets the vector store push lifecycle and authorization
// filtering into candidate selection. Syncing it on every search is what made
// the read path hold a global lock across a network write; here it happens in
// the background, coalesced per published generation, and readers only observe
// whether it has caught up.
//
// Callers must stop the reconciler by cancelling ctx. Tests that do not start it
// still behave correctly: a read that finds the projection behind converges it
// itself, which is what preserves the fail-closed contract.
//
// The closure performs the raw projection write only: controlplane.Converge owns
// the locking and the synced-generation bookkeeping, so calling it from inside
// this callback would re-enter its own lock.
func (s *DocumentIndexService) StartProjectionReconciler(ctx context.Context) {
	s.projection.StartReconciler(ctx, controlplane.ReconcilerConfig{
		Interval: projectionReconcileInterval,
		Timeout:  projectionSyncTimeout,
		Target:   func() uint64 { return s.state.Generation() },
		Sync: func(syncCtx context.Context, generation uint64) error {
			current := s.state.Load()
			if current.generation != generation {
				return nil
			}
			return s.syncProjection(syncCtx, current)
		},
	})
}

// ensureProjection guarantees that the projection is at least as new as the
// snapshot a request is about to filter against, and fails closed with the
// vector-store error when it cannot be brought there.
func (s *DocumentIndexService) ensureProjection(ctx context.Context, snapshot *controlSnapshot) error {
	if s.projection.Synced() >= snapshot.generation {
		return nil
	}
	return s.projection.Converge(ctx, snapshot.generation, func(inner context.Context) error {
		return s.syncProjection(inner, snapshot)
	})
}

// syncProjection writes the snapshot's controls to the vector store. It is the
// raw write: callers reach it through controlplane.Projection, which serializes
// concurrent convergence.
func (s *DocumentIndexService) syncProjection(ctx context.Context, snapshot *controlSnapshot) error {
	return s.core.SyncDocumentControls(ctx, snapshot.vectorDocumentControls(s.storageDomain))
}
