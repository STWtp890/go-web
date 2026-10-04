// Package grpcapi_test drives the document service through a real gRPC server.
//
// Everything here goes over the wire: the boundary interceptors authenticate a
// signed assertion, the application layer runs against the real PostgreSQL and the
// call returns through the generated client. That is the only way to prove the
// documented status codes for a wrong audience, an expired token, a missing scope
// and a cross-namespace subject.
package grpcapi_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"document-service/internal/infrastructure/postgres"
	"document-service/internal/interfaces/grpcapi"
	"document-service/internal/testsupport"

	documentv1 "packages/gen/document/v1"
	"packages/serviceauth"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type transportHarness struct {
	client documentv1.DocumentServiceClient
	codec  *serviceauth.Codec
	key    []byte
	ids    *testsupport.IDs
	pool   *postgres.Pool
}

// newTransportHarness starts the service on an ephemeral loopback port with the
// boundary interceptors installed exactly as the process installs them.
func newTransportHarness(t *testing.T) *transportHarness {
	t.Helper()
	ids := testsupport.NewIDs()
	key := testsupport.BoundaryKey()
	cfg := testsupport.TestConfig(t, key)
	pool := testsupport.OpenPool(t, cfg)
	t.Cleanup(func() { ids.Cleanup(t, pool) })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	api, err := grpcapi.New(cfg, pool, key, logger)
	if err != nil {
		t.Fatalf("assemble the transport boundary: %v", err)
	}
	server := grpc.NewServer(
		grpc.UnaryInterceptor(api.UnaryInterceptor()),
		grpc.StreamInterceptor(api.StreamInterceptor()),
	)
	api.Register(server)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on a loopback port: %v", err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial %s: %v", listener.Addr().String(), err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	codec, err := serviceauth.NewCodec(key)
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	return &transportHarness{
		client: documentv1.NewDocumentServiceClient(connection),
		codec:  codec,
		key:    key,
		ids:    ids,
		pool:   pool,
	}
}

// claims builds the assertion a trusted go-web entry point would sign.
func (h *transportHarness) claims(subjectKey string, scopes ...serviceauth.Scope) serviceauth.AssertionClaims {
	names := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		names = append(names, string(scope))
	}
	now := time.Now().UTC()
	return serviceauth.AssertionClaims{
		Caller:     serviceauth.CallerGoWeb,
		Audience:   serviceauth.AudienceDocumentService,
		Scopes:     names,
		SubjectKey: subjectKey,
		Actor:      "transport-test",
		IssuedAt:   now.Unix(),
		ExpiresAt:  now.Add(2 * time.Minute).Unix(),
	}
}

func (h *transportHarness) seal(t *testing.T, claims serviceauth.AssertionClaims) string {
	t.Helper()
	token, err := h.codec.SealAssertion(claims)
	if err != nil {
		t.Fatalf("SealAssertion: %v", err)
	}
	return token
}

// contextWith carries the credential the way a caller presents it.
func contextWith(token string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(),
		serviceauth.HeaderAuthorization, serviceauth.AuthorizationHeader(token))
}

