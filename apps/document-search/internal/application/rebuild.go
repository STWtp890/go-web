package application

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	documentsearchv1 "packages/gen/documentsearch/v1"

	"google.golang.org/protobuf/proto"
)

// Rebuilding replays the applied event log this schema already holds. It never
// contacts document-service: the payload of every consumed event is stored
// verbatim, so the index is reproducible from local data alone even when the
// fact source is unreachable. That is what makes the query path's "never call
// back into the source" rule survivable.
//
// Both halves of the index are rebuilt from that log: the SQL projection and the
// vector collection. The embedding is a pure function of the payload, so a
// rebuild reproduces the same vectors rather than re-deriving them from a model
// that may have changed.

// rebuildBatchSize bounds one replay transaction. Each batch commits
// independently, so a long rebuild does not hold one transaction open for the
// whole event log.
const rebuildBatchSize = 250

// replayRow is one applied event read back from the local log.
type replayRow struct {
	Sequence   int64
	DocumentID string
	Payload    []byte
}

// rebuildCounters accumulates one run. `documents` tracks distinct documents so
// the reported count answers "how many documents were rebuilt", not "how many
// rows were touched".
type rebuildCounters struct {
	documents map[string]struct{}
	skipped   int
	failed    int
}

func newRebuildCounters() rebuildCounters {
	return rebuildCounters{documents: make(map[string]struct{})}
}

func (counters rebuildCounters) rebuilt() int {
	return len(counters.documents)
}

// rebuild replays the applied events into document_index, the vector collection
// and, afterwards, clears the superseded collections of this alias.
func (service *Service) rebuild(ctx context.Context) (*documentsearchv1.RebuildIndexResponse, error) {
	if service.pool == nil || service.pool.Pgx() == nil {
		return nil, fmt.Errorf("%w: database pool is not initialized", ErrDatabase)
	}
	runID, err := newIdentifier()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDatabase, err)
	}
	if _, err := service.pool.Pgx().Exec(ctx, insertRebuildRunSQL, runID); err != nil {
		return nil, fmt.Errorf("%w: start rebuild run: %v", ErrDatabase, err)
	}
	counters, replayErr := service.replayAppliedEvents(ctx)
	if replayErr != nil {
		// The failure is recorded even when the caller's context is already gone,
		// because a rebuild run that stays "running" forever is indistinguishable
		// from one that is still working.
		_, _ = service.pool.Pgx().Exec(context.WithoutCancel(ctx), failRebuildRunSQL,
			runID, replayErr.Error(), int32(counters.rebuilt()), int32(counters.skipped), int32(counters.failed))
		return nil, replayErr
	}
	// The replay restored everything the local log describes, so a physical
	// collection no index_generations row references is a leftover from an
	// earlier generation and is removed. A failure here fails the run: a rebuild
	// that silently leaves an unreferenced collection behind is not the state the
	// index record claims.
	if err := service.pruneSupersededGenerations(ctx); err != nil {
		_, _ = service.pool.Pgx().Exec(context.WithoutCancel(ctx), failRebuildRunSQL,
			runID, err.Error(), int32(counters.rebuilt()), int32(counters.skipped), int32(counters.failed))
		return nil, err
	}
	if _, err := service.pool.Pgx().Exec(ctx, completeRebuildRunSQL,
		runID, int32(counters.rebuilt()), int32(counters.skipped), int32(counters.failed)); err != nil {
		return nil, fmt.Errorf("%w: complete rebuild run: %v", ErrDatabase, err)
	}
	return &documentsearchv1.RebuildIndexResponse{
		DocumentsRebuilt: int32(counters.rebuilt()),
		DocumentsSkipped: int32(counters.skipped),
		DocumentsFailed:  int32(counters.failed),
	}, nil
}

// pruneSupersededGenerations drops the physical collections this alias no longer
// records. The keep-set is read from index_generations, so the record stays the
// only authority for which collections are live.
func (service *Service) pruneSupersededGenerations(ctx context.Context) error {
	if service.vectors == nil {
		return nil
	}
	referenced, err := referencedCollections(ctx, service.pool)
	if err != nil {
		return err
	}
	dropped, err := service.vectors.PruneGenerations(ctx, referenced)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrVectorIndex, err)
	}
	if len(dropped) > 0 {
		slog.Debug("document-search rebuild removed superseded vector collections", "collections", dropped)
	}
	return nil
}

// replayAppliedEvents walks the local event log in sequence order and applies
// each batch in one transaction.
func (service *Service) replayAppliedEvents(ctx context.Context) (rebuildCounters, error) {
	counters := newRebuildCounters()
	lastSequence := int64(0)
	for {
		batch, err := service.readReplayBatch(ctx, lastSequence)
		if err != nil {
			return counters, err
		}
		if len(batch) == 0 {
			return counters, nil
		}
		if err := service.replayBatch(ctx, batch, &counters); err != nil {
			return counters, err
		}
		lastSequence = batch[len(batch)-1].Sequence
		if len(batch) < rebuildBatchSize {
			return counters, nil
		}
	}
}

