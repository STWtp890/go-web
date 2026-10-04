package application

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"document-search/internal/config"
	"document-search/internal/dbtest"

	documentv1 "packages/gen/document/v1"
	documentsearchv1 "packages/gen/documentsearch/v1"

	"google.golang.org/grpc"
)

// This file proves the consumer contract against the real database with a real
// gRPC server standing in for document-service: the cursor advances in the same
// transaction as the index change, a crash between batches is survivable, and a
// restart resumes at the durable cursor instead of replaying from the beginning
// or skipping ahead.

// fakeDocumentService serves ListDocumentEvents from a fixed event list. It
// stops after a total delivery budget and keeps the stream open, which is what
// lets the test freeze the consumer mid-batch deterministically.
//
// The budget is global rather than per connection on purpose: a consumer that
// reconnects (because the test stopped it, or because a stream ended) must not
// be able to pull more events than the test intended to freeze at.
type fakeDocumentService struct {
	documentv1.UnimplementedDocumentServiceServer

	mu             sync.Mutex
	envelopes      []*documentv1.DocumentEventEnvelope
	afterSequences []int64
	budget         int64
	delivered      int64
}

func (fake *fakeDocumentService) ListDocumentEvents(
	request *documentv1.ListDocumentEventsRequest,
	stream grpc.ServerStreamingServer[documentv1.DocumentEventEnvelope],
) error {
	fake.mu.Lock()
	fake.afterSequences = append(fake.afterSequences, request.GetAfterSequence())
	envelopes := fake.envelopes
	fake.mu.Unlock()

	for _, envelope := range envelopes {
		if envelope.GetSequence() <= request.GetAfterSequence() {
			continue
		}
		if !fake.claimDelivery() {
			break
		}
		if err := stream.Send(envelope); err != nil {
			return err
		}
	}
	if !request.GetFollow() {
		return nil
	}
	// Follow mode: stay open until the consumer goes away, exactly like a real
	// long-lived Outbox stream.
	<-stream.Context().Done()
	return nil
}

// claimDelivery reserves one delivery from the total budget.
func (fake *fakeDocumentService) claimDelivery() bool {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.budget > 0 && fake.delivered >= fake.budget {
		return false
	}
	fake.delivered++
	return true
}

func (fake *fakeDocumentService) setBudget(budget int64) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.budget = budget
}

// resetConnections forgets the connections seen so far, so one phase's
// assertions (the restart) are not diluted by an earlier phase.
func (fake *fakeDocumentService) resetConnections() {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.afterSequences = nil
}

func (fake *fakeDocumentService) requestedCursors() []int64 {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]int64(nil), fake.afterSequences...)
}

// deliveredCount reports how many events the stand-in has handed out.
func (fake *fakeDocumentService) deliveredCount() int64 {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.delivered
}

// startFakeSource starts a document-service stand-in on a real loopback port and
// returns its address.
func startFakeSource(t *testing.T, fake *fakeDocumentService) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("document-search integration: listen for the fake source: %v", err)
	}
	server := grpc.NewServer()
	documentv1.RegisterDocumentServiceServer(server, fake)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}

