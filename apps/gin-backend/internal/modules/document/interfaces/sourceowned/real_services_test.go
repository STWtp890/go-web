package sourceowned

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"packages/serviceauth"

	"gin-backend/internal/modules/document/domain"
	"gin-backend/internal/modules/document/infrastructure/documentsearch"
	"gin-backend/internal/modules/document/infrastructure/documentservice"
	"gin-backend/internal/platform/httpserver/identity"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// These tests exercise the real chain: the real gRPC clients against the running
// document-service and document-search processes, over the real boundary
// credential. They are opt-in because they need those processes and a live
// development database:
//
//	GOWEB_REAL_SERVICES=1 go test ./internal/modules/document/interfaces/sourceowned/ -run TestRealChain -v
//
// The default `go test ./...` run stays hermetic and covers the same behaviour
// with in-process fakes (service_chain_test.go).

const (
	realServicesEnv       = "GOWEB_REAL_SERVICES"
	realDocumentAddrEnv   = "GOWEB_DOCUMENT_SERVICE_ADDR"
	realSearchAddrEnv     = "GOWEB_DOCUMENT_SEARCH_ADDR"
	realBoundaryKeyEnvVar = "GOWEB_BOUNDARY_KEY_FILE"
	realPageSize          = 9
	realDocumentCount     = 25
)

func requireRealServices(t *testing.T) {
	t.Helper()
	if strings.TrimSpace(os.Getenv(realServicesEnv)) != "1" {
		t.Skipf("set %s=1 with document-service and document-search running to exercise the real chain", realServicesEnv)
	}
}

func realEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

