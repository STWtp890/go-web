package rag

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/qdrant/go-client/qdrant"
)

// TestQdrantAliasSwitchIsPerCorpus is ADR-014 decision 4 in executable form at
// the store level: two corpora, two aliases, one switch.
//
// It records the alias mapping before and after the switch, proves that reads
// follow the alias (the corpus goes dark when the alias points at an empty
// generation and comes back when it is switched back), proves the other corpus's
// mapping never moves, and fails closed on an unknown target. Whatever happens,
// the original mapping is restored and the test's own corpora are deleted, so a
// failing run cannot leave a switched alias behind.
//
// It needs a Qdrant endpoint: QDRANT_INTEGRATION=1 plus QDRANT_HOST/QDRANT_PORT.
func TestQdrantAliasSwitchIsPerCorpus(t *testing.T) {
	if os.Getenv("QDRANT_INTEGRATION") != "1" {
		t.Skip("set QDRANT_INTEGRATION=1 to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	run := time.Now().UnixNano()
	documentAlias := fmt.Sprintf("rag_alias_doc_%d", run)
	chatAlias := fmt.Sprintf("rag_alias_chat_%d", run)

	documents := newAliasIntegrationStore(t, ctx, documentAlias)
	chat := newAliasIntegrationStore(t, ctx, chatAlias)

	// Bootstrap must have created one generation per corpus and pointed the alias
	// at it: the configured name is an alias, never a physical collection.
	assertAliasMapping(t, ctx, documents, documentAlias+"_"+DefaultQdrantGeneration)
	originalChatTarget := assertAliasMapping(t, ctx, chat, chatAlias+"_"+DefaultQdrantGeneration)

	restored := true
	createdGeneration := ""
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if !restored {
			if err := chat.SwitchAlias(cleanupContext, originalChatTarget); err != nil {
				t.Errorf("restore chat alias %q to %q: %v", chatAlias, originalChatTarget, err)
			}
		}
		// The state of the mapping is the point of this test, so report the final
		// one rather than assuming the restore worked.
		if target, err := chat.PhysicalCollection(cleanupContext); err != nil || target != originalChatTarget {
			t.Errorf("chat alias %q ended at %q (err=%v), want %q", chatAlias, target, err, originalChatTarget)
		}
		// Delete what this test added: the alias and both generations it owns. The
		// generation the alias pointed at is removed by deleteAliasAndPhysical, the
		// extra one only here.
		if createdGeneration != "" {
			if err := chat.client.DeleteCollection(cleanupContext, createdGeneration); err != nil {
				t.Errorf("delete test generation %q: %v", createdGeneration, err)
			}
		}
		deleteAliasAndPhysical(t, cleanupContext, chat)
		deleteAliasAndPhysical(t, cleanupContext, documents)
		_ = chat.Close()
		_ = documents.Close()
	})

	// Data in both corpora, so an empty result means "the alias moved" and not
	// "nothing was ever written".
	mustWriteAliasChunk(t, ctx, documents, "alias-document", "aliasdocumentneedle")
	mustWriteAliasChunk(t, ctx, chat, "alias-chat", "aliaschatneedle")
	assertAliasSearchCount(t, ctx, documents, "aliasdocumentneedle", 1)
	assertAliasSearchCount(t, ctx, chat, "aliaschatneedle", 1)

	// A new generation starts empty. Switching the chat alias to it must hide the
	// chat corpus while the document corpus keeps answering on its own alias.
	next := chatAlias + "_g2"
	if _, err := chat.PrepareGeneration(ctx, "g2", localEmbeddingDimensions); err != nil {
		t.Fatalf("prepare generation g2: %v", err)
	}
	createdGeneration = next
	if err := chat.SwitchAlias(ctx, next); err != nil {
		t.Fatalf("switch chat alias to %q: %v", next, err)
	}
	restored = false
	assertAliasMapping(t, ctx, chat, next)
	assertAliasMapping(t, ctx, documents, documentAlias+"_"+DefaultQdrantGeneration)
	assertAliasSearchCount(t, ctx, chat, "aliaschatneedle", 0)
	assertAliasSearchCount(t, ctx, documents, "aliasdocumentneedle", 1)

	// Switching back restores the corpus, which is what makes the switch usable
	// for a rebuild rather than a one-way door.
	if err := chat.SwitchAlias(ctx, originalChatTarget); err != nil {
		t.Fatalf("switch chat alias back to %q: %v", originalChatTarget, err)
	}
	restored = true
	assertAliasMapping(t, ctx, chat, originalChatTarget)
	assertAliasSearchCount(t, ctx, chat, "aliaschatneedle", 1)

	// Fail closed: an unknown target is refused and the mapping stays where it is.
	if err := chat.SwitchAlias(ctx, chatAlias+"_missing"); err == nil {
		t.Fatal("switching to a collection that does not exist was accepted")
	}
	if err := chat.SwitchAlias(ctx, chatAlias); err == nil {
		t.Fatal("switching an alias onto itself was accepted")
	}
	assertAliasMapping(t, ctx, chat, originalChatTarget)
}

