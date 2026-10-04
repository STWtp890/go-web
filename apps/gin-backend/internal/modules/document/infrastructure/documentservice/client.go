// Package documentservice is the go-web client of the document service.
//
// go-web keeps the Web account, the Web surface and the Web business entry
// points; it does not read or write the document service's business tables. Every
// call presents a signed service assertion that carries the Web subject the
// request is being made for, and the document service decides resource
// permission from its own facts.
//
// The two identity families are deliberately not bound to each other: a Web
// subject is "web:user:<id>" and needs no QQ identity to operate documents.
package documentservice

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	documentv1 "packages/gen/document/v1"
	"packages/serviceauth"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Errors surfaced to the Web layer. The HTTP adapter maps them to status codes;
// it never inspects gRPC codes directly.
var (
	// ErrNotConfigured means the document service client was not wired. A Web
	// deployment without a document service must fail loudly rather than fall
	// back to writing tables directly.
	ErrNotConfigured = errors.New("document service client: not configured")
	// ErrNotFound means the document service reported no such document.
	ErrNotFound = errors.New("document service client: not found")
	// ErrForbidden means the document service denied the resource.
	ErrForbidden = errors.New("document service client: forbidden")
	// ErrInvalidInput means the caller supplied something the document service
	// rejected.
	ErrInvalidInput = errors.New("document service client: invalid input")
	// ErrConflict means a concurrency or state precondition failed.
	ErrConflict = errors.New("document service client: precondition failed")
	// ErrUnavailable means the document service could not be reached.
	ErrUnavailable = errors.New("document service client: unavailable")
)

// Config wires the client.
type Config struct {
	// Endpoint is the document service gRPC address.
	Endpoint string
	// Audience is the audience the document service expects.
	Audience string
	// Caller is the service identity presented. It must be a registered caller
	// that may claim web subjects.
	Caller serviceauth.Caller
	// Actor is the operator identity recorded in audit rows. It defaults to the
	// caller name.
	Actor string
	// Scopes are the scopes every assertion carries. Grant only what the Web
	// surface actually needs.
	Scopes []serviceauth.Scope
	// RequestTimeout bounds one call.
	RequestTimeout time.Duration
	// CapabilityTTL bounds how long a query capability stays valid.
	CapabilityTTL time.Duration
}

// Client is the document service client.
type Client struct {
	config  Config
	codec   *serviceauth.Codec
	conn    *grpc.ClientConn
	service documentv1.DocumentServiceClient
	now     func() time.Time
}

