// Package security implements the service-call capability used on the
// mixin-search boundary: callers prove who they are and which document scope
// they were granted, and the service rejects any request that exceeds it.
//
// The token is a compact, self-contained HMAC-SHA256 credential:
//
//	base64url(payload_json) "." base64url(hmac_sha256(key, base64url(payload_json)))
//
// It deliberately carries no algorithm header: the only accepted algorithm is
// HMAC-SHA256 with the shared boundary key, so there is nothing for a caller to
// negotiate or downgrade. go-web signs, mixin-search verifies, and the two sides
// are pinned together by the shared format documented in
// docs/contracts/SERVICE_CALL_CAPABILITY.md.
package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// TokenVersion is the only accepted payload version.
const TokenVersion = 1

// Caller roles. A role grants a fixed set of RPCs; it never grants data scope.
//
// Roles are corpus-qualified where the corpus matters. The document roles and
// the chat roles are disjoint sets on purpose: a capability minted for document
// search must not be replayable against the chat corpus, and the reverse, so a
// single leaked capability can never span both corpora
// (see docs/adr/014-per-corpus-control-plane-isolation.md).
const (
	// RoleIndexWriter may mutate the derived document index. It is held by the
	// go-web index worker, outbox reconciler and admin tooling.
	RoleIndexWriter = "index-writer"
	// RoleSearcher may run document searches, bounded by the granted scope.
	RoleSearcher = "searcher"
	// RoleOps may inspect document index state and never searches or writes.
	RoleOps = "ops"

	// RoleChatIndexWriter may mutate the derived chat index. It is held by
	// py-agent's chat indexing path.
	RoleChatIndexWriter = "chat-index-writer"
	// RoleChatSearcher may run chat retrievals, bounded by the granted scope.
	RoleChatSearcher = "chat-searcher"
	// RoleChatOps may inspect chat index state and never searches or writes.
	RoleChatOps = "chat-ops"
)

// MinKeyBytes is the shortest accepted boundary key. HMAC-SHA256 keys shorter
// than the hash block are padded, so short keys weaken the only secret on this
// boundary; 32 bytes is the security level of the MAC itself.
const MinKeyBytes = 32

// maxTokenBytes bounds parsing work for unauthenticated input.
const maxTokenBytes = 8 << 10

var (
	// ErrMalformedToken reports a token that cannot be parsed at all.
	ErrMalformedToken = errors.New("capability token is malformed")
	// ErrUnsupportedVersion reports a payload this service does not understand.
	ErrUnsupportedVersion = errors.New("capability token version is not supported")
	// ErrInvalidSignature reports a token not signed by the boundary key.
	ErrInvalidSignature = errors.New("capability token signature is invalid")
	// ErrExpiredToken reports a token outside its validity window.
	ErrExpiredToken = errors.New("capability token is expired")
	// ErrWrongIssuer reports a token minted by an issuer this service does not trust.
	ErrWrongIssuer = errors.New("capability token issuer does not match")
	// ErrWrongAudience reports a token minted for a different service.
	ErrWrongAudience = errors.New("capability token audience does not match")
	// ErrUnknownRole reports a role this service does not define.
	ErrUnknownRole = errors.New("capability token role is unknown")
	// ErrScopeExceeded reports a request that asks for more than was granted.
	ErrScopeExceeded = errors.New("requested scope exceeds the granted capability scope")
	// ErrInvalidKey reports a boundary key that cannot be used.
	ErrInvalidKey = errors.New("capability boundary key is invalid")
)

// Identity is the verified caller: who called, in which role, acting for which
// end user, and within which granted document scope.
type Identity struct {
	CallerID string
	Role     string
	UserID   string
	// GrantedSpaceIDs and GrantedDocumentIDs are the only space and document
	// identifiers this caller may ask for. The zero value is not "unbounded":
	// it grants nothing beyond public documents.
	GrantedSpaceIDs    []string
	GrantedDocumentIDs []string
}

// Allows reports whether the requested scope is contained in the granted scope.
//
// The rule is containment, never intersection: a caller may ask for less than it
// was granted (including nothing at all, which searches public documents only)
// and is rejected when it asks for anything outside the grant. Silently trimming
// the request instead would let a caller probe for identifiers it may not read
// and would return results for a range it never proved.
func (identity Identity) Allows(spaceIDs, documentIDs []string) error {
	grantedSpaces := stringSet(identity.GrantedSpaceIDs)
	for _, spaceID := range normalizeIDs(spaceIDs) {
		if _, ok := grantedSpaces[spaceID]; !ok {
			return fmt.Errorf("%w: space %q was not granted to caller %q", ErrScopeExceeded, spaceID, identity.CallerID)
		}
	}
	grantedDocuments := stringSet(identity.GrantedDocumentIDs)
	for _, documentID := range normalizeIDs(documentIDs) {
		if _, ok := grantedDocuments[documentID]; !ok {
			return fmt.Errorf("%w: document %q was not granted to caller %q", ErrScopeExceeded, documentID, identity.CallerID)
		}
	}
	return nil
}