// realBoundaryKey reads the shared development boundary key by walking up from
// the test's working directory, so the test does not hard-code a repo depth.
func realBoundaryKey(t *testing.T) []byte {
	t.Helper()
	if path := strings.TrimSpace(os.Getenv(realBoundaryKeyEnvVar)); path != "" {
		key, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return key
	}
	directory, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for depth := 0; depth < 12; depth++ {
		candidate := filepath.Join(directory, "deployments", "secrets", "mixin_search_capability.key")
		if key, readErr := os.ReadFile(candidate); readErr == nil {
			return key
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	t.Fatalf("boundary key not found; set %s", realBoundaryKeyEnvVar)
	return nil
}

// realChain wires the real clients exactly as the composition root does.
type realChain struct {
	t         *testing.T
	gateway   *Gateway
	documents *documentservice.Client
	router    *gin.Engine
	userID    int64
	ownerID   int64
	otherID   int64
	ownerKey  string
	otherKey  string
	keyword   string
	created   []string
}

func newRealChain(t *testing.T) *realChain {
	t.Helper()
	requireRealServices(t)
	key := realBoundaryKey(t)

	documentClient, err := documentservice.New(documentservice.Config{
		Endpoint:       realEnv(realDocumentAddrEnv, "127.0.0.1:18081"),
		Audience:       string(serviceauth.AudienceDocumentService),
		Caller:         serviceauth.CallerGoWeb,
		Scopes:         []serviceauth.Scope{serviceauth.ScopeDocumentWrite, serviceauth.ScopeDocumentRead},
		RequestTimeout: 15 * time.Second,
	}, key)
	if err != nil {
		t.Fatalf("document client: %v", err)
	}
	t.Cleanup(func() { _ = documentClient.Close() })
	searchClient, err := documentsearch.New(documentsearch.Config{
		Endpoint:       realEnv(realSearchAddrEnv, "127.0.0.1:18082"),
		Audience:       string(serviceauth.AudienceDocumentSearch),
		Caller:         serviceauth.CallerGoWeb,
		RequestTimeout: 15 * time.Second,
	}, key)
	if err != nil {
		t.Fatalf("search client: %v", err)
	}
	t.Cleanup(func() { _ = searchClient.Close() })

	gateway, err := New(Config{Documents: documentClient, Search: searchClient})
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}

	// A unique subject and keyword per run keeps the assertions independent of
	// whatever else the development database contains.
	suffix := uuid.NewString()[:8]
	ownerID := int64(9_100_000) + int64(time.Now().UnixNano()%100_000)
	otherID := ownerID + 1
	ownerKey, err := documentservice.WebSubjectKey(ownerID)
	if err != nil {
		t.Fatalf("owner subject: %v", err)
	}
	otherKey, err := documentservice.WebSubjectKey(otherID)
	if err != nil {
		t.Fatalf("other subject: %v", err)
	}
	chain := &realChain{
		t: t, gateway: gateway, documents: documentClient,
		ownerID: ownerID, otherID: otherID, ownerKey: ownerKey, otherKey: otherKey,
		keyword: "zzchain" + suffix,
	}
	chain.userID = ownerID
	chain.router = buildRealRouter(chain, gateway)
	t.Cleanup(chain.cleanup)
	return chain
}

// buildRealRouter mounts the real HTTP surface with the real gateway behind it,
// so a test can drive the same endpoints the browser calls.
func buildRealRouter(chain *realChain, gateway *Gateway) *gin.Engine {
	adapter, err := NewServiceAdapter(gateway)
	if err != nil {
		chain.t.Fatalf("adapter: %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	protected := router.Group("/api/v1/protected")
	protected.Use(func(c *gin.Context) {
		identity.Set(c, identity.Principal{
			Kind: identity.KindUser, Subject: strconv.FormatInt(chain.userID, 10),
			UserID: chain.userID, SessionID: "real-chain",
		})
		c.Next()
	})
	adapter.RegisterRoutes(router.Group("/api/v1/public"), protected)
	return router
}

// httpGet performs one request against the real surface and decodes the list
// envelope the front end consumes.
func (chain *realChain) httpGet(t *testing.T, path string) (int, listEnvelopePayload) {
	t.Helper()
	recorder := httptest.NewRecorder()
	chain.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	var payload listEnvelopePayload
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode %s: %v: %s", path, err, recorder.Body.String())
		}
	}
	return recorder.Code, payload
}

// TestRealChainHTTPCursorPaging runs the paging acceptance through the real HTTP
// handlers, the real gateway, the real gRPC clients and the real services: this
// is the exact path the browser takes.
func TestRealChainHTTPCursorPaging(t *testing.T) {
	chain := newRealChain(t)
	for index := 1; index <= realDocumentCount; index++ {
		chain.create(chain.ownerID, index, index%2 == 0)
	}

	status, first := chain.httpGet(t, "/api/v1/protected/documents/mine?pageSize=9")
	if status != http.StatusOK {
		t.Fatalf("GET mine = %d", status)
	}
	if len(first.Data.DocumentList) != 9 || first.Meta.Total != 25 || first.Meta.TotalPages != 3 {
		t.Fatalf("page 1 = %d items, total %d, pages %d; want 9/25/3", len(first.Data.DocumentList), first.Meta.Total, first.Meta.TotalPages)
	}
	if first.Meta.NextCursor == "" {
		t.Fatal("page 1 handed back no cursor")
	}
	status, second := chain.httpGet(t, "/api/v1/protected/documents/mine?pageSize=9&cursor="+url.QueryEscape(first.Meta.NextCursor))
	if status != http.StatusOK || len(second.Data.DocumentList) != 9 {
		t.Fatalf("page 2 = %d items (status %d), want 9", len(second.Data.DocumentList), status)
	}
	status, third := chain.httpGet(t, "/api/v1/protected/documents/mine?pageSize=9&cursor="+url.QueryEscape(second.Meta.NextCursor))
	if status != http.StatusOK || len(third.Data.DocumentList) != 7 {
		t.Fatalf("page 3 = %d items (status %d), want 7", len(third.Data.DocumentList), status)
	}
	if third.Meta.NextCursor != "" {
		t.Fatalf("page 3 advertised a cursor %q", third.Meta.NextCursor)
	}
	if third.Meta.Total != 25 || third.Meta.TotalPages != 3 {
		t.Fatalf("page 3 meta = %+v, want the same real total", third.Meta)
	}
	t.Logf("HTTP mine pages: 9/%d, 9/%d, 7/%d, total=%d", first.Meta.Total, second.Meta.Total, third.Meta.Total, first.Meta.Total)

	seen := map[string]int{}
	for _, page := range []listEnvelopePayload{first, second, third} {
		for _, id := range documentIDs(page) {
			seen[id]++
		}
	}
	if len(seen) != 25 {
		t.Fatalf("HTTP pages covered %d distinct documents, want 25", len(seen))
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("document %s appeared %d times over HTTP", id, count)
		}
	}

	// Visibility must be the policy flag on every page, not the publication
	// status: every document here is published.
	publicCount := 0
	for _, item := range first.Data.DocumentList {
		if item.Visibility == "public" {
			publicCount++
		}
	}
	if publicCount == len(first.Data.DocumentList) {
		t.Fatal("every summary rendered public although half the documents are private")
	}
}

// cleanup trashes everything the run created so the development dataset is left
// in the state it was found in (soft delete: no row is destroyed).
func (chain *realChain) cleanup() {
	for _, id := range chain.created {
		_ = chain.documents.TrashDocument(context.Background(), chain.ownerKey, id, "")
		_ = chain.documents.TrashDocument(context.Background(), chain.otherKey, id, "")
	}
}

