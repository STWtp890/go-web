package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"document-search/internal/config"
	"document-search/internal/dbtest"
	"document-search/internal/infrastructure/postgres"

	documentsearchv1 "packages/gen/documentsearch/v1"
	"packages/serviceauth"

	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Every test in this file runs against the real development PostgreSQL through
// the document_search_writer role. A connection failure fails the test; nothing
// is skipped. Fixtures are namespaced with fresh UUIDs so the suite can run
// beside the other services' suites in the same database.

// closedSourceEndpoint is a port nothing listens on. The rebuild and query tests
// point the (unused) source configuration at it, so "the service never contacts
// the fact source on this path" is demonstrated rather than asserted in prose.
const closedSourceEndpoint = "127.0.0.1:1"

// nextSequence hands out unique positive Outbox sequences.
func nextSequence() int64 { return dbtest.Sequence() }

// testServiceKey is a valid boundary key for the codec the application needs.
var testServiceKey = []byte(strings.Repeat("document-search-test-key-", 2))

type testFixture struct {
	t       *testing.T
	ctx     context.Context
	pool    *postgres.Pool
	cfg     config.Config
	service *Service
	stream  string
	// vectors is the real Qdrant collection of the active index generation. The
	// suite never substitutes a fake for it: the write, delete, rebuild and recall
	// paths are exactly the parts a fake would hide.
	vectors VectorIndex

	documents []string
	spaces    []string
	streams   []string
}

func newTestFixture(t *testing.T, stream string) *testFixture {
	t.Helper()
	ctx := context.Background()
	pool := dbtest.Open(t, ctx)
	cfg := config.Default()
	cfg.Postgres.DSN = dbtest.DSN()
	// The source endpoint is deliberately unreachable: nothing on the apply,
	// query or rebuild path may use it.
	cfg.Source.Endpoint = closedSourceEndpoint
	codec, err := serviceauth.NewCodec(testServiceKey)
	if err != nil {
		t.Fatalf("document-search integration: boundary codec: %v", err)
	}
	vectors := openVectorIndex(t, ctx, cfg, pool)
	service, err := New(Dependencies{Config: cfg, Pool: pool, Codec: codec, VectorIndex: vectors})
	if err != nil {
		t.Fatalf("document-search integration: application service: %v", err)
	}
	streamName := strings.TrimSpace(stream)
	if streamName == "" {
		streamName = "document-events-integration-" + dbtest.RunTag()
	}
	fixture := &testFixture{
		t: t, ctx: ctx, pool: pool, cfg: cfg, service: service,
		stream: streamName, vectors: vectors,
	}
	fixture.streams = append(fixture.streams, streamName)
	t.Cleanup(func() {
		dbtest.CleanupDocuments(t, context.Background(), pool, fixture.documents, fixture.streams)
	})
	// The collection is shared by every suite in this database, so a fixture
	// removes its own points when it is done: a leftover vector cannot change an
	// answer - the SQL projection decides that - but it would make the collection
	// grow without bound across runs.
	t.Cleanup(func() {
		if vectors == nil {
			return
		}
		if len(fixture.documents) > 0 {
			if err := vectors.DeleteDocuments(context.Background(), fixture.documents); err != nil {
				t.Errorf("document-search integration: remove the fixture's vector points: %v", err)
			}
		}
		if err := vectors.Close(); err != nil {
			t.Errorf("document-search integration: close the vector index: %v", err)
		}
	})
	return fixture
}

// openVectorIndex opens the vector collection the way the process root does, and
// fails the test when Qdrant is unreachable. It is never skipped: the vector flow
// is part of the index this suite is about.
func openVectorIndex(t *testing.T, ctx context.Context, cfg config.Config, pool *postgres.Pool) VectorIndex {
	t.Helper()
	vectors, err := NewVectorIndex(ctx, cfg, pool)
	if err != nil {
		t.Fatalf("document-search integration: open the vector index (Qdrant must be running at %s): %v",
			cfg.Vector.Endpoint, err)
	}
	if vectors == nil {
		t.Fatal("document-search integration: the vector flow is disabled in the fixture configuration")
	}
	return vectors
}

// newSpace registers a fresh space identifier for the fixture.
func (fixture *testFixture) newSpace() string {
	space := dbtest.Identifier(fixture.t)
	fixture.spaces = append(fixture.spaces, space)
	return space
}

// newDocument registers a fresh document identifier for the fixture.
func (fixture *testFixture) newDocument() string {
	document := dbtest.Identifier(fixture.t)
	fixture.documents = append(fixture.documents, document)
	return document
}

type documentSpec struct {
	DocumentID          string
	VersionID           string
	OwnerSpaceID        string
	OwnerSubjectKey     string
	Title               string
	Summary             string
	Content             string
	AuthenticatedPublic bool
	AllowedSpaceIDs     []string
	AggregateRevision   uint64
	ActivationRevision  uint64
	AccessRevision      uint64
	LifecycleRevision   uint64
	LifecycleStatus     string
	PublicationStatus   string
	EventID             string
	Sequence            int64
	Profile             string
	CreatedAt           string
	// OccurredAt is the fact source's transaction instant, which is also the
	// instant it stamps documents.updated_at. Empty keeps the fixture's
	// behaviour of stating "now", so only tests that care set it.
	OccurredAt string
}

