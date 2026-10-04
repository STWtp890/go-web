// Package grpcapi is the document service transport boundary.
//
// It performs exactly three jobs and nothing else: it authenticates the calling
// service from its signed assertion through the shared serviceauth boundary, it
// maps protocol messages to business commands, and it exposes the Outbox stream.
// Identity never comes from the request body; a caller can only act for a subject
// inside its own namespace.
package grpcapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"document-service/internal/application"
	"document-service/internal/config"
	"document-service/internal/infrastructure/postgres"

	documentv1 "packages/gen/document/v1"
	"packages/serviceauth"

	"google.golang.org/grpc"
)

// methodScopes is the authorization policy. A method absent from this table is
// rejected by the boundary, so adding an RPC without registering a policy fails
// closed at runtime and in the transport tests.
var methodScopes = serviceauth.BoundaryPolicy{
	"/document.v1.DocumentService/CreateDocument":     {serviceauth.ScopeDocumentWrite},
	"/document.v1.DocumentService/UpdateDraft":        {serviceauth.ScopeDocumentWrite},
	"/document.v1.DocumentService/SaveDocument":       {serviceauth.ScopeDocumentWrite},
	"/document.v1.DocumentService/PublishDocument":    {serviceauth.ScopeDocumentWrite},
	"/document.v1.DocumentService/WithdrawVersion":    {serviceauth.ScopeDocumentWrite},
	"/document.v1.DocumentService/ArchiveDocument":    {serviceauth.ScopeDocumentWrite},
	"/document.v1.DocumentService/TrashDocument":      {serviceauth.ScopeDocumentWrite},
	"/document.v1.DocumentService/GetDocument":        {serviceauth.ScopeDocumentRead},
	"/document.v1.DocumentService/ListDocuments":      {serviceauth.ScopeDocumentRead},
	"/document.v1.DocumentService/GetSpace":           {serviceauth.ScopeDocumentRead},
	"/document.v1.DocumentService/ListSubjects":       {serviceauth.ScopeDocumentRead},
	"/document.v1.DocumentService/CreateTeamSpace":    {serviceauth.ScopeSpaceAdmin},
	"/document.v1.DocumentService/GrantSpaceMember":   {serviceauth.ScopeSpaceAdmin},
	"/document.v1.DocumentService/RevokeSpaceMember":  {serviceauth.ScopeSpaceAdmin},
	"/document.v1.DocumentService/BindGroupSpace":     {serviceauth.ScopeSpaceAdmin},
	"/document.v1.DocumentService/RevokeGroupSpace":   {serviceauth.ScopeSpaceAdmin},
	"/document.v1.DocumentService/ResolveAccessScope": {serviceauth.ScopeAccessResolve},
	// Issuing a search capability resolves the same scope, so it requires the same
	// scope. The capability it mints is the credential, not the permission.
	"/document.v1.DocumentService/IssueSearchCapability": {serviceauth.ScopeAccessResolve},
	"/document.v1.DocumentService/ListDocumentEvents":    {serviceauth.ScopeDocumentEventReader},
}

// defaultAllowedCallers is the closed set of services that may call the document
// service when configuration does not narrow it further.
var defaultAllowedCallers = []serviceauth.Caller{
	serviceauth.CallerGoWeb,
	serviceauth.CallerPyAgent,
	serviceauth.CallerDocumentService,
	serviceauth.CallerSpacectl,
}

// AnonymousProbeMethods are served without a credential. They expose nothing but
// status, which is why container probes can call them without holding the key.
var AnonymousProbeMethods = []string{
	"/grpc.health.v1.Health/Check",
	"/grpc.health.v1.Health/Watch",
}

// API assembles the transport boundary.
type API struct {
	documentv1.UnimplementedDocumentServiceServer

	cfg       config.Config
	boundary  *serviceauth.Boundary
	documents Documents
	logger    *slog.Logger
}

