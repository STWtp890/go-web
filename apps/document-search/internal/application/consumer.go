package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"document-search/internal/config"
	"document-search/internal/infrastructure/postgres"

	documentv1 "packages/gen/document/v1"
	documentsearchv1 "packages/gen/documentsearch/v1"
	"packages/serviceauth"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// The consumer is the only component that owns a document-service client. It is
// deliberately absent from the query path: internal/architecture asserts that no
// file reachable from Search imports a source client, so a query can never grow
// a synchronous call back into the fact source.
//
// Delivery is at-least-once. The cursor and the index change commit in one
// transaction, so a crash can only cause redelivery; duplicates are absorbed by
// the event_id key and by the revision fences.

// CursorStream is the consumer stream this service owns by default.
const CursorStream = "document-events"

// assertionTTL matches the capability lifetime published in the credential
// contract. A fresh assertion is minted for every connection attempt, so a
// long-lived follow stream is never carried by an expired credential.
const assertionTTL = 2 * time.Minute

// defaultReconnectBackoff is used when the configured value is unusable.
const defaultReconnectBackoff = 2 * time.Second

// ConsumerDependencies is what the event consumer needs from the composition
// root.
type ConsumerDependencies struct {
	Config config.Config
	Pool   *postgres.Pool
	Logger *slog.Logger
	// VectorIndex writes the vector half of each applied event inside the same
	// transaction window as the SQL projection. It is nil when the vector flow
	// is disabled.
	VectorIndex VectorIndex
	// Stream names the cursor row this consumer owns. Empty means the
	// contract's document-events stream; tests use a private stream so they
	// never disturb a running service.
	Stream string
}

// Consumer follows the document service change Outbox and applies each event to
// the local index. It owns the durable cursor: the cursor and the applied
// document state commit in the same transaction, so a crash can only cause
// redelivery, never a skipped event.
type Consumer struct {
	cfg     config.Config
	pool    *postgres.Pool
	logger  *slog.Logger
	vectors VectorIndex
	stream  string
}

// NewConsumer assembles the consumer.
func NewConsumer(dependencies ConsumerDependencies) (*Consumer, error) {
	if dependencies.Pool == nil {
		return nil, errors.New("document-search consumer: database pool is required")
	}
	logger := dependencies.Logger
	if logger == nil {
		logger = slog.Default()
	}
	stream := strings.TrimSpace(dependencies.Stream)
	if stream == "" {
		stream = CursorStream
	}
	return &Consumer{
		cfg:     dependencies.Config,
		pool:    dependencies.Pool,
		logger:  logger,
		vectors: dependencies.VectorIndex,
		stream:  stream,
	}, nil
}

// Stream reports the cursor row this consumer advances.
func (consumer *Consumer) Stream() string {
	if consumer == nil || consumer.stream == "" {
		return CursorStream
	}
	return consumer.stream
}

