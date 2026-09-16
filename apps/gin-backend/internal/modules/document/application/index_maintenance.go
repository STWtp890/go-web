package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	domain "gin-backend/internal/modules/document/domain"

	"github.com/google/uuid"
)

type IndexMaintenanceStore interface {
	domain.Repository
	HasUnfinishedIndexDelivery(context.Context, string) (bool, error)
	ListIndexDeliveryFailures(context.Context, int) ([]*domain.IndexDeliveryEvent, error)
	RequeueIndexDelivery(context.Context, string) error
	CreateIndexRebuildRun(context.Context, string) error
	FailIndexRebuildRun(context.Context, string, string) error
	RefreshIndexRebuildRun(context.Context, string) (*domain.IndexRebuildRun, error)
	GetIndexRebuildRun(context.Context, string) (*domain.IndexRebuildRun, error)
}

type IndexMaintenanceService struct {
	store          IndexMaintenanceStore
	client         DocumentIndexClient
	newID          func() string
	now            func() time.Time
	controlTimeout time.Duration
}

func NewIndexMaintenanceService(store IndexMaintenanceStore, client DocumentIndexClient) (*IndexMaintenanceService, error) {
	if store == nil || client == nil {
		return nil, errors.New("document index maintenance: store and client are required")
	}
	return &IndexMaintenanceService{
		store: store, client: client, newID: uuid.NewString, now: time.Now, controlTimeout: 5 * time.Second,
	}, nil
}

func (service *IndexMaintenanceService) Failures(ctx context.Context, limit int) ([]*domain.IndexDeliveryEvent, error) {
	return service.store.ListIndexDeliveryFailures(ctx, limit)
}

func (service *IndexMaintenanceService) Replay(ctx context.Context, eventID string) error {
	parsed, err := uuid.Parse(strings.TrimSpace(eventID))
	if err != nil {
		return fmt.Errorf("document index maintenance: event id must be a UUID: %w", err)
	}
	return service.store.RequeueIndexDelivery(ctx, parsed.String())
}

// Reconcile 比较 PostgreSQL 事实源和 mixin-search 中的活动版本状态，并把修复
// 记录为新的 Outbox 事件。已有 backlog 的文档会跳过，避免越过正常投递顺序。
func (service *IndexMaintenanceService) Reconcile(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	scanID := service.newID()
	documents, err := service.store.ListDocumentsForIndexing(ctx, "", limit)
	if err != nil {
		return 0, err
	}
	created := 0
	for index := range documents {
		document := &documents[index]
		unfinished, err := service.store.HasUnfinishedIndexDelivery(ctx, document.DocumentID)
		if err != nil {
			return created, err
		}
		if unfinished {
			continue
		}
		snapshot, err := loadIndexSnapshot(ctx, service.store, document)
		if err != nil {
			return created, err
		}
		kind := domain.IndexDeliveryDeleteDocument
		if document.LifecycleStatus == domain.LifecycleActive && snapshot.Version != nil {
			callCtx, cancel := context.WithTimeout(ctx, service.controlTimeout)
			state, stateErr := service.client.GetDocumentVersionState(callCtx, document.DocumentID, snapshot.Version.VersionID)
			cancel()
			if stateErr != nil {
				return created, fmt.Errorf("get mixin-search document state: %w", stateErr)
			}
			kind = domain.IndexDeliverySyncAccess
			if !indexStateMatches(snapshot, state) {
				kind = domain.IndexDeliverySyncDocument
			}
		}
		event := maintenanceEvent(service.newID(), "reconcile:"+scanID, domain.IndexDeliverySourceReconcile, nil, kind, snapshot, service.now().UTC())
		inserted, err := service.store.AppendIndexDeliveryEventIfAbsent(ctx, event)
		if err != nil {
			return created, err
		}
		if inserted {
			created++
		}
	}
	return created, nil
}

