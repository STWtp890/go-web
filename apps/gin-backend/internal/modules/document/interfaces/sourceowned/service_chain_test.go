package sourceowned

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	documentv1 "packages/gen/document/v1"
	documentsearchv1 "packages/gen/documentsearch/v1"
	"packages/serviceauth"

	"gin-backend/internal/modules/document/infrastructure/documentsearch"
	"gin-backend/internal/modules/document/infrastructure/documentservice"
	"gin-backend/internal/platform/httpserver/identity"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// This file drives the whole Web document surface the way production does: real
// HTTP handlers, the real gateway, the real gRPC clients and a real grpc.Server
// reached over TCP. The document service and the search service behind them are
// in-process fakes, because those two services are separate deliverables with
// their own test suites; what is under test here is go-web's side of the
// contract — which RPC it calls, with which fields, and how it renders the
// answer.
//
// The fakes behave like the contract requires (cursor listing with total_count,
// single-transaction save with ownership and idempotency, personal search with a
// real total), so the acceptance numbers below are meaningful: 25 documents at 9
// per page must come back as 9, 9, 7 with no repeats and no gaps.

const (
	testOwnerSubject  = "web:user:42"
	testOtherSubject  = "web:user:99"
	testDocumentCount = 25
	testPageSize      = 9
)

var testBoundaryKey = []byte("go-web-test-boundary-key-32bytes!")

type fakeDocument struct {
	id              string
	owner           string
	title           string
	content         string
	public          bool
	revision        uint64
	activeVersionID string
	createdAt       time.Time
	updatedAt       time.Time
}

func (document *fakeDocument) summary() *documentv1.DocumentSummary {
	return &documentv1.DocumentSummary{
		DocumentId:      document.id,
		OwnerSubjectKey: document.owner,
		ActiveVersionId: document.activeVersionID,
		Title:           document.title,
		LifecycleStatus: documentv1.LifecycleStatus_LIFECYCLE_STATUS_ACTIVE,
		// Every fake document is published, and visibility still varies: that is
		// exactly the regression the surface must not have — publication says
		// which version is active, the policy says who may read it.
		PublicationStatus:   documentv1.PublicationStatus_PUBLICATION_STATUS_PUBLISHED,
		AggregateRevision:   document.revision,
		CreatedAt:           document.createdAt.Format(time.RFC3339Nano),
		UpdatedAt:           document.updatedAt.Format(time.RFC3339Nano),
		AuthenticatedPublic: document.public,
	}
}

func (document *fakeDocument) detail() *documentv1.DocumentDetail {
	return &documentv1.DocumentDetail{
		Summary: document.summary(),
		Version: &documentv1.DocumentVersion{
			VersionId:           document.activeVersionID,
			DocumentId:          document.id,
			Revision:            document.revision,
			PublicationStatus:   documentv1.PublicationStatus_PUBLICATION_STATUS_PUBLISHED,
			Title:               document.title,
			Summary:             "摘要 " + document.id,
			Content:             document.content,
			ContentFormat:       "markdown",
			CreatedBySubjectKey: document.owner,
			CreatedAt:           document.updatedAt.Format(time.RFC3339Nano),
		},
		AuthenticatedPublic: document.public,
	}
}

// fakeDocumentBackend implements document.v1 over the wire.
type fakeDocumentBackend struct {
	documentv1.UnimplementedDocumentServiceServer

	mu            sync.Mutex
	codec         *serviceauth.Codec
	order         []string
	documents     map[string]*fakeDocument
	replays       map[string]*documentv1.SaveDocumentResponse
	listRequests  []*documentv1.ListDocumentsRequest
	saveRequests  []*documentv1.SaveDocumentRequest
	subjectErrors int
}

func newFakeDocumentBackend(t *testing.T) *fakeDocumentBackend {
	t.Helper()
	codec, err := serviceauth.NewCodec(testBoundaryKey)
	if err != nil {
		t.Fatalf("codec: %v", err)
	}
	backend := &fakeDocumentBackend{
		codec:     codec,
		documents: map[string]*fakeDocument{},
		replays:   map[string]*documentv1.SaveDocumentResponse{},
	}
	base := time.Unix(1_700_000_000, 0).UTC()
	for index := 1; index <= testDocumentCount; index++ {
		id := fmt.Sprintf("doc-%02d", index)
		backend.order = append(backend.order, id)
		backend.documents[id] = &fakeDocument{
			id:              id,
			owner:           testOwnerSubject,
			title:           fmt.Sprintf("文稿 %02d orion", index),
			content:         fmt.Sprintf("正文 %s 第一版 orion", id),
			public:          index%2 == 0,
			revision:        1,
			activeVersionID: "version-1-" + id,
			createdAt:       base.Add(time.Duration(index) * time.Minute),
			updatedAt:       base.Add(time.Duration(index) * time.Minute),
		}
	}
	// Another subject's authenticated-public document. A personal search must
	// never surface it, and it must stay out of the owner's own list.
	backend.order = append(backend.order, "doc-other")
	backend.documents["doc-other"] = &fakeDocument{
		id: "doc-other", owner: testOtherSubject, title: "别人的公开文稿 orion", content: "不属于当前主体",
		public: true, revision: 1, activeVersionID: "version-1-doc-other",
		createdAt: base, updatedAt: base,
	}
	return backend
}

