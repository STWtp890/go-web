package serviceauth

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// HeaderAuthorization is the single transport header carrying either credential
// family. It is the same header the existing mixin-search boundary uses, so one
// boundary key and one interceptor pattern cover the whole system.
const HeaderAuthorization = "authorization"

// HeaderCapability carries a resource-scope capability on a query call, while
// HeaderAuthorization carries the calling service's own assertion. Keeping them
// apart matters: the assertion says who is asking, the capability says what the
// asker was already granted. A search service authorizes the request from the
// capability, never from the caller's self-description.
const HeaderCapability = "x-resource-capability"

// HeaderRequestID carries an optional correlation id. It is copied into audit
// records and never used for authorization.
const HeaderRequestID = "x-request-id"

// Principal is the authenticated caller extracted at a trusted entry point. It
// is the only identity source the business layer may read: a request body can
// never introduce or widen it.
type Principal struct {
	Caller              Caller
	Audience            Audience
	Scopes              []string
	SubjectKey          string
	Conversation        *Conversation
	Actor               string
	RequestID           string
	OnBehalfOf          string
	Channel             string
	BotID               string
	ExternalUserID      string
	ExternalGroupID     string
	CredentialExpiresAt time.Time
	Extra               map[string]string
}

// HasScope reports whether the principal holds a scope.
func (principal *Principal) HasScope(scope Scope) bool {
	if principal == nil {
		return false
	}
	for _, held := range principal.Scopes {
		if held == string(scope) {
			return true
		}
	}
	return false
}

// RequireScope fails closed when the principal lacks a scope.
func (principal *Principal) RequireScope(scope Scope) error {
	if principal == nil {
		return fmt.Errorf("%w: no principal", ErrScopeNotGranted)
	}
	if !principal.HasScope(scope) {
		return fmt.Errorf("%w: %q is required", ErrScopeNotGranted, scope)
	}
	return nil
}

// RequireSubject fails when the caller did not supply a resource subject.
func (principal *Principal) RequireSubject() (string, error) {
	if principal == nil || principal.SubjectKey == "" {
		return "", fmt.Errorf("%w: caller did not assert a resource subject", ErrSubjectNotAllowed)
	}
	return principal.SubjectKey, nil
}

// RequireConversation fails when the caller did not supply session context.
func (principal *Principal) RequireConversation() (Conversation, error) {
	if principal == nil || principal.Conversation == nil {
		return Conversation{}, fmt.Errorf("%w: caller did not assert a conversation context", ErrSubjectNotAllowed)
	}
	switch principal.Conversation.Kind {
	case ConversationPrivate:
		if principal.Conversation.ExternalGroupID != "" {
			return Conversation{}, fmt.Errorf("%w: a private conversation must not carry a group id", ErrNamespaceViolation)
		}
	case ConversationGroup:
		if strings.TrimSpace(principal.Conversation.ExternalGroupID) == "" {
			return Conversation{}, fmt.Errorf("%w: a group conversation requires a group id", ErrNamespaceViolation)
		}
	default:
		return Conversation{}, fmt.Errorf("%w: unknown conversation kind %q", ErrNamespaceViolation, principal.Conversation.Kind)
	}
	return *principal.Conversation, nil
}

type principalContextKey struct{}

