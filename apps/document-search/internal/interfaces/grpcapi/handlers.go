package grpcapi

import (
	"context"

	documentsearchv1 "packages/gen/documentsearch/v1"
	"packages/serviceauth"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// IndexDocumentEvent applies one document-service change event. The caller is the
// index consumer and already holds the document-index-writer scope, checked by
// the boundary interceptor.
func (api *API) IndexDocumentEvent(ctx context.Context, request *documentsearchv1.IndexDocumentEventRequest) (*documentsearchv1.IndexDocumentEventResponse, error) {
	return api.index.ApplyEvent(ctx, request)
}

// SearchDocuments answers a query inside the range the caller's capability
// granted. The requested range is included in the granted range or the whole
// request is denied; the service never trims it silently.
func (api *API) SearchDocuments(ctx context.Context, request *documentsearchv1.SearchDocumentsRequest) (*documentsearchv1.SearchDocumentsResponse, error) {
	principal, ok := serviceauth.PrincipalFrom(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "document-search: no authenticated caller in context")
	}
	capability, err := api.boundary.CapabilityFromIncomingMetadata(ctx)
	if err != nil {
		api.logger.Warn("document search rejected a query capability", "caller", string(principal.Caller), "reason", err.Error())
		return nil, serviceauth.GRPCStatus(err)
	}
	if err := serviceauth.Requires(capability.Claims.Scopes, serviceauth.ScopeDocumentSearcher); err != nil {
		return nil, serviceauth.GRPCStatus(err)
	}
	// A capability minted by one fact source cannot be replayed for another
	// subject: the range and the subject travel together.
	if capability.Claims.SubjectKey == "" {
		return nil, serviceauth.GRPCStatus(serviceauth.ErrSubjectNotAllowed)
	}
	return api.index.Search(ctx, principal, &capability.Claims, request)
}

// GetDocumentIndexState reports the applied state of one document.
func (api *API) GetDocumentIndexState(ctx context.Context, request *documentsearchv1.GetDocumentIndexStateRequest) (*documentsearchv1.GetDocumentIndexStateResponse, error) {
	return api.index.GetDocumentIndexState(ctx, request.GetDocumentId())
}

// RebuildIndex replays the locally stored applied events. It never fetches
// content from the document service.
func (api *API) RebuildIndex(ctx context.Context, request *documentsearchv1.RebuildIndexRequest) (*documentsearchv1.RebuildIndexResponse, error) {
	return api.index.Rebuild(ctx, request.GetConfirm())
}

// GetIndexStatus reports collection identity and cursor progress.
func (api *API) GetIndexStatus(ctx context.Context, request *documentsearchv1.GetIndexStatusRequest) (*documentsearchv1.GetIndexStatusResponse, error) {
	return api.index.Status(ctx)
}

// CheckScopeInclusion applies the "only narrow" rule to a search request. It is
// exported so the rule is testable without standing up a server.
func CheckScopeInclusion(claims *serviceauth.CapabilityClaims, requestedSpaces, requestedDocuments []string) error {
	if claims == nil {
		return serviceauth.ErrSubjectNotAllowed
	}
	grantedSpaces := append(append([]string{}, claims.AllowedSpaceIDs...), claims.PrivateSpaceIDs...)
	grantedSpaces = append(grantedSpaces, claims.OtherTeamSpaceIDs...)
	if claims.CurrentTeamSpaceID != "" {
		grantedSpaces = append(grantedSpaces, claims.CurrentTeamSpaceID)
	}
	if !serviceauth.ContainsAll(grantedSpaces, requestedSpaces) {
		return serviceauth.ErrScopeNotGranted
	}
	if !serviceauth.ContainsAll(claims.AllowedDocumentIDs, requestedDocuments) {
		return serviceauth.ErrScopeNotGranted
	}
	return nil
}

// effectiveScope is the authorization ceiling a capability grants. It is derived
// from the capability only, never from the request. The range a query actually
// filters on is resolved in internal/application: a request that names a space or
// a document replaces this envelope with exactly what it named, so the effective
// filter is never wider than what was asked for.
func effectiveScope(claims *serviceauth.CapabilityClaims) (spaces []string, documents []string, public bool) {
	if claims == nil {
		return nil, nil, false
	}
	spaces = append(spaces, claims.AllowedSpaceIDs...)
	spaces = append(spaces, claims.PrivateSpaceIDs...)
	spaces = append(spaces, claims.OtherTeamSpaceIDs...)
	if claims.CurrentTeamSpaceID != "" {
		spaces = append(spaces, claims.CurrentTeamSpaceID)
	}
	return serviceauth.CanonicalIDs(spaces), serviceauth.CanonicalIDs(claims.AllowedDocumentIDs), claims.AuthenticatedPublic
}