func (backend *fakeDocumentBackend) subject(ctx context.Context, audience serviceauth.Audience) (string, error) {
	values := metadata.ValueFromIncomingContext(ctx, serviceauth.HeaderAuthorization)
	if len(values) != 1 {
		return "", status.Error(codes.Unauthenticated, "missing service assertion")
	}
	token, err := serviceauth.BearerToken(values[0])
	if err != nil {
		return "", status.Error(codes.Unauthenticated, err.Error())
	}
	seal, err := backend.codec.OpenAssertion(token, audience)
	if err != nil {
		return "", status.Error(codes.Unauthenticated, err.Error())
	}
	return seal.Claims.SubjectKey, nil
}

func (backend *fakeDocumentBackend) ListDocuments(ctx context.Context, request *documentv1.ListDocumentsRequest) (*documentv1.ListDocumentsResponse, error) {
	subject, err := backend.subject(ctx, serviceauth.AudienceDocumentService)
	if err != nil {
		return nil, err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.listRequests = append(backend.listRequests, request)

	matched := make([]*documentv1.DocumentSummary, 0, len(backend.order))
	for _, id := range backend.order {
		document := backend.documents[id]
		if owner := request.GetOwnerSubjectKey(); owner != "" && document.owner != owner {
			continue
		}
		if request.GetAuthenticatedPublicOnly() && !document.public {
			continue
		}
		matched = append(matched, document.summary())
	}
	_ = subject

	offset := 0
	if token := request.GetPageToken(); token != "" {
		parsed, parseErr := strconv.Atoi(strings.TrimPrefix(token, "cursor:"))
		if parseErr != nil || parsed < 0 {
			return nil, status.Error(codes.InvalidArgument, "page token is not a cursor this service issued")
		}
		offset = parsed
	}
	if offset > len(matched) {
		offset = len(matched)
	}
	size := int(request.GetPageSize())
	if size <= 0 {
		size = 10
	}
	end := offset + size
	if end > len(matched) {
		end = len(matched)
	}
	next := ""
	if end < len(matched) {
		next = "cursor:" + strconv.Itoa(end)
	}
	return &documentv1.ListDocumentsResponse{
		Documents:     matched[offset:end],
		NextPageToken: next,
		TotalCount:    int64(len(matched)),
	}, nil
}

func (backend *fakeDocumentBackend) GetDocument(ctx context.Context, request *documentv1.GetDocumentRequest) (*documentv1.GetDocumentResponse, error) {
	subject, err := backend.subject(ctx, serviceauth.AudienceDocumentService)
	if err != nil {
		return nil, err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	document, ok := backend.documents[request.GetDocumentId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "document not found")
	}
	if document.owner != subject && !document.public {
		return nil, status.Error(codes.PermissionDenied, "not the owner")
	}
	return &documentv1.GetDocumentResponse{Document: document.detail()}, nil
}

func (backend *fakeDocumentBackend) SaveDocument(ctx context.Context, request *documentv1.SaveDocumentRequest) (*documentv1.SaveDocumentResponse, error) {
	subject, err := backend.subject(ctx, serviceauth.AudienceDocumentService)
	if err != nil {
		return nil, err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.saveRequests = append(backend.saveRequests, request)

	document, ok := backend.documents[request.GetDocumentId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "document not found")
	}
	if document.owner != subject {
		// Only the owner may save. The contract makes this PERMISSION_DENIED, and
		// the Web surface must render 403 rather than a generic failure.
		backend.subjectErrors++
		return nil, status.Error(codes.PermissionDenied, "only the owner may save this document")
	}
	if replay, seen := backend.replays[request.GetRequestId()]; seen && request.GetRequestId() != "" {
		// A replay changes nothing: it returns the committed state and the version
		// the first attempt produced, so the caller can tell the two apart.
		return &documentv1.SaveDocumentResponse{
			Document:         replay.GetDocument(),
			Replayed:         true,
			AppliedVersionId: replay.GetAppliedVersionId(),
		}, nil
	}
	if expected := request.GetExpectedAggregateRevision(); expected != 0 && expected != document.revision {
		return nil, status.Error(codes.FailedPrecondition, "aggregate revision moved")
	}
	document.revision++
	document.activeVersionID = fmt.Sprintf("version-%d-%s", document.revision, document.id)
	document.title = request.GetTitle()
	document.content = request.GetContent()
	if request.AuthenticatedPublic != nil {
		// Presence, not the value, is what requests a policy change; the contract
		// requires the service to reject an absent field on UpdateDraft for the
		// same reason.
		document.public = request.GetAuthenticatedPublic()
	}
	document.updatedAt = document.updatedAt.Add(time.Minute)
	response := &documentv1.SaveDocumentResponse{
		Document:         document.detail(),
		AppliedVersionId: document.activeVersionID,
	}
	if request.GetRequestId() != "" {
		backend.replays[request.GetRequestId()] = response
	}
	return response, nil
}

func (backend *fakeDocumentBackend) IssueSearchCapability(ctx context.Context, request *documentv1.IssueSearchCapabilityRequest) (*documentv1.IssueSearchCapabilityResponse, error) {
	subject, err := backend.subject(ctx, serviceauth.AudienceDocumentService)
	if err != nil {
		return nil, err
	}
	// The fact source is the only capability issuer, so the fake mints a real
	// signed capability: the search service validates it offline exactly as it
	// does in production, and a caller cannot pass off its own range.
	now := time.Now().UTC()
	capability, err := backend.codec.SealCapability(serviceauth.CapabilityClaims{
		Issuer:              serviceauth.CallerDocumentService,
		Audience:            serviceauth.AudienceDocumentSearch,
		Scopes:              []string{string(serviceauth.ScopeDocumentSearcher)},
		SubjectKey:          subject,
		AuthenticatedPublic: true,
		IssuedAt:            now.Unix(),
		ExpiresAt:           now.Add(2 * time.Minute).Unix(),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &documentv1.IssueSearchCapabilityResponse{
		Decision:            documentv1.Decision_DECISION_GRANTED,
		Capability:          capability,
		AuthenticatedPublic: true,
	}, nil
}

// fakeSearchBackend implements documentsearch.v1 over the wire.
type fakeSearchBackend struct {
	documentsearchv1.UnimplementedDocumentSearchServiceServer

	mu              sync.Mutex
	codec           *serviceauth.Codec
	documents       *fakeDocumentBackend
	requests        []*documentsearchv1.SearchDocumentsRequest
	capabilityValue string
}

func newFakeSearchBackend(t *testing.T, documents *fakeDocumentBackend) *fakeSearchBackend {
	t.Helper()
	codec, err := serviceauth.NewCodec(testBoundaryKey)
	if err != nil {
		t.Fatalf("codec: %v", err)
	}
	return &fakeSearchBackend{codec: codec, documents: documents}
}

func (backend *fakeSearchBackend) capability(ctx context.Context) (string, error) {
	values := metadata.ValueFromIncomingContext(ctx, serviceauth.HeaderCapability)
	if len(values) != 1 {
		return "", status.Error(codes.Unauthenticated, "missing resource capability")
	}
	token, err := serviceauth.BearerToken(values[0])
	if err != nil {
		return "", status.Error(codes.Unauthenticated, err.Error())
	}
	seal, err := backend.codec.OpenCapability(token, serviceauth.AudienceDocumentSearch)
	if err != nil {
		return "", status.Error(codes.Unauthenticated, err.Error())
	}
	return seal.Claims.SubjectKey, nil
}

func (backend *fakeSearchBackend) SearchDocuments(ctx context.Context, request *documentsearchv1.SearchDocumentsRequest) (*documentsearchv1.SearchDocumentsResponse, error) {
	subject, err := backend.capability(ctx)
	if err != nil {
		return nil, err
	}
	backend.mu.Lock()
	backend.requests = append(backend.requests, request)
	backend.capabilityValue = subject
	backend.mu.Unlock()

	// The index projection: every document that matches the query, with the
	// fields a Web surface renders. owned_by_subject_only narrows it to the
	// capability subject, which is what keeps another subject's public document
	// out of a personal search.
	hits := make([]*documentsearchv1.SearchHit, 0, testDocumentCount)
	for _, id := range backend.documents.order {
		document := backend.documents.documents[id]
		if !strings.Contains(strings.ToLower(document.title+document.content), strings.ToLower(request.GetQuery())) {
			continue
		}
		if request.GetOwnedBySubjectOnly() && document.owner != subject {
			continue
		}
		hits = append(hits, &documentsearchv1.SearchHit{
			DocumentId:          document.id,
			VersionId:           document.activeVersionID,
			Title:               document.title,
			Snippet:             document.content,
			Source:              "document",
			OwnerSubjectKey:     document.owner,
			AuthenticatedPublic: document.public,
			CreatedAt:           document.createdAt.Format(time.RFC3339Nano),
			UpdatedAt:           document.updatedAt.Format(time.RFC3339Nano),
		})
	}
	// total counts what the caller can actually page through: every match here is
	// readable, so any page up to ceil(total/page_size) returns rows. truncated
	// follows the F02 definition — there are results after this page — which is
	// why an out-of-range page answers with no hits and truncated=false instead
	// of advertising a page that would come back empty.
	total := int32(len(hits))
	page := int(request.GetPage())
	if page <= 0 {
		page = 1
	}
	size := int(request.GetPageSize())
	if size <= 0 {
		size = 10
	}
	start := (page - 1) * size
	if start > len(hits) {
		start = len(hits)
	}
	end := start + size
	if end > len(hits) {
		end = len(hits)
	}
	return &documentsearchv1.SearchDocumentsResponse{
		Query:     request.GetQuery(),
		Hits:      hits[start:end],
		Total:     total,
		Truncated: end < len(hits),
	}, nil
}

func serveGRPC(t *testing.T, register func(*grpc.Server)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	register(server)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}

// webHarness is the whole Web surface wired the way production wires it.
type webHarness struct {
	router      *gin.Engine
	userID      int64
	documents   *fakeDocumentBackend
	search      *fakeSearchBackend
	listRequest func() *documentv1.ListDocumentsRequest
	saveRequest func() *documentv1.SaveDocumentRequest
	searchQuery func() *documentsearchv1.SearchDocumentsRequest
	capability  func() string
}

func newWebHarness(t *testing.T) *webHarness {
	t.Helper()
	documents := newFakeDocumentBackend(t)
	search := newFakeSearchBackend(t, documents)
	documentAddress := serveGRPC(t, func(server *grpc.Server) {
		documentv1.RegisterDocumentServiceServer(server, documents)
	})
	searchAddress := serveGRPC(t, func(server *grpc.Server) {
		documentsearchv1.RegisterDocumentSearchServiceServer(server, search)
	})

	documentClient, err := documentservice.New(documentservice.Config{
		Endpoint:       documentAddress,
		Audience:       string(serviceauth.AudienceDocumentService),
		Caller:         serviceauth.CallerGoWeb,
		Scopes:         []serviceauth.Scope{serviceauth.ScopeDocumentWrite, serviceauth.ScopeDocumentRead},
		RequestTimeout: 5 * time.Second,
	}, testBoundaryKey)
	if err != nil {
		t.Fatalf("document client: %v", err)
	}
	t.Cleanup(func() { _ = documentClient.Close() })
	searchClient, err := documentsearch.New(documentsearch.Config{
		Endpoint:       searchAddress,
		Audience:       string(serviceauth.AudienceDocumentSearch),
		Caller:         serviceauth.CallerGoWeb,
		RequestTimeout: 5 * time.Second,
	}, testBoundaryKey)
	if err != nil {
		t.Fatalf("search client: %v", err)
	}
	t.Cleanup(func() { _ = searchClient.Close() })

	gateway, err := New(Config{Documents: documentClient, Search: searchClient})
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	adapter, err := NewServiceAdapter(gateway)
	if err != nil {
		t.Fatalf("adapter: %v", err)
	}

	harness := &webHarness{userID: 42, documents: documents, search: search}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	protected := router.Group("/api/v1/protected")
	protected.Use(func(c *gin.Context) {
		identity.Set(c, identity.Principal{Kind: identity.KindUser, Subject: strconv.FormatInt(harness.userID, 10), UserID: harness.userID, SessionID: "session-1"})
		c.Next()
	})
	adapter.RegisterRoutes(router.Group("/api/v1/public"), protected)
	harness.router = router
	harness.listRequest = func() *documentv1.ListDocumentsRequest {
		documents.mu.Lock()
		defer documents.mu.Unlock()
		if len(documents.listRequests) == 0 {
			return nil
		}
		return documents.listRequests[len(documents.listRequests)-1]
	}
	harness.saveRequest = func() *documentv1.SaveDocumentRequest {
		documents.mu.Lock()
		defer documents.mu.Unlock()
		if len(documents.saveRequests) == 0 {
			return nil
		}
		return documents.saveRequests[len(documents.saveRequests)-1]
	}
	harness.searchQuery = func() *documentsearchv1.SearchDocumentsRequest {
		search.mu.Lock()
		defer search.mu.Unlock()
		if len(search.requests) == 0 {
			return nil
		}
		return search.requests[len(search.requests)-1]
	}
	harness.capability = func() string {
		search.mu.Lock()
		defer search.mu.Unlock()
		return search.capabilityValue
	}
	return harness
}

// listEnvelopePayload mirrors the response the front end consumes.
type listEnvelopePayload struct {
	Success bool `json:"success"`
	Data    struct {
		DocumentList []struct {
			DocumentID string `json:"documentId"`
			OwnerID    string `json:"ownerId"`
			Title      string `json:"title"`
			Summary    string `json:"summary"`
			Visibility string `json:"visibility"`
			CreatedAt  int64  `json:"createdAt"`
			UpdatedAt  int64  `json:"updatedAt"`
		} `json:"documentList"`
		Keyword string `json:"keyword"`
	} `json:"data"`
	Meta struct {
		Page       int    `json:"page"`
		PerPage    int    `json:"per_page"`
		Total      int    `json:"total"`
		TotalPages int    `json:"total_pages"`
		NextCursor string `json:"nextCursor"`
		Truncated  bool   `json:"truncated"`
	} `json:"meta"`
}

func (harness *webHarness) get(t *testing.T, path string) (*httptest.ResponseRecorder, listEnvelopePayload) {
	t.Helper()
	recorder := httptest.NewRecorder()
	harness.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	var payload listEnvelopePayload
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode %s: %v: %s", path, err, recorder.Body.String())
		}
	}
	return recorder, payload
}

func (harness *webHarness) list(t *testing.T, path string) listEnvelopePayload {
	t.Helper()
	recorder, payload := harness.get(t, path)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, recorder.Code, recorder.Body.String())
	}
	return payload
}

