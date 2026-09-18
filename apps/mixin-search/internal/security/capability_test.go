package security

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	testKey      = "0123456789abcdef0123456789abcdef"
	testAudience = "mixin-search"
)

// fixedClock pins token timestamps so a minted token is a stable test vector.
func fixedClock() time.Time {
	return time.Unix(1_760_000_000, 0).UTC()
}

func testVerifier(t *testing.T) *Verifier {
	t.Helper()
	verifier, err := NewVerifier([]byte(testKey), "go-web", testAudience, WithClock(fixedClock))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return verifier
}

func testIssuer(t *testing.T, ttl time.Duration) *Issuer {
	t.Helper()
	issuer, err := NewIssuer([]byte(testKey), "go-web", testAudience, ttl, WithClock(fixedClock))
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	return issuer
}

// TestVerifyStoresTheCanonicalRole covers a credential that verifies and then
// fails every role lookup: the payload validator trims a role before accepting
// it, so the identity it produces has to carry the trimmed value too, or the
// caller sees a permission error for a token this boundary just approved.
func TestVerifyStoresTheCanonicalRole(t *testing.T) {
	t.Parallel()

	claims := Claims{
		Version:   TokenVersion,
		Issuer:    "go-web",
		Subject:   "go-web-shadow-search",
		Audience:  testAudience,
		Role:      " " + RoleSearcher + " ",
		IssuedAt:  fixedClock().Unix(),
		ExpiresAt: fixedClock().Add(time.Minute).Unix(),
	}
	token, err := Sign(claims, []byte(testKey))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	identity, err := testVerifier(t).Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if identity.Role != RoleSearcher {
		t.Fatalf("identity role = %q, want %q", identity.Role, RoleSearcher)
	}
}

