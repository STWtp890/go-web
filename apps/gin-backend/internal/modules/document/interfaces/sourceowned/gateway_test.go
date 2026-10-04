package sourceowned

import (
	"context"
	"errors"
	"testing"
	"time"

	documentv1 "packages/gen/document/v1"
	documentsearchv1 "packages/gen/documentsearch/v1"

	"gin-backend/internal/modules/document/infrastructure/documentservice"
)

// fakeDocuments records the commands the gateway sends to the document service
// and returns canned transport responses. It is deliberately a fake at the
// client boundary: the gateway's job is choosing which RPC, with which fields,
// and mapping the answer back — not re-testing the document service.
type fakeDocuments struct {
	createRequest *documentv1.CreateDocumentRequest
	saveRequest   *documentv1.SaveDocumentRequest
	trashID       string
	listRequest   *documentv1.ListDocumentsRequest
	listResponse  *documentv1.ListDocumentsResponse
	detail        *documentv1.DocumentDetail
	saveResponse  *documentv1.SaveDocumentResponse
	capability    *documentv1.IssueSearchCapabilityResponse
	err           error
}

func (fake *fakeDocuments) CreateDocument(_ context.Context, _ string, request *documentv1.CreateDocumentRequest) (*documentv1.DocumentDetail, error) {
	fake.createRequest = request
	return fake.detail, fake.err
}

func (fake *fakeDocuments) SaveDocument(_ context.Context, _ string, request *documentv1.SaveDocumentRequest) (*documentv1.SaveDocumentResponse, error) {
	fake.saveRequest = request
	if fake.err != nil {
		return nil, fake.err
	}
	if fake.saveResponse != nil {
		return fake.saveResponse, nil
	}
	return &documentv1.SaveDocumentResponse{Document: fake.detail}, nil
}

func (fake *fakeDocuments) TrashDocument(_ context.Context, _, documentID, _ string) error {
	fake.trashID = documentID
	return fake.err
}

func (fake *fakeDocuments) GetDocument(_ context.Context, _, _ string) (*documentv1.DocumentDetail, error) {
	return fake.detail, fake.err
}

func (fake *fakeDocuments) ListDocuments(_ context.Context, _ string, request *documentv1.ListDocumentsRequest) ([]*documentv1.DocumentSummary, string, int64, error) {
	fake.listRequest = request
	if fake.err != nil {
		return nil, "", 0, fake.err
	}
	response := fake.listResponse
	if response == nil {
		response = &documentv1.ListDocumentsResponse{}
	}
	return response.GetDocuments(), response.GetNextPageToken(), response.GetTotalCount(), nil
}

func (fake *fakeDocuments) IssueSearchCapability(_ context.Context, _ string, _ *documentv1.ConversationContext, _, _ []string) (*documentv1.IssueSearchCapabilityResponse, error) {
	if fake.err != nil {
		return nil, fake.err
	}
	if fake.capability != nil {
		return fake.capability, nil
	}
	return &documentv1.IssueSearchCapabilityResponse{
		Decision:   documentv1.Decision_DECISION_GRANTED,
		Capability: "capability-token",
	}, nil
}

// fakeSearch records the query the gateway presents to the search service.
type fakeSearch struct {
	capability         string
	spaces             []string
	documents          []string
	query              string
	page               int
	pageSize           int
	ownedBySubjectOnly bool
	response           *documentsearchv1.SearchDocumentsResponse
	err                error
}

func (fake *fakeSearch) Search(_ context.Context, _, capability string, spaces, documents []string, query string, page, pageSize int, ownedBySubjectOnly bool) (*documentsearchv1.SearchDocumentsResponse, error) {
	fake.capability, fake.spaces, fake.documents = capability, spaces, documents
	fake.query, fake.page, fake.pageSize, fake.ownedBySubjectOnly = query, page, pageSize, ownedBySubjectOnly
	if fake.err != nil {
		return nil, fake.err
	}
	if fake.response == nil {
		return &documentsearchv1.SearchDocumentsResponse{}, nil
	}
	return fake.response, nil
}

func newTestGateway(t *testing.T, documents documentserviceAPI, search documentsearchAPI) *Gateway {
	t.Helper()
	gateway, err := New(Config{Documents: documents, Search: search})
	if err != nil {
		t.Fatalf("build gateway: %v", err)
	}
	return gateway
}