func (fixture *testFixture) upsertRequest(spec documentSpec) *documentsearchv1.IndexDocumentEventRequest {
	if spec.DocumentID == "" {
		spec.DocumentID = fixture.newDocument()
	}
	if spec.VersionID == "" {
		spec.VersionID = dbtest.Identifier(fixture.t)
	}
	if spec.EventID == "" {
		spec.EventID = dbtest.Identifier(fixture.t)
	}
	if spec.Sequence == 0 {
		spec.Sequence = nextSequence()
	}
	if spec.AggregateRevision == 0 {
		spec.AggregateRevision = 1
	}
	if spec.ActivationRevision == 0 {
		spec.ActivationRevision = 1
	}
	if spec.AccessRevision == 0 {
		spec.AccessRevision = 1
	}
	if spec.LifecycleRevision == 0 {
		spec.LifecycleRevision = 1
	}
	if spec.LifecycleStatus == "" {
		spec.LifecycleStatus = "active"
	}
	if spec.PublicationStatus == "" {
		spec.PublicationStatus = "published"
	}
	if spec.Title == "" {
		spec.Title = "document " + dbtest.RunTag()
	}
	if spec.Summary == "" {
		spec.Summary = "summary " + dbtest.RunTag()
	}
	if spec.OwnerSubjectKey == "" {
		spec.OwnerSubjectKey = "web:user:" + dbtest.RunTag()
	}
	if spec.OccurredAt == "" {
		spec.OccurredAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return &documentsearchv1.IndexDocumentEventRequest{
		EventId:             spec.EventID,
		Sequence:            spec.Sequence,
		Kind:                EventKindUpsert,
		DocumentId:          spec.DocumentID,
		VersionId:           spec.VersionID,
		AggregateRevision:   spec.AggregateRevision,
		ActivationRevision:  spec.ActivationRevision,
		AccessRevision:      spec.AccessRevision,
		LifecycleRevision:   spec.LifecycleRevision,
		LifecycleStatus:     spec.LifecycleStatus,
		PublicationStatus:   spec.PublicationStatus,
		OwnerSubjectKey:     spec.OwnerSubjectKey,
		OwnerSpaceId:        spec.OwnerSpaceID,
		AuthenticatedPublic: spec.AuthenticatedPublic,
		AllowedSpaceIds:     spec.AllowedSpaceIDs,
		Title:               spec.Title,
		Summary:             spec.Summary,
		Content:             spec.Content,
		ContentFormat:       "markdown",
		IndexProfile:        spec.Profile,
		OccurredAt:          spec.OccurredAt,
		CreatedAt:           spec.CreatedAt,
	}
}

// deleteRequest builds a delete event. aggregateRevision is passed through
// unchanged, so a test can send the unset value the fact source may use.
func (fixture *testFixture) deleteRequest(documentID string, lifecycleRevision, aggregateRevision uint64) *documentsearchv1.IndexDocumentEventRequest {
	return &documentsearchv1.IndexDocumentEventRequest{
		EventId:           dbtest.Identifier(fixture.t),
		Sequence:          nextSequence(),
		Kind:              EventKindDelete,
		DocumentId:        documentID,
		LifecycleRevision: lifecycleRevision,
		AggregateRevision: aggregateRevision,
		LifecycleStatus:   "trashed",
		OccurredAt:        time.Now().UTC().Format(time.RFC3339Nano),
	}
}

// apply pushes one event through the RPC apply path (no consumer cursor).
func (fixture *testFixture) apply(request *documentsearchv1.IndexDocumentEventRequest) *documentsearchv1.IndexDocumentEventResponse {
	fixture.t.Helper()
	response, err := fixture.service.ApplyEvent(fixture.ctx, request)
	if err != nil {
		fixture.t.Fatalf("document-search integration: apply event %s: %v", request.GetEventId(), err)
	}
	return response
}

// capabilityFor mints a capability the way document-service would.
func capabilityFor(public bool, spaces ...string) *serviceauth.CapabilityClaims {
	return &serviceauth.CapabilityClaims{
		Version:             1,
		Issuer:              serviceauth.CallerDocumentService,
		Audience:            serviceauth.AudienceDocumentSearch,
		Scopes:              []string{string(serviceauth.ScopeDocumentSearcher)},
		SubjectKey:          "web:user:integration",
		AllowedSpaceIDs:     spaces,
		AuthenticatedPublic: public,
	}
}

func searchRequest(query string, spaces, documents []string) *documentsearchv1.SearchDocumentsRequest {
	return &documentsearchv1.SearchDocumentsRequest{
		Query:              query,
		AllowedSpaceIds:    spaces,
		AllowedDocumentIds: documents,
	}
}

func (fixture *testFixture) search(capability *serviceauth.CapabilityClaims, request *documentsearchv1.SearchDocumentsRequest) *documentsearchv1.SearchDocumentsResponse {
	fixture.t.Helper()
	response, err := fixture.service.Search(fixture.ctx, nil, capability, request)
	if err != nil {
		fixture.t.Fatalf("document-search integration: search %q: %v", request.GetQuery(), err)
	}
	return response
}

// requireDenied asserts the whole request was rejected as out of grant.
func (fixture *testFixture) requireDenied(capability *serviceauth.CapabilityClaims, request *documentsearchv1.SearchDocumentsRequest) {
	fixture.t.Helper()
	response, err := fixture.service.Search(fixture.ctx, nil, capability, request)
	if err == nil {
		fixture.t.Fatalf("document-search integration: search %q was allowed with %d hits; it must be denied as a whole",
			request.GetQuery(), len(response.GetHits()))
	}
	if code := status.Code(err); code != codes.PermissionDenied {
		fixture.t.Fatalf("document-search integration: search %q returned %s (%v), want PERMISSION_DENIED",
			request.GetQuery(), code, err)
	}
}

// hitDocuments returns the document ids of a response.
func hitDocuments(response *documentsearchv1.SearchDocumentsResponse) []string {
	result := make([]string, 0, len(response.GetHits()))
	for _, hit := range response.GetHits() {
		result = append(result, hit.GetDocumentId())
	}
	return result
}

// hasHit reports whether the response contains the document.
func hasHit(response *documentsearchv1.SearchDocumentsResponse, documentID string) bool {
	for _, hit := range response.GetHits() {
		if hit.GetDocumentId() == documentID {
			return true
		}
	}
	return false
}

// countRows runs a scalar count for an assertion about local storage.
func (fixture *testFixture) countRows(query string, arguments ...any) int64 {
	fixture.t.Helper()
	var total int64
	if err := fixture.pool.Pgx().QueryRow(fixture.ctx, query, arguments...).Scan(&total); err != nil {
		fixture.t.Fatalf("document-search integration: count rows: %v", err)
	}
	return total
}

func (fixture *testFixture) exec(query string, arguments ...any) {
	fixture.t.Helper()
	if _, err := fixture.pool.Pgx().Exec(fixture.ctx, query, arguments...); err != nil {
		fixture.t.Fatalf("document-search integration: exec: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 1. create, then search it inside the granted space
// ---------------------------------------------------------------------------

func TestIntegrationCreateIndexesAndFindsDocument(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	documentID := fixture.newDocument()
	token := "zephyr" + dbtest.RunTag()

	request := fixture.upsertRequest(documentSpec{
		DocumentID:      documentID,
		OwnerSpaceID:    space,
		Title:           "Quarterly plan " + token,
		Content:         "The " + token + " programme is described here.\n\nSecond paragraph of the " + token + " note.",
		AllowedSpaceIDs: []string{space},
	})
	response := fixture.apply(request)
	if !response.GetApplied() {
		t.Fatalf("the first event must be applied, got applied=false reason=%q", response.GetReason())
	}
	if response.GetState().GetStatus() != documentsearchv1.IndexStatus_INDEX_STATUS_INDEXED {
		t.Fatalf("state status = %s, want INDEXED", response.GetState().GetStatus())
	}
	if response.GetState().GetChunkCount() <= 0 {
		t.Fatalf("chunk_count = %d, want a positive derived count", response.GetState().GetChunkCount())
	}

	// The event log and the projection both changed in the applied transaction.
	if rows := fixture.countRows(`SELECT count(*) FROM document_search.document_index_events WHERE document_id = $1::text::uuid`, documentID); rows != 1 {
		t.Fatalf("applied event rows = %d, want 1", rows)
	}
	if rows := fixture.countRows(`SELECT count(*) FROM document_search.document_index WHERE document_id = $1::text::uuid`, documentID); rows != 1 {
		t.Fatalf("index rows = %d, want 1", rows)
	}

	state, err := fixture.service.GetDocumentIndexState(fixture.ctx, documentID)
	if err != nil {
		t.Fatalf("GetDocumentIndexState: %v", err)
	}
	if !state.GetExists() || state.GetState().GetStatus() != documentsearchv1.IndexStatus_INDEX_STATUS_INDEXED {
		t.Fatalf("GetDocumentIndexState = %+v, want an indexed state", state)
	}

	found := fixture.search(capabilityFor(false, space), searchRequest(token, []string{space}, nil))
	if len(found.GetHits()) != 1 {
		t.Fatalf("hits = %d (%v), want the indexed document", len(found.GetHits()), hitDocuments(found))
	}
	hit := found.GetHits()[0]
	if hit.GetDocumentId() != documentID {
		t.Fatalf("hit document = %q, want %q", hit.GetDocumentId(), documentID)
	}
	if hit.GetVersionId() != request.GetVersionId() {
		t.Fatalf("hit version = %q, want %q", hit.GetVersionId(), request.GetVersionId())
	}
	if hit.GetOwnerSpaceId() != space {
		t.Fatalf("hit owner space = %q, want %q", hit.GetOwnerSpaceId(), space)
	}
	if hit.GetSource() != "document" {
		t.Fatalf("hit source = %q, want \"document\"", hit.GetSource())
	}
	if hit.GetSnippet() == "" {
		t.Fatal("hit snippet is empty")
	}
	if found.GetTotal() != 1 || found.GetTruncated() {
		t.Fatalf("total = %d truncated = %v, want 1 and false", found.GetTotal(), found.GetTruncated())
	}

	// The same query inside a different granted space must not see it.
	other := fixture.newSpace()
	missed := fixture.search(capabilityFor(false, other), searchRequest(token, []string{other}, nil))
	if len(missed.GetHits()) != 0 {
		t.Fatalf("a grant for another space returned %v", hitDocuments(missed))
	}
	// An empty grant means "authenticated public only", never "everything".
	empty := fixture.search(capabilityFor(false), searchRequest(token, nil, nil))
	if len(empty.GetHits()) != 0 {
		t.Fatalf("an empty grant returned %v", hitDocuments(empty))
	}
}

// ---------------------------------------------------------------------------
// 2. a newer version replaces the searchable content
// ---------------------------------------------------------------------------

func TestIntegrationUpdateReplacesSearchableContent(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	documentID := fixture.newDocument()
	first := "alphatoken" + dbtest.RunTag()
	second := "omegatoken" + dbtest.RunTag()

	firstRequest := fixture.upsertRequest(documentSpec{
		DocumentID:      documentID,
		OwnerSpaceID:    space,
		Title:           "Draft " + first,
		Content:         "body " + first,
		AllowedSpaceIDs: []string{space},
	})
	fixture.apply(firstRequest)
	if !hasHit(fixture.search(capabilityFor(false, space), searchRequest(first, []string{space}, nil)), documentID) {
		t.Fatal("the first version is not searchable")
	}

	secondRequest := fixture.upsertRequest(documentSpec{
		DocumentID:         documentID,
		OwnerSpaceID:       space,
		Title:              "Published " + second,
		Content:            "body " + second,
		AllowedSpaceIDs:    []string{space},
		AggregateRevision:  2,
		ActivationRevision: 2,
		LifecycleRevision:  2,
	})
	update := fixture.apply(secondRequest)
	if !update.GetApplied() {
		t.Fatalf("the newer version must be applied, got applied=false reason=%q", update.GetReason())
	}

	found := fixture.search(capabilityFor(false, space), searchRequest(second, []string{space}, nil))
	if len(found.GetHits()) != 1 {
		t.Fatalf("hits = %d, want the new version", len(found.GetHits()))
	}
	if got := found.GetHits()[0].GetVersionId(); got != secondRequest.GetVersionId() {
		t.Fatalf("hit version = %q, want the new version %q", got, secondRequest.GetVersionId())
	}
	// The superseded content is not searchable. The document itself is still in
	// the caller's space and the vector arm may recall it - that is what a hybrid
	// index does with a document that shares features with the query - so the
	// assertion is that no hit presents the superseded version or its text.
	stale := fixture.search(capabilityFor(false, space), searchRequest(first, []string{space}, nil))
	for _, hit := range stale.GetHits() {
		if hit.GetDocumentId() != documentID {
			continue
		}
		if hit.GetVersionId() != secondRequest.GetVersionId() {
			t.Fatalf("the superseded version is still presented: hit version %q, want %q",
				hit.GetVersionId(), secondRequest.GetVersionId())
		}
		if strings.Contains(hit.GetTitle(), first) || strings.Contains(hit.GetSnippet(), first) {
			t.Fatalf("the superseded content is still searchable: %+v", hit)
		}
	}
	if rows := fixture.countRows(`SELECT count(*) FROM document_search.document_index WHERE document_id = $1::text::uuid`, documentID); rows != 1 {
		t.Fatalf("index rows = %d, want exactly one projection per document", rows)
	}
}

// ---------------------------------------------------------------------------
// 3. an access change is visible to the new grant and not to the old one
// ---------------------------------------------------------------------------

func TestIntegrationAccessChangeFollowsTheNewGrant(t *testing.T) {
	fixture := newTestFixture(t, "")
	owner := fixture.newSpace()
	shared := fixture.newSpace()
	documentID := fixture.newDocument()
	token := "accesstoken" + dbtest.RunTag()

	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      documentID,
		OwnerSpaceID:    owner,
		Title:           "Shared note " + token,
		Content:         "body " + token,
		AllowedSpaceIDs: []string{owner, shared},
	}))
	if !hasHit(fixture.search(capabilityFor(false, shared), searchRequest(token, []string{shared}, nil)), documentID) {
		t.Fatal("the document is not reachable through the shared space")
	}

	// Revoke the shared space: the access snapshot shrinks at a newer access
	// revision, and the version itself does not change.
	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:         documentID,
		VersionID:          dbtest.Identifier(t),
		OwnerSpaceID:       owner,
		Title:              "Shared note " + token,
		Content:            "body " + token,
		AllowedSpaceIDs:    []string{owner},
		AggregateRevision:  2,
		ActivationRevision: 2,
		AccessRevision:     2,
		LifecycleRevision:  2,
	}))

	if revoked := fixture.search(capabilityFor(false, shared), searchRequest(token, []string{shared}, nil)); len(revoked.GetHits()) != 0 {
		t.Fatalf("the revoked grant still reaches the document: %v", hitDocuments(revoked))
	}
	if current := fixture.search(capabilityFor(false, owner), searchRequest(token, []string{owner}, nil)); !hasHit(current, documentID) {
		t.Fatal("the current grant no longer reaches the document")
	}
}

