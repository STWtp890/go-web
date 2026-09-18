package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"testing"
	"time"
)

// The capacity profile answers the question the plan left open: how big does one
// corpus's control snapshot get, and what does one write cost, at 1k / 10k / 50k
// messages?
//
// It is a measurement, not an assertion: thresholds belong in configuration and
// in the contract, and they are set from these numbers rather than from a guess
// inside a test. Run it with:
//
//	CHAT_CAPACITY_PROFILE=1 go test ./internal/chat -run TestChatCapacityProfile -v
//
// Optional: set CAPACITY_CAS_DSN to a PostgreSQL DSN to also measure the real
// compare-and-swap latency (one UPDATE ... RETURNING per write) instead of only
// the in-process clone and encode. The DSN's database must allow creating the
// chat control table; the measurement uses a scratch namespace and deletes it.
//
// Assumptions, stated so the numbers can be read correctly:
//   - every message is indexed exactly once, in batches of 50 (one idempotency
//     ledger entry per batch), which is what a well-behaved producer does;
//   - no message is retracted and no conversation is deleted, so the state holds
//     no tombstones; a corpus with heavy retraction carries the same message
//     records either way, because retraction keeps them;
//   - pending write intents and pending deletes are transient and empty here.
func TestChatCapacityProfile(t *testing.T) {
	if os.Getenv("CHAT_CAPACITY_PROFILE") != "1" {
		t.Skip("set CHAT_CAPACITY_PROFILE=1 to measure the control snapshot at 1k/10k/30k/50k messages")
	}
	const batchSize = 50
	t.Logf(
		"machine: %s/%s ncpu=%d go=%s",
		runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version(),
	)
	t.Logf(
		"model: synthetic messages, content and metadata identical per index, batch=%d, ledger entry per batch carrying every message state",
		batchSize,
	)
	for _, messages := range []int{1_000, 10_000, 30_000, 50_000} {
		state := buildCapacityState(messages, batchSize)
		profile := measureCapacity(t, state)
		t.Logf(
			"messages=%d conversations=%d operations=%d payload_bytes=%d payload_kib=%.0f ledger_bytes=%d ledger_kib=%.0f ledger_share=%.0f%% clone_encode_p50_ms=%.2f clone_encode_p95_ms=%.2f alloc_kib_per_write=%.0f",
			profile.messages,
			profile.conversations,
			profile.operations,
			profile.payloadBytes,
			float64(profile.payloadBytes)/1024,
			profile.ledgerBytes,
			float64(profile.ledgerBytes)/1024,
			profile.ledgerShare*100,
			profile.cloneEncodeP50Millis,
			profile.cloneEncodeP95Millis,
			float64(profile.allocBytes)/1024,
		)
	}
}

// TestChatCapacityLedgerCeilingBudget answers the question the two limits raise
// together: does a corpus at the message limit plus a ledger at its entry ceiling
// still fit in the snapshot budget?
//
// It models the ceiling the way an operator would reach it - many small write
// batches, each receipt carrying its batch's states - and reports whether the
// result fits the proposed 24 MiB budget.
func TestChatCapacityLedgerCeilingBudget(t *testing.T) {
	if os.Getenv("CHAT_CAPACITY_PROFILE") != "1" {
		t.Skip("set CHAT_CAPACITY_PROFILE=1 to measure the control snapshot at 1k/10k/30k/50k messages")
	}
	const budget = 24 * 1024 * 1024
	cases := []struct {
		name               string
		messages           int
		operations         int
		statesPerOperation int
	}{
		{name: "30k messages, batch of 50", messages: 30_000, operations: 600, statesPerOperation: 50},
		{name: "30k messages, batch of 5", messages: 30_000, operations: 6_000, statesPerOperation: 5},
		{name: "30k messages, ceiling 100k receipts", messages: 30_000, operations: 100_000, statesPerOperation: 1},
	}
	for _, testCase := range cases {
		state := buildLedgerState(testCase.messages, testCase.operations, testCase.statesPerOperation)
		payload := mustMarshal(t, state)
		ledgerBytes := len(payload) - len(mustMarshal(t, stateWithoutOperations(state)))
		verdict := "fits"
		if len(payload) > budget {
			verdict = "EXCEEDS"
		}
		t.Logf(
			"%s: messages=%d receipts=%d payload_bytes=%d payload_mib=%.1f ledger_bytes=%d ledger_mib=%.1f ledger_share=%.0f%% budget24mib=%s",
			testCase.name,
			len(state.Messages),
			len(state.Operations),
			len(payload),
			float64(len(payload))/(1024*1024),
			ledgerBytes,
			float64(ledgerBytes)/(1024*1024),
			float64(ledgerBytes)/float64(len(payload))*100,
			verdict,
		)
	}
}

