package sourceowned

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gin-backend/internal/modules/document/domain"
	"gin-backend/internal/platform/httpserver/identity"

	documentv1 "packages/gen/document/v1"

	"github.com/gin-gonic/gin"
)

// gatewayStub records what the Web surface asked for and returns a canned result.
// The HTTP contract is what this package owns, so the tests assert on the request
// the surface produces and on the status it renders, not on transport details.
type gatewayStub struct {
	create   CreateCommand
	update   UpdateCommand
	trash    TrashCommand
	getID    string
	viewerID int64
	err      error
	items    []domain.DocumentSummary
	page     domain.Page
	keyword  string
	// cursor and pageSize record what the surface forwarded, so the tests can
	// assert the token is passed through verbatim instead of being recomputed.
	cursor   string
	pageSize int
	// updateResult replaces the canned save result when a test needs to exercise
	// the replay path.
	updateResult *MutationResult
}

func (stub *gatewayStub) Create(_ context.Context, command CreateCommand) (*MutationResult, error) {
	stub.create = command
	if stub.err != nil {
		return nil, stub.err
	}
	now := time.Unix(1_700_000_000, 0).UTC().Format(time.RFC3339Nano)
	return &MutationResult{Document: &documentv1.DocumentDetail{
		Summary: &documentv1.DocumentSummary{
			DocumentId: "document-id", LifecycleStatus: documentv1.LifecycleStatus_LIFECYCLE_STATUS_ACTIVE,
			PublicationStatus: documentv1.PublicationStatus_PUBLICATION_STATUS_PUBLISHED,
			CreatedAt:         now, UpdatedAt: now,
		},
		Version: &documentv1.DocumentVersion{
			Title: command.Title, Summary: "summary", Content: command.Content,
			PublicationStatus: documentv1.PublicationStatus_PUBLICATION_STATUS_PUBLISHED,
		},
		AuthenticatedPublic: command.AuthenticatedPublic,
	}}, nil
}

func (stub *gatewayStub) Update(_ context.Context, command UpdateCommand) (*MutationResult, error) {
	stub.update = command
	if stub.err != nil {
		return nil, stub.err
	}
	if stub.updateResult != nil {
		return stub.updateResult, nil
	}
	now := time.Unix(1_700_000_100, 0).UTC().Format(time.RFC3339Nano)
	return &MutationResult{Document: &documentv1.DocumentDetail{
		Summary: &documentv1.DocumentSummary{
			DocumentId: command.DocumentID, ActiveVersionId: "version-2",
			LifecycleStatus:   documentv1.LifecycleStatus_LIFECYCLE_STATUS_ACTIVE,
			PublicationStatus: documentv1.PublicationStatus_PUBLICATION_STATUS_PUBLISHED,
			CreatedAt:         now, UpdatedAt: now,
		},
		Version:             &documentv1.DocumentVersion{Title: command.Title, Summary: "summary", Content: command.Content},
		AuthenticatedPublic: command.AuthenticatedPublic,
	}, AppliedVersionID: "version-2"}, nil
}

func (stub *gatewayStub) Trash(_ context.Context, command TrashCommand) error {
	stub.trash = command
	return stub.err
}

func (stub *gatewayStub) Get(_ context.Context, viewerID int64, documentID string) (*domain.DocumentView, error) {
	stub.viewerID, stub.getID = viewerID, documentID
	if stub.err != nil {
		return nil, stub.err
	}
	return &domain.DocumentView{
		DocumentHead: domain.DocumentHead{DocumentID: documentID, AuthenticatedPublic: true},
		Title:        "标题", Summary: "摘要", Content: "正文",
	}, nil
}

func (stub *gatewayStub) ListOwned(_ context.Context, ownerID int64, pageSize int, cursor string) ([]domain.DocumentSummary, domain.Page, error) {
	stub.viewerID, stub.pageSize, stub.cursor = ownerID, pageSize, cursor
	return stub.items, stub.page, stub.err
}

func (stub *gatewayStub) ListPublic(_ context.Context, viewerID int64, pageSize int, cursor string) ([]domain.DocumentSummary, domain.Page, error) {
	stub.viewerID, stub.pageSize, stub.cursor = viewerID, pageSize, cursor
	return stub.items, stub.page, stub.err
}