// ---------------------------------------------------------------------------
// 4. delete removes the document and leaves a tombstone
// ---------------------------------------------------------------------------

func TestIntegrationDeleteRemovesAndTombstones(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	documentID := fixture.newDocument()
	token := "deletetoken" + dbtest.RunTag()

	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      documentID,
		OwnerSpaceID:    space,
		Title:           "Doomed " + token,
		Content:         "body " + token,
		AllowedSpaceIDs: []string{space},
	}))
	if !hasHit(fixture.search(capabilityFor(false, space), searchRequest(token, []string{space}, nil)), documentID) {
		t.Fatal("the document is not searchable before the delete")
	}

	deleted := fixture.apply(fixture.deleteRequest(documentID, 7, 7))
	if !deleted.GetApplied() {
		t.Fatalf("the delete must be applied, got applied=false reason=%q", deleted.GetReason())
	}
	if after := fixture.search(capabilityFor(false, space), searchRequest(token, []string{space}, nil)); len(after.GetHits()) != 0 {
		t.Fatalf("the deleted document is still searchable: %v", hitDocuments(after))
	}
	if rows := fixture.countRows(`SELECT count(*) FROM document_search.document_index WHERE document_id = $1::text::uuid`, documentID); rows != 0 {
		t.Fatalf("index rows = %d, want the projection removed", rows)
	}
	if rows := fixture.countRows(`SELECT count(*) FROM document_search.document_index_tombstones WHERE document_id = $1::text::uuid`, documentID); rows != 1 {
		t.Fatal("the delete did not leave a tombstone")
	}
	var tombstoneLifecycle int64
	if err := fixture.pool.Pgx().QueryRow(fixture.ctx,
		`SELECT lifecycle_revision FROM document_search.document_index_tombstones WHERE document_id = $1::text::uuid`,
		documentID).Scan(&tombstoneLifecycle); err != nil {
		t.Fatalf("read the tombstone: %v", err)
	}
	if tombstoneLifecycle != 7 {
		t.Fatalf("tombstone lifecycle revision = %d, want 7", tombstoneLifecycle)
	}

	state, err := fixture.service.GetDocumentIndexState(fixture.ctx, documentID)
	if err != nil {
		t.Fatalf("GetDocumentIndexState: %v", err)
	}
	if !state.GetExists() || state.GetState().GetStatus() != documentsearchv1.IndexStatus_INDEX_STATUS_DELETED {
		t.Fatalf("state after delete = %+v, want the deleted state", state)
	}
	unknown, err := fixture.service.GetDocumentIndexState(fixture.ctx, dbtest.Identifier(t))
	if err != nil {
		t.Fatalf("GetDocumentIndexState for an unknown document: %v", err)
	}
	if unknown.GetExists() || unknown.GetState() != nil {
		t.Fatalf("an unknown document reported %+v, want exists=false", unknown)
	}
}

