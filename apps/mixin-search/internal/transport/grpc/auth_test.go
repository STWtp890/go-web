package grpcadapter

import (
	"context"
	"strings"
	"testing"
	"time"

	"mixin-search/internal/security"
	mixinsearchv1 "packages/gen/mixin-search/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const testBoundaryKey = "0123456789abcdef0123456789abcdef"

type capturedAudit struct {
	records []security.AuditRecord
}

func (sink *capturedAudit) record(record security.AuditRecord) {
	sink.records = append(sink.records, record)
}

func (sink *capturedAudit) last() security.AuditRecord {
	if len(sink.records) == 0 {
		return security.AuditRecord{}
	}
	return sink.records[len(sink.records)-1]
}

func newTestAuthenticator(t *testing.T, options ...func(*AuthConfig)) (*Authenticator, *security.Issuer, *capturedAudit) {
	t.Helper()
	verifier, err := security.NewVerifier([]byte(testBoundaryKey), "mixin-search")
	if err != nil {
		t.Fatalf("build verifier: %v", err)
	}
	issuer, err := security.NewIssuer([]byte(testBoundaryKey), "go-web", "mixin-search", 5*time.Minute)
	if err != nil {
		t.Fatalf("build issuer: %v", err)
	}
	sink := &capturedAudit{}
	config := AuthConfig{Verifier: verifier, Audit: sink.record}
	for _, option := range options {
		option(&config)
	}
	authenticator, err := NewAuthenticator(config)
	if err != nil {
		t.Fatalf("build authenticator: %v", err)
	}
	return authenticator, issuer, sink
}

func incomingContext(token string) context.Context {
	if token == "" {
		return context.Background()
	}
	return metadata.NewIncomingContext(
		context.Background(),
		metadata.Pairs(authorizationHeader, "Bearer "+token),
	)
}

// handlerSpy records whether the RPC body ran and with which identity.
type handlerSpy struct {
	called   bool
	identity security.Identity
	hasID    bool
}

func (spy *handlerSpy) handler(ctx context.Context, request any) (any, error) {
	spy.called = true
	spy.identity, spy.hasID = security.IdentityFrom(ctx)
	return "ok", nil
}

func TestInterceptorRejectsCallsWithoutAValidCapability(t *testing.T) {
	t.Parallel()

	authenticator, issuer, sink := newTestAuthenticator(t)
	foreignIssuer, err := security.NewIssuer([]byte("ffffffffffffffffffffffffffffffff"), "go-web", "mixin-search", time.Minute)
	if err != nil {
		t.Fatalf("build foreign issuer: %v", err)
	}
	foreignToken, err := foreignIssuer.Issue("go-web-index-worker", security.RoleIndexWriter, "", nil, nil)
	if err != nil {
		t.Fatalf("issue foreign token: %v", err)
	}
	expiredIssuer, err := security.NewIssuer([]byte(testBoundaryKey), "go-web", "mixin-search", time.Minute,
		security.WithClock(func() time.Time { return time.Now().Add(-time.Hour) }))
	if err != nil {
		t.Fatalf("build expired issuer: %v", err)
	}
	expiredToken, err := expiredIssuer.Issue("go-web-index-worker", security.RoleIndexWriter, "", nil, nil)
	if err != nil {
		t.Fatalf("issue expired token: %v", err)
	}

	cases := []struct {
		name  string
		token string
	}{
		{name: "no capability", token: ""},
		{name: "not a token", token: "not-a-token"},
		{name: "signed by another key", token: foreignToken},
		{name: "expired", token: expiredToken},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			spy := &handlerSpy{}
			_, err := authenticator.UnaryInterceptor(
				incomingContext(testCase.token),
				&mixinsearchv1.IndexDocumentVersionRequest{},
				&grpc.UnaryServerInfo{FullMethod: mixinsearchv1.RAGService_IndexDocumentVersion_FullMethodName},
				spy.handler,
			)
			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("code = %s (err=%v), want Unauthenticated", status.Code(err), err)
			}
			if spy.called {
				t.Fatal("the RPC body ran for an unauthenticated call")
			}
			if sink.last().Outcome != security.OutcomeDenied {
				t.Fatalf("audit outcome = %q, want denied", sink.last().Outcome)
			}
		})
	}
	_ = issuer
}