func TestUpdateCallsSaveDocumentWithExplicitPolicyRevisionAndRequestID(t *testing.T) {
	// "Edit and save" must be the document service's single-transaction use case,
	// not a draft followed by a publish, and the access policy must be sent as an
	// explicit presence so the service can apply it with the new version.
	documents := &fakeDocuments{detail: &documentv1.DocumentDetail{
		Summary: &documentv1.DocumentSummary{
			DocumentId: "doc-1", ActiveVersionId: "version-2",
			AuthenticatedPublic: true,
		},
		Version:             &documentv1.DocumentVersion{Title: "新标题", Content: "新正文"},
		AuthenticatedPublic: true,
	}}
	gateway := newTestGateway(t, documents, &fakeSearch{})

	result, err := gateway.Update(context.Background(), UpdateCommand{
		OwnerID: 42, DocumentID: "doc-1", Title: "新标题", Content: "新正文",
		AuthenticatedPublic: true, ExpectedAggregateRevision: 7, RequestID: "save-key-1",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	request := documents.saveRequest
	if request == nil {
		t.Fatal("Update must call SaveDocument; it called nothing")
	}
	if request.GetDocumentId() != "doc-1" || request.GetTitle() != "新标题" || request.GetContent() != "新正文" {
		t.Fatalf("save request = %+v", request)
	}
	if request.AuthenticatedPublic == nil {
		t.Fatal("the Web editor always states a visibility: the policy change must be an explicit presence")
	}
	if !request.GetAuthenticatedPublic() {
		t.Fatalf("policy = false, want the public flag the editor sent")
	}
	if request.GetExpectedAggregateRevision() != 7 {
		t.Fatalf("expected revision = %d, want 7", request.GetExpectedAggregateRevision())
	}
	if request.GetRequestId() != "save-key-1" {
		t.Fatalf("request id = %q, want save-key-1", request.GetRequestId())
	}
	if result.Document.GetVersion().GetContent() != "新正文" || !result.Document.GetAuthenticatedPublic() {
		t.Fatalf("save result must carry the newly active body and policy: %+v", result.Document)
	}
}

func TestUpdateKeepsPrivatePolicyExplicit(t *testing.T) {
	documents := &fakeDocuments{detail: &documentv1.DocumentDetail{Summary: &documentv1.DocumentSummary{DocumentId: "doc-1"}}}
	gateway := newTestGateway(t, documents, &fakeSearch{})

	if _, err := gateway.Update(context.Background(), UpdateCommand{
		OwnerID: 42, DocumentID: "doc-1", Title: "t", Content: "c", AuthenticatedPublic: false, RequestID: "key",
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if documents.saveRequest.AuthenticatedPublic == nil || documents.saveRequest.GetAuthenticatedPublic() {
		t.Fatalf("private must be sent as an explicit false, not as an absent field: %+v", documents.saveRequest)
	}
}

func TestUpdateMapsForbiddenToForbiddenClass(t *testing.T) {
	// A non-owner save is refused by the document service; the Web surface must
	// render 403, never a generic failure.
	documents := &fakeDocuments{err: documentservice.ErrForbidden}
	gateway := newTestGateway(t, documents, &fakeSearch{})

	_, err := gateway.Update(context.Background(), UpdateCommand{OwnerID: 7, DocumentID: "doc-1", Title: "t", Content: "c"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
	if class := ClassifyError(err); class != StatusForbidden {
		t.Fatalf("class = %v, want StatusForbidden", class)
	}
}

func TestListOwnedForwardsCursorVerbatimAndUsesRealTotal(t *testing.T) {
	documents := &fakeDocuments{listResponse: &documentv1.ListDocumentsResponse{
		Documents: []*documentv1.DocumentSummary{{
			DocumentId: "doc-10", Title: "第十篇",
			AuthenticatedPublic: true,
			CreatedAt:           "2026-09-01T01:00:00Z", UpdatedAt: "2026-09-02T02:00:00Z",
		}},
		NextPageToken: "cursor-2",
		TotalCount:    25,
	}}
	gateway := newTestGateway(t, documents, &fakeSearch{})

	items, page, err := gateway.ListOwned(context.Background(), 42, 9, "cursor-1")
	if err != nil {
		t.Fatalf("list owned: %v", err)
	}
	if documents.listRequest.GetPageToken() != "cursor-1" {
		t.Fatalf("page token = %q, want the cursor the client sent, unchanged", documents.listRequest.GetPageToken())
	}
	if documents.listRequest.GetPageSize() != 9 {
		t.Fatalf("page size = %d, want 9", documents.listRequest.GetPageSize())
	}
	if documents.listRequest.GetOwnerSubjectKey() != "web:user:42" {
		t.Fatalf("owner subject = %q, want web:user:42", documents.listRequest.GetOwnerSubjectKey())
	}
	if page.NextCursor != "cursor-2" {
		t.Fatalf("next cursor = %q, want cursor-2", page.NextCursor)
	}
	if page.Total != 25 {
		t.Fatalf("total = %d, want the service's real total_count 25", page.Total)
	}
	if len(items) != 1 || !items[0].AuthenticatedPublic {
		t.Fatalf("summary public flag must come from the policy field: %+v", items)
	}
	wantCreated, _ := time.Parse(time.RFC3339, "2026-09-01T01:00:00Z")
	if !items[0].CreatedAt.Equal(wantCreated) {
		t.Fatalf("created at = %s, want %s", items[0].CreatedAt, wantCreated)
	}
}

func TestListPublicRequestsOnlyAuthenticatedPublicDocuments(t *testing.T) {
	documents := &fakeDocuments{listResponse: &documentv1.ListDocumentsResponse{
		Documents: []*documentv1.DocumentSummary{{
			DocumentId: "doc-1", Title: "公开",
			// A published-but-private document must render as private: the flag
			// comes from the access policy, not from the publication status.
			PublicationStatus:   documentv1.PublicationStatus_PUBLICATION_STATUS_PUBLISHED,
			AuthenticatedPublic: false,
		}},
		TotalCount: 1,
	}}
	gateway := newTestGateway(t, documents, &fakeSearch{})

	items, _, err := gateway.ListPublic(context.Background(), 42, 9, "")
	if err != nil {
		t.Fatalf("list public: %v", err)
	}
	if !documents.listRequest.GetAuthenticatedPublicOnly() {
		t.Fatal("the public list must filter on authenticated_public_only")
	}
	if len(items) != 1 || items[0].AuthenticatedPublic {
		t.Fatalf("published-but-private must render private: %+v", items)
	}
}

func TestSearchIsPersonalAndUsesRealFields(t *testing.T) {
	documents := &fakeDocuments{}
	search := &fakeSearch{response: &documentsearchv1.SearchDocumentsResponse{
		Total: 12,
		Hits: []*documentsearchv1.SearchHit{{
			DocumentId: "doc-4", Title: "命中标题", Snippet: "命中片段",
			OwnerSubjectKey: "web:user:42", AuthenticatedPublic: true,
			CreatedAt: "2026-08-01T00:00:00Z", UpdatedAt: "2026-08-02T00:00:00Z",
		}},
	}}
	gateway := newTestGateway(t, documents, search)

	items, page, keyword, err := gateway.Search(context.Background(), 42, "  orion  ", 2, 9)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if keyword != "orion" {
		t.Fatalf("keyword = %q, want the trimmed keyword", keyword)
	}
	if !search.ownedBySubjectOnly {
		t.Fatal("a Web search is personal: owned_by_subject_only must be set so other subjects' documents cannot appear")
	}
	if search.page != 2 || search.pageSize != 9 {
		t.Fatalf("page/pageSize forwarded = %d/%d, want 2/9", search.page, search.pageSize)
	}
	if search.capability != "capability-token" {
		t.Fatalf("capability = %q, want the one the document service minted", search.capability)
	}
	if page.Total != 12 {
		t.Fatalf("total = %d, want the response's real total 12", page.Total)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if !items[0].AuthenticatedPublic {
		t.Fatal("visibility must come from the hit's authenticated_public flag")
	}
	created, _ := time.Parse(time.RFC3339, "2026-08-01T00:00:00Z")
	updated, _ := time.Parse(time.RFC3339, "2026-08-02T00:00:00Z")
	if !items[0].CreatedAt.Equal(created) || !items[0].UpdatedAt.Equal(updated) {
		t.Fatalf("hit times = %s/%s, want the indexed created/updated times", items[0].CreatedAt, items[0].UpdatedAt)
	}
}

func TestSearchDeniedScopeReturnsNoResultsAndNeverQueriesIndex(t *testing.T) {
	documents := &fakeDocuments{capability: &documentv1.IssueSearchCapabilityResponse{
		Decision:     documentv1.Decision_DECISION_DENIED,
		DeniedReason: documentv1.DeniedReason_DENIED_REASON_SUBJECT_UNKNOWN,
	}}
	search := &fakeSearch{}
	gateway := newTestGateway(t, documents, search)

	items, page, _, err := gateway.Search(context.Background(), 42, "orion", 1, 9)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(items) != 0 || page.Total != 0 {
		t.Fatalf("denied scope must render as no accessible documents: %+v", items)
	}
	if search.query != "" {
		t.Fatal("a denied scope must never be presented to the search service, not even as an empty range")
	}
}

// TestSearchForwardsTotalAndTruncated pins the F02 contract on the Web side: the
// answer's paging numbers come from the search service and are never recomputed
// from the page that happened to be returned.
func TestSearchForwardsTotalAndTruncated(t *testing.T) {
	documents := &fakeDocuments{}
	search := &fakeSearch{response: &documentsearchv1.SearchDocumentsResponse{
		Total: 3,
		Hits: []*documentsearchv1.SearchHit{{
			DocumentId: "doc-9", Title: "只有一条命中",
		}},
		Truncated: true,
	}}
	gateway := newTestGateway(t, documents, search)

	items, page, _, err := gateway.Search(context.Background(), 42, "orion", 1, 1)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if page.Total != 3 {
		t.Fatalf("total = %d, want the service total 3 rather than the %d hits on this page", page.Total, len(items))
	}
	if !page.Truncated {
		t.Fatal("truncated must be forwarded: the client cannot warn about a result set it was never told about")
	}
	if page.Number != 1 || page.Size != 1 {
		t.Fatalf("page = %d/%d, want 1/1", page.Number, page.Size)
	}
}

func TestListPagesAreNotTruncated(t *testing.T) {
	// Cursor lists have no truncation concept: a next cursor means "ask again",
	// while truncated would claim the answer itself is incomplete.
	documents := &fakeDocuments{listResponse: &documentv1.ListDocumentsResponse{
		Documents:     []*documentv1.DocumentSummary{{DocumentId: "doc-1"}},
		NextPageToken: "cursor-2",
		TotalCount:    25,
	}}
	gateway := newTestGateway(t, documents, &fakeSearch{})

	_, page, err := gateway.ListOwned(context.Background(), 42, 9, "")
	if err != nil {
		t.Fatalf("list owned: %v", err)
	}
	if page.Truncated {
		t.Fatal("a cursor list must not report truncation")
	}
	if page.NextCursor != "cursor-2" || page.Total != 25 {
		t.Fatalf("page = %+v, want cursor-2 and total 25", page)
	}
}

func TestUpdateCarriesReplayAndAppliedVersion(t *testing.T) {
	documents := &fakeDocuments{
		detail: &documentv1.DocumentDetail{
			Summary: &documentv1.DocumentSummary{DocumentId: "doc-1", ActiveVersionId: "version-9"},
		},
		saveResponse: &documentv1.SaveDocumentResponse{
			Document: &documentv1.DocumentDetail{
				Summary: &documentv1.DocumentSummary{DocumentId: "doc-1", ActiveVersionId: "version-9"},
				Version: &documentv1.DocumentVersion{VersionId: "version-9", Content: "第一次写入的正文"},
			},
			Replayed:         true,
			AppliedVersionId: "version-9",
		},
	}
	gateway := newTestGateway(t, documents, &fakeSearch{})

	result, err := gateway.Update(context.Background(), UpdateCommand{
		OwnerID: 42, DocumentID: "doc-1", Title: "t", Content: "重试的正文", RequestID: "same-key",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !result.Replayed {
		t.Fatal("replayed must be forwarded so the editor can say the request was already applied")
	}
	if result.AppliedVersionID != "version-9" {
		t.Fatalf("applied version = %q, want version-9", result.AppliedVersionID)
	}
}