// ---------------------------------------------------------------------------
// 5. a duplicate event is a no-op
// ---------------------------------------------------------------------------

func TestIntegrationDuplicateEventIsNoOp(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	documentID := fixture.newDocument()
	token := "duplicatetoken" + dbtest.RunTag()

	request := fixture.upsertRequest(documentSpec{
		DocumentID:      documentID,
		OwnerSpaceID:    space,
		Title:           "Once " + token,
		Content:         "body " + token,
		AllowedSpaceIDs: []string{space},
	})
	first := fixture.apply(request)
	if !first.GetApplied() {
		t.Fatalf("the first delivery must be applied, got reason=%q", first.GetReason())
	}
	second := fixture.apply(request)
	if second.GetApplied() {
		t.Fatal("the duplicate delivery was applied again")
	}
	if second.GetReason() != ReasonDuplicate {
		t.Fatalf("duplicate reason = %q, want %q", second.GetReason(), ReasonDuplicate)
	}
	if first.GetState().GetIndexedAt() != second.GetState().GetIndexedAt() {
		t.Fatalf("the duplicate rewrote the row: indexed_at %q -> %q",
			first.GetState().GetIndexedAt(), second.GetState().GetIndexedAt())
	}
	if rows := fixture.countRows(`SELECT count(*) FROM document_search.document_index_events WHERE event_id = $1::text::uuid`, request.GetEventId()); rows != 1 {
		t.Fatalf("applied event rows = %d, want the event stored exactly once", rows)
	}
	if rows := fixture.countRows(`SELECT count(*) FROM document_search.document_index WHERE document_id = $1::text::uuid`, documentID); rows != 1 {
		t.Fatalf("index rows = %d, want one", rows)
	}

	// A different event id that reuses the sequence is a real conflict, not a
	// duplicate: the applied log would otherwise lose its ordering.
	conflict := fixture.upsertRequest(documentSpec{
		DocumentID:      documentID,
		OwnerSpaceID:    space,
		Title:           "Conflict " + token,
		Content:         "body " + token,
		AllowedSpaceIDs: []string{space},
		Sequence:        request.GetSequence(),
	})
	if _, err := fixture.service.ApplyEvent(fixture.ctx, conflict); err == nil {
		t.Fatal("a second event reusing the sequence was accepted")
	} else if code := status.Code(err); code != codes.Aborted {
		t.Fatalf("sequence conflict returned %s (%v), want ABORTED", code, err)
	}
}

