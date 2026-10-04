package sourceowned

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	documentv1 "packages/gen/document/v1"
	documentsearchv1 "packages/gen/documentsearch/v1"

	"gin-backend/internal/modules/document/domain"
	"gin-backend/internal/modules/document/infrastructure/documentsearch"
	"gin-backend/internal/modules/document/infrastructure/documentservice"
)

// SubjectResolver maps an authenticated Web user to the resource access subject
// the document service knows. go-web asserts the subject; the document service
// decides what that subject may do.
type SubjectResolver interface {
	WebSubjectKey(userID int64) (string, error)
}

// ResolverFunc adapts a function to SubjectResolver.
type ResolverFunc func(int64) (string, error)

// WebSubjectKey implements SubjectResolver.
func (fn ResolverFunc) WebSubjectKey(userID int64) (string, error) { return fn(userID) }

// DefaultSubjectResolver builds the canonical `web:user:<id>` subject key.
func DefaultSubjectResolver(userID int64) (string, error) {
	return documentservice.WebSubjectKey(userID)
}

// Gateway implements DocumentGateway on top of the document service and the
// formal document search service.
type Gateway struct {
	documents documentserviceAPI
	search    documentsearchAPI
	subjects  SubjectResolver
}

// documentserviceAPI is the subset of the document service client this gateway
// uses. Declaring it as an interface keeps the gateway testable without a live
// service and stops the HTTP surface from reaching into transport types.
type documentserviceAPI interface {
	CreateDocument(ctx context.Context, subjectKey string, request *documentv1.CreateDocumentRequest) (*documentv1.DocumentDetail, error)
	SaveDocument(ctx context.Context, subjectKey string, request *documentv1.SaveDocumentRequest) (*documentv1.SaveDocumentResponse, error)
	TrashDocument(ctx context.Context, subjectKey, documentID, requestID string) error
	GetDocument(ctx context.Context, subjectKey, documentID string) (*documentv1.DocumentDetail, error)
	ListDocuments(ctx context.Context, subjectKey string, request *documentv1.ListDocumentsRequest) ([]*documentv1.DocumentSummary, string, int64, error)
	IssueSearchCapability(ctx context.Context, subjectKey string, conversation *documentv1.ConversationContext, requestedSpaces, requestedDocuments []string) (*documentv1.IssueSearchCapabilityResponse, error)
}

// documentsearchAPI is the subset of the search client this gateway uses.
type documentsearchAPI interface {
	Search(ctx context.Context, subjectKey, capability string, requestedSpaces, requestedDocuments []string, query string, page, pageSize int, ownedBySubjectOnly bool) (*documentsearchv1.SearchDocumentsResponse, error)
}

// Config wires the gateway.
type Config struct {
	Documents documentserviceAPI
	Search    documentsearchAPI
	Subjects  SubjectResolver
}

// Errors the Web layer maps to HTTP status codes.
var (
	// ErrForbidden means the document service denied the resource.
	ErrForbidden = errors.New("document service gateway: forbidden")
	// ErrNotFound means the document does not exist or is not visible.
	ErrNotFound = errors.New("document service gateway: not found")
	// ErrInvalidInput means the caller sent something the service rejected.
	ErrInvalidInput = errors.New("document service gateway: invalid input")
	// ErrConflict means a concurrency or state precondition failed.
	ErrConflict = errors.New("document service gateway: precondition failed")
	// ErrUnavailable means a downstream source-owned service could not be used.
	ErrUnavailable = errors.New("document service gateway: unavailable")
	// ErrNotConfigured means the gateway was built without its dependencies.
	ErrNotConfigured = errors.New("document service gateway: not configured")
)