// buildEnvelopes turns the fixture's events into source envelopes.
func buildEnvelopes(requests ...*documentsearchv1.IndexDocumentEventRequest) []*documentv1.DocumentEventEnvelope {
	envelopes := make([]*documentv1.DocumentEventEnvelope, 0, len(requests))
	for _, request := range requests {
		envelopes = append(envelopes, &documentv1.DocumentEventEnvelope{
			Sequence:            request.GetSequence(),
			EventId:             request.GetEventId(),
			Kind:                documentv1.DocumentEventKind_DOCUMENT_EVENT_KIND_UPSERT,
			DocumentId:          request.GetDocumentId(),
			VersionId:           request.GetVersionId(),
			AggregateRevision:   request.GetAggregateRevision(),
			ActivationRevision:  request.GetActivationRevision(),
			AccessRevision:      request.GetAccessRevision(),
			LifecycleRevision:   request.GetLifecycleRevision(),
			LifecycleStatus:     documentv1.LifecycleStatus_LIFECYCLE_STATUS_ACTIVE,
			PublicationStatus:   documentv1.PublicationStatus_PUBLICATION_STATUS_PUBLISHED,
			OwnerSubjectKey:     request.GetOwnerSubjectKey(),
			OwnerSpaceId:        request.GetOwnerSpaceId(),
			AuthenticatedPublic: request.GetAuthenticatedPublic(),
			AllowedSpaceIds:     request.GetAllowedSpaceIds(),
			Title:               request.GetTitle(),
			Summary:             request.GetSummary(),
			Content:             request.GetContent(),
			ContentFormat:       request.GetContentFormat(),
			IndexProfile:        request.GetIndexProfile(),
			OccurredAt:          request.GetOccurredAt(),
			CreatedAt:           request.GetCreatedAt(),
		})
	}
	return envelopes
}

// newSourceConsumer assembles a consumer whose source is the stand-in at
// endpoint, with a real boundary key file exactly as the process would use.
func newSourceConsumer(t *testing.T, fixture *testFixture, endpoint string) *Consumer {
	t.Helper()
	cfg := config.Default()
	cfg.Postgres.DSN = dbtest.DSN()
	cfg.Source.Endpoint = endpoint
	cfg.Source.CallerIdentity = "document-search"
	cfg.Source.Audience = "document-service"
	cfg.Source.Scope = "document-event-reader"
	cfg.Source.BatchSize = 2
	cfg.Source.ReconnectBackoff = "10ms"
	cfg.Source.PollInterval = "10ms"
	cfg.Source.RequestTimeout = "2s"
	keyPath := filepath.Join(t.TempDir(), "source.key")
	if err := os.WriteFile(keyPath, testServiceKey, 0o600); err != nil {
		t.Fatalf("write the source boundary key: %v", err)
	}
	cfg.Source.CapabilityKeyFile = keyPath
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	consumer, err := NewConsumer(ConsumerDependencies{
		Config:      cfg,
		Pool:        fixture.pool,
		Logger:      logger,
		VectorIndex: fixture.vectors,
		Stream:      fixture.stream,
	})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	return consumer
}