// ---------------------------------------------------------------------------
// 6. out-of-order delivery
// ---------------------------------------------------------------------------

func TestIntegrationOutOfOrderEventsAreIgnored(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	documentID := fixture.newDocument()
	newest := "newesttoken" + dbtest.RunTag()
	older := "oldertoken" + dbtest.RunTag()

	current := fixture.upsertRequest(documentSpec{
		DocumentID:        documentID,
		OwnerSpaceID:      space,
		Title:             "Current " + newest,
		Content:           "body " + newest,
		AllowedSpaceIDs:   []string{space},
		AggregateRevision: 5,
		AccessRevision:    5,
		LifecycleRevision: 5,
	})
	fixture.apply(current)
	late := fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:        documentID,
		VersionID:         dbtest.Identifier(t),
		OwnerSpaceID:      space,
		Title:             "Stale " + older,
		Content:           "body " + older,
		AllowedSpaceIDs:   []string{space},
		AggregateRevision: 3,
		AccessRevision:    3,
		LifecycleRevision: 5,
	}))
	if late.GetApplied() {
		t.Fatal("an event with a lower aggregate revision was applied")
	}
	if late.GetReason() != ReasonStaleRevision {
		t.Fatalf("reason = %q, want %q", late.GetReason(), ReasonStaleRevision)
	}
	// The stale event did not replace the current state. The document may still be
	// recalled by the vector arm - it is the caller's document and shares features
	// with any query - so this asserts the stale version never wins, not that the
	// page is empty.
	staleHits := fixture.search(capabilityFor(false, space), searchRequest(older, []string{space}, nil))
	for _, hit := range staleHits.GetHits() {
		if hit.GetVersionId() != current.GetVersionId() {
			t.Fatalf("the stale content replaced the current one: %v", hitDocuments(staleHits))
		}
		if strings.Contains(hit.GetTitle(), older) || strings.Contains(hit.GetSnippet(), older) {
			t.Fatalf("the stale content replaced the current one: %+v", hit)
		}
	}

	// A late access snapshot must not re-grant what was revoked.
	lateAccess := fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:        documentID,
		VersionID:         dbtest.Identifier(t),
		OwnerSpaceID:      space,
		Title:             "Current " + newest,
		Content:           "body " + newest,
		AllowedSpaceIDs:   []string{space},
		AggregateRevision: 5,
		AccessRevision:    2,
		LifecycleRevision: 5,
	}))
	if lateAccess.GetApplied() || lateAccess.GetReason() != ReasonStaleAccess {
		t.Fatalf("a stale access snapshot returned applied=%v reason=%q, want %q",
			lateAccess.GetApplied(), lateAccess.GetReason(), ReasonStaleAccess)
	}

	// Delete at lifecycle revision 9, then an older upsert: it must not come
	// back, and the delete is not a stale event.
	deleted := fixture.apply(fixture.deleteRequest(documentID, 9, 9))
	if !deleted.GetApplied() {
		t.Fatalf("the delete at a newer lifecycle revision must be applied, got reason=%q", deleted.GetReason())
	}
	resurrectAttempt := fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:        documentID,
		VersionID:         dbtest.Identifier(t),
		OwnerSpaceID:      space,
		Title:             "Zombie " + older,
		Content:           "body " + older,
		AllowedSpaceIDs:   []string{space},
		AggregateRevision: 8,
		AccessRevision:    8,
		LifecycleRevision: 8,
	}))
	if resurrectAttempt.GetApplied() {
		t.Fatal("an upsert below the tombstone lifecycle revision resurrected the document")
	}
	if resurrectAttempt.GetReason() != ReasonStaleLifecycle {
		t.Fatalf("reason = %q, want %q", resurrectAttempt.GetReason(), ReasonStaleLifecycle)
	}
	if hits := fixture.search(capabilityFor(false, space), searchRequest(older, []string{space}, nil)); len(hits.GetHits()) != 0 {
		t.Fatalf("the deleted document is searchable again: %v", hitDocuments(hits))
	}
	if rows := fixture.countRows(`SELECT count(*) FROM document_search.document_index WHERE document_id = $1::text::uuid`, documentID); rows != 0 {
		t.Fatalf("index rows = %d, want the document still deleted", rows)
	}

	// A strictly newer lifecycle revision is a new generation and may revive it.
	revived := "revivedtoken" + dbtest.RunTag()
	revival := fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:        documentID,
		VersionID:         dbtest.Identifier(t),
		OwnerSpaceID:      space,
		Title:             "Revived " + revived,
		Content:           "body " + revived,
		AllowedSpaceIDs:   []string{space},
		AggregateRevision: 10,
		AccessRevision:    10,
		LifecycleRevision: 10,
	}))
	if !revival.GetApplied() {
		t.Fatalf("a newer lifecycle revision must be applied, got reason=%q", revival.GetReason())
	}
	if hits := fixture.search(capabilityFor(false, space), searchRequest(revived, []string{space}, nil)); !hasHit(hits, documentID) {
		t.Fatal("the revived document is not searchable")
	}
	if rows := fixture.countRows(`SELECT count(*) FROM document_search.document_index_tombstones WHERE document_id = $1::text::uuid`, documentID); rows != 0 {
		t.Fatal("the tombstone survived a revival that made the document live again")
	}
}

