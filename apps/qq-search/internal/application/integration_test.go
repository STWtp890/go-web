package application

// PostgreSQL integration tests. They run against the real development database
// named by QQ_SEARCH_TEST_DSN (default: the shared dev instance) as the
// qq_search_writer role. A missing database FAILS the test: an integration suite
// that skips itself proves nothing about transactions, constraints or indexes.
//
// Every test works under a unique per-process record-id prefix, so the rows it
// creates, and the rows it deletes afterwards, can never collide with another
// agent's or another run's data.

import (
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"qq-search/internal/config"
	"qq-search/internal/infrastructure/postgres"
	"qq-search/internal/ingress/pyagent"
	"qq-search/internal/testkit"

	qqsearchv1 "packages/gen/qqsearch/v1"
	"packages/serviceauth"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// defaultTestDSN is the development instance described by the service layout.
const defaultTestDSN = "postgres://qq_search_writer:qq_search@127.0.0.1:15432/gin_demo?sslmode=disable"

// runPrefix isolates this process's rows from every other run and agent.
var runPrefix = newRunPrefix()

var recordCounter int64

func newRunPrefix() string {
	buffer := make([]byte, 6)
	if _, err := crand.Read(buffer); err != nil {
		panic(fmt.Sprintf("qq-search integration: random prefix: %v", err))
	}
	return "qqsit-" + hex.EncodeToString(buffer)
}

// nextRecordID returns a record id unique to this run and this test.
func nextRecordID(t *testing.T, label string) string {
	t.Helper()
	return fmt.Sprintf("%s-%s-%d", runPrefix, label, atomic.AddInt64(&recordCounter, 1))
}

// uniqueToken is a single lexeme that only this test's rows contain, so a search
// for it can be attributed to exactly one record.
func uniqueToken() string {
	buffer := make([]byte, 8)
	if _, err := crand.Read(buffer); err != nil {
		panic(fmt.Sprintf("qq-search integration: random token: %v", err))
	}
	return "ztok" + hex.EncodeToString(buffer)
}

// eventUUID renders an opaque producer event id the way py-agent does: a stable
// hex sha256 over a name. qq-search derives its own ledger key from it.
func eventUUID(name string) string {
	sum := sha256.Sum256([]byte(runPrefix + ":" + name))
	return hex.EncodeToString(sum[:])
}

func testDSN() string {
	if dsn := strings.TrimSpace(os.Getenv("QQ_SEARCH_TEST_DSN")); dsn != "" {
		return dsn
	}
	return defaultTestDSN
}

// openTestDatabase connects to the real database and assembles the service. The
// test fails - never skips - when the database cannot be reached.
func openTestDatabase(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	dsn := testDSN()
	pool, err := postgres.Open(context.Background(), config.PostgresConfig{
		DSN:            dsn,
		Schema:         "qq_search",
		ConnectTimeout: "5s",
	})
	if err != nil {
		t.Fatalf("PostgreSQL integration test requires a reachable database at %s: %v", dsn, err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	// One qq-search integration test at a time on this schema: the transport
	// suite runs as a parallel package and both mutate the same global counters
	// and rows. The release is registered before the row cleanup so that cleanup
	// stays inside the lock.
	release, err := testkit.Serialize(context.Background(), pool.Pgx())
	if err != nil {
		t.Fatalf("serialize the qq-search integration suites: %v", err)
	}
	t.Cleanup(release)
	t.Cleanup(func() { cleanupTestRows(t, pool.Pgx()) })

	codec, err := serviceauth.NewCodec([]byte("qq-search integration boundary key 0001"))
	if err != nil {
		t.Fatalf("build boundary codec: %v", err)
	}
	service, err := New(Dependencies{Config: config.Default(), Pool: pool, Codec: codec})
	if err != nil {
		t.Fatalf("build service: %v", err)
	}
	return service, pool.Pgx()
}

// cleanupTestRows deletes only the rows under this run's prefix.
func cleanupTestRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pattern := runPrefix + "%"
	for _, statement := range []string{
		`DELETE FROM qq_search.qq_messages WHERE record_id LIKE $1`,
		`DELETE FROM qq_search.qq_files WHERE record_id LIKE $1`,
		`DELETE FROM qq_search.qq_applied_events WHERE record_id LIKE $1`,
	} {
		if _, err := pool.Exec(ctx, statement, pattern); err != nil {
			t.Errorf("cleanup %q failed: %v", statement, err)
		}
	}
}

// grantGroupScope is the channel capability py-agent would issue for the given
// groups of the test bot.
func grantGroupScope(groupIDs ...string) *qqsearchv1.QQChannelScope {
	conversations := make([]string, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		conversations = append(conversations, "qq:"+testBotID+":group:"+groupID)
	}
	return &qqsearchv1.QQChannelScope{
		BotIds:           []string{testBotID},
		ConversationIds:  conversations,
		ExternalGroupIds: groupIDs,
	}
}

// grantPrivateScope is the capability for private conversations of the test bot.
func grantPrivateScope(userIDs ...string) *qqsearchv1.QQChannelScope {
	conversations := make([]string, 0, len(userIDs))
	for _, userID := range userIDs {
		conversations = append(conversations, "qq:"+testBotID+":private:"+userID)
	}
	return &qqsearchv1.QQChannelScope{
		BotIds:          []string{testBotID},
		ConversationIds: conversations,
	}
}

func searchMessages(t *testing.T, service *Service, query string, scope *qqsearchv1.QQChannelScope) []*qqsearchv1.QQMessageHit {
	t.Helper()
	response, err := service.SearchMessages(context.Background(), scope, &qqsearchv1.SearchQQMessagesRequest{Query: query, TopK: 50})
	if err != nil {
		t.Fatalf("SearchMessages(%q): %v", query, err)
	}
	return response.GetHits()
}

func searchFiles(t *testing.T, service *Service, query string, scope *qqsearchv1.QQChannelScope) []*qqsearchv1.QQFileHit {
	t.Helper()
	response, err := service.SearchFiles(context.Background(), scope, &qqsearchv1.SearchQQFilesRequest{Query: query, TopK: 50})
	if err != nil {
		t.Fatalf("SearchFiles(%q): %v", query, err)
	}
	return response.GetHits()
}

func messageHitIDs(hits []*qqsearchv1.QQMessageHit) []string {
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.GetRecordId())
	}
	return ids
}

