package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"document-search/internal/config"
	"document-search/internal/infrastructure/postgres"

	documentsearchv1 "packages/gen/documentsearch/v1"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/proto"
)

// Event kinds. The index models exactly these two: an upsert carries the
// complete indexable state of one document version, a delete removes it. An
// access change is delivered as an upsert with a new access snapshot, which is
// why the fence below compares access revisions separately.
const (
	EventKindUpsert = "upsert"
	EventKindDelete = "delete"
)

// Reasons reported in IndexDocumentEventResponse.reason.
const (
	ReasonApplied        = "applied"
	ReasonDuplicate      = "duplicate"
	ReasonStaleRevision  = "stale_revision"
	ReasonStaleLifecycle = "stale_lifecycle"
	ReasonStaleAccess    = "stale_access"
)

// Default profile bounds used when a configuration value is unusable. They match
// the development configuration in configs/config.yaml.
const (
	defaultIndexProfile = "markdown-v1"
	defaultMaxChunks    = 200
)

// querier is the subset of pgx shared by the pool and a transaction, so the
// index reads and writes work identically inside and outside a transaction.
type querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row
}

// documentEvent is the validated, canonical form of one applied event. Nothing
// downstream of normalizeEvent has to re-check the input.
type documentEvent struct {
	request *documentsearchv1.IndexDocumentEventRequest

	EventID             string
	Sequence            int64
	Kind                string
	DocumentID          string
	VersionID           string
	AggregateRevision   uint64
	ActivationRevision  uint64
	AccessRevision      uint64
	LifecycleRevision   uint64
	LifecycleStatus     string
	PublicationStatus   string
	OwnerSubjectKey     string
	OwnerSpaceID        string
	AuthenticatedPublic bool
	AllowedSpaceIDs     []string
	Title               string
	Summary             string
	Content             string
	ContentFormat       string
	ContentSHA256       string
	IndexProfile        string
	ChunkCount          int
	// CreatedAt is the document's creation instant as the fact source stated it.
	// nil means the event did not carry one, and the index falls back to the
	// apply time rather than inventing an instant.
	CreatedAt *time.Time
	// DocumentUpdatedAt is the document's own update instant. The fact source
	// writes documents.updated_at in the same transaction that emits the event,
	// so occurred_at is that transaction's time and the faithful source for it.
	// nil means the event stated none: the projection then keeps the instant it
	// already has instead of presenting the index write as a document update.
	DocumentUpdatedAt *time.Time
}

// eventRevisions groups the monotonic fences an event carries, together with the
// kind that decides which of them apply.
type eventRevisions struct {
	Kind              string
	AggregateRevision uint64
	AccessRevision    uint64
	LifecycleRevision uint64
}

func (event *documentEvent) revisions() eventRevisions {
	return eventRevisions{
		Kind:              event.Kind,
		AggregateRevision: event.AggregateRevision,
		AccessRevision:    event.AccessRevision,
		LifecycleRevision: event.LifecycleRevision,
	}
}

// fenceState is what the index currently knows about one document: the live
// projection row, if any, and the tombstone, if any.
type fenceState struct {
	HasIndex                   bool
	AggregateRevision          uint64
	AccessRevision             uint64
	LifecycleRevision          uint64
	HasTombstone               bool
	TombstoneLifecycleRevision uint64
}

