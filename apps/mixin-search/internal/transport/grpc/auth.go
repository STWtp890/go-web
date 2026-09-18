package grpcadapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"mixin-search/internal/security"
	mixinsearchchatv1 "packages/gen/mixin-search/chat/v1"
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
// Every method of a protected service must appear here. A method that is missing
// is denied rather than allowed, so adding an RPC without a policy fails closed.
//
// Chat methods list chat roles only, and document methods list document roles
// only. The two role sets are disjoint, so a capability minted for one corpus can
// never call the other (docs/adr/014-per-corpus-control-plane-isolation.md).
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

	mixinsearchchatv1.ChatIndexService_IndexConversationMessages_FullMethodName: {security.RoleChatIndexWriter},
	mixinsearchchatv1.ChatIndexService_ArchiveConversation_FullMethodName:       {security.RoleChatIndexWriter},
	mixinsearchchatv1.ChatIndexService_UpdateConversationAccess_FullMethodName:  {security.RoleChatIndexWriter},
	mixinsearchchatv1.ChatIndexService_RetractMessage_FullMethodName:            {security.RoleChatIndexWriter},
	mixinsearchchatv1.ChatIndexService_DeleteConversation_FullMethodName:        {security.RoleChatIndexWriter},
	mixinsearchchatv1.ChatIndexService_GetConversationIndexState_FullMethodName: {
		security.RoleChatIndexWriter,
		security.RoleChatOps,
	},
	mixinsearchchatv1.ChatIndexService_SearchChatMessages_FullMethodName: {security.RoleChatSearcher},
}

// protectedServicePrefixes identifies the protected services, including methods
// this build does not know about.
var protectedServicePrefixes = []string{
	"/" + mixinsearchv1.RAGService_ServiceDesc.ServiceName + "/",
	"/" + mixinsearchchatv1.ChatIndexService_ServiceDesc.ServiceName + "/",
}

// AuthConfig configures the boundary authenticator.
type AuthConfig struct {
	// Verifier verifies document-corpus capabilities. It is required: without it
	// the service cannot prove any caller of the document corpus.
	Verifier *security.Verifier
	// ChatVerifier verifies chat-corpus capabilities. It is required whenever the
	// chat service is served; a chat call without it is denied rather than
	// checked against the document verifier, because no service may accept two
	// audiences (docs/adr/014-per-corpus-control-plane-isolation.md).
	ChatVerifier *security.Verifier
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
	verifier     *security.Verifier
	chatVerifier *security.Verifier
	limiter      *security.RateLimiter
	audit        security.AuditSink
	now          func() time.Time
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
	return &Authenticator{
		verifier:     config.Verifier,
		chatVerifier: config.ChatVerifier,
		limiter:      config.Limiter,
		audit:        config.Audit,
		now:          now,
	}, nil
}

// verifierFor picks the single verifier that owns a method's corpus. There is no
// fallback: a service whose verifier is not configured cannot be called at all,
// which is what keeps one corpus from accepting the other's audience.
func (a *Authenticator) verifierFor(fullMethod string) *security.Verifier {
	switch {
	case strings.HasPrefix(fullMethod, "/"+mixinsearchv1.RAGService_ServiceDesc.ServiceName+"/"):
		return a.verifier
	case strings.HasPrefix(fullMethod, "/"+mixinsearchchatv1.ChatIndexService_ServiceDesc.ServiceName+"/"):
		return a.chatVerifier
	default:
		return nil
	}
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
	if !protected && !isProtectedServiceMethod(info.FullMethod) {
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

	identity, err := a.authenticate(ctx, info.FullMethod)
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
		record.RequestedScope = distinctScopeCount(search.GetAllowedSpaceIds(), search.GetAllowedDocumentIds())
		if err := identity.Allows(search.GetAllowedSpaceIds(), search.GetAllowedDocumentIds()); err != nil {
			record.Outcome = security.OutcomeDenied
			record.Detail = err.Error()
			a.record(record, startedAt)
			return nil, status.Errorf(codes.PermissionDenied, "search scope exceeds the granted capability: %v", err)
		}
	}

	if info.FullMethod == mixinsearchchatv1.ChatIndexService_SearchChatMessages_FullMethodName {
		search, ok := request.(*mixinsearchchatv1.SearchChatMessagesRequest)
		if !ok {
			record.Outcome = security.OutcomeDenied
			record.Detail = fmt.Sprintf("unexpected request type %T", request)
			a.record(record, startedAt)
			return nil, status.Error(codes.Internal, "unexpected request type for SearchChatMessages")
		}
		// The containment rule is the same one documents use: the granted
		// envelope carries container identifiers (chat scopes) and object
		// identifiers (conversations) for this corpus.
		record.RequestedScope = distinctScopeCount(search.GetAllowedScopeIds(), search.GetAllowedConversationIds())
		if err := identity.Allows(search.GetAllowedScopeIds(), search.GetAllowedConversationIds()); err != nil {
			record.Outcome = security.OutcomeDenied
			record.Detail = err.Error()
			a.record(record, startedAt)
			return nil, status.Errorf(codes.PermissionDenied, "chat search scope exceeds the granted capability: %v", err)
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

func (a *Authenticator) authenticate(ctx context.Context, fullMethod string) (security.Identity, error) {
	verifier := a.verifierFor(fullMethod)
	if verifier == nil {
		// The corpus of this method has no verifier configured. Denying is the
		// only safe answer: checking it against the other corpus's verifier would
		// be exactly the cross-corpus acceptance this design forbids.
		return security.Identity{}, fmt.Errorf("no capability verifier is configured for %s", fullMethod)
	}
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
	return verifier.Verify(value[len(bearerPrefix):])
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

// distinctScopeCount reports how many identifiers a request names, counted the
// way the granted side counts its own: trimmed, without blanks, without repeats
// inside one list. Counting raw entries made the audit record incomparable - a
// client that sent ["space-a", "space-a", ""] under a grant of ["space-a"] was
// logged as requesting three scopes against one, which reads as an attempted
// over-reach that never happened.
func distinctScopeCount(groups ...[]string) int {
	total := 0
	for _, group := range groups {
		seen := make(map[string]struct{}, len(group))
		for _, value := range group {
			trimmed := strings.TrimSpace(value)
			if trimmed == "" {
				continue
			}
			seen[trimmed] = struct{}{}
		}
		total += len(seen)
	}
	return total
}

// isProtectedServiceMethod reports whether the method belongs to a service whose
// methods must all have a policy. An unmapped method of a known service is denied
// rather than allowed, so a new RPC cannot go live without a role decision.
func isProtectedServiceMethod(fullMethod string) bool {
	for _, prefix := range protectedServicePrefixes {
		if strings.HasPrefix(fullMethod, prefix) {
			return true
		}
	}
	return false
}