// TestAliasSwitchStaysInsideItsOwnCorpus pins the guard that keeps a switch from
// reaching another corpus's collection. It needs no Qdrant because validation
// happens before the first client call.
func TestAliasSwitchStaysInsideItsOwnCorpus(t *testing.T) {
	store := &QdrantStore{alias: "go_web_chat_v1"}
	for _, target := range []string{
		"go_web_shadow_v1_g1", // the document corpus's generation
		"go_web_chat_v1",      // the alias itself
		"go_web_chat_v1x",     // a lookalike without the separator
		"",
	} {
		if err := store.SwitchAlias(context.Background(), target); err == nil {
			t.Fatalf("switching alias %q to %q was accepted", store.alias, target)
		}
	}
	if err := store.validateAliasTarget("go_web_chat_v1_g2"); err != nil {
		t.Fatalf("a generation of this corpus was rejected: %v", err)
	}
}

// TestQdrantAliasBatchFailureIsCompensated records what the pinned server does when
// the create half of the documented switch batch fails, and proves the
// compensation the store relies on works.
//
// The two review passes disagreed about whether Qdrant rolls the batch back, so
// this test observes the behaviour instead of asserting a belief about it: it
// logs where the alias ended up, then runs the same repair SwitchAlias performs
// and asserts the alias is serving the original collection again. That is the
// property the store needs - either the server rolled back (repair is a no-op
// overwrite) or it did not (repair is what keeps the corpus reachable).
func TestQdrantAliasBatchFailureIsCompensated(t *testing.T) {
	if os.Getenv("QDRANT_INTEGRATION") != "1" {
		t.Skip("set QDRANT_INTEGRATION=1 to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	alias := fmt.Sprintf("rag_alias_repair_%d", time.Now().UnixNano())
	store := newAliasIntegrationStore(t, ctx, alias)
	original := assertAliasMapping(t, ctx, store, alias+"_"+DefaultQdrantGeneration)
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := store.RestoreAlias(cleanupContext, original); err != nil {
			t.Errorf("restore alias %q: %v", alias, err)
		}
		deleteAliasAndPhysical(t, cleanupContext, store)
		_ = store.Close()
	})

	// The documented batch, with a create action that must fail: the target
	// collection does not exist.
	actions := []*qdrant.AliasOperations{
		{Action: &qdrant.AliasOperations_DeleteAlias{DeleteAlias: &qdrant.DeleteAlias{AliasName: alias}}},
		{Action: &qdrant.AliasOperations_CreateAlias{CreateAlias: &qdrant.CreateAlias{
			AliasName:      alias,
			CollectionName: alias + "_missing",
		}}},
	}
	if err := store.client.UpdateAliases(ctx, actions); err == nil {
		t.Fatal("the server accepted a create action naming a collection that does not exist")
	}
	observed, stillAliased, err := store.aliasedCollection(ctx)
	if err != nil {
		t.Fatalf("read alias %q after the failed batch: %v", alias, err)
	}
	if !stillAliased {
		t.Logf("observed server behaviour: a failed create action leaves alias %q deleted (no rollback)", alias)
	} else {
		t.Logf("observed server behaviour: the failed batch left alias %q at %q", alias, observed)
	}

	// The compensation the store performs.
	if err := store.RestoreAlias(ctx, original); err != nil {
		t.Fatalf("compensate the failed switch: %v", err)
	}
	assertAliasMapping(t, ctx, store, original)
}