// evaluateFence applies the out-of-order rules and returns an empty string when
// the event may be applied, or the reason it must be ignored.
//
//   - A tombstone at lifecycle revision N closes the document for every event at
//     revision <= N, so a late delete cannot be undone by an older upsert. An
//     event at a strictly newer lifecycle revision is a new document generation
//     and may resurrect it.
//   - A lower aggregate revision never overwrites a higher one.
//   - A lower access revision never overwrites a newer access snapshot, so a
//     late delivery cannot re-grant access that was already revoked.
//   - Equal revisions are allowed: re-delivering the same state with a different
//     event_id is idempotent, and it is how a rebuild repairs a corrupted row.
//
// The access fence applies to upserts only. A delete is not an access change: it
// retires the document, and the fact source may carry a stale or zero
// access_revision on it while its lifecycle_revision is strictly newer. Gating a
// delete on the access fence would silently keep a deleted document searchable,
// which is worse than any staleness it would prevent.
//
// The aggregate fence is applied to a delete only when the delete actually
// carries an aggregate revision. Zero is the unset value of the field: a delete
// that simply does not state a content revision must still retire the document,
// otherwise an unset field in the source envelope would leave deleted documents
// in the index.
//
// It is a pure function so the ordering rules are unit tested without a
// database, and so the apply path and the rebuild path cannot drift apart.
func evaluateFence(state fenceState, event eventRevisions) string {
	if state.HasTombstone && event.LifecycleRevision <= state.TombstoneLifecycleRevision {
		return ReasonStaleLifecycle
	}
	if !state.HasIndex {
		return ""
	}
	if event.Kind == EventKindDelete {
		if event.AggregateRevision > 0 && event.AggregateRevision < state.AggregateRevision {
			return ReasonStaleRevision
		}
	} else {
		if event.AggregateRevision < state.AggregateRevision {
			return ReasonStaleRevision
		}
		if event.AccessRevision < state.AccessRevision {
			return ReasonStaleAccess
		}
	}
	if event.LifecycleRevision < state.LifecycleRevision {
		return ReasonStaleLifecycle
	}
	return ""
}

// normalizeEvent validates one request and returns its canonical form. Every
// rejection is INVALID_ARGUMENT at the transport boundary.
func normalizeEvent(request *documentsearchv1.IndexDocumentEventRequest, cfg config.Config) (*documentEvent, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: request is required", ErrInvalidInput)
	}
	kind := strings.ToLower(strings.TrimSpace(request.GetKind()))
	if kind != EventKindUpsert && kind != EventKindDelete {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedEventKind, request.GetKind())
	}
	rawDocumentID := strings.TrimSpace(request.GetDocumentId())
	if rawDocumentID == "" {
		return nil, ErrMissingDocumentID
	}
	documentID, err := normalizeIdentifier(rawDocumentID)
	if err != nil {
		return nil, fmt.Errorf("%w: document_id %q", ErrInvalidIdentifier, rawDocumentID)
	}
	rawEventID := strings.TrimSpace(request.GetEventId())
	if rawEventID == "" {
		return nil, ErrMissingEventID
	}
	eventID, err := normalizeIdentifier(rawEventID)
	if err != nil {
		return nil, fmt.Errorf("%w: event_id %q", ErrInvalidIdentifier, rawEventID)
	}
	if request.GetSequence() <= 0 {
		return nil, ErrMissingSequence
	}

	event := &documentEvent{
		request:             request,
		EventID:             eventID,
		Sequence:            request.GetSequence(),
		Kind:                kind,
		DocumentID:          documentID,
		AggregateRevision:   request.GetAggregateRevision(),
		ActivationRevision:  request.GetActivationRevision(),
		AccessRevision:      request.GetAccessRevision(),
		LifecycleRevision:   request.GetLifecycleRevision(),
		OwnerSubjectKey:     truncateRunes(strings.TrimSpace(request.GetOwnerSubjectKey()), 192),
		AuthenticatedPublic: request.GetAuthenticatedPublic(),
		AllowedSpaceIDs:     canonicalIDs(request.GetAllowedSpaceIds()),
		Content:             request.GetContent(),
	}

	if kind == EventKindDelete {
		return event, nil
	}

	rawVersionID := strings.TrimSpace(request.GetVersionId())
	if rawVersionID == "" {
		return nil, ErrMissingVersionID
	}
	versionID, err := normalizeIdentifier(rawVersionID)
	if err != nil {
		return nil, fmt.Errorf("%w: version_id %q", ErrInvalidIdentifier, rawVersionID)
	}
	rawOwnerSpaceID := strings.TrimSpace(request.GetOwnerSpaceId())
	if rawOwnerSpaceID == "" {
		return nil, ErrMissingOwnerSpace
	}
	ownerSpaceID, err := normalizeIdentifier(rawOwnerSpaceID)
	if err != nil {
		return nil, fmt.Errorf("%w: owner_space_id %q", ErrInvalidIdentifier, rawOwnerSpaceID)
	}

	profile := strings.TrimSpace(request.GetIndexProfile())
	if profile == "" {
		profile = strings.TrimSpace(cfg.Index.IndexProfile)
	}
	if profile == "" {
		profile = defaultIndexProfile
	}
	profile = truncateRunes(profile, 32)
	maxChunks := cfg.Index.MaxChunks
	if maxChunks <= 0 {
		maxChunks = defaultMaxChunks
	}

	event.VersionID = versionID
	event.OwnerSpaceID = ownerSpaceID
	event.IndexProfile = profile
	event.CreatedAt = parseEventInstant(request.GetCreatedAt())
	// occurred_at is the instant of the fact source's transaction, which is also
	// the instant it stamped documents.updated_at. It is therefore the document's
	// real update time, not the moment this index happened to write the row.
	event.DocumentUpdatedAt = parseEventInstant(request.GetOccurredAt())
	event.LifecycleStatus = normalizeStatus(request.GetLifecycleStatus(), "lifecycle_status", "active")
	// Publication defaults to draft on purpose: an event that does not say the
	// version is published must not become searchable.
	event.PublicationStatus = normalizeStatus(request.GetPublicationStatus(), "publication_status", "draft")
	event.Title = truncateRunes(strings.TrimSpace(request.GetTitle()), 255)
	event.Summary = truncateRunes(strings.TrimSpace(request.GetSummary()), 512)
	event.ContentFormat = truncateRunes(normalizeStatus(request.GetContentFormat(), "", "markdown"), 16)
	event.ContentSHA256 = contentDigest(request.GetContentSha256(), request.GetContent())
	event.ChunkCount = chunkCount(event.Content, profile, maxChunks)
	return event, nil
}