func (harness *webHarness) put(t *testing.T, path string, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, path, strings.NewReader(string(raw)))
	request.Header.Set("Content-Type", "application/json")
	harness.router.ServeHTTP(recorder, request)
	var payload map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
	return recorder, payload
}

func documentIDs(payload listEnvelopePayload) []string {
	ids := make([]string, 0, len(payload.Data.DocumentList))
	for _, item := range payload.Data.DocumentList {
		ids = append(ids, item.DocumentID)
	}
	return ids
}

// TestCursorPagingOverTwentyFiveDocuments is the paging acceptance: 25 documents
// at 9 per page must be 9, 9, 7, with no repeats and no gaps, and walking back
// must reproduce the same pages.
func TestCursorPagingOverTwentyFiveDocuments(t *testing.T) {
	harness := newWebHarness(t)

	first := harness.list(t, "/api/v1/protected/documents/mine?pageSize=9")
	if len(first.Data.DocumentList) != 9 {
		t.Fatalf("page 1 size = %d, want 9", len(first.Data.DocumentList))
	}
	if first.Meta.Total != 25 || first.Meta.TotalPages != 3 {
		t.Fatalf("page 1 meta = %+v, want the real total 25 over 3 pages", first.Meta)
	}
	if first.Meta.NextCursor == "" {
		t.Fatal("page 1 must hand the client a cursor for the next page")
	}
	if got := harness.listRequest().GetPageToken(); got != "" {
		t.Fatalf("first page page_token = %q, want empty", got)
	}
	if got := harness.listRequest().GetPageSize(); got != 9 {
		t.Fatalf("page size on the wire = %d, want 9", got)
	}
	if got := harness.listRequest().GetOwnerSubjectKey(); got != testOwnerSubject {
		t.Fatalf("owner subject = %q, want %q: the mine list filters by the session subject", got, testOwnerSubject)
	}

	second := harness.list(t, "/api/v1/protected/documents/mine?pageSize=9&cursor="+url.QueryEscape(first.Meta.NextCursor))
	if len(second.Data.DocumentList) != 9 {
		t.Fatalf("page 2 size = %d, want 9", len(second.Data.DocumentList))
	}
	if got := harness.listRequest().GetPageToken(); got != first.Meta.NextCursor {
		t.Fatalf("page 2 page_token = %q, want the cursor verbatim %q", got, first.Meta.NextCursor)
	}

	third := harness.list(t, "/api/v1/protected/documents/mine?pageSize=9&cursor="+url.QueryEscape(second.Meta.NextCursor))
	if len(third.Data.DocumentList) != 7 {
		t.Fatalf("page 3 size = %d, want 7", len(third.Data.DocumentList))
	}
	if third.Meta.NextCursor != "" {
		t.Fatalf("page 3 must not advertise a next cursor, got %q", third.Meta.NextCursor)
	}
	if third.Meta.Total != 25 || third.Meta.TotalPages != 3 {
		t.Fatalf("page 3 meta = %+v, want the same real total on every page", third.Meta)
	}

	seen := map[string]int{}
	for _, page := range []listEnvelopePayload{first, second, third} {
		for _, id := range documentIDs(page) {
			seen[id]++
		}
	}
	if len(seen) != 25 {
		t.Fatalf("pages covered %d distinct documents, want 25 (repeats or gaps present)", len(seen))
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("document %s appeared %d times across pages", id, count)
		}
	}

	// Back and forward again: the same cursors must reproduce the same pages, and
	// the first page must be reachable without any cursor.
	backFirst := harness.list(t, "/api/v1/protected/documents/mine?pageSize=9")
	if strings.Join(documentIDs(backFirst), ",") != strings.Join(documentIDs(first), ",") {
		t.Fatal("going back to page 1 must return the same documents")
	}
	backSecond := harness.list(t, "/api/v1/protected/documents/mine?pageSize=9&cursor="+url.QueryEscape(first.Meta.NextCursor))
	if strings.Join(documentIDs(backSecond), ",") != strings.Join(documentIDs(second), ",") {
		t.Fatal("re-using page 2's cursor must return the same documents")
	}
}