// WithPrincipal attaches an authenticated principal to a context. Only
// transport boundaries may call it; the business layer only reads.
func WithPrincipal(ctx context.Context, principal *Principal) context.Context {
	if principal == nil {
		return ctx
	}
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// PrincipalFrom extracts the authenticated principal from a context.
func PrincipalFrom(ctx context.Context) (*Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(*Principal)
	return principal, ok && principal != nil
}

// Authenticator validates service assertions at a trusted entry point.
type Authenticator struct {
	codec    *Codec
	audience Audience
	callers  map[Caller]struct{}
}

// NewAuthenticator builds an entry-point authenticator. When allowed is empty,
// every registered caller is accepted; otherwise only the listed callers are,
// which is how an entry point states "only go-web and py-agent may call me".
func NewAuthenticator(codec *Codec, audience Audience, allowed ...Caller) (*Authenticator, error) {
	if codec == nil {
		return nil, fmt.Errorf("%w: codec is required", ErrConfiguration)
	}
	if audience == "" {
		return nil, fmt.Errorf("%w: audience is required", ErrConfiguration)
	}
	callers := make(map[Caller]struct{}, len(allowed))
	for _, caller := range allowed {
		if !caller.Valid() {
			return nil, fmt.Errorf("%w: unregistered caller %q", ErrConfiguration, caller)
		}
		callers[caller] = struct{}{}
	}
	return &Authenticator{codec: codec, audience: audience, callers: callers}, nil
}

// Authenticate validates an assertion token and returns the resulting principal.
func (authenticator *Authenticator) Authenticate(token string) (*Principal, error) {
	if authenticator == nil || authenticator.codec == nil {
		return nil, fmt.Errorf("%w: authenticator is nil", ErrConfiguration)
	}
	seal, err := authenticator.codec.OpenAssertion(token, authenticator.audience)
	if err != nil {
		return nil, err
	}
	if len(authenticator.callers) > 0 {
		if _, ok := authenticator.callers[seal.Claims.Caller]; !ok {
			return nil, fmt.Errorf("%w: caller %q may not call audience %q", ErrNamespaceViolation, seal.Claims.Caller, authenticator.audience)
		}
	}
	claims := seal.Claims
	return &Principal{
		Caller:              claims.Caller,
		Audience:            claims.Audience,
		Scopes:              append([]string(nil), claims.Scopes...),
		SubjectKey:          claims.SubjectKey,
		Conversation:        claims.Conversation,
		Actor:               claims.Actor,
		RequestID:           claims.RequestID,
		OnBehalfOf:          claims.OnBehalfOf,
		Channel:             claims.Channel,
		BotID:               claims.BotID,
		ExternalUserID:      claims.ExternalUserID,
		ExternalGroupID:     claims.ExternalGroupID,
		CredentialExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC(),
		Extra:               claims.Extra,
	}, nil
}

// BearerToken extracts the credential from an Authorization header value.
func BearerToken(header string) (string, error) {
	trimmed := strings.TrimSpace(header)
	if trimmed == "" {
		return "", fmt.Errorf("%w: authorization header is missing", ErrMalformedCredential)
	}
	const prefix = "Bearer "
	if len(trimmed) <= len(prefix) || !strings.EqualFold(trimmed[:len(prefix)], prefix) {
		return "", fmt.Errorf("%w: authorization header must use the Bearer scheme", ErrMalformedCredential)
	}
	token := strings.TrimSpace(trimmed[len(prefix):])
	if token == "" {
		return "", fmt.Errorf("%w: bearer token is empty", ErrMalformedCredential)
	}
	return token, nil
}

// AuthorizationHeader renders a credential as an Authorization header value.
func AuthorizationHeader(token string) string { return "Bearer " + token }

// FromIncomingMetadata authenticates a gRPC request from its metadata.
func (authenticator *Authenticator) FromIncomingMetadata(ctx context.Context) (*Principal, error) {
	values := metadata.ValueFromIncomingContext(ctx, HeaderAuthorization)
	if len(values) != 1 {
		return nil, fmt.Errorf("%w: exactly one %s value is required", ErrMalformedCredential, HeaderAuthorization)
	}
	token, err := BearerToken(values[0])
	if err != nil {
		return nil, err
	}
	principal, err := authenticator.Authenticate(token)
	if err != nil {
		return nil, err
	}
	if requestIDs := metadata.ValueFromIncomingContext(ctx, HeaderRequestID); len(requestIDs) == 1 && principal.RequestID == "" {
		principal.RequestID = strings.TrimSpace(requestIDs[0])
	}
	return principal, nil
}

// FromRequest authenticates an HTTP request from its headers.
func (authenticator *Authenticator) FromRequest(header func(string) string) (*Principal, error) {
	if header == nil {
		return nil, fmt.Errorf("%w: header accessor is nil", ErrConfiguration)
	}
	token, err := BearerToken(header(HeaderAuthorization))
	if err != nil {
		return nil, err
	}
	principal, err := authenticator.Authenticate(token)
	if err != nil {
		return nil, err
	}
	if principal.RequestID == "" {
		principal.RequestID = strings.TrimSpace(header(HeaderRequestID))
	}
	return principal, nil
}

// GRPCStatus maps a credential error to the stable gRPC status the contract
// publishes. Authentication failures are UNAUTHENTICATED; a valid credential
// missing a scope is PERMISSION_DENIED; a broken setup is INTERNAL so it cannot
// be mistaken for a caller problem.
func GRPCStatus(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrConfiguration):
		return status.Error(codes.Internal, "service credential configuration is invalid")
	case errors.Is(err, ErrScopeNotGranted):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, ErrNamespaceViolation), errors.Is(err, ErrSubjectNotAllowed):
		return status.Error(codes.PermissionDenied, err.Error())
	default:
		return status.Error(codes.Unauthenticated, err.Error())
	}
}

// HTTPStatus maps a credential error to an HTTP status code for the JSON entry
// points the services expose for tooling and health probes.
func HTTPStatus(err error) int {
	if err == nil {
		return 0
	}
	switch {
	case errors.Is(err, ErrConfiguration):
		return 500
	case errors.Is(err, ErrScopeNotGranted), errors.Is(err, ErrNamespaceViolation), errors.Is(err, ErrSubjectNotAllowed):
		return 403
	default:
		return 401
	}
}

// ParseSequence parses a cursor value supplied by a consumer. A negative or
// unparsable cursor is a client error, never silently treated as zero: silently
// replaying from the beginning would be indistinguishable from data loss.
func ParseSequence(raw string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%w: cursor %q is not a non-negative integer", ErrUnknownClaim, raw)
	}
	return value, nil
}

// ErrStreamExhausted reports that a cursor-bounded event read reached the end of
// the currently available range. It is a normal termination for a non-following
// stream, not a failure.
var ErrStreamExhausted = errors.New("serviceauth: event stream exhausted")