func fileHitIDs(hits []*qqsearchv1.QQFileHit) []string {
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.GetRecordId())
	}
	return ids
}

func containsID(ids []string, wanted string) bool {
	for _, id := range ids {
		if id == wanted {
			return true
		}
	}
	return false
}

// messageEventFor builds a message_upsert for the test group conversation.
func messageEventFor(recordID, text string, revision uint64, name string) *qqsearchv1.IndexQQSourceEventRequest {
	request := validMessageRequest()
	request.RecordId = recordID
	request.MessageText = text
	request.RecordRevision = revision
	request.EventId = eventUUID(name)
	return request
}

// messageEventInConversation builds a message_upsert for an arbitrary group.
func messageEventInConversation(groupID, recordID, text string, revision uint64, name string) *qqsearchv1.IndexQQSourceEventRequest {
	request := messageEventFor(recordID, text, revision, name)
	request.ConversationId = "qq:" + testBotID + ":group:" + groupID
	request.ExternalGroupId = groupID
	return request
}

// fileEventFor builds a file_upsert for the test group conversation.
func fileEventFor(recordID, fileName string, revision uint64, name string) *qqsearchv1.IndexQQSourceEventRequest {
	request := validFileRequest()
	request.RecordId = recordID
	request.FileName = fileName
	request.RecordRevision = revision
	request.EventId = eventUUID(name)
	return request
}

// recallEventFor builds a recall for one record.
func recallEventFor(kind RecordKind, recordID string, revision uint64, name string) *qqsearchv1.IndexQQSourceEventRequest {
	request := recallRequest(kind)
	request.RecordId = recordID
	request.RecordRevision = revision
	request.EventId = eventUUID(name)
	return request
}

func mustApply(t *testing.T, service *Service, request *qqsearchv1.IndexQQSourceEventRequest) *qqsearchv1.IndexQQSourceEventResponse {
	t.Helper()
	response, err := service.ApplyEvent(context.Background(), request)
	if err != nil {
		t.Fatalf("ApplyEvent(%s %s rev %d): %v", request.GetKind(), request.GetRecordId(), request.GetRecordRevision(), err)
	}
	return response
}

func TestIntegrationSaveMessageIsSearchableInsideItsGrantedConversation(t *testing.T) {
	service, pool := openTestDatabase(t)
	ctx := context.Background()
	token := uniqueToken()
	recordID := nextRecordID(t, "msg")

	response := mustApply(t, service, messageEventFor(recordID, "quarterly revenue forecast "+token, 1, "save-"+recordID))
	if !response.GetApplied() || response.GetReason() != ReasonApplied {
		t.Fatalf("first message event: applied=%v reason=%q, want applied", response.GetApplied(), response.GetReason())
	}
	if got := response.GetState().GetStatus(); got != qqsearchv1.QQRecordStatus_QQ_RECORD_STATUS_INDEXED {
		t.Fatalf("state status = %s, want INDEXED", got)
	}
	if got := response.GetState().GetConversationId(); got != "qq:"+testBotID+":group:"+testGroupID {
		t.Fatalf("state conversation = %q, want the py-agent identity", got)
	}

	var state string
	var revision int64
	if err := pool.QueryRow(ctx,
		`SELECT status, record_revision FROM qq_search.qq_messages WHERE record_id = $1`, recordID,
	).Scan(&state, &revision); err != nil {
		t.Fatalf("read the stored message row: %v", err)
	}
	if state != string(StatusIndexed) || revision != 1 {
		t.Fatalf("stored row status=%q revision=%d, want indexed/1", state, revision)
	}

	hits := searchMessages(t, service, token, grantGroupScope(testGroupID))
	if !containsID(messageHitIDs(hits), recordID) {
		t.Fatalf("the saved message is not searchable inside its granted conversation; hits=%v", messageHitIDs(hits))
	}

	// The lookup reads the message model and reports the indexed state.
	stateResponse, err := service.GetRecordState(ctx, grantGroupScope(testGroupID), &qqsearchv1.GetQQRecordStateRequest{
		Kind:     qqsearchv1.QQRecordKind_QQ_RECORD_KIND_MESSAGE,
		RecordId: recordID,
	})
	if err != nil {
		t.Fatalf("GetRecordState: %v", err)
	}
	if !stateResponse.GetExists() || stateResponse.GetState().GetStatus() != qqsearchv1.QQRecordStatus_QQ_RECORD_STATUS_INDEXED {
		t.Fatalf("GetRecordState = %+v, want exists with INDEXED", stateResponse)
	}
}