func (stub *gatewayStub) Search(_ context.Context, viewerID int64, keyword string, page, pageSize int) ([]domain.DocumentSummary, domain.Page, string, error) {
	stub.viewerID, stub.keyword, stub.pageSize = viewerID, keyword, pageSize
	stub.page.Number = page
	return stub.items, stub.page, keyword, stub.err
}

func TestRegisterRoutesRegistersDocumentSurface(t *testing.T) {
	adapter, err := NewServiceAdapter(&gatewayStub{})
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	adapter.RegisterRoutes(router.Group("/api/v1/public"), router.Group("/api/v1/protected"))

	routes := make(map[string]struct{})
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	for _, expected := range []string{
		"POST /api/v1/protected/documents",
		"GET /api/v1/protected/documents/mine",
		"GET /api/v1/protected/documents/public",
		"GET /api/v1/protected/documents/search",
		"GET /api/v1/protected/documents/:documentId",
		"PUT /api/v1/protected/documents/:documentId",
		"DELETE /api/v1/protected/documents/:documentId",
	} {
		if _, ok := routes[expected]; !ok {
			t.Fatalf("route is not registered: %s", expected)
		}
	}
}

func TestCreateParsesJWTSubjectAsWebUserID(t *testing.T) {
	gateway := &gatewayStub{}
	adapter, _ := NewServiceAdapter(gateway)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/upload", func(c *gin.Context) {
		identity.Set(c, identity.Principal{Kind: identity.KindUser, Subject: "42", UserID: 42, SessionID: "session-1"})
		adapter.create(c)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(`{"title":"标题","content":"正文","visibility":"public"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated || gateway.create.OwnerID != 42 || !gateway.create.AuthenticatedPublic {
		t.Fatalf("unexpected create result: status=%d command=%#v body=%s", recorder.Code, gateway.create, recorder.Body.String())
	}
}

func TestCreateRejectsNonNumericJWTSubject(t *testing.T) {
	adapter, _ := NewServiceAdapter(&gatewayStub{})
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/upload", func(c *gin.Context) {
		// 主体无法解析为数值时, 中间件注入的 Principal.UserID 为 0
		identity.Set(c, identity.Principal{Kind: identity.KindUser, Subject: "not-a-user-id", SessionID: "session-1"})
		adapter.create(c)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(`{"title":"标题","content":"正文"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusUnauthorized, recorder.Body.String())
	}
}

func TestGatewayErrorClassesRenderStableStatusCodes(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"not found", ErrNotFound, http.StatusNotFound, "NOT_FOUND"},
		{"forbidden", ErrForbidden, http.StatusForbidden, "FORBIDDEN"},
		{"invalid", ErrInvalidInput, http.StatusBadRequest, "VALIDATION_FAILED"},
		{"conflict", ErrConflict, http.StatusConflict, "CONFLICT"},
		{"unavailable", ErrUnavailable, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE"},
		{"unknown", errors.New("boom"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			gateway := &gatewayStub{err: testCase.err}
			adapter, _ := NewServiceAdapter(gateway)
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.GET("/document", func(c *gin.Context) {
				identity.Set(c, identity.Principal{Kind: identity.KindUser, Subject: "7", UserID: 7, SessionID: "session-1"})
				adapter.get(c)
			})

			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/document", nil))
			if recorder.Code != testCase.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, testCase.status, recorder.Body.String())
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Error.Code != testCase.code {
				t.Fatalf("code = %q, want %q", body.Error.Code, testCase.code)
			}
		})
	}
}

func TestGetPassesAuthenticatedViewerToGateway(t *testing.T) {
	gateway := &gatewayStub{}
	adapter, _ := NewServiceAdapter(gateway)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/documents/:documentId", func(c *gin.Context) {
		identity.Set(c, identity.Principal{Kind: identity.KindUser, Subject: "9", UserID: 9, SessionID: "session-1"})
		adapter.get(c)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/documents/2f0c0b7e-6f4e-4a4f-9d2f-2f6b1c9a4d31", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if gateway.viewerID != 9 {
		t.Fatalf("viewer id = %d, want 9: the subject must come from the session, never the request", gateway.viewerID)
	}
}

