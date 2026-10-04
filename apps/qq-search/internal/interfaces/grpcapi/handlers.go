package grpcapi

import (
	"context"

	qqsearchv1 "packages/gen/qqsearch/v1"
	"packages/serviceauth"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// IndexQQSourceEvent applies one qqsource.v1 event produced by py-agent. The
// caller already holds the qq-index-writer scope, checked by the boundary.
func (api *API) IndexQQSourceEvent(ctx context.Context, request *qqsearchv1.IndexQQSourceEventRequest) (*qqsearchv1.IndexQQSourceEventResponse, error) {
	return api.index.ApplyEvent(ctx, request)
}

// SearchQQMessages queries raw QQ messages inside the granted channel scope.
func (api *API) SearchQQMessages(ctx context.Context, request *qqsearchv1.SearchQQMessagesRequest) (*qqsearchv1.SearchQQMessagesResponse, error) {
	scope, err := api.channelScope(ctx, request.GetScope())
	if err != nil {
		return nil, err
	}
	return api.index.SearchMessages(ctx, scope, request)
}

// SearchQQFiles queries raw QQ files inside the granted channel scope.
func (api *API) SearchQQFiles(ctx context.Context, request *qqsearchv1.SearchQQFilesRequest) (*qqsearchv1.SearchQQFilesResponse, error) {
	scope, err := api.channelScope(ctx, request.GetScope())
	if err != nil {
		return nil, err
	}
	return api.index.SearchFiles(ctx, scope, request)
}

// GetQQRecordState reports whether one raw record is indexed and whether it has
// been recalled, inside the granted channel scope only.
//
// It resolves the channel capability exactly like the two search RPCs do: the
// granted scope is read from x-resource-capability and a request naming an
// identifier outside it is refused as a whole. Handing the granted scope to the
// business layer is what keeps a record outside it indistinguishable from a
// record that does not exist.
func (api *API) GetQQRecordState(ctx context.Context, request *qqsearchv1.GetQQRecordStateRequest) (*qqsearchv1.GetQQRecordStateResponse, error) {
	scope, err := api.channelScope(ctx, request.GetScope())
	if err != nil {
		return nil, err
	}
	return api.index.GetRecordState(ctx, scope, request)
}

// RebuildIndex replays locally applied events.
func (api *API) RebuildIndex(ctx context.Context, request *qqsearchv1.RebuildIndexRequest) (*qqsearchv1.RebuildIndexResponse, error) {
	return api.index.Rebuild(ctx, request.GetConfirm())
}

// GetIndexStatus reports the two collection identities and applied progress.
func (api *API) GetIndexStatus(ctx context.Context, request *qqsearchv1.GetIndexStatusRequest) (*qqsearchv1.GetIndexStatusResponse, error) {
	return api.index.Status(ctx)
}

// channelScope validates the caller's channel capability and checks that the
// requested scope is a subset of it. The returned scope is always the granted
// one, never the requested one: a request can narrow the query but never widen
// it, and an out-of-scope identifier denies the whole request instead of being
// trimmed.
func (api *API) channelScope(ctx context.Context, requested *qqsearchv1.QQChannelScope) (*qqsearchv1.QQChannelScope, error) {
	if _, ok := serviceauth.PrincipalFrom(ctx); !ok {
		return nil, status.Error(codes.Unauthenticated, "qq-search: no authenticated caller in context")
	}
	capability, err := api.boundary.CapabilityFromIncomingMetadata(ctx)
	if err != nil {
		return nil, serviceauth.GRPCStatus(err)
	}
	if err := serviceauth.Requires(capability.Claims.Scopes, serviceauth.ScopeQQSearcher); err != nil {
		return nil, serviceauth.GRPCStatus(err)
	}
	if err := CheckChannelInclusion(&capability.Claims, requested); err != nil {
		return nil, serviceauth.GRPCStatus(err)
	}
	return &qqsearchv1.QQChannelScope{
		BotIds:           serviceauth.CanonicalIDs(capability.Claims.BotIDs),
		ConversationIds:  serviceauth.CanonicalIDs(capability.Claims.ConversationIDs),
		ExternalGroupIds: serviceauth.CanonicalIDs(capability.Claims.ExternalGroupIDs),
	}, nil
}

// CheckChannelInclusion applies the "only narrow" rule to a QQ query. It is
// exported so the rule is testable without standing up a server.
func CheckChannelInclusion(claims *serviceauth.CapabilityClaims, requested *qqsearchv1.QQChannelScope) error {
	if claims == nil {
		return serviceauth.ErrSubjectNotAllowed
	}
	if requested == nil {
		return nil
	}
	if !serviceauth.ContainsAll(claims.BotIDs, requested.GetBotIds()) {
		return serviceauth.ErrScopeNotGranted
	}
	if !serviceauth.ContainsAll(claims.ConversationIDs, requested.GetConversationIds()) {
		return serviceauth.ErrScopeNotGranted
	}
	if !serviceauth.ContainsAll(claims.ExternalGroupIDs, requested.GetExternalGroupIds()) {
		return serviceauth.ErrScopeNotGranted
	}
	return nil
}
