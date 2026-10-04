package application

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"document-service/internal/domain"

	documentv1 "packages/gen/document/v1"
	"packages/serviceauth"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These tests exercise the business seams that do not need a database: capability
// minting, caller resolution, conversation merging and the listing cursor. They
// live in the application package because they drive unexported seams directly.

// testClock is a fixed instant used by both the issuer and the verifier. Sharing it
// matters: a capability is only valid inside its own window, so a test that pins the
// minting clock must pin the validating clock to the same instant instead of relying
// on the machine clock.
var testClockAt = time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)

func testClock() time.Time { return testClockAt }

func testBoundaryKey() []byte {
	return bytes.Repeat([]byte("document-service-test-boundary"), 2)
}

// mustCodec builds a codec that reads the same clock the test pins the service to.
func mustCodec(t *testing.T, key []byte) *serviceauth.Codec {
	t.Helper()
	codec, err := serviceauth.NewCodec(key, serviceauth.WithClock(testClock))
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	return codec
}

// capabilityService builds a Service with only the fields capability minting
// needs. Production assembly goes through New, which also opens the database.
func capabilityService(t *testing.T) (*Service, []byte) {
	t.Helper()
	key := testBoundaryKey()
	codec := mustCodec(t, key)
	return &Service{
		codec:  codec,
		now:    testClock,
		newID:  func() string { return "id" },
		capTTL: 2 * time.Minute,
	}, key
}

func grantedResolution(t *testing.T) domain.Resolution {
	t.Helper()
	resolution, err := domain.GrantedResolution(
		"qq:user:10001/20002",
		[]string{"11111111-1111-4111-8111-111111111111"},
		"22222222-2222-4222-8222-222222222222",
		[]string{"33333333-3333-4333-8333-333333333333"},
		[]string{"44444444-4444-4444-8444-444444444444"},
	)
	if err != nil {
		t.Fatalf("GrantedResolution: %v", err)
	}
	return resolution
}

func TestMintSearchCapabilitySignsTheResolvedEnvelope(t *testing.T) {
	service, key := capabilityService(t)
	resolution := grantedResolution(t)

	capability, err := service.MintSearchCapability(resolution, SearchCapabilityRequest{})
	if err != nil {
		t.Fatalf("MintSearchCapability: %v", err)
	}

	claims := capability.Claims
	if claims.Issuer != serviceauth.CallerDocumentService {
		t.Fatalf("issuer = %q", claims.Issuer)
	}
	if claims.Audience != serviceauth.AudienceDocumentSearch {
		t.Fatalf("audience = %q", claims.Audience)
	}
	if len(claims.Scopes) != 1 || claims.Scopes[0] != string(serviceauth.ScopeDocumentSearcher) {
		t.Fatalf("scopes = %v", claims.Scopes)
	}
	if claims.SubjectKey != "qq:user:10001/20002" {
		t.Fatalf("subject = %q", claims.SubjectKey)
	}
	if !claims.AuthenticatedPublic {
		t.Fatal("a granted capability must allow authenticated public documents")
	}
	if claims.IssuedAt != testClockAt.Unix() {
		t.Fatalf("issued_at = %d, want %d", claims.IssuedAt, testClockAt.Unix())
	}
	if claims.ExpiresAt != testClockAt.Add(2*time.Minute).Unix() {
		t.Fatalf("expires_at = %d, want the configured TTL", claims.ExpiresAt)
	}

	// A verifier is a separate component holding the same boundary key and pinned to
	// the audience of the search service.
	verifier := mustCodec(t, key)
	seal, err := verifier.OpenCapability(capability.Token, serviceauth.AudienceDocumentSearch)
	if err != nil {
		t.Fatalf("OpenCapability for document-search: %v", err)
	}
	if !serviceauth.ContainsAll(seal.Claims.AllowedSpaceIDs, resolution.MemberSpaceIDs) ||
		!serviceauth.ContainsAll(resolution.MemberSpaceIDs, seal.Claims.AllowedSpaceIDs) {
		t.Fatalf("allowed spaces = %v, want the resolved envelope %v",
			seal.Claims.AllowedSpaceIDs, resolution.MemberSpaceIDs)
	}
	if seal.Claims.CurrentTeamSpaceID != resolution.CurrentTeam {
		t.Fatalf("current team = %q, want %q", seal.Claims.CurrentTeamSpaceID, resolution.CurrentTeam)
	}
	if !serviceauth.ContainsAll(seal.Claims.PrivateSpaceIDs, resolution.Private) ||
		!serviceauth.ContainsAll(seal.Claims.OtherTeamSpaceIDs, resolution.OtherTeams) {
		t.Fatalf("labels do not match the resolution: %+v", seal.Claims)
	}
	if !serviceauth.ContainsAll(seal.Claims.AllowedDocumentIDs, resolution.DocumentIDs) {
		t.Fatalf("allowed documents = %v, want %v", seal.Claims.AllowedDocumentIDs, resolution.DocumentIDs)
	}

	// The same token must not be usable as an assertion, nor at another audience.
	if _, err := verifier.OpenAssertion(capability.Token, serviceauth.AudienceDocumentSearch); err == nil {
		t.Fatal("a capability must not open as a service assertion")
	}
	if _, err := verifier.OpenCapability(capability.Token, serviceauth.AudienceDocumentService); !errors.Is(err, serviceauth.ErrAudienceMismatch) {
		t.Fatalf("err = %v, want an audience mismatch", err)
	}
}

