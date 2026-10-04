package grpcapi

// End-to-end transport test: a real gRPC server with the boundary interceptors,
// a real client, real capabilities minted with packages/serviceauth, and the
// real database. It asserts the four authorization outcomes the contract
// publishes, not just the happy path.

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"qq-search/internal/config"
	"qq-search/internal/infrastructure/postgres"
	"qq-search/internal/ingress/pyagent"
	"qq-search/internal/testkit"

	qqsearchv1 "packages/gen/qqsearch/v1"
	"packages/serviceauth"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Default development instance. A connection failure fails the test.
const transportTestDefaultDSN = "postgres://qq_search_writer:qq_search@127.0.0.1:15432/gin_demo?sslmode=disable"

// transportBoundaryKey is shared by the server and the test's minting codec.
const transportBoundaryKey = "qq-search transport integration boundary key 0001"

var transportPrefix = newTransportPrefix()

func newTransportPrefix() string {
	buffer := make([]byte, 6)
	if _, err := crand.Read(buffer); err != nil {
		panic(err)
	}
	return "qqstr-" + hex.EncodeToString(buffer)
}

func transportTestDSN() string {
	if dsn := strings.TrimSpace(os.Getenv("QQ_SEARCH_TEST_DSN")); dsn != "" {
		return dsn
	}
	return transportTestDefaultDSN
}

// transportToken is a single alphanumeric lexeme unique to this run. It carries
// no punctuation on purpose: the full-text query language treats a hyphen as a
// negation, so a token containing one would not match what was indexed.
func transportToken() string {
	buffer := make([]byte, 8)
	if _, err := crand.Read(buffer); err != nil {
		panic(err)
	}
	return "ztoktransport" + hex.EncodeToString(buffer)
}