// Claims is the signed payload.
//
// The two scope declarations are corpus-neutral by shape and corpus-specific by
// meaning: they are the granted container identifiers and the granted object
// identifiers for whichever corpus the role belongs to. For the document roles
// they carry space and document identifiers; for the chat roles they carry chat
// scope and conversation identifiers. The containment rule is identical either
// way, which is why one verifier and one format serve both.
type Claims struct {
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

// Sign encodes and signs claims with the boundary key.
func Sign(claims Claims, key []byte) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	if err := claims.validate(); err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode capability claims: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + sign(encoded, key), nil
}

func (claims Claims) validate() error {
	if claims.Version != TokenVersion {
		return fmt.Errorf("%w: %d", ErrUnsupportedVersion, claims.Version)
	}
	if strings.TrimSpace(claims.Issuer) == "" {
		return fmt.Errorf("%w: issuer is required", ErrMalformedToken)
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return fmt.Errorf("%w: subject is required", ErrMalformedToken)
	}
	if strings.TrimSpace(claims.Audience) == "" {
		return fmt.Errorf("%w: audience is required", ErrMalformedToken)
	}
	if _, err := ParseRole(claims.Role); err != nil {
		return err
	}
	if claims.ExpiresAt <= claims.IssuedAt {
		return fmt.Errorf("%w: expiry must be after issue time", ErrMalformedToken)
	}
	return nil
}

// ParseRole reports whether role is a role this service defines.
func ParseRole(role string) (string, error) {
	switch trimmed := strings.TrimSpace(role); trimmed {
	case RoleIndexWriter, RoleSearcher, RoleOps,
		RoleChatIndexWriter, RoleChatSearcher, RoleChatOps:
		return trimmed, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownRole, role)
	}
}

// clocks carries the two settings shared by issuers and verifiers.
type clocks struct {
	now    func() time.Time
	leeway time.Duration
}

// VerifierOption customises a Verifier or an Issuer.
type VerifierOption func(*clocks)

// WithClock replaces the clock, which tests use to exercise expiry.
func WithClock(now func() time.Time) VerifierOption {
	return func(shared *clocks) {
		if now != nil {
			shared.now = now
		}
	}
}

// WithLeeway allows bounded clock skew between the issuer and this service.
func WithLeeway(leeway time.Duration) VerifierOption {
	return func(shared *clocks) {
		if leeway >= 0 {
			shared.leeway = leeway
		}
	}
}

func newClocks(options ...VerifierOption) clocks {
	shared := clocks{now: time.Now, leeway: 30 * time.Second}
	for _, option := range options {
		option(&shared)
	}
	return shared
}

// Verifier checks tokens minted for one audience.
type Verifier struct {
	key      []byte
	issuer   string
	audience string
	leeway   time.Duration
	now      func() time.Time
}

// NewVerifier builds a verifier for one trusted issuer and audience.
func NewVerifier(key []byte, issuer, audience string, options ...VerifierOption) (*Verifier, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if strings.TrimSpace(issuer) == "" {
		return nil, fmt.Errorf("%w: issuer is required", ErrMalformedToken)
	}
	if strings.TrimSpace(audience) == "" {
		return nil, fmt.Errorf("%w: audience is required", ErrMalformedToken)
	}
	shared := newClocks(options...)
	return &Verifier{
		key:      append([]byte{}, key...),
		issuer:   strings.TrimSpace(issuer),
		audience: strings.TrimSpace(audience),
		leeway:   shared.leeway,
		now:      shared.now,
	}, nil
}

// Verify returns the identity carried by token, or an error explaining why the
// token cannot be trusted. Every failure path is closed: no partial identity is
// ever returned alongside an error.
func (verifier *Verifier) Verify(token string) (Identity, error) {
	claims, err := verifier.verifyClaims(token)
	if err != nil {
		return Identity{}, err
	}
	return Identity{
		CallerID:           claims.Subject,
		Role:               claims.Role,
		UserID:             claims.UserID,
		GrantedSpaceIDs:    normalizeIDs(claims.AllowedSpaceIDs),
		GrantedDocumentIDs: normalizeIDs(claims.AllowedDocumentIDs),
	}, nil
}