// TestListRendersPolicyVisibilityNotPublicationStatus pins the fix for the
// regression this stage is about: every fake document is published, so a surface
// that derived visibility from publication status would call them all public.
func TestListRendersPolicyVisibilityNotPublicationStatus(t *testing.T) {
	harness := newWebHarness(t)
	page := harness.list(t, "/api/v1/protected/documents/mine?pageSize=9")
	visibilityByID := map[string]string{}
	for _, item := range page.Data.DocumentList {
		visibilityByID[item.DocumentID] = item.Visibility
	}
	if visibilityByID["doc-01"] != "private" || visibilityByID["doc-02"] != "public" {
		t.Fatalf("visibility = %v, want doc-01 private and doc-02 public from the access policy", visibilityByID)
	}
}

// TestSearchTruncationAndReadablePageBoundaries is the F02 acceptance on the Web
// side: the page count comes from the service's readable total, the truncation
// flag is visible, and a page beyond the last readable one is never presented as
// a result page.
func TestSearchTruncationAndReadablePageBoundaries(t *testing.T) {
	harness := newWebHarness(t)

	first := harness.list(t, "/api/v1/protected/documents/search?keyword=orion&page=1&pageSize=9")
	if !first.Meta.Truncated {
		t.Fatalf("page 1 of 3 must report truncated=true: %+v", first.Meta)
	}
	if first.Meta.Total != 25 || first.Meta.TotalPages != 3 {
		t.Fatalf("page 1 meta = %+v, want total 25 over 3 readable pages", first.Meta)
	}
	last := harness.list(t, "/api/v1/protected/documents/search?keyword=orion&page=3&pageSize=9")
	if len(last.Data.DocumentList) != 7 {
		t.Fatalf("last page size = %d, want 7", len(last.Data.DocumentList))
	}
	if last.Meta.Truncated {
		t.Fatalf("the last readable page has nothing after it, so truncated must be false: %+v", last.Meta)
	}

	// A page past the advertised last page keeps the total and reports no
	// truncation; the client guard is "activePage < total_pages", so page 4 is
	// unreachable through the UI and never rendered as a result page.
	beyond := harness.list(t, "/api/v1/protected/documents/search?keyword=orion&page=4&pageSize=9")
	if len(beyond.Data.DocumentList) != 0 {
		t.Fatalf("page 4 returned %d hits although only 3 pages are readable", len(beyond.Data.DocumentList))
	}
	if beyond.Meta.Total != 25 || beyond.Meta.TotalPages != 3 {
		t.Fatalf("page 4 meta = %+v, want the unchanged readable total", beyond.Meta)
	}
	if beyond.Meta.Truncated {
		t.Fatalf("nothing follows the last page, so truncated must be false: %+v", beyond.Meta)
	}

	// Every page the meta advertises must actually return results: that is the
	// invariant F02 broke.
	for page := 1; page <= first.Meta.TotalPages; page++ {
		envelope := harness.list(t, fmt.Sprintf("/api/v1/protected/documents/search?keyword=orion&page=%d&pageSize=9", page))
		if len(envelope.Data.DocumentList) == 0 {
			t.Fatalf("page %d is advertised (total_pages=%d) but came back empty", page, first.Meta.TotalPages)
		}
	}
}