// New validates the wiring. A gateway without both clients must not exist: a
// half-wired Web document surface would have to fall back to a direct table
// path, which is exactly what ADR-017 removed.
func New(config Config) (*Gateway, error) {
	if config.Documents == nil {
		return nil, fmt.Errorf("%w: document service client is required", ErrNotConfigured)
	}
	if config.Search == nil {
		return nil, fmt.Errorf("%w: document search client is required", ErrNotConfigured)
	}
	if config.Subjects == nil {
		config.Subjects = ResolverFunc(DefaultSubjectResolver)
	}
	return &Gateway{
		documents: config.Documents,
		search:    config.Search,
		subjects:  config.Subjects,
	}, nil
}

// CreateCommand is the Web create request after the HTTP layer validated it.
type CreateCommand struct {
	OwnerID             int64
	Title               string
	Content             string
	AuthenticatedPublic bool
	RequestID           string
}

// UpdateCommand is the Web "edit and save" request.
//
// AuthenticatedPublic is the explicit access policy the edit page sent. It is
// always present for a Web save (the editor always states public/private), and
// presence — not the boolean value — is what tells the document service that a
// policy change is requested. ExpectedAggregateRevision is optional optimistic
// concurrency: 0 means "do not check". RequestID is the idempotency key, so a
// retried save replays instead of appending a second version.
type UpdateCommand struct {
	OwnerID                   int64
	DocumentID                string
	Title                     string
	Content                   string
	AuthenticatedPublic       bool
	ExpectedAggregateRevision uint64
	RequestID                 string
}

// TrashCommand is the Web delete request.
type TrashCommand struct {
	OwnerID    int64
	DocumentID string
}

// MutationResult is the detail returned after a successful mutation.
type MutationResult struct {
	Document *documentv1.DocumentDetail
	// Replayed reports that the document service recognised the request id and
	// returned the state an earlier attempt produced instead of saving again.
	Replayed bool
	// AppliedVersionID is the version this request id produced: on a fresh save
	// the version just activated, on a replay the version the first attempt
	// created. It is how a retried save is correlated with its original effect.
	AppliedVersionID string
}

// Create creates a formal document through the document service.
func (gateway *Gateway) Create(ctx context.Context, command CreateCommand) (*MutationResult, error) {
	subjectKey, err := gateway.subject(command.OwnerID)
	if err != nil {
		return nil, err
	}
	detail, err := gateway.documents.CreateDocument(ctx, subjectKey, &documentv1.CreateDocumentRequest{
		Title:               command.Title,
		Content:             command.Content,
		ContentFormat:       "markdown",
		AuthenticatedPublic: command.AuthenticatedPublic,
		Source:              &documentv1.DocumentSource{Origin: "web"},
		RequestId:           command.RequestID,
	})
	if err != nil {
		return nil, translate(err)
	}
	return &MutationResult{Document: detail}, nil
}

// Update runs the document service "edit and save" use case.
//
// It must not be a draft followed by a publish: the access policy change, the
// version switch and the index event belong to one transaction, and only
// SaveDocument performs all three together. The returned detail is the version
// that was just activated, so the caller can render the new body immediately.
func (gateway *Gateway) Update(ctx context.Context, command UpdateCommand) (*MutationResult, error) {
	subjectKey, err := gateway.subject(command.OwnerID)
	if err != nil {
		return nil, err
	}
	// The Web editor always states a visibility, so the access policy is always
	// sent explicitly: taking the pointer means "change the policy", and the
	// document service applies it in the same transaction as the new version.
	authenticatedPublic := command.AuthenticatedPublic
	response, err := gateway.documents.SaveDocument(ctx, subjectKey, &documentv1.SaveDocumentRequest{
		DocumentId:                command.DocumentID,
		Title:                     command.Title,
		Content:                   command.Content,
		ContentFormat:             "markdown",
		AuthenticatedPublic:       &authenticatedPublic,
		ExpectedAggregateRevision: command.ExpectedAggregateRevision,
		RequestId:                 command.RequestID,
	})
	if err != nil {
		return nil, translate(err)
	}
	return &MutationResult{
		Document: response.GetDocument(),
		// A replay means "this request id was already committed and this call
		// changed nothing": the Web layer must say so instead of claiming a fresh
		// save, and the applied version id identifies what the original attempt
		// actually wrote.
		Replayed:         response.GetReplayed(),
		AppliedVersionID: response.GetAppliedVersionId(),
	}, nil
}

