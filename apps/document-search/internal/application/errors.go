package application

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrInvalidInput is the base error for every condition the caller could have
// avoided. The specific sentinels below derive from it, so one errors.Is check
// classifies the whole family as INVALID_ARGUMENT.
var ErrInvalidInput = errors.New("document-search: invalid input")

var (
	// ErrUnsupportedEventKind rejects any event kind the index does not model.
	// Failing closed matters here: silently ignoring an unknown kind would drop a
	// document change without recording that it was dropped.
	ErrUnsupportedEventKind = fmt.Errorf("%w: kind must be upsert or delete", ErrInvalidInput)
	// ErrMissingDocumentID rejects an event that names no document.
	ErrMissingDocumentID = fmt.Errorf("%w: document_id is required", ErrInvalidInput)
	// ErrMissingEventID rejects an event without the idempotency key.
	ErrMissingEventID = fmt.Errorf("%w: event_id is required", ErrInvalidInput)
	// ErrMissingSequence rejects an event without its Outbox sequence, because
	// the applied event log is keyed by it.
	ErrMissingSequence = fmt.Errorf("%w: sequence must be a positive Outbox sequence", ErrInvalidInput)
	// ErrMissingVersionID rejects an upsert without the version it publishes.
	ErrMissingVersionID = fmt.Errorf("%w: version_id is required for an upsert", ErrInvalidInput)
	// ErrMissingOwnerSpace rejects an upsert without the owning space, which is
	// what the query scope is evaluated against.
	ErrMissingOwnerSpace = fmt.Errorf("%w: owner_space_id is required for an upsert", ErrInvalidInput)
	// ErrMissingQuery rejects an empty search query.
	ErrMissingQuery = fmt.Errorf("%w: query is required", ErrInvalidInput)
	// ErrInvalidIdentifier rejects a value that is not a UUID.
	ErrInvalidIdentifier = fmt.Errorf("%w: identifier must be a UUID", ErrInvalidInput)
)

// ErrRebuildNotConfirmed guards the destructive rebuild path.
var ErrRebuildNotConfirmed = errors.New("document-search: rebuild requires confirm=true")

// ErrScopeNotGranted reports a request that asks for more than the capability
// granted. The whole request is rejected; nothing is trimmed.
var ErrScopeNotGranted = errors.New("document-search: the requested scope is not a subset of the granted scope")

// ErrEventSequenceTaken reports an applied event log conflict: another event
// already owns this Outbox sequence.
var ErrEventSequenceTaken = errors.New("document-search: the event sequence already belongs to another event")

// ErrDatabase reports an unusable database. It is retryable, unlike the caller
// errors above.
var ErrDatabase = errors.New("document-search: database is unavailable")

// ErrSourceProtocol reports a document service event that the index cannot
// interpret. The consumer treats it as fatal for the current stream and retries
// rather than skipping the event.
var ErrSourceProtocol = errors.New("document-search: the document service event stream is not usable")

// grpcError maps a domain error onto the status codes the contract publishes.
// An already-formed status error is passed through so nested layers can add
// detail without changing the outcome.
func grpcError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	switch {
	case errors.Is(err, ErrInvalidInput):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ErrRebuildNotConfirmed):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, ErrScopeNotGranted):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, ErrEventSequenceTaken):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, ErrSourceProtocol):
		return status.Error(codes.Internal, err.Error())
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.Unavailable, err.Error())
	default:
		// Unknown failures are reported as UNAVAILABLE rather than INTERNAL: the
		// consumer keeps retrying instead of dying, and the caller learns the
		// service could not answer rather than that its input was wrong.
		return status.Error(codes.Unavailable, err.Error())
	}
}