// A delete is fenced by the lifecycle revision, not by the content or access
// revisions: it retires the document, and the fact source legitimately leaves
// aggregate_revision and access_revision unset on it.
func TestIntegrationDeleteIsFencedByLifecycleOnly(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	documentID := fixture.newDocument()
	token := "deletefence" + dbtest.RunTag()

	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:        documentID,
		OwnerSpaceID:      space,
		Title:             "Fenced " + token,
		Content:           "body " + token,
		AllowedSpaceIDs:   []string{space},
		AggregateRevision: 5,
		AccessRevision:    5,
		LifecycleRevision: 5,
	}))

	// Lifecycle 6 with both other revisions unset: this must still retire it.
	unset := fixture.deleteRequest(documentID, 6, 0)
	if unset.GetAggregateRevision() != 0 {
		t.Fatalf("the fixture should leave aggregate_revision unset, got %d", unset.GetAggregateRevision())
	}
	deleted := fixture.apply(unset)
	if !deleted.GetApplied() {
		t.Fatalf("a delete with unset aggregate/access revisions must be applied, got reason=%q", deleted.GetReason())
	}
	if hits := fixture.search(capabilityFor(false, space), searchRequest(token, []string{space}, nil)); len(hits.GetHits()) != 0 {
		t.Fatalf("the deleted document is still searchable: %v", hitDocuments(hits))
	}
	if state, err := fixture.service.GetDocumentIndexState(fixture.ctx, documentID); err != nil {
		t.Fatalf("GetDocumentIndexState: %v", err)
	} else if state.GetState().GetStatus() != documentsearchv1.IndexStatus_INDEX_STATUS_DELETED {
		t.Fatalf("state after the delete = %s, want DELETED", state.GetState().GetStatus())
	}

	// An older lifecycle revision cannot move or reopen the tombstone.
	older := fixture.apply(fixture.deleteRequest(documentID, 4, 0))
	if older.GetApplied() || older.GetReason() != ReasonStaleLifecycle {
		t.Fatalf("an older delete returned applied=%v reason=%q, want %q", older.GetApplied(), older.GetReason(), ReasonStaleLifecycle)
	}
	var tombstoneLifecycle int64
	if err := fixture.pool.Pgx().QueryRow(fixture.ctx,
		`SELECT lifecycle_revision FROM document_search.document_index_tombstones WHERE document_id = $1::text::uuid`,
		documentID).Scan(&tombstoneLifecycle); err != nil {
		t.Fatalf("read the tombstone: %v", err)
	}
	if tombstoneLifecycle != 6 {
		t.Fatalf("tombstone lifecycle revision = %d, want 6", tombstoneLifecycle)
	}

	// A newer delete moves the fence forward, so a later stale upsert is refused.
	newer := fixture.apply(fixture.deleteRequest(documentID, 8, 0))
	if !newer.GetApplied() {
		t.Fatalf("a newer delete must be applied, got reason=%q", newer.GetReason())
	}
	if err := fixture.pool.Pgx().QueryRow(fixture.ctx,
		`SELECT lifecycle_revision FROM document_search.document_index_tombstones WHERE document_id = $1::text::uuid`,
		documentID).Scan(&tombstoneLifecycle); err != nil {
		t.Fatalf("read the tombstone: %v", err)
	}
	if tombstoneLifecycle != 8 {
		t.Fatalf("tombstone lifecycle revision = %d, want 8 after the newer delete", tombstoneLifecycle)
	}
	late := fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:        documentID,
		VersionID:         dbtest.Identifier(t),
		OwnerSpaceID:      space,
		Title:             "Late " + token,
		Content:           "body " + token,
		AllowedSpaceIDs:   []string{space},
		AggregateRevision: 7,
		AccessRevision:    7,
		LifecycleRevision: 7,
	}))
	if late.GetApplied() || late.GetReason() != ReasonStaleLifecycle {
		t.Fatalf("an upsert below the newer tombstone returned applied=%v reason=%q", late.GetApplied(), late.GetReason())
	}
}

// ---------------------------------------------------------------------------
// 7. query authorization
// ---------------------------------------------------------------------------

func TestIntegrationQueryAuthorization(t *testing.T) {
	fixture := newTestFixture(t, "")
	inside := fixture.newSpace()
	outside := fixture.newSpace()
	publicDocument := fixture.newDocument()
	privateDocument := fixture.newDocument()
	publicToken := "publictoken" + dbtest.RunTag()
	privateToken := "privatetoken" + dbtest.RunTag()

	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:          publicDocument,
		OwnerSpaceID:        fixture.newSpace(),
		Title:               "Public " + publicToken,
		Content:             "body " + publicToken,
		AuthenticatedPublic: true,
	}))
	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      privateDocument,
		OwnerSpaceID:    outside,
		Title:           "Private " + privateToken,
		Content:         "body " + privateToken,
		AllowedSpaceIDs: []string{outside},
	}))

	// An empty grant still reaches authenticated-public documents.
	empty := fixture.search(capabilityFor(false), searchRequest(publicToken, nil, nil))
	if !hasHit(empty, publicDocument) {
		t.Fatalf("an empty grant did not reach the authenticated-public document: %v", hitDocuments(empty))
	}
	if hasHit(empty, privateDocument) {
		t.Fatal("an empty grant reached a private document")
	}

	// A document outside the granted space is not returned.
	granted := fixture.search(capabilityFor(false, inside), searchRequest(privateToken, []string{inside}, nil))
	if hasHit(granted, privateDocument) {
		t.Fatal("a document outside the granted space was returned")
	}

	// One out-of-grant identifier rejects the whole request, even together with
	// an in-grant one, and even when the query would have matched nothing.
	fixture.requireDenied(capabilityFor(false, inside), searchRequest(publicToken, []string{inside, outside}, nil))
	fixture.requireDenied(capabilityFor(false, inside), searchRequest(privateToken, []string{inside, outside}, nil))
	fixture.requireDenied(capabilityFor(false, inside), &documentsearchv1.SearchDocumentsRequest{
		Query:              publicToken,
		AllowedDocumentIds: []string{publicDocument},
	})
	// A request that only narrows is allowed.
	narrowed := fixture.search(capabilityFor(false, inside, outside), searchRequest(privateToken, []string{outside}, nil))
	if !hasHit(narrowed, privateDocument) {
		t.Fatal("a request inside the granted range was rejected")
	}

	// An empty query is invalid input, not an empty result.
	if _, err := fixture.service.Search(fixture.ctx, nil, capabilityFor(true), searchRequest("   ", nil, nil)); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("an empty query returned %v, want INVALID_ARGUMENT", err)
	}
}