// Trash moves a document out of the searchable set through the document service.
func (gateway *Gateway) Trash(ctx context.Context, command TrashCommand) error {
	subjectKey, err := gateway.subject(command.OwnerID)
	if err != nil {
		return err
	}
	return translate(gateway.documents.TrashDocument(ctx, subjectKey, command.DocumentID, ""))
}

// Get reads one document's detail through the document service.
func (gateway *Gateway) Get(ctx context.Context, viewerID int64, documentID string) (*domain.DocumentView, error) {
	subjectKey, err := gateway.subject(viewerID)
	if err != nil {
		return nil, err
	}
	detail, err := gateway.documents.GetDocument(ctx, subjectKey, documentID)
	if err != nil {
		return nil, translate(err)
	}
	return detailToView(detail), nil
}

// ListOwned lists the documents owned by one Web subject.
//
// cursor is the opaque forward cursor the document service issued on the
// previous page; it is passed back verbatim. pageSize is the only page shape
// go-web chooses: it never converts a page number into an offset, because the
// service owns the ordering and only it can name a position in it. The total is
// the service's real total_count, not a count of the returned page.
func (gateway *Gateway) ListOwned(ctx context.Context, ownerID int64, pageSize int, cursor string) ([]domain.DocumentSummary, domain.Page, error) {
	subjectKey, err := gateway.subject(ownerID)
	if err != nil {
		return nil, domain.Page{}, err
	}
	summaries, next, total, err := gateway.documents.ListDocuments(ctx, subjectKey, &documentv1.ListDocumentsRequest{
		OwnerSubjectKey: subjectKey,
		LifecycleStatus: documentv1.LifecycleStatus_LIFECYCLE_STATUS_ACTIVE,
		PageSize:        int32(pageSize),
		PageToken:       cursor,
	})
	if err != nil {
		return nil, domain.Page{}, translate(err)
	}
	items := summariesToDomain(summaries)
	return items, domain.Page{Size: pageSize, Total: total, NextCursor: next}, nil
}

// ListPublic lists authenticated-public documents the subject may read.
func (gateway *Gateway) ListPublic(ctx context.Context, viewerID int64, pageSize int, cursor string) ([]domain.DocumentSummary, domain.Page, error) {
	subjectKey, err := gateway.subject(viewerID)
	if err != nil {
		return nil, domain.Page{}, err
	}
	summaries, next, total, err := gateway.documents.ListDocuments(ctx, subjectKey, &documentv1.ListDocumentsRequest{
		LifecycleStatus:         documentv1.LifecycleStatus_LIFECYCLE_STATUS_ACTIVE,
		AuthenticatedPublicOnly: true,
		PageSize:                int32(pageSize),
		PageToken:               cursor,
	})
	if err != nil {
		return nil, domain.Page{}, translate(err)
	}
	items := summariesToDomain(summaries)
	return items, domain.Page{Size: pageSize, Total: total, NextCursor: next}, nil
}