// readReplayBatch reads one ordered batch of applied events.
func (service *Service) readReplayBatch(ctx context.Context, afterSequence int64) ([]replayRow, error) {
	rows, err := service.pool.Pgx().Query(ctx, selectReplayBatchSQL, afterSequence, int64(rebuildBatchSize))
	if err != nil {
		return nil, fmt.Errorf("%w: read applied events: %v", ErrDatabase, err)
	}
	defer rows.Close()
	batch := make([]replayRow, 0, rebuildBatchSize)
	for rows.Next() {
		var row replayRow
		if err := rows.Scan(&row.Sequence, &row.DocumentID, &row.Payload); err != nil {
			return nil, fmt.Errorf("%w: read an applied event: %v", ErrDatabase, err)
		}
		batch = append(batch, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: read the applied events: %v", ErrDatabase, err)
	}
	return batch, nil
}

// replayBatch applies one batch inside a single transaction. An event whose
// payload cannot be interpreted is counted as failed and does not abort the run;
// a database failure does, because the batch transaction is then unusable.
//
// The batch's vector mutations are flushed before the commit, for the same
// reason the live apply path does it: the SQL replay and the collection must not
// diverge, and a redelivery of the batch re-writes the same points.
func (service *Service) replayBatch(ctx context.Context, batch []replayRow, counters *rebuildCounters) error {
	tx, err := service.pool.Pgx().Begin(ctx)
	if err != nil {
		return fmt.Errorf("%w: begin rebuild transaction: %v", ErrDatabase, err)
	}
	// Rolling back must not depend on the caller's context: a cancelled context
	// would leave the transaction open on a pooled connection.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	vectors := newVectorBatch(service.vectors)
	for _, row := range batch {
		request := &documentsearchv1.IndexDocumentEventRequest{}
		if err := proto.Unmarshal(row.Payload, request); err != nil {
			counters.failed++
			continue
		}
		if strings.TrimSpace(request.GetDocumentId()) == "" {
			request.DocumentId = row.DocumentID
		}
		if request.GetSequence() == 0 {
			request.Sequence = row.Sequence
		}
		event, err := normalizeEvent(request, service.cfg)
		if err != nil {
			counters.failed++
			continue
		}
		reason, err := mutateIndex(ctx, tx, service.cfg, vectors, event)
		if err != nil {
			return err
		}
		if reason != "" {
			counters.skipped++
			continue
		}
		counters.documents[event.DocumentID] = struct{}{}
	}
	if err := vectors.flush(ctx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit rebuild transaction: %v", ErrDatabase, err)
	}
	return nil
}

// indexStatus reports the collection identity and the cursor progress.
func (service *Service) indexStatus(ctx context.Context) (*documentsearchv1.GetIndexStatusResponse, error) {
	if service.pool == nil || service.pool.Pgx() == nil {
		return nil, fmt.Errorf("%w: database pool is not initialized", ErrDatabase)
	}
	var (
		generation      string
		collectionAlias string
		indexed         int64
		eventsApplied   int64
		lastSequence    int64
	)
	if err := service.pool.Pgx().QueryRow(ctx, selectIndexStatusSQL).Scan(
		&generation, &collectionAlias, &indexed, &eventsApplied, &lastSequence,
	); err != nil {
		return nil, fmt.Errorf("%w: read index status: %v", ErrDatabase, err)
	}
	if strings.TrimSpace(collectionAlias) == "" {
		collectionAlias = service.cfg.Index.CollectionAlias
	}
	return &documentsearchv1.GetIndexStatusResponse{
		CollectionAlias:  collectionAlias,
		Generation:       generation,
		IndexedDocuments: int32(indexed),
		EventsApplied:    eventsApplied,
		LastSequence:     lastSequence,
	}, nil
}

const insertRebuildRunSQL = `
INSERT INTO document_search.rebuild_runs (run_id, state, started_at)
VALUES ($1::text::uuid, 'running', clock_timestamp())`

const completeRebuildRunSQL = `
UPDATE document_search.rebuild_runs
SET state = 'succeeded',
    documents_rebuilt = $2,
    documents_skipped = $3,
    documents_failed = $4,
    completed_at = clock_timestamp()
WHERE run_id = $1::text::uuid`

const failRebuildRunSQL = `
UPDATE document_search.rebuild_runs
SET state = 'failed',
    last_error = $2,
    documents_rebuilt = $3,
    documents_skipped = $4,
    documents_failed = $5,
    completed_at = clock_timestamp()
WHERE run_id = $1::text::uuid`

const selectReplayBatchSQL = `
SELECT sequence, document_id::text, payload
FROM document_search.document_index_events
WHERE sequence > $1
ORDER BY sequence
LIMIT $2`

const selectIndexStatusSQL = `
SELECT
    COALESCE((SELECT generation FROM document_search.index_generations WHERE active ORDER BY generation LIMIT 1), ''),
    COALESCE((SELECT collection_alias FROM document_search.index_generations WHERE active ORDER BY generation LIMIT 1), ''),
    (SELECT count(*) FROM document_search.document_index),
    (SELECT count(*) FROM document_search.document_index_events),
    COALESCE((SELECT max(sequence) FROM document_search.document_index_events), 0)`
