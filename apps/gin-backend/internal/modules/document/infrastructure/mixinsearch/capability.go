package mixinsearch

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The mixin-search boundary rejects any caller that cannot present a capability,
// so every RPC this package issues carries one. The wire format is shared with
// apps/mixin-search/internal/security and pinned by a golden vector asserted on
// both sides; see docs/contracts/SERVICE_CALL_CAPABILITY.md.
//
//	go-web signs, mixin-search verifies.
//	authorization: Bearer base64url(payload_json) "." base64url(hmac_sha256(key, payload))
const (
	capabilityVersion = 1

	// RoleIndexWriter may mutate the derived document index.
	RoleIndexWriter = "index-writer"
	// RoleSearcher may run document searches inside the granted scope.
	RoleSearcher = "searcher"
	// RoleOps may inspect index state and never searches or writes.
	RoleOps = "ops"

	// MinCapabilityKeyBytes is the shortest accepted boundary key. It matches the
	// verifier's rule so a key accepted here is never rejected there.
	MinCapabilityKeyBytes = 32
)

// ErrCapabilityKey reports a boundary key that cannot be used.
var ErrCapabilityKey = errors.New("mixin-search capability key is invalid")

type capabilityClaims struct {
	Version            int      `json:"version"`
	Issuer             string   `json:"issuer"`
	Subject            string   `json:"subject"`
	Audience           string   `json:"audience"`
	Role               string   `json:"role"`
	UserID             string   `json:"user_id,omitempty"`
	IssuedAt           int64    `json:"issued_at"`
	ExpiresAt          int64    `json:"expires_at"`
	AllowedSpaceIDs    []string `json:"allowed_space_ids,omitempty"`
	AllowedDocumentIDs []string `json:"allowed_document_ids,omitempty"`
}

// CapabilityIssuer mints short-lived capabilities for the mixin-search boundary.
type CapabilityIssuer struct {
	key      []byte
	issuer   string
	subject  string
	audience string
	ttl      time.Duration
	now      func() time.Time
}

// LoadCapabilityKey reads the shared boundary key from disk. The key is a file
// rather than a configuration value so it never appears in committed YAML, in a
// process command line or in a configuration dump.
func LoadCapabilityKey(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("%w: capability key path is empty", ErrCapabilityKey)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %w", ErrCapabilityKey, path, err)
	}
	key := []byte(strings.TrimSpace(string(contents)))
	if len(key) < MinCapabilityKeyBytes {
		return nil, fmt.Errorf(
			"%w: %s holds %d bytes, need at least %d",
			ErrCapabilityKey, path, len(key), MinCapabilityKeyBytes,
		)
	}
	return key, nil
}

// NewCapabilityIssuerFromConfig loads the boundary key and builds an issuer in
// one step, so every call site applies the same path, identity and lifetime
// rules instead of repeating the assembly.
func NewCapabilityIssuerFromConfig(keyPath, issuer, subject, audience string, ttl time.Duration) (*CapabilityIssuer, error) {
	key, err := LoadCapabilityKey(keyPath)
	if err != nil {
		return nil, err
	}
	return NewCapabilityIssuer(key, issuer, subject, audience, ttl)
}

// NewCapabilityIssuer validates the boundary credential configuration.
func NewCapabilityIssuer(key []byte, issuer, subject, audience string, ttl time.Duration) (*CapabilityIssuer, error) {
	if len(key) < MinCapabilityKeyBytes {
		return nil, fmt.Errorf("%w: key holds %d bytes, need at least %d", ErrCapabilityKey, len(key), MinCapabilityKeyBytes)
	}
	if strings.TrimSpace(issuer) == "" {
		return nil, fmt.Errorf("%w: issuer is required", ErrCapabilityKey)
	}
	if strings.TrimSpace(subject) == "" {
		return nil, fmt.Errorf("%w: caller subject is required", ErrCapabilityKey)
	}
	if strings.TrimSpace(audience) == "" {
		return nil, fmt.Errorf("%w: audience is required", ErrCapabilityKey)
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("%w: token lifetime must be positive", ErrCapabilityKey)
	}
	return &CapabilityIssuer{
		key:      append([]byte{}, key...),
		issuer:   strings.TrimSpace(issuer),
		subject:  strings.TrimSpace(subject),
		audience: strings.TrimSpace(audience),
		ttl:      ttl,
		now:      time.Now,
	}, nil
}

// IndexToken mints a capability for index mutation and index-state reads.
func (issuer *CapabilityIssuer) IndexToken() (string, error) {
	// A write capability deliberately carries no search scope: the role is the
	// permission, so a leaked index capability can never be replayed as a search.
	return issuer.mint(RoleIndexWriter, "", nil, nil)
}

// SearchToken mints a capability for a search that is bounded to the scope
// go-web has already authorised for this caller.
func (issuer *CapabilityIssuer) SearchToken(userID string, spaceIDs, documentIDs []string) (string, error) {
	return issuer.mint(RoleSearcher, userID, spaceIDs, documentIDs)
}

func (issuer *CapabilityIssuer) mint(role, userID string, spaceIDs, documentIDs []string) (string, error) {
	issuedAt := issuer.now().UTC()
	claims := capabilityClaims{
		Version:   capabilityVersion,
		Issuer:    issuer.issuer,
		Subject:   issuer.subject,
		Audience:  issuer.audience,
		Role:      role,
		UserID:    strings.TrimSpace(userID),
		IssuedAt:  issuedAt.Unix(),
		ExpiresAt: issuedAt.Add(issuer.ttl).Unix(),
	}
	if role == RoleSearcher {
		claims.AllowedSpaceIDs = normalizeCapabilityIDs(spaceIDs)
		claims.AllowedDocumentIDs = normalizeCapabilityIDs(documentIDs)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode capability claims: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, issuer.key)
	mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// FormatUserID renders an end-user id for the audit claim. Zero means "unknown"
// and is omitted rather than recorded as a misleading literal zero.
func FormatUserID(userID int64) string {
	if userID <= 0 {
		return ""
	}
	return strconv.FormatInt(userID, 10)
}

func normalizeCapabilityIDs(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, duplicate := seen[trimmed]; duplicate {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	sort.Strings(normalized)
	return normalized
}