func TestInterceptorEnforcesRoleSeparation(t *testing.T) {
	t.Parallel()

	authenticator, issuer, sink := newTestAuthenticator(t)

	// A search capability must not mutate the index.
	searchToken, err := issuer.Issue("py-agent", security.RoleSearcher, "7", []string{"space-a"}, nil)
	if err != nil {
		t.Fatalf("issue searcher token: %v", err)
	}
	spy := &handlerSpy{}
	_, err = authenticator.UnaryInterceptor(
		incomingContext(searchToken),
		&mixinsearchv1.IndexDocumentVersionRequest{},
		&grpc.UnaryServerInfo{FullMethod: mixinsearchv1.RAGService_IndexDocumentVersion_FullMethodName},
		spy.handler,
	)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("searcher writing the index: code = %s, want PermissionDenied", status.Code(err))
	}
	if spy.called {
		t.Fatal("the index-write body ran for a searcher capability")
	}
	if sink.last().Outcome != security.OutcomeDenied {
		t.Fatalf("last audit outcome = %q, want denied", sink.last().Outcome)
	}

	// An index capability must not run searches, even for public documents.
	indexToken, err := issuer.Issue("go-web-index-worker", security.RoleIndexWriter, "", nil, nil)
	if err != nil {
		t.Fatalf("issue index-writer token: %v", err)
	}
	spy = &handlerSpy{}
	_, err = authenticator.UnaryInterceptor(
		incomingContext(indexToken),
		&mixinsearchv1.SearchDocumentsRequest{Query: "x", AllowedSpaceIds: []string{"space-a"}},
		&grpc.UnaryServerInfo{FullMethod: mixinsearchv1.RAGService_SearchDocuments_FullMethodName},
		spy.handler,
	)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("index-writer searching: code = %s, want PermissionDenied", status.Code(err))
	}
	if spy.called {
		t.Fatal("the search body ran for an index-writer capability")
	}
}

func TestInterceptorRejectsSearchBeyondGrantedScope(t *testing.T) {
	t.Parallel()

	authenticator, issuer, sink := newTestAuthenticator(t)
	token, err := issuer.Issue("py-agent", security.RoleSearcher, "7", []string{"space-a"}, []string{"doc-1"})
	if err != nil {
		t.Fatalf("issue searcher token: %v", err)
	}

	cases := []struct {
		name    string
		request *mixinsearchv1.SearchDocumentsRequest
	}{
		{
			name:    "ungranted space",
			request: &mixinsearchv1.SearchDocumentsRequest{Query: "x", AllowedSpaceIds: []string{"space-z"}},
		},
		{
			name:    "ungranted document",
			request: &mixinsearchv1.SearchDocumentsRequest{Query: "x", AllowedDocumentIds: []string{"doc-9"}},
		},
		{
			name: "granted scope widened by one entry",
			request: &mixinsearchv1.SearchDocumentsRequest{
				Query: "x", AllowedSpaceIds: []string{"space-a", "space-z"},
			},
		},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			spy := &handlerSpy{}
			_, err := authenticator.UnaryInterceptor(
				incomingContext(token),
				testCase.request,
				&grpc.UnaryServerInfo{FullMethod: mixinsearchv1.RAGService_SearchDocuments_FullMethodName},
				spy.handler,
			)
			if status.Code(err) != codes.PermissionDenied {
				t.Fatalf("code = %s (err=%v), want PermissionDenied", status.Code(err), err)
			}
			if spy.called {
				t.Fatal("the search body ran for a request outside the granted scope")
			}
			if !strings.Contains(err.Error(), "granted capability") {
				t.Fatalf("error = %v, want it to name the granted capability", err)
			}
			if sink.last().Outcome != security.OutcomeDenied {
				t.Fatalf("audit outcome = %q, want denied", sink.last().Outcome)
			}
		})
	}
}

func TestInterceptorAllowsSearchInsideGrantedScope(t *testing.T) {
	t.Parallel()

	authenticator, issuer, sink := newTestAuthenticator(t)
	token, err := issuer.Issue("go-web-shadow-search", security.RoleSearcher, "42", []string{"space-a", "space-b"}, nil)
	if err != nil {
		t.Fatalf("issue searcher token: %v", err)
	}

	spy := &handlerSpy{}
	response, err := authenticator.UnaryInterceptor(
		incomingContext(token),
		&mixinsearchv1.SearchDocumentsRequest{Query: "x", AllowedSpaceIds: []string{"space-b"}},
		&grpc.UnaryServerInfo{FullMethod: mixinsearchv1.RAGService_SearchDocuments_FullMethodName},
		spy.handler,
	)
	if err != nil {
		t.Fatalf("in-scope search failed: %v", err)
	}
	if response != "ok" || !spy.called {
		t.Fatal("handler did not run for an in-scope search")
	}
	if !spy.hasID || spy.identity.CallerID != "go-web-shadow-search" || spy.identity.UserID != "42" {
		t.Fatalf("handler identity = %+v (present=%t)", spy.identity, spy.hasID)
	}

	record := sink.last()
	if record.Outcome != security.OutcomeAllowed {
		t.Fatalf("audit outcome = %q, want allowed", record.Outcome)
	}
	if record.CallerID != "go-web-shadow-search" || record.UserID != "42" || record.Role != security.RoleSearcher {
		t.Fatalf("audit record does not identify the caller: %+v", record)
	}
	if record.RequestedScope != 1 || record.GrantedScope != 2 {
		t.Fatalf("audit scope = requested %d granted %d, want 1 and 2", record.RequestedScope, record.GrantedScope)
	}
}

