package grpcadapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"mixin-search/internal/security"
	mixinsearchv1 "packages/gen/mixin-search/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// authorizationHeader carries the caller capability on every protected call.
const authorizationHeader = "authorization"

// bearerPrefix is the only accepted credential scheme.
const bearerPrefix = "bearer "

// methodRoles maps each protected RPC to the roles allowed to call it. Index
// mutation and retrieval are deliberately different roles: the process that
// writes the derived index is not the process that serves searches, so a
// compromised searcher cannot rewrite what it reads.
//
// Every method of the RAG service must appear here. A method that is missing is
// denied rather than allowed, so adding an RPC without a policy fails closed.
var methodRoles = map[string][]string{
	mixinsearchv1.RAGService_IndexDocumentVersion_FullMethodName:    {security.RoleIndexWriter},
	mixinsearchv1.RAGService_ActivateDocumentVersion_FullMethodName: {security.RoleIndexWriter},
	mixinsearchv1.RAGService_UpdateDocumentAccess_FullMethodName:    {security.RoleIndexWriter},
	mixinsearchv1.RAGService_DeleteDocumentVersion_FullMethodName:   {security.RoleIndexWriter},
	mixinsearchv1.RAGService_DeleteDocument_FullMethodName:          {security.RoleIndexWriter},
	mixinsearchv1.RAGService_GetDocumentVersionState_FullMethodName: {
		security.RoleIndexWriter,
		security.RoleOps,
	},
	mixinsearchv1.RAGService_SearchDocuments_FullMethodName: {security.RoleSearcher},
}

// ragServicePrefix identifies the protected service, including methods this
// build does not know about.
var ragServicePrefix = "/" + mixinsearchv1.RAGService_ServiceDesc.ServiceName + "/"

// AuthConfig configures the boundary authenticator.
type AuthConfig struct {
	// Verifier is required: without it the service cannot prove any caller.
	Verifier *security.Verifier
	// Limiter bounds per-caller request rate. A nil limiter disables throttling.
	Limiter *security.RateLimiter
	// Audit receives one record per protected call. A nil sink discards records.
	Audit security.AuditSink
	// Now supplies the clock used for duration accounting.
	Now func() time.Time
}

// Authenticator authenticates and authorises every protected RPC before the
// transport adapter sees it.
type Authenticator struct {
	verifier *security.Verifier
	limiter  *security.RateLimiter
	audit    security.AuditSink
	now      func() time.Time
}