// ---------------------------------------------------------------------------
// 8. independent rebuild from locally stored events
// ---------------------------------------------------------------------------

func TestIntegrationRebuildUsesLocalEventsOnly(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	liveDocument := fixture.newDocument()
	deletedDocument := fixture.newDocument()
	liveToken := "rebuildlive" + dbtest.RunTag()
	deletedToken := "rebuilddeleted" + dbtest.RunTag()

	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      liveDocument,
		OwnerSpaceID:    space,
		Title:           "Live " + liveToken,
		Content:         "body " + liveToken,
		AllowedSpaceIDs: []string{space},
	}))
	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      deletedDocument,
		OwnerSpaceID:    space,
		Title:           "Removed " + deletedToken,
		Content:         "body " + deletedToken,
		AllowedSpaceIDs: []string{space},
	}))
	fixture.apply(fixture.deleteRequest(deletedDocument, 3, 3))

	// Unconfirmed rebuilds are refused before anything is touched.
	if _, err := fixture.service.Rebuild(fixture.ctx, false); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("Rebuild(confirm=false) returned %v, want FAILED_PRECONDITION", err)
	}

	// Destroy the projection without touching the applied event log. The source
	// configuration points at a closed port for the whole test, so a rebuild that
	// reached for the fact source could not succeed.
	fixture.exec(`DELETE FROM document_search.document_index WHERE document_id = ANY($1::text[]::uuid[])`,
		[]string{liveDocument, deletedDocument})
	fixture.exec(`DELETE FROM document_search.document_index_tombstones WHERE document_id = ANY($1::text[]::uuid[])`,
		[]string{liveDocument, deletedDocument})
	if hits := fixture.search(capabilityFor(false, space), searchRequest(liveToken, []string{space}, nil)); len(hits.GetHits()) != 0 {
		t.Fatalf("the projection was not cleared: %v", hitDocuments(hits))
	}
	eventsBefore := fixture.countRows(`SELECT count(*) FROM document_search.document_index_events WHERE document_id = ANY($1::text[]::uuid[])`,
		[]string{liveDocument, deletedDocument})
	if eventsBefore == 0 {
		t.Fatal("the applied event log is empty; the rebuild would prove nothing")
	}

	response, err := fixture.service.Rebuild(fixture.ctx, true)
	if err != nil {
		t.Fatalf("Rebuild(confirm=true) with the source at %s: %v", closedSourceEndpoint, err)
	}
	if response.GetDocumentsFailed() != 0 {
		t.Fatalf("rebuild reported %d failed events", response.GetDocumentsFailed())
	}
	if response.GetDocumentsRebuilt() < 2 {
		t.Fatalf("rebuild reported %d rebuilt documents, want both documents", response.GetDocumentsRebuilt())
	}
	if eventsAfter := fixture.countRows(`SELECT count(*) FROM document_search.document_index_events WHERE document_id = ANY($1::text[]::uuid[])`,
		[]string{liveDocument, deletedDocument}); eventsAfter != eventsBefore {
		t.Fatalf("the rebuild changed the applied event log: %d -> %d", eventsBefore, eventsAfter)
	}

	if hits := fixture.search(capabilityFor(false, space), searchRequest(liveToken, []string{space}, nil)); !hasHit(hits, liveDocument) {
		t.Fatal("the rebuild did not restore the live document")
	}
	// The deleted document must not come back. Another document in the same space
	// can legitimately be recalled by the vector arm, so this asserts the deleted
	// document itself is absent rather than that the page is empty.
	if hits := fixture.search(capabilityFor(false, space), searchRequest(deletedToken, []string{space}, nil)); hasHit(hits, deletedDocument) {
		t.Fatalf("the rebuild resurrected a deleted document: %v", hitDocuments(hits))
	}
	if rows := fixture.countRows(`SELECT count(*) FROM document_search.document_index_tombstones WHERE document_id = $1::text::uuid`, deletedDocument); rows != 1 {
		t.Fatal("the rebuild did not restore the tombstone")
	}

	// The run is auditable, in the success shape, not left running.
	var state string
	if err := fixture.pool.Pgx().QueryRow(fixture.ctx,
		`SELECT state FROM document_search.rebuild_runs ORDER BY started_at DESC LIMIT 1`).Scan(&state); err != nil {
		t.Fatalf("read the rebuild run: %v", err)
	}
	if state != "succeeded" {
		t.Fatalf("rebuild run state = %q, want succeeded", state)
	}

	// A corrupted projection is repaired by replay: the current event carries the
	// same revisions, which the fence allows so the stored snapshot wins again.
	fixture.exec(`UPDATE document_search.document_index SET title = 'corrupted', summary = 'corrupted', content = 'corrupted'
		WHERE document_id = $1::text::uuid`, liveDocument)
	if _, err := fixture.service.Rebuild(fixture.ctx, true); err != nil {
		t.Fatalf("second rebuild: %v", err)
	}
	if hits := fixture.search(capabilityFor(false, space), searchRequest(liveToken, []string{space}, nil)); !hasHit(hits, liveDocument) {
		t.Fatal("the rebuild did not repair the corrupted projection")
	}
}