func (verifier *Verifier) verifyClaims(token string) (Claims, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Claims{}, fmt.Errorf("%w: token is empty", ErrMalformedToken)
	}
	if len(token) > maxTokenBytes {
		return Claims{}, fmt.Errorf("%w: token exceeds %d bytes", ErrMalformedToken, maxTokenBytes)
	}
	encoded, signature, found := strings.Cut(token, ".")
	if !found || encoded == "" || signature == "" {
		return Claims{}, fmt.Errorf("%w: expected <payload>.<signature>", ErrMalformedToken)
	}
	if !hmac.Equal([]byte(signature), []byte(sign(encoded, verifier.key))) {
		return Claims{}, ErrInvalidSignature
	}

	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: payload is not base64url: %w", ErrMalformedToken, err)
	}
	var claims Claims
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&claims); err != nil {
		return Claims{}, fmt.Errorf("%w: payload is not a capability: %w", ErrMalformedToken, err)
	}
	if err := claims.validate(); err != nil {
		return Claims{}, err
	}
	if claims.Issuer != verifier.issuer {
		return Claims{}, fmt.Errorf("%w: token was minted by %q", ErrWrongIssuer, claims.Issuer)
	}
	if claims.Audience != verifier.audience {
		return Claims{}, fmt.Errorf("%w: token was minted for %q", ErrWrongAudience, claims.Audience)
	}

	now := verifier.now().UTC()
	if now.After(time.Unix(claims.ExpiresAt, 0).Add(verifier.leeway)) {
		return Claims{}, fmt.Errorf("%w: expired at %s", ErrExpiredToken, time.Unix(claims.ExpiresAt, 0).UTC())
	}
	if now.Add(verifier.leeway).Before(time.Unix(claims.IssuedAt, 0)) {
		return Claims{}, fmt.Errorf("%w: issued in the future at %s", ErrMalformedToken, time.Unix(claims.IssuedAt, 0).UTC())
	}
	return claims, nil
}

// Issuer mints capability tokens. Both applications in this repository share the
// format; go-web is the only side that holds the key for online callers.
type Issuer struct {
	key      []byte
	issuer   string
	audience string
	ttl      time.Duration
	now      func() time.Time
}

// NewIssuer builds an issuer with a bounded token lifetime.
func NewIssuer(key []byte, issuer, audience string, ttl time.Duration, options ...VerifierOption) (*Issuer, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if strings.TrimSpace(issuer) == "" {
		return nil, fmt.Errorf("%w: issuer is required", ErrMalformedToken)
	}
	if strings.TrimSpace(audience) == "" {
		return nil, fmt.Errorf("%w: audience is required", ErrMalformedToken)
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("%w: token lifetime must be positive", ErrMalformedToken)
	}
	shared := newClocks(options...)
	return &Issuer{
		key:      append([]byte{}, key...),
		issuer:   strings.TrimSpace(issuer),
		audience: strings.TrimSpace(audience),
		ttl:      ttl,
		now:      shared.now,
	}, nil
}

// Issue mints a token for one caller. scope is ignored for roles that do not
// search, and is normalised and deduplicated for roles that do.
func (issuer *Issuer) Issue(subject, role, userID string, spaceIDs, documentIDs []string) (string, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "", fmt.Errorf("%w: subject is required", ErrMalformedToken)
	}
	parsedRole, err := ParseRole(role)
	if err != nil {
		return "", err
	}
	issuedAt := issuer.now().UTC()
	claims := Claims{
		Version:   TokenVersion,
		Issuer:    issuer.issuer,
		Subject:   subject,
		Audience:  issuer.audience,
		Role:      parsedRole,
		UserID:    strings.TrimSpace(userID),
		IssuedAt:  issuedAt.Unix(),
		ExpiresAt: issuedAt.Add(issuer.ttl).Unix(),
	}
	// Scope-bearing roles carry the granted envelope. Index writers and ops
	// deliberately carry none: their permission is the role, so a leaked write
	// capability can never be replayed as a scoped search.
	if parsedRole == RoleSearcher || parsedRole == RoleChatSearcher {
		claims.AllowedSpaceIDs = normalizeIDs(spaceIDs)
		claims.AllowedDocumentIDs = normalizeIDs(documentIDs)
	}
	return Sign(claims, issuer.key)
}

func sign(encodedPayload string, key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(encodedPayload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// ValidateKey reports whether key can be used as the boundary key. It is
// exported so both the server entry point and callers in tests can fail with the
// same rule instead of restating the minimum length.
func ValidateKey(key []byte) error {
	return validateKey(key)
}

func validateKey(key []byte) error {
	if len(key) < MinKeyBytes {
		return fmt.Errorf("%w: boundary key must be at least %d bytes, got %d", ErrInvalidKey, MinKeyBytes, len(key))
	}
	return nil
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range normalizeIDs(values) {
		set[value] = struct{}{}
	}
	return set
}

// normalizeIDs trims, drops empties, sorts and deduplicates identifiers so that
// ordering and repeated entries never change a scope decision.
func normalizeIDs(values []string) []string {
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