// New builds the client. A missing endpoint, key or identity is a configuration
// failure: this client never degrades into an unauthenticated caller.
func New(config Config, boundaryKey []byte) (*Client, error) {
	if strings.TrimSpace(config.Endpoint) == "" {
		return nil, fmt.Errorf("%w: endpoint is required", ErrNotConfigured)
	}
	if strings.TrimSpace(config.Audience) == "" {
		return nil, fmt.Errorf("%w: audience is required", ErrNotConfigured)
	}
	if !config.Caller.Valid() {
		return nil, fmt.Errorf("%w: caller %q is not a registered service identity", ErrNotConfigured, config.Caller)
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
		return nil, fmt.Errorf("document service client: dial %s: %w", config.Endpoint, err)
	}
	return &Client{
		config:  config,
		codec:   codec,
		conn:    conn,
		service: documentv1.NewDocumentServiceClient(conn),
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

// WebSubjectKey builds the canonical subject key of a Web account.
func WebSubjectKey(userID int64) (string, error) {
	if userID <= 0 {
		return "", fmt.Errorf("%w: web user id must be positive", ErrInvalidInput)
	}
	return serviceauth.WebSubjectKey(strconv.FormatInt(userID, 10))
}

// callContext attaches the service assertion for one Web subject.
//
// The subject is never taken from the request body: it is derived from the
// authenticated Web session that the HTTP layer already validated.
func (client *Client) callContext(ctx context.Context, subjectKey, requestID string, extraScopes ...serviceauth.Scope) (context.Context, context.CancelFunc, error) {
	if client == nil || client.codec == nil {
		return nil, nil, ErrNotConfigured
	}
	if strings.TrimSpace(subjectKey) == "" {
		return nil, nil, fmt.Errorf("%w: subject key is required", ErrInvalidInput)
	}
	scopes := make([]string, 0, len(client.config.Scopes)+len(extraScopes))
	for _, scope := range client.config.Scopes {
		scopes = append(scopes, string(scope))
	}
	for _, scope := range extraScopes {
		scopes = append(scopes, string(scope))
	}
	now := client.now()
	token, err := client.codec.SealAssertion(serviceauth.AssertionClaims{
		Caller:     client.config.Caller,
		Audience:   serviceauth.Audience(client.config.Audience),
		Scopes:     scopes,
		SubjectKey: subjectKey,
		Actor:      client.config.Actor,
		RequestID:  requestID,
		Conversation: &serviceauth.Conversation{
			Kind: serviceauth.ConversationPrivate,
		},
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(2 * time.Minute).Unix(),
	})
	if err != nil {
		return nil, nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, client.config.RequestTimeout)
	callCtx = metadata.AppendToOutgoingContext(callCtx,
		serviceauth.HeaderAuthorization, serviceauth.AuthorizationHeader(token),
	)
	if requestID != "" {
		callCtx = metadata.AppendToOutgoingContext(callCtx, serviceauth.HeaderRequestID, requestID)
	}
	return callCtx, cancel, nil
}

// CreateDocument creates a formal document owned by the Web subject.
func (client *Client) CreateDocument(ctx context.Context, subjectKey string, request *documentv1.CreateDocumentRequest) (*documentv1.DocumentDetail, error) {
	callCtx, cancel, err := client.callContext(ctx, subjectKey, request.GetRequestId())
	if err != nil {
		return nil, err
	}
	defer cancel()
	response, err := client.service.CreateDocument(callCtx, request)
	if err != nil {
		return nil, translate(err)
	}
	return response.GetDocument(), nil
}

// UpdateDraft appends a draft version.
//
// A draft is staging: it does not become the active version and produces no
// event. The Web "edit and save" path uses SaveDocument instead, so an ordinary
// save never leaves the document on a draft by accident.
func (client *Client) UpdateDraft(ctx context.Context, subjectKey string, request *documentv1.UpdateDraftRequest) (*documentv1.DocumentDetail, error) {
	callCtx, cancel, err := client.callContext(ctx, subjectKey, request.GetRequestId())
	if err != nil {
		return nil, err
	}
	defer cancel()
	response, err := client.service.UpdateDraft(callCtx, request)
	if err != nil {
		return nil, translate(err)
	}
	return response.GetDocument(), nil
}

// SaveDocument runs the "edit and save" use case.
//
// The document service performs ownership, expected-revision, version switch,
// optional access-policy change and Outbox write in one transaction, so go-web
// never has to orchestrate a draft-then-publish sequence that could half-apply.
// The returned detail is the newly active version, so a detail read straight
// after a save sees the new body.
func (client *Client) SaveDocument(ctx context.Context, subjectKey string, request *documentv1.SaveDocumentRequest) (*documentv1.SaveDocumentResponse, error) {
	callCtx, cancel, err := client.callContext(ctx, subjectKey, request.GetRequestId())
	if err != nil {
		return nil, err
	}
	defer cancel()
	response, err := client.service.SaveDocument(callCtx, request)
	if err != nil {
		return nil, translate(err)
	}
	return response, nil
}

// TrashDocument moves a document out of the searchable set.
func (client *Client) TrashDocument(ctx context.Context, subjectKey, documentID, requestID string) error {
	callCtx, cancel, err := client.callContext(ctx, subjectKey, requestID)
	if err != nil {
		return err
	}
	defer cancel()
	_, err = client.service.TrashDocument(callCtx, &documentv1.TrashDocumentRequest{DocumentId: documentID, RequestId: requestID})
	return translate(err)
}

// GetDocument reads one document's detail.
func (client *Client) GetDocument(ctx context.Context, subjectKey, documentID string) (*documentv1.DocumentDetail, error) {
	callCtx, cancel, err := client.callContext(ctx, subjectKey, "")
	if err != nil {
		return nil, err
	}
	defer cancel()
	response, err := client.service.GetDocument(callCtx, &documentv1.GetDocumentRequest{DocumentId: documentID})
	if err != nil {
		return nil, translate(err)
	}
	return response.GetDocument(), nil
}

// ListDocuments lists documents the subject may see.
//
// The returned cursor is the service's own opaque forward position. go-web
// passes it back verbatim and never derives one from a page number: the
// document service owns the ordering, so only it can name a position in it.
// totalCount is the real number of matches, independent of the page returned.
func (client *Client) ListDocuments(ctx context.Context, subjectKey string, request *documentv1.ListDocumentsRequest) ([]*documentv1.DocumentSummary, string, int64, error) {
	callCtx, cancel, err := client.callContext(ctx, subjectKey, "")
	if err != nil {
		return nil, "", 0, err
	}
	defer cancel()
	response, err := client.service.ListDocuments(callCtx, request)
	if err != nil {
		return nil, "", 0, translate(err)
	}
	return response.GetDocuments(), response.GetNextPageToken(), response.GetTotalCount(), nil
}

// ResolveAccessScope asks the document service what the subject may reach in a
// conversation. The document service decides; this client only carries the
// verified identity and session context.
func (client *Client) ResolveAccessScope(ctx context.Context, subjectKey string, conversation *documentv1.ConversationContext) (*documentv1.ResolveAccessScopeResponse, error) {
	callCtx, cancel, err := client.callContext(ctx, subjectKey, "", serviceauth.ScopeAccessResolve)
	if err != nil {
		return nil, err
	}
	defer cancel()
	response, err := client.service.ResolveAccessScope(callCtx, &documentv1.ResolveAccessScopeRequest{
		SubjectKey:   subjectKey,
		Conversation: conversation,
	})
	if err != nil {
		return nil, translate(err)
	}
	return response, nil
}

// IssueSearchCapability resolves the scope and mints the resource capability the
// caller presents to document-search.
//
// The document service is the issuer on purpose: the search service validates the
// credential offline and never has to trust a caller's own description of its
// range. A Denied resolution returns no capability, so a denial can never be
// downgraded into an empty-but-valid grant.
func (client *Client) IssueSearchCapability(ctx context.Context, subjectKey string, conversation *documentv1.ConversationContext, requestedSpaces, requestedDocuments []string) (*documentv1.IssueSearchCapabilityResponse, error) {
	callCtx, cancel, err := client.callContext(ctx, subjectKey, "", serviceauth.ScopeAccessResolve)
	if err != nil {
		return nil, err
	}
	defer cancel()
	response, err := client.service.IssueSearchCapability(callCtx, &documentv1.IssueSearchCapabilityRequest{
		SubjectKey:           subjectKey,
		Conversation:         conversation,
		RequestedSpaceIds:    requestedSpaces,
		RequestedDocumentIds: requestedDocuments,
	})
	if err != nil {
		return nil, translate(err)
	}
	return response, nil
}

// translate maps gRPC status codes to the stable errors the Web layer handles.
// The mapping lives in one place so a new RPC cannot invent a new convention.
func translate(err error) error {
	if err == nil {
		return nil
	}
	switch status.Code(err) {
	case codes.NotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, err.Error())
	case codes.PermissionDenied:
		return fmt.Errorf("%w: %s", ErrForbidden, err.Error())
	case codes.InvalidArgument:
		return fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())
	case codes.FailedPrecondition, codes.AlreadyExists, codes.Aborted:
		return fmt.Errorf("%w: %s", ErrConflict, err.Error())
	case codes.Unavailable, codes.DeadlineExceeded:
		return fmt.Errorf("%w: %s", ErrUnavailable, err.Error())
	case codes.Unauthenticated:
		// An authentication failure is a go-web configuration bug, not a user
		// error: report it as unavailable so the caller retries and operators see
		// it, instead of blaming the end user.
		return fmt.Errorf("%w: document service rejected our service identity: %s", ErrUnavailable, err.Error())
	default:
		return fmt.Errorf("document service client: %w", err)
	}
}