func stateWithoutOperations(state ControlState) ControlState {
	copied := state
	copied.Operations = make(map[string]ControlOperation)
	return copied
}

// buildLedgerState builds a state whose ledger holds a chosen number of receipts,
// each carrying statesPerOperation message states taken from the message set. It
// models a corpus indexed in fixed-size batches under a given retention ceiling.
func buildLedgerState(messages, operations, statesPerOperation int) ControlState {
	state := buildCapacityState(messages, statesPerOperation)
	state.Operations = make(map[string]ControlOperation, operations)
	keys := make([]string, 0, len(state.Messages))
	for key := range state.Messages {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for index := 0; index < operations; index++ {
		states := make([]MessageState, 0, statesPerOperation)
		for position := 0; position < statesPerOperation; position++ {
			message := state.Messages[keys[(index*statesPerOperation+position)%len(keys)]]
			states = append(states, MessageState{
				ConversationID:    message.ConversationID,
				MessageID:         message.MessageID,
				OwnerScopeID:      "scope-" + message.ConversationID,
				SenderID:          message.SenderID,
				SentAtUnixMs:      message.SentAtUnixMs,
				ArchiveRevision:   1,
				AccessRevision:    1,
				LifecycleRevision: 1,
				ChunkCount:        1,
				ContentSHA256:     message.ContentSHA256,
			})
		}
		state.Operations[fmt.Sprintf("operation-%d", index)] = ControlOperation{
			Kind:                operationIndexMessages,
			Fingerprint:         fmt.Sprintf("sha256:%064x", index),
			RecordedAtUnixMilli: 1_760_000_000_000,
			Result:              ControlOperationResult{Messages: states},
		}
	}
	return state
}

// TestChatCapacityCASLatency measures the write path that actually blocks other
// writers: one full snapshot persisted with compare-and-swap. It needs a real
// PostgreSQL because that is where the cost lives.
func TestChatCapacityCASLatency(t *testing.T) {
	if os.Getenv("CHAT_CAPACITY_PROFILE") != "1" {
		t.Skip("set CHAT_CAPACITY_PROFILE=1 to measure the control snapshot at 1k/10k/50k messages")
	}
	dsn := os.Getenv("CAPACITY_CAS_DSN")
	if dsn == "" {
		t.Skip("set CAPACITY_CAS_DSN to measure compare-and-swap latency against PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	const batchSize = 50
	for _, messages := range []int{1_000, 10_000, 50_000} {
		namespace := fmt.Sprintf("capacity-%d-%d", messages, time.Now().UnixNano())
		store, err := NewPostgresControlStore(ctx, PostgresControlStoreConfig{
			DSN: dsn, Namespace: namespace, Bootstrap: true,
		})
		if err != nil {
			t.Fatalf("open chat control store: %v", err)
		}
		state := buildCapacityState(messages, batchSize)
		if _, err := store.Save(ctx, 0, state); err != nil {
			store.Close()
			t.Fatalf("seed %d-message state: %v", messages, err)
		}
		stored, err := store.Load(ctx)
		if err != nil {
			store.Close()
			t.Fatalf("load %d-message state: %v", messages, err)
		}
		const writes = 20
		samples := make([]time.Duration, 0, writes)
		generation := stored.Generation
		for write := 0; write < writes; write++ {
			// Save validates that the snapshot's generation matches the expected
			// one, exactly as the control plane sets it before committing.
			stored.Generation = generation
			started := time.Now()
			next, err := store.Save(ctx, generation, stored)
			samples = append(samples, time.Since(started))
			if err != nil {
				store.Close()
				t.Fatalf("cas write %d at %d messages: %v", write, messages, err)
			}
			generation = next
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		var total time.Duration
		for _, sample := range samples {
			total += sample
		}
		t.Logf(
			"cas messages=%d payload_bytes=%d writes=%d mean_ms=%.2f p50_ms=%.2f p95_ms=%.2f worst_ms=%.2f",
			messages,
			len(mustMarshal(t, stored)),
			writes,
			float64(total.Microseconds())/float64(writes)/1000,
			millis(percentile(samples, 0.50)),
			millis(percentile(samples, 0.95)),
			millis(samples[len(samples)-1]),
		)
		_, _ = store.pool.Exec(ctx, "DELETE FROM mixin_search_control.chat_control_states WHERE namespace = $1", namespace)
		store.Close()
	}
}

type capacityProfile struct {
	messages             int
	conversations        int
	operations           int
	payloadBytes         int
	ledgerBytes          int
	ledgerShare          float64
	cloneEncodeP50Millis float64
	cloneEncodeP95Millis float64
	allocBytes           uint64
}

func measureCapacity(t *testing.T, state ControlState) capacityProfile {
	t.Helper()
	payload := mustMarshal(t, state)
	ledgerBytes := len(payload) - len(mustMarshal(t, stateWithoutOperations(state)))
	const writes = 20
	// One warm-up write so the first-touch page faults are not attributed to the
	// measured samples.
	_, _ = cloneControlState(state)

	durations := make([]time.Duration, 0, writes)
	var allocated uint64
	for write := 0; write < writes; write++ {
		started := time.Now()
		clone, err := cloneControlState(state)
		if err != nil {
			t.Fatalf("clone control state: %v", err)
		}
		encoded := mustMarshal(t, clone)
		durations = append(durations, time.Since(started))
		allocated += uint64(len(encoded))
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	return capacityProfile{
		messages:             len(state.Messages),
		conversations:        len(state.Conversations),
		operations:           len(state.Operations),
		payloadBytes:         len(payload),
		ledgerBytes:          ledgerBytes,
		ledgerShare:          float64(ledgerBytes) / float64(len(payload)),
		cloneEncodeP50Millis: millis(percentile(durations, 0.50)),
		cloneEncodeP95Millis: millis(percentile(durations, 0.95)),
		allocBytes:           allocated / writes,
	}
}

func millis(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1000
}

// percentile returns the nearest-rank percentile of a sorted slice.
func percentile(sorted []time.Duration, fraction float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(fraction*float64(len(sorted))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func mustMarshal(t *testing.T, state ControlState) []byte {
	t.Helper()
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("encode control state: %v", err)
	}
	return encoded
}

// buildCapacityState builds the state a corpus would hold after indexing
// messages in batches, without going through the service: this measures the data
// structure, not the admission path.
//
// The ledger entry of a batch carries one MessageState per message, exactly as
// IndexMessages records it, because that is where a large part of the snapshot
// comes from: an earlier version of this builder stored a single state per batch
// and under-measured the ledger by the batch size.
func buildCapacityState(messages, batchSize int) ControlState {
	state := newControlState()
	const conversations = 100
	conversationsCount := conversations
	if messages < conversationsCount {
		conversationsCount = messages
	}
	batch := make([]MessageState, 0, batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		operationID := fmt.Sprintf("operation-%d", len(state.Operations))
		states := append([]MessageState(nil), batch...)
		state.Operations[operationID] = ControlOperation{
			Kind:                operationIndexMessages,
			Fingerprint:         fmt.Sprintf("sha256:%064x", len(state.Operations)),
			RecordedAtUnixMilli: 1_760_000_000_000,
			Result:              ControlOperationResult{Messages: states},
		}
		batch = batch[:0]
	}
	for index := 0; index < messages; index++ {
		conversationID := fmt.Sprintf("conversation-%d", index%conversationsCount)
		conversation, ok := state.Conversations[conversationID]
		if !ok {
			conversation = ControlConversation{
				OwnerScopeID:      "scope-" + conversationID,
				Archived:          true,
				ArchiveRevision:   1,
				AccessRevision:    1,
				GrantedScopeIDs:   []string{"scope-" + conversationID},
				LifecycleRevision: 1,
				DeleteResults:     map[uint64]DeleteResult{},
			}
			state.Conversations[conversationID] = conversation
		}
		messageID := fmt.Sprintf("message-%d", index)
		storageID := fmt.Sprintf("chat-postgres:capacity/%s/%s", conversationID, messageID)
		state.Messages[messageKey(conversationID, messageID)] = ControlMessage{
			ConversationID:    conversationID,
			MessageID:         messageID,
			SenderID:          fmt.Sprintf("qq-%d", index%1000),
			SentAtUnixMs:      1_700_000_000_000 + int64(index),
			ContentSHA256:     fmt.Sprintf("%064x", index),
			StorageID:         storageID,
			ChunkCount:        1,
			LifecycleRevision: 1,
			Metadata:          map[string]string{"batch": fmt.Sprintf("%d", index/batchSize)},
		}
		// The ledger entry records the response IndexMessages would return for the
		// batch, which is one state per message.
		batch = append(batch, MessageState{
			ConversationID:    conversationID,
			MessageID:         messageID,
			OwnerScopeID:      "scope-" + conversationID,
			SenderID:          fmt.Sprintf("qq-%d", index%1000),
			SentAtUnixMs:      1_700_000_000_000 + int64(index),
			ArchiveRevision:   1,
			AccessRevision:    1,
			LifecycleRevision: 1,
			ChunkCount:        1,
			ContentSHA256:     fmt.Sprintf("%064x", index),
		})
		if len(batch) == batchSize {
			flush()
		}
	}
	flush()
	return state
}
