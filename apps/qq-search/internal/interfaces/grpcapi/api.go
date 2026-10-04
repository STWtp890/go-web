// Package grpcapi is the QQ search transport boundary.
//
// It authenticates the caller, enforces the per-method scope policy, and applies
// the channel inclusion rule: a query may only address conversations inside the
// channel scope py-agent granted. Resource permission stays with
// document-service; this boundary decides nothing about spaces or documents.
package grpcapi

import (
	"context"
	"fmt"
	"log/slog"

	"qq-search/internal/application"
	"qq-search/internal/config"
	"qq-search/internal/infrastructure/postgres"

	qqsearchv1 "packages/gen/qqsearch/v1"
	"packages/serviceauth"

	"google.golang.org/grpc"
)

// methodScopes is the authorization policy. A method absent from this table is
// rejected, so a new RPC without a registered policy fails closed.
var methodScopes = serviceauth.BoundaryPolicy{
	"/qqsearch.v1.QQSearchService/IndexQQSourceEvent": {serviceauth.ScopeQQIndexWriter},
	"/qqsearch.v1.QQSearchService/RebuildIndex":       {serviceauth.ScopeQQIndexWriter},
	"/qqsearch.v1.QQSearchService/GetIndexStatus":     {serviceauth.ScopeQQIndexWriter},
	"/qqsearch.v1.QQSearchService/SearchQQMessages":   {serviceauth.ScopeQQSearcher},
	"/qqsearch.v1.QQSearchService/SearchQQFiles":      {serviceauth.ScopeQQSearcher},
	"/qqsearch.v1.QQSearchService/GetQQRecordState":   {serviceauth.ScopeQQSearcher},
}

// defaultAllowedCallers is the closed set of callers when configuration does not
// narrow it further. py-agent owns QQ raw facts and issues both the indexing
// identity and the channel scope capability.
var defaultAllowedCallers = []serviceauth.Caller{
	serviceauth.CallerPyAgent,
	serviceauth.CallerDocumentService,
}

// AnonymousProbeMethods are served without a credential.
var AnonymousProbeMethods = []string{
	"/grpc.health.v1.Health/Check",
	"/grpc.health.v1.Health/Watch",
}

// API assembles the transport boundary.
type API struct {
	qqsearchv1.UnimplementedQQSearchServiceServer

	cfg      config.Config
	boundary *serviceauth.Boundary
	index    Index
	logger   *slog.Logger
}

// Index is the business interface the transport layer calls.
type Index interface {
	ApplyEvent(ctx context.Context, request *qqsearchv1.IndexQQSourceEventRequest) (*qqsearchv1.IndexQQSourceEventResponse, error)
	SearchMessages(ctx context.Context, scope *qqsearchv1.QQChannelScope, request *qqsearchv1.SearchQQMessagesRequest) (*qqsearchv1.SearchQQMessagesResponse, error)
	SearchFiles(ctx context.Context, scope *qqsearchv1.QQChannelScope, request *qqsearchv1.SearchQQFilesRequest) (*qqsearchv1.SearchQQFilesResponse, error)
	GetRecordState(ctx context.Context, scope *qqsearchv1.QQChannelScope, request *qqsearchv1.GetQQRecordStateRequest) (*qqsearchv1.GetQQRecordStateResponse, error)
	Rebuild(ctx context.Context, confirm bool) (*qqsearchv1.RebuildIndexResponse, error)
	Status(ctx context.Context) (*qqsearchv1.GetIndexStatusResponse, error)
}

// New builds the transport boundary, failing closed when the boundary key or the
// caller allow-list is unusable.
func New(cfg config.Config, pool *postgres.Pool, boundaryKey []byte, logger *slog.Logger) (*API, error) {
	if logger == nil {
		logger = slog.Default()
	}
	codec, err := serviceauth.NewCodec(boundaryKey)
	if err != nil {
		return nil, err
	}
	allowed, err := allowedCallers(cfg)
	if err != nil {
		return nil, err
	}
	audience, err := cfg.Audience()
	if err != nil {
		return nil, err
	}
	authenticator, err := serviceauth.NewAuthenticator(codec, serviceauth.Audience(audience), allowed...)
	if err != nil {
		return nil, err
	}
	boundary, err := serviceauth.NewBoundary("qq-search", authenticator, methodScopes,
		serviceauth.WithAnonymous(AnonymousProbeMethods...),
	)
	if err != nil {
		return nil, err
	}
	business, err := application.New(application.Dependencies{
		Config: cfg,
		Pool:   pool,
		Codec:  codec,
	})
	if err != nil {
		return nil, err
	}
	return &API{cfg: cfg, boundary: boundary, index: business, logger: logger}, nil
}

func allowedCallers(cfg config.Config) ([]serviceauth.Caller, error) {
	configured, err := cfg.AllowedCallers()
	if err != nil {
		return nil, err
	}
	if len(configured) == 0 {
		return defaultAllowedCallers, nil
	}
	callers := make([]serviceauth.Caller, 0, len(configured))
	for _, raw := range configured {
		caller := serviceauth.Caller(raw)
		if !caller.Valid() {
			return nil, fmt.Errorf("qq-search grpc: unregistered caller %q", raw)
		}
		callers = append(callers, caller)
	}
	return callers, nil
}

// Register attaches the service to a server that already carries the boundary
// interceptors.
func (api *API) Register(server *grpc.Server) {
	qqsearchv1.RegisterQQSearchServiceServer(server, api)
}

// UnaryInterceptor authenticates every unary call and enforces its scope policy.
func (api *API) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return api.boundary.UnaryInterceptor()
}

// StreamInterceptor authenticates every streaming call.
func (api *API) StreamInterceptor() grpc.StreamServerInterceptor {
	return api.boundary.StreamInterceptor()
}

// Authenticate validates a credential presented on a non-gRPC entry point.
func (api *API) Authenticate(header string) (*serviceauth.Principal, error) {
	return api.boundary.AuthenticateHeader(header)
}