func (chain *realChain) create(ownerID int64, index int, public bool) string {
	chain.t.Helper()
	title := fmt.Sprintf("%s 文稿 %02d", chain.keyword, index)
	content := fmt.Sprintf("# %s\n第 %02d 篇真实链路正文 %s", chain.keyword, index, chain.keyword)
	result, err := chain.gateway.Create(context.Background(), CreateCommand{
		OwnerID: ownerID, Title: title, Content: content, AuthenticatedPublic: public,
		RequestID: uuid.NewString(),
	})
	if err != nil {
		chain.t.Fatalf("create document %d: %v", index, err)
	}
	documentID := result.Document.GetSummary().GetDocumentId()
	if documentID == "" {
		chain.t.Fatalf("create document %d returned no id", index)
	}
	chain.created = append(chain.created, documentID)
	return documentID
}

// TestRealChainCursorPagingTwentyFiveDocuments is the paging acceptance against
// the running services: 25 documents at 9 per page must be 9, 9, 7.
func TestRealChainCursorPagingTwentyFiveDocuments(t *testing.T) {
	chain := newRealChain(t)
	for index := 1; index <= realDocumentCount; index++ {
		chain.create(chain.ownerID, index, index%2 == 0)
	}

	first, firstPage, err := chain.gateway.ListOwned(context.Background(), chain.ownerID, realPageSize, "")
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	if len(first) != realPageSize {
		t.Fatalf("page 1 size = %d, want %d", len(first), realPageSize)
	}
	if firstPage.Total != realDocumentCount {
		t.Fatalf("page 1 total = %d, want %d (real total_count)", firstPage.Total, realDocumentCount)
	}
	if firstPage.NextCursor == "" {
		t.Fatal("page 1 returned no cursor although 25 documents were created")
	}
	t.Logf("page 1: size=%d total=%d nextCursor=%q", len(first), firstPage.Total, firstPage.NextCursor)

	second, secondPage, err := chain.gateway.ListOwned(context.Background(), chain.ownerID, realPageSize, firstPage.NextCursor)
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if len(second) != realPageSize {
		t.Fatalf("page 2 size = %d, want %d", len(second), realPageSize)
	}
	t.Logf("page 2: size=%d total=%d nextCursor=%q", len(second), secondPage.Total, secondPage.NextCursor)

	third, thirdPage, err := chain.gateway.ListOwned(context.Background(), chain.ownerID, realPageSize, secondPage.NextCursor)
	if err != nil {
		t.Fatalf("list page 3: %v", err)
	}
	if len(third) != 7 {
		t.Fatalf("page 3 size = %d, want 7", len(third))
	}
	if thirdPage.NextCursor != "" {
		t.Fatalf("page 3 returned a cursor %q although the set is exhausted", thirdPage.NextCursor)
	}
	t.Logf("page 3: size=%d total=%d nextCursor=%q", len(third), thirdPage.Total, thirdPage.NextCursor)

	seen := map[string]int{}
	for _, page := range [][]string{ids(first), ids(second), ids(third)} {
		for _, id := range page {
			seen[id]++
		}
	}
	if len(seen) != realDocumentCount {
		t.Fatalf("pages covered %d documents, want %d", len(seen), realDocumentCount)
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("document %s appeared %d times", id, count)
		}
	}

	// Back to page 1 and forward to page 2 again must reproduce the same pages.
	backFirst, backPage, err := chain.gateway.ListOwned(context.Background(), chain.ownerID, realPageSize, "")
	if err != nil {
		t.Fatalf("list back to page 1: %v", err)
	}
	if strings.Join(ids(backFirst), ",") != strings.Join(ids(first), ",") || backPage.Total != firstPage.Total {
		t.Fatal("returning to page 1 produced a different page")
	}
	backSecond, _, err := chain.gateway.ListOwned(context.Background(), chain.ownerID, realPageSize, firstPage.NextCursor)
	if err != nil {
		t.Fatalf("list back to page 2: %v", err)
	}
	if strings.Join(ids(backSecond), ",") != strings.Join(ids(second), ",") {
		t.Fatal("re-using page 2's cursor produced a different page")
	}
}