// normalizeStatus lower-cases a status string and drops the enum prefix the
// document service proto uses, so "PUBLICATION_STATUS_PUBLISHED" and "published"
// are the same value. An empty value falls back to the documented default.
func normalizeStatus(value, enumPrefix, fallback string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return fallback
	}
	if enumPrefix != "" {
		trimmed = strings.TrimPrefix(trimmed, enumPrefix+"_")
	} else {
		for _, prefix := range []string{"content_format_", "lifecycle_status_", "publication_status_"} {
			trimmed = strings.TrimPrefix(trimmed, prefix)
		}
	}
	trimmed = strings.ReplaceAll(trimmed, "_status", "")
	if trimmed == "" || trimmed == "unspecified" {
		return fallback
	}
	return trimmed
}

// parseEventInstant reads the RFC3339 instant the fact source states for the
// document's creation time.
//
// An absent value is not an error, and neither is one this service cannot read:
// the target column falls back to the apply time, which is honest about what the
// index knows. Rejecting an otherwise valid document event because a timestamp
// format drifted would stop the consumer and leave the index stale, which is the
// worse failure.
func parseEventInstant(value string) *time.Time {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999-07"} {
		parsed, err := time.Parse(layout, trimmed)
		if err != nil {
			continue
		}
		utc := parsed.UTC()
		return &utc
	}
	return nil
}