// TestUpdateSavesThroughSaveDocumentAndReturnsTheNewBody walks the save path the
// Web editor uses: one PUT, explicit policy, expected revision, idempotency key.
func TestUpdateSavesThroughSaveDocumentAndReturnsTheNewBody(t *testing.T) {
	harness := newWebHarness(t)
	path := "/api/v1/protected/documents/doc-01"

	recorder, payload := harness.put(t, path, map[string]any{
		"title": "编辑后的标题", "content": "编辑后的正文", "visibility": "public",
		"expectedRevision": 1, "requestId": "save-key-1",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", recorder.Code, recorder.Body.String())
	}
	data, _ := payload["data"].(map[string]any)
	if data["content"] != "编辑后的正文" || data["visibility"] != "public" {
		t.Fatalf("save response = %v, want the newly active body and policy", data)
	}
	if data["replayed"] != false {
		t.Fatalf("a fresh save must report replayed=false, got %v", data["replayed"])
	}
	if applied, _ := data["appliedVersionId"].(string); applied == "" {
		t.Fatal("a fresh save must report which version it applied")
	}
	request := harness.saveRequest()
	if request == nil {
		t.Fatal("the surface must call SaveDocument, not UpdateDraft followed by PublishDocument")
	}
	if request.AuthenticatedPublic == nil || !request.GetAuthenticatedPublic() {
		t.Fatalf("policy must be sent as an explicit presence: %+v", request)
	}
	if request.GetExpectedAggregateRevision() != 1 {
		t.Fatalf("expected revision = %d, want 1", request.GetExpectedAggregateRevision())
	}
	if request.GetRequestId() != "save-key-1" {
		t.Fatalf("request id = %q, want save-key-1", request.GetRequestId())
	}

	// The detail read immediately after the save must show the new body, and the
	// list must agree with the detail about visibility.
	_, detail := harness.get(t, path)
	if detail.Data.DocumentList != nil {
		t.Fatal("detail endpoint must not render a list")
	}
	var detailPayload struct {
		Data struct {
			Content    string `json:"content"`
			Visibility string `json:"visibility"`
			Revision   uint64 `json:"revision"`
			OwnerID    string `json:"ownerId"`
		} `json:"data"`
	}
	detailRecorder := httptest.NewRecorder()
	harness.router.ServeHTTP(detailRecorder, httptest.NewRequest(http.MethodGet, path, nil))
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("GET detail = %d: %s", detailRecorder.Code, detailRecorder.Body.String())
	}
	if err := json.Unmarshal(detailRecorder.Body.Bytes(), &detailPayload); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detailPayload.Data.Content != "编辑后的正文" || detailPayload.Data.Visibility != "public" {
		t.Fatalf("detail after save = %+v, want the new body and policy", detailPayload.Data)
	}
	if detailPayload.Data.Revision != 2 {
		t.Fatalf("detail revision = %d, want 2 after one save", detailPayload.Data.Revision)
	}
	if detailPayload.Data.OwnerID != "42" {
		t.Fatalf("detail owner = %q, want the Web owner id parsed from web:user:42", detailPayload.Data.OwnerID)
	}
	after := harness.list(t, "/api/v1/protected/documents/mine?pageSize=9")
	for _, item := range after.Data.DocumentList {
		if item.DocumentID == "doc-01" && item.Visibility != "public" {
			t.Fatalf("list visibility = %q, want public after the save: list and detail must agree", item.Visibility)
		}
	}
}

