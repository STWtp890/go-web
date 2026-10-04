package serviceauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// credentialVersion is the only supported envelope version. A token carrying
// another version is rejected as an unknown claim rather than parsed leniently.
const credentialVersion = 1

// ConversationKind mirrors the conversation context of a chat session. It is
// part of the trusted identity material: the caller proves which session it is
// acting in, and the receiver decides what that session may reach.
type ConversationKind string

const (
	ConversationPrivate ConversationKind = "private"
	ConversationGroup   ConversationKind = "group"
)

// Conversation is the session context supplied by a trusted entry point.
type Conversation struct {
	Kind            ConversationKind `json:"kind"`
	ExternalGroupID string           `json:"external_group_id,omitempty"`
}

// AssertionClaims is the payload of a service assertion. It answers "which
// service is calling, with which scopes, on behalf of which subject, in which
// session" and nothing else.
//
// Every field is asserted by the calling service and bounded by the caller
// registry: a caller may only claim subjects in its own namespace, and only
// scopes its role holds. Resource decisions are not made here.
type AssertionClaims struct {
	Version         int               `json:"version"`
	Caller          Caller            `json:"caller"`
	Audience        Audience          `json:"audience"`
	Scopes          []string          `json:"scopes,omitempty"`
	SubjectKey      string            `json:"subject_key,omitempty"`
	Conversation    *Conversation     `json:"conversation,omitempty"`
	Actor           string            `json:"actor,omitempty"`
	RequestID       string            `json:"request_id,omitempty"`
	IssuedAt        int64             `json:"issued_at"`
	ExpiresAt       int64             `json:"expires_at"`
	OnBehalfOf      string            `json:"on_behalf_of,omitempty"`
	Channel         string            `json:"channel,omitempty"`
	BotID           string            `json:"bot_id,omitempty"`
	ExternalUserID  string            `json:"external_user_id,omitempty"`
	ExternalGroupID string            `json:"external_group_id,omitempty"`
	Extra           map[string]string `json:"extra,omitempty"`
}

// CapabilityClaims is the payload of a resource-scope capability. A fact source
// mints it after deciding the resource envelope; a search service validates it
// offline and then applies the inclusion rule.
//
// A capability always carries at least one labelled range family or the explicit
// authenticated-public flag. An empty capability is still meaningful - it limits
// the holder to authenticated-public documents - but it is never produced from a
// denied resolution.
type CapabilityClaims struct {
	Version             int      `json:"version"`
	Issuer              Caller   `json:"issuer"`
	Audience            Audience `json:"audience"`
	Scopes              []string `json:"scopes,omitempty"`
	SubjectKey          string   `json:"subject_key,omitempty"`
	OnBehalfOf          string   `json:"on_behalf_of,omitempty"`
	PrivateSpaceIDs     []string `json:"private_space_ids,omitempty"`
	CurrentTeamSpaceID  string   `json:"current_team_space_id,omitempty"`
	OtherTeamSpaceIDs   []string `json:"other_team_space_ids,omitempty"`
	AllowedSpaceIDs     []string `json:"allowed_space_ids,omitempty"`
	AllowedDocumentIDs  []string `json:"allowed_document_ids,omitempty"`
	AuthenticatedPublic bool     `json:"authenticated_public,omitempty"`
	BotIDs              []string `json:"bot_ids,omitempty"`
	ConversationIDs     []string `json:"conversation_ids,omitempty"`
	ExternalGroupIDs    []string `json:"external_group_ids,omitempty"`
	Channel             string   `json:"channel,omitempty"`
	IssuedAt            int64    `json:"issued_at"`
	ExpiresAt           int64    `json:"expires_at"`
}

// CredentialKind distinguishes the two credential families so a capability can
// never be replayed as a service assertion or the other way round.
type CredentialKind string

const (
	// KindAssertion marks a service assertion payload.
	KindAssertion CredentialKind = "assertion"
	// KindCapability marks a resource-scope capability payload.
	KindCapability CredentialKind = "capability"
)

// Seal holds a validated credential together with its decoded claims.
type Seal[T any] struct {
	Kind   CredentialKind
	Claims T
}

// Codec mints and validates credentials with one boundary key. The zero value is
// unusable: call NewCodec so a missing key fails at startup instead of degrading
// into "accept anything".
type Codec struct {
	key       []byte
	leeway    int64
	now       func() time.Time
	minKeyLen int
}

// CodecOption customizes a Codec.
type CodecOption func(*Codec)

// WithClock replaces the clock used for validity checks. It exists so tests can
// pin expiry behaviour without sleeping.
func WithClock(clock func() time.Time) CodecOption {
	return func(codec *Codec) {
		if clock != nil {
			codec.now = clock
		}
	}
}

// WithLeeway overrides the clock skew tolerance in seconds.
func WithLeeway(seconds int64) CodecOption {
	return func(codec *Codec) {
		if seconds >= 0 {
			codec.leeway = seconds
		}
	}
}

// minBoundaryKeyBytes is the shortest accepted boundary key. A short key is a
// configuration error, not an authentication result.
const minBoundaryKeyBytes = 32