// Documents is the business interface the transport layer calls. It is declared
// here, next to its only caller, so the transport cannot reach into persistence.
type Documents interface {
	CreateDocument(ctx context.Context, principal *serviceauth.Principal, request *documentv1.CreateDocumentRequest) (*documentv1.CreateDocumentResponse, error)
	UpdateDraft(ctx context.Context, principal *serviceauth.Principal, request *documentv1.UpdateDraftRequest) (*documentv1.UpdateDraftResponse, error)
	SaveDocument(ctx context.Context, principal *serviceauth.Principal, request *documentv1.SaveDocumentRequest) (*documentv1.SaveDocumentResponse, error)
	PublishDocument(ctx context.Context, principal *serviceauth.Principal, request *documentv1.PublishDocumentRequest) (*documentv1.PublishDocumentResponse, error)
	WithdrawVersion(ctx context.Context, principal *serviceauth.Principal, request *documentv1.WithdrawVersionRequest) (*documentv1.WithdrawVersionResponse, error)
	ArchiveDocument(ctx context.Context, principal *serviceauth.Principal, request *documentv1.ArchiveDocumentRequest) (*documentv1.ArchiveDocumentResponse, error)
	TrashDocument(ctx context.Context, principal *serviceauth.Principal, request *documentv1.TrashDocumentRequest) (*documentv1.TrashDocumentResponse, error)
	GetDocument(ctx context.Context, principal *serviceauth.Principal, request *documentv1.GetDocumentRequest) (*documentv1.GetDocumentResponse, error)
	ListDocuments(ctx context.Context, principal *serviceauth.Principal, request *documentv1.ListDocumentsRequest) (*documentv1.ListDocumentsResponse, error)
	CreateTeamSpace(ctx context.Context, principal *serviceauth.Principal, request *documentv1.CreateTeamSpaceRequest) (*documentv1.CreateTeamSpaceResponse, error)
	GetSpace(ctx context.Context, principal *serviceauth.Principal, request *documentv1.GetSpaceRequest) (*documentv1.GetSpaceResponse, error)
	ListSubjects(ctx context.Context, principal *serviceauth.Principal, request *documentv1.ListSubjectsRequest) (*documentv1.ListSubjectsResponse, error)
	GrantSpaceMember(ctx context.Context, principal *serviceauth.Principal, request *documentv1.GrantSpaceMemberRequest) (*documentv1.GrantSpaceMemberResponse, error)
	RevokeSpaceMember(ctx context.Context, principal *serviceauth.Principal, request *documentv1.RevokeSpaceMemberRequest) (*documentv1.RevokeSpaceMemberResponse, error)
	BindGroupSpace(ctx context.Context, principal *serviceauth.Principal, request *documentv1.BindGroupSpaceRequest) (*documentv1.BindGroupSpaceResponse, error)
	RevokeGroupSpace(ctx context.Context, principal *serviceauth.Principal, request *documentv1.RevokeGroupSpaceRequest) (*documentv1.RevokeGroupSpaceResponse, error)
	ResolveAccessScope(ctx context.Context, principal *serviceauth.Principal, request *documentv1.ResolveAccessScopeRequest) (*documentv1.ResolveAccessScopeResponse, error)
	// IssueSearchCapability mints a document-search resource capability from a
	// granted resolution and reports a denial without a capability otherwise.
	IssueSearchCapability(ctx context.Context, principal *serviceauth.Principal, request *documentv1.IssueSearchCapabilityRequest) (*documentv1.IssueSearchCapabilityResponse, error)
	// ListDocumentEvents opens the Outbox stream. The reader type is declared by
	// the application package, which owns the cursor semantics.
	ListDocumentEvents(ctx context.Context, principal *serviceauth.Principal, afterSequence int64, batchSize int) (application.DocumentEventReader, error)
	// CapabilityCodec exposes the boundary codec so resource capabilities are
	// minted by the same component that validates assertions.
	CapabilityCodec() *serviceauth.Codec
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
	authenticator, err := serviceauth.NewAuthenticator(codec, serviceauth.Audience(cfg.Auth.Audience), allowed...)
	if err != nil {
		return nil, err
	}
	boundary, err := serviceauth.NewBoundary("document-service", authenticator, methodScopes,
		serviceauth.WithAnonymous(AnonymousProbeMethods...),
	)
	if err != nil {
		return nil, err
	}
	business, err := application.New(application.Dependencies{
		Config:    cfg,
		Pool:      pool,
		Principal: principalFromContext,
	})
	if err != nil {
		return nil, err
	}
	return &API{cfg: cfg, boundary: boundary, documents: business, logger: logger}, nil
}

func allowedCallers(cfg config.Config) ([]serviceauth.Caller, error) {
	if len(cfg.Auth.AllowedCallers) == 0 {
		return defaultAllowedCallers, nil
	}
	callers := make([]serviceauth.Caller, 0, len(cfg.Auth.AllowedCallers))
	for _, raw := range cfg.Auth.AllowedCallers {
		caller := serviceauth.Caller(raw)
		if !caller.Valid() {
			return nil, fmt.Errorf("document-service grpc: unregistered caller %q", raw)
		}
		callers = append(callers, caller)
	}
	return callers, nil
}

// Register attaches the service to a server that already carries the boundary
// interceptors.
func (api *API) Register(server *grpc.Server) {
	documentv1.RegisterDocumentServiceServer(server, api)
}

// UnaryInterceptor authenticates every unary call and enforces its scope policy.
func (api *API) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return api.boundary.UnaryInterceptor()
}

// StreamInterceptor authenticates every streaming call and enforces its scope
// policy before the first message is produced.
func (api *API) StreamInterceptor() grpc.StreamServerInterceptor {
	return api.boundary.StreamInterceptor()
}

// HealthServiceName is the gRPC health service registered alongside this API.
const HealthServiceName = "document.v1.DocumentService"

func principalFromContext(ctx context.Context) (*serviceauth.Principal, error) {
	principal, ok := serviceauth.PrincipalFrom(ctx)
	if !ok {
		return nil, errors.New("document-service: no authenticated principal in context")
	}
	return principal, nil
}