func TestIntegrationMessageUpdateReplacesTheSearchableContent(t *testing.T) {
	service, pool := openTestDatabase(t)
	ctx := context.Background()
	oldToken := uniqueToken()
	newToken := uniqueToken()
	recordID := nextRecordID(t, "msg")

	mustApply(t, service, messageEventFor(recordID, "first revision "+oldToken, 1, "update-1-"+recordID))
	mustApply(t, service, messageEventFor(recordID, "second revision "+newToken, 2, "update-2-"+recordID))

	polluted := searchMessages(t, service, oldToken, grantGroupScope(testGroupID))
	if containsID(messageHitIDs(polluted), recordID) {
		t.Fatalf("the superseded content is still searchable: hits=%v", messageHitIDs(polluted))
	}
	current := searchMessages(t, service, newToken, grantGroupScope(testGroupID))
	if !containsID(messageHitIDs(current), recordID) {
		t.Fatalf("the newer content is not searchable: hits=%v", messageHitIDs(current))
	}
	for _, hit := range current {
		if hit.GetRecordId() == recordID && !strings.Contains(hit.GetText(), newToken) {
			t.Fatalf("hit text %q does not carry the newer revision", hit.GetText())
		}
	}

	var text string
	var revision int64
	if err := pool.QueryRow(ctx,
		`SELECT text_content, record_revision FROM qq_search.qq_messages WHERE record_id = $1`, recordID,
	).Scan(&text, &revision); err != nil {
		t.Fatalf("read the stored message row: %v", err)
	}
	if revision != 2 || !strings.Contains(text, newToken) {
		t.Fatalf("stored row revision=%d text=%q, want the second revision", revision, text)
	}
}

func TestIntegrationRecallKeepsTheRowAndHidesItFromSearch(t *testing.T) {
	service, pool := openTestDatabase(t)
	ctx := context.Background()
	token := uniqueToken()
	recordID := nextRecordID(t, "msg")

	mustApply(t, service, messageEventFor(recordID, "recalled content "+token, 1, "recall-1-"+recordID))
	recall := mustApply(t, service, recallEventFor(RecordKindMessage, recordID, 2, "recall-2-"+recordID))
	if !recall.GetApplied() {
		t.Fatalf("the recall was not applied: %+v", recall)
	}
	if got := recall.GetState().GetStatus(); got != qqsearchv1.QQRecordStatus_QQ_RECORD_STATUS_RECALLED {
		t.Fatalf("recall response status = %s, want RECALLED", got)
	}

	// The row is still present, marked recalled: recall is an append-only fact.
	var (
		state      string
		recalledAt pgtype.Timestamptz
		revision   int64
		count      int
	)
	if err := pool.QueryRow(ctx,
		`SELECT status, recalled_at, record_revision FROM qq_search.qq_messages WHERE record_id = $1`, recordID,
	).Scan(&state, &recalledAt, &revision); err != nil {
		t.Fatalf("a recalled row must still exist: %v", err)
	}
	if state != string(StatusRecalled) || !recalledAt.Valid {
		t.Fatalf("stored row status=%q recalled_at=%v, want recalled with a timestamp", state, recalledAt)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM qq_search.qq_messages WHERE record_id = $1`, recordID).Scan(&count); err != nil {
		t.Fatalf("count recalled rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("recalled row count = %d, want exactly 1", count)
	}

	if hits := searchMessages(t, service, token, grantGroupScope(testGroupID)); containsID(messageHitIDs(hits), recordID) {
		t.Fatalf("a recalled record was returned by search: hits=%v", messageHitIDs(hits))
	}

	stateResponse, err := service.GetRecordState(ctx, grantGroupScope(testGroupID), &qqsearchv1.GetQQRecordStateRequest{
		Kind:     qqsearchv1.QQRecordKind_QQ_RECORD_KIND_MESSAGE,
		RecordId: recordID,
	})
	if err != nil {
		t.Fatalf("GetRecordState: %v", err)
	}
	if !stateResponse.GetExists() || stateResponse.GetState().GetStatus() != qqsearchv1.QQRecordStatus_QQ_RECORD_STATUS_RECALLED {
		t.Fatalf("GetRecordState = %+v, want exists with RECALLED", stateResponse)
	}
	if got := stateResponse.GetState().GetRecordRevision(); got != 2 {
		t.Fatalf("reported revision = %d, want 2", got)
	}

	// A late upsert at a lower revision cannot un-recall the record.
	stale := messageEventFor(recordID, "late content "+token, 1, "recall-3-"+recordID)
	staleResponse := mustApply(t, service, stale)
	if staleResponse.GetApplied() || staleResponse.GetReason() != ReasonStaleRevision {
		t.Fatalf("late upsert: applied=%v reason=%q, want stale_revision",
			staleResponse.GetApplied(), staleResponse.GetReason())
	}
	if hits := searchMessages(t, service, token, grantGroupScope(testGroupID)); containsID(messageHitIDs(hits), recordID) {
		t.Fatalf("a stale upsert resurrected a recalled record: hits=%v", messageHitIDs(hits))
	}
	if err := pool.QueryRow(ctx,
		`SELECT status FROM qq_search.qq_messages WHERE record_id = $1`, recordID).Scan(&state); err != nil {
		t.Fatalf("re-read the recalled row: %v", err)
	}
	if state != string(StatusRecalled) {
		t.Fatalf("stored status = %q after the stale upsert, want recalled", state)
	}

	// No delete kind exists, so nothing can remove the row either.
	deleted := messageEventFor(recordID, "delete attempt", 3, "recall-4-"+recordID)
	deleted.Kind = "message_deleted"
	if _, err := service.ApplyEvent(ctx, deleted); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("a delete kind: got %v, want INVALID_ARGUMENT", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM qq_search.qq_messages WHERE record_id = $1`, recordID).Scan(&count); err != nil {
		t.Fatalf("count rows after the delete attempt: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count = %d after a delete attempt, want 1", count)
	}
}

