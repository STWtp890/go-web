// Package documentsearch is the go-web client of the formal document search
// service.
//
// go-web never queries a retrieval index without a resource scope the fact source
// already granted: it asks the document service for a capability, then presents
// that capability here. The search service validates it offline and applies the
// inclusion rule, so a caller can narrow a query but never widen it.
package documentsearch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	documentsearchv1 "packages/gen/documentsearch/v1"
	"packages/serviceauth"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Errors surfaced to the Web layer.
var (
	// ErrForbidden means the request range was outside the granted capability.
	ErrForbidden = errors.New("document search client: forbidden")
	// ErrInvalidInput means the search service rejected the request shape.
	ErrInvalidInput = errors.New("document search client: invalid input")
	// ErrUnavailable means the search service could not be reached or rejected
	// our service identity.
	ErrUnavailable = errors.New("document search client: unavailable")
)

// Config wires the client.
type Config struct {
	// Endpoint is the document search gRPC address.
	Endpoint string
	// Audience is the audience the search service expects. It must equal the
	// audience the issued capability carries, otherwise every query is rejected
	// as unauthenticated.
	Audience string
	// Caller is the service identity presented in the assertion. It identifies
	// who is asking for audit purposes; the resource range still comes from the
	// capability.
	Caller serviceauth.Caller
	// Actor is the operator identity recorded in audit records.
	Actor string
	// RequestTimeout bounds one search call.
	RequestTimeout time.Duration
}

// Client is the document search client.
type Client struct {
	config  Config
	codec   *serviceauth.Codec
	conn    *grpc.ClientConn
	service documentsearchv1.DocumentSearchServiceClient
	now     func() time.Time
}

// New builds the client. A missing endpoint, key or identity is a configuration
// failure, never a silent fall back to a direct database query.
func New(config Config, boundaryKey []byte) (*Client, error) {
	if strings.TrimSpace(config.Endpoint) == "" {
		return nil, fmt.Errorf("document search client: endpoint is required")
	}
	if strings.TrimSpace(config.Audience) == "" {
		return nil, fmt.Errorf("document search client: audience is required")
	}
	if !config.Caller.Valid() {
		return nil, fmt.Errorf("document search client: caller %q is not a registered service identity", config.Caller)
	}
	codec, err := serviceauth.NewCodec(boundaryKey)
	if err != nil {
		return nil, err
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 10 * time.Second
	}
	if strings.TrimSpace(config.Actor) == "" {
		config.Actor = string(config.Caller)
	}
	conn, err := grpc.NewClient(config.Endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("document search client: dial %s: %w", config.Endpoint, err)
	}
	return &Client{
		config:  config,
		codec:   codec,
		conn:    conn,
		service: documentsearchv1.NewDocumentSearchServiceClient(conn),
		now:     func() time.Time { return time.Now().UTC() },
	}, nil
}

// Close releases the connection.
func (client *Client) Close() error {
	if client == nil || client.conn == nil {
		return nil
	}
	return client.conn.Close()
}

// Search runs one query inside the granted capability.
//
// requestedSpaces and requestedDocuments are the caller's narrowing request. They
// must stay inside the capability; the search service rejects the whole request
// otherwise, which is the behaviour the Web layer surfaces as "no results within
// your access" rather than silently trimming the range.
//
// page is 1-based and pageSize is the number of hits per page; the response
// carries the real total so the caller can render an honest page count.
// ownedBySubjectOnly restricts the answer to the capability subject's own
// documents, which is what a personal search needs: it must never surface
// someone else's public document.
func (client *Client) Search(ctx context.Context, subjectKey, capability string, requestedSpaces, requestedDocuments []string, query string, page, pageSize int, ownedBySubjectOnly bool) (*documentsearchv1.SearchDocumentsResponse, error) {
	if client == nil || client.codec == nil {
		return nil, fmt.Errorf("document search client: client is not configured")
	}
	if strings.TrimSpace(capability) == "" {
		return nil, fmt.Errorf("document search client: a resource capability is required")
	}
	if strings.TrimSpace(subjectKey) == "" {
		return nil, fmt.Errorf("document search client: subject key is required")
	}
	now := client.now()
	assertion, err := client.codec.SealAssertion(serviceauth.AssertionClaims{
		Caller:     client.config.Caller,
		Audience:   serviceauth.Audience(client.config.Audience),
		Scopes:     []string{string(serviceauth.ScopeDocumentSearcher)},
		SubjectKey: subjectKey,
		Actor:      client.config.Actor,
		Conversation: &serviceauth.Conversation{
			Kind: serviceauth.ConversationPrivate,
		},
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(2 * time.Minute).Unix(),
	})
	if err != nil {
		return nil, err
	}

	callCtx, cancel := context.WithTimeout(ctx, client.config.RequestTimeout)
	defer cancel()
	callCtx = metadata.AppendToOutgoingContext(callCtx,
		serviceauth.HeaderAuthorization, serviceauth.AuthorizationHeader(assertion),
		serviceauth.HeaderCapability, serviceauth.AuthorizationHeader(capability),
	)
	response, err := client.service.SearchDocuments(callCtx, &documentsearchv1.SearchDocumentsRequest{
		Query:              query,
		AllowedSpaceIds:    requestedSpaces,
		AllowedDocumentIds: requestedDocuments,
		Page:               int32(page),
		PageSize:           int32(pageSize),
		OwnedBySubjectOnly: ownedBySubjectOnly,
	})
	if err != nil {
		return nil, translate(err)
	}
	return response, nil
}

// translate maps gRPC status codes to stable errors for the Web layer.
func translate(err error) error {
	if err == nil {
		return nil
	}
	switch status.Code(err) {
	case codes.PermissionDenied:
		return fmt.Errorf("%w: %s", ErrForbidden, err.Error())
	case codes.InvalidArgument:
		return fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())
	case codes.Unavailable, codes.DeadlineExceeded:
		return fmt.Errorf("%w: %s", ErrUnavailable, err.Error())
	case codes.Unauthenticated:
		// A rejected service identity is a go-web configuration bug, not a user
		// error. Report it as unavailable so it is retried and alerted on.
		return fmt.Errorf("%w: document search rejected our service identity: %s", ErrUnavailable, err.Error())
	default:
		return fmt.Errorf("document search client: %w", err)
	}
}