// TestUpdateReplaysSameRequestID covers idempotency: a retried save with the same
// key must not append a second version.
func TestUpdateReplaysSameRequestID(t *testing.T) {
	harness := newWebHarness(t)
	path := "/api/v1/protected/documents/doc-03"
	body := map[string]any{"title": "第一次", "content": "第一次正文", "visibility": "private", "requestId": "retry-key"}

	first, _ := harness.put(t, path, body)
	if first.Code != http.StatusOK {
		t.Fatalf("first PUT = %d: %s", first.Code, first.Body.String())
	}
	body["content"] = "第二次正文（重试不应写入）"
	second, payload := harness.put(t, path, body)
	if second.Code != http.StatusOK {
		t.Fatalf("replayed PUT = %d: %s", second.Code, second.Body.String())
	}
	data, _ := payload["data"].(map[string]any)
	if data["replayed"] != true {
		t.Fatalf("replayed = %v, want true on the second attempt with the same key", data["replayed"])
	}
	if data["content"] != "第一次正文" {
		t.Fatalf("replayed content = %v, want the first attempt's result", data["content"])
	}
	// The replay must name the version the first attempt wrote, which is what lets
	// the editor say "this request was already applied" instead of "saved again".
	firstPayload := map[string]any{}
	if err := json.Unmarshal(first.Body.Bytes(), &firstPayload); err != nil {
		t.Fatalf("decode first save response: %v", err)
	}
	firstData, _ := firstPayload["data"].(map[string]any)
	applied, _ := data["appliedVersionId"].(string)
	if applied == "" {
		t.Fatal("a replay must identify the version the first attempt produced")
	}
	if applied != firstData["appliedVersionId"] {
		t.Fatalf("replay appliedVersionId = %q, want the first attempt's %v", applied, firstData["appliedVersionId"])
	}
}