// NewCodec builds a codec from the raw boundary key material.
func NewCodec(key []byte, options ...CodecOption) (*Codec, error) {
	if len(key) < minBoundaryKeyBytes {
		return nil, fmt.Errorf("%w: boundary key must be at least %d bytes", ErrConfiguration, minBoundaryKeyBytes)
	}
	codec := &Codec{
		key:       append([]byte(nil), key...),
		leeway:    defaultLeeway,
		now:       func() time.Time { return time.Now().UTC() },
		minKeyLen: minBoundaryKeyBytes,
	}
	for _, option := range options {
		if option != nil {
			option(codec)
		}
	}
	return codec, nil
}

// SealAssertion signs service assertion claims.
func (codec *Codec) SealAssertion(claims AssertionClaims) (string, error) {
	if codec == nil {
		return "", fmt.Errorf("%w: codec is nil", ErrConfiguration)
	}
	if !claims.Caller.Valid() {
		return "", fmt.Errorf("%w: unregistered caller %q", ErrUnknownClaim, claims.Caller)
	}
	if claims.Audience == "" {
		return "", fmt.Errorf("%w: audience is required", ErrUnknownClaim)
	}
	for _, scope := range claims.Scopes {
		if !KnownScope(Scope(scope)) {
			return "", fmt.Errorf("%w: unregistered scope %q", ErrUnknownClaim, scope)
		}
	}
	if claims.SubjectKey != "" && !claims.Caller.inNamespace(claims.SubjectKey) {
		return "", fmt.Errorf("%w: caller %q may not claim subject %q", ErrNamespaceViolation, claims.Caller, claims.SubjectKey)
	}
	claims.Version = credentialVersion
	claims.Scopes = CanonicalScope(claims.Scopes)
	return codec.seal(claims)
}

// SealCapability signs resource-scope capability claims.
func (codec *Codec) SealCapability(claims CapabilityClaims) (string, error) {
	if codec == nil {
		return "", fmt.Errorf("%w: codec is nil", ErrConfiguration)
	}
	if !claims.Issuer.Valid() {
		return "", fmt.Errorf("%w: unregistered issuer %q", ErrUnknownClaim, claims.Issuer)
	}
	if claims.Audience == "" {
		return "", fmt.Errorf("%w: audience is required", ErrUnknownClaim)
	}
	// Minting is checked as strictly as validating: a capability the boundary
	// would refuse must not be produced in the first place, or the mismatch only
	// shows up as an unexplained rejection at the far end.
	if err := checkCapabilityIssuer(claims.Issuer, claims.Audience); err != nil {
		return "", err
	}
	for _, scope := range claims.Scopes {
		if !KnownScope(Scope(scope)) {
			return "", fmt.Errorf("%w: unregistered scope %q", ErrUnknownClaim, scope)
		}
	}
	claims.Version = credentialVersion
	claims.Scopes = CanonicalScope(claims.Scopes)
	claims.PrivateSpaceIDs = CanonicalIDs(claims.PrivateSpaceIDs)
	claims.OtherTeamSpaceIDs = CanonicalIDs(claims.OtherTeamSpaceIDs)
	claims.AllowedSpaceIDs = CanonicalIDs(claims.AllowedSpaceIDs)
	claims.AllowedDocumentIDs = CanonicalIDs(claims.AllowedDocumentIDs)
	claims.BotIDs = CanonicalIDs(claims.BotIDs)
	claims.ConversationIDs = CanonicalIDs(claims.ConversationIDs)
	claims.ExternalGroupIDs = CanonicalIDs(claims.ExternalGroupIDs)
	return codec.seal(claims)
}

// checkCapabilityIssuer enforces "the fact source is the only issuer" for one
// audience. It rejects both a wrong issuer and an audience that has no issuer
// registered at all: an unregistered audience must fail closed rather than
// accept whichever registered caller signed the token.
func checkCapabilityIssuer(issuer Caller, audience Audience) error {
	allowed, known := CapabilityIssuer(audience)
	if !known {
		return fmt.Errorf("%w: audience %q has no registered capability issuer", ErrUnknownClaim, audience)
	}
	if issuer != allowed {
		return fmt.Errorf("%w: issuer %q may not mint a capability for audience %q", ErrUnknownClaim, issuer, audience)
	}
	return nil
}

func (codec *Codec) seal(payload any) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("serviceauth: encode credential: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return encoded + "." + codec.sign(encoded), nil
}

