package grpcapi

import (
	"context"
	"errors"
	"time"

	documentv1 "packages/gen/document/v1"
	"packages/serviceauth"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The methods below adapt the authenticated transport call to the business
// boundary. They perform no business validation: every rule (who may act, which
// revision is current, whether a space is a team space) belongs to the
// application layer, and duplicating it here would let the two copies diverge.

// CreateDocument creates a formal document owned by the authenticated subject.
func (api *API) CreateDocument(ctx context.Context, request *documentv1.CreateDocumentRequest) (*documentv1.CreateDocumentResponse, error) {
	principal, err := api.principal(ctx)
	if err != nil {
		return nil, err
	}
	return api.documents.CreateDocument(ctx, principal, request)
}

// UpdateDraft appends a draft version to an existing document.
func (api *API) UpdateDraft(ctx context.Context, request *documentv1.UpdateDraftRequest) (*documentv1.UpdateDraftResponse, error) {
	principal, err := api.principal(ctx)
	if err != nil {
		return nil, err
	}
	return api.documents.UpdateDraft(ctx, principal, request)
}

// SaveDocument edits and saves a document in one transaction.
func (api *API) SaveDocument(ctx context.Context, request *documentv1.SaveDocumentRequest) (*documentv1.SaveDocumentResponse, error) {
	principal, err := api.principal(ctx)
	if err != nil {
		return nil, err
	}
	return api.documents.SaveDocument(ctx, principal, request)
}

// PublishDocument makes a version visible to the formal document index.
func (api *API) PublishDocument(ctx context.Context, request *documentv1.PublishDocumentRequest) (*documentv1.PublishDocumentResponse, error) {
	principal, err := api.principal(ctx)
	if err != nil {
		return nil, err
	}
	return api.documents.PublishDocument(ctx, principal, request)
}

// WithdrawVersion retires a published version without deleting its content.
func (api *API) WithdrawVersion(ctx context.Context, request *documentv1.WithdrawVersionRequest) (*documentv1.WithdrawVersionResponse, error) {
	principal, err := api.principal(ctx)
	if err != nil {
		return nil, err
	}
	return api.documents.WithdrawVersion(ctx, principal, request)
}

// ArchiveDocument moves a document out of the searchable set while keeping it.
func (api *API) ArchiveDocument(ctx context.Context, request *documentv1.ArchiveDocumentRequest) (*documentv1.ArchiveDocumentResponse, error) {
	principal, err := api.principal(ctx)
	if err != nil {
		return nil, err
	}
	return api.documents.ArchiveDocument(ctx, principal, request)
}

// TrashDocument removes a document from the index.
func (api *API) TrashDocument(ctx context.Context, request *documentv1.TrashDocumentRequest) (*documentv1.TrashDocumentResponse, error) {
	principal, err := api.principal(ctx)
	if err != nil {
		return nil, err
	}
	return api.documents.TrashDocument(ctx, principal, request)
}

// GetDocument reads one document's detail, subject to a resource decision.
func (api *API) GetDocument(ctx context.Context, request *documentv1.GetDocumentRequest) (*documentv1.GetDocumentResponse, error) {
	principal, err := api.principal(ctx)
	if err != nil {
		return nil, err
	}
	return api.documents.GetDocument(ctx, principal, request)
}

// ListDocuments lists documents the authenticated subject may see.
func (api *API) ListDocuments(ctx context.Context, request *documentv1.ListDocumentsRequest) (*documentv1.ListDocumentsResponse, error) {
	principal, err := api.principal(ctx)
	if err != nil {
		return nil, err
	}
	return api.documents.ListDocuments(ctx, principal, request)
}

// CreateTeamSpace creates a team space and its owner membership in one
// transaction.
func (api *API) CreateTeamSpace(ctx context.Context, request *documentv1.CreateTeamSpaceRequest) (*documentv1.CreateTeamSpaceResponse, error) {
	principal, err := api.principal(ctx)
	if err != nil {
		return nil, err
	}
	return api.documents.CreateTeamSpace(ctx, principal, request)
}

// GetSpace reads a space with its members and group bindings.
func (api *API) GetSpace(ctx context.Context, request *documentv1.GetSpaceRequest) (*documentv1.GetSpaceResponse, error) {
	principal, err := api.principal(ctx)
	if err != nil {
		return nil, err
	}
	return api.documents.GetSpace(ctx, principal, request)
}

// ListSubjects reads the document service's own access subject registry.
func (api *API) ListSubjects(ctx context.Context, request *documentv1.ListSubjectsRequest) (*documentv1.ListSubjectsResponse, error) {
	principal, err := api.principal(ctx)
	if err != nil {
		return nil, err
	}
	return api.documents.ListSubjects(ctx, principal, request)
}

// GrantSpaceMember adds or restores an active space membership. Joining a QQ
// group never calls this: membership is an explicit resource-side grant.
func (api *API) GrantSpaceMember(ctx context.Context, request *documentv1.GrantSpaceMemberRequest) (*documentv1.GrantSpaceMemberResponse, error) {
	principal, err := api.principal(ctx)
	if err != nil {
		return nil, err
	}
	return api.documents.GrantSpaceMember(ctx, principal, request)
}

// RevokeSpaceMember closes an active space membership.
func (api *API) RevokeSpaceMember(ctx context.Context, request *documentv1.RevokeSpaceMemberRequest) (*documentv1.RevokeSpaceMemberResponse, error) {
	principal, err := api.principal(ctx)
	if err != nil {
		return nil, err
	}
	return api.documents.RevokeSpaceMember(ctx, principal, request)
}

// BindGroupSpace binds a QQ group to a team space.
func (api *API) BindGroupSpace(ctx context.Context, request *documentv1.BindGroupSpaceRequest) (*documentv1.BindGroupSpaceResponse, error) {
	principal, err := api.principal(ctx)
	if err != nil {
		return nil, err
	}
	return api.documents.BindGroupSpace(ctx, principal, request)
}

// RevokeGroupSpace closes an active QQ group to space binding.
func (api *API) RevokeGroupSpace(ctx context.Context, request *documentv1.RevokeGroupSpaceRequest) (*documentv1.RevokeGroupSpaceResponse, error) {
	principal, err := api.principal(ctx)
	if err != nil {
		return nil, err
	}
	return api.documents.RevokeGroupSpace(ctx, principal, request)
}

// ResolveAccessScope answers the resource half of the scope question.
func (api *API) ResolveAccessScope(ctx context.Context, request *documentv1.ResolveAccessScopeRequest) (*documentv1.ResolveAccessScopeResponse, error) {
	principal, err := api.principal(ctx)
	if err != nil {
		return nil, err
	}
	return api.documents.ResolveAccessScope(ctx, principal, request)
}

// IssueSearchCapability mints the resource capability a search service validates.
// The decision, the range and the denial are all made by the business layer; this
// adapter only carries the authenticated caller across.
func (api *API) IssueSearchCapability(ctx context.Context, request *documentv1.IssueSearchCapabilityRequest) (*documentv1.IssueSearchCapabilityResponse, error) {
	principal, err := api.principal(ctx)
	if err != nil {
		return nil, err
	}
	return api.documents.IssueSearchCapability(ctx, principal, request)
}

// ListDocumentEvents streams the Outbox. The stream is resumable by sequence and
// at-least-once: consumers dedupe on event id.
func (api *API) ListDocumentEvents(request *documentv1.ListDocumentEventsRequest, stream documentv1.DocumentService_ListDocumentEventsServer) error {
	ctx := stream.Context()
	principal, err := api.principal(ctx)
	if err != nil {
		return err
	}
	batchSize := int(request.GetBatchSize())
	if batchSize <= 0 {
		batchSize = 100
	}
	if maxBatch := api.cfg.Outbox.MaxBatchSize; maxBatch > 0 && batchSize > maxBatch {
		batchSize = maxBatch
	}
	reader, err := api.documents.ListDocumentEvents(ctx, principal, request.GetAfterSequence(), batchSize)
	if err != nil {
		return err
	}
	defer reader.Close()

	pollInterval := 500 * time.Millisecond
	if interval, err := time.ParseDuration(api.cfg.Outbox.PollInterval); err == nil && interval > 0 {
		pollInterval = interval
	}
	for {
		batch, err := reader.Next(ctx)
		if err != nil {
			if isStreamExhausted(err) {
				return nil
			}
			return err
		}
		for _, event := range batch {
			if err := stream.Send(event); err != nil {
				return err
			}
		}
		if len(batch) > 0 {
			continue
		}
		if !request.GetFollow() {
			return nil
		}
		// Following a live stream: wait for the next commit instead of spinning.
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(pollInterval):
		}
	}
}

func (api *API) principal(ctx context.Context) (*serviceauth.Principal, error) {
	principal, ok := serviceauth.PrincipalFrom(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "document-service: no authenticated caller in context")
	}
	return principal, nil
}

// isStreamExhausted reports whether a bounded Outbox read reached the end of the
// available range, which ends a non-following stream cleanly.
func isStreamExhausted(err error) bool {
	return errors.Is(err, serviceauth.ErrStreamExhausted)
}