// TestIntegrationTransportBoundary is the end-to-end transport test: a correctly
// signed assertion creates a document and resolves a scope, and every documented
// rejection path answers with its documented status.
func TestIntegrationTransportBoundary(t *testing.T) {
	harness := newTransportHarness(t)
	subject := harness.ids.Subject("transport")
	// Unique identifiers: this database is shared with the other services' tests.
	botID := harness.ids.NumericID()
	groupID := harness.ids.NumericID()
	writer := harness.seal(t, harness.claims(subject,
		serviceauth.ScopeDocumentWrite, serviceauth.ScopeDocumentRead,
		serviceauth.ScopeSpaceAdmin, serviceauth.ScopeAccessResolve))

	// A correctly signed assertion reaches the business layer. The request carries
	// no identity at all: the subject comes from the assertion.
	created, err := harness.client.CreateDocument(contextWith(writer), &documentv1.CreateDocumentRequest{
		Title:   "transport document",
		Content: "written over a real gRPC call",
	})
	if err != nil {
		t.Fatalf("CreateDocument over the transport boundary: %v", err)
	}
	detail := created.GetDocument()
	if detail.GetSummary().GetOwnerSubjectKey() != subject {
		t.Fatalf("document owner = %q, want the asserted subject %q", detail.GetSummary().GetOwnerSubjectKey(), subject)
	}
	documentID := detail.GetSummary().GetDocumentId()
	privateSpace := detail.GetSummary().GetOwnerSpaceId()
	if documentID == "" || privateSpace == "" {
		t.Fatalf("incomplete detail over the wire: %+v", detail)
	}

	resolved, err := harness.client.ResolveAccessScope(contextWith(writer), &documentv1.ResolveAccessScopeRequest{
		Conversation: &documentv1.ConversationContext{Kind: documentv1.ConversationKind_CONVERSATION_KIND_PRIVATE},
	})
	if err != nil {
		t.Fatalf("ResolveAccessScope over the transport boundary: %v", err)
	}
	if resolved.GetDecision() != documentv1.Decision_DECISION_GRANTED {
		t.Fatalf("resolution = %+v", resolved)
	}
	if len(resolved.GetPrivateSpaceIds()) != 1 || resolved.GetPrivateSpaceIds()[0] != privateSpace {
		t.Fatalf("private range = %v, want [%s]", resolved.GetPrivateSpaceIds(), privateSpace)
	}

	// The capability endpoint mints a token the search service can verify.
	issued, err := harness.client.IssueSearchCapability(contextWith(writer), &documentv1.IssueSearchCapabilityRequest{
		Conversation: &documentv1.ConversationContext{Kind: documentv1.ConversationKind_CONVERSATION_KIND_PRIVATE},
	})
	if err != nil {
		t.Fatalf("IssueSearchCapability over the transport boundary: %v", err)
	}
	if issued.GetDecision() != documentv1.Decision_DECISION_GRANTED || issued.GetCapability() == "" {
		t.Fatalf("capability response = %+v", issued)
	}
	seal, err := harness.codec.OpenCapability(issued.GetCapability(), serviceauth.AudienceDocumentSearch)
	if err != nil {
		t.Fatalf("a document-search verifier must accept the minted capability: %v", err)
	}
	if seal.Claims.SubjectKey != subject {
		t.Fatalf("capability subject = %q, want %q", seal.Claims.SubjectKey, subject)
	}

	// No credential at all.
	if _, err := harness.client.CreateDocument(context.Background(), &documentv1.CreateDocumentRequest{
		Title: "anonymous", Content: "body",
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing credential: code = %s, want Unauthenticated", status.Code(err))
	}

	// A credential minted for another service is unauthenticated here, not
	// forbidden: it never proved anything about this boundary.
	wrongAudience := harness.claims(subject, serviceauth.ScopeDocumentWrite)
	wrongAudience.Audience = serviceauth.AudienceDocumentSearch
	if _, err := harness.client.CreateDocument(contextWith(harness.seal(t, wrongAudience)), &documentv1.CreateDocumentRequest{
		Title: "wrong audience", Content: "body",
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("wrong audience: code = %s, want Unauthenticated", status.Code(err))
	}

	// An expired assertion is unauthenticated, even with a valid signature.
	expired := harness.claims(subject, serviceauth.ScopeDocumentWrite)
	expired.IssuedAt = time.Now().Add(-time.Hour).Unix()
	expired.ExpiresAt = time.Now().Add(-time.Hour + 30*time.Second).Unix()
	if _, err := harness.client.CreateDocument(contextWith(harness.seal(t, expired)), &documentv1.CreateDocumentRequest{
		Title: "expired", Content: "body",
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expired credential: code = %s, want Unauthenticated", status.Code(err))
	}

	// A valid assertion without the method's scope is forbidden. space.admin is the
	// scope the binding command requires, and no binding data is touched because the
	// boundary refuses the call first.
	withoutSpaceAdmin := harness.seal(t, harness.claims(subject,
		serviceauth.ScopeDocumentWrite, serviceauth.ScopeDocumentRead, serviceauth.ScopeAccessResolve))
	if _, err := harness.client.BindGroupSpace(contextWith(withoutSpaceAdmin), &documentv1.BindGroupSpaceRequest{
		BotId: botID, ExternalGroupId: groupID,
		SpaceId: "11111111-1111-4111-8111-111111111111", Reason: "must be refused by the boundary",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("missing space.admin: code = %s, want PermissionDenied", status.Code(err))
	}
	if got := testsupport.Count(t, harness.pool,
		`SELECT count(*) FROM document_service.group_space_bindings WHERE bot_id = $1 AND external_group_id = $2`,
		botID, groupID); got != 0 {
		t.Fatal("an unauthorized call reached the database")
	}

	// A caller may only claim subjects inside its own namespace. The shared codec
	// refuses to seal such a claim, so the test signs the payload itself: the point
	// is that the receiver rejects a hand-crafted assertion, not that the helper
	// does.
	crossNamespace := harness.signRaw(t, map[string]any{
		"version":     1,
		"caller":      string(serviceauth.CallerGoWeb),
		"audience":    string(serviceauth.AudienceDocumentService),
		"scopes":      []string{string(serviceauth.ScopeDocumentWrite)},
		"subject_key": "qq:user:10001/20002",
		"issued_at":   time.Now().Unix(),
		"expires_at":  time.Now().Add(2 * time.Minute).Unix(),
	})
	if _, err := harness.client.CreateDocument(contextWith(crossNamespace), &documentv1.CreateDocumentRequest{
		Title: "cross namespace", Content: "body",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross namespace subject: code = %s, want PermissionDenied", status.Code(err))
	}
}

// signRaw renders and signs an assertion payload by hand, so a test can present a
// credential the shared seal helper would refuse to produce.
func (h *transportHarness) signRaw(t *testing.T, payload map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode the assertion payload: %v", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, h.key)
	mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