// PrepareRebuild 在一个可重复读事务中冻结文档控制快照并生成重建事件。
// 网络调用由正常 Worker 在事务提交后执行。
func (service *IndexMaintenanceService) PrepareRebuild(ctx context.Context, runID string) (*domain.IndexRebuildRun, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(runID))
	if err != nil {
		return nil, fmt.Errorf("document index maintenance: run id must be a UUID: %w", err)
	}
	runID = parsed.String()
	if err := service.store.CreateIndexRebuildRun(ctx, runID); err != nil {
		return nil, err
	}
	run, err := service.store.GetIndexRebuildRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.State != domain.IndexRebuildPreparing {
		return service.store.RefreshIndexRebuildRun(ctx, runID)
	}

	err = service.store.InRepeatableRead(ctx, func(repository domain.Repository) error {
		after := ""
		for {
			documents, listErr := repository.ListDocumentsForIndexing(ctx, after, 200)
			if listErr != nil {
				return listErr
			}
			if len(documents) == 0 {
				return nil
			}
			for index := range documents {
				document := &documents[index]
				snapshot, snapshotErr := loadIndexSnapshot(ctx, repository, document)
				if snapshotErr != nil {
					return snapshotErr
				}
				kind := domain.IndexDeliveryDeleteDocument
				if document.LifecycleStatus == domain.LifecycleActive && snapshot.Version != nil {
					kind = domain.IndexDeliverySyncDocument
				}
				event := maintenanceEvent(service.newID(), "rebuild:"+runID, domain.IndexDeliverySourceRebuild, &runID, kind, snapshot, service.now().UTC())
				if _, appendErr := repository.AppendIndexDeliveryEventIfAbsent(ctx, event); appendErr != nil {
					return appendErr
				}
				after = document.DocumentID
			}
		}
	})
	if err != nil {
		_ = service.store.FailIndexRebuildRun(ctx, runID, truncateDeliveryError(err.Error()))
		return nil, fmt.Errorf("prepare document index rebuild: %w", err)
	}
	return service.store.RefreshIndexRebuildRun(ctx, runID)
}

func (service *IndexMaintenanceService) RebuildStatus(ctx context.Context, runID string) (*domain.IndexRebuildRun, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(runID))
	if err != nil {
		return nil, fmt.Errorf("document index maintenance: run id must be a UUID: %w", err)
	}
	return service.store.RefreshIndexRebuildRun(ctx, parsed.String())
}

func loadIndexSnapshot(ctx context.Context, repository domain.Repository, document *domain.Document) (*domain.IndexDocumentSnapshot, error) {
	policy, err := repository.GetAccessPolicy(ctx, document.DocumentID)
	if err != nil {
		return nil, err
	}
	grantedSpaceIDs, err := repository.ListActiveGrantedSpaceIDs(ctx, document.DocumentID)
	if err != nil {
		return nil, err
	}
	var version *domain.DocumentVersion
	if document.ActiveVersionID != nil {
		version, err = repository.GetDocumentVersion(ctx, *document.ActiveVersionID)
		if err != nil {
			return nil, err
		}
	}
	return &domain.IndexDocumentSnapshot{Document: document, Version: version, Policy: policy, GrantedSpaceIDs: grantedSpaceIDs}, nil
}

func indexStateMatches(snapshot *domain.IndexDocumentSnapshot, state DocumentVersionIndexState) bool {
	return state.Exists && snapshot.Version != nil &&
		state.DocumentID == snapshot.Document.DocumentID && state.VersionID == snapshot.Version.VersionID &&
		state.Status == "DOCUMENT_INDEX_STATUS_ACTIVE" &&
		state.ActivationRevision == uint64(snapshot.Document.ActivationRevision) &&
		state.AccessRevision == uint64(snapshot.Document.AccessRevision) &&
		state.LifecycleRevision == uint64(snapshot.Document.LifecycleRevision) &&
		state.ContentSHA256 == snapshot.Version.ContentSHA256
}

func maintenanceEvent(
	eventID, keyPrefix string,
	source domain.IndexDeliverySource,
	runID *string,
	kind domain.IndexDeliveryKind,
	snapshot *domain.IndexDocumentSnapshot,
	now time.Time,
) *domain.IndexDeliveryEvent {
	document := snapshot.Document
	event := &domain.IndexDeliveryEvent{
		EventID: eventID, Source: source, SourceRunID: runID, DocumentID: document.DocumentID,
		AggregateRevision: document.AggregateRevision, Kind: kind, OwnerSpaceID: document.OwnerSpaceID,
		ActivationRevision: document.ActivationRevision, AccessRevision: document.AccessRevision,
		LifecycleRevision: document.LifecycleRevision, AuthenticatedPublic: snapshot.Policy.AuthenticatedPublic,
		GrantedSpaceIDs: append([]string{}, snapshot.GrantedSpaceIDs...), IndexProfile: domain.DefaultIndexProfile,
		State: domain.IndexDeliveryPending, AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if kind == domain.IndexDeliverySyncDocument && snapshot.Version != nil {
		event.VersionID = stringPointer(snapshot.Version.VersionID)
		event.ContentSHA256 = snapshot.Version.ContentSHA256
	}
	event.DedupeKey = fmt.Sprintf("%s:%s:%d:%s", keyPrefix, document.DocumentID, document.AggregateRevision, kind)
	return event
}
