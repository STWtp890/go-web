package application

import (
	"context"
	"errors"
	"fmt"

	"document-service/internal/domain"
	"document-service/internal/infrastructure/postgres"

	documentv1 "packages/gen/document/v1"
	"packages/serviceauth"

	"google.golang.org/protobuf/proto"
)

// defaultEventBatchSize is used when a consumer asks for no particular batch.
const defaultEventBatchSize = 100

// ListDocumentEvents opens the Outbox stream from a consumer owned cursor.
//
// The reader returned here is live: when it has drained the committed range it
// reports an empty batch, which is what a following stream needs in order to keep
// polling. A backfill consumer that wants "the range is finished" instead opens a
// bounded reader with OpenEventReader; that one reports
// serviceauth.ErrStreamExhausted once a read comes back empty.
func (service *Service) ListDocumentEvents(ctx context.Context, principal *serviceauth.Principal, afterSequence int64, batchSize int) (DocumentEventReader, error) {
	return service.OpenEventReader(ctx, principal, afterSequence, batchSize, false)
}

// OpenEventReader opens an Outbox reader. bounded selects the end-of-range
// behaviour described on ListDocumentEvents.
//
// The event stream is a service-to-service read with no resource subject: the
// consumer follows every document's changes, so requiring it to assert "which
// subject am I acting for" would be meaningless. Identity still applies, and it
// is enforced twice: the transport boundary authenticates the caller and checks
// the document-event-reader scope, and this function re-checks the principal and
// the same scope so the rule does not depend on which adapter is in front.
func (service *Service) OpenEventReader(ctx context.Context, principal *serviceauth.Principal, afterSequence int64, batchSize int, bounded bool) (DocumentEventReader, error) {
	if principal == nil {
		return nil, errUnauthenticated
	}
	if err := principal.RequireScope(serviceauth.ScopeDocumentEventReader); err != nil {
		return nil, err
	}
	if afterSequence < 0 {
		return nil, fmt.Errorf("%w: after_sequence must not be negative", domain.ErrInvalidInput)
	}
	if batchSize <= 0 {
		batchSize = defaultEventBatchSize
	}
	if maxBatch := service.cfg.Outbox.MaxBatchSize; maxBatch > 0 && batchSize > maxBatch {
		batchSize = maxBatch
	}
	return &eventReader{
		store:     service.store,
		cursor:    afterSequence,
		batchSize: batchSize,
		bounded:   bounded,
	}, nil
}

// DocumentEventReader is one Outbox read cursor. The consumer owns the cursor
// value; the reader only advances it as batches are handed out, so re-reading from
// the same starting point is always possible (at-least-once delivery).
type DocumentEventReader interface {
	Next(ctx context.Context) ([]*documentv1.DocumentEventEnvelope, error)
	Close()
}

// eventReader reads the Outbox in sequence order. Readers are stateless with
// respect to the stream itself - there is no server side cursor to advance and no
// advisory lock to take - so two consumers reading the same range never block each
// other.
type eventReader struct {
	store     *postgres.Store
	cursor    int64
	batchSize int
	bounded   bool
	closed    bool
}

// Next returns the next batch in sequence order.
//
//   - a non-empty batch always means "these events, in this order";
//   - an empty batch means the committed range is drained, which a live reader
//     reports as an empty batch and a bounded reader reports as
//     serviceauth.ErrStreamExhausted;
//   - a cursor beyond the highest committed sequence can never be served, so it is
//     exhausted for both kinds of reader. That distinguishes a consumer whose
//     cursor is ahead of the stream (a real anomaly) from one that is simply up to
//     date.
func (reader *eventReader) Next(ctx context.Context) ([]*documentv1.DocumentEventEnvelope, error) {
	if reader == nil || reader.store == nil {
		return nil, errors.New("document-service: event reader is not configured")
	}
	if reader.closed {
		return nil, serviceauth.ErrStreamExhausted
	}

	events, err := reader.store.ReadEvents(ctx, reader.cursor, reader.batchSize)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		maxSequence, err := reader.store.MaxEventSequence(ctx)
		if err != nil {
			return nil, err
		}
		if reader.bounded || reader.cursor > maxSequence {
			return nil, serviceauth.ErrStreamExhausted
		}
		return nil, nil
	}

	batch := make([]*documentv1.DocumentEventEnvelope, 0, len(events))
	for _, event := range events {
		envelope := &documentv1.DocumentEventEnvelope{}
		if err := proto.Unmarshal(event.Payload, envelope); err != nil {
			return nil, fmt.Errorf("document-service: decode document event %d: %w", event.Sequence, err)
		}
		// The row's sequence is authoritative: the payload carries it too, and the
		// two must agree, so the column wins if they ever diverge.
		envelope.Sequence = event.Sequence
		batch = append(batch, envelope)
		reader.cursor = event.Sequence
	}
	return batch, nil
}

// Close releases the reader. The next call to Next reports the stream as
// exhausted instead of silently continuing from a half-closed cursor.
func (reader *eventReader) Close() {
	if reader == nil {
		return
	}
	reader.closed = true
}

// CapabilityCodec exposes the boundary codec used to mint resource capabilities.
// It is the same codec that validates inbound assertions, so a capability can only
// be minted by the component that already holds the boundary key.
func (service *Service) CapabilityCodec() *serviceauth.Codec {
	return service.codec
}