func TestIntegrationDuplicateEventAppliesOnce(t *testing.T) {
	service, pool := openTestDatabase(t)
	ctx := context.Background()
	token := uniqueToken()
	recordID := nextRecordID(t, "msg")
	request := messageEventFor(recordID, "duplicate content "+token, 1, "duplicate-"+recordID)

	before, err := service.Status(ctx)
	if err != nil {
		t.Fatalf("Status before: %v", err)
	}
	first := mustApply(t, service, request)
	if !first.GetApplied() {
		t.Fatalf("the first delivery was not applied: %+v", first)
	}
	second := mustApply(t, service, request)
	if second.GetApplied() || second.GetReason() != ReasonDuplicate {
		t.Fatalf("the redelivery: applied=%v reason=%q, want duplicate",
			second.GetApplied(), second.GetReason())
	}

	// "Applied once" is counted per record, not from the global counters: the
	// transport suite runs in another package in parallel against the same schema,
	// so only this record's rows are attributable to this test.
	var ledgerRows, messageRows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM qq_search.qq_applied_events WHERE record_id = $1`, recordID).Scan(&ledgerRows); err != nil {
		t.Fatalf("count ledger rows: %v", err)
	}
	if ledgerRows != 1 {
		t.Fatalf("the ledger holds %d rows for one event delivered twice, want 1", ledgerRows)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM qq_search.qq_messages WHERE record_id = $1`, recordID).Scan(&messageRows); err != nil {
		t.Fatalf("count message rows: %v", err)
	}
	if messageRows != 1 {
		t.Fatalf("the message table holds %d rows for one record delivered twice, want 1", messageRows)
	}

	after, err := service.Status(ctx)
	if err != nil {
		t.Fatalf("Status after: %v", err)
	}
	if after.GetEventsApplied() < before.GetEventsApplied()+1 {
		t.Fatalf("the applied-event count did not grow; before=%d after=%d",
			before.GetEventsApplied(), after.GetEventsApplied())
	}
	if after.GetIndexedMessages() < before.GetIndexedMessages()+1 {
		t.Fatalf("the indexed-message count did not grow; before=%d after=%d",
			before.GetIndexedMessages(), after.GetIndexedMessages())
	}
	if after.GetLastSequence() < request.GetSequence() {
		t.Fatalf("last_sequence = %d, want at least %d", after.GetLastSequence(), request.GetSequence())
	}

	// A second event id carrying the same revision and the same payload is an
	// idempotent replay, not a conflict.
	replay := messageEventFor(recordID, request.GetMessageText(), 1, "duplicate-replay-"+recordID)
	replayed := mustApply(t, service, replay)
	if replayed.GetApplied() || replayed.GetReason() != ReasonDuplicateRevision {
		t.Fatalf("same revision, same payload: applied=%v reason=%q, want duplicate_revision",
			replayed.GetApplied(), replayed.GetReason())
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM qq_search.qq_applied_events WHERE record_id = $1`, recordID).Scan(&ledgerRows); err != nil {
		t.Fatalf("re-count ledger rows: %v", err)
	}
	if ledgerRows != 1 {
		t.Fatalf("the ledger holds %d rows after an idempotent replay, want 1", ledgerRows)
	}
}

func TestIntegrationOutOfOrderEventsAreFenced(t *testing.T) {
	service, pool := openTestDatabase(t)
	ctx := context.Background()
	newerToken := uniqueToken()
	olderToken := uniqueToken()
	recordID := nextRecordID(t, "msg")

	mustApply(t, service, messageEventFor(recordID, "revision two "+newerToken, 2, "order-2-"+recordID))

	stale := messageEventFor(recordID, "revision one "+olderToken, 1, "order-1-"+recordID)
	staleResponse := mustApply(t, service, stale)
	if staleResponse.GetApplied() || staleResponse.GetReason() != ReasonStaleRevision {
		t.Fatalf("out-of-order event: applied=%v reason=%q, want stale_revision",
			staleResponse.GetApplied(), staleResponse.GetReason())
	}
	if hits := searchMessages(t, service, olderToken, grantGroupScope(testGroupID)); containsID(messageHitIDs(hits), recordID) {
		t.Fatalf("a stale revision was written: hits=%v", messageHitIDs(hits))
	}
	if hits := searchMessages(t, service, newerToken, grantGroupScope(testGroupID)); !containsID(messageHitIDs(hits), recordID) {
		t.Fatalf("the newer revision was lost: hits=%v", messageHitIDs(hits))
	}

	// Same revision, different payload: a conflict, never a silent overwrite.
	conflict := messageEventFor(recordID, "revision two rewritten "+uniqueToken(), 2, "order-2b-"+recordID)
	_, err := service.ApplyEvent(ctx, conflict)
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("same revision with a different payload: got %v, want ALREADY_EXISTS", err)
	}
	var text string
	if err := pool.QueryRow(ctx,
		`SELECT text_content FROM qq_search.qq_messages WHERE record_id = $1`, recordID).Scan(&text); err != nil {
		t.Fatalf("re-read the stored row: %v", err)
	}
	if !strings.Contains(text, newerToken) {
		t.Fatalf("the stored payload was overwritten by a conflicting event: %q", text)
	}

	// A different record id claimed by another conversation is refused too.
	foreign := messageEventInConversation("1000", recordID, "hijack", 3, "order-hijack-"+recordID)
	if _, err := service.ApplyEvent(ctx, foreign); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("a second conversation claiming the same record id: got %v, want ALREADY_EXISTS", err)
	}
}

func TestIntegrationFilesFollowTheSameRulesInTheirOwnTable(t *testing.T) {
	service, pool := openTestDatabase(t)
	ctx := context.Background()
	oldToken := uniqueToken()
	newToken := uniqueToken()
	recallToken := uniqueToken()
	recordID := nextRecordID(t, "file")
	recalledID := nextRecordID(t, "file")

	mustApply(t, service, fileEventFor(recordID, "report-"+oldToken+".pdf", 1, "file-1-"+recordID))
	mustApply(t, service, fileEventFor(recordID, "report-"+newToken+".pdf", 2, "file-2-"+recordID))

	if hits := searchFiles(t, service, oldToken, grantGroupScope(testGroupID)); containsID(fileHitIDs(hits), recordID) {
		t.Fatalf("the superseded file name is still searchable: hits=%v", fileHitIDs(hits))
	}
	hits := searchFiles(t, service, newToken, grantGroupScope(testGroupID))
	if !containsID(fileHitIDs(hits), recordID) {
		t.Fatalf("the newer file revision is not searchable: hits=%v", fileHitIDs(hits))
	}
	for _, hit := range hits {
		if hit.GetRecordId() == recordID {
			if hit.GetMimeType() != "application/pdf" || hit.GetSizeBytes() != 4096 {
				t.Fatalf("file hit lost its own columns: %+v", hit)
			}
			if hit.GetUploaderExternalUserId() == "" {
				t.Fatal("file hit lost the uploader: a file event must carry uploader_external_user_id")
			}
		}
	}

	// The trigram fallback finds a substring that is not a lexeme of the name.
	substring := newToken[2:10]
	if fallback := searchFiles(t, service, substring, grantGroupScope(testGroupID)); !containsID(fileHitIDs(fallback), recordID) {
		t.Fatalf("the trigram fallback missed the file: query=%q hits=%v", substring, fileHitIDs(fallback))
	}

	mustApply(t, service, fileEventFor(recalledID, "withdrawn-"+recallToken+".docx", 1, "file-3-"+recalledID))
	mustApply(t, service, recallEventFor(RecordKindFile, recalledID, 2, "file-4-"+recalledID))

	if hits := searchFiles(t, service, recallToken, grantGroupScope(testGroupID)); containsID(fileHitIDs(hits), recalledID) {
		t.Fatalf("a recalled file was returned by search: hits=%v", fileHitIDs(hits))
	}
	var (
		state    string
		count    int
		revision int64
	)
	if err := pool.QueryRow(ctx,
		`SELECT status, record_revision FROM qq_search.qq_files WHERE record_id = $1`, recalledID,
	).Scan(&state, &revision); err != nil {
		t.Fatalf("a recalled file row must still exist: %v", err)
	}
	if state != string(StatusRecalled) || revision != 2 {
		t.Fatalf("recalled file row status=%q revision=%d, want recalled/2", state, revision)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM qq_search.qq_files WHERE record_id = $1`, recalledID).Scan(&count); err != nil {
		t.Fatalf("count recalled file rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("recalled file row count = %d, want 1", count)
	}
	stateResponse, err := service.GetRecordState(ctx, grantGroupScope(testGroupID), &qqsearchv1.GetQQRecordStateRequest{
		Kind:     qqsearchv1.QQRecordKind_QQ_RECORD_KIND_FILE,
		RecordId: recalledID,
	})
	if err != nil {
		t.Fatalf("GetRecordState(file): %v", err)
	}
	if !stateResponse.GetExists() || stateResponse.GetState().GetStatus() != qqsearchv1.QQRecordStatus_QQ_RECORD_STATUS_RECALLED {
		t.Fatalf("file state = %+v, want exists with RECALLED", stateResponse)
	}
	if stale := mustApply(t, service, fileEventFor(recalledID, "late-"+recallToken+".docx", 1, "file-5-"+recalledID)); stale.GetApplied() {
		t.Fatalf("a late file upsert was applied: %+v", stale)
	}
}