// contentDigest prefers a well-formed digest supplied by the fact source and
// falls back to hashing the content, so the stored digest is always a real
// sha256 of the indexed body.
func contentDigest(provided, content string) string {
	candidate := strings.ToLower(strings.TrimSpace(provided))
	if len(candidate) == 64 {
		if _, err := hex.DecodeString(candidate); err == nil {
			return candidate
		}
	}
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

// applyOptions selects between the three callers of the apply path.
type applyOptions struct {
	// recordEvent appends the event to the local applied event log. The rebuild
	// path sets it to false because the rows are already there.
	recordEvent bool
	// cursorStream names the consumer cursor to advance inside the same
	// transaction. Empty means the caller is not the consumer, so the cursor is
	// left alone.
	cursorStream string
}

// applyOutcome is the result of one apply.
type applyOutcome struct {
	Applied bool
	Reason  string
	State   *documentsearchv1.DocumentIndexState
}

// applyEventTx applies one event in a single transaction: the applied event log,
// the index projection, the tombstone, the vector collection and the consumer
// cursor move together, so a crash can only cause redelivery, never a skipped
// event.
//
// The vector half is applied inside the transaction but is not part of it; see
// vector_write.go for why it is ordered before the commit.
func applyEventTx(
	ctx context.Context,
	cfg config.Config,
	pool *postgres.Pool,
	vectors VectorIndex,
	request *documentsearchv1.IndexDocumentEventRequest,
	options applyOptions,
) (applyOutcome, error) {
	event, err := normalizeEvent(request, cfg)
	if err != nil {
		return applyOutcome{}, err
	}
	if pool == nil || pool.Pgx() == nil {
		return applyOutcome{}, fmt.Errorf("%w: database pool is not initialized", ErrDatabase)
	}
	tx, err := pool.Pgx().Begin(ctx)
	if err != nil {
		return applyOutcome{}, fmt.Errorf("%w: begin transaction: %v", ErrDatabase, err)
	}
	// The rollback deliberately does not use the caller's context. A cancelled
	// context (the consumer is cancelled mid-apply, or the RPC is aborted) would
	// make the ROLLBACK itself fail, and the pool would then hand the connection
	// out again while it is still inside an open transaction: the next reader on
	// that connection would see index state that was never committed.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	outcome := applyOutcome{Reason: ReasonApplied}
	batch := newVectorBatch(vectors)
	if options.recordEvent {
		duplicate, err := recordEvent(ctx, tx, event)
		if err != nil {
			return applyOutcome{}, err
		}
		if duplicate {
			// The event was applied by an earlier delivery. Nothing in the index
			// changes, but the cursor still advances: the event is not pending.
			outcome.Reason = ReasonDuplicate
		}
	}
	if outcome.Reason != ReasonDuplicate {
		reason, err := mutateIndex(ctx, tx, cfg, batch, event)
		if err != nil {
			return applyOutcome{}, err
		}
		if reason == "" {
			outcome.Applied = true
			outcome.Reason = ReasonApplied
		} else {
			outcome.Reason = reason
		}
	}
	if options.cursorStream != "" {
		if err := advanceCursor(ctx, tx, options.cursorStream, event.Sequence); err != nil {
			return applyOutcome{}, err
		}
	}
	state, err := loadIndexState(ctx, tx, event.DocumentID)
	if err != nil {
		return applyOutcome{}, err
	}
	outcome.State = state
	if err := batch.flush(ctx); err != nil {
		return applyOutcome{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return applyOutcome{}, fmt.Errorf("%w: commit transaction: %v", ErrDatabase, err)
	}
	return outcome, nil
}

// recordEvent appends the event to the applied event log. It reports a duplicate
// when the event id is already present and rejects a sequence that another event
// already owns, because that would make the log's ordering a lie.
//
// Idempotency is decided by looking the event id up, not by which unique index
// the database happens to check first. Redelivery is the normal case for an
// at-least-once consumer, and a redelivered event whose sequence row already
// exists must be a no-op: relying on ON CONFLICT arbitration alone lets the
// primary key violation on (sequence) surface instead of the duplicate, which
// turned every reconnect into a spurious failure.
func recordEvent(ctx context.Context, tx querier, event *documentEvent) (bool, error) {
	payload, err := proto.Marshal(event.request)
	if err != nil {
		return false, fmt.Errorf("%w: encode event %s: %v", ErrInvalidInput, event.EventID, err)
	}
	var storedSequence int64
	err = tx.QueryRow(ctx, selectEventSequenceSQL, event.EventID).Scan(&storedSequence)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return false, fmt.Errorf("%w: look up event %s: %v", ErrDatabase, event.EventID, err)
	}
	tag, err := tx.Exec(ctx, insertEventSQL,
		event.Sequence,
		event.EventID,
		event.DocumentID,
		event.Kind,
		int64(event.AggregateRevision),
		payload,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "document_index_events_pkey" {
			// The sequence is occupied. Two appliers can reach this point at the
			// same moment (a reconnect racing the delivery it is replacing), so
			// the conflict is reported and the retry decides: on the next attempt
			// the lookup above finds the event and treats it as a duplicate.
			return false, fmt.Errorf("%w: sequence %d", ErrEventSequenceTaken, event.Sequence)
		}
		return false, fmt.Errorf("%w: record event %s: %v", ErrDatabase, event.EventID, err)
	}
	return tag.RowsAffected() == 0, nil
}

// mutateIndex writes the index projection for one event after applying the
// revision fences. It returns the ignore reason, or an empty string when the
// index was changed.
//
// The vector mutation is recorded on the batch rather than written here: the
// batch is flushed inside the same transaction window, and a fenced or duplicate
// event must not touch the collection at all. A delete removes the document's
// points; an upsert replaces them with the chunks of the version that won the
// fence.
func mutateIndex(ctx context.Context, tx querier, cfg config.Config, batch *vectorBatch, event *documentEvent) (string, error) {
	state, err := readFence(ctx, tx, event.DocumentID)
	if err != nil {
		return "", err
	}
	if reason := evaluateFence(state, event.revisions()); reason != "" {
		return reason, nil
	}
	if event.Kind == EventKindDelete {
		if _, err := tx.Exec(ctx, deleteIndexRowSQL, event.DocumentID); err != nil {
			return "", fmt.Errorf("%w: remove index row: %v", ErrDatabase, err)
		}
		if _, err := tx.Exec(ctx, upsertTombstoneSQL, event.DocumentID, int64(event.LifecycleRevision), int64(event.AggregateRevision)); err != nil {
			return "", fmt.Errorf("%w: write tombstone: %v", ErrDatabase, err)
		}
		batch.remove(event.DocumentID)
		return "", nil
	}
	allowedSpaces, err := json.Marshal(event.AllowedSpaceIDs)
	if err != nil {
		return "", fmt.Errorf("%w: encode allowed_space_ids: %v", ErrInvalidInput, err)
	}
	vectorProfile, vectorDimensions := configuredVectorProfile(cfg)
	if _, err := tx.Exec(ctx, upsertIndexRowSQL,
		event.DocumentID,
		event.VersionID,
		event.OwnerSubjectKey,
		event.OwnerSpaceID,
		event.AuthenticatedPublic,
		string(allowedSpaces),
		event.LifecycleStatus,
		event.PublicationStatus,
		event.Title,
		event.Summary,
		event.Content,
		event.ContentFormat,
		event.ContentSHA256,
		int32(event.ChunkCount),
		event.IndexProfile,
		int64(event.AggregateRevision),
		int64(event.ActivationRevision),
		int64(event.AccessRevision),
		int64(event.LifecycleRevision),
		event.CreatedAt,
		event.DocumentUpdatedAt,
		vectorProfile,
		int32(vectorDimensions),
	); err != nil {
		return "", fmt.Errorf("%w: upsert index row: %v", ErrDatabase, err)
	}
	// A document that comes back at a newer lifecycle revision is live again, so
	// its tombstone is removed. The live row's lifecycle revision is then the
	// fence that keeps the older events out.
	if _, err := tx.Exec(ctx, deleteTombstoneSQL, event.DocumentID); err != nil {
		return "", fmt.Errorf("%w: clear tombstone: %v", ErrDatabase, err)
	}
	batch.replace(vectorDocumentForEvent(event, cfg))
	return "", nil
}

// readFence loads the current index row and tombstone revisions.
func readFence(ctx context.Context, tx querier, documentID string) (fenceState, error) {
	var state fenceState
	var aggregateRevision, accessRevision, lifecycleRevision int64
	err := tx.QueryRow(ctx, selectIndexFenceSQL, documentID).Scan(&aggregateRevision, &accessRevision, &lifecycleRevision)
	switch {
	case err == nil:
		state.HasIndex = true
		state.AggregateRevision = uint64(aggregateRevision)
		state.AccessRevision = uint64(accessRevision)
		state.LifecycleRevision = uint64(lifecycleRevision)
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return state, fmt.Errorf("%w: read index fence: %v", ErrDatabase, err)
	}
	var tombstoneLifecycle int64
	err = tx.QueryRow(ctx, selectTombstoneFenceSQL, documentID).Scan(&tombstoneLifecycle)
	switch {
	case err == nil:
		state.HasTombstone = true
		state.TombstoneLifecycleRevision = uint64(tombstoneLifecycle)
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return state, fmt.Errorf("%w: read tombstone fence: %v", ErrDatabase, err)
	}
	return state, nil
}

// loadIndexState reports the applied state of one document, or nil when the
// service has never applied an event for it. A tombstone is an applied state:
// callers learn that the document was deleted rather than that it is unknown.
func loadIndexState(ctx context.Context, tx querier, documentID string) (*documentsearchv1.DocumentIndexState, error) {
	var (
		versionID         string
		aggregateRevision int64
		activationRev     int64
		accessRevision    int64
		lifecycleRevision int64
		lifecycleStatus   string
		publicationStatus string
		chunkCount        int32
		contentSHA256     string
		updatedAt         time.Time
	)
	err := tx.QueryRow(ctx, selectIndexStateSQL, documentID).Scan(
		&versionID,
		&aggregateRevision,
		&activationRev,
		&accessRevision,
		&lifecycleRevision,
		&lifecycleStatus,
		&publicationStatus,
		&chunkCount,
		&contentSHA256,
		&updatedAt,
	)
	switch {
	case err == nil:
		return &documentsearchv1.DocumentIndexState{
			DocumentId:         documentID,
			VersionId:          versionID,
			AggregateRevision:  uint64(aggregateRevision),
			ActivationRevision: uint64(activationRev),
			AccessRevision:     uint64(accessRevision),
			LifecycleRevision:  uint64(lifecycleRevision),
			Status:             documentsearchv1.IndexStatus_INDEX_STATUS_INDEXED,
			ChunkCount:         chunkCount,
			ContentSha256:      strings.TrimSpace(contentSHA256),
			IndexedAt:          updatedAt.UTC().Format(time.RFC3339Nano),
		}, nil
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return nil, fmt.Errorf("%w: read index state: %v", ErrDatabase, err)
	}

	var tombstoneLifecycle, tombstoneAggregate int64
	var deletedAt time.Time
	err = tx.QueryRow(ctx, selectTombstoneStateSQL, documentID).Scan(&tombstoneLifecycle, &tombstoneAggregate, &deletedAt)
	switch {
	case err == nil:
		return &documentsearchv1.DocumentIndexState{
			DocumentId:        documentID,
			AggregateRevision: uint64(tombstoneAggregate),
			LifecycleRevision: uint64(tombstoneLifecycle),
			Status:            documentsearchv1.IndexStatus_INDEX_STATUS_DELETED,
			IndexedAt:         deletedAt.UTC().Format(time.RFC3339Nano),
		}, nil
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: read tombstone state: %v", ErrDatabase, err)
	}
}

// advanceCursor moves the consumer cursor forward inside the caller's
// transaction. It is monotonic: a redelivered event never moves it backwards.
func advanceCursor(ctx context.Context, tx querier, stream string, sequence int64) error {
	if _, err := tx.Exec(ctx, advanceCursorSQL, stream, sequence); err != nil {
		return fmt.Errorf("%w: advance cursor %s: %v", ErrDatabase, stream, err)
	}
	return nil
}

// advanceCursorValue is the cursor arithmetic the statement above applies inside
// the database, expressed once so it is unit testable: a cursor only moves
// forward, and a redelivered event below it changes nothing.
func advanceCursorValue(current, incoming int64) int64 {
	if incoming > current {
		return incoming
	}
	return current
}

// advanceCursor reads the durable cursor of one consumer stream.
func readCursor(ctx context.Context, tx querier, stream string) (int64, error) {
	var cursor int64
	err := tx.QueryRow(ctx, selectCursorSQL, stream).Scan(&cursor)
	switch {
	case err == nil:
		return cursor, nil
	case errors.Is(err, pgx.ErrNoRows):
		return 0, nil
	default:
		return 0, fmt.Errorf("%w: read cursor %s: %v", ErrDatabase, stream, err)
	}
}

// All statements are schema qualified on purpose: the service writes its own
// schema and never relies on search_path, so a misconfigured connection cannot
// silently write into another service's schema.
const insertEventSQL = `
INSERT INTO document_search.document_index_events
    (sequence, event_id, document_id, event_kind, aggregate_revision, payload)
VALUES ($1, $2::text::uuid, $3::text::uuid, $4, $5, $6)
ON CONFLICT (event_id) DO NOTHING`

const selectEventSequenceSQL = `
SELECT sequence
FROM document_search.document_index_events
WHERE event_id = $1::text::uuid`

const selectIndexFenceSQL = `
SELECT aggregate_revision, access_revision, lifecycle_revision
FROM document_search.document_index
WHERE document_id = $1::text::uuid`

const selectTombstoneFenceSQL = `
SELECT lifecycle_revision
FROM document_search.document_index_tombstones
WHERE document_id = $1::text::uuid`

// upsertIndexRowSQL writes the index projection.
//
// created_at and document_updated_at come from the document service event, so a
// hit reports the document's own instants rather than when this index happened
// to write the row (that is `updated_at`, which stays local bookkeeping and is
// what DocumentIndexState.indexed_at reports). When the event states neither,
// an insert falls back to the apply time and an update keeps the instant already
// stored, so a re-delivered or partial event cannot move a document's timeline.
//
// vector_profile and vector_dimensions record which embedding produced the
// document's points. They are the per-document half of the generation record:
// after a profile change the row still says what the stored vector means.
const upsertIndexRowSQL = `
INSERT INTO document_search.document_index (
    document_id, version_id, owner_subject_key, owner_space_id, authenticated_public,
    allowed_space_ids, lifecycle_status, publication_status, title, summary, content,
    content_format, content_sha256, chunk_count, index_profile,
    aggregate_revision, activation_revision, access_revision, lifecycle_revision,
    created_at, document_updated_at, vector_profile, vector_dimensions, updated_at
) VALUES (
    $1::text::uuid, $2::text::uuid, $3, $4::text::uuid, $5,
    $6::text::jsonb, $7, $8, $9, $10, $11,
    $12, $13, $14, $15,
    $16, $17, $18, $19,
    COALESCE($20::timestamptz, clock_timestamp()),
    COALESCE($21::timestamptz, clock_timestamp()),
    $22, $23,
    clock_timestamp()
)
ON CONFLICT (document_id) DO UPDATE SET
    version_id = EXCLUDED.version_id,
    owner_subject_key = EXCLUDED.owner_subject_key,
    owner_space_id = EXCLUDED.owner_space_id,
    authenticated_public = EXCLUDED.authenticated_public,
    allowed_space_ids = EXCLUDED.allowed_space_ids,
    lifecycle_status = EXCLUDED.lifecycle_status,
    publication_status = EXCLUDED.publication_status,
    title = EXCLUDED.title,
    summary = EXCLUDED.summary,
    content = EXCLUDED.content,
    content_format = EXCLUDED.content_format,
    content_sha256 = EXCLUDED.content_sha256,
    chunk_count = EXCLUDED.chunk_count,
    index_profile = EXCLUDED.index_profile,
    aggregate_revision = EXCLUDED.aggregate_revision,
    activation_revision = EXCLUDED.activation_revision,
    access_revision = EXCLUDED.access_revision,
    lifecycle_revision = EXCLUDED.lifecycle_revision,
    vector_profile = EXCLUDED.vector_profile,
    vector_dimensions = EXCLUDED.vector_dimensions,
    created_at = COALESCE($20::timestamptz, document_search.document_index.created_at),
    document_updated_at = COALESCE($21::timestamptz, document_search.document_index.document_updated_at),
    updated_at = clock_timestamp()`

const deleteIndexRowSQL = `
DELETE FROM document_search.document_index
WHERE document_id = $1::text::uuid`

const deleteTombstoneSQL = `
DELETE FROM document_search.document_index_tombstones
WHERE document_id = $1::text::uuid`

const upsertTombstoneSQL = `
INSERT INTO document_search.document_index_tombstones AS existing (
    document_id, lifecycle_revision, aggregate_revision, deleted_at
) VALUES (
    $1::text::uuid, $2, $3, clock_timestamp()
)
ON CONFLICT (document_id) DO UPDATE SET
    lifecycle_revision = GREATEST(existing.lifecycle_revision, EXCLUDED.lifecycle_revision),
    aggregate_revision = GREATEST(existing.aggregate_revision, EXCLUDED.aggregate_revision),
    deleted_at = clock_timestamp()`

const selectIndexStateSQL = `
SELECT version_id::text, aggregate_revision, activation_revision, access_revision, lifecycle_revision,
       lifecycle_status, publication_status, chunk_count, content_sha256, updated_at
FROM document_search.document_index
WHERE document_id = $1::text::uuid`

const selectTombstoneStateSQL = `
SELECT lifecycle_revision, aggregate_revision, deleted_at
FROM document_search.document_index_tombstones
WHERE document_id = $1::text::uuid`

const advanceCursorSQL = `
INSERT INTO document_search.consumer_cursors (stream, last_sequence, updated_at)
VALUES ($1, $2, clock_timestamp())
ON CONFLICT (stream) DO UPDATE SET
    last_sequence = GREATEST(document_search.consumer_cursors.last_sequence, EXCLUDED.last_sequence),
    updated_at = clock_timestamp()`

const selectCursorSQL = `
SELECT last_sequence
FROM document_search.consumer_cursors
WHERE stream = $1`