func newAliasIntegrationStore(t *testing.T, ctx context.Context, alias string) *QdrantStore {
	t.Helper()
	store, err := NewQdrantStore(ctx, QdrantConfig{
		Host:       qdrantIntegrationHost(),
		Port:       qdrantIntegrationPort(t),
		Collection: alias,
		Dimensions: localEmbeddingDimensions,
	})
	if err != nil {
		t.Fatalf("open qdrant store %q: %v", alias, err)
	}
	return store
}

// assertAliasMapping records where an alias points and checks it against the
// expected collection, returning the observed target.
func assertAliasMapping(t *testing.T, ctx context.Context, store *QdrantStore, want string) string {
	t.Helper()
	got, err := store.PhysicalCollection(ctx)
	if err != nil {
		t.Fatalf("resolve alias %q: %v", store.Alias(), err)
	}
	if got != want {
		t.Fatalf("alias %q points at %q, want %q", store.Alias(), got, want)
	}
	return got
}

func mustWriteAliasChunk(t *testing.T, ctx context.Context, store *QdrantStore, documentID, content string) {
	t.Helper()
	dense := make([]float64, localEmbeddingDimensions)
	for index := range dense {
		dense[index] = 0.01
	}
	chunk := IndexedChunk{
		Chunk: Chunk{
			ID:         documentID + "#000",
			DocumentID: documentID,
			Title:      documentID,
			Content:    content,
			Position:   0,
		},
		Dense:  dense,
		Terms:  map[string]int{content: 1},
		Length: 1,
	}
	if err := store.ReplaceDocument(ctx, documentID, []IndexedChunk{chunk}); err != nil {
		t.Fatalf("write %q into %q: %v", documentID, store.Alias(), err)
	}
}

func assertAliasSearchCount(t *testing.T, ctx context.Context, store *QdrantStore, token string, want int) {
	t.Helper()
	hits, err := store.SparseSearch(ctx, []string{token}, 10)
	if err != nil {
		t.Fatalf("sparse search %q on %q: %v", token, store.Alias(), err)
	}
	if len(hits) != want {
		t.Fatalf("sparse search %q on %q returned %d hits, want %d", token, store.Alias(), len(hits), want)
	}
}

// deleteAliasAndPhysical removes one test corpus completely: the alias first,
// then the collection it pointed at. Deleting the collection alone would leave
// the alias behind, and the next run would then fail on a name clash instead of
// testing anything.
func deleteAliasAndPhysical(t *testing.T, ctx context.Context, store *QdrantStore) {
	t.Helper()
	physical, err := store.PhysicalCollection(ctx)
	if err != nil {
		// No alias: fall back to the name itself so the helper still cleans up a
		// store that never finished bootstrapping.
		if deleteErr := store.client.DeleteCollection(ctx, store.alias); deleteErr != nil {
			t.Errorf("delete qdrant test collection %q: %v", store.alias, deleteErr)
		}
		return
	}
	if err := store.client.DeleteAlias(ctx, store.alias); err != nil {
		t.Errorf("delete qdrant test alias %q: %v", store.alias, err)
	}
	if err := store.client.DeleteCollection(ctx, physical); err != nil {
		t.Errorf("delete qdrant test collection %q: %v", physical, err)
	}
}
