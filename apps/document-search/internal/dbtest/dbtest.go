// Package dbtest is the real-PostgreSQL fixture shared by the document-search
// integration tests.
//
// A missing or unreachable database fails the test that needs it. It is never
// skipped: the index behavior of this service is defined by transactions,
// revision fences and database constraints, and none of that is exercised by a
// mock, so a "green" run without a database would be a false claim.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"document-search/internal/config"
	"document-search/internal/infrastructure/postgres"
)

// DefaultDSN is the documented development database and the role this service
// writes with. Running the whole suite through this DSN is what proves the
// service only reaches its own schema.
const DefaultDSN = "postgres://document_search_writer:document_search@127.0.0.1:15432/gin_demo?sslmode=disable"

// DSN returns the DSN the integration tests must use.
func DSN() string {
	if value := strings.TrimSpace(os.Getenv("DOCUMENT_SEARCH_TEST_DSN")); value != "" {
		return value
	}
	return DefaultDSN
}

// Open connects to the real database and fails the test when it cannot.
func Open(t testing.TB, ctx context.Context) *postgres.Pool {
	t.Helper()
	settings := config.Default().Postgres
	settings.DSN = DSN()
	pool, err := postgres.Open(ctx, settings)
	if err != nil {
		t.Fatalf("document-search integration tests require the real PostgreSQL at %s: %v", DSN(), err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

// Identifier returns a fresh canonical UUID for a test fixture.
func Identifier(t testing.TB) string {
	t.Helper()
	value, err := newIdentifier()
	if err != nil {
		t.Fatalf("document-search dbtest: generate identifier: %v", err)
	}
	return value
}

// RunTag is a short token unique to this test process. Titles and names built
// from it cannot collide with another agent's rows in the shared database.
func RunTag() string { return runTag }

var runTag = func() string {
	value, err := newIdentifier()
	if err != nil {
		return "dshrun"
	}
	return value[:8]
}()

// sequenceBaseFloor keeps generated sequences far above the small values older
// fixtures used, so a fresh run cannot re-claim one of those rows either.
const sequenceBaseFloor = int64(1) << 46

// Sequence hands out unique positive Outbox sequences.
//
// Uniqueness has to hold across every process that writes this table, not only
// within one test binary. A wall-clock base does not provide it: `go test ./...`
// starts the package binaries in the same instant, so they read the same clock
// tick and derive the same base (measured on this machine: three of four
// binaries shared one base, and four of four in another run). Two packages then
// claim the same sequence for different events, one loses the primary-key race,
// and the loser's test is the one that fails. The base is therefore random, with
// the clock only as a fallback when randomness is unavailable.
var (
	sequenceBase    = newSequenceBase()
	sequenceCounter = atomic.Int64{}
)

// Sequence returns the next event sequence for this process.
func Sequence() int64 { return sequenceBase + sequenceCounter.Add(1) }

// newSequenceBase returns a per-process base in [2^46, 2^47).
func newSequenceBase() int64 {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return (time.Now().UnixNano() % sequenceBaseFloor) + sequenceBaseFloor
	}
	return int64(binary.BigEndian.Uint64(raw[:])>>18) | sequenceBaseFloor
}

// CleanupDocuments removes only the rows a test created. The applied event log,
// the index projection, the tombstones and the private consumer stream are
// deleted for the given document ids and stream names, and nothing else.
func CleanupDocuments(t *testing.T, ctx context.Context, pool *postgres.Pool, documentIDs, streams []string) {
	t.Helper()
	if pool == nil || pool.Pgx() == nil {
		return
	}
	for _, statement := range []string{
		`DELETE FROM document_search.document_index_events WHERE document_id = ANY($1::text[]::uuid[])`,
		`DELETE FROM document_search.document_index WHERE document_id = ANY($1::text[]::uuid[])`,
		`DELETE FROM document_search.document_index_tombstones WHERE document_id = ANY($1::text[]::uuid[])`,
	} {
		if _, err := pool.Pgx().Exec(ctx, statement, documentIDs); err != nil {
			t.Errorf("document-search dbtest: clean up fixtures: %v", err)
		}
	}
	for _, stream := range streams {
		if strings.TrimSpace(stream) == "" {
			continue
		}
		if _, err := pool.Pgx().Exec(ctx, `DELETE FROM document_search.consumer_cursors WHERE stream = $1`, stream); err != nil {
			t.Errorf("document-search dbtest: clean up the consumer cursor: %v", err)
		}
	}
}

// newIdentifier returns a random version 4 UUID in canonical form. It mirrors
// the service helper so the test fixture does not depend on unexported
// production code.
func newIdentifier() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	buffer := make([]byte, 36)
	hex.Encode(buffer[0:8], raw[0:4])
	buffer[8] = '-'
	hex.Encode(buffer[9:13], raw[4:6])
	buffer[13] = '-'
	hex.Encode(buffer[14:18], raw[6:8])
	buffer[18] = '-'
	hex.Encode(buffer[19:23], raw[8:10])
	buffer[23] = '-'
	hex.Encode(buffer[24:36], raw[10:16])
	return string(buffer), nil
}
