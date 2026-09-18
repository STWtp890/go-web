package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestCapacityGuardAgainstPostgres closes the gap the in-process guard tests
// leave open: the snapshot-bytes limit reads a size the store reports, and only
// the persistent adapter proves that path end to end (real schema, real payload,
// real compare-and-swap).
//
//	CHAT_CONTROL_STORE_INTEGRATION=1 \
//	CONTROL_DATABASE_DSN=postgres://postgres:postgres@127.0.0.1:15432/gin_demo?sslmode=disable \
//	go test ./internal/chat -run TestCapacityGuardAgainstPostgres -v
//
// It uses a scratch namespace and deletes it afterwards.
func TestCapacityGuardAgainstPostgres(t *testing.T) {
	if os.Getenv("CHAT_CONTROL_STORE_INTEGRATION") != "1" {
		t.Skip("set CHAT_CONTROL_STORE_INTEGRATION=1 to run against PostgreSQL")
	}
	dsn := os.Getenv("CONTROL_DATABASE_DSN")
	if dsn == "" {
		t.Fatal("CONTROL_DATABASE_DSN is required for chat control-store integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	t.Run("message limit", func(t *testing.T) {
		h := newPostgresHarness(t, ctx, dsn, IndexServiceConfig{MaxMessages: 2})
		h.index(t, "op-pg-limit-1", "room-pg-limit", "scope-pg-limit", 1, "first", "second")

		if _, err := h.service.IndexMessages(ctx, IndexMessagesRequest{
			OperationID: "op-pg-limit-2", ConversationID: "room-pg-limit", OwnerScopeID: "scope-pg-limit",
			LifecycleRevision: 1,
			Messages: []MessageInput{{
				MessageID: "third", SenderID: "sender", SentAtUnixMs: 1_700_000_000_002, Content: "third",
			}},
		}); !errors.Is(err, ErrCapacityExceeded) {
			t.Fatalf("write past the message limit error = %v, want ErrCapacityExceeded", err)
		}
		// The corpus is still serving: a refused write is not an outage, and the
		// accepted state survived the refusal.
		h.archive(t, "op-pg-limit-archive", "room-pg-limit", 1, 1)
		if hits := h.search(t, "first", []string{"scope-pg-limit"}, nil).Hits; len(hits) != 1 {
			t.Fatalf("hits after a refused write = %d, want 1", len(hits))
		}
	})

	t.Run("snapshot limit", func(t *testing.T) {
		h := newPostgresHarness(t, ctx, dsn, IndexServiceConfig{MaxSnapshotBytes: 200})
		h.index(t, "op-pg-snapshot-1", "room-pg-snapshot", "scope-pg-snapshot", 1, "first")
		if got := h.postgres.LastSnapshotBytes(); got <= 200 {
			t.Fatalf("persistent adapter reported %d bytes, want the guard's threshold crossed", got)
		}
		if _, err := h.service.IndexMessages(ctx, IndexMessagesRequest{
			OperationID: "op-pg-snapshot-2", ConversationID: "room-pg-snapshot", OwnerScopeID: "scope-pg-snapshot",
			LifecycleRevision: 1,
			Messages: []MessageInput{{
				MessageID: "second", SenderID: "sender", SentAtUnixMs: 1_700_000_000_001, Content: "second",
			}},
		}); !errors.Is(err, ErrCapacityExceeded) {
			t.Fatalf("write past the snapshot limit error = %v, want ErrCapacityExceeded", err)
		}
	})

	// Ledger retention has to prune through the same compare-and-swap the real
	// writes use, so the persistent store is the only place that proves it.
	t.Run("ledger retention", func(t *testing.T) {
		clock := time.Unix(1_760_000_000, 0).UTC()
		restoreClock := setClock(t, &clock)
		defer restoreClock()

		h := newPostgresHarness(t, ctx, dsn, IndexServiceConfig{OperationRetention: time.Hour})
		h.index(t, "op-pg-retention", "room-pg-retention", "scope-pg-retention", 1, "one")
		before := h.postgres.LastSnapshotBytes()

		clock = clock.Add(2 * time.Hour)
		if err := h.service.pruneOperationLedger(ctx, h.service.state.Load()); err != nil {
			t.Fatalf("prune ledger: %v", err)
		}
		state, err := h.postgres.Load(ctx)
		if err != nil {
			t.Fatalf("load control state: %v", err)
		}
		if _, stillThere := state.Operations["op-pg-retention"]; stillThere {
			t.Fatalf("expired operation survived the retention window in PostgreSQL: %+v", state.Operations)
		}
		if after := h.postgres.LastSnapshotBytes(); after >= before {
			t.Fatalf("snapshot did not shrink after pruning: %d -> %d", before, after)
		}
	})
}

// postgresHarness is the in-process harness backed by the persistent store: the
// fake indexer and searcher stay, so the only thing under test is the store path.
type postgresHarness struct {
	*harness
	postgres  *PostgresControlStore
	namespace string
}

func newPostgresHarness(t *testing.T, ctx context.Context, dsn string, config IndexServiceConfig) *postgresHarness {
	t.Helper()
	namespace := fmt.Sprintf("guard-%d", time.Now().UnixNano())
	store, err := NewPostgresControlStore(ctx, PostgresControlStoreConfig{
		DSN: dsn, Namespace: namespace, Bootstrap: true,
	})
	if err != nil {
		t.Fatalf("open chat control store: %v", err)
	}
	indexer := newFakeIndexer()
	searcher := &fakeSearcher{indexer: indexer}
	config.ControlStore = store
	config.Indexer = indexer
	config.Projection = indexer
	config.Searcher = searcher
	service, err := NewIndexService(ctx, config)
	if err != nil {
		store.Close()
		t.Fatalf("new chat index service on PostgreSQL: %v", err)
	}
	harnessed := &postgresHarness{
		harness:   &harness{service: service, indexer: indexer, searcher: searcher},
		postgres:  store,
		namespace: namespace,
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if _, err := store.pool.Exec(cleanupContext,
			"DELETE FROM mixin_search_control.chat_control_states WHERE namespace = $1", namespace); err != nil {
			t.Errorf("delete chat control namespace %q: %v", namespace, err)
		}
		store.Close()
	})
	return harnessed
}