func TestMintSearchCapabilityRefusesADenial(t *testing.T) {
	service, _ := capabilityService(t)
	denied := domain.DeniedResolution("web:user:1", domain.DeniedGroupUnbound)

	capability, err := service.MintSearchCapability(denied, SearchCapabilityRequest{})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("err = %v, want a forbidden error", err)
	}
	if capability != nil {
		t.Fatal("a denied resolution must not produce a capability")
	}
}

func TestMintSearchCapabilityNarrowsButNeverWidens(t *testing.T) {
	service, _ := capabilityService(t)
	resolution := grantedResolution(t)

	narrowed, err := service.MintSearchCapability(resolution, SearchCapabilityRequest{
		RequestedSpaceIDs: []string{"22222222-2222-4222-8222-222222222222"},
	})
	if err != nil {
		t.Fatalf("narrowing to the current team: %v", err)
	}
	if len(narrowed.Claims.AllowedSpaceIDs) != 1 {
		t.Fatalf("allowed spaces = %v, want exactly the requested one", narrowed.Claims.AllowedSpaceIDs)
	}
	if len(narrowed.Claims.PrivateSpaceIDs) != 0 || len(narrowed.Claims.OtherTeamSpaceIDs) != 0 {
		t.Fatalf("a narrowed capability must not keep the dropped labels: %+v", narrowed.Claims)
	}
	if narrowed.Claims.CurrentTeamSpaceID != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("current team = %q", narrowed.Claims.CurrentTeamSpaceID)
	}

	// Dropping the current team must not move it to another label.
	withoutCurrent, err := service.MintSearchCapability(resolution, SearchCapabilityRequest{
		RequestedSpaceIDs: []string{"33333333-3333-4333-8333-333333333333"},
	})
	if err != nil {
		t.Fatalf("narrowing to an other team: %v", err)
	}
	if withoutCurrent.Claims.CurrentTeamSpaceID != "" {
		t.Fatalf("current team = %q, want empty", withoutCurrent.Claims.CurrentTeamSpaceID)
	}
	if len(withoutCurrent.Claims.OtherTeamSpaceIDs) != 1 {
		t.Fatalf("other teams = %v", withoutCurrent.Claims.OtherTeamSpaceIDs)
	}

	// An identifier outside the envelope rejects the whole request: nothing is
	// trimmed, because trimming would confirm which identifiers exist.
	if _, err := service.MintSearchCapability(resolution, SearchCapabilityRequest{
		RequestedSpaceIDs: []string{"99999999-9999-4999-8999-999999999999"},
	}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("outside envelope err = %v, want a forbidden error", err)
	}
	if _, err := service.MintSearchCapability(resolution, SearchCapabilityRequest{
		RequestedSpaceIDs: []string{"22222222-2222-4222-8222-222222222222", "99999999-9999-4999-8999-999999999999"},
	}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("partially outside err = %v, want the whole request refused", err)
	}

	// Document level grants narrow the same way.
	if _, err := service.MintSearchCapability(resolution, SearchCapabilityRequest{
		RequestedDocumentIDs: []string{"55555555-5555-4555-8555-555555555555"},
	}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("unknown document err = %v, want a forbidden error", err)
	}
	grantedDocument, err := service.MintSearchCapability(resolution, SearchCapabilityRequest{
		RequestedDocumentIDs: []string{"44444444-4444-4444-8444-444444444444"},
	})
	if err != nil {
		t.Fatalf("narrowing to the granted document: %v", err)
	}
	if len(grantedDocument.Claims.AllowedDocumentIDs) != 1 {
		t.Fatalf("allowed documents = %v", grantedDocument.Claims.AllowedDocumentIDs)
	}

	// An empty request asks for the whole envelope.
	whole, err := service.MintSearchCapability(resolution, SearchCapabilityRequest{})
	if err != nil {
		t.Fatalf("whole envelope: %v", err)
	}
	if len(whole.Claims.AllowedSpaceIDs) != len(resolution.MemberSpaceIDs) {
		t.Fatalf("allowed spaces = %v, want %v", whole.Claims.AllowedSpaceIDs, resolution.MemberSpaceIDs)
	}
}