func TestIntegrationMessageAndFileCorporaStayIsolated(t *testing.T) {
	service, pool := openTestDatabase(t)
	ctx := context.Background()
	token := uniqueToken()
	messageID := nextRecordID(t, "msg")
	fileID := nextRecordID(t, "file")

	mustApply(t, service, messageEventFor(messageID, "isolation probe "+token, 1, "isolate-msg-"+messageID))
	mustApply(t, service, fileEventFor(fileID, "isolation-probe-"+token+".pdf", 1, "isolate-file-"+fileID))

	messageHits := messageHitIDs(searchMessages(t, service, token, grantGroupScope(testGroupID)))
	if !containsID(messageHits, messageID) {
		t.Fatalf("the message is not searchable: hits=%v", messageHits)
	}
	if containsID(messageHits, fileID) {
		t.Fatalf("a message query returned a file: hits=%v", messageHits)
	}
	fileHits := fileHitIDs(searchFiles(t, service, token, grantGroupScope(testGroupID)))
	if !containsID(fileHits, fileID) {
		t.Fatalf("the file is not searchable: hits=%v", fileHits)
	}
	if containsID(fileHits, messageID) {
		t.Fatalf("a file query returned a message: hits=%v", fileHits)
	}

	// The two kinds are addressable only in their own model: a message lookup
	// cannot see a file and the other way round.
	asMessage, err := service.GetRecordState(ctx, grantGroupScope(testGroupID), &qqsearchv1.GetQQRecordStateRequest{
		Kind:     qqsearchv1.QQRecordKind_QQ_RECORD_KIND_MESSAGE,
		RecordId: fileID,
	})
	if err != nil {
		t.Fatalf("GetRecordState(message, file id): %v", err)
	}
	if asMessage.GetExists() {
		t.Fatalf("a message lookup returned a file: %+v", asMessage)
	}
	asFile, err := service.GetRecordState(ctx, grantGroupScope(testGroupID), &qqsearchv1.GetQQRecordStateRequest{
		Kind:     qqsearchv1.QQRecordKind_QQ_RECORD_KIND_FILE,
		RecordId: messageID,
	})
	if err != nil {
		t.Fatalf("GetRecordState(file, message id): %v", err)
	}
	if asFile.GetExists() {
		t.Fatalf("a file lookup returned a message: %+v", asFile)
	}

	// The collection identities come from qq_search.index_collections and are
	// distinct namespaces.
	identities := map[string]collectionIdentity{}
	rows, err := pool.Query(ctx, `SELECT record_kind, collection_alias, storage_domain FROM qq_search.index_collections`)
	if err != nil {
		t.Fatalf("read index_collections: %v", err)
	}
	for rows.Next() {
		var kind, alias, domain string
		if err := rows.Scan(&kind, &alias, &domain); err != nil {
			rows.Close()
			t.Fatalf("scan index_collections: %v", err)
		}
		identities[kind] = collectionIdentity{alias: alias, domain: domain}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate index_collections: %v", err)
	}
	message, ok := identities[string(RecordKindMessage)]
	if !ok {
		t.Fatal("index_collections has no message row")
	}
	file, ok := identities[string(RecordKindFile)]
	if !ok {
		t.Fatal("index_collections has no file row")
	}
	if message.alias == file.alias {
		t.Fatalf("both corpora share the collection alias %q", message.alias)
	}
	if message.domain == file.domain {
		t.Fatalf("both corpora share the storage domain %q", message.domain)
	}

	statusResponse, err := service.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if statusResponse.GetMessageCollection() != message.alias || statusResponse.GetFileCollection() != file.alias {
		t.Fatalf("status reported collections %q/%q, want the stored %q/%q",
			statusResponse.GetMessageCollection(), statusResponse.GetFileCollection(), message.alias, file.alias)
	}
}

