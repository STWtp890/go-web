package application

// This file answers the two query RPCs. It is the entire query path, and it
// contains no client of any kind: the service never calls py-agent while
// answering a query. The channel scope in the request was already validated
// against the capability py-agent issued; this layer only narrows it further and
// refuses to widen it.

import (
	"context"
	"fmt"
	"strings"

	qqsearchv1 "packages/gen/qqsearch/v1"
	"packages/serviceauth"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// channelScope is the resolved query range: the three identifier families
// py-agent's channel capability speaks in, after narrowing by the request.
type channelScope struct {
	botIDs           []string
	conversationIDs  []string
	externalGroupIDs []string
}

// empty reports whether the scope names nothing at all.
func (scope channelScope) empty() bool {
	return len(scope.botIDs) == 0 && len(scope.conversationIDs) == 0 && len(scope.externalGroupIDs) == 0
}

// resolveScope intersects the granted channel capability with the identifiers
// the caller asked about.
//
// The granted scope is the only authority. Two rules matter here:
//
//   - A request may only narrow. Anything the caller names must already be in
//     the grant, and a request naming something outside it is rejected as a
//     whole rather than trimmed, so the caller cannot probe identifiers it was
//     not granted.
//   - A request that names one conversation may not return another, even when
//     both are granted. That is why the requested families are intersected
//     instead of being ignored.
func resolveScope(granted, requested *qqsearchv1.QQChannelScope) (channelScope, error) {
	if granted == nil {
		return channelScope{}, fmt.Errorf("%w: no channel capability was granted", serviceauth.ErrScopeNotGranted)
	}
	scope := channelScope{
		botIDs:           serviceauth.CanonicalIDs(granted.GetBotIds()),
		conversationIDs:  serviceauth.CanonicalIDs(granted.GetConversationIds()),
		externalGroupIDs: serviceauth.CanonicalIDs(granted.GetExternalGroupIds()),
	}
	if requested == nil {
		return scope, nil
	}
	var err error
	if scope.botIDs, err = narrowFamily("bot_ids", scope.botIDs, requested.GetBotIds()); err != nil {
		return channelScope{}, err
	}
	if scope.conversationIDs, err = narrowFamily("conversation_ids", scope.conversationIDs, requested.GetConversationIds()); err != nil {
		return channelScope{}, err
	}
	if scope.externalGroupIDs, err = narrowFamily("external_group_ids", scope.externalGroupIDs, requested.GetExternalGroupIds()); err != nil {
		return channelScope{}, err
	}
	return scope, nil
}

// narrowFamily applies the inclusion rule to one identifier family. An empty
// request family means "everything I was granted in this family".
func narrowFamily(name string, granted, requested []string) ([]string, error) {
	if len(requested) == 0 {
		return granted, nil
	}
	canonical := serviceauth.CanonicalIDs(requested)
	if !serviceauth.ContainsAll(granted, canonical) {
		return nil, fmt.Errorf("%w: %s %v is not inside the granted channel scope %v",
			serviceauth.ErrScopeNotGranted, name, canonical, granted)
	}
	return canonical, nil
}

// searchMessages queries raw QQ messages strictly inside the granted scope.
func (service *Service) searchMessages(ctx context.Context, granted *qqsearchv1.QQChannelScope, request *qqsearchv1.SearchQQMessagesRequest) (*qqsearchv1.SearchQQMessagesResponse, error) {
	if request == nil {
		return nil, invalidArgument("request is required")
	}
	query := strings.TrimSpace(request.GetQuery())
	if query == "" {
		return nil, invalidArgument("query is required")
	}
	scope, err := resolveScope(granted, request.GetScope())
	if err != nil {
		return nil, serviceauth.GRPCStatus(err)
	}
	if scope.empty() {
		// An empty granted scope means "no conversations". It never means "all
		// conversations": silently widening an empty grant is exactly the failure
		// the channel-scope contract exists to prevent, so this returns zero hits
		// without touching the database. The same rule applies when the caller
		// narrows itself out of every family it was granted.
		return &qqsearchv1.SearchQQMessagesResponse{}, nil
	}
	if err := service.requirePool(); err != nil {
		return nil, err
	}
	limit := service.topK(request.GetTopK())
	hits, truncated, err := service.searchMessageHits(ctx, query, scope, limit)
	if err != nil {
		return nil, internalError("search messages: %v", err)
	}
	return &qqsearchv1.SearchQQMessagesResponse{Hits: hits, Truncated: truncated}, nil
}

// searchFiles queries raw QQ files strictly inside the granted scope, using the
// file model's own table and its own full-text plus trigram statement.
func (service *Service) searchFiles(ctx context.Context, granted *qqsearchv1.QQChannelScope, request *qqsearchv1.SearchQQFilesRequest) (*qqsearchv1.SearchQQFilesResponse, error) {
	if request == nil {
		return nil, invalidArgument("request is required")
	}
	query := strings.TrimSpace(request.GetQuery())
	if query == "" {
		return nil, invalidArgument("query is required")
	}
	scope, err := resolveScope(granted, request.GetScope())
	if err != nil {
		return nil, serviceauth.GRPCStatus(err)
	}
	if scope.empty() {
		// Same rule as SearchMessages: an empty scope is no conversations, never
		// every conversation.
		return &qqsearchv1.SearchQQFilesResponse{}, nil
	}
	if err := service.requirePool(); err != nil {
		return nil, err
	}
	limit := service.topK(request.GetTopK())
	hits, truncated, err := service.searchFileHits(ctx, query, scope, limit)
	if err != nil {
		return nil, internalError("search files: %v", err)
	}
	return &qqsearchv1.SearchQQFilesResponse{Hits: hits, Truncated: truncated}, nil
}

// topK applies the configured bound. A caller-supplied value can lower the
// result set but never raise it past max_top_k.
func (service *Service) topK(requested int32) int {
	index := service.cfg.Index
	limit := int(requested)
	if limit <= 0 {
		limit = index.DefaultTopK
	}
	if index.MaxTopK > 0 && limit > index.MaxTopK {
		limit = index.MaxTopK
	}
	if limit <= 0 {
		limit = 10
	}
	return limit
}

// getRecordState reports whether one raw record is indexed and whether it has
// been recalled. The kind selects the model: a message lookup reads
// qq_search.qq_messages, a file lookup reads qq_search.qq_files, and neither
// statement can see the other's rows.
//
// The lookup is scoped exactly like the two searches. The granted channel scope
// is the only authority, a request naming an identifier outside it is rejected
// as a whole, and the effective scope is applied inside each model's own
// statement. A record outside the scope is reported exactly like a record that
// does not exist (exists=false, no state), so this RPC cannot be used to probe
// whether another bot or conversation holds a given record id.
func (service *Service) getRecordState(ctx context.Context, granted *qqsearchv1.QQChannelScope, request *qqsearchv1.GetQQRecordStateRequest) (*qqsearchv1.GetQQRecordStateResponse, error) {
	if request == nil {
		return nil, invalidArgument("request is required")
	}
	kind, err := recordKindFromProto(request.GetKind())
	if err != nil {
		return nil, err
	}
	recordID := strings.TrimSpace(request.GetRecordId())
	if recordID == "" {
		return nil, invalidArgument("record_id is required")
	}
	if err := checkLength("record_id", recordID, maxRecordIDLen); err != nil {
		return nil, err
	}
	scope, err := resolveScope(granted, request.GetScope())
	if err != nil {
		return nil, serviceauth.GRPCStatus(err)
	}
	if scope.empty() {
		// An empty grant means "no conversations". It never means "all
		// conversations": a lookup that degraded to an unscoped read would turn
		// this RPC into the probe the channel scope exists to prevent. The same
		// rule applies when the caller narrows itself out of every family it was
		// granted.
		return &qqsearchv1.GetQQRecordStateResponse{Exists: false}, nil
	}
	if err := service.requirePool(); err != nil {
		return nil, err
	}
	state, err := service.recordStateInScope(ctx, service.pool.Pgx(), kind, recordID, scope)
	if err != nil {
		return nil, err
	}
	if state == nil {
		// Out-of-scope and nonexistent are the same outcome on purpose: the
		// response carries no field that could tell them apart.
		return &qqsearchv1.GetQQRecordStateResponse{Exists: false}, nil
	}
	return &qqsearchv1.GetQQRecordStateResponse{Exists: true, State: state}, nil
}

// recordStateInScope reads one record's reported state through the model that
// owns it, with the effective channel scope applied inside that model's own
// statement. It is the read-side counterpart of recordState: writes address a
// record by its id, while a caller-visible lookup must also prove the record is
// inside the scope it was granted.
func (service *Service) recordStateInScope(ctx context.Context, q querier, kind RecordKind, recordID string, scope channelScope) (*qqsearchv1.QQRecordState, error) {
	switch kind {
	case RecordKindMessage:
		state, found, err := loadMessageStateInScope(ctx, q, recordID, scope)
		if err != nil {
			return nil, internalError("read message state: %v", err)
		}
		if !found {
			return nil, nil
		}
		return state, nil
	case RecordKindFile:
		state, found, err := loadFileStateInScope(ctx, q, recordID, scope)
		if err != nil {
			return nil, internalError("read file state: %v", err)
		}
		if !found {
			return nil, nil
		}
		return state, nil
	default:
		return nil, internalError("unknown record kind %q", kind)
	}
}

// requirePool fails closed when the service was assembled without a database.
func (service *Service) requirePool() error {
	if service.pool == nil || service.pool.Pgx() == nil {
		return status.Error(codes.Internal, "qq-search: database pool is not available")
	}
	return nil
}
