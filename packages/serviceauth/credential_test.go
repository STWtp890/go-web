package serviceauth

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"
)

func base64URL(raw string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func testCodec(t *testing.T) *Codec {
	t.Helper()
	codec, err := NewCodec([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	return codec
}

// TestCredentialGoldenVectors pins the exact wire bytes of both credential
// families for a fixed key, a fixed payload and a fixed clock.
//
// The vectors exist so another implementation (py-agent) can prove its encoder
// and verifier agree with this one byte for byte instead of "close enough": the
// envelope is deliberately minimal, and every field name, ordering and
// canonicalization rule is part of the contract rather than an implementation
// detail. A change to any of them must show up here.
func TestCredentialGoldenVectors(t *testing.T) {
	// 32 bytes of ASCII "k" is the fixed key of the published vector.
	codec, err := NewCodec([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	issuedAt := int64(1_800_000_000)
	expiresAt := issuedAt + 120
	// Both minting and verification read the clock; the vector pins it so the
	// bytes below are reproducible on any machine at any time.
	codec.now = func() time.Time { return time.Unix(issuedAt, 0).UTC() }

	assertion, err := codec.SealAssertion(AssertionClaims{
		Caller:       CallerGoWeb,
		Audience:     AudienceDocumentService,
		Scopes:       []string{string(ScopeDocumentWrite), string(ScopeDocumentRead)},
		SubjectKey:   "web:user:42",
		Actor:        "web-api",
		RequestID:    "req-42",
		Conversation: &Conversation{Kind: ConversationPrivate},
		IssuedAt:     issuedAt,
		ExpiresAt:    expiresAt,
	})
	if err != nil {
		t.Fatalf("SealAssertion: %v", err)
	}
	capability, err := codec.SealCapability(CapabilityClaims{
		Issuer:              CallerDocumentService,
		Audience:            AudienceDocumentSearch,
		Scopes:              []string{string(ScopeDocumentSearcher)},
		SubjectKey:          "web:user:42",
		PrivateSpaceIDs:     []string{"11111111-1111-1111-1111-111111111111"},
		AuthenticatedPublic: true,
		IssuedAt:            issuedAt,
		ExpiresAt:           expiresAt,
	})
	if err != nil {
		t.Fatalf("SealCapability: %v", err)
	}

	const (
		goldenAssertion  = "eyJ2ZXJzaW9uIjoxLCJjYWxsZXIiOiJnby13ZWIiLCJhdWRpZW5jZSI6ImRvY3VtZW50LXNlcnZpY2UiLCJzY29wZXMiOlsiZG9jdW1lbnQucmVhZCIsImRvY3VtZW50LndyaXRlIl0sInN1YmplY3Rfa2V5Ijoid2ViOnVzZXI6NDIiLCJjb252ZXJzYXRpb24iOnsia2luZCI6InByaXZhdGUifSwiYWN0b3IiOiJ3ZWItYXBpIiwicmVxdWVzdF9pZCI6InJlcS00MiIsImlzc3VlZF9hdCI6MTgwMDAwMDAwMCwiZXhwaXJlc19hdCI6MTgwMDAwMDEyMH0.D-gzsOOKQZiPrZH7u2YRhvQNmU2WMkPw8yJ8L8Rpss4"
		goldenCapability = "eyJ2ZXJzaW9uIjoxLCJpc3N1ZXIiOiJkb2N1bWVudC1zZXJ2aWNlIiwiYXVkaWVuY2UiOiJkb2N1bWVudC1zZWFyY2giLCJzY29wZXMiOlsiZG9jdW1lbnQtc2VhcmNoZXIiXSwic3ViamVjdF9rZXkiOiJ3ZWI6dXNlcjo0MiIsInByaXZhdGVfc3BhY2VfaWRzIjpbIjExMTExMTExLTExMTEtMTExMS0xMTExLTExMTExMTExMTExMSJdLCJhdXRoZW50aWNhdGVkX3B1YmxpYyI6dHJ1ZSwiaXNzdWVkX2F0IjoxODAwMDAwMDAwLCJleHBpcmVzX2F0IjoxODAwMDAwMTIwfQ.nesRevAEys4X4ESv8cW0eGWleoJOX9A9as8TTV2KqTs"
	)
	if assertion != goldenAssertion {
		t.Errorf("assertion vector changed:\n got %s\nwant %s", assertion, goldenAssertion)
	}
	if capability != goldenCapability {
		t.Errorf("capability vector changed:\n got %s\nwant %s", capability, goldenCapability)
	}

	// The vectors must open, and each must refuse the other family's boundary.
	if _, err := codec.OpenAssertion(assertion, AudienceDocumentService); err != nil {
		t.Fatalf("OpenAssertion on the published vector: %v", err)
	}
	if _, err := codec.OpenCapability(capability, AudienceDocumentSearch); err != nil {
		t.Fatalf("OpenCapability on the published vector: %v", err)
	}
	// An assertion presented as a capability fails on the payload shape, and a
	// capability presented at the other audience fails on the audience. The two
	// families share an envelope, so both checks are needed to keep them apart.
	if _, err := codec.OpenCapability(assertion, AudienceDocumentService); !errors.Is(err, ErrMalformedCredential) {
		t.Fatalf("an assertion must not open as a capability, got %v", err)
	}
	if _, err := codec.OpenCapability(capability, AudienceQQSearch); !errors.Is(err, ErrAudienceMismatch) {
		t.Fatalf("a capability must not open at another audience, got %v", err)
	}
}

func TestSealAndOpenAssertionRoundTrip(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	codec := testCodec(t)
	codec.now = func() time.Time { return now }
	token, err := codec.SealAssertion(AssertionClaims{
		Caller:       CallerGoWeb,
		Audience:     AudienceDocumentService,
		Scopes:       []string{string(ScopeDocumentWrite), string(ScopeDocumentRead)},
		SubjectKey:   "web:user:42",
		Actor:        "admin@example.com",
		RequestID:    "req-1",
		IssuedAt:     now.Unix(),
		ExpiresAt:    now.Add(2 * time.Minute).Unix(),
		Conversation: &Conversation{Kind: ConversationPrivate},
	})
	if err != nil {
		t.Fatalf("SealAssertion: %v", err)
	}
	if !strings.Contains(token, ".") {
		t.Fatalf("token must have two parts: %q", token)
	}

	seal, err := codec.OpenAssertion(token, AudienceDocumentService)
	if err != nil {
		t.Fatalf("OpenAssertion: %v", err)
	}
	if seal.Claims.Caller != CallerGoWeb || seal.Claims.SubjectKey != "web:user:42" {
		t.Fatalf("unexpected claims: %+v", seal.Claims)
	}
	if len(seal.Claims.Scopes) != 2 || seal.Claims.Scopes[0] != string(ScopeDocumentRead) {
		t.Fatalf("scopes must be canonical and sorted: %v", seal.Claims.Scopes)
	}
}

func TestOpenRejectsWrongAudience(t *testing.T) {
	codec := testCodec(t)
	now := time.Now().UTC()
	token, err := codec.SealAssertion(AssertionClaims{
		Caller: CallerPyAgent, Audience: AudienceQQSearch, SubjectKey: "qq:1:user:2",
		IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("SealAssertion: %v", err)
	}
	if _, err := codec.OpenAssertion(token, AudienceDocumentService); !errors.Is(err, ErrAudienceMismatch) {
		t.Fatalf("expected audience mismatch, got %v", err)
	}
}

func TestOpenRejectsTamperedSignatureAndPayload(t *testing.T) {
	codec := testCodec(t)
	now := time.Now().UTC()
	token, err := codec.SealAssertion(AssertionClaims{
		Caller: CallerGoWeb, Audience: AudienceDocumentService, SubjectKey: "web:user:1",
		IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("SealAssertion: %v", err)
	}
	parts := strings.Split(token, ".")
	if _, err := codec.OpenAssertion(parts[0]+"."+strings.Repeat("A", len(parts[1])), AudienceDocumentService); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("expected signature mismatch, got %v", err)
	}
	other, err := NewCodec([]byte(strings.Repeat("x", 32)))
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	if _, err := other.OpenAssertion(token, AudienceDocumentService); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("a different key must not validate the token, got %v", err)
	}
}

func TestSealRejectsNamespaceViolation(t *testing.T) {
	codec := testCodec(t)
	now := time.Now().UTC()
	_, err := codec.SealAssertion(AssertionClaims{
		Caller: CallerGoWeb, Audience: AudienceDocumentService, SubjectKey: "qq:1:user:2",
		IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(),
	})
	if !errors.Is(err, ErrNamespaceViolation) {
		t.Fatalf("go-web must not be able to assert a QQ subject, got %v", err)
	}
}

func TestSealRejectsUnregisteredScopeAndCaller(t *testing.T) {
	codec := testCodec(t)
	now := time.Now().UTC()
	if _, err := codec.SealAssertion(AssertionClaims{
		Caller: CallerGoWeb, Audience: AudienceDocumentService, Scopes: []string{"root.everything"},
		IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(),
	}); !errors.Is(err, ErrUnknownClaim) {
		t.Fatalf("unregistered scope must be rejected, got %v", err)
	}
	if _, err := codec.SealAssertion(AssertionClaims{
		Caller: "attacker", Audience: AudienceDocumentService,
		IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(),
	}); !errors.Is(err, ErrUnknownClaim) {
		t.Fatalf("unregistered caller must be rejected, got %v", err)
	}
}

func TestExpiryWindow(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	codec := testCodec(t)
	codec.now = func() time.Time { return base }
	token, err := codec.SealAssertion(AssertionClaims{
		Caller: CallerGoWeb, Audience: AudienceDocumentService, SubjectKey: "web:user:1",
		IssuedAt: base.Unix(), ExpiresAt: base.Add(30 * time.Second).Unix(),
	})
	if err != nil {
		t.Fatalf("SealAssertion: %v", err)
	}
	if _, err := codec.OpenAssertion(token, AudienceDocumentService); err != nil {
		t.Fatalf("token inside its window must validate: %v", err)
	}
	codec.now = func() time.Time { return base.Add(59 * time.Second) }
	if _, err := codec.OpenAssertion(token, AudienceDocumentService); err != nil {
		t.Fatalf("token inside the leeway window must validate: %v", err)
	}
	codec.now = func() time.Time { return base.Add(61 * time.Second) }
	if _, err := codec.OpenAssertion(token, AudienceDocumentService); !errors.Is(err, ErrExpired) {
		t.Fatalf("expected expiry, got %v", err)
	}
}

func TestOpenRejectsUnknownPayloadField(t *testing.T) {
	// A payload with an extra field must not be parsed leniently: silently
	// ignoring an unknown claim is how a widened scope gets accepted.
	codec := testCodec(t)
	raw := `{"version":1,"caller":"go-web","audience":"document-service","issued_at":1,"expires_at":2,"superuser":true}`
	encoded := base64URL(raw)
	token := encoded + "." + codec.sign(encoded)
	if _, err := codec.OpenAssertion(token, AudienceDocumentService); !errors.Is(err, ErrMalformedCredential) {
		t.Fatalf("expected malformed credential, got %v", err)
	}
}

func TestOpenCapabilityRoundTrip(t *testing.T) {
	codec := testCodec(t)
	now := time.Now().UTC()
	token, err := codec.SealCapability(CapabilityClaims{
		Issuer:              CallerDocumentService,
		Audience:            AudienceDocumentSearch,
		Scopes:              []string{string(ScopeDocumentSearcher)},
		AllowedSpaceIDs:     []string{"b", "a", "a"},
		AuthenticatedPublic: true,
		IssuedAt:            now.Unix(),
		ExpiresAt:           now.Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("SealCapability: %v", err)
	}
	seal, err := codec.OpenCapability(token, AudienceDocumentSearch)
	if err != nil {
		t.Fatalf("OpenCapability: %v", err)
	}
	if len(seal.Claims.AllowedSpaceIDs) != 2 || seal.Claims.AllowedSpaceIDs[0] != "a" {
		t.Fatalf("allowed spaces must be canonical: %v", seal.Claims.AllowedSpaceIDs)
	}
	if !seal.Claims.AuthenticatedPublic {
		t.Fatal("authenticated public flag must survive the round trip")
	}
}

// TestCapabilityIssuerIsTheFactSource pins the "only the fact source signs its
// own range" rule at both ends: a registered caller that does not own the corpus
// cannot mint its capability, and the boundary refuses the token even when the
// signature is valid, because a valid signature only proves who signed.
func TestCapabilityIssuerIsTheFactSource(t *testing.T) {
	codec := testCodec(t)
	now := time.Now().UTC()

	// The wrong issuer cannot even mint the credential.
	if _, err := codec.SealCapability(CapabilityClaims{
		Issuer:    CallerGoWeb,
		Audience:  AudienceQQSearch,
		Scopes:    []string{string(ScopeQQSearcher)},
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(time.Minute).Unix(),
	}); !errors.Is(err, ErrUnknownClaim) {
		t.Fatalf("go-web must not mint a qq-search capability, got %v", err)
	}

	// An audience with no registered issuer fails closed instead of accepting
	// whichever registered caller happened to sign.
	if _, err := codec.SealCapability(CapabilityClaims{
		Issuer:    CallerDocumentService,
		Audience:  Audience("mixin-search"),
		Scopes:    []string{string(ScopeDocumentSearcher)},
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(time.Minute).Unix(),
	}); !errors.Is(err, ErrUnknownClaim) {
		t.Fatalf("an audience with no registered issuer must be refused, got %v", err)
	}

	// A token minted by the right issuer for the right corpus still opens, and
	// the same token is refused at the other boundary.
	token, err := codec.SealCapability(CapabilityClaims{
		Issuer:           CallerPyAgent,
		Audience:         AudienceQQSearch,
		Scopes:           []string{string(ScopeQQSearcher)},
		SubjectKey:       "qq:user:10001/20002",
		BotIDs:           []string{"10001"},
		ConversationIDs:  []string{"conv-1"},
		ExternalGroupIDs: []string{"group-1"},
		IssuedAt:         now.Unix(),
		ExpiresAt:        now.Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("py-agent must mint the qq-search capability: %v", err)
	}
	if _, err := codec.OpenCapability(token, AudienceQQSearch); err != nil {
		t.Fatalf("OpenCapability at qq-search: %v", err)
	}
	if _, err := codec.OpenCapability(token, AudienceDocumentSearch); !errors.Is(err, ErrAudienceMismatch) {
		t.Fatalf("a qq-search capability must not open at document-search, got %v", err)
	}
}

func TestCapabilityIsNotAnAssertion(t *testing.T) {
	// Both families share an envelope; the audience check keeps them apart.
	codec := testCodec(t)
	now := time.Now().UTC()
	capability, err := codec.SealCapability(CapabilityClaims{
		Issuer: CallerDocumentService, Audience: AudienceDocumentSearch,
		IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("SealCapability: %v", err)
	}
	if _, err := codec.OpenAssertion(capability, AudienceDocumentSearch); !errors.Is(err, ErrMalformedCredential) {
		t.Fatalf("a capability must not open as an assertion, got %v", err)
	}
}

func TestSubjectKeys(t *testing.T) {
	web, err := WebSubjectKey("42")
	if err != nil || web != "web:user:42" {
		t.Fatalf("unexpected web subject key %q (%v)", web, err)
	}
	qq, err := QQSubjectKey("10001", "20002")
	if err != nil || qq != "qq:user:10001/20002" {
		t.Fatalf("unexpected qq subject key %q (%v)", qq, err)
	}
	if SubjectOriginOf(qq) != SubjectOriginQQ {
		t.Fatalf("origin of %q must be qq", qq)
	}
	if _, err := SubjectKey("web", "user", "a:b"); err == nil {
		t.Fatal("a subject key part containing the separator must be rejected")
	}
}

func TestContainsAll(t *testing.T) {
	granted := []string{"a", "b"}
	if !ContainsAll(granted, nil) {
		t.Fatal("an empty request is a subset of anything")
	}
	if !ContainsAll(granted, []string{"a", "a"}) {
		t.Fatal("duplicates must not change inclusion")
	}
	if ContainsAll(granted, []string{"a", "z"}) {
		t.Fatal("any identifier outside the grant must reject the whole request")
	}
	if ContainsAll(nil, []string{"a"}) {
		t.Fatal("an empty grant admits nothing")
	}
}

func TestAuthenticatorRestrictsCallersAndScopes(t *testing.T) {
	codec := testCodec(t)
	now := time.Now().UTC()
	authenticator, err := NewAuthenticator(codec, AudienceDocumentService, CallerGoWeb, CallerPyAgent)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	token, err := codec.SealAssertion(AssertionClaims{
		Caller: CallerGoWeb, Audience: AudienceDocumentService, Scopes: []string{string(ScopeDocumentWrite)},
		SubjectKey: "web:user:7", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("SealAssertion: %v", err)
	}
	principal, err := authenticator.Authenticate(token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if err := principal.RequireScope(ScopeDocumentWrite); err != nil {
		t.Fatalf("RequireScope: %v", err)
	}
	if err := principal.RequireScope(ScopeSpaceAdmin); !errors.Is(err, ErrScopeNotGranted) {
		t.Fatalf("expected scope denial, got %v", err)
	}
	if _, err := principal.RequireSubject(); err != nil {
		t.Fatalf("RequireSubject: %v", err)
	}

	spacectlToken, err := codec.SealAssertion(AssertionClaims{
		Caller: CallerSpacectl, Audience: AudienceDocumentService,
		IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("SealAssertion: %v", err)
	}
	if _, err := authenticator.Authenticate(spacectlToken); !errors.Is(err, ErrNamespaceViolation) {
		t.Fatalf("an unlisted caller must be rejected, got %v", err)
	}
}

func TestMetadataAuthentication(t *testing.T) {
	codec := testCodec(t)
	now := time.Now().UTC()
	token, err := codec.SealAssertion(AssertionClaims{
		Caller: CallerPyAgent, Audience: AudienceDocumentService, SubjectKey: "qq:user:1/2",
		Scopes:       []string{string(ScopeAccessResolve)},
		Conversation: &Conversation{Kind: ConversationGroup, ExternalGroupID: "999"},
		IssuedAt:     now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("SealAssertion: %v", err)
	}
	authenticator, err := NewAuthenticator(codec, AudienceDocumentService, CallerGoWeb, CallerPyAgent)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		HeaderAuthorization, AuthorizationHeader(token),
		HeaderRequestID, "req-9",
	))
	principal, err := authenticator.FromIncomingMetadata(ctx)
	if err != nil {
		t.Fatalf("FromIncomingMetadata: %v", err)
	}
	if principal.RequestID != "req-9" {
		t.Fatalf("request id must be propagated: %q", principal.RequestID)
	}
	conversation, err := principal.RequireConversation()
	if err != nil {
		t.Fatalf("RequireConversation: %v", err)
	}
	if conversation.Kind != ConversationGroup || conversation.ExternalGroupID != "999" {
		t.Fatalf("unexpected conversation: %+v", conversation)
	}

	if _, err := authenticator.FromIncomingMetadata(context.Background()); !errors.Is(err, ErrMalformedCredential) {
		t.Fatalf("missing metadata must be rejected, got %v", err)
	}
}

func TestPrincipalContextRoundTrip(t *testing.T) {
	principal := &Principal{Caller: CallerGoWeb, SubjectKey: "web:user:1"}
	ctx := WithPrincipal(context.Background(), principal)
	got, ok := PrincipalFrom(ctx)
	if !ok || got.SubjectKey != "web:user:1" {
		t.Fatalf("principal must survive the context round trip")
	}
	if _, ok := PrincipalFrom(context.Background()); ok {
		t.Fatal("a context without a principal must not report one")
	}
}

func TestNewCodecRejectsShortKey(t *testing.T) {
	if _, err := NewCodec([]byte("short")); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("a short boundary key must fail at construction, got %v", err)
	}
}

func TestGRPCStatusMapping(t *testing.T) {
	if got := GRPCStatus(ErrScopeNotGranted); got == nil || !strings.Contains(got.Error(), "PermissionDenied") {
		t.Fatalf("scope denial must map to PermissionDenied, got %v", got)
	}
	if got := GRPCStatus(ErrSignatureMismatch); got == nil || !strings.Contains(got.Error(), "Unauthenticated") {
		t.Fatalf("signature mismatch must map to Unauthenticated, got %v", got)
	}
	if got := GRPCStatus(ErrConfiguration); got == nil || !strings.Contains(got.Error(), "Internal") {
		t.Fatalf("configuration errors must map to Internal, got %v", got)
	}
}
