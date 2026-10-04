package application

// The business entry points one by one. Each is a use case named exactly like the
// RPC it serves and delegates to the sibling file that owns the mechanics:
// event.go for writes, search.go for queries, rebuild.go and status.go for the
// operational calls. Nothing here returns Unimplemented, and nothing here decides
// record content.

import (
	"context"

	qqsearchv1 "packages/gen/qqsearch/v1"
)

// ApplyEvent applies one qqsource.v1 event produced by py-agent: idempotent by
// event id, order-tolerant per record and never destructive.
func (service *Service) ApplyEvent(ctx context.Context, request *qqsearchv1.IndexQQSourceEventRequest) (*qqsearchv1.IndexQQSourceEventResponse, error) {
	return service.applyEvent(ctx, request)
}

// SearchMessages queries raw QQ messages strictly inside the granted scope.
func (service *Service) SearchMessages(ctx context.Context, granted *qqsearchv1.QQChannelScope, request *qqsearchv1.SearchQQMessagesRequest) (*qqsearchv1.SearchQQMessagesResponse, error) {
	return service.searchMessages(ctx, granted, request)
}

// SearchFiles queries raw QQ files strictly inside the granted scope.
func (service *Service) SearchFiles(ctx context.Context, granted *qqsearchv1.QQChannelScope, request *qqsearchv1.SearchQQFilesRequest) (*qqsearchv1.SearchQQFilesResponse, error) {
	return service.searchFiles(ctx, granted, request)
}

// GetRecordState reports one raw record's lifecycle state, or exists=false.
//
// The granted channel scope comes from the capability the transport boundary
// already validated; a record outside it is reported exactly like a record that
// does not exist.
func (service *Service) GetRecordState(ctx context.Context, granted *qqsearchv1.QQChannelScope, request *qqsearchv1.GetQQRecordStateRequest) (*qqsearchv1.GetQQRecordStateResponse, error) {
	return service.getRecordState(ctx, granted, request)
}

// Rebuild replays locally stored applied events into both indexes.
func (service *Service) Rebuild(ctx context.Context, confirm bool) (*qqsearchv1.RebuildIndexResponse, error) {
	return service.rebuild(ctx, confirm)
}

// Status reports the two collection identities and applied progress.
func (service *Service) Status(ctx context.Context) (*qqsearchv1.GetIndexStatusResponse, error) {
	return service.status(ctx)
}