// NewAuthenticator validates the configuration. A missing verifier is an error
// rather than an open door: an unauthenticated mixin-search must not start.
func NewAuthenticator(config AuthConfig) (*Authenticator, error) {
	if config.Verifier == nil {
		return nil, errors.New("mixin-search authenticator: capability verifier is required")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Authenticator{verifier: config.Verifier, limiter: config.Limiter, audit: config.Audit, now: now}, nil
}

// UnaryInterceptor enforces caller identity, per-caller throttling, role
// permission and capability scope containment.
//
// Methods outside the RAG service (gRPC health, for example) are not intercepted
// so container probes stay independent of caller credentials. Reflection is a
// streaming service and is controlled by the server's reflection switch instead.
func (a *Authenticator) UnaryInterceptor(
	ctx context.Context,
	request any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	roles, protected := methodRoles[info.FullMethod]
	if !protected && !strings.HasPrefix(info.FullMethod, ragServicePrefix) {
		return handler(ctx, request)
	}
	startedAt := a.now()

	if !protected {
		a.record(security.AuditRecord{
			Method: info.FullMethod, Outcome: security.OutcomeDenied,
			Detail: "no role policy is defined for this method",
		}, startedAt)
		return nil, status.Errorf(codes.PermissionDenied, "method %s has no caller policy", info.FullMethod)
	}

	identity, err := a.authenticate(ctx)
	if err != nil {
		a.record(security.AuditRecord{
			Method: info.FullMethod, Outcome: security.OutcomeDenied, Detail: err.Error(),
		}, startedAt)
		return nil, status.Errorf(codes.Unauthenticated, "mixin-search requires a valid service capability: %v", err)
	}

	record := security.AuditRecord{
		CallerID:     identity.CallerID,
		Role:         identity.Role,
		UserID:       identity.UserID,
		Method:       info.FullMethod,
		GrantedScope: len(identity.GrantedSpaceIDs) + len(identity.GrantedDocumentIDs),
	}

	if !roleAllowed(roles, identity.Role) {
		record.Outcome = security.OutcomeDenied
		record.Detail = fmt.Sprintf("role %q may not call this method", identity.Role)
		a.record(record, startedAt)
		return nil, status.Errorf(codes.PermissionDenied, "role %q may not call %s", identity.Role, info.FullMethod)
	}

	if decision := a.limiter.Decide(identity.CallerID); decision != security.DecisionAllowed {
		record.Outcome = security.OutcomeThrottled
		switch decision {
		case security.DecisionOverBudget:
			record.Detail = "caller exceeded its request budget"
		case security.DecisionCallerTableFull:
			// Not the caller's fault: the service cannot track one more caller
			// until an idle one is reclaimed. The audit record says so instead of
			// blaming a caller that has spent nothing.
			record.Detail = "caller table is at its hard limit and no idle caller could be reclaimed"
		default:
			record.Detail = "throttled"
		}
		a.record(record, startedAt)
		return nil, status.Errorf(codes.ResourceExhausted, "caller %q was not admitted: %s", identity.CallerID, record.Detail)
	}

	if info.FullMethod == mixinsearchv1.RAGService_SearchDocuments_FullMethodName {
		search, ok := request.(*mixinsearchv1.SearchDocumentsRequest)
		if !ok {
			record.Outcome = security.OutcomeDenied
			record.Detail = fmt.Sprintf("unexpected request type %T", request)
			a.record(record, startedAt)
			return nil, status.Error(codes.Internal, "unexpected request type for SearchDocuments")
		}
		record.RequestedScope = len(search.GetAllowedSpaceIds()) + len(search.GetAllowedDocumentIds())
		if err := identity.Allows(search.GetAllowedSpaceIds(), search.GetAllowedDocumentIds()); err != nil {
			record.Outcome = security.OutcomeDenied
			record.Detail = err.Error()
			a.record(record, startedAt)
			return nil, status.Errorf(codes.PermissionDenied, "search scope exceeds the granted capability: %v", err)
		}
	}

	response, handlerErr := handler(security.WithIdentity(ctx, identity), request)
	record.Outcome = security.OutcomeAllowed
	if handlerErr != nil {
		record.Detail = status.Code(handlerErr).String()
	} else {
		record.Detail = codes.OK.String()
	}
	a.record(record, startedAt)
	return response, handlerErr
}

func (a *Authenticator) authenticate(ctx context.Context) (security.Identity, error) {
	values := metadata.ValueFromIncomingContext(ctx, authorizationHeader)
	if len(values) == 0 {
		return security.Identity{}, errors.New("no caller capability was presented")
	}
	if len(values) > 1 {
		return security.Identity{}, errors.New("multiple caller capabilities were presented")
	}
	value := strings.TrimSpace(values[0])
	if len(value) < len(bearerPrefix) || !strings.EqualFold(value[:len(bearerPrefix)], bearerPrefix) {
		return security.Identity{}, errors.New("caller capability must use the Bearer scheme")
	}
	return a.verifier.Verify(value[len(bearerPrefix):])
}

func (a *Authenticator) record(record security.AuditRecord, startedAt time.Time) {
	if a.audit == nil {
		return
	}
	record.Duration = a.now().Sub(startedAt)
	a.audit(record)
}

func roleAllowed(allowed []string, role string) bool {
	for _, candidate := range allowed {
		if candidate == role {
			return true
		}
	}
	return false
}
