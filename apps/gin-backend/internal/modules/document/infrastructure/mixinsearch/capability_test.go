package mixinsearch

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testCapabilityKey = "0123456789abcdef0123456789abcdef"
	testAudience      = "mixin-search"

	// goldenToken is the same byte-exact vector asserted by
	// apps/mixin-search/internal/security. Both sides must agree on the format,
	// so a change on either side fails on both.
	goldenToken = "eyJ2ZXJzaW9uIjoxLCJpc3N1ZXIiOiJnby13ZWIiLCJzdWJqZWN0IjoiZ28td2ViLXNoYWRvdy1zZWFyY2giLCJhdWRpZW5jZSI6Im1peGluLXNlYXJjaCIsInJvbGUiOiJzZWFyY2hlciIsInVzZXJfaWQiOiI0MiIsImlzc3VlZF9hdCI6MTc2MDAwMDAwMCwiZXhwaXJlc19hdCI6MTc2MDAwMDMwMCwiYWxsb3dlZF9zcGFjZV9pZHMiOlsic3BhY2UtYSJdLCJhbGxvd2VkX2RvY3VtZW50X2lkcyI6WyJkb2MtMSJdfQ.AHuUu3qHLvyZbdkt5_687OiIjPwU0Ry9brIoURQGB-g"
)

func fixedClock() time.Time {
	return time.Unix(1_760_000_000, 0).UTC()
}

func newTestIssuer(t *testing.T, subject string) *CapabilityIssuer {
	t.Helper()
	issuer, err := NewCapabilityIssuer([]byte(testCapabilityKey), "go-web", subject, testAudience, 5*time.Minute)
	if err != nil {
		t.Fatalf("NewCapabilityIssuer: %v", err)
	}
	issuer.now = fixedClock
	return issuer
}

func decodeClaims(t *testing.T, token string) capabilityClaims {
	t.Helper()
	encoded, _, found := strings.Cut(token, ".")
	if !found {
		t.Fatalf("capability %q has no signature separator", token)
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode capability payload: %v", err)
	}
	var claims capabilityClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal capability payload: %v", err)
	}
	return claims
}

func TestMintedSearchTokenMatchesSharedVector(t *testing.T) {
	t.Parallel()

	issuer := newTestIssuer(t, "go-web-shadow-search")
	token, err := issuer.SearchToken("42", []string{"space-a"}, []string{"doc-1"})
	if err != nil {
		t.Fatalf("SearchToken: %v", err)
	}
	if token != goldenToken {
		t.Fatalf("minted token drifted from the shared vector:\n got %s\nwant %s", token, goldenToken)
	}
}

func TestSearchTokenCarriesTheGrantedScope(t *testing.T) {
	t.Parallel()

	issuer := newTestIssuer(t, "go-web-shadow-search")
	token, err := issuer.SearchToken("42", []string{" space-b ", "space-a", "space-a", ""}, nil)
	if err != nil {
		t.Fatalf("SearchToken: %v", err)
	}
	claims := decodeClaims(t, token)
	if claims.Version != capabilityVersion {
		t.Fatalf("version = %d", claims.Version)
	}
	if claims.Subject != "go-web-shadow-search" || claims.UserID != "42" || claims.Role != RoleSearcher {
		t.Fatalf("claims = %+v", claims)
	}
	// Normalisation must match the verifier's, or a legitimate request would be
	// rejected as out of scope because of ordering alone.
	if strings.Join(claims.AllowedSpaceIDs, ",") != "space-a,space-b" {
		t.Fatalf("granted spaces = %v", claims.AllowedSpaceIDs)
	}
	if claims.ExpiresAt-claims.IssuedAt != int64((5 * time.Minute).Seconds()) {
		t.Fatalf("lifetime = %d seconds", claims.ExpiresAt-claims.IssuedAt)
	}
}