func TestInterceptorThrottlesPerCaller(t *testing.T) {
	t.Parallel()

	authenticator, issuer, sink := newTestAuthenticator(t, func(config *AuthConfig) {
		config.Limiter = security.NewRateLimiter(0.0001, 2)
	})
	token, err := issuer.Issue("go-web-shadow-search", security.RoleSearcher, "", []string{"space-a"}, nil)
	if err != nil {
		t.Fatalf("issue searcher token: %v", err)
	}
	info := &grpc.UnaryServerInfo{FullMethod: mixinsearchv1.RAGService_SearchDocuments_FullMethodName}
	request := &mixinsearchv1.SearchDocumentsRequest{Query: "x", AllowedSpaceIds: []string{"space-a"}}

	for attempt := 0; attempt < 2; attempt++ {
		spy := &handlerSpy{}
		if _, err := authenticator.UnaryInterceptor(incomingContext(token), request, info, spy.handler); err != nil {
			t.Fatalf("call %d inside the burst budget failed: %v", attempt+1, err)
		}
	}
	spy := &handlerSpy{}
	_, err = authenticator.UnaryInterceptor(incomingContext(token), request, info, spy.handler)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("code = %s (err=%v), want ResourceExhausted", status.Code(err), err)
	}
	if spy.called {
		t.Fatal("the search body ran after the caller was throttled")
	}
	if sink.last().Outcome != security.OutcomeThrottled {
		t.Fatalf("audit outcome = %q, want throttled", sink.last().Outcome)
	}
}

func TestInterceptorFailsClosedForUnmappedRAGMethods(t *testing.T) {
	t.Parallel()

	authenticator, issuer, _ := newTestAuthenticator(t)
	token, err := issuer.Issue("go-web-index-worker", security.RoleIndexWriter, "", nil, nil)
	if err != nil {
		t.Fatalf("issue index-writer token: %v", err)
	}

	spy := &handlerSpy{}
	_, err = authenticator.UnaryInterceptor(
		incomingContext(token),
		&mixinsearchv1.GetDocumentVersionStateRequest{},
		&grpc.UnaryServerInfo{FullMethod: "/mixin_search.v1.RAGService/FutureRPC"},
		spy.handler,
	)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %s, want PermissionDenied for an unmapped RAG method", status.Code(err))
	}
	if spy.called {
		t.Fatal("an unmapped RAG method reached the handler")
	}
}

func TestInterceptorLeavesHealthUnauthenticated(t *testing.T) {
	t.Parallel()

	authenticator, _, _ := newTestAuthenticator(t)
	spy := &handlerSpy{}
	// Container probes carry no caller credential, so health must stay reachable
	// without one; it exposes serving status only.
	if _, err := authenticator.UnaryInterceptor(
		context.Background(),
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/grpc.health.v1.Health/Check"},
		spy.handler,
	); err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	if !spy.called {
		t.Fatal("health check did not reach its handler")
	}
}

func TestInterceptorAllowsOpsToReadStateOnly(t *testing.T) {
	t.Parallel()

	authenticator, issuer, _ := newTestAuthenticator(t)
	token, err := issuer.Issue("go-web-index-admin", security.RoleOps, "", nil, nil)
	if err != nil {
		t.Fatalf("issue ops token: %v", err)
	}

	spy := &handlerSpy{}
	if _, err := authenticator.UnaryInterceptor(
		incomingContext(token),
		&mixinsearchv1.GetDocumentVersionStateRequest{DocumentId: "doc-1", VersionId: "v1"},
		&grpc.UnaryServerInfo{FullMethod: mixinsearchv1.RAGService_GetDocumentVersionState_FullMethodName},
		spy.handler,
	); err != nil {
		t.Fatalf("ops state read failed: %v", err)
	}

	spy = &handlerSpy{}
	_, err = authenticator.UnaryInterceptor(
		incomingContext(token),
		&mixinsearchv1.DeleteDocumentRequest{DocumentId: "doc-1"},
		&grpc.UnaryServerInfo{FullMethod: mixinsearchv1.RAGService_DeleteDocument_FullMethodName},
		spy.handler,
	)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ops delete: code = %s, want PermissionDenied", status.Code(err))
	}
}

func TestNewAuthenticatorRequiresVerifier(t *testing.T) {
	t.Parallel()

	if _, err := NewAuthenticator(AuthConfig{}); err == nil {
		t.Fatal("NewAuthenticator without a verifier succeeded, want error")
	}
}

func TestEveryRAGMethodHasARolePolicy(t *testing.T) {
	t.Parallel()

	// A new RPC without a policy would be denied at runtime, but silently. This
	// test makes the omission visible while the contract is being extended.
	for _, method := range mixinsearchv1.RAGService_ServiceDesc.Methods {
		fullMethod := ragServicePrefix + method.MethodName
		roles, ok := methodRoles[fullMethod]
		if !ok {
			t.Errorf("RPC %s has no role policy", method.MethodName)
			continue
		}
		if len(roles) == 0 {
			t.Errorf("RPC %s has an empty role policy", method.MethodName)
		}
	}
}