// TestUpdateByNonOwnerIsForbidden pins the 403 the acceptance asks for.
func TestUpdateByNonOwnerIsForbidden(t *testing.T) {
	harness := newWebHarness(t)
	harness.userID = 99
	recorder, _ := harness.put(t, "/api/v1/protected/documents/doc-01", map[string]any{
		"title": "冒名编辑", "content": "不该写入", "visibility": "public", "requestId": "intruder",
	})
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("PUT by a non-owner = %d, want 403: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "FORBIDDEN") {
		t.Fatalf("body = %s, want the FORBIDDEN code", recorder.Body.String())
	}
}

// TestSearchPagingUsesRealTotalAndOwnsershipFilter covers the search acceptance:
// page/page_size forwarded, the response's real total rendered, and a personal
// search that cannot surface another subject's document.
func TestSearchPagingUsesRealTotalAndOwnershipFilter(t *testing.T) {
	harness := newWebHarness(t)

	second := harness.list(t, "/api/v1/protected/documents/search?keyword=orion&page=2&pageSize=9")
	if len(second.Data.DocumentList) != 9 {
		t.Fatalf("search page 2 size = %d, want 9", len(second.Data.DocumentList))
	}
	if second.Meta.Total != 25 || second.Meta.TotalPages != 3 || second.Meta.Page != 2 {
		t.Fatalf("search meta = %+v, want the real total 25 over 3 pages on page 2", second.Meta)
	}
	query := harness.searchQuery()
	if query.GetPage() != 2 || query.GetPageSize() != 9 {
		t.Fatalf("search request page/page_size = %d/%d, want 2/9", query.GetPage(), query.GetPageSize())
	}
	if !query.GetOwnedBySubjectOnly() {
		t.Fatal("a Web search is personal: owned_by_subject_only must be sent")
	}
	if len(query.GetAllowedSpaceIds()) != 0 || len(query.GetAllowedDocumentIds()) != 0 {
		t.Fatal("an unnarrowed personal search must not name spaces or documents")
	}
	if harness.capability() != testOwnerSubject {
		t.Fatalf("capability subject = %q, want the session subject %q", harness.capability(), testOwnerSubject)
	}

	third := harness.list(t, "/api/v1/protected/documents/search?keyword=orion&page=3&pageSize=9")
	if len(third.Data.DocumentList) != 7 {
		t.Fatalf("search page 3 size = %d, want 7", len(third.Data.DocumentList))
	}

	first := harness.list(t, "/api/v1/protected/documents/search?keyword=orion&page=1&pageSize=9")
	seen := map[string]int{}
	for _, page := range []listEnvelopePayload{first, second, third} {
		for _, id := range documentIDs(page) {
			seen[id]++
			if id == "doc-other" {
				t.Fatal("a personal search must never return another subject's document")
			}
		}
	}
	if len(seen) != 25 {
		t.Fatalf("search pages covered %d documents, want 25", len(seen))
	}
}

