// Package grpcapi is the document search transport boundary.
//
// It authenticates the caller, enforces the per-method scope policy, and then
// applies the resource inclusion rule: the request range must be a subset of the
// range the caller's capability granted. Any identifier outside the grant rejects
// the whole request. The service never calls back into document-service, go-web
// or py-agent while answering a query.
package grpcapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"document-search/internal/application"
	"document-search/internal/config"
	"document-search/internal/infrastructure/postgres"

	documentsearchv1 "packages/gen/documentsearch/v1"
	"packages/serviceauth"

	"google.golang.org/grpc"
)

// methodScopes is the authorization policy. A method absent from this table is
// rejected, so a new RPC without a registered policy fails closed.
var methodScopes = serviceauth.BoundaryPolicy{
	"/documentsearch.v1.DocumentSearchService/IndexDocumentEvent":    {serviceauth.ScopeDocumentIndexWriter},
	"/documentsearch.v1.DocumentSearchService/RebuildIndex":          {serviceauth.ScopeDocumentIndexWriter},
	"/documentsearch.v1.DocumentSearchService/GetIndexStatus":        {serviceauth.ScopeDocumentIndexWriter},
	"/documentsearch.v1.DocumentSearchService/SearchDocuments":       {serviceauth.ScopeDocumentSearcher},
	"/documentsearch.v1.DocumentSearchService/GetDocumentIndexState": {serviceauth.ScopeDocumentSearcher},
}

// defaultAllowedCallers is the closed set of callers when configuration does not
// narrow it further. The document service both produces the events and mints the
// search capabilities, so it appears twice: as the index writer identity carried
// by the consumer, and as the capability issuer for search.
var defaultAllowedCallers = []serviceauth.Caller{
	serviceauth.CallerDocumentService,
	serviceauth.CallerGoWeb,
	serviceauth.CallerPyAgent,
}

// AnonymousProbeMethods are served without a credential.
var AnonymousProbeMethods = []string{
	"/grpc.health.v1.Health/Check",
	"/grpc.health.v1.Health/Watch",
}

// API assembles the transport boundary.
type API struct {
	documentsearchv1.UnimplementedDocumentSearchServiceServer

	cfg      config.Config
	boundary *serviceauth.Boundary
	index    Index
	logger   *slog.Logger
}

// Index is the business interface the transport layer calls.
type Index interface {
	ApplyEvent(ctx context.Context, request *documentsearchv1.IndexDocumentEventRequest) (*documentsearchv1.IndexDocumentEventResponse, error)
	Search(ctx context.Context, principal *serviceauth.Principal, capability *serviceauth.CapabilityClaims, request *documentsearchv1.SearchDocumentsRequest) (*documentsearchv1.SearchDocumentsResponse, error)
	GetDocumentIndexState(ctx context.Context, documentID string) (*documentsearchv1.GetDocumentIndexStateResponse, error)
	Rebuild(ctx context.Context, confirm bool) (*documentsearchv1.RebuildIndexResponse, error)
	Status(ctx context.Context) (*documentsearchv1.GetIndexStatusResponse, error)
}

// New builds the transport boundary, failing closed when the boundary key or the
// caller allow-list is unusable.
//
// vectorIndex is the vector collection of the active index generation, opened by
// the composition root; it is required whenever the vector flow is enabled, so
// the transport cannot come up as a keyword-only service by accident.
func New(cfg config.Config, pool *postgres.Pool, boundaryKey []byte, logger *slog.Logger, vectorIndex application.VectorIndex) (*API, error) {
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
	boundary, err := serviceauth.NewBoundary("document-search", authenticator, methodScopes,
		serviceauth.WithAnonymous(AnonymousProbeMethods...),
	)
	if err != nil {
		return nil, err
	}
	business, err := application.New(application.Dependencies{
		Config:      cfg,
		Pool:        pool,
		Codec:       codec,
		VectorIndex: vectorIndex,
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
			return nil, fmt.Errorf("document-search grpc: unregistered caller %q", raw)
		}
		callers = append(callers, caller)
	}
	return callers, nil
}

// Register attaches the service to a server that already carries the boundary
// interceptors.
func (api *API) Register(server *grpc.Server) {
	documentsearchv1.RegisterDocumentSearchServiceServer(server, api)
}

// UnaryInterceptor authenticates every unary call and enforces its scope policy.
func (api *API) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return api.boundary.UnaryInterceptor()
}

// StreamInterceptor authenticates every streaming call.
func (api *API) StreamInterceptor() grpc.StreamServerInterceptor {
	return api.boundary.StreamInterceptor()
}

// Authenticate validates a credential presented on a non-gRPC entry point, so
// operational HTTP endpoints apply exactly the same rules.
func (api *API) Authenticate(header string) (*serviceauth.Principal, error) {
	return api.boundary.AuthenticateHeader(header)
}

// errUnimplemented marks a business path that is not wired yet. It exists so the
// transport never fabricates a successful response for an unwired use case.
var errUnimplemented = errors.New("document-search: business path is not wired yet")