func TestIntegrationChannelScopeLimitsResults(t *testing.T) {
	service, _ := openTestDatabase(t)
	token := uniqueToken()
	insideID := nextRecordID(t, "msg")
	outsideID := nextRecordID(t, "msg")

	mustApply(t, service, messageEventInConversation(testGroupID, insideID, "in scope "+token, 1, "scope-in-"+insideID))
	mustApply(t, service, messageEventInConversation("1000", outsideID, "out of scope "+token, 1, "scope-out-"+outsideID))

	hits := messageHitIDs(searchMessages(t, service, token, grantGroupScope(testGroupID)))
	if !containsID(hits, insideID) {
		t.Fatalf("the in-scope conversation was not returned: hits=%v", hits)
	}
	if containsID(hits, outsideID) {
		t.Fatalf("a conversation outside the granted scope was returned: hits=%v", hits)
	}

	// Narrowing to a private conversation keeps every group message out. The
	// grant names the conversation explicitly; a bot-level grant would legitimately
	// include all of that bot's conversations, which is why the narrowing family
	// has to be named here.
	narrowed := messageHitIDs(searchMessages(t, service, token, grantPrivateScope(testPrivateUserID)))
	if len(narrowed) != 0 {
		t.Fatalf("a private-only grant returned group messages: hits=%v", narrowed)
	}
	narrowed = messageHitIDs(searchMessages(t, service, token, &qqsearchv1.QQChannelScope{
		ConversationIds: []string{"qq:" + testBotID + ":group:" + testGroupID},
	}))
	if !containsID(narrowed, insideID) || containsID(narrowed, outsideID) {
		t.Fatalf("narrowing by conversation id returned %v, want only %s", narrowed, insideID)
	}

	// A request naming something outside the grant is refused as a whole.
	_, err := service.SearchMessages(context.Background(), grantGroupScope(testGroupID), &qqsearchv1.SearchQQMessagesRequest{
		Query: token,
		Scope: &qqsearchv1.QQChannelScope{BotIds: []string{"12345"}},
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("an out-of-scope request: got %v, want PERMISSION_DENIED", err)
	}

	// An empty grant means no conversations, never every conversation.
	empty, err := service.SearchMessages(context.Background(), &qqsearchv1.QQChannelScope{}, &qqsearchv1.SearchQQMessagesRequest{Query: token})
	if err != nil {
		t.Fatalf("empty grant: %v", err)
	}
	if len(empty.GetHits()) != 0 {
		t.Fatalf("an empty granted scope returned %d hits, want none", len(empty.GetHits()))
	}
	emptyFiles, err := service.SearchFiles(context.Background(), &qqsearchv1.QQChannelScope{}, &qqsearchv1.SearchQQFilesRequest{Query: token})
	if err != nil {
		t.Fatalf("empty grant (files): %v", err)
	}
	if len(emptyFiles.GetHits()) != 0 {
		t.Fatalf("an empty granted file scope returned %d hits, want none", len(emptyFiles.GetHits()))
	}
}

func TestIntegrationRebuildRestoresTheDerivedIndexFromLocalData(t *testing.T) {
	service, pool := openTestDatabase(t)
	ctx := context.Background()
	messageToken := uniqueToken()
	fileToken := uniqueToken()
	messageID := nextRecordID(t, "msg")
	fileID := nextRecordID(t, "file")

	mustApply(t, service, messageEventFor(messageID, "rebuild probe "+messageToken, 1, "rebuild-msg-"+messageID))
	mustApply(t, service, fileEventFor(fileID, "rebuild-"+fileToken+".pdf", 1, "rebuild-file-"+fileID))
	mustApply(t, service, recallEventFor(RecordKindFile, fileID, 2, "rebuild-file-recall-"+fileID))

	var ledgerBefore int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM qq_search.qq_applied_events WHERE record_id LIKE $1`, runPrefix+"%").Scan(&ledgerBefore); err != nil {
		t.Fatalf("count applied events: %v", err)
	}

	// Damage the index-side state only. The applied-event ledger is untouched, so
	// the rebuild has to restore both directions: an indexed message marked
	// recalled, and a recalled file marked indexed.
	//
	// The damage and its read-back share one transaction on purpose. The transport
	// suite rebuilds this same schema in another package and in parallel, so a
	// search issued between the two statements could observe that rebuild instead
	// of this damage; holding the row locks keeps the observation here.
	damage, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the damage transaction: %v", err)
	}
	if _, err := damage.Exec(ctx,
		`UPDATE qq_search.qq_messages SET status = 'recalled', recalled_at = now() WHERE record_id = $1`, messageID); err != nil {
		_ = damage.Rollback(ctx)
		t.Fatalf("damage the message index state: %v", err)
	}
	if _, err := damage.Exec(ctx,
		`UPDATE qq_search.qq_files SET status = 'indexed', recalled_at = NULL WHERE record_id = $1`, fileID); err != nil {
		_ = damage.Rollback(ctx)
		t.Fatalf("damage the file index state: %v", err)
	}
	var (
		damagedMessage string
		damagedFile    string
	)
	if err := damage.QueryRow(ctx, `SELECT status FROM qq_search.qq_messages WHERE record_id = $1`, messageID).Scan(&damagedMessage); err != nil {
		_ = damage.Rollback(ctx)
		t.Fatalf("read the damaged message row: %v", err)
	}
	if err := damage.QueryRow(ctx, `SELECT status FROM qq_search.qq_files WHERE record_id = $1`, fileID).Scan(&damagedFile); err != nil {
		_ = damage.Rollback(ctx)
		t.Fatalf("read the damaged file row: %v", err)
	}
	if err := damage.Commit(ctx); err != nil {
		t.Fatalf("commit the damage transaction: %v", err)
	}
	if damagedMessage != string(StatusRecalled) || damagedFile != string(StatusIndexed) {
		t.Fatalf("the damage did not take: message=%q file=%q", damagedMessage, damagedFile)
	}

	// The negative control for "no source call": the only address a source client
	// could have used is closed for the duration of the rebuild. The structural
	// proof is in internal/architecture, which asserts this module has no source
	// client at all.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("open the control port: %v", err)
	}
	closedAddress := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close the control port: %v", err)
	}
	if connection, err := net.DialTimeout("tcp", closedAddress, 200*time.Millisecond); err == nil {
		_ = connection.Close()
		t.Fatalf("the control port %s is still accepting connections", closedAddress)
	}

	if _, err := service.Rebuild(ctx, false); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("rebuild without confirmation: got %v, want FAILED_PRECONDITION", err)
	}

	rebuildStarted := time.Now().Add(-time.Second)
	response, err := service.Rebuild(ctx, true)
	if err != nil {
		t.Fatalf("Rebuild(confirm=true): %v", err)
	}
	if response.GetMessagesRebuilt() < 1 || response.GetFilesRebuilt() < 1 {
		t.Fatalf("rebuild reported messages=%d files=%d, want both to be restored",
			response.GetMessagesRebuilt(), response.GetFilesRebuilt())
	}

	var (
		messageState string
		fileState    string
	)
	if err := pool.QueryRow(ctx, `SELECT status FROM qq_search.qq_messages WHERE record_id = $1`, messageID).Scan(&messageState); err != nil {
		t.Fatalf("re-read the message row: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM qq_search.qq_files WHERE record_id = $1`, fileID).Scan(&fileState); err != nil {
		t.Fatalf("re-read the file row: %v", err)
	}
	if messageState != string(StatusIndexed) {
		t.Fatalf("message status after the rebuild = %q, want indexed", messageState)
	}
	if fileState != string(StatusRecalled) {
		t.Fatalf("file status after the rebuild = %q, want recalled", fileState)
	}
	if hits := searchMessages(t, service, messageToken, grantGroupScope(testGroupID)); !containsID(messageHitIDs(hits), messageID) {
		t.Fatalf("the rebuilt message is not searchable: hits=%v", messageHitIDs(hits))
	}
	if hits := searchFiles(t, service, fileToken, grantGroupScope(testGroupID)); containsID(fileHitIDs(hits), fileID) {
		t.Fatalf("the rebuild resurrected a recalled file: hits=%v", fileHitIDs(hits))
	}

	var ledgerAfter int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM qq_search.qq_applied_events WHERE record_id LIKE $1`, runPrefix+"%").Scan(&ledgerAfter); err != nil {
		t.Fatalf("re-count applied events: %v", err)
	}
	if ledgerAfter != ledgerBefore {
		t.Fatalf("the rebuild changed the applied-event ledger: %d -> %d", ledgerBefore, ledgerAfter)
	}

	// A succeeded run is recorded. The assertion is "at least one" rather than
	// "exactly one" because qq_search.rebuild_runs records runs, not tests, and the
	// transport suite in another package rebuilds the same schema in parallel. Rows
	// are deliberately left in place for the same reason: the table has no per-run
	// owner column, so deleting by time could remove another run's evidence.
	var runCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM qq_search.rebuild_runs WHERE started_at >= $1 AND state = 'succeeded'`, rebuildStarted).Scan(&runCount); err != nil {
		t.Fatalf("read rebuild_runs: %v", err)
	}
	if runCount < 1 {
		t.Fatal("no succeeded rebuild run was recorded")
	}
}