func (codec *Codec) sign(encodedPayload string) string {
	mac := hmac.New(sha256.New, codec.key)
	mac.Write([]byte(encodedPayload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// OpenAssertion validates a service assertion and returns its claims. The
// expected audience is mandatory: a credential minted for another service must
// fail as unauthenticated, not as forbidden.
func (codec *Codec) OpenAssertion(token string, expected Audience) (*Seal[AssertionClaims], error) {
	raw, err := codec.open(token, expected)
	if err != nil {
		return nil, err
	}
	var claims AssertionClaims
	if err := decodeClaims(raw, &claims); err != nil {
		return nil, err
	}
	if claims.Version != credentialVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrUnknownClaim, claims.Version)
	}
	if !claims.Caller.Valid() {
		return nil, fmt.Errorf("%w: unregistered caller %q", ErrUnknownClaim, claims.Caller)
	}
	for _, scope := range claims.Scopes {
		if !KnownScope(Scope(scope)) {
			return nil, fmt.Errorf("%w: unregistered scope %q", ErrUnknownClaim, scope)
		}
	}
	if claims.SubjectKey != "" && !claims.Caller.inNamespace(claims.SubjectKey) {
		return nil, fmt.Errorf("%w: caller %q claimed subject %q", ErrNamespaceViolation, claims.Caller, claims.SubjectKey)
	}
	if err := codec.checkWindow(claims.IssuedAt, claims.ExpiresAt); err != nil {
		return nil, err
	}
	claims.Scopes = CanonicalScope(claims.Scopes)
	return &Seal[AssertionClaims]{Kind: KindAssertion, Claims: claims}, nil
}

// OpenCapability validates a resource-scope capability and returns its claims.
func (codec *Codec) OpenCapability(token string, expected Audience) (*Seal[CapabilityClaims], error) {
	raw, err := codec.open(token, expected)
	if err != nil {
		return nil, err
	}
	var claims CapabilityClaims
	if err := decodeClaims(raw, &claims); err != nil {
		return nil, err
	}
	if claims.Version != credentialVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrUnknownClaim, claims.Version)
	}
	if !claims.Issuer.Valid() {
		return nil, fmt.Errorf("%w: unregistered issuer %q", ErrUnknownClaim, claims.Issuer)
	}
	// The fact source is the only issuer: a valid signature from another
	// registered caller is not enough, because that caller does not own the facts
	// the range is about.
	if err := checkCapabilityIssuer(claims.Issuer, expected); err != nil {
		return nil, err
	}
	for _, scope := range claims.Scopes {
		if !KnownScope(Scope(scope)) {
			return nil, fmt.Errorf("%w: unregistered scope %q", ErrUnknownClaim, scope)
		}
	}
	if err := codec.checkWindow(claims.IssuedAt, claims.ExpiresAt); err != nil {
		return nil, err
	}
	claims.Scopes = CanonicalScope(claims.Scopes)
	claims.PrivateSpaceIDs = CanonicalIDs(claims.PrivateSpaceIDs)
	claims.OtherTeamSpaceIDs = CanonicalIDs(claims.OtherTeamSpaceIDs)
	claims.AllowedSpaceIDs = CanonicalIDs(claims.AllowedSpaceIDs)
	claims.AllowedDocumentIDs = CanonicalIDs(claims.AllowedDocumentIDs)
	claims.BotIDs = CanonicalIDs(claims.BotIDs)
	claims.ConversationIDs = CanonicalIDs(claims.ConversationIDs)
	claims.ExternalGroupIDs = CanonicalIDs(claims.ExternalGroupIDs)
	return &Seal[CapabilityClaims]{Kind: KindCapability, Claims: claims}, nil
}

func (codec *Codec) open(token string, expected Audience) ([]byte, error) {
	if codec == nil {
		return nil, fmt.Errorf("%w: codec is nil", ErrConfiguration)
	}
	if expected == "" {
		return nil, fmt.Errorf("%w: expected audience is required", ErrConfiguration)
	}
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: credential is empty", ErrMalformedCredential)
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("%w: credential must have two dot-separated parts", ErrMalformedCredential)
	}
	expectedSignature := codec.sign(parts[0])
	if !hmac.Equal([]byte(expectedSignature), []byte(parts[1])) {
		return nil, ErrSignatureMismatch
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: payload is not base64url: %v", ErrMalformedCredential, err)
	}
	var envelope struct {
		Audience Audience `json:"audience"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("%w: payload is not JSON: %v", ErrMalformedCredential, err)
	}
	if envelope.Audience != expected {
		return nil, fmt.Errorf("%w: credential audience %q, expected %q", ErrAudienceMismatch, envelope.Audience, expected)
	}
	return raw, nil
}

func decodeClaims(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedCredential, err)
	}
	return nil
}

func (codec *Codec) checkWindow(issuedAt, expiresAt int64) error {
	if issuedAt <= 0 || expiresAt <= 0 || expiresAt <= issuedAt {
		return fmt.Errorf("%w: invalid validity window", ErrUnknownClaim)
	}
	now := codec.now().Unix()
	if now > expiresAt+codec.leeway {
		return fmt.Errorf("%w: expired at %d", ErrExpired, expiresAt)
	}
	if issuedAt > now+codec.leeway {
		return fmt.Errorf("%w: issued in the future at %d", ErrExpired, issuedAt)
	}
	return nil
}

// Requires reports whether the claims carry every required scope.
func Requires(scopes []string, required ...Scope) error {
	held := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		held[scope] = struct{}{}
	}
	for _, want := range required {
		if _, ok := held[string(want)]; !ok {
			return fmt.Errorf("%w: %q is required", ErrScopeNotGranted, want)
		}
	}
	return nil
}