// Run follows the source Outbox until the context is cancelled.
//
// A source failure is never fatal and never silent: the loop logs the reason,
// waits for the configured backoff and reconnects from the stored cursor. It
// only returns when the context ends or when the local configuration is
// unusable, because stopping while the process still reports ready is exactly
// the "silently stopped consuming" failure this design must not have.
func (consumer *Consumer) Run(ctx context.Context) error {
	source, err := consumer.cfg.SourceConfig()
	if err != nil {
		return fmt.Errorf("document-search consumer: %w", err)
	}
	if source.Disabled {
		consumer.logger.Info("document event consumption is disabled", "stream", consumer.Stream())
		<-ctx.Done()
		return ctx.Err()
	}
	sourceKey, err := consumer.cfg.SourceKey()
	if err != nil {
		return fmt.Errorf("document-search consumer: %w", err)
	}
	codec, err := serviceauth.NewCodec(sourceKey)
	if err != nil {
		return fmt.Errorf("document-search consumer: %w", err)
	}
	backoff := defaultReconnectBackoff
	if configured, err := time.ParseDuration(source.ReconnectBackoff); err == nil && configured > 0 {
		backoff = configured
	}
	connection, err := grpc.NewClient(source.Endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("document-search consumer: dial %s: %w", source.Endpoint, err)
	}
	defer func() { _ = connection.Close() }()
	client := documentv1.NewDocumentServiceClient(connection)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		streamErr := consumer.consume(ctx, client, codec, source)
		if streamErr == nil {
			// The source closed a finished (non-following) stream. Reconnect from
			// the stored cursor instead of assuming there is nothing left, after
			// the same backoff so a source that closes immediately cannot turn
			// this loop into a spin.
			consumer.logger.Debug("document event stream ended, reconnecting", "stream", consumer.Stream())
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		consumer.logger.Warn("document event stream interrupted, retrying",
			"endpoint", source.Endpoint,
			"stream", consumer.Stream(),
			"code", status.Code(streamErr).String(),
			"retryable", retryableSourceCode(streamErr),
			"error", streamErr.Error(),
			"backoff", backoff.String(),
		)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
}

// consume follows one connection until it fails or the context ends. It re-reads
// the durable cursor every time, so a reconnect resumes exactly where the last
// committed transaction stopped.
func (consumer *Consumer) consume(
	ctx context.Context,
	client documentv1.DocumentServiceClient,
	codec *serviceauth.Codec,
	source config.SourceConfig,
) error {
	cursor, err := readCursor(ctx, consumer.pool.Pgx(), consumer.Stream())
	if err != nil {
		return err
	}
	assertion, err := mintSourceAssertion(codec, source)
	if err != nil {
		return err
	}
	// The stream runs on its own cancellable context so the configured request
	// timeout can bound how long a silent source may hold the consumer before the
	// connection is dropped and retried. A peer that accepts the connection and
	// then answers nothing must not look healthy forever.
	streamContext, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	var firstMessageTimeout *time.Timer
	if timeout, err := time.ParseDuration(source.RequestTimeout); err == nil && timeout > 0 {
		firstMessageTimeout = time.AfterFunc(timeout, cancelStream)
		defer firstMessageTimeout.Stop()
	}
	streamContext = metadata.AppendToOutgoingContext(streamContext,
		serviceauth.HeaderAuthorization, serviceauth.AuthorizationHeader(assertion))
	stream, err := client.ListDocumentEvents(streamContext, &documentv1.ListDocumentEventsRequest{
		AfterSequence: cursor,
		BatchSize:     int32(source.BatchSize),
		Follow:        true,
	})
	if err != nil {
		return fmt.Errorf("document-search consumer: open the document event stream: %w", err)
	}
	consumer.logger.Info("following document events",
		"stream", consumer.Stream(),
		"endpoint", source.Endpoint,
		"after_sequence", cursor,
	)
	progress := cursor
	awaitingFirstMessage := true
	for {
		envelope, err := stream.Recv()
		if awaitingFirstMessage {
			awaitingFirstMessage = false
			if firstMessageTimeout != nil {
				firstMessageTimeout.Stop()
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("document-search consumer: receive a document event: %w", err)
		}
		request, err := requestFromEnvelope(envelope)
		if err != nil {
			// The event cannot be interpreted, so it cannot be applied and must
			// not be skipped either. Reconnecting re-reads it; the operator sees
			// the reason in the log.
			return err
		}
		outcome, err := applyEventTx(ctx, consumer.cfg, consumer.pool, consumer.vectors, request, applyOptions{
			recordEvent:  true,
			cursorStream: consumer.Stream(),
		})
		if err != nil {
			return err
		}
		progress = advanceCursorValue(progress, request.GetSequence())
		consumer.logger.Debug("document event consumed",
			"stream", consumer.Stream(),
			"sequence", request.GetSequence(),
			"cursor", progress,
			"event_id", request.GetEventId(),
			"document_id", request.GetDocumentId(),
			"kind", request.GetKind(),
			"applied", outcome.Applied,
			"reason", outcome.Reason,
		)
	}
}

// mintSourceAssertion signs the service assertion presented to document-service.
// The caller identity is fixed to document-service, which is the identity the
// document service registers for its own event reader; the configured caller
// identity is carried as the audit actor instead of being trusted as a claim.
func mintSourceAssertion(codec *serviceauth.Codec, source config.SourceConfig) (string, error) {
	actor := strings.TrimSpace(source.CallerIdentity)
	if actor == "" {
		actor = "document-search"
	}
	now := time.Now().UTC()
	assertion, err := codec.SealAssertion(serviceauth.AssertionClaims{
		Caller:    serviceauth.CallerDocumentService,
		Audience:  serviceauth.Audience(source.Audience),
		Scopes:    []string{source.Scope},
		Actor:     actor,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(assertionTTL).Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("document-search consumer: sign the source assertion: %w", err)
	}
	return assertion, nil
}

// requestFromEnvelope converts the source contract into the index contract.
// The mapping is explicit rather than reflective so an unknown enum value is
// reported instead of silently becoming "upsert".
func requestFromEnvelope(envelope *documentv1.DocumentEventEnvelope) (*documentsearchv1.IndexDocumentEventRequest, error) {
	if envelope == nil {
		return nil, fmt.Errorf("%w: the source delivered an empty envelope", ErrSourceProtocol)
	}
	kind, err := eventKindName(envelope.GetKind())
	if err != nil {
		return nil, err
	}
	return &documentsearchv1.IndexDocumentEventRequest{
		EventId:             envelope.GetEventId(),
		Sequence:            envelope.GetSequence(),
		Kind:                kind,
		DocumentId:          envelope.GetDocumentId(),
		VersionId:           envelope.GetVersionId(),
		AggregateRevision:   envelope.GetAggregateRevision(),
		ActivationRevision:  envelope.GetActivationRevision(),
		AccessRevision:      envelope.GetAccessRevision(),
		LifecycleRevision:   envelope.GetLifecycleRevision(),
		LifecycleStatus:     lifecycleStatusName(envelope.GetLifecycleStatus()),
		PublicationStatus:   publicationStatusName(envelope.GetPublicationStatus()),
		OwnerSubjectKey:     envelope.GetOwnerSubjectKey(),
		OwnerSpaceId:        envelope.GetOwnerSpaceId(),
		AuthenticatedPublic: envelope.GetAuthenticatedPublic(),
		AllowedSpaceIds:     append([]string(nil), envelope.GetAllowedSpaceIds()...),
		Title:               envelope.GetTitle(),
		Summary:             envelope.GetSummary(),
		Content:             envelope.GetContent(),
		ContentFormat:       envelope.GetContentFormat(),
		ContentSha256:       envelope.GetContentSha256(),
		IndexProfile:        envelope.GetIndexProfile(),
		OccurredAt:          envelope.GetOccurredAt(),
		CreatedAt:           envelope.GetCreatedAt(),
	}, nil
}

// eventKindName maps the source enum onto the index event kind.
func eventKindName(kind documentv1.DocumentEventKind) (string, error) {
	switch kind {
	case documentv1.DocumentEventKind_DOCUMENT_EVENT_KIND_UPSERT:
		return EventKindUpsert, nil
	case documentv1.DocumentEventKind_DOCUMENT_EVENT_KIND_DELETE:
		return EventKindDelete, nil
	default:
		return "", fmt.Errorf("%w: unknown document event kind %s", ErrSourceProtocol, kind.String())
	}
}

// lifecycleStatusName maps the source lifecycle enum onto the stored status.
func lifecycleStatusName(status documentv1.LifecycleStatus) string {
	switch status {
	case documentv1.LifecycleStatus_LIFECYCLE_STATUS_ACTIVE:
		return "active"
	case documentv1.LifecycleStatus_LIFECYCLE_STATUS_ARCHIVED:
		return "archived"
	case documentv1.LifecycleStatus_LIFECYCLE_STATUS_TRASHED:
		return "trashed"
	default:
		// An unspecified lifecycle is not invented here: normalizeEvent applies
		// the documented default for an empty value.
		return ""
	}
}

// publicationStatusName maps the source publication enum onto the stored status.
func publicationStatusName(status documentv1.PublicationStatus) string {
	switch status {
	case documentv1.PublicationStatus_PUBLICATION_STATUS_DRAFT:
		return "draft"
	case documentv1.PublicationStatus_PUBLICATION_STATUS_PUBLISHED:
		return "published"
	case documentv1.PublicationStatus_PUBLICATION_STATUS_SUPERSEDED:
		return "superseded"
	case documentv1.PublicationStatus_PUBLICATION_STATUS_WITHDRAWN:
		return "withdrawn"
	default:
		// Unknown or unspecified stays empty, which normalizeEvent turns into
		// "draft": an event that does not state publication must not become
		// searchable.
		return ""
	}
}

// retryableSourceCode reports whether a source failure is worth retrying. Every
// current failure is retried, but the classification is kept explicit so the
// log line tells an operator whether the credential or the network is at fault.
func retryableSourceCode(err error) bool {
	switch status.Code(err) {
	case codes.Unauthenticated, codes.PermissionDenied, codes.Unavailable, codes.DeadlineExceeded, codes.Unknown:
		return true
	default:
		return false
	}
}