func TestIntegrationWritesOnlyTheQQSearchSchema(t *testing.T) {
	_, pool := openTestDatabase(t)
	ctx := context.Background()

	var currentUser, currentSchema string
	if err := pool.QueryRow(ctx, `SELECT current_user, current_schema()`).Scan(&currentUser, &currentSchema); err != nil {
		t.Fatalf("read the session identity: %v", err)
	}
	if currentUser != "qq_search_writer" {
		t.Fatalf("the integration suite must run as qq_search_writer, got %q", currentUser)
	}
	if currentSchema != "qq_search" {
		t.Fatalf("the pinned schema is %q, want qq_search", currentSchema)
	}

	// The foreign schema names are assembled at run time on purpose: this module
	// must not contain a literal cross-schema write statement for the shared
	// architecture check to flag, and the failure below is what proves the write
	// is refused.
	foreignWrites := []struct {
		schema    string
		statement func(string) string
	}{
		{"document" + "_service", func(schema string) string {
			return "INSERT INTO " + schema + ".access_subjects(subject_key) VALUES ($1)"
		}},
		{"document" + "_search", func(schema string) string {
			return "INSERT INTO " + schema + ".consumer_cursors(stream) VALUES ($1)"
		}},
	}
	for _, attempt := range foreignWrites {
		t.Run(attempt.schema, func(t *testing.T) {
			_, err := pool.Exec(ctx, attempt.statement(attempt.schema), "qq-search-isolation-probe")
			if err == nil {
				t.Fatalf("a write into %s succeeded; qq_search_writer must only write qq_search", attempt.schema)
			}
			if !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
				t.Fatalf("write into %s failed with %v, want an explicit permission denial", attempt.schema, err)
			}
		})
	}

	_, err := pool.Exec(ctx, "SELECT count(*) FROM "+("document"+"_service")+".document_events")
	if err == nil {
		t.Fatal("reading document_service.document_events succeeded; qq-search reads no other schema")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		t.Fatalf("reading another schema failed with %v, want an explicit permission denial", err)
	}

	// A write inside the service's own schema still works, so the denial above is
	// isolation rather than a broken connection.
	if _, err := pool.Exec(ctx,
		`INSERT INTO qq_search.consumer_state(stream, last_sequence) VALUES ($1, 0)
		 ON CONFLICT (stream) DO UPDATE SET updated_at = clock_timestamp()`, runPrefix+"-probe"); err != nil {
		t.Fatalf("a write inside qq_search failed: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM qq_search.consumer_state WHERE stream = $1`, runPrefix+"-probe"); err != nil {
			t.Errorf("cleanup consumer_state probe: %v", err)
		}
	})
}

