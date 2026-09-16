package application

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strings"
	"sync"
	"time"

	domain "gin-backend/internal/modules/document/domain"
)

var ErrIndexDeliveryLeaseLost = errors.New("document index delivery: lease lost")

type IndexDeliveryStore interface {
	ClaimIndexDeliveryEvents(context.Context, string, int, time.Duration) ([]*domain.IndexDeliveryEvent, error)
	RenewIndexDeliveryLease(context.Context, string, string, string, time.Duration) error
	MarkIndexDeliverySucceeded(context.Context, string, string, string) error
	RescheduleIndexDelivery(context.Context, string, string, string, time.Time, string, string) error
	MarkIndexDeliveryDeadLetter(context.Context, string, string, string, string, string) error
	GetDocumentVersion(context.Context, string) (*domain.DocumentVersion, error)
}

type IndexDocumentVersionInput = domain.IndexDocumentVersionInput
type UpdateDocumentAccessInput = domain.UpdateDocumentAccessInput
type ActivateDocumentVersionInput = domain.ActivateDocumentVersionInput
type DeleteDocumentVersionInput = domain.DeleteDocumentVersionInput
type DeleteDocumentInput = domain.DeleteDocumentInput
type DocumentVersionIndexState = domain.DocumentVersionIndexState
type DocumentIndexClient = domain.DocumentIndexClient
type RemoteIndexError = domain.RemoteIndexError

type IndexWorkerConfig struct {
	BatchSize      int
	Concurrency    int
	PollInterval   time.Duration
	LeaseDuration  time.Duration
	IndexTimeout   time.Duration
	ControlTimeout time.Duration
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
}

func DefaultIndexWorkerConfig() IndexWorkerConfig {
	return IndexWorkerConfig{
		BatchSize: 32, Concurrency: 8, PollInterval: 500 * time.Millisecond,
		LeaseDuration: 2 * time.Minute, IndexTimeout: 30 * time.Second,
		ControlTimeout: 5 * time.Second, BaseBackoff: time.Second, MaxBackoff: 5 * time.Minute,
	}
}

type IndexWorker struct {
	store    IndexDeliveryStore
	client   DocumentIndexClient
	workerID string
	config   IndexWorkerConfig
	now      func() time.Time
}

func NewIndexWorker(store IndexDeliveryStore, client DocumentIndexClient, workerID string, config IndexWorkerConfig) (*IndexWorker, error) {
	if store == nil || client == nil {
		return nil, errors.New("document index worker: store and client are required")
	}
	if strings.TrimSpace(workerID) == "" {
		return nil, errors.New("document index worker: worker id is required")
	}
	defaults := DefaultIndexWorkerConfig()
	if config.BatchSize <= 0 {
		config.BatchSize = defaults.BatchSize
	}
	if config.Concurrency <= 0 {
		config.Concurrency = defaults.Concurrency
	}
	if config.PollInterval <= 0 {
		config.PollInterval = defaults.PollInterval
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = defaults.LeaseDuration
	}
	if config.IndexTimeout <= 0 {
		config.IndexTimeout = defaults.IndexTimeout
	}
	if config.ControlTimeout <= 0 {
		config.ControlTimeout = defaults.ControlTimeout
	}
	if config.BaseBackoff <= 0 {
		config.BaseBackoff = defaults.BaseBackoff
	}
	if config.MaxBackoff <= 0 {
		config.MaxBackoff = defaults.MaxBackoff
	}
	return &IndexWorker{store: store, client: client, workerID: workerID, config: config, now: time.Now}, nil
}

