package application

import (
	"errors"
	"strings"
	"testing"
	"time"

	"document-search/internal/config"

	documentv1 "packages/gen/document/v1"

	"google.golang.org/protobuf/proto"
	documentsearchv1 "packages/gen/documentsearch/v1"
	"packages/serviceauth"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These tests run without a database. They cover the rules that decide whether
// an event is applied at all - kind validation, the revision fences, the
// inclusion rule and the deterministic chunking - because those rules are the
// difference between a derived index and a second, disagreeing copy of the
// document.

func unitConfig() config.Config {
	cfg := config.Default()
	cfg.Index.IndexProfile = "markdown-v1"
	cfg.Index.MaxChunks = 200
	return cfg
}

func baseUpsert() *documentsearchv1.IndexDocumentEventRequest {
	return &documentsearchv1.IndexDocumentEventRequest{
		EventId:           "11111111-1111-4111-8111-111111111111",
		Sequence:          42,
		Kind:              "upsert",
		DocumentId:        "22222222-2222-4222-8222-222222222222",
		VersionId:         "33333333-3333-4333-8333-333333333333",
		OwnerSpaceId:      "44444444-4444-4444-8444-444444444444",
		OwnerSubjectKey:   "web:user:7",
		AggregateRevision: 3,
		AccessRevision:    3,
		LifecycleRevision: 3,
		LifecycleStatus:   "active",
		PublicationStatus: "published",
		Title:             "A title",
		Content:           "A body with enough words to be chunked.",
	}
}

func TestNormalizeEventKindValidation(t *testing.T) {
	cfg := unitConfig()
	for _, kind := range []string{"upsert", "UPSERT", " delete ", "Delete"} {
		request := baseUpsert()
		request.Kind = kind
		if kind == " delete " || kind == "Delete" {
			request.VersionId = ""
		}
		if _, err := normalizeEvent(request, cfg); err != nil {
			t.Fatalf("kind %q was rejected: %v", kind, err)
		}
	}
	for _, kind := range []string{"", "   ", "access_changed", "publish", "upsert_delete"} {
		request := baseUpsert()
		request.Kind = kind
		_, err := normalizeEvent(request, cfg)
		if err == nil {
			t.Fatalf("kind %q was accepted", kind)
		}
		if !errors.Is(err, ErrUnsupportedEventKind) {
			t.Fatalf("kind %q returned %v, want ErrUnsupportedEventKind", kind, err)
		}
		if code := status.Code(grpcError(err)); code != codes.InvalidArgument {
			t.Fatalf("kind %q maps to %s, want INVALID_ARGUMENT", kind, code)
		}
	}
}

func TestNormalizeEventRejectsIncompleteEvents(t *testing.T) {
	cfg := unitConfig()

	missingDocument := baseUpsert()
	missingDocument.DocumentId = "  "
	if _, err := normalizeEvent(missingDocument, cfg); !errors.Is(err, ErrMissingDocumentID) {
		t.Fatalf("missing document_id returned %v, want ErrMissingDocumentID", err)
	}

	malformedDocument := baseUpsert()
	malformedDocument.DocumentId = "not-a-uuid"
	if _, err := normalizeEvent(malformedDocument, cfg); !errors.Is(err, ErrInvalidIdentifier) {
		t.Fatalf("malformed document_id returned %v, want ErrInvalidIdentifier", err)
	}

	missingEvent := baseUpsert()
	missingEvent.EventId = ""
	if _, err := normalizeEvent(missingEvent, cfg); !errors.Is(err, ErrMissingEventID) {
		t.Fatalf("missing event_id returned %v, want ErrMissingEventID", err)
	}

	missingSequence := baseUpsert()
	missingSequence.Sequence = 0
	if _, err := normalizeEvent(missingSequence, cfg); !errors.Is(err, ErrMissingSequence) {
		t.Fatalf("missing sequence returned %v, want ErrMissingSequence", err)
	}

	missingVersion := baseUpsert()
	missingVersion.VersionId = ""
	if _, err := normalizeEvent(missingVersion, cfg); !errors.Is(err, ErrMissingVersionID) {
		t.Fatalf("missing version_id returned %v, want ErrMissingVersionID", err)
	}

	missingOwnerSpace := baseUpsert()
	missingOwnerSpace.OwnerSpaceId = ""
	if _, err := normalizeEvent(missingOwnerSpace, cfg); !errors.Is(err, ErrMissingOwnerSpace) {
		t.Fatalf("missing owner_space_id returned %v, want ErrMissingOwnerSpace", err)
	}

	// A delete carries no version and is still valid.
	deleted := baseUpsert()
	deleted.Kind = "delete"
	deleted.VersionId = ""
	deleted.OwnerSpaceId = ""
	event, err := normalizeEvent(deleted, cfg)
	if err != nil {
		t.Fatalf("a delete event was rejected: %v", err)
	}
	if event.Kind != EventKindDelete {
		t.Fatalf("kind = %q, want delete", event.Kind)
	}
}

func TestNormalizeEventCanonicalizesTheStoredProjection(t *testing.T) {
	cfg := unitConfig()
	request := baseUpsert()
	request.AllowedSpaceIds = []string{
		" 44444444-4444-4444-8444-444444444444 ",
		"55555555-5555-4555-8555-555555555555",
		"44444444-4444-4444-8444-444444444444",
	}
	request.LifecycleStatus = "LIFECYCLE_STATUS_ACTIVE"
	request.PublicationStatus = "PUBLICATION_STATUS_PUBLISHED"
	request.Title = strings.Repeat("t", 300)
	request.IndexProfile = ""
	request.ContentSha256 = ""

	event, err := normalizeEvent(request, cfg)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	want := []string{"44444444-4444-4444-8444-444444444444", "55555555-5555-4555-8555-555555555555"}
	if len(event.AllowedSpaceIDs) != len(want) {
		t.Fatalf("allowed spaces = %v, want %v", event.AllowedSpaceIDs, want)
	}
	for index, value := range want {
		if event.AllowedSpaceIDs[index] != value {
			t.Fatalf("allowed spaces = %v, want %v", event.AllowedSpaceIDs, want)
		}
	}
	if event.LifecycleStatus != "active" {
		t.Fatalf("lifecycle status = %q, want active", event.LifecycleStatus)
	}
	if event.PublicationStatus != "published" {
		t.Fatalf("publication status = %q, want published", event.PublicationStatus)
	}
	if len([]rune(event.Title)) != 255 {
		t.Fatalf("title runes = %d, want the column width 255", len([]rune(event.Title)))
	}
	if event.IndexProfile != "markdown-v1" {
		t.Fatalf("index profile = %q, want the configured default", event.IndexProfile)
	}
	if len(event.ContentSHA256) != 64 {
		t.Fatalf("content digest = %q, want a derived sha256", event.ContentSHA256)
	}
	if event.ChunkCount <= 0 {
		t.Fatalf("chunk count = %d, want a positive derived count", event.ChunkCount)
	}
	if event.DocumentID != strings.ToLower(request.DocumentId) {
		t.Fatalf("document id = %q, want the canonical form", event.DocumentID)
	}
}

func TestNormalizeEventTreatsUnstatedPublicationAsDraft(t *testing.T) {
	cfg := unitConfig()
	request := baseUpsert()
	request.PublicationStatus = ""
	request.LifecycleStatus = ""
	event, err := normalizeEvent(request, cfg)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if event.PublicationStatus != "draft" {
		t.Fatalf("publication status = %q, want draft so an unstated version is not searchable", event.PublicationStatus)
	}
	if event.LifecycleStatus != "active" {
		t.Fatalf("lifecycle status = %q, want active", event.LifecycleStatus)
	}
}

func TestEvaluateFenceRejectsOutOfOrderRevisions(t *testing.T) {
	live := fenceState{HasIndex: true, AggregateRevision: 5, AccessRevision: 5, LifecycleRevision: 5}
	tombstoned := fenceState{HasTombstone: true, TombstoneLifecycleRevision: 9}
	fresh := fenceState{}

	cases := []struct {
		name  string
		state fenceState
		event eventRevisions
		want  string
	}{
		{"unknown document accepts anything", fresh, eventRevisions{Kind: EventKindUpsert, AggregateRevision: 1, AccessRevision: 1, LifecycleRevision: 1}, ""},
		{"equal revisions are idempotent", live, eventRevisions{Kind: EventKindUpsert, AggregateRevision: 5, AccessRevision: 5, LifecycleRevision: 5}, ""},
		{"higher revisions are newer", live, eventRevisions{Kind: EventKindUpsert, AggregateRevision: 6, AccessRevision: 6, LifecycleRevision: 6}, ""},
		{"lower aggregate revision is stale", live, eventRevisions{Kind: EventKindUpsert, AggregateRevision: 4, AccessRevision: 5, LifecycleRevision: 5}, ReasonStaleRevision},
		{"lower lifecycle revision is stale", live, eventRevisions{Kind: EventKindUpsert, AggregateRevision: 5, AccessRevision: 5, LifecycleRevision: 4}, ReasonStaleLifecycle},
		{"lower access revision is stale", live, eventRevisions{Kind: EventKindUpsert, AggregateRevision: 5, AccessRevision: 4, LifecycleRevision: 5}, ReasonStaleAccess},
		// A delete retires the document; it is not an access change, so an older or
		// absent access_revision must not keep a deleted document searchable.
		{"delete ignores the access fence", live, eventRevisions{Kind: EventKindDelete, AggregateRevision: 6, AccessRevision: 0, LifecycleRevision: 6}, ""},
		{"delete still respects the lifecycle fence", live, eventRevisions{Kind: EventKindDelete, AggregateRevision: 6, AccessRevision: 0, LifecycleRevision: 4}, ReasonStaleLifecycle},
		{"delete still respects the aggregate fence", live, eventRevisions{Kind: EventKindDelete, AggregateRevision: 4, AccessRevision: 0, LifecycleRevision: 6}, ReasonStaleRevision},
		// Zero is the unset value of aggregate_revision: a delete that does not
		// state a content revision still has to retire the document, or an unset
		// field in the source envelope would leave deleted documents indexed.
		{"delete without an aggregate revision is not fenced by it", live, eventRevisions{Kind: EventKindDelete, AggregateRevision: 0, AccessRevision: 0, LifecycleRevision: 6}, ""},
		{"delete only closes a tombstone when it is newer", tombstoned, eventRevisions{Kind: EventKindDelete, AggregateRevision: 9, AccessRevision: 0, LifecycleRevision: 9}, ReasonStaleLifecycle},
		{"tombstone blocks an older lifecycle", tombstoned, eventRevisions{Kind: EventKindUpsert, AggregateRevision: 8, AccessRevision: 8, LifecycleRevision: 8}, ReasonStaleLifecycle},
		{"tombstone blocks the same lifecycle", tombstoned, eventRevisions{Kind: EventKindUpsert, AggregateRevision: 9, AccessRevision: 9, LifecycleRevision: 9}, ReasonStaleLifecycle},
		{"newer lifecycle revision revives", tombstoned, eventRevisions{Kind: EventKindUpsert, AggregateRevision: 10, AccessRevision: 10, LifecycleRevision: 10}, ""},
		{"aggregate is checked before lifecycle", live, eventRevisions{Kind: EventKindUpsert, AggregateRevision: 1, AccessRevision: 1, LifecycleRevision: 1}, ReasonStaleRevision},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := evaluateFence(testCase.state, testCase.event); got != testCase.want {
				t.Fatalf("evaluateFence = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestChunkingIsDeterministic(t *testing.T) {
	cfg := unitConfig()
	content := strings.Repeat("a", chunkRuneLimit) + "\n\n" + strings.Repeat("b", chunkRuneLimit) + "\n\nshort tail"
	first := chunkCount(content, cfg.Index.IndexProfile, cfg.Index.MaxChunks)
	for attempt := 0; attempt < 5; attempt++ {
		if again := chunkCount(content, cfg.Index.IndexProfile, cfg.Index.MaxChunks); again != first {
			t.Fatalf("chunk count changed between calls: %d then %d", first, again)
		}
	}
	if first != 3 {
		t.Fatalf("chunk count = %d, want 3 chunks for two full paragraphs and a tail", first)
	}
	if shorter := chunkCount("one paragraph", cfg.Index.IndexProfile, cfg.Index.MaxChunks); shorter != 1 {
		t.Fatalf("chunk count for one paragraph = %d, want 1", shorter)
	}
	if empty := chunkCount("   \n\n  ", cfg.Index.IndexProfile, cfg.Index.MaxChunks); empty != 0 {
		t.Fatalf("chunk count for empty content = %d, want 0", empty)
	}
	if capped := chunkCount(content, cfg.Index.IndexProfile, 1); capped != 1 {
		t.Fatalf("chunk count with max_chunks=1 = %d, want 1", capped)
	}
	if grew := chunkCount(content+"\n\n"+strings.Repeat("c", chunkRuneLimit), cfg.Index.IndexProfile, cfg.Index.MaxChunks); grew < first {
		t.Fatalf("adding content reduced the chunk count: %d then %d", first, grew)
	}
}

func TestCanonicalIdentifiersAreStable(t *testing.T) {
	got := canonicalIDs([]string{" B ", "a", "A", "", "c"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("canonicalIDs = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("canonicalIDs = %v, want %v", got, want)
		}
	}
	kept := uuidIDs([]string{"44444444-4444-4444-8444-444444444444", "not-a-uuid", ""})
	if len(kept) != 1 || kept[0] != "44444444-4444-4444-8444-444444444444" {
		t.Fatalf("uuidIDs = %v, want only the well-formed identifier", kept)
	}
	if empty := uuidIDs(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("uuidIDs(nil) = %#v, want a non-nil empty slice so SQL never sees NULL", empty)
	}
}

func TestAdvanceCursorValueIsMonotonic(t *testing.T) {
	cases := []struct {
		current  int64
		incoming int64
		want     int64
	}{
		{0, 1, 1},
		{5, 6, 6},
		{5, 5, 5},
		{5, 4, 5},
		{10, 0, 10},
	}
	for _, testCase := range cases {
		if got := advanceCursorValue(testCase.current, testCase.incoming); got != testCase.want {
			t.Fatalf("advanceCursorValue(%d, %d) = %d, want %d", testCase.current, testCase.incoming, got, testCase.want)
		}
	}
}

func TestNewIdentifierIsUsable(t *testing.T) {
	value, err := newIdentifier()
	if err != nil {
		t.Fatalf("newIdentifier: %v", err)
	}
	if normalized, err := normalizeIdentifier(value); err != nil || normalized != value {
		t.Fatalf("newIdentifier produced %q, which normalizes to %q (%v)", value, normalized, err)
	}
	if normalized, err := normalizeIdentifier(strings.ToUpper(strings.ReplaceAll(value, "-", ""))); err != nil || normalized != value {
		t.Fatalf("the compact spelling of %q normalized to %q (%v)", value, normalized, err)
	}
	for _, malformed := range []string{"", "abc", value + "0", value[:35], "44444444-4444-4444-8444-44444444444z"} {
		if _, err := normalizeIdentifier(malformed); err == nil {
			t.Fatalf("malformed identifier %q was accepted", malformed)
		}
	}
}

func TestBuildSnippetCentresTheMatch(t *testing.T) {
	long := strings.Repeat("padding ", 100) + "needle" + strings.Repeat(" tail", 100)
	snippet := buildSnippet("", long, "needle")
	if !strings.Contains(snippet, "needle") {
		t.Fatalf("snippet %q does not contain the match", snippet)
	}
	if len([]rune(snippet)) > snippetLength+2 {
		t.Fatalf("snippet runes = %d, want at most %d", len([]rune(snippet)), snippetLength+2)
	}
	if first := buildSnippet("", long, "needle"); first != snippet {
		t.Fatal("snippet is not deterministic")
	}
	if fallback := buildSnippet("summary only", "", "missing"); fallback != "summary only" {
		t.Fatalf("snippet fallback = %q, want the summary", fallback)
	}
	if empty := buildSnippet("", "", "anything"); empty != "" {
		t.Fatalf("snippet for empty content = %q, want empty", empty)
	}
	if collapsed := buildSnippet("", "line one\nline two", "one"); strings.Contains(collapsed, "\n") {
		t.Fatalf("snippet %q still contains a newline", collapsed)
	}
}

func TestGRPCErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		want codes.Code
	}{
		{ErrMissingDocumentID, codes.InvalidArgument},
		{ErrUnsupportedEventKind, codes.InvalidArgument},
		{ErrInvalidIdentifier, codes.InvalidArgument},
		{ErrScopeNotGranted, codes.PermissionDenied},
		{ErrEventSequenceTaken, codes.Aborted},
		{ErrRebuildNotConfirmed, codes.FailedPrecondition},
		{ErrSourceProtocol, codes.Internal},
		{ErrDatabase, codes.Unavailable},
	}
	for _, testCase := range cases {
		mapped := grpcError(testCase.err)
		if code := status.Code(mapped); code != testCase.want {
			t.Fatalf("%v mapped to %s, want %s", testCase.err, code, testCase.want)
		}
	}
	if grpcError(nil) != nil {
		t.Fatal("grpcError(nil) is not nil")
	}
	existing := status.Error(codes.NotFound, "already a status")
	if grpcError(existing) != existing {
		t.Fatal("grpcError replaced an existing status error")
	}
}

func TestRequestFromEnvelopeMapsTheSourceContract(t *testing.T) {
	envelope := &documentv1.DocumentEventEnvelope{
		Sequence:            77,
		EventId:             "11111111-1111-4111-8111-111111111111",
		Kind:                documentv1.DocumentEventKind_DOCUMENT_EVENT_KIND_UPSERT,
		DocumentId:          "22222222-2222-4222-8222-222222222222",
		VersionId:           "33333333-3333-4333-8333-333333333333",
		AggregateRevision:   4,
		ActivationRevision:  4,
		AccessRevision:      4,
		LifecycleRevision:   4,
		LifecycleStatus:     documentv1.LifecycleStatus_LIFECYCLE_STATUS_ARCHIVED,
		PublicationStatus:   documentv1.PublicationStatus_PUBLICATION_STATUS_SUPERSEDED,
		OwnerSubjectKey:     "web:user:7",
		OwnerSpaceId:        "44444444-4444-4444-8444-444444444444",
		AuthenticatedPublic: true,
		AllowedSpaceIds:     []string{"55555555-5555-4555-8555-555555555555"},
		Title:               "title",
		Summary:             "summary",
		Content:             "content",
		ContentFormat:       "markdown",
		ContentSha256:       strings.Repeat("a", 64),
		IndexProfile:        "markdown-v1",
		OccurredAt:          "2026-09-26T00:00:00Z",
	}
	request, err := requestFromEnvelope(envelope)
	if err != nil {
		t.Fatalf("requestFromEnvelope: %v", err)
	}
	if request.GetKind() != EventKindUpsert {
		t.Fatalf("kind = %q, want upsert", request.GetKind())
	}
	if request.GetSequence() != 77 || request.GetDocumentId() != envelope.GetDocumentId() {
		t.Fatalf("sequence/document = %d/%q, want 77/%q", request.GetSequence(), request.GetDocumentId(), envelope.GetDocumentId())
	}
	if request.GetLifecycleStatus() != "archived" || request.GetPublicationStatus() != "superseded" {
		t.Fatalf("statuses = %q/%q, want archived/superseded", request.GetLifecycleStatus(), request.GetPublicationStatus())
	}
	if !request.GetAuthenticatedPublic() || len(request.GetAllowedSpaceIds()) != 1 {
		t.Fatalf("access snapshot = %v/%v, want the source snapshot", request.GetAuthenticatedPublic(), request.GetAllowedSpaceIds())
	}
	if request.GetContent() != "content" || request.GetTitle() != "title" {
		t.Fatal("the indexable projection was not copied from the envelope")
	}

	// proto messages embed a mutex, so the copy is made field by field through
	// proto.Clone instead of by value.
	deleted := proto.Clone(envelope).(*documentv1.DocumentEventEnvelope)
	deleted.Kind = documentv1.DocumentEventKind_DOCUMENT_EVENT_KIND_DELETE
	deletedRequest, err := requestFromEnvelope(deleted)
	if err != nil {
		t.Fatalf("delete envelope: %v", err)
	}
	if deletedRequest.GetKind() != EventKindDelete {
		t.Fatalf("kind = %q, want delete", deletedRequest.GetKind())
	}

	unknown := proto.Clone(envelope).(*documentv1.DocumentEventEnvelope)
	unknown.Kind = documentv1.DocumentEventKind_DOCUMENT_EVENT_KIND_UNSPECIFIED
	if _, err := requestFromEnvelope(unknown); !errors.Is(err, ErrSourceProtocol) {
		t.Fatalf("an unspecified kind returned %v, want ErrSourceProtocol", err)
	}
	if _, err := requestFromEnvelope(nil); !errors.Is(err, ErrSourceProtocol) {
		t.Fatalf("a nil envelope returned %v, want ErrSourceProtocol", err)
	}
}

func TestMintSourceAssertionCarriesTheEventReaderIdentity(t *testing.T) {
	key := []byte(strings.Repeat("boundary-key-", 4))
	codec, err := serviceauth.NewCodec(key)
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	source := config.SourceConfig{
		Endpoint:         "127.0.0.1:18081",
		CallerIdentity:   "document-search",
		Audience:         "document-service",
		Scope:            "document-event-reader",
		BatchSize:        10,
		ReconnectBackoff: "10ms",
	}
	assertion, err := mintSourceAssertion(codec, source)
	if err != nil {
		t.Fatalf("mintSourceAssertion: %v", err)
	}
	seal, err := codec.OpenAssertion(assertion, serviceauth.AudienceDocumentService)
	if err != nil {
		t.Fatalf("the assertion does not verify: %v", err)
	}
	if seal.Claims.Caller != serviceauth.CallerDocumentService {
		t.Fatalf("caller = %q, want document-service", seal.Claims.Caller)
	}
	if err := serviceauth.Requires(seal.Claims.Scopes, serviceauth.ScopeDocumentEventReader); err != nil {
		t.Fatalf("the assertion does not carry document-event-reader: %v", err)
	}
	if seal.Claims.SubjectKey != "" {
		t.Fatalf("subject = %q, want no claimed subject", seal.Claims.SubjectKey)
	}
	if seal.Claims.ExpiresAt <= time.Now().Unix() {
		t.Fatal("the assertion is already expired")
	}
	if _, err := codec.OpenAssertion(assertion, serviceauth.AudienceDocumentSearch); err == nil {
		t.Fatal("the assertion verified against the wrong audience")
	}
}
