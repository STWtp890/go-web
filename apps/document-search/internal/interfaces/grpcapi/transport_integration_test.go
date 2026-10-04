package grpcapi_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"document-search/internal/application"
	"document-search/internal/config"
	"document-search/internal/dbtest"
	"document-search/internal/interfaces/grpcapi"

	documentsearchv1 "packages/gen/documentsearch/v1"
	"packages/serviceauth"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// This is a real transport test: a gRPC server with the boundary interceptors,
// a real client, real credentials minted exactly as document-service mints them,
// and the real database behind the service. It covers the four outcomes the
// credential contract promises - allowed, unauthenticated for a credential
// minted for another audience, permission denied for a request outside the
// grant, and unauthenticated without a capability.
func TestIntegrationTransportBoundaryAndCapability(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Open(t, ctx)

	cfg := config.Default()
	cfg.Postgres.DSN = dbtest.DSN()
	boundaryKey := []byte(strings.Repeat("document-search-transport-key-", 2))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// The transport boundary is opened the way the process root opens it,
	// including the vector collection: a query that goes through this boundary
	// has to answer from the same index the production path serves.
	vectors, err := application.NewVectorIndex(ctx, cfg, pool)
	if err != nil {
		t.Fatalf("open the vector index (Qdrant must be running at %s): %v", cfg.Vector.Endpoint, err)
	}
	if vectors == nil {
		t.Fatal("the vector flow is disabled in the transport fixture")
	}
	api, err := grpcapi.New(cfg, pool, boundaryKey, logger, vectors)
	if err != nil {
		t.Fatalf("grpcapi.New: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer(
		grpc.UnaryInterceptor(api.UnaryInterceptor()),
		grpc.StreamInterceptor(api.StreamInterceptor()),
	)
	api.Register(server)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := documentsearchv1.NewDocumentSearchServiceClient(connection)

	codec, err := serviceauth.NewCodec(boundaryKey)
	if err != nil {
		t.Fatalf("serviceauth.NewCodec: %v", err)
	}

	documentID := dbtest.Identifier(t)
	spaceID := dbtest.Identifier(t)
	otherSpaceID := dbtest.Identifier(t)
	t.Cleanup(func() {
		if err := vectors.DeleteDocuments(context.Background(), []string{documentID}); err != nil {
			t.Errorf("remove the transport fixture's vector points: %v", err)
		}
		if err := vectors.Close(); err != nil {
			t.Errorf("close the vector index: %v", err)
		}
	})
	versionID := dbtest.Identifier(t)
	eventID := dbtest.Identifier(t)
	t.Cleanup(func() {
		dbtest.CleanupDocuments(t, context.Background(), pool, []string{documentID}, nil)
	})

	token := "transporttoken" + dbtest.RunTag()
	t.Logf("transport fixtures: document=%s space=%s run=%s", documentID, spaceID, dbtest.RunTag())

	// (1) IndexDocumentEvent with a valid document-index-writer assertion.
	writerToken := mintAssertion(t, codec, serviceauth.CallerDocumentService, serviceauth.AudienceDocumentSearch, serviceauth.ScopeDocumentIndexWriter)
	indexed, err := client.IndexDocumentEvent(
		authorize(ctx, writerToken, ""),
		&documentsearchv1.IndexDocumentEventRequest{
			EventId:             eventID,
			Sequence:            dbtest.Sequence(),
			Kind:                "upsert",
			DocumentId:          documentID,
			VersionId:           versionID,
			AggregateRevision:   1,
			ActivationRevision:  1,
			AccessRevision:      1,
			LifecycleRevision:   1,
			LifecycleStatus:     "active",
			PublicationStatus:   "published",
			OwnerSubjectKey:     "web:user:transport",
			OwnerSpaceId:        spaceID,
			AuthenticatedPublic: false,
			AllowedSpaceIds:     []string{spaceID},
			Title:               "Transport " + token,
			Summary:             "summary " + token,
			Content:             "body " + token,
			ContentFormat:       "markdown",
		},
	)
	if err != nil {
		t.Fatalf("IndexDocumentEvent with a writer assertion: %v", err)
	}
	if !indexed.GetApplied() {
		t.Fatalf("IndexDocumentEvent applied=false reason=%q", indexed.GetReason())
	}

	// The same RPC without a credential is unauthenticated.
	if _, err := client.IndexDocumentEvent(ctx, &documentsearchv1.IndexDocumentEventRequest{
		EventId: dbtest.Identifier(t), Sequence: dbtest.Sequence(), Kind: "upsert", DocumentId: documentID,
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("IndexDocumentEvent without a credential returned %v, want UNAUTHENTICATED", err)
	}
	// A credential minted for another audience is unauthenticated, not forbidden.
	foreignToken := mintAssertion(t, codec, serviceauth.CallerDocumentService, serviceauth.AudienceQQSearch, serviceauth.ScopeDocumentIndexWriter)
	if _, err := client.IndexDocumentEvent(authorize(ctx, foreignToken, ""), &documentsearchv1.IndexDocumentEventRequest{
		EventId: dbtest.Identifier(t), Sequence: dbtest.Sequence(), Kind: "upsert", DocumentId: documentID,
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("IndexDocumentEvent with a foreign-audience assertion returned %v, want UNAUTHENTICATED", err)
	}
	// A credential without the writer scope is forbidden.
	searcherOnly := mintAssertion(t, codec, serviceauth.CallerGoWeb, serviceauth.AudienceDocumentSearch, serviceauth.ScopeDocumentSearcher)
	if _, err := client.IndexDocumentEvent(authorize(ctx, searcherOnly, ""), &documentsearchv1.IndexDocumentEventRequest{
		EventId: dbtest.Identifier(t), Sequence: dbtest.Sequence(), Kind: "upsert", DocumentId: documentID,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("IndexDocumentEvent without the writer scope returned %v, want PERMISSION_DENIED", err)
	}

	searchAssertion := mintAssertion(t, codec, serviceauth.CallerGoWeb, serviceauth.AudienceDocumentSearch, serviceauth.ScopeDocumentSearcher)
	request := &documentsearchv1.SearchDocumentsRequest{Query: token, AllowedSpaceIds: []string{spaceID}, PageSize: 5}

	// (2a) a capability minted for this service is allowed.
	capability := mintCapability(t, codec, serviceauth.CallerDocumentService, serviceauth.AudienceDocumentSearch, []string{spaceID}, false)
	found, err := client.SearchDocuments(authorize(ctx, searchAssertion, capability), request)
	if err != nil {
		t.Fatalf("SearchDocuments with a valid capability: %v", err)
	}
	if len(found.GetHits()) != 1 {
		t.Fatalf("hits = %d, want the indexed document", len(found.GetHits()))
	}
	if found.GetHits()[0].GetSource() != "document" {
		t.Fatalf("hit source = %q, want document", found.GetHits()[0].GetSource())
	}
	if found.GetTruncated() {
		t.Fatal("a single hit was reported as truncated")
	}

	// (2b) a capability minted for another search service is unauthenticated.
	// It is minted by that audience's own fact source (py-agent), because
	// SealCapability now refuses an issuer that does not own the audience: a
	// document-service range can never be presented to qq-search and the other
	// way round.
	foreignCapability := mintCapability(t, codec, serviceauth.CallerPyAgent, serviceauth.AudienceQQSearch, []string{spaceID}, false)
	if _, err := client.SearchDocuments(authorize(ctx, searchAssertion, foreignCapability), request); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("SearchDocuments with a qq-search capability returned %v, want UNAUTHENTICATED", err)
	}

	// (2c) a request outside the granted range is denied as a whole.
	narrow := mintCapability(t, codec, serviceauth.CallerDocumentService, serviceauth.AudienceDocumentSearch, []string{spaceID}, false)
	outOfGrant := &documentsearchv1.SearchDocumentsRequest{
		Query:           token,
		AllowedSpaceIds: []string{spaceID, otherSpaceID},
		PageSize:        5,
	}
	if _, err := client.SearchDocuments(authorize(ctx, searchAssertion, narrow), outOfGrant); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("SearchDocuments with an out-of-grant space returned %v, want PERMISSION_DENIED", err)
	}
	// The equivalent in-grant request still succeeds, so the denial is the
	// inclusion rule and not a broken capability.
	if _, err := client.SearchDocuments(authorize(ctx, searchAssertion, narrow), request); err != nil {
		t.Fatalf("SearchDocuments inside the grant failed: %v", err)
	}

	// (2d) no capability at all is unauthenticated.
	if _, err := client.SearchDocuments(authorize(ctx, searchAssertion, ""), request); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("SearchDocuments without a capability returned %v, want UNAUTHENTICATED", err)
	}
}

// authorize builds outgoing metadata: the service assertion always, the resource
// capability when the call needs one.
func authorize(ctx context.Context, assertion, capability string) context.Context {
	ctx = metadata.AppendToOutgoingContext(ctx, serviceauth.HeaderAuthorization, serviceauth.AuthorizationHeader(assertion))
	if strings.TrimSpace(capability) != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, serviceauth.HeaderCapability, serviceauth.AuthorizationHeader(capability))
	}
	return ctx
}