func TestVerifyCallerTakesIdentityFromTheAssertion(t *testing.T) {
	service := &Service{}

	principal := &serviceauth.Principal{
		Caller:     serviceauth.CallerGoWeb,
		Audience:   serviceauth.AudienceDocumentService,
		SubjectKey: "web:user:a-1",
	}
	caller, err := service.verifyCaller(principal, "body-request-id")
	if err != nil {
		t.Fatalf("verifyCaller: %v", err)
	}
	if caller.Subject.Key != "web:user:a-1" || caller.Subject.Origin != domain.OriginWeb {
		t.Fatalf("subject = %+v", caller.Subject)
	}
	if caller.Actor != "go-web" {
		t.Fatalf("actor = %q, want the calling service when no operator is named", caller.Actor)
	}
	if caller.Source != "go-web-api" {
		t.Fatalf("source = %q", caller.Source)
	}
	if caller.RequestID != "body-request-id" {
		t.Fatalf("request id = %q, want the body field when the assertion carried none", caller.RequestID)
	}

	// The asserted request id is trusted material and wins over the body.
	principal.RequestID = "assertion-request-id"
	principal.Actor = "operator-7"
	caller, err = service.verifyCaller(principal, "body-request-id")
	if err != nil {
		t.Fatalf("verifyCaller: %v", err)
	}
	if caller.RequestID != "assertion-request-id" {
		t.Fatalf("request id = %q, want the asserted one", caller.RequestID)
	}
	if caller.Actor != "operator-7" {
		t.Fatalf("actor = %q", caller.Actor)
	}

	// spacectl keeps its own audit source.
	spacectl := &serviceauth.Principal{Caller: serviceauth.CallerSpacectl, SubjectKey: "web:user:a-1"}
	caller, err = service.verifyCaller(spacectl, "")
	if err != nil {
		t.Fatalf("verifyCaller: %v", err)
	}
	if caller.Source != "spacectl" || caller.Actor != "spacectl" {
		t.Fatalf("spacectl identity = (%q,%q)", caller.Source, caller.Actor)
	}
}

func TestVerifyCallerRefusesMissingIdentity(t *testing.T) {
	service := &Service{}

	if _, err := service.verifyCaller(nil, ""); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("nil principal: code = %v, want Unauthenticated", status.Code(err))
	}
	if _, err := service.verifyCaller(&serviceauth.Principal{Caller: serviceauth.CallerGoWeb}, ""); !errors.Is(err, serviceauth.ErrSubjectNotAllowed) {
		t.Fatalf("missing subject: err = %v", err)
	}
	if _, err := service.verifyCaller(&serviceauth.Principal{
		Caller:     serviceauth.CallerGoWeb,
		SubjectKey: "web:robot:1",
	}, ""); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("malformed subject: err = %v", err)
	}
}

func TestResolveConversationMergesAssertionAndRequest(t *testing.T) {
	private := &documentv1.ConversationContext{Kind: documentv1.ConversationKind_CONVERSATION_KIND_PRIVATE}
	groupOne := &documentv1.ConversationContext{
		Kind:            documentv1.ConversationKind_CONVERSATION_KIND_GROUP,
		ExternalGroupId: "20002",
	}
	groupTwo := &documentv1.ConversationContext{
		Kind:            documentv1.ConversationKind_CONVERSATION_KIND_GROUP,
		ExternalGroupId: "20003",
	}

	merged, err := resolveConversation(&serviceauth.Conversation{
		Kind:            serviceauth.ConversationGroup,
		ExternalGroupID: "20002",
	}, groupOne)
	if err != nil {
		t.Fatalf("agreeing contexts: %v", err)
	}
	if !merged.IsGroup() || merged.GroupID() != "20002" {
		t.Fatalf("merged = %+v", merged)
	}

	if _, err := resolveConversation(&serviceauth.Conversation{
		Kind:            serviceauth.ConversationGroup,
		ExternalGroupID: "20002",
	}, groupTwo); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("disagreeing contexts: err = %v, want invalid input", err)
	}

	fromAssertion, err := resolveConversation(&serviceauth.Conversation{Kind: serviceauth.ConversationPrivate}, nil)
	if err != nil || fromAssertion.Kind != domain.ConversationPrivate {
		t.Fatalf("assertion only = (%+v,%v)", fromAssertion, err)
	}
	fromRequest, err := resolveConversation(nil, private)
	if err != nil || fromRequest.Kind != domain.ConversationPrivate {
		t.Fatalf("request only = (%+v,%v)", fromRequest, err)
	}
	if _, err := resolveConversation(nil, nil); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("no context at all: err = %v, want invalid input", err)
	}
	if _, err := resolveConversation(nil, &documentv1.ConversationContext{}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("unspecified kind: err = %v, want invalid input", err)
	}
}

func TestPageTokenRoundTrip(t *testing.T) {
	createdAt := testClockAt.Add(-3 * time.Second)
	token := encodePageToken(createdAt, "44444444-4444-4444-8444-444444444444")
	decodedAt, decodedID, err := decodePageToken(token)
	if err != nil {
		t.Fatalf("decodePageToken: %v", err)
	}
	if !decodedAt.Equal(createdAt) {
		t.Fatalf("created at = %v, want %v", decodedAt, createdAt)
	}
	if decodedID != "44444444-4444-4444-8444-444444444444" {
		t.Fatalf("document id = %q", decodedID)
	}

	if _, _, err := decodePageToken(""); err != nil {
		t.Fatalf("empty token must mean the first page: %v", err)
	}
	if _, _, err := decodePageToken("not-base64!!"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("malformed token: err = %v, want invalid input", err)
	}
	if _, _, err := decodePageToken("aGVsbG8"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("token without a separator: err = %v, want invalid input", err)
	}
}