func TestIntegrationConsumerResumesWithoutSkippingOrReapplying(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	token := "consumertoken" + dbtest.RunTag()

	requests := make([]*documentsearchv1.IndexDocumentEventRequest, 0, 6)
	documentIDs := make([]string, 0, 6)
	for index := 0; index < 6; index++ {
		documentID := fixture.newDocument()
		documentIDs = append(documentIDs, documentID)
		requests = append(requests, fixture.upsertRequest(documentSpec{
			DocumentID:      documentID,
			OwnerSpaceID:    space,
			Title:           "Consumed " + token,
			Content:         "body " + token,
			AllowedSpaceIDs: []string{space},
		}))
	}

	fake := &fakeDocumentService{envelopes: buildEnvelopes(requests...), budget: 2}
	endpoint := startFakeSource(t, fake)

	// First run: consume the first batch, then stop as if the process died. The
	// wait covers the durable cursor too: the crash point is only meaningful once
	// the cursor of the last applied event is committed.
	firstConsumer := newSourceConsumer(t, fixture, endpoint)
	firstContext, stopFirst := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() { firstDone <- firstConsumer.Run(firstContext) }()

	waitFor(t, 10*time.Second, "the first batch and its cursor to commit", func() bool {
		return fixture.appliedEventCount(documentIDs) >= 2 && fixture.cursor() == requests[1].GetSequence()
	})
	stopFirst()
	select {
	case err := <-firstDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("the stopped consumer returned %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the consumer did not stop after its context was cancelled")
	}

	appliedAtCrash := fixture.appliedEventCount(documentIDs)
	if appliedAtCrash != 2 {
		t.Fatalf("applied events at the crash = %d, want the batch size 2", appliedAtCrash)
	}
	cursorAtCrash := fixture.cursor()
	if cursorAtCrash != requests[appliedAtCrash-1].GetSequence() {
		t.Fatalf("cursor at the crash = %d, want the last applied sequence %d",
			cursorAtCrash, requests[appliedAtCrash-1].GetSequence())
	}
	// The cursor and the applied events advanced together: two events in the log,
	// and the cursor is exactly the second event's sequence.
	if rows := fixture.countRows(`SELECT count(*) FROM document_search.document_index_events WHERE document_id = ANY($1::text[]::uuid[])`,
		documentIDs); rows != appliedAtCrash {
		t.Fatalf("applied event rows = %d, want %d", rows, appliedAtCrash)
	}
	stateOfSecond, err := fixture.service.GetDocumentIndexState(fixture.ctx, documentIDs[1])
	if err != nil {
		t.Fatalf("GetDocumentIndexState: %v", err)
	}

	// Second run: the same durable state must carry the consumer to the end. The
	// recorded connections are reset so the assertions below describe the restart
	// alone, whatever the first phase did.
	fake.resetConnections()
	fake.setBudget(100)
	secondConsumer := newSourceConsumer(t, fixture, endpoint)
	secondContext, stopSecond := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() { secondDone <- secondConsumer.Run(secondContext) }()
	waitFor(t, 15*time.Second, "every event to be applied", func() bool {
		return fixture.appliedEventCount(documentIDs) >= int64(len(requests))
	})
	stopSecond()
	select {
	case err := <-secondDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("the restarted consumer returned %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the restarted consumer did not stop")
	}

	// Exactly once: one stored event per event id, and every document indexed.
	if rows := fixture.countRows(`SELECT count(*) FROM document_search.document_index_events WHERE document_id = ANY($1::text[]::uuid[])`,
		documentIDs); rows != int64(len(requests)) {
		t.Fatalf("applied event rows = %d, want %d (one per event)", rows, len(requests))
	}
	if rows := fixture.countRows(`SELECT count(*) FROM document_search.document_index WHERE document_id = ANY($1::text[]::uuid[])`,
		documentIDs); rows != int64(len(requests)) {
		t.Fatalf("index rows = %d, want %d", rows, len(requests))
	}
	if cursor := fixture.cursor(); cursor != requests[len(requests)-1].GetSequence() {
		t.Fatalf("final cursor = %d, want the last sequence %d", cursor, requests[len(requests)-1].GetSequence())
	}
	if hits := fixture.search(capabilityFor(false, space), searchRequest(token, []string{space}, nil)); len(hits.GetHits()) != len(requests) {
		t.Fatalf("hits = %d, want %d", len(hits.GetHits()), len(requests))
	}

	// No skipping: the restart resumed from the durable cursor, so every event
	// between the crash and the end was delivered by sequence.
	cursors := fake.requestedCursors()
	if len(cursors) == 0 {
		t.Fatal("the restarted consumer never opened a stream against the source")
	}
	if cursors[0] != cursorAtCrash {
		t.Fatalf("the restart's first connection asked for after_sequence=%d (all: %v), want the durable cursor %d",
			cursors[0], cursors, cursorAtCrash)
	}
	for index, requested := range cursors {
		if requested < cursorAtCrash {
			t.Fatalf("restart connection %d asked for after_sequence=%d (all: %v), behind the durable cursor %d",
				index, requested, cursors, cursorAtCrash)
		}
	}

	// No double applying: the event applied before the crash still holds the
	// state it had then.
	stateAfter, err := fixture.service.GetDocumentIndexState(fixture.ctx, documentIDs[1])
	if err != nil {
		t.Fatalf("GetDocumentIndexState after the restart: %v", err)
	}
	if stateAfter.GetState().GetIndexedAt() != stateOfSecond.GetState().GetIndexedAt() {
		t.Fatalf("the restarted consumer re-applied an already applied event: indexed_at %q -> %q",
			stateOfSecond.GetState().GetIndexedAt(), stateAfter.GetState().GetIndexedAt())
	}
}