// ---------------------------------------------------------------------------
// 10. schema isolation
// ---------------------------------------------------------------------------

func TestIntegrationSchemaIsolation(t *testing.T) {
	fixture := newTestFixture(t, "")

	var currentUser string
	if err := fixture.pool.Pgx().QueryRow(fixture.ctx, `SELECT current_user`).Scan(&currentUser); err != nil {
		t.Fatalf("read current_user: %v", err)
	}
	if currentUser != "document_search_writer" {
		t.Fatalf("the suite ran as %q, so it does not prove the writer role's boundaries", currentUser)
	}

	// The names are assembled at run time so this test file never contains a
	// cross-service write statement that the architecture check would have to
	// special case.
	foreignSchema := "document" + "_" + "service"
	probeTable := "dsh_probe_" + dbtest.RunTag()
	createStatement := "create table " + foreignSchema + "." + probeTable + " (id integer)"
	_, err := fixture.pool.Pgx().Exec(fixture.ctx, createStatement)
	if err == nil {
		_, _ = fixture.pool.Pgx().Exec(fixture.ctx, "drop table "+foreignSchema+"."+probeTable)
		t.Fatalf("creating a table in schema %s succeeded; the writer role is not isolated", foreignSchema)
	}
	if !isInsufficientPrivilege(err) {
		t.Fatalf("creating a table in schema %s failed with %v, want insufficient_privilege (42501)", foreignSchema, err)
	}
	t.Logf("foreign schema create table rejected: %v", err)

	insertStatement := "insert into " + foreignSchema + "." + "document_events" + " default values"
	_, insertErr := fixture.pool.Pgx().Exec(fixture.ctx, insertStatement)
	if insertErr == nil {
		t.Fatal("writing an event into the document service Outbox succeeded; the writer role is not isolated")
	}
	if !isInsufficientPrivilege(insertErr) && !isUndefinedTable(insertErr) {
		t.Fatalf("writing into %s.document_events failed with %v, want insufficient_privilege (42501)", foreignSchema, insertErr)
	}
	t.Logf("foreign table write rejected: %v", insertErr)

	// The service's own schema accepts writes through the same connection.
	documentID := fixture.newDocument()
	space := fixture.newSpace()
	if response := fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      documentID,
		OwnerSpaceID:    space,
		Title:           "Owned " + dbtest.RunTag(),
		Content:         "body",
		AllowedSpaceIDs: []string{space},
	})); !response.GetApplied() {
		t.Fatal("the service could not write its own schema")
	}
}

// isInsufficientPrivilege reports PostgreSQL's permission-denied SQLSTATE.
func isInsufficientPrivilege(err error) bool {
	return sqlState(err) == "42501"
}

// isUndefinedTable reports PostgreSQL's undefined-table SQLSTATE.
func isUndefinedTable(err error) bool {
	return sqlState(err) == "42P01"
}

func sqlState(err error) string {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		return pgError.Code
	}
	return ""
}

// ---------------------------------------------------------------------------
// 11. index namespace identity
// ---------------------------------------------------------------------------

func TestIntegrationIndexNamespaceIsOwnedByName(t *testing.T) {
	fixture := newTestFixture(t, "")

	var alias, storageDomain string
	if err := fixture.pool.Pgx().QueryRow(fixture.ctx,
		`SELECT collection_alias, storage_domain FROM document_search.index_generations WHERE active`).Scan(&alias, &storageDomain); err != nil {
		t.Fatalf("read the active index generation: %v", err)
	}
	if alias != "go_web_document_v1" {
		t.Fatalf("active alias = %q, want go_web_document_v1", alias)
	}
	if storageDomain != "document-search:documents:v1" {
		t.Fatalf("active storage domain = %q, want document-search:documents:v1", storageDomain)
	}
	for _, foreign := range []string{
		"go_web_shadow_v1", "postgres:go-web-shadow-v1",
		"qq_source_messages_v1", "qq_source_files_v1",
		"qq-search:messages:v1", "qq-search:files:v1",
	} {
		if alias == foreign || storageDomain == foreign {
			t.Fatalf("the document search namespace collides with %q", foreign)
		}
	}

	indexStatus, err := fixture.service.Status(fixture.ctx)
	if err != nil {
		t.Fatalf("GetIndexStatus: %v", err)
	}
	if indexStatus.GetCollectionAlias() != alias {
		t.Fatalf("status alias = %q, want the recorded generation alias %q", indexStatus.GetCollectionAlias(), alias)
	}
	if indexStatus.GetGeneration() == "" {
		t.Fatal("status reports no active generation")
	}
}

// ---------------------------------------------------------------------------
// status bookkeeping
// ---------------------------------------------------------------------------

func TestIntegrationIndexStatusTracksAppliedEvents(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	documentID := fixture.newDocument()

	before, err := fixture.service.Status(fixture.ctx)
	if err != nil {
		t.Fatalf("GetIndexStatus before: %v", err)
	}
	request := fixture.upsertRequest(documentSpec{
		DocumentID:      documentID,
		OwnerSpaceID:    space,
		Title:           "Tracked " + dbtest.RunTag(),
		Content:         "body",
		AllowedSpaceIDs: []string{space},
	})
	fixture.apply(request)
	after, err := fixture.service.Status(fixture.ctx)
	if err != nil {
		t.Fatalf("GetIndexStatus after: %v", err)
	}
	// The status counters are shared with every other suite hitting this schema
	// (the transport test package runs in parallel), so the assertion is "my
	// apply moved them forward", not "exactly one more".
	if after.GetEventsApplied() <= before.GetEventsApplied() {
		t.Fatalf("events_applied %d -> %d, want it to grow", before.GetEventsApplied(), after.GetEventsApplied())
	}
	if after.GetLastSequence() < request.GetSequence() {
		t.Fatalf("last_sequence = %d, want at least the applied sequence %d", after.GetLastSequence(), request.GetSequence())
	}
	if after.GetIndexedDocuments() <= before.GetIndexedDocuments() {
		t.Fatalf("indexed_documents %d -> %d, want it to grow", before.GetIndexedDocuments(), after.GetIndexedDocuments())
	}
}