func TestSignAndVerifyRoundTrip(t *testing.T) {
	t.Parallel()

	issuer := testIssuer(t, 5*time.Minute)
	token, err := issuer.Issue("go-web-shadow-search", RoleSearcher, "42",
		[]string{"space-b", "space-a", "space-a"}, []string{"doc-1"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	identity, err := testVerifier(t).Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if identity.CallerID != "go-web-shadow-search" {
		t.Fatalf("caller = %q", identity.CallerID)
	}
	if identity.Role != RoleSearcher {
		t.Fatalf("role = %q", identity.Role)
	}
	if identity.UserID != "42" {
		t.Fatalf("user = %q", identity.UserID)
	}
	// Scope is normalised: sorted and deduplicated, so a token's meaning never
	// depends on the order the issuer happened to use.
	if got := strings.Join(identity.GrantedSpaceIDs, ","); got != "space-a,space-b" {
		t.Fatalf("granted spaces = %q", got)
	}
	if got := strings.Join(identity.GrantedDocumentIDs, ","); got != "doc-1" {
		t.Fatalf("granted documents = %q", got)
	}
}

// goldenToken is the byte-exact token for testKey, testAudience, fixedClock and
// the claims built in TestGoldenCapabilityVector. gin-backend's client tests
// assert the same constant, so any format change on either side fails on both.
const goldenToken = "eyJ2ZXJzaW9uIjoxLCJpc3N1ZXIiOiJnby13ZWIiLCJzdWJqZWN0IjoiZ28td2ViLXNoYWRvdy1zZWFyY2giLCJhdWRpZW5jZSI6Im1peGluLXNlYXJjaCIsInJvbGUiOiJzZWFyY2hlciIsInVzZXJfaWQiOiI0MiIsImlzc3VlZF9hdCI6MTc2MDAwMDAwMCwiZXhwaXJlc19hdCI6MTc2MDAwMDMwMCwiYWxsb3dlZF9zcGFjZV9pZHMiOlsic3BhY2UtYSJdLCJhbGxvd2VkX2RvY3VtZW50X2lkcyI6WyJkb2MtMSJdfQ.AHuUu3qHLvyZbdkt5_687OiIjPwU0Ry9brIoURQGB-g"

func TestGoldenCapabilityVector(t *testing.T) {
	t.Parallel()

	issuer := testIssuer(t, 5*time.Minute)
	token, err := issuer.Issue("go-web-shadow-search", RoleSearcher, "42", []string{"space-a"}, []string{"doc-1"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if token != goldenToken {
		t.Fatalf("minted token drifted from the shared vector:\n got %s\nwant %s", token, goldenToken)
	}

	identity, err := testVerifier(t).Verify(goldenToken)
	if err != nil {
		t.Fatalf("Verify(golden): %v", err)
	}
	if identity.CallerID != "go-web-shadow-search" || identity.UserID != "42" {
		t.Fatalf("golden identity = %+v", identity)
	}
}

func TestIssueIgnoresScopeForNonSearcherRoles(t *testing.T) {
	t.Parallel()

	issuer := testIssuer(t, time.Minute)
	token, err := issuer.Issue("go-web-index-worker", RoleIndexWriter, "", []string{"space-a"}, []string{"doc-1"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	identity, err := testVerifier(t).Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// An index writer must not carry a search scope: its permission is the role,
	// so a leaked write capability can never be replayed as a scoped search.
	if len(identity.GrantedSpaceIDs) != 0 || len(identity.GrantedDocumentIDs) != 0 {
		t.Fatalf("index-writer identity carries scope: %+v", identity)
	}
}

func TestVerifyRejectsUntrustedTokens(t *testing.T) {
	t.Parallel()

	issuer := testIssuer(t, 5*time.Minute)
	valid, err := issuer.Issue("go-web-shadow-search", RoleSearcher, "", []string{"space-a"}, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	cases := []struct {
		name  string
		token string
		want  error
	}{
		{name: "empty", token: "", want: ErrMalformedToken},
		{name: "no separator", token: "abcdef", want: ErrMalformedToken},
		{name: "empty signature", token: "abcdef.", want: ErrMalformedToken},
		{name: "not base64url", token: "!!!.AAAA", want: ErrInvalidSignature},
		{name: "truncated", token: valid[:len(valid)-4], want: ErrInvalidSignature},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if _, err := testVerifier(t).Verify(testCase.token); !errors.Is(err, testCase.want) {
				t.Fatalf("Verify(%q) error = %v, want %v", testCase.token, err, testCase.want)
			}
		})
	}
}

func TestVerifyRejectsForeignSignature(t *testing.T) {
	t.Parallel()

	otherIssuer, err := NewIssuer([]byte("ffffffffffffffffffffffffffffffff"), "go-web", testAudience, time.Minute, WithClock(fixedClock))
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	token, err := otherIssuer.Issue("go-web-shadow-search", RoleSearcher, "", nil, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := testVerifier(t).Verify(token); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("Verify(token from another key) error = %v, want ErrInvalidSignature", err)
	}
}

func TestVerifyRejectsWrongIssuer(t *testing.T) {
	t.Parallel()

	otherIssuer, err := NewIssuer([]byte(testKey), "other-service", testAudience, time.Minute, WithClock(fixedClock))
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	token, err := otherIssuer.Issue("other-caller", RoleSearcher, "", nil, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := testVerifier(t).Verify(token); !errors.Is(err, ErrWrongIssuer) {
		t.Fatalf("Verify(token from another issuer) error = %v, want ErrWrongIssuer", err)
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	t.Parallel()

	token, err := testIssuer(t, time.Minute).Issue("go-web-shadow-search", RoleSearcher, "", nil, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// The issuer clock is fixed; verifying well past expiry plus leeway must fail.
	late, err := NewVerifier([]byte(testKey), "go-web", testAudience, WithClock(func() time.Time {
		return fixedClock().Add(2 * time.Minute)
	}))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if _, err := late.Verify(token); !errors.Is(err, ErrExpiredToken) {
		t.Fatalf("Verify(expired) error = %v, want ErrExpiredToken", err)
	}
}

func TestVerifyRejectsFutureToken(t *testing.T) {
	t.Parallel()

	token, err := testIssuer(t, 5*time.Minute).Issue("go-web-shadow-search", RoleSearcher, "", nil, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	early, err := NewVerifier([]byte(testKey), "go-web", testAudience, WithClock(func() time.Time {
		return fixedClock().Add(-10 * time.Minute)
	}), WithLeeway(0))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if _, err := early.Verify(token); !errors.Is(err, ErrMalformedToken) {
		t.Fatalf("Verify(future) error = %v, want ErrMalformedToken", err)
	}
}

func TestVerifyRejectsWrongAudience(t *testing.T) {
	t.Parallel()

	token, err := testIssuer(t, time.Minute).Issue("go-web-shadow-search", RoleSearcher, "", nil, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	other, err := NewVerifier([]byte(testKey), "go-web", "py-agent", WithClock(fixedClock))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if _, err := other.Verify(token); !errors.Is(err, ErrWrongAudience) {
		t.Fatalf("Verify(wrong audience) error = %v, want ErrWrongAudience", err)
	}
}

func TestClaimsValidation(t *testing.T) {
	t.Parallel()

	base := Claims{
		Version: TokenVersion, Issuer: "go-web", Subject: "caller", Audience: testAudience,
		Role: RoleOps, IssuedAt: fixedClock().Unix(), ExpiresAt: fixedClock().Add(time.Minute).Unix(),
	}
	cases := []struct {
		name   string
		mutate func(*Claims)
		want   error
	}{
		{name: "unknown version", mutate: func(c *Claims) { c.Version = 2 }, want: ErrUnsupportedVersion},
		{name: "missing issuer", mutate: func(c *Claims) { c.Issuer = " " }, want: ErrMalformedToken},
		{name: "missing subject", mutate: func(c *Claims) { c.Subject = " " }, want: ErrMalformedToken},
		{name: "missing audience", mutate: func(c *Claims) { c.Audience = "" }, want: ErrMalformedToken},
		{name: "unknown role", mutate: func(c *Claims) { c.Role = "root" }, want: ErrUnknownRole},
		{name: "expiry before issue", mutate: func(c *Claims) { c.ExpiresAt = c.IssuedAt }, want: ErrMalformedToken},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			claims := base
			testCase.mutate(&claims)
			if _, err := Sign(claims, []byte(testKey)); !errors.Is(err, testCase.want) {
				t.Fatalf("Sign error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestSignRejectsShortKey(t *testing.T) {
	t.Parallel()

	claims := Claims{
		Version: TokenVersion, Issuer: "go-web", Subject: "caller", Audience: testAudience,
		Role: RoleOps, IssuedAt: fixedClock().Unix(), ExpiresAt: fixedClock().Add(time.Minute).Unix(),
	}
	if _, err := Sign(claims, []byte("short")); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("Sign with a short key error = %v, want ErrInvalidKey", err)
	}
	if _, err := NewVerifier([]byte("short"), "go-web", testAudience); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("NewVerifier with a short key error = %v, want ErrInvalidKey", err)
	}
}

func TestAllowsIsContainment(t *testing.T) {
	t.Parallel()

	identity := Identity{
		CallerID:           "go-web-shadow-search",
		Role:               RoleSearcher,
		GrantedSpaceIDs:    []string{"space-a", "space-b"},
		GrantedDocumentIDs: []string{"doc-1"},
	}

	allowed := []struct {
		name      string
		spaces    []string
		documents []string
	}{
		{name: "exact grant", spaces: []string{"space-a", "space-b"}, documents: []string{"doc-1"}},
		{name: "subset of spaces", spaces: []string{"space-a"}},
		{name: "public only", spaces: nil, documents: nil},
		{name: "empty entries are ignored", spaces: []string{"", "  "}},
		{name: "order and duplicates do not matter", spaces: []string{"space-b", "space-a", "space-a"}},
	}
	for _, testCase := range allowed {
		testCase := testCase
		t.Run("allows "+testCase.name, func(t *testing.T) {
			t.Parallel()
			if err := identity.Allows(testCase.spaces, testCase.documents); err != nil {
				t.Fatalf("Allows = %v, want nil", err)
			}
		})
	}

	denied := []struct {
		name      string
		spaces    []string
		documents []string
	}{
		{name: "unknown space", spaces: []string{"space-c"}},
		{name: "unknown document", documents: []string{"doc-2"}},
		{name: "one extra space among granted ones", spaces: []string{"space-a", "space-c"}},
	}
	for _, testCase := range denied {
		testCase := testCase
		t.Run("denies "+testCase.name, func(t *testing.T) {
			t.Parallel()
			if err := identity.Allows(testCase.spaces, testCase.documents); !errors.Is(err, ErrScopeExceeded) {
				t.Fatalf("Allows = %v, want ErrScopeExceeded", err)
			}
		})
	}
}

func TestAllowsDeniesEverythingWithoutAGrant(t *testing.T) {
	t.Parallel()

	identity := Identity{CallerID: "py-agent", Role: RoleSearcher}
	if err := identity.Allows(nil, nil); err != nil {
		t.Fatalf("public-only search with no grant = %v, want nil", err)
	}
	if err := identity.Allows([]string{"space-a"}, nil); !errors.Is(err, ErrScopeExceeded) {
		t.Fatalf("scoped search without a grant = %v, want ErrScopeExceeded", err)
	}
}

func TestParseRole(t *testing.T) {
	t.Parallel()

	for _, role := range []string{RoleIndexWriter, RoleSearcher, RoleOps} {
		if _, err := ParseRole(" " + role + " "); err != nil {
			t.Fatalf("ParseRole(%q) = %v", role, err)
		}
	}
	if _, err := ParseRole("admin"); !errors.Is(err, ErrUnknownRole) {
		t.Fatalf("ParseRole(admin) error = %v, want ErrUnknownRole", err)
	}
}
