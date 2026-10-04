package serviceauth

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Auditor receives one structured record per rejected call, or per accepted call
// when Accepted is non-nil. Audit records deliberately never contain request
// bodies: "who asked what" is answered from the method name and the identifiers,
// not from the content.
type Auditor interface {
	// Rejected records a call that failed authentication or authorization.
	Rejected(ctx context.Context, method string, reason error)
	// Accepted records a call that passed the boundary.
	Accepted(ctx context.Context, principal *Principal, method string)
}

// BoundaryPolicy maps a fully qualified gRPC method to the scopes it requires.
// A method absent from the map is refused, so adding an RPC without registering
// a policy fails closed at runtime and in tests.
type BoundaryPolicy map[string][]Scope

// Boundary is the reusable authentication and authorization interceptor pair.
// Each service configures it with its own audience, caller allow-list and
// method policy; the wire format and the failure semantics stay identical.
type Boundary struct {
	service   string
	auth      *Authenticator
	policy    BoundaryPolicy
	auditor   Auditor
	anonymous map[string]struct{}
}

// BoundaryOption customizes a Boundary.
type BoundaryOption func(*Boundary)

// WithAuditor attaches an audit sink.
func WithAuditor(auditor Auditor) BoundaryOption {
	return func(boundary *Boundary) { boundary.auditor = auditor }
}

// WithAnonymous registers a method that is served without authentication. It is
// only legitimate for the standard gRPC health service, which container probes
// call without holding the boundary key and which exposes nothing but status.
func WithAnonymous(methods ...string) BoundaryOption {
	return func(boundary *Boundary) {
		if boundary.anonymous == nil {
			boundary.anonymous = make(map[string]struct{}, len(methods))
		}
		for _, method := range methods {
			boundary.anonymous[method] = struct{}{}
		}
	}
}

// NewBoundary builds the interceptor pair. A missing authenticator or an empty
// policy is a construction failure: a boundary that authorizes nothing would
// otherwise look like a boundary that authorizes everything.
func NewBoundary(service string, auth *Authenticator, policy BoundaryPolicy, options ...BoundaryOption) (*Boundary, error) {
	if auth == nil {
		return nil, fmt.Errorf("%w: authenticator is required", ErrConfiguration)
	}
	if len(policy) == 0 {
		return nil, fmt.Errorf("%w: %s has an empty method policy", ErrConfiguration, service)
	}
	boundary := &Boundary{service: service, auth: auth, policy: policy}
	for _, option := range options {
		if option != nil {
			option(boundary)
		}
	}
	return boundary, nil
}

// UnaryInterceptor authenticates and authorizes every unary call.
func (boundary *Boundary) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		principal, err := boundary.authorize(ctx, info.FullMethod)
		if err != nil {
			return nil, err
		}
		if principal == nil {
			return handler(ctx, request)
		}
		return handler(WithPrincipal(ctx, principal), request)
	}
}

// StreamInterceptor authenticates and authorizes every streaming call before the
// first message is produced.
func (boundary *Boundary) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		principal, err := boundary.authorize(stream.Context(), info.FullMethod)
		if err != nil {
			return err
		}
		if principal == nil {
			return handler(server, stream)
		}
		return handler(server, &principalStream{ServerStream: stream, ctx: WithPrincipal(stream.Context(), principal)})
	}
}

// principalStream carries a context enriched with the authenticated principal,
// so streaming handlers read identity exactly like unary handlers do.
type principalStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (stream *principalStream) Context() context.Context { return stream.ctx }

// Authenticate validates one credential for this boundary's audience. HTTP entry
// points use it directly, so they share the exact rules the gRPC path applies.
func (boundary *Boundary) Authenticate(token string) (*Principal, error) {
	return boundary.auth.Authenticate(token)
}

// AuthenticateHeader validates an Authorization header value.
func (boundary *Boundary) AuthenticateHeader(header string) (*Principal, error) {
	token, err := BearerToken(header)
	if err != nil {
		return nil, err
	}
	return boundary.auth.Authenticate(token)
}

// OpenCapability validates a resource-scope capability for this boundary's
// audience. Query entry points use it so the resource range always comes from a
// credential the fact source minted, never from the request body.
func (boundary *Boundary) OpenCapability(token string) (*Seal[CapabilityClaims], error) {
	return boundary.auth.codec.OpenCapability(token, boundary.auth.audience)
}

// CapabilityFromIncomingMetadata reads and validates the resource capability of a
// gRPC call. Exactly one value is required: accepting the first of several would
// let a caller smuggle a second, wider grant past review.
func (boundary *Boundary) CapabilityFromIncomingMetadata(ctx context.Context) (*Seal[CapabilityClaims], error) {
	values := metadata.ValueFromIncomingContext(ctx, HeaderCapability)
	if len(values) != 1 {
		return nil, fmt.Errorf("%w: exactly one %s value is required", ErrMalformedCredential, HeaderCapability)
	}
	token, err := BearerToken(values[0])
	if err != nil {
		return nil, err
	}
	return boundary.OpenCapability(token)
}

// Authorize checks a method's policy against an already authenticated principal.
func (boundary *Boundary) Authorize(principal *Principal, fullMethod string) error {
	required, known := boundary.policy[fullMethod]
	if !known {
		return fmt.Errorf("%w: method %s has no authorization policy", ErrScopeNotGranted, fullMethod)
	}
	return Requires(principal.scopesOrNil(), required...)
}

func (boundary *Boundary) authorize(ctx context.Context, fullMethod string) (*Principal, error) {
	if boundary.isAnonymous(fullMethod) {
		return nil, nil
	}
	required, known := boundary.policy[fullMethod]
	if !known {
		err := fmt.Errorf("%w: method %s has no authorization policy", ErrScopeNotGranted, fullMethod)
		boundary.record(ctx, nil, fullMethod, err)
		return nil, status.Errorf(codes.PermissionDenied, "%s: method %s has no authorization policy", boundary.service, fullMethod)
	}
	principal, err := boundary.auth.FromIncomingMetadata(ctx)
	if err != nil {
		boundary.record(ctx, nil, fullMethod, err)
		return nil, GRPCStatus(err)
	}
	if err := Requires(principal.Scopes, required...); err != nil {
		boundary.record(ctx, principal, fullMethod, err)
		return nil, GRPCStatus(err)
	}
	if boundary.auditor != nil {
		boundary.auditor.Accepted(ctx, principal, fullMethod)
	}
	return principal, nil
}

func (boundary *Boundary) isAnonymous(fullMethod string) bool {
	if len(boundary.anonymous) == 0 {
		return false
	}
	_, ok := boundary.anonymous[fullMethod]
	return ok
}

func (boundary *Boundary) record(ctx context.Context, principal *Principal, method string, reason error) {
	if boundary.auditor == nil {
		return
	}
	if principal != nil {
		ctx = WithPrincipal(ctx, principal)
	}
	boundary.auditor.Rejected(ctx, method, reason)
}

func (principal *Principal) scopesOrNil() []string {
	if principal == nil {
		return nil
	}
	return principal.Scopes
}