// startServer assembles the real transport boundary over a real database and
// serves it on a loopback port inside this process.
func startServer(t *testing.T) (qqsearchv1.QQSearchServiceClient, *serviceauth.Codec, string) {
	t.Helper()
	dsn := transportTestDSN()
	pool, err := postgres.Open(context.Background(), config.PostgresConfig{
		DSN:            dsn,
		Schema:         "qq_search",
		ConnectTimeout: "5s",
	})
	if err != nil {
		t.Fatalf("PostgreSQL integration test requires a reachable database at %s: %v", dsn, err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	// One qq-search integration test at a time on this schema: the application
	// suite runs as a parallel package and both mutate the same global counters
	// and rows. The release is registered before the row cleanup so that cleanup
	// stays inside the lock.
	release, err := testkit.Serialize(context.Background(), pool.Pgx())
	if err != nil {
		t.Fatalf("serialize the qq-search integration suites: %v", err)
	}
	t.Cleanup(release)
	t.Cleanup(func() { cleanupTransportRows(t, pool.Pgx()) })

	key := []byte(transportBoundaryKey)
	api, err := New(config.Default(), pool, key, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
		t.Fatalf("listen for the in-process server: %v", err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial the in-process server: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	codec, err := serviceauth.NewCodec(key)
	if err != nil {
		t.Fatalf("build the boundary codec: %v", err)
	}
	return qqsearchv1.NewQQSearchServiceClient(connection), codec, transportPrefix
}

// cleanupTransportRows removes only this process's rows.
func cleanupTransportRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pattern := transportPrefix + "%"
	for _, statement := range []string{
		`DELETE FROM qq_search.qq_messages WHERE record_id LIKE $1`,
		`DELETE FROM qq_search.qq_files WHERE record_id LIKE $1`,
		`DELETE FROM qq_search.qq_applied_events WHERE record_id LIKE $1`,
	} {
		if _, err := pool.Exec(ctx, statement, pattern); err != nil {
			t.Errorf("cleanup %q failed: %v", statement, err)
		}
	}
}

// assertion mints the service identity assertion py-agent signs.
func assertion(t *testing.T, codec *serviceauth.Codec, scopes ...serviceauth.Scope) string {
	t.Helper()
	now := time.Now().UTC()
	values := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		values = append(values, string(scope))
	}
	token, err := codec.SealAssertion(serviceauth.AssertionClaims{
		Caller:     serviceauth.CallerPyAgent,
		Audience:   serviceauth.AudienceQQSearch,
		Scopes:     values,
		SubjectKey: "qq:10001:user:20003",
		Channel:    "qq",
		BotID:      "10001",
		IssuedAt:   now.Unix(),
		ExpiresAt:  now.Add(2 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("mint the py-agent assertion: %v", err)
	}
	return token
}

// capability mints the channel scope capability py-agent issues.
func capability(t *testing.T, codec *serviceauth.Codec, audience serviceauth.Audience, scopes []string, scope *qqsearchv1.QQChannelScope) string {
	t.Helper()
	return capabilityFrom(t, codec, serviceauth.CallerPyAgent, audience, scopes, scope)
}

// capabilityFrom mints a capability for an explicit issuer. Only the fact source
// registered for an audience may sign for it, so a foreign-audience token has to
// be minted by that audience's own source: presenting a py-agent token at
// document-search is now impossible to construct, which is the point of the
// issuer rule.
func capabilityFrom(t *testing.T, codec *serviceauth.Codec, issuer serviceauth.Caller, audience serviceauth.Audience, scopes []string, scope *qqsearchv1.QQChannelScope) string {
	t.Helper()
	now := time.Now().UTC()
	token, err := codec.SealCapability(serviceauth.CapabilityClaims{
		Issuer:           issuer,
		Audience:         audience,
		Scopes:           scopes,
		SubjectKey:       "qq:10001:user:20003",
		BotIDs:           scope.GetBotIds(),
		ConversationIDs:  scope.GetConversationIds(),
		ExternalGroupIDs: scope.GetExternalGroupIds(),
		Channel:          "qq",
		IssuedAt:         now.Unix(),
		ExpiresAt:        now.Add(2 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("mint the channel capability: %v", err)
	}
	return token
}

func authorized(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, serviceauth.HeaderAuthorization, serviceauth.AuthorizationHeader(token))
}

func withCapability(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, serviceauth.HeaderCapability, serviceauth.AuthorizationHeader(token))
}

func TestIntegrationTransportBoundary(t *testing.T) {
	client, codec, prefix := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	token := transportToken()
	recordID := prefix + "-msg-1"
	inbound := pyagent.InboundEvent{
		BotID:     "10001",
		UserID:    "20003",
		MessageID: recordID,
		Text:      "transport probe " + token,
		GroupID:   "999",
		Mentioned: true,
	}
	request, err := pyagent.RequestFromEnvelope(inbound.MessageEnvelope(1, 1, time.Now().UTC()))
	if err != nil {
		t.Fatalf("convert the py-agent event: %v", err)
	}

	writerAssertion := assertion(t, codec, serviceauth.ScopeQQIndexWriter)
	searcherAssertion := assertion(t, codec, serviceauth.ScopeQQSearcher)
	granted := &qqsearchv1.QQChannelScope{
		BotIds:           []string{"10001"},
		ConversationIds:  []string{"qq:10001:group:999"},
		ExternalGroupIds: []string{"999"},
	}
	searcherCapability := capability(t, codec, serviceauth.AudienceQQSearch, []string{string(serviceauth.ScopeQQSearcher)}, granted)

	// (1) Index with a valid qq-index-writer assertion from py-agent.
	writerContext := authorized(ctx, writerAssertion)
	indexed, err := client.IndexQQSourceEvent(writerContext, request)
	if err != nil {
		t.Fatalf("IndexQQSourceEvent with a valid writer assertion: %v", err)
	}
	if !indexed.GetApplied() || indexed.GetReason() != "applied" {
		t.Fatalf("IndexQQSourceEvent = %+v, want applied", indexed)
	}
	if got := indexed.GetState().GetConversationId(); got != "qq:10001:group:999" {
		t.Fatalf("indexed under conversation %q, want the py-agent identity", got)
	}

	// A redelivery of the same event over the wire is still a duplicate.
	redelivered, err := client.IndexQQSourceEvent(writerContext, request)
	if err != nil {
		t.Fatalf("redelivering the same event: %v", err)
	}
	if redelivered.GetApplied() || redelivered.GetReason() != "duplicate" {
		t.Fatalf("redelivery = %+v, want a duplicate", redelivered)
	}

	// (2a) A valid qq-searcher capability is allowed.
	searchContext := withCapability(authorized(ctx, searcherAssertion), searcherCapability)
	messages, err := client.SearchQQMessages(searchContext, &qqsearchv1.SearchQQMessagesRequest{Query: token, TopK: 10})
	if err != nil {
		t.Fatalf("SearchQQMessages with a valid capability: %v", err)
	}
	found := false
	for _, hit := range messages.GetHits() {
		if hit.GetRecordId() == recordID {
			found = true
			if hit.GetRecalled() {
				t.Error("an indexed message was reported as recalled")
			}
			if hit.GetBotId() != "10001" || hit.GetConversationId() != "qq:10001:group:999" {
				t.Errorf("hit identity = %s/%s, want the py-agent identity", hit.GetBotId(), hit.GetConversationId())
			}
		}
	}
	if !found {
		t.Fatalf("the indexed message is not searchable: %+v", messages.GetHits())
	}
	if _, err := client.SearchQQFiles(searchContext, &qqsearchv1.SearchQQFilesRequest{Query: token, TopK: 10}); err != nil {
		t.Fatalf("SearchQQFiles with a valid capability: %v", err)
	}

	// (2b) A capability minted by the other fact source for its own audience is
	// UNAUTHENTICATED here. The token has to be signed by document-service
	// because that is the only issuer document-search accepts, which is itself a
	// consequence of the issuer rule: a py-agent credential can no longer exist
	// for this audience at all.
	wrongAudience := capabilityFrom(t, codec, serviceauth.CallerDocumentService, serviceauth.AudienceDocumentSearch, []string{string(serviceauth.ScopeDocumentSearcher)}, granted)
	_, err = client.SearchQQMessages(
		withCapability(authorized(ctx, searcherAssertion), wrongAudience),
		&qqsearchv1.SearchQQMessagesRequest{Query: token},
	)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("a capability for audience document-search: got %v, want UNAUTHENTICATED", err)
	}

	// (2c) A request outside the granted channel scope is PERMISSION_DENIED as a
	// whole, not silently trimmed.
	_, err = client.SearchQQMessages(searchContext, &qqsearchv1.SearchQQMessagesRequest{
		Query: token,
		Scope: &qqsearchv1.QQChannelScope{
			BotIds:           []string{"10001"},
			ConversationIds:  []string{"qq:10001:group:999", "qq:10001:group:1000"},
			ExternalGroupIds: []string{"999", "1000"},
		},
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a request outside the granted scope: got %v, want PERMISSION_DENIED", err)
	}

	// (2d) No resource capability at all is UNAUTHENTICATED.
	_, err = client.SearchQQMessages(authorized(ctx, searcherAssertion), &qqsearchv1.SearchQQMessagesRequest{Query: token})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("a search without a capability: got %v, want UNAUTHENTICATED", err)
	}

	// (2e) No assertion at all is UNAUTHENTICATED, capability or not.
	_, err = client.SearchQQMessages(withCapability(ctx, searcherCapability), &qqsearchv1.SearchQQMessagesRequest{Query: token})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("a search without an assertion: got %v, want UNAUTHENTICATED", err)
	}

	// (2f) An index-writer credential cannot search.
	_, err = client.SearchQQMessages(
		withCapability(authorized(ctx, writerAssertion), searcherCapability),
		&qqsearchv1.SearchQQMessagesRequest{Query: token},
	)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a writer assertion searching: got %v, want PERMISSION_DENIED", err)
	}

	// (2g) A searcher credential cannot index.
	_, err = client.IndexQQSourceEvent(authorized(ctx, searcherAssertion), request)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a searcher assertion indexing: got %v, want PERMISSION_DENIED", err)
	}

	// (2h) A capability cannot be replayed as a service assertion.
	_, err = client.SearchQQMessages(withCapability(authorized(ctx, searcherCapability), searcherCapability), &qqsearchv1.SearchQQMessagesRequest{Query: token})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("a capability replayed as an assertion: got %v, want UNAUTHENTICATED", err)
	}

	// (2i) A tampered capability fails its signature check.
	tampered := []byte(searcherCapability)
	if tampered[len(tampered)-1] == 'A' {
		tampered[len(tampered)-1] = 'B'
	} else {
		tampered[len(tampered)-1] = 'A'
	}
	_, err = client.SearchQQMessages(withCapability(authorized(ctx, searcherAssertion), string(tampered)), &qqsearchv1.SearchQQMessagesRequest{Query: token})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("a tampered capability: got %v, want UNAUTHENTICATED", err)
	}

	// (3) The operational RPCs: status is readable by the writer identity and
	// reports two distinct collections; a rebuild needs explicit confirmation.
	indexStatus, err := client.GetIndexStatus(writerContext, &qqsearchv1.GetIndexStatusRequest{})
	if err != nil {
		t.Fatalf("GetIndexStatus with a writer assertion: %v", err)
	}
	if indexStatus.GetMessageCollection() == "" || indexStatus.GetFileCollection() == "" {
		t.Fatalf("GetIndexStatus reported empty collections: %+v", indexStatus)
	}
	if indexStatus.GetMessageCollection() == indexStatus.GetFileCollection() {
		t.Fatalf("both corpora report the same collection %q", indexStatus.GetMessageCollection())
	}
	if indexStatus.GetEventsApplied() < 1 {
		t.Fatalf("GetIndexStatus reported %d applied events, want at least 1", indexStatus.GetEventsApplied())
	}
	if _, err := client.RebuildIndex(writerContext, &qqsearchv1.RebuildIndexRequest{Confirm: false}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("RebuildIndex(confirm=false): got %v, want FAILED_PRECONDITION", err)
	}
	rebuilt, err := client.RebuildIndex(writerContext, &qqsearchv1.RebuildIndexRequest{Confirm: true})
	if err != nil {
		t.Fatalf("RebuildIndex(confirm=true): %v", err)
	}
	if rebuilt.GetMessagesRebuilt() < 1 {
		t.Fatalf("the rebuild reported %d messages, want at least the one this test indexed", rebuilt.GetMessagesRebuilt())
	}
	if _, err := client.RebuildIndex(authorized(ctx, searcherAssertion), &qqsearchv1.RebuildIndexRequest{Confirm: true}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a searcher assertion rebuilding: got %v, want PERMISSION_DENIED", err)
	}

	// The rebuilt record is still searchable, so the rebuild did not drop it.
	after, err := client.SearchQQMessages(searchContext, &qqsearchv1.SearchQQMessagesRequest{Query: token, TopK: 10})
	if err != nil {
		t.Fatalf("SearchQQMessages after the rebuild: %v", err)
	}
	stillFound := false
	for _, hit := range after.GetHits() {
		if hit.GetRecordId() == recordID {
			stillFound = true
		}
	}
	if !stillFound {
		t.Fatalf("the rebuilt message is not searchable: %+v", after.GetHits())
	}
}