func TestIndexTokenCarriesNoSearchScope(t *testing.T) {
	t.Parallel()

	issuer := newTestIssuer(t, "go-web-index-worker")
	token, err := issuer.IndexToken()
	if err != nil {
		t.Fatalf("IndexToken: %v", err)
	}
	claims := decodeClaims(t, token)
	if claims.Role != RoleIndexWriter {
		t.Fatalf("role = %q, want %q", claims.Role, RoleIndexWriter)
	}
	// A write capability must never be replayable as a scoped search.
	if len(claims.AllowedSpaceIDs) != 0 || len(claims.AllowedDocumentIDs) != 0 {
		t.Fatalf("index token carries scope: %+v", claims)
	}
}

func TestNewCapabilityIssuerValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		key       []byte
		issuer    string
		subject   string
		audience  string
		ttl       time.Duration
		wantError bool
	}{
		{name: "valid", key: []byte(testCapabilityKey), issuer: "go-web", subject: "caller", audience: testAudience, ttl: time.Minute},
		{name: "short key", key: []byte("short"), issuer: "go-web", subject: "caller", audience: testAudience, ttl: time.Minute, wantError: true},
		{name: "missing issuer", key: []byte(testCapabilityKey), subject: "caller", audience: testAudience, ttl: time.Minute, wantError: true},
		{name: "missing subject", key: []byte(testCapabilityKey), issuer: "go-web", audience: testAudience, ttl: time.Minute, wantError: true},
		{name: "missing audience", key: []byte(testCapabilityKey), issuer: "go-web", subject: "caller", ttl: time.Minute, wantError: true},
		{name: "zero ttl", key: []byte(testCapabilityKey), issuer: "go-web", subject: "caller", audience: testAudience, wantError: true},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewCapabilityIssuer(testCase.key, testCase.issuer, testCase.subject, testCase.audience, testCase.ttl)
			if testCase.wantError && !errors.Is(err, ErrCapabilityKey) {
				t.Fatalf("error = %v, want ErrCapabilityKey", err)
			}
			if !testCase.wantError && err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
		})
	}
}

func TestLoadCapabilityKey(t *testing.T) {
	t.Parallel()

	t.Run("missing path", func(t *testing.T) {
		t.Parallel()
		if _, err := LoadCapabilityKey(""); !errors.Is(err, ErrCapabilityKey) {
			t.Fatalf("error = %v, want ErrCapabilityKey", err)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		if _, err := LoadCapabilityKey(filepath.Join(t.TempDir(), "absent.key")); !errors.Is(err, ErrCapabilityKey) {
			t.Fatalf("error = %v, want ErrCapabilityKey", err)
		}
	})

	t.Run("short file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "short.key")
		if err := os.WriteFile(path, []byte("too-short"), 0o600); err != nil {
			t.Fatalf("write key: %v", err)
		}
		if _, err := LoadCapabilityKey(path); !errors.Is(err, ErrCapabilityKey) {
			t.Fatalf("error = %v, want ErrCapabilityKey", err)
		}
	})

	t.Run("valid file is trimmed", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "capability.key")
		if err := os.WriteFile(path, []byte("  "+testCapabilityKey+"\n"), 0o600); err != nil {
			t.Fatalf("write key: %v", err)
		}
		key, err := LoadCapabilityKey(path)
		if err != nil {
			t.Fatalf("LoadCapabilityKey: %v", err)
		}
		if string(key) != testCapabilityKey {
			t.Fatalf("key = %q", key)
		}
	})
}

func TestFormatUserID(t *testing.T) {
	t.Parallel()

	if got := FormatUserID(0); got != "" {
		t.Fatalf("FormatUserID(0) = %q, want an empty claim", got)
	}
	if got := FormatUserID(-1); got != "" {
		t.Fatalf("FormatUserID(-1) = %q, want an empty claim", got)
	}
	if got := FormatUserID(42); got != "42" {
		t.Fatalf("FormatUserID(42) = %q", got)
	}
}