func TestListRendersEmptyOwnerForIndexHits(t *testing.T) {
	// The formal document index does not know the Web owner id. Rendering a
	// guessed owner would attribute someone else's document, so the surface must
	// render an empty owner instead.
	gateway := &gatewayStub{
		items: []domain.DocumentSummary{{DocumentID: "doc-1", Title: "标题", UpdatedAt: time.Unix(1_700_000_000, 0)}},
		page:  domain.Page{Number: 1, Size: 10, Total: 1},
	}
	adapter, _ := NewServiceAdapter(gateway)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/documents/mine", func(c *gin.Context) {
		identity.Set(c, identity.Principal{Kind: identity.KindUser, Subject: "3", UserID: 3, SessionID: "session-1"})
		adapter.listMine(c)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/documents/mine", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data struct {
			DocumentList []documentSummaryResponse `json:"documentList"`
		} `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Data.DocumentList) != 1 || body.Data.DocumentList[0].OwnerID != "" {
		t.Fatalf("unexpected list response: %#v", body.Data.DocumentList)
	}
}

// listEnvelope mirrors the parts of the list response the front end reads.
type listEnvelope struct {
	Data struct {
		DocumentList []documentSummaryResponse `json:"documentList"`
		Keyword      string                    `json:"keyword"`
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

func TestListPassesCursorThroughVerbatimAndReturnsNextCursor(t *testing.T) {
	// The document service owns the ordering, so go-web must neither derive a
	// cursor from a page number nor invent a total. It forwards what the client
	// sent and hands back what the service issued.
	gateway := &gatewayStub{
		items: []domain.DocumentSummary{{DocumentID: "doc-2", Title: "第二页"}},
		page:  domain.Page{Size: 9, Total: 25, NextCursor: "opaque-cursor-3"},
	}
	adapter, _ := NewServiceAdapter(gateway)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/documents/mine", func(c *gin.Context) {
		identity.Set(c, identity.Principal{Kind: identity.KindUser, Subject: "5", UserID: 5, SessionID: "session-1"})
		adapter.listMine(c)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/documents/mine?pageSize=9&page=2&cursor=opaque-cursor-2", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if gateway.cursor != "opaque-cursor-2" {
		t.Fatalf("cursor forwarded = %q, want %q: the surface must pass the token through unchanged", gateway.cursor, "opaque-cursor-2")
	}
	if gateway.pageSize != 9 {
		t.Fatalf("page size forwarded = %d, want 9", gateway.pageSize)
	}
	var body listEnvelope
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Meta.NextCursor != "opaque-cursor-3" {
		t.Fatalf("meta.nextCursor = %q, want %q", body.Meta.NextCursor, "opaque-cursor-3")
	}
	if body.Meta.Total != 25 || body.Meta.TotalPages != 3 || body.Meta.PerPage != 9 {
		t.Fatalf("meta = %+v, want real total 25 with 3 pages", body.Meta)
	}
}

func TestListOmitsNextCursorOnLastPage(t *testing.T) {
	gateway := &gatewayStub{
		items: []domain.DocumentSummary{{DocumentID: "doc-25"}},
		page:  domain.Page{Size: 9, Total: 25},
	}
	adapter, _ := NewServiceAdapter(gateway)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/documents/mine", func(c *gin.Context) {
		identity.Set(c, identity.Principal{Kind: identity.KindUser, Subject: "5", UserID: 5, SessionID: "session-1"})
		adapter.listMine(c)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/documents/mine?pageSize=9&cursor=opaque-cursor-3", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "nextCursor") {
		t.Fatalf("last page must not advertise a cursor: %s", recorder.Body.String())
	}
}

func TestUpdateSendsExplicitPolicyRevisionAndRequestID(t *testing.T) {
	gateway := &gatewayStub{}
	adapter, _ := NewServiceAdapter(gateway)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/documents/:documentId", func(c *gin.Context) {
		identity.Set(c, identity.Principal{Kind: identity.KindUser, Subject: "11", UserID: 11, SessionID: "session-1"})
		adapter.update(c)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/documents/2f0c0b7e-6f4e-4a4f-9d2f-2f6b1c9a4d31", strings.NewReader(
		`{"title":"新标题","content":"新正文","visibility":"public","expectedRevision":7,"requestId":"save-key-1"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if gateway.update.DocumentID != "2f0c0b7e-6f4e-4a4f-9d2f-2f6b1c9a4d31" ||
		!gateway.update.AuthenticatedPublic || gateway.update.ExpectedAggregateRevision != 7 ||
		gateway.update.RequestID != "save-key-1" {
		t.Fatalf("unexpected update command: %#v", gateway.update)
	}
	var body struct {
		Data struct {
			Content          string `json:"content"`
			Visibility       string `json:"visibility"`
			Replayed         bool   `json:"replayed"`
			AppliedVersionID string `json:"appliedVersionId"`
		} `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.Content != "新正文" || body.Data.Visibility != "public" {
		t.Fatalf("save must return the newly activated body: %+v", body.Data)
	}
	if body.Data.Replayed || body.Data.AppliedVersionID != "version-2" {
		t.Fatalf("fresh save must report replayed=false and the applied version: %+v", body.Data)
	}
}

// TestUpdateRendersReplayAsAlreadyApplied pins the F02 save-path contract: a
// replayed save must say so and identify the version the original attempt wrote,
// so the editor cannot present it as a brand-new save.
func TestUpdateRendersReplayAsAlreadyApplied(t *testing.T) {
	gateway := &gatewayStub{updateResult: &MutationResult{
		Document: &documentv1.DocumentDetail{
			Summary: &documentv1.DocumentSummary{DocumentId: "doc-1", ActiveVersionId: "version-7"},
			Version: &documentv1.DocumentVersion{VersionId: "version-7", Content: "第一次写入的正文"},
		},
		Replayed:         true,
		AppliedVersionID: "version-7",
	}}
	adapter, _ := NewServiceAdapter(gateway)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/documents/:documentId", func(c *gin.Context) {
		identity.Set(c, identity.Principal{Kind: identity.KindUser, Subject: "11", UserID: 11, SessionID: "session-1"})
		adapter.update(c)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/documents/doc-1", strings.NewReader(
		`{"title":"t","content":"重试的正文","visibility":"private","requestId":"same-key"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data struct {
			Replayed         bool   `json:"replayed"`
			AppliedVersionID string `json:"appliedVersionId"`
			Content          string `json:"content"`
		} `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Data.Replayed {
		t.Fatal("replayed=true must reach the editor: it is what distinguishes 'already applied' from 'just saved'")
	}
	if body.Data.AppliedVersionID != "version-7" {
		t.Fatalf("appliedVersionId = %q, want the version the first attempt wrote", body.Data.AppliedVersionID)
	}
	if body.Data.Content != "第一次写入的正文" {
		t.Fatalf("replay must render the committed state, got %q", body.Data.Content)
	}
}

func TestUpdateMintsRequestIDWhenClientSendsNone(t *testing.T) {
	gateway := &gatewayStub{}
	adapter, _ := NewServiceAdapter(gateway)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/documents/:documentId", func(c *gin.Context) {
		identity.Set(c, identity.Principal{Kind: identity.KindUser, Subject: "11", UserID: 11, SessionID: "session-1"})
		adapter.update(c)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/documents/doc-1", strings.NewReader(`{"title":"t","content":"c","visibility":"private"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if gateway.update.RequestID == "" {
		t.Fatal("every save must carry an idempotency key so a retry can replay instead of appending a second version")
	}
	if gateway.update.AuthenticatedPublic {
		t.Fatalf("explicit private policy must be forwarded as-is: %#v", gateway.update)
	}
}

func TestUpdateRejectsMissingVisibility(t *testing.T) {
	// Without a visibility the surface cannot tell "keep the policy" from
	// "make it private", and the contract forbids guessing. It refuses instead.
	gateway := &gatewayStub{}
	adapter, _ := NewServiceAdapter(gateway)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/documents/:documentId", func(c *gin.Context) {
		identity.Set(c, identity.Principal{Kind: identity.KindUser, Subject: "11", UserID: 11, SessionID: "session-1"})
		adapter.update(c)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/documents/doc-1", strings.NewReader(`{"title":"t","content":"c"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	if gateway.update.DocumentID != "" {
		t.Fatalf("a rejected request must not reach the gateway: %#v", gateway.update)
	}
}

func TestSearchReportsRealTotalAndPage(t *testing.T) {
	gateway := &gatewayStub{
		items: []domain.DocumentSummary{{DocumentID: "doc-4", Title: "命中"}},
		page:  domain.Page{Size: 9, Total: 12, Number: 2},
	}
	adapter, _ := NewServiceAdapter(gateway)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/documents/search", func(c *gin.Context) {
		identity.Set(c, identity.Principal{Kind: identity.KindUser, Subject: "5", UserID: 5, SessionID: "session-1"})
		adapter.searchMine(c)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/documents/search?keyword=orion&page=2&pageSize=9", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if gateway.pageSize != 9 || gateway.keyword != "orion" {
		t.Fatalf("search forwarded page size %d keyword %q", gateway.pageSize, gateway.keyword)
	}
	var body listEnvelope
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Meta.Total != 12 || body.Meta.TotalPages != 2 || body.Meta.Page != 2 {
		t.Fatalf("meta = %+v, want the real total 12 across 2 pages on page 2", body.Meta)
	}
	if body.Data.Keyword != "orion" {
		t.Fatalf("keyword = %q, want orion", body.Data.Keyword)
	}
	if body.Meta.Truncated {
		t.Fatalf("truncated must stay false when the service did not report it: %+v", body.Meta)
	}
}

// TestSearchExposesTruncationAndNeverAdvertisesAnUnreadablePage covers the two
// F02 acceptance points on the HTTP surface: total_pages is derived from the
// service's readable total (so the client cannot offer a page that comes back
// empty), and truncated is visible rather than silently swallowed.
func TestSearchExposesTruncationAndNeverAdvertisesAnUnreadablePage(t *testing.T) {
	gateway := &gatewayStub{
		items: []domain.DocumentSummary{{DocumentID: "doc-4", Title: "命中"}},
		// 25 readable results with 9 per page: the last page is 3. A page beyond
		// it must never be advertised, and the answer is incomplete.
		page: domain.Page{Size: 9, Total: 25, Number: 2, Truncated: true},
	}
	adapter, _ := NewServiceAdapter(gateway)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/documents/search", func(c *gin.Context) {
		identity.Set(c, identity.Principal{Kind: identity.KindUser, Subject: "5", UserID: 5, SessionID: "session-1"})
		adapter.searchMine(c)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/documents/search?keyword=orion&page=2&pageSize=9", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var body listEnvelope
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Meta.Truncated {
		t.Fatal("meta.truncated must be rendered so the front end can warn instead of hiding the gap")
	}
	if body.Meta.Total != 25 || body.Meta.TotalPages != 3 {
		t.Fatalf("meta = %+v, want total 25 over the 3 readable pages", body.Meta)
	}
}

// TestSearchOutOfRangePageKeepsTotalAndDropsTruncation: the search service answers
// an out-of-range page with no hits but the same total and truncated=false (there
// is nothing after it). The envelope must not turn that into a new readable page.
func TestSearchOutOfRangePageKeepsTotalAndDropsTruncation(t *testing.T) {
	gateway := &gatewayStub{
		items: nil,
		page:  domain.Page{Size: 9, Total: 25, Number: 4},
	}
	adapter, _ := NewServiceAdapter(gateway)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/documents/search", func(c *gin.Context) {
		identity.Set(c, identity.Principal{Kind: identity.KindUser, Subject: "5", UserID: 5, SessionID: "session-1"})
		adapter.searchMine(c)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/documents/search?keyword=orion&page=4&pageSize=9", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var body listEnvelope
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Data.DocumentList) != 0 {
		t.Fatalf("out-of-range page must render no hits, got %d", len(body.Data.DocumentList))
	}
	if body.Meta.Total != 25 || body.Meta.TotalPages != 3 {
		t.Fatalf("meta = %+v, want the unchanged real total and page count", body.Meta)
	}
	if body.Meta.Truncated {
		t.Fatal("nothing follows the last page, so truncated must be false")
	}
}