// TestRealChainSaveAppliesPolicyAndRejectsNonOwner drives the real SaveDocument
// through the Web gateway: the body and the access policy change in one call,
// the detail read straight after sees the new body, and a non-owner is refused.
func TestRealChainSaveAppliesPolicyAndRejectsNonOwner(t *testing.T) {
	chain := newRealChain(t)
	documentID := chain.create(chain.ownerID, 1, false)

	before, err := chain.gateway.Get(context.Background(), chain.ownerID, documentID)
	if err != nil {
		t.Fatalf("get before save: %v", err)
	}
	if before.AuthenticatedPublic {
		t.Fatal("document was created private but reads back public")
	}

	updated := fmt.Sprintf("保存后的正文 %s", chain.keyword)
	saveRequestID := uuid.NewString()
	result, err := chain.gateway.Update(context.Background(), UpdateCommand{
		OwnerID: chain.ownerID, DocumentID: documentID,
		Title: chain.keyword + " 已保存", Content: updated,
		AuthenticatedPublic:       true,
		ExpectedAggregateRevision: uint64(before.ActivationRevision),
		RequestID:                 saveRequestID,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := result.Document.GetVersion().GetContent(); got != updated {
		t.Fatalf("save returned content %q, want the new body", got)
	}
	if !result.Document.GetAuthenticatedPublic() {
		t.Fatal("save returned the document as private although the policy change asked for public")
	}
	if result.Replayed {
		t.Fatal("the first save must not be reported as a replay")
	}
	activeVersion := result.Document.GetSummary().GetActiveVersionId()
	if result.AppliedVersionID == "" || result.AppliedVersionID != activeVersion {
		t.Fatalf("appliedVersionId = %q, want the newly active version %q", result.AppliedVersionID, activeVersion)
	}

	// Retrying the same request id must replay: nothing is written, the applied
	// version is the one the first attempt produced, and the caller can tell.
	replay, err := chain.gateway.Update(context.Background(), UpdateCommand{
		OwnerID: chain.ownerID, DocumentID: documentID,
		Title: chain.keyword + " 重试标题", Content: "重试不应写入的正文",
		AuthenticatedPublic: true, RequestID: saveRequestID,
	})
	if err != nil {
		t.Fatalf("replay save: %v", err)
	}
	if !replay.Replayed {
		t.Fatal("a retried request id must be reported as a replay")
	}
	if replay.AppliedVersionID != result.AppliedVersionID {
		t.Fatalf("replay appliedVersionId = %q, want the first attempt's %q", replay.AppliedVersionID, result.AppliedVersionID)
	}

	after, err := chain.gateway.Get(context.Background(), chain.ownerID, documentID)
	if err != nil {
		t.Fatalf("get after save: %v", err)
	}
	if after.Content != updated {
		t.Fatalf("detail content = %q, want the saved body %q", after.Content, updated)
	}
	if !after.AuthenticatedPublic {
		t.Fatal("detail still reports private after the policy change: list and detail would disagree")
	}

	public, _, err := chain.gateway.ListPublic(context.Background(), chain.ownerID, realPageSize, "")
	if err != nil {
		t.Fatalf("list public: %v", err)
	}
	found := false
	for _, item := range public {
		if item.DocumentID == documentID {
			found = true
			if !item.AuthenticatedPublic {
				t.Fatal("the list summary of a public document renders private")
			}
		}
	}
	if !found {
		t.Fatal("the saved document is missing from the public list although its policy says public")
	}

	// A different subject may read a public document — but only once it is a
	// registered subject, and registration happens on its first command, never on
	// a read. Creating one document under that subject is what a real Web user
	// would have done by logging in and saving something.
	chain.create(chain.otherID, 50, false)
	if _, err := chain.gateway.Get(context.Background(), chain.otherID, documentID); err != nil {
		t.Fatalf("another registered subject cannot read a public document: %v", err)
	}
	_, err = chain.gateway.Update(context.Background(), UpdateCommand{
		OwnerID: chain.otherID, DocumentID: documentID,
		Title: "冒名", Content: "不该写入", AuthenticatedPublic: true, RequestID: uuid.NewString(),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner save error = %v, want ErrForbidden", err)
	}
	if class := ClassifyError(err); class != StatusForbidden {
		t.Fatalf("non-owner save class = %v, want StatusForbidden (HTTP 403)", class)
	}
}

// TestRealChainSearchPagingAndOwnershipFilter covers the search acceptance
// against the running services: page/page_size are honoured, the total is the
// real match count, and a personal search never returns another subject's public
// document.
func TestRealChainSearchPagingAndOwnershipFilter(t *testing.T) {
	chain := newRealChain(t)
	visibility := map[string]bool{}
	for index := 1; index <= realDocumentCount; index++ {
		documentID := chain.create(chain.ownerID, index, index%2 == 0)
		visibility[documentID] = index%2 == 0
	}
	// Another subject's authenticated-public document carries the same keyword.
	// A personal search must not count or return it.
	otherDocumentID := chain.create(chain.otherID, 99, true)

	first, firstPage := chain.waitForSearch(t, 1)
	if firstPage.Total != realDocumentCount {
		t.Fatalf("search total = %d, want %d: another subject's public document must not be counted", firstPage.Total, realDocumentCount)
	}
	if len(first) != realPageSize {
		t.Fatalf("search page 1 size = %d, want %d", len(first), realPageSize)
	}
	if !firstPage.Truncated {
		t.Fatal("page 1 of 3 must report truncated=true: results exist beyond this page")
	}
	t.Logf("search page 1: size=%d total=%d truncated=%t", len(first), firstPage.Total, firstPage.Truncated)

	second, secondPage := chain.waitForSearch(t, 2)
	if len(second) != realPageSize || secondPage.Total != realDocumentCount {
		t.Fatalf("search page 2 = %d hits, total %d, want %d hits and total %d", len(second), secondPage.Total, realPageSize, realDocumentCount)
	}
	third, thirdPage := chain.waitForSearch(t, 3)
	if len(third) != 7 || thirdPage.Total != realDocumentCount {
		t.Fatalf("search page 3 = %d hits, total %d, want 7 hits and total %d", len(third), thirdPage.Total, realDocumentCount)
	}
	if thirdPage.Truncated {
		t.Fatal("the last readable page has nothing after it, so truncated must be false")
	}
	t.Logf("search pages: %d/%d/%d hits with real total %d", len(first), len(second), len(third), firstPage.Total)

	// F02: every page the total advertises must return results, and a page beyond
	// it must not look like a result page.
	lastPage := int((firstPage.Total + realPageSize - 1) / realPageSize)
	for page := 1; page <= lastPage; page++ {
		items, pageInfo, _, err := chain.gateway.Search(context.Background(), chain.ownerID, chain.keyword, page, realPageSize)
		if err != nil {
			t.Fatalf("search page %d: %v", page, err)
		}
		if len(items) == 0 {
			t.Fatalf("page %d is inside ceil(total/page_size)=%d but came back empty (total=%d)", page, lastPage, pageInfo.Total)
		}
		if pageInfo.Total != firstPage.Total {
			t.Fatalf("page %d total = %d, want the same readable total %d", page, pageInfo.Total, firstPage.Total)
		}
	}
	beyond, beyondPage, _, err := chain.gateway.Search(context.Background(), chain.ownerID, chain.keyword, lastPage+1, realPageSize)
	if err != nil {
		t.Fatalf("search beyond the last page: %v", err)
	}
	if len(beyond) != 0 {
		t.Fatalf("page %d returned %d hits although only %d pages are readable", lastPage+1, len(beyond), lastPage)
	}
	if beyondPage.Truncated {
		t.Fatal("nothing follows the last page, so truncated must be false there too")
	}
	if beyondPage.Total != firstPage.Total {
		t.Fatalf("out-of-range page total = %d, want it unchanged at %d", beyondPage.Total, firstPage.Total)
	}

	seen := map[string]int{}
	for _, page := range [][]domain.DocumentSummary{first, second, third} {
		for _, hit := range page {
			if hit.DocumentID == otherDocumentID {
				t.Fatal("a personal search returned another subject's public document")
			}
			seen[hit.DocumentID]++
			if hit.CreatedAt.IsZero() || hit.UpdatedAt.IsZero() {
				t.Fatalf("hit %s has no indexed timestamps: %+v", hit.DocumentID, hit)
			}
			if want, known := visibility[hit.DocumentID]; known && hit.AuthenticatedPublic != want {
				t.Fatalf("hit %s visibility = %v, want %v from the index policy flag", hit.DocumentID, hit.AuthenticatedPublic, want)
			}
		}
	}
	if len(seen) != realDocumentCount {
		t.Fatalf("search pages covered %d documents, want %d", len(seen), realDocumentCount)
	}
}

// waitForSearch pages until the index has caught up with the just-written
// documents, because the search service consumes the outbox asynchronously.
func (chain *realChain) waitForSearch(t *testing.T, page int) ([]domain.DocumentSummary, domain.Page) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		items, pageInfo, _, err := chain.gateway.Search(context.Background(), chain.ownerID, chain.keyword, page, realPageSize)
		if err == nil && pageInfo.Total == realDocumentCount {
			return items, pageInfo
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("search page %d never reached the indexed set: %v", page, lastErr)
	}
	t.Fatalf("search page %d never reached a total of %d documents", page, realDocumentCount)
	return nil, domain.Page{}
}

func ids(items []domain.DocumentSummary) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.DocumentID)
	}
	return out
}
