package application

import (
	"context"
	"fmt"

	documentsearchv1 "packages/gen/documentsearch/v1"
	"packages/serviceauth"
)

// This file holds the five business entry points the transport boundary calls.
// They stay thin: validation, transaction and query logic live in the sibling
// files, so the same apply path serves the RPC, the consumer and the rebuild.

// ApplyEvent applies one document-service change event idempotently. The caller
// is the index writer (the event consumer), already authenticated by the
// boundary interceptor.
func (service *Service) ApplyEvent(ctx context.Context, request *documentsearchv1.IndexDocumentEventRequest) (*documentsearchv1.IndexDocumentEventResponse, error) {
	outcome, err := service.applyEvent(ctx, request, applyOptions{recordEvent: true})
	if err != nil {
		return nil, grpcError(err)
	}
	return &documentsearchv1.IndexDocumentEventResponse{
		Applied: outcome.Applied,
		Reason:  outcome.Reason,
		State:   outcome.State,
	}, nil
}

// applyEvent routes an apply through the single transactional apply path. The
// consumer additionally passes the cursor stream, which makes the cursor advance
// in the same transaction as the index change.
func (service *Service) applyEvent(ctx context.Context, request *documentsearchv1.IndexDocumentEventRequest, options applyOptions) (applyOutcome, error) {
	return applyEventTx(ctx, service.cfg, service.pool, service.vectors, request, options)
}

// Search answers a query strictly inside the capability's granted range. The
// capability was minted by the document service and validated offline by the
// boundary; this method re-applies the inclusion rule against the request and
// then queries only local index data.
func (service *Service) Search(ctx context.Context, principal *serviceauth.Principal, capability *serviceauth.CapabilityClaims, request *documentsearchv1.SearchDocumentsRequest) (*documentsearchv1.SearchDocumentsResponse, error) {
	return service.searchDocuments(ctx, capability, request)
}

// GetDocumentIndexState reports the applied state of one document. A document
// the service has never seen reports exists=false; a deleted one reports the
// deleted state rather than being indistinguishable from an unknown document.
func (service *Service) GetDocumentIndexState(ctx context.Context, documentID string) (*documentsearchv1.GetDocumentIndexStateResponse, error) {
	normalized, err := normalizeIdentifier(documentID)
	if err != nil {
		return nil, grpcError(fmt.Errorf("%w: document_id %q", ErrInvalidIdentifier, documentID))
	}
	if service.pool == nil || service.pool.Pgx() == nil {
		return nil, grpcError(fmt.Errorf("%w: database pool is not initialized", ErrDatabase))
	}
	state, err := loadIndexState(ctx, service.pool.Pgx(), normalized)
	if err != nil {
		return nil, grpcError(err)
	}
	if state == nil {
		return &documentsearchv1.GetDocumentIndexStateResponse{Exists: false}, nil
	}
	return &documentsearchv1.GetDocumentIndexStateResponse{Exists: true, State: state}, nil
}

// Rebuild replays locally stored applied events. It never fetches content from
// the document service, so it must be confirmed explicitly.
func (service *Service) Rebuild(ctx context.Context, confirm bool) (*documentsearchv1.RebuildIndexResponse, error) {
	if !confirm {
		return nil, grpcError(fmt.Errorf("%w: rebuilding replays locally stored events and removes nothing else, but it must be confirmed", ErrRebuildNotConfirmed))
	}
	response, err := service.rebuild(ctx)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}

// Status reports collection identity and cursor progress.
func (service *Service) Status(ctx context.Context) (*documentsearchv1.GetIndexStatusResponse, error) {
	response, err := service.indexStatus(ctx)
	if err != nil {
		return nil, grpcError(err)
	}
	return response, nil
}