func (worker *IndexWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(worker.config.PollInterval)
	defer ticker.Stop()
	for {
		if err := worker.RunOnce(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (worker *IndexWorker) RunOnce(ctx context.Context) error {
	events, err := worker.store.ClaimIndexDeliveryEvents(ctx, worker.workerID, worker.config.BatchSize, worker.config.LeaseDuration)
	if err != nil {
		return fmt.Errorf("claim document index deliveries: %w", err)
	}
	semaphore := make(chan struct{}, worker.config.Concurrency)
	var wait sync.WaitGroup
	for _, event := range events {
		wait.Go(func() {
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			worker.process(ctx, event)
		})
	}
	wait.Wait()
	return nil
}

func (worker *IndexWorker) process(ctx context.Context, event *domain.IndexDeliveryEvent) {
	if event.LeaseToken == nil {
		slog.Error("索引事件缺少租约令牌", slog.String("event_id", event.EventID))
		return
	}
	err := worker.deliver(ctx, event)
	if err == nil {
		if markErr := worker.store.MarkIndexDeliverySucceeded(ctx, event.EventID, worker.workerID, *event.LeaseToken); markErr != nil {
			slog.Error("标记索引事件成功失败", slog.String("event_id", event.EventID), slog.String("error", markErr.Error()))
			return
		}
		slog.Info("索引事件投递成功",
			slog.String("event_id", event.EventID), slog.String("document_id", event.DocumentID),
			slog.String("event_kind", string(event.Kind)), slog.Int64("aggregate_revision", event.AggregateRevision),
			slog.Int("attempt", event.AttemptCount), slog.Int64("delivery_latency_ms", worker.now().UTC().Sub(event.CreatedAt).Milliseconds()))
		return
	}

	code, retryable := classifyIndexDeliveryError(err)
	message := truncateDeliveryError(err.Error())
	if retryable {
		next := worker.now().UTC().Add(worker.retryDelay(event))
		if retryErr := worker.store.RescheduleIndexDelivery(ctx, event.EventID, worker.workerID, *event.LeaseToken, next, code, message); retryErr != nil {
			slog.Error("重排索引事件失败", slog.String("event_id", event.EventID), slog.String("error", retryErr.Error()))
			return
		}
		slog.Warn("索引事件等待重试",
			slog.String("event_id", event.EventID), slog.String("document_id", event.DocumentID),
			slog.String("event_kind", string(event.Kind)), slog.Int64("aggregate_revision", event.AggregateRevision),
			slog.Int("attempt", event.AttemptCount), slog.String("grpc_code", code),
			slog.Time("next_attempt_at", next), slog.String("error", message))
		return
	}
	if deadErr := worker.store.MarkIndexDeliveryDeadLetter(ctx, event.EventID, worker.workerID, *event.LeaseToken, code, message); deadErr != nil {
		slog.Error("标记索引事件死信失败", slog.String("event_id", event.EventID), slog.String("error", deadErr.Error()))
		return
	}
	slog.Error("索引事件进入死信",
		slog.String("event_id", event.EventID), slog.String("document_id", event.DocumentID),
		slog.String("event_kind", string(event.Kind)), slog.Int64("aggregate_revision", event.AggregateRevision),
		slog.Int("attempt", event.AttemptCount), slog.String("grpc_code", code), slog.String("error", message))
}

func (worker *IndexWorker) deliver(ctx context.Context, event *domain.IndexDeliveryEvent) error {
	switch event.Kind {
	case domain.IndexDeliverySyncDocument:
		return worker.deliverSyncDocument(ctx, event)
	case domain.IndexDeliverySyncAccess:
		input, err := accessInput(event)
		if err != nil {
			return err
		}
		return worker.callControl(ctx, event, func(callCtx context.Context) error {
			return worker.client.UpdateDocumentAccess(callCtx, input)
		})
	case domain.IndexDeliveryDeleteVersion:
		if event.VersionID == nil {
			return errors.New("delete version event has no version id")
		}
		lifecycle, err := revisionAsUint64("lifecycle", event.LifecycleRevision)
		if err != nil {
			return err
		}
		return worker.callControl(ctx, event, func(callCtx context.Context) error {
			return worker.client.DeleteDocumentVersion(callCtx, DeleteDocumentVersionInput{
				OperationID: event.OperationID("delete-version"), DocumentID: event.DocumentID,
				VersionID: *event.VersionID, LifecycleRevision: lifecycle,
			})
		})
	case domain.IndexDeliveryDeleteDocument:
		lifecycle, err := revisionAsUint64("lifecycle", event.LifecycleRevision)
		if err != nil {
			return err
		}
		return worker.callControl(ctx, event, func(callCtx context.Context) error {
			return worker.client.DeleteDocument(callCtx, DeleteDocumentInput{
				OperationID: event.OperationID("delete-document"), DocumentID: event.DocumentID, LifecycleRevision: lifecycle,
			})
		})
	default:
		return fmt.Errorf("unsupported index delivery kind %q", event.Kind)
	}
}

func (worker *IndexWorker) deliverSyncDocument(ctx context.Context, event *domain.IndexDeliveryEvent) error {
	if event.VersionID == nil {
		return errors.New("sync document event has no version id")
	}
	version, err := worker.store.GetDocumentVersion(ctx, *event.VersionID)
	if err != nil {
		return fmt.Errorf("load immutable document version: %w", err)
	}
	if version.DocumentID != event.DocumentID || version.ContentSHA256 != event.ContentSHA256 {
		return errors.New("immutable document version does not match delivery snapshot")
	}
	lifecycle, err := revisionAsUint64("lifecycle", event.LifecycleRevision)
	if err != nil {
		return err
	}
	chunkSize, overlap, err := indexProfile(event.IndexProfile)
	if err != nil {
		return err
	}
	if err := worker.callIndex(ctx, event, func(callCtx context.Context) error {
		return worker.client.IndexDocumentVersion(callCtx, IndexDocumentVersionInput{
			OperationID: event.OperationID("index"), DocumentID: event.DocumentID, VersionID: version.VersionID,
			OwnerSpaceID: event.OwnerSpaceID, Filename: version.VersionID + "." + version.ContentFormat,
			Title: version.Title, Content: []byte(version.Content), ContentSHA256: version.ContentSHA256,
			ChunkSize: chunkSize, Overlap: overlap, SourceURI: "go-web://documents/" + event.DocumentID + "/versions/" + version.VersionID,
			Metadata:          map[string]string{"content_format": version.ContentFormat, "index_profile": event.IndexProfile},
			LifecycleRevision: lifecycle,
		})
	}); err != nil {
		return err
	}
	access, err := accessInput(event)
	if err != nil {
		return err
	}
	if err := worker.callControl(ctx, event, func(callCtx context.Context) error {
		return worker.client.UpdateDocumentAccess(callCtx, access)
	}); err != nil {
		return err
	}
	activation, err := revisionAsUint64("activation", event.ActivationRevision)
	if err != nil {
		return err
	}
	previous := ""
	if event.PreviousVersionID != nil {
		previous = *event.PreviousVersionID
	}
	return worker.callControl(ctx, event, func(callCtx context.Context) error {
		return worker.client.ActivateDocumentVersion(callCtx, ActivateDocumentVersionInput{
			OperationID: event.OperationID("activate"), DocumentID: event.DocumentID, VersionID: version.VersionID,
			ActivationRevision: activation, ExpectedPreviousVersionID: previous, LifecycleRevision: lifecycle,
		})
	})
}

func accessInput(event *domain.IndexDeliveryEvent) (UpdateDocumentAccessInput, error) {
	access, err := revisionAsUint64("access", event.AccessRevision)
	if err != nil {
		return UpdateDocumentAccessInput{}, err
	}
	lifecycle, err := revisionAsUint64("lifecycle", event.LifecycleRevision)
	if err != nil {
		return UpdateDocumentAccessInput{}, err
	}
	return UpdateDocumentAccessInput{
		OperationID: event.OperationID("access"), DocumentID: event.DocumentID,
		AccessRevision: access, LifecycleRevision: lifecycle, AuthenticatedPublic: event.AuthenticatedPublic,
		GrantedSpaceIDs: append([]string{}, event.GrantedSpaceIDs...),
	}, nil
}

func (worker *IndexWorker) callIndex(ctx context.Context, event *domain.IndexDeliveryEvent, call func(context.Context) error) error {
	return worker.call(ctx, event, worker.config.IndexTimeout, call)
}

func (worker *IndexWorker) callControl(ctx context.Context, event *domain.IndexDeliveryEvent, call func(context.Context) error) error {
	return worker.call(ctx, event, worker.config.ControlTimeout, call)
}

func (worker *IndexWorker) call(ctx context.Context, event *domain.IndexDeliveryEvent, timeout time.Duration, call func(context.Context) error) error {
	if err := worker.store.RenewIndexDeliveryLease(ctx, event.EventID, worker.workerID, *event.LeaseToken, worker.config.LeaseDuration); err != nil {
		return fmt.Errorf("renew delivery lease: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return call(callCtx)
}

func (worker *IndexWorker) retryDelay(event *domain.IndexDeliveryEvent) time.Duration {
	attempt := event.AttemptCount
	if attempt < 1 {
		attempt = 1
	}
	capDelay := worker.config.BaseBackoff
	for index := 1; index < attempt && capDelay < worker.config.MaxBackoff/2; index++ {
		capDelay *= 2
	}
	if capDelay > worker.config.MaxBackoff {
		capDelay = worker.config.MaxBackoff
	}
	hasher := fnv.New64a()
	_, _ = fmt.Fprintf(hasher, "%s:%d", event.EventID, attempt)
	return time.Duration(hasher.Sum64() % uint64(capDelay+1))
}

func classifyIndexDeliveryError(err error) (string, bool) {
	var remote *RemoteIndexError
	if errors.As(err, &remote) {
		return remote.Code, remote.Retryable
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "DeadlineExceeded", true
	}
	if errors.Is(err, context.Canceled) {
		return "Canceled", true
	}
	return "LocalPermanent", false
}

func revisionAsUint64(name string, revision int64) (uint64, error) {
	if revision < 0 {
		return 0, fmt.Errorf("%s revision must not be negative", name)
	}
	return uint64(revision), nil
}

func indexProfile(profile string) (int32, int32, error) {
	switch profile {
	case domain.DefaultIndexProfile:
		return 180, 20, nil
	default:
		return 0, 0, fmt.Errorf("unsupported index profile %q", profile)
	}
}

func truncateDeliveryError(message string) string {
	const limit = 2048
	if len(message) <= limit {
		return message
	}
	return message[:limit]
}
