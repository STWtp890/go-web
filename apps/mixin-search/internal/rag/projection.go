package rag

import (
	"context"
	"time"
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
func (s *DocumentIndexService) StartProjectionReconciler(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(projectionReconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.projectionWake:
			case <-ticker.C:
			}
			s.reconcileProjection(ctx)
		}
	}()
}

func (s *DocumentIndexService) reconcileProjection(ctx context.Context) {
	snapshot := s.snapshot.Load()
	if s.projectionGeneration() >= snapshot.generation {
		return
	}
	syncCtx, cancel := context.WithTimeout(ctx, projectionSyncTimeout)
	defer cancel()
	// A background failure is recorded, not raised: the next read that needs a
	// current projection fails closed with the recorded error.
	_ = s.convergeProjection(syncCtx, snapshot)
}

// ensureProjection guarantees that the projection is at least as new as the
// snapshot a request is about to filter against, and fails closed with the
// vector-store error when it cannot be brought there.
func (s *DocumentIndexService) ensureProjection(ctx context.Context, snapshot *controlSnapshot) error {
	if s.projectionGeneration() >= snapshot.generation {
		return nil
	}
	return s.convergeProjection(ctx, snapshot)
}

// convergeProjection writes the snapshot's controls to the vector store once,
// even when several requests and the reconciler ask at the same time.
func (s *DocumentIndexService) convergeProjection(ctx context.Context, snapshot *controlSnapshot) error {
	s.projectionMu.Lock()
	defer s.projectionMu.Unlock()
	if s.projectionSynced >= snapshot.generation {
		return nil
	}
	err := s.core.SyncDocumentControls(ctx, snapshot.vectorDocumentControls(s.storageDomain))
	if err == nil {
		s.projectionSynced = snapshot.generation
		s.projectionErr = nil
		return nil
	}
	s.projectionErr = err
	return err
}

func (s *DocumentIndexService) projectionGeneration() uint64 {
	s.projectionMu.Lock()
	defer s.projectionMu.Unlock()
	return s.projectionSynced
}

// signalProjection wakes the reconciler without blocking: a pending wake-up
// already covers any number of published snapshots, because the reconciler
// always converges to the newest published generation.
func (s *DocumentIndexService) signalProjection() {
	select {
	case s.projectionWake <- struct{}{}:
	default:
	}
}