// Search runs a formal document search inside the scope the document service
// granted.
//
// The chain is one-directional: go-web asks the document service for a resource
// capability, then presents that capability to the search service. The search
// service validates it offline, so it never has to call back into the fact source
// while answering.
//
// A Web search is always a personal search: owned_by_subject_only is set, and the
// owner is read from the validated capability by the search service, so another
// subject's documents — public or private — can never appear in the answer. The
// page number and size are forwarded as page/page_size.
//
// Paging is only as honest as the numbers the search service returns: total is the
// count of results the caller can actually page through (so ceil(total/page_size)
// is the last page that returns anything), and truncated says whether the answer
// leaves results out. go-web forwards both instead of deriving its own page count.
func (gateway *Gateway) Search(ctx context.Context, viewerID int64, keyword string, page, pageSize int) ([]domain.DocumentSummary, domain.Page, string, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, domain.Page{}, "", fmt.Errorf("%w: keyword is required", ErrInvalidInput)
	}
	subjectKey, err := gateway.subject(viewerID)
	if err != nil {
		return nil, domain.Page{}, "", err
	}
	issued, err := gateway.documents.IssueSearchCapability(ctx, subjectKey, privateConversation(), nil, nil)
	if err != nil {
		return nil, domain.Page{}, "", translate(err)
	}
	if issued.GetDecision() != documentv1.Decision_DECISION_GRANTED {
		// A denial is reported as "no accessible documents" and deliberately not
		// as an empty-but-valid grant: the search service must never be asked with
		// a range the fact source refused to sign.
		return []domain.DocumentSummary{}, domain.Page{Number: page, Size: pageSize, Total: 0}, keyword, nil
	}
	response, err := gateway.search.Search(ctx, subjectKey, issued.GetCapability(), nil, nil, keyword, page, pageSize, true)
	if err != nil {
		return nil, domain.Page{}, "", translateSearch(err)
	}
	items := make([]domain.DocumentSummary, 0, len(response.GetHits()))
	for _, hit := range response.GetHits() {
		items = append(items, domain.DocumentSummary{
			DocumentID: hit.GetDocumentId(),
			// The hit carries the fields the Web surface renders, stored in the
			// index projection when the event was applied. Falling back to the
			// query time would invent a creation instant the document never had.
			OwnerID:             webUserIDFromSubject(hit.GetOwnerSubjectKey()),
			AuthenticatedPublic: hit.GetAuthenticatedPublic(),
			Title:               hit.GetTitle(),
			Summary:             hit.GetSnippet(),
			CreatedAt:           parseTimestamp(hit.GetCreatedAt()),
			UpdatedAt:           parseTimestamp(hit.GetUpdatedAt()),
		})
	}
	// Owner ids come from the hit's owner_subject_key, which the index stored
	// when it applied the event. A subject outside go-web's namespace stays
	// unattributed rather than being guessed onto a Web account.
	//
	// total is the number of results the caller can actually page through, and
	// truncated means the answer does not include everything that matched. Both
	// are passed through untouched: recomputing either one here is what produced
	// pages that could be clicked but never returned anything.
	return items, domain.Page{
		Number:    page,
		Size:      pageSize,
		Total:     int64(response.GetTotal()),
		Truncated: response.GetTruncated(),
	}, keyword, nil
}

// Page mirrors the document module's page model.

func (gateway *Gateway) subject(userID int64) (string, error) {
	if userID <= 0 {
		return "", fmt.Errorf("%w: viewer id must be positive", ErrInvalidInput)
	}
	return gateway.subjects.WebSubjectKey(userID)
}

func privateConversation() *documentv1.ConversationContext {
	// The Web surface always acts in a private conversation: it has no group
	// context, and the document service must not be told otherwise.
	return &documentv1.ConversationContext{Kind: documentv1.ConversationKind_CONVERSATION_KIND_PRIVATE}
}

// summariesToDomain maps list summaries without inferring anything the document
// service did not send: the public flag is read from the access policy, not from
// the publication status, so a list and a detail read of the same document
// cannot disagree about visibility.
func summariesToDomain(summaries []*documentv1.DocumentSummary) []domain.DocumentSummary {
	items := make([]domain.DocumentSummary, 0, len(summaries))
	for _, summary := range summaries {
		items = append(items, domain.DocumentSummary{
			DocumentID:          summary.GetDocumentId(),
			OwnerID:             webUserIDFromSubject(summary.GetOwnerSubjectKey()),
			AuthenticatedPublic: summary.GetAuthenticatedPublic(),
			Title:               summary.GetTitle(),
			CreatedAt:           parseTimestamp(summary.GetCreatedAt()),
			UpdatedAt:           parseTimestamp(summary.GetUpdatedAt()),
		})
	}
	return items
}