// A consumer that is handed the same events again - because its stored cursor was
// lost, because the source replayed, or because a reconnect raced the delivery it
// replaced - must treat every one of them as a no-op. This is the at-least-once
// case, and it must not surface as a sequence conflict: an event id that is
// already in the log is a duplicate no matter which unique key the database
// notices first.
func TestIntegrationConsumerRedeliveryIsIdempotent(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	token := "redeliverytoken" + dbtest.RunTag()

	requests := make([]*documentsearchv1.IndexDocumentEventRequest, 0, 2)
	documentIDs := make([]string, 0, 2)
	for index := 0; index < 2; index++ {
		documentID := fixture.newDocument()
		documentIDs = append(documentIDs, documentID)
		requests = append(requests, fixture.upsertRequest(documentSpec{
			DocumentID:      documentID,
			OwnerSpaceID:    space,
			Title:           "Redelivered " + token,
			Content:         "body " + token,
			AllowedSpaceIDs: []string{space},
		}))
	}

	fake := &fakeDocumentService{envelopes: buildEnvelopes(requests...)}
	endpoint := startFakeSource(t, fake)

	firstConsumer := newSourceConsumer(t, fixture, endpoint)
	firstContext, stopFirst := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() { firstDone <- firstConsumer.Run(firstContext) }()
	waitFor(t, 10*time.Second, "the first delivery to commit", func() bool {
		return fixture.appliedEventCount(documentIDs) >= 2 && fixture.cursor() == requests[1].GetSequence()
	})
	stopFirst()
	select {
	case err := <-firstDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("the stopped consumer returned %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the consumer did not stop after its context was cancelled")
	}

	statesBefore := make([]string, 0, len(documentIDs))
	for _, documentID := range documentIDs {
		state, err := fixture.service.GetDocumentIndexState(fixture.ctx, documentID)
		if err != nil {
			t.Fatalf("GetDocumentIndexState: %v", err)
		}
		statesBefore = append(statesBefore, state.GetState().GetIndexedAt())
	}

	// Rewind the durable cursor, exactly as a restored or lost cursor would look,
	// and let the consumer be handed the same two events again.
	fixture.exec(`UPDATE document_search.consumer_cursors SET last_sequence = 0 WHERE stream = $1`, fixture.stream)
	deliveredBefore := fake.deliveredCount()
	secondConsumer := newSourceConsumer(t, fixture, endpoint)
	secondContext, stopSecond := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() { secondDone <- secondConsumer.Run(secondContext) }()
	waitFor(t, 10*time.Second, "the redelivery to be consumed", func() bool {
		return fake.deliveredCount() >= deliveredBefore+int64(len(requests))
	})
	// The consumer must still be running: a duplicate is not a stream failure.
	select {
	case err := <-secondDone:
		t.Fatalf("the consumer stopped while consuming duplicates: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	stopSecond()
	select {
	case err := <-secondDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("the consumer returned %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the redelivering consumer did not stop")
	}

	if rows := fixture.countRows(`SELECT count(*) FROM document_search.document_index_events WHERE document_id = ANY($1::text[]::uuid[])`,
		documentIDs); rows != int64(len(requests)) {
		t.Fatalf("applied event rows = %d, want %d: a duplicate must not add a row", rows, len(requests))
	}
	if cursor := fixture.cursor(); cursor != requests[1].GetSequence() {
		t.Fatalf("cursor = %d after the redelivery, want it back at %d and never behind", cursor, requests[1].GetSequence())
	}
	for index, documentID := range documentIDs {
		state, err := fixture.service.GetDocumentIndexState(fixture.ctx, documentID)
		if err != nil {
			t.Fatalf("GetDocumentIndexState after the redelivery: %v", err)
		}
		if state.GetState().GetIndexedAt() != statesBefore[index] {
			t.Fatalf("document %d was re-applied: indexed_at %q -> %q",
				index, statesBefore[index], state.GetState().GetIndexedAt())
		}
	}
}

func TestIntegrationConsumerCursorIsMonotonicAndTransactional(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()

	request := fixture.upsertRequest(documentSpec{
		DocumentID:      fixture.newDocument(),
		OwnerSpaceID:    space,
		Title:           "Behind " + dbtest.RunTag(),
		Content:         "body",
		AllowedSpaceIDs: []string{space},
	})
	// Seed the cursor above the event's own sequence, as if a longer stream had
	// already been consumed: an event below the cursor must not move it back.
	seeded := request.GetSequence() + 1000
	fixture.exec(`INSERT INTO document_search.consumer_cursors (stream, last_sequence)
		VALUES ($1, $2)
		ON CONFLICT (stream) DO UPDATE SET last_sequence = EXCLUDED.last_sequence`, fixture.stream, seeded)

	outcome, err := applyEventTx(fixture.ctx, fixture.cfg, fixture.pool, fixture.vectors, request, applyOptions{
		recordEvent:  true,
		cursorStream: fixture.stream,
	})
	if err != nil {
		t.Fatalf("apply through the consumer path: %v", err)
	}
	if !outcome.Applied {
		t.Fatalf("the event was not applied: %q", outcome.Reason)
	}
	if cursor := fixture.cursor(); cursor != seeded {
		t.Fatalf("cursor = %d, want it unchanged at %d: an event below the cursor must not move it back", cursor, seeded)
	}

	// A failed apply must roll back the cursor together with the index change.
	conflict := fixture.upsertRequest(documentSpec{
		DocumentID:      fixture.newDocument(),
		OwnerSpaceID:    space,
		Title:           "Conflicting " + dbtest.RunTag(),
		Content:         "body",
		AllowedSpaceIDs: []string{space},
		Sequence:        request.GetSequence(),
	})
	if _, err := applyEventTx(fixture.ctx, fixture.cfg, fixture.pool, fixture.vectors, conflict, applyOptions{
		recordEvent:  true,
		cursorStream: fixture.stream,
	}); err == nil {
		t.Fatal("an event reusing the sequence was accepted")
	}
	if cursor := fixture.cursor(); cursor != seeded {
		t.Fatalf("cursor = %d after a failed apply, want %d", cursor, seeded)
	}
	if rows := fixture.countRows(`SELECT count(*) FROM document_search.document_index WHERE document_id = $1::text::uuid`,
		conflict.GetDocumentId()); rows != 0 {
		t.Fatalf("index rows = %d after a failed apply, want 0", rows)
	}
	if rows := fixture.countRows(`SELECT count(*) FROM document_search.document_index_events WHERE event_id = $1::text::uuid`,
		conflict.GetEventId()); rows != 0 {
		t.Fatalf("applied event rows = %d after a failed apply, want 0", rows)
	}
}

// appliedEventCount counts the fixture's documents in the applied event log.
func (fixture *testFixture) appliedEventCount(documentIDs []string) int64 {
	return fixture.countRows(`SELECT count(*) FROM document_search.document_index_events WHERE document_id = ANY($1::text[]::uuid[])`, documentIDs)
}

// cursor reports the fixture's consumer cursor.
func (fixture *testFixture) cursor() int64 {
	return fixture.countRows(`SELECT last_sequence FROM document_search.consumer_cursors WHERE stream = $1`, fixture.stream)
}

// waitFor polls a condition until it holds or the deadline passes.
func waitFor(t *testing.T, timeout time.Duration, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if condition() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