// TestSearchHitsCarryIndexedTimesAndPolicy replaces the old behaviour of stamping
// every hit with the query time: the hit's own created_at/updated_at and
// authenticated_public must be rendered.
func TestSearchHitsCarryIndexedTimesAndPolicy(t *testing.T) {
	harness := newWebHarness(t)
	page := harness.list(t, "/api/v1/protected/documents/search?keyword=orion&page=1&pageSize=25")
	wanted := time.Unix(1_700_000_000, 0).UTC()
	byID := map[string]struct {
		visibility string
		createdAt  int64
	}{}
	for _, item := range page.Data.DocumentList {
		byID[item.DocumentID] = struct {
			visibility string
			createdAt  int64
		}{item.Visibility, item.CreatedAt}
	}
	first, ok := byID["doc-01"]
	if !ok {
		t.Fatal("doc-01 missing from the search page")
	}
	if first.visibility != "private" {
		t.Fatalf("doc-01 visibility = %q, want private from the indexed policy flag", first.visibility)
	}
	expected := wanted.Add(time.Minute).Unix()
	if first.createdAt != expected {
		t.Fatalf("doc-01 createdAt = %d, want the indexed creation time %d", first.createdAt, expected)
	}
	if first.createdAt == time.Now().Unix() {
		t.Fatal("createdAt must not be the query time")
	}
}