// webUserIDFromSubject maps a document-service subject key back to the Web
// account id. go-web may only claim its own namespace, so anything else (a QQ
// subject, an unknown future namespace) yields 0 and the surface renders no
// author rather than attributing the document to the wrong account.
func webUserIDFromSubject(subjectKey string) int64 {
	const prefix = "web:user:"
	if !strings.HasPrefix(subjectKey, prefix) {
		return 0
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(subjectKey, prefix), 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

func detailToView(detail *documentv1.DocumentDetail) *domain.DocumentView {
	if detail == nil {
		return nil
	}
	summary := detail.GetSummary()
	version := detail.GetVersion()
	return &domain.DocumentView{
		DocumentHead: domain.DocumentHead{
			DocumentID:          summary.GetDocumentId(),
			OwnerID:             webUserIDFromSubject(summary.GetOwnerSubjectKey()),
			ActiveVersionID:     summary.GetActiveVersionId(),
			AuthenticatedPublic: detail.GetAuthenticatedPublic(),
			ActivationRevision:  int64(summary.GetAggregateRevision()),
			CreatedAt:           parseTimestamp(summary.GetCreatedAt()),
			UpdatedAt:           parseTimestamp(summary.GetUpdatedAt()),
		},
		Title:   version.GetTitle(),
		Summary: version.GetSummary(),
		Content: version.GetContent(),
	}
}

// MutationDetailToView exposes the detail mapping so the HTTP adapter renders a
// mutation result without duplicating field choices.
func MutationDetailToView(detail *documentv1.DocumentDetail) *domain.DocumentView {
	return detailToView(detail)
}

func parseTimestamp(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

// HTTPStatusClass is the coarse outcome of a gateway error, expressed without
// HTTP or gRPC types so the HTTP adapter owns the exact status codes.
type HTTPStatusClass int

const (
	// StatusUnknown maps to an internal failure.
	StatusUnknown HTTPStatusClass = iota
	// StatusNotFound means the resource does not exist or is not visible.
	StatusNotFound
	// StatusForbidden means the caller may not access the resource.
	StatusForbidden
	// StatusInvalidInput means the request shape was rejected.
	StatusInvalidInput
	// StatusConflict means a precondition failed.
	StatusConflict
	// StatusUnavailable means a downstream service could not be used.
	StatusUnavailable
)

// ClassifyError maps a gateway error to its coarse outcome. It is exported so the
// HTTP adapter renders status codes from one place instead of re-deriving them.
func ClassifyError(err error) HTTPStatusClass {
	switch {
	case err == nil:
		return StatusUnknown
	case errors.Is(err, ErrNotFound):
		return StatusNotFound
	case errors.Is(err, ErrForbidden):
		return StatusForbidden
	case errors.Is(err, ErrInvalidInput):
		return StatusInvalidInput
	case errors.Is(err, ErrConflict):
		return StatusConflict
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrNotConfigured):
		return StatusUnavailable
	default:
		return StatusUnknown
	}
}

// translate maps document service client errors to gateway errors.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, documentservice.ErrNotFound):
		return fmt.Errorf("%w: %s", ErrNotFound, err.Error())
	case errors.Is(err, documentservice.ErrForbidden):
		return fmt.Errorf("%w: %s", ErrForbidden, err.Error())
	case errors.Is(err, documentservice.ErrInvalidInput):
		return fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())
	case errors.Is(err, documentservice.ErrConflict):
		return fmt.Errorf("%w: %s", ErrConflict, err.Error())
	case errors.Is(err, documentservice.ErrUnavailable), errors.Is(err, documentservice.ErrNotConfigured):
		return fmt.Errorf("%w: %s", ErrUnavailable, err.Error())
	default:
		return err
	}
}

func translateSearch(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, documentsearch.ErrForbidden):
		return fmt.Errorf("%w: %s", ErrForbidden, err.Error())
	case errors.Is(err, documentsearch.ErrInvalidInput):
		return fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())
	case errors.Is(err, documentsearch.ErrUnavailable):
		return fmt.Errorf("%w: %s", ErrUnavailable, err.Error())
	default:
		return err
	}
}