// mintAssertion signs a service assertion the way document-service would.
func mintAssertion(t *testing.T, codec *serviceauth.Codec, caller serviceauth.Caller, audience serviceauth.Audience, scopes ...serviceauth.Scope) string {
	t.Helper()
	now := time.Now().UTC()
	values := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		values = append(values, string(scope))
	}
	token, err := codec.SealAssertion(serviceauth.AssertionClaims{
		Caller:    caller,
		Audience:  audience,
		Scopes:    values,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(2 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("SealAssertion: %v", err)
	}
	return token
}

// mintCapability signs a resource scope capability the way the audience's fact
// source would after resolving the subject's range. The issuer is a parameter
// because a capability is only valid for the audience its source owns.
func mintCapability(t *testing.T, codec *serviceauth.Codec, issuer serviceauth.Caller, audience serviceauth.Audience, spaces []string, authenticatedPublic bool) string {
	t.Helper()
	now := time.Now().UTC()
	token, err := codec.SealCapability(serviceauth.CapabilityClaims{
		Issuer:              issuer,
		Audience:            audience,
		Scopes:              []string{string(serviceauth.ScopeDocumentSearcher)},
		SubjectKey:          "web:user:transport",
		AllowedSpaceIDs:     spaces,
		AuthenticatedPublic: authenticatedPublic,
		IssuedAt:            now.Unix(),
		ExpiresAt:           now.Add(2 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("SealCapability: %v", err)
	}
	return token
}