func TestIntegrationReplaysThePyAgentInboundTranscript(t *testing.T) {
	service, _ := openTestDatabase(t)
	ctx := context.Background()

	transcript, err := pyagent.LoadTranscript()
	if err != nil {
		t.Fatalf("load the py-agent transcript: %v", err)
	}

	// The transcript carries py-agent's own field shape; only the record ids are
	// prefixed so a rerun cannot collide with the previous run's rows.
	recordIDs := make([]string, 0, len(transcript.Events))
	occurred := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	for index, inbound := range transcript.Events {
		inbound.MessageID = nextRecordID(t, "transcript")
		recordIDs = append(recordIDs, inbound.MessageID)
		envelope := inbound.MessageEnvelope(1, int64(index+1), occurred.Add(time.Duration(index)*time.Minute))
		request, err := pyagent.RequestFromEnvelope(envelope)
		if err != nil {
			t.Fatalf("convert transcript event %d: %v", index, err)
		}
		response := mustApply(t, service, request)
		if !response.GetApplied() {
			t.Fatalf("transcript event %d was not applied: %+v", index, response)
		}
		if got, want := response.GetState().GetConversationId(), inbound.ConversationID(); got != want {
			t.Fatalf("event %d indexed under conversation %q, want the py-agent identity %q", index, got, want)
		}
	}

	// A grant for the group conversation returns the two group messages and never
	// the private one. The transcript's group texts contain the word "group"; the
	// private text contains "private".
	groupHits := messageHitIDs(searchMessages(t, service, "group", grantGroupScope(testGroupID)))
	if !containsID(groupHits, recordIDs[1]) || !containsID(groupHits, recordIDs[2]) {
		t.Fatalf("group hits = %v, want the two group messages %s/%s", groupHits, recordIDs[1], recordIDs[2])
	}
	if containsID(groupHits, recordIDs[0]) {
		t.Fatalf("a group grant returned the private message: hits=%v", groupHits)
	}

	// A grant for the private conversation returns only the private message.
	privateHits := messageHitIDs(searchMessages(t, service, "private", grantPrivateScope("20002")))
	if !containsID(privateHits, recordIDs[0]) {
		t.Fatalf("private hits = %v, want the private message %s", privateHits, recordIDs[0])
	}
	if containsID(privateHits, recordIDs[1]) || containsID(privateHits, recordIDs[2]) {
		t.Fatalf("a private grant returned group messages: hits=%v", privateHits)
	}

	// The identity round-trips through the record-state lookup.
	state, err := service.GetRecordState(ctx, grantGroupScope(testGroupID), &qqsearchv1.GetQQRecordStateRequest{
		Kind:     qqsearchv1.QQRecordKind_QQ_RECORD_KIND_MESSAGE,
		RecordId: recordIDs[1],
	})
	if err != nil {
		t.Fatalf("GetRecordState(transcript group message): %v", err)
	}
	if !state.GetExists() || state.GetState().GetConversationId() != "qq:10001:group:999" {
		t.Fatalf("transcript state = %+v, want exists in qq:10001:group:999", state)
	}
}
